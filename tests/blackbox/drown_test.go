package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestMobDrownParity: LivingEntity.baseTick's air block — an eye in
// water (not a bubble column) on a mob that can't breathe under water
// drains airSupply one per tick; at -20 it resets to zero, broadcasts
// entity event 67 and takes 2 drown damage. Measured on the 26.2 jar:
// decreaseAirSupply's oxygen_bonus branch never fires for mobs, so the
// drain is a flat -1/tick and the first hit lands ~320 ticks in (300
// down to -20), repeating every 20 ticks until death.
//
// A swimming pig does NOT drown — FloatGoal's surface bob keeps the
// eye (~0.77 above the feet) past the fluid surface at the ~0.4 bob
// depth. The fixture therefore summons a NoAI pig (no goals → sinks to
// the column bed and stays submerged) inside a 5-deep walled pool.
func TestMobDrownParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("DR")}, nil)
	defer targets.cleanup()

	type read struct {
		dump   string
		air    int64
		health float64
	}
	mid := map[string]read{}
	end := map[string]read{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "DR")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			for _, cmd := range []string{
				"fill 9 -61 9 11 -54 9 minecraft:stone",
				"fill 9 -61 11 11 -54 11 minecraft:stone",
				"fill 9 -61 9 9 -54 11 minecraft:stone",
				"fill 11 -61 9 11 -54 11 minecraft:stone",
				// 5-deep column: cells -60..-56.
				"fill 10 -60 10 10 -56 10 minecraft:water",
			} {
				sendCommand(t, pw, cmd)
			}
			// NoAI pig inside the column — sinks to the bed and its
			// eye (-59.2) sits in the bottom water cell from then on.
			cmd := `summon minecraft:pig 10.5 -56.5 10.5 {NoAI:1b}`
			for attempt := 0; attempt < 5; attempt++ {
				sendCommand(t, pw, cmd)
				time.Sleep(300 * time.Millisecond)
				if d := dataGetAll(t, sink, pw,
					"@e[type=minecraft:pig,limit=1,sort=nearest]"); d != "" {
					break
				}
				if attempt == 4 {
					t.Fatalf("%s: pig summon never landed", s.label)
				}
			}
			readNow := func() read {
				d := dataGetAll(t, sink, pw,
					"@e[type=minecraft:pig,limit=1,sort=nearest]")
				if d == "" {
					return read{}
				}
				var r read
				r.dump = d
				r.air, _ = snbtInt(t, d, "Air")
				r.health, _ = snbtFloat(t, d, "Health")
				return r
			}
			// ~100 submerged ticks: 300 → ~200.
			sendCommand(t, pw, "tick sprint 100")
			time.Sleep(1200 * time.Millisecond)
			mid[s.label] = readNow()
			// Just past the first drown window (~320 ticks): air cycles
			// in (-20,0] and the first hits have landed — but not the
			// ~6 needed to kill a 10-health pig.
			sendCommand(t, pw, "tick sprint 200")
			time.Sleep(1500 * time.Millisecond)
			end[s.label] = readNow()
			// 10-health pig, 2 damage per 20 ticks: ~280 more ticks
			// covers the remaining hits on both servers.
			sendCommand(t, pw, "tick sprint 280")
			time.Sleep(1500 * time.Millisecond)
			if d := dataGetAll(t, sink, pw,
				"@e[type=minecraft:pig,limit=1,sort=nearest]"); d != "" {
				t.Errorf("%s: pig still alive after drowning window", s.label)
			}
		})
	}
	// Mid-read: ~100 ticks of flat drain — wide band for the real-time
	// ticks between the sprint and the data get.
	for _, c := range []struct {
		label string
		r     read
	}{{"vanilla", mid["vanilla"]}, {"mcgo", mid["mcgo"]}} {
		if c.r.dump == "" {
			t.Fatalf("%s: pig unreadable mid-window", c.label)
		}
		if c.r.air < 150 || c.r.air > 250 {
			t.Errorf("%s: Air %d outside drain band [150,250] after ~100 ticks", c.label, c.r.air)
		}
	}
	if d := math.Abs(float64(mid["vanilla"].air - mid["mcgo"].air)); d > 40 {
		t.Errorf("Air divergence vanilla %d vs mc-go %d (>40)", mid["vanilla"].air, mid["mcgo"].air)
	}
	// End-read: ~350 ticks in the first drown hits landed — air is
	// in the reset cycle and health has dropped below the 10 start.
	for _, c := range []struct {
		label string
		r     read
	}{{"vanilla", end["vanilla"]}, {"mcgo", end["mcgo"]}} {
		if c.r.dump == "" {
			t.Fatalf("%s: pig gone before the drown window", c.label)
		}
		if c.r.air > 0 || c.r.air < -20 {
			t.Errorf("%s: Air %d outside reset band [-20,0]", c.label, c.r.air)
		}
		if c.r.health >= 10 {
			t.Errorf("%s: Health %.1f never took drown damage", c.label, c.r.health)
		}
	}
}

// TestAirSupplyRestoreParity: the same block's else arm — an eye out
// of water regenerates airSupply by increaseAirSupply's +4 per tick up
// to getMaxAirSupply (measured on the 26.2 jar: a merged Air:100 on a
// pig on land read back 148 after ~12 ticks, capped at 300 by ~50).
func TestAirSupplyRestoreParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("AR")}, nil)
	defer targets.cleanup()

	air := map[string]int64{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "AR")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			cmd := `summon minecraft:pig 10.5 -60 10.5 {NoAI:1b}`
			for attempt := 0; attempt < 5; attempt++ {
				sendCommand(t, pw, cmd)
				time.Sleep(300 * time.Millisecond)
				if d := dataGetAll(t, sink, pw,
					"@e[type=minecraft:pig,limit=1,sort=nearest]"); d != "" {
					break
				}
				if attempt == 4 {
					t.Fatalf("%s: pig summon never landed", s.label)
				}
			}
			sendCommand(t, pw,
				"data merge entity @e[type=minecraft:pig,limit=1,sort=nearest] {Air:100s}")
			time.Sleep(600 * time.Millisecond)
			d := dataGetAll(t, sink, pw,
				"@e[type=minecraft:pig,limit=1,sort=nearest]")
			if d == "" {
				t.Fatalf("%s: pig unreadable after merge", s.label)
			}
			a, ok := snbtInt(t, d, "Air")
			if !ok {
				t.Fatalf("%s: Air unreadable after merge: %q", s.label, d)
			}
			air[s.label] = a
		})
	}
	// Dry land: the else arm regenerates +4/tick from the merged 100 —
	// each leg reads >100 inside ~50 ticks and never past the 300 cap.
	for _, c := range []struct {
		label string
		a     int64
	}{{"vanilla", air["vanilla"]}, {"mcgo", air["mcgo"]}} {
		if c.a <= 100 || c.a > 300 {
			t.Errorf("%s: Air %d after merge on land outside regen band (100,300]", c.label, c.a)
		}
	}
}
