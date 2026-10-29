package worldgen

import (
	"encoding/json"
	"strings"

	"mc-go/internal/world"
)

// Structure processors beyond the feature-template set
// (StructureProcessor implementations used by structure pieces):
// rule with position predicates and block entity modifiers, capped,
// gravity, jigsaw_replacement, block_age, lava_submerged_block,
// blackstone_replace, plus the per-element processor spec that carries
// finalizeProcessing (capped) and evaluatesEntirePieceState.

// tplFinalizer is StructureProcessor.finalizeProcessing over the whole
// processed list (originals are the template-local infos).
type tplFinalizer func(rg *Region, off, pivot BlockPos, originals, processed []tplBlock, s *placeSettings) []tplBlock

// procSpec is a compiled processor list.
type procSpec struct {
	procs  []tplProcessor
	fins   []tplFinalizer
	entire bool
}

// compileProcSpec resolves a processor list (by name or inline) with
// capped processors' finalizers kept in list order.
func (g *Generator) compileProcSpec(raw json.RawMessage) procSpec {
	var spec procSpec
	if raw == nil {
		return spec
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		r, ok := g.RS.data.Get("processor_list/" + trimNS(name))
		if !ok {
			panic("worldgen: unknown processor list " + name)
		}
		raw = r
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		var o obj
		jsonUnmarshal(raw, &o)
		jsonUnmarshal(o["processors"], &list)
	}
	for _, p := range list {
		var o obj
		jsonUnmarshal(p, &o)
		if trimNS(o.str("processor_type")) == "capped" {
			spec.fins = append(spec.fins, g.cappedFinalizer(o))
			spec.entire = true
			continue
		}
		spec.procs = append(spec.procs, g.compileProcessor(p))
	}
	return spec
}

// structProcessorBuilders take precedence over compileProcessor's own
// cases.
var structProcessorBuilders = map[string]func(g *Generator, o obj) tplProcessor{
	"rule":               compileRuleProcessor,
	"gravity":            func(g *Generator, o obj) tplProcessor { return gravityProc(heightmapType(o.optStrDef("heightmap", "WORLD_SURFACE_WG")), o.optI32("offset", 0)) },
	"jigsaw_replacement": func(g *Generator, o obj) tplProcessor { return jigsawReplacementProc },
	"block_age":          func(g *Generator, o obj) tplProcessor { return blockAgeProc(parseF32(o["mossiness"])) },
	"lava_submerged_block": func(g *Generator, o obj) tplProcessor {
		return lavaSubmergedProc
	},
	"blackstone_replace": func(g *Generator, o obj) tplProcessor { return blackstoneReplaceProc },
}

func (o obj) optStrDef(k, def string) string {
	if v := o.optStr(k); v != "" {
		return v
	}
	return def
}

// ignoreBlocksProc is BlockIgnoreProcessor over block names.
func ignoreBlocksProc(set map[string]bool) tplProcessor {
	return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
		if inTag(cur.state, set) {
			return nil
		}
		return cur
	}
}

// jigsawReplacementProc is JigsawReplacementProcessor: a jigsaw block
// becomes its final_state (structure_void drops the block), losing its
// block entity data.
func jigsawReplacementProc(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
	if !isBlock(cur.state, "jigsaw") || cur.nbt == nil {
		return cur
	}
	fs, ok := cur.nbt["final_state"].(string)
	if !ok {
		fs = "minecraft:air"
	}
	st, ok := parseBlockState(fs)
	if !ok {
		return nil
	}
	if isBlock(st, "structure_void") {
		return nil
	}
	return &tplBlock{pos: cur.pos, state: st}
}

// parseBlockState is BlockStateParser.parseForBlock for
// "ns:name[k=v,...]" (unknown properties make it fail).
func parseBlockState(s string) (world.Block, bool) {
	name, rest, hasProps := strings.Cut(s, "[")
	b, ok := world.BlockStateOf(trimNS(strings.TrimSpace(name)), nil)
	if !ok {
		return world.Air, false
	}
	if hasProps {
		rest = strings.TrimSuffix(strings.TrimSpace(rest), "]")
		for _, kv := range strings.Split(rest, ",") {
			k, v, _ := strings.Cut(kv, "=")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "" {
				continue
			}
			nb, ok := world.WithProperty(b, k, v)
			if !ok {
				return world.Air, false
			}
			b = nb
		}
	}
	return b, true
}

// gravityProc is GravityProcessor in a worldgen region: the block's y
// becomes the heightmap top at its column plus offset plus its
// template-local y.
func gravityProc(hm int, off int32) tplProcessor {
	return func(rg *Region, o, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
		y := rg.Height(hm, cur.pos[0], cur.pos[2]) + off
		return &tplBlock{pos: BlockPos{cur.pos[0], y + local[1], cur.pos[2]}, state: cur.state, nbt: cur.nbt}
	}
}

// posRuleTest is a PosRuleTest(original, current, pivot, random).
type posRuleTest func(orig, cur, pivot BlockPos, r Random) bool

func clampedLerpF(delta, lo, hi float32) float32 {
	if delta < 0 {
		return lo
	}
	if delta > 1 {
		return hi
	}
	return lo + float32(delta*(hi-lo))
}

func compilePosRuleTest(raw json.RawMessage) posRuleTest {
	if raw == nil {
		return func(BlockPos, BlockPos, BlockPos, Random) bool { return true }
	}
	var o obj
	jsonUnmarshal(raw, &o)
	minC := float32(0)
	maxC := float32(0)
	if _, ok := o["min_chance"]; ok {
		minC = parseF32(o["min_chance"])
	}
	if _, ok := o["max_chance"]; ok {
		maxC = parseF32(o["max_chance"])
	}
	minD, maxD := o.optI32("min_dist", 0), o.optI32("max_dist", 0)
	chance := func(dist int32, r Random) bool {
		f := r.NextFloat()
		t := float32(float32(dist)-float32(minD)) / float32(float32(maxD)-float32(minD))
		return f <= clampedLerpF(t, minC, maxC)
	}
	switch t := trimNS(o.str("predicate_type")); t {
	case "always_true":
		return func(BlockPos, BlockPos, BlockPos, Random) bool { return true }
	case "linear_pos":
		// LinearPosTest: by manhattan distance from current to pivot.
		return func(orig, cur, pivot BlockPos, r Random) bool {
			d := absI32(cur[0]-pivot[0]) + absI32(cur[1]-pivot[1]) + absI32(cur[2]-pivot[2])
			return chance(d, r)
		}
	case "axis_aligned_linear_pos":
		axis := o.optStrDef("axis", "y")
		return func(orig, cur, pivot BlockPos, r Random) bool {
			var d int32
			switch axis {
			case "x":
				d = absI32(cur[0] - pivot[0])
			case "z":
				d = absI32(cur[2] - pivot[2])
			default:
				d = absI32(cur[1] - pivot[1])
			}
			return chance(d, r)
		}
	default:
		panic("worldgen: unknown position predicate " + t)
	}
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// blockEntityModifier is a RuleBlockEntityModifier.
type blockEntityModifier func(r Random, nbt map[string]any) map[string]any

func compileBlockEntityModifier(raw json.RawMessage) blockEntityModifier {
	if raw == nil {
		return func(_ Random, n map[string]any) map[string]any { return n }
	}
	var o obj
	jsonUnmarshal(raw, &o)
	switch t := trimNS(o.str("type")); t {
	case "passthrough":
		return func(_ Random, n map[string]any) map[string]any { return n }
	case "clear":
		return func(Random, map[string]any) map[string]any { return map[string]any{} }
	case "append_loot":
		table := o.str("loot_table")
		return func(r Random, n map[string]any) map[string]any {
			out := copyNBT(n)
			out["LootTable"] = table
			out["LootTableSeed"] = r.NextLong()
			return out
		}
	case "append_static":
		var data map[string]any
		jsonUnmarshal(o["data"], &data)
		return func(_ Random, n map[string]any) map[string]any {
			out := copyNBT(n)
			for k, v := range data {
				out[k] = v
			}
			return out
		}
	default:
		panic("worldgen: unknown block entity modifier " + t)
	}
}

func copyNBT(n map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range n {
		out[k] = v
	}
	return out
}

// compileRuleProcessor is RuleProcessor: the first rule whose input,
// location and position tests pass (one random seeded by the current
// position, drawn in that order) replaces the state and rewrites the
// block entity data.
func compileRuleProcessor(g *Generator, o obj) tplProcessor {
	var rules []obj
	jsonUnmarshal(o["rules"], &rules)
	type rule struct {
		input, loc ruleTest
		pos        posRuleTest
		out        world.Block
		mod        blockEntityModifier
	}
	var rs []rule
	for _, ru := range rules {
		var st obj
		jsonUnmarshal(ru["output_state"], &st)
		rs = append(rs, rule{compileRuleTest(ru["input_predicate"]), compileRuleTest(ru["location_predicate"]),
			compilePosRuleTest(ru["position_predicate"]), stateFromJSON(st), compileBlockEntityModifier(ru["block_entity_modifier"])})
	}
	return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
		r := NewLegacy(BlockSeed(cur.pos[0], cur.pos[1], cur.pos[2]))
		for _, ru := range rs {
			if ru.input(cur.state, r) && ru.loc(rg.at(cur.pos), r) && ru.pos(local, cur.pos, pivot, r) {
				return &tplBlock{pos: cur.pos, state: ru.out, nbt: ru.mod(r, cur.nbt)}
			}
		}
		return cur
	}
}

// cappedFinalizer is CappedProcessor.finalizeProcessing: the delegate
// applied to a shuffled prefix of the blocks until limit of them
// changed, with a random positioned at the piece offset from the
// level seed.
func (g *Generator) cappedFinalizer(o obj) tplFinalizer {
	delegate := g.compileProcessor(o["delegate"])
	limit := parseIntProvider(o["limit"])
	maxLimit := intProviderMax(o["limit"])
	seed := g.RS.Seed
	return func(rg *Region, off, pivot BlockPos, originals, processed []tplBlock, s *placeSettings) []tplBlock {
		if maxLimit == 0 || len(processed) == 0 || len(originals) != len(processed) {
			return processed
		}
		r := NewLegacy(seed).ForkPositional().At(off[0], off[1], off[2])
		n := min(limit(r), int32(len(processed)))
		if n < 1 {
			return processed
		}
		idx := make([]int32, len(processed))
		for i := range idx {
			idx[i] = int32(i)
		}
		for i := int32(len(idx)); i > 1; i-- {
			j := r.NextIntN(i)
			idx[i-1], idx[j] = idx[j], idx[i-1]
		}
		changed := int32(0)
		for _, i := range idx {
			if changed >= n {
				break
			}
			cur := processed[i]
			out := delegate(rg, off, pivot, originals[i].pos, &cur, s)
			if out != nil && !sameBlockInfo(out, &processed[i]) {
				changed++
				processed[i] = *out
			}
		}
		return processed
	}
}

func sameBlockInfo(a, b *tplBlock) bool {
	if a.pos != b.pos || a.state != b.state {
		return false
	}
	if (a.nbt == nil) != (b.nbt == nil) || len(a.nbt) != len(b.nbt) {
		return false
	}
	for k, v := range a.nbt {
		if w, ok := b.nbt[k]; !ok || !nbtEqual(v, w) {
			return false
		}
	}
	return true
}

func nbtEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !nbtEqual(v, y[k]) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !nbtEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	defer func() { recover() }()
	return a == b
}

// blockAgeProc is BlockAgeProcessor (random from the settings at the
// block's position).
func blockAgeProc(mossiness float32) tplProcessor {
	named := func(n string) world.Block { return blockNamed(n) }
	randomStairs := func(r Random, name string) world.Block {
		b := named(name)
		b, _ = world.WithProperty(b, "facing", [4]string{"north", "east", "south", "west"}[r.NextIntN(4)])
		b, _ = world.WithProperty(b, "half", [2]string{"top", "bottom"}[r.NextIntN(2)])
		return b
	}
	pick := func(r Random, plain, mossy []world.Block) world.Block {
		if r.NextFloat() < mossiness {
			return mossy[r.NextIntN(int32(len(mossy)))]
		}
		return plain[r.NextIntN(int32(len(plain)))]
	}
	return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
		r := s.randomAt(cur.pos)
		st := cur.state
		var out world.Block
		ok := false
		switch {
		case isBlock(st, "stone_bricks") || isBlock(st, "stone") || isBlock(st, "chiseled_stone_bricks"):
			if r.NextFloat() < 0.5 {
				plain := []world.Block{named("cracked_stone_bricks"), randomStairs(r, "stone_brick_stairs")}
				mossy := []world.Block{named("mossy_stone_bricks"), randomStairs(r, "mossy_stone_brick_stairs")}
				out, ok = pick(r, plain, mossy), true
			}
		case blockInTag(st, "stairs"):
			if r.NextFloat() < 0.5 {
				plain := []world.Block{named("stone_slab"), named("stone_brick_slab")}
				mossy := []world.Block{withPropertiesOf(named("mossy_stone_brick_stairs"), st), named("mossy_stone_brick_slab")}
				out, ok = pick(r, plain, mossy), true
			}
		case blockInTag(st, "slabs"):
			if r.NextFloat() < mossiness {
				out, ok = withPropertiesOf(named("mossy_stone_brick_slab"), st), true
			}
		case blockInTag(st, "walls"):
			if r.NextFloat() < mossiness {
				out, ok = withPropertiesOf(named("mossy_stone_brick_wall"), st), true
			}
		case isBlock(st, "obsidian"):
			if r.NextFloat() < 0.15 {
				out, ok = named("crying_obsidian"), true
			}
		}
		if !ok {
			return cur
		}
		return &tplBlock{pos: cur.pos, state: out, nbt: cur.nbt}
	}
}

// withPropertiesOf is Block.withPropertiesOf: the shared properties
// copied onto the default state.
func withPropertiesOf(b, from world.Block) world.Block {
	_, props, ok := world.DescribeState(from)
	if !ok {
		return b
	}
	for k, v := range props {
		if nb, ok := world.WithProperty(b, k, v); ok {
			b = nb
		}
	}
	return b
}

// lavaSubmergedProc is LavaSubmergedBlockProcessor: a non-full-block
// template state over world lava becomes lava. (Block.isShapeFullBlock
// of the outline is approximated by sturdy faces on all six sides.)
func lavaSubmergedProc(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
	if !isBlock(rg.at(cur.pos), "lava") {
		return cur
	}
	full := true
	for d := 0; d < 6; d++ {
		full = full && sturdyFull(cur.state, d)
	}
	if full {
		return cur
	}
	return &tplBlock{pos: cur.pos, state: blockNamed("lava"), nbt: cur.nbt}
}

// blackstoneReplacements is BlackstoneReplaceProcessor's block map.
var blackstoneReplacements = map[string]string{
	"cobblestone": "blackstone", "mossy_cobblestone": "blackstone", "stone": "polished_blackstone",
	"stone_bricks": "polished_blackstone_bricks", "mossy_stone_bricks": "polished_blackstone_bricks",
	"cobblestone_stairs": "blackstone_stairs", "mossy_cobblestone_stairs": "blackstone_stairs",
	"stone_stairs": "polished_blackstone_stairs", "stone_brick_stairs": "polished_blackstone_brick_stairs",
	"mossy_stone_brick_stairs": "polished_blackstone_brick_stairs", "cobblestone_slab": "blackstone_slab",
	"mossy_cobblestone_slab": "blackstone_slab", "smooth_stone_slab": "polished_blackstone_slab",
	"stone_slab": "polished_blackstone_slab", "stone_brick_slab": "polished_blackstone_brick_slab",
	"mossy_stone_brick_slab": "polished_blackstone_brick_slab", "stone_brick_wall": "polished_blackstone_brick_wall",
	"mossy_stone_brick_wall": "polished_blackstone_brick_wall", "cobblestone_wall": "blackstone_wall",
	"mossy_cobblestone_wall": "blackstone_wall", "chiseled_stone_bricks": "chiseled_polished_blackstone",
	"cracked_stone_bricks": "cracked_polished_blackstone_bricks", "iron_bars": "chain",
}

// blackstoneReplaceProc is BlackstoneReplaceProcessor: mapped blocks
// keep facing, half and slab type.
func blackstoneReplaceProc(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
	n, _ := world.StateOwner(cur.state)
	to, ok := blackstoneReplacements[n]
	if !ok {
		return cur
	}
	out := blockNamed(to)
	for _, k := range []string{"facing", "half", "type"} {
		if v, ok := world.StateProperty(cur.state, k); ok {
			if nb, ok := world.WithProperty(out, k, v); ok {
				out = nb
			}
		}
	}
	return &tplBlock{pos: cur.pos, state: out, nbt: cur.nbt}
}
