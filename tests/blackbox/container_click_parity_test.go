package blackbox

// Container-click parity: an identical click script against a chest on
// both servers must leave identical logical slot states after every
// step. The script covers pickup/place, right-click half and single
// placement, merging, quick move, hotbar swap, throw one/all, drag
// splits, double-click collect, and window-0 clicks after close.
// States are compared parsed (carried, menu slots, player slots), not
// byte for byte: state counters are folded, not matched.

import (
	"fmt"
	"os"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// clickSnap is the logical inventory state after one script step.
type clickSnap struct {
	carried string
	menu    map[int32]string
	p0      map[int32]string
}

func stackKey(it server.ItemStack) string {
	if it.Empty() {
		return "-"
	}
	return fmt.Sprintf("%d#%d", it.ID, it.Count)
}

// clickDriver runs clicks on one open chest and folds the resulting
// slot traffic into logical state.
type clickDriver struct {
	t       *testing.T
	name    string
	sink    *useClient
	pw      *protocol.Writer
	win     int32
	state   int32
	p0state int32
	snap    clickSnap
	debug   bool
}

func newClickDriver(t *testing.T, addr, name string) *clickDriver {
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
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
		t.Fatalf("%s: never ready: %v", name, d.sink.err)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	if !digSentinel(t, pw, d.sink, "TPOK", 10*time.Second) {
		t.Fatalf("%s: no tp", name)
	}
	sendCommand(t, pw, "setblock 8 -61 0 minecraft:chest")
	if !digSentinel(t, pw, d.sink, "SBOK", 10*time.Second) {
		t.Fatalf("%s: no setblock", name)
	}
	sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:dirt 64")
	if !digSentinel(t, pw, d.sink, "IT0", 10*time.Second) {
		t.Fatalf("%s: no item0", name)
	}
	sendCommand(t, pw, "item replace entity @s hotbar.1 with minecraft:cobblestone 32")
	if !digSentinel(t, pw, d.sink, "IT1", 10*time.Second) {
		t.Fatalf("%s: no item1", name)
	}
	sendCommand(t, pw, "item replace entity @s inventory.0 with minecraft:oak_planks 16")
	if !digSentinel(t, pw, d.sink, "IT2", 10*time.Second) {
		t.Fatalf("%s: no item2", name)
	}
	// Drop setup traffic: the sentinel polls consume packets as they wait
	// for the chat echo, so folding here is racy per server. Window-0
	// state and slots heal deterministically off the sync-w0 resync
	// after close instead.
	d.sink.take()
	sendUseItemOn(t, pw, 8, -61, 0, 1, 11)
	var opened []usePkt
	settleUse(t, d.sink, func(pkts []usePkt) { opened = append(opened, pkts...) }, "use-chest")
	v := judgeUse(opened)
	if !v.open || v.menu != 2 {
		t.Fatalf("%s: chest did not open (open=%v menu=%d)", name, v.open, v.menu)
	}
	d.foldPkts(opened)
	return d
}

// fold merges newly arrived container traffic into the snapshot.
func (d *clickDriver) fold() {
	d.t.Helper()
	d.foldPkts(d.sink.take())
}

// foldPkts merges already-captured packets, so setup can judge the
// open and still fold its content. It reports whether the batch
// changed any slots and whether it carried a cursor update: vanilla
// omits SetCursorItem when the cursor empties, so slot traffic
// without one means an empty cursor.
func (d *clickDriver) foldPkts(pkts []usePkt) (slotsTouched, sawCarried bool) {
	for _, p := range pkts {
		if d.debug {
			d.t.Logf("%s recv id=%#x %x", d.name, p.id, p.data)
		}
		r := protocol.NewPacketReader(p.data)
		switch p.id {
		case server.IDPlayOpenScreen:
			if id, err := r.VarInt(); err == nil {
				d.win = id
				d.state = 0
			}
		case server.IDPlayContainerContent:
			wid, err1 := r.VarInt()
			st, err2 := r.VarInt()
			n, err3 := r.VarInt()
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			m := d.snap.p0
			if wid == d.win {
				m = d.snap.menu
				d.state = st
				slotsTouched = true
			} else if wid == 0 {
				d.p0state = st
				slotsTouched = true
			} else {
				continue
			}
			for i := int32(0); i < n; i++ {
				it, err := readSlotBB(r)
				if err != nil {
					break
				}
				m[i] = stackKey(it)
			}
			// The content ends with the cursor stack.
			if wid == d.win {
				if it, err := readSlotBB(r); err == nil {
					d.snap.carried = stackKey(it)
					sawCarried = true
				}
			}
		case server.IDPlayContainerSlot:
			wid, err1 := r.VarInt()
			st, err2 := r.VarInt()
			slot16, err3 := r.Short()
			slot := int32(slot16)
			it, err4 := readSlotBB(r)
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
				continue
			}
			if wid == d.win {
				d.state = st
				d.snap.menu[slot] = stackKey(it)
				slotsTouched = true
			} else if wid == 0 {
				d.p0state = st
				d.snap.p0[slot] = stackKey(it)
				slotsTouched = true
			}
		case server.IDPlaySetCarriedItem:
			if it, err := readSlotBB(r); err == nil {
				d.snap.carried = stackKey(it)
				sawCarried = true
			}
		}
	}
	return slotsTouched, sawCarried
}

// click sends one Container Click on the open window, or on window 0
// when w0 is set, then folds the answer. With badState the click goes
// out with a stale state id: vanilla ignores it and resyncs the whole
// window, and the fold heals the tracked state off that resync.
func (d *clickDriver) click(tag string, slot int16, button byte, mode int32, w0 bool, badState bool) {
	d.t.Helper()
	win := d.win
	st := d.state
	if w0 {
		win = 0
		st = d.p0state
	}
	if badState {
		st += 999
	}
	var b protocol.Buffer
	b.VarInt(win)
	b.VarInt(st)
	b.Short(slot)
	b.Byte(button)
	b.VarInt(mode)
	b.VarInt(0) // changed slots
	b.VarInt(0) // carried count 0
	if d.debug {
		d.t.Logf("%s send tag=%s win=%d state=%d slot=%d btn=%d mode=%d", d.name, tag, win, st, slot, button, mode)
	}
	if err := d.pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
		d.t.Fatal(err)
	}
	var touched, carried bool
	settleUse(d.t, d.sink, func(pkts []usePkt) {
		mt, sc := d.foldPkts(pkts)
		touched = touched || mt
		carried = carried || sc
	}, tag)
	if touched && !carried {
		d.snap.carried = "-"
	}
}

func (d *clickDriver) close() {
	d.t.Helper()
	var b protocol.Buffer
	b.VarInt(d.win)
	_ = d.pw.WritePacket(server.IDPlayCloseContainerSB, b.Bytes())
	settleUse(d.t, d.sink, func(pkts []usePkt) { d.foldPkts(pkts) }, "close")
	d.win = -1
}

func diffSnaps(a, b clickSnap) []string {
	var out []string
	if a.carried != b.carried {
		out = append(out, fmt.Sprintf("carried: vanilla=%s mc-go=%s", a.carried, b.carried))
	}
	// Slots a server never re-sent read back missing; that is the
	// same logical state as an explicit empty.
	norm := func(m map[int32]string, i int32) string {
		if s, ok := m[i]; ok {
			return s
		}
		return "-"
	}
	for i := int32(0); i < 63; i++ {
		if norm(a.menu, i) != norm(b.menu, i) {
			out = append(out, fmt.Sprintf("menu[%d]: vanilla=%s mc-go=%s", i, norm(a.menu, i), norm(b.menu, i)))
		}
	}
	for i := int32(0); i < 46; i++ {
		if norm(a.p0, i) != norm(b.p0, i) {
			out = append(out, fmt.Sprintf("p0[%d]: vanilla=%s mc-go=%s", i, norm(a.p0, i), norm(b.p0, i)))
		}
	}
	return out
}

// TestContainerClickParity replays the click script on both servers,
// comparing logical slot state after every step.
func TestContainerClickParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ClickV", "ClickM"),
	}, nil)
	defer tg.cleanup()

	steps := []struct {
		tag         string
		slot        int16
		button      byte
		mode        int32
		w0          bool
		badState    bool
		closeFirst  bool
		expectSetup bool
	}{
		{tag: "setup", expectSetup: true},
		{tag: "pickup-dirt", slot: 54, button: 0, mode: 0},
		{tag: "place-chest0", slot: 0, button: 0, mode: 0},
		{tag: "half-cobble", slot: 55, button: 1, mode: 0},
		{tag: "place-one", slot: 1, button: 1, mode: 0},
		{tag: "merge-rest", slot: 1, button: 0, mode: 0},
		{tag: "pickup-back", slot: 1, button: 0, mode: 0},
		{tag: "place-back", slot: 1, button: 0, mode: 0},
		{tag: "shift-chest0", slot: 0, button: 0, mode: 1},
		{tag: "swap-num", slot: 54, button: 1, mode: 2},
		{tag: "throw-one", slot: 54, button: 0, mode: 4},
		{tag: "throw-rest", slot: 54, button: 1, mode: 4},
		{tag: "pickup-cobble", slot: 55, button: 0, mode: 0},
		{tag: "drag-start", slot: 2, button: 0, mode: 5},
		{tag: "drag-add3", slot: 3, button: 1, mode: 5},
		{tag: "drag-add4", slot: 4, button: 1, mode: 5},
		{tag: "drag-end", slot: 2, button: 2, mode: 5},
		{tag: "dblclick", slot: 2, button: 0, mode: 6},
		// Stale window state: ignored, whole menu resynced.
		{tag: "stale-menu", slot: 5, button: 0, mode: 0, badState: true},
		{tag: "close", closeFirst: true},
		// Heal window-0 state: setup traffic is dropped above, so this
		// stale click forces a full inventory resync on both servers.
		{tag: "sync-w0", slot: 10, button: 0, mode: 0, w0: true, badState: true},
		// Hotbar.8 still holds the shift-moved dirt64 on both servers.
		{tag: "w0-pickup", slot: 44, button: 0, mode: 0, w0: true},
		{tag: "w0-place", slot: 10, button: 0, mode: 0, w0: true},
		{tag: "w0-shift", slot: 10, button: 0, mode: 1, w0: true},
		// Stale window-0 state: ignored, whole inventory resynced.
		{tag: "stale-w0", slot: 10, button: 0, mode: 0, w0: true, badState: true},
	}

	run := func(addr, name string) []clickSnap {
		d := newClickDriver(t, addr, name)
		snaps := make([]clickSnap, 0, len(steps))
		for _, s := range steps {
			if s.closeFirst {
				d.close()
			} else if !s.expectSetup {
				d.click(s.tag, s.slot, s.button, s.mode, s.w0, s.badState)
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
		return snaps
	}

	want := run(tg.vanilla, "ClickV")
	got := run(tg.mcgo, "ClickM")
	for i, s := range steps {
		if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
			for _, df := range diffs {
				t.Errorf("step %s: %s", s.tag, df)
			}
		}
	}
}
