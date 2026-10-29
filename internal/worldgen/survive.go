package worldgen

import (
	"encoding/json"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// blockClass returns a block's behaviour class chain (most specific
// first), as measured by tools/genblockprops.
func blockClass(name string) []string {
	return strings.Split(vanillaBlockClasses[name], ",")
}

func hasClass(name, class string) bool {
	for _, c := range blockClass(name) {
		if c == class {
			return true
		}
	}
	return false
}

var fluidTagCache sync.Map // tag -> map[string]bool of fluid ids

// fluidTag resolves a fluid tag from the bundled data pack.
func fluidTag(tag string) map[string]bool {
	if v, ok := fluidTagCache.Load(tag); ok {
		return v.(map[string]bool)
	}
	set := map[string]bool{}
	var walk func(t string)
	walk = func(t string) {
		raw, ok := Vanilla().Get("tags/fluid/" + t)
		if !ok {
			panic("worldgen: unknown fluid tag " + t)
		}
		var doc struct {
			Values []string `json:"values"`
		}
		json.Unmarshal(raw, &doc)
		for _, v := range doc.Values {
			if strings.HasPrefix(v, "#") {
				walk(trimNS(v[1:]))
			} else {
				set[trimNS(v)] = true
			}
		}
	}
	walk(tag)
	fluidTagCache.Store(tag, set)
	return set
}

// fluidID names a state's fluid ("water", "flowing_water", "lava",
// "flowing_lava", "empty").
func fluidID(b world.Block) string {
	if !hasFluid(b) {
		return "empty"
	}
	src := isFluidSource(b)
	switch {
	case isWaterFluid(b) && src:
		return "water"
	case isWaterFluid(b):
		return "flowing_water"
	case src:
		return "lava"
	}
	return "flowing_lava"
}

func inFluidTag(b world.Block, tag string) bool { return fluidTag(tag)[fluidID(b)] }

var supportTags sync.Map

func blockInTag(b world.Block, tag string) bool {
	v, ok := supportTags.Load(tag)
	if !ok {
		v = blockTagSet(tag)
		supportTags.Store(tag, v)
	}
	return inTag(b, v.(map[string]bool))
}

// rawBrightness is getRawBrightness(pos, 0) during features: the
// chunks are not lit yet, so the sky engine reports full light. The
// nether has no sky engine, leaving the (unlit) block light: 0.
func rawBrightness(rg *Region, _ BlockPos) int32 {
	if rg != nil && rg.g.RS.settings.name == "nether" {
		return 0
	}
	return 15
}

// SurviveFunc is one block family's canSurvive.
type SurviveFunc func(rg *Region, state world.Block, p BlockPos) bool

// surviveByClass holds canSurvive implementations keyed by vanilla
// behaviour class; the most specific class in a block's chain wins.
// Feature files register the families they place in init().
var surviveByClass = map[string]SurviveFunc{}

// canSurvive is BlockState.canSurvive over the decorating region, for
// the block families worldgen places.
func canSurvive(rg *Region, state world.Block, p BlockPos) bool {
	name, _ := world.StateOwner(state)
	for _, c := range blockClass(name) {
		if f, ok := surviveByClass[c]; ok {
			return f(rg, state, p)
		}
		if c == "VegetationBlock" {
			break
		}
	}
	get := func(q BlockPos) world.Block { return rg.Block(q[0], q[1], q[2]) }
	below := offset(p, 0, -1, 0)
	cls := blockClass(name)
	switch cls[0] {
	case "DoublePlantBlock", "TallFlowerBlock", "TallSeagrassBlock", "SmallDripleafBlock":
		if h, _ := world.StateProperty(state, "half"); h == "upper" {
			b := get(below)
			bn, _ := world.StateOwner(b)
			bh, _ := world.StateProperty(b, "half")
			return bn == name && bh == "lower"
		}
	}
	switch cls[0] {
	case "LeafLitterBlock":
		return sturdyFull(get(below), dirUp)
	case "MushroomBlock":
		b := get(below)
		if blockInTag(b, "overrides_mushroom_light_requirement") {
			return true
		}
		return rawBrightness(rg, p) < 13 && props(b)&(1<<bpSolidRender) != 0
	case "SporeBlossomBlock":
		above := get(offset(p, 0, 1, 0))
		return sturdyCenter(above, dirDown) && !isWaterAt(get(p))
	case "LilyPadBlock":
		b := get(below)
		return (inFluidTag(b, "supports_lily_pad") || blockInTag(b, "supports_lily_pad")) && fluidID(get(p)) == "empty"
	case "MossyCarpetBlock":
		b := get(below)
		if base, _ := world.StateProperty(state, "bottom"); base == "true" {
			return !isAir(b)
		}
		bb, _ := world.StateProperty(b, "bottom")
		return isBlock(b, name) && bb == "true"
	case "CarpetBlock", "WoolCarpetBlock":
		return !isAir(get(below))
	case "SugarCaneBlock":
		b := get(below)
		if isBlock(b, name) {
			return true
		}
		if blockInTag(b, "supports_sugar_cane") {
			for _, d := range horizontalDirs {
				o := dirOffsets[d]
				n := get(offset(below, o[0], o[1], o[2]))
				if inFluidTag(n, "supports_sugar_cane_adjacently") || blockInTag(n, "supports_sugar_cane_adjacently") {
					return true
				}
			}
		}
		return false
	case "CactusBlock":
		for _, d := range horizontalDirs {
			o := dirOffsets[d]
			n := get(offset(p, o[0], o[1], o[2]))
			if isSolidState(n) || inFluidTag(n, "lava") {
				return false
			}
		}
		b := get(below)
		return (isBlock(b, name) || blockInTag(b, "supports_cactus")) && props(get(offset(p, 0, 1, 0)))&(1<<bpLiquidBlock) == 0
	case "MangrovePropaguleBlock":
		if h, _ := world.StateProperty(state, "hanging"); h == "true" {
			return blockInTag(get(offset(p, 0, 1, 0)), "supports_hanging_mangrove_propagule")
		}
		return blockInTag(get(below), "supports_mangrove_propagule")
	}
	if hasClass(name, "VegetationBlock") {
		return mayPlaceOn(name, cls, get(below))
	}
	if !surviveUnchecked[cls[0]] {
		rg.g.featMu.Lock()
		UnsupportedSurvive[cls[0]] = true
		rg.g.featMu.Unlock()
	}
	return true
}

// surviveUnchecked are classes with no canSurvive override.
var surviveUnchecked = map[string]bool{"Block": true, "PumpkinBlock": true, "RotatedPillarBlock": true, "PotentSulfurBlock": true}

// UnsupportedSurvive collects block classes whose canSurvive is not
// modeled yet (they report true).
var UnsupportedSurvive = map[string]bool{}

func isWaterAt(b world.Block) bool { return inFluidTag(b, "water") }

// mayPlaceOn is VegetationBlock.mayPlaceOn and its overrides.
func mayPlaceOn(name string, cls []string, b world.Block) bool {
	for _, c := range cls {
		switch c {
		case "DryVegetationBlock":
			return blockInTag(b, "supports_dry_vegetation")
		case "AzaleaBlock":
			return blockInTag(b, "supports_azalea")
		case "NetherRootsBlock", "NetherSproutsBlock", "FungusBlock":
			return netherRootsSupport(name, b)
		case "VegetationBlock":
			return blockInTag(b, "supports_vegetation")
		}
	}
	return blockInTag(b, "supports_vegetation")
}

func netherRootsSupport(name string, b world.Block) bool {
	return blockInTag(b, "nylium") || isBlock(b, "soul_soil") || blockInTag(b, "supports_vegetation")
}

func init() {
	featureBuilders["simple_block"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		prov := g.compileStateProvider(cfg["to_place"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			st, ok := prov(c.rg, r, p)
			if !ok {
				return false
			}
			if !canSurvive(c.rg, st, p) {
				return false
			}
			name, _ := world.StateOwner(st)
			cls := blockClass(name)[0]
			switch {
			case hasClass(name, "DoublePlantBlock"):
				if !isAir(c.rg.Block(p[0], p[1]+1, p[2])) {
					return false
				}
				placeDoublePlant(c.rg, st, p)
			case cls == "MossyCarpetBlock":
				mossyCarpetPlaceAt(c.rg, p)
			default:
				c.rg.SetBlock(p[0], p[1], p[2], st)
			}
			return true
		}
	}
}

// placeDoublePlant is DoublePlantBlock.placeAt (waterlogging copied
// from the target cells).
func placeDoublePlant(rg *Region, st world.Block, p BlockPos) {
	lower, _ := world.WithProperty(st, "half", "lower")
	upper, _ := world.WithProperty(st, "half", "upper")
	if world.HasProperty(st, "waterlogged") {
		if isWaterAt(rg.Block(p[0], p[1], p[2])) {
			lower, _ = world.WithProperty(lower, "waterlogged", "true")
		}
		if isWaterAt(rg.Block(p[0], p[1]+1, p[2])) {
			upper, _ = world.WithProperty(upper, "waterlogged", "true")
		}
	}
	rg.SetBlock(p[0], p[1], p[2], lower)
	rg.SetBlock(p[0], p[1]+1, p[2], upper)
}
