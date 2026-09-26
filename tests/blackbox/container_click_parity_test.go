package blackbox

// Container-click parity: an identical click script against a chest on
// both servers must leave identical logical slot states after every
// step. The script covers pickup/place, right-click half and single
// placement, merging, quick move, hotbar swap, throw one/all, drag
// splits, double-click collect, creative clone, and window-0 clicks
// after close. Closing with a cursor returns it to the inventory
// (order and full-inventory probes included).
// States are compared parsed (carried, menu slots, player slots), not
// byte for byte: state counters are folded, not matched.

import (
	"bytes"
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
	key := fmt.Sprintf("%d#%d", it.ID, it.Count)
	// Damage, repair cost, and custom name discriminate anvil inputs
	// from their outputs: a repaired pick reads back bare, its parents
	// worn; a renamed one carries its name.
	if it.Damage != 0 {
		key += fmt.Sprintf("[d%d]", it.Damage)
	}
	if it.Name != "" {
		key += fmt.Sprintf("[n%q]", it.Name)
	}
	if it.RepairCost != 0 {
		key += fmt.Sprintf("[rc%d]", it.RepairCost)
	}
	if it.Potion != "" {
		key += fmt.Sprintf("[p%s]", it.Potion)
	}
	if it.StoredEnch != "" {
		key += fmt.Sprintf("[se%s]", it.StoredEnch)
	}
	if it.Ench != "" {
		key += fmt.Sprintf("[e%s]", it.Ench)
	}
	if it.BookWritable != "" {
		key += fmt.Sprintf("[bw%s]", it.BookWritable)
	}
	if it.BookWritten != "" {
		key += fmt.Sprintf("[bwr%s]", it.BookWritten)
	}
	if it.Trim != "" {
		key += fmt.Sprintf("[t%s]", it.Trim)
	}
	if it.BannerPatterns != "" {
		key += fmt.Sprintf("[bp%s]", it.BannerPatterns)
	}
	return key
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
	opened  []usePkt
	debug   bool
	// cost is the last Container Property 0 seen on the open window
	// (the anvil's level cost), with costSeen false until the first
	// one arrives. xpLevel is the last Set Experience level, likewise
	// gated. sounds counts level-event 1030s (anvil use); sawClose
	// records a clientbound Container Close. invSets counts Set Player
	// Inventory packets (anvil close returns).
	cost     int32
	costSeen bool
	// props keeps every Container Property value by index (the
	// enchantment menu's ten offer slots), so menus other than the
	// anvil's single cost field can be read back.
	props   map[int16]int16
	xpLevel int32
	xpSeen  bool
	// announce counts advancement-announce system chats seen by the
	// fold (chat.type.advancement.* translate keys).
	announce int
	// sndPkts counts clientbound sound packets (id 0x75 on 26.2 — the
	// enchanting table's use sound).
	sndPkts int
	// brewDone counts level-event 1035s (brewing stand finished),
	// pageTurns level-event 1043s (lectern page changed).
	brewDone  int
	pageTurns int
	sounds    int
	sawClose  bool
	invSets   int
	// blkUps counts Block Update packets per position: the anvil's
	// 12%-per-take degrade roll is a block change at the menu's pos.
	blkUps map[[3]int32]int
	// soundIDs decodes every named-sound packet's registry id, so a
	// test can tell a book_put from a block break.
	soundIDs map[int32]int
	// grindSounds counts the 1042 level event (grindstone use);
	// orbSpawns/orbXP fold XP-orb spawns and their value metadata.
	grindSounds int
	orbSpawns   int
	orbXP       int
	// death holds the killer's entity id when a death chat arrives:
	// a dead survival player loses its menu, which makes every later
	// step silently diverge.
	death string
	// adv, when non-nil, receives every UpdateAdvancements packet the
	// fold sees — click flows that grant advancements (recipe_crafted)
	// can then assert progress without a rejoin.
	adv *advFlush
}

func newClickDriver(t *testing.T, addr, name string) *clickDriver {
	t.Helper()
	return newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{
		{"hotbar.0", "minecraft:dirt 64"},
		{"hotbar.1", "minecraft:cobblestone 32"},
		{"inventory.0", "minecraft:oak_planks 16"},
	}, nil)
}

// newClickDriverOn runs the click setup against any menu block: it
// places the block, hands the player the gives, pre-fills the ender
// storage when prefill is set (for the ender chest's shared storage),
// and opens the menu with a use. wantMenu is the menu-registry id the
// open screen must carry (2 generic_9x3, 12 crafting, 16 hopper).
func newClickDriverOn(t *testing.T, addr, name, block string, wantMenu int32, gives [][2]string, prefill [][2]string) *clickDriver {
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
	sendCommand(t, pw, "setblock 8 -61 0 "+block)
	if !digSentinel(t, pw, d.sink, "SBOK", 10*time.Second) {
		t.Fatalf("%s: no setblock", name)
	}
	for i, g := range gives {
		sendCommand(t, pw, "item replace entity @s "+g[0]+" with "+g[1])
		if !digSentinel(t, pw, d.sink, fmt.Sprintf("IT%d", i), 10*time.Second) {
			t.Fatalf("%s: no item%d", name, i)
		}
	}
	for i, g := range prefill {
		sendCommand(t, pw, "item replace entity @s "+g[0]+" with "+g[1])
		if !digSentinel(t, pw, d.sink, fmt.Sprintf("PF%d", i), 10*time.Second) {
			t.Fatalf("%s: no prefill%d", name, i)
		}
	}
	// Drop setup traffic: the sentinel polls consume packets as they wait
	// for the chat echo, so folding here is racy per server. Window-0
	// state and slots heal deterministically off the sync-w0 resync
	// after close instead.
	d.sink.take()
	d.reopen(8, -61, 0, 11, "use-open")
	v := judgeUse(d.opened)
	if !v.open || v.menu != wantMenu {
		t.Fatalf("%s: %s did not open (open=%v menu=%d)", name, block, v.open, v.menu)
	}
	return d
}

// reopen uses the menu block again (first open or after a close) and
// folds the answer, keeping the open packets for title checks.
func (d *clickDriver) reopen(x, y, z, seq int32, tag string) {
	d.t.Helper()
	sendUseItemOn(d.t, d.pw, x, y, z, 1, seq)
	var opened []usePkt
	settleUse(d.t, d.sink, func(pkts []usePkt) { opened = append(opened, pkts...) }, tag)
	d.opened = opened
	d.foldPkts(opened)
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
		// Death messages are translated chat payloads naming the
		// killer (death.attack.mob, entity.minecraft.slime). Flag any
		// packet carrying one so a stale menu surfaces as a death,
		// not a string of slot diffs.
		if bytes.Contains(p.data, []byte("death.")) {
			i := bytes.Index(p.data, []byte("entity.minecraft."))
			if i >= 0 {
				d.death = string(p.data[i:])
			}
		}
		if d.adv != nil && p.id == server.IDPlayUpdateAdvance {
			decodeUpdateAdv(d.t, p.data, d.adv)
		}
		if bytes.Contains(p.data, []byte("chat.type.advancement")) {
			d.announce++
		}
		if p.id == 0x75 {
			d.sndPkts++
			if id, err := protocol.NewPacketReader(p.data).VarInt(); err == nil {
				if d.soundIDs == nil {
					d.soundIDs = map[int32]int{}
				}
				d.soundIDs[id]++
			}
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
		case server.IDPlayContainerProperty:
			wid, err1 := r.VarInt()
			prop16, err2 := r.Short()
			val16, err3 := r.Short()
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			if wid == d.win && prop16 == 0 {
				d.cost = int32(val16)
				d.costSeen = true
			}
			if wid == d.win {
				if d.props == nil {
					d.props = map[int16]int16{}
				}
				d.props[prop16] = val16
			}
		case server.IDPlaySetExperience:
			bar, err1 := r.Float()
			lv, err2 := r.VarInt()
			if err1 != nil || err2 != nil {
				continue
			}
			_ = bar
			d.xpLevel = lv
			d.xpSeen = true
		case server.IDPlayLevelEvent:
			ev, err := r.Int()
			if err != nil {
				continue
			}
			if ev == 1030 {
				d.sounds++
			}
			if ev == 1035 {
				d.brewDone++
			}
			if ev == 1043 {
				d.pageTurns++
			}
			if ev == 1042 {
				d.grindSounds++
			}
		case server.IDPlayContainerClose:
			d.sawClose = true
		case server.IDPlayBlockUpdateCB:
			x, y, z, err := r.Position()
			if err != nil {
				continue
			}
			if d.blkUps == nil {
				d.blkUps = map[[3]int32]int{}
			}
			d.blkUps[[3]int32{x, y, z}]++
		case server.IDPlaySetPlayerInventory:
			// One moved stack outside a click, by player-inventory
			// container index (0-8 hotbar, 9-35 main): fold it into
			// the window-0 snapshot under its window slot.
			slot, err1 := r.VarInt()
			it, err2 := readSlotBB(r)
			if err1 != nil || err2 != nil {
				continue
			}
			d.invSets++
			if d.debug {
				d.t.Logf("%s SPI slot=%d it=%s", d.name, slot, stackKey(it))
			}
			w0 := slot
			if slot >= 0 && slot < 9 {
				w0 = slot + 36
			}
			d.snap.p0[w0] = stackKey(it)
			slotsTouched = true
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.data)
			if _, err := r.VarInt(); err != nil {
				continue
			}
			if _, err := r.UUID(); err != nil {
				continue
			}
			if et, err := r.VarInt(); err == nil && et == 49 { // experience_orb in entity_types
				d.orbSpawns++
			}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.data)
			if _, err := r.VarInt(); err != nil {
				continue
			}
			// The orb's value rides as a lone index-8 VarInt entry;
			// any other shape is not an orb payload.
			idx, err1 := r.Byte()
			typ, err2 := r.Byte()
			if err1 != nil || err2 != nil || idx != 8 || typ != 1 {
				continue
			}
			if v, err := r.VarInt(); err == nil {
				d.orbXP += int(v)
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

// rename sends a Rename Item packet (the anvil's text field) and folds
// the answer: a recomputed output slot and level cost, or nothing.
func (d *clickDriver) rename(tag, name string) {
	d.t.Helper()
	var b protocol.Buffer
	b.String(name)
	if err := d.pw.WritePacket(server.IDPlayRenameItemSB, b.Bytes()); err != nil {
		d.t.Fatal(err)
	}
	settleUse(d.t, d.sink, func(pkts []usePkt) { d.foldPkts(pkts) }, tag)
}

// button sends one Container Button Click (the enchantment table's
// option buttons, the lectern's take-book, the beacon's confirm) and
// folds the answer.
func (d *clickDriver) button(tag string, btn int32) {
	d.t.Helper()
	var b protocol.Buffer
	b.VarInt(d.win)
	b.VarInt(btn)
	if err := d.pw.WritePacket(server.IDPlayContainerButtonClickSB, b.Bytes()); err != nil {
		d.t.Fatal(err)
	}
	settleUse(d.t, d.sink, func(pkts []usePkt) { d.foldPkts(pkts) }, tag)
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

// fullFillInv fills every player-inventory slot the close-return can
// reach (hotbar, main, offhand) so closing with a full cursor has
// nowhere to put it.
func fullFillInv() []string {
	ops := []string{}
	for i := 0; i < 9; i++ {
		ops = append(ops, fmt.Sprintf("item replace entity @s hotbar.%d with minecraft:stone 64", i))
	}
	for i := 0; i < 27; i++ {
		ops = append(ops, fmt.Sprintf("item replace entity @s inventory.%d with minecraft:stone 64", i))
	}
	return append(ops, "item replace entity @s weapon.offhand with minecraft:stone 64")
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
		reopen      bool
		ops         []string
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
		// Creative clone: a middle-click copies the full stack onto
		// the cursor and leaves the slot alone; other buttons and
		// empty slots are ignored.
		{tag: "clone-menu1", slot: 1, button: 2, mode: 3},
		{tag: "clone-menu1-left", slot: 1, button: 0, mode: 3},
		{tag: "clone-empty", slot: 5, button: 2, mode: 3},
		// Put the clone back: it merges into its source, leaving 16 on
		// the cursor; closing returns those to the inventory.
		{tag: "merge-back", slot: 1, button: 0, mode: 0},
		{tag: "close", closeFirst: true},
		// Heal window-0 state: setup traffic is dropped above, so this
		// stale click forces a full inventory resync on both servers.
		{tag: "sync-w0", slot: 10, button: 0, mode: 0, w0: true, badState: true},
		// Creative clone on window 0, then ignored variants.
		{tag: "w0-clone", slot: 37, button: 2, mode: 3, w0: true},
		{tag: "w0-clone-left", slot: 37, button: 0, mode: 3, w0: true},
		// The clone sits on the cursor, so this swaps with hotbar.8's
		// shift-moved dirt64 on both servers.
		{tag: "w0-pickup", slot: 44, button: 0, mode: 0, w0: true},
		{tag: "w0-place", slot: 10, button: 0, mode: 0, w0: true},
		{tag: "w0-shift", slot: 10, button: 0, mode: 1, w0: true},
		// Stale window-0 state: ignored, whole inventory resynced.
		{tag: "stale-w0", slot: 10, button: 0, mode: 0, w0: true, badState: true},
		// Close with a full cursor returns it to the inventory. With
		// hotbar.0 occupied and main slot 1 cleared, hotbar-first and
		// main-first orders pick different slots.
		{tag: "ord-fill", ops: []string{
			"item replace entity @s hotbar.0 with minecraft:stone 64",
			"item replace entity @s hotbar.1 with minecraft:air",
			"item replace entity @s hotbar.2 with minecraft:air",
			"item replace entity @s hotbar.3 with minecraft:air",
			"item replace entity @s hotbar.4 with minecraft:air",
			"item replace entity @s hotbar.5 with minecraft:air",
			"item replace entity @s hotbar.6 with minecraft:air",
			"item replace entity @s hotbar.7 with minecraft:air",
			"item replace entity @s hotbar.8 with minecraft:air",
			"item replace entity @s inventory.1 with minecraft:air",
		}},
		{tag: "ord-reopen", reopen: true},
		{tag: "ord-heal", slot: 5, button: 0, mode: 0, badState: true},
		{tag: "ord-clone", slot: 1, button: 2, mode: 3},
		{tag: "ord-close", closeFirst: true},
		// A full inventory leaves the cursor nowhere to go.
		{tag: "full-fill", ops: fullFillInv()},
		{tag: "full-reopen", reopen: true},
		{tag: "full-heal", slot: 5, button: 0, mode: 0, badState: true},
		{tag: "full-clone", slot: 1, button: 2, mode: 3},
		{tag: "full-close", closeFirst: true},
	}

	run := func(addr, name string) []clickSnap {
		d := newClickDriver(t, addr, name)
		seq := int32(12)
		opN := 0
		snaps := make([]clickSnap, 0, len(steps))
		for _, s := range steps {
			switch {
			case s.expectSetup:
			case s.closeFirst:
				d.close()
			case s.reopen:
				d.reopen(8, -61, 0, seq, s.tag)
				seq++
			case len(s.ops) > 0:
				for _, cmd := range s.ops {
					sendCommand(t, d.pw, cmd)
					opN++
					if !digSentinel(t, d.pw, d.sink, fmt.Sprintf("CLKOP%d", opN), 10*time.Second) {
						t.Fatalf("%s: no op ack", name)
					}
				}
				d.fold()
			default:
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
