package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"mc-go/internal/data"
)

func TestAdvancementCapturedCoordinatesPreserveDefinitions(t *testing.T) {
	checked := 0
	for id, definition := range data.AdvDefWire {
		if meta := gameAdvs[id]; meta == nil || !meta.hasDisplay {
			continue
		}
		original := append([]byte(nil), definition...)
		rewritten, err := advancementDefinitionCoordinates(definition, advancementLayoutPoint{1.25, 2.5})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(rewritten) != len(definition) || !bytes.Equal(definition, original) {
			t.Fatalf("%s changed captured definition storage/shape", id)
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only%d built-in displayed definitions checked", checked)
	}
}

func TestAdvancementDisplayRejectsMalformedNestedValues(t *testing.T) {
	for _, body := range []string{
		`{"title":{"text":"x","extra":[1]},"description":"d","icon":{"id":"minecraft:paper"}}`,
		`{"title":{"color":"red"},"description":"d","icon":{"id":"minecraft:paper"}}`,
		`{"title":"x","description":"d","icon":{"id":"minecraft:paper","count":1.5}}`,
		`{"title":"x","description":"d","icon":{"id":"minecraft:paper","components":[]}}`,
		`{"title":"x","description":"d","icon":{"id":"minecraft:paper"},"show_toast":"yes"}`,
	} {
		var raw any
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		if _, err := parseAdvancementDisplay(raw); err == nil {
			t.Errorf("accepted malformed display%s", body)
		}
	}
}

// Check public tidy-drawing constraints on varied shapes, independently of
// the layout's contour/thread representation. These constraints also pin the
// real vanilla wire fixture, where sibling order varies by object identity.
func TestAdvancementTidyLayoutConstraints(t *testing.T) {
	random := rand.New(rand.NewSource(9173))
	for sample := 0; sample < 80; sample++ {
		count := 30 + random.Intn(50)
		entries := map[string]*advMeta{}
		children := map[string][]string{}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("tree:n%d", i)
			meta := &advMeta{id: id, hasDisplay: i == 0 || random.Intn(4) != 0}
			if i > 0 {
				meta.parent = fmt.Sprintf("tree:n%d", random.Intn(i))
				children[meta.parent] = append(children[meta.parent], id)
			}
			entries[id] = meta
		}
		points := layoutAdvancementTree("tree:n0", entries, children)
		levels := map[float32][]float32{}
		var visibleChildren func(string) []string
		visibleChildren = func(id string) []string {
			var out []string
			for _, child := range children[id] {
				if entries[child].hasDisplay {
					out = append(out, child)
				} else {
					out = append(out, visibleChildren(child)...)
				}
			}
			return out
		}
		for id, meta := range entries {
			point, found := points[id]
			if !meta.hasDisplay {
				if found {
					t.Errorf("sample%d non-display node%s rendered", sample, id)
				}
				continue
			}
			if !found {
				t.Fatalf("sample%d missing node%s", sample, id)
			}
			depth := 0
			for parent := meta.parent; parent != ""; parent = entries[parent].parent {
				if entries[parent].hasDisplay {
					depth++
				}
			}
			if point.x != float32(depth) || point.y < 0 {
				t.Fatalf("sample%d %s point%v depth%d", sample, id, point, depth)
			}
			levels[point.x] = append(levels[point.x], point.y)
			visible := visibleChildren(id)
			if len(visible) == 0 {
				continue
			}
			low, high := points[visible[0]].y, points[visible[0]].y
			for _, child := range visible {
				low = min(low, points[child].y)
				high = max(high, points[child].y)
			}
			if math.Abs(float64(point.y-(low+high)/2)) > 1e-5 {
				t.Fatalf("sample%d %s not centered y%g children%g..%g", sample, id, point.y, low, high)
			}
		}
		for depth, ys := range levels {
			sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
			for i := 1; i < len(ys); i++ {
				if ys[i]-ys[i-1] < 1-1e-5 {
					t.Fatalf("sample%d depth%g overlaps%v", sample, depth, ys)
				}
			}
		}
	}
}

func TestAdvancementIconTemplateCountBounds(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100, 127} {
		raw := map[string]any{"title": "x", "description": "d", "icon": map[string]any{"id": "minecraft:paper", "count": float64(count)}}
		display, err := parseAdvancementDisplay(raw)
		valid := count >= 1 && count <= 99
		if (err == nil) != valid {
			t.Errorf("count%d accepted=%v wanted%v", count, err == nil, valid)
		}
		if valid && display.icon.Count != int32(count) {
			t.Errorf("count%d changed to%d", count, display.icon.Count)
		}
	}
}
