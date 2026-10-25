package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestTripwirePoweredParity drives TripWireBlock.entityInside/tick —
// an entity standing in a wire cell powers just that wire (flags 3
// broadcast), updateSource reaches back through the run to flip both
// hooks' powered+attached, and a 10-tick recheck releases the wire
// once the entity leaves. Wires at x=98..100 hang between hooks
// anchored on stone at x=96 and x=102.
func TestTripwirePoweredParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`fill 90 -59 120 120 -55 145 air`,
		// The pig's inside-check only runs in an entity-ticking
		// chunk — stand the player next to the rig.
		`tp @s 99.5 -58 131.5`,
		// Vanilla attach order matters: a hook's calculateState runs
		// from wire onPlace -> updateSource (south/west walks) or the
		// hook's own setPlacedBy — never from a command-written hook.
		// Anchors and hooks first, wires last, so the final wire's
		// onPlace completes the run.
		`setblock 96 -60 130 minecraft:stone`,
		`setblock 102 -60 130 minecraft:stone`,
		`setblock 97 -60 130 minecraft:tripwire_hook[facing=east]`,
		`setblock 101 -60 130 minecraft:tripwire_hook[facing=west]`,
		`setblock 98 -60 130 minecraft:tripwire`,
		`setblock 99 -60 130 minecraft:tripwire`,
		`setblock 100 -60 130 minecraft:tripwire`,
		// A passive mob — the flat world is peaceful and refuses
		// monsters.
		`summon minecraft:pig 98.5 -60 130.5 {NoAI:1b}`,
	}
	pressedProbes := []struct{ name, spec string }{
		{"walked wire powered", "98 -60 130 minecraft:tripwire[powered=true]"},
		{"untouched wire cold", "99 -60 130 minecraft:tripwire[powered=false]"},
		{"far wire cold", "100 -60 130 minecraft:tripwire[powered=false]"},
		{"west hook powered", "97 -60 130 minecraft:tripwire_hook[powered=true]"},
		{"east hook powered", "101 -60 130 minecraft:tripwire_hook[powered=true]"},
		{"west hook attached", "97 -60 130 minecraft:tripwire_hook[attached=true]"},
		{"east hook attached", "101 -60 130 minecraft:tripwire_hook[attached=true]"},
	}
	releasedProbes := []struct{ name, spec string }{
		{"wire released", "98 -60 130 minecraft:tripwire[powered=false]"},
		{"west hook released", "97 -60 130 minecraft:tripwire_hook[powered=false]"},
		{"east hook released", "101 -60 130 minecraft:tripwire_hook[powered=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZT-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZT-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZTK%d", i))
		marker := fmt.Sprintf("ZTK%d", i)
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZT-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZT-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		sendCommand(t, pw, `say ZSYNC`)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			found := false
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSYNC") {
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Let the pig land on the ground under the wire and the
		// inside-check fire.
		time.Sleep(3 * time.Second)

		var out []string
		for i, p := range pressedProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// Remove the presser: wire must release within its 10-tick
		// recheck (a second of wall time is generous).
		// The killed pig drops porkchops — item entities press the
		// wire too on vanilla, so clear the drops as well.
		sendCommand(t, pw, `kill @e[type=minecraft:pig]`)
		sendCommand(t, pw, `kill @e[type=minecraft:item,distance=..8]`)
		time.Sleep(2 * time.Second)
		for i, p := range releasedProbes {
			out = append(out, probe(pw, sink, p.spec, i+100))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(pressedProbes, releasedProbes...)
	for i, p := range all {
		t.Logf("%-30s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
