//go:build blackbox

package blackbox

// TestMapExtendingParity pins crafting_special_mapextending on 26.2:
//   - the fixed ShapedRecipePattern ### / #x# / ### — eight paper
//     around one center filled_map — only shows a result when the
//     center stack's map_id resolves to saved map data that isn't an
//     exploration map and has scale < 4 (MapItem.getSavedData gates).
//   - the result is a count-1 copy of the center stack stamped
//     map_post_processing=scale; taking it runs the post-processing:
//     a fresh derived map id lands on the cursor and the next chain
//     level's scale comes from that derived data.
//   - a scale-4 map in the center shows no result.

import (
	"fmt"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestMapExtendingParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("MapXV", "MapXM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) (map[string]string, *clickDriver) {
		d := newClickDriverOn(t, addr, name, "minecraft:crafting_table", 12, [][2]string{
			{"hotbar.0", "minecraft:map"},
			{"hotbar.1", "minecraft:paper 4"},
			{"hotbar.2", "minecraft:paper 4"},
			{"hotbar.3", "minecraft:paper 4"},
			{"hotbar.4", "minecraft:paper 4"},
			{"hotbar.5", "minecraft:paper 4"},
			{"hotbar.6", "minecraft:paper 4"},
			{"hotbar.7", "minecraft:paper 4"},
			{"hotbar.8", "minecraft:paper 4"},
		}, nil)
		ev := map[string]string{}
		step := func(tag string) {
			d.fold()
			ev[tag] = fmt.Sprintf("m0=%s|m5=%s|car=%s|h0=%s|h1=%s|h8=%s",
				d.snap.menu[0], d.snap.menu[5], d.snap.carried,
				d.snap.menu[37], d.snap.menu[38], d.snap.menu[45])
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
			// map lands via Inventory.add in the first free slot — the
			// hotbar is full so it lands at main-inventory.0 = menu 10.
			for i := int16(10); i < 46; i++ {
				if strings.HasPrefix(d.snap.menu[int32(i)], "1133#") {
					return i
				}
			}
			t.Fatalf("%s: %s no filled map landed: %v", name, tag, d.snap.menu)
			return -1
		}

		mapSlot := useMap("use-map", 0)
		click("map-lift", mapSlot, 0, 0)
		click("map-center", 5, 0, 0)
		// Fill the ring: each hotbar stack carries paper 4 — left-click
		// places the whole stack, so the ring has 4 paper per slot.
		ring := []int16{1, 2, 3, 4, 6, 7, 8, 9}
		for k, gslot := range ring {
			click(fmt.Sprintf("ring-lift-%d", k), int16(38+k), 0, 0)
			click(fmt.Sprintf("ring-place-%d", k), gslot, 0, 0)
		}
		if got := d.snap.menu[0]; !(strings.HasPrefix(got, "1133#1[mid") && strings.HasSuffix(got, "[mpp1]")) {
			t.Fatalf("%s: ring-filled result=%s want 1133#1[mid*][mpp1]: %v", name, got, d.snap.menu)
		}
		// Four extends take the map from scale 0 to scale 4; each take
		// re-centers the derived map for the next level.
		for k := 1; k <= 4; k++ {
			click(fmt.Sprintf("take-%d", k), 0, 0, 0)
			click(fmt.Sprintf("re-%d", k), 5, 0, 0)
		}
		// scale-4 data refuses the recipe: no result.
		if got := d.snap.menu[0]; got != "-" {
			t.Fatalf("%s: scale-4 result=%s want empty: %v", name, got, d.snap.menu)
		}
		step("after")
		return ev, d
	}

	vanilla, _ := run(tg.vanilla, "MapXV")
	mcgo, _ := run(tg.mcgo, "MapXM")
	for tag, w := range vanilla {
		if got := mcgo[tag]; got != w {
			t.Fatalf("step %s:\nvanilla %s\nmc-go   %s", tag, w, got)
		}
	}
	for tag := range mcgo {
		if _, ok := vanilla[tag]; !ok {
			t.Fatalf("mc-go has extra step %s", tag)
		}
	}
}
