package blackbox

import (
	"strings"
	"testing"
	"time"
)

// Entity.removePassenger arms a 60-tick boardingCooldown on the
// departing rider; canRide refuses a startRiding while it's still
// positive, so a just-dismounted mob can't be re-scooped for three
// seconds. Pinned on a powered-rail loop: a moving plain minecart
// scoops the pig on its first lap, `/ride dismount` drops it back on
// the track, and further laps inside the 60-tick window must NOT
// re-mount. set_passengers (0x6B) broadcasts only on list changes —
// mount adds one packet, dismount adds one — so the per-phase packet
// counts tell mounts from a bare dismount.
func TestBoardingCooldownParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		mountsFirst   int
		mountsCoolWin int
		mountsAfter   int
	}

	count6B := func(s *chatSink) int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.ids[0x6B]
	}

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 30.5 -60 44.5")
		// Plain-rail loop: normal rail friction keeps a cart above the
		// 0.1 scoop speed for hundreds of ticks, so no power source
		// is needed at all. Corners get explicit curve shapes.
		sendCommand(t, pw, "fill 36 -61 36 52 -61 52 minecraft:stone")
		sendCommand(t, pw, "fill 41 -60 40 47 -60 40 minecraft:rail[shape=east_west]")
		sendCommand(t, pw, "fill 41 -60 48 47 -60 48 minecraft:rail[shape=east_west]")
		sendCommand(t, pw, "fill 40 -60 41 40 -60 47 minecraft:rail[shape=north_south]")
		sendCommand(t, pw, "fill 48 -60 41 48 -60 47 minecraft:rail[shape=north_south]")
		sendCommand(t, pw, "setblock 40 -60 40 minecraft:rail[shape=south_east]")
		sendCommand(t, pw, "setblock 48 -60 40 minecraft:rail[shape=south_west]")
		sendCommand(t, pw, "setblock 40 -60 48 minecraft:rail[shape=north_east]")
		sendCommand(t, pw, "setblock 48 -60 48 minecraft:rail[shape=north_west]")
		sendCommand(t, pw, "summon minecraft:pig 44.5 -60 40.5 {NoAI:1b}")
		sendCommand(t, pw, "execute if entity @e[type=minecraft:pig] run say PIGFOUND")
		sendCommand(t, pw, "summon minecraft:minecart 40.5 -60 44.5 {Motion:[0.0,0.0,-0.45]}")

		// Lap one scoops the pig off the rail — one mount packet.
		time.Sleep(4 * time.Second)
		r.mountsFirst = count6B(sink)
		if r.mountsFirst == 0 {
			sawPig := false
			for _, line := range sink.take() {
				if strings.Contains(line, "PIGFOUND") {
					sawPig = true
				}
			}
			t.Logf("%s pigFound=%v cart: %s", addr, sawPig,
				dataGetAll(t, sink, pw, "@e[type=minecraft:minecart,limit=1]"))
		}

		// Dismount + 25 ticks inside the cooldown: exactly one new
		// set_passengers (the dismount) and nothing else.
		sendCommand(t, pw, "ride @e[type=minecraft:pig,limit=1] dismount")
		sendCommand(t, pw, "tick sprint 25")
		time.Sleep(1500 * time.Millisecond)
		before := count6B(sink)
		r.mountsCoolWin = before - r.mountsFirst - 1

		// Past the 60-tick cooldown a later lap scoops again. The
		// cooldown-window laps drained the cart's momentum on pig
		// pushes, so swap in a fresh fast cart and put the pig back
		// on the north edge.
		sendCommand(t, pw, "kill @e[type=minecraft:minecart]")
		sendCommand(t, pw, "tp @e[type=minecraft:pig,limit=1] 44.5 -60 40.5")
		sendCommand(t, pw, "summon minecraft:minecart 40.5 -60 44.5 {Motion:[0.0,0.0,-0.45]}")
		sendCommand(t, pw, "tick sprint 110")
		time.Sleep(2 * time.Second)
		r.mountsAfter = count6B(sink) - before
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if r.mountsFirst < 1 {
			t.Errorf("%s: pig was never scooped by the moving cart", name)
			continue
		}
		if r.mountsCoolWin > 0 {
			t.Errorf("%s: pig re-mounted inside the 60-tick boardingCooldown", name)
		}
		if r.mountsAfter < 1 {
			t.Errorf("%s: pig never re-mounted after the cooldown expired", name)
		}
	}
}
