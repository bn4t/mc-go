package blackbox

// Anvil repair-combine parity: the same survival script runs on both
// servers — two charged takes (hand and quick-move), a poor take with
// no levels, an invalid dirt+pick pair, empty-output and
// place-into-output clicks, then a close with the inputs full and a
// reopen. Logical slot state (damage- and repair-cost-sensitive),
// the level cost, XP levels, anvil-use sounds, Set Player Inventory
// returns, the open title bytes, and the absence of a Container Close
// echo are compared after every step.

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

type anvilSnap struct {
	snap     clickSnap
	cost     int32
	costSeen bool
	xp       int32
	xpSeen   bool
	sounds   int
	close    bool
	invSets  int
	title    []byte
	death    string
}

func snapAnvil(d *clickDriver) anvilSnap {
	cp := clickSnap{carried: d.snap.carried, menu: map[int32]string{}, p0: map[int32]string{}}
	for k, v := range d.snap.menu {
		cp.menu[k] = v
	}
	for k, v := range d.snap.p0 {
		cp.p0[k] = v
	}
	return anvilSnap{snap: cp, cost: d.cost, costSeen: d.costSeen,
		xp: d.xpLevel, xpSeen: d.xpSeen, sounds: d.sounds,
		close: d.sawClose, invSets: d.invSets, death: d.death}
}

func TestAnvilParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AnvilV", "AnvilM"),
	}, nil)
	defer tg.cleanup()

	mkSetup := func(d *clickDriver, cmds ...string) {
		d.t.Helper()
		for i, cmd := range cmds {
			sendCommand(d.t, d.pw, cmd)
			if !digSentinel(d.t, d.pw, d.sink, fmt.Sprintf("AX%d", i), 10*time.Second) {
				d.t.Fatalf("%s: no setup ack %d for %q", d.name, i, cmd)
			}
		}
	}

	run := func(addr, name string) []anvilSnap {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		t.Cleanup(func() { c.Close() })
		_ = c.SetDeadline(time.Time{})
		d := &clickDriver{
			t:     t,
			name:  name,
			sink:  newUseClient(pr, pw, true),
			pw:    pw,
			snap:  clickSnap{carried: "-", menu: map[int32]string{}, p0: map[int32]string{}},
			debug: os.Getenv("MCGO_CLICKDEBUG") != "",
		}
		sendClientInformation(t, pw)
		if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
			t.Fatalf("%s: never ready", name)
		}
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		var snaps []anvilSnap
		mkSetup(d,
			// A flat world at y=-61 is slime country; survival spells
			// "killed, inventory dropped, menu dead" on vanilla. No test
			// here needs natural spawns.
			"gamerule minecraft:spawn_mobs false",
			"kill @e[type=minecraft:slime]",
			"tp @s 6 -60 2 0 0",
			"setblock 8 -61 0 minecraft:anvil",
			"item replace entity @s hotbar.0 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.0 {"function":"set_damage","damage":0.5}`,
			"item replace entity @s hotbar.1 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.1 {"function":"set_damage","damage":0.5}`,
			"item replace entity @s hotbar.2 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.2 {"function":"set_damage","damage":0.9}`,
			"item replace entity @s hotbar.3 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.3 {"function":"set_damage","damage":0.9}`,
			"item replace entity @s hotbar.4 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.4 {"function":"set_damage","damage":0.5}`,
			"item replace entity @s hotbar.5 with minecraft:iron_pickaxe 1",
			`item modify entity @s hotbar.5 {"function":"set_damage","damage":0.5}`,
			"item replace entity @s hotbar.6 with minecraft:dirt 64",
			"item replace entity @s hotbar.7 with minecraft:stone 64",
			"xp set @s 30 levels",
			"gamemode survival",
		)
		d.sink.take()
		d.reopen(8, -61, 0, 11, "use-open")
		v := judgeUse(d.opened)
		if !v.open || v.menu != 8 {
			t.Fatalf("%s: anvil did not open (open=%v menu=%d)", name, v.open, v.menu)
		}
		s := snapAnvil(d)
		s.title = v.title
		snaps = append(snaps, s)

		step := func(tag string, slot int16, button byte, mode int32) {
			d.click(tag, slot, button, mode, false, false)
			snaps = append(snaps, snapAnvil(d))
		}
		// Every take rolls a 12% chance to wear the anvil down; three
		// degrades in a row break it and strand the window. Re-setting
		// the block after each take keeps the script deterministic —
		// the degrade roll itself is covered by TestAnvilDegradeParity.
		reset := func() {
			// Thrown leftovers land near the pickup boundary and can
			// be re-collected mid-scenario on either server — clear
			// them so the window state stays deterministic.
			mkSetup(d, "setblock 8 -61 0 minecraft:anvil",
				"kill @e[type=minecraft:item]")
			d.fold()
		}
		// Charged take by hand: 125+125 costs 2 of 30 levels.
		step("ch-p30", 30, 0, 0)
		step("ch-i0", 0, 0, 0)
		step("ch-p31", 31, 0, 0)
		step("ch-i1", 1, 0, 0)
		step("ch-take", 2, 0, 0)
		reset()
		step("ch-park38", 38, 0, 0)
		// Charged quick-move take: 125+125 again, output rides home.
		step("qm-p34", 34, 0, 0)
		step("qm-i0", 0, 0, 0)
		step("qm-p35", 35, 0, 0)
		step("qm-i1", 1, 0, 0)
		step("qm-out", 2, 0, 1)
		reset()
		// Poor take: no levels left, nothing moves.
		sendCommand(t, pw, "xp set @s 0 levels")
		if !digSentinel(t, pw, d.sink, "AXP", 10*time.Second) {
			t.Fatalf("%s: no xp drain ack", name)
		}
		d.fold()
		snaps = append(snaps, snapAnvil(d))
		step("poor-p32", 32, 0, 0)
		step("poor-i0", 0, 0, 0)
		step("poor-p33", 33, 0, 0)
		step("poor-i1", 1, 0, 0)
		step("poor-take", 2, 0, 0)
		// Back out, then the invalid pair: dirt+pick computes nothing.
		step("poor-ret0", 0, 0, 0)
		step("poor-back32", 32, 0, 0)
		step("poor-ret1", 1, 0, 0)
		step("poor-back33", 33, 0, 0)
		step("inv-dirt0", 36, 0, 0)
		step("inv-dirt-in", 0, 0, 0)
		step("inv-pick1", 32, 0, 0)
		step("inv-pick-in", 1, 0, 0)
		step("inv-take", 2, 0, 0)
		reset()
		step("inv-dirt-back", 0, 0, 0)
		step("inv-dirt36", 36, 0, 0)
		step("inv-pick-back", 1, 0, 0)
		step("inv-pick32", 32, 0, 0)
		// Empty output with an empty cursor is silent.
		step("empty-out", 2, 0, 0)
		// Placing into a computed output is silently ignored.
		step("blk-p32", 32, 0, 0)
		step("blk-i0", 0, 0, 0)
		step("blk-p33", 33, 0, 0)
		step("blk-i1", 1, 0, 0)
		step("blk-stone37", 37, 0, 0)
		step("blk-place2", 2, 0, 0)
		step("blk-stone-back", 37, 0, 0)
		step("blk-poor-take", 2, 0, 0)
		// Close with the inputs full: they return, the output
		// vanishes, the cursor is untouched, no close is echoed.
		d.close()
		snaps = append(snaps, snapAnvil(d))
		d.reopen(8, -61, 0, 12, "use-reopen")
		v = judgeUse(d.opened)
		if !v.open || v.menu != 8 {
			t.Fatalf("%s: anvil did not reopen (open=%v menu=%d)", name, v.open, v.menu)
		}
		s = snapAnvil(d)
		s.title = v.title
		snaps = append(snaps, s)
		d.close()
		snaps = append(snaps, snapAnvil(d))

		// --- phase 2: rename, material repair, the >39 gate -------
		// Fresh items and levels arrive while the window is closed so
		// both servers see the same inventory; parking lands in main
		// slots cleared here. The anvil is re-set between takes so the
		// degrade roll can never strand the window mid-script.
		mkSetup(d,
			"gamemode survival",
			"xp set @s 60 levels",
			"setblock 8 -61 0 minecraft:anvil",
			"item replace entity @s hotbar.0 with minecraft:iron_pickaxe 1",
			`item replace entity @s hotbar.1 with minecraft:iron_pickaxe[minecraft:custom_name="Gamma"] 1`,
			"item replace entity @s hotbar.2 with minecraft:iron_pickaxe[minecraft:damage=100] 1",
			"item replace entity @s hotbar.3 with minecraft:iron_pickaxe[minecraft:damage=100] 1",
			"item replace entity @s hotbar.4 with minecraft:diamond_pickaxe[minecraft:damage=1561] 1",
			"item replace entity @s hotbar.5 with minecraft:diamond 64",
			"item replace entity @s hotbar.6 with minecraft:diamond_pickaxe[minecraft:damage=1000] 1",
			"item replace entity @s hotbar.7 with minecraft:diamond 1",
			"item replace entity @s hotbar.8 with minecraft:iron_ingot 64",
			"item replace entity @s inventory.20 with minecraft:iron_pickaxe[minecraft:damage=100,minecraft:repair_cost=15] 1",
			"item replace entity @s inventory.21 with minecraft:iron_pickaxe[minecraft:damage=100,minecraft:repair_cost=30] 1",
			"item replace entity @s inventory.0 with minecraft:air",
			"item replace entity @s inventory.1 with minecraft:air",
			"item replace entity @s inventory.2 with minecraft:air",
			"item replace entity @s inventory.3 with minecraft:air",
			"item replace entity @s inventory.4 with minecraft:air",
			"item replace entity @s inventory.5 with minecraft:air",
		)
		d.fold()
		ren := func(tag, name string) {
			d.rename(tag, name)
			snaps = append(snaps, snapAnvil(d))
		}
		d.reopen(8, -61, 0, 13, "p2-open")
		v = judgeUse(d.opened)
		if !v.open || v.menu != 8 {
			t.Fatalf("%s: anvil did not reopen for phase 2 (open=%v menu=%d)", name, v.open, v.menu)
		}
		s = snapAnvil(d)
		s.title = v.title
		snaps = append(snaps, s)

		// Rename-only on a fresh pick: cost 1, output named.
		step("r2-p30", 30, 0, 0)
		step("r2-i0", 0, 0, 0)
		ren("r2-ren", "Alpha")
		step("r2-take", 2, 0, 0)
		step("r2-park3", 3, 0, 0)
		reset()
		// The pending name persists across takes: the Gamma pick
		// placed next computes an "Alpha" output before any rename.
		step("gm-p31", 31, 0, 0)
		step("gm-i0", 0, 0, 0)
		ren("gm-same", "Gamma") // renaming to its own name: nothing
		ren("gm-clr", "")       // the empty field clears the name
		step("gm-take", 2, 0, 0)
		step("gm-park4", 4, 0, 0)
		reset()
		// A fresh left input computes nothing, even with a damaged
		// right: pending "" on a now-plain item is no rename either.
		step("fr-p4", 4, 0, 0)
		step("fr-i0", 0, 0, 0)
		step("fr-p32", 32, 0, 0)
		step("fr-i1", 1, 0, 0)
		step("fr-b1", 1, 0, 0)
		step("fr-b32", 32, 0, 0)
		step("fr-b0", 0, 0, 0)
		step("fr-b4", 4, 0, 0)
		// Combine plus rename: cost 0+0+2+1, output "Beta" rc1.
		step("co-p32", 32, 0, 0)
		step("co-i0", 0, 0, 0)
		step("co-p33", 33, 0, 0)
		step("co-i1", 1, 0, 0)
		ren("co-ren", "Beta")
		step("co-take", 2, 0, 0)
		step("co-park5", 5, 0, 0)
		reset()
		ren("co-clr", "")
		// Material repair: the dead pick eats ceil(1561/390)=5
		// diamonds, output a fresh dpick with rc1, cost 5.
		step("mt-p34", 34, 0, 0)
		step("mt-i0", 0, 0, 0)
		step("mt-p35", 35, 0, 0)
		step("mt-i1", 1, 0, 0)
		step("mt-take", 2, 0, 0)
		step("mt-park6", 6, 0, 0)
		step("mt-dia1", 1, 0, 0)
		step("mt-dia35", 35, 0, 0)
		reset()
		// Partial repair: one diamond on damage 1000 leaves 610.
		step("pt-p36", 36, 0, 0)
		step("pt-i0", 0, 0, 0)
		step("pt-p37", 37, 0, 0)
		step("pt-i1", 1, 0, 0)
		step("pt-take", 2, 0, 0)
		step("pt-park7", 7, 0, 0)
		reset()
		// A wrong material computes nothing.
		step("wm-p7", 7, 0, 0)
		step("wm-i0", 0, 0, 0)
		step("wm-p38", 38, 0, 0)
		step("wm-i1", 1, 0, 0)
		step("wm-b1", 1, 0, 0)
		step("wm-b38", 38, 0, 0)
		step("wm-b0", 0, 0, 0)
		step("wm-b7", 7, 0, 0)
		// Cost past 39 in survival: priced (property 47), no output.
		step("hi-p23", 23, 0, 0)
		step("hi-i0", 0, 0, 0)
		step("hi-p24", 24, 0, 0)
		step("hi-i1", 1, 0, 0)
		step("hi-take", 2, 0, 0)
		step("hi-b0", 0, 0, 0)
		step("hi-b23", 23, 0, 0)
		step("hi-b1", 1, 0, 0)
		step("hi-b24", 24, 0, 0)
		// A throw on a live output takes it and drops it as an item.
		step("md-p4", 4, 0, 0)
		step("md-i0", 0, 0, 0)
		ren("md-ren", "Zed")
		step("md-throw", 2, 0, 4)
		reset()
		// A name past the 50-character cap is ignored: the pending
		// name stays "Zed" and the output is unchanged.
		step("ov-p3", 3, 0, 0)
		step("ov-i0", 0, 0, 0)
		ren("ov-long", strings.Repeat("q", 60))
		ren("ov-clr", "")
		step("ov-take", 2, 0, 0)
		step("ov-park3", 3, 0, 0)
		reset()
		// Renaming a worked item prices its repair_cost and keeps it.
		step("xp-p23", 23, 0, 0)
		step("xp-i0", 0, 0, 0)
		ren("xp-ren", "Rco")
		step("xp-take", 2, 0, 0)
		step("xp-park8", 8, 0, 0)

		d.close()
		snaps = append(snaps, snapAnvil(d))
		return snaps
	}

	want := run(tg.vanilla, "AnvilV")
	got := run(tg.mcgo, "AnvilM")
	tags := []string{"open",
		"ch-p30", "ch-i0", "ch-p31", "ch-i1", "ch-take", "ch-park38",
		"qm-p34", "qm-i0", "qm-p35", "qm-i1", "qm-out", "xp-drain",
		"poor-p32", "poor-i0", "poor-p33", "poor-i1", "poor-take",
		"poor-ret0", "poor-back32", "poor-ret1", "poor-back33",
		"inv-dirt0", "inv-dirt-in", "inv-pick1", "inv-pick-in", "inv-take",
		"inv-dirt-back", "inv-dirt36", "inv-pick-back", "inv-pick32",
		"empty-out",
		"blk-p32", "blk-i0", "blk-p33", "blk-i1", "blk-stone37",
		"blk-place2", "blk-stone-back", "blk-poor-take",
		"close", "reopen", "close2",
		"p2-open",
		"r2-p30", "r2-i0", "r2-ren", "r2-take", "r2-park3",
		"gm-p31", "gm-i0", "gm-same", "gm-clr", "gm-take", "gm-park4",
		"fr-p4", "fr-i0", "fr-p32", "fr-i1", "fr-b1", "fr-b32", "fr-b0", "fr-b4",
		"co-p32", "co-i0", "co-p33", "co-i1", "co-ren", "co-take", "co-park5", "co-clr",
		"mt-p34", "mt-i0", "mt-p35", "mt-i1", "mt-take", "mt-park6", "mt-dia1", "mt-dia35",
		"pt-p36", "pt-i0", "pt-p37", "pt-i1", "pt-take", "pt-park7",
		"wm-p7", "wm-i0", "wm-p38", "wm-i1", "wm-b1", "wm-b38", "wm-b0", "wm-b7",
		"hi-p23", "hi-i0", "hi-p24", "hi-i1", "hi-take", "hi-b0", "hi-b23", "hi-b1", "hi-b24",
		"md-p4", "md-i0", "md-ren", "md-throw",
		"ov-p3", "ov-i0", "ov-long", "ov-clr", "ov-take", "ov-park3",
		"xp-p23", "xp-i0", "xp-ren", "xp-take", "xp-park8",
		"close3"}
	if len(want) != len(tags) || len(got) != len(tags) {
		t.Fatalf("snap counts: vanilla=%d mc-go=%d want %d", len(want), len(got), len(tags))
	}
	for i, tag := range tags {
		if diffs := diffSnaps(want[i].snap, got[i].snap); len(diffs) > 0 {
			t.Errorf("step %s slots differ:\n  %s", tag, joinLines(diffs))
		}
		a, b := want[i], got[i]
		if a.costSeen != b.costSeen || (a.costSeen && a.cost != b.cost) {
			t.Errorf("step %s cost: vanilla=(%d,%v) mc-go=(%d,%v)",
				tag, a.cost, a.costSeen, b.cost, b.costSeen)
		}
		if a.xpSeen != b.xpSeen || (a.xpSeen && a.xp != b.xp) {
			t.Errorf("step %s xp: vanilla=(%d,%v) mc-go=(%d,%v)",
				tag, a.xp, a.xpSeen, b.xp, b.xpSeen)
		}
		if a.sounds != b.sounds {
			t.Errorf("step %s sounds: vanilla=%d mc-go=%d", tag, a.sounds, b.sounds)
		}
		if a.death != b.death {
			t.Errorf("step %s death: vanilla=%q mc-go=%q", tag, a.death, b.death)
		}
		if a.close != b.close {
			t.Errorf("step %s close echo: vanilla=%v mc-go=%v", tag, a.close, b.close)
		}
		if a.invSets != b.invSets {
			t.Errorf("step %s SetPlayerInventory: vanilla=%d mc-go=%d",
				tag, a.invSets, b.invSets)
		}
		if (a.title == nil) != (b.title == nil) || (a.title != nil && !bytes.Equal(a.title, b.title)) {
			t.Errorf("step %s title: vanilla=%x mc-go=%x", tag, a.title, b.title)
		}
	}
}

func joinLines(in []string) string {
	out := ""
	for i, l := range in {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}

// Anvil degrade parity: every take rolls a 12% chance to wear the anvil
// down a stage. The roll's position is random, so the script takes the
// same pick on both servers until a Block Update lands on the anvil's
// position (36 takes bounds the miss chance past 1e-4 per server) and
// counts how many updates each side saw.
func TestAnvilDegradeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("DgrV", "DgrM"),
	}, nil)
	defer tg.cleanup()

	anvilPos := [3]int32{8, -61, 0}
	run := func(addr, name string) int {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		d := &clickDriver{
			t:     t,
			name:  name,
			sink:  newUseClient(pr, pw, true),
			pw:    pw,
			snap:  clickSnap{carried: "-", menu: map[int32]string{}, p0: map[int32]string{}},
			debug: os.Getenv("MCGO_CLICKDEBUG") != "",
		}
		sendClientInformation(t, pw)
		if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
			t.Fatalf("%s: never ready", name)
		}
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		mkSetup := func(cmds ...string) {
			for i, cmd := range cmds {
				sendCommand(d.t, d.pw, cmd)
				if !digSentinel(d.t, d.pw, d.sink, fmt.Sprintf("DG%d", i), 10*time.Second) {
					d.t.Fatalf("%s: no setup ack %d for %q", d.name, i, cmd)
				}
			}
		}
		mkSetup(
			"gamerule minecraft:spawn_mobs false",
			"kill @e[type=minecraft:slime]",
			"tp @s 6 -60 2 0 0",
			"setblock 8 -61 0 minecraft:anvil",
			"item replace entity @s hotbar.0 with minecraft:iron_pickaxe 1",
			"gamemode creative",
		)
		d.sink.take()
		d.reopen(8, -61, 0, 11, "dgr-open")
		v := judgeUse(d.opened)
		if !v.open || v.menu != 8 {
			t.Fatalf("%s: anvil did not open (open=%v menu=%d)", name, v.open, v.menu)
		}

		// The pick ping-pongs through the anvil: each cycle places it,
		// flips the pending name so the output exists, and quick-takes
		// it back into the player rows. The rename is what makes the
		// output non-empty, and the name alternates so the next cycle's
		// input differs from the pending field again.
		src := int16(30) // hotbar.0 in menu coords; later cycles take from where the take landed
		for i := 0; i < 36 && d.blkUps[anvilPos] == 0; i++ {
			d.click(fmt.Sprintf("pick%d", i), src, 0, 0, false, false)
			d.click(fmt.Sprintf("in%d", i), 0, 0, 0, false, false)
			if i%2 == 0 {
				d.rename(fmt.Sprintf("ren%d", i), "Worn")
			} else {
				d.rename(fmt.Sprintf("ren%d", i), "Torn")
			}
			d.click(fmt.Sprintf("take%d", i), 2, 0, 1, false, false)
			// The quick-take parks the output into the player side
			// backward: the last hotbar slot first.
			src = 38
		}
		return d.blkUps[anvilPos]
	}

	v := run(tg.vanilla, "DgrV")
	m := run(tg.mcgo, "DgrM")
	if v == 0 {
		t.Error("vanilla never sent a Block Update at the anvil across 36 takes")
	}
	if m == 0 {
		t.Error("mc-go never sent a Block Update at the anvil across 36 takes")
	}
}
