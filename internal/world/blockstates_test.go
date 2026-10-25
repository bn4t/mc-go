package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// reportBlocks loads vanilla/reports/blocks.json, the official data
// generator's block table. Tests below use it as the source of truth
// for every state id and property value, the same way the packet-ID
// test uses packets.json.
func reportBlocks(t *testing.T) map[string]struct {
	Properties map[string][]string `json:"properties"`
	States     []struct {
		ID         int32             `json:"id"`
		Default    bool              `json:"default"`
		Properties map[string]string `json:"properties"`
	} `json:"states"`
} {
	t.Helper()
	path := filepath.Join("..", "..", "vanilla", "reports", "blocks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("official block report unavailable: %v", err)
	}
	var doc map[string]struct {
		Properties map[string][]string `json:"properties"`
		States     []struct {
			ID         int32             `json:"id"`
			Default    bool              `json:"default"`
			Properties map[string]string `json:"properties"`
		} `json:"states"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

// TestBlockStateTableMatchesOfficialReport walks every one of the 32366
// states in the official report and checks that mc-go's generated table
// reproduces it exactly: the same owning block, the same property
// values, and a round-trip through BlockStateOf. This is what makes the
// packed encoding safe to ship as an opaque blob.
func TestBlockStateTableMatchesOfficialReport(t *testing.T) {
	doc := reportBlocks(t)

	checked := 0
	for fullName, blk := range doc {
		name := fullName[len("minecraft:"):]
		if _, ok := blockIndexByName[name]; !ok {
			t.Fatalf("block %s missing from the generated table", name)
		}
		for _, st := range blk.States {
			b := Block(st.ID)
			gotName, gotProps, ok := DescribeState(b)
			if !ok {
				t.Fatalf("state %d (%s): DescribeState failed", st.ID, name)
			}
			if gotName != name {
				t.Fatalf("state %d: owner = %q, want %q", st.ID, gotName, name)
			}
			if len(gotProps) != len(st.Properties) {
				t.Fatalf("state %d (%s): %d properties, want %d", st.ID, name, len(gotProps), len(st.Properties))
			}
			for k, want := range st.Properties {
				if gotProps[k] != want {
					t.Fatalf("state %d (%s): property %s = %q, want %q", st.ID, name, k, gotProps[k], want)
				}
			}
			// Resolving the same name + properties must land back on
			// the same id.
			back, ok := BlockStateOf(name, st.Properties)
			if !ok {
				t.Fatalf("state %d (%s): BlockStateOf rejected the report's own properties", st.ID, name)
			}
			if back != b {
				t.Fatalf("state %d (%s): BlockStateOf = %d, want %d", st.ID, name, back, b)
			}
			checked++
		}
	}
	if checked != StateCount() {
		t.Fatalf("checked %d states, table declares %d", checked, StateCount())
	}
}

// TestDefaultStateMatchesReport pins BlockStateOf(name, nil) to the
// report's own default state, and to the default-state id the older
// blocks_gen.go table carries.
func TestDefaultStateMatchesReport(t *testing.T) {
	doc := reportBlocks(t)
	for fullName, blk := range doc {
		name := fullName[len("minecraft:"):]
		var want Block
		found := false
		for _, st := range blk.States {
			if st.Default {
				want = Block(st.ID)
				found = true
			}
		}
		if !found {
			t.Fatalf("%s has no default state in the report", name)
		}
		got, ok := BlockStateOf(name, nil)
		if !ok {
			t.Fatalf("%s: BlockStateOf(name, nil) failed", name)
		}
		if got != want {
			t.Fatalf("%s: default state = %d, want %d", name, got, want)
		}
		if def, ok := BlockState(name); !ok || def != want {
			t.Fatalf("%s: BlockState = %d (ok=%v), want %d", name, def, ok, want)
		}
	}
}

// TestStatePropertiesRoundTrip exercises the property accessors on
// every state: reading each property back, and rebuilding the state by
// applying every property to the default.
func TestStatePropertiesRoundTrip(t *testing.T) {
	doc := reportBlocks(t)
	for fullName, blk := range doc {
		name := fullName[len("minecraft:"):]
		for _, st := range blk.States {
			b := Block(st.ID)
			for prop, want := range st.Properties {
				got, ok := StateProperty(b, prop)
				if !ok {
					t.Fatalf("state %d (%s): StateProperty(%s) missing", st.ID, name, prop)
				}
				if got != want {
					t.Fatalf("state %d (%s): StateProperty(%s) = %q, want %q", st.ID, name, prop, got, want)
				}
			}
			// Apply every property onto the block's default state.
			if len(st.Properties) == 0 {
				continue
			}
			built, ok := BlockStateOf(name, st.Properties)
			if !ok || built != b {
				t.Fatalf("state %d (%s): rebuilt = %d (ok=%v), want %d", st.ID, name, built, ok, b)
			}
			// And one property at a time, from the previous state.
			for prop, want := range st.Properties {
				next, ok := WithProperty(b, prop, want)
				if !ok {
					t.Fatalf("state %d (%s): WithProperty(%s=%s) failed", st.ID, name, prop, want)
				}
				if next != b {
					t.Fatalf("state %d (%s): WithProperty(%s=%s) = %d, want unchanged", st.ID, name, prop, want, next)
				}
			}
		}
	}
}

// TestWithPropertyChangesState checks that WithProperty really moves
// between states: every block with an enum property must reach a
// different id for each of that property's values.
func TestWithPropertyChangesState(t *testing.T) {
	doc := reportBlocks(t)
	for fullName, blk := range doc {
		name := fullName[len("minecraft:"):]
		if len(blk.States) == 0 {
			continue
		}
		def, _ := BlockStateOf(name, nil)
		for prop, vals := range blk.Properties {
			seen := map[Block]string{}
			for _, v := range vals {
				got, ok := WithProperty(def, prop, v)
				if !ok {
					t.Fatalf("%s: WithProperty(%s=%s) failed", name, prop, v)
				}
				back, _ := StateProperty(got, prop)
				if back != v {
					t.Fatalf("%s: WithProperty(%s=%s) read back %q", name, prop, v, back)
				}
				if prev, dup := seen[got]; dup && prev != v {
					t.Fatalf("%s: %s values %q and %q share state %d", name, prop, prev, v, got)
				}
				seen[got] = v
			}
		}
	}
}

// TestUnknownStatesAreRejected pins the failure modes so callers can
// rely on them.
func TestUnknownStatesAreRejected(t *testing.T) {
	if _, ok := BlockStateOf("definitely_not_a_block", nil); ok {
		t.Fatal("unknown block name accepted")
	}
	if _, ok := BlockStateOf("stone", map[string]string{"nope": "1"}); ok {
		t.Fatal("unknown property accepted")
	}
	if _, ok := BlockStateOf("water", map[string]string{"level": "99"}); ok {
		t.Fatal("out-of-range property value accepted")
	}
	if _, ok := WithProperty(Stone, "level", "1"); ok {
		t.Fatal("WithProperty accepted a property stone does not have")
	}
	if _, _, ok := DescribeState(Block(maxBlockState)); ok {
		t.Fatal("out-of-range state id described")
	}
	if _, ok := StateOwner(Block(maxBlockState)); ok {
		t.Fatal("out-of-range state id owned")
	}
}

// TestFluidStatesResolve checks the concrete states the fluid engine
// will lean on, so a regenerated table cannot silently drift.
func TestFluidStatesResolve(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]string
		id    Block
	}{
		{"water", map[string]string{"level": "0"}, 86},
		{"water", map[string]string{"level": "15"}, 101},
		{"lava", map[string]string{"level": "0"}, 102},
		{"lava", map[string]string{"level": "15"}, 117},
	}
	for _, c := range cases {
		got, ok := BlockStateOf(c.name, c.props)
		if !ok {
			t.Fatalf("%s%v: not resolvable", c.name, c.props)
		}
		if got != c.id {
			t.Fatalf("%s%v = %d, want %d", c.name, c.props, got, c.id)
		}
	}
	if n := len(StatesOfBlock("water")); n != 16 {
		t.Fatalf("water has %d states, want 16", n)
	}
}
