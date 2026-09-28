package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// TestSuffocateParity: LivingEntity.baseTick's isAlive block opens
// with the suffocation arm — Entity.isInWall probes a 1e-6-tall plane
// centred on the eye position (width*0.8 across) for a non-air cell
// that isSuffocating and whose collision shape meets the plane, then
// lands a 1.0 in_wall hit every tick. The twenty-tick invulnerability
// window inside hurt thins that to one accepted hit per ~10 ticks —
// a 10-health pig dies around tick 91, a 20-health villager around 191.
// Measured on the 26.2 jar (LivingEntity.baseTick bytecode + a
// frozen/stepped probe): hits land on a ten-tick cadence, NOT every
// tick and NOT every twenty.
//
// The glass lane pins the isSuffocating gate itself: a pig whose
// head sits inside transparent glass never suffocates. The 20-health
// lane uses a villager — passive, never burns, and big enough (eye
// 1.66) to pin a second eye height.
//
// Vanilla's /summon refuses a spot where the entity's box collides,
// so the mobs stand in open air and the fills bury them after.
func TestSuffocateParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("SF")}, nil)
	defer targets.cleanup()

	type healthRead struct {
		dump   string
		health float64
		gone   bool
	}
	reads := map[string][]healthRead{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "SF")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			// Clear the lanes before the summons — vanilla's
			// /summon refuses a spot where the entity's box
			// collides, and the plain worldgen floor lives here.
			sendCommand(t, pw, "fill 8 -62 8 17 -56 17 minecraft:air")
			// Summon in the open shaft FIRST so each mob joins the
			// ticklist on a normal tick (a summon under freeze joins
			// ~9 ticks late, drowning the hit-cadence comparison),
			// then freeze, then bury — nothing suffocates until a
			// cell covers the eye plane, so the few real ticks in
			// between are harmless.
			summonAndWait := func(cmd, sel string) {
				for attempt := 0; attempt < 5; attempt++ {
					sendCommand(t, pw, cmd)
					time.Sleep(300 * time.Millisecond)
					if d := dataGetAll(t, sink, pw, sel); d != "" {
						return
					}
				}
				t.Fatalf("%s: summon never landed: %s", s.label, cmd)
			}
			summonAndWait(`summon minecraft:pig 10.5 -58.5 10.5 {NoAI:1b}`,
				"@e[type=minecraft:pig,x=8,y=-62,z=8,dx=4,dy=7,dz=4,limit=1]")
			summonAndWait(`summon minecraft:villager 15.5 -58.5 10.5 {NoAI:1b}`,
				"@e[type=minecraft:villager,x=13,y=-62,z=8,dx=4,dy=7,dz=4,limit=1]")
			summonAndWait(`summon minecraft:pig 10.5 -58.5 15.5 {NoAI:1b}`,
				"@e[type=minecraft:pig,x=8,y=-62,z=13,dx=4,dy=7,dz=4,limit=1]")
			sendCommand(t, pw, "tick freeze")
			time.Sleep(300 * time.Millisecond)
			for _, cmd := range []string{
				// Lane A: stone box around the pig — its eye cell
				// ends up stone wherever it settled in the shaft.
				"fill 9 -62 9 11 -58 11 minecraft:stone",
				// Lane B: deeper box around the villager — eye
				// (~feet+1.66) lands in cells -62..-57.
				"fill 14 -62 9 16 -57 11 minecraft:stone",
				// Lane C: glass box — transparent, not isSuffocating.
				"fill 9 -62 14 11 -58 16 minecraft:glass",
			} {
				sendCommand(t, pw, cmd)
			}
			read := func(sel string) healthRead {
				d := dataGetAll(t, sink, pw, sel)
				if d == "" {
					return healthRead{gone: true}
				}
				h, _ := snbtFloat(t, d, "Health")
				return healthRead{dump: d, health: h}
			}
			pig := "@e[type=minecraft:pig,x=8,y=-60,z=8,dx=4,dy=4,dz=4,limit=1]"
			vil := "@e[type=minecraft:villager,x=13,y=-60,z=8,dx=4,dy=4,dz=4,limit=1]"
			glass := "@e[type=minecraft:pig,x=8,y=-60,z=13,dx=4,dy=4,dz=4,limit=1]"
			var out []healthRead
			// A stepped tick still runs at the server's ~20tps — the
			// reads must wait the real-time span or they land
			// mid-window.
			step := func(n int) {
				sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
				time.Sleep(time.Duration(n*55+700) * time.Millisecond)
			}
			// Checkpoint 25: hits at ~1,11,21 → pig 7, villager 17.
			step(25)
			out = append(out, read(pig), read(vil), read(glass))
			// Checkpoint 55: six hits → pig 4, villager 14.
			step(30)
			out = append(out, read(pig), read(vil), read(glass))
			// Checkpoint 115: pig dead (hit 10 ~t91, linger ~20 →
			// gone or Health 0), villager at 12 hits → 8.
			step(60)
			out = append(out, read(pig), read(vil), read(glass))
			// Checkpoint 215: villager dead too (hit 20 ~t191 + linger).
			step(100)
			out = append(out, read(pig), read(vil), read(glass))
			reads[s.label] = out
		})
	}
	// Each leg's mob falls a different distance before the freeze
	// lands, so absolute hit counts carry a several-tick offset —
	// what pins parity is the ten-tick cadence and the dead/alive
	// state, not the offset. The villager (alive through cp115)
	// gives the cadence directly: health lost between checkpoints is
	// the hit count times 1.0, independent of when the lane started.
	deadOf := func(r healthRead) bool { return r.gone || r.health <= 0 }
	lane := func(sv string, cp, l int) healthRead { return reads[sv][cp*3+l] }
	// Cadence pin (villager only — the pig dies mid-window):
	// cp0→cp1 is 30 ticks ≈ 3 hits, cp1→cp2 is 60 ≈ 6, phase edge
	// shaves one at a boundary.
	for _, sv := range []string{"vanilla", "mcgo"} {
		for _, w := range []struct {
			a, b int
			want float64
		}{{0, 1, 3}, {1, 2, 6}} {
			a, b := lane(sv, w.a, 1), lane(sv, w.b, 1)
			if deadOf(a) || deadOf(b) {
				t.Fatalf("%s villager died early — cp%d=%.1f cp%d=%.1f", sv, w.a, a.health, w.b, b.health)
			}
			if d := a.health - b.health; d < w.want-1 || d > w.want+1 {
				t.Fatalf("%s villager cadence off: cp%d→cp%d dropped %.1f, want ~%.0f (10-tick invuln cadence)", sv, w.a, w.b, d, w.want)
			}
		}
	}
	// Dead/alive parity at every checkpoint: the pig survives the
	// first two (a per-tick impl would be gone by cp1) and dies by
	// cp115; the villager survives three and dies by cp215; the
	// glass pig is never touched.
	for cp := 0; cp < 4; cp++ {
		for l := 0; l < 3; l++ {
			v, m := lane("vanilla", cp, l), lane("mcgo", cp, l)
			if deadOf(v) != deadOf(m) {
				t.Fatalf("cp%d lane%d: dead state differs — vanilla Health %.1f gone=%v, mc-go %.1f gone=%v", cp, l, v.health, v.gone, m.health, m.gone)
			}
		}
	}
	for cp := 0; cp < 2; cp++ {
		if deadOf(lane("vanilla", cp, 0)) || deadOf(lane("mcgo", cp, 0)) {
			t.Fatalf("pig dead by checkpoint %d — a per-tick hurt, not the invuln cadence", cp)
		}
	}
	for cp := 0; cp < 3; cp++ {
		if deadOf(lane("vanilla", cp, 1)) || deadOf(lane("mcgo", cp, 1)) {
			t.Fatalf("villager dead by checkpoint %d — a per-tick hurt, not the invuln cadence", cp)
		}
	}
	if !deadOf(reads["vanilla"][6]) || !deadOf(reads["mcgo"][6]) {
		t.Fatalf("pig survived the suffocation window")
	}
	if !deadOf(reads["vanilla"][9]) || !deadOf(reads["mcgo"][9]) {
		t.Fatalf("pig still registered at checkpoint 3")
	}
	if !deadOf(reads["vanilla"][10]) || !deadOf(reads["mcgo"][10]) {
		t.Fatalf("villager survived the suffocation window")
	}
	for i := 2; i < 12; i += 3 {
		v, m := reads["vanilla"][i], reads["mcgo"][i]
		if v.health != 10 || m.health != 10 {
			t.Fatalf("glass-pig took damage — vanilla %.1f mc-go %.1f, want 10", v.health, m.health)
		}
	}
}
