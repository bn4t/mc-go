//go:build blackbox

package blackbox

// TestPatrolParity drives PatrolSpawner's natural spawn cycle on both
// servers. The countdown is 12000+rand(1200) ticks with a 1-in-5 spawn
// roll each cycle, so wall-clock waiting is hopeless — /tick rate
// burns the countdown fast while a re-pinned `time set 122000` every
// poll keeps isBrightOutside true and effectiveDifficulty at its
// fresh-world 1.5. (advance_time must stay ON: ServerChunkCache only
// calls tickCustomSpawners while tickTime is set — and the spawn_mobs
// gamerule gates the whole customSpawners pass, so it stays ON too;
// the_void biome's empty spawn lists keep the natural cycle quiet
// anyway.) Vanilla's custom spawners also need spawn-enemies on, so
// the vanilla leg overrides spawn-monsters=true.
//
// Compared per leg: the patrol size (ceil(effectiveDifficulty)+1 = 3
// on a fresh NORMAL world), exactly one leader carrying the ominous
// head patch [9 12 18 72], the other members' heads empty, and every
// member wearing the finalizeSpawn crossbow in mainhand.

import (
	"sort"
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

func TestPatrolParity(t *testing.T) {
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

	pilType, _ := server.EntityTypeID("pillager")

	run := func(addr string) *patrolObs {
		c, pr, pw := joinPlayBlackbox(t, addr, "Cap2")
		defer c.Close()
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
			case server.IDPlaySystemChatCB:
				obs.chat = append(obs.chat, chatKey(p.Data))
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

		pump(1200 * time.Millisecond)
		sendCommand(t, pw, "gamerule minecraft:advance_weather false")
		// Phantom + wandering-trader spawners tick alongside the patrol
		// one; off, so a phantom pack can't pollute the spawn window.
		sendCommand(t, pw, "gamerule minecraft:spawn_phantoms false")
		sendCommand(t, pw, "gamerule minecraft:spawn_wandering_traders false")
		// randomTickSpeed 0 keeps the accelerated tick cheap — 17ms of
		// random-tick work per tick would cap mc-go near 60 tps.
		sendCommand(t, pw, "gamerule minecraft:random_tick_speed 0")
		sendCommand(t, pw, "weather clear")
		sendCommand(t, pw, "difficulty normal")
		sendCommand(t, pw, "time set 122000")
		sendCommand(t, pw, "tick rate 5000")
		pump(600 * time.Millisecond)

		// Each rearmed cycle costs 12000+rand(1200) ticks ≈ 2.5s at
		// 5000 tps; the 20% roll means ~5 cycles on average. `time set
		// 1000` every poll keeps the day pinned to morning — the day
		// counter would otherwise advance ~31 days over the window and
		// the inhabited/moon terms could push the count to 4.
		deadline := time.Now().Add(240 * time.Second)
		for len(obs.pillagers) == 0 && time.Now().Before(deadline) {
			sendCommand(t, pw, "time set 122000")
			pump(time.Second)
		}
		if len(obs.pillagers) == 0 {
			t.Fatalf("no patrol spawned (chat %v)", obs.chat)
		}
		// The whole group lands in one tick; two extra seconds catches
		// every member's set_equipment.
		pump(2500 * time.Millisecond)
		sendCommand(t, pw, "tick rate 20")
		return obs
	}

	legs := []*patrolObs{run(targets.vanilla), run(targets.mcgo)}
	wantComps := []int32{9, 12, 18, 72}
	for i, leg := range legs {
		n := len(leg.pillagers)
		if n != 3 {
			t.Fatalf("leg %d: patrol size = %d, want ceil(1.5)+1=3", i, n)
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
