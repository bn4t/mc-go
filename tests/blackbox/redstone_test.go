//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestRedstoneParity exercises the 26.2 SignalGetter model end-to-end
// through command writes and hand use:
//
//   - ownSignal / getSignal / getDirectSignal carve-outs (lever pushes
//     its mount face, torch inverts on a powered mount, wire-free
//     power into lamps, rails, doors, TNT)
//   - updateNeighborsAt's first-order fan-out — including the quirk
//     that a setblock'd source does NOT wake consumers that sit two
//     blocks away behind the mount
//   - the torch's own place/remove ring-2 notification
//   - scheduled-tick latencies: lamp unlatch 4t, torch toggle 2t,
//     stone-button release 20t
//
// All reads are `execute if block` — the state on the wire is the
// probe, identical against vanilla and mc-go.
func TestRedstoneParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	// Fixture rows along x at z=40, surface layer -60, props on -59.
	setup := []string{
		`gamerule send_command_feedback true`,

		// A: lever beside lamp — lamp lights instantly when the lever
		// write fans out, unlatches 4 ticks after unpowering.
		`setblock 41 -59 40 minecraft:redstone_lamp`,
		`setblock 40 -60 40 minecraft:stone`,
		`setblock 40 -59 40 minecraft:lever[face=floor,powered=true]`,

		// B: lamp beside the MOUNT (two from the lever) stays dark —
		// setblock's first-order fan-out never reaches it. Rewriting
		// the mount wakes the lamp and it sees the strong power.
		`setblock 45 -60 40 minecraft:redstone_lamp`,
		`setblock 44 -60 40 minecraft:stone`,
		`setblock 44 -59 40 minecraft:lever[face=floor,powered=true]`,

		// C: redstone block beside a lamp.
		`setblock 49 -60 40 minecraft:redstone_lamp`,
		`setblock 48 -60 40 minecraft:redstone_block`,

		// D: torch on a mount. A powered lever attached to the mount
		// lights nothing by itself (torch is 2-away, never notified),
		// but the next write at the mount makes the torch re-check
		// hasSignal(mount, DOWN) and unlatch 2 ticks later.
		`setblock 52 -60 40 minecraft:stone`,
		`setblock 52 -59 40 minecraft:redstone_torch`,
		`setblock 53 -60 40 minecraft:lever[face=wall,facing=east,powered=true]`,

		// E: iron door + adjacent powered lever -> open+powered.
		`setblock 56 -60 40 minecraft:iron_door`,
		`setblock 57 -60 40 minecraft:lever[face=floor,powered=true]`,

		// F: powered rail beside a powered lever.
		`setblock 60 -60 40 minecraft:powered_rail`,
		`setblock 61 -60 40 minecraft:lever[face=floor,powered=true]`,

		// G: TNT next to a powered lever primes and self-removes.
		`setblock 64 -60 40 minecraft:tnt`,
		`setblock 65 -60 40 minecraft:lever[face=floor,powered=true]`,

		// H: torch setblock'd onto a powered mount — the torch's own
		// ring-2 notify reaches back to it and it unlatches.
		`setblock 68 -60 40 minecraft:redstone_block`,
		`setblock 68 -59 40 minecraft:redstone_torch`,

		// I: hand-use targets — unpowered floor lever + stone button.
		`setblock 72 -60 40 minecraft:lever[face=floor]`,
		`setblock 76 -60 40 minecraft:stone_button[face=floor]`,
	}
	// Phase 1 probes: after setup settles (~2t for torch schedules).
	probesA := []struct{ name, spec string }{
		{"lamp beside lever lit", "41 -59 40 minecraft:redstone_lamp[lit=true]"},
		{"lamp beside mount dark (quirk)", "45 -60 40 minecraft:redstone_lamp[lit=false]"},
		{"lamp beside redstone block lit", "49 -60 40 minecraft:redstone_lamp[lit=true]"},
		{"torch still lit (quirk)", "52 -59 40 minecraft:redstone_torch[lit=true]"},
		{"torch on powered mount off", "68 -59 40 minecraft:redstone_torch[lit=false]"},
		{"iron door open", "56 -60 40 minecraft:iron_door[open=true]"},
		{"rail powered", "60 -60 40 minecraft:powered_rail[powered=true]"},
		{"tnt primed to air", "64 -60 40 minecraft:air"},
	}
	// Phase 2: poke writes that wake the quirked consumers.
	poke := []string{
		`setblock 44 -60 40 minecraft:dirt`, // mount write -> lamp sees strong power
		`setblock 52 -60 40 minecraft:dirt`, // mount write -> torch sees strong power
		`setblock 48 -60 40 minecraft:air`,  // remove redstone block
		`setblock 57 -60 40 minecraft:lever[face=floor,powered=false]`,
		`setblock 61 -60 40 minecraft:lever[face=floor,powered=false]`,
		`setblock 40 -59 40 minecraft:lever[face=floor,powered=false]`,
	}
	probesB := []struct{ name, spec string }{
		{"mount-side lamp now lit", "45 -60 40 minecraft:redstone_lamp[lit=true]"},
		{"torch unlatched on power", "52 -59 40 minecraft:redstone_torch[lit=false]"},
		{"lamp beside removed block dark", "49 -60 40 minecraft:redstone_lamp[lit=false]"},
		{"lamp beside lever dark", "41 -59 40 minecraft:redstone_lamp[lit=false]"},
		{"iron door closed", "56 -60 40 minecraft:iron_door[open=false]"},
		{"rail unpowered", "60 -60 40 minecraft:powered_rail[powered=false]"},
	}
	// Phase 3: hand use — lever flips powered, stone button presses
	// then releases on its own 20-tick schedule.
	probesC1 := []struct{ name, spec string }{
		{"lever pressed on", "72 -60 40 minecraft:lever[powered=true]"},
		{"button pressed on", "76 -60 40 minecraft:stone_button[powered=true]"},
	}
	probesC2 := []struct{ name, spec string }{
		{"lever stays on", "72 -60 40 minecraft:lever[powered=true]"},
		{"button released", "76 -60 40 minecraft:stone_button[powered=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZR-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZR-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZRK%d", i))
		marker := fmt.Sprintf("ZRK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZR-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZR-miss-%d", i)) {
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
		var out []string
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		// Let the scheduled ticks land: torch self-check (2t), lamp
		// unlatch (4t), TNT fuse start.
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range poke {
			sendCommand(t, pw, cmd)
		}
		// 4t lamp unlatch + 2t torch toggle need ~300ms minimum.
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		// Hand use: stand within 5.5 of each click and press.
		sendCommand(t, pw, `tp @s 74 -59 42`)
		time.Sleep(300 * time.Millisecond)
		sendUseItemOn(t, pw, 72, -60, 40, 1, 10)
		sendUseItemOn(t, pw, 76, -60, 40, 1, 11)
		time.Sleep(400 * time.Millisecond)
		for i, p := range probesC1 {
			out = append(out, probe(pw, sink, p.spec, 200+i))
		}
		// 20 ticks for the stone button release.
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesC2 {
			out = append(out, probe(pw, sink, p.spec, 220+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(probesA, probesB...), probesC1...), probesC2...)
	for i, p := range all {
		t.Logf("%-40s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
