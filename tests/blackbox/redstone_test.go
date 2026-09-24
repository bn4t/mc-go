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

// TestRedstoneWireParity pins the DefaultRedstoneWireEvaluator model:
//
//   - target = max(block signal, incoming wire − 1) — the 15→1 decay
//     down a dust line, lamp lit at the end via the powered mount
//   - getConnectingSide folding: dot stays dot when isolated, pair
//     arms, climb onto a solid (west=up), drop off a ledge
//   - wire emission carve-outs: down into the mount (strong power,
//     wall torch unlatches), never up (lamp above stays dark), and
//     only through connected arms (lamp/rail beside an unconnected
//     arm stay dark)
//   - the placed-vs-setblock quirk: a hand-placed lone dust folds
//     from crossState into a +, a setblock'd one stays a dot
func TestRedstoneWireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,

		// z=44: powered dust line 61..75 from a lever — 15,14,...,1 —
		// and the lamp beside the powered mount at the end.
		`fill 61 -60 44 75 -60 44 minecraft:redstone_wire`,
		`setblock 76 -61 44 minecraft:redstone_lamp`,
		`setblock 60 -60 44 minecraft:lever[face=floor,powered=true]`,

		// z=46: fold probes — an isolated dust stays a dot, a pair
		// arms toward each other only.
		`setblock 80 -60 46 minecraft:redstone_wire`,
		`setblock 82 -60 46 minecraft:redstone_wire`,
		`setblock 81 -60 46 minecraft:redstone_wire`,

		// z=48: the line climbs over a solid and drops back down.
		`fill 86 -60 48 86 -60 48 minecraft:redstone_wire`,
		`setblock 87 -60 48 minecraft:stone`,
		`setblock 87 -59 48 minecraft:redstone_wire`,
		`setblock 88 -60 48 minecraft:redstone_wire`,
		`setblock 88 -61 49 minecraft:redstone_lamp`,
		`setblock 85 -60 48 minecraft:lever[face=floor,powered=true]`,

		// z=50: dust on a solid strong-powers it (wall torch mounted
		// on it unlatches) but never emits upward (lamp above dark).
		`setblock 96 -60 50 minecraft:stone`,
		`setblock 96 -59 50 minecraft:redstone_wire`,
		`setblock 97 -60 50 minecraft:redstone_wall_torch[facing=east]`,
		`setblock 96 -58 50 minecraft:redstone_lamp`,
		`setblock 95 -59 50 minecraft:redstone_block`,

		// z=52: removal — lever off collapses the whole line.
		`fill 100 -60 52 102 -60 52 minecraft:redstone_wire`,
		`setblock 99 -60 52 minecraft:lever[face=floor,powered=true]`,

		// z=54: ledge drop + dot/cross quirk + rail/lamp beside an
		// unconnected arm.
		`setblock 110 -60 54 minecraft:stone`,
		`setblock 110 -59 54 minecraft:redstone_wire`,
		`setblock 111 -60 54 minecraft:redstone_wire`,
		`setblock 108 -60 54 minecraft:redstone_wire`, // isolated dot
		`setblock 115 -60 54 minecraft:powered_rail`,
		`setblock 116 -60 54 minecraft:redstone_lamp`,
		`setblock 114 -60 54 minecraft:redstone_wire`,
		`setblock 113 -60 54 minecraft:lever[face=floor,powered=true]`,

		// z=56: a hand-flipped lever — the flip's updateShape folds
		// the dot's arm before its power reaches the rail.
		`setblock 120 -60 56 minecraft:redstone_wire`,
		`setblock 121 -60 56 minecraft:powered_rail`,
		`setblock 119 -60 56 minecraft:lever[face=floor]`,
	}

	probesA := []struct{ name, spec string }{
		{"chain head power 15", "61 -60 44 minecraft:redstone_wire[power=15]"},
		{"chain mid power 6", "70 -60 44 minecraft:redstone_wire[power=6]"},
		{"chain tail power 1", "75 -60 44 minecraft:redstone_wire[power=1]"},
		{"end mount lamp lit", "76 -61 44 minecraft:redstone_lamp[lit=true]"},
		{"isolated dust stays dot", "108 -60 54 minecraft:redstone_wire[north=none,south=none,east=none,west=none]"},
		{"pair left dust both arms", "80 -60 46 minecraft:redstone_wire[east=side,west=side,north=none,south=none]"},
		{"pair middle both arms", "81 -60 46 minecraft:redstone_wire[west=side,east=side]"},
		{"climb arm up", "88 -60 48 minecraft:redstone_wire[west=up]"},
		{"ledge drop arm", "110 -59 54 minecraft:redstone_wire[east=side]"},
		{"climbed dust powered", "88 -60 48 minecraft:redstone_wire[power=13]"},
		{"climb end lamp lit", "88 -61 49 minecraft:redstone_lamp[lit=true]"},
		{"wall torch unlatched", "97 -60 50 minecraft:redstone_wall_torch[lit=false]"},
		{"lamp above dust dark", "96 -58 50 minecraft:redstone_lamp[lit=false]"},
		{"rail lit by survivor arm", "115 -60 54 minecraft:powered_rail[powered=true]"},
		{"lamp beside dust dark", "116 -60 54 minecraft:redstone_lamp[lit=false]"},
		{"lever-side dust power 15", "114 -60 54 minecraft:redstone_wire[power=15]"},
	}
	poke := []string{
		`setblock 99 -60 52 minecraft:air`,
	}
	probesB := []struct{ name, spec string }{
		{"line collapsed head", "100 -60 52 minecraft:redstone_wire[power=0]"},
		{"line collapsed tail", "102 -60 52 minecraft:redstone_wire[power=0]"},
	}
	probesC := []struct{ name, spec string }{
		{"hand-placed dust is cross", "118 -60 54 minecraft:redstone_wire[north=side,south=side,east=side,west=side]"},
	}
	probesD := []struct{ name, spec string }{
		{"flip folds dust east", "120 -60 56 minecraft:redstone_wire[east=side]"},
		{"flip dust power 15", "120 -60 56 minecraft:redstone_wire[power=15]"},
		{"flip lights rail", "121 -60 56 minecraft:powered_rail[powered=true]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZW-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZW-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZWK%d", i))
		marker := fmt.Sprintf("ZWK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZW-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZW-miss-%d", i)) {
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
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range poke {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		// Hand-place a lone dust: give redstone, stand at the spot,
		// click the grass top face.
		sendCommand(t, pw, `give @s minecraft:redstone`)
		sendCommand(t, pw, `tp @s 118.5 -59 55.5`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 118, -61, 54, 1, 20)
		time.Sleep(500 * time.Millisecond)
		for i, p := range probesC {
			out = append(out, probe(pw, sink, p.spec, 200+i))
		}
		// Hand-flip the lever: the updateNeighbourShapes pass folds
		// the dust's east arm before the chained power update reaches
		// the rail.
		sendCommand(t, pw, `tp @s 120.5 -59 57.5`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 119, -60, 56, 1, 21)
		time.Sleep(500 * time.Millisecond)
		for i, p := range probesD {
			out = append(out, probe(pw, sink, p.spec, 220+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(probesA, probesB...), probesC...), probesD...)
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
