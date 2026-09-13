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
		close: d.sawClose, invSets: d.invSets}
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
		// Charged take by hand: 125+125 costs 2 of 30 levels.
		step("ch-p30", 30, 0, 0)
		step("ch-i0", 0, 0, 0)
		step("ch-p31", 31, 0, 0)
		step("ch-i1", 1, 0, 0)
		step("ch-take", 2, 0, 0)
		step("ch-park38", 38, 0, 0)
		// Charged quick-move take: 125+125 again, output rides home.
		step("qm-p34", 34, 0, 0)
		step("qm-i0", 0, 0, 0)
		step("qm-p35", 35, 0, 0)
		step("qm-i1", 1, 0, 0)
		step("qm-out", 2, 0, 1)
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
		"close", "reopen", "close2"}
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
