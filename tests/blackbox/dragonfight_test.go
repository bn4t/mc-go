//go:build blackbox

package blackbox

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/worldgen"
)

// fightLog renders a dragon fight trace into comparable lines. The
// dragon's flight is random, so its moves are left out; what it does
// deterministically - boss bar events, spawn point, pairing data, the
// crystals, level events, explosions, beams - is kept, by role.
type fightLog struct {
	roles      map[int32]string
	crystals   int
	dragons    int
	dragonID   map[int32]bool
	dragonData map[int32]bool
	noYaw      bool
	lines      []string
	spawns     []string // crystal spawns, sorted at the end of a phase
}

func newFightLog() *fightLog {
	return &fightLog{roles: map[int32]string{}, dragonID: map[int32]bool{}, dragonData: map[int32]bool{}}
}

func (l *fightLog) add(t *testing.T, ps []dfPkt) {
	crystal, _ := server.EntityTypeID("end_crystal")
	dragon, _ := server.EntityTypeID("ender_dragon")
	for _, p := range ps {
		switch p.id {
		case 0x01:
			s, ok := dfDecodeSpawn(p.data)
			if !ok {
				continue
			}
			switch s.typ {
			case crystal:
				if _, seen := l.roles[s.eid]; seen {
					continue
				}
				l.crystals++
				r := fmt.Sprintf("crystal@(%.1f,%.1f,%.1f)", s.x, s.y, s.z)
				l.roles[s.eid] = r
				yaw := fmt.Sprint(s.yaw)
				if l.noYaw {
					yaw = "?" // the respawn's spikes roll RandomSource.create()
				}
				l.spawns = append(l.spawns, fmt.Sprintf("spawn %s yaw=%s pitch=%d head=%d", r, yaw, s.pitch, s.headYaw))
			case dragon:
				if _, seen := l.roles[s.eid]; seen {
					continue
				}
				l.dragons++
				r := fmt.Sprintf("dragon%d", l.dragons)
				l.roles[s.eid] = r
				l.dragonID[s.eid] = true
				pos := fmt.Sprintf("(%.3f,%.3f,%.3f)", s.x, s.y, s.z)
				if l.dragons == 1 {
					// The first dragon spawns while the arena loads; when
					// its chunk reaches the client depends on generation
					// speed, so it may already be flying.
					pos = "?"
				}
				l.lines = append(l.lines, fmt.Sprintf("spawn %s pos=%s pitch=%d data=%d", r, pos, s.pitch, s.data))
			}
		case 0x63:
			r := protocol.NewPacketReader(p.data)
			eid, _ := r.VarInt()
			role, ok := l.roles[eid]
			if !ok {
				continue
			}
			rest, _ := r.Read(r.Remaining())
			if l.dragonID[eid] {
				// Only the first (pairing) pack: later ones follow the
				// random flight (crystal healing, phase changes).
				if !l.dragonData[eid] {
					l.dragonData[eid] = true
					l.lines = append(l.lines, "data "+role+" "+hex.EncodeToString(rest))
				}
				continue
			}
			l.lines = append(l.lines, "data "+role+" "+hex.EncodeToString(rest))
		case 0x4D:
			r := protocol.NewPacketReader(p.data)
			n, _ := r.VarInt()
			for i := int32(0); i < n; i++ {
				eid, _ := r.VarInt()
				// Only the placed crystals: which spike crystals a
				// player tracks depends on when their chunks' entities
				// loaded relative to the chunk send (vanilla pairs late
				// ones only on a player move), so their removals are
				// timing-dependent.
				if role, ok := l.roles[eid]; ok && !l.dragonID[eid] && strings.Contains(role, ",65.0,") {
					l.lines = append(l.lines, "remove "+role)
				}
			}
		case 0x09:
			b := dfDecodeBoss(t, p.data)
			switch b.op {
			case 0:
				l.lines = append(l.lines, fmt.Sprintf("bar add name=%s prog=%v color=%d style=%d flags=%d", b.name, b.prog, b.color, b.style, b.flags))
			case 2:
				l.lines = append(l.lines, fmt.Sprintf("bar progress %v", b.prog))
			default:
				l.lines = append(l.lines, fmt.Sprintf("bar op %d", b.op))
			}
		case 0x2E:
			r := protocol.NewPacketReader(p.data)
			id, _ := r.Int()
			pos, _ := r.Long()
			data, _ := r.Int()
			if id == 2001 || id == 2008 || id == 1017 {
				continue // block breaks, dragon griefing and fireball sounds ride the flight
			}
			l.lines = append(l.lines, fmt.Sprintf("level %d at %d,%d,%d data %d", id, pos>>38, pos<<52>>52, pos<<26>>38, data))
		case 0x24:
			ep := decodeExplode(t, p.data)
			l.lines = append(l.lines, fmt.Sprintf("explode (%.2f,%.2f,%.2f) r=%v blocks>0=%v", ep.x, ep.y, ep.z, ep.radius, ep.blockCount > 0))
		case 0x79:
			txt := chatTextBlackbox(t, p.data)
			if strings.HasPrefix(txt, "commands.save") || strings.Contains(txt, "eleport") {
				continue // saves, and the cross-dimension tp reply (an mc-go gap outside this pin)
			}
			l.lines = append(l.lines, "chat "+txt)
		}
	}
}

// flushSpawns appends the phase's crystal spawns in position order
// (chunk streaming order differs between the servers).
func (l *fightLog) flushSpawns() {
	sort.Strings(l.spawns)
	l.lines = append(l.lines, l.spawns...)
	l.spawns = nil
}

// fightSaveData reads ender_dragon_fight.dat's data compound, with the
// random dragon uuid reduced to its presence.
func fightSaveData(t *testing.T, worldDir string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(worldDir, "dimensions", "minecraft", "the_end", "data", "minecraft", "ender_dragon_fight.dat"))
	if err != nil {
		return "missing: " + err.Error()
	}
	defer f.Close()
	v, err := readGzipNBTBlackbox(f)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	root, _ := v.(map[string]any)
	data, _ := root["data"].(map[string]any)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		val := data[k]
		if k == "dragon_uuid" {
			val = "<set>"
		}
		if l, ok := val.(nbt.List); ok {
			val = fmt.Sprint(l.Items)
			if k == "respawn_crystals" {
				val = fmt.Sprintf("%d uuids", len(l.Items))
			}
		}
		fmt.Fprintf(&b, "%s=%v ", k, val)
	}
	return fmt.Sprintf("DataVersion=%v %s", root["DataVersion"], b.String())
}

// metaPhase reads DATA_PHASE (index 16, INT) out of a set_entity_data
// body, walking the entry types a dragon's pack carries.
func metaPhase(body []byte) (int32, bool) {
	r := protocol.NewPacketReader(body)
	if _, err := r.VarInt(); err != nil {
		return 0, false
	}
	for {
		idx, err := r.Byte()
		if err != nil || idx == 0xFF {
			return 0, false
		}
		typ, _ := r.VarInt()
		switch typ {
		case 0, 8: // byte, boolean
			_, _ = r.Byte()
		case 1, 20: // varint, pose
			v, _ := r.VarInt()
			if idx == 16 && typ == 1 {
				return v, true
			}
		case 3: // float
			_, _ = r.Float()
		default:
			return 0, false
		}
	}
}

// quietChunks keeps tracing until no chunk arrived for 5 seconds (at
// most two minutes): the window has streamed, its entities paired.
func quietChunks(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) []dfPkt {
	t.Helper()
	var out []dfPkt
	end := time.Now().Add(2 * time.Minute)
	for time.Now().Before(end) {
		ps := dfTrace(t, c, pr, pw, 5*time.Second, nil)
		out = append(out, ps...)
		chunk := false
		for _, p := range ps {
			if p.id == 0x2D {
				chunk = true
			}
		}
		if !chunk {
			break
		}
	}
	return out
}

// saveAndRead runs save-all flush, waits for its success reply and
// reads the fight's saved data.
func saveAndRead(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, worldDir string) string {
	t.Helper()
	sendCommand(t, pw, "save-all flush")
	dfTrace(t, c, pr, pw, 3*time.Minute, func(p dfPkt) bool {
		return p.id == 0x79 && strings.Contains(chatTextBlackbox(t, p.data), "commands.save.success")
	})
	return fightSaveData(t, worldDir)
}

// sortTickRuns sorts runs of consecutive same-payload data lines and of
// consecutive removals: entities that change in one tick go out in
// entity-map order, which differs between the servers.
func sortTickRuns(lines []string) []string {
	key := func(s string) string {
		f := strings.Fields(s)
		switch {
		case len(f) == 3 && f[0] == "data":
			return "data " + f[2]
		case len(f) == 2 && f[0] == "remove":
			return "remove"
		}
		return ""
	}
	out := append([]string(nil), lines...)
	for i := 0; i < len(out); {
		k := key(out[i])
		j := i + 1
		for k != "" && j < len(out) && key(out[j]) == k {
			j++
		}
		sort.Strings(out[i:j])
		i = j
	}
	return out
}

// blockChecks asks `execute if block` for each (pos, block) pair and
// returns pass/fail per check.
func blockChecks(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, checks [][2]string) []string {
	t.Helper()
	var out []string
	for _, ck := range checks {
		sendCommand(t, pw, "execute if block "+ck[0]+" "+ck[1])
		res := "no reply"
		dfTrace(t, c, pr, pw, 20*time.Second, func(p dfPkt) bool {
			if p.id != 0x79 {
				return false
			}
			txt := chatTextBlackbox(t, p.data)
			switch {
			case strings.Contains(txt, "conditional.pass"):
				res = "pass"
			case strings.Contains(txt, "conditional.fail"):
				res = "fail"
			default:
				return false
			}
			return true
		})
		out = append(out, ck[0]+" "+ck[1]+" "+res)
	}
	return out
}

// TestDragonFightParity pins EnderDragonFight in a generated End (seed
// 12345, End features limited to the spikes): entering the End hands
// the player the dragon's boss bar (PINK, PROGRESS, music+fog), the
// ten spike crystals stand at the spike tops with the feature's rolled
// yaw, the first scan places the inactive exit podium on the origin
// column and the dragon spawns at (0, 128, 0) in HOLDING_PATTERN with
// health 200; the saved fight data matches. /kill then records the
// kill: the bar's progress drops to 0 and the bar leaves, the active
// portal fills, the egg lands on the pillar, gateway slot 11 opens with
// level event 3000. Four crystals on the portal's sides run the
// respawn: beams, level events 3001, the ten spikes rebuilt with
// invulnerable crystals, the blasts, and a new dragon.
func TestDragonFightParity(t *testing.T) {
	const seed = 12345
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(p string) bool { return p == "end_spike" }, true, true)
	props := map[string]string{
		"level-type": "minecraft:normal", "generator-settings": "{}",
		"level-seed": fmt.Sprint(seed), "generate-structures": "false",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("DragonProbe")
	mdir := t.TempDir()
	tg := requireBlackboxSlow(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
		s.WorldDir = mdir
	})
	vdir := vanillaDirForDebug
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute

	podium := [][2]string{
		{"0 63 0", "minecraft:bedrock"}, {"0 64 0", "minecraft:bedrock"}, {"0 67 0", "minecraft:bedrock"},
		{"0 68 0", "minecraft:air"}, {"0 68 0", "minecraft:dragon_egg"},
		{"0 66 -1", "minecraft:wall_torch[facing=north]"}, {"1 66 0", "minecraft:wall_torch[facing=east]"},
		{"0 66 1", "minecraft:wall_torch[facing=south]"}, {"-1 66 0", "minecraft:wall_torch[facing=west]"},
		{"3 64 0", "minecraft:bedrock"}, {"2 64 2", "minecraft:bedrock"}, {"0 64 -3", "minecraft:bedrock"},
		{"1 64 0", "minecraft:air"}, {"2 64 0", "minecraft:end_portal"}, {"-1 64 -1", "minecraft:end_portal"},
		{"2 63 0", "minecraft:bedrock"}, {"3 63 0", "minecraft:end_stone"}, {"2 63 2", "minecraft:end_stone"},
		{"0 65 2", "minecraft:air"},
		{"-92 75 -30", "minecraft:end_gateway"}, {"-92 77 -30", "minecraft:bedrock"}, {"-92 73 -30", "minecraft:bedrock"},
		{"-91 74 -30", "minecraft:bedrock"}, {"-91 75 -30", "minecraft:air"},
	}
	run := func(tag, addr, worldDir string) []string {
		c, pr, pw := joinPlayBlackboxView(t, addr, "DragonProbe", 8)
		defer c.Close()
		l := newFightLog()
		dfTrace(t, c, pr, pw, 3*time.Second, nil)
		sendCommand(t, pw, "gamerule minecraft:spawn_monsters false")
		sendCommand(t, pw, "execute in minecraft:the_end run tp @s 0.5 70 40.5")
		// The arena loads and the dragon spawns: wait for it.
		ps := dfTrace(t, c, pr, pw, 4*time.Minute, func(p dfPkt) bool {
			if p.id != 0x01 {
				return false
			}
			s, _ := dfDecodeSpawn(p.data)
			dragon, _ := server.EntityTypeID("ender_dragon")
			return s.typ == dragon
		})
		ps = append(ps, quietChunks(t, c, pr, pw)...)
		l.add(t, ps)
		l.spawns = nil // which spike crystals pair now is timing-dependent; their yaws are read below
		for _, sp := range worldgen.EndSpikes(seed) {
			sendCommand(t, pw, fmt.Sprintf("execute positioned %d.5 %d %d.5 run data get entity @e[type=minecraft:end_crystal,limit=1,sort=nearest] Rotation[0]",
				sp.X, sp.Height+1, sp.Z))
			dfTrace(t, c, pr, pw, 20*time.Second, func(p dfPkt) bool {
				if p.id != 0x79 {
					return false
				}
				txt := chatTextBlackbox(t, p.data)
				if !strings.Contains(txt, "commands.data") && !strings.Contains(txt, "argument.entity") {
					return false
				}
				l.lines = append(l.lines, fmt.Sprintf("spike (%d,%d) crystal yaw %s", sp.X, sp.Z, txt))
				return true
			})
		}
		l.lines = append(l.lines, blockChecks(t, c, pr, pw, podium)...)
		l.lines = append(l.lines, "saved "+saveAndRead(t, c, pr, pw, worldDir))

		// The kill.
		sendCommand(t, pw, "kill @e[type=minecraft:ender_dragon]")
		l.add(t, dfTrace(t, c, pr, pw, 4*time.Second, nil))
		l.lines = append(l.lines, blockChecks(t, c, pr, pw, podium)...)
		l.lines = append(l.lines, "saved "+saveAndRead(t, c, pr, pw, worldDir))

		// The respawn: a crystal on each side of the exit portal.
		sendCommand(t, pw, "give @s minecraft:end_crystal 4")
		for _, side := range [][4]int{{4, 0, 3, 0}, {-3, 0, -3, 0}, {0, 4, 0, 3}, {0, -3, 0, -3}} {
			sendCommand(t, pw, fmt.Sprintf("tp @s %d.5 65 %d.5", side[0], side[1]))
			dfTrace(t, c, pr, pw, 700*time.Millisecond, nil)
			sendUseItemOn(t, pw, int32(side[2]), 64, int32(side[3]), 1, 1)
			l.add(t, dfTrace(t, c, pr, pw, 700*time.Millisecond, nil))
		}
		// The player stays by the portal for the whole sequence, inside
		// the 64 blocks of the level events at (0, 128, 0).
		l.noYaw = true
		ps = dfTrace(t, c, pr, pw, 3*time.Minute, func(p dfPkt) bool {
			if p.id != 0x01 {
				return false
			}
			s, _ := dfDecodeSpawn(p.data)
			dragon, _ := server.EntityTypeID("ender_dragon")
			return s.typ == dragon
		})
		ps = append(ps, dfTrace(t, c, pr, pw, 5*time.Second, nil)...)
		l.add(t, ps)
		l.flushSpawns()
		l.lines = append(l.lines, "saved "+saveAndRead(t, c, pr, pw, worldDir))

		// Landing: with only a creative player around, LANDING_APPROACH
		// flies to the podium, LANDING settles on it, SITTING_SCANNING
		// finds no target for 100 ticks, TAKEOFF leaves and HOLDING_PATTERN
		// resumes once 10 blocks out.
		var dragonEID int32
		for eid := range l.dragonID {
			if l.roles[eid] == "dragon2" {
				dragonEID = eid
			}
		}
		sendCommand(t, pw, "data merge entity @e[type=minecraft:ender_dragon,limit=1] {DragonPhase:2}")
		var phases []int32
		dfTrace(t, c, pr, pw, 3*time.Minute, func(p dfPkt) bool {
			if p.id != 0x63 {
				return false
			}
			r := protocol.NewPacketReader(p.data)
			if eid, _ := r.VarInt(); eid != dragonEID {
				return false
			}
			if ph, ok := metaPhase(p.data); ok && (len(phases) == 0 || phases[len(phases)-1] != ph) {
				phases = append(phases, ph)
			}
			return len(phases) >= 5
		})
		l.lines = append(l.lines, fmt.Sprintf("landing phases %v", phases))

		// The killing blow in flight: handleKillingBlow leaves 1 health
		// and DYING; the dragon flies to the podium, dies there and
		// tickDeath runs (global level event 1028, eleven experience
		// awards - 12000 a first kill, 500 now), then the fight records
		// the kill: progress 0, the bar leaves, gateway slot 1 opens.
		sendCommand(t, pw, "damage @e[type=minecraft:ender_dragon,limit=1] 1000 minecraft:player_attack by DragonProbe")
		orbType, _ := server.EntityTypeID("experience_orb")
		awards := map[string]bool{}
		var death []string
		phases = nil
		dfTrace(t, c, pr, pw, 3*time.Minute, func(p dfPkt) bool {
			r := protocol.NewPacketReader(p.data)
			switch p.id {
			case 0x63:
				if eid, _ := r.VarInt(); eid == dragonEID {
					if ph, ok := metaPhase(p.data); ok && (len(phases) == 0 || phases[len(phases)-1] != ph) {
						phases = append(phases, ph)
					}
				}
			case 0x01:
				if s, _ := dfDecodeSpawn(p.data); s.typ == orbType {
					awards[fmt.Sprintf("%.2f", s.y)] = true
				}
			case 0x2E:
				id, _ := r.Int()
				_, _ = r.Long()
				_, _ = r.Int()
				global, _ := r.Bool()
				if id == 1028 {
					death = append(death, fmt.Sprintf("level 1028 global %v", global))
				}
			case 0x09:
				b := dfDecodeBoss(t, p.data)
				if b.op != 2 || b.prog == 0 {
					death = append(death, fmt.Sprintf("bar op %d prog %v", b.op, b.prog))
				}
			case 0x4D:
				n, _ := r.VarInt()
				for i := int32(0); i < n; i++ {
					if eid, _ := r.VarInt(); eid == dragonEID {
						death = append(death, "remove dragon2")
						return true
					}
				}
			}
			return false
		})
		l.lines = append(l.lines, fmt.Sprintf("death phases %v", phases))
		l.lines = append(l.lines, death...)
		l.lines = append(l.lines, fmt.Sprintf("xp awards %d", len(awards)))
		l.lines = append(l.lines, blockChecks(t, c, pr, pw, [][2]string{
			{"91 75 29", "minecraft:end_gateway"}, {"91 77 29", "minecraft:bedrock"}, {"0 68 0", "minecraft:dragon_egg"},
		})...)
		l.lines = append(l.lines, "saved "+saveAndRead(t, c, pr, pw, worldDir))
		return sortTickRuns(l.lines)
	}
	v := run("vanilla", tg.vanilla, filepath.Join(vdir, "world"))
	m := run("mc-go", tg.mcgo, mdir)
	t.Logf("vanilla:\n%s", strings.Join(v, "\n"))
	if strings.Join(v, "\n") != strings.Join(m, "\n") {
		t.Errorf("dragon fight differs\nmc-go:\n%s", strings.Join(m, "\n"))
		for i := 0; i < len(v) || i < len(m); i++ {
			var a, b string
			if i < len(v) {
				a = v[i]
			}
			if i < len(m) {
				b = m[i]
			}
			if a != b {
				t.Errorf("first difference at line %d:\nvanilla %q\nmc-go   %q", i, a, b)
				break
			}
		}
	}
}
