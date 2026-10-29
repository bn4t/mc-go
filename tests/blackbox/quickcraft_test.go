package blackbox

import (
	"fmt"
	"strings"
	"testing"
)

// TestQuickCraftStorageParity uses nonempty cursors throughout: even
// split, one-per-slot, duplicate selection, interruption, creative fill,
// and player-inventory dragging after closing a storage menu.
func TestQuickCraftStorageParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("DragV", "DragM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:dirt 10"}}, nil)
		var out []string
		check := func(tag string, w0 bool, wantCursor int, want map[int32]int) {
			m := d.snap.menu
			if w0 {
				m = d.snap.p0
			}
			count := func(key string) int {
				if key == "" || key == "-" {
					return 0
				}
				var n int
				_, _ = fmt.Sscanf(strings.Split(key, "#")[1], "%d", &n)
				return n
			}
			cur := count(d.snap.carried)
			if cur != wantCursor {
				t.Errorf("%s %s cursor=%s want%d", name, tag, d.snap.carried, wantCursor)
			}
			line := fmt.Sprintf("%s cursor=%d", tag, cur)
			for _, sl := range []int32{0, 1, 2, 3, 4, 5, 6, 8, 9, 10, 11} {
				if expected, ok := want[sl]; ok {
					got := count(m[sl])
					if got != expected {
						t.Errorf("%s %s slot%d=%s want%d", name, tag, sl, m[sl], expected)
					}
					line += fmt.Sprintf(" %d=%d", sl, got)
				}
			}
			out = append(out, line)
		}
		d.click("pickup10", 54, 0, 0, false, false)
		d.click("start-even", -999, 0, 5, false, false)
		d.click("add0", 0, 1, 5, false, false)
		d.click("duplicate0", 0, 1, 5, false, false)
		d.click("add1", 1, 1, 5, false, false)
		d.click("end-even", -999, 2, 5, false, false)
		check("even", false, 0, map[int32]int{0: 5, 1: 5})
		d.click("pickup5", 0, 0, 0, false, false)
		d.click("start-right", -999, 4, 5, false, false)
		d.click("right2", 2, 5, 5, false, false)
		d.click("right3", 3, 5, 5, false, false)
		d.click("end-right", -999, 6, 5, false, false)
		check("right", false, 3, map[int32]int{2: 1, 3: 1})
		d.click("start-abort", -999, 0, 5, false, false)
		d.click("abort4", 4, 1, 5, false, false)
		d.click("interrupt", 5, 0, 0, false, false)
		d.click("orphan-end", -999, 2, 5, false, false)
		check("aborted", false, 3, map[int32]int{4: 0, 5: 0})
		d.click("start-single", -999, 0, 5, false, false)
		d.click("single6", 6, 1, 5, false, false)
		d.click("end-single", -999, 2, 5, false, false)
		check("single", false, 0, map[int32]int{6: 3})
		d.click("pickup-other5", 1, 0, 0, false, false)
		d.click("start-remainder", -999, 0, 5, false, false)
		d.click("existing2", 2, 1, 5, false, false)
		d.click("existing3", 3, 1, 5, false, false)
		d.click("end-remainder", -999, 2, 5, false, false)
		check("remainder", false, 1, map[int32]int{2: 3, 3: 3})
		d.click("start-creative", -999, 8, 5, false, false)
		d.click("creative8", 8, 9, 5, false, false)
		d.click("creative9", 9, 9, 5, false, false)
		d.click("end-creative", -999, 10, 5, false, false)
		check("creative", false, 0, map[int32]int{8: 64, 9: 64})
		d.click("pickup-before-close", 6, 0, 0, false, false)
		d.click("start-before-close", -999, 0, 5, false, false)
		d.click("select-before-close", 4, 1, 5, false, false)
		d.close()
		sendCommand(t, d.pw, "item replace entity @s hotbar.0 with minecraft:dirt 10")
		if !digSentinel(t, d.pw, d.sink, "DRAG-ITEM", 10e9) {
			t.Fatal("item setup barrier")
		}
		d.click("resync-player", 9, 0, 0, true, true)
		d.click("player-pickup", 36, 0, 0, true, false)
		d.click("player-start", -999, 0, 5, true, false)
		d.click("player10", 10, 1, 5, true, false)
		d.click("player11", 11, 1, 5, true, false)
		d.click("player-end", -999, 2, 5, true, false)
		check("player", true, 0, map[int32]int{10: 5, 11: 5})
		return out
	}
	want := run(tg.vanilla, "DragV")
	got := run(tg.mcgo, "DragM")
	for i := range want {
		t.Logf("vanilla %s; mcgo %s", want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("drag step differs")
		}
	}
}
