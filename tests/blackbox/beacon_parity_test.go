// TestBeaconEffectsParity drives the beacon menu end to end on both
// servers: a four-layer pyramid raises the block entity's Levels, the
// payment slot takes beacon_payment_items, Set Beacon commits a
// validateEffects-passing pair onto the block entity and consumes one
// item, and the 80-tick rescan re-applies the powers as ambient
// Update Mob Effect packets — the secondary boosting the primary to
// amplifier 1 when it matches. Selections that fail validation and a
// commit with an empty payment are ignored outright.
package blackbox

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// newBeaconDriver joins, builds a four-layer iron pyramid under a
// beacon at (8,-60,0), hands the player three iron ingots, and opens
// the beacon menu. The flat world's bedrock is at -64, so -60 is the
// lowest beacon that still fits all four layers.
func newBeaconDriver(t *testing.T, addr, name string) *clickDriver {
	t.Helper()
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
	t.Cleanup(func() {
		if name != "BcnM" && t.Failed() {
			vanillaLogTail(t)
		}
	})
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
		t.Fatalf("%s: never ready: %v", name, d.sink.err)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	if !digSentinel(t, pw, d.sink, "TPOK", 10*time.Second) {
		vanillaLogTail(t)
		t.Fatalf("%s: no tp (sink err %v)", name, d.sink.err)
	}
	// (2i+1)-square iron layers at y=-61..-64: the smallest full
	// pyramid — supersets count, only the inner square is checked.
	// Chat is rate-limited on vanilla ("Kicked for spamming"), so the
	// setup commands ride one burst ahead of a single sentinel.
	for _, cmd := range []string{
		"fill -1 -64 -9 17 -64 9 iron_block",
		"fill 1 -63 -7 15 -63 7 iron_block",
		"fill 3 -62 -5 13 -62 5 iron_block",
		"fill 5 -61 -3 11 -61 3 iron_block",
		"setblock 8 -60 0 beacon",
		"item replace entity @s inventory.0 with minecraft:iron_ingot 3",
	} {
		sendCommand(t, pw, cmd)
	}
	if !digSentinel(t, pw, d.sink, "ZBN-setup", 15*time.Second) {
		t.Fatalf("%s: setup never answered (sink err %v)", name, d.sink.err)
	}
	d.sink.take()
	sendUseItemOn(t, d.pw, 8, -60, 0, 1, 11)
	// Poll for the OpenScreen: mc-go answers the use fast but under
	// load the packet can land after settleUse's 800ms quiet window.
	deadline := time.Now().Add(10 * time.Second)
	var opened []usePkt
	for time.Now().Before(deadline) {
		pkts := d.sink.take()
		d.foldPkts(pkts)
		opened = append(opened, pkts...)
		if judgeUse(opened).open {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	d.opened = opened
	v := judgeUse(d.opened)
	if !v.open || v.menu != 9 {
		t.Fatalf("%s: beacon did not open (open=%v menu=%d tooHigh=%v chats=%d, sink err %v)", name, v.open, v.menu, v.tooHigh, v.chats, d.sink.err)
	}
	return d
}

// sendSetBeacon writes the Set Beacon Effect packet: two prefixed
// optionals carrying mob-effect registry ids.
func sendSetBeacon(t *testing.T, pw *protocol.Writer, primary, secondary int32) {
	t.Helper()
	var b protocol.Buffer
	b.Bool(primary >= 0)
	if primary >= 0 {
		b.VarInt(primary)
	}
	b.Bool(secondary >= 0)
	if secondary >= 0 {
		b.VarInt(secondary)
	}
	if err := pw.WritePacket(server.IDPlaySetBeaconSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestBeaconEffectsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BcnV", "BcnM"),
	}, nil)
	defer targets.cleanup()
	speedID, _ := server.MobEffectID("speed")
	hasteID, _ := server.MobEffectID("haste")
	strengthID, _ := server.MobEffectID("strength")
	regenID, _ := server.MobEffectID("regeneration")

	// probe polls an execute if/unless pair once and reports hit/miss.
	probe := func(d *clickDriver, spec string, i int) string {
		sendCommand(t, d.pw, fmt.Sprintf("execute if %s run say ZBN-hit-%d", spec, i))
		sendCommand(t, d.pw, fmt.Sprintf("execute unless %s run say ZBN-miss-%d", spec, i))
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			pkts := d.sink.take()
			d.foldPkts(pkts)
			for _, p := range pkts {
				if strings.Contains(string(p.data), fmt.Sprintf("ZBN-hit-%d", i)) {
					return "hit"
				}
				if strings.Contains(string(p.data), fmt.Sprintf("ZBN-miss-%d", i)) {
					return "miss"
				}
			}
			time.Sleep(80 * time.Millisecond)
		}
		return "none"
	}
	// waitLevels polls until the rescan has raised the beacon to 4;
	// the pyramid's first tick lands on the 80-tick cadence.
	waitLevels := func(d *clickDriver) string {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if probe(d, "data block 8 -60 0 {Levels:4}", 1) == "hit" {
				return "lv4"
			}
			time.Sleep(700 * time.Millisecond)
		}
		return "lv-miss"
	}
	// waitEffect drains the sink until an Update Mob Effect for
	// want lands (the rescan cadence is 80 ticks) or the window
	// lapses, folding menu traffic as it goes.
	waitEffect := func(d *clickDriver, want int32) string {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			pkts := d.sink.take()
			d.foldPkts(pkts)
			for _, p := range pkts {
				if p.id != server.IDPlayUpdateMobEffect {
					continue
				}
				rd := protocol.NewPacketReader(p.data)
				rd.VarInt() // entity id
				eff, err1 := rd.VarInt()
				amp, err2 := rd.Byte()
				dur, err3 := rd.VarInt()
				if err1 == nil && err2 == nil && err3 == nil && eff == want {
					return fmt.Sprintf("amp%d dur%d", amp, dur)
				}
			}
			time.Sleep(80 * time.Millisecond)
		}
		return "none"
	}
	// waitDisconnect polls for the play Disconnect packet vanilla
	// sends when updateEffects rejects a commit, returning the
	// component JSON the packet carried ("closed" when the connection
	// ends without one, "alive" when nothing came).
	waitDisconnect := func(d *clickDriver) string {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			for _, p := range d.sink.take() {
				if p.id != server.IDPlayDisconnect {
					continue
				}
				rd := protocol.NewPacketReader(p.data)
				s, _ := rd.String()
				return "dc:" + s
			}
			if d.sink.err != nil {
				return "closed"
			}
			time.Sleep(80 * time.Millisecond)
		}
		return "alive"
	}
	// waitSlot polls the folded snapshot until a menu slot shows the
	// wanted stack — the slot sync a commit triggers lands a tick
	// after the packet itself.
	waitSlot := func(d *clickDriver, slot int32, want string) string {
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			d.fold()
			if got := d.snap.menu[slot]; got == want {
				return want
			}
			time.Sleep(80 * time.Millisecond)
		}
		return d.snap.menu[slot]
	}
	// waitProps polls the menu's container properties until prop1/prop2
	// show the wanted pair.
	waitProps := func(d *clickDriver, p1, p2 int16) string {
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			d.fold()
			if d.props[1] == p1 && d.props[2] == p2 {
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
		return fmt.Sprintf("%d,%d", d.props[1], d.props[2])
	}
	// pay moves menu slot 1 (inventory.0's ingots) into payment 0;
	// the slot's stack limit of 1 keeps the rest on the cursor.
	pay := func(d *clickDriver) {
		d.click("pick", 1, 0, 0, false, false)
		d.click("pay", 0, 0, 0, false, false)
	}

	run := func(addr, name string) []string {
		var steps []string

		// Happy path: level-4 pyramid, pay, commit speed+speed for the
		// amp-1 boost, then a distinct secondary commit.
		d := newBeaconDriver(t, addr, name)
		steps = append(steps, waitLevels(d))
		d.fold()
		steps = append(steps, fmt.Sprintf("props:%d,%d,%d", d.props[0], d.props[1], d.props[2]))
		pay(d)
		steps = append(steps, "paid:"+d.snap.menu[0])
		sendSetBeacon(t, d.pw, speedID, speedID)
		steps = append(steps, "afterCommit:"+waitSlot(d, 0, "-"))
		steps = append(steps, "effprops:"+waitProps(d, int16(speedID+1), int16(speedID+1)))
		steps = append(steps, "speed:"+waitEffect(d, speedID))
		steps = append(steps, probe(d, `data block 8 -60 0 {primary_effect:"minecraft:speed"}`, 3))
		steps = append(steps, probe(d, `data block 8 -60 0 {secondary_effect:"minecraft:speed"}`, 4))
		// The cursor still holds the rest of the paid stack: pay again
		// and commit a distinct secondary — regen joins speed at amp 0.
		d.click("pay2", 0, 0, 0, false, false)
		steps = append(steps, "repay:"+d.snap.menu[0])
		sendSetBeacon(t, d.pw, speedID, regenID)
		steps = append(steps, "commit2:"+waitSlot(d, 0, "-"))
		steps = append(steps, "props2:"+waitProps(d, int16(speedID+1), int16(regenID+1)))
		steps = append(steps, "regen:"+waitEffect(d, regenID))

		// A pair failing validateEffects disconnects the client with
		// multiplayer.disconnect.generic — fresh menu for it.
		d2 := newBeaconDriver(t, addr, name)
		steps = append(steps, waitLevels(d2))
		pay(d2)
		sendSetBeacon(t, d2.pw, hasteID, strengthID)
		steps = append(steps, waitDisconnect(d2))

		// An empty payment slot fails updateEffects the same way.
		d3 := newBeaconDriver(t, addr, name)
		steps = append(steps, waitLevels(d3))
		sendSetBeacon(t, d3.pw, hasteID, hasteID)
		steps = append(steps, waitDisconnect(d3))

		// With no beacon menu open the packet is ignored outright.
		d4 := newBeaconDriver(t, addr, name)
		steps = append(steps, waitLevels(d4))
		d4.close()
		sendSetBeacon(t, d4.pw, speedID, speedID)
		steps = append(steps, waitDisconnect(d4))
		return steps
	}

	van := run(targets.vanilla, "BcnV")
	mc := run(targets.mcgo, "BcnM")
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	if len(van) != len(mc) {
		t.Fatalf("step count diverged: vanilla %v vs mc-go %v", van, mc)
	}
	for i := range van {
		if van[i] != mc[i] {
			t.Fatalf("step %d diverged: vanilla %q vs mc-go %q (vanilla %v | mc-go %v)", i, van[i], mc[i], van, mc)
		}
	}
}
