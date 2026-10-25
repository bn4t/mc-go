package blackbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"testing"
	"time"

	"mc-go/conformance"
	"mc-go/internal/server"
)

type mixedFacts struct {
	BlockStates                         int `json:"block_states"`
	Biomes                              int
	Items, Entities, Attributes, Blocks map[string]int32
}

func mixedVI(b []byte, v int32) []byte {
	for uint32(v) >= 128 {
		b = append(b, byte(v)|128)
		v = int32(uint32(v) >> 7)
	}
	return append(b, byte(v))
}
func mixedReadVI(r *bytes.Reader) (int32, error) {
	var n int32
	for i := 0; i < 5; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, e
		}
		n |= int32(b&127) << (7 * i)
		if b < 128 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("overlong integer")
}
func mixedPos(x, y, z int32) uint64 {
	return uint64(x&0x3ffffff)<<38 | uint64(z&0x3ffffff)<<12 | uint64(y&0xfff)
}
func mixedWait(t *testing.T, c *conformance.PlayClient, match func(conformance.Packet) bool) conformance.Packet {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, e := c.WaitPacket(ctx, match)
	if e != nil {
		t.Fatalf("protocol%d waitingpacket: %v", c.Protocol(), e)
	}
	return p
}
func mixedSend(t *testing.T, c *conformance.PlayClient, id int32, b []byte) {
	t.Helper()
	if e := c.SendPacket(id, b); e != nil {
		t.Fatal(e)
	}
}
func mixedBarrier(t *testing.T, c *conformance.PlayClient, command string) {
	t.Helper()
	if e := c.Command(command); e != nil {
		t.Fatal(e)
	}
	token := fmt.Sprintf("mixed-%d", time.Now().UnixNano())
	if e := c.Command("tellraw @s \"" + token + "\""); e != nil {
		t.Fatal(e)
	}
	mixedWait(t, c, func(p conformance.Packet) bool { return p.ID == 0x79 && bytes.Contains(p.Data, []byte(token)) })
}
func mixedSlot(r *bytes.Reader) (int32, int32, error) {
	count, e := mixedReadVI(r)
	if e != nil || count == 0 {
		return 0, count, e
	}
	id, e := mixedReadVI(r)
	if e != nil {
		return 0, 0, e
	}
	add, e := mixedReadVI(r)
	if e != nil {
		return 0, 0, e
	}
	remove, e := mixedReadVI(r)
	if e != nil {
		return 0, 0, e
	}
	if add != 0 || remove != 0 {
		return 0, 0, fmt.Errorf("unexpected patch in simple fixture slot")
	}
	return id, count, nil
}
func mixedReady(t *testing.T, c *conformance.PlayClient, f mixedFacts) {
	t.Helper()
	chunks, inventory, recipes, attributes := 0, false, false, false
	columns := map[[2]int32]bool{}
	decodeChunk := func(p conformance.Packet) {
		chunk, e := conformance.DecodeChunk(c.Protocol(), p.Data, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes})
		if e != nil {
			t.Fatalf("protocol%d initialchunk: %v", c.Protocol(), e)
		}
		key := [2]int32{chunk.X, chunk.Z}
		if columns[key] {
			t.Fatalf("duplicate initial column%v", key)
		}
		columns[key] = true
		chunks++
	}
	for _, p := range c.InitialPackets() {
		switch p.ID {
		case 0x2d:
			decodeChunk(p)
		case 0x12:
			r := bytes.NewReader(p.Data)
			window, e := mixedReadVI(r)
			if e != nil {
				t.Fatal(e)
			}
			if window != 0 {
				continue
			}
			_, e = mixedReadVI(r)
			if e != nil {
				t.Fatal(e)
			}
			n, e := mixedReadVI(r)
			if e != nil || n < 46 {
				t.Fatal("incomplete playerinventory")
			}
			for range n {
				if _, _, e = mixedSlot(r); e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e = mixedSlot(r); e != nil || r.Len() != 0 {
				t.Fatal("inventorybody boundary")
			}
			inventory = true
		case 0x85:
			if len(p.Data) < 10 {
				t.Fatal("missing recipedefinitions")
			}
			recipes = true
		case 0x83:
			r := bytes.NewReader(p.Data)
			eid, _ := mixedReadVI(r)
			if eid != c.EntityID {
				continue
			}
			n, e := mixedReadVI(r)
			if e != nil {
				t.Fatal(e)
			}
			for range n {
				a, e := mixedReadVI(r)
				if e != nil {
					t.Fatal(e)
				}
				if a == f.Attributes["movement_speed"] {
					attributes = true
				}
				if _, e = r.Seek(8, io.SeekCurrent); e != nil {
					t.Fatal(e)
				}
				m, e := mixedReadVI(r)
				if e != nil {
					t.Fatal(e)
				}
				for range m {
					size, e := mixedReadVI(r)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = r.Seek(int64(size)+9, io.SeekCurrent); e != nil {
						t.Fatal(e)
					}
				}
			}
			if r.Len() != 0 {
				t.Fatal("attributebody boundary")
			}
		}
	}
	// Attributes follow the opening nine columns. Continue the ordinary
	// acknowledged batch flow to all49 unique columns; the public client
	// answers batch-finished and keepalive controls while returning packets.
	readyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for chunks < 49 {
		p, e := c.ReadPacket(readyCtx)
		if e != nil {
			t.Fatalf("protocol%d ready columns%d/49: %v", c.Protocol(), chunks, e)
		}
		if p.ID == 0x2d {
			decodeChunk(p)
		}
	}
	if chunks != 49 || !inventory || !recipes || !attributes {
		t.Fatalf("protocol%d incomplete ready chunks=%d inventory=%v recipes=%v attrs=%v", c.Protocol(), chunks, inventory, recipes, attributes)
	}
}

// Both clients cross actual TCP and decode independently pinned official facts.
// This must fail if either admission gate regresses; a skip is not compatibility.
func TestMixedJavaProtocolSharedWorld(t *testing.T) {
	admitted := map[int32]bool{}
	for _, v := range server.SupportedVersions() {
		admitted[v.Protocol] = true
	}
	if !admitted[775] || !admitted[776] {
		t.Fatal("mixed Java test requires both775and776 advertised/admitted")
	}
	raw, e := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
	if e != nil {
		t.Fatal(e)
	}
	var facts struct{ Profiles map[string]mixedFacts }
	if e = json.Unmarshal(raw, &facts); e != nil {
		t.Fatal(e)
	}
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) { s.ViewDistance = 2; s.SimulationDistance = 2 })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	clients := make([]*conformance.PlayClient, 2)
	for i, n := range []int32{775, 776} {
		c, e := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: n, Username: fmt.Sprintf("Mixed%d", n), Timeout: 20 * time.Second})
		if e != nil {
			t.Fatal(e)
		}
		clients[i] = c
		defer c.Close()
		mixedReady(t, c, facts.Profiles[fmt.Sprint(n)])
	}
	// The first player learns the second's shifted player type; the second's
	// initial burst includes the first player with its own profile's type.
	for i, c := range clients {
		other := clients[1-i]
		want := facts.Profiles[fmt.Sprint(c.Protocol())].Entities["player"]
		check := func(p conformance.Packet) bool {
			if p.ID != 1 {
				return false
			}
			r := bytes.NewReader(p.Data)
			eid, _ := mixedReadVI(r)
			if eid != other.EntityID {
				return false
			}
			var uuid [16]byte
			if _, e := io.ReadFull(r, uuid[:]); e != nil {
				t.Fatal(e)
			}
			typ, e := mixedReadVI(r)
			if e != nil || typ != want {
				t.Fatalf("protocol%d player type%d want%d", c.Protocol(), typ, want)
			}
			return true
		}
		found := false
		for _, p := range c.InitialPackets() {
			if check(p) {
				found = true
			}
		}
		if !found {
			mixedWait(t, c, check)
		}
	}
	mixedBarrier(t, clients[0], "fill 5 69 5 12 69 12 minecraft:stone")
	for i, c := range clients {
		mixedBarrier(t, c, fmt.Sprintf("tp @s %d.5 70 7.5", 7+i))
	}
	for i, actor := range clients {
		observer := clients[1-i]
		af := facts.Profiles[fmt.Sprint(actor.Protocol())]
		of := facts.Profiles[fmt.Sprint(observer.Protocol())]
		// Movement crosses the same canonical action path in both directions.
		movedX := 7.75 + float64(i)
		if e := actor.Move(movedX, 70, 7.5, 30, 0, true); e != nil {
			t.Fatal(e)
		}
		mixedWait(t, observer, func(p conformance.Packet) bool {
			if p.ID != 0x23 {
				return false
			}
			r := bytes.NewReader(p.Data)
			eid, e := mixedReadVI(r)
			if e != nil || eid != actor.EntityID {
				return false
			}
			var xyz [3]float64
			for j := range xyz {
				if e = binary.Read(r, binary.BigEndian, &xyz[j]); e != nil {
					t.Fatal(e)
				}
			}
			return xyz == [3]float64{movedX, 70, 7.5}
		})
		// A predicted move into a solid block is corrected to the committed
		// position; the peer must subsequently observe a valid move from it.
		// Keep the new wall entirely outside the prior0.6-wide player box.
		wallX := int(movedX) + 2
		mixedBarrier(t, actor, fmt.Sprintf("setblock %d 70 7 minecraft:stone", wallX))
		if e := actor.Move(float64(wallX)+.5, 70, 7.5, 30, 0, true); e != nil {
			t.Fatal(e)
		}
		mixedWait(t, actor, func(p conformance.Packet) bool {
			if p.ID != 0x48 {
				return false
			}
			r := bytes.NewReader(p.Data)
			if _, e := mixedReadVI(r); e != nil {
				t.Fatal(e)
			}
			var xyz [3]float64
			for j := range xyz {
				if e := binary.Read(r, binary.BigEndian, &xyz[j]); e != nil {
					t.Fatal(e)
				}
			}
			if xyz != [3]float64{movedX, 70, 7.5} {
				t.Fatalf("collision correction%v wantcommittedposition", xyz)
			}
			return true
		})
		mixedBarrier(t, actor, fmt.Sprintf("setblock %d 70 7 minecraft:air", wallX))
		// A target-version creative item ID mutates canonical inventory. Its peer
		// receives the same equipment item through a different numeric projection.
		b := binary.BigEndian.AppendUint16(nil, 36)
		b = mixedVI(b, 1)
		b = mixedVI(b, af.Items["copper_block"])
		b = append(b, 0, 0)
		mixedSend(t, actor, 0x38, b)
		mixedSend(t, actor, 0x35, []byte{0, 0})
		mixedWait(t, observer, func(p conformance.Packet) bool {
			if p.ID != 0x66 {
				return false
			}
			r := bytes.NewReader(p.Data)
			eid, _ := mixedReadVI(r)
			if eid != actor.EntityID {
				return false
			}
			for r.Len() > 0 {
				slot, e := r.ReadByte()
				if e != nil {
					t.Fatal(e)
				}
				id, count, e := mixedSlot(r)
				if e != nil {
					t.Fatal(e)
				}
				if slot&127 == 0 && count == 1 {
					if id != of.Items["copper_block"] {
						t.Fatalf("projected item%d want%d", id, of.Items["copper_block"])
					}
					return true
				}
				if slot&128 == 0 {
					break
				}
			}
			return false
		})
		x := int32(8 + i)
		position := mixedPos(x, 70, 9)
		b = mixedVI(nil, 0)
		b = binary.BigEndian.AppendUint64(b, mixedPos(x, 69, 9))
		b = mixedVI(b, 1)
		for range 3 {
			b = binary.BigEndian.AppendUint32(b, math.Float32bits(.5))
		}
		b = append(b, 0, 0)
		seq := int32(20 + i)
		b = mixedVI(b, seq)
		mixedSend(t, actor, 0x42, b)
		mixedWait(t, actor, func(p conformance.Packet) bool { return p.ID == 4 && bytes.Equal(p.Data, mixedVI(nil, seq)) })
		mixedWait(t, observer, func(p conformance.Packet) bool {
			if p.ID != 8 || len(p.Data) < 9 || binary.BigEndian.Uint64(p.Data[:8]) != position {
				return false
			}
			state, e := mixedReadVI(bytes.NewReader(p.Data[8:]))
			if e != nil || state != of.Blocks["copper_block"] {
				t.Fatalf("protocol%d blockstate%d want%d", observer.Protocol(), state, of.Blocks["copper_block"])
			}
			return true
		})
		b = mixedVI(nil, 0)
		b = binary.BigEndian.AppendUint64(b, position)
		b = append(b, 1)
		b = mixedVI(b, seq+10)
		mixedSend(t, actor, 0x29, b)
		mixedWait(t, observer, func(p conformance.Packet) bool {
			return p.ID == 8 && len(p.Data) == 9 && binary.BigEndian.Uint64(p.Data[:8]) == position && p.Data[8] == 0
		})
		// Native recipe display IDs differ from canonical indexes and from each other.
		mixedBarrier(t, actor, "recipe take @s minecraft:crafting_table")
		if e := actor.Command("recipe give @s minecraft:crafting_table"); e != nil {
			t.Fatal(e)
		}
		version := "26.1"
		if actor.Protocol() == 776 {
			version = "26.2"
		}
		recipeWant, e := os.ReadFile("../../conformance/testdata/recipe-crafting-table-" + version + ".bin")
		if e != nil {
			t.Fatal(e)
		}
		recipePacket := mixedWait(t, actor, func(p conformance.Packet) bool { return p.ID == 0x4a })
		if !bytes.Equal(recipePacket.Data, recipeWant) {
			t.Fatalf("protocol%d recipe display differs from official fixture", actor.Protocol())
		}
		b = binary.BigEndian.AppendUint16(nil, 36)
		b = mixedVI(b, 4)
		b = mixedVI(b, af.Items["oak_planks"])
		b = append(b, 0, 0)
		mixedSend(t, actor, 0x38, b)
		display := int32(293)
		if actor.Protocol() == 776 {
			display = 318
		}
		b = mixedVI(nil, 0)
		b = mixedVI(b, display)
		b = append(b, 0)
		mixedSend(t, actor, 0x27, b)
		mixedWait(t, actor, func(p conformance.Packet) bool {
			if p.ID != 0x12 {
				return false
			}
			r := bytes.NewReader(p.Data)
			window, _ := mixedReadVI(r)
			if window != 0 {
				return false
			}
			_, _ = mixedReadVI(r)
			n, e := mixedReadVI(r)
			if e != nil || n < 5 {
				return false
			}
			correct := 0
			for j := int32(0); j < n; j++ {
				id, count, e := mixedSlot(r)
				if e != nil {
					t.Fatal(e)
				}
				if j >= 1 && j <= 4 && id == af.Items["oak_planks"] && count == 1 {
					correct++
				}
			}
			return correct == 4
		})
		mixedBarrier(t, actor, "list") // both sessions remain live beyond full initial replication.
	}
}

// A missing target representation retires only that viewer; the newest viewer
// and world continue after a detached chunk snapshot cannot encode for775.
func TestMixedJavaProtocolMissingStateContainment(t *testing.T) {
	admitted := false
	for _, v := range server.SupportedVersions() {
		if v.Protocol == 775 {
			admitted = true
		}
	}
	if !admitted {
		t.Fatal("775 admission gate disabled")
	}
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) { s.ViewDistance = 2; s.SimulationDistance = 2 })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	current, e := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 776, Username: "NewView", Timeout: 60 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	defer current.Close()
	// sendWindow synchronously warms sky columns before submitting detached
	// encoders. Measure that setup separately, with only representable state;
	// the strict retirement timer must measure the later missing-state failure.
	warmWindow := func(x, z int32) {
		t.Helper()
		started := time.Now()
		if e := current.Command(fmt.Sprintf("tp @s %d.0 71 %d.0", x, z)); e != nil {
			t.Fatal(e)
		}
		ready, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		columns := map[[2]int32]bool{}
		synced := false
		for len(columns) < 49 {
			packet, e := current.ReadPacket(ready)
			if e != nil {
				t.Fatalf("compatible setup center%d,%d: %d/49 columns after%v: %v", x/16, z/16, len(columns), time.Since(started), e)
			}
			if packet.ID == 0x48 {
				r := bytes.NewReader(packet.Data)
				if _, e := mixedReadVI(r); e != nil {
					t.Fatal(e)
				}
				var xyz [3]float64
				for i := range xyz {
					if e := binary.Read(r, binary.BigEndian, &xyz[i]); e != nil {
						t.Fatal(e)
					}
				}
				synced = xyz == [3]float64{float64(x), 71, float64(z)}
			}
			if packet.ID != 0x2d {
				continue
			}
			chunk, e := conformance.DecodeChunk(776, packet.Data, conformance.ChunkShape{Sections: 24, BlockStates: 32366, Biomes: 66})
			if e != nil {
				t.Fatal(e)
			}
			if !synced || chunk.X < x/16-3 || chunk.X > x/16+3 || chunk.Z < z/16-3 || chunk.Z > z/16+3 {
				continue
			}
			key := [2]int32{chunk.X, chunk.Z}
			if columns[key] {
				t.Fatalf("duplicate compatible setup column%v", key)
			}
			columns[key] = true
		}
		mixedBarrier(t, current, "list")
		t.Logf("compatible49-column setup center%d,%d completed in%v", x/16, z/16, time.Since(started))
	}
	warmWindow(320, 320)
	warmWindow(8, 8)
	// Admit the old viewer only after setup, so keepalive silence during a cold
	// warmup cannot be mistaken for missing-state containment. Its target
	// viewport is still unvisited; both viewers are outside when we mutate it.
	old, e := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 775, Username: "OldView", Timeout: 60 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	mixedBarrier(t, old, "list")

	mixedBarrier(t, current, "setblock 320 70 320 minecraft:cinnabar")
	if e := old.Command("tp @s 320 71 320"); e != nil {
		t.Fatal(e)
	}
	retirementStarted := time.Now()
	deadline, cancelRead := context.WithTimeout(ctx, 10*time.Second)
	defer cancelRead()
	closed := false
	for !closed {
		_, e = old.ReadPacket(deadline)
		if e != nil {
			if deadline.Err() != nil {
				t.Fatal("775 remained connected until test deadline")
			}
			if n, ok := e.(net.Error); ok && n.Timeout() {
				t.Fatal("775 read timeout is not retirement")
			}
			closed = true
		}
	}
	t.Logf("775 missing-state retirement completed in%v", time.Since(retirementStarted))
	mixedBarrier(t, current, "setblock 8 70 8 minecraft:stone")
	if e := current.Command("tp @s 320 71 320"); e != nil {
		t.Fatal(e)
	}
	mixedWait(t, current, func(p conformance.Packet) bool {
		if p.ID != 0x2d {
			return false
		}
		chunk, e := conformance.DecodeChunk(776, p.Data, conformance.ChunkShape{Sections: 24, BlockStates: 32366, Biomes: 66})
		if e != nil {
			t.Fatal(e)
		}
		if chunk.X != 20 || chunk.Z != 20 {
			return false
		}
		// The newest viewer must receive the actual committed newer block,
		// not merely stay connected while the world mutation is discarded.
		raw, err := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
		if err != nil {
			t.Fatal(err)
		}
		var facts struct{ Profiles map[string]mixedFacts }
		if err = json.Unmarshal(raw, &facts); err != nil {
			t.Fatal(err)
		}
		index := ((70+64)/16)*4096 + ((70+64)%16)*256 // x=z=0 in chunk20,20
		if chunk.Blocks[index] != facts.Profiles["776"].Blocks["cinnabar"] {
			t.Fatalf("new viewer lost committed cinnabar state%d", chunk.Blocks[index])
		}
		return true
	})
	mixedBarrier(t, current, "list")
}
