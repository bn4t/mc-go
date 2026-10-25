package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestBoneMealUseParity covers BoneMealItem.useOn by hand (the
// dispenser table is separate): clicking a bonemealable block with
// bone meal grows it by the same table — CropBlock +2..5 clamped,
// cocoa/berry +1 — consumes one meal in survival, and a max-age or
// non-bonemealable click does nothing (no consume, no event).
func TestBoneMealUseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZBM-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZBM-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZBMK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZBMK%d", i)) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZBM-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZBM-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) map[string]string {
		out := map[string]string{}
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 101.5 -60 102.5")
		time.Sleep(1500 * time.Millisecond) // vanilla chunk stream
		for _, cmd := range []string{
			"setblock 100 -61 104 minecraft:farmland",
			"setblock 102 -61 104 minecraft:farmland",
			"setblock 100 -60 104 minecraft:wheat[age=6]",
			"setblock 102 -60 104 minecraft:wheat[age=7]",
			"setblock 104 -60 104 minecraft:jungle_log",
			"setblock 103 -60 104 minecraft:cocoa[age=0,facing=west]",
			"setblock 105 -61 104 minecraft:soul_sand",
			"setblock 105 -60 104 minecraft:nether_wart[age=0]",
			"item replace entity @s hotbar.0 with minecraft:bone_meal 64",
			"item replace entity @s hotbar.1 with minecraft:stone 1",
		} {
			sendCommand(t, pw, cmd)
			time.Sleep(60 * time.Millisecond)
		}
		time.Sleep(800 * time.Millisecond)
		mark0 := func(name, spec string, i int) {
			out[name] = probe(pw, sink, spec, i)
		}
		mark0("sanity farmland", `block 100 -61 104 minecraft:farmland`, 60)
		mark0("sanity wheat", `block 100 -60 104 minecraft:wheat`, 61)
		mark0("sanity meal", `items entity @s hotbar.0 minecraft:bone_meal`, 62)

		seq := int32(1)
		click := func(x, y, z, face int32) {
			sendUseItemOn(t, pw, x, y, z, face, seq)
			seq++
			time.Sleep(350 * time.Millisecond)
		}
		mark := func(name, spec string, i int) {
			out[name] = probe(pw, sink, spec, i)
		}

		// Wheat 6 → 7 (clamp), one meal consumed.
		click(100, -60, 104, 1)
		mark("wheat6 grew", `block 100 -60 104 minecraft:wheat[age=7]`, 0)
		mark("meal -1", `items entity @s hotbar.0 minecraft:bone_meal[count=63]`, 1)
		// Max-age wheat: nothing happens — count stays.
		click(102, -60, 104, 1)
		mark("max wheat unchanged", `block 102 -60 104 minecraft:wheat[age=7]`, 2)
		mark("meal kept", `items entity @s hotbar.0 minecraft:bone_meal[count=63]`, 3)
		// Cocoa 0 → 1.
		click(103, -60, 104, 1)
		mark("cocoa grew", `block 103 -60 104 minecraft:cocoa[age=1]`, 4)
		mark("meal -2", `items entity @s hotbar.0 minecraft:bone_meal[count=62]`, 5)
		// Empty hand on a crop: nothing.
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:air")
		click(100, -60, 104, 1)
		mark("empty hand no-op", `block 100 -60 104 minecraft:wheat[age=7]`, 6)
		// Stone in hand (not bonemeal): nothing, no consume path.
		sendCommand(t, pw, "item replace entity @s hotbar.1 with minecraft:stone 1")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:stone 1")
		sendCommand(t, pw, "item replace entity @s hotbar.1 with minecraft:air")
		click(103, -60, 104, 1)
		mark("stone no grow", `block 103 -60 104 minecraft:cocoa[age=1]`, 7)
		// Nether wart is NOT bonemealable (CropBlock table excludes
		// it) — the click is a no-op on both servers.
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:bone_meal 64")
		click(105, -60, 104, 1)
		mark("wart not bonemealable", `block 105 -60 104 minecraft:nether_wart[age=0]`, 8)
		mark("meal kept-3", `items entity @s hotbar.0 minecraft:bone_meal[count=64]`, 9)
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for _, name := range []string{
		"sanity farmland", "sanity wheat", "sanity meal",
		"wheat6 grew", "meal -1", "max wheat unchanged", "meal kept",
		"cocoa grew", "meal -2", "empty hand no-op", "stone no grow",
		"wart not bonemealable", "meal kept-3",
	} {
		t.Logf("%-24s vanilla=%s mcgo=%s", name, vanilla[name], mcgo[name])
		if vanilla[name] != mcgo[name] {
			t.Errorf("probe %q: vanilla %s, mc-go %s", name, vanilla[name], mcgo[name])
		}
	}
}
