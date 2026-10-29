//go:build blackbox

package blackbox

import (
	"fmt"
	"reflect"
	"testing"
)

func TestDoubleChestMenuParity(t *testing.T) {
	names := []string{"DoubleV", "DoubleM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:stick 12"}, {"hotbar.1", "minecraft:stick 1"}}, nil)
		d.close()
		off := 0
		for _, c := range []byte(name) {
			off = (off*31 + int(c)) & 15
		}
		x := 8 + 32*off
		command := func(text string) {
			sendCommand(t, d.pw, text)
			settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "double-setup")
		}
		command(fmt.Sprintf("setblock %d -61 0 minecraft:chest[facing=north,type=right]", x+1))
		for _, seed := range []struct {
			x, slot int
			item    string
		}{{x, 0, "stone 3"}, {x, 26, "coal 2"}, {x + 1, 0, "iron_ingot 4"}, {x + 1, 26, "gold_ingot 5"}} {
			command(fmt.Sprintf("item replace block %d -61 0 container.%d with minecraft:%s", seed.x, seed.slot, seed.item))
		}
		var out []string
		sample := func(tag string) {
			out = append(out, fmt.Sprintf("%s menu=%d size=%d slots=%v cursor=%s", tag, judgeUse(d.opened).menu, len(d.snap.menu), d.snap.menu, d.snap.carried))
		}
		d.reopen(int32(x), -61, 0, 12, "double-open-left")
		sample("left")
		d.click("double-lift", 81, 0, 0, false, false)
		d.click("double-drag-start", -999, 0, 5, false, false)
		for _, slot := range []int16{1, 28, 52} {
			d.click("double-drag-cell", slot, 1, 5, false, false)
		}
		d.click("double-drag-end", -999, 2, 5, false, false)
		sample("drag")
		d.click("double-seed", 82, 0, 0, false, false)
		d.click("double-collect", 2, 0, 6, false, false)
		sample("collect")
		d.close()
		d.reopen(int32(x+1), -61, 0, 13, "double-open-right")
		sample("right")
		command("tick freeze")
		command(fmt.Sprintf("item replace block %d -61 0 container.4 with minecraft:diamond 2", x))
		command(fmt.Sprintf("item replace block %d -61 0 container.5 with minecraft:emerald 3", x+1))
		sample("external-halves")
		command("tick unfreeze")
		d.close()
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	for i := range v {
		t.Logf("step%d vanilla=%s mcgo=%s", i, v[i], g[i])
		if !reflect.DeepEqual(v[i], g[i]) {
			t.Errorf("step%d double chest differs", i)
		}
	}
}

func TestDoubleChestObstructionParity(t *testing.T) {
	names := []string{"BlockDoubleV", "BlockDoubleM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, nil, nil)
		d.close()
		off := 0
		for _, c := range []byte(name) {
			off = (31*off + int(c)) & 15
		}
		x := 8 + 32*off
		command := func(text string) {
			sendCommand(t, d.pw, text)
			settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "double-obstruction-setup")
		}
		command(fmt.Sprintf("setblock %d -61 0 minecraft:chest[facing=north,type=right]", x+1))
		command("tick freeze")
		seq := int32(30)
		var out []string
		use := func(tag string, clicked int) {
			sendUseItemOn(t, d.pw, int32(clicked), -61, 0, 1, seq)
			seq++
			var ps []usePkt
			settleUse(t, d.sink, func(p []usePkt) { ps = append(ps, p...) }, "double-obstruction-use")
			v := judgeUse(ps)
			out = append(out, fmt.Sprintf("%s open=%v menu=%d", tag, v.open, v.menu))
			if v.open {
				d.opened = ps
				d.foldPkts(ps)
				d.close()
			}
		}
		command(fmt.Sprintf("setblock %d -60 0 minecraft:stone", x+1))
		use("partner-stone", x)
		use("self-stone", x+1)
		command(fmt.Sprintf("setblock %d -60 0 minecraft:glass", x+1))
		use("partner-glass", x)
		command(fmt.Sprintf("setblock %d -60 0 minecraft:air", x+1))
		command(fmt.Sprintf("setblock %d -60 0 minecraft:stone", x))
		use("left-stone-from-right", x+1)
		command(fmt.Sprintf("setblock %d -60 0 minecraft:air", x))
		command(fmt.Sprintf("summon minecraft:cat %.1f -60 0.5 {NoAI:1b,Sitting:0b}", float64(x)+1.5))
		use("standing-cat", x)
		command("kill @e[type=minecraft:cat]")
		command(fmt.Sprintf("summon minecraft:cat %.1f -60 0.5 {NoAI:1b,Sitting:1b}", float64(x)+1.5))
		use("sitting-cat-partner", x)
		use("sitting-cat-self", x+1)
		command("kill @e[type=minecraft:cat]")
		command("tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	t.Logf("vanilla=%v mcgo=%v", v, g)
	if !reflect.DeepEqual(v, g) {
		t.Error("double chest obstruction differs")
	}
}

func TestDoubleChestSharedViewParity(t *testing.T) {
	names := []string{"ShareChestV", "ShareChestM", "ShareWatchV", "ShareWatchM"}
	for _, i := range []int{0, 1} {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			off := 0
			for _, b := range []byte(candidate) {
				off = (31*off + int(b)) & 15
			}
			if off == 0 {
				names[i] = candidate
				break
			}
		}
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name, watchName string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:dirt 8"}}, nil)
		d.close()
		command := func(text string) {
			sendCommand(t, d.pw, text)
			settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "double-share-setup")
		}
		command("setblock 9 -61 0 minecraft:chest[facing=north,type=right]")
		command("tick freeze")
		d.reopen(8, -61, 0, 40, "share-left")
		other := newMenuLifecycleObserver(t, addr, watchName)
		sendCommand(t, other.pw, "tp @s 6 -60 2 0 0")
		settleUse(t, other.sink, func(ps []usePkt) { other.foldPkts(ps) }, "share-observer-tp")
		other.reopen(9, -61, 0, 41, "share-right")
		var out []string
		settle := func(view *clickDriver) {
			settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "shared-menu-broadcast")
		}
		sample := func(tag string) {
			out = append(out, fmt.Sprintf("%s actor0=%s actor27=%s actor29=%s observer0=%s observer27=%s observer29=%s cursors=%s/%s", tag, d.snap.menu[0], d.snap.menu[27], d.snap.menu[29], other.snap.menu[0], other.snap.menu[27], other.snap.menu[29], d.snap.carried, other.snap.carried))
		}
		d.click("share-left-put", 27, 0, 2, false, false)
		settle(other)
		sample("left-put")
		other.click("share-left-take", 27, 0, 0, false, false)
		settle(d)
		sample("left-take")
		other.click("share-right-put", 0, 0, 0, false, false)
		settle(d)
		sample("right-put")
		command("item replace block 8 -61 0 container.2 with minecraft:gold_ingot 3")
		settle(other)
		sample("external-left")
		other.close()
		d.close()
		other.reopen(8, -61, 0, 42, "share-reopen-left")
		out = append(out, fmt.Sprintf("reopen0=%s reopen29=%s", other.snap.menu[0], other.snap.menu[29]))
		other.close()
		command("tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0], names[2]), run(tg.mcgo, names[1], names[3])
	for i := range v {
		t.Logf("step%d vanilla=%s mcgo=%s", i, v[i], g[i])
		if v[i] != g[i] {
			t.Errorf("step%d shared chest differs", i)
		}
	}
}
