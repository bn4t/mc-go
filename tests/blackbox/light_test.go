package blackbox

import (
	"bytes"
	"fmt"
	"net"
	"reflect"
	"strings"
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

// TestBlockChangeSendsLightUpdate pins the single-opaque-block probe measured
// by TestSkyPropParity: below the block skylight is 14 from horizontal
// propagation; removing it restores 15 in the retained lower layers. The
// observer stands in chunk (3,0), placing chunk (0,0) on its tracked west border.
// A focused vanilla 26.2 wire probe of the existing SkyPropParity scenario
// measured place mask 0x1c6 (five arrays), then removal mask 0x6 (two arrays).
// Removal omits the higher layer containing y=39; that omission alone does
// not encode a light value. Assert the restored nibbles in a shipped layer.
func TestBlockChangeSendsLightUpdate(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Light")
	defer c.Close()
	drainToJoinBlackbox(t, "light", pr)
	awaitPlayReadyBlackbox(t, "light", c, pr, pw)
	if err := sendMovePosRot(pw, 56.5, 100, 8.5); err != nil {
		t.Fatal(err)
	}

	skyAt := func(snap lightSnapshot, x, y, z int) (byte, bool) {
		bit := (y+64)/16 + 1
		if !bitSet(snap.skyMask, bit) {
			return 0, false
		}
		array := 0
		for i := 0; i < bit; i++ {
			if bitSet(snap.skyMask, i) {
				array++
			}
		}
		if array >= len(snap.skyArrays) {
			t.Fatal("sky mask names an absent array")
		}
		index := (y&15)*256 + (z&15)*16 + (x & 15)
		value := snap.skyArrays[array][index/2]
		if index&1 != 0 {
			value >>= 4
		}
		return value & 15, true
	}
	checkMasks := func(mask, empty []uint64, arrays [][]byte) {
		count := 0
		for i := 0; i < len(mask)*64; i++ {
			if !bitSet(mask, i) {
				continue
			}
			if bitSet(empty, i) {
				t.Fatalf("light section %d is both populated and empty", i)
			}
			count++
		}
		if count != len(arrays) {
			t.Fatalf("light mask names %d arrays, packet carries %d", count, len(arrays))
		}
	}

	for _, phase := range []struct {
		name, block string
		sampleY     int
		below, at   byte
	}{
		{"shadow", "stone", 39, 14, 0},
		{"restore", "air", -59, 15, 15},
	} {
		sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 40 8 "+phase.block)
		deadline := time.Now().Add(10 * time.Second)
		_ = c.SetReadDeadline(deadline)
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s: waiting for sky %d below block: %v", phase.name, phase.below, err)
			}
			switch p.ID {
			case server.IDPlayChunkBatchDone:
				if err := ackChunkBatch(pw); err != nil {
					t.Fatal(err)
				}
				continue
			case server.IDPlayKeepAliveCB:
				if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
					t.Fatal(err)
				}
				continue
			case server.IDPlayUpdateLight:
			default:
				continue
			}
			cx, cz, snap := decodeLightUpdate(t, &p)
			if cx != 0 || cz != 0 {
				continue
			}
			checkMasks(snap.skyMask, snap.emptySky, snap.skyArrays)
			checkMasks(snap.blockMask, snap.emptyBlock, snap.blockArrays)
			below, present := skyAt(snap, 8, phase.sampleY, 8)
			// Initial chunk settling can precede the command's update.
			// Wait for semantic state instead of assuming a packet count.
			if !present || below != phase.below {
				continue
			}
			samples := []struct {
				x, y, z int
				want    byte
			}{
				{8, 40, 8, phase.at}, {8, 41, 8, 15}, {9, 39, 8, 15},
			}
			if phase.name == "restore" {
				if !bitSet(snap.skyMask, 1) || !bitSet(snap.skyMask, 2) || bitSet(snap.skyMask, 7) || bitSet(snap.emptySky, 7) {
					t.Fatalf("restore masks sky=%x emptySky=%x: want retained low layers and no y=39 layer", snap.skyMask, snap.emptySky)
				}
				samples = []struct {
					x, y, z int
					want    byte
				}{
					{8, -59, 8, 15}, {9, -59, 8, 15}, {8, -43, 8, 15},
				}
			}
			for _, sample := range samples {
				got, present := skyAt(snap, sample.x, sample.y, sample.z)
				if !present || got != sample.want {
					t.Fatalf("%s: sky at (%d,%d,%d)=%d present=%v, want %d", phase.name, sample.x, sample.y, sample.z, got, present, sample.want)
				}
			}
			break
		}
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

func nonzeroArrays(arrays [][]byte) int {
	n := 0
	for _, a := range arrays {
		n += nonzero(a)
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
		if p.ID == server.IDPlayChunkBatchDone {
			_ = ackChunkBatch(pw)
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
	// Hard cap: a settling world (the nether's ambient light engine,
	// mostly) can feed updates forever — the sweep is over once the
	// stream quiets OR the cap is hit, never on traffic alone.
	deadline := time.Now().Add(quietFor * 8)
	for time.Now().Before(quiet) && time.Now().Before(deadline) && len(out) < 128 {
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		}
		if p.ID == server.IDPlayChunkBatchDone {
			_ = ackChunkBatch(pw)
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
// survives a removal, so it ships an array rather than the empty
// mask). Masks and nibble arrays must match vanilla byte for byte.
//
// Vanilla broadcasts an update only to players whose tracked chunk
// set has the lit chunk on its border — ChunkMap.getPlayers(pos,
// true) → isChunkOnTrackedBorder — so the test teleports the player
// to (56.5,100,8.5) (chunk 3,0): the fallback 7x7 tracked window then
// covers chunks x{0..6} x z{-3..3}, putting the torch's lit chunks
// (0,-1) and (0,0) on the west border while (1,*) stays interior and
// gets no wire update on either server. Chunks x<0 are outside the
// window entirely — the spread into them is never reported.
//
// Measured on 26.2 (LayerLightSectionUpdate / ClientboundLightUpdatePacket):
// place at (8,-58,0) -> update for chunk (0,0) and (0,-1); every
// section a lit cell's 3x3x3 neighbourhood touches is marked
// (SectionPos.aroundAndAtBlockPos), and a marked section with no
// materialized layer ships on emptyBlock instead of blockMask.
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
		sendCommand(t, pw, "tp @s 56.5 100 8.5")
		streamed := settleChunks(t, c, pr, pw, 4*time.Second)
		if !streamed[[2]int{0, 0}] {
			t.Fatalf("%s (%s): torch chunk (0,0) was never streamed — window keys %v", addr, leg, windowKeys(streamed))
		}
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
		cmpLightSnap(t, what, ck, a, b)
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

// TestNetherTorchLightParity pins vanilla's Update Light behaviour in a
// no-skylight dimension: block light propagates and broadcasts exactly
// like the overworld — same mask semantics, same nibble arrays — while
// the sky side stays silent. Measured on 26.2 inside a sealed
// netherrack pocket (ambient lava light can't leak in): a torch place
// emits blockMask {5} (section 4) with the 14-level spread array on the
// torch's chunk alone, and breaking it re-sends the section with an
// all-zero array.
//
// The pocket: shell at x/z 96..104, y 60..78, hollow x/z 97..103,
// y 64..76 — the torch's spread dies against the opaque walls inside
// chunk (6,6), so exactly one packet per write is expected.
func TestNetherTorchLightParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("NL")}, nil)
	defer targets.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()
	var legs [2]struct {
		pre, place, remove map[[2]int]lightSnapshot
	}
	for i, addr := range []string{targets.vanilla, targets.mcgo} {
		leg := "vanilla"
		if i == 1 {
			leg = "mc-go"
		}
		c, pr, pw := joinPlayBlackbox(t, addr, "NL")
		drainToJoinBlackbox(t, "nl", pr)
		sendCommand(t, pw, "execute in minecraft:the_nether run tp @s 100.5 70 100.5")
		// Vanilla gates light updates to the tracked border of the
		// client's view: a tracked column with at least one untracked
		// neighbour (ChunkMap.getPlayers(pos, true)). A torch placed
		// mid-view emits nothing on the wire — the client relights it
		// locally — so the sealed pocket sits three columns east, on the
		// edge of the client-default view-distance-2 window.
		streamed := settleChunks(t, c, pr, pw, 4*time.Second)
		sendCommand(t, pw, "execute in minecraft:the_nether run fill 144 60 96 152 78 104 minecraft:netherrack")
		sendCommand(t, pw, "execute in minecraft:the_nether run fill 145 64 97 151 76 103 minecraft:air")
		if !streamed[[2]int{9, 6}] {
			t.Fatalf("%s (%s): chunk (9,6) never streamed (window %v)", addr, leg, windowKeys(streamed))
		}
		// Let the pocket's own light update pass before the torch write.
		legs[i].pre = collectChunkLightFor(t, c, pr, pw, [2]int{9, 6}, 10*time.Second)

		sendCommand(t, pw, "execute in minecraft:the_nether run setblock 148 64 100 minecraft:torch")
		legs[i].place = collectChunkLightFor(t, c, pr, pw, [2]int{9, 6}, 30*time.Second)
		if len(legs[i].place[[2]int{9, 6}].blockArrays) == 0 {
			t.Fatalf("%s (%s): no update_light for chunk (9,6) after placing the nether torch (map %v)",
				addr, leg, lightKeys(legs[i].place))
		}
		sendCommand(t, pw, "execute in minecraft:the_nether run setblock 148 64 100 minecraft:air")
		legs[i].remove = collectChunkLightFor(t, c, pr, pw, [2]int{9, 6}, 30*time.Second)
		if len(legs[i].remove[[2]int{9, 6}].blockArrays) == 0 {
			t.Fatalf("%s (%s): no update_light for chunk (9,6) after removing the nether torch (map %v)",
				addr, leg, lightKeys(legs[i].remove))
		}
		c.Close()
	}
	van, got := legs[0], legs[1]

	// Anti-vacuous guards on the vanilla leg: chunk (9,6) must ship the
	// sealed section's lit array on place and an all-zero array on
	// remove.
	want := [2]int{9, 6}
	if snap, ok := van.place[want]; !ok || len(snap.blockArrays) == 0 || nonzeroArrays(snap.blockArrays) == 0 {
		t.Fatalf("vanilla place: chunk (9,6) missing or unlit: %+v", van.place)
	}
	if snap, ok := van.remove[want]; !ok || len(snap.blockArrays) == 0 || nonzeroArrays(snap.blockArrays) != 0 {
		t.Fatalf("vanilla remove: chunk (9,6) not cleared: %+v", van.remove)
	}
	// Vanilla occasionally settles ambient light in unrelated chunks —
	// the sealed write only concerns the torch's chunk, so compare just
	// that chunk's snapshot. Sky masks must be empty on both servers.
	for _, phase := range []struct {
		name string
		m    map[[2]int]lightSnapshot
		g    map[[2]int]lightSnapshot
	}{
		{"place", van.place, got.place},
		{"remove", van.remove, got.remove},
	} {
		vs, vok := phase.m[want]
		ms, mok := phase.g[want]
		if vok != mok {
			t.Fatalf("%s: chunk (9,6) presence mismatch vanilla=%v mc-go=%v (mc-go map %+v)",
				phase.name, vok, mok, phase.g)
		}
		if !vok {
			continue
		}
		if !reflect.DeepEqual(vs.skyMask, ms.skyMask) ||
			!reflect.DeepEqual(vs.blockMask, ms.blockMask) ||
			!reflect.DeepEqual(vs.emptySky, ms.emptySky) ||
			!reflect.DeepEqual(vs.emptyBlock, ms.emptyBlock) {
			t.Fatalf("%s chunk (9,6): mask mismatch\nvanilla sky=%v block=%v esky=%v eblock=%v\nmc-go   sky=%v block=%v esky=%v eblock=%v\nvanilla arrays %v\nmc-go   arrays %v",
				phase.name,
				vs.skyMask, vs.blockMask, vs.emptySky, vs.emptyBlock,
				ms.skyMask, ms.blockMask, ms.emptySky, ms.emptyBlock,
				arrayStats(vs.blockArrays), arrayStats(ms.blockArrays))
		}
		if len(vs.skyArrays) != 0 || len(ms.skyArrays) != 0 {
			t.Fatalf("%s chunk (9,6): sky arrays in a no-skylight dimension: v=%d m=%d",
				phase.name, len(vs.skyArrays), len(ms.skyArrays))
		}
		if !reflect.DeepEqual(vs.blockArrays, ms.blockArrays) {
			t.Fatalf("%s chunk (9,6): block arrays mismatch (v=%d arrays m=%d)",
				phase.name, len(vs.blockArrays), len(ms.blockArrays))
		}
	}
}

// settleChunks drains for a fixed window, answering keepalives and
// chunk batches — long enough for the paced nether chunk stream to
// reach the tracked set near the player. Returns the columns whose
// level_chunk_with_light packets were seen.
func settleChunks(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) map[[2]int]bool {
	t.Helper()
	seen := map[[2]int]bool{}
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkData:
			r := protocol.NewPacketReader(p.Data)
			cx, ex := r.Int()
			cz, ez := r.Int()
			if ex == nil && ez == nil {
				seen[[2]int{int(cx), int(cz)}] = true
			}
		}
	}
	return seen
}

func windowKeys(m map[[2]int]bool) string {
	var b strings.Builder
	for k := range m {
		fmt.Fprintf(&b, "(%d,%d) ", k[0], k[1])
	}
	return b.String()
}

// collectChunkLightFor waits for an update_light packet naming the
// target chunk (ambient light traffic on other chunks does not end the
// wait — the settling nether floods those forever), then sweeps 700ms
// past the last packet for that chunk to catch any follow-up sections.
func collectChunkLightFor(t *testing.T, c interface {
	SetReadDeadline(time.Time) error
}, pr *protocol.Reader, pw *protocol.Writer, want [2]int, timeout time.Duration) map[[2]int]lightSnapshot {
	t.Helper()
	out := map[[2]int]lightSnapshot{}
	deadline := time.Now().Add(timeout)
	lastWant := time.Time{}
	for time.Now().Before(deadline) {
		if !lastWant.IsZero() && time.Since(lastWant) > 700*time.Millisecond {
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
			continue
		case server.IDPlayUpdateLight:
		default:
			continue
		}
		cx, cz, snap := decodeLightUpdate(t, &p)
		k := [2]int{int(cx), int(cz)}
		out[k] = mergeLightSnap(out[k], snap)
		if k == want {
			lastWant = time.Now()
		}
	}
	return out
}

// drainLight settles the light engine: vanilla keeps producing deferred
// sky/block updates well past the 700ms sweep — a fill's propagation can
// straggle into the next probe's window — so reads continue until the
// wire has been silent for `quiet`.
func drainLight(t *testing.T, c interface {
	SetReadDeadline(time.Time) error
}, pr *protocol.Reader, pw *protocol.Writer, quiet time.Duration) map[[2]int]lightSnapshot {
	t.Helper()
	out := map[[2]int]lightSnapshot{}
	last := time.Now()
	for time.Since(last) < quiet {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			continue
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
			continue
		case server.IDPlayUpdateLight:
		default:
			continue
		}
		cx, cz, snap := decodeLightUpdate(t, &p)
		out[[2]int{int(cx), int(cz)}] = mergeLightSnap(out[[2]int{int(cx), int(cz)}], snap)
		last = time.Now()
	}
	return out
}

// mergeLightSnap folds a fresh packet's snapshot into the running one:
// vanilla may emit several update_light packets for the same chunk in a
// window (the probe write plus a delayed sky-settle), so masks union and
// each shipped array is keyed to its mask bit — ascending set-bit order
// matches array order on the wire.
func mergeLightSnap(a, b lightSnapshot) lightSnapshot {
	secsOf := func(mask []uint64) []int {
		var out []int
		for i := 0; i < 40; i++ {
			if bitSet(mask, i) {
				out = append(out, i)
			}
		}
		return out
	}
	keyed := func(mask []uint64, arrays [][]byte) map[int][]byte {
		m := map[int][]byte{}
		for i, s := range secsOf(mask) {
			if i < len(arrays) {
				m[s] = arrays[i]
			}
		}
		return m
	}
	unkeyed := func(m map[int][]byte) ([]uint64, [][]byte) {
		mask := make([]uint64, 1)
		var arrays [][]byte
		for s := 0; s < 40; s++ {
			if a, ok := m[s]; ok {
				mask[0] |= 1 << uint(s)
				arrays = append(arrays, a)
			}
		}
		return mask, arrays
	}
	sky := keyed(a.skyMask, a.skyArrays)
	for s, arr := range keyed(b.skyMask, b.skyArrays) {
		sky[s] = arr
	}
	blk := keyed(a.blockMask, a.blockArrays)
	for s, arr := range keyed(b.blockMask, b.blockArrays) {
		blk[s] = arr
	}
	a.skyMask, a.skyArrays = unkeyed(sky)
	a.blockMask, a.blockArrays = unkeyed(blk)
	for i := 0; i < len(b.emptySky); i++ {
		if i < len(a.emptySky) {
			a.emptySky[i] |= b.emptySky[i]
		} else {
			a.emptySky = append(a.emptySky, b.emptySky[i])
		}
	}
	for i := 0; i < len(b.emptyBlock); i++ {
		if i < len(a.emptyBlock) {
			a.emptyBlock[i] |= b.emptyBlock[i]
		} else {
			a.emptyBlock = append(a.emptyBlock, b.emptyBlock[i])
		}
	}
	return a
}

func lightKeys(m map[[2]int]lightSnapshot) string {
	var b strings.Builder
	for k := range m {
		fmt.Fprintf(&b, "(%d,%d) ", k[0], k[1])
	}
	return b.String()
}

// arrayStats summarizes each nibble array as nonzero-nibble count —
// tells an all-zero array (empty-mask material) from a lit one without
// dumping 2048 bytes.
func arrayStats(arrays [][]byte) []int {
	out := make([]int, len(arrays))
	for i, a := range arrays {
		n := 0
		for _, v := range a {
			if v != 0 {
				n++
			}
		}
		out[i] = n
	}
	return out
}

// cmpLightSnap is the per-chunk snapshot comparison every light probe
// shares: the four masks must be bit-identical and every shipped
// nibble array byte-identical.
// emptyLightSnap reports whether a snapshot carries no actual light
// data: every mask bit zero and no arrays.
func emptyLightSnap(s lightSnapshot) bool {
	for _, m := range [4][]uint64{s.skyMask, s.blockMask, s.emptySky, s.emptyBlock} {
		for _, w := range m {
			if w != 0 {
				return false
			}
		}
	}
	return len(s.skyArrays) == 0 && len(s.blockArrays) == 0
}

func cmpLightSnap(t *testing.T, what string, ck [2]int, a, b lightSnapshot) {
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

// TestLightEmissionParity pins vanilla's emission table — every
// lightLevel lambda, constant or state-folded — by writing emitters
// into a border chunk one probe at a time and comparing the update
// light each produces on both servers. The player teleports to
// (56.5,100,8.5) so chunk (0,0) sits on the west border of the 7x7
// tracked window, exactly as the torch tests arrange.
//
// Measured on 26.2 (Blocks.* lightLevel registrations): the state-folded
// entries are CaveVines.emission(14) on berries, the sea pickle's
// waterlogged 3*(pickles+1), a lit candle's 3*candles, the respawn
// anchor's floor(15*charge/4), the light block's level prop, an
// inverted daylight detector and a powered lightning rod at 15 — plus
// the flat entries for beacon/conduit/end_gateway/end_portal (15),
// nether_portal and lava_cauldron (15/11), enchanting_table (7),
// sculk_catalyst (6), redstone ore and torch gated on lit.
//
// Probing subtleties measured on vanilla: an airborne
// waterlogged sea pickle drains its water and dies (emits 0), so the
// wet probes go into a real contained pool; cave vines need a block
// overhead or they self-destroy inside the tick; and a dark,
// non-occluding probe writes air→transparent — no light change, no
// packet — which both legs must reproduce.
func TestLightEmissionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("LE")}, nil)
	defer targets.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()
	probes := []struct {
		name, spec, restore string
		wantEmpty           bool // dark+transparent: air→transparent writes nothing, both legs must emit nothing
	}{
		{"cave vine berries", "minecraft:cave_vines[berries=true]", "minecraft:air", false},
		{"cave vine bare", "minecraft:cave_vines[berries=false]", "minecraft:air", true},
		{"sea pickle dry", "minecraft:sea_pickle[pickles=3,waterlogged=false]", "minecraft:air", true},
		{"candle x4 lit", "minecraft:candle[candles=4,lit=true]", "minecraft:air", false},
		{"candle x1 lit", "minecraft:candle[candles=1,lit=true]", "minecraft:air", false},
		{"candle x3 dark", "minecraft:white_candle[candles=3,lit=false]", "minecraft:air", true},
		{"candle cake lit", "minecraft:candle_cake[lit=true]", "minecraft:air", false},
		{"enchanting table", "minecraft:enchanting_table", "minecraft:air", false},
		{"beacon", "minecraft:beacon", "minecraft:air", false},
		{"end gateway", "minecraft:end_gateway", "minecraft:air", false},
		{"end portal", "minecraft:end_portal", "minecraft:air", false},
		{"lava cauldron", "minecraft:lava_cauldron", "minecraft:air", false},
		{"sculk catalyst", "minecraft:sculk_catalyst", "minecraft:air", false},
		{"respawn anchor c4", "minecraft:respawn_anchor[charges=4]", "minecraft:air", false},
		{"respawn anchor c1", "minecraft:respawn_anchor[charges=1]", "minecraft:air", false},
		{"redstone ore lit", "minecraft:redstone_ore[lit=true]", "minecraft:air", false},
		{"redstone ore dark", "minecraft:redstone_ore[lit=false]", "minecraft:air", false},
		{"redstone torch dark", "minecraft:redstone_torch[lit=false]", "minecraft:air", true},
		{"daylight detector inverted", "minecraft:daylight_detector[inverted=true]", "minecraft:air", true},
		{"lightning rod powered", "minecraft:lightning_rod[powered=true]", "minecraft:air", true},
		{"light block l7", "minecraft:light[level=7]", "minecraft:air", false},
		{"light block default", "minecraft:light", "minecraft:air", false},
		{"amethyst cluster", "minecraft:amethyst_cluster", "minecraft:air", false},
		{"magma", "minecraft:magma_block", "minecraft:air", false},
	}
	wetProbes := []struct {
		name, spec, restore string
		wantEmpty           bool
	}{
		{"sea pickle x4 wet", "minecraft:sea_pickle[pickles=4,waterlogged=true]", "minecraft:water", false},
		{"sea pickle x2 wet", "minecraft:sea_pickle[pickles=2,waterlogged=true]", "minecraft:water", false},
	}
	want := [2]int{0, 0}
	type snapPair = struct {
		place, remove map[[2]int]lightSnapshot
	}
	var legs [2][]snapPair
	for i, addr := range []string{targets.vanilla, targets.mcgo} {
		leg := "vanilla"
		if i == 1 {
			leg = "mc-go"
		}
		c, pr, pw := joinPlayBlackbox(t, addr, "LE")
		drainToJoinBlackbox(t, "le", pr)
		sendCommand(t, pw, "tp @s 56.5 100 8.5")
		streamed := settleChunks(t, c, pr, pw, 4*time.Second)
		if !streamed[want] {
			t.Fatalf("%s (%s): probe chunk (0,0) was never streamed — window keys %v", addr, leg, windowKeys(streamed))
		}
		// Sealed probe cavity: floor at y=9 for surface-sitting
		// emitters (candles, pickles, torches) and ceiling at y=11 —
		// cave vines fail canSurvive without a block overhead and
		// self-destroy inside the tick, leaving marked masks over
		// zeroed arrays. The walls make interior sky 0 on both
		// servers: vanilla's propagated sky would still dim the
		// cavity by a nibble that mc-go's column model can't see.
		sendCommand(t, pw, "fill 6 9 6 10 9 10 minecraft:stone")
		sendCommand(t, pw, "fill 6 11 6 10 11 10 minecraft:stone")
		sendCommand(t, pw, "fill 6 10 6 6 10 10 minecraft:stone")
		sendCommand(t, pw, "fill 10 10 6 10 10 10 minecraft:stone")
		sendCommand(t, pw, "fill 7 10 6 9 10 6 minecraft:stone")
		sendCommand(t, pw, "fill 7 10 10 9 10 10 minecraft:stone")
		_ = drainLight(t, c, pr, pw, 2*time.Second)
		var snaps []snapPair
		run := func(name, spec, restore string, wantEmpty bool) {
			timeout := 15 * time.Second
			if wantEmpty {
				timeout = 5 * time.Second
			}
			sendCommand(t, pw, "setblock 8 10 8 "+spec)
			place := collectChunkLightFor(t, c, pr, pw, want, timeout)
			sendCommand(t, pw, "setblock 8 10 8 "+restore)
			remove := collectChunkLightFor(t, c, pr, pw, want, timeout)
			snaps = append(snaps, snapPair{place, remove})
		}
		for _, probe := range probes {
			run(probe.name, probe.spec, probe.restore, probe.wantEmpty)
		}
		// Contained pool for the submerged pickles: solid stone box
		// with a water interior so the cell stays a source — airborne
		// waterlogged drains and dies, open pools flow away.
		sendCommand(t, pw, "fill 6 9 6 10 11 10 minecraft:stone")
		sendCommand(t, pw, "fill 7 10 7 9 10 9 minecraft:water")
		_ = drainLight(t, c, pr, pw, 2*time.Second)
		for _, probe := range wetProbes {
			run(probe.name, probe.spec, probe.restore, probe.wantEmpty)
		}
		legs[i] = snaps
		c.Close()
	}
	van, got := legs[0], legs[1]
	allProbes := append(append([]struct {
		name, spec, restore string
		wantEmpty           bool
	}{}, probes...), wetProbes...)

	// Anti-vacuous: the vanilla leg must carry lit nibbles for the real
	// emitters (a flat-15 probe and a state-folded one).
	for i, snap := range van {
		switch allProbes[i].name {
		case "beacon", "cave vine berries", "sea pickle x4 wet":
			vsnap, ok := snap.place[want]
			if !ok {
				t.Fatalf("place %s: vanilla emitted nothing for probe chunk", allProbes[i].name)
			}
			if nonzeroArrays(vsnap.blockArrays) == 0 {
				t.Fatalf("vanilla place %s shipped no lit nibbles", allProbes[i].name)
			}
		}
	}

	for i, probe := range allProbes {
		if len(van[i].place) == 0 {
			// Dark, non-occluding probes: air→transparent writes no
			// light change, so vanilla emits nothing — mc-go must not
			// either.
			if len(got[i].place) != 0 {
				t.Fatalf("place %s: vanilla emitted nothing but mc-go emitted %d chunks", probe.name, len(got[i].place))
			}
		} else {
			for ck, vsnap := range van[i].place {
				msnap, ok := got[i].place[ck]
				if !ok {
					t.Fatalf("place %s %v: vanilla emitted, mc-go did not", probe.name, ck)
				}
				cmpLightSnap(t, "place "+probe.name, ck, vsnap, msnap)
			}
		}
		if len(van[i].remove) == 0 {
			if len(got[i].remove) != 0 {
				t.Fatalf("remove %s: vanilla emitted nothing but mc-go emitted %d chunks", probe.name, len(got[i].remove))
			}
		} else {
			for ck, vsnap := range van[i].remove {
				msnap, ok := got[i].remove[ck]
				if !ok {
					t.Fatalf("remove %s %v: vanilla emitted, mc-go did not", probe.name, ck)
				}
				cmpLightSnap(t, "remove "+probe.name, ck, vsnap, msnap)
			}
		}
	}
}

// TestSkyPropParity pins vanilla's horizontal sky propagation: a top
// write floods sky light around overhangs at cost max(1, opacity) per
// step, fluids count toward the heightmap, and a recomputed write can
// ship an update whose arrays changed nothing. Each probe writes into
// the west-border chunk (0,0) and compares every emitted update_light
// chunk-for-chunk, mask and nibble array identical, on both servers.
//
// Measured on 26.2 (javap on the extracted jar plus live wire probes):
// a single opaque block at y=40 shadows its column to 14 forever; the
// centre of a 9x9 platform drops to 10 (five steps to open sky); a
// lone water cell raises its top and reads 14 (15-opacity), glass and
// torches do not; a torch placed under the platform silently lowers
// its own cell 10 -> 9, and a torch in open air ships a mark-only
// update (sections pos+-1 vertically) whose arrays are all-15 either
// way.
func TestSkyPropParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("TP")}, nil)
	defer targets.cleanup()
	cv, prv, pwv := joinPlayBlackbox(t, targets.vanilla, "TP")
	defer cv.Close()
	cm, prm, pwm := joinPlayBlackbox(t, targets.mcgo, "TP")
	defer cm.Close()
	drainToJoinBlackbox(t, "tp", prv)
	drainToJoinBlackbox(t, "tp", prm)
	sendCommand(t, pwv, "tp @s 56.5 100 8.5")
	sendCommand(t, pwm, "tp @s 56.5 100 8.5")
	_ = settleChunks(t, cv, prv, pwv, 4*time.Second)
	_ = settleChunks(t, cm, prm, pwm, 4*time.Second)
	_ = drainLight(t, cv, prv, pwv, 2*time.Second)
	_ = drainLight(t, cm, prm, pwm, 2*time.Second)

	want := [2]int{0, 0}
	compare := func(what string) {
		t.Helper()
		van := collectChunkLightFor(t, cv, prv, pwv, want, 15*time.Second)
		for ck, s := range drainLight(t, cv, prv, pwv, 1200*time.Millisecond) {
			van[ck] = mergeLightSnap(van[ck], s)
		}
		got := collectChunkLightFor(t, cm, prm, pwm, want, 15*time.Second)
		for ck, s := range drainLight(t, cm, prm, pwm, 1200*time.Millisecond) {
			got[ck] = mergeLightSnap(got[ck], s)
		}
		keys := map[[2]int]bool{}
		for ck := range van {
			keys[ck] = true
		}
		for ck := range got {
			keys[ck] = true
		}
		for ck := range keys {
			// Vanilla sporadically ships a content-free update for a
			// chunk while its neighbours are still streaming in
			// (the border check includes pending chunks) — a packet
			// with every mask empty carries no data, so drop it from
			// both legs before comparing.
			if emptyLightSnap(van[ck]) && emptyLightSnap(got[ck]) {
				continue
			}
			cmpLightSnap(t, what, ck, van[ck], got[ck])
		}
	}

	probes := []struct{ name, cmd string }{
		{"single opaque", "setblock 8 40 8 minecraft:stone"},
		{"undo", "setblock 8 40 8 minecraft:air"},
		{"platform", "fill 4 40 4 12 40 12 minecraft:stone"},
		{"under-platform torch", "setblock 8 39 8 minecraft:torch"},
		{"under-platform torch undo", "setblock 8 39 8 minecraft:air"},
		{"undo platform", "fill 4 40 4 12 40 12 minecraft:air"},
		{"fluid top", "setblock 8 40 8 minecraft:water"},
		// The undo clears the chunk's top half: a falling column's
		// drain cadence and foot spread are fluid-sim timing (vanilla
		// ~17t/cell vs mc-go ~7t, different foot reach), so any removal
		// that leaves residual water compares differently-lit cells.
		// Clearing every cell makes the update deterministic.
		{"undo fluid", "fill 0 -60 0 15 40 15 minecraft:air"},
		{"transparent torch", "setblock 8 40 8 minecraft:torch"},
		{"undo transparent", "setblock 8 40 8 minecraft:air"},
		{"glass", "setblock 8 40 8 minecraft:glass"},
		{"undo glass", "setblock 8 40 8 minecraft:air"},
	}
	for _, p := range probes {
		sendCommand(t, pwv, p.cmd)
		sendCommand(t, pwm, p.cmd)
		compare(p.name)
	}
}
