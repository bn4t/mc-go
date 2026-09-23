//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestEnchantDamageParity pins vanilla 26.2's data-driven enchantment
// damage model: the weapon's damage component (sharpness 1+0.5·(l-1)
// unconditional, smite/bane/impaling 2.5·l against their sensitivity
// sets) folds in scaled linearly by the attack charge AFTER the crit
// multiply, and the victim's protection EPF (protection 1/l, typed
// protections 2/l, feather_falling 3/l, capped at 20) shaves epf/25
// off post-armor damage — skipped only for bypasses_enchantments
// (sonic_boom). Frost walker's damage_immunity negates
// burn_from_stepping outright.
//
// Player-target legs read the victim's update_health float; mob legs
// read the surviving Health via /data get and compare floats with a
// small tolerance. Natural regeneration is switched off so the
// health timeline is only the scripted hits.
func TestEnchantDamageParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "V", "C")}, nil)
	defer targets.cleanup()

	readHealth := func(t *testing.T, pr *protocol.Reader, c net.Conn, d time.Duration) float32 {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			if p.ID != server.IDPlaySetHealth {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			h, err := r.Float()
			if err != nil {
				t.Fatalf("set_health read: %v", err)
			}
			_ = c.SetReadDeadline(time.Time{})
			return h
		}
		t.Fatalf("no set_health within %s", d)
		return 0
	}

	run := func(addr string) []string {
		// A joins first so V's fresh spawn lands in A's drain. A third
		// op console (C) issues every command — the chat sink it owns
		// must not share a conn with the spawn/health reads.
		ca, pra, pwa := joinPlayBlackbox(t, addr, "A")
		defer ca.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "C")
		defer cc.Close()
		cv, prv, pwv := joinPlayBlackbox(t, addr, "V")
		defer cv.Close()
		_ = ca.SetDeadline(time.Now().Add(180 * time.Second))
		_ = cv.SetDeadline(time.Now().Add(180 * time.Second))
		_ = cc.SetDeadline(time.Now().Add(180 * time.Second))
		sink := newChatSinkTP(prc, pwc)
		sink.cap = 1 << 20
		sendClientInformation(t, pwv)

		var posA, posV [3]float64
		obsA, obsV := wearObs{}, wearObs{}
		drainWear(t, cv, prv, pwv, &posV, 1200*time.Millisecond, &obsV, nil)
		// Capture V's eid before W joins — the spawn map keeps only the
		// latest player spawn.
		sp := map[int32]int32{}
		drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, sp)
		victimEID := sp[server.EntityTypePlayer]
		if victimEID == 0 {
			t.Fatalf("%s: A never saw V spawn", addr)
		}
		cw, prw, pww := joinPlayBlackbox(t, addr, "W")
		defer cw.Close()
		_ = cw.SetDeadline(time.Now().Add(180 * time.Second))
		sendClientInformation(t, pww)

		sendCommand(t, pwc, "gamerule natural_health_regeneration false")
		sendCommand(t, pwc, "tp A 4.5 -60 2.5 -90 0")
		sendCommand(t, pwc, "gamemode survival V")
		sendCommand(t, pwc, "gamemode survival W")
		sendCommand(t, pwc, "tp V 6.5 -60 2.5 90 0")
		sendCommand(t, pwc, "tp W 20 -60 20")
		drainWear(t, ca, pra, pwa, &posA, 800*time.Millisecond, &obsA, nil)
		drainWear(t, cv, prv, pwv, &posV, 400*time.Millisecond, &obsV, nil)

		movePos := func(x, y, z float64) {
			var b protocol.Buffer
			b.Double(x)
			b.Double(y)
			b.Double(z)
			b.Byte(0x01)
			if err := pwa.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		movePos(4.5, -60, 2.5)

		attack := func(eid int32) {
			var atk protocol.Buffer
			atk.VarInt(eid)
			if err := pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		equipA := func(item string) {
			sendCommand(t, pwc, "item replace entity A weapon.mainhand with "+item)
			drainWear(t, ca, pra, pwa, &posA, 500*time.Millisecond, &obsA, nil)
		}
		var posW [3]float64
		obsW := wearObs{}
		equipW := func(item string) {
			sendCommand(t, pwc, "item replace entity W armor.feet with "+item)
			drainWear(t, cw, prw, pww, &posW, 300*time.Millisecond, &obsW, nil)
		}
		// Summon a mob and return its entity id, read off A's spawn
		// traffic.
		summon := func(mob string, x, z float64, nbt string) int32 {
			sendCommand(t, pwc, fmt.Sprintf("summon minecraft:%s %s -60 %s %s", mob, ftoa(x), ftoa(z), nbt))
			spm := map[int32]int32{}
			drainWear(t, ca, pra, pwa, &posA, 900*time.Millisecond, &obsA, spm)
			ty, _ := server.EntityTypeID(mob)
			if eid := spm[ty]; eid != 0 {
				return eid
			}
			t.Fatalf("%s: %s never spawned", addr, mob)
			return 0
		}
		// mobHealth reads a mob's Health via /data get and returns the
		// raw reply component — identical across servers iff the
		// surviving float32 matches to the last repr digit.
		mobHealth := func(tag, mob string) string {
			sendCommand(t, pwc, fmt.Sprintf("data get entity @e[type=minecraft:%s,limit=1] Health", mob))
			sendCommand(t, pwc, "say ZEND-"+tag)
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				got, endSeen := "", false
				for _, line := range sink.take() {
					if strings.Contains(line, "data.entity.query") {
						got = line[strings.Index(line, "data.entity.query"):]
					}
					if strings.Contains(line, "ZEND-"+tag) {
						endSeen = true
					}
				}
				if endSeen {
					if got != "" {
						return got
					}
					return "none"
				}
				time.Sleep(40 * time.Millisecond)
			}
			return "timeout"
		}
		var out []string
		// --- player-victim melee legs (cumulative health) ---
		// sharpness5 diamond: 7 + 3 = 10 → 20-10 = 10
		equipA("minecraft:diamond_sword[enchantments={sharpness:5}]")
		time.Sleep(700 * time.Millisecond)
		attack(victimEID)
		out = append(out, fmt.Sprintf("sharp5 %v", readHealth(t, prv, cv, 4*time.Second)))
		// smite4 iron vs player (not undead): base 6 → 10-6 = 4
		equipA("minecraft:iron_sword[enchantments={smite:4}]")
		time.Sleep(800 * time.Millisecond)
		attack(victimEID)
		out = append(out, fmt.Sprintf("smite4-on-player %v", readHealth(t, prv, cv, 4*time.Second)))

		// --- mob-target legs (zombie carries vanilla's natural armor 2) ---
		// smite4 diamond on zombie: 7+10 = 17 → def max(0.4,2-8.5)=0.4
		// → 16.728 → Health 3.272
		z1 := summon("zombie", 6.5, 4.0, "{NoAI:1b}")
		equipA("minecraft:diamond_sword[enchantments={smite:4}]")
		time.Sleep(800 * time.Millisecond)
		attack(z1)
		time.Sleep(300 * time.Millisecond)
		out = append(out, "zombie-smite "+mobHealth("z1", "zombie"))
		// sharpness5 diamond on a fresh zombie: 7+3 = 10 → 9.84 → 10.16
		sendCommand(t, pwc, "kill @e[type=minecraft:zombie]")
		z2 := summon("zombie", 6.5, 4.0, "{NoAI:1b}")
		equipA("minecraft:diamond_sword[enchantments={sharpness:5}]")
		time.Sleep(800 * time.Millisecond)
		attack(z2)
		time.Sleep(300 * time.Millisecond)
		out = append(out, "zombie-sharp "+mobHealth("z2", "zombie"))
		// bane2 wooden sword on spider: 4 + 5 = 9 → Health 7
		spEID := summon("spider", 6.5, 4.0, "{NoAI:1b}")
		equipA("minecraft:wooden_sword[enchantments={bane_of_arthropods:2}]")
		time.Sleep(800 * time.Millisecond)
		attack(spEID)
		time.Sleep(300 * time.Millisecond)
		out = append(out, "spider-bane "+mobHealth("sp", "spider"))
		// impaling3 iron on turtle (aquatic): 6 + 7.5 = 13.5 → 16.5
		tu := summon("turtle", 6.5, 4.0, "{NoAI:1b}")
		equipA("minecraft:iron_sword[enchantments={impaling:3}]")
		time.Sleep(800 * time.Millisecond)
		attack(tu)
		time.Sleep(300 * time.Millisecond)
		out = append(out, "turtle-impale "+mobHealth("tu", "turtle"))
		// impaling3 iron on pig (not aquatic): base 6 → Health 4
		pg := summon("pig", 6.5, 4.0, "{NoAI:1b}")
		time.Sleep(800 * time.Millisecond)
		attack(pg)
		time.Sleep(300 * time.Millisecond)
		out = append(out, "pig-impale "+mobHealth("pg", "pig"))

		// --- protection legs: /damage on W, armor+EPF stacked ---
		// feather_falling4 + fall 10: epf 12 → 10*0.52 = 5.2 → 14.8
		equipW("minecraft:leather_boots[enchantments={feather_falling:4}]")
		sendCommand(t, pwc, "damage W 10 minecraft:fall")
		out = append(out, fmt.Sprintf("ff4-fall %v", readHealth(t, prw, cw, 4*time.Second)))
		time.Sleep(1100 * time.Millisecond)
		// protection4 boots + generic 10: armor 1 → 9.92, epf 4 →
		// 8.3328 → 6.4672
		equipW("minecraft:leather_boots[enchantments={protection:4}]")
		sendCommand(t, pwc, "damage W 10 minecraft:generic")
		out = append(out, fmt.Sprintf("prot4 %v", readHealth(t, prw, cw, 4*time.Second)))
		time.Sleep(1100 * time.Millisecond)
		// frost_walker2 vs hot_floor 5: immune → no damage, then a
		// follow-up generic proves the standing health on both.
		equipW("minecraft:leather_boots[enchantments={frost_walker:2}]")
		sendCommand(t, pwc, "damage W 5 minecraft:hot_floor")
		time.Sleep(1200 * time.Millisecond)
		// Past the i-frames window a tiny generic hit always lands:
		// immune → 6.4672-0.3968 ≈ 6.07, vulnerable → ~1.11.
		sendCommand(t, pwc, "damage W 0.4 minecraft:generic")
		out = append(out, fmt.Sprintf("frost-immune %v", readHealth(t, prw, cw, 4*time.Second)))
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i := range vanilla {
		name := strings.Fields(vanilla[i])[0]
		t.Logf("%-18s vanilla=%s mcgo=%s", name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %d (%s): vanilla %s, mc-go %s", i, name, vanilla[i], mcgo[i])
		}
	}
}
