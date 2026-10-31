//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"slices"
	"testing"
	"time"
)

// Preserve the measured local-coordinate regression independently of biome
// lookup: begin in uniform plains, then distinguish edited source frames.
func TestExecuteBiomeLocalBlockPositionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"level-seed": "12345", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("LocalBio", "QuartLoader")}, func(s *server.Server) { s.WorldSeed = 12345; s.ViewDistance = 2; s.SimulationDistance = 2 })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "LocalBio")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			for _, cmd := range []string{"tick freeze", "forceload add 0 0 31 31", "fillbiome 0 -64 0 23 -45 23 minecraft:plains", "execute positioned 8.5 -57.5 11.5 rotated 0 0 if biome 8 -59 11 minecraft:plains run say LOCALLITERAL", "execute positioned 8.5 -57.5 11.5 rotated 0 0 if biome ~ ~-1 ~ minecraft:plains run say LOCALRELATIVE", "execute positioned 8.5 -57.5 11.5 rotated 0 0 if biome ^ ^-1 ^ minecraft:plains run say LOCALVALUE", "say LOCALEND"} {
				sendCommand(t, pw, cmd)
			}
			lines := waitForExactSayBB(t, sink, "LOCALEND", 30*time.Second)
			for _, mark := range []string{"LOCALLITERAL", "LOCALRELATIVE", "LOCALVALUE"} {
				if !structureSaidBB(lines, mark) {
					t.Errorf("local block-position receipt%s absent", mark)
				}
			}
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("LOCALFRAMEEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			expect := func(frame, coords, biome, label string) {
				mark := "LOCALFRAME_" + label
				if !structureSaidBB(command("execute "+frame+" if biome "+coords+" minecraft:"+biome+" run say "+mark), mark) {
					t.Errorf("local source-frame receipt%s absent", label)
				}
			}
			command("fillbiome 8 -60 8 11 -57 11 minecraft:sulfur_caves")
			// Existing independent literal/relative/dimension witnesses also
			// establish the edited source cells and loaded target dimensions.
			executeBiomeFramePins(t, leg.addr, pw, sink)
			for i, row := range []struct{ frame, relative, local string }{
				{"positioned -7.5 -57.5 11.5 rotated 0 0", "~16 ~ ~", "^16 ^ ^"},
				{"positioned 24.5 -57.5 11.5 rotated 90 0", "~-16 ~ ~", "^ ^ ^16"},
				{"positioned 8.5 -57.5 27.5 rotated 180 0", "~ ~ ~-16", "^ ^ ^16"},
				{"positioned -7.5 -57.5 11.5 rotated 270 0", "~16 ~ ~", "^ ^ ^16"},
				{"positioned 8.5 -41.5 11.5 rotated 0 90", "~ ~-16 ~", "^ ^ ^16"},
				{"positioned 8.5 -61.5 11.5 rotated 0 -90", "~ ~4 ~", "^ ^ ^4"},
				{"positioned 10.25 -59.2 4.65 rotated 33 -27", "~-2 ~2 ~7", "^2.25 ^-1.5 ^6.75"},
			} {
				expect(row.frame, "8 -58 11", "sulfur_caves", fmt.Sprintf("absolute%d", i))
				expect(row.frame, row.relative, "sulfur_caves", fmt.Sprintf("relative%d", i))
				expect(row.frame, row.local, "sulfur_caves", fmt.Sprintf("local%d", i))
				// Every source is physically distinct from the sulfur target.
				mark := fmt.Sprintf("LOCALFRAME_source_negative%d", i)
				if !structureSaidBB(command("execute "+row.frame+" unless biome ~ ~ ~ minecraft:sulfur_caves run say "+mark), mark) {
					t.Errorf("source unexpectedly sulfur%d", i)
				}
			}
			expect("positioned 8.5 -59.12 11.5 rotated 0 0 anchored eyes", "^ ^ ^", "sulfur_caves", "eyes")
			expect("positioned 8.5 -59.12 11.5 rotated 0 0 anchored feet", "^ ^ ^", "plains", "feet")
			expect("positioned 8.5 -59.12 11.5 rotated 0 0 anchored eyes", "8 -60 11", "plains", "absolute_ignores_anchor")
			expect("anchored eyes positioned 8.5 -59.12 11.5 rotated 0 0", "^ ^ ^", "plains", "positioned_resets_anchor")
			expect("positioned 64.5 70.5 88.5 in minecraft:the_nether rotated 0 0", "^ ^ ^", "badlands", "dimension_scales_source")
			expect("in minecraft:the_nether positioned 64.5 70.5 88.5 rotated 0 0", "^ ^ ^", "forest", "position_after_dimension")
			expect("positioned 192.5 70.5 88.5 in minecraft:the_nether rotated 90 0", "^ ^ ^16", "badlands", "local_distance_not_scaled")
			for i, coords := range []string{"^ ~ ^", "~ ^ ~"} {
				mark := fmt.Sprintf("LOCALINVALID%d", i)
				lines := command("execute positioned 8.5 -57.5 11.5 if biome " + coords + " minecraft:sulfur_caves run say " + mark)
				keys := executePlanFeedbackKeysBB(lines)
				if structureSaidBB(lines, mark) || !slices.Contains(keys, "argument.pos.mixed") {
					t.Errorf("mixed local coordinates%d executed or missing typed error: %v", i, keys)
				}
			}

			for i, row := range []struct{ coords, key string }{
				{"^NaN ^ ^", "parsing.double.expected"},
				{"^Inf ^ ^", "parsing.double.expected"},
				{"^-Inf ^ ^", "parsing.double.invalid"},
				{"^1e2 ^ ^", "argument.pos3d.incomplete"},
				{"^1.5e-1 ^ ^", "argument.pos3d.incomplete"},
				{"^+1 ^ ^", "parsing.double.expected"},
				{"^no ^ ^", "parsing.double.expected"},
				{"^ ^NaN ^", "parsing.double.expected"},
				{"^ ^ ^Inf", "parsing.double.expected"},
				{"^ ^ ^1e2", "command.expected.separator"},
			} {
				mark := fmt.Sprintf("LOCALBADNUMBER%d", i)
				lines := command("execute positioned 8.5 -57.5 11.5 if biome " + row.coords + " minecraft:sulfur_caves run say " + mark)
				keys := executePlanFeedbackKeysBB(lines)
				if structureSaidBB(lines, mark) || !slices.Contains(keys, row.key) {
					t.Errorf("numeric local token%s executed or missing%s: %v", row.coords, row.key, keys)
				}
			}

		})
	}
}
