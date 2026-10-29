package world

import (
	"strings"
	"testing"

	"mc-go/internal/nbt"
)

// TestRegionRoundTripAtOverworldBounds round-trips overrides through a
// region file at the overworld's vertical bounds. The section index a
// chunk stores is absolute - the block Y divided by sixteen - so the
// lowest overworld section is -4: a writer that numbered sections from
// the world's floor would put every block sixteen sections too high,
// and a reader that did not multiply back would put them at Y>=0. Three
// positions, one per band, is enough to catch both.
func TestRegionRoundTripAtOverworldBounds(t *testing.T) {
	const minY, height = -64, 384
	w := New(nil, 4)
	w.Start()
	defer w.Stop()

	want := map[Pos]Block{
		{X: 3, Y: -63, Z: 5}:   Stone, // inside the lowest section
		{X: 3, Y: 70, Z: 5}:    Dirt,  // a section near the middle
		{X: 40, Y: 319, Z: 40}: Stone, // the top of the world
	}
	for p, b := range want {
		w.SetBlock(p, b)
	}

	dir := t.TempDir()
	if err := w.SaveRegion(dir, minY, height); err != nil {
		t.Fatalf("save: %v", err)
	}

	w2 := New(nil, 4)
	w2.Start()
	defer w2.Stop()
	if err := w2.LoadRegion(dir, minY, height); err != nil {
		t.Fatalf("load: %v", err)
	}
	for p, b := range want {
		if got := w2.GetBlock(p); got != b {
			t.Fatalf("block at %v = %d, want %d", p, got, b)
		}
	}
	// The generator's own ground truth survives the round trip: a
	// column nothing was written to still generates the flat preset.
	if got := w2.GetBlock(Pos{X: 100, Y: -61, Z: 100}); got != Grass {
		t.Fatalf("untouched column = %d, want the generator's grass", got)
	}
}

// TestRegionChunkCarriesVanillaSectionIndex pins the section index the
// file itself carries, not just that a round trip agrees with itself:
// the lowest overworld section is written as -4 and the topmost as 19.
func TestRegionChunkCarriesVanillaSectionIndex(t *testing.T) {
	const minY, height = -64, 384
	w := New(nil, 2)
	w.Start()
	defer w.Stop()
	w.SetBlock(Pos{X: 1, Y: -64, Z: 1}, Stone)

	dir := t.TempDir()
	if err := w.SaveRegion(dir, minY, height); err != nil {
		t.Fatal(err)
	}
	chunks, err := ReadRegionFile(regionFileName(dir, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunk, ok := chunks[[2]int32{0, 0}]
	if !ok {
		t.Fatal("chunk (0,0) missing from the region file")
	}
	sections, _ := chunk["sections"].(nbt.List)
	if len(sections.Items) != height/16 {
		t.Fatalf("chunk has %d sections, want %d", len(sections.Items), height/16)
	}
	first, _ := sections.Items[0].(map[string]any)
	last, _ := sections.Items[len(sections.Items)-1].(map[string]any)
	if got, _ := first["Y"].(int8); got != -4 {
		t.Fatalf("lowest section Y = %d, want -4", got)
	}
	if got, _ := last["Y"].(int8); got != 19 {
		t.Fatalf("topmost section Y = %d, want 19", got)
	}
	if got, _ := chunk["DataVersion"].(int32); got != DataVersion26_2 {
		t.Fatalf("chunk DataVersion = %d, want %d", got, DataVersion26_2)
	}
}

// flatHeightmapLong is one long of the real server's heightmap for a
// flat column, read out of a chunk the real 26.2 server saved: seven
// columns of 4, nine bits each. The value is the ground's block Y
// relative to the world floor plus one - grass at -61 over a -64 floor -
// so it also pins the packing order.
const flatHeightmapLong = 72198606942111748

// flatHeightmapTail is the same measurement for the last long of the
// array, which holds only the four leftover columns (256 is 36 sevens
// plus four). It is a separate constant because a packer that filled the
// tail's unused lanes - or wrote the full seven-column value there -
// would still pass the loop above.
const flatHeightmapTail = 537921540

// TestChunkHeightmapsMatchVanilla packs the flat world's column heights
// and requires the real server's own long. A heightmap that is one off,
// or packed with the wrong bit width or entry order, still round-trips
// through mc-go and still loads, but draws the terrain at the wrong
// height in a client - which is why this is compared against a
// measurement rather than against a round trip.
func TestChunkHeightmapsMatchVanilla(t *testing.T) {
	const minY, height = -64, 384
	w := New(nil, 2)
	w.Start()
	defer w.Stop()

	blocks := make([]Block, 16*height*16)
	w.FillBox(Pos{X: 0, Y: minY, Z: 0}, Pos{X: 15, Y: minY + height - 1, Z: 15}, blocks)
	hm := heightmaps(blocks, height, minY)
	for _, kind := range []string{"WORLD_SURFACE", "OCEAN_FLOOR", "MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES"} {
		arr, ok := hm[kind].([]int64)
		if !ok {
			t.Fatalf("heightmap %s is %T, want a long array", kind, hm[kind])
		}
		if len(arr) != 37 {
			t.Fatalf("heightmap %s has %d longs, want 37 (256 columns, seven to a long)", kind, len(arr))
		}
		for i, v := range arr {
			want := int64(flatHeightmapLong)
			if i == len(arr)-1 {
				want = flatHeightmapTail
			}
			if v != want {
				t.Fatalf("heightmap %s long %d = %d, want %d (the real server's value for a flat column)",
					kind, i, v, want)
			}
		}
	}
}

// TestChunkLoadSkipsUnfinishedChunks pins which chunks a load trusts.
// The real server saves every chunk it had loaded, including ones it
// had not finished generating: its own file from a fresh boot holds
// chunks marked "minecraft:biomes", "minecraft:carvers" and
// "minecraft:structure_starts" whose sections are entirely air. Those
// sections describe no terrain - the real server runs them back through
// its generator - so a loader that read them as blocks would erase the
// world it was opening.
func TestChunkLoadSkipsUnfinishedChunks(t *testing.T) {
	bedrock, ok := BlockState("bedrock")
	if !ok {
		t.Fatal("no bedrock state")
	}
	// airChunk is what the real server writes for a chunk it has not
	// finished: one all-air section with no data array at all.
	airChunk := func(status string) map[string]any {
		return map[string]any{
			"Status": status,
			"sections": nbt.List{Elem: nbt.TagCompound, Items: []any{
				map[string]any{
					"Y": int8(-4),
					chunkBlockStates: map[string]any{
						"palette": nbt.List{Elem: nbt.TagCompound, Items: []any{
							map[string]any{"Name": "minecraft:air"},
						}},
					},
					"biomes": map[string]any{
						"palette": nbt.List{Elem: nbt.TagString, Items: []any{"minecraft:plains"}},
					},
				},
			}},
		}
	}

	for _, status := range []string{
		"minecraft:structure_starts",
		"minecraft:biomes",
		"minecraft:carvers",
		"minecraft:initialize_light",
		"minecraft:empty",
	} {
		w := New(nil, 2)
		w.Start()
		w.loadChunkOverrides(airChunk(status), 0, 0, -64)
		// The generator's ground survives: the bedrock floor is still
		// there rather than replaced by the placeholder's air.
		if got := w.GetBlock(Pos{X: 1, Y: -64, Z: 1}); got != bedrock {
			t.Errorf("status %q: bedrock floor = %d, want the generator's bedrock (the chunk's placeholder air was applied)", status, got)
		}
		w.Stop()
	}

	// A finished chunk is read: the same all-air section, saved as
	// full, is a real "this column is empty" and clears the floor.
	w := New(nil, 2)
	w.Start()
	defer w.Stop()
	w.loadChunkOverrides(airChunk("minecraft:full"), 0, 0, -64)
	if got := w.GetBlock(Pos{X: 1, Y: -64, Z: 1}); got != Air {
		t.Errorf("full chunk: block at 1,-64,1 = %d, want air from the file", got)
	}
}

// TestChunkPaletteCarriesBlockProperties pins what a palette entry
// holds. The real server writes every property of a block, defaults
// included - its own saved chunk spells out grass_block as
// {snowy: "false"} - and writes no properties map at all for a block
// that has none, like stone. Both halves matter: a missing property map
// reads back as the same state but is not the same file, and a
// properties map on a block that has none is a palette entry the real
// server's palette parser rejects.
func TestChunkPaletteCarriesBlockProperties(t *testing.T) {
	grass, ok := BlockState("grass_block")
	if !ok {
		t.Fatal("no grass_block state")
	}
	props := BlockProperties(grass)
	if got := props["snowy"]; got != "false" {
		t.Fatalf("grass_block snowy = %q, want the default spelled out", got)
	}
	if got := BlockProperties(Stone); len(got) != 0 {
		t.Fatalf("stone carries properties %v, want none", got)
	}
	if got := BlockProperties(Air); len(got) != 0 {
		t.Fatalf("air carries properties %v, want none", got)
	}

	w := New(nil, 2)
	w.Start()
	defer w.Stop()
	// One override is enough to make the chunk worth writing; the
	// palette under test is the generated terrain's, which the write
	// materializes alongside it.
	w.SetBlock(Pos{X: 1, Y: -64, Z: 1}, Stone)
	dir := t.TempDir()
	if err := w.SaveRegion(dir, -64, 384); err != nil {
		t.Fatal(err)
	}
	chunks, err := ReadRegionFile(regionFileName(dir, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	sections, _ := chunks[[2]int32{0, 0}]["sections"].(nbt.List)
	lowest, _ := sections.Items[0].(map[string]any)
	states, _ := lowest[chunkBlockStates].(map[string]any)
	pal, _ := states["palette"].(nbt.List)
	// propsOf records whether the properties map is there at all, not
	// just what it holds: an absent map and an empty one are different
	// palette entries.
	propsOf := map[string]map[string]any{}
	hasProps := map[string]bool{}
	for _, pAny := range pal.Items {
		entry, _ := pAny.(map[string]any)
		name, _ := entry["Name"].(string)
		pm, ok := entry["Properties"].(map[string]any)
		propsOf[name] = pm
		hasProps[name] = ok
	}
	if pm, ok := propsOf["minecraft:grass_block"]; !ok || pm["snowy"] != "false" {
		t.Errorf("saved grass_block palette entry = %v (Properties present: %v), want {snowy: false}",
			pm, hasProps["minecraft:grass_block"])
	}
	if _, ok := propsOf["minecraft:stone"]; !ok {
		t.Fatal("saved palette has no stone entry, so the check below measures nothing")
	}
	if hasProps["minecraft:stone"] {
		t.Errorf("saved stone palette entry carries %v, want no Properties map", propsOf["minecraft:stone"])
	}
}

// TestChunkPaletteNamesEveryState round-trips a block state that is not
// its block's default. A chunk palette entry is a name and a properties
// map, so a sign facing east is saved as oak_sign with rotation=1 - and
// a writer that looked a state up in a table of default states only
// found nothing, wrote the entry out as minecraft:air, and lost the
// block on the way back in.
func TestChunkPaletteNamesEveryState(t *testing.T) {
	const minY, height = -64, 384
	sign, ok := BlockStateOf("oak_sign", map[string]string{"rotation": "1", "waterlogged": "false"})
	if !ok {
		t.Fatal("no oak_sign state with rotation=1")
	}
	if def, _ := BlockState("oak_sign"); sign == def {
		t.Fatal("the state under test is oak_sign's default, so it tests nothing")
	}
	pos := Pos{X: 4, Y: -60, Z: 4}

	w := New(nil, 2)
	w.Start()
	w.SetBlock(pos, sign)
	dir := t.TempDir()
	if err := w.SaveRegion(dir, minY, height); err != nil {
		t.Fatal(err)
	}
	chunks, err := ReadRegionFile(regionFileName(dir, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	sections, _ := chunks[[2]int32{0, 0}]["sections"].(nbt.List)
	lowest, _ := sections.Items[0].(map[string]any)
	states, _ := lowest[chunkBlockStates].(map[string]any)
	pal, _ := states["palette"].(nbt.List)
	named := false
	for _, pAny := range pal.Items {
		entry, _ := pAny.(map[string]any)
		name, _ := entry["Name"].(string)
		if strings.HasPrefix(name, "minecraft:air") {
			if pm, _ := entry["Properties"].(map[string]any); pm["rotation"] == "1" {
				t.Fatalf("the sign is saved as %q, want its block's own name", name)
			}
		}
		if name != "minecraft:oak_sign" {
			continue
		}
		named = true
		if pm, _ := entry["Properties"].(map[string]any); pm["rotation"] != "1" {
			t.Errorf("saved oak_sign properties = %v, want rotation=1", entry["Properties"])
		}
	}
	if !named {
		t.Fatal("the saved palette has no oak_sign entry")
	}

	w2 := New(nil, 2)
	w2.Start()
	defer w2.Stop()
	if err := w2.LoadRegion(dir, minY, height); err != nil {
		t.Fatal(err)
	}
	if got := w2.GetBlock(pos); got != sign {
		t.Fatalf("block at %v = %d, want %d (the sign did not survive the round trip)", pos, got, sign)
	}
}

// TestChunkLoadLeavesUnknownPalettesAlone pins what happens to a section
// the reader cannot translate: a palette entry naming a block this build
// does not know. Writing the section anyway would put air wherever that
// entry stood, deleting terrain over a word mc-go failed to recognise,
// so the whole section is left to the generator instead.
func TestChunkLoadLeavesUnknownPalettesAlone(t *testing.T) {
	chunk := map[string]any{
		"Status": "minecraft:full",
		"sections": nbt.List{Elem: nbt.TagCompound, Items: []any{
			map[string]any{
				"Y": int8(-4),
				chunkBlockStates: map[string]any{
					"palette": nbt.List{Elem: nbt.TagCompound, Items: []any{
						// A block this build has never heard of, and a
						// real one carrying a property value that does
						// not exist in this version.
						map[string]any{"Name": "minecraft:not_a_block"},
						map[string]any{"Name": "minecraft:stone", "Properties": map[string]any{"not_a_property": "true"}},
					}},
				},
				"biomes": map[string]any{
					"palette": nbt.List{Elem: nbt.TagString, Items: []any{"minecraft:plains"}},
				},
			},
		}},
	}
	w := New(nil, 2)
	w.Start()
	defer w.Stop()
	w.loadChunkOverrides(chunk, 0, 0, -64)
	bedrock, _ := BlockState("bedrock")
	if got := w.GetBlock(Pos{X: 1, Y: -64, Z: 1}); got != bedrock {
		t.Errorf("block at 1,-64,1 = %d, want the generator's bedrock (an unreadable section was applied as air)", got)
	}
}

// TestSectionDataWidthFollowsPalette pins the packed width of a saved
// section: four bits at least, else the fewest bits that index the
// palette - the width a reader derives from the palette size. Twenty
// states take five bits (a 320-long array), not six.
func TestSectionDataWidthFollowsPalette(t *testing.T) {
	const minY, height = -64, 384
	blocks := make([]Block, 16*height*16)
	states := 0
	for b := Block(1); states < 19; b++ {
		if _, ok := StateOwner(b); ok {
			blocks[states] = b
			states++
		}
	}
	sec := sectionNBT(blocks, minY, 0, "minecraft:plains", nil, nil)
	st := sec[chunkBlockStates].(map[string]any)
	pal := st["palette"].(nbt.List)
	data := st["data"].([]int64)
	if len(pal.Items) != 20 || len(data) != 4096/(64/5)+1 {
		t.Fatalf("palette %d entries, data %d longs; want 20 and %d", len(pal.Items), len(data), 4096/(64/5)+1)
	}
	got := unpackIndices(data, len(pal.Items))
	for i := 0; i < 19; i++ {
		if pal.Items[got[i]].(map[string]any)["Name"] == "minecraft:air" {
			t.Fatalf("cell %d decodes to air", i)
		}
	}
}
