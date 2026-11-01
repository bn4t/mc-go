//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestLeafScaffoldingDistanceParity(t *testing.T) {
	const name = "LeafScaffold"
	targets := requireBlackboxFiles(t, map[string]string{"simulation-distance": "3"}, map[string]string{"ops.json": opsJSONFor(name)}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			old := clientDeadline
			clientDeadline = 2 * time.Minute
			defer func() { clientDeadline = old }()
			c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			seq := 0
			command := func(s string) []string {
				seq++
				sendCommand(t, pw, s)
				m := fmt.Sprintf("LEAF_DONE_%d", seq)
				sendCommand(t, pw, "say "+m)
				return waitForExactSayBB(t, sink, m, 60*time.Second)
			}
			witness := func(s string) bool {
				seq++
				m := fmt.Sprintf("LEAF_IF_%d", seq)
				return structureSaidBB(command(s+" run say "+m), m)
			}
			command("tick freeze")
			command("tp @s 0 100 0")
			command("fill -4 99 -4 10 104 4 air")
			command("setblock 0 100 0 stone")
			command("setblock 1 101 0 oak_leaves[distance=1,persistent=true]")
			command("setblock 0 101 0 scaffolding[distance=0]")
			command("setblock 5 101 0 oak_leaves[distance=1,persistent=true]")
			command("setblock 4 101 0 oak_log")
			command("setblock 8 101 0 oak_leaves[distance=1,persistent=true]")
			command("setblock 7 101 0 stone")
			for _, w := range []string{"execute if block 0 101 0 scaffolding[distance=0]", "execute if block 1 101 0 oak_leaves[distance=1,persistent=true]", "execute if block 4 101 0 oak_log", "execute if block 5 101 0 oak_leaves[distance=1,persistent=true]", "execute if block 7 101 0 stone", "execute if block 8 101 0 oak_leaves[distance=1,persistent=true]"} {
				if !witness(w) {
					t.Fatalf("setup witness %s", w)
				}
			}
			before := gameTimeOf(t, pw, sink)
			command("tick step 1")
			deadline := time.Now().Add(20 * time.Second)
			for gameTimeOf(t, pw, sink) < before+1 {
				if time.Now().After(deadline) {
					t.Fatal("step did not advance")
				}
			}
			// A second marker round trip gives the single stepped owner pass a chance
			// to complete while world age remains frozen at the requested boundary.
			deadline = time.Now().Add(5 * time.Second)
			found := false
			for time.Now().Before(deadline) {
				if witness("execute if block 1 101 0 oak_leaves[distance=7,persistent=true]") {
					found = true
					break
				}
			}
			if !found {
				t.Error("scaffolding neighbor did not fall back to distance 7")
			}
			if !witness("execute if block 8 101 0 oak_leaves[distance=7,persistent=true]") {
				t.Error("independent scheduled leaf tick did not execute")
			}
			if !witness("execute if block 0 101 0 scaffolding[distance=0]") {
				t.Error("scaffolding control changed")
			}
			if !witness("execute if block 5 101 0 oak_leaves[distance=1,persistent=true]") {
				t.Error("log neighbor control changed")
			}
			if after := gameTimeOf(t, pw, sink); after != before+1 {
				t.Fatalf("world age advanced beyond boundary: %d -> %d", before, after)
			}
		})
	}
}
