package externalavatar

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"mc-go/api/worldview"
	"mc-go/conformance"
)

type blockFacts struct {
	BlockStates   int `json:"block_states"`
	Biomes        int
	States, Items map[string]int32
	Packets       map[string]int32
}

func factsFor(t *testing.T, n int32) blockFacts {
	t.Helper()
	b, e := os.ReadFile("facts.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct{ Profiles map[string]blockFacts }
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	return f.Profiles[fmt.Sprint(n)]
}
func appendVI(b []byte, n int32) []byte {
	for uint32(n) >= 128 {
		b = append(b, byte(n)|128)
		n = int32(uint32(n) >> 7)
	}
	return append(b, byte(n))
}
func longVI(r *bytes.Reader) (uint64, error) {
	var n uint64
	for i := 0; i < 10; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, e
		}
		if i == 9 && b > 1 {
			return 0, fmt.Errorf("overflowing VarLong")
		}
		n |= uint64(b&127) << (7 * i)
		if b < 128 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("overlong VarLong")
}
func packedPos(p worldview.Pos) uint64 {
	return uint64(p.X&0x3ffffff)<<38 | uint64(p.Z&0x3ffffff)<<12 | uint64(p.Y&0xfff)
}
func unpackedPos(n uint64) worldview.Pos {
	return worldview.Pos{X: int32(int64(n) >> 38), Y: int32(int64(n<<52) >> 52), Z: int32(int64(n<<26) >> 38)}
}
func inside(b worldview.Box, p worldview.Pos) bool {
	return p.X >= b.Min.X && p.X < b.Max.X && p.Y >= b.Min.Y && p.Y < b.Max.Y && p.Z >= b.Min.Z && p.Z < b.Max.Z
}
func at(b worldview.Box, p worldview.Pos) int {
	return int(((p.Y-b.Min.Y)*(b.Max.Z-b.Min.Z)+(p.Z-b.Min.Z))*(b.Max.X-b.Min.X) + (p.X - b.Min.X))
}
func positions(b worldview.Box) []worldview.Pos {
	var out []worldview.Pos
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for z := b.Min.Z; z < b.Max.Z; z++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				out = append(out, worldview.Pos{X: x, Y: y, Z: z})
			}
		}
	}
	return out
}

type nativeBlocks struct {
	j        *javaPeer
	facts    blockFacts
	box      worldview.Box
	cells    map[worldview.Pos]int32
	onChange func(worldview.Pos, int32)
}

func newNative(t *testing.T, j *javaPeer, b worldview.Box) *nativeBlocks {
	return &nativeBlocks{j: j, facts: factsFor(t, j.client.Protocol()), box: b, cells: map[worldview.Pos]int32{}}
}
func (n *nativeBlocks) apply(t *testing.T, p conformance.Packet) {
	t.Helper()
	store := func(pos worldview.Pos, state int32) {
		if state < 0 || int(state) >= n.facts.BlockStates {
			t.Fatalf("Java%d invalid state%d", n.j.client.Protocol(), state)
		}
		if inside(n.box, pos) {
			if n.onChange != nil {
				n.onChange(pos, state)
			}
			n.cells[pos] = state
		}
	}
	switch p.ID {
	case n.facts.Packets["minecraft:block_update"]:
		r := bytes.NewReader(p.Data)
		var packed uint64
		if e := binary.Read(r, binary.BigEndian, &packed); e != nil {
			t.Fatal(e)
		}
		state, e := vi(r)
		if e != nil || r.Len() != 0 {
			t.Fatal("single block update boundary")
		}
		store(unpackedPos(packed), state)
	case n.facts.Packets["minecraft:section_blocks_update"]:
		r := bytes.NewReader(p.Data)
		var section uint64
		if e := binary.Read(r, binary.BigEndian, &section); e != nil {
			t.Fatal(e)
		}
		cx := int32(int64(section) >> 42)
		cz := int32(int64(section<<22) >> 42)
		cy := int32(int64(section<<44) >> 44)
		count, e := vi(r)
		if e != nil || count < 0 || count > 4096 {
			t.Fatal("section update count")
		}
		for range count {
			v, e := longVI(r)
			if e != nil || v>>12 > math.MaxInt32 {
				t.Fatal("section update value")
			}
			local := uint32(v & 4095)
			store(worldview.Pos{X: cx*16 + int32(local>>8), Y: cy*16 + int32(local&15), Z: cz*16 + int32(local>>4&15)}, int32(v>>12))
		}
		if r.Len() != 0 {
			t.Fatal("section update boundary")
		}
	case n.facts.Packets["minecraft:level_chunk_with_light"]:
		c, e := conformance.DecodeChunk(n.j.client.Protocol(), p.Data, conformance.ChunkShape{Sections: 16, BlockStates: n.facts.BlockStates, Biomes: n.facts.Biomes})
		if e != nil {
			t.Fatal(e)
		}
		for _, pos := range positions(n.box) {
			if pos.X>>4 == c.X && pos.Z>>4 == c.Z {
				index := int(pos.Y/16)*4096 + int(pos.Y&15)*256 + int(pos.Z&15)*16 + int(pos.X&15)
				store(pos, c.Blocks[index])
			}
		}
	}
}
func (n *nativeBlocks) barrier(t *testing.T, token string) {
	t.Helper()
	n.j.until(t, func(p conformance.Packet) bool {
		n.apply(t, p)
		return p.ID == 0x79 && bytes.Contains(p.Data, []byte(token))
	})
}
func blockFence(t *testing.T, h *host, viewers []*nativeBlocks) {
	t.Helper()
	h.s.StepTicks(1)
	token := fmt.Sprintf("block-fence-%d", time.Now().UnixNano())
	command(t, h.scope.core, "tellraw @a \""+token+"\"")
	for _, n := range viewers {
		n.barrier(t, token)
	}
}
func javaBlockCommand(t *testing.T, h *host, issuer *javaPeer, cmd string, viewers []*nativeBlocks) {
	t.Helper()
	if e := issuer.client.Command(cmd); e != nil {
		t.Fatal(e)
	}
	token := fmt.Sprintf("block-input-%d", time.Now().UnixNano())
	if e := issuer.client.Command("tellraw @a \"" + token + "\""); e != nil {
		t.Fatal(e)
	}
	for _, n := range viewers {
		n.barrier(t, token)
	}
	blockFence(t, h, viewers)
}
func (n *nativeBlocks) expect(t *testing.T, pos worldview.Pos, key string) {
	t.Helper()
	want, ok := n.facts.States[key]
	if !ok {
		t.Fatalf("missing independent wire fact%q", key)
	}
	if got, ok := n.cells[pos]; !ok || got != want {
		t.Fatalf("Java%d at%+v got%d present%v want%s=%d", n.j.client.Protocol(), pos, got, ok, key, want)
	}
}
func readyNether(t *testing.T, n *nativeBlocks) {
	t.Helper()
	j := n.j
	if e := j.client.Command("execute in minecraft:the_nether run tp @s -0.5 70.0 -0.5"); e != nil {
		t.Fatal(e)
	}
	j.respawn(t, "minecraft:the_nether")
	seen := map[[2]int32]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for len(seen) < 49 {
		p, e := j.client.ReadPacket(ctx)
		if e != nil {
			t.Fatalf("Nether readiness columns%d/49: %v", len(seen), e)
		}
		n.apply(t, p)
		if p.ID != 0x2d {
			continue
		}
		key := [2]int32{int32(binary.BigEndian.Uint32(p.Data[:4])), int32(binary.BigEndian.Uint32(p.Data[4:8]))}
		if seen[key] {
			t.Fatal("duplicate Nether opening column")
		}
		seen[key] = true
	}
}

func creativeBlock(t *testing.T, j *javaPeer, pos worldview.Pos, sequence int32) {
	t.Helper()
	f := factsFor(t, j.client.Protocol())
	b := binary.BigEndian.AppendUint16(nil, 36)
	b = appendVI(b, 1)
	b = appendVI(b, f.Items["minecraft:waxed_copper_block"])
	b = append(b, 0, 0)
	if e := j.client.SendPacket(0x38, b); e != nil {
		t.Fatal(e)
	}
	if e := j.client.SendPacket(0x35, []byte{0, 0}); e != nil {
		t.Fatal(e)
	}
	b = appendVI(nil, 0)
	support := pos
	support.Y--
	b = binary.BigEndian.AppendUint64(b, packedPos(support))
	b = appendVI(b, 1)
	for range 3 {
		b = binary.BigEndian.AppendUint32(b, math.Float32bits(.5))
	}
	b = append(b, 0, 0)
	b = appendVI(b, sequence)
	if e := j.client.SendPacket(0x42, b); e != nil {
		t.Fatal(e)
	}
}
func creativeDig(t *testing.T, j *javaPeer, pos worldview.Pos, sequence int32) {
	t.Helper()
	b := appendVI(nil, 0)
	b = binary.BigEndian.AppendUint64(b, packedPos(pos))
	b = append(b, 1)
	b = appendVI(b, sequence)
	if e := j.client.SendPacket(0x29, b); e != nil {
		t.Fatal(e)
	}
}
