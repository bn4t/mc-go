package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestQuickCraftGridParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("GridDragV", "GridDragM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:crafting_table", 12, [][2]string{{"hotbar.0", "minecraft:oak_planks 8"}, {"hotbar.1", "minecraft:crafting_table"}}, nil)
		tableKey := d.snap.menu[38]
		if tableKey == "" || tableKey == "-" {
			t.Fatal("missing reference crafting-table item")
		}
		var out []string
		for _, player := range []bool{false, true} {
			source := int16(37)
			grid := []int16{1, 2, 4, 5}
			m := d.snap.menu
			if player {
				d.close()
				sendCommand(t, d.pw, "item replace entity @s hotbar.0 with minecraft:oak_planks 8")
				if !digSentinel(t, d.pw, d.sink, "GRID-DIRT", 10*time.Second) {
					t.Fatal("grid item barrier")
				}
				d.click("resync-grid-player", 9, 0, 0, true, true)
				source = 36
				grid = []int16{1, 2, 3, 4}
				m = d.snap.p0
			}
			d.click("lift-planks", source, 0, 0, player, false)
			d.click("grid-start", -999, 0, 5, player, false)
			d.click("reject-result", 0, 1, 5, player, false)
			for _, sl := range grid {
				d.click("grid-add", sl, 1, 5, player, false)
			}
			d.click("grid-end", -999, 2, 5, player, false)
			if player {
				m = d.snap.p0
			} else {
				m = d.snap.menu
			}
			key := m[0]
			if key != tableKey {
				t.Errorf("%s player=%v result=%s want crafting table %s", name, player, key, tableKey)
			}
			for _, sl := range grid {
				if !strings.HasSuffix(m[int32(sl)], "#2") {
					t.Errorf("%s player=%v grid%d=%s want two planks", name, player, sl, m[int32(sl)])
				}
			}
			if d.snap.carried != "-" {
				t.Errorf("%s grid left cursor=%s", name, d.snap.carried)
			}
			out = append(out, fmt.Sprintf("player=%v result=%s cursor=%s", player, key, d.snap.carried))
		}
		return out
	}
	want := run(tg.vanilla, "GridDragV")
	got := run(tg.mcgo, "GridDragM")
	for i := range want {
		t.Logf("vanilla %s; mcgo %s", want[i], got[i])
		if want[i] != got[i] {
			t.Error("drag crafting differs")
		}
	}
}
