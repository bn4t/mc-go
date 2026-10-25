package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFrostWalkerParity pins the frost_walker enchantment's
// location_changed effect: a grounded position packet freezes a disk
// of water one layer under the feet to frosted_ice[age=0], radius
// clamp(3 + (level-1), 0, 16) measured from the entity position
// (distToCenterSqr < r^2). The air-born flag gates it, removing the
// boots stops it, and a disk border cell separates level 1 (r3) from
// level 2 (r4). The probes run at night so daylight never melts the
// frosted ice between write and read.
func TestFrostWalkerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{
			"ops.json": opsJSONFor("P"),
		}, nil)
	defer targets.cleanup()

	setup := []string{
		`fill 95 -62 120 103 -62 138 minecraft:stone`,
		`fill 95 -61 120 103 -61 138 minecraft:water`,
		`fill 95 -60 120 103 -58 138 minecraft:air`,
		`time set night`,
		`tp P 99.5 -60 130.5`,
	}

	// movePos sends a PlayMovePosSB — doubles xyz then the onGround
	// flag byte (0x01 grounded).
	legs := []struct {
		name string
		// pre commands run before the move; moveto is nil to stay put.
		pre     []string
		moveto  *[4]float64 // x, y, z, ground
		probe   string
		reverse bool // probe is an `unless` — vanilla and mc-go both miss
	}{
		{"bare feet no freeze", nil, &[4]float64{99.5, -60, 130.5, 1},
			`execute unless block 99 -61 130 minecraft:frosted_ice run say ZW-ok-%d`, true},
		{"boots on freeze", []string{`item replace entity P armor.feet with minecraft:leather_boots[enchantments={frost_walker:1}]`}, &[4]float64{99.5, -60, 131.5, 1},
			`execute if block 99 -61 131 minecraft:frosted_ice run say ZW-ok-%d`, false},
		{"r3 edge in", nil, nil,
			`execute if block 99 -61 133 minecraft:frosted_ice run say ZW-ok-%d`, false},
		{"r3 edge out", nil, nil,
			`execute unless block 99 -61 134 minecraft:frosted_ice run say ZW-ok-%d`, true},
		{"airborne no freeze", nil, &[4]float64{99.5, -60, 124.5, 0},
			`execute unless block 99 -61 124 minecraft:frosted_ice run say ZW-ok-%d`, true},
		{"grounded same spot", nil, &[4]float64{99.5, -60, 124.5, 1},
			`execute if block 99 -61 124 minecraft:frosted_ice run say ZW-ok-%d`, false},
		{"boots off", []string{`item replace entity P armor.feet with minecraft:iron_boots`}, &[4]float64{99.5, -60, 121.5, 1},
			`execute unless block 99 -61 121 minecraft:frosted_ice run say ZW-ok-%d`, true},
		{"level2 r4", []string{`item replace entity P armor.feet with minecraft:leather_boots[enchantments={frost_walker:2}]`}, &[4]float64{99.5, -60, 131.5, 1},
			`execute if block 99 -61 134 minecraft:frosted_ice run say ZW-ok-%d`, false},
		{"r4 edge out", nil, nil,
			`execute unless block 99 -61 135 minecraft:frosted_ice run say ZW-ok-%d`, true},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(3 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(800 * time.Millisecond)

		movePos := func(x, y, z float64, ground bool) {
			var b protocol.Buffer
			b.Double(x)
			b.Double(y)
			b.Double(z)
			if ground {
				b.Byte(0x01)
			} else {
				b.Byte(0x00)
			}
			if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}

		var out []string
		for i, leg := range legs {
			for _, cmd := range leg.pre {
				sendCommand(t, pw, cmd)
			}
			if leg.moveto != nil {
				movePos(leg.moveto[0], leg.moveto[1], leg.moveto[2], leg.moveto[3] != 0)
			}
			// Vanilla's location_changed runs on the entity tick; give
			// the packet->tick->write chain a full second.
			time.Sleep(1000 * time.Millisecond)
			sendCommand(t, pw, fmt.Sprintf(leg.probe, i))
			sendCommand(t, pw, fmt.Sprintf("say ZWK%d", i))
			marker := fmt.Sprintf("ZWK%d", i)
			hit := "miss"
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						done = true
					}
					if strings.Contains(line, fmt.Sprintf("ZW-ok-%d", i)) {
						hit = "hit"
					}
				}
				if done {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, leg := range legs {
		t.Logf("%-22s vanilla=%s mcgo=%s", leg.name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %q: vanilla %s, mc-go %s", leg.name, vanilla[i], mcgo[i])
		}
	}
}
