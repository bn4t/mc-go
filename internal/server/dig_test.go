package server

import (
	"testing"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestPositionCodecRoundTrip(t *testing.T) {
	cases := [][3]int32{
		{0, 0, 0}, {1, 2, 3}, {-1, -2, -3},
		{8, 65, 8}, {3, 70, 3}, {-30, 62, 30},
		{29999984, 319, 29999984}, {-29999984, -64, -29999984},
		{33554431, 2047, 33554431}, {-33554432, -2048, -33554432},
	}
	for _, c := range cases {
		v := protocol.EncodePosition(c[0], c[1], c[2])
		if x, y, z := protocol.DecodePosition(v); x != c[0] || y != c[1] || z != c[2] {
			t.Fatalf("pos %v round-trips as %d,%d,%d", c, x, y, z)
		}
		var b protocol.Buffer
		b.Position(c[0], c[1], c[2])
		r := protocol.NewPacketReader(b.Bytes())
		if x, y, z, err := r.Position(); err != nil || x != c[0] || y != c[1] || z != c[2] {
			t.Fatalf("framed %v = %d,%d,%d err %v", c, x, y, z, err)
		}
	}
}

func TestBlockUpdateEncoding(t *testing.T) {
	body := encodeBlockUpdate(3, 70, -5, StateStone)
	r := protocol.NewPacketReader(body)
	x, y, z, err := r.Position()
	if err != nil || x != 3 || y != 70 || z != -5 {
		t.Fatalf("pos = %d,%d,%d err %v", x, y, z, err)
	}
	if st, err := r.VarInt(); err != nil || st != StateStone {
		t.Fatalf("state = %d err %v", st, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing = %d", r.Remaining())
	}
}

func sendDig(t *testing.T, pw *protocol.Writer, status int32, x, y, z int32, seq int32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(status)
	b.Position(x, y, z)
	b.VarInt(1) // face: top
	b.VarInt(seq)
	if err := pw.WritePacket(IDPlayPlayerActionSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func readAck(t *testing.T, pr *protocol.Reader) int32 {
	t.Helper()
	p, err := pr.ReadPacket()
	if err != nil {
		t.Fatalf("ack read: %v", err)
	}
	if p.ID != IDPlayBlockChangedAck {
		t.Fatalf("want ack %#x, got %#x", IDPlayBlockChangedAck, p.ID)
	}
	seq, err := protocol.NewPacketReader(p.Data).VarInt()
	if err != nil {
		t.Fatalf("ack seq: %v", err)
	}
	return seq
}

func TestDigBreaksBlockToAir(t *testing.T) {
	s := New("unused")
	ch, unsub := s.Bus().Subscribe(event.TopicBlockChange, 8)
	defer unsub()
	c, pr, pw := joinPlay(t, s, "Digger")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	pos := world.Pos{X: 2, Y: 70, Z: 2}
	s.world.SetBlock(pos, world.Stone) // direct: no broadcast
	sendDig(t, pw, digStart, 2, 70, 2, 7)

	upd, err := pr.ReadPacket()
	if err != nil || upd.ID != IDPlayBlockUpdateCB {
		t.Fatalf("update = %#x, err %v", upd.ID, err)
	}
	ur := protocol.NewPacketReader(upd.Data)
	if x, y, z, err := ur.Position(); err != nil || x != 2 || y != 70 || z != 2 {
		t.Fatalf("pos = %d,%d,%d err %v", x, y, z, err)
	}
	if st, err := ur.VarInt(); err != nil || st != StateAir {
		t.Fatalf("state = %d err %v, want air", st, err)
	}
	if seq := readAck(t, pr); seq != 7 {
		t.Fatalf("ack seq = %d, want 7", seq)
	}
	if got := s.world.GetBlock(pos); got != world.Air {
		t.Fatalf("world = %d, want air", got)
	}
	select {
	case ev := <-ch:
		bc := ev.Data.(BlockChange)
		if bc.X != 2 || bc.Y != 70 || bc.Z != 2 || bc.Block != world.Air || bc.Player != "Digger" {
			t.Fatalf("event = %+v", bc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no BlockChange event")
	}
}

func TestDigFinishAlsoBreaks(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Digger")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	pos := world.Pos{X: 4, Y: 70, Z: -1}
	s.world.SetBlock(pos, world.Dirt)
	sendDig(t, pw, digFinish, 4, 70, -1, 11)
	upd, err := pr.ReadPacket()
	if err != nil || upd.ID != IDPlayBlockUpdateCB {
		t.Fatalf("update = %#x, err %v", upd.ID, err)
	}
	if seq := readAck(t, pr); seq != 11 {
		t.Fatalf("ack = %d, want 11", seq)
	}
	if got := s.world.GetBlock(pos); got != world.Air {
		t.Fatalf("world = %d, want air", got)
	}
}

func TestDigCancelChangesNothing(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Digger")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	pos := world.Pos{X: 2, Y: 70, Z: 2}
	s.world.SetBlock(pos, world.Stone)
	sendDig(t, pw, digCancel, 2, 70, 2, 8)
	if seq := readAck(t, pr); seq != 8 {
		t.Fatalf("ack = %d, want 8", seq)
	}
	if got := s.world.GetBlock(pos); got != world.Stone {
		t.Fatalf("world = %d, want untouched stone", got)
	}
}

func TestDigAirIsAckOnly(t *testing.T) {
	s := New("unused")
	ch, unsub := s.Bus().Subscribe(event.TopicBlockChange, 8)
	defer unsub()
	c, pr, pw := joinPlay(t, s, "Digger")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	// (5,120,5) is air in the flat world and untouched.
	sendDig(t, pw, digStart, 5, 120, 5, 9)
	if seq := readAck(t, pr); seq != 9 {
		t.Fatalf("ack = %d, want 9", seq)
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev.Data)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDigOutOfRangeDropped(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Digger")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sendDig(t, pw, digStart, 0, 9999, 0, 12)
	if seq := readAck(t, pr); seq != 12 {
		t.Fatalf("ack = %d, want 12", seq)
	}
}

func TestDigBroadcastReachesWatcher(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Watcher")
	defer ca.Close()
	drainJoinBurst(t, pra)
	cb, prb, pwb := joinPlay(t, s, "Digger")
	defer cb.Close()
	drainJoinBurst(t, prb)
	waitPlayerInfo(t, prb, "Watcher")
	expectPlayerSpawnAnnouncement(t, prb, "Watcher")
	drainToQuiet(t, prb)
	waitSystemChat(t, pra, "Digger")
	waitPlayerInfo(t, pra, "Digger")
	expectPlayerSpawnAnnouncement(t, pra, "Digger")

	pos := world.Pos{X: 6, Y: 70, Z: 6}
	s.world.SetBlock(pos, world.Grass)
	// Drain the direct-set silence: no packets expected. Dig for real.
	sendDig(t, pwb, digStart, 6, 70, 6, 21)
	// Digger: update + ack.
	if _, err := prb.ReadPacket(); err != nil {
		t.Fatalf("digger update: %v", err)
	}
	if seq := readAck(t, prb); seq != 21 {
		t.Fatalf("digger ack = %d", seq)
	}
	// Watcher: update for the same block.
	upd, err := waitPacket(t, pra, IDPlayBlockUpdateCB)
	if err != nil {
		t.Fatalf("watcher update: %v", err)
	}
	ur := protocol.NewPacketReader(upd.Data)
	x, y, z, err := ur.Position()
	if err != nil || x != 6 || y != 70 || z != 6 {
		t.Fatalf("watcher pos = %d,%d,%d err %v", x, y, z, err)
	}
	if st, err := ur.VarInt(); err != nil || st != StateAir {
		t.Fatalf("watcher state = %d err %v, want air", st, err)
	}
}
