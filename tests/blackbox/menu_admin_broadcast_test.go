//go:build blackbox

package blackbox

import (
	"fmt"
	"reflect"
	"testing"

	"mc-go/internal/protocol"
)

// ServerPlayer continues broadcasting open menus during /tick freeze.
// The command response alone must expose an external slot mutation;
// no click, resync request, or world simulation step is issued.
func TestFrozenMenuAdminBroadcastParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("MenuAdmin")}, nil)
	defer tg.cleanup()
	for _, tc := range []struct {
		block string
		menu  int32
		slot  int
	}{{"chest", 2, 0}, {"furnace", 14, 2}, {"hopper", 16, 0}, {"brewing_stand", 11, 3}} {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr string) []string {
				d := newClickDriverOn(t, addr, "MenuAdmin", "minecraft:"+tc.block, tc.menu, nil, nil)
				command := func(text string) int {
					sendCommand(t, d.pw, text)
					updates := 0
					settleUse(t, d.sink, func(ps []usePkt) {
						d.foldPkts(ps)
						for _, p := range ps {
							if p.id == 0x14 {
								r := protocol.NewPacketReader(p.data)
								win, _ := r.VarInt()
								if win == d.win {
									updates++
								}
							}
						}
					}, "admin-menu")
					return updates
				}
				command("tick freeze")
				off := 0
				for _, c := range []byte("MenuAdmin") {
					off = (off*31 + int(c)) & 15
				}
				x := 8 + 32*off
				item := "iron_ingot"
				if tc.block == "brewing_stand" {
					item = "nether_wart"
				}
				var out []string
				for _, count := range []int{2, 2, 3} {
					n := command(fmt.Sprintf("item replace block %d -61 0 container.%d with minecraft:%s %d", x, tc.slot, item, count))
					out = append(out, fmt.Sprintf("slot=%s updates=%d cursor=%s", d.snap.menu[int32(tc.slot)], n, d.snap.carried))
				}
				command("tick unfreeze")
				d.close()
				return out
			}
			v, g := run(tg.vanilla), run(tg.mcgo)
			t.Logf("vanilla=%v mcgo=%v", v, g)
			if !reflect.DeepEqual(v, g) {
				t.Errorf("frozen menu external mutation differs")
			}
		})
	}
}
