package blackbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/protocol"
)

// Completed preparation units may publish valid initialization marks to an
// existing viewer before the new player joins. Their timing and batching are
// intentionally unconstrained; the existing viewer's materialized final light
// must agree with a fresh snapshot. The opaque-block samples reuse the official
// observations pinned by TestSkyPropParity and TestColumnPreparationColdWarmWire.
func TestJoinPreparationOverlappingViewerLight(t *testing.T) {
	raw, err := os.ReadFile("../../server/testdata/external-world/facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var measured struct{ Profiles map[string]coldJoinFacts }
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	// This is the committed official datagen report, not production IDs.
	raw, err = os.ReadFile("../../internal/server/testdata/packets-776.json")
	if err != nil {
		t.Fatal(err)
	}
	var packets map[string]map[string]map[string]struct {
		ID int32 `json:"protocol_id"`
	}
	if err := json.Unmarshal(raw, &packets); err != nil {
		t.Fatal(err)
	}
	lightID, ok := packets["play"]["clientbound"]["minecraft:light_update"]
	if !ok {
		t.Fatal("official light update packet fact missing")
	}
	addr := requireMcGoOnly(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	open := func(number int32, name string) *conformance.PlayClient {
		t.Helper()
		c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: number, Username: name, Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	f := measured.Profiles["776"]
	observer := open(776, "OverlapObserver")
	// Chunk (3,3) is the observer's loaded corner, inside the next join's
	// window. The new center (3,3) extends preparation beyond the old halo.
	baseline := overlapJoinWindow(t, ctx, observer, f, 0)
	view := parseChunkView(t, "existing border", baseline)
	state, updates, targetUpdates := int32(-1), 0, 0
	apply := func(p conformance.Packet) error {
		if p.ID == lightID.ID {
			cx, cz, snap := decodeLightUpdate(t, &protocol.Packet{ID: p.ID, Data: p.Data})
			updates++
			for kind, mask := range [][]uint64{snap.skyMask, snap.blockMask} {
				empty, arrays := snap.emptySky, snap.skyArrays
				if kind == 1 {
					empty, arrays = snap.emptyBlock, snap.blockArrays
				}
				if len(mask) > 1 || len(empty) > 1 {
					t.Fatal("light mask exceeds 26 dimension sections")
				}
				if len(mask) == 1 && mask[0]>>26 != 0 || len(empty) == 1 && empty[0]>>26 != 0 {
					t.Fatal("light mask has out-of-dimension bits")
				}
				array := 0
				for bit := 0; bit < 26; bit++ {
					populated, zero := bitSet(mask, bit), bitSet(empty, bit)
					if populated && zero {
						t.Fatalf("light section %d both populated and empty", bit)
					}
					var value []byte
					if populated {
						if array >= len(arrays) {
							t.Fatal("light mask names missing array")
						}
						value = arrays[array]
						array++
					} else if zero {
						value = make([]byte, 2048)
					}
					if cx == 3 && cz == 3 && bit >= 1 && bit <= 24 && value != nil {
						if kind == 0 {
							view.sky[bit-1] = value
						} else {
							view.block[bit-1] = value
						}
					}
				}
				if array != len(arrays) {
					t.Fatal("light packet carries arrays absent from mask")
				}
			}
			if cx == 3 && cz == 3 {
				targetUpdates++
			}
		}
		if p.ID == f.Packets["minecraft:level_chunk_with_light"] {
			chunk, err := conformance.DecodeChunk(776, p.Data, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes})
			if err != nil {
				return err
			}
			if chunk.X == 3 && chunk.Z == 3 {
				view = parseChunkView(t, "resent border", p.Data)
				state = chunk.Blocks[(40+64)*256+8*16+8]
			}
		}
		if p.ID == f.Packets["minecraft:block_update"] {
			r := bytes.NewReader(p.Data)
			var position uint64
			if err := binary.Read(r, binary.BigEndian, &position); err != nil {
				return err
			}
			value, err := mixedReadVI(r)
			if err != nil || r.Len() != 0 {
				return fmt.Errorf("single block update framing: %v", err)
			}
			if position == mixedPos(56, 40, 56) {
				state = value
			}
		}
		return nil
	}
	fence := func(token string) {
		t.Helper()
		for _, command := range []string{"tick freeze", "tick step 1", "tick unfreeze"} {
			if err := observer.Command(command); err != nil {
				t.Fatal(err)
			}
		}
		if err := coldJoinToken(ctx, observer, token, apply); err != nil {
			t.Fatal(err)
		}
	}
	mixedBarrier(t, observer, "setworldspawn 56 71 56")
	type opened struct {
		client *conformance.PlayClient
		err    error
	}
	joining := make(chan opened, 1)
	go func() {
		c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 775, Username: "OverlapJoin", Timeout: 90 * time.Second})
		joining <- opened{c, err}
	}()
	// Request the edit concurrently with the join. Its exact placement among
	// preparation units is unconstrained here; gated unit tests pin overlap.
	if err := observer.Command("setblock 56 40 56 minecraft:stone"); err != nil {
		t.Fatal(err)
	}
	fence("overlap-edit-fence")
	var joined opened
	waiting := true
	for sequence := 0; waiting; sequence++ {
		select {
		case joined = <-joining:
			waiting = false
		default:
			if err := coldJoinToken(ctx, observer, fmt.Sprintf("overlap-progress-%d", sequence), apply); err != nil {
				t.Fatal(err)
			}
		}
	}
	if joined.err != nil {
		t.Fatal(joined.err)
	}
	defer joined.client.Close()
	overlapJoinWindow(t, ctx, joined.client, measured.Profiles["775"], 3)
	fence("overlap-join-fence")
	if targetUpdates == 0 || state != f.States["minecraft:stone"] {
		t.Fatalf("existing viewer target light updates=%d, block=%d, want measured stone=%d", targetUpdates, state, f.States["minecraft:stone"])
	}
	// Compare all 24 materialized light sections against a fresh same-profile
	// chunk. The two-column halo may suppress extra init packets entirely.
	fresh := open(776, "OverlapFresh")
	body := overlapJoinWindow(t, ctx, fresh, f, 3)
	want := parseChunkView(t, "fresh border", body)
	var dark [2048]byte
	for section := 0; section < 24; section++ {
		gotBlock, wantBlock := view.block[section], want.block[section]
		if gotBlock == nil {
			gotBlock = dark[:]
		}
		if wantBlock == nil {
			wantBlock = dark[:]
		}
		if !bytes.Equal(view.sky[section], want.sky[section]) || !bytes.Equal(gotBlock, wantBlock) {
			t.Fatalf("existing viewer final light differs from fresh chunk in section %d", section)
		}
	}
	coldJoinSky(t, body)
	t.Logf("existing 776 viewer consumed %d well-formed light updates (%d for loaded corner) during overlapping 775 join", updates, targetUpdates)
}

func overlapJoinWindow(t *testing.T, ctx context.Context, c *conformance.PlayClient, facts coldJoinFacts, center int32) []byte {
	t.Helper()
	initial := c.InitialPackets()
	columns := map[[2]int32]bool{}
	var target []byte
	for {
		var p conformance.Packet
		if len(initial) != 0 {
			p, initial = initial[0], initial[1:]
		} else {
			var err error
			p, err = c.ReadPacket(ctx)
			if err != nil {
				t.Fatalf("overlap window: %d/49 columns: %v", len(columns), err)
			}
		}
		if p.ID == facts.Packets["minecraft:level_chunk_with_light"] {
			chunk, err := conformance.DecodeChunk(c.Protocol(), p.Data, conformance.ChunkShape{Sections: 24, BlockStates: facts.BlockStates, Biomes: facts.Biomes})
			if err != nil {
				t.Fatal(err)
			}
			key := [2]int32{chunk.X, chunk.Z}
			if columns[key] || chunk.X < center-3 || chunk.X > center+3 || chunk.Z < center-3 || chunk.Z > center+3 {
				t.Fatalf("overlap unexpected/duplicate column %v", key)
			}
			columns[key] = true
			if key == [2]int32{3, 3} {
				target = p.Data
			}
		}
		if p.ID == 0x0b && len(columns) == 49 {
			if target == nil {
				t.Fatal("overlap target chunk missing")
			}
			return target
		}
	}
}
