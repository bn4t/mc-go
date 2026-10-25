//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFrostedIceParity drives FrostedIceBlock — not random-ticking;
// decay is the scheduled-tick chain onPlace arms at 60-120 ticks:
// each tick slightly-melts (age bumps to 3, then a liquid write) when
// nextInt(3)==0 or the cell keeps <4 frosted neighbours, gated on
// maxLocalRawBrightness > 10 - age (the 11-age-dampening gate with
// dampening 1) — sun or a torch three cells off both clear it, a
// torch four off does not. neighbourChanged melts a sparse cell (<2
// frosted neighbours) outright when the NOTIFY block is frosted ice:
// command writes notify the new state (arrival collapse) and gameplay
// writes the old one (departure collapse), so a /setblock'd frosted
// pair in the dark collapses both cells instantly.
func TestFrostedIceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`time set noon`,
		`fill 44 -60 10 76 -60 40 minecraft:stone`,
		`fill 44 -59 10 76 -56 40 minecraft:air`,
		// S: open sky at noon — melts through its ages into water.
		`setblock 60 -59 16 minecraft:frosted_ice`,
		// Dark cells need a SEALED box: vanilla's raw sky light floods
		// sideways under a thin overhang at 14, which still clears the
		// 10-age gate. One block of stone on every face shuts it.
		`fill 59 -59 19 61 -57 21 minecraft:stone`,
		`fill 60 -59 20 60 -58 20 minecraft:air`,
		`setblock 60 -59 20 minecraft:frosted_ice`,
		// T2: boxed, torch at taxicab 2 — block light 12 melts.
		`fill 63 -59 19 65 -57 23 minecraft:stone`,
		`fill 64 -59 20 64 -58 22 minecraft:air`,
		`setblock 64 -59 20 minecraft:frosted_ice`,
		`setblock 64 -59 22 minecraft:torch`,
		// T3: boxed, torch at taxicab 3 — 11 is over 10, melts.
		`fill 67 -59 19 69 -57 24 minecraft:stone`,
		`fill 68 -59 20 68 -58 23 minecraft:air`,
		`setblock 68 -59 20 minecraft:frosted_ice`,
		`setblock 68 -59 23 minecraft:torch`,
		// T4: boxed, torch at taxicab 4 — 10 is not over 10, holds.
		`fill 71 -59 18 73 -57 24 minecraft:stone`,
		`fill 72 -59 19 72 -58 23 minecraft:air`,
		`setblock 72 -59 19 minecraft:frosted_ice`,
		`setblock 72 -59 23 minecraft:torch`,
		// C: a lone frosted in a box, then a frosted setblock'd on its
		// face — the command write notifies the NEW state, so the
		// donor (1 frosted neighbour) melts instantly, and its
		// departure melts the writer in turn. Both water.
		`fill 51 -59 19 53 -57 22 minecraft:stone`,
		`fill 52 -59 20 52 -58 21 minecraft:air`,
		`setblock 52 -59 20 minecraft:frosted_ice`,
		`setblock 52 -59 21 minecraft:frosted_ice`,
		// L: a 3-cell line written in order — A melts on B's arrival,
		// B melts on A's departure (old-state notify), C lands on a
		// dead pair and sits. The departure half is what Frost
		// Walker's gameplay writes rely on.
		`fill 55 -59 18 57 -57 25 minecraft:stone`,
		`fill 56 -59 19 56 -58 24 minecraft:air`,
		`setblock 56 -59 20 minecraft:frosted_ice`,
		`setblock 56 -59 21 minecraft:frosted_ice`,
		`setblock 56 -59 22 minecraft:frosted_ice`,
		// Water moats stop melt flow crossing lanes — they span past
		// every cell so a 7-cell flow cannot flank them.
		`fill 50 -59 10 50 -59 30 minecraft:stone`,
		`fill 54 -59 10 54 -59 30 minecraft:stone`,
		`fill 58 -59 10 58 -59 30 minecraft:stone`,
		`fill 62 -59 10 62 -59 30 minecraft:stone`,
		`fill 66 -59 10 66 -59 30 minecraft:stone`,
		`fill 70 -59 10 70 -59 30 minecraft:stone`,
		`fill 74 -59 10 74 -59 30 minecraft:stone`,
		`tp @s 58.5 -58 22.5`,
	}
	preProbes := []struct{ name, spec string }{
		{"dark frosted sits", "60 -59 20 minecraft:frosted_ice"},
		{"torch-far frosted sits", "72 -59 19 minecraft:frosted_ice"},
		{"pair donor already water", "52 -59 20 minecraft:water"},
		{"pair writer already water", "52 -59 21 minecraft:water"},
		{"line tail sits", "56 -59 22 minecraft:frosted_ice"},
	}
	midProbes := []struct{ name, spec string }{
		{"sky frosted melted", "60 -59 16 minecraft:water"},
		{"dark frosted holds", "60 -59 20 minecraft:frosted_ice"},
		{"torch-near melted", "64 -59 20 minecraft:water"},
		{"torch-mid melted", "68 -59 20 minecraft:water"},
		{"torch-far holds", "72 -59 19 minecraft:frosted_ice"},
		{"pair donor melted", "52 -59 20 minecraft:water"},
		{"pair writer melted", "52 -59 21 minecraft:water"},
		{"line head water", "56 -59 20 minecraft:water"},
		{"line mid water", "56 -59 21 minecraft:water"},
		{"line tail holds", "56 -59 22 minecraft:frosted_ice"},
	}
	nightProbes := []struct{ name, spec string }{
		{"night frosted holds", "56 -59 16 minecraft:frosted_ice"},
	}
	dayProbes := []struct{ name, spec string }{
		{"night frosted melts by day", "56 -59 16 minecraft:water"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZF-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZF-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZFK%d", i))
		marker := fmt.Sprintf("ZFK%d", i)
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZF-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZF-miss-%d", i)) {
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
		time.Sleep(6 * time.Second)

		var out []string
		for i, p := range preProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// onPlace's 60-120-tick arm plus three ageing rounds of 20-40
		// fit in ~12s of real ticks; give it room.
		time.Sleep(25 * time.Second)
		for i, p := range midProbes {
			out = append(out, probe(pw, sink, p.spec, i+len(preProbes)))
		}
		// Night pins the skyDarken half: exposed frosted holds at
		// midnight, melts when the sun comes back.
		sendCommand(t, pw, `time set midnight`)
		sendCommand(t, pw, `setblock 56 -59 16 minecraft:frosted_ice`)
		time.Sleep(18 * time.Second)
		for i, p := range nightProbes {
			out = append(out, probe(pw, sink, p.spec, i+len(preProbes)+len(midProbes)))
		}
		sendCommand(t, pw, `time set noon`)
		time.Sleep(18 * time.Second)
		for i, p := range dayProbes {
			out = append(out, probe(pw, sink, p.spec, i+len(preProbes)+len(midProbes)+len(nightProbes)))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(preProbes, midProbes...), nightProbes...), dayProbes...)
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
