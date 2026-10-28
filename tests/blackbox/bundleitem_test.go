package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestBundleParity pins the measured 26.2 bundle (BundleItem /
// BundleContents) behavior against the real server.
//
// Chest-menu clicks cover both click overrides: a carried bundle
// left-clicked onto a stack pulls in as much as its 1/64-unit weight
// budget allows (pearls weigh 1/16, a sword a whole bundle, a shulker
// box never fits), right-clicked onto an empty slot it lets out its
// front entry; a carried stack left-clicked onto a bundle in a slot
// goes in, an empty cursor right-clicking it takes the front entry
// out. Merged entries move to the front, a Select Bundle Item packet
// picks which entry the next removal takes, a bundle nests at 1/16
// plus its own contents, and an emptied bundle drops back to a bare
// stack. A carried bundle right-clicked onto a different stack takes
// doClick's plain swap.
//
// Window-0 use then drops the contents one entry per use tick — the
// selected one first — with the bundle slot re-sent after each drop
// and the drop-contents sound (the only bundle sound the user's own
// client is sent) after each one. The insert/remove sounds go to
// everyone but the clicking player, so a lone client hears none.
func TestBundleParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BunV", "BunM"),
	}, nil)

	type result struct {
		steps []string
		use   []string
		snds  map[string]int
	}
	sndNames := map[int32]string{}
	for _, n := range []string{"item.bundle.insert", "item.bundle.insert_fail", "item.bundle.remove_one", "item.bundle.drop_contents"} {
		if id, ok := server.SoundEventID(n); ok {
			sndNames[id+1] = n
		}
	}

	run := func(addr, name string) result {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{
			{"hotbar.0", "minecraft:bundle 1"},
			{"hotbar.1", "minecraft:dirt 40"},
			{"hotbar.2", "minecraft:ender_pearl 16"},
			{"hotbar.3", "minecraft:iron_sword 1"},
			{"hotbar.4", "minecraft:shulker_box 1"},
			{"hotbar.5", `minecraft:red_bundle[minecraft:bundle_contents=[{id:"minecraft:stone",count:2}]] 1`},
			{"hotbar.6", `minecraft:bundle[minecraft:bundle_contents=[{id:"minecraft:stick",count:2},{id:"minecraft:dirt",count:5},{id:"minecraft:ender_pearl"}]] 1`},
			{"inventory.0", "minecraft:dirt 30"},
		}, nil)
		var res result
		step := func(tag string) {
			d.fold()
			res.steps = append(res.steps, fmt.Sprintf("%s: car=%s m=%v", tag, d.snap.carried, d.snap.menu))
		}
		// Chest slots 0-26, inventory.i at 27+i, hotbar.i at 54+i.
		d.click("pick-bundle", 54, 0, 0, false, false)
		d.click("suck-dirt", 55, 0, 0, false, false)
		step("dirt-in")
		d.click("suck-pearls", 56, 0, 0, false, false)
		step("pearls-in")
		d.click("sword-too-heavy", 57, 0, 0, false, false)
		d.click("shulker-refused", 58, 0, 0, false, false)
		d.click("full", 27, 0, 0, false, false)
		step("refusals")
		d.click("out-front", 0, 1, 0, false, false)
		d.click("out-next", 1, 1, 0, false, false)
		d.click("out-empty", 2, 1, 0, false, false)
		step("emptied")
		d.click("place-bundle", 3, 0, 0, false, false)
		d.click("take-dirt", 1, 0, 0, false, false)
		d.click("dirt-into-slot-bundle", 3, 0, 0, false, false)
		d.click("take-pearls", 0, 0, 0, false, false)
		d.click("pearls-into-slot-bundle", 3, 0, 0, false, false)
		step("slot-bundle-filled")
		selectBundleItem(t, d.pw, 3, 1)
		d.click("take-selected", 3, 1, 0, false, false)
		step("selected-out")
		d.click("merge-front", 3, 0, 0, false, false)
		d.click("take-front", 3, 1, 0, false, false)
		d.click("park-dirt", 4, 0, 0, false, false)
		d.click("take-last", 3, 1, 0, false, false)
		d.click("park-pearls", 5, 0, 0, false, false)
		step("slot-bundle-emptied")
		d.click("pick-red", 59, 0, 0, false, false)
		d.click("red-out", 6, 1, 0, false, false)
		d.click("nest-bundle", 3, 0, 0, false, false)
		step("nested")
		d.click("swap-on-right", 4, 1, 0, false, false)
		step("swapped")
		d.click("park-swapped", 7, 0, 0, false, false)
		step("parked")
		d.close()
		time.Sleep(300 * time.Millisecond)
		d.sink.take()

		// Window 0: hold hotbar.6 (menu slot 42), select its dirt
		// entry, then use it until every entry has dropped.
		var hb protocol.Buffer
		hb.Short(6)
		if err := d.pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		selectBundleItem(t, d.pw, 42, 1)
		time.Sleep(200 * time.Millisecond)
		sendUseItemAim(t, d.pw, 0, 7, 0, 0)
		time.Sleep(2 * time.Second)
		var rb protocol.Buffer
		rb.VarInt(5) // RELEASE_USE_ITEM
		rb.Position(0, 0, 0)
		rb.Byte(0)
		rb.VarInt(0)
		_ = d.pw.WritePacket(server.IDPlayPlayerActionSB, rb.Bytes())
		time.Sleep(500 * time.Millisecond)
		res.snds = map[string]int{}
		for _, p := range d.sink.take() {

			switch p.id {
			case server.IDPlayContainerSlot:
				r := protocol.NewPacketReader(p.data)
				wid, _ := r.VarInt()
				_, _ = r.VarInt()
				slot, _ := r.Short()
				it, err := readSlotBB(r)
				if err == nil && wid == 0 && slot == 42 {
					res.use = append(res.use, stackKey(it))
				}
			case 0x75: // positioned sound (26.2)
				if id, err := protocol.NewPacketReader(p.data).VarInt(); err == nil {
					if n, ok := sndNames[id]; ok {
						res.snds[n]++
					}
				}
			}
		}
		return res
	}

	want := run(tg.vanilla, "BunV")
	got := run(tg.mcgo, "BunM")
	for i := range want.steps {
		if i >= len(got.steps) || got.steps[i] != want.steps[i] {
			g := "<missing>"
			if i < len(got.steps) {
				g = got.steps[i]
			}
			t.Errorf("step %d:\n  vanilla %s\n  mc-go   %s", i, want.steps[i], g)
		}
	}
	if fmt.Sprint(want.use) != fmt.Sprint(got.use) {
		t.Errorf("use drops:\n  vanilla %v\n  mc-go   %v", want.use, got.use)
	}
	if fmt.Sprint(want.snds) != fmt.Sprint(got.snds) {
		t.Errorf("use sounds heard by the user:\n  vanilla %v\n  mc-go   %v", want.snds, got.snds)
	}
	// Guard the oracle leg itself: the selected dirt leaves first, then
	// the front stick, then the pearl, each with its drop sound.
	bundleID, _ := world.ItemID("bundle")
	stickID, _ := world.ItemID("stick")
	pearlID, _ := world.ItemID("ender_pearl")
	wantUse := fmt.Sprint([]string{
		fmt.Sprintf("%d#1[bun%d#2|%d#1]", bundleID, stickID, pearlID),
		fmt.Sprintf("%d#1[bun%d#1]", bundleID, pearlID),
		fmt.Sprintf("%d#1", bundleID),
	})
	if fmt.Sprint(want.use) != wantUse || want.snds["item.bundle.drop_contents"] != 3 {
		t.Errorf("vanilla use leg: %v sounds %v, want %s and 3 drops", want.use, want.snds, wantUse)
	}
}

// selectBundleItem sends Select Bundle Item: VarInt menu slot, VarInt
// entry index (-1 clears).
func selectBundleItem(t *testing.T, pw *protocol.Writer, slot, idx int32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(slot)
	b.VarInt(idx)
	if err := pw.WritePacket(server.IDPlayBundleItemSelectedSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// TestContainerItemSpillParity pins ItemStack.onDestroyed for item
// entities lava burns away: a bundle (BundleItem) and a shulker box
// (BlockItem's container branch) each spill every stored stack as a
// new item entity where they died. Fire-immune netherite survives the
// lava to be counted; the burnable dirt that spilled with it burns too.
func TestContainerItemSpillParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("SP")}, nil)
	defer targets.cleanup()
	queries := []string{
		`execute if entity @e[type=item,nbt={Item:{id:"minecraft:netherite_ingot",count:3}}]`,
		`execute if entity @e[type=item,nbt={Item:{id:"minecraft:netherite_scrap",count:2}}]`,
		`execute if entity @e[type=item,nbt={Item:{id:"minecraft:bundle"}}]`,
		`execute if entity @e[type=item,nbt={Item:{id:"minecraft:shulker_box"}}]`,
		`execute if entity @e[type=item,nbt={Item:{id:"minecraft:dirt"}}]`,
	}
	results := map[string][]int32{}
	for _, side := range []struct{ label, addr string }{{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, side.addr, "SP")
		sendClientInformation(t, pw)
		time.Sleep(400 * time.Millisecond)
		drainChunkWindow(t, side.label, c, pr, pw, 300*time.Millisecond)
		for _, cmd := range []string{
			"gamerule do_mob_spawning false",
			"kill @e[type=!minecraft:player]",
			"fill 9 -60 9 11 -56 9 minecraft:stone",
			"fill 9 -60 11 11 -56 11 minecraft:stone",
			"fill 9 -60 9 9 -56 11 minecraft:stone",
			"fill 11 -60 9 11 -56 11 minecraft:stone",
			"fill 10 -60 10 10 -60 10 minecraft:lava",
			`summon item 10.5 -59.5 10.5 {Item:{id:"minecraft:bundle",count:1,components:{"minecraft:bundle_contents":[{id:"minecraft:netherite_ingot",count:3},{id:"minecraft:dirt",count:4}]}}}`,
			`summon item 10.5 -59.5 10.5 {Item:{id:"minecraft:shulker_box",count:1,components:{"minecraft:container":[{slot:0,item:{id:"minecraft:netherite_scrap",count:2}},{slot:5,item:{id:"minecraft:dirt",count:1}}]}}}`,
		} {
			sendCommand(t, pw, cmd)
		}
		pumpFor(c, pr, pw, 3*time.Second)
		for _, q := range queries {
			sendCommand(t, pw, q)
			results[side.label] = append(results[side.label], readPassCount(t, c, pr, pw))
		}
		c.Close()
	}
	if fmt.Sprint(results["vanilla"]) != "[1 1 0 0 0]" {
		t.Fatalf("vanilla spill counts %v, want [1 1 0 0 0]", results["vanilla"])
	}
	if fmt.Sprint(results["mcgo"]) != fmt.Sprint(results["vanilla"]) {
		t.Fatalf("spill counts (ingot/scrap/bundle/box/dirt): vanilla %v mc-go %v", results["vanilla"], results["mcgo"])
	}
}

// pumpFor reads and discards traffic for d, answering keep-alives
// and chunk batches so the session stays live and its chunks loaded.
func pumpFor(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		}
	}
	_ = c.SetReadDeadline(time.Time{})
}

// readPassCount reads system chat until an execute-if reply arrives:
// the pass_count value, or 0 for commands.execute.conditional.fail.
// Keep-alives and chunk batches are answered on the way.
func readPassCount(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) int32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySystemChatCB:
			data := string(p.Data)
			if strings.Contains(data, "conditional.fail") {
				return 0
			}
			if i := strings.Index(data, "with"); i >= 0 && strings.Contains(data, "conditional.pass_count") {
				r := protocol.NewPacketReader(p.Data[i+4:])
				if typ, err := r.Byte(); err == nil && typ == 3 {
					if n, err := r.Int(); err == nil && n == 1 {
						if v, err := r.Int(); err == nil {
							return v
						}
					}
				}
			}
		}
	}
	t.Fatal("no execute reply")
	return -1
}
