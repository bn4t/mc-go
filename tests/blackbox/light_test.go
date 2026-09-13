package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// readMask decodes a protocol BitSet (VarInt long count + longs).
func readMask(t *testing.T, r *protocol.PacketReader) []uint64 {
	t.Helper()
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 64 {
		t.Fatalf("mask longs = %d, err %v", n, err)
	}
	out := make([]uint64, n)
	for i := range out {
		v, err := r.Long()
		if err != nil {
			t.Fatalf("mask long %d: %v", i, err)
		}
		out[i] = uint64(v)
	}
	return out
}

func bitSet(mask []uint64, bit int) bool {
	li := bit / 64
	if li >= len(mask) {
		return false
	}
	return mask[li]&(1<<uint(bit%64)) != 0
}

// waitLightUpdate reads until an Update Light for chunk (0,0) arrives.
func waitLightUpdate(t *testing.T, c interface{ SetReadDeadline(time.Time) error }, pr *protocol.Reader, timeout time.Duration) *protocol.Packet {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			return nil
		}
		if p.ID != server.IDPlayUpdateLight {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		cx, _ := r.VarInt()
		cz, _ := r.VarInt()
		if cx == 0 && cz == 0 {
			return &p
		}
	}
	return nil
}

// TestBlockChangeSendsLightUpdate proves the column-model light engine
// at the packet level: placing a block high above the ground shadows
// its whole column (empty sky masks for the affected sections), and
// removing it restores explicit full-sky arrays.
func TestBlockChangeSendsLightUpdate(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Light")
	defer c.Close()
	drainToJoinBlackbox(t, "light", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)

	// Place a block at y=100 in column (8,0): that column's top goes
	// -61 -> 100, so sections 0..10 each change (one dark column among
	// the lit ones) and carry explicit sky arrays.
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 100 0 stone")
	p := waitLightUpdate(t, c, pr, 5*time.Second)
	if p == nil {
		t.Fatal("no light update after setblock")
	}
	r := protocol.NewPacketReader(p.Data)
	_, _ = r.VarInt() // chunk x
	_, _ = r.VarInt() // chunk z
	sky := readMask(t, r)
	_ = readMask(t, r) // block mask
	_ = readMask(t, r) // empty sky mask
	_ = readMask(t, r) // empty block mask
	arrays, err := r.VarInt()
	if err != nil {
		t.Fatalf("sky arrays: %v", err)
	}
	if arrays != 11 {
		t.Fatalf("shadowed update arrays = %d, want 11", arrays)
	}
	// Sections 0..10 are in the sky mask (bits are section+1).
	for sec := 0; sec <= 10; sec++ {
		if !bitSet(sky, sec+1) {
			t.Fatalf("section %d missing from sky mask %x", sec, sky)
		}
	}
	// Every array carries a dark column at x=8, z=0. Arrays are
	// length-prefixed (Prefixed Array of Byte).
	for sec := 0; sec <= 10; sec++ {
		n, err := r.VarInt()
		if err != nil || n != 2048 {
			t.Fatalf("array %d length = %d, err %v", sec, n, err)
		}
		arr, err := r.Read(int(n))
		if err != nil {
			t.Fatalf("array %d: %v", sec, err)
		}
		for yOff := 0; yOff < 16; yOff++ {
			y := -64 + sec*16 + yOff
			idx := yOff*256 + 8 // z=0, x=8
			if y > 100 {
				continue // above the placed block: lit
			}
			b := idx / 2
			nib := arr[b] & 0x0F // even index: low nibble
			if nib != 0 {
				t.Fatalf("section %d y=%d column (8,0) light = %d, want 0", sec, y, nib)
			}
		}
	}
	if n, err := r.VarInt(); err != nil || n != 0 {
		t.Fatalf("block light arrays = %d, err %v (want 0)", n, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing light bytes: %d", r.Remaining())
	}

	// Remove the block: all 11 sections become fully lit arrays.
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 100 0 air")
	p2 := waitLightUpdate(t, c, pr, 5*time.Second)
	if p2 == nil {
		t.Fatal("no light update after removing the block")
	}
	r2 := protocol.NewPacketReader(p2.Data)
	_, _ = r2.VarInt()
	_, _ = r2.VarInt()
	sky2 := readMask(t, r2)
	_ = readMask(t, r2)
	emptySky2 := readMask(t, r2)
	_ = readMask(t, r2)
	n2, err := r2.VarInt()
	if err != nil {
		t.Fatalf("sky arrays: %v", err)
	}
	if n2 != 11 {
		t.Fatalf("restore arrays = %d, want 11", n2)
	}
	if len(emptySky2) != 0 {
		t.Fatalf("restore left empty sky bits: %x", emptySky2)
	}
	for sec := 0; sec < 11; sec++ {
		n, err := r2.VarInt()
		if err != nil || n != 2048 {
			t.Fatalf("array %d length = %d, err %v", sec, n, err)
		}
		arr, err := r2.Read(int(n))
		if err != nil {
			t.Fatalf("array %d: %v", sec, err)
		}
		for i, v := range arr {
			want := byte(0xFF)
			if sec == 0 && i < 512 {
				want = 0 // the four solid ground layers stay dark
			}
			if v != want {
				t.Fatalf("section %d byte %d = %#x, want %#x", sec, i, v, want)
			}
		}
	}
	if !bitSet(sky2, 1) {
		t.Fatalf("restore sky mask lacks section 0: %x", sky2)
	}
}
