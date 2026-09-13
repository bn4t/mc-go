package blackbox

// Throwaway probe: vanilla 26.2 anvil takes in SURVIVAL (the harness
// defaults to creative, so every earlier anvil run measured creative
// takes). Survival charge shape, poor-take rejection, quick-move output
// charge. Deleted once encoded.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestProbeAnvilSurvTake(t *testing.T) {
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
	setup := []struct{ slot, item, dmg string }{
		{"hotbar.0", "minecraft:iron_pickaxe 1", "0.5"},
		{"hotbar.1", "minecraft:iron_pickaxe 1", "0.5"},
		{"hotbar.2", "minecraft:iron_pickaxe 1", "0.9"},
		{"hotbar.3", "minecraft:iron_pickaxe 1", "0.9"},
		{"hotbar.4", "minecraft:iron_pickaxe 1", "0.5"},
		{"hotbar.5", "minecraft:iron_pickaxe 1", "0.5"},
	}
	for i, s := range setup {
		mk("item replace entity @s "+s.slot+" with "+s.item, "IT"+string(rune('0'+i)))
		mk(`item modify entity @s `+s.slot+` {"function":"set_damage","damage":`+s.dmg+`}`, "DM"+string(rune('0'+i)))
	}
	mk("xp set @s 30 levels", "XPOK")
	// Bare switch plus data read-back: the sentinel would eat the
	// evidence, so the mode is proven here before the menu opens.
	sendCommand(t, pw, "gamemode survival")
	time.Sleep(1500 * time.Millisecond)
	for _, p := range d.sink.take() {
		t.Logf("mode id=%02x %x", p.id, p.data)
	}
	sendCommand(t, pw, "data get entity @s playerGameType")
	time.Sleep(1500 * time.Millisecond)
	for _, p := range d.sink.take() {
		t.Logf("gtype id=%02x %x", p.id, p.data)
	}
	if !digSentinel(t, pw, d.sink, "SYNC", 10*time.Second) {
		t.Fatal("no sync")
	}
	d.sink.take()
	d.reopen(8, -61, 0, 11, "use-open")
	t.Logf("open menu=%d win=%d", judgeUse(d.opened).menu, d.win)
	dump := func(tag string) {
		t.Helper()
		t.Logf("%s carried=%s menu=%v p0=%v", tag, d.snap.carried, d.snap.menu, d.snap.p0)
	}
	dump("opened")
	// Charged take: 125+125, cost 2, level 30.
	d.click("ch-p30", 30, 0, 0, false, false)
	d.click("ch-i0", 0, 0, 0, false, false)
	d.click("ch-p31", 31, 0, 0, false, false)
	d.click("ch-i1", 1, 0, 0, false, false)
	dump("ch-in")
	d.click("ch-take", 2, 0, 0, false, false)
	dump("ch-take")
	d.click("ch-park38", 38, 0, 0, false, false)
	dump("ch-park")
	// Quick-move output charges too: 125+125 in, shift-click it out.
	d.click("qm-p34", 34, 0, 0, false, false)
	d.click("qm-i0", 0, 0, 0, false, false)
	d.click("qm-p35", 35, 0, 0, false, false)
	d.click("qm-i1", 1, 0, 0, false, false)
	dump("qm-in")
	d.click("qm-out", 2, 0, 1, false, false)
	dump("qm-out")
	// Poor take: 25+25 in with 0 levels.
	mk("xp set @s 0 levels", "XP0")
	d.click("poor-p32", 32, 0, 0, false, false)
	d.click("poor-i0", 0, 0, 0, false, false)
	d.click("poor-p33", 33, 0, 0, false, false)
	d.click("poor-i1", 1, 0, 0, false, false)
	dump("poor-in")
	d.click("poor-take", 2, 0, 0, false, false)
	dump("poor-take")
	d.close()
	dump("close")
}
