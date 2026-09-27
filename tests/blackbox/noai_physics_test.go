package blackbox

import (
	"testing"
	"time"
)

// TestNoAIMobHoverParity pins the isEffectiveAi gate on
// LivingEntity.aiStep's travel call, measured on the 26.2 jar: travel
// only runs when the mob has effective AI, so a NoAI summon ten blocks
// up never falls — it hovers at its spawn point indefinitely (the
// classic floating-NoAI-mob behavior). Pos[1] must stay within ~0.5
// of the summon height for the whole window on both servers.
func TestNoAIMobHoverParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("NaA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "NaA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		sinkA := newChatSinkTP(prA, pwA)
		time.Sleep(600 * time.Millisecond)

		sendCommand(t, pwA, "summon minecraft:zombie 40.5 -50 40.5 {NoAI:1b,PersistenceRequired:1b}")
		sel := "@e[type=minecraft:zombie,limit=1]"

		var worst float64 = -50
		moved := false
		ok := false
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if y, ok2 := flopQueryNum(t, sinkA, pwA, sel, "Pos[1]"); ok2 {
				ok = true
				if y < worst {
					worst = y
				}
				if y < -50.5 {
					moved = true
					break
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("%s: zombie Pos[1] never answered", s.label)
		}
		if moved {
			t.Fatalf("%s: NoAI zombie fell — Pos[1] dipped to %v, want to hover at -50", s.label, worst)
		}
		sendCommand(t, pwA, "kill "+sel)
	}
}
