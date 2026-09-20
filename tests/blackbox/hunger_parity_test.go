package blackbox

// Hunger and damage parity: player-side damage and food ticks run on
// the wall-clock connection tick in vanilla 26.2 — ungated by
// /tick freeze and never advanced by /tick step. A dip into the void
// (producing paced hits through the post-hit invulnerability window)
// followed by a fed recovery window replays the same Set Health
// sequence on both servers.

import (
	"bytes"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type healthTuple struct {
	health float32
	food   int32
	sat    float32
}

// collectHealthUntil reads until the marker chat echo arrives, keeping
// every Set Health tuple seen on the way.
func collectHealthUntil(t *testing.T, pr *protocol.Reader, marker string, timeout time.Duration) []healthTuple {
	t.Helper()
	var out []healthTuple
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("health scan %s: %v", marker, err)
		}
		if p.ID == server.IDPlaySetHealth {
			r := protocol.NewPacketReader(p.Data)
			h, err1 := r.Float()
			f, err2 := r.VarInt()
			s, err3 := r.Float()
			if err1 == nil && err2 == nil && err3 == nil {
				out = append(out, healthTuple{h, f, s})
			}
		}
		if bytes.Contains(p.Data, []byte(marker)) {
			return out
		}
	}
	t.Fatalf("marker %s never arrived", marker)
	return nil
}

func TestHungerRegenParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("HungV", "HungM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()

	run := func(addr, name string) [][]healthTuple {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()

		sendCommand(t, pw, "say READY")
		collectHealthUntil(t, pr, "READY", 30*time.Second)
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "say FROZEN")
		collectHealthUntil(t, pr, "FROZEN", 15*time.Second)

		// Two wall-clock seconds in the void band while frozen: void
		// damage is paced by the post-hit invulnerability, so this is a
		// handful of hits, well short of lethal.
		sendCommand(t, pw, "tp @s 6 -140 2")
		sendCommand(t, pw, "say TPD")
		collectHealthUntil(t, pr, "TPD", 15*time.Second)
		time.Sleep(2 * time.Second)
		sendCommand(t, pw, "say VOIDED")
		voided := collectHealthUntil(t, pr, "VOIDED", 15*time.Second)

		// Back on the ground: the saturation-boosted regen heals
		// min(sat,6)/6 every ten ticks while food and saturation last.
		sendCommand(t, pw, "tp @s 6 -60 2")
		sendCommand(t, pw, "say BACK")
		collectHealthUntil(t, pr, "BACK", 15*time.Second)
		time.Sleep(3 * time.Second)
		sendCommand(t, pw, "say REGEN")
		regen := collectHealthUntil(t, pr, "REGEN", 15*time.Second)
		return [][]healthTuple{voided, regen}
	}

	want := run(tg.vanilla, "HungV")
	got := run(tg.mcgo, "HungM")
	for i, phase := range []string{"void", "regen"} {
		w, g := want[i], got[i]
		t.Logf("%s: vanilla=%v mc-go=%v", phase, w, g)
		if len(w) < 2 || len(g) < 2 {
			t.Errorf("%s phase: too few health packets to compare (vanilla=%d mc-go=%d)",
				phase, len(w), len(g))
			continue
		}
		// Wall-clock windows clip a running sequence at tick-boundary
		// luck, so packet counts and phase endpoints wander by a packet
		// or two. What must match is the physics: the same number of
		// void hits' worth of damage (within one hit) and the same
		// end-of-window food/saturation.
		wf, gf := w[len(w)-1], g[len(g)-1]
		if wf.food != gf.food {
			t.Errorf("%s end state: vanilla food=%d mc-go food=%d", phase, wf.food, gf.food)
		}
		if d := wf.sat - gf.sat; d < -1.01 || d > 1.01 {
			t.Errorf("%s end saturation: vanilla=%v mc-go=%v (one drain of boundary jitter allowed)",
				phase, wf.sat, gf.sat)
		}
		if d := wf.health - gf.health; d < -4.01 || d > 4.01 {
			t.Errorf("%s end health: vanilla=%v mc-go=%v (one-hit boundary tolerance 4)",
				phase, wf.health, gf.health)
		}
		// Every packet's health delta is either a void hit (-4), a regen
		// heal (min(sat,6)/6 > 0), or a pure drain (0) — anything else
		// means a different damage/heal model.
		check := func(seq []healthTuple, who string) {
			for j := 1; j < len(seq); j++ {
				d := seq[j].health - seq[j-1].health
				if d > -0.001 && d < 0.001 {
					continue
				}
				if d > 0 {
					if d > 0.84 {
						t.Errorf("%s %s packet %d: heal %v exceeds sat-regen max", phase, who, j, d)
					}
				} else if d < -4.01 || d > -0.49 {
					t.Errorf("%s %s packet %d: damage %v is not a void hit", phase, who, j, d)
				}
			}
		}
		check(w, "vanilla")
		check(g, "mc-go")
	}
}
