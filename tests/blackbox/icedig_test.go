//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestIceDigMeltParity is IceBlock.playerDestroy (measured on the 26.2
// jar): the survival dig path — but not the creative one, whose
// destroyBlock returns before playerDestroy — refills an ice or
// frosted_ice cell with a water source when the cell below could hold
// it (blocksMotion or liquid). Silk touch suppresses the melt, an
// air gap below suppresses it, packed_ice is a different class, and in
// the nether WATER_EVAPORATES leaves the cell empty.
func TestIceDigMeltParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZB-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZB-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZBK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZBK%d", i)) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZB-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZB-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// digAt drives one survival start->settle->finish dig at (x,y,z).
	digAt := func(pw *protocol.Writer, x, y, z int) {
		sendDig(t, pw, 0, int32(x), int32(y), int32(z), 1)
		time.Sleep(900 * time.Millisecond)
		sendDig(t, pw, 2, int32(x), int32(y), int32(z), 2)
		time.Sleep(300 * time.Millisecond)
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

		// One shared pad; the floating case punches its support out.
		for _, cmd := range []string{
			`fill 19 -63 59 41 -62 61 minecraft:stone`,
			`setblock 28 -62 60 minecraft:air`,
			`setblock 20 -61 60 minecraft:ice`,
			`setblock 24 -61 60 minecraft:ice`,
			`setblock 28 -61 60 minecraft:ice`,
			`setblock 32 -61 60 minecraft:packed_ice`,
			`setblock 36 -61 60 minecraft:frosted_ice`,
			`setblock 40 -61 60 minecraft:ice`,
			`tp @s 20.5 -60 60.5`,
		} {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(700 * time.Millisecond)

		var out []string
		// Still creative here: the break itself runs but the
		// playerDestroy melt does not — the cell must end air.
		sendDig(t, pw, 0, 20, -61, 60, 1)
		time.Sleep(800 * time.Millisecond)
		out = append(out, probe(pw, sink, "20 -61 60 minecraft:air", 0))

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:iron_pickaxe 1")
		time.Sleep(300 * time.Millisecond)

		// Plain dig over stone: melts into a water source.
		sendCommand(t, pw, `tp @s 24.5 -60 60.5`)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 24.5, -60, 60.5)
		digAt(pw, 24, -61, 60)
		out = append(out, probe(pw, sink, "24 -61 60 minecraft:water[level=0]", 1))

		// Ice over air: nothing holds the melt, stays air.
		sendCommand(t, pw, `tp @s 28.5 -60 60.5`)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 28.5, -60, 60.5)
		digAt(pw, 28, -61, 60)
		out = append(out, probe(pw, sink, "28 -61 60 minecraft:air", 2))

		// packed_ice is not IceBlock: never melts.
		sendCommand(t, pw, `tp @s 32.5 -60 60.5`)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 32.5, -60, 60.5)
		digAt(pw, 32, -61, 60)
		out = append(out, probe(pw, sink, "32 -61 60 minecraft:air", 3))

		// frosted_ice extends IceBlock: same melt.
		sendCommand(t, pw, `tp @s 36.5 -60 60.5`)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 36.5, -60, 60.5)
		digAt(pw, 36, -61, 60)
		out = append(out, probe(pw, sink, "36 -61 60 minecraft:water[level=0]", 4))

		// Silk touch keeps the block (and the melt) to the drop.
		sendCommand(t, pw, `item replace entity @s hotbar.0 with minecraft:diamond_pickaxe[minecraft:enchantments={silk_touch:1}] 1`)
		sendCommand(t, pw, `tp @s 40.5 -60 60.5`)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 40.5, -60, 60.5)
		digAt(pw, 40, -61, 60)
		out = append(out, probe(pw, sink, "40 -61 60 minecraft:air", 5))

		// Nether: WATER_EVAPORATES removes it outright.
		sendCommand(t, pw, `execute in minecraft:the_nether run fill 98 38 98 102 45 102 minecraft:air`)
		sendCommand(t, pw, `execute in minecraft:the_nether run fill 98 38 98 102 39 102 minecraft:stone`)
		sendCommand(t, pw, `execute in minecraft:the_nether run setblock 100 40 100 minecraft:ice`)
		sendCommand(t, pw, `execute in minecraft:the_nether run tp @s 100.5 41 100.5`)
		time.Sleep(600 * time.Millisecond)
		time.Sleep(300 * time.Millisecond)
		sendMovePosRot(pw, 100.5, 41, 100.5)
		digAt(pw, 100, 40, 100)
		out = append(out, probe(pw, sink, "100 40 100 minecraft:air", 6))
		return out
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range va {
		t.Logf("probe %02d: vanilla=%-6s mcgo=%-6s", i, va[i], mc[i])
	}
	for i := range va {
		if va[i] != "hit" || mc[i] != "hit" {
			t.Errorf("probe %d: vanilla=%s mc-go=%s (want hit)", i, va[i], mc[i])
		}
	}
}
