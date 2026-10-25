package server

import (
	"errors"
	"strings"
	"testing"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// This deliberately non-production projection swaps two measured776 IDs. It
// catches raw runtime-ID casts that an identity production table would hide.
func permutedBuiltinBlockMapping(t *testing.T) *registry.BlockMapping {
	t.Helper()
	catalog := world.BlockCatalog()
	tnt, ok := world.BlockStateOf("tnt", nil)
	if !ok {
		t.Fatal("missing TNT builtin")
	}
	entries := make([]registry.BlockWireEntry, catalog.Len())
	for i := range entries {
		key, _ := catalog.State(registry.BlockStateID(i))
		wire := int32(i)
		if world.Block(i) == world.Stone {
			wire = int32(tnt)
		} else if world.Block(i) == tnt {
			wire = int32(world.Stone)
		}
		entries[i] = registry.BlockWireEntry{State: key, WireID: wire}
	}
	mapping, err := registry.NewBlockMapping(999, catalog, entries, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return mapping
}

func TestBlockBreakLevelEventUsesProfileProjection(t *testing.T) {
	mapping := permutedBuiltinBlockMapping(t)
	profile := &javaProfile{blocks: mapping}
	body, err := profile.encodeLevelEvent(2001, world.Pos{X: -17, Y: 70, Z: 16}, int32(world.Stone))
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	event, _ := r.Int()
	x, y, z, positionErr := r.Position()
	data, dataErr := r.Int()
	global, boolErr := r.Bool()
	want, _ := mapping.Encode(world.Stone)
	if event != 2001 || x != -17 || y != 70 || z != 16 || positionErr != nil || dataErr != nil || boolErr != nil || data != want || data == int32(world.Stone) || global || r.Remaining() != 0 {
		t.Fatalf("projected effect event=%d pos=(%d,%d,%d) data=%d want=%d remaining=%d", event, x, y, z, data, want, r.Remaining())
	}
	normal, err := profile.encodeLevelEvent(2000, world.Pos{}, 7)
	if err != nil {
		t.Fatal(err)
	}
	nr := protocol.NewPacketReader(normal)
	_, _ = nr.Int()
	_, _, _, _ = nr.Position()
	unchanged, _ := nr.Int()
	if unchanged != 7 {
		t.Fatal("non-block effect data was projected")
	}
}

func TestTNTAndEndermanBlockScalarsUseExplicitMapping(t *testing.T) {
	mapping := permutedBuiltinBlockMapping(t)
	body, err := encodePrimedTntMetadataFor(23, mapping)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	fuseIndex, _ := r.Byte()
	fuseType, _ := r.Byte()
	fuse, _ := r.VarInt()
	stateIndex, _ := r.Byte()
	stateType, _ := r.Byte()
	wire, _ := r.VarInt()
	end, _ := r.Byte()
	if fuseIndex != 8 || fuseType != 1 || fuse != 23 || stateIndex != 9 || stateType != 14 || wire != int32(world.Stone) || end != 255 || r.Remaining() != 0 {
		t.Fatalf("TNT scalar mapping failed: %x", body)
	}
	carried, err := enderCarryStateFor(&Entity{EnderCarryName: "stone"}, mapping)
	want, _ := mapping.Encode(world.Stone)
	if err != nil || carried != want || carried == int32(world.Stone) {
		t.Fatalf("enderman state=%d want=%d err=%v", carried, want, err)
	}
	if empty, err := enderCarryStateFor(&Entity{}, mapping); err != nil || empty != 0 {
		t.Fatal("empty optional was treated as a block handle")
	}
}

func TestBlockEffectsRejectMissingProtocolState(t *testing.T) {
	mapping, _ := world.BlockMapping(775)
	missing := mapping.Missing()[0]
	if body, err := (&javaProfile{blocks: mapping}).encodeLevelEvent(2001, world.Pos{}, int32(missing)); !errors.Is(err, registry.ErrMissingBlockState) || body != nil {
		t.Fatal("missing break effect state was fabricated")
	}
	key, _ := world.BlockCatalog().State(missing)
	entity := &Entity{EnderCarryName: strings.TrimPrefix(key.Name(), "minecraft:"), EnderCarryProps: key.Properties()}
	if _, err := enderCarryStateFor(entity, mapping); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatal("missing carried state was silently emptied")
	}
}
