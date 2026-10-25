package blackbox

// TestDespawnParity pins Mob.checkDespawn's three discard paths on
// both servers with summoned mobs on the flat world:
//
//   - distance: a mob past its category despawnDistance (128; the
//     129..143-block band is ticked at sim-distance 8) discards at
//     once — but Animals (removeWhenFarAway=false), persistence, and
//     passengers/leads exempt.
//   - idle: past the 32-block noDespawnDistance and 600 ticks of
//     noActionTime, each tick is a 1-in-800 discard roll — a 6000-tick
//     sprint makes that ~99.9% certain.
//   - peaceful: difficulty=peaceful discards every
//     not-allowedInPeaceful type on the spot, persistence or not.
//
// Player stays in creative the whole test: it anchors the despawn
// rules without drawing hostile targets.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// despawnCount returns the number of live entities matching a selector,
// measured via execute-as + `data get ... Pos` reply fan-out.
func despawnCount(t *testing.T, obs *patrolObs, pw *protocol.Writer, pump func(time.Duration), sel string) int {
	t.Helper()
	return len(patrolQueryPoss(t, obs, pw, pump, "execute as "+sel+" run data get entity @s Pos"))
}

func TestDespawnParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Cap2"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) {
		obs, pump, pw, done := patrolLeg(t, addr)
		defer done()
		patrolSetup(t, pw, pump)
		sendCommand(t, pw, "gamemode creative Cap2")
		// Night: zombies on the open flat world burn to death in
		// daylight — a 6000-tick sprint is ~300s of fire.
		sendCommand(t, pw, "time set 13000")
		sendCommand(t, pw, "tp Cap2 0.5 -60 0.5")
		pump(400 * time.Millisecond)

		// The cast: a plain zombie (despawns), a persistent zombie
		// (exempt from distance/idle but not the peaceful purge), and
		// a pig (Animal.removeWhenFarAway=false — never despawns).
		sendCommand(t, pw, "summon minecraft:zombie 4.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:zombie 8.5 -60 0.5 {PersistenceRequired:1b}")
		sendCommand(t, pw, "summon minecraft:pig 4.5 -60 6.5")
		pump(1500 * time.Millisecond)
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie]"); n != 2 {
			t.Fatalf("post-summon zombies = %d, want 2", n)
		}
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:pig]"); n != 1 {
			t.Fatalf("post-summon pigs = %d, want 1", n)
		}

		// Distance discard: hop 140 east — inside the sim-8 ticking
		// window (chunk dist 8, ~136 blocks), past the 128 threshold.
		sendCommand(t, pw, "tp Cap2 140.5 -60 0.5")
		pump(2 * time.Second)
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie]"); n != 1 {
			t.Fatalf("after >128 hop zombies = %d, want 1 (persistent only)", n)
		}
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:pig]"); n != 1 {
			t.Fatalf("after >128 hop pigs = %d, want 1 (animal exempt)", n)
		}

		// Idle discard: a zombie 56 blocks out (>32, still ticked)
		// burns a 6000-tick sprint — past the 600-tick idle warmup each
		// tick is a 1-in-800 roll, ~99.9% to land.
		sendCommand(t, pw, "summon minecraft:zombie 196.5 -60 0.5")
		pump(500 * time.Millisecond)
		sendCommand(t, pw, "tick sprint 6000")
		deadline := time.Now().Add(180 * time.Second)
		gone := false
		for time.Now().Before(deadline) && !gone {
			pump(3 * time.Second)
			if despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie,nbt={PersistenceRequired:0b}]") == 0 {
				gone = true
			}
		}
		sendCommand(t, pw, "tick sprint stop")
		pump(500 * time.Millisecond)
		if !gone {
			// One last look after the sprint backlog flushed.
			if despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie,nbt={PersistenceRequired:0b}]") != 0 {
				t.Fatalf("idle zombie survived 6000 sprinted ticks at >32 blocks")
			}
		}
		// The persistent zombie and pig live on.
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie]"); n != 1 {
			t.Fatalf("post-sprint zombies = %d, want 1 (persistent only)", n)
		}
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:pig]"); n != 1 {
			t.Fatalf("post-sprint pigs = %d, want 1", n)
		}

		// Peaceful purge: every non-allowedInPeaceful type discards —
		// persistenceRequired doesn't save the persistent zombie.
		sendCommand(t, pw, "difficulty peaceful")
		pump(2 * time.Second)
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:zombie]"); n != 0 {
			t.Fatalf("peaceful zombies = %d, want 0 (purge beats persistence)", n)
		}
		if n := despawnCount(t, obs, pw, pump, "@e[type=minecraft:pig]"); n != 1 {
			t.Fatalf("peaceful pigs = %d, want 1", n)
		}
		sendCommand(t, pw, "difficulty normal")
	}

	run(targets.vanilla)
	run(targets.mcgo)
}
