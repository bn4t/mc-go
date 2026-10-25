package server

import (
	"testing"

	"mc-go/internal/protocol"
)

func TestSlotCodecRoundTrip(t *testing.T) {
	cases := []ItemStack{
		{},
		{ID: 1, Count: 1},
		{ID: 1, Count: 64},
		{ID: 900, Count: 16},
	}
	for _, want := range cases {
		var b protocol.Buffer
		writeSlot(&b, want)
		got, err := readSlot(protocol.NewPacketReader(b.Bytes()))
		if err != nil {
			t.Fatalf("read %+v: %v", want, err)
		}
		if got != want {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

func clickPacket(t *testing.T, sess *Session, slot int16, button byte, mode int32) protocol.Packet {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(0)                    // window
	b.VarInt(sess.invState.Load()) // state, like a synced client echoes
	b.Short(slot)
	b.Byte(button)
	b.VarInt(mode)
	b.VarInt(0) // changed slots
	b.VarInt(0) // carried item: empty
	return protocol.Packet{ID: IDPlayClickContainerSB, Data: b.Bytes()}
}

func TestClickPickupAndMerge(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Clicker", 8.5, -60, 8.5)
	sess.setStack(slotHotbarStart, ItemStack{ID: 55, Count: 10})
	sess.setStack(slotMainStart, ItemStack{ID: 55, Count: 60})

	// Pick up the hotbar stack into the cursor.
	s.handleClickContainer(sess, clickPacket(t, sess, slotHotbarStart, 0, 0))
	if got := sess.carried; got != (ItemStack{ID: 55, Count: 10}) {
		t.Fatalf("carried = %+v", got)
	}
	if !sess.stackAt(slotHotbarStart).Empty() {
		t.Fatalf("source slot = %+v", sess.stackAt(slotHotbarStart))
	}
	// Place onto a same-item stack: merge up to 64.
	s.handleClickContainer(sess, clickPacket(t, sess, slotMainStart, 0, 0))
	if got := sess.stackAt(slotMainStart); got != (ItemStack{ID: 55, Count: 64}) {
		t.Fatalf("target = %+v", got)
	}
	if got := sess.carried; got != (ItemStack{ID: 55, Count: 6}) {
		t.Fatalf("carried = %+v", got)
	}
}

func TestClickRightSplit(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Clicker", 8.5, -60, 8.5)
	sess.setStack(slotMainStart, ItemStack{ID: 55, Count: 5})

	// Right click picks up half (rounded up), then places one at a time.
	s.handleClickContainer(sess, clickPacket(t, sess, slotMainStart, 1, 0))
	if got := sess.carried; got != (ItemStack{ID: 55, Count: 3}) {
		t.Fatalf("carried = %+v", got)
	}
	if got := sess.stackAt(slotMainStart); got != (ItemStack{ID: 55, Count: 2}) {
		t.Fatalf("slot = %+v", got)
	}
	s.handleClickContainer(sess, clickPacket(t, sess, slotHotbarStart, 1, 0))
	if got := sess.stackAt(slotHotbarStart); got != (ItemStack{ID: 55, Count: 1}) {
		t.Fatalf("placed = %+v", got)
	}
	if got := sess.carried; got != (ItemStack{ID: 55, Count: 2}) {
		t.Fatalf("carried = %+v", got)
	}
}

func TestClickQuickMove(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Clicker", 8.5, -60, 8.5)
	sess.setStack(slotHotbarStart, ItemStack{ID: 1, Count: 64})
	sess.setStack(slotMainStart, ItemStack{ID: 1, Count: 64})
	sess.setStack(slotMainStart+1, ItemStack{ID: 1, Count: 1})

	s.handleClickContainer(sess, clickPacket(t, sess, slotHotbarStart, 0, 1))
	if !sess.stackAt(slotHotbarStart).Empty() {
		t.Fatalf("source = %+v", sess.stackAt(slotHotbarStart))
	}
	if got := sess.stackAt(slotMainStart + 1); got != (ItemStack{ID: 1, Count: 64}) {
		t.Fatalf("merge target = %+v", got)
	}
}

func TestClickCreativeClone(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Clicker", 8.5, -60, 8.5)
	sess.setStack(slotMainStart, ItemStack{ID: 1, Count: 7})
	sess.setMode(GameModeCreative)

	s.handleClickContainer(sess, clickPacket(t, sess, slotMainStart, 0, 3))
	if got := sess.carried; !got.Empty() {
		t.Fatalf("clone wrong button = %+v", got)
	}
	s.handleClickContainer(sess, clickPacket(t, sess, slotMainStart, 2, 3))
	if got := sess.carried; got != (ItemStack{ID: 1, Count: maxStack}) {
		t.Fatalf("clone full = %+v", got)
	}
}

// TestClickStaleStateResyncs proves a click with a stale state id is
// ignored: the slot and cursor keep their stacks, and the server
// answers with a full window-0 resync carrying the live state.
func TestClickStaleStateResyncs(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Clicker", 8.5, -60, 8.5)
	sess.setStack(slotHotbarStart, ItemStack{ID: 55, Count: 10})
	live := sess.invState.Load()
	for len(sess.sendCh) > 0 {
		<-sess.sendCh
	}

	var b protocol.Buffer
	b.VarInt(0)
	b.VarInt(live + 1) // stale
	b.Short(slotHotbarStart)
	b.Byte(0)
	b.VarInt(0)
	b.VarInt(0) // changed slots
	b.VarInt(0) // carried item: empty
	s.handleClickContainer(sess, protocol.Packet{ID: IDPlayClickContainerSB, Data: b.Bytes()})
	if got := sess.stackAt(slotHotbarStart); got != (ItemStack{ID: 55, Count: 10}) {
		t.Fatalf("slot = %+v", got)
	}
	if !sess.carried.Empty() {
		t.Fatalf("carried = %+v", sess.carried)
	}
	for {
		var p sendPkt
		select {
		case p = <-sess.sendCh:
		default:
			t.Fatal("no resync after stale click")
		}
		if p.id != IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.data)
		wid, _ := r.VarInt()
		st, _ := r.VarInt()
		if wid != 0 || st != live {
			t.Fatalf("resync window=%d state=%d, want 0/%d", wid, st, live)
		}
		return
	}
}

func TestContainerContentCarriesInventory(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Inv")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Inv")
	if sess == nil {
		t.Fatal("session missing")
	}
	sess.setStack(slotHotbarStart, ItemStack{ID: 1, Count: 3})

	var b protocol.Buffer
	b.VarInt(0)
	b.VarInt(sess.invState.Load())
	writeSlot(&b, ItemStack{})
	if err := s.sendContainerContent(sess); err != nil {
		t.Fatal(err)
	}
	_ = b
	// The client reads window 0, state, 46 slots, carried.
	p, err := waitPacket(t, pr, IDPlayContainerContent)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if w, err := r.VarInt(); err != nil || w != 0 {
		t.Fatalf("window = %d, err %v", w, err)
	}
	if _, err := r.VarInt(); err != nil {
		t.Fatal(err)
	}
	n, err := r.VarInt()
	if err != nil || n != slotCount {
		t.Fatalf("slots = %d, err %v", n, err)
	}
	for i := int32(0); i < n; i++ {
		it, err := readSlot(r)
		if err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
		if i == slotHotbarStart && it != (ItemStack{ID: 1, Count: 3}) {
			t.Fatalf("hotbar = %+v", it)
		}
	}
}

func TestCommandTreeListsBuiltins(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Cmds")
	defer c.Close()
	drainJoinBurst(t, pr)
	sess := s.findSessionByName("Cmds")
	if sess == nil {
		t.Fatal("session missing")
	}
	if err := s.sendCommands(sess); err != nil {
		t.Fatal(err)
	}
	p, err := waitPacket(t, pr, IDPlayCommands)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	nodes, err := r.VarInt()
	if err != nil || nodes < 2 {
		t.Fatalf("nodes = %d, err %v", nodes, err)
	}
	names := s.helpNames()
	if len(names) == 0 {
		t.Fatal("no builtin commands registered")
	}
	// Root: no flags, len(names) children.
	flags, _ := r.Byte()
	if flags != 0 {
		t.Fatalf("root flags = %#x", flags)
	}
	children, _ := r.VarInt()
	if int(children) != len(names) {
		t.Fatalf("root children = %d, want %d", children, len(names))
	}
	for i := int32(0); i < children; i++ {
		if _, err := r.VarInt(); err != nil { // child index
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for i := int32(0); i < children; i++ {
		f, _ := r.Byte()
		if f != cmdNodeLiteral|cmdNodeExecutable {
			t.Fatalf("literal flags = %#x", f)
		}
		if n, _ := r.VarInt(); n != 0 {
			t.Fatalf("literal children = %d", n)
		}
		name, err := r.String()
		if err != nil {
			t.Fatal(err)
		}
		seen[name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("command %q missing from tree", name)
		}
	}
}
