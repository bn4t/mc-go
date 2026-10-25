// Blackbox parity pin for LivingEntity.getVoicePitch on the 26.2
// decompile: every ambient/hurt/death voice goes out at
// (nextFloat() - nextFloat()) * 0.2 + 1.0 — and baby mobs pitch the
// same call at +1.5 instead of +1.0 (LivingEntity.getVoicePitch's
// isBaby arm). The wire carries the pitch float on named_sound, so
// a baby's hurt cries land inside [1.3, 1.7] while an adult's sit in
// [0.8, 1.2]. Other positioned sounds (steps at the fixed 1.0) share
// the packet, so the pin buckets on the zombie voice ids only.
package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// zombieVoiceSnds are the WIRE ids for entity.zombie.ambient/.hurt/
// .death — every one rides getVoicePitch; unrelated positioned
// sounds (steps at fixed 1.0) are ignored.
var zombieVoiceSnds = map[int32]bool{1870: true, 1875: true, 1882: true}

func TestBabyPitchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	voice := func(o vehObs) []float64 {
		var out []float64
		for i, id := range o.soundIds {
			if zombieVoiceSnds[id] {
				out = append(out, o.soundPitch[i])
			}
		}
		return out
	}

	run := func(t *testing.T, name, addr string) (babyP, adultP []float64) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, _, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)

		sendCommand(t, pwc, "gamemode spectator BOATA")
		sendCommand(t, pwc, "gamemode spectator BOATB")
		sendCommand(t, pwc, "gamemode spectator BOATC")
		sendCommand(t, pwc, "gamerule do_mob_spawning false")
		sendCommand(t, pwc, "kill @e[type=minecraft:zombie]")
		sendCommand(t, pwc, "tp BOATA 8.5 -60 20")
		sendCommand(t, pwc, "tp BOATB 8.5 -60 20")
		sendCommand(t, pwc, "tp BOATC 8.5 -60 20")

		// Phase A — one baby zombie; /damage drops it and its hurt
		// voice goes out pitched ~1.5.
		sendCommand(t, pwc, "summon zombie 8.5 -60 8.5 {IsBaby:1b}")
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pwc, "damage @e[type=minecraft:zombie] 4")
		time.Sleep(700 * time.Millisecond)
		sendCommand(t, pwc, "damage @e[type=minecraft:zombie] 4")
		sendCommand(t, pwc, "kill @e[type=minecraft:zombie]")
		babyP = append(babyP, voice(vehWatch(t, v, false, 0, 3500*time.Millisecond))...)

		// Phase B — one adult zombie on the same spot.
		sendCommand(t, pwc, "summon zombie 8.5 -60 8.5 {}")
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pwc, "damage @e[type=minecraft:zombie] 4")
		time.Sleep(700 * time.Millisecond)
		sendCommand(t, pwc, "damage @e[type=minecraft:zombie] 4")
		adultP = append(adultP, voice(vehWatch(t, v, false, 0, 3500*time.Millisecond))...)
		return babyP, adultP
	}

	check := func(t *testing.T, name string, babyP, adultP []float64) {
		// /damage hits inside the ten-tick hurt grace are silent on
		// both servers, and queue jitter can slide the second hit
		// into that window — pin the band on every voice that did
		// emit and only demand at least one per phase.
		if len(babyP) < 1 {
			t.Fatalf("%s: no voice sounds during the baby phase", name)
		}
		if len(adultP) < 1 {
			t.Fatalf("%s: no voice sounds during the adult phase", name)
		}
		for i, p := range babyP {
			if p < 1.28 || p > 1.72 {
				t.Fatalf("%s: baby voice pitch[%d] %.3f outside [1.28,1.72]", name, i, p)
			}
		}
		for i, p := range adultP {
			if p < 0.78 || p > 1.22 {
				t.Fatalf("%s: adult voice pitch[%d] %.3f outside [0.78,1.22]", name, i, p)
			}
		}
		t.Logf("%s: babyP=%v adultP=%v", name, babyP, adultP)
	}

	vB, vA := run(t, "vanilla", targets.vanilla)
	mB, mA := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vB, vA)
	check(t, "mc-go", mB, mA)
}
