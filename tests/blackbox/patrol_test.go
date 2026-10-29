package blackbox

// TestPatrolSpawnParity drives PatrolSpawner's natural spawn cycle on
// both servers. The countdown is 12000+rand(1200) ticks with a 1-in-5
// spawn roll each cycle — `tick rate 2000` paces the wait while a
// re-pinned `time set 120000` keeps isBrightOutside true and the day
// counter at 5 so effectiveDifficulty stays ~1.5. (advance_time must
// stay ON: ServerChunkCache only calls tickCustomSpawners while tickTime
// is set — and spawn_mobs gamerule stays on too; the_void's empty spawn
// lists keep the natural cycle quiet.) Vanilla's custom spawners need
// spawn-enemies on, so the vanilla leg overrides spawn-monsters=true.
// The tp hop every poll glues the player to the leader so the group
// never crosses the 128-block patrolling despawn radius.
//
// Compared per leg: the patrol size (ceil(effectiveDifficulty)+1 = 3
// on a fresh NORMAL world), exactly one leader carrying the ominous
// head patch [9 12 18 72], and every member wearing the finalizeSpawn
// crossbow in mainhand.
//
// TestPatrolMoveParity pins LongDistancePatrolGoal on a summoned pair:
// a Patrolling leader and one companion walking a patrol_target laid
// due east. With the target 296 blocks out, the rotated ~117° skewed
// waypoint (yRot(90) is radians in vanilla) sits ~(349, 106) off the
// leader, so the leader's march is east-positive and z-positive —
// a wander fallback can't imitate that heading. Members trail the
// leader's pushed waypoint.

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

type patrolObs struct {
	heads     map[int32][]int32 // eid -> head component ids
	mainhand  map[int32]string  // eid -> mainhand item name
	pillagers map[int32]bool    // pillager eids seen
	chat      []string
}

// patrolLeg joins Cap2 and wires the shared packet plumbing: keepalive
// echoes, teleport confirms, chat capture, and the pillager
// spawn/equipment fold.
func patrolLeg(t *testing.T, addr string) (*patrolObs, func(time.Duration), *protocol.Writer, func()) {
	t.Helper()
	return patrolLegView(t, addr, -1)
}

// patrolLegView is patrolLeg with a requested client view distance
// (-1 sends no Client Information).
func patrolLegView(t *testing.T, addr string, view int8) (*patrolObs, func(time.Duration), *protocol.Writer, func()) {
	t.Helper()
	c, pr, pw := joinPlayBlackboxView(t, addr, "Cap2", view)
	pilType, _ := server.EntityTypeID("pillager")
	obs := &patrolObs{
		heads:     map[int32][]int32{},
		mainhand:  map[int32]string{},
		pillagers: map[int32]bool{},
	}
	fold := func(p protocol.Packet) {
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlayChunkBatchDone:
			// A leg that asked for a view distance keeps acknowledging so
			// the whole window streams (and counts as loaded).
			if view >= 0 {
				_ = ackChunkBatch(pw)
			}
		case server.IDPlaySystemChatCB:
			obs.chat = append(obs.chat, chatTextBlackbox(t, p.Data))
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			r.UUID()
			ty, _ := r.VarInt()
			if ty == pilType {
				obs.pillagers[eid] = true
			}
		case server.IDPlaySetEquipment:
			r := protocol.NewPacketReader(p.Data)
			ee, _ := r.VarInt()
			for {
				slot, err := r.Byte()
				if err != nil {
					break
				}
				last := slot&0x80 == 0
				slot &= 0x7F
				st, _, _, comps, err := readSlotBBComps(r)
				if err != nil {
					break
				}
				switch {
				case slot == 5 && !st.Empty():
					obs.heads[ee] = comps
				case slot == 0 && !st.Empty():
					nm, _ := world.ItemName(st.ID)
					obs.mainhand[ee] = nm
				}
				if last {
					break
				}
			}
		}
	}
	pump := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				return
			}
			fold(p)
		}
	}
	return obs, pump, pw, func() { c.Close() }
}

// patrolSetup is the shared preamble both patrol tests run per leg.
func patrolSetup(t *testing.T, pw *protocol.Writer, pump func(time.Duration)) {
	t.Helper()
	pump(1200 * time.Millisecond)
	sendCommand(t, pw, "gamerule minecraft:advance_weather false")
	// Phantom + wandering-trader spawners tick alongside the patrol
	// one; off, so a pack can't pollute the observation window.
	sendCommand(t, pw, "gamerule minecraft:spawn_phantoms false")
	sendCommand(t, pw, "gamerule minecraft:spawn_wandering_traders false")
	// randomTickSpeed 0 keeps the accelerated tick cheap — 17ms of
	// random-tick work per tick would cap mc-go near 60 tps.
	sendCommand(t, pw, "gamerule minecraft:random_tick_speed 0")
	sendCommand(t, pw, "weather clear")
	sendCommand(t, pw, "difficulty normal")
	pump(600 * time.Millisecond)
}

func TestPatrolSpawnParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		// spawnEnemies is a constructor arg into tickCustomSpawners —
		// the harness default false would suppress PatrolSpawner
		// entirely.
		"spawn-monsters": "true",
	}, map[string]string{
		"ops.json": opsJSONFor("Cap2"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *patrolObs {
		obs, pump, pw, done := patrolLeg(t, addr)
		defer done()
		patrolSetup(t, pw, pump)

		// ~2000 tps burns a 13200-tick patrol cycle every ~7s while
		// packets still flush in real time — tick sprint batches replies
		// so hard that a mid-burst spawn marches past the 128-block
		// instant-despawn radius before it can be observed. `time set
		// 120000` every poll keeps the day at 5 (bright ~93% of the
		// drift, early_game gate satisfied).
		sendCommand(t, pw, "time set 120000")
		sendCommand(t, pw, "tick rate 2000")
		leaderTP := "tp Cap2 @e[type=minecraft:pillager,nbt={PatrolLeader:1b},limit=1]"
		deadline := time.Now().Add(720 * time.Second)
		spawned := false
		for time.Now().Before(deadline) && !spawned {
			sendCommand(t, pw, "time set 120000")
			base := len(obs.chat)
			sendCommand(t, pw, leaderTP)
			pump(150 * time.Millisecond)
			for _, line := range obs.chat[base:] {
				if strings.Contains(line, "teleport.success") {
					spawned = true
				}
			}
			if len(obs.pillagers) > 0 {
				spawned = true
			}
		}
		if !spawned {
			t.Fatalf("no patrol spawned (chat %v)", obs.chat)
		}
		// Real-time again; the tp keeps the player beside the patrol.
		sendCommand(t, pw, "tick rate 20")
		sendCommand(t, pw, leaderTP)
		// The whole group lands in one tick; a few seconds catches
		// every member's spawn + set_equipment.
		pump(4 * time.Second)
		return obs
	}

	legs := []*patrolObs{run(targets.vanilla), run(targets.mcgo)}
	wantComps := []int32{9, 12, 18, 72}
	for i, leg := range legs {
		n := len(leg.pillagers)
		// ceil(effectiveDifficulty)+1 = 3 is the cap — a member whose
		// jittered cell fails isValidEmptySpawnBlock/isUnobstructed is
		// skipped without breaking the loop, so vanilla can land 2.
		if n < 2 || n > 3 {
			t.Fatalf("leg %d: patrol size = %d, want 2..3 (cap ceil(1.5)+1)", i, n)
		}
		leaders := 0
		for eid := range leg.pillagers {
			if h, ok := leg.heads[eid]; ok {
				sort.Slice(h, func(a, b int) bool { return h[a] < h[b] })
				leaders++
				if len(h) != len(wantComps) {
					t.Fatalf("leg %d: leader head comps %v", i, h)
				}
				for j, c := range wantComps {
					if h[j] != c {
						t.Fatalf("leg %d: leader head comps %v", i, h)
					}
				}
			}
			if leg.mainhand[eid] != "crossbow" {
				t.Fatalf("leg %d: member %d mainhand = %q, want crossbow", i, eid, leg.mainhand[eid])
			}
		}
		if leaders != 1 {
			t.Fatalf("leg %d: %d ominous heads, want exactly 1 leader", i, leaders)
		}
	}
}

func TestPatrolMoveParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Cap2"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	type move struct {
		lead0, lead1 [3]float64
		mem0, mem1   [][3]float64
		patrolling   bool // leader's `data get ... Patrolling` reply
	}
	run := func(addr string) *move {
		obs, pump, pw, done := patrolLeg(t, addr)
		defer done()
		patrolSetup(t, pw, pump)
		// Creative: mobs never target the player (LongDistancePatrol
		// Goal's canUse needs getTarget()==null) while the player still
		// anchors removeWhenFarAway as a non-spectator. The early_game
		// gate keeps PatrolSpawner quiet at day <120000.
		sendCommand(t, pw, "gamemode creative Cap2")
		sendCommand(t, pw, "time set 6000")
		sendCommand(t, pw, "tp Cap2 0.5 -60 0.5")
		pump(400 * time.Millisecond)
		// The patrol: a leader at +4 east and a companion at +4 north,
		// both patrolling a target 296 blocks due east. Positions land
		// at mob feet on the flat surface.
		sendCommand(t, pw, "summon minecraft:pillager 4.5 -60 0.5 {Patrolling:1b,PatrolLeader:1b,patrol_target:[I;300,-60,0]}")
		sendCommand(t, pw, "summon minecraft:pillager 0.5 -60 4.5 {Patrolling:1b,patrol_target:[I;300,-60,0]}")
		pump(1500 * time.Millisecond)
		m := &move{}
		m.patrolling = patrolFlagQuery(t, obs, pw, pump)
		leaderTP := "tp Cap2 @e[type=minecraft:pillager,nbt={PatrolLeader:1b},limit=1]"
		m.lead0 = patrolQueryPos(t, obs, pw, pump, "data get entity @e[type=minecraft:pillager,nbt={PatrolLeader:1b},limit=1] Pos")
		m.mem0 = patrolQueryPoss(t, obs, pw, pump, "execute as @e[type=minecraft:pillager,nbt={PatrolLeader:0b}] run data get entity @s Pos")
		// The patrol marches ~4 blocks a second — the player re-anchors
		// to the leader every slice so removeWhenFarAway's 128-block
		// radius never closes on a straggler mid-measurement.
		for i := 0; i < 10; i++ {
			sendCommand(t, pw, leaderTP)
			pump(time.Second)
		}
		m.lead1 = patrolQueryPos(t, obs, pw, pump, "data get entity @e[type=minecraft:pillager,nbt={PatrolLeader:1b},limit=1] Pos")
		m.mem1 = patrolQueryPoss(t, obs, pw, pump, "execute as @e[type=minecraft:pillager,nbt={PatrolLeader:0b}] run data get entity @s Pos")
		if len(m.mem1) == 0 {
			// Debug dump: where are ALL the pillagers?
			all := patrolQueryPoss(t, obs, pw, pump, "execute as @e[type=minecraft:pillager] run data get entity @s Pos")
			t.Logf("leg pillager census: %v", all)
		}
		return m
	}

	legs := []*move{run(targets.vanilla), run(targets.mcgo)}
	for i, leg := range legs {
		dx := leg.lead1[0] - leg.lead0[0]
		dz := leg.lead1[2] - leg.lead0[2]
		t.Logf("leg %d leader %v -> %v (+%.1f, %+.1f)", i, leg.lead0, leg.lead1, dx, dz)
		for j, mm := range leg.mem1 {
			gap := math.Hypot(mm[0]-leg.lead1[0], mm[2]-leg.lead1[2])
			t.Logf("leg %d member %d at %v, %.1f from leader", i, j, mm, gap)
		}
		if !leg.patrolling {
			t.Fatalf("leg %d: leader's Patrolling NBT didn't round-trip", i)
		}
		if len(leg.mem1) != 1 {
			t.Fatalf("leg %d: member Pos replies %d, want 1", i, len(leg.mem1))
		}
		// The skewed waypoint puts the march east-positive with a
		// z-positive drift: ~0.21 blocks/tick × 200 ticks ≈ 42 blocks,
		// direction ~(0.96, 0.29). A wander fallback could match the
		// distance but not the heading.
		if dx < 15 || dz < 0 {
			t.Fatalf("leg %d: leader marched (+%.1f, %+.1f) in 10s, want east-skewed ≥15/≥0", i, dx, dz)
		}
		for j, mm := range leg.mem1 {
			gap := math.Hypot(mm[0]-leg.lead1[0], mm[2]-leg.lead1[2])
			if gap > 64 {
				t.Fatalf("leg %d: member %d ended %.1f blocks from the leader (left behind)", i, j, gap)
			}
		}
	}
}

// patrolFlagQuery reads the leader's Patrolling NBT byte out of a
// `data get` reply — the save-data round trip that keeps a summoned
// patrol marching.
func patrolFlagQuery(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration)) bool {
	t.Helper()
	start := len(obs.chat)
	sendCommand(t, pw, "data get entity @e[type=minecraft:pillager,nbt={PatrolLeader:1b},limit=1] Patrolling")
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(8 * time.Second)
	got := false
	idx := start
	for time.Now().Before(deadline) {
		pump(300 * time.Millisecond)
		end := false
		for ; idx < len(obs.chat); idx++ {
			line := obs.chat[idx]
			if strings.Contains(line, "data.entity.query") && strings.Contains(line, "1b") {
				got = true
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
	}
	return got
}

// patrolQueryPoss runs a Pos-producing command and parses every
// data.entity.query reply line's double list — execute-as forks one
// reply per member. A `say ZEND` sentinel marks the round.
func patrolQueryPoss(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration), cmd string) [][3]float64 {
	t.Helper()
	start := len(obs.chat)
	sendCommand(t, pw, cmd)
	sendCommand(t, pw, "say ZEND")
	var out [][3]float64
	var saw []string
	deadline := time.Now().Add(8 * time.Second)
	idx := start
	for time.Now().Before(deadline) {
		pump(300 * time.Millisecond)
		end := false
		for ; idx < len(obs.chat); idx++ {
			line := obs.chat[idx]
			saw = append(saw, line)
			if strings.Contains(line, "data.entity.query") {
				if p, ok := patrolPosList(line); ok {
					out = append(out, p)
				}
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
	}
	if len(out) == 0 {
		t.Logf("%q replies: %v", cmd, saw)
	}
	return out
}

// patrolQueryPos is patrolQueryPoss for a single-entity selector.
func patrolQueryPos(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration), cmd string) [3]float64 {
	t.Helper()
	got := patrolQueryPoss(t, obs, pw, pump, cmd)
	if len(got) != 1 {
		t.Fatalf("%q: %d Pos replies, want 1", cmd, len(got))
	}
	return got[0]
}

// patrolPosList reads the rendered SNBT double list out of a
// data.entity.query reply. mc-go's pretty-printer keeps the `[x, y, z]`
// delimiters; flattening vanilla's list component drops them, leaving
// the three `d`-suffixed doubles concatenated — parse either form.
var patrolNumRe = regexp.MustCompile(`(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)[df]`)

func patrolPosList(line string) ([3]float64, bool) {
	var out [3]float64
	if l := strings.Index(line, "["); l >= 0 {
		if r := strings.Index(line[l:], "]"); r > 0 {
			parts := strings.Split(line[l+1:l+r], ",")
			if len(parts) == 3 {
				ok := true
				for i, p := range parts {
					v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimRight(p, "bdfslL")), 64)
					if err != nil {
						ok = false
						break
					}
					out[i] = v
				}
				if ok {
					return out, true
				}
			}
		}
	}
	matches := patrolNumRe.FindAllStringSubmatch(line, -1)
	if len(matches) < 3 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(matches[len(matches)-3+i][1], 64)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return out, true
}
