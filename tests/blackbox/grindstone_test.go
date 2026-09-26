//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// xpProbe reads the player's XpTotal via /data get, scanning raw
// packet bytes for the data.entity.query translate key.
func xpProbe(t *testing.T, d *clickDriver) float64 {
	sendCommand(t, d.pw, "data get entity @s XpTotal")
	sendCommand(t, d.pw, "say ZXPEND")
	dl := time.Now().Add(8 * time.Second)
	for time.Now().Before(dl) {
		end := false
		for _, p := range d.sink.take() {
			if strings.Contains(string(p.data), "data.entity.query") {
				return lastNumber(chatTextPieces(string(p.data)))
			}
			if strings.Contains(string(p.data), "ZXPEND") {
				end = true
			}
		}
		if end {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Log("xpProbe: no reply")
	return -2
}

// xpProbeStable samples XpTotal until two consecutive reads agree:
// orbs take a few hundred ms to fly to the player and pay out at
// one orb per two ticks.
func xpProbeStable(t *testing.T, d *clickDriver) float64 {
	t.Helper()
	last := -2.0
	for i := 0; i < 16; i++ {
		v := xpProbe(t, d)
		if v >= 0 && v == last {
			return v
		}
		last = v
		time.Sleep(300 * time.Millisecond)
	}
	return last
}

// TestGrindstoneParity pins the measured 26.2 grindstone behaviour.
// Inputs take only a damageable item or one carrying enchantments
// (GrindstoneMenu$2/$3.mayPlace) — a plain stone stays on the cursor,
// which makes mergeItems' non-damageable count-2 branch unreachable.
// A lone enchanted item grinds to the same item minus non-curse
// enchantments (a drained enchanted_book transmutes to book); a pair
// of the same damageable item merges remaining durability plus 5% of
// max. Taking pays getExperienceAmount XP — i3=ceil(x/2) plus a
// random [0,i3) — as orbs at the block, and sounds event 1042.
func TestGrindstoneParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("GrinV", "GrinM"),
	}, nil)

	run := func(addr, name string) (map[string]string, map[string]float64) {
		d := newClickDriverOn(t, addr, name, "minecraft:grindstone", 15, [][2]string{
			{"hotbar.0", `minecraft:diamond_pickaxe[minecraft:enchantments={sharpness:5,unbreaking:3}] 1`},
			{"hotbar.1", `minecraft:diamond_pickaxe[minecraft:enchantments={binding_curse:1,sharpness:5}] 1`},
			{"hotbar.2", `minecraft:enchanted_book[minecraft:stored_enchantments={fortune:3}] 1`},
			{"hotbar.3", `minecraft:iron_pickaxe[minecraft:damage=200] 1`},
			{"hotbar.4", `minecraft:iron_pickaxe[minecraft:damage=200] 1`},
			{"hotbar.5", `minecraft:stone 2`},
			{"hotbar.6", `minecraft:stick[minecraft:enchantments={knockback:1}] 2`},
			{"hotbar.7", `minecraft:wooden_pickaxe 1`},
			{"hotbar.8", `minecraft:iron_pickaxe[minecraft:damage=100] 1`},
			{"inventory.0", `minecraft:diamond_pickaxe[minecraft:enchantments={sharpness:5},minecraft:damage=100,minecraft:custom_name='{"text":"APick"}',minecraft:repair_cost=20] 1`},
			{"inventory.1", `minecraft:stick[minecraft:enchantments={knockback:1}] 1`},
			{"inventory.2", `minecraft:stick[minecraft:enchantments={knockback:1}] 1`},
		}, nil)
		ev := map[string]string{}
		xps := map[string]float64{}
		step := func(tag string) {
			d.fold()
			ev[tag] = fmt.Sprintf("menu=%v|car=%q|grind=%d", d.snap.menu, d.snap.carried, d.grindSounds)
		}
		// hotbar.i sits at menu slot 30+i (3 container + 27 main);
		// inventory.i at menu slot 3+i. Taken results park at
		// distinct main slots 29,28,... so they never swap-chain.
		d.click("p1", 30, 0, 0, false, false)
		d.click("i1", 0, 0, 0, false, false)
		step("single")
		if got := d.snap.menu[2]; got != "966#1" {
			t.Fatalf("%s: single result=%s want bare pick: %v", name, got, d.snap.menu)
		}
		xp0 := xpProbeStable(t, d)
		d.click("tk", 2, 0, 0, false, false)
		step("take1")
		xps["take1"] = xpProbeStable(t, d) - xp0
		d.click("out1", 29, 0, 0, false, false)

		d.click("p2", 31, 0, 0, false, false)
		d.click("i2", 0, 0, 0, false, false)
		step("curse")
		if got := d.snap.menu[2]; got != "966#1[rc1][e2:1]" {
			t.Fatalf("%s: curse result=%s want rc1+curse kept: %v", name, got, d.snap.menu)
		}
		xp0 = xpProbeStable(t, d)
		d.click("tk", 2, 0, 0, false, false)
		step("take2")
		xps["take2"] = xpProbeStable(t, d) - xp0
		d.click("out2", 28, 0, 0, false, false)

		d.click("p3", 32, 0, 0, false, false)
		d.click("i3", 0, 0, 0, false, false)
		step("book")
		if got := d.snap.menu[2]; got != "1058#1" {
			t.Fatalf("%s: book result=%s want plain book: %v", name, got, d.snap.menu)
		}
		xp0 = xpProbeStable(t, d)
		d.click("tk", 2, 0, 0, false, false)
		step("take3")
		xps["take3"] = xpProbeStable(t, d) - xp0
		d.click("out3", 27, 0, 0, false, false)

		d.click("p4", 33, 0, 0, false, false)
		d.click("i4", 0, 0, 0, false, false)
		d.click("p5", 34, 0, 0, false, false)
		d.click("i5", 1, 0, 0, false, false)
		step("pair")
		if got := d.snap.menu[2]; got != "961#1[d138]" {
			t.Fatalf("%s: pair result=%s want merged dmg: %v", name, got, d.snap.menu)
		}
		d.click("tk", 2, 0, 0, false, false)
		step("take4")
		d.click("out4", 26, 0, 0, false, false)

		// mayPlace refuses the stone: it stays on the cursor and the
		// input stays empty. An enchanted stick 2-stack enters
		// (hasAnyEnchantments) but a count>1 input never produces
		// output.
		d.click("p6", 35, 0, 0, false, false)
		d.click("i6", 0, 0, 0, false, false)
		step("refused")
		if d.snap.carried != "1#2" {
			t.Fatalf("%s: stone carried=%s want it refused back: %v", name, d.snap.carried, d.snap.menu)
		}
		d.click("drop6", 35, 0, 0, false, false)
		d.click("p7", 36, 0, 0, false, false)
		d.click("i7", 0, 0, 0, false, false)
		step("count2")
		if got := d.snap.menu[2]; got != "-" && got != "" {
			t.Fatalf("%s: count-2 result=%s want empty: %v", name, got, d.snap.menu)
		}
		d.click("back7", 0, 0, 0, false, false)
		d.click("drop7", 36, 0, 0, false, false)

		// Two enchanted sticks merge through the non-damageable
		// branch: same id, stackable, matching components -> count-2
		// with every enchantment stripped.
		d.click("ps1", 4, 0, 0, false, false)
		d.click("is1", 0, 0, 0, false, false)
		d.click("ps2", 5, 0, 0, false, false)
		d.click("is2", 1, 0, 0, false, false)
		step("sticks")
		if got := d.snap.menu[2]; got != "974#2" {
			t.Fatalf("%s: stick merge result=%s want 974#2: %v", name, got, d.snap.menu)
		}
		d.click("tk", 2, 0, 0, false, false)
		step("take5")
		d.click("out5", 25, 0, 0, false, false)

		// An unenchanted damaged item enters (damageable) but has
		// nothing to strip.
		d.click("p8", 38, 0, 0, false, false)
		d.click("i8", 0, 0, 0, false, false)
		step("noench")
		if got := d.snap.menu[2]; got != "-" && got != "" {
			t.Fatalf("%s: no-ench result=%s want empty: %v", name, got, d.snap.menu)
		}
		d.click("back8", 0, 0, 0, false, false)
		d.click("drop8", 38, 0, 0, false, false)

		// Named+damaged+repair_cost pick: enchants strip, name and
		// damage stay, and the folded repair_cost drops to absent.
		d.click("p9", 3, 0, 0, false, false)
		d.click("i9", 0, 0, 0, false, false)
		step("named")
		want := `966#1[d100][n"{\"text\":\"APick\"}"]`
		if got := d.snap.menu[2]; got != want {
			t.Fatalf("%s: named result=%s want %s: %v", name, got, want, d.snap.menu)
		}
		xp0 = xpProbeStable(t, d)
		d.click("tk", 2, 0, 0, false, false)
		step("take6")
		xps["take6"] = xpProbeStable(t, d) - xp0
		d.click("out6", 24, 0, 0, false, false)

		// Mismatched ids never merge.
		d.click("p10", 37, 0, 0, false, false)
		d.click("i10", 0, 0, 0, false, false)
		d.click("p11", 29, 0, 0, false, false)
		d.click("i11", 1, 0, 0, false, false)
		step("mismatch")
		if got := d.snap.menu[2]; got != "-" && got != "" {
			t.Fatalf("%s: mismatch result=%s want empty: %v", name, got, d.snap.menu)
		}
		time.Sleep(200 * time.Millisecond)
		d.fold()
		return ev, xps
	}

	want, wx := run(tg.vanilla, "GrinV")
	got, gx := run(tg.mcgo, "GrinM")
	for tag, w := range want {
		if got[tag] != w {
			t.Errorf("step %s:\n  vanilla %s\n  mc-go   %s", tag, w, got[tag])
		}
	}
	// The XP payout is randomized: i3=ceil(x/2) plus a draw in [0,i3).
	// sharp5+unb3=66 -> [33,65], curse+sharp5=45 -> [23,45],
	// fortune3=33 -> [17,33], sharp5=45 -> [23,45].
	for _, tc := range []struct {
		key    string
		lo, hi float64
	}{
		{"take1", 33, 65},
		{"take2", 23, 45},
		{"take3", 17, 33},
		{"take6", 23, 45},
	} {
		for side, xps := range map[string]map[string]float64{"vanilla": wx, "mc-go": gx} {
			v := xps[tc.key]
			if v < tc.lo || v > tc.hi {
				t.Errorf("%s %s: xp delta %v out of [%v,%v]", side, tc.key, v, tc.lo, tc.hi)
			}
		}
	}
}
