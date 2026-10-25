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

// TestPoweredRailChainParity pins PoweredRailBlock.findPoweredRailSignal:
// a rail lights when a lit same-block rail sits within 8 links along
// the connected track — the 9-rail boost. Covers the flat reach, the
// 8-link cap, the slope climb (ascending shape steps up; a level step
// also checks the cell below), and the source's own flip on removal.
func TestPoweredRailChainParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,

		// A @70..80,z80: an 11-long flat EW chain with the lever
		// driving rail 70 — power reaches 8 links, the 9th stays dark.
		`setblock 70 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 71 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 72 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 73 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 74 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 75 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 76 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 77 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 78 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 79 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 80 -60 80 minecraft:powered_rail[shape=east_west]`,
		`setblock 69 -60 80 minecraft:lever[face=floor,powered=true]`,

		// B @85..91,z82: the chain climbs — a level step also probes
		// the cell below, an ascending step climbs one.
		`setblock 85 -60 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 86 -60 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 87 -60 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 88 -60 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 89 -60 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 90 -60 82 minecraft:powered_rail[shape=ascending_east]`,
		`setblock 91 -60 82 minecraft:stone`,
		`setblock 91 -59 82 minecraft:powered_rail[shape=east_west]`,
		`setblock 84 -60 82 minecraft:lever[face=floor,powered=true]`,

		// C @95,z84: a detector rail never answers a direct signal —
		// its neighborChanged is the minecart scan only.
		`setblock 95 -60 84 minecraft:detector_rail[shape=north_south]`,
		`setblock 94 -60 84 minecraft:lever[face=floor,powered=true]`,
	}
	probesA := []struct{ name, spec string }{
		{"A source lit", "70 -60 80 minecraft:powered_rail[powered=true]"},
		{"A link 7 lit", "77 -60 80 minecraft:powered_rail[powered=true]"},
		{"A link 8 lit", "78 -60 80 minecraft:powered_rail[powered=true]"},
		{"A link 9 dark", "79 -60 80 minecraft:powered_rail[powered=false]"},
		{"A link 10 dark", "80 -60 80 minecraft:powered_rail[powered=false]"},
		{"B flat link lit", "88 -60 82 minecraft:powered_rail[powered=true]"},
		{"B slope link lit", "90 -60 82 minecraft:powered_rail[powered=true]"},
		{"B climbed rail lit", "91 -59 82 minecraft:powered_rail[powered=true]"},
		{"C detector stays dark", "95 -60 84 minecraft:detector_rail[powered=false]"},
	}
	unpower := []string{
		`setblock 69 -60 80 minecraft:lever[face=floor,powered=false]`,
		`setblock 84 -60 82 minecraft:lever[face=floor,powered=false]`,
	}
	probesB := []struct{ name, spec string }{
		{"A source dark", "70 -60 80 minecraft:powered_rail[powered=false]"},
		{"A link 8 dark", "78 -60 80 minecraft:powered_rail[powered=false]"},
		{"B climbed rail dark", "91 -59 82 minecraft:powered_rail[powered=false]"},
		{"B slope dark", "90 -60 82 minecraft:powered_rail[powered=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZR-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZR-miss-%d", spec, i))
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZR-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZR-miss-%d", i)) {
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
		// The lit wave runs one rail per neighbour pass.
		time.Sleep(2500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range unpower {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(2500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append([]struct{ name, spec string }{}, probesA...), probesB...)
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

// TestTorchBurnoutParity pins RedstoneTorchBlock.RECENT_TOGGLES: the
// 8th lit→dark flip inside a 60-tick window burns the torch out —
// levelEvent 1502 + a 160-tick restart. While the window is saturated
// a relight is refused even unpowered; once it expires the restart
// tick relights. Toggling the mount cell (stone <-> redstone_block)
// drives the flips with plain setblock writes.
func TestTorchBurnoutParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,
		// D @50,z90: the burner — floor torch on a mount we flip.
		`setblock 50 -60 90 minecraft:redstone_torch`,
		// E @53,z90: a control that toggles only 3 times — under the
		// cap, so it always relights.
		`setblock 53 -60 90 minecraft:redstone_torch`,
	}
	probesA := []struct{ name, spec string }{
		{"D burnt out dark", "50 -60 90 minecraft:redstone_torch[lit=false]"},
		{"E control relit", "53 -60 90 minecraft:redstone_torch[lit=true]"},
	}
	probesB := []struct{ name, spec string }{
		{"D relit after restart", "50 -60 90 minecraft:redstone_torch[lit=true]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZT-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZT-miss-%d", spec, i))
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZT-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZT-miss-%d", i)) {
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
		time.Sleep(400 * time.Millisecond)
		// 8 off-transitions within the 60-tick (3s) window. Each
		// redstone_block write powers the mount → the torch flips
		// dark 2t later and records a toggle.
		for i := 0; i < 8; i++ {
			sendCommand(t, pw, `setblock 50 -61 90 minecraft:redstone_block`)
			time.Sleep(140 * time.Millisecond)
			sendCommand(t, pw, `setblock 50 -61 90 minecraft:stone`)
			time.Sleep(140 * time.Millisecond)
		}
		// Control: 3 toggles — relights fine.
		for i := 0; i < 3; i++ {
			sendCommand(t, pw, `setblock 53 -61 90 minecraft:redstone_block`)
			time.Sleep(140 * time.Millisecond)
			sendCommand(t, pw, `setblock 53 -61 90 minecraft:stone`)
			time.Sleep(140 * time.Millisecond)
		}
		time.Sleep(700 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// The restart lands ~160t (8s) after the last burnout flip.
		time.Sleep(8500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append([]struct{ name, spec string }{}, probesA...), probesB...)
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

// TestObserverParity pins ObserverBlock: updateShape arms a 2-tick
// scheduleTick when the cell the sensor faces changes while unpowered;
// the tick flips powered on, re-arms the off-tick 2t out, then flips
// off — flag-2 writes with updateNeighborsInFront doing the notify, so
// the pulse only reaches the cell behind the sensor. getSignal emits
// 15 toward FACING (the reader sits behind). Command writes also pulse
// — BlockInput.place folds the requested state through
// updateFromNeighbourShapes, and the dir==FACING step arms it — while
// a hand-placed observer stays dark. onPlace snaps a powered=true
// write back to unpowered.
func TestObserverParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,
		// A @60,z100: setblock'd observer facing east watches (61);
		// the write itself self-folds and pulses, driving the south
		// piston at the back cell — the stone lands one south before
		// the trigger even runs.
		`setblock 60 -60 100 minecraft:observer[facing=east]`,
		`setblock 59 -60 100 minecraft:piston[facing=south]`,
		`setblock 59 -60 101 minecraft:stone`,

		// B @64,z100: same rig — its setup pulse also lands. The
		// trigger then writes ABOVE the sensor (not the watched cell)
		// which must NOT arm a second pulse.
		`setblock 64 -60 100 minecraft:observer[facing=east]`,
		`setblock 63 -60 100 minecraft:piston[facing=south]`,
		`setblock 63 -60 101 minecraft:stone`,

		// C @67,z100: back cell piston+stone; the powered=true
		// setblock in the trigger phase snaps dark without pulsing.
		`setblock 67 -60 100 minecraft:piston[facing=south]`,
		`setblock 67 -60 101 minecraft:stone`,

		// D @73..75,z100: hand-placed observer rig — a placement does
		// NOT self-fold, so the piston stays put until the watched
		// cell (the clicked stone at 75) is rewritten.
		`setblock 75 -60 100 minecraft:stone`,
		`setblock 73 -60 100 minecraft:piston[facing=south]`,
		`setblock 73 -60 101 minecraft:stone`,

		// E @79,z100: watched-cell air write pulses the same way.
		`setblock 80 -60 100 minecraft:observer[facing=east]`,
		`setblock 81 -60 100 minecraft:stone`,
		`setblock 79 -60 100 minecraft:piston[facing=south]`,
		`setblock 79 -60 101 minecraft:stone`,
	}
	// probesA run after the trigger writes + settle. A refilled front
	// cell lets the watched-write pulse move the chain again — the
	// second stone landing at 103 is the witness.
	probesA := []struct{ name, spec string }{
		{"A setup pulse pushed", "59 -60 102 minecraft:stone"},
		{"A second pulse chain", "59 -60 103 minecraft:stone"},
		{"A front emptied", "59 -60 101 minecraft:air"},
		{"A piston retracted", "59 -60 100 minecraft:piston[extended=false]"},
		{"A observer dark", "60 -60 100 minecraft:observer[powered=false]"},

		{"B setup pulse pushed", "63 -60 102 minecraft:stone"},
		{"B above-write: no pulse", "63 -60 103 minecraft:air"},
		{"B stone still", "63 -60 101 minecraft:stone"},
		{"B piston closed", "63 -60 100 minecraft:piston[extended=false]"},
		{"B observer dark", "64 -60 100 minecraft:observer[powered=false]"},

		{"C powered snaps off", "68 -60 100 minecraft:observer[powered=false]"},
		{"C no push", "67 -60 102 minecraft:air"},
		{"C stone still", "67 -60 101 minecraft:stone"},

		{"E setup pulse pushed", "79 -60 102 minecraft:stone"},
		{"E air write chain", "79 -60 103 minecraft:stone"},
		{"E front emptied", "79 -60 101 minecraft:air"},
		{"E observer dark", "80 -60 100 minecraft:observer[powered=false]"},
	}
	// probesMid check the hand-placed observer BEFORE the poke: the
	// placement itself must not have pulsed.
	probesMid := []struct{ name, spec string }{
		{"D placed faces east", "74 -60 100 minecraft:observer[facing=east]"},
		{"D place did NOT pulse", "73 -60 101 minecraft:stone"},
		{"D back cell quiet", "73 -60 102 minecraft:air"},
	}
	probesB := []struct{ name, spec string }{
		{"D watched write pushed", "73 -60 102 minecraft:stone"},
		{"D observer dark", "74 -60 100 minecraft:observer[powered=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZO-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZO-miss-%d", spec, i))
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZO-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZO-miss-%d", i)) {
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
		// A: refill the front cell, then the watched write pulses.
		sendCommand(t, pw, `setblock 59 -60 101 minecraft:stone`)
		sendCommand(t, pw, `setblock 61 -60 100 minecraft:stone`)
		// B: refill, then write ABOVE the sensor — must not arm.
		sendCommand(t, pw, `setblock 63 -60 101 minecraft:stone`)
		sendCommand(t, pw, `setblock 64 -59 100 minecraft:stone`)
		// C: powered=true write snaps back dark.
		sendCommand(t, pw, `setblock 68 -60 100 minecraft:observer[facing=east,powered=true]`)
		// E: refill, then an air write on the watched cell pulses.
		sendCommand(t, pw, `setblock 79 -60 101 minecraft:stone`)
		sendCommand(t, pw, `setblock 81 -60 100 minecraft:air`)
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// D: hand-place the observer looking east on the stone's west
		// face — the sensor reads the look direction; the placement
		// itself must not pulse.
		sendCommand(t, pw, `item replace entity @s hotbar.0 with minecraft:observer 1`)
		sendCommand(t, pw, `tp @s 72.5 -60 100.5 -90 0`)
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 75, -60, 100, 4, 40)
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesMid {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		// Now the watched-cell write fires the placed observer.
		sendCommand(t, pw, `setblock 75 -60 100 minecraft:dirt`)
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 200+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append([]struct{ name, spec string }{}, probesA...), probesMid...), probesB...)
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

// TestDispenserDropperParity pins DispenserBlock.neighborChanged +
// dispenseFrom and the DropperBlock override: a rising signal edge
// (hasNeighborSignal on the cell OR the QC cell above) while
// TRIGGERED=false latches triggered and schedules the dispense 4
// ticks out; the falling edge clears the latch. The tick picks a
// random non-empty slot — empty stores fire the 1001 click and
// nothing else. A dropper inserts one item into the container at its
// mouth; a dispenser throws the item as an entity. Firing once per
// edge and the QC power read are the pinned behaviours; the per-item
// DispenseItemBehavior table is not part of this slice.
func TestDispenserDropperParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{
		`gamerule send_command_feedback true`,
		// A @60,z110: dropper east into a chest — one item per edge.
		`setblock 60 -60 110 minecraft:dropper[facing=east]`,
		`setblock 61 -60 110 minecraft:chest`,
		`item replace block 60 -60 110 container.0 with minecraft:stone 4`,
		// B @64,z110: dispenser east into open air — the item leaves
		// as an entity; the slot still drains by one.
		`setblock 64 -60 110 minecraft:dispenser[facing=east]`,
		`item replace block 64 -60 110 container.0 with minecraft:dirt 4`,
		// C @68,z110: the triggered latch — power held steady fires
		// once; a refill while powered does NOT refire.
		`setblock 68 -60 110 minecraft:dropper[facing=east]`,
		`setblock 69 -60 110 minecraft:chest`,
		`item replace block 68 -60 110 container.0 with minecraft:stone 4`,
		// D @72,z110: quasi-connectivity — a source two cells up
		// only powers the dropper when a separate update reaches it
		// (the stone kick below it); the latch then persists until a
		// powered update lands.
		`setblock 72 -60 110 minecraft:dropper[facing=east]`,
		`setblock 73 -60 110 minecraft:chest`,
		`item replace block 72 -60 110 container.0 with minecraft:stone 4`,
		// E @76,z110: dispenser — powers while empty (the 1001 click
		// fires nothing); a refill while powered still holds because
		// TRIGGERED never cleared; on repower it throws, not inserts.
		`setblock 76 -60 110 minecraft:dispenser[facing=east]`,
		`setblock 77 -60 110 minecraft:chest`,
	}
	probesA := []struct{ name, spec string }{
		{"A chest got one", "items block 61 -60 110 container.* minecraft:stone"},
		{"A dropper drained 1", "items block 60 -60 110 container.0 minecraft:stone[count=3]"},
		{"A triggered latched", "block 60 -60 110 minecraft:dropper[triggered=true]"},
		{"B dispenser drained 1", "items block 64 -60 110 container.0 minecraft:dirt[count=3]"},
		{"B triggered latched", "block 64 -60 110 minecraft:dispenser[triggered=true]"},
		{"B mouth is air", "block 65 -60 110 minecraft:air"},
		{"C one edge one item", "items block 69 -60 110 container.* minecraft:stone"},
		{"C drained 1", "items block 68 -60 110 container.0 minecraft:stone[count=3]"},
		{"D QC fired", "items block 73 -60 110 container.* minecraft:stone"},
		{"D drained 1", "items block 72 -60 110 container.0 minecraft:stone[count=3]"},
		{"E chest untouched", "!items block 77 -60 110 container.* minecraft:stone"},
		{"E triggered latched", "block 76 -60 110 minecraft:dispenser[triggered=true]"},
	}
	probesMid := []struct{ name, spec string }{
		// E's refill while powered must NOT fire — the latch held
		// through it.
		{"E refill held", "items block 76 -60 110 container.0 minecraft:stone[count=4]"},
	}
	probesB1 := []struct{ name, spec string }{
		// The identical-state rewrite below the dropper carries no
		// update: TRIGGERED still holds after unpowering.
		{"D identical noop", "block 72 -60 110 minecraft:dropper[triggered=true]"},
	}
	probesB := []struct{ name, spec string }{
		{"C repower fired", "items block 68 -60 110 container.0 minecraft:stone[count=2]"},
		{"C chest total 2", "items block 69 -60 110 container.* minecraft:stone[count=2]"},
		{"D triggered cleared", "block 72 -60 110 minecraft:dropper[triggered=false]"},
		{"E repower threw 1", "items block 76 -60 110 container.0 minecraft:stone[count=3]"},
		{"E chest still empty", "!items block 77 -60 110 container.* minecraft:stone"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		// A leading '!' pins the negative: the "ok" line comes from
		// execute unless instead of if.
		if strings.HasPrefix(spec, "!") {
			spec = spec[1:]
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZD-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZD-miss-%d", spec, i))
		} else {
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZD-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZD-miss-%d", spec, i))
		}
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZD-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZD-miss-%d", i)) {
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
		time.Sleep(400 * time.Millisecond)
		// Power A/B/C/E adjacent; D gets its QC source two up plus a
		// kick update directly below so the dropper re-evaluates.
		for _, cmd := range []string{
			`setblock 60 -61 110 minecraft:redstone_block`,
			`setblock 64 -61 110 minecraft:redstone_block`,
			`setblock 68 -61 110 minecraft:redstone_block`,
			`setblock 72 -58 110 minecraft:redstone_block`,
			`setblock 72 -61 110 minecraft:stone`,
			`setblock 76 -61 110 minecraft:redstone_block`,
		} {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// Refill E while powered — the held latch keeps it from
		// firing (probesMid) — then unpower everything. D's source
		// goes away but the dropper only learns of it through the
		// fresh kick below (rewriting the stone there), which also
		// drops its triggered latch without a fire.
		sendCommand(t, pw, `item replace block 76 -60 110 container.0 with minecraft:stone 4`)
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesMid {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		for _, cmd := range []string{
			`setblock 60 -61 110 minecraft:stone`,
			`setblock 64 -61 110 minecraft:stone`,
			`setblock 68 -61 110 minecraft:stone`,
			`setblock 72 -58 110 minecraft:stone`,
			// Identical-state setblock: vanilla's chunk write
			// early-outs — no update reaches the dropper, so the
			// latch must still hold (probesB2).
			`setblock 72 -61 110 minecraft:stone`,
			`setblock 76 -61 110 minecraft:stone`,
		} {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(800 * time.Millisecond)
		for i, p := range probesB1 {
			out = append(out, probe(pw, sink, p.spec, 200+i))
		}
		// A real state change (stone→dirt) finally updates the
		// dropper: powered is gone, the latch clears, no fire.
		sendCommand(t, pw, `setblock 72 -61 110 minecraft:dirt`)
		sendCommand(t, pw, `setblock 68 -61 110 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 76 -61 110 minecraft:redstone_block`)
		time.Sleep(1500 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 300+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(append(append([]struct{ name, spec string }{},
		probesA...), probesMid...), probesB1...), probesB...)
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

// TestDispenseProjectileParity pins the registerProjectileBehavior
// items: ProjectileDispenseBehavior spawns the projectile entity at
// center + facing*0.7 + (0,0.1,0) and shoots it along the facing
// (power 1.1). Up-facing dispensers give every type a long flight —
// phase A probes mid-flight existence, phase B pins the landing
// behaviour: arrows/spectral stick, thrown items break, the rocket
// pops on its fuse, and the armor stand (DispenseItemBehavior$1)
// appears in the mouth cell.
func TestDispenseProjectileParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	items := []struct {
		x         int
		item, ent string
	}{
		{60, "arrow", "arrow"},
		{64, "spectral_arrow", "spectral_arrow"},
		{68, "egg", "egg"},
		{72, "snowball", "snowball"},
		{76, "experience_bottle", "experience_bottle"},
		{80, "splash_potion", "splash_potion"},
		{84, "lingering_potion", "lingering_potion"},
		{88, "fire_charge", "small_fireball"},
		{92, "wind_charge", "wind_charge"},
		{96, "firework_rocket", "firework_rocket"},
	}
	setup := []string{`gamerule send_command_feedback true`}
	for _, it := range items {
		count := "4"
		if it.item == "splash_potion" || it.item == "lingering_potion" {
			// Stack size 1 — vanilla rejects the overstacked fill.
			count = "1"
		}
		spec := fmt.Sprintf("minecraft:%s", it.item)
		if it.item == "lingering_potion" {
			spec = `minecraft:lingering_potion[minecraft:potion_contents={potion:"minecraft:poison"}]`
		}
		setup = append(setup,
			fmt.Sprintf(`setblock %d -60 120 minecraft:dispenser[facing=up]`, it.x),
			fmt.Sprintf(`item replace block %d -60 120 container.0 with %s %s`, it.x, spec, count),
		)
	}
	// The armor stand is not a projectile — it spawns in the mouth
	// cell (DispenseItemBehavior$1), so its dispenser faces east.
	setup = append(setup,
		`setblock 100 -60 120 minecraft:dispenser[facing=east]`,
		`item replace block 100 -60 120 container.0 with minecraft:armor_stand 4`,
		// Fireball ignite: east-facing dispenser with a wall two out.
		`setblock 84 -60 140 minecraft:dispenser[facing=east]`,
		`setblock 86 -59 140 minecraft:oak_log`,
		`item replace block 84 -60 140 container.0 with minecraft:fire_charge 1`,
		`setblock 90 -60 140 minecraft:dispenser[facing=up]`,
		`setblock 90 -58 140 minecraft:oak_log`,
		`item replace block 90 -60 140 container.0 with minecraft:fire_charge 1`,
	)

	probesA := []struct{ name, spec string }{}
	for _, it := range items {
		probesA = append(probesA, struct{ name, spec string }{
			it.item + " mid-flight",
			fmt.Sprintf("entity @e[type=minecraft:%s]", it.ent),
		})
	}
	probesA = append(probesA,
		struct{ name, spec string }{
			"armor_stand spawned",
			"entity @e[type=minecraft:armor_stand,x=101,y=-60,z=120,distance=..2]",
		})
	probesB := []struct{ name, spec string }{
		{"arrow stuck stays", "entity @e[type=minecraft:arrow]"},
		{"spectral stuck stays", "entity @e[type=minecraft:spectral_arrow]"},
		{"egg broke", "!entity @e[type=minecraft:egg]"},
		{"snowball broke", "!entity @e[type=minecraft:snowball]"},
		{"xp bottle broke", "!entity @e[type=minecraft:experience_bottle]"},
		{"splash broke", "!entity @e[type=minecraft:splash_potion]"},
		{"lingering broke", "!entity @e[type=minecraft:lingering_potion]"},
		{"rocket popped", "!entity @e[type=minecraft:firework_rocket]"},
		{"fireball still flying", "entity @e[type=minecraft:small_fireball]"},
		{"armor_stand persists", "entity @e[type=minecraft:armor_stand,x=101,y=-60,z=120,distance=..2]"},
		{"lingering cloud formed", "entity @e[type=minecraft:area_effect_cloud]"},
		{"fireball ignites the log face", "block 85 -59 140 minecraft:fire"},
		{"under-log shot ignites below", "block 90 -59 140 minecraft:fire"},
		{"drain arrow", "!items block 60 -60 120 container.* minecraft:arrow[count=4]"},
		{"drain egg", "!items block 68 -60 120 container.* minecraft:egg[count=4]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		if strings.HasPrefix(spec, "!") {
			spec = spec[1:]
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZP-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZP-miss-%d", spec, i))
		} else {
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZP-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZP-miss-%d", spec, i))
		}
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
			time.Sleep(30 * time.Millisecond)
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
		time.Sleep(400 * time.Millisecond)
		for _, it := range items {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -61 120 minecraft:redstone_block`, it.x))
		}
		sendCommand(t, pw, `setblock 100 -61 120 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 84 -61 140 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 90 -61 140 minecraft:redstone_block`)
		// Mid-flight window: +4t dispense, ~450ms later everything is
		// still airborne (or, for the fireball/wind charge, flying).
		time.Sleep(500 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		// Landing outcomes need the projectiles to have come down —
		// power 1.1 up takes ~3.7s of rise+fall; 4.5s covers it plus
		// the rocket fuse.
		time.Sleep(4200 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append([]struct{ name, spec string }{}, probesA...), probesB...)
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

// TestDispenseBehaviorParity pins the non-projectile dispense
// behaviors: filled buckets emptyContents at the mouth, empty
// buckets BucketPickup against a source, minecarts need a rail at
// the mouth (else the item throws), boats need water (else the
// item throws), and flint&steel primes TNT or lights fire.
func TestDispenseBehaviorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	setup := []string{`gamerule send_command_feedback true`,
		`setblock 60 -60 130 minecraft:dispenser[facing=east]`,
		`item replace block 60 -60 130 container.0 with minecraft:water_bucket 1`,
		`setblock 64 -60 134 minecraft:dispenser[facing=south]`,
		`item replace block 64 -60 134 container.0 with minecraft:lava_bucket 1`,
		`setblock 68 -60 130 minecraft:dispenser[facing=east]`,
		`setblock 69 -60 130 minecraft:water`,
		// pen the source — open water floods east onto the rail
		// cell before the bucket pickup fires.
		`setblock 70 -60 130 minecraft:stone`,
		`setblock 69 -60 129 minecraft:stone`,
		`setblock 69 -60 131 minecraft:stone`,
		`item replace block 68 -60 130 container.0 with minecraft:bucket 2`,
		`setblock 72 -60 130 minecraft:dispenser[facing=east]`,
		`setblock 73 -60 130 minecraft:powered_rail`,
		`item replace block 72 -60 130 container.0 with minecraft:minecart 1`,
		`setblock 76 -60 130 minecraft:dispenser[facing=east]`,
		`item replace block 76 -60 130 container.0 with minecraft:chest_minecart 1`,
		`setblock 80 -60 130 minecraft:dispenser[facing=east]`,
		`setblock 81 -60 130 minecraft:water`,
		`item replace block 80 -60 130 container.0 with minecraft:oak_boat 1`,
		`setblock 84 -60 130 minecraft:dispenser[facing=east]`,
		`item replace block 84 -60 130 container.0 with minecraft:spruce_boat 1`,
		`setblock 88 -60 130 minecraft:dispenser[facing=east]`,
		`item replace block 88 -60 130 container.0 with minecraft:flint_and_steel 1`,
		`setblock 92 -60 130 minecraft:dispenser[facing=east]`,
		`setblock 93 -60 130 minecraft:tnt`,
		`item replace block 92 -60 130 container.0 with minecraft:flint_and_steel 1`,
		`setblock 60 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 60 -60 140 container.0 with minecraft:tnt 1`,
		`setblock 64 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 64 -60 140 container.0 with minecraft:pig_spawn_egg 1`,
		`setblock 68 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 68 -60 140 container.0 with minecraft:white_shulker_box 1`,
		`setblock 72 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 72 -60 140 container.0 with minecraft:tnt 1`,
		`setblock 73 -60 140 minecraft:stone`,
		`setblock 89 -60 140 minecraft:dispenser[facing=east]`,
		`setblock 90 -61 140 minecraft:farmland`,
		`setblock 90 -60 140 minecraft:wheat[age=6]`,
		`item replace block 89 -60 140 container.0 with minecraft:bone_meal 1`,
		// tryShearEntity: a sheep in the mouth loses its wool.
		`summon minecraft:sheep 85.5 -60 141.5 {NoAI:1b}`,
		`setblock 84 -60 141 minecraft:dispenser[facing=east]`,
		`item replace block 84 -60 141 container.0 with minecraft:shears 1`,
		// bottle half of $9: honey_level>=5 fills a honey_bottle and
		// resets the hive (bee release deferred).
		`setblock 88 -60 141 minecraft:dispenser[facing=east]`,
		`setblock 89 -60 141 minecraft:beehive[honey_level=5]`,
		`item replace block 88 -60 141 container.0 with minecraft:glass_bottle 1`,
		// Equippable saddle slot → Saddle flag on a saddleable mount.
		`summon minecraft:pig 85.5 -60 142.5 {NoAI:1b}`,
		`setblock 84 -60 142 minecraft:dispenser[facing=east]`,
		`item replace block 84 -60 142 container.0 with minecraft:saddle 1`,
		// $2: chest → ChestedHorse flag on a TAMED chestless donkey.
		`summon minecraft:donkey 89.5 -60 142.5 {NoAI:1b,Tame:1b}`,
		`setblock 88 -60 142 minecraft:dispenser[facing=east]`,
		`item replace block 88 -60 142 container.0 with minecraft:chest 1`,
		// snow_golem.shear: the carved pumpkin pops off as an item.
		`summon minecraft:snow_golem 85.5 -60 143.5 {NoAI:1b}`,
		`setblock 84 -60 143 minecraft:dispenser[facing=east]`,
		`item replace block 84 -60 143 container.0 with minecraft:shears 1`,
		// bogged.shear: 2 rolls x {1 brown + 1 red mushroom}.
		`summon minecraft:bogged 89.5 -60 143.5 {NoAI:1b}`,
		`setblock 88 -60 143 minecraft:dispenser[facing=east]`,
		`item replace block 88 -60 143 container.0 with minecraft:shears 1`,
		// mooshroom.shear: converts to cow + 5 red mushrooms.
		`summon minecraft:mooshroom 93.5 -60 143.5 {NoAI:1b}`,
		`setblock 92 -60 143 minecraft:dispenser[facing=east]`,
		`item replace block 92 -60 143 container.0 with minecraft:shears 1`,
		// $11: brushing an armadillo drops one scute and wears the
		// brush by 16.
		`summon minecraft:armadillo 86.5 -60 140.5 {NoAI:1b}`,
		`setblock 85 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 85 -60 140 container.0 with minecraft:brush 1`,
		// EquipmentDispenseItemBehavior: a living entity in the mouth
		// cell wears the dispensed armor.
		`summon minecraft:pig 80.5 -60 140.5 {NoAI:1b,CanPickUpLoot:1b}`,
		`setblock 79 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 79 -60 140 container.0 with minecraft:iron_helmet 2`,
		// $8: a carved pumpkin that completes the snow column spawns a
		// golem; the same pumpkin over a bare mouth stays in the slot.
		`setblock 84 -59 140 minecraft:dispenser[facing=down]`,
		`setblock 84 -61 140 minecraft:snow_block`,
		`setblock 84 -62 140 minecraft:snow_block`,
		`item replace block 84 -59 140 container.0 with minecraft:carved_pumpkin 1`,
		`setblock 88 -59 140 minecraft:dispenser[facing=down]`,
		`item replace block 88 -59 140 container.0 with minecraft:carved_pumpkin 1`,
		`setblock 60 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 61 -60 150 minecraft:water`,
		`item replace block 60 -60 150 container.0 with minecraft:glass_bottle 2`,
		`setblock 64 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 65 -60 150 minecraft:respawn_anchor[charges=1]`,
		`item replace block 64 -60 150 container.0 with minecraft:glowstone 2`,
		`setblock 68 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 69 -60 150 minecraft:copper_block`,
		`item replace block 68 -60 150 container.0 with minecraft:honeycomb 1`,
		`setblock 72 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 73 -60 150 minecraft:beehive[honey_level=5]`,
		`item replace block 72 -60 150 container.0 with minecraft:shears 1`,
		`setblock 76 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 77 -60 150 minecraft:dirt`,
		`item replace block 76 -60 150 container.0 with minecraft:potion[minecraft:potion_contents={potion:"minecraft:water"}] 1`,
		`setblock 80 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 81 -60 150 minecraft:dirt`,
		`item replace block 80 -60 150 container.0 with minecraft:potion[minecraft:potion_contents={potion:"minecraft:leaping"}] 1`,
		`setblock 84 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 85 -61 150 minecraft:farmland`,
		`setblock 85 -60 150 minecraft:wheat[age=6]`,
		`item replace block 84 -60 150 container.0 with minecraft:bone_meal 1`,
		`setblock 88 -60 150 minecraft:dispenser[facing=east]`,
		`setblock 89 -61 150 minecraft:farmland`,
		`setblock 89 -60 150 minecraft:wheat[age=7]`,
		`item replace block 88 -60 150 container.0 with minecraft:bone_meal 1`,
		// $8: carved pumpkin that completes the snow column spawns a
		// golem; the same pumpkin over a bare mouth stays in the slot.
		`setblock 92 -59 150 minecraft:dispenser[facing=down]`,
		`setblock 92 -60 150 minecraft:snow_block`,
		`setblock 92 -61 150 minecraft:snow_block`,
		`item replace block 92 -59 150 container.0 with minecraft:carved_pumpkin 1`,
		`setblock 96 -59 150 minecraft:dispenser[facing=down]`,
		`item replace block 96 -59 150 container.0 with minecraft:carved_pumpkin 1`,
		// $8 equipment fallback: a pumpkin that can't golem-place
		// equips a mouth entity's head slot instead.
		`summon minecraft:pig 97.5 -60 140.5 {NoAI:1b,CanPickUpLoot:1b}`,
		`setblock 96 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 96 -60 140 container.0 with minecraft:carved_pumpkin 1`,
		// $7 fallback: same rule for the wither skeleton skull.
		`summon minecraft:pig 101.5 -60 140.5 {NoAI:1b,CanPickUpLoot:1b}`,
		`setblock 100 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 100 -60 140 container.0 with minecraft:wither_skeleton_skull 1`,
		// canDispenserEquipIntoSlot for a Mob is canPickUpLoot — a
		// no-pickup mob can't take the item, so the equipment
		// behavior falls through to the default throw.
		`summon minecraft:pig 105.5 -60 140.5 {NoAI:1b}`,
		`setblock 104 -60 140 minecraft:dispenser[facing=east]`,
		`item replace block 104 -60 140 container.0 with minecraft:iron_helmet 1`,
	}

	probesA := []struct{ name, spec string }{
		{"A bucket still loaded", `items block 60 -60 130 container.* minecraft:water_bucket`},
		{"C water source waiting", `block 69 -60 130 minecraft:water`},
		{"D rail in place", `block 73 -60 130 minecraft:powered_rail`},
		{"H TNT waiting", `block 93 -60 130 minecraft:tnt`},
		{"R0 water potion loaded", `items block 76 -60 150 container.* minecraft:potion`},
		{"S0 leaping potion loaded", `items block 80 -60 150 container.* minecraft:potion`},
	}
	probesB := []struct{ name, spec string }{
		{"A water at the mouth", `block 61 -60 130 minecraft:water`},
		{"A slot now an empty bucket", `items block 60 -60 130 container.* minecraft:bucket`},
		{"B lava at the mouth", `block 64 -60 135 minecraft:lava`},
		{"!C source consumed (reflood is flowing)", `!block 69 -60 130 minecraft:water[level=0]`},
		{"C slot now a water bucket", `items block 68 -60 130 container.* minecraft:water_bucket`},
		{"C one empty bucket remains", `items block 68 -60 130 container.* minecraft:bucket[count=1]`},
		{"D minecart on the rail", `entity @e[type=minecraft:minecart,x=73,y=-60,z=130,distance=..4]`},
		{"D slot drained", `!items block 72 -60 130 container.* minecraft:minecart`},
		{"!E no rail: no cart entity", `!entity @e[type=minecraft:chest_minecart,x=77,y=-60,z=130,distance=..4]`},
		{"E cart thrown as an item", `entity @e[type=minecraft:item,x=77,y=-60,z=130,distance=..4]`},
		{"F boat floats on the mouth water", `entity @e[type=minecraft:oak_boat,x=81,y=-60,z=130,distance=..4]`},
		{"F boat slot drained", `!items block 80 -60 130 container.* minecraft:oak_boat`},
		{"!G dry mouth: no spruce boat", `!entity @e[type=minecraft:spruce_boat,x=85,y=-60,z=130,distance=..4]`},
		{"G boat thrown as an item", `entity @e[type=minecraft:item,x=85,y=-60,z=130,distance=..4]`},
		{"H TNT block primed away", `block 93 -60 130 minecraft:air`},
		{"H primed tnt at the mouth", `entity @e[type=minecraft:tnt,x=93,y=-60,z=130,distance=..4]`},
		{"I dispensed tnt primes at the mouth", `entity @e[type=minecraft:tnt,x=61,y=-60,z=140,distance=..4]`},
		{"I tnt slot drained", `!items block 60 -60 140 container.* minecraft:tnt`},
		{"J pig spawn egg spawns the mob", `entity @e[type=minecraft:pig,x=65,y=-60,z=140,distance=..4]`},
		{"J egg slot drained", `!items block 64 -60 140 container.* minecraft:pig_spawn_egg`},
		{"K shulker box placed at the mouth", `block 69 -60 140 minecraft:white_shulker_box`},
		{"K box slot drained", `!items block 68 -60 140 container.* minecraft:white_shulker_box`},
		{"L tnt primes even into a solid mouth", `entity @e[type=minecraft:tnt,x=73,y=-60,z=140,distance=..4]`},

		{"M0 dispenser exists", `block 60 -60 150 minecraft:dispenser`},
		{"M0c bottle count (fired→1,unfired→2)", `items block 60 -60 150 container.* minecraft:glass_bottle[count=2]`},
		{"M slot now a water bottle", `items block 60 -60 150 container.* minecraft:potion`},
		{"M one glass bottle remains", `items block 60 -60 150 container.* minecraft:glass_bottle[count=1]`},
		{"N anchor charged to 2", `block 65 -60 150 minecraft:respawn_anchor[charges=2]`},
		{"N glowstone shrunk to 1", `items block 64 -60 150 container.* minecraft:glowstone[count=1]`},
		{"O copper waxed", `block 69 -60 150 minecraft:waxed_copper_block`},
		{"O honeycomb slot drained", `!items block 68 -60 150 container.* minecraft:honeycomb`},
		{"Q beehive emptied", `block 73 -60 150 minecraft:beehive[honey_level=0]`},
		{"Q shears stayed in the slot", `items block 72 -60 150 container.* minecraft:shears`},
		{"R dirt became mud", `block 77 -60 150 minecraft:mud`},
		{"R slot holds the empty bottle", `items block 76 -60 150 container.* minecraft:glass_bottle`},
		{"!S non-water potion: no mud", `!block 81 -60 150 minecraft:mud`},
		{"T bonemeal clamps wheat to max", `block 85 -60 150 minecraft:wheat[age=7]`},
		{"T meal consumed", `!items block 84 -60 150 container.* minecraft:bone_meal`},
		{"U max-age wheat: meal kept", `items block 88 -60 150 container.* minecraft:bone_meal`},
		{"T bonemeal clamps wheat to max", `block 90 -60 140 minecraft:wheat[age=7]`},
		{"T meal consumed", `!items block 89 -60 140 container.* minecraft:bone_meal`},
		{"Z wool dropped at sheep", `entity @e[type=minecraft:item,x=85,y=-60,z=141,distance=..3]`},
		{"Z sheep reads sheared", `entity @e[type=minecraft:sheep,nbt={Sheared:1b},x=85,y=-60,z=141,distance=..2]`},
		{"Z shears stay (238 max)", `items block 84 -60 141 container.* minecraft:shears`},
		{"Zz hive emptied", `block 89 -60 141 minecraft:beehive[honey_level=0]`},
		{"Zz honey bottle back", `items block 88 -60 141 container.* minecraft:honey_bottle`},
		{"Zz bottle drained", `!items block 88 -60 141 container.* minecraft:glass_bottle`},
		{"Aa pig saddled", `items entity @e[type=minecraft:pig,x=85,y=-60,z=142,distance=..2] saddle minecraft:saddle`},
		{"Aa saddle consumed", `!items block 84 -60 142 container.* minecraft:saddle`},
		{"Ab chest consumed", `!items block 88 -60 142 container.* minecraft:chest`},
		{"Ac pumpkin popped", `entity @e[type=minecraft:item,x=85,y=-60,z=143,distance=..3]`},
		{"Ac golem reads pumpkinless", `entity @e[type=minecraft:snow_golem,nbt={Pumpkin:0b},x=85,y=-60,z=143,distance=..2]`},
		{"Ad bogged shrooms popped", `entity @e[type=minecraft:item,x=89,y=-60,z=143,distance=..3]`},
		{"Ad bogged reads sheared", `entity @e[type=minecraft:bogged,nbt={sheared:1b},x=89,y=-60,z=143,distance=..2]`},
		{"Ae mooshroom converted", `entity @e[type=minecraft:cow,x=93,y=-60,z=143,distance=..2]`},
		{"Ae mooshroom gone", `!entity @e[type=minecraft:mooshroom,x=93,y=-60,z=143,distance=..5]`},
		{"Ae mooshroom shrooms", `entity @e[type=minecraft:item,x=93,y=-60,z=143,distance=..3]`},
		{"Y scute dropped at armadillo", `entity @e[type=minecraft:item,x=86,y=-60,z=140,distance=..3]`},
		{"Y brush stays (worn, not broken)", `items block 85 -60 140 container.* minecraft:brush`},
		{"X pig wears the helmet", `items entity @e[type=minecraft:pig,x=80,y=-60,z=140,distance=..2] armor.head minecraft:iron_helmet`},
		{"X one helmet left", `items block 79 -60 140 container.* minecraft:iron_helmet[count=1]`},
		{"X2 dispenser exists", `block 79 -60 140 minecraft:dispenser`},
		{"X2 unfired: still 2 helmets", `items block 79 -60 140 container.* minecraft:iron_helmet[count=2]`},
		{"X2 pig present", `entity @e[type=minecraft:pig,x=80,y=-60,z=140,distance=..2]`},
		{"V snow golem spawned", `entity @e[type=minecraft:snow_golem]`},
		{"V pattern aired out", `!block 84 -61 140 minecraft:snow_block`},
		{"W bare mouth keeps pumpkin", `items block 88 -59 140 container.* minecraft:carved_pumpkin`},
		{"W no pumpkin placed", `!block 88 -60 140 minecraft:carved_pumpkin`},
		{"S thrown as an item instead", `entity @e[type=minecraft:item,x=81,y=-60,z=150,distance=..6]`},
		{"S potion drained", `!items block 80 -60 150 container.* minecraft:potion`},
		{"Y pig wears the pumpkin", `items entity @e[type=minecraft:pig,x=97,y=-60,z=140,distance=..2] armor.head minecraft:carved_pumpkin`},
		{"Y pumpkin slot drained", `!items block 96 -60 140 container.* minecraft:carved_pumpkin`},
		{"Z pig wears the skull", `items entity @e[type=minecraft:pig,x=101,y=-60,z=140,distance=..2] armor.head minecraft:wither_skeleton_skull`},
		{"Z skull slot drained", `!items block 100 -60 140 container.* minecraft:wither_skeleton_skull`},
		{"Yx no-pickup pig stays bare", `!items entity @e[type=minecraft:pig,x=105,y=-60,z=140,distance=..2] armor.head minecraft:iron_helmet`},
		{"Yx helmet thrown as an item", `entity @e[type=minecraft:item,x=105,y=-60,z=140,distance=..4]`},
		{"Yx throw drained the slot", `!items block 104 -60 140 container.* minecraft:iron_helmet`},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		if strings.HasPrefix(spec, "!") {
			spec = spec[1:]
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZD2-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZD2-miss-%d", spec, i))
		} else {
			sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZD2-ok-%d", spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZD2-miss-%d", spec, i))
		}
		deadline := time.Now().Add(4 * time.Second)
		hit := ""
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZD2-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZD2-miss-%d", i)) {
					hit = "miss"
				}
			}
			if hit != "" {
				break
			}
			time.Sleep(30 * time.Millisecond)
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
		// Spawn sits ~100 blocks from the fixture rows — hover the probe
		// client over their middle so every cell lands inside the loaded
		// chunk ring (otherwise edge cells silently refuse setblock).
		sendCommand(t, pw, `tp @s 80 -59 140`)
		time.Sleep(4 * time.Second)
		sink.take()
		var out []string
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
			time.Sleep(25 * time.Millisecond)
		}
		time.Sleep(600 * time.Millisecond)
		for i, p := range probesA {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, x := range []int{60, 68, 72, 76, 80, 84, 88, 92} {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -60 129 minecraft:redstone_block`, x))
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -60 129 minecraft:air`, x))
		}
		sendCommand(t, pw, `setblock 64 -60 133 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 64 -60 133 minecraft:air`)
		for _, x := range []int{60, 64, 68, 72} {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -60 139 minecraft:redstone_block`, x))
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -60 139 minecraft:air`, x))
		}
		// The $7/$8 equip-fallback pair keep their power on — a pulse
		// re-fire would dispense a second item and confuse the probes.
		sendCommand(t, pw, `setblock 96 -60 139 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 100 -60 139 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 104 -60 139 minecraft:redstone_block`)
		// Persistent floor power for the z=140 bonemeal pair; side cells
		// for the two down-facing golem dispensers.
		sendCommand(t, pw, `setblock 89 -61 140 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 79 -61 140 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 85 -61 140 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 84 -61 141 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 88 -61 141 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 84 -61 142 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 88 -61 142 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 84 -61 143 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 88 -61 143 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 92 -61 143 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 83 -59 140 minecraft:redstone_block`)
		sendCommand(t, pw, `setblock 87 -59 140 minecraft:redstone_block`)
		for _, x := range []int{60, 64, 68, 72, 76, 80} {
			sendCommand(t, pw, fmt.Sprintf(`setblock %d -61 150 minecraft:redstone_block`, x))
			time.Sleep(30 * time.Millisecond)
		}
		// +4t dispense delay, then the primed TNT fuse starts.
		time.Sleep(1200 * time.Millisecond)
		for i, p := range probesB {
			out = append(out, probe(pw, sink, p.spec, 100+i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append([]struct{ name, spec string }{}, probesA...), probesB...)
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
