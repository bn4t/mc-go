//go:build blackbox

package blackbox

// TestCartographyParity pins the cartography table on 26.2:
//   - the map slot takes only stacks carrying map_id, the additional
//     slot paper / the empty map / glass_pane, the result is
//     take-only (measured mayPlace on CartographyTableMenu$3/$4/$5).
//   - setupResultSlot: paper + unlocked + scale<4 → a count-1 copy
//     stamped map_post_processing=scale; glass_pane + unlocked →
//     =lock; the empty map item → a count-2 clone of the same id.
//     Locked data refuses paper and glass.
//   - a take removes one of each input, resolves the post-processing
//     into a fresh map id (scaled or locked data), and plays
//     ui.cartography_table.take_result once per game tick.
//   - EmptyMapItem.use mints the scale-0 data entry: an emptied hand
//     becomes the filled map.
//   - quickMoveStack splits by component: filled maps move to slot 0,
//     paper/empty-map/glass to slot 1, anything else stays.

import (
	"fmt"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestCartographyParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CartV", "CartM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) (map[string]string, *clickDriver) {
		d := newClickDriverOn(t, addr, name, "minecraft:cartography_table", 23, [][2]string{
			{"hotbar.0", "minecraft:map"},
			{"hotbar.1", "minecraft:map"},
			{"hotbar.2", "minecraft:map 4"},
			{"inventory.0", "minecraft:paper 8"},
			{"inventory.1", "minecraft:glass_pane 4"},
			{"inventory.2", "minecraft:map 4"},
			{"inventory.3", "minecraft:dirt"},
		}, nil)
		ev := map[string]string{}
		step := func(tag string) {
			d.fold()
			ev[tag] = fmt.Sprintf("m0=%s|m1=%s|m2=%s|car=%s|i0=%s|i1=%s|i2=%s|i3=%s|i4=%s|i5=%s|h0=%s|h1=%s|h2=%s|h3=%s|snd=%v",
				d.snap.menu[0], d.snap.menu[1], d.snap.menu[2], d.snap.carried,
				d.snap.menu[3], d.snap.menu[4], d.snap.menu[5],
				d.snap.menu[6], d.snap.menu[7], d.snap.menu[8],
				d.snap.menu[30], d.snap.menu[31], d.snap.menu[32], d.snap.menu[33], d.soundIDs)
		}
		click := func(tag string, slot int16, btn byte, mode int32) {
			d.click(tag, slot, btn, mode, false, false)
			step(tag)
		}
		useMap := func(tag string, held int16) int16 {
			var hb protocol.Buffer
			hb.Short(held)
			_ = d.pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())
			var ub protocol.Buffer
			ub.VarInt(0)
			ub.VarInt(0)
			ub.Float(0)
			ub.Float(0)
			_ = d.pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
			settleUse(d.t, d.sink, func(pkts []usePkt) { d.foldPkts(pkts) }, tag)
			step(tag)
			// creative: consume() leaves the hand alone and the filled
			// map lands via Inventory.add in the first free slot — find it.
			for i := int16(3); i < 39; i++ {
				if strings.HasPrefix(d.snap.menu[int32(i)], "1133#") {
					return i
				}
			}
			t.Fatalf("%s: %s no filled map landed: %v", name, tag, d.snap.menu)
			return -1
		}

		// 3 container slots: inventory.i = 3+i, hotbar.i = 30+i.
		mapSlot := useMap("use-map", 0)
		click("map-in", mapSlot, 0, 0)
		click("map-place", 0, 0, 0)
		click("paper-pick", 3, 0, 0)
		click("paper-in", 1, 0, 0)
		if got := d.snap.menu[2]; !(strings.HasPrefix(got, "1133#1[mid") && strings.HasSuffix(got, "[mpp1]")) {
			t.Fatalf("%s: paper-in result=%s want 1133#1[mid*][mpp1]: %v", name, got, d.snap.menu)
		}
		click("take-1", 2, 0, 0)
		click("scaled1-in", 0, 0, 0)
		click("take-2", 2, 0, 0)
		click("scaled2-in", 0, 0, 0)
		click("take-3", 2, 0, 0)
		click("scaled3-in", 0, 0, 0)
		click("take-4", 2, 0, 0)
		click("scaled4-in", 0, 0, 0)
		// scale-4 data refuses paper: no result.
		if got := d.snap.menu[2]; got != "-" {
			t.Fatalf("%s: scaled4-in result=%s want empty: %v", name, got, d.snap.menu)
		}
		click("paper-out", 1, 0, 0)
		click("paper-back", 3, 0, 0)
		click("glass-pick", 4, 0, 0)
		click("glass-in", 1, 0, 0)
		if got := d.snap.menu[2]; !(strings.HasPrefix(got, "1133#1[mid") && strings.HasSuffix(got, "[mpp0]")) {
			t.Fatalf("%s: glass-in result=%s want 1133#1[mid*][mpp0]: %v", name, got, d.snap.menu)
		}
		click("take-lock", 2, 0, 0)
		click("locked-in", 0, 0, 0)
		// locked refuses glass (still in slot 1): no result.
		if got := d.snap.menu[2]; got != "-" {
			t.Fatalf("%s: locked-in result=%s want empty: %v", name, got, d.snap.menu)
		}
		click("glass-out", 1, 0, 0)
		click("glass-back", 4, 0, 0)
		click("paper-pick2", 3, 0, 0)
		click("paper-in2", 1, 0, 0)
		// locked refuses paper too.
		if got := d.snap.menu[2]; got != "-" {
			t.Fatalf("%s: locked-paper result=%s want empty: %v", name, got, d.snap.menu)
		}
		click("paper-out2", 1, 0, 0)
		click("paper-back2", 3, 0, 0)
		// the empty map clones the locked map — locked does not gate it.
		click("emptymap-pick", 5, 0, 0)
		click("emptymap-in", 1, 0, 0)
		if got := d.snap.menu[2]; !strings.HasPrefix(got, "1133#2[mid") {
			t.Fatalf("%s: clone result=%s want 1133#2[mid*]: %v", name, got, d.snap.menu)
		}
		click("take-clone", 2, 0, 0)
		// park the two-count clone, repick it: a filled map into the
		// additional slot refuses.
		click("clone-park", 7, 0, 0)
		click("clone-repick", 7, 0, 0)
		click("add-refuse", 1, 0, 0)
		click("map-back", 0, 0, 0)
		// dirt into the map slot refuses; an empty map into the map
		// slot refuses (it carries no map_id).
		click("dirt-pick", 6, 0, 0)
		click("map-refuse", 0, 0, 0)
		click("dirt-back", 6, 0, 0)
		click("addmap-pick", 1, 0, 0)
		click("map-refuse2", 0, 0, 0)
		click("addmap-back", 1, 0, 0)
		// move the filled map into the inventory, then shift-click it
		// back into the map slot; shift-click paper/dirt/empty-map.
		click("fill-pick", 0, 0, 0)
		click("fill-park", 8, 0, 0)
		click("qm-fill", 8, 0, 1)
		click("qm-paper", 3, 0, 1)
		click("qm-dirt", 6, 0, 1)
		click("qm-emptymap", 32, 0, 1)
		map2 := useMap("use-map2", 1)
		// a filled map under an occupied map slot stays put.
		click("qm-map2", map2, 0, 1)
		// close: the two inputs come back to the player.
		d.close()
		step("close")
		return ev, d
	}

	vanilla, _ := run(tg.vanilla, "CartV")
	mcgo, _ := run(tg.mcgo, "CartM")
	for tag, w := range vanilla {
		if got := mcgo[tag]; got != w {
			t.Fatalf("step %s:\nvanilla %s\nmc-go   %s", tag, w, got)
		}
	}
}
