package blackbox

// Throwaway probe: capture vanilla 26.2's anvil menu surface (open
// geometry and properties, click rules, take costs, close). Deleted
// once encoded. Run with MCGO_CLICKDEBUG=1 for the raw packet hex.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestProbeAnvil(t *testing.T) {
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
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	if !digSentinel(t, pw, d.sink, "TPOK", 10*time.Second) {
		t.Fatal("no tp")
	}
	sendCommand(t, pw, "setblock 8 -61 0 minecraft:anvil")
	if !digSentinel(t, pw, d.sink, "SBOK", 10*time.Second) {
		t.Fatal("no setblock")
	}
	for i, g := range [][2]string{
		{"hotbar.0", "minecraft:iron_pickaxe 1"},
		{"hotbar.1", "minecraft:iron_pickaxe 1"},
		{"hotbar.2", "minecraft:dirt 64"},
	} {
		sendCommand(t, pw, "item replace entity @s "+g[0]+" with "+g[1])
		if !digSentinel(t, pw, d.sink, "IT"+string(rune('0'+i)), 10*time.Second) {
			t.Fatalf("no item%d", i)
		}
	}
	// Damage both pickaxes so the anvil has a repair to offer.
	for i, slot := range []string{"hotbar.0", "hotbar.1"} {
		sendCommand(t, pw, `item modify entity @s `+slot+` {"function":"set_damage","damage":0.5}`)
		if !digSentinel(t, pw, d.sink, "DM"+string(rune('0'+i)), 10*time.Second) {
			t.Fatalf("no dmg%d", i)
		}
		for _, p := range d.sink.take() {
			t.Logf("dmg%d id=%02x %x", i, p.id, p.data)
		}
	}
	sendCommand(t, pw, "data get entity @s Inventory")
	time.Sleep(time.Second)
	for _, p := range d.sink.take() {
		t.Logf("inv id=%02x %x", p.id, p.data)
	}
	d.sink.take()
	sendCommand(t, pw, "gamemode creative")
	time.Sleep(time.Second)
	d.sink.take()

	d.reopen(8, -61, 0, 11, "use-open")
	t.Logf("open menu=%d win=%d", judgeUse(d.opened).menu, d.win)
	dump := func(tag string) {
		t.Helper()
		t.Logf("%s carried=%s menu=%v p0=%v", tag, d.snap.carried, d.snap.menu, d.snap.p0)
	}
	dump("opened")
	d.click("pickup-pick", 30, 0, 0, false, false)
	dump("pickup-pick")
	d.click("place-in0", 0, 0, 0, false, false)
	dump("place-in0")
	d.click("pickup-pick2", 31, 0, 0, false, false)
	dump("pickup-pick2")
	d.click("place-in1", 1, 0, 0, false, false)
	dump("place-in1")
	d.click("take-out", 2, 0, 0, false, false)
	dump("take-out")
	d.click("place-dirt0", 32, 0, 0, false, false)
	dump("pickup-dirt")
	d.click("dirt-in0", 0, 0, 0, false, false)
	dump("dirt-in0")
	d.close()
	dump("close")
}
