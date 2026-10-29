package blackbox

import (
	"fmt"
	"testing"
)

func TestPickupAllParity(t *testing.T) {
	names := []string{"PAV0", "PAM0", "PAV1", "PAM1", "PAV2", "PAM2", "PAV3", "PAM3", "PAV4", "PAM4"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string, kind int) []clickSnap {
		item, full, a, b := "stone", 64, 40, 40
		if kind == 2 {
			item, full, a, b = "egg", 16, 7, 12
		}
		block, menu := "minecraft:chest", int32(2)
		if kind == 4 {
			block, menu = "minecraft:crafting_table", 12
		}
		gives := [][2]string{{"hotbar.0", fmt.Sprintf("minecraft:%s 1", item)}, {"hotbar.1", fmt.Sprintf("minecraft:%s %d", item, full)}, {"hotbar.2", fmt.Sprintf("minecraft:%s %d", item, a)}, {"hotbar.3", fmt.Sprintf("minecraft:%s %d", item, b)}, {"hotbar.4", fmt.Sprintf(`minecraft:%s[custom_name='{"text":"other"}'] 8`, item)}}
		if kind == 4 {
			gives = [][2]string{{"hotbar.0", "minecraft:oak_planks 1"}, {"hotbar.1", "minecraft:oak_log 1"}, {"hotbar.2", "minecraft:oak_planks 8"}}
		}
		d := newClickDriverOn(t, addr, name, block, menu, gives, nil)
		var snaps []clickSnap
		snap := func() { snaps = append(snaps, snapAnvil(d).snap) }
		if kind == 4 {
			d.click("log", 38, 0, 0, false, false)
			d.click("grid", 1, 0, 0, false, false)
			d.click("seed", 37, 0, 0, false, false)
			d.click("collect-result-excluded", 2, 0, 6, false, false)
			snap()
			t.Logf("%s crafting cursor=%s output=%s", name, d.snap.carried, d.snap.menu[0])
		} else if kind == 3 {
			d.close()
			d.click("sync", 10, 0, 0, true, true)
			d.click("empty-cursor", 9, 0, 6, true, false)
			snap()
			d.click("seed", 36, 0, 0, true, false)
			d.click("occupied", 37, 0, 6, true, false)
			snap()
			d.click("collect", 9, 0, 6, true, false)
			snap()
			d.click("full-cursor", 9, 1, 6, true, false)
			snap()
			t.Logf("%s window0 cursor=%s slots=%v", name, d.snap.carried, d.snap.p0)
		} else {
			for i := 1; i <= 4; i++ {
				d.click("get", int16(54+i), 0, 0, false, false)
				d.click("put", int16(i-1), 0, 0, false, false)
			}
			d.click("empty-cursor", 4, 0, 6, false, false)
			snap()
			d.click("seed", 54, 0, 0, false, false)
			d.click("occupied", 0, 0, 6, false, false)
			snap()
			direction := byte(0)
			if kind == 1 {
				direction = 1
			}
			d.click("collect", 4, direction, 6, false, false)
			snap()
			d.click("full-cursor", 4, direction, 6, false, false)
			snap()
			t.Logf("%s cursor=%s slots=%v", name, d.snap.carried, d.snap.menu)
		}
		return snaps
	}
	for i, tag := range []string{"forward", "reverse", "limit16", "window0", "craft-result"} {
		t.Run(tag, func(t *testing.T) {
			want := run(tg.vanilla, fmt.Sprintf("PAV%d", i), i)
			got := run(tg.mcgo, fmt.Sprintf("PAM%d", i), i)
			for j := range want {
				if diffs := diffSnaps(want[j], got[j]); len(diffs) > 0 {
					t.Errorf("step%d: %v", j, diffs)
				}
			}
		})
	}
}
