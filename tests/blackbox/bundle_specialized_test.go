package blackbox

import (
	"fmt"
	"reflect"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// 26.2 BundleItem overrides, BundleContents.Mutable.tryTransfer and
// Slot.safeTake/safeInsert/allowModification, measured with Temurin25 javap.
func TestBundleSpecializedParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BundleF", "BundleB")}, nil)
	defer tg.cleanup()
	for _, tc := range []struct {
		name, block string
		menu        int32
	}{{"BundleF", "furnace", 14}, {"BundleB", "brewing_stand", 11}} {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr string) []string {
				gives := [][2]string{{"hotbar.0", "minecraft:bundle"}, {"hotbar.1", `minecraft:bundle[bundle_contents=[{id:"minecraft:stone",count:60}]]`}}
				if tc.block == "brewing_stand" {
					gives = [][2]string{{"hotbar.0", `minecraft:bundle[bundle_contents=[{id:"minecraft:glass_bottle",count:12},{id:"minecraft:blaze_powder",count:5}]]`}}
				}
				d := newClickDriverOn(t, addr, tc.name, "minecraft:"+tc.block, tc.menu, gives, nil)
				command := func(text string) {
					sendCommand(t, d.pw, text)
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "bundle-typed-setup")
				}
				off := 0
				for _, c := range []byte(tc.name) {
					off = (off*31 + int(c)) & 15
				}
				x := 8 + 32*off
				set := func(slot int, item string) {
					command(fmt.Sprintf("item replace block %d -61 0 container.%d with minecraft:%s", x, slot, item))
					d.click("setup-resync", 20, 9, 2, false, true)
				}
				var out []string
				sample := func(tag string, slots ...int32) {
					line := tag + " cursor=" + d.snap.carried
					for _, slot := range slots {
						line += fmt.Sprintf(" %d=%s", slot, d.snap.menu[slot])
					}
					out = append(out, line)
				}
				if tc.block == "furnace" {
					command("tick freeze")
					d.click("lift-empty", 30, 0, 0, false, false)
					set(2, "iron_ingot 8")
					d.click("absorb-result", 2, 0, 0, false, false)
					sample("full-output", 2)
					d.click("stash", 3, 0, 0, false, false)
					d.click("lift-filled", 31, 0, 0, false, false)
					set(2, "iron_ingot 8")
					d.click("reject-part-result", 2, 0, 0, false, false)
					sample("capacity-output", 2)
					set(2, "iron_ingot 4")
					d.click("fit-result", 2, 0, 0, false, false)
					sample("fitting-output", 2)
					d.click("reject-output-place", 2, 1, 0, false, false)
					sample("output-place", 2)
					d.click("stash-full", 4, 0, 0, false, false)
					set(1, `bundle[bundle_contents=[{id:"minecraft:stone",count:3}]]`)
					d.click("invalid-target-bundle", 1, 1, 0, false, false)
					sample("fuel-target", 1)
					var request protocol.Buffer
					request.VarInt(1)
					_ = d.pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
					crafted := int32(0)
					settleUse(t, d.sink, func(packets []usePkt) {
						d.foldPkts(packets)
						for _, p := range packets {
							if p.id != 0x03 {
								continue
							}
							r := protocol.NewPacketReader(p.data)
							n, _ := r.VarInt()
							for range n {
								cat, _ := r.VarInt()
								id, _ := r.VarInt()
								value, _ := r.VarInt()
								if cat == 1 && id == world.ItemIDs["iron_ingot"] {
									crafted = value
								}
							}
						}
					}, "bundle-crafted-stats")
					out = append(out, fmt.Sprintf("crafted=%d", crafted))
					if crafted != 12 {
						t.Errorf("crafted=%d want12", crafted)
					}
				} else {
					d.click("lift", 32, 0, 0, false, false)
					d.click("bottle-cap", 0, 1, 0, false, false)
					sample("bottle-cap", 0)
					d.click("wrong-fuel", 4, 1, 0, false, false)
					sample("fuel-reject", 4)
					d.click("recover-bottle", 0, 0, 0, false, false)
					d.click("store", 6, 0, 0, false, false)
					var selectEntry protocol.Buffer
					selectEntry.VarInt(6)
					selectEntry.VarInt(1)
					_ = d.pw.WritePacket(server.IDPlayBundleItemSelectedSB, selectEntry.Bytes())
					d.click("extract-powder", 6, 1, 0, false, false)
					d.click("put-powder-first", 6, 0, 0, false, false)
					d.click("lift-reordered", 6, 0, 0, false, false)
					d.click("valid-fuel", 4, 1, 0, false, false)
					sample("fuel-place", 4)
					d.click("wrong-ingredient", 3, 1, 0, false, false)
					sample("ingredient-reject", 3)
					d.click("store-final", 7, 0, 0, false, false)
					set(1, `bundle[bundle_contents=[{id:"minecraft:stone",count:3}]]`)
					d.click("invalid-potion-target", 1, 1, 0, false, false)
					sample("potion-target", 1)
				}
				return out
			}
			want := run(tg.vanilla)
			got := run(tg.mcgo)
			for i := range want {
				t.Logf("vanilla %s; mcgo %s", want[i], got[i])
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatal("specialized bundle state differs")
			}
		})
	}
}
