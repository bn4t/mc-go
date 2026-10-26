package conformance

import (
	"encoding/binary"
	"fmt"
	"math"
)

// PlayConfig declares the independently known decoding bounds of the initial
// dimension. play-chunks requires a stationary fresh admission, an offline
// endpoint with server view distance at least two, and this explicit shape.
// The probe requests client view distance two; it needs no operator privileges.
type PlayConfig struct {
	Shape ChunkShape `json:"shape"`
}

// chunkWindow measures a complete initial view, rather than treating Join Game
// or the first chunk as readiness. Packet IDs/layouts are shared by the two
// independently measured Java profiles. Unrelated packets are retained in the
// transcript, without claiming validation of their gameplay semantics.
func chunkWindow(w *wire, c Config) error {
	// Configuration Client Information fixes the requested view before the
	// server admits the player or captures its initial chunk window.
	info := str(nil, "en_us")
	info = append(info, 2, 0, 1, 0x7f, 1, 0, 1, 0)
	var entity int32
	if err := configurePlayWithInfo(w, c, func(p packet) {
		if p.id == 0x31 {
			entity = int32(binary.BigEndian.Uint32(p.data[:4]))
		}
	}, info); err != nil {
		return err
	}
	columns := map[[2]int32]bool{}
	var center, own [2]int32
	centerSeen, positionSeen, attributes := false, false, false
	batchOpen, openingDone, loaded := false, false, false
	batchCount := 0
	for range 4096 {
		p, err := w.read()
		if err != nil {
			return fmt.Errorf("chunk window incomplete (%d/49 columns): %w", len(columns), err)
		}
		switch p.id {
		case 0x20: // disconnect
			return fmt.Errorf("play disconnected before complete chunk window")
		case 0x25, 0x52: // forget level chunk, respawn
			return fmt.Errorf("initial stationary fixture unloaded chunks or respawned")
		case 0x2c: // keepalive
			if len(p.data) != 8 {
				return fmt.Errorf("invalid play keepalive")
			}
			if err := w.send(0x1c, p.data); err != nil {
				return err
			}
		case 0x48: // synchronize position
			id, column, err := initialPosition(p.data)
			if err != nil {
				return fmt.Errorf("initial position: %w", err)
			}
			if positionSeen && own != column {
				return fmt.Errorf("initial fixture moved during chunk window")
			}
			own, positionSeen = column, true
			if centerSeen && own != center {
				return fmt.Errorf("position column %v differs from cache center %v", own, center)
			}
			if err := w.send(0, appendInt(nil, id)); err != nil {
				return err
			}
		case 0x5e: // set chunk cache center
			f := bodyFields(p.data)
			next := [2]int32{f.integer(), f.integer()}
			if err := f.complete(); err != nil {
				return fmt.Errorf("chunk cache center: %w", err)
			}
			if centerSeen && center != next {
				return fmt.Errorf("initial chunk cache center changed")
			}
			center, centerSeen = next, true
			if positionSeen && own != center {
				return fmt.Errorf("cache center %v differs from position column %v", center, own)
			}
		case 0x0c: // chunk batch start
			if len(p.data) != 0 || batchOpen {
				return fmt.Errorf("invalid or nested chunk batch start")
			}
			batchOpen, batchCount = true, 0
		case 0x2d: // level chunk with light
			if !batchOpen || !centerSeen || !positionSeen {
				return fmt.Errorf("chunk preceded batch start, position or cache center")
			}
			chunk, err := DecodeChunk(c.Protocol, p.data, c.Play.Shape)
			if err != nil {
				return fmt.Errorf("chunk body: %w", err)
			}
			key := [2]int32{chunk.X, chunk.Z}
			if dx, dz := int64(chunk.X)-int64(center[0]), int64(chunk.Z)-int64(center[1]); dx < -3 || dx > 3 || dz < -3 || dz > 3 {
				return fmt.Errorf("chunk %v outside requested initial window centered on %v", key, center)
			}
			if columns[key] {
				return fmt.Errorf("duplicate initial chunk %v", key)
			}
			columns[key] = true
			batchCount++
		case 0x0b: // chunk batch finished
			f := bodyFields(p.data)
			count := f.nonnegative()
			if err := f.complete(); err != nil {
				return fmt.Errorf("chunk batch finished: %w", err)
			}
			if !batchOpen || batchCount == 0 || int(count) != batchCount {
				return fmt.Errorf("chunk batch count %d differs from %d received, open=%v", count, batchCount, batchOpen)
			}
			batchOpen = false
			if !openingDone && !columns[own] {
				return fmt.Errorf("opening chunk batch omitted player's column %v", own)
			}
			openingDone = true
			if err := w.send(0x0b, binary.BigEndian.AppendUint32(nil, math.Float32bits(64))); err != nil {
				return err
			}
		case 0x83: // update attributes
			id, count, err := attributePacket(p.data)
			if err != nil {
				return fmt.Errorf("player attributes: %w", err)
			}
			if id == entity {
				if count == 0 {
					return fmt.Errorf("initial player attributes were empty")
				}
				if !openingDone {
					return fmt.Errorf("player attributes preceded opening chunk batch completion")
				}
				attributes = true
			}
		}
		if openingDone && positionSeen && attributes && !loaded {
			if err := w.send(0x2c, nil); err != nil { // player loaded
				return err
			}
			loaded = true
		}
		if len(columns) == 49 && !batchOpen && loaded {
			return nil
		}
	}
	return fmt.Errorf("chunk window exceeded 4096 packets without readiness")
}

func initialPosition(data []byte) (int32, [2]int32, error) {
	f := bodyFields(data)
	id := f.nonnegative()
	var values [6]float64
	for i := range values {
		values[i] = math.Float64frombits(binary.BigEndian.Uint64(padded(f.fixed(8), 8)))
		if math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
			f.fail("nonfinite position or delta")
		}
	}
	for range 2 {
		v := math.Float32frombits(binary.BigEndian.Uint32(padded(f.fixed(4), 4)))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			f.fail("nonfinite rotation")
		}
	}
	flags := binary.BigEndian.Uint32(padded(f.fixed(4), 4))
	if flags&7 != 0 || flags & ^uint32(511) != 0 {
		f.fail("initial position must have absolute coordinates and known flags")
	}
	if math.Abs(values[0]) > 30000000 || math.Abs(values[2]) > 30000000 {
		f.fail("initial position outside world bounds")
	}
	return id, [2]int32{int32(math.Floor(values[0] / 16)), int32(math.Floor(values[2] / 16))}, f.complete()
}

func attributePacket(data []byte) (int32, int32, error) {
	f := bodyFields(data)
	id := f.nonnegative()
	count := f.count()
	seen := map[int32]bool{}
	for i := int32(0); i < count && f.err == nil; i++ {
		attribute := f.nonnegative()
		if seen[attribute] {
			f.fail("duplicate attribute")
		}
		seen[attribute] = true
		f.fixed(8) // base value
		for j, count := int32(0), f.count(); j < count && f.err == nil; j++ {
			f.identifier() // modifier ID
			f.fixed(8)     // modifier amount
			if f.byte() > 2 {
				f.fail("invalid attribute modifier operation")
			}
		}
	}
	return id, count, f.complete()
}
