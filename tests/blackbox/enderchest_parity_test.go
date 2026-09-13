package blackbox

// Ender-chest parity: the menu opens over the player's own ender
// storage, clicks move stacks between that storage and the player, and
// the storage follows the player across close, reopen, and the block
// being broken. One script runs on both servers; logical slot state is
// compared after every step, and the open title bytes exactly.

import (
	"bytes"
	"testing"
	"time"
)

func TestEnderChestParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("EnderV", "EnderM"),
	}, nil)
	defer tg.cleanup()

	steps := []struct {
		tag        string
		slot       int16
		button     byte
		mode       int32
		closeFirst bool
		reopen     bool
		moveChest  bool
		expectOpen bool
	}{
		{tag: "setup", expectOpen: true},
		{tag: "pickup-dirt", slot: 54, button: 0, mode: 0},
		{tag: "place-ender0", slot: 0, button: 0, mode: 0},
		{tag: "half-cobble", slot: 55, button: 1, mode: 0},
		{tag: "place-one", slot: 2, button: 1, mode: 0},
		{tag: "merge-rest", slot: 2, button: 0, mode: 0},
		{tag: "shift-ender0", slot: 0, button: 0, mode: 1},
		{tag: "shift-player", slot: 55, button: 0, mode: 1},
		{tag: "close", closeFirst: true},
		{tag: "reopen", reopen: true, expectOpen: true},
		{tag: "close2", closeFirst: true},
		{tag: "break-move", moveChest: true, expectOpen: true},
	}

	run := func(addr, name string) ([]clickSnap, [][]byte) {
		d := newClickDriverOn(t, addr, name, "minecraft:ender_chest", 2, [][2]string{
			{"hotbar.0", "minecraft:dirt 64"},
			{"hotbar.1", "minecraft:cobblestone 32"},
		}, [][2]string{
			{"enderchest.1", "minecraft:oak_planks 16"},
		})
		snaps := make([]clickSnap, 0, len(steps))
		var titles [][]byte
		seq := int32(12)
		for _, s := range steps {
			switch {
			case s.closeFirst:
				d.close()
			case s.reopen:
				d.reopen(8, -61, 0, seq, "reopen")
				seq++
			case s.moveChest:
				sendCommand(t, d.pw, "setblock 8 -61 0 minecraft:air")
				if !digSentinel(t, d.pw, d.sink, "BRK", 10*time.Second) {
					t.Fatalf("%s: no break", name)
				}
				sendCommand(t, d.pw, "setblock 10 -61 0 minecraft:ender_chest")
				if !digSentinel(t, d.pw, d.sink, "SB2", 10*time.Second) {
					t.Fatalf("%s: no setblock2", name)
				}
				d.sink.take()
				d.reopen(10, -61, 0, seq, "reopen-moved")
				seq++
			case !s.expectOpen:
				d.click(s.tag, s.slot, s.button, s.mode, false, false)
			}
			if s.expectOpen {
				v := judgeUse(d.opened)
				if !v.open || v.menu != 2 {
					t.Fatalf("%s: step %s did not open (open=%v menu=%d)",
						name, s.tag, v.open, v.menu)
				}
				titles = append(titles, v.title)
			}
			cp := clickSnap{carried: d.snap.carried, menu: map[int32]string{}, p0: map[int32]string{}}
			for k, v := range d.snap.menu {
				cp.menu[k] = v
			}
			for k, v := range d.snap.p0 {
				cp.p0[k] = v
			}
			snaps = append(snaps, cp)
		}
		return snaps, titles
	}

	want, wantTitles := run(tg.vanilla, "EnderV")
	got, gotTitles := run(tg.mcgo, "EnderM")
	for i, s := range steps {
		if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
			for _, df := range diffs {
				t.Errorf("step %s: %s", s.tag, df)
			}
		}
	}
	for i := range wantTitles {
		if !bytes.Equal(wantTitles[i], gotTitles[i]) {
			t.Errorf("open %d: title vanilla=%x mc-go=%x", i, wantTitles[i], gotTitles[i])
		}
	}
}
