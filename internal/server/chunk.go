package server

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Block-state numeric ids, derived from the official block-state report
// via world's generated constants (see blocks_gen.go). StateAir and
// friends are aliases kept for the existing call sites and tests.
const (
	StateAir   = int32(world.Air)
	StateStone = int32(world.Stone)
	StateDirt  = int32(world.Dirt)
	StateGrass = int32(world.Grass)
)

// World vertical range for the flat generator.
const (
	MinY     = -64
	Height   = 384
	Sections = Height / 16 // 24
)

func blockState(b world.Block) int32 {
	mapping, ok := world.BlockMapping(ProtocolVersion)
	if !ok {
		panic("missing built-in Java block mapping")
	}
	wire, err := mapping.Encode(b)
	if err != nil {
		panic(err)
	}
	return wire
}

// DescribeBlockState resolves a global state id to its block name and
// property values. It exists for diagnostics and tests that print what
// the server is actually sending; the simulation itself works on ids.
func DescribeBlockState(state int32) (string, map[string]string, bool) {
	mapping, ok := world.BlockMapping(ProtocolVersion)
	if !ok {
		return "", nil, false
	}
	canonical, ok := mapping.FromWire(state)
	if !ok {
		return "", nil, false
	}
	return world.DescribeState(canonical)
}

// heightmapTypes are the heightmaps vanilla sends with each chunk, in
// vanilla's order: WORLD_SURFACE, MOTION_BLOCKING_NO_LEAVES,
// MOTION_BLOCKING.
var heightmapTypes = []int32{1, 5, 4}

// heightmapBits is ceil(log2(worldHeight+1)) for Height=384.
const heightmapBits = 9

// packPadded packs values into big-endian longs, least-significant
// entry first, with entries never spanning longs (1.21.5+ format).
func packPadded(vals []int32, bits int) []int64 {
	perLong := 64 / bits
	longs := (len(vals) + perLong - 1) / perLong
	out := make([]int64, longs)
	for i, v := range vals {
		li := i / perLong
		shift := uint(i%perLong) * uint(bits)
		out[li] |= int64(uint64(v) << shift)
	}
	return out
}

func writeLongs(b *protocol.Buffer, longs []int64) {
	for _, l := range longs {
		b.Long(l)
	}
}

// heightmapValues returns the packed heightmap entries for one column:
// for each (z,x) column (x fastest), the highest occupied Y offset
// from the dimension's minY, plus one. Zero means the whole column is
// empty.
func heightmapValues(states []world.Block, height int) []int32 {
	vals := make([]int32, 16*16)
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			top := int32(0)
			for y := height - 1; y >= 0; y-- {
				if states[(y*16+z)*16+x] != world.Air {
					top = int32(y) + 1
					break
				}
			}
			vals[z*16+x] = top
		}
	}
	return vals
}

// encodeChunk builds one Chunk Data and Update Light body for column
// (cx, cz) in the 776 format: chunk coords, prefixed heightmaps, a
// length-prefixed data blob of sections (block count, fluid count,
// block palette, biome palette), block entities, then inline light.
// The dimension supplies the world, vertical bounds, and biome.
func encodeChunk(d *dimension, cx, cz int, blockEntities []clientBlockEntity) []byte {
	mapping, ok := world.BlockMapping(ProtocolVersion)
	if !ok {
		panic("missing built-in Java block mapping")
	}
	body, err := encodeChunkSnapshot(captureChunkSnapshot(d, cx, cz, blockEntities, mapping))
	if err != nil {
		panic(err) // Built-in world states must exist in the built-in mapping.
	}
	return body
}

// encodePalettedContainer writes one paletted container: bits per
// entry (Unsigned Byte), palette, and a fixed-size padded data array.
// entries is 4096 for blocks, 64 for biomes; minIndirectBits is the
// smallest legal indirect BPE for that global palette.
func encodePalettedContainer(b *protocol.Buffer, vals []int32, entries, minIndirectBits int) {
	// Fast path: single-valued containers skip the index map. The
	// encoding matches the general path's single-palette output.
	single := len(vals) > 0
	for _, v := range vals[1:] {
		if v != vals[0] {
			single = false
			break
		}
	}
	if single {
		b.Byte(0)
		b.VarInt(vals[0])
		return
	}
	var palette []int32
	index := map[int32]int{}
	for _, v := range vals {
		if _, ok := index[v]; !ok {
			index[v] = len(palette)
			palette = append(palette, v)
		}
	}
	if len(palette) == 1 {
		b.Byte(0)
		b.VarInt(palette[0])
		return
	}
	bpe := minIndirectBits
	for (1 << bpe) < len(palette) {
		bpe++
	}
	if bpe > 8 {
		// Direct: global ids, padded, no palette. Vanilla uses 15 for
		// blocks and the registry-derived width for biomes.
		bits := 15
		if entries == 64 {
			bits = 7
		}
		b.Byte(byte(bits))
		writePackedLongs(b, vals, nil, bits)
		return
	}
	b.Byte(byte(bpe))
	b.VarInt(int32(len(palette)))
	for _, p := range palette {
		b.VarInt(p)
	}
	writePackedLongs(b, vals, index, bpe)
}

// writePackedLongs writes vals as fixed-size padded longs, using index
// to map to palette indices when non-nil.
func writePackedLongs(b *protocol.Buffer, vals []int32, index map[int32]int, bits int) {
	packed := make([]int32, len(vals))
	for i, v := range vals {
		if index != nil {
			packed[i] = int32(index[v])
		} else {
			packed[i] = v
		}
	}
	writeLongs(b, packPadded(packed, bits))
}

// lightSections is Sections plus the below/above edge arrays vanilla
// includes in light data.
const lightSections = Sections + 2

// encodeLightData builds the Light Data portion shared by chunk and
// update-light packets for the flat overworld: sky light dark through
// the four solid layers of the ground section and full above, block
// light empty everywhere.
func encodeLightData() []byte {
	var b protocol.Buffer
	// Sky mask bits 1,2 (ground section and the section above it).
	b.VarInt(1)
	b.Long(0x6)
	b.VarInt(0) // block light mask
	// Empty sky below the world; empty block light below and at ground.
	b.VarInt(1)
	b.Long(0x1)
	b.VarInt(1)
	b.Long(0x7)
	// Sky arrays: ground section (4 dark layers, 12 lit) then all-lit.
	b.VarInt(2)
	ground := make([]byte, 2048)
	for i := 512; i < 2048; i++ {
		ground[i] = 0xFF
	}
	writeByteArray(&b, ground)
	writeByteArray(&b, bytes.Repeat([]byte{0xFF}, 2048))
	b.VarInt(0) // block light arrays
	return b.Bytes()
}

// litBlockSections lists the sections of column (cx,cz) whose block
// light grid holds any lit cell — those ship arrays on chunk send
// the way vanilla ships its BlockLightSections, while the sections
// chunk generation lit (and emptied again) stay on the empty mask.
func litBlockSections(d *dimension, cx, cz int) []int {
	var out []int
	seen := map[int]bool{}
	for p := range d.blockLight {
		if p.X>>4 != cx || p.Z>>4 != cz {
			continue
		}
		sec := (p.Y - d.minY) / 16
		if sec < 0 || sec >= d.sections() || seen[sec] {
			continue
		}
		seen[sec] = true
		out = append(out, sec)
	}
	sort.Ints(out)
	return out
}

// encodeLightDataFor emits the chunk's generated light profile: the
// materialized sky and block layers straight from the sectioned model
// (a layer ships its array when it holds a non-zero cell, the empty
// mask when it is all-zero), plus vanilla's below-world empty-sky
// bit. Skylight-less dimensions (nether, end) mark every section
// empty so the client relies on ambient light.
func encodeLightDataFor(d *dimension, cx, cz int) []byte {
	// The sectioned light model is shared with session-goroutine
	// floods and the tick-end flush — serialize against them.
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	var blockMask uint64
	var blockArrays [][]byte
	for _, sec := range litBlockSections(d, cx, cz) {
		blockMask |= 1 << uint(sec+1)
		blockArrays = append(blockArrays, blockSectionArray(d, cx, cz, sec))
	}
	var b protocol.Buffer
	if d.skylight {
		ensureSkyChunk(d, cx, cz)
		ck := [2]int{cx, cz}
		var skyMask, emptySky uint64
		var skyArrays [][]byte
		for sec := 0; sec < d.sections(); sec++ {
			bit := uint64(1) << uint(sec+1)
			if skyLayerNonZero(d, cx, cz, sec) {
				skyMask |= bit
				skyArrays = append(skyArrays, skySectionArray(d, cx, cz, sec))
			} else if d.skyLayers[ck][sec] != nil {
				emptySky |= bit
			}
		}
		emptySky |= 0x1 // below the world
		if skyMask != 0 {
			b.VarInt(1)
			b.Long(int64(skyMask))
		} else {
			b.VarInt(0)
		}
		if blockMask != 0 {
			b.VarInt(1)
			b.Long(int64(blockMask))
		} else {
			b.VarInt(0)
		}
		b.VarInt(1)
		b.Long(int64(emptySky))
		emptyBlock := uint64(0)
		for sec := 0; sec < d.sections(); sec++ {
			bit := uint64(1) << uint(sec+1)
			if blockMask&bit != 0 {
				continue
			}
			if d.blkLayered[ck][sec] {
				emptyBlock |= bit
			}
		}
		b.VarInt(1)
		b.Long(int64(emptyBlock))
		b.VarInt(int32(len(skyArrays)))
		for _, arr := range skyArrays {
			writeByteArray(&b, arr)
		}
	} else {
		// sections+2 light slots, all flagged empty; no arrays follow.
		bits := d.sections() + 2
		b.VarInt(0) // sky light mask: no arrays
		if blockMask != 0 {
			b.VarInt(1)
			b.Long(int64(blockMask))
		} else {
			b.VarInt(0)
		}
		b.VarInt(1) // empty sky light mask
		b.Long(int64(uint64(1)<<uint(bits) - 1))
		b.VarInt(1) // empty block light mask
		b.Long(int64((uint64(1)<<uint(bits) - 1) &^ blockMask))
		b.VarInt(0) // sky arrays
	}
	b.VarInt(int32(len(blockArrays)))
	for _, arr := range blockArrays {
		writeByteArray(&b, arr)
	}
	return b.Bytes()
}

func writeByteArray(b *protocol.Buffer, data []byte) {
	b.VarInt(int32(len(data)))
	b.Raw(data)
}

// encodeLight builds one Update Light body for column (cx, cz).
func encodeLight(cx, cz int) []byte {
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	b.Raw(encodeLightData())
	return b.Bytes()
}

// encodeLightFor builds one Update Light body for a dimension column.
func encodeLightFor(d *dimension, cx, cz int) []byte {
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	b.Raw(encodeLightDataFor(d, cx, cz))
	return b.Bytes()
}

// pruneBorder is how many columns past the view window the server
// keeps before forgetting them, avoiding churn on small movements.
const pruneBorder = 2

// minViewDistance is the smallest client view distance the real server
// works with, and the one it falls back to for a client that never
// announced one.
const minViewDistance = 2

// windowCorner is how far past the player's view distance the circular
// part of the window reaches, in columns.
//
// The window the real server streams is not a square. Measured on 26.2
// by joining with one view distance after another and listing the
// columns streamed: it is the square of radius viewDistance+1 with the
// corners beyond a circle of radius viewDistance+windowCorner cut off.
// The readings are
//
//	view distance 2  ->  49 columns: the whole 7x7 square, corners
//	                     (3,3) included (squared distance 18)
//	view distance 3  ->  81 columns: the whole 9x9 square, corners (4,4)
//	                     included (32)
//	view distance 4  -> 117 columns: the 11x11 square less its four
//	                     corners, so (5,4) at 41 is in and (5,5) at 50
//	                     is out
//	view distance 6  -> 213 columns: rows dz=-7..7 as 11,13,15,15,15,
//	                     15,15,15,15,15,15,13,11 columns wide, so
//	                     (5,7) at 74 is in and (6,7) at 85 is out
//	view distance 8  -> 329 columns: rows 11,15,17,17,19...19,17,17,15,
//	                     11, so (8,7) at 113 is in and (9,6) at 117 is
//	                     out. An integer radius cannot produce this
//	                     window: radius 10 gives 317 columns.
//
// Every reading agrees, and they leave the constant between 2.657 (from
// the 9x9 square being complete) and 2.817 (from (9,6) being out at
// view distance 8). 2.75 is the value used; the probe that fits the
// constant over the view distances 2..32 is what says whether a
// different one is needed.
const windowCorner = 2.75

// chunkInWindow reports whether the column (dx, dz) away from the
// player's own column is inside the streamed window of a player whose
// view distance is viewDistance.
func chunkInWindow(viewDistance, dx, dz int) bool {
	limit := viewDistance + 1
	if dx > limit || dx < -limit || dz > limit || dz < -limit {
		return false
	}
	reach := float64(viewDistance) + windowCorner
	return float64(dx*dx+dz*dz) <= reach*reach
}

// The Set Render Distance (chunk cache radius) packet is not sent: the
// real server has it in its packet table but never sends it, measured on
// 26.2 by joining with a scripted client and listing every play packet
// it was sent - with no Client Information at all, after announcing a
// view distance of 8, and after asking for 2, the packet never appeared,
// while Set Center Chunk (0x5E) did. A client sizes its own chunk cache
// from the view distance it announced.

// encodeForget builds one Forget Level Chunk body: a single Long with
// x in the low 32 bits and z in the high 32 bits.
func encodeForget(cx, cz int) []byte {
	var b protocol.Buffer
	b.Long(int64(uint64(uint32(int32(cx))) | uint64(uint32(int32(cz)))<<32))
	return b.Bytes()
}

// initialChunkBatch is how many columns the real server streams before
// the client has acknowledged anything.
//
// Measured on 26.2 with clients that never answer a chunk batch: the
// first batch is nine columns whatever the view distance (2, 3 and 8 all
// read 9), and no further column ever arrives - not when the client
// asks for a wider window, and not when it teleports three hundred
// blocks into country it has never seen.
const initialChunkBatch = 9

// maxChunkBatch caps the chunks-per-batch rate a client can ask for.
// Measured on 26.2: a request for 1000 chunks a tick is answered with
// batches of 64.
const maxChunkBatch = 64

// burstBatches is how many batches one acknowledgement buys. Measured on
// 26.2: a client that acknowledges once and then goes quiet receives
// exactly ten batches at the rate it asked for - ten chunks after asking
// for one a tick, twenty after two, thirty after three, ten after a
// quarter - and then nothing at all, however long it waits. A client that
// acknowledges every batch never runs the burst out, because each
// acknowledgement buys ten more.
const burstBatches = 10

// zeroRateInterval is the tick spacing the real server uses after an
// acknowledgement that asks for a rate of zero. Measured on 26.2: one
// chunk at the acknowledgement and then one every 5050 ms, which is 101
// ticks - what a rate of one per `1/rate` ticks comes to when the
// division by zero is clamped rather than special-cased.
const zeroRateInterval = 101

// chunkRate turns a requested chunks-per-tick rate into the whole number
// of chunks a batch holds: truncated, and clamped to at least one chunk
// and at most maxChunkBatch. Measured on 26.2: 25 gives batches of 25,
// 2.5 gives 2, 0.1 gives 1, 1000 gives 64. A NaN reads as zero, which is
// what the real server's cast produces, and so becomes 1.
func chunkRate(rate float32) int {
	switch {
	case rate != rate:
		return 1
	case rate > maxChunkBatch:
		return maxChunkBatch
	case rate >= 1:
		return int(rate)
	default:
		return 1
	}
}

// chunkInterval turns the same requested rate into the number of ticks
// the real server leaves between the batches of one burst: one tick for
// any rate of one chunk a tick or more, and otherwise the ticks it takes
// to be owed a whole chunk - `ceil(1/rate)`, measured on 26.2 as 2 ticks
// for 0.5 and 0.9, 4 for 0.25 and 10 for 0.1.
func chunkInterval(rate float32) int {
	switch {
	case rate != rate, rate <= 0:
		return zeroRateInterval
	case rate >= 1:
		return 1
	default:
		n := 1
		for float32(n)*rate < 1 {
			n++
		}
		return n
	}
}

// pumpChunks sends one chunk batch if the pacing allows it: the opening
// batch of initialChunkBatch columns while the client has not
// acknowledged a batch yet, and afterwards one of the ten batches the
// last acknowledgement bought, at the size and spacing that
// acknowledgement asked for.
//
// All of it is measured on 26.2. A client that never acknowledges gets
// the nine-column opening batch and then nothing, for as long as it
// waits. An acknowledgement arrives with a rate and buys ten batches:
// `ack(25)` released the remaining 81 columns of a view distance of 3 as
// 25, 25, 25, 6, and `ack(1)` released exactly ten columns as ten batches
// of one a tick before falling silent. An acknowledgement that reaches
// the server before the opening batch has gone out replaces it - the
// first batch is at the acknowledged rate rather than nine columns - and
// the batch it buys goes out at once rather than on the next tick, which
// is where the real server sends the opening batch too: about twelve
// milliseconds after the join burst.
func (s *Server) pumpChunks(sess *Session) error {
	sess.chunkPumpMu.Lock()
	defer sess.chunkPumpMu.Unlock()
	sess.sentMu.Lock()
	size := 0
	switch {
	case sess.rate > 0:
		if sess.burst > 0 && sess.cool <= 0 {
			size = sess.rate
		}
	case !sess.opened:
		size = initialChunkBatch
	}
	if size > len(sess.pending) {
		size = len(sess.pending)
	}
	initialJoin := !sess.opened && !sess.spawned.Load()
	batch := append([][2]int(nil), sess.pending[:size]...)
	sess.sentMu.Unlock()
	if len(batch) == 0 {
		return nil
	}

	d := sess.dimension()
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	mapping := profile.blocks
	if mapping == nil {
		return fmt.Errorf("no block mapping for Java protocol %d", sess.ver.Protocol)
	}
	var reservedBytes int64
	async := sess.pw != nil
	var reservation *chunkEncodingReservation
	var ok bool
	if async {
		// Hard configuration limits may lower throughput, but temporary pool
		// pressure must not consume pacing credit or omit any pending column.
		fitting := 0
		for i, key := range batch {
			// Preflight only one column's metadata at a time. Retaining all
			// sign NBT here would bypass the global snapshot reservation.
			metadata := s.chunkClientBlockEntities(d, key[0], key[1])
			next := reservedBytes + chunkSnapshotUpperBound(d, metadata)
			if !s.chunkEncodingCanFit(i+1, next) {
				break
			}
			fitting, reservedBytes = i+1, next
		}
		if fitting == 0 {
			sess.closeQueue()
			return fmt.Errorf("one chunk snapshot exceeds encoder resource limits")
		}
		batch = batch[:fitting]
		reservation, ok = s.tryReserveChunkEncoding(fitting, reservedBytes)
		if !ok {
			if !initialJoin {
				return nil
			}
			// Preserve the initial join's packet order under saturation. This
			// bounded fallback holds only one detached snapshot at a time and
			// never waits for another session's worker or snapshot capacity.
			async = false
		} else {
			defer reservation.Close()
		}
	}

	// The reservation precedes this commit: later owner mutations see these
	// columns as delivered and queue deltas behind their immutable futures.
	sess.sentMu.Lock()
	sess.pending = sess.pending[len(batch):]
	if sess.sent == nil {
		sess.sent = make(map[[2]int]bool)
	}
	for _, key := range batch {
		sess.sent[key] = true
	}
	sess.opened = true
	if sess.rate > 0 && sess.burst > 0 {
		sess.burst--
		sess.cool = sess.interval
	}
	sess.sentMu.Unlock()
	if err := sess.write(sess.ids().PlayChunkBatchStart, nil); err != nil {
		return err
	}
	for _, key := range batch {
		metadata := s.chunkClientBlockEntities(d, key[0], key[1])
		snapshot := captureChunkSnapshot(d, key[0], key[1], metadata, mapping)
		if async {
			future := reservation.Submit(snapshot.retainedBytes(), func() ([]byte, error) { return encodeChunkSnapshot(snapshot) })
			if err := sess.writeChunkFuture(sess.ids().PlayChunkData, future); err != nil {
				return err
			}
		} else {
			body, err := encodeChunkSnapshot(snapshot)
			if err != nil {
				sess.closeQueue()
				return err
			}
			if err := sess.write(sess.ids().PlayChunkData, body); err != nil {
				return err
			}
		}
	}
	var done protocol.Buffer
	done.VarInt(int32(len(batch)))
	if err := sess.write(sess.ids().PlayChunkBatchDone, done.Bytes()); err != nil {
		return err
	}
	s.evalChunkPairs(sess, batch)
	return nil
}

// evalChunkPairs re-runs the pairing eval for every entity standing in
// freshly sent columns — applyChunkTrackingView's tracked-chunk branch
// (a static entity whose chunk was still queued at spawn time pairs as
// soon as its column actually goes out).
func (s *Server) evalChunkPairs(sess *Session, keys [][2]int) {
	if len(keys) == 0 {
		return
	}
	set := make(map[[2]int]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		ec := e
		s.sessions.Range(func(_, v any) bool {
			os := v.(*Session)
			if os.entityID.Load() == e.ID {
				x, y, z, _, _, _ := os.Pos()
				ec = &Entity{}
				*ec = *e
				ec.X, ec.Y, ec.Z = x, y, z
			}
			return true
		})
		k := [2]int{int(math.Floor(ec.X / 16)), int(math.Floor(ec.Z / 16))}
		if set[k] {
			s.evalPair(sess, ec)
		}
	}
}

// chunkTickStep streams at most one batch per tick to every player whose
// window still has columns waiting. This is the pacing the real server
// uses, so a client that never acknowledges its chunk batches is left
// with the opening batch no matter how far it travels, and one that
// acknowledges once and goes quiet gets that acknowledgement's ten
// batches and no more.
func (s *Server) chunkTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		sess.sentMu.Lock()
		if sess.cool > 0 {
			sess.cool--
		}
		sess.sentMu.Unlock()
		_ = s.pumpChunks(sess)
		return true
	})
}

// sendWindow takes on every column in the effective view window around
// (ccx, ccz) that the session is not already tracking, and forgets the
// ones past the prune border: queued but unstreamed columns are simply
// dropped, while streamed ones are told to the client via Forget Level
// Chunk. The new columns are queued nearest first (see queueOrder);
// forgets are sorted ascending (x, z) so tests and captures are stable.
//
// What goes out on the wire is up to pumpChunks: the opening batch, or
// whatever the client's last acknowledgement allows.
func (s *Server) sendWindow(sess *Session, ccx, ccz int) error {
	radius := sess.viewRadius(s.ViewDistance)
	var fresh [][2]int
	sess.sentMu.Lock()
	if sess.queued == nil {
		sess.queued = make(map[[2]int]bool)
	}
	for dx := -radius - 1; dx <= radius+1; dx++ {
		for dz := -radius - 1; dz <= radius+1; dz++ {
			if !chunkInWindow(radius, dx, dz) {
				continue
			}
			cx, cz := ccx+dx, ccz+dz
			key := [2]int{cx, cz}
			if sess.queued[key] {
				continue
			}
			sess.queued[key] = true
			fresh = append(fresh, key)
		}
	}
	queueOrder(fresh, ccx, ccz)
	sess.pending = append(sess.pending, fresh...)
	sess.sentMu.Unlock()
	// Warm the random-tick section flags for the fresh columns off the
	// tick path — this call site is a packet-handler thread, and each
	// section's flag build is a 4096-block read vanilla does once when
	// its LevelChunkSection materializes. Doing it here keeps GameTick
	// from paying ~10k scans in the one tick after a join or teleport.
	if d := sess.dimension(); d != nil {
		for _, k := range fresh {
			s.warmSectionFlags(d, k[0], k[1])
		}
		// Vanilla light-initializes every streamed chunk up front
		// (the INITIALIZE_LIGHT pipeline), so its initializeSection
		// marks all land while the column is still streaming. Doing
		// the same eagerly keeps light floods from lazily generating
		// a column mid-tick and emitting its init marks long after
		// they should have gone out. The margin covers the writes a
		// flood can reach plus the extra chunk an init mark crosses.
		if d.skylight {
			d.lightMu.Lock()
			for _, k := range fresh {
				for dx := -2; dx <= 2; dx++ {
					for dz := -2; dz <= 2; dz++ {
						ensureSkyChunk(d, k[0]+dx, k[1]+dz)
					}
				}
			}
			d.lightMu.Unlock()
		}
	}
	// The batch that goes out now precedes the forgets for columns that
	// dropped out of the window, as it did before the throttle: the
	// queue is filled first so the pump can pick the new columns up.
	if err := s.pumpChunks(sess); err != nil {
		return err
	}

	prune := radius + pruneBorder
	var forgot [][2]int
	sess.sentMu.Lock()
	for k := range sess.queued {
		dx, dz := k[0]-ccx, k[1]-ccz
		if chunkInWindow(prune, dx, dz) {
			continue
		}
		delete(sess.queued, k)
		if sess.sent[k] {
			delete(sess.sent, k)
			forgot = append(forgot, k)
		}
	}
	sess.pending = pruneQueue(sess.pending, sess.queued)
	sess.sentMu.Unlock()
	sortPairs(forgot)
	for _, k := range forgot {
		s.unpairChunk(sess, k[0], k[1])
	}
	for _, k := range forgot {
		if err := sess.write(sess.ids().PlayForgetChunk, encodeForget(k[0], k[1])); err != nil {
			return err
		}
	}
	return nil
}

// unpairChunk drops pairings for entities standing in a column that
// fell out of the session's tracking view — the drop branch of
// applyChunkTrackingView.
func (s *Server) unpairChunk(sess *Session, cx, cz int) {
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		if e.Dim != sess.dimName() {
			continue
		}
		ex, ez := e.X, e.Z
		s.sessions.Range(func(_, v any) bool {
			os := v.(*Session)
			if os.entityID.Load() == e.ID {
				ex, _, ez, _, _, _ = os.Pos()
			}
			return true
		})
		if int(math.Floor(ex/16)) == cx && int(math.Floor(ez/16)) == cz {
			s.unpairEnt(sess, e.ID)
		}
	}
}

// queueOrder sorts one window's new columns nearest first, so the batch
// a joining client is sent before it has acknowledged anything holds the
// ground it is standing on rather than a corner of its view.
//
// The real server's own order is not a function of the world. Measured on
// 26.2 at view distance 3, with a client that keeps acknowledging, its
// eighty-one columns arrived as [0 -1] [1 -1] [-1 -1] [0 0] [0 -2] [1 0]
// [-1 -2] [-1 0] [1 -2] [2 -1] [0 1] [0 -3] [-2 -1] ... - the opening
// nine are all within two columns of the player, but nothing outside the
// server reproduces the sequence, because it is the order its own
// generation happened to finish in.
//
// What the order has to have is the property those nine show: the
// columns closest to the player go first. mc-go orders by distance
// squared with (dx, dz) breaking ties, which makes the opening batch the
// nine columns the player can be standing in - the 3x3 around them - and
// every batch after it the ring outside the last. A client that is only
// ever sent the opening batch, which is what a client that never
// acknowledges is, can then render where it is.
func queueOrder(keys [][2]int, ccx, ccz int) {
	sort.Slice(keys, func(i, j int) bool {
		dxa, dza := keys[i][0]-ccx, keys[i][1]-ccz
		dxb, dzb := keys[j][0]-ccx, keys[j][1]-ccz
		if da, db := dxa*dxa+dza*dza, dxb*dxb+dzb*dzb; da != db {
			return da < db
		}
		if dxa != dxb {
			return dxa < dxb
		}
		return dza < dzb
	})
}

// pruneQueue drops the columns a forget pass took out of the stream from
// the pending queue, keeping the queue's order.
func pruneQueue(pending [][2]int, queued map[[2]int]bool) [][2]int {
	kept := pending[:0]
	for _, k := range pending {
		if queued[k] {
			kept = append(kept, k)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// resetChunks drops every column the session is tracking, for a
// dimension change or a respawn where the whole window is built again.
// The client's acknowledged rate survives, as it does in the real server
// where the acknowledgement belongs to the connection rather than the
// dimension, so streaming carries on at the same pace in the new world.
func (sess *Session) resetChunks() {
	sess.sentMu.Lock()
	sess.queued = nil
	sess.sent = nil
	sess.pending = nil
	sess.sentMu.Unlock()
}

// sortPairs orders chunk keys ascending by x then z.
func sortPairs(p [][2]int) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && (p[j][0] < p[j-1][0] ||
			(p[j][0] == p[j-1][0] && p[j][1] < p[j-1][1])); j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

// sendCenterChunk tells the client its new loaded-chunk center, once per
// column: repeats for the same center are dropped. Vanilla uses this to
// evict stale chunks; without it far columns may never render.
func (s *Server) sendCenterChunk(sess *Session, cx, cz int) {
	sess.sentMu.Lock()
	same := sess.centerSet && sess.centerX == cx && sess.centerZ == cz
	sess.centerSet, sess.centerX, sess.centerZ = true, cx, cz
	sess.sentMu.Unlock()
	if same {
		return
	}
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	_ = sess.write(sess.ids().PlaySetCenterChunk, b.Bytes())
}
