package blackbox

// Throwaway probe: vanilla 26.2 anvil gaps left by the first two runs.
// Survival take (xp charge shape), uncapped repair bonus, quick-move of
// the output, quick-move of inputs back out, poor-take rejection, and a
// dirt+pick invalid combo. Every transfer is a pickup click plus a place
// click. Deleted once encoded.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestProbeAnvilGaps(t *testing.T) {
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
	// hotbar.0,1: 0.1 + 0.1 (dmg ~225 each, uncapped repair measures the
	// bonus). hotbar.2,3: 0.9 + 0.9 (poor-take pair). hotbar.4,5: 0.5 +
	// 0.5 (quick-move-output pair). hotbar.6: dirt. hotbar.7: fresh pick
	// for the invalid combo.
	setup := []struct{ slot, item, dmg string }{
		{"hotbar.0", "minecraft:iron_pickaxe 1", "0.1"},
		{"hotbar.1", "minecraft:iron_pickaxe 1", "0.1"},
		{"hotbar.2", "minecraft:iron_pickaxe 1", "0.9"},
		{"hotbar.3", "minecraft:iron_pickaxe 1", "0.9"},
		{"hotbar.4", "minecraft:iron_pickaxe 1", "0.5"},
		{"hotbar.5", "minecraft:iron_pickaxe 1", "0.5"},
		{"hotbar.6", "minecraft:dirt 64", ""},
		{"hotbar.7", "minecraft:iron_pickaxe 1", ""},
	}
	for i, s := range setup {
		mk("item replace entity @s "+s.slot+" with "+s.item, "IT"+string(rune('0'+i)))
		if s.dmg != "" {
			mk(`item modify entity @s `+s.slot+` {"function":"set_damage","damage":`+s.dmg+`}`, "DM"+string(rune('0'+i)))
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
	// Bonus round: 225+225 in, survival take (xp charge shape), park.
	d.click("bns-p30", 30, 0, 0, false, false)
	d.click("bns-i0", 0, 0, 0, false, false)
	d.click("bns-p31", 31, 0, 0, false, false)
	d.click("bns-i1", 1, 0, 0, false, false)
	dump("bns-in")
	d.click("bns-take", 2, 0, 0, false, false)
	dump("bns-take")
	d.click("bns-park38", 38, 0, 0, false, false)
	dump("bns-park")
	// Quick-move round: 125+125 in, shift-click the output out.
	d.click("qm-p34", 34, 0, 0, false, false)
	d.click("qm-i0", 0, 0, 0, false, false)
	d.click("qm-p35", 35, 0, 0, false, false)
	d.click("qm-i1", 1, 0, 0, false, false)
	dump("qm-in")
	d.click("qm-out", 2, 0, 1, false, false)
	dump("qm-out")
	// Poor round: 25+25 in with 0 levels, take must fail somehow.
	mk("xp set @s 0 levels", "XP0")
	for _, p := range d.sink.take() {
		t.Logf("xp0 id=%02x %x", p.id, p.data)
	}
	d.click("poor-p32", 32, 0, 0, false, false)
	d.click("poor-i0", 0, 0, 0, false, false)
	d.click("poor-p33", 33, 0, 0, false, false)
	d.click("poor-i1", 1, 0, 0, false, false)
	dump("poor-in")
	d.click("poor-take", 2, 0, 0, false, false)
	dump("poor-take")
	// Quick-move the stuck inputs back out, then dirt+pick invalid.
	d.click("poor-qm0", 0, 0, 1, false, false)
	dump("poor-qm0")
	d.click("poor-qm1", 1, 0, 1, false, false)
	dump("poor-qm1")
	d.click("inv-p36", 36, 0, 0, false, false)
	d.click("inv-d0", 0, 0, 0, false, false)
	d.click("inv-p37", 37, 0, 0, false, false)
	d.click("inv-p1", 1, 0, 0, false, false)
	dump("inv-combo")
	d.click("inv-b0", 0, 0, 0, false, false)
	d.click("inv-b36", 36, 0, 0, false, false)
	d.click("inv-b1", 1, 0, 0, false, false)
	d.click("inv-b37", 37, 0, 0, false, false)
	dump("inv-back")
	d.close()
	dump("close")
}
