package blackbox

// Natural spawning (NaturalSpawner) against the real 26.2 server.
//
// The per-tick spawner rolls the level random, which no two servers
// share, so these pins are statistical/structural rather than
// byte-exact: constrained arenas where only one floor cell height is a
// valid spawn, fixed daylight, sprinted ticks, and bounds that follow
// from the measured caps and gates. Each leg is asserted on its own and
// both results are logged side by side.
//
// The flat comparison world generates in the_void (empty spawn lists),
// so nothing spawns until /fillbiome paints plains under an arena.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/worldgen"
)

var passCount = regexp.MustCompile(`commands\.execute\.conditional\.pass_count(\d+)`)

// spawnCountQuery counts a selector's matches through `execute if
// entity` (pass = 1, pass_count = n, fail = 0).
func spawnCountQuery(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration), sel string) int {
	t.Helper()
	start := len(obs.chat)
	sendCommand(t, pw, "execute if entity "+sel)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pump(200 * time.Millisecond)
		for _, line := range obs.chat[start:] {
			if m := passCount.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				return n
			}
			if strings.Contains(line, "commands.execute.conditional.fail") {
				return 0
			}
			if strings.Contains(line, "commands.execute.conditional.pass") {
				return 1
			}
		}
	}
	t.Fatalf("no reply to execute if entity %s: %v", sel, obs.chat[start:])
	return 0
}

var gameTimeReply = regexp.MustCompile(`commands\.time\.query\.gametime(\d+)`)

// spawnGameTime reads /time query gametime.
func spawnGameTime(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration)) int64 {
	t.Helper()
	start := len(obs.chat)
	sendCommand(t, pw, "time query gametime")
	// mc-go answers a command queued behind a running sprint only once
	// the sprint is over, which on a loaded machine takes a while.
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		pump(200 * time.Millisecond)
		for _, line := range obs.chat[start:] {
			if m := gameTimeReply.FindStringSubmatch(line); m != nil {
				n, _ := strconv.ParseInt(m[1], 10, 64)
				return n
			}
		}
	}
	t.Fatalf("no gametime reply: %q", obs.chat[start:])
	return 0
}

// spawnSprint runs /tick sprint n and waits until it is over — vanilla's
// sprint is asynchronous, commands queued behind it would interleave.
// Done is the sprint report, or (should a report go missing) the game
// clock having advanced the n ticks with the sprint no longer running.
func spawnSprint(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration), n int) {
	t.Helper()
	before := spawnGameTime(t, obs, pw, pump)
	start := len(obs.chat)
	sendCommand(t, pw, fmt.Sprintf("tick sprint %d", n))
	deadline := time.Now().Add(300 * time.Second)
	lastPoll := time.Now()
	for time.Now().Before(deadline) {
		pump(300 * time.Millisecond)
		for _, line := range obs.chat[start:] {
			if strings.Contains(line, "tick.sprint.report") {
				pump(300 * time.Millisecond)
				return
			}
		}
		if time.Since(lastPoll) > 15*time.Second {
			lastPoll = time.Now()
			if now := spawnGameTime(t, obs, pw, pump); now >= before+int64(n) {
				t.Logf("tick sprint %d: no report, but the clock moved %d ticks", n, now-before)
				return
			}
		}
	}
	// An in-process mc-go that stalls is easiest read from its stacks.
	_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
	t.Logf("chat since the sprint: %q", obs.chat[start:])
	t.Fatalf("tick sprint %d: not finished", n)
}

type spawnArenaResult struct {
	monstersOff  int // dark arena, spawn_monsters=false
	dark, lit    int // after spawn_monsters=true
	capMonsters  int // dark+lit after the long sprint
	creatures    int // open plains field after the long sprint
	afterMobsOff int // everything spawned once spawn_mobs=false
}

// TestNaturalSpawnArenaParity builds, on both servers:
//   - arena A: a sealed stone box (x/z 32..63, floor y=-61, roof y=-57,
//     interior 3 tall) — block and sky light 0 inside, so the only
//     valid spawn cells are the floor's y=-60;
//   - arena B: the same box at x -64..-33 with every interior cell a
//     light[level=1] block — block light 1 everywhere;
//   - field C: open grass at x 32..63, z -64..-33 (sky light 15);
//
// all painted plains by /fillbiome (with a zoom margin), the player
// parked at the origin in creative (45..89 blocks from every spawn
// cell: past the 24-block player and world-spawn exclusion, inside the
// 128-block chunk reach) and the clock pinned at noon. Then:
//
//   - spawn_monsters=false: 1200 ticks spawn no monster in A;
//   - spawn_monsters=true: 1200 more fill A while B stays empty —
//     Monster.isDarkEnoughToSpawn's block-light limit is 0 in the
//     overworld, so block light 1 alone vetoes;
//   - 4000 more: the monster count sits at the lone-player cap of 70
//     (70*289/289; a chunk call can overshoot by at most 3, and idle
//     despawns past 32 blocks keep it just under), the creature field
//     reaches the CREATURE cap of 10 through the every-400-ticks
//     cadence (a horse pack can overshoot by up to 5);
//   - spawn_mobs=false after a purge: 1200 ticks spawn nothing at all.
func TestNaturalSpawnArenaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Cap2"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		// The vanilla harness world is level-seed=1: slime chunks and
		// the biome zoom both key on the seed.
		s.WorldSeed = 1
		// A max-speed sprint with 70+ tracked mobs outruns the default
		// 256-packet outbound queue whenever the test client reads
		// slowly (a loaded machine); vanilla's netty buffer is unbounded.
		s.OutboundPacketLimit = 1 << 16
		s.OutboundByteLimit = 512 << 20
	})
	defer targets.cleanup()

	const (
		arenaA = "x=33,y=-60,z=33,dx=29,dy=2,dz=29"
		arenaB = "x=-63,y=-60,z=33,dx=29,dy=2,dz=29"
	)
	run := func(tag, addr string) spawnArenaResult {
		obs, pump, pw, done := patrolLegView(t, addr, 8)
		defer done()
		patrolSetup(t, pw, pump)
		var r spawnArenaResult
		sendCommand(t, pw, "gamemode creative Cap2")
		sendCommand(t, pw, "gamerule minecraft:advance_time false")
		sendCommand(t, pw, "gamerule minecraft:spawn_patrols false")
		sendCommand(t, pw, "time set 6000")
		// Nothing spawns while the arenas go up; monsters stay off for
		// the first sprint.
		sendCommand(t, pw, "gamerule minecraft:spawn_mobs false")
		sendCommand(t, pw, "gamerule minecraft:spawn_monsters false")
		sendCommand(t, pw, "tp Cap2 0.5 -60 0.5")
		pump(500 * time.Millisecond)
		for _, c := range []string{
			"fill 32 -61 32 63 -57 63 minecraft:stone hollow",
			"fill -64 -61 32 -33 -57 63 minecraft:stone hollow",
			"fill -63 -60 33 -34 -58 62 minecraft:light[level=1]",
			"fillbiome 28 -64 28 67 -57 67 minecraft:plains",
			"fillbiome -68 -64 28 -29 -57 67 minecraft:plains",
			"fillbiome 28 -64 -68 67 -57 -29 minecraft:plains",
		} {
			sendCommand(t, pw, c)
			pump(300 * time.Millisecond)
		}
		pump(time.Second)
		monster := "type=!minecraft:bat,type=!minecraft:player"
		count := func(region string) int {
			return spawnCountQuery(t, obs, pw, pump, "@e["+monster+","+region+"]")
		}
		if n := count(arenaA) + count(arenaB); n != 0 {
			t.Fatalf("%s: %d mobs in the arenas before spawning", tag, n)
		}

		sendCommand(t, pw, "gamerule minecraft:spawn_mobs true")
		pump(300 * time.Millisecond)
		spawnSprint(t, obs, pw, pump, 1200)
		r.monstersOff = count(arenaA)

		sendCommand(t, pw, "gamerule minecraft:spawn_monsters true")
		pump(300 * time.Millisecond)
		spawnSprint(t, obs, pw, pump, 1200)
		r.dark, r.lit = count(arenaA), count(arenaB)

		spawnSprint(t, obs, pw, pump, 4000)
		r.capMonsters = count(arenaA) + count(arenaB)
		// Plains' CREATURE list; animals wander off the field onto the
		// surrounding the_void grass, so count them anywhere.
		for _, ty := range []string{"sheep", "pig", "chicken", "cow", "horse", "donkey"} {
			r.creatures += spawnCountQuery(t, obs, pw, pump, "@e[type=minecraft:"+ty+"]")
		}

		sendCommand(t, pw, "gamerule minecraft:spawn_mobs false")
		// Killed slimes split (size 4 -> 2 -> 1) and drops land: purge
		// four times.
		for i := 0; i < 4; i++ {
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			pump(1500 * time.Millisecond)
		}
		pump(500 * time.Millisecond)
		sendCommand(t, pw, "gamerule minecraft:spawn_mobs false")
		pump(300 * time.Millisecond)
		spawnSprint(t, obs, pw, pump, 1200)
		left := "@e[type=!minecraft:player,type=!minecraft:item,type=!minecraft:experience_orb]"
		r.afterMobsOff = spawnCountQuery(t, obs, pw, pump, left)
		if r.afterMobsOff > 0 {
			from := len(obs.chat)
			sendCommand(t, pw, "execute as "+left+" run data get entity @s")
			pump(2 * time.Second)
			t.Logf("%s leftovers: %v", tag, obs.chat[from:])
		}
		sendCommand(t, pw, "gamerule minecraft:spawn_mobs true")
		t.Logf("%s: %+v", tag, r)
		return r
	}
	check := func(tag string, r spawnArenaResult) {
		if r.monstersOff != 0 {
			t.Errorf("%s: spawn_monsters=false still spawned %d monsters", tag, r.monstersOff)
		}
		if r.dark < 10 {
			t.Errorf("%s: dark arena got only %d monsters in 1200 ticks", tag, r.dark)
		}
		if r.lit != 0 {
			t.Errorf("%s: block-light-1 arena spawned %d monsters", tag, r.lit)
		}
		if r.capMonsters < 55 || r.capMonsters > 73 {
			t.Errorf("%s: %d monsters after the long sprint, want the 70 cap (55..73)", tag, r.capMonsters)
		}
		if r.creatures < 10 || r.creatures > 15 {
			t.Errorf("%s: %d creatures in the field, want the 10 cap (10..15)", tag, r.creatures)
		}
		if r.afterMobsOff != 0 {
			t.Errorf("%s: spawn_mobs=false spawned %d mobs", tag, r.afterMobsOff)
		}
	}
	v := run("vanilla", targets.vanilla)
	m := run("mc-go", targets.mcgo)
	t.Logf("vanilla %+v", v)
	t.Logf("mc-go   %+v", m)
	check("vanilla", v)
	check("mc-go", m)
}

// genSpawn is one add_entity a joining client saw.
type genSpawn struct {
	typ     string
	x, y, z float64
	yaw     byte
}

// collectGenSpawns drains a joined client's window like collectJoin,
// recording every streamed chunk and the first add_entity of every
// CREATURE-category entity.
func collectGenSpawns(t *testing.T, tag string, addr string, view int8, areas [][2]int) (map[[2]int32]bool, []genSpawn) {
	t.Helper()
	c, pr, pw := joinPlayBlackboxView(t, addr, "GenProbe", view)
	defer c.Close()
	creature := map[int32]string{}
	for _, n := range []string{"sheep", "pig", "chicken", "cow", "horse", "donkey", "mule", "rabbit",
		"wolf", "fox", "llama", "goat", "frog", "camel", "armadillo", "parrot", "ocelot", "panda",
		"polar_bear", "mooshroom", "turtle", "strider", "sniffer"} {
		if id, ok := server.EntityTypeID(n); ok {
			creature[id] = n
		}
	}
	chunks := map[[2]int32]bool{}
	var spawns []genSpawn
	seen := map[int32]bool{}

	drain := func() {
		start := time.Now()
		last, quiet := start, 3*time.Minute
		for time.Since(last) < quiet {
			_ = c.SetReadDeadline(time.Now().Add(quiet))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayChunkData:
				r := protocol.NewPacketReader(p.Data)
				x, _ := r.Int()
				z, _ := r.Int()
				chunks[[2]int32{x, z}] = true
				last, quiet = time.Now(), 40*time.Second
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				r.UUID()
				ty, _ := r.VarInt()
				name, ok := creature[ty]
				if !ok || seen[eid] {
					continue
				}
				seen[eid] = true
				x, _ := r.Double()
				y, _ := r.Double()
				z, _ := r.Double()
				// LpVec3 velocity: one zero byte, or six bytes plus a
				// VarInt scale extension when bit 2 is set.
				b0, _ := r.Byte()
				if b0 != 0 {
					r.Read(5)
					if b0&4 != 0 {
						r.VarInt()
					}
				}
				r.Byte() // pitch
				yaw, _ := r.Byte()
				spawns = append(spawns, genSpawn{name, x, y, z, yaw})
			case server.IDPlaySystemChatCB:
				t.Logf("%s chat: %s", tag, chatTextBlackbox(t, p.Data))
			case server.IDPlayChunkBatchDone:
				if err := ackChunkBatch(pw); err != nil {
					t.Fatal(err)
				}
			case server.IDPlayKeepAliveCB:
				if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
					t.Fatal(err)
				}
			case server.IDPlaySyncPos:
				sr := protocol.NewPacketReader(p.Data)
				if id, err := sr.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
						t.Fatal(err)
					}
					// Vanilla moves the chunk window on the client's next
					// position after a same-dimension teleport.
					x, _ := sr.Double()
					y, _ := sr.Double()
					z, _ := sr.Double()
					_ = sendMovePosRot(pw, x, y, z)
				}
			}
		}
		t.Logf("%s: %d chunks, %d creature spawns in %v", tag, len(chunks), len(spawns), last.Sub(start))
	}
	// Slow the game tick so freshly generated creatures barely stroll
	// before the census: chunk streaming still follows the batch acks
	// and the generator's own pace, but RandomStrollGoal gets a twentieth
	// of the ticks it would at 20 TPS.
	sendCommand(t, pw, "tick rate 1")
	drain()
	// The census reads the server's own entities (execute as … data
	// get entity @s Pos) in a box around each target: the add_entity
	// stream depends on the tracker's pairing schedule — vanilla
	// leaves some of a fresh chunk's mobs unpaired for minutes with
	// a stationary client — while the census does not.
	numRe := regexp.MustCompile(`(-?[0-9]+(?:\.[0-9]+)?(?:E-?[0-9]+)?)d`)
	var census []genSpawn
	for _, a := range areas {
		sendCommand(t, pw, fmt.Sprintf("tp GenProbe %d 200 %d", a[0], a[1]))
		drain()
		for _, ty := range genCensusTypes {
			sendCommand(t, pw, fmt.Sprintf("execute as @e[type=minecraft:%s,x=%d,y=-64,z=%d,dx=160,dy=400,dz=160] run data get entity @s Pos",
				ty, a[0]-80, a[1]-80))
		}
		sendCommand(t, pw, `tellraw @s "CENSUS-END"`)
		deadline := time.Now().Add(60 * time.Second)
		done := false
		lines := 0
		for !done && time.Now().Before(deadline) {
			_ = c.SetReadDeadline(deadline)
			p, err := pr.ReadPacket()
			if err != nil {
				t.Logf("%s: census read: %v (%d chat lines)", tag, err, lines)
				break
			}
			switch p.ID {
			case server.IDPlayDisconnect:
				t.Fatalf("%s: disconnected during census: %q", tag, p.Data)
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySystemChatCB, server.IDPlayPlayerChatCB:
				var line string
				if p.ID == server.IDPlaySystemChatCB {
					line = chatTextBlackbox(t, p.Data)
				} else {
					line = string(p.Data)
				}
				if lines++; lines == 1 {
					t.Logf("%s census chat: %.160q", tag, line)
				}
				if strings.Contains(line, "CENSUS-END") {
					done = true
					break
				}
				if !strings.Contains(line, "commands.data.entity.query") {
					continue
				}
				m := numRe.FindAllStringSubmatch(line, -1)
				i := strings.Index(line, "entity.minecraft.")
				if len(m) < 3 || i < 0 {
					continue
				}
				name := line[i+len("entity.minecraft."):]
				if j := strings.IndexAny(name, "-0123456789"); j > 0 {
					name = name[:j]
				}
				var v [3]float64
				for k := 0; k < 3; k++ {
					v[k], _ = strconv.ParseFloat(m[len(m)-3+k][1], 64)
				}
				census = append(census, genSpawn{typ: name, x: v[0], y: v[1], z: v[2]})
			}
		}
		if !done {
			t.Fatalf("%s: census at %v did not finish", tag, a)
		}
	}
	t.Logf("%s: census %d creatures, %d add_entity creatures seen", tag, len(census), len(spawns))
	return chunks, census
}

// genCensusTypes are the CREATURE types grassland biomes list.
var genCensusTypes = []string{"sheep", "pig", "chicken", "cow", "horse", "donkey", "mule", "rabbit", "wolf", "fox", "llama", "goat"}

// TestNaturalSpawnChunkGenParity pins NaturalSpawner
// .spawnMobsForChunkGeneration on a normal world (same seed, features
// stripped by the same data pack as TestNormalWorldChunkParity so the
// terrain matches block for block). Chunk-generation spawns roll a
// WorldgenRandom seeded setDecorationSeed(seed, minX, minZ), so which
// creatures spawn where is deterministic: every creature the census
// finds around two grassland teleport targets on one server must have
// a twin of the same type at (nearly) the same spot on the other. Mobs
// stroll between generation and the census (taken once the window
// streamed; both legs run at /tick rate 1 to keep that short), hence
// the 8-block tolerance; unmoved twins sit on the identical (clamped,
// block-aligned) spawn point, give or take a crowding nudge.
func TestNaturalSpawnChunkGenParity(t *testing.T) {
	const seed = 12345
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(seed),
		"generate-structures": "false",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("GenProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
		// Terrain generation competes with the vanilla JVM for the CPU;
		// a long generation burst must not read as a dead client.
		s.KeepAliveTimeout = 5 * time.Minute
		// A teleport into fresh terrain queues hundreds of chunk and
		// entity packets at once; keep the slow-reader budget out of it.
		s.OutboundPacketLimit = 1 << 16
		s.OutboundByteLimit = 512 << 20
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	areas := genSpawnAreas(t, ov, seed, 2)
	vc, vs := collectGenSpawns(t, "vanilla", tg.vanilla, 6, areas)
	mc, ms := collectGenSpawns(t, "mc-go", tg.mcgo, 6, areas)

	// Interior chunks: streamed by both, with all eight neighbours
	// streamed by both too (a creature near an edge chunk may have
	// wandered from one the other side never sent).
	both := func(k [2]int32) bool { return vc[k] && mc[k] }
	interior := map[[2]int32]bool{}
	for k := range vc {
		ok := true
		for dx := int32(-1); dx <= 1 && ok; dx++ {
			for dz := int32(-1); dz <= 1 && ok; dz++ {
				ok = both([2]int32{k[0] + dx, k[1] + dz})
			}
		}
		if ok {
			interior[k] = true
		}
	}
	// The census box is 160 blocks wide around each target: the side
	// being checked counts creatures within 64 blocks of a target, its
	// twins may stand anywhere within 80 (a mob near the rim may have
	// strolled across it).
	within := func(list []genSpawn, r float64) []genSpawn {
		var out []genSpawn
		for _, s := range list {
			for _, a := range areas {
				dx, dz := s.x-float64(a[0]), s.z-float64(a[1])
				if dx*dx+dz*dz < r*r && interior[[2]int32{int32(floorDiv16(s.x)), int32(floorDiv16(s.z))}] {
					out = append(out, s)
					break
				}
			}
		}
		return out
	}
	// match pairs every creature of a with the nearest same-type
	// creature of b within 8 blocks horizontally and 3 vertically,
	// nearest pairs first, one twin each.
	match := func(a, b []genSpawn) (near, exact int, missing []genSpawn) {
		type cand struct {
			i, j int
			d    float64
		}
		var cs []cand
		for i, s := range a {
			for j, o := range b {
				dx, dy, dz := o.x-s.x, o.y-s.y, o.z-s.z
				if o.typ == s.typ && dx*dx+dz*dz <= 64 && dy*dy <= 9 {
					cs = append(cs, cand{i, j, dx*dx + dz*dz + dy*dy})
				}
			}
		}
		sort.Slice(cs, func(x, y int) bool { return cs[x].d < cs[y].d })
		ua, ub := make([]bool, len(a)), make([]bool, len(b))
		for _, c := range cs {
			if ua[c.i] || ub[c.j] {
				continue
			}
			ua[c.i], ub[c.j] = true, true
			near++
			s, o := a[c.i], b[c.j]
			t.Logf("pair %s: %.3f %.1f %.3f | %.3f %.1f %.3f", s.typ, s.x, s.y, s.z, o.x, o.y, o.z)
			// Yaw is not compared: BodyRotationControl turns an idle
			// mob's body toward its wandering head within ticks.
			if math.Abs(o.x-s.x) <= 0.05 && o.y == s.y && math.Abs(o.z-s.z) <= 0.05 {
				exact++
			}
		}
		for i, s := range a {
			if !ua[i] {
				missing = append(missing, s)
			}
		}
		return
	}
	vi, mi := within(vs, 64), within(ms, 64)
	t.Logf("%d interior chunks; creatures within 64 blocks of a target: vanilla %d mc-go %d", len(interior), len(vi), len(mi))
	vn, ve, vmiss := match(vi, within(ms, 80))
	mn, me, mmiss := match(mi, within(vs, 80))
	t.Logf("vanilla->mc-go: %d/%d twinned within 8 blocks, %d on the identical spot", vn, len(vi), ve)
	t.Logf("mc-go->vanilla: %d/%d twinned within 8 blocks, %d on the identical spot", mn, len(mi), me)
	for _, s := range vmiss {
		t.Logf("vanilla-only %s at %.2f %.2f %.2f", s.typ, s.x, s.y, s.z)
	}
	for _, s := range mmiss {
		t.Logf("mc-go-only %s at %.2f %.2f %.2f", s.typ, s.x, s.y, s.z)
	}
	if len(vi) < 10 {
		t.Fatalf("only %d vanilla creatures in the compared areas", len(vi))
	}
	// Random per-tick spawns would never line up like this; a mob or
	// two may wander past the 8-block tolerance before the census.
	if float64(vn) < 0.8*float64(len(vi)) || float64(mn) < 0.8*float64(len(mi)) {
		t.Errorf("chunk-generation creatures diverge: %d/%d vanilla and %d/%d mc-go twinned", vn, len(vi), mn, len(mi))
	}
	// Measured run: 18/18 and 19/19 twinned, 6 on the identical spot.
	if ve < 3 {
		t.Errorf("only %d vanilla creatures have an mc-go twin on the identical spawn point", ve)
	}
}

func floorDiv16(v float64) int {
	i := int(v)
	if float64(i) > v {
		i--
	}
	return i >> 4
}

// genSpawnAreas finds, with mc-go's generator (biomes are pinned
// against vanilla by TestTerrainBiomeParity), up to n spots at least
// 400 blocks apart whose 11x11 chunk neighbourhood is mostly grassland
// — biomes whose CREATURE lists the farm animals — so chunk generation
// spawns plenty to compare.
func genSpawnAreas(t *testing.T, ov map[string]json.RawMessage, seed int64, n int) [][2]int {
	t.Helper()
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	g.RS.SetLevelBounds(-64, 384)
	grass := map[string]bool{"plains": true, "sunflower_plains": true, "meadow": true,
		"savanna": true, "forest": true, "flower_forest": true, "birch_forest": true}
	var out [][2]int
	for r := 160; r <= 2400 && len(out) < n; r += 160 {
		for _, c := range [][2]int{{r, 0}, {-r, 0}, {0, r}, {0, -r}, {r, r}, {-r, -r}, {r, -r}, {-r, r}} {
			far := true
			for _, o := range out {
				if dx, dz := c[0]-o[0], c[1]-o[1]; dx*dx+dz*dz < 400*400 {
					far = false
				}
			}
			if !far {
				continue
			}
			ok := 0
			for dx := -5; dx <= 5; dx += 2 {
				for dz := -5; dz <= 5; dz += 2 {
					if grass[strings.TrimPrefix(g.BiomeAt(int32(c[0]+dx*16), 64, int32(c[1]+dz*16)), "minecraft:")] {
						ok++
					}
				}
			}
			if ok >= 32 {
				t.Logf("grassland area at %d,%d (%d/36 samples)", c[0], c[1], ok)
				out = append(out, c)
				if len(out) == n {
					break
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no grassland area within 2400 blocks")
	}
	return out
}
