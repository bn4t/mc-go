package blackbox

// Throwaway probe: vanilla 26.2 anvil repair in SURVIVAL. Damage points,
// level costs, take/xp rules, quick-move, invalid combos, close order.
// Deleted once encoded. Clicks are written so every step is a no-op-safe
// absolute slot pair; mine MCGO_CLICKDEBUG raw hex (esp. 0x67 xp, 0x13
// cost, 0x2e level event) alongside the dumps.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestProbeAnvilSurv(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ProbeA"),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, "ProbeA")
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	d := &clickDriver{
		t:     t,
		name:  "ProbeA",
		sink:  newUseClient(pr, pw, true),
		pw:    pw,
		snap:  clickSnap{carried: "-", menu: map[int32]string{}, p0: map[int32]string{}},
		debug: true,
	}
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
		t.Fatal("never ready")
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	mk := func(cmd, want string) {
		sendCommand(t, pw, cmd)
		if !digSentinel(t, pw, d.sink, want, 10*time.Second) {
			t.Fatalf("no %s for %q", want, cmd)
		}
	}
	mk("tp @s 6 -60 2 0 0", "TPOK")
	mk("setblock 8 -61 0 minecraft:anvil", "SBOK")
	dmgs := []string{"0.5", "0.5", "0.8", "0.8", "0.9", "0.2"}
	for i, f := range dmgs {
		mk("item replace entity @s hotbar."+string(rune('0'+i))+" with minecraft:iron_pickaxe 1", "IT"+string(rune('0'+i)))
		mk(`item modify entity @s hotbar.`+string(rune('0'+i))+` {"function":"set_damage","damage":`+f+`}`, "DM"+string(rune('0'+i)))
		for _, p := range d.sink.take() {
			t.Logf("dmg%d id=%02x %x", i, p.id, p.data)
		}
	}
	mk("item replace entity @s hotbar.6 with minecraft:dirt 64", "IT6")
	mk("item replace entity @s hotbar.7 with minecraft:stone 64", "IT7")
	// R4 reserve: two damaged picks in main inventory (menu slots 3, 4),
	// untouched until the poor-take round.
	for i, f := range []string{"0.9", "0.9"} {
		mk("item replace entity @s inventory."+string(rune('0'+i))+" with minecraft:iron_pickaxe 1", "RS"+string(rune('0'+i)))
		mk(`item modify entity @s inventory.`+string(rune('0'+i))+` {"function":"set_damage","damage":`+f+`}`, "RM"+string(rune('0'+i)))
		for _, p := range d.sink.take() {
			t.Logf("rsv%d id=%02x %x", i, p.id, p.data)
		}
	}
	mk("xp set @s 30 levels", "XPOK")
	for _, p := range d.sink.take() {
		t.Logf("setup id=%02x %x", p.id, p.data)
	}
	d.sink.take()
	d.reopen(8, -61, 0, 11, "use-open")
	t.Logf("open menu=%d win=%d", judgeUse(d.opened).menu, d.win)
	dump := func(tag string) {
		t.Helper()
		t.Logf("%s carried=%s menu=%v p0=%v", tag, d.snap.carried, d.snap.menu, d.snap.p0)
	}
	dump("opened")
	// R1: 125+125. Take pays xp; park the repaired pick at free hotbar.8.
	d.click("r1a-30-0", 30, 0, 0, false, false)
	dump("r1a")
	d.click("r1b-31-1", 31, 0, 0, false, false)
	dump("r1b")
	d.click("r1-take", 2, 0, 0, false, false)
	dump("r1-take")
	d.click("r1-park38", 38, 0, 0, false, false)
	dump("r1-park")
	// Invalid combo: dirt + pick -> output?
	d.click("inv-dirt36-0", 36, 0, 0, false, false)
	dump("inv-d0")
	d.click("inv-pick32-1", 32, 0, 0, false, false)
	dump("inv-p1")
	d.click("inv-dirt-back0", 0, 0, 0, false, false)
	dump("inv-dback")
	d.click("inv-dirt-back36", 36, 0, 0, false, false)
	dump("inv-dback36")
	d.click("inv-pick-back1", 1, 0, 0, false, false)
	dump("inv-pback1")
	d.click("inv-pick-back32", 32, 0, 0, false, false)
	dump("inv-pback32")
	// R3: 200+200. Quick-move the output instead of taking it.
	d.click("r3a-32-0", 32, 0, 0, false, false)
	dump("r3a")
	d.click("r3b-33-1", 33, 0, 0, false, false)
	dump("r3b")
	d.click("r3-qm-out", 2, 0, 1, false, false)
	dump("r3-qm-out")
	// Quick-move routing: damaged pick, then stone.
	d.click("r3-qm-in34", 34, 0, 1, false, false)
	dump("r3-qm-in34")
	d.click("r3-qm-in37", 37, 0, 1, false, false)
	dump("r3-qm-in37")
	// Return whatever moved (each pair is no-op-safe in every case).
	d.click("r3-ret0", 0, 0, 0, false, false)
	dump("r3-ret0a")
	d.click("r3-ret34", 34, 0, 0, false, false)
	dump("r3-ret34")
	d.click("r3-ret1", 1, 0, 0, false, false)
	dump("r3-ret1a")
	d.click("r3-ret37", 37, 0, 0, false, false)
	dump("r3-ret37")
	// Empty-output click.
	d.click("r3-empty-out", 2, 0, 0, false, false)
	dump("r3-empty-out")
	// Manual place into the output with dirt on the cursor.
	d.click("r3-cur-dirt36", 36, 0, 0, false, false)
	dump("r3-cur-dirt")
	d.click("r3-place-out", 2, 0, 0, false, false)
	dump("r3-place-out")
	d.click("r3-dirt-back36", 36, 0, 0, false, false)
	dump("r3-dirt-back")
	// R4: expensive combo, take with 0 levels.
	mk("xp set @s 0 levels", "XP0")
	for _, p := range d.sink.take() {
		t.Logf("xp0 id=%02x %x", p.id, p.data)
	}
	d.click("r4a-3-0", 3, 0, 0, false, false)
	dump("r4a")
	d.click("r4b-4-1", 4, 0, 0, false, false)
	dump("r4b")
	d.click("r4-poor-take", 2, 0, 0, false, false)
	dump("r4-poor-take")
	d.close()
	dump("close")
}
