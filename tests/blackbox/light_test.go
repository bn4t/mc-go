package blackbox

import (
	"bytes"
	"fmt"
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

// lightSnapshot is one decoded Update Light packet's variable parts:
// the four masks plus every shipped array (arrays ride in mask order).
type lightSnapshot struct {
	skyMask, blockMask, emptySky, emptyBlock []uint64
	skyArrays, blockArrays                   [][]byte
}

func decodeLightUpdate(t *testing.T, p *protocol.Packet) (int32, int32, lightSnapshot) {
	t.Helper()
	var snap lightSnapshot
	r := protocol.NewPacketReader(p.Data)
	cx, _ := r.VarInt()
	cz, _ := r.VarInt()
	snap.skyMask = readMask(t, r)
	snap.blockMask = readMask(t, r)
	snap.emptySky = readMask(t, r)
	snap.emptyBlock = readMask(t, r)
	readArrays := func() [][]byte {
		n, err := r.VarInt()
		if err != nil {
			t.Fatalf("array count: %v", err)
		}
		out := make([][]byte, 0, n)
		for i := int32(0); i < n; i++ {
			ln, err := r.VarInt()
			if err != nil || ln != 2048 {
				t.Fatalf("light array length = %d, err %v", ln, err)
			}
			arr, err := r.Read(int(ln))
			if err != nil {
				t.Fatalf("light array: %v", err)
			}
			out = append(out, arr)
		}
		return out
	}
	snap.skyArrays = readArrays()
	snap.blockArrays = readArrays()
	if r.Remaining() != 0 {
		t.Fatalf("trailing light bytes: %d", r.Remaining())
	}
	return cx, cz, snap
}

func maskString(mask []uint64) string {
	out := "["
	for i := 0; i < 40; i++ {
		if bitSet(mask, i) {
			out += string(rune('0'+i)) + " "
		}
	}
	return out + "]"
}

func nonzero(arr []byte) int {
	n := 0
	for _, v := range arr {
		if v != 0 {
			n++
		}
	}
	return n
}

// collectChunkLights reads until the first Update Light arrives, then
// sweeps stragglers for quietFor longer — vanilla emits one packet per
// affected chunk in the same tick, in arbitrary order, and occasionally
// skips the centre chunk's packet, so parity compares per chunk.
func collectChunkLights(t *testing.T, c interface{ SetReadDeadline(time.Time) error }, pr *protocol.Reader, pw *protocol.Writer, firstWait, quietFor time.Duration) map[[2]int]lightSnapshot {
	t.Helper()
	out := map[[2]int]lightSnapshot{}
	first := time.Now().Add(firstWait)
	for len(out) == 0 && time.Now().Before(first) {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		}
		if p.ID != server.IDPlayUpdateLight {
			continue
		}
		cx, cz, snap := decodeLightUpdate(t, &p)
		out[[2]int{int(cx), int(cz)}] = snap
	}
	if len(out) == 0 {
		return out
	}
	quiet := time.Now().Add(quietFor)
	for time.Now().Before(quiet) {
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		}
		if p.ID != server.IDPlayUpdateLight {
			continue
		}
		quiet = time.Now().Add(quietFor)
		cx, cz, snap := decodeLightUpdate(t, &p)
		out[[2]int{int(cx), int(cz)}] = snap
	}
	return out
}

// TestTorchLightParity pins vanilla's block-light wire on both
// servers: a torch placed in a flat overworld sends Update Light
// carrying ONLY block arrays for the sections it lit (the sky side
// stays silent — a torch does not shadow its column), and breaking it
// re-sends the same sections with all-zero arrays (the section object
// survives a removal, so the empty mask is never used). Masks and
// nibble arrays must match vanilla byte for byte.
//
// Measured on 26.2 (LayerLightSectionUpdate / ClientboundLightUpdatePacket):
// place at (8,-58,0) -> the 29x29 lit region spans chunks x{-1..1},
// z{-1..0}; each ships its own packet, blockMask bits {1,2} (sections
// 0,1) for chunks below the spread, {1} elsewhere; remove -> the same
// masks with all-zero arrays. Vanilla occasionally drops the centre
// chunk's packet entirely — the comparison is over the chunks vanilla
// actually emitted.
func TestTorchLightParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("LP")}, nil)
	defer targets.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()
	var legs [2]struct {
		place, remove map[[2]int]lightSnapshot
	}
	for i, addr := range []string{targets.vanilla, targets.mcgo} {
		leg := "vanilla"
		if i == 1 {
			leg = "mc-go"
		}
		c, pr, pw := joinPlayBlackbox(t, addr, "LP")
		drainToJoinBlackbox(t, "lp", pr)
		drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
		sendCommand(t, pw, "setblock 8 -58 0 torch")
		legs[i].place = collectChunkLights(t, c, pr, pw, 20*time.Second, 700*time.Millisecond)
		if len(legs[i].place) == 0 {
			t.Fatalf("%s (%s): no update_light after placing the torch", addr, leg)
		}
		sendCommand(t, pw, "setblock 8 -58 0 air")
		legs[i].remove = collectChunkLights(t, c, pr, pw, 20*time.Second, 700*time.Millisecond)
		if len(legs[i].remove) == 0 {
			t.Fatalf("%s (%s): no update_light after removing the torch", addr, leg)
		}
		c.Close()
	}
	van, got := legs[0], legs[1]

	// Anti-vacuous guards on the vanilla leg: some chunk must ship lit
	// block nibbles on place and all-zero arrays on remove.
	litBytes := 0
	for _, snap := range van.place {
		for _, a := range snap.blockArrays {
			litBytes += nonzero(a)
		}
	}
	if litBytes == 0 {
		t.Fatal("vanilla place shipped no lit block-light bytes")
	}
	for ck, snap := range van.remove {
		for i, a := range snap.blockArrays {
			if nonzero(a) != 0 {
				t.Fatalf("vanilla remove %v array %d has %d nonzero bytes, want 0", ck, i, nonzero(a))
			}
		}
	}

	cmpSnap := func(what string, ck [2]int, a, b lightSnapshot) {
		t.Helper()
		if fmt.Sprintf("%x", a.skyMask) != fmt.Sprintf("%x", b.skyMask) ||
			fmt.Sprintf("%x", a.blockMask) != fmt.Sprintf("%x", b.blockMask) ||
			fmt.Sprintf("%x", a.emptySky) != fmt.Sprintf("%x", b.emptySky) ||
			fmt.Sprintf("%x", a.emptyBlock) != fmt.Sprintf("%x", b.emptyBlock) {
			t.Fatalf("%s %v masks: vanilla sky=%s block=%s emptySky=%s emptyBlock=%s; mc-go sky=%s block=%s emptySky=%s emptyBlock=%s",
				what, ck, maskString(a.skyMask), maskString(a.blockMask), maskString(a.emptySky), maskString(a.emptyBlock),
				maskString(b.skyMask), maskString(b.blockMask), maskString(b.emptySky), maskString(b.emptyBlock))
		}
		if len(a.skyArrays) != len(b.skyArrays) || len(a.blockArrays) != len(b.blockArrays) {
			t.Fatalf("%s %v array counts: vanilla sky=%d block=%d; mc-go sky=%d block=%d",
				what, ck, len(a.skyArrays), len(a.blockArrays), len(b.skyArrays), len(b.blockArrays))
		}
		for i := range a.blockArrays {
			if !bytes.Equal(a.blockArrays[i], b.blockArrays[i]) {
				t.Fatalf("%s %v block array %d differs from vanilla", what, ck, i)
			}
		}
		for i := range a.skyArrays {
			if !bytes.Equal(a.skyArrays[i], b.skyArrays[i]) {
				t.Fatalf("%s %v sky array %d differs from vanilla", what, ck, i)
			}
		}
	}
	// Every chunk vanilla emitted must match mc-go's packet for the
	// same chunk; mc-go must cover the chunks vanilla skipped too.
	for ck, vsnap := range van.place {
		msnap, ok := got.place[ck]
		if !ok {
			t.Fatalf("place %v: vanilla emitted, mc-go did not", ck)
		}
		cmpSnap("place", ck, vsnap, msnap)
	}
	if _, ok := got.place[[2]int{0, 0}]; !ok {
		t.Fatal("place: mc-go did not emit for the torch's own chunk (0,0)")
	}
	for ck, vsnap := range van.remove {
		msnap, ok := got.remove[ck]
		if !ok {
			t.Fatalf("remove %v: vanilla emitted, mc-go did not", ck)
		}
		cmpSnap("remove", ck, vsnap, msnap)
	}
}
