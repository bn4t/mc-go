package blackbox

// TestNeutralProvokeParity pins the neutral-mob targeting gates:
// zombified_piglin (NeutralMob persistent anger) and iron_golem
// retaliate only when provoked; spiders hostily pick targets only
// under SpiderTargetGoal's light<0.5 gate — daylight calm, nighttime
// hostile — but always counter-attack whoever hurt them. Each leg
// watches P's own damage_event count: unprovoked waits must see
// zero, provoked waits must see at least one.
import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestNeutralProvokeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string

		join := func(night bool) (net.Conn, *protocol.Reader, *protocol.Writer) {
			c, pr, pw := joinPlayBlackbox(t, addr, "P")
			_ = c.SetDeadline(time.Now().Add(120 * time.Second))
			sendCommand(t, pw, "gamemode survival P")
			sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
			if night {
				sendCommand(t, pw, "time set night")
			} else {
				sendCommand(t, pw, "time set 6000")
			}
			sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			return c, pr, pw
		}

		// Provoke via /damage ... player_attack by @s: hurt-by-player
		// semantics identical to a punch, but no melee reach to whiff.
		provoke := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *paObs, mob string) {
			sendCommand(t, pw, fmt.Sprintf(
				"damage @e[type=minecraft:%s,limit=1] 1 minecraft:player_attack by @s", mob))
			paPump(t, c, pr, pw, obs, 700*time.Millisecond)
		}

		newObs := func() *paObs {
			return &paObs{
				health:   20,
				effects:  map[int32][2]int32{},
				effFlags: map[int32]byte{},
				events:   map[int32][]byte{},
				motion:   map[int32][]float64{},
				spawned:  map[int32]int32{},
			}
		}

		spawnAndTrack := func(t *testing.T, c net.Conn, pr *protocol.Reader,
			pw *protocol.Writer, obs *paObs, mob string) {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s ~2 ~ ~", mob))
			paPump(t, c, pr, pw, obs, 1200*time.Millisecond)
			ty, _ := server.EntityTypeID(mob)
			if obs.spawned[ty] == 0 {
				t.Fatalf("%s: %s never spawned", addr, mob)
			}
		}

		// Each neutral: noon, unprovoked — the mob must stay calm;
		// then one punch provokes it and the counter-attack lands.
		for _, mob := range []string{"zombified_piglin", "spider", "iron_golem"} {
			c, pr, pw := join(false)
			obs := newObs()
			spawnAndTrack(t, c, pr, pw, obs, mob)
			paPump(t, c, pr, pw, obs, 6*time.Second)
			calm := obs.dmgEvent == 0
			provoke(c, pr, pw, obs, mob)
			paPump(t, c, pr, pw, obs, 10*time.Second)
			out = append(out, fmt.Sprintf("%s calm=%v hit=%v",
				mob, calm, obs.dmgEvent > 0))
			c.Close()
		}

		// Night leg: a fresh unprovoked spider hunts P.
		c, pr, pw := join(true)
		obs := newObs()
		spawnAndTrack(t, c, pr, pw, obs, "spider")
		paPump(t, c, pr, pw, obs, 10*time.Second)
		out = append(out, fmt.Sprintf("spider_night hit=%v", obs.dmgEvent > 0))
		c.Close()

		// Shade leg: a noon spider standing deep under an opaque slab
		// is dark enough to hunt unprovoked (sky light falls ~1/block
		// in from the roof's edge — the spider sits ~12 blocks deep).
		c, pr, pw = join(false)
		obs = newObs()
		sendCommand(t, pw, "fill ~-14 ~4 ~-14 ~16 ~4 ~14 minecraft:stone")
		spawnAndTrack(t, c, pr, pw, obs, "spider")
		paPump(t, c, pr, pw, obs, 10*time.Second)
		out = append(out, fmt.Sprintf("spider_shade hit=%v", obs.dmgEvent > 0))
		sendCommand(t, pw, "fill ~-14 ~4 ~-14 ~16 ~4 ~14 minecraft:air")
		c.Close()

		// Herd alert: two piglins — hit the far one, the near one
		// must pick up the same anger and land its own swing (both
		// contribute to dmgEvent).
		c, pr, pw = join(false)
		obs = newObs()
		sendCommand(t, pw, "summon minecraft:zombified_piglin ~2 ~ ~")
		sendCommand(t, pw, "summon minecraft:zombified_piglin ~6 ~ ~")
		paPump(t, c, pr, pw, obs, 1200*time.Millisecond)
		provoke(c, pr, pw, obs, "zombified_piglin")
		paPump(t, c, pr, pw, obs, 10*time.Second)
		out = append(out, fmt.Sprintf("herd hit=%v", obs.dmgEvent > 0))
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
