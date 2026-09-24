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

// TestRepeaterParity pins the 26.2 DiodeBlock/RepeaterBlock model:
//
//   - powered flips go through the scheduled-tick queue (2t × DELAY),
//     armed by neighborChanged — a setblock'd diode does NOT re-check
//     its input until some neighbour write wakes it (the quirk)
//   - emission is direction-gated (getSignal: FACING == dir): dust on
//     the output side reads 15, a block on the output side never feeds
//     the input, side cells read nothing
//   - onPlace → updateNeighborsInFront runs on EVERY write: the output
//     dust's power refreshes on the powered flip itself
//   - RepeaterBlock locking: a powered side-diode sets LOCKED and
//     freezes powered; unlocking re-arms the pending flip
//   - support loss pops the diode (updateShape DOWN / neighborChanged
//     canSurvive) to air
//   - useWithoutItem cycles DELAY 1→2 on a hand click
//
// All reads are `execute if block` — state on the wire is the probe,
// identical against vanilla and mc-go.
func TestRepeaterParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,

		// A @41,z60: the source block's own write fans out and NCs the
		// diode — flips on 2t later, dust on the output reads 15.
		`setblock 41 -60 60 minecraft:repeater[facing=west]`,
		`setblock 42 -60 60 minecraft:redstone_wire`,
		`setblock 40 -60 60 minecraft:redstone_block`,

		// B @45,z60: input AND dust written before the diode — the
		// diode's own write never self-NCs, so it stays dark until a
		// side poke. Its setblock is LAST so no later write wakes it.
		`setblock 44 -60 60 minecraft:redstone_block`,
		`setblock 46 -60 60 minecraft:redstone_wire`,

		// C @49,z60: a block on the OUTPUT side is not an input.
		`setblock 49 -60 60 minecraft:repeater[facing=west]`,
		`setblock 50 -60 60 minecraft:redstone_block`,

		// D @53,z60: locking. Side diode @53,59 facing=north outputs
		// south into the main diode's side; block @53,58 powers it.
		`setblock 53 -60 60 minecraft:repeater[facing=west]`,
		`setblock 53 -60 59 minecraft:repeater[facing=north]`,
		`setblock 53 -60 58 minecraft:redstone_block`,

		// E @56,-58: diode on a removable stone pedestal.
		`setblock 56 -59 60 minecraft:stone`,
		`setblock 56 -58 60 minecraft:repeater[facing=west]`,

		// F @59,z64: useWithoutItem delay-cycle target.
		`setblock 59 -60 64 minecraft:repeater[facing=west]`,

		// B's diode is the last write — every setup write after it
		// would NC it and arm the flip, defeating the quirk probe.
		`setblock 45 -60 60 minecraft:repeater[facing=west]`,
	}
	probesA := []struct{ name, spec string }{
		{"A diode powered", "41 -60 60 minecraft:repeater[powered=true]"},
		{"A dust power 15", "42 -60 60 minecraft:redstone_wire[power=15]"},
		{"B diode still dark (no self-NC)", "45 -60 60 minecraft:repeater[powered=false]"},
		{"B dust unpowered", "46 -60 60 minecraft:redstone_wire[power=0]"},
		{"C output-side block not input", "49 -60 60 minecraft:repeater[powered=false]"},
		{"D side diode powered", "53 -60 59 minecraft:repeater[powered=true]"},
		{"D main locked", "53 -60 60 minecraft:repeater[locked=true]"},
		{"D main dark while locked", "53 -60 60 minecraft:repeater[powered=false]"},
		{"E diode on pedestal", "56 -58 60 minecraft:repeater"},
	}
	poke1 := []string{
		`setblock 45 -60 61 minecraft:stone`,          // wakes B
		`setblock 52 -60 60 minecraft:redstone_block`, // D main input — locked, stays off
	}
	probesB := []struct{ name, spec string }{
		{"B powered after poke", "45 -60 60 minecraft:repeater[powered=true]"},
		{"B dust power 15", "46 -60 60 minecraft:redstone_wire[power=15]"},
		{"D input ignored while locked", "53 -60 60 minecraft:repeater[powered=false]"},
		{"D still locked", "53 -60 60 minecraft:repeater[locked=true]"},
	}
	poke2 := []string{
		`setblock 53 -60 58 minecraft:air`, // unlock D — side diode dies
		`setblock 56 -59 60 minecraft:air`, // E support gone
		`setblock 40 -60 60 minecraft:air`, // A input off
	}
	probesC := []struct{ name, spec string }{
		{"D unlocked", "53 -60 60 minecraft:repeater[locked=false]"},
		{"D powered after unlock", "53 -60 60 minecraft:repeater[powered=true]"},
		{"E popped to air", "56 -58 60 minecraft:air"},
		{"A unpowered", "41 -60 60 minecraft:repeater[powered=false]"},
		{"A dust collapsed", "42 -60 60 minecraft:redstone_wire[power=0]"},
	}
	probesD := []struct{ name, spec string }{
		{"F delay cycled to 2", "59 -60 64 minecraft:repeater[delay=2]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZD-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZD-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZDK%d", i))
		marker := fmt.Sprintf("ZDK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZD-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZD-miss-%d", i)) {
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
		// Diode flips land on the tick queue (2t at DELAY=1); the side
		// diode's write then folds LOCKED into the main diode.
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range poke1 {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		for _, cmd := range poke2 {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesC {
			out = append(out, probe(pw, sink, p.spec, 200+i))
		}
		// Hand-use: click the F repeater's top face within reach.
		sendCommand(t, pw, `tp @s 59 -59 66`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 59, -60, 64, 1, 30)
		time.Sleep(500 * time.Millisecond)
		for i, p := range probesD {
			out = append(out, probe(pw, sink, p.spec, 300+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(probesA, probesB...), probesC...), probesD...)
	for i, p := range all {
		t.Logf("%-42s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}

// TestComparatorParity pins ComparatorBlock's analog path: the
// block-entity-backed output level (emission is the stored calc, not
// a constant 15), container/analog input reads, the conductor deep
// read plus the updateNeighbourForOutputSignal wake that feeds it,
// side-signal COMPARE vs SUBTRACT, and the use-cycle that recomputes
// on the spot.
func TestComparatorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,

		// A @71,z60: plain powered passthrough — block input gives
		// 15, dust on the output reads the stored 15.
		`setblock 71 -60 60 minecraft:comparator[facing=west]`,
		`setblock 72 -60 60 minecraft:redstone_wire`,
		`setblock 70 -60 60 minecraft:redstone_block`,

		// B @81,z60: analog read of a composter's level prop.
		`setblock 81 -60 60 minecraft:comparator[facing=west]`,
		`setblock 82 -60 60 minecraft:redstone_wire`,
		`setblock 80 -60 60 minecraft:composter[level=3]`,

		// C @90,z60: COMPARE with a weaker side signal — a six-cell
		// dust chain off a block feeds 10 into the north side while
		// the input block gives 15.
		`setblock 90 -60 60 minecraft:comparator[facing=west]`,
		`setblock 91 -60 60 minecraft:redstone_wire`,
		`setblock 89 -60 60 minecraft:redstone_block`,
		`setblock 90 -60 53 minecraft:redstone_block`,
		`setblock 90 -60 54 minecraft:redstone_wire`,
		`setblock 90 -60 55 minecraft:redstone_wire`,
		`setblock 90 -60 56 minecraft:redstone_wire`,
		`setblock 90 -60 57 minecraft:redstone_wire`,
		`setblock 90 -60 58 minecraft:redstone_wire`,
		`setblock 90 -60 59 minecraft:redstone_wire`,

		// D @96,z60: SUBTRACT with the side dominating — input 4 from
		// a composter, side 15 from a block, output stays dark.
		`setblock 96 -60 60 minecraft:comparator[facing=west,mode=subtract]`,
		`setblock 97 -60 60 minecraft:redstone_wire`,
		`setblock 95 -60 60 minecraft:composter[level=4]`,
		`setblock 96 -60 59 minecraft:redstone_block`,

		// E @99..102,z60: the conductor deep read — comparator ->
		// stone -> chest, plus the output-signal wake when the
		// chest's contents change with no neighbour block write.
		`setblock 101 -60 60 minecraft:comparator[facing=west]`,
		`setblock 102 -60 60 minecraft:redstone_wire`,
		`setblock 100 -60 60 minecraft:stone`,
		`setblock 99 -60 60 minecraft:chest`,
	}
	probesA := []struct{ name, spec string }{
		{"A powered", "71 -60 60 minecraft:comparator[powered=true]"},
		{"A dust power 15", "72 -60 60 minecraft:redstone_wire[power=15]"},
		{"B powered on composter 3", "81 -60 60 minecraft:comparator[powered=true]"},
		{"B dust power 3", "82 -60 60 minecraft:redstone_wire[power=3]"},
		{"C powered compare 15>10", "90 -60 60 minecraft:comparator[powered=true]"},
		{"C dust power 15", "91 -60 60 minecraft:redstone_wire[power=15]"},
		{"D subtract side wins: dark", "96 -60 60 minecraft:comparator[powered=false]"},
		{"D dust power 0", "97 -60 60 minecraft:redstone_wire[power=0]"},
		{"E chest empty: dark", "101 -60 60 minecraft:comparator[powered=false]"},
		{"E dust power 0", "102 -60 60 minecraft:redstone_wire[power=0]"},
	}
	poke1 := []string{
		`item replace block 99 -60 60 container.0 with minecraft:dirt 64`,
	}
	probesB := []struct{ name, spec string }{
		{"E powered after fill", "101 -60 60 minecraft:comparator[powered=true]"},
		{"E dust power 1", "102 -60 60 minecraft:redstone_wire[power=1]"},
		{"C mode cycled subtract", "90 -60 60 minecraft:comparator[mode=subtract]"},
		{"C dust power 5 subtracted", "91 -60 60 minecraft:redstone_wire[power=5]"},
	}
	probesC := []struct{ name, spec string }{
		{"E dust power 15 full", "102 -60 60 minecraft:redstone_wire[power=15]"},
		{"E dark after chest break", "101 -60 60 minecraft:comparator[powered=false]"},
		{"E dust collapsed", "102 -60 60 minecraft:redstone_wire[power=0]"},
		{"D still dark", "96 -60 60 minecraft:comparator[powered=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZC-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZC-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZCK%d", i))
		marker := fmt.Sprintf("ZCK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZC-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZC-miss-%d", i)) {
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
		for _, cmd := range poke1 {
			sendCommand(t, pw, cmd)
		}
		// Hand-use: click the C comparator's top face within reach —
		// useWithoutItem cycles the mode and refreshes in place.
		sendCommand(t, pw, `tp @s 90 -59 62`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 90, -60, 60, 1, 30)
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		for slot := 1; slot <= 26; slot++ {
			sendCommand(t, pw, fmt.Sprintf("item replace block 99 -60 60 container.%d with minecraft:dirt 64", slot))
		}
		time.Sleep(1200 * time.Millisecond)
		out = append(out, probe(pw, sink, probesC[0].spec, 200))
		sendCommand(t, pw, `setblock 99 -60 60 minecraft:air`)
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesC[1:] {
			out = append(out, probe(pw, sink, p.spec, 201+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(probesA, probesB...), probesC[0]), probesC[1:]...)
	for i, p := range all {
		t.Logf("%-42s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}

// TestPistonParity pins PistonBaseBlock + PistonStructureResolver:
// quasi-connectivity power, extend/retract through the tick-tail
// block event queue, the 12-block push cap, DESTROY pops, BLOCK
// vetoes (obsidian, block entities, extended pistons), PUSH_ONLY
// glazed terracotta, slime/honey drag-and-branch, and the sticky
// pull. Fixtures all sit inside ticking range of spawn.
func TestPistonParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,

		// A @70,z70: plain powered extension.
		`setblock 70 -60 70 minecraft:piston[facing=east]`,
		`setblock 69 -60 70 minecraft:lever[face=floor,powered=true]`,

		// B @73,z70: a lone stone is pushed one cell.
		`setblock 73 -60 70 minecraft:piston[facing=east]`,
		`setblock 74 -60 70 minecraft:stone`,
		`setblock 72 -60 70 minecraft:lever[face=floor,powered=true]`,

		// C @76,z70: obsidian vetoes the move entirely.
		`setblock 76 -60 70 minecraft:piston[facing=east]`,
		`setblock 77 -60 70 minecraft:obsidian`,
		`setblock 76 -60 71 minecraft:lever[face=floor,powered=true]`,

		// D @79,z70: a torch in the path is destroyed, not pushed.
		`setblock 79 -60 70 minecraft:piston[facing=east]`,
		`setblock 80 -60 70 minecraft:torch`,
		`setblock 78 -60 70 minecraft:lever[face=floor,powered=true]`,

		// E @82,z70: a 12-deep chain still moves.
		`setblock 82 -60 70 minecraft:piston[facing=east]`,
		`setblock 83 -60 70 minecraft:stone`,
		`setblock 84 -60 70 minecraft:stone`,
		`setblock 85 -60 70 minecraft:stone`,
		`setblock 86 -60 70 minecraft:stone`,
		`setblock 87 -60 70 minecraft:stone`,
		`setblock 88 -60 70 minecraft:stone`,
		`setblock 89 -60 70 minecraft:stone`,
		`setblock 90 -60 70 minecraft:stone`,
		`setblock 91 -60 70 minecraft:stone`,
		`setblock 92 -60 70 minecraft:stone`,
		`setblock 93 -60 70 minecraft:stone`,
		`setblock 94 -60 70 minecraft:stone`,
		`setblock 81 -60 70 minecraft:lever[face=floor,powered=true]`,

		// F @97,z70: the 13th block is over the cap — nothing moves.
		`setblock 97 -60 70 minecraft:piston[facing=east]`,
		`setblock 98 -60 70 minecraft:stone`,
		`setblock 99 -60 70 minecraft:stone`,
		`setblock 100 -60 70 minecraft:stone`,
		`setblock 101 -60 70 minecraft:stone`,
		`setblock 102 -60 70 minecraft:stone`,
		`setblock 103 -60 70 minecraft:stone`,
		`setblock 104 -60 70 minecraft:stone`,
		`setblock 105 -60 70 minecraft:stone`,
		`setblock 106 -60 70 minecraft:stone`,
		`setblock 107 -60 70 minecraft:stone`,
		`setblock 108 -60 70 minecraft:stone`,
		`setblock 109 -60 70 minecraft:stone`,
		`setblock 110 -60 70 minecraft:stone`,
		`setblock 96 -60 70 minecraft:lever[face=floor,powered=true]`,

		// G @70,z74: a sticky piston pulls its block back.
		`setblock 70 -60 74 minecraft:sticky_piston[facing=east]`,
		`setblock 71 -60 74 minecraft:stone`,
		`setblock 69 -60 74 minecraft:lever[face=floor,powered=true]`,

		// H @74,z74: glazed terracotta pushes but never pulls.
		`setblock 74 -60 74 minecraft:sticky_piston[facing=east]`,
		`setblock 75 -60 74 minecraft:white_glazed_terracotta`,
		`setblock 73 -60 74 minecraft:lever[face=floor,powered=true]`,

		// I @78,z74: slime drags its perpendicular neighbours along —
		// the slime above and the stone north of it — but honey never
		// sticks to slime. The whole row floats one cell up so the
		// slime does not drag the floor row over the push cap.
		`setblock 78 -59 74 minecraft:piston[facing=east]`,
		`setblock 79 -59 74 minecraft:slime_block`,
		`setblock 79 -58 74 minecraft:slime_block`,
		`setblock 79 -59 73 minecraft:stone`,
		`setblock 79 -59 75 minecraft:honey_block`,
		`setblock 77 -60 74 minecraft:stone`,
		`setblock 77 -59 74 minecraft:lever[face=floor,powered=true]`,

		// J @82,z74: a chest is unpushable (it carries a block
		// entity) — the piston stays closed.
		`setblock 82 -60 74 minecraft:piston[facing=east]`,
		`setblock 83 -60 74 minecraft:chest`,
		`setblock 81 -60 74 minecraft:lever[face=floor,powered=true]`,

		// K @86,z74: an unextended piston is itself pushable.
		`setblock 86 -60 74 minecraft:piston[facing=east]`,
		`setblock 87 -60 74 minecraft:piston[facing=east]`,
		`setblock 85 -60 74 minecraft:lever[face=floor,powered=true]`,

		// L @92,z74: quasi-connectivity — a power source two cells up
		// powers the ring above the piston, but the piston only sees it
		// after a neighbour update (the dummy stone write).
		`setblock 92 -60 74 minecraft:piston[facing=east]`,
		`setblock 92 -58 74 minecraft:redstone_block`,
		`setblock 92 -60 73 minecraft:stone`,

		// M @97,z74: an already-extended piston is unpushable — B's
		// own power keeps it out, so A can never open.
		`setblock 97 -60 74 minecraft:piston[facing=east]`,
		`setblock 98 -60 74 minecraft:piston[facing=east]`,
		`setblock 98 -59 74 minecraft:redstone_block`,
		`setblock 96 -60 74 minecraft:lever[face=floor,powered=true]`,
	}
	probesA := []struct{ name, spec string }{
		{"A extended", "70 -60 70 minecraft:piston[extended=true]"},
		{"A head placed", "71 -60 70 minecraft:piston_head[facing=east,type=normal]"},
		{"B stone pushed +1", "75 -60 70 minecraft:stone"},
		{"B head fills origin", "74 -60 70 minecraft:piston_head[facing=east]"},
		{"C obsidian vetoes", "76 -60 70 minecraft:piston[extended=false]"},
		{"C obsidian still", "77 -60 70 minecraft:obsidian"},
		{"D torch destroyed", "80 -60 70 minecraft:piston_head[facing=east]"},
		{"D extended", "79 -60 70 minecraft:piston[extended=true]"},
		{"E 12-deep tail lands", "95 -60 70 minecraft:stone"},
		{"E head over origin", "83 -60 70 minecraft:piston_head[facing=east]"},
		{"E far end stays empty", "96 -60 70 minecraft:lever[powered=true]"},
		{"F 13-deep: closed", "97 -60 70 minecraft:piston[extended=false]"},
		{"F chain untouched", "98 -60 70 minecraft:stone"},
		{"F tail untouched", "110 -60 70 minecraft:stone"},
		{"G stone pushed to +2", "72 -60 74 minecraft:stone"},
		{"G sticky head", "71 -60 74 minecraft:piston_head[type=sticky]"},
		{"H terracotta pushed", "76 -60 74 minecraft:white_glazed_terracotta"},
		{"H sticky head", "75 -60 74 minecraft:piston_head[type=sticky]"},
		{"I slime front lands", "80 -59 74 minecraft:slime_block"},
		{"I slime above dragged", "80 -58 74 minecraft:slime_block"},
		{"I stone side-dragged", "80 -59 73 minecraft:stone"},
		{"I honey unstuck", "79 -59 75 minecraft:honey_block"},
		{"J chest unpushable", "82 -60 74 minecraft:piston[extended=false]"},
		{"J chest still", "83 -60 74 minecraft:chest"},
		{"K piston pushed east", "88 -60 74 minecraft:piston[facing=east,extended=false]"},
		{"K head at origin", "87 -60 74 minecraft:piston_head[facing=east]"},
		{"L QC extended", "92 -60 74 minecraft:piston[extended=true]"},
		{"L QC head", "93 -60 74 minecraft:piston_head[facing=east]"},
		{"M A cannot push B", "97 -60 74 minecraft:piston[extended=false]"},
		{"M B stayed extended", "98 -60 74 minecraft:piston[extended=true]"},
		{"M B head", "99 -60 74 minecraft:piston_head[facing=east]"},
	}
	unpower := []string{
		`setblock 69 -60 70 minecraft:lever[face=floor,powered=false]`,
		`setblock 72 -60 70 minecraft:lever[face=floor,powered=false]`,
		`setblock 69 -60 74 minecraft:lever[face=floor,powered=false]`,
		`setblock 73 -60 74 minecraft:lever[face=floor,powered=false]`,
	}
	probesQc := []struct{ name, spec string }{
		{"L QC latched extended", "92 -60 74 minecraft:piston[extended=true]"},
	}
	qcPoke := []string{
		`setblock 92 -60 73 minecraft:air`,
	}
	probesB := []struct{ name, spec string }{
		{"A retracted", "70 -60 70 minecraft:piston[extended=false]"},
		{"A head gone", "71 -60 70 minecraft:air"},
		{"B retracted", "73 -60 70 minecraft:piston[extended=false]"},
		{"B stone stays out", "75 -60 70 minecraft:stone"},
		{"B head gone", "74 -60 70 minecraft:air"},
		{"G stone pulled back", "71 -60 74 minecraft:stone"},
		{"G +2 cell cleared", "72 -60 74 minecraft:air"},
		{"G retracted", "70 -60 74 minecraft:sticky_piston[extended=false]"},
		{"H terracotta NOT pulled", "76 -60 74 minecraft:white_glazed_terracotta"},
		{"H head cell empty", "75 -60 74 minecraft:air"},
		{"L QC retracted", "92 -60 74 minecraft:piston[extended=false]"},
		{"L head gone", "93 -60 74 minecraft:air"},
	}
	// probesC run after a hand-placement: a piston placed while
	// looking straight down points up.
	probePlace := struct{ name, spec string }{
		"placed looking down faces up", "60 -60 74 minecraft:piston[facing=up]",
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZP-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZP-miss-%d", spec, i))
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZP-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZP-miss-%d", i)) {
					hit = "miss"
				}
			}
			if hit != "" {
				break
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
		for _, cmd := range unpower {
			sendCommand(t, pw, cmd)
		}
		// Removing QC power alone must not retract — the piston only
		// re-checks when a neighbour update lands.
		sendCommand(t, pw, `setblock 92 -58 74 minecraft:air`)
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesQc {
			out = append(out, probe(pw, sink, p.spec, 150+i))
		}
		for _, cmd := range qcPoke {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		// Hand placement: hold a piston on hotbar.0, stand on the
		// pad looking straight down, click the grass top.
		sendCommand(t, pw, `item replace entity @s hotbar.0 with minecraft:piston 1`)
		sendCommand(t, pw, `tp @s 60 -59 74 -90 90`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 60, -61, 74, 1, 40)
		time.Sleep(1200 * time.Millisecond)
		out = append(out, probe(pw, sink, probePlace.spec, 300))
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(append([]struct{ name, spec string }{}, probesA...), probesQc...), probesB...), probePlace)
	for i, p := range all {
		t.Logf("%-44s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
