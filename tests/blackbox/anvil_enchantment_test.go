package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// The same menu clicks measure book/item upgrades, limits, applicability,
// conflict penalties, prior work, and the paid take's consumption on both servers.
func TestAnvilEnchantmentParity(t *testing.T) {
	cases := []struct {
		tag, left, right string
		creative         bool
	}{
		{"equal", `diamond_sword[enchantments={sharpness:2}]`, `enchanted_book[stored_enchantments={sharpness:2}]`, false},
		{"cap", `diamond_sword[enchantments={sharpness:5}]`, `enchanted_book[stored_enchantments={sharpness:5}]`, false},
		{"lower", `diamond_sword[enchantments={sharpness:4}]`, `enchanted_book[stored_enchantments={sharpness:1}]`, false},
		{"item", `diamond_sword[enchantments={unbreaking:2}]`, `diamond_sword[enchantments={unbreaking:2}]`, false},
		{"books", `enchanted_book[stored_enchantments={fortune:2}]`, `enchanted_book[stored_enchantments={fortune:2}]`, false},
		{"conflict", `diamond_sword[enchantments={sharpness:2},damage=500]`, `diamond_sword[enchantments={smite:2},damage=500]`, false},
		{"partial", `diamond_sword[enchantments={sharpness:2}]`, `enchanted_book[stored_enchantments={smite:2,unbreaking:1}]`, false},
		{"unsupported", `diamond_pickaxe`, `enchanted_book[stored_enchantments={sharpness:2}]`, false},
		{"creative", `diamond_pickaxe`, `enchanted_book[stored_enchantments={sharpness:2}]`, true},
		{"expensive", `diamond_sword[repair_cost=39]`, `enchanted_book[stored_enchantments={sharpness:2}]`, false},
		{"expensivecreative", `diamond_sword[repair_cost=39]`, `enchanted_book[stored_enchantments={sharpness:2}]`, true},
		{"repair", `diamond_pickaxe[damage=1000,enchantments={efficiency:2}]`, `diamond_pickaxe[damage=1000,enchantments={efficiency:2}]`, false},
	}
	var names []string
	for i := range cases {
		names = append(names, fmt.Sprintf("AEV%d", i), fmt.Sprintf("AEM%d", i))
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string, i int) []anvilSnap {
		tc := cases[i]
		d := newClickDriverOn(t, addr, name, "minecraft:anvil", 8, [][2]string{{"hotbar.0", "minecraft:" + tc.left + " 1"}, {"hotbar.1", "minecraft:" + tc.right + " 1"}}, nil)
		if !tc.creative {
			sendCommand(t, d.pw, "gamemode survival")
		}
		if !digSentinel(t, d.pw, d.sink, "AES", 10*time.Second) {
			t.Fatal("no setup ack")
		}
		// Observe the setup XP packet instead of letting the command
		// sentinel discard it; previews retain the last known XP state.
		sendCommand(t, d.pw, "xp set @s 100 levels")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			d.fold()
			if d.xpSeen && d.xpLevel == 100 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !d.xpSeen || d.xpLevel != 100 {
			t.Fatalf("%s: setup XP packet not observed", name)
		}
		d.click("left", 30, 0, 0, false, false)
		d.click("left-in", 0, 0, 0, false, false)
		d.click("right", 31, 0, 0, false, false)
		d.click("right-in", 1, 0, 0, false, false)
		result := []anvilSnap{snapAnvil(d)}
		t.Logf("%s %s cost=%d output=%s", name, tc.tag, d.cost, d.snap.menu[2])
		d.click("take", 2, 0, 0, false, false)
		result = append(result, snapAnvil(d))
		d.close()
		return result
	}
	for i, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			a := run(tg.vanilla, fmt.Sprintf("AEV%d", i), i)
			b := run(tg.mcgo, fmt.Sprintf("AEM%d", i), i)
			for j := range a {
				if diff := diffSnaps(a[j].snap, b[j].snap); len(diff) > 0 {
					t.Errorf("step %d: %v", j, diff)
				}
				if a[j].cost != b[j].cost {
					t.Errorf("step %d cost vanilla=%d mcgo=%d", j, a[j].cost, b[j].cost)
				}
				if a[j].xp != b[j].xp {
					t.Errorf("step %d XP vanilla=%d mcgo=%d", j, a[j].xp, b[j].xp)
				}
				if a[j].sounds != b[j].sounds {
					t.Errorf("step %d sounds vanilla=%d mcgo=%d", j, a[j].sounds, b[j].sounds)
				}
			}
		})
	}
}
