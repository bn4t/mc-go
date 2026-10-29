package worldgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"mc-go/internal/nbt"
)

// TestStructureStartRoundTrip generates the seed-12345 plains village
// at chunk (-9, -52) — 99 pieces, as the real server's saved start has
// (TestStructureVillagePlainsParity) — and checks that its saved
// compound loads back to the same pieces. Loading recomputes jigsaw
// boxes from the element, so expansion-hack growth is not kept.
func TestStructureStartRoundTrip(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	g.Structures = true
	starts := g.StructureStarts(-9, -52)
	if len(starts) != 1 || starts[0].Structure != "village_plains" {
		t.Fatalf("starts at -9,-52: %v", starts)
	}
	s := starts[0]
	if len(s.Pieces) != 99 {
		t.Fatalf("%d pieces, want 99", len(s.Pieces))
	}
	tag := StartTag(s)
	// Through the NBT codec and back.
	var buf bytes.Buffer
	if err := nbt.WriteNamed(&buf, "", tag); err != nil {
		t.Fatal(err)
	}
	_, v, err := nbt.ReadNamed(&buf)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := g.LoadStart(v.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pieces) != len(s.Pieces) {
		t.Fatalf("loaded %d pieces", len(loaded.Pieces))
	}
	for i, p := range s.Pieces {
		a, b := p.(*JigsawPiece), loaded.Pieces[i].(*JigsawPiece)
		ea, _ := json.Marshal(a.element.raw())
		var x, y any
		json.Unmarshal(a.element.raw(), &x)
		json.Unmarshal(b.element.raw(), &y)
		if a.Position != b.Position || a.Rotation != b.Rotation || a.GroundLvl != b.GroundLvl ||
			fmt.Sprint(a.Junctions) != fmt.Sprint(b.Junctions) || fmt.Sprint(x) != fmt.Sprint(y) {
			t.Errorf("piece %d differs after load: %s", i, ea)
		}
		if b.Box != b.element.boundingBox(b.Position, b.Rotation) {
			t.Errorf("piece %d: loaded box %v not the element box", i, b.Box)
		}
	}
	refs := g.StructureReferences(-9, -52)
	if len(refs) != 1 || len(refs[0].Starts) != 1 || refs[0].Starts[0] != [2]int32{-9, -52} {
		t.Errorf("references at the start chunk: %v", refs)
	}
}
