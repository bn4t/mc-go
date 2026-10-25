package blackbox

import (
	"testing"

	"mc-go/internal/server"
)

// TestFirstHitMobXPCreditParity measures first-hit lethal melee over the wire:
// one fully charged netherite axe hit must produce pig XP, and collecting the
// orb must grant its exact observed value. The shared helper also remains an
// ordinary Java-free regression, with all original XP/metadata assertions.
func TestFirstHitMobXPCreditParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"},
		map[string]string{"ops.json": opsJSONFor("FirstXP")}, func(s *server.Server) {
			s.GameMode = server.GameModeSurvival
		})
	defer targets.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()
	for _, endpoint := range []struct{ name, address string }{
		{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			mobXPOrbPackets(t, endpoint.address, "FirstXP")
		})
	}
}
