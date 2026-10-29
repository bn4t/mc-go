package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestGhastShootParity pins Ghast$GhastShootFireballGoal and the
// reflected-fireball rule (javap-measured, see internal/server/ghast.go):
//
//   - Shoot leg: a survival player fourteen blocks from a ghast on the
//     same level draws the charge cycle — level event 1015 (warning) at
//     chargeTime 10, DATA_IS_CHARGING (index 16, boolean) on the tick
//     after, and at chargeTime 20 level event 1016 plus one fireball
//     add_entity (data = the ghast's id) 4 blocks out along the facing
//     and 2.5 above the ghast's feet at |delta| 0.1, the flag dropping
//     the same tick; the next shot follows 60 ticks later
//     (chargeTime −40 → 20). A stone + slab wall in front of the player
//     catches the balls below the eye line, and mob_griefing is off so
//     the blasts set no fire.
//   - Reflect leg: an ownerless fireball batted by the player flies the
//     player's look into a NoAI ghast — Ghast.hurtServer's
//     isReflectedFireball arm deals 1000, so the ghast dies (entity
//     event 3).
func TestGhastShootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	ghastType, _ := server.EntityTypeID("ghast")
	fbType, _ := server.EntityTypeID("fireball")

	type shot struct {
		at      time.Time
		h, dy   float64
		speed   float64
		ownerOK bool
	}
	type result struct {
		warn, fire     []time.Time
		chargeOn       []time.Time
		chargeOff      []time.Time
		shots          []shot
		reflectDied    bool
		reflectFbFound bool
	}

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		fw := startFlyerWire(t, c, pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "gamerule minecraft:mob_griefing false")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "tp P 0.5 -60 0.5 0 0")
		sendCommand(t, pw, "fill -8 -60 2 8 -60 2 minecraft:stone")
		sendCommand(t, pw, "fill -8 -59 2 8 -59 2 minecraft:stone_slab")
		time.Sleep(800 * time.Millisecond)

		start := fw.mark()
		sendCommand(t, pw, "summon minecraft:ghast 0.5 -60 16.5")
		time.Sleep(1500 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival P")
		for i := 0; i < 11; i++ {
			sendCommand(t, pw, "effect give P minecraft:instant_health 1 10 true")
			time.Sleep(time.Second)
		}
		sendCommand(t, pw, "gamemode creative P")
		tr := newFwTracker()
		for _, p := range fw.since(0)[:start] {
			tr.feed(p)
		}
		var ghastEID int32 = -1
		for _, p := range fw.since(start) {
			switch p.id {
			case server.IDPlaySpawnEntity:
				s, ok := fwDecodeSpawn(p)
				if !ok {
					break
				}
				if s.typ == ghastType && ghastEID < 0 {
					ghastEID = s.eid
				}
				if s.typ == fbType && ghastEID >= 0 {
					gp := tr.pos[ghastEID]
					r.shots = append(r.shots, shot{
						at:      p.at,
						h:       math.Hypot(s.x-gp[0], s.z-gp[2]),
						dy:      s.y - gp[1],
						speed:   fwLen(s.vx, s.vy, s.vz),
						ownerOK: s.data == ghastEID,
					})
				}
			case server.IDPlayEntityMetadata:
				eid, ents := fwDecodeMeta(p.data)
				if eid != ghastEID {
					break
				}
				for _, e := range ents {
					if e.idx == 16 && e.typ == 8 {
						if e.b && len(r.chargeOn) == len(r.chargeOff) {
							r.chargeOn = append(r.chargeOn, p.at)
						} else if !e.b && len(r.chargeOn) > len(r.chargeOff) {
							r.chargeOff = append(r.chargeOff, p.at)
						}
					}
				}
			case server.IDPlayLevelEvent:
				if ev, _, _, _, ok := fwLevelEvent(p); ok {
					switch ev {
					case 1015:
						r.warn = append(r.warn, p.at)
					case 1016:
						r.fire = append(r.fire, p.at)
					}
				}
			}
			tr.feed(p)
		}

		// Reflect leg.
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 40.5 -60 20.5 0 0")
		time.Sleep(800 * time.Millisecond)
		m := fw.mark()
		sendCommand(t, pw, "summon minecraft:ghast 40.5 -60 28.5 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:fireball 40.5 -58.9 22.5 {Motion:[0.0d,0.0d,0.0d]}")
		time.Sleep(1200 * time.Millisecond)
		var gEID, fEID int32 = -1, -1
		for _, p := range fw.since(m) {
			if p.id == server.IDPlaySpawnEntity {
				if s, ok := fwDecodeSpawn(p); ok {
					if s.typ == ghastType {
						gEID = s.eid
					}
					if s.typ == fbType {
						fEID = s.eid
					}
				}
			}
		}
		r.reflectFbFound = gEID >= 0 && fEID >= 0
		if r.reflectFbFound {
			m = fw.mark()
			sendAttackEntity(pw, fEID)
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) && !r.reflectDied {
				time.Sleep(100 * time.Millisecond)
				for _, p := range fw.since(m) {
					if p.id == server.IDPlayEntityEvent {
						rd := protocol.NewPacketReader(p.data)
						eid, _ := rd.Int()
						st, _ := rd.Byte()
						if eid == gEID && st == 3 {
							r.reflectDied = true
						}
					}
				}
			}
		}
		return r
	}

	check := func(name string, r result) {
		t.Logf("%s: warn=%d fire=%d chargeOn=%d chargeOff=%d shots=%d reflect(found=%v died=%v)",
			name, len(r.warn), len(r.fire), len(r.chargeOn), len(r.chargeOff), len(r.shots), r.reflectFbFound, r.reflectDied)
		for i, s := range r.shots {
			t.Logf("%s shot %d: t=%s h=%.3f dy=%.3f |v|=%.4f owner=%v", name, i, s.at.Format("05.000"), s.h, s.dy, s.speed, s.ownerOK)
		}
		for i, w := range r.warn {
			t.Logf("%s warn %d: %s", name, i, w.Format("05.000"))
		}
		for i, w := range r.fire {
			t.Logf("%s fire %d: %s", name, i, w.Format("05.000"))
		}
		for i := range r.chargeOn {
			off := "-"
			if i < len(r.chargeOff) {
				off = r.chargeOff[i].Format("05.000")
			}
			t.Logf("%s charging %d: on=%s off=%s", name, i, r.chargeOn[i].Format("05.000"), off)
		}
		if len(r.warn) == 0 || len(r.fire) == 0 || len(r.chargeOn) == 0 || len(r.chargeOff) == 0 {
			t.Errorf("%s: no complete charge cycle", name)
			return
		}
		if g := r.fire[0].Sub(r.warn[0]); g < 300*time.Millisecond || g > 900*time.Millisecond {
			t.Errorf("%s: warning→shot %v, want ~0.5s (10 ticks)", name, g)
		}
		if g := r.chargeOn[0].Sub(r.warn[0]); g < 0 || g > 300*time.Millisecond {
			t.Errorf("%s: warning→charging %v, want ~1 tick", name, g)
		}
		// The flag drops on the shot tick; set_entity_data then rides
		// the next sendChanges pass.
		if g := r.chargeOff[0].Sub(r.fire[0]); g < -100*time.Millisecond || g > 700*time.Millisecond {
			t.Errorf("%s: shot→charging off %v, want the shot tick (+ sync cadence)", name, g)
		}
		if len(r.fire) > 1 {
			if g := r.fire[1].Sub(r.fire[0]); g < 2500*time.Millisecond || g > 3700*time.Millisecond {
				t.Errorf("%s: shot period %v, want ~3s (60 ticks)", name, g)
			}
		} else {
			t.Errorf("%s: only %d shot(s) in 11s", name, len(r.fire))
		}
		if len(r.shots) != len(r.fire) {
			t.Errorf("%s: %d fireball spawns for %d level events 1016", name, len(r.shots), len(r.fire))
		}
		for i, s := range r.shots {
			if !s.ownerOK {
				t.Errorf("%s shot %d: add_entity data is not the ghast id", name, i)
			}
			if s.h < 3.0 || s.h > 5.0 || s.dy < 2.0 || s.dy > 3.0 {
				t.Errorf("%s shot %d: spawn offset (h %.3f, y %.3f), want (4, 2.5)", name, i, s.h, s.dy)
			}
			if math.Abs(s.speed-0.1) > 0.01 {
				t.Errorf("%s shot %d: |delta| %.4f, want 0.1", name, i, s.speed)
			}
		}
		if !r.reflectFbFound {
			t.Errorf("%s: reflect leg never saw the ghast + fireball spawns", name)
		} else if !r.reflectDied {
			t.Errorf("%s: the batted fireball did not kill the ghast (1000 reflected damage)", name)
		}
	}
	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	check("vanilla", van)
	check("mc-go", mc)
}
