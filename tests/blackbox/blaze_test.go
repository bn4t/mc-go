package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestBlazeAttackParity pins Blaze$BlazeAttackGoal and the SmallFireball
// it shoots (javap-measured, see internal/server/blaze.go):
//
//   - Burst leg: a survival player eight blocks out under a low ceiling
//     (the blaze can't float up) draws the charge/burst cycle —
//     DATA_FLAGS_ID (index 16, byte) bit 0 on, 60 ticks later three
//     small_fireball add_entity packets 6 ticks apart, each preceded by
//     level event 1018 at the blaze's block, the flag off 6 ticks after
//     the third, and the next charge 100 ticks after that. Each ball
//     carries the blaze's id as add_entity data, spawns at the blaze's
//     x/z and 1.4 above its feet, and flies at |delta| = 0.1.
//   - Melee leg: inside two blocks the goal swings doHurtTarget
//     instead — the first hit lands attack_damage 6 (20 → 14).
//   - Fireball leg: an ownerless small fireball flown into the player
//     lands SmallFireball.onHitEntity's 5.0 (20 → 15) and sets the
//     player burning (shared flags bit 0).
func TestBlazeAttackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	blazeType, _ := server.EntityTypeID("blaze")
	fbType, _ := server.EntityTypeID("small_fireball")

	type shot struct {
		at        time.Time
		dx, dy    float64 // spawn offset from the tracked blaze (dx = horizontal)
		speed     float64
		ownerOK   bool
		levelEvAt bool
	}
	type result struct {
		chargeOn  []time.Time
		chargeOff []time.Time
		shots     []shot
		events    int
		melee     float32
		fireball  float32
		burning   bool
	}

	run := func(addr string) result {
		var r result
		r.melee, r.fireball = -1, -1
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		fw := startFlyerWire(t, c, pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "tp P 0.5 -60 0.5 -90 0")
		sendCommand(t, pw, "fill 4 -58 -3 12 -58 3 minecraft:stone")
		time.Sleep(800 * time.Millisecond)

		// Burst leg.
		start := fw.mark()
		sendCommand(t, pw, "summon minecraft:blaze 8.5 -60 0.5")
		time.Sleep(1500 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival P")
		// Keep the target alive through the bursts: a second-by-second
		// instant_health top-up (the hits land, the player never dies).
		for i := 0; i < 14; i++ {
			sendCommand(t, pw, "effect give P minecraft:instant_health 1 10 true")
			time.Sleep(time.Second)
		}
		sendCommand(t, pw, "gamemode creative P")
		tr := newFwTracker()
		var blazeEID int32 = -1
		for _, p := range fw.since(0)[:start] {
			tr.feed(p)
		}
		for _, p := range fw.since(start) {
			switch p.id {
			case server.IDPlaySpawnEntity:
				s, ok := fwDecodeSpawn(p)
				if !ok {
					break
				}
				if s.typ == blazeType && blazeEID < 0 {
					blazeEID = s.eid
				}
				if s.typ == fbType && blazeEID >= 0 {
					bp := tr.pos[blazeEID]
					r.shots = append(r.shots, shot{
						at:      p.at,
						dx:      math.Hypot(s.x-bp[0], s.z-bp[2]),
						dy:      s.y - bp[1],
						speed:   fwLen(s.vx, s.vy, s.vz),
						ownerOK: s.data == blazeEID,
					})
				}
			case server.IDPlayEntityMetadata:
				eid, ents := fwDecodeMeta(p.data)
				if eid != blazeEID {
					break
				}
				for _, e := range ents {
					if e.idx == 16 && e.typ == 0 {
						on := e.i&1 != 0
						if on && (len(r.chargeOn) == len(r.chargeOff)) {
							r.chargeOn = append(r.chargeOn, p.at)
						} else if !on && len(r.chargeOn) > len(r.chargeOff) {
							r.chargeOff = append(r.chargeOff, p.at)
						}
					}
				}
			case server.IDPlayLevelEvent:
				if ev, _, _, _, ok := fwLevelEvent(p); ok && ev == 1018 {
					r.events++
				}
			}
			tr.feed(p)
		}

		// Melee leg.
		sendCommand(t, pw, "kill @e[type=minecraft:blaze]")
		sendCommand(t, pw, "kill @e[type=minecraft:small_fireball]")
		// Let the last ball's five-second burn run out, then top up.
		for i := 0; i < 6; i++ {
			sendCommand(t, pw, "effect give P minecraft:instant_health 1 10 true")
			time.Sleep(time.Second)
		}
		sendCommand(t, pw, "summon minecraft:blaze 1.7 -60 0.5")
		time.Sleep(1200 * time.Millisecond)
		m := fw.mark()
		sendCommand(t, pw, "gamemode survival P")
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) && r.melee < 0 {
			time.Sleep(100 * time.Millisecond)
			for _, p := range fw.since(m) {
				if p.id == server.IDPlaySetHealth {
					if h, ok := fwHealth(p); ok && h < 20 {
						r.melee = h
						break
					}
				}
			}
		}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=minecraft:blaze]")

		// Fireball leg: a fresh spot, full health, no effects.
		sendCommand(t, pw, "effect clear P")
		sendCommand(t, pw, "tp P 30.5 -60 0.5 180 0")
		time.Sleep(600 * time.Millisecond)
		sendCommand(t, pw, "effect give P minecraft:instant_health 1 10 true")
		time.Sleep(600 * time.Millisecond)
		m = fw.mark()
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "summon minecraft:small_fireball 30.5 -59 4.5 {Motion:[0.0d,0.0d,-0.3d]}")
		deadline = time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && (r.fireball < 0 || !r.burning) {
			time.Sleep(100 * time.Millisecond)
			for _, p := range fw.since(m) {
				switch p.id {
				case server.IDPlaySetHealth:
					if h, ok := fwHealth(p); ok && h < 20 && r.fireball < 0 {
						r.fireball = h
					}
				case server.IDPlayEntityMetadata:
					_, ents := fwDecodeMeta(p.data)
					for _, e := range ents {
						if e.idx == 0 && e.typ == 0 && e.i&1 != 0 {
							r.burning = true
						}
					}
				}
			}
		}
		sendCommand(t, pw, "gamemode creative P")
		return r
	}

	check := func(name string, r result) {
		t.Logf("%s: chargeOn=%d chargeOff=%d shots=%d events=%d melee=%.1f fireball=%.1f burning=%v",
			name, len(r.chargeOn), len(r.chargeOff), len(r.shots), r.events, r.melee, r.fireball, r.burning)
		for i, s := range r.shots {
			t.Logf("%s shot %d: t=%s dx=%.3f dy=%.3f |v|=%.4f owner=%v", name, i, s.at.Format("05.000"), s.dx, s.dy, s.speed, s.ownerOK)
		}
		for i := range r.chargeOn {
			off := "-"
			if i < len(r.chargeOff) {
				off = r.chargeOff[i].Format("05.000")
			}
			t.Logf("%s charge %d: on=%s off=%s", name, i, r.chargeOn[i].Format("05.000"), off)
		}
		if len(r.chargeOn) == 0 || len(r.chargeOff) == 0 {
			t.Errorf("%s: no complete charge cycle on DATA_FLAGS_ID", name)
			return
		}
		on, off := r.chargeOn[0], r.chargeOff[0]
		var burst []shot
		for _, s := range r.shots {
			if s.at.After(on) && s.at.Before(off) {
				burst = append(burst, s)
			}
		}
		if len(burst) != 3 {
			t.Errorf("%s: %d fireballs inside the first charge window, want 3", name, len(burst))
		}
		if r.events != len(r.shots) {
			t.Errorf("%s: %d level events 1018 for %d fireballs", name, r.events, len(r.shots))
		}
		if len(burst) > 0 {
			if g := burst[0].at.Sub(on); g < 2400*time.Millisecond || g > 3800*time.Millisecond {
				t.Errorf("%s: charge→first shot %v, want ~3s (60 ticks)", name, g)
			}
			if g := burst[len(burst)-1].at.Sub(burst[0].at); g < 300*time.Millisecond || g > 1200*time.Millisecond {
				t.Errorf("%s: first→last shot %v, want ~0.6s (2×6 ticks)", name, g)
			}
			if g := off.Sub(burst[len(burst)-1].at); g > 800*time.Millisecond {
				t.Errorf("%s: last shot→charge off %v, want ~0.3s (6 ticks)", name, g)
			}
		}
		if len(r.chargeOn) > 1 {
			if g := r.chargeOn[1].Sub(off); g < 4200*time.Millisecond || g > 6000*time.Millisecond {
				t.Errorf("%s: charge off→next charge %v, want ~5s (100 ticks)", name, g)
			}
		}
		for i, s := range r.shots {
			if !s.ownerOK {
				t.Errorf("%s shot %d: add_entity data is not the blaze id", name, i)
			}
			if s.dx > 0.35 || s.dy < 1.2 || s.dy > 1.65 {
				t.Errorf("%s shot %d: spawn offset (h %.3f, y %.3f), want (0, 1.4)", name, i, s.dx, s.dy)
			}
			if math.Abs(s.speed-0.1) > 0.01 {
				t.Errorf("%s shot %d: |delta| %.4f, want 0.1", name, i, s.speed)
			}
		}
		if r.melee != 14 {
			t.Errorf("%s: melee hit left health %.1f, want 14 (attack_damage 6)", name, r.melee)
		}
		if r.fireball != 15 {
			t.Errorf("%s: small fireball hit left health %.1f, want 15 (5.0)", name, r.fireball)
		}
		if !r.burning {
			t.Errorf("%s: small fireball hit never set the player burning", name)
		}
	}
	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	check("vanilla", van)
	check("mc-go", mc)
}
