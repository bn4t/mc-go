package blackbox

// TestSkeletonRangedParity pins RangedBowAttackGoal's wire shape for
// bow-armed AbstractSkeletons: the mob approaches to the 15-block
// attack radius, draws 20 ticks, and releases an arrow entity every
// attackInterval (40 normal). A bare-handed skeleton falls back to
// MeleeAttackGoal — swings, no arrows.
import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSkeletonRangedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	arrowID, ok := server.EntityTypeID("arrow")
	if !ok {
		t.Fatal("arrow entity type id missing")
	}

	run := func(addr string) []string {
		var out []string

		join := func() (net.Conn, *protocol.Reader, *protocol.Writer, *paObs) {
			c, pr, pw := joinPlayBlackbox(t, addr, "P")
			_ = c.SetDeadline(time.Now().Add(120 * time.Second))
			sendCommand(t, pw, "gamemode survival P")
			sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			sendCommand(t, pw, "time set night")
			return c, pr, pw, &paObs{
				health:   20,
				effects:  map[int32][2]int32{},
				effFlags: map[int32]byte{},
				events:   map[int32][]byte{},
				motion:   map[int32][]float64{},
				spawned:  map[int32]int32{},
			}
		}

		// Bow skeleton at 8 blocks: inside the 15-block attack radius
		// on arrival — draw starts once targeted, an arrow entity
		// flies at ~1s, and hits land while the draw/release cycle
		// repeats.
		c, pr, pw, obs := join()
		paPump(t, c, pr, pw, obs, 800*time.Millisecond)
		// Drop spawn-tracking state collected during join — stuck
		// arrows from earlier legs re-broadcast on re-track.
		obs.spawned = map[int32]int32{}
		sendCommand(t, pw, "summon minecraft:skeleton ~8 ~ ~ {equipment:{mainhand:{id:\"minecraft:bow\",count:1}}}")
		paPump(t, c, pr, pw, obs, 15*time.Second)
		out = append(out, fmt.Sprintf("bow arrows=%v hit=%v",
			obs.spawned[arrowID] != 0, obs.dmgEvent > 0))
		c.Close()

		// Stone-armed skeleton: holding a non-bow flips
		// reassessWeaponGoal to MeleeAttackGoal — hits, never fires.
		c, pr, pw, obs = join()
		paPump(t, c, pr, pw, obs, 800*time.Millisecond)
		sendCommand(t, pw, "kill @e[type=minecraft:arrow]")
		paPump(t, c, pr, pw, obs, 1200*time.Millisecond)
		obs.spawned = map[int32]int32{}
		sendCommand(t, pw, "summon minecraft:skeleton ~2 ~ ~ {equipment:{mainhand:{id:\"minecraft:stone_sword\",count:1}}}")
		paPump(t, c, pr, pw, obs, 12*time.Second)
		out = append(out, fmt.Sprintf("melee arrows=%v hit=%v",
			obs.spawned[arrowID] != 0, obs.dmgEvent > 0))
		c.Close()

		// Bare /summon: vanilla's finalizeSpawn runs
		// populateDefaultEquipmentSlots — the skeleton arrives
		// bow-armed and shoots.
		c, pr, pw, obs = join()
		paPump(t, c, pr, pw, obs, 800*time.Millisecond)
		sendCommand(t, pw, "kill @e[type=minecraft:arrow]")
		paPump(t, c, pr, pw, obs, 1200*time.Millisecond)
		obs.spawned = map[int32]int32{}
		sendCommand(t, pw, "summon minecraft:skeleton ~8 ~ ~")
		paPump(t, c, pr, pw, obs, 15*time.Second)
		out = append(out, fmt.Sprintf("bare arrows=%v hit=%v",
			obs.spawned[arrowID] != 0, obs.dmgEvent > 0))
		c.Close()

		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range van {
		t.Logf("leg %d vanilla: %s", i, van[i])
		t.Logf("leg %d mc-go:   %s", i, mc[i])
	}
	if len(van) != len(mc) {
		t.Fatalf("leg count %d vs %d", len(van), len(mc))
	}
	for i := range van {
		if van[i] != mc[i] {
			t.Errorf("leg %d mismatch: vanilla %q mc-go %q", i, van[i], mc[i])
		}
	}
}
