//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"slices"
	"testing"
	"time"
)

func TestExecuteLoadedLocalPositionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("LocalLoaded")}, func(s *server.Server) { s.ViewDistance = 2; s.SimulationDistance = 2 })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "LocalLoaded")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("LOADLOCALEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 30*time.Second)
			}
			expect := func(cond, label string) {
				marker := "LOADLOCAL_" + label
				if !structureSaidBB(command("execute "+cond+" run say "+marker), marker) {
					t.Errorf("missing %s", label)
				}
			}
			absent := func(cond, label string) {
				marker := "LOADLOCAL_" + label
				if structureSaidBB(command("execute "+cond+" run say "+marker), marker) {
					t.Errorf("unexpected %s", label)
				}
			}
			command("tick freeze")
			command("forceload add 0 0")
			ready := false
			deadline := time.Now().Add(60 * time.Second)
			for !ready && time.Now().Before(deadline) {
				ready = structureSaidBB(command("execute if loaded 0 70 0 run say LOADLOCALREADY"), "LOADLOCALREADY")
			}
			if !ready {
				t.Fatal("loaded control unavailable")
			}
			// The far source is unloaded; local forward reaches the loaded origin.
			far := "positioned 4000.5 70 0.5 rotated 90 0"
			near := "positioned 0.5 70 0.5 rotated 90 0"
			expect(far+" unless loaded ~ ~ ~", "far_source")
			expect(far+" if loaded 0 70 0", "far_literal")
			expect(far+" if loaded ^ ^ ^4000", "far_to_near")
			absent(far+" unless loaded ^ ^ ^4000", "far_to_near_unless")
			expect(near+" if loaded ~ ~ ~", "near_source")
			expect(near+" unless loaded 4000 70 0", "near_literal")
			expect(near+" unless loaded ^ ^ ^-4000", "near_to_far")
			absent(near+" if loaded ^ ^ ^-4000", "near_to_far_if")
			expect("positioned -4000.5 70 0.5 rotated 270 0 if loaded ^ ^ ^4001", "negative_source")
			for i, r := range []struct{ coords, key string }{{"^ ~ ^", "argument.pos.mixed"}, {"~ ^ ~", "argument.pos.mixed"}, {"^NaN ^ ^", "parsing.double.expected"}, {"^-Inf ^ ^", "parsing.double.invalid"}, {"^1e2 ^ ^", "argument.pos3d.incomplete"}, {"^+1 ^ ^", "parsing.double.expected"}, {"^ ^ ^Inf", "parsing.double.expected"}, {"^ ^ ^1e2", "command.expected.separator"}} {
				for _, kind := range []string{"if", "unless"} {
					marker := fmt.Sprintf("LOADINVALID%d%s", i, kind)
					lines := command("execute " + near + " " + kind + " loaded " + r.coords + " run say " + marker)
					keys := executePlanFeedbackKeysBB(lines)
					if structureSaidBB(lines, marker) || !slices.Contains(keys, r.key) {
						t.Errorf("%s %s executed or missing %s: %v", kind, r.coords, r.key, keys)
					}
				}
			}
		})
	}
}
