package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/server"
)

// TestColumnPreparationColdWarmWire exercises the owner-side preparation path
// through actual acknowledged 775/776 streams. Numeric bounds and the stone ID
// come from the pinned official reports in mixed-profile-facts.json. The sky
// samples are the single-opaque-block observation pinned by TestSkyPropParity
// and TestBlockChangeSendsLightUpdate. No new vanilla observation is implied.
func TestColumnPreparationColdWarmWire(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts struct{ Profiles map[string]mixedFacts }
	if err := json.Unmarshal(raw, &facts); err != nil {
		t.Fatal(err)
	}
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
				s.ViewDistance, s.SimulationDistance = 2, 2
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			c, err := conformance.OpenPlay(ctx, conformance.Config{
				Endpoint: addr, Protocol: number, Username: "ColumnProbe", Timeout: 90 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			f := facts.Profiles[fmt.Sprint(number)]
			shape := conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}
			baseline := columnPreparationWindow(t, ctx, c, shape, "cold join", 0, c.InitialPackets(), false)
			far := func() {
				t.Helper()
				body := columnPreparationWindow(t, ctx, c, shape, "far window", 20, nil, true)
				columnPreparationEqual(t, "far window", baseline, body)
			}
			back := func(stage string) []byte {
				t.Helper()
				return columnPreparationWindow(t, ctx, c, shape, stage, 0, nil, true)
			}
			far()
			columnPreparationEqual(t, "warm return", baseline, back("warm return"))

			// One block changes an otherwise empty section into an occupied
			// section, then removal returns it to empty. Re-entry must observe
			// both transitions after all older batches have completed.
			mixedBarrier(t, c, "setblock 8 40 8 minecraft:stone")
			far()
			editedBody := back("edited return")
			edited, err := conformance.DecodeChunk(number, editedBody, shape)
			if err != nil {
				t.Fatal(err)
			}
			if got := edited.Blocks[(40+64)*256+8*16+8]; got != f.Blocks["stone"] {
				t.Fatalf("edited stone wire ID %d, want measured %d", got, f.Blocks["stone"])
			}
			before := parseChunkView(t, "baseline", baseline)
			after := parseChunkView(t, "edited", editedBody)
			if len(before.hms) == 0 || !reflect.DeepEqual(heightmapKeys(before.hms), heightmapKeys(after.hms)) {
				t.Fatal("heightmap types changed or baseline has none")
			}
			for typ, values := range before.hms {
				changed := after.hms[typ]
				if len(values) != 37 || len(changed) != 37 {
					t.Fatalf("heightmap %d long counts %d/%d, want 37 for 256 padded 9-bit entries", typ, len(values), len(changed))
				}
				for cell := 0; cell < 256; cell++ {
					// The measured overworld has 384 vertical cells: padded
					// heightmaps use seven 9-bit entries per long.
					old := uint64(values[cell/7]) >> uint(cell%7*9) & 511
					now := uint64(changed[cell/7]) >> uint(cell%7*9) & 511
					if cell == 8*16+8 {
						if now <= old {
							t.Fatalf("heightmap %d edited column did not rise: %d -> %d", typ, old, now)
						}
					} else if now != old {
						t.Fatalf("heightmap %d unrelated column %d changed: %d -> %d", typ, cell, old, now)
					}
				}
			}
			for _, sample := range []struct {
				x, y, z int
				want    byte
			}{
				{8, 39, 8, 14}, {8, 40, 8, 0}, {8, 41, 8, 15}, {9, 39, 8, 15},
			} {
				index := (sample.y&15)*256 + sample.z*16 + sample.x
				got := after.sky[(sample.y+64)/16][index/2] >> uint(index%2*4) & 15
				if got != sample.want {
					t.Fatalf("edited sky (%d,%d,%d)=%d, want measured %d", sample.x, sample.y, sample.z, got, sample.want)
				}
			}
			mixedBarrier(t, c, "setblock 8 40 8 minecraft:air")
			far()
			columnPreparationEqual(t, "removed return", baseline, back("removed return"))
		})
	}
}

// columnPreparationWindow waits for the complete measured 7x7 window and its
// final batch-finished packet. PlayClient acknowledges teleport, batch and
// keepalive controls; a matching position packet separates successive windows.
func columnPreparationWindow(t *testing.T, ctx context.Context, c *conformance.PlayClient, shape conformance.ChunkShape, stage string, center int32, initial []conformance.Packet, teleport bool) []byte {
	t.Helper()
	x := float64(center*16) + 8.5
	if teleport {
		if err := c.Command(fmt.Sprintf("tp @s %.1f 71 %.1f", x, x)); err != nil {
			t.Fatal(err)
		}
	}
	ready, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	columns := map[[2]int32]bool{}
	synced := !teleport
	var sample []byte
	for {
		var p conformance.Packet
		if len(initial) > 0 {
			p, initial = initial[0], initial[1:]
		} else {
			var err error
			p, err = c.ReadPacket(ready)
			if err != nil {
				t.Fatalf("protocol %d %s: %d/49 columns, position confirmed=%v: %v", c.Protocol(), stage, len(columns), synced, err)
			}
		}
		if p.ID == 0x48 && teleport {
			r := bytes.NewReader(p.Data)
			if _, err := mixedReadVI(r); err != nil {
				t.Fatal(err)
			}
			var xyz [3]float64
			if err := binary.Read(r, binary.BigEndian, &xyz); err != nil {
				t.Fatal(err)
			}
			synced = xyz == [3]float64{x, 71, x}
		}
		if p.ID == 0x2d && synced {
			chunk, err := conformance.DecodeChunk(c.Protocol(), p.Data, shape)
			if err != nil {
				t.Fatalf("%s chunk: %v", stage, err)
			}
			if chunk.X < center-3 || chunk.X > center+3 || chunk.Z < center-3 || chunk.Z > center+3 {
				t.Fatalf("%s unexpected column (%d,%d)", stage, chunk.X, chunk.Z)
			}
			key := [2]int32{chunk.X, chunk.Z}
			if columns[key] {
				t.Fatalf("%s duplicate column %v", stage, key)
			}
			columns[key] = true
			if key == [2]int32{center, center} {
				sample = p.Data
			}
		}
		if p.ID == 0x0b && len(columns) == 49 {
			if sample == nil {
				t.Fatalf("%s missing center chunk", stage)
			}
			t.Logf("protocol %d %s: 49 unique columns, final batch acknowledged, center body sha256=%x", c.Protocol(), stage, sha256.Sum256(sample))
			return sample
		}
	}
}

func heightmapKeys(m map[int32][]int64) map[int32]bool {
	keys := make(map[int32]bool, len(m))
	for key := range m {
		keys[key] = true
	}
	return keys
}

func columnPreparationEqual(t *testing.T, stage string, wantBody, gotBody []byte) {
	t.Helper()
	want, got := parseChunkView(t, "baseline", wantBody), parseChunkView(t, stage, gotBody)
	if !reflect.DeepEqual(want.hms, got.hms) || !bytes.Equal(want.data, got.data) {
		t.Fatalf("%s heightmaps or section bytes changed", stage)
	}
	var dark [2048]byte
	for section := 0; section < 24; section++ {
		wantBlock, gotBlock := want.block[section], got.block[section]
		if wantBlock == nil {
			wantBlock = dark[:]
		}
		if gotBlock == nil {
			gotBlock = dark[:]
		}
		if !bytes.Equal(want.sky[section], got.sky[section]) || !bytes.Equal(wantBlock, gotBlock) {
			t.Fatalf("%s canonical light changed in section %d", stage, section)
		}
	}
}
