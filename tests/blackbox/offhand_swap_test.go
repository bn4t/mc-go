package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"testing"
	"time"
)

func TestOffhandContainerSwapParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("OffVChest", "OffMChest", "OffVBarrel", "OffMBarrel")}, nil)
	defer tg.cleanup()
	run := func(addr, name, block string) []clickSnap {
		d := newClickDriverOn(t, addr, name, block, 2, [][2]string{{"hotbar.0", "minecraft:dirt 10"}, {"weapon.offhand", "minecraft:cobblestone 3"}}, nil)
		var snaps []clickSnap
		step := func(tag string, slot int16, button byte, mode int32, w0, bad bool) {
			d.click(tag, slot, button, mode, w0, bad)
			snaps = append(snaps, snapAnvil(d).snap)
		}
		step("pickup", 54, 0, 0, false, false)
		step("place", 0, 0, 0, false, false)
		step("occupied-offhand", 0, 40, 2, false, false)
		if want := stackKey(server.ItemStack{ID: world.ItemIDs["cobblestone"], Count: 3}); d.snap.menu[0] != want {
			t.Fatalf("%s: occupied offhand swap got %q want %q", name, d.snap.menu[0], want)
		}
		step("swap-back", 0, 40, 2, false, false)
		sendCommand(t, d.pw, "item replace entity @s weapon.offhand with minecraft:air")
		if !digSentinel(t, d.pw, d.sink, "EMPTYOFF", 10*time.Second) {
			t.Fatal("missing offhand clear ack")
		}
		d.fold()
		step("empty-offhand", 0, 40, 2, false, false)
		if d.snap.menu[0] != "-" {
			t.Fatalf("%s: empty offhand did not clear target: %s", name, d.snap.menu[0])
		}
		step("invalid-nine-menu", 1, 9, 2, false, false)
		d.close()
		step("heal-player", 9, 0, 0, true, true)
		step("offhand-to-player", 36, 40, 2, true, false)
		step("player-to-offhand", 36, 40, 2, true, false)
		step("invalid-nine-player", 36, 9, 2, true, false)
		return snaps
	}
	for _, tc := range []struct{ name, block string }{{"Chest", "minecraft:chest"}, {"Barrel", "minecraft:barrel"}} {
		t.Run(tc.name, func(t *testing.T) {
			want := run(tg.vanilla, "OffV"+tc.name, tc.block)
			got := run(tg.mcgo, "OffM"+tc.name, tc.block)
			if len(want) != len(got) {
				t.Fatal("snapshot count")
			}
			for i := range want {
				if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
					t.Errorf("step%d: %s", i, fmt.Sprint(diffs))
				}
			}
		})
	}
}
