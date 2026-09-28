package server

import (
	"math"

	"mc-go/internal/world"
	"strconv"
	"strings"
)

// Tree growth — SaplingBlock.randomTick / advanceTree /
// TreeGrower.growTree with TreeFeature.doPlace for the straight-trunk
// blob-canopy species (small oak, birch). Measured against the 26.2
// jar: SaplingBlock.java, TreeGrower.java, TreeFeature.java,
// TrunkPlacer.java, StraightTrunkPlacer.java, FoliagePlacer.java,
// BlobFoliagePlacer.java, and the oak.json / birch.json configured
// features.

// canopyKind is the species' foliage_placer variant: the small oak /
// birch blob, the spruce zigzag cone, or the acacia flat umbrella.
type canopyKind int

const (
	canopyBlob canopyKind = iota
	canopySpruce
	canopyAcacia
	canopyCherry
	canopyDarkOak
	canopyMegaPine
	canopyMegaJungle
	canopySpread // random_spread_foliage_placer (radius/height/attempts)
)

// trunkKind is the species' trunk_placer variant: the straight column
// every small tree shares or the acacia forking trunk (diagonal lean
// plus an optional side branch).
type trunkKind int

const (
	trunkStraight trunkKind = iota
	trunkForking
	trunkCherry
	trunkDarkOak
	trunkGiant
	trunkMegaJungle
	trunkMangrove
	trunkBending // BendingTrunkPlacer — wobble column + full side bend (azalea)
)

// decorKind is the tree's decorator set (TreeConfiguration.decorators):
// none, the podzol alter_ground under mega spruces, the mega jungle's
// trunk+leave vines, or the pale oak's pale_moss hangers.
type decorKind int

const (
	decorNone decorKind = iota
	decorPodzol
	decorJungleVines // trunk_vine + leave_vine{0.25} (mega jungle)
	decorPaleMoss    // pale_moss{ground .8, leaves .15, trunk .4} hangers
	decorMangrove    // {leave_vine .125, attached_to_leaves propagule, beehive .01}
	decorBeehive     // beehive only at beehiveP — the BEES_005 flower variants
)

// foliageAttach is one FoliageAttachment: the cell the canopy centers
// on and the radiusOffset the placer adds to its sampled radius.
type foliageAttach struct {
	pos          world.Pos
	radiusOffset int
	doubleTrunk  bool // attachment of a 2x2 trunk — the canopy row is +1 wide
}

// treeShape is one species' single-trunk feature: a log column of
// baseHeight + intn(randA+1) + intn(randB+1), the feature-size space
// check (minR — getSizeAtHeight), and one canopy placer. Every small
// oak and birch shares foliage_placer {radius: 2, offset: 0,
// height: 3}; spruce runs SpruceFoliagePlacer {radius U(2..3),
// offset U(0..2), trunk_height U(1..2)}. mega species grow from a
// 2x2 sapling quad instead of a lone sapling.
type treeShape struct {
	log, leaf                string
	baseHeight, randA, randB int
	fancyChance              float64
	minR                     func(h, y int) int // FeatureSize.getSizeAtHeight
	trunk                    trunkKind
	canopy                   canopyKind
	jungleDecor              bool       // the {cocoa, trunk_vine, leave_vine} decorator set
	mega                     bool       // can grow from a 2x2 sapling quad (mega feature)
	noSingle                 bool       // mega-only species: a lone sapling never grows
	megaShape                *treeShape // the mega feature spec (nil → this entry is it)
	megaAlt                  *treeShape // secondaryMegaFeature (spruce's pine)
	megaAltChance            float64
	ignoreVines              bool // FeatureConfiguration.ignoreVines
	decor                    decorKind
	creaking                 bool       // pale_oak_creaking adds creaking_heart{p=1.0}
	secondary                *treeShape // getConfiguredFeature's secondaryFeature
	secondaryChance          float64
	branchStepsMax           int        // upwards_branching extra_branch_steps U{1..max}
	trunkOffMin, trunkOffMax int        // root placer trunk_offset_y U{min..max}
	crownMin, crownMax       int        // mega_pine_foliage_placer's crown_height uniform
	flowers                  *treeShape // TreeGrower flowers variant (BEES_005 specs)
	fancyFlowers             bool       // flowers on the secondary roll → fancy + beehive
	beehiveP                 float64    // decorBeehive's decorator probability
	bendMin, bendMax         int        // BendingTrunkPlacer bend_length U{min..max}
	minLeafLevel             int        // BendingTrunkPlacer min_height_for_leaves
	spreadRadius             int        // random_spread radius → ±U{0..r}-U{0..r}
	spreadHeight             int        // random_spread foliage_height → ±U{0..h-1}-U{0..h-1}
	spreadAttempts           int        // random_spread leaf_placement_attempts
	leafAlt                  string     // weighted foliage_provider's second leaf block
	leafW, leafAltW          int        // leaf provider weights
	rootedDirt               bool       // below_trunk_provider rooted_dirt (azalea)
}

// twoLayers — TwoLayersFeatureSize.getSizeAtHeight: lower below
// limit, upper at and above it.
func twoLayers(limit, lower, upper int) func(h, y int) int {
	return func(h, y int) int {
		if y < limit {
			return lower
		}
		return upper
	}
}

// threeLayers — ThreeLayersFeatureSize.getSizeAtHeight: lower below
// limit, upper from treeHeight-upperLimit, middle between.
func threeLayers(limit, upperLimit, lower, middle, upper int) func(h, y int) int {
	return func(h, y int) int {
		if y < limit {
			return lower
		}
		if y >= h-upperLimit {
			return upper
		}
		return middle
	}
}

// treeShapes maps each implemented sapling to its feature config.
// Saplings without an entry still advance their stage property (that
// half of advanceTree is species-agnostic) but their growTree finds
// no feature and fails. Oak's 10% fancy roll runs FancyTrunkPlacer +
// FancyFoliagePlacer against fancy_oak's own minsize rule. The
// two_layers entries use vanilla's codec defaults (limit 1, lower 0,
// upper 1) — a block beside the column at ground level never blocks
// growth; acacia and cherry's {upper_size 2} check the column at
// y=0 and the 5x5 from y=1.
//
// The BEES_005 flower variants — a #flowers block in the 5x3x5 around
// the sapling swaps the base feature for the same placers plus only
// the beehive decorator at .05 (oak/birch/cherry/fancy_oak
// *_bees_005). TreeGrower.getConfiguredFeature draws ONE roll for
// secondary-vs-primary before consulting hasFlowers.
var oakBeesShape = treeShape{
	log: "oak_log", leaf: "oak_leaves", baseHeight: 4, randA: 2,
	minR:  twoLayers(1, 0, 1),
	trunk: trunkStraight, canopy: canopyBlob, decor: decorBeehive, beehiveP: 0.05,
}
var birchBeesShape = treeShape{
	log: "birch_log", leaf: "birch_leaves", baseHeight: 5, randA: 2,
	minR: twoLayers(1, 0, 1), trunk: trunkStraight, canopy: canopyBlob,
	decor: decorBeehive, beehiveP: 0.05,
}
var cherryBeesShape = treeShape{
	log: "cherry_log", leaf: "cherry_leaves", baseHeight: 7, randA: 1,
	minR: twoLayers(1, 0, 2), trunk: trunkCherry, canopy: canopyCherry,
	decor: decorBeehive, beehiveP: 0.05,
}

var treeShapes = map[string]treeShape{
	"oak_sapling": {
		log: "oak_log", leaf: "oak_leaves", baseHeight: 4, randA: 2,
		fancyChance: 0.1, minR: twoLayers(1, 0, 1),
		trunk: trunkStraight, canopy: canopyBlob,
		flowers: &oakBeesShape, fancyFlowers: true,
	},
	"birch_sapling": {
		log: "birch_log", leaf: "birch_leaves", baseHeight: 5, randA: 2,
		minR: twoLayers(1, 0, 1), trunk: trunkStraight, canopy: canopyBlob,
		flowers: &birchBeesShape,
	},
	"spruce_sapling": {
		log: "spruce_log", leaf: "spruce_leaves", baseHeight: 5, randA: 2, randB: 1,
		minR: twoLayers(2, 0, 2), trunk: trunkStraight, canopy: canopySpruce,
		// SpruceTreeGrower: a 2x2 quad takes the mega feature, with
		// secondaryMegaTree MEGA_PINE rolled at 50% against
		// MEGA_SPRUCE; the lone sapling always takes `spruce`.
		mega: true, megaShape: &megaSpruceShape, megaAlt: &megaPineShape, megaAltChance: 0.5,
	},
	"jungle_sapling": {
		log: "jungle_log", leaf: "jungle_leaves", baseHeight: 4, randA: 8,
		minR: twoLayers(1, 0, 1), trunk: trunkStraight, canopy: canopyBlob,
		// JungleTreeGrower: lone sapling → jungle_tree_no_vine (this
		// entry); a 2x2 quad → mega_jungle_tree.
		mega: true, megaShape: &megaJungleShape,
	},
	"acacia_sapling": {
		log: "acacia_log", leaf: "acacia_leaves", baseHeight: 5, randA: 2, randB: 2,
		minR: twoLayers(1, 0, 2), trunk: trunkForking, canopy: canopyAcacia,
	},
	"cherry_sapling": {
		log: "cherry_log", leaf: "cherry_leaves", baseHeight: 7, randA: 1,
		minR: twoLayers(1, 0, 2), trunk: trunkCherry, canopy: canopyCherry,
		flowers: &cherryBeesShape,
	},
	// dark_oak.json: base 6 + intn(3) + intn(2) → 6..10; three_layers
	// {limit 1, upper_limit 1, lower 0, middle 1, upper 2} — column
	// at y=0, 3x3 middle, 5x5 top two layers; DarkOakTrunkPlacer's
	// leaning 2x2 with fringe stems + DarkOakFoliagePlacer. Mega only —
	// a lone dark_oak sapling never grows.
	"dark_oak_sapling": {
		log: "dark_oak_log", leaf: "dark_oak_leaves", baseHeight: 6, randA: 2, randB: 1,
		minR: threeLayers(1, 1, 0, 1, 2), trunk: trunkDarkOak, canopy: canopyDarkOak,
		mega: true, noSingle: true,
	},
	// PaleOakTreeGrower's mega feature is pale_oak_bonemeal — the same
	// dark-oak placers on pale names, ignore_vines, and NO decorators
	// (the pale_moss set belongs to the pale_oak placed feature).
	"mangrove_propagule": mangroveShape,
	// Azalea bushes grow via bonemeal into the same azalea_tree
	// feature — TreeGrower.AZALEA has no flowers/mega/secondary
	// variants.
	"azalea":           azaleaShape,
	"flowering_azalea": azaleaShape,
	"pale_oak_sapling": {
		log: "pale_oak_log", leaf: "pale_oak_leaves", baseHeight: 6, randA: 2, randB: 1,
		minR: threeLayers(1, 1, 0, 1, 2), trunk: trunkDarkOak, canopy: canopyDarkOak,
		mega: true, noSingle: true, ignoreVines: true,
	},
}

// megaSpruceShape — mega_spruce.json: giant trunk {13, intn(3),
// intn(15)} → 13..29, mega_pine_foliage crown U{13..17}, podzol
// alter_ground, two_layers {lower 1, upper 2} → 3x3 at y=0, 5x5 up.
var megaSpruceShape = treeShape{
	log: "spruce_log", leaf: "spruce_leaves", baseHeight: 13, randA: 2, randB: 14,
	minR: twoLayers(1, 1, 2), trunk: trunkGiant, canopy: canopyMegaPine,
	crownMin: 13, crownMax: 17, decor: decorPodzol,
}

// megaPineShape — mega_pine.json: same giant trunk, crown U{3..7}.
var megaPineShape = treeShape{
	log: "spruce_log", leaf: "spruce_leaves", baseHeight: 13, randA: 2, randB: 14,
	minR: twoLayers(1, 1, 2), trunk: trunkGiant, canopy: canopyMegaPine,
	crownMin: 3, crownMax: 7, decor: decorPodzol,
}

// megaJungleShape — mega_jungle_tree.json: mega_jungle trunk
// {10, intn(3), intn(20)} → 10..31 (giant column + angled branches),
// mega_jungle_foliage {radius 2, offset 0, height 2}, trunk_vine +
// leave_vine{0.25} decorators, two_layers {lower 1, upper 2}.
var megaJungleShape = treeShape{
	log: "jungle_log", leaf: "jungle_leaves", baseHeight: 10, randA: 2, randB: 19,
	minR: twoLayers(1, 1, 2), trunk: trunkMegaJungle, canopy: canopyMegaJungle,
	decor: decorJungleVines,
}

// paleOakShape — pale_oak_bonemeal.json (the sapling-grown variant):
// dark-oak placers, no decorators, ignore_vines.
var paleOakShape = treeShape{
	log: "pale_oak_log", leaf: "pale_oak_leaves", baseHeight: 6, randA: 2, randB: 1,
	minR: threeLayers(1, 1, 0, 1, 2), trunk: trunkDarkOak, canopy: canopyDarkOak,
	ignoreVines: true,
}

// paleOakMossShape — pale_oak.json (the placed feature): the
// bonemeal spec plus the pale_moss decorator set.
var paleOakMossShape = treeShape{
	log: "pale_oak_log", leaf: "pale_oak_leaves", baseHeight: 6, randA: 2, randB: 1,
	minR: threeLayers(1, 1, 0, 1, 2), trunk: trunkDarkOak, canopy: canopyDarkOak,
	ignoreVines: true, decor: decorPaleMoss,
}

// paleOakCreakingShape — pale_oak_creaking.json: the planted spec plus
// the creaking_heart decorator (p=1.0) on top of pale_moss.
var paleOakCreakingShape = treeShape{
	log: "pale_oak_log", leaf: "pale_oak_leaves", baseHeight: 6, randA: 2, randB: 1,
	minR: threeLayers(1, 1, 0, 1, 2), trunk: trunkDarkOak, canopy: canopyDarkOak,
	ignoreVines: true, decor: decorPaleMoss, creaking: true,
}

// tallMangroveShape — tall_mangrove.json: the stilt trunk raises 3..7,
// the column runs 4+U{0..1}+U{0..9} and branches step U{1..6}.
var tallMangroveShape = treeShape{
	log: "mangrove_log", leaf: "mangrove_leaves", baseHeight: 4, randA: 1, randB: 9,
	minR: twoLayers(3, 0, 2), trunk: trunkMangrove, canopy: canopySpread,
	branchStepsMax: 6, trunkOffMin: 3, trunkOffMax: 7,
	spreadRadius: 2, spreadHeight: 2, spreadAttempts: 70,
	decor: decorMangrove,
}

// mangroveShape — mangrove.json: stilt offset 1..3, column
// 2+U{0..1}+U{0..4}, branches step U{1..4}. A propagule's 0.85
// secondary roll replaces this spec with the tall one.
var mangroveShape = treeShape{
	log: "mangrove_log", leaf: "mangrove_leaves", baseHeight: 2, randA: 1, randB: 4,
	minR: twoLayers(2, 0, 2), trunk: trunkMangrove, canopy: canopySpread,
	branchStepsMax: 4, trunkOffMin: 1, trunkOffMax: 3,
	spreadRadius: 2, spreadHeight: 2, spreadAttempts: 70,
	decor:     decorMangrove,
	secondary: &tallMangroveShape, secondaryChance: 0.85,
}

// azaleaShape — azalea_tree.json: BendingTrunkPlacer{base 4, randA 2,
// bend_length U{1..2}, min_height_for_leaves 3} +
// RandomSpreadFoliagePlacer{radius 3, offset 0, height 2,
// attempts 50}, weighted foliage azalea_leaves 3 : flowering 1, and
// below_trunk_provider rooted_dirt. Azalea bushes have no stage —
// they grow on bonemeal alone.
var azaleaShape = treeShape{
	log: "oak_log", leaf: "azalea_leaves",
	leafAlt: "flowering_azalea_leaves", leafW: 3, leafAltW: 1,
	baseHeight: 4, randA: 2,
	minR:    twoLayers(1, 0, 1),
	trunk:   trunkBending,
	canopy:  canopySpread,
	bendMin: 1, bendMax: 2,
	minLeafLevel: 3,
	spreadRadius: 3, spreadHeight: 2, spreadAttempts: 50,
	rootedDirt: true,
}

// saplingRule — SaplingBlock.randomTick: getMaxLocalRawBrightness of
// the cell above at least 9 (raw sky minus skyDarken, or the block
// light grid — daytime open sky is the bright case) and a 1-in-7 roll.
func saplingRule(s *Server, d *dimension, pos world.Pos, blk world.Block) {
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if s.saplingLight(d, above) < 9 || s.rng.intn(7) != 0 {
		return
	}
	s.advanceTree(d, pos, blk)
}

// saplingLight is getMaxLocalRawBrightness: the brighter of the
// block-light grid cell and the propagated sky value minus skyDarken.
// The sky half reads the engine's BFS grid, so a cell shaded by a
// single floating block still floods in at ~14 from the open columns
// beside it — vanilla's propagation, which a line-of-sight check
// cannot see. skyDarken is the unweathered reading: 0 by day, 4 by
// night — a 15 cell still clears the 9 the rule needs in the dark.
func (s *Server) saplingLight(d *dimension, pos world.Pos) int {
	v := int(d.blockLight[pos])
	if d.skylight {
		if sky := skyEffective(d, pos) - s.skyDarken(); sky > v {
			v = sky
		}
	}
	return v
}

// skyDarken is vanilla's ambient darkness: 0 through the day and 4
// through the night — the flat edges of the real celestial-angle
// curve, with its dawn/dusk ramp folded into the ends. Weather adds
// on top in vanilla and is unmodelled here.
func (s *Server) skyDarken() int {
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 13000 && dt < 23000 {
		return 4
	}
	return 0
}

// advanceTree — SaplingBlock.advanceTree: stage 0 cycles to 1, stage 1
// runs the tree feature. The stage write is silent in vanilla (flags
// 260 — no neighbour updates); setSilentBlock mirrors that.
func (s *Server) advanceTree(d *dimension, pos world.Pos, blk world.Block) {
	if st, ok := world.StateProperty(blk, "stage"); ok && st == "0" {
		if nb, ok := world.WithProperty(blk, "stage", "1"); ok {
			s.setSilentBlock(d, pos, nb)
		}
		return
	}
	s.growTree(d, pos, blk)
}

// growTree — TreeGrower.growTree: pick the species' feature (oak
// rolls fancy at 10%), clear the sapling, run the feature and restore
// the sapling on a failed place — all inside TreeGrower, before
// TreeFeature.doPlace ever sees the world.
func (s *Server) growTree(d *dimension, pos world.Pos, blk world.Block) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	shape, ok := treeShapes[strings.TrimPrefix(name, "minecraft:")]
	if !ok {
		return
	}
	// The mega feature wins over the single-tree feature: a 2x2 quad
	// of matching saplings takes the mega path (and even a failed quad
	// growth is terminal — vanilla never falls through to the single
	// feature once a quad matched). Mega-only species never reach the
	// single path.
	if shape.mega {
		ms := &shape
		if shape.megaShape != nil {
			ms = shape.megaShape
		}
		// getConfiguredMegaFeature: secondaryMegaTree replaces the
		// mega feature at secondaryChance — spruce's 50% pine roll.
		if shape.megaAlt != nil && s.rng.nextDouble() < shape.megaAltChance {
			ms = shape.megaAlt
		}
		if matched, _ := s.quadGrow(d, pos, blk, *ms); matched {
			return
		}
		if shape.noSingle {
			return
		}
	}
	// TreeGrower.getConfiguredFeature(random, hasFlowers) draws ONE
	// roll: below secondaryChance the secondary spec wins (oak's
	// secondary is the fancy feature, mangrove's tall_mangrove —
	// neither species has a secondaryFlowers variant); otherwise the
	// primary spec, swapped for its BEES_005 variant when hasFlowers
	// found a #flowers block in the 5x3x5 around the sapling.
	flowery := s.treeHasFlowers(d, pos)
	roll := s.rng.nextDouble()

	// growTree clears the sapling before the feature sees the world,
	// so the origin cell reads air for the whole place.
	s.setSilentBlock(d, pos, world.Air)
	var grew bool
	switch {
	case shape.secondary != nil && roll < shape.secondaryChance:
		grew = s.treeFeature(d, pos, *shape.secondary)
	case roll < shape.fancyChance:
		grew = s.fancyOakFeature(d, pos, flowery && shape.fancyFlowers)
	case flowery && shape.flowers != nil:
		grew = s.treeFeature(d, pos, *shape.flowers)
	default:
		grew = s.treeFeature(d, pos, shape)
	}
	if !grew {
		s.setSilentBlock(d, pos, blk)
	}
}

// treeHasFlowers — TreeGrower.hasFlowers: a #flowers block anywhere
// in the 5x3x5 from (x-2,y-1,z-2) to (x+2,y+1,z+2) around the
// sapling.
func (s *Server) treeHasFlowers(d *dimension, pos world.Pos) bool {
	for dx := -2; dx <= 2; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -2; dz <= 2; dz++ {
				bn, _ := world.StateOwner(d.w.GetBlock(world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}))
				if flowersTag[strings.TrimPrefix(bn, "minecraft:")] {
					return true
				}
			}
		}
	}
	return false
}

// flowersTag — #minecraft:flowers: #small_flowers plus the tall
// two-block flowers and the exotic members (flowering azalea,
// propagule, chorus flower, ...).
var flowersTag = map[string]bool{
	"dandelion": true, "open_eyeblossom": true, "poppy": true,
	"blue_orchid": true, "allium": true, "azure_bluet": true,
	"red_tulip": true, "orange_tulip": true, "white_tulip": true,
	"pink_tulip": true, "oxeye_daisy": true, "cornflower": true,
	"lily_of_the_valley": true, "wither_rose": true, "torchflower": true,
	"closed_eyeblossom": true, "golden_dandelion": true,
	"sunflower": true, "lilac": true, "peony": true, "rose_bush": true,
	"pitcher_plant": true, "flowering_azalea_leaves": true,
	"flowering_azalea": true, "mangrove_propagule": true,
	"cherry_leaves": true, "pink_petals": true, "wildflowers": true,
	"chorus_flower": true, "spore_blossom": true, "cactus_flower": true,
}

// quadGrow — TreeGrower.growTree's mega branch: scan the four
// candidate 2x2 quads the ticked sapling can anchor (dx,dz in
// {0,-1}, first match wins), clear the quad, run the mega feature
// at its NW corner, restore the saplings when the place fails. The
// first return reports whether a quad matched at all — a failed
// quad is still terminal for growTree.
func (s *Server) quadGrow(d *dimension, pos world.Pos, blk world.Block, shape treeShape) (bool, bool) {
	name, _ := world.StateOwner(blk)
	for dx := 0; dx >= -1; dx-- {
		for dz := 0; dz >= -1; dz-- {
			o := world.Pos{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}
			quad := [4]world.Pos{
				o,
				{X: o.X + 1, Y: o.Y, Z: o.Z},
				{X: o.X, Y: o.Y, Z: o.Z + 1},
				{X: o.X + 1, Y: o.Y, Z: o.Z + 1},
			}
			allSame := true
			for _, c := range quad {
				if cn, _ := world.StateOwner(d.w.GetBlock(c)); cn != name {
					allSame = false
					break
				}
			}
			if !allSame {
				continue
			}
			for _, c := range quad {
				s.setSilentBlock(d, c, world.Air)
			}
			if s.treeFeature(d, o, shape) {
				return true, true
			}
			for _, c := range quad {
				s.setSilentBlock(d, c, blk)
			}
			return true, false
		}
	}
	return false, false
}

// placeFeatureName maps /place feature ids onto the implemented tree
// features and runs doPlace at pos. Every unimplemented feature fails
// like a failed place() — the configured-feature set on the wire
// already includes the whole vanilla registry, so unsupported names
// report "Failed to place feature" the same way vanilla reports a
// real placement that could not run.
func (s *Server) placeFeatureName(d *dimension, name string, pos world.Pos) bool {
	switch name {
	case "fancy_oak":
		return s.fancyOakFeature(d, pos, false)
	case "fancy_oak_bees_005":
		return s.fancyOakFeature(d, pos, true)
	case "azalea_tree":
		return s.treeFeature(d, pos, azaleaShape)
	case "oak", "birch", "spruce", "acacia", "cherry", "dark_oak":
		return s.treeFeature(d, pos, treeShapes[name+"_sapling"])
	case "oak_bees_005":
		return s.treeFeature(d, pos, oakBeesShape)
	case "birch_bees_005":
		return s.treeFeature(d, pos, birchBeesShape)
	case "cherry_bees_005":
		return s.treeFeature(d, pos, cherryBeesShape)
	case "mega_spruce":
		return s.treeFeature(d, pos, megaSpruceShape)
	case "mega_pine":
		return s.treeFeature(d, pos, megaPineShape)
	case "mega_jungle_tree":
		return s.treeFeature(d, pos, megaJungleShape)
	case "pale_oak":
		return s.treeFeature(d, pos, paleOakMossShape)
	case "pale_oak_creaking":
		return s.treeFeature(d, pos, paleOakCreakingShape)
	case "mangrove":
		return s.treeFeature(d, pos, mangroveShape)
	case "tall_mangrove":
		return s.treeFeature(d, pos, tallMangroveShape)
	case "pale_oak_bonemeal":
		return s.treeFeature(d, pos, paleOakShape)
	case "jungle_tree_no_vine":
		return s.treeFeature(d, pos, treeShapes["jungle_sapling"])
	case "jungle_tree":
		// jungle_tree shares no_vine's trunk and canopy and adds the
		// {cocoa, trunk_vine, leave_vine} decorator set.
		shape := treeShapes["jungle_sapling"]
		shape.jungleDecor = true
		return s.treeFeature(d, pos, shape)
	}
	return false
}

// treeFeature — TreeFeature.doPlace for the small-tree features:
// sample the trunk height, gate on bounds and the two-layers space
// check, write dirt under the trunk, then trunk, canopy, decorators
// and the updateLeaves distance pass. false when place() fails — the
// caller decides whether a sapling comes back.
func (s *Server) treeFeature(d *dimension, pos world.Pos, shape treeShape) bool {
	treeHeight := shape.baseHeight + s.rng.intn(shape.randA+1) + s.rng.intn(shape.randB+1)

	// doPlace's bounds gate: the feature refuses to run when the
	// trunk's own origin or its top would sit outside the world's
	// buildable range.
	if pos.Y < d.minY+1 || pos.Y+treeHeight+1 > d.minY+d.height {
		return false
	}

	// getMaxFreeTreeHeight with the shape's feature size:
	// getSizeAtHeight maps each layer to its check radius — oak/birch
	// check the trunk column at y=0 and the whole 3x3 from y=1 up;
	// spruce widens to the whole 5x5 from y=2 up; dark oak runs
	// column/3x3/5x5-top via three_layers.
	clipped := treeHeight
	for y := 0; y <= treeHeight+1; y++ {
		if !s.treeSpaceFree(d, pos, y, shape.minR(treeHeight, y), shape.ignoreVines) {
			clipped = y - 2
			break
		}
	}
	if clipped < treeHeight {
		return false
	}

	trunkPos := pos
	if shape.trunk == trunkMangrove {
		// MangroveRootPlacer.getTrunkOrigin: the trunk base raises
		// trunk_offset_y above the feature origin, and placeRoots
		// runs before the trunk — a failed root simulation is a
		// failed tree.
		off := shape.trunkOffMin + s.rng.intn(shape.trunkOffMax-shape.trunkOffMin+1)
		trunkPos = world.Pos{X: pos.X, Y: pos.Y + off, Z: pos.Z}
		if !s.mangroveRoots(d, pos, trunkPos) {
			return false
		}
	} else {
		// placeBelowTrunkBlock: the provider writes dirt under the trunk
		// when the below block isn't already in
		// cannot_replace_below_tree_trunk — grass/mycelium/regular blocks
		// under a tree turn to dirt. UpwardsBranchingTrunkPlacer never
		// calls it, so the mangrove path writes no substrate; azalea's
		// below_trunk_provider is rooted_dirt instead of dirt.
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
			substrate := "dirt"
			if shape.rootedDirt {
				substrate = "rooted_dirt"
			}
			if dirt, ok := world.BlockStateOf(substrate, nil); ok {
				s.setNaturalBlock(d, below, dirt)
			}
		}
	}

	// placeTrunk: each trunk cell only writes when validTreePos — a
	// log (free for space but not a placement target) truncates the
	// visible column without failing the tree. The written cells are
	// collected — the decorators' context.logs() reads them — and the
	// trunk hands back its foliage attachments.
	logState, _ := world.BlockStateOf(shape.log, map[string]string{"axis": "y"})
	var logs []world.Pos
	var attachments []foliageAttach
	switch shape.trunk {
	case trunkForking:
		attachments = s.forkingTrunk(d, pos, treeHeight, logState, &logs)
	case trunkCherry:
		attachments = s.cherryTrunk(d, pos, treeHeight, logState, &logs)
	case trunkDarkOak:
		attachments = s.darkOakTrunk(d, pos, treeHeight, logState, &logs)
	case trunkGiant:
		attachments = s.giantTrunk(d, pos, treeHeight, logState, &logs)
	case trunkMegaJungle:
		attachments = s.megaJungleTrunk(d, pos, treeHeight, logState, &logs)
	case trunkMangrove:
		attachments = s.mangroveTrunk(d, trunkPos, treeHeight, shape, logState, &logs)
	case trunkBending:
		attachments = s.bendingTrunk(d, trunkPos, treeHeight, shape, logState, &logs)
	default:
		for y := 0; y < treeHeight; y++ {
			p := world.Pos{X: pos.X, Y: pos.Y + y, Z: pos.Z}
			if s.treeCanOccupy(d, p) {
				s.setNaturalBlock(d, p, logState)
				logs = append(logs, p)
			}
		}
		attachments = []foliageAttach{
			{pos: world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z}, radiusOffset: 0},
		}
	}

	// createFoliage: the attachment sits one above the top log.
	top := world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z}
	var leaves []world.Pos
	leafState, _ := world.BlockStateOf(shape.leaf, map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	var leafAltState world.Block
	if shape.leafAlt != "" {
		leafAltState, _ = world.BlockStateOf(shape.leafAlt, map[string]string{
			"distance": "7", "persistent": "false", "waterlogged": "false",
		})
	}
	placeLeaf := func(p world.Pos) {
		ls := leafState
		if shape.leafAlt != "" && s.rng.intn(shape.leafW+shape.leafAltW) >= shape.leafW {
			ls = leafAltState
		}
		s.placeTreeLeaf(d, p, ls, &leaves)
	}
	switch shape.canopy {
	case canopySpruce:
		s.spruceCanopy(d, top, treeHeight, placeLeaf)
	case canopyAcacia:
		s.acaciaCanopy(d, attachments, placeLeaf)
	case canopyCherry:
		s.cherryCanopy(d, attachments, leafState, &leaves)
	case canopyDarkOak:
		s.darkOakCanopy(d, attachments, placeLeaf)
	case canopyMegaPine:
		s.megaPineCanopy(d, attachments, shape, placeLeaf)
	case canopyMegaJungle:
		s.megaJungleCanopy(d, attachments, placeLeaf)
	case canopySpread:
		s.randomSpreadCanopy(d, attachments, shape, placeLeaf)
	default:
		// blob (radius 2, offset 0, height 3): four layers at yo
		// 0..-3 with radius max(2 - 1 - yo/2, 0) → 1,1,2,2 (Java's
		// yo/2 truncates toward zero; Go's integer division does too).
		for yo := 0; yo >= -3; yo-- {
			r := 2 - 1 - yo/2
			if r < 0 {
				r = 0
			}
			for dx := -r; dx <= r; dx++ {
				for dz := -r; dz <= r; dz++ {
					// shouldSkipLocation: the corner of the square,
					// always skipped at yo=0 and half the time elsewhere.
					if absi(dx) == r && absi(dz) == r && (s.rng.intn(2) == 0 || yo == 0) {
						continue
					}
					placeLeaf(world.Pos{X: top.X + dx, Y: top.Y + yo, Z: top.Z + dz})
				}
			}
		}
	}

	// Tree decorators run after foliage placement and before
	// updateLeaves. The set exists for jungle_tree-shaped features —
	// vanilla's own 1x1 jungle sapling grows jungle_tree_no_vine
	// (decorators: []) so no shape entry currently sets the flag; it
	// is wired for the 2x2 mega_jungle path (trunk_vine + leave_vine).
	if shape.jungleDecor {
		s.jungleDecorators(d, logs, leaves)
	}
	switch shape.decor {
	case decorPodzol:
		s.podzolDecor(d, logs)
	case decorJungleVines:
		s.jungleVineDecorators(d, logs, leaves)
	case decorPaleMoss:
		s.paleMossDecor(d, logs, leaves)
		if shape.creaking {
			s.creakingHeartDecor(d, logs)
		}
	case decorMangrove:
		s.mangroveDecorators(d, logs, leaves)
	case decorBeehive:
		s.beehiveDecor(d, logs, leaves, shape.beehiveP)
	}

	// updateLeaves: BFS from every written log assigns
	// DISTANCE=min(neighbour+1, 7) on the leaf cells at once — the same
	// result the leaf distance ticks would converge to over ticks.
	s.treeLeafDistances(d, logs, leaves)
	return true
}

// cherryTrunk — CherryTrunkPlacer.placeTrunk: a straight column whose
// height depends on the rolled branch count, plus one or two side
// branches that step off the column and random-walk to their end
// cell. cherry.json: branch_start U{-4..-3} for the first branch and
// U{-4..-4} for the second (bumped past the first on collision),
// branch_end U{-1..0}, horizontal length U{2..4}, and a weighted
// 1/2/3 branch count — 3 branches adds a top-of-trunk attachment.
func (s *Server) cherryTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	first := max(0, treeHeight-5+s.rng.intn(2))
	second := max(0, treeHeight-5)
	if second >= first {
		second = first + 1
	}
	count := 1 + s.rng.intn(3)
	var trunkHeight int
	switch {
	case count == 3:
		trunkHeight = treeHeight
	case count == 2:
		trunkHeight = max(first, second) + 1
	default:
		trunkHeight = first + 1
	}
	for y := 0; y < trunkHeight; y++ {
		p := world.Pos{X: pos.X, Y: pos.Y + y, Z: pos.Z}
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			*logs = append(*logs, p)
		}
	}
	var attachments []foliageAttach
	if count == 3 {
		attachments = append(attachments, foliageAttach{
			pos: world.Pos{X: pos.X, Y: pos.Y + trunkHeight, Z: pos.Z},
		})
	}
	dir := horizDirs[s.rng.intn(4)]
	attachments = append(attachments, s.cherryBranch(d, pos, treeHeight,
		logState, dir, first, first < trunkHeight-1, logs))
	if count >= 2 {
		opp := world.Pos{X: -dir.X, Y: -dir.Y, Z: -dir.Z}
		attachments = append(attachments, s.cherryBranch(d, pos, treeHeight,
			logState, opp, second, second < trunkHeight-1, logs))
	}
	return attachments
}

// cherryBranch — CherryTrunkPlacer.generateBranch: one or two
// horizontal steps off the trunk cell at the branch offset, then a
// random walk to the end cell — each step goes vertical with
// probability |dy|/manhattan, horizontal otherwise; vertical cells
// keep axis y, horizontal cells take the branch direction's axis.
func (s *Server) cherryBranch(d *dimension, pos world.Pos, treeHeight int, logState world.Block, dir world.Pos, offset int, middleUp bool, logs *[]world.Pos) foliageAttach {
	lp := world.Pos{X: pos.X, Y: pos.Y + offset, Z: pos.Z}
	endOffset := treeHeight - 1 - s.rng.intn(2)
	extend := middleUp || endOffset < offset
	dist := 2 + s.rng.intn(3)
	if extend {
		dist++
	}
	end := world.Pos{
		X: pos.X + dir.X*dist,
		Y: pos.Y + endOffset,
		Z: pos.Z + dir.Z*dist,
	}
	axis := "x"
	if dir.X == 0 {
		axis = "z"
	}
	dirLog, _ := world.WithProperty(logState, "axis", axis)
	place := func(p world.Pos, st world.Block) {
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, st)
			*logs = append(*logs, p)
		}
	}
	steps := 1
	if extend {
		steps = 2
	}
	for i := 0; i < steps; i++ {
		lp = world.Pos{X: lp.X + dir.X, Y: lp.Y, Z: lp.Z + dir.Z}
		place(lp, dirLog)
	}
	vertDir := -1
	if end.Y > lp.Y {
		vertDir = 1
	}
	for {
		dman := absi(end.X-lp.X) + absi(end.Y-lp.Y) + absi(end.Z-lp.Z)
		if dman == 0 {
			return foliageAttach{pos: world.Pos{X: end.X, Y: end.Y + 1, Z: end.Z}}
		}
		if s.rng.nextDouble() < float64(absi(end.Y-lp.Y))/float64(dman) {
			lp = world.Pos{X: lp.X, Y: lp.Y + vertDir, Z: lp.Z}
			place(lp, logState)
		} else {
			lp = world.Pos{X: lp.X + dir.X, Y: lp.Y, Z: lp.Z + dir.Z}
			place(lp, dirLog)
		}
	}
}

// cherryCanopy — CherryFoliagePlacer {radius 4, offset 0, height 5,
// wide_bottom .25, corner .25, hanging .1667, hang_ext .3334}:
// currentRadius = 4+0-1 = 3; square rows (r=1, y=2), (r=2, y=1),
// (r=3, y=0), then the two hanging rows (r=3, y=-1) and (r=2, y=-2),
// each of which can also drop a leaf or two below its perimeter.
func (s *Server) cherryCanopy(d *dimension, attachments []foliageAttach, leafState world.Block, leaves *[]world.Pos) {
	placed := map[world.Pos]bool{}
	for _, at := range attachments {
		fp := at.pos
		s.cherryRow(d, fp, 1, 2, leafState, leaves, placed)
		s.cherryRow(d, fp, 2, 1, leafState, leaves, placed)
		s.cherryRow(d, fp, 3, 0, leafState, leaves, placed)
		s.cherryHangRow(d, fp, 3, -1, leafState, leaves, placed)
		s.cherryHangRow(d, fp, 2, -2, leafState, leaves, placed)
	}
}

// cherryRow is placeLeavesRow with CherryFoliagePlacer's
// shouldSkipLocation: on the wide bottom row (y=-1) every edge cell
// holes out at .25, corners always hole on r>2 with the diag pairs
// also .25, and narrow rows hole only true corners at .25.
func (s *Server) cherryRow(d *dimension, origin world.Pos, r, y int, leafState world.Block, leaves *[]world.Pos, placed map[world.Pos]bool) {
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			ax, az := absi(dx), absi(dz)
			var skip bool
			if y == -1 && (ax == r || az == r) && s.rng.nextDouble() < 0.25 {
				skip = true
			} else {
				corner := ax == r && az == r
				if r > 2 {
					skip = corner || ax+az > 2*r-2 && s.rng.nextDouble() < 0.25
				} else {
					skip = corner && s.rng.nextDouble() < 0.25
				}
			}
			if skip {
				continue
			}
			s.cherryLeaf(d, world.Pos{X: origin.X + dx, Y: origin.Y + y, Z: origin.Z + dz}, leafState, leaves, placed)
		}
	}
}

// cherryHangRow is placeLeavesRowWithHangingLeavesBelow: the row,
// then a walk around its perimeter — under every cell whose own
// foliage landed, a hanging leaf appears at 1/6 and one more below
// that at 1/3, each only within Manhattan 7 of the cell under the
// attachment.
func (s *Server) cherryHangRow(d *dimension, origin world.Pos, r, y int, leafState world.Block, leaves *[]world.Pos, placed map[world.Pos]bool) {
	s.cherryRow(d, origin, r, y, leafState, leaves, placed)
	logPos := world.Pos{X: origin.X, Y: origin.Y - 1, Z: origin.Z}
	for _, along := range horizDirs {
		toEdge := world.Pos{X: -along.Z, Y: 0, Z: along.X} // clockwise
		c := world.Pos{
			X: origin.X + toEdge.X*r - along.X*r,
			Y: origin.Y + y - 1,
			Z: origin.Z + toEdge.Z*r - along.Z*r,
		}
		for off := -r; off < r; off++ {
			above := world.Pos{X: c.X, Y: c.Y + 1, Z: c.Z}
			if placed[above] && s.cherryExtend(d, c, logPos, 1.0/6.0, leafState, leaves, placed) {
				below := world.Pos{X: c.X, Y: c.Y - 1, Z: c.Z}
				s.cherryExtend(d, below, logPos, 1.0/3.0, leafState, leaves, placed)
			}
			c = world.Pos{X: c.X + along.X, Y: c.Y, Z: c.Z + along.Z}
		}
	}
}

// cherryExtend is tryPlaceExtension: the drop lands only inside the
// Manhattan-7 bubble and on the chance roll.
func (s *Server) cherryExtend(d *dimension, p, logPos world.Pos, chance float64, leafState world.Block, leaves *[]world.Pos, placed map[world.Pos]bool) bool {
	if absi(p.X-logPos.X)+absi(p.Y-logPos.Y)+absi(p.Z-logPos.Z) >= 7 {
		return false
	}
	if s.rng.nextDouble() >= chance {
		return false
	}
	s.cherryLeaf(d, p, leafState, leaves, placed)
	return true
}

// cherryLeaf wraps placeTreeLeaf and records which cells the setter
// actually wrote — the hanging rows only drop below a cell the
// foliage pass already filled.
func (s *Server) cherryLeaf(d *dimension, p world.Pos, leafState world.Block, leaves *[]world.Pos, placed map[world.Pos]bool) {
	before := len(*leaves)
	s.placeTreeLeaf(d, p, leafState, leaves)
	if len(*leaves) > before {
		placed[p] = true
	}
}

// darkOakTrunk — DarkOakTrunkPlacer.placeTrunk: dirt under all four
// column cells, then a 2x2 that leans up to two cells total starting
// at h-intn(4) (one cell per dy once the steps run out), each layer
// written only when its NW cell is air-or-leaves. The top attachment
// is doubleTrunk; a 1-in-3 fringe of cells on the ring around the
// top — at the ORIGIN x/z, not the leaned top — each drops a
// 2..4-long stem and a single-trunk attachment.
func (s *Server) darkOakTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	for _, off := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		below := world.Pos{X: pos.X + off[0], Y: pos.Y - 1, Z: pos.Z + off[1]}
		if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
			if dirt, ok := world.BlockStateOf("dirt", nil); ok {
				s.setNaturalBlock(d, below, dirt)
			}
		}
	}
	lean := horizDirs[s.rng.intn(4)]
	leanHeight := treeHeight - s.rng.intn(4)
	leanSteps := 2 - s.rng.intn(3)
	tx, tz := pos.X, pos.Z
	ey := pos.Y + treeHeight - 1
	var attachments []foliageAttach
	for dy := 0; dy < treeHeight; dy++ {
		if dy >= leanHeight && leanSteps > 0 {
			tx += lean.X
			tz += lean.Z
			leanSteps--
		}
		nw := world.Pos{X: tx, Y: pos.Y + dy, Z: tz}
		if !s.airOrLeaves(d, nw) {
			continue
		}
		for _, off := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			p := world.Pos{X: nw.X + off[0], Y: nw.Y, Z: nw.Z + off[1]}
			if s.treeCanOccupy(d, p) {
				s.setNaturalBlock(d, p, logState)
				*logs = append(*logs, p)
			}
		}
	}
	attachments = append(attachments, foliageAttach{
		pos:         world.Pos{X: tx, Y: ey, Z: tz},
		doubleTrunk: true,
	})
	for ox := -1; ox <= 2; ox++ {
		for oz := -1; oz <= 2; oz++ {
			if ox >= 0 && ox <= 1 && oz >= 0 && oz <= 1 {
				continue
			}
			if s.rng.intn(3) > 0 {
				continue
			}
			length := s.rng.intn(3) + 2
			for b := 0; b < length; b++ {
				p := world.Pos{X: pos.X + ox, Y: ey - 1 - b, Z: pos.Z + oz}
				if s.treeCanOccupy(d, p) {
					s.setNaturalBlock(d, p, logState)
					*logs = append(*logs, p)
				}
			}
			attachments = append(attachments, foliageAttach{
				pos: world.Pos{X: pos.X + ox, Y: ey, Z: pos.Z + oz},
			})
		}
	}
	return attachments
}

// airOrLeaves — TreeFeature.isAirOrLeaves: air or a #leaves cell.
func (s *Server) airOrLeaves(d *dimension, p world.Pos) bool {
	n, _ := world.StateOwner(d.w.GetBlock(p))
	n = strings.TrimPrefix(n, "minecraft:")
	return n == "air" || n == "cave_air" || n == "void_air" || isLeafBlock(n)
}

// darkOakCanopy — DarkOakFoliagePlacer {radius 0, offset 0,
// foliageHeight 4}: the double-trunk top draws (r=2,y=-1), (r=3,y=0),
// (r=2,y=1) and half the time (r=0,y=2); each fringe stem draws
// (r=2,y=-1) and (r=1,y=0). Rows are square, extended +1 on x/z for
// the double trunk.
func (s *Server) darkOakCanopy(d *dimension, attachments []foliageAttach, placeLeaf func(world.Pos)) {
	for _, at := range attachments {
		fp := at.pos
		if at.doubleTrunk {
			s.darkOakRow(fp, 2, -1, true, placeLeaf)
			s.darkOakRow(fp, 3, 0, true, placeLeaf)
			s.darkOakRow(fp, 2, 1, true, placeLeaf)
			if s.rng.intn(2) == 0 {
				s.darkOakRow(fp, 0, 2, true, placeLeaf)
			}
		} else {
			s.darkOakRow(fp, 2, -1, false, placeLeaf)
			s.darkOakRow(fp, 1, 0, false, placeLeaf)
		}
	}
}

// darkOakRow is placeLeavesRow with DarkOakFoliagePlacer's skips:
// the double trunk's y=0 row holes out the 3x3 where both coords sit
// on the outer boundary set {-r, r, r+1}; a single-trunk y=-1 row
// drops its corners; a y=1 row drops cells with minDx+minDz > 2r-2.
func (s *Server) darkOakRow(fp world.Pos, r, y int, doubleTrunk bool, placeLeaf func(world.Pos)) {
	ext := 0
	if doubleTrunk {
		ext = 1
	}
	for dx := -r; dx <= r+ext; dx++ {
		for dz := -r; dz <= r+ext; dz++ {
			var skip bool
			switch {
			case y == 0 && doubleTrunk:
				skip = !(dx != -r && dx < r) && !(dz != -r && dz < r)
			default:
				mdx, mdz := absi(dx), absi(dz)
				if doubleTrunk {
					mdx = min(absi(dx), absi(dx-1))
					mdz = min(absi(dz), absi(dz-1))
				}
				switch {
				case y == -1 && !doubleTrunk:
					skip = mdx == r && mdz == r
				case y == 1:
					skip = mdx+mdz > 2*r-2
				}
			}
			if !skip {
				placeLeaf(world.Pos{X: fp.X + dx, Y: fp.Y + y, Z: fp.Z + dz})
			}
		}
	}
}

// giantTrunk — GiantTrunkPlacer.placeTrunk: dirt under all four
// column cells, then a bare 2x2 whose top layer collapses to the
// single column cell. One attachment directly above the top log,
// doubleTrunk.
func (s *Server) giantTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	for _, off := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		below := world.Pos{X: pos.X + off[0], Y: pos.Y - 1, Z: pos.Z + off[1]}
		if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
			if dirt, ok := world.BlockStateOf("dirt", nil); ok {
				s.setNaturalBlock(d, below, dirt)
			}
		}
	}
	for hh := 0; hh < treeHeight; hh++ {
		cells := [][2]int{{0, 0}}
		if hh < treeHeight-1 {
			cells = append(cells, [2]int{1, 0}, [2]int{1, 1}, [2]int{0, 1})
		}
		for _, off := range cells {
			p := world.Pos{X: pos.X + off[0], Y: pos.Y + hh, Z: pos.Z + off[1]}
			if s.treeFree(d, p) {
				s.setNaturalBlock(d, p, logState)
				*logs = append(*logs, p)
			}
		}
	}
	return []foliageAttach{{
		pos:         world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z},
		doubleTrunk: true,
	}}
}

// megaJungleTrunk — MegaJungleTrunkPlacer.placeTrunk: the giant
// column plus angled branches — from h-2-intn(4) stepping down
// 2..5 while above h/2, each branch walks 5 cells outward along a
// random angle ((int)(1.5 + cos·b) truncation — cells can re-hit the
// column) rising b/2 cells, then a single-trunk attachment at
// radiusOffset -2.
func (s *Server) megaJungleTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	attachments := s.giantTrunk(d, pos, treeHeight, logState, logs)
	for branchHeight := treeHeight - 2 - s.rng.intn(4); branchHeight > treeHeight/2; branchHeight -= 2 + s.rng.intn(4) {
		angle := s.rng.nextDouble() * math.Pi * 2
		bx, bz := 0, 0
		for b := 0; b < 5; b++ {
			bx = int(1.5 + math.Cos(angle)*float64(b))
			bz = int(1.5 + math.Sin(angle)*float64(b))
			p := world.Pos{X: pos.X + bx, Y: pos.Y + branchHeight - 3 + b/2, Z: pos.Z + bz}
			if s.treeCanOccupy(d, p) {
				s.setNaturalBlock(d, p, logState)
				*logs = append(*logs, p)
			}
		}
		attachments = append(attachments, foliageAttach{
			pos:          world.Pos{X: pos.X + bx, Y: pos.Y + branchHeight, Z: pos.Z + bz},
			radiusOffset: -2,
		})
	}
	return attachments
}

// megaPineCanopy — MegaPineFoliagePlacer {radius 0, offset 0}: a
// crown_height-deep cone whose rows smooth from floor(yo/crown·3.5),
// jagged +1 on even world-Y rows when the radius stalls. Rows are
// circular with the signed dx+dz>=7 corner cut.
func (s *Server) megaPineCanopy(d *dimension, attachments []foliageAttach, shape treeShape, placeLeaf func(world.Pos)) {
	for _, at := range attachments {
		fp := at.pos
		crown := shape.crownMin
		if shape.crownMax > shape.crownMin {
			crown += s.rng.intn(shape.crownMax - shape.crownMin + 1)
		}
		prevR := 0
		for yy := fp.Y - crown; yy <= fp.Y; yy++ {
			yo := fp.Y - yy
			smooth := at.radiusOffset + int(math.Floor(float64(yo)/float64(crown)*3.5))
			r := smooth
			if yo > 0 && smooth == prevR && yy%2 == 0 {
				r = smooth + 1
			}
			s.treeRowCircular(fp, r, yy, at.doubleTrunk, placeLeaf)
			prevR = smooth
		}
	}
}

// megaJungleCanopy — MegaJungleFoliagePlacer {radius 2, offset 0,
// height 2}: rows yo=0..-leafHeight with radius 2+offset+1-yo — a
// circular cone widening downward; leafHeight is the fixed height on
// the double trunk and 1+intn(2) on single branches.
func (s *Server) megaJungleCanopy(d *dimension, attachments []foliageAttach, placeLeaf func(world.Pos)) {
	for _, at := range attachments {
		fp := at.pos
		leafH := 2
		if !at.doubleTrunk {
			leafH = 1 + s.rng.intn(2)
		}
		for yo := 0; yo >= -leafH; yo-- {
			r := 2 + at.radiusOffset + 1 - yo
			s.treeRowCircular(fp, r, fp.Y+yo, at.doubleTrunk, placeLeaf)
		}
	}
}

// treeRowCircular draws one MegaPine/MegaJungle row: a circular disc
// (dx²+dz² <= r²) with the signed dx+dz >= 7 corner cut, extended +1
// on x/z for a double trunk.
func (s *Server) treeRowCircular(fp world.Pos, r, y int, doubleTrunk bool, placeLeaf func(world.Pos)) {
	if r < 0 {
		return
	}
	ext := 0
	if doubleTrunk {
		ext = 1
	}
	for dx := -r; dx <= r+ext; dx++ {
		for dz := -r; dz <= r+ext; dz++ {
			if dx+dz >= 7 || dx*dx+dz*dz > r*r {
				continue
			}
			placeLeaf(world.Pos{X: fp.X + dx, Y: y, Z: fp.Z + dz})
		}
	}
}

// podzolDecor — AlterGroundDecorator: for every lowest-Y trunk cell,
// four fixed podzol circles at the corner-adjacent offsets plus five
// random border placements; each circle is a 5x5 minus its corners,
// written top-down (dy 2..-3) at the first
// beneath_tree_podzol_replaceable cell, tunnelling through air only.
func (s *Server) podzolDecor(d *dimension, logs []world.Pos) {
	if len(logs) == 0 {
		return
	}
	minY := logs[0].Y
	for _, lp := range logs {
		if lp.Y < minY {
			minY = lp.Y
		}
	}
	podzol, _ := world.BlockStateOf("podzol", map[string]string{"snowy": "false"})
	for _, lp := range logs {
		if lp.Y != minY {
			continue
		}
		for _, off := range [][2]int{{-1, -1}, {2, -1}, {-1, 2}, {2, 2}} {
			s.podzolCircle(d, podzol, world.Pos{X: lp.X + off[0], Y: lp.Y, Z: lp.Z + off[1]})
		}
		for i := 0; i < 5; i++ {
			placement := s.rng.intn(64)
			xx, zz := placement%8, placement/8
			if xx == 0 || xx == 7 || zz == 0 || zz == 7 {
				s.podzolCircle(d, podzol, world.Pos{X: lp.X - 3 + xx, Y: lp.Y, Z: lp.Z - 3 + zz})
			}
		}
	}
}

// podzolCircle is placeCircle: a 5x5 minus the four outer corners.
func (s *Server) podzolCircle(d *dimension, podzol world.Block, pos world.Pos) {
	for xx := -2; xx <= 2; xx++ {
		for zz := -2; zz <= 2; zz++ {
			if absi(xx) == 2 && absi(zz) == 2 {
				continue
			}
			s.podzolAt(d, podzol, world.Pos{X: pos.X + xx, Y: pos.Y, Z: pos.Z + zz})
		}
	}
}

// podzolAt is placeBlockAt: scan dy=2..-3, write podzol on the first
// substrate cell, and stop at solid ground below the surface (cells
// at dy>=0 can't tunnel, only air passes).
func (s *Server) podzolAt(d *dimension, podzol world.Block, pos world.Pos) {
	for dy := 2; dy >= -3; dy-- {
		p := world.Pos{X: pos.X, Y: pos.Y + dy, Z: pos.Z}
		n, _ := world.StateOwner(d.w.GetBlock(p))
		if podzolReplaceable[strings.TrimPrefix(n, "minecraft:")] {
			s.setNaturalBlock(d, p, podzol)
			return
		}
		if !treeReplaceable[strings.TrimPrefix(n, "minecraft:")] && !isAirCell(d.w.GetBlock(p)) && dy < 0 {
			return
		}
	}
}

// podzolReplaceable — #beneath_tree_podzol_replaceable =
// #substrate_overworld = dirt + mud + moss_blocks + grass_blocks.
var podzolReplaceable = map[string]bool{
	"dirt": true, "coarse_dirt": true, "rooted_dirt": true,
	"mud": true, "muddy_mangrove_roots": true,
	"moss_block": true, "pale_moss_block": true,
	"grass_block": true, "podzol": true, "mycelium": true,
}

// jungleVineDecorators — the mega jungle's {trunk_vine, leave_vine
// {0.25}} decorator set: same rolls as jungleDecorators without the
// cocoa pass.
func (s *Server) jungleVineDecorators(d *dimension, logs, leaves []world.Pos) {
	isAir := func(p world.Pos) bool {
		return isAirCell(d.w.GetBlock(p))
	}
	placeVine := func(p world.Pos, face string) {
		if v, ok := world.BlockStateOf("vine", map[string]string{face: "true"}); ok {
			s.setNaturalBlock(d, p, v)
		}
	}
	for _, lp := range logs {
		for _, vd := range trunkVineDirs {
			if s.rng.intn(3) > 0 {
				vp := world.Pos{X: lp.X + vd.dx, Y: lp.Y, Z: lp.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
				}
			}
		}
	}
	s.leaveVines(d, leaves, 0.25)
}

// leaveVines — LeaveVineDecorator: per leaf and per horizontal
// direction, at p, a vine with the opposite face plus a downward
// hang of up to four more while air continues.
func (s *Server) leaveVines(d *dimension, leaves []world.Pos, p float64) {
	isAir := func(pos world.Pos) bool {
		return isAirCell(d.w.GetBlock(pos))
	}
	placeVine := func(pos world.Pos, face string) {
		if v, ok := world.BlockStateOf("vine", map[string]string{face: "true"}); ok {
			s.setNaturalBlock(d, pos, v)
		}
	}
	for _, lf := range leaves {
		for _, vd := range trunkVineDirs {
			if s.rng.nextDouble() < p {
				vp := world.Pos{X: lf.X + vd.dx, Y: lf.Y, Z: lf.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
					for i := 0; i < 4; i++ {
						vp.Y--
						if !isAir(vp) {
							break
						}
						placeVine(vp, vd.face)
					}
				}
			}
		}
	}
}

// paleMossDecor — PaleMossDecorator: at 0.8 the PALE_MOSS_PATCH
// vegetation feature anchors one cell above the lowest log; then per
// log at 0.4 and per leaf at 0.15, a pale_hanging_moss hanger descends
// from the cell below while air continues below and a 50% roll keeps
// going (tip on the last cell).
func (s *Server) paleMossDecor(d *dimension, logs, leaves []world.Pos) {
	isAir := func(p world.Pos) bool {
		return isAirCell(d.w.GetBlock(p))
	}
	if len(logs) > 0 {
		lowest := logs[0]
		for _, lp := range logs[1:] {
			if lp.Y < lowest.Y {
				lowest = lp
			}
		}
		if s.rng.nextDouble() < 0.8 {
			s.paleMossPatch(d, world.Pos{X: lowest.X, Y: lowest.Y + 1, Z: lowest.Z})
		}
	}
	hanger := func(p world.Pos) {
		mid, _ := world.BlockStateOf("pale_hanging_moss", map[string]string{"tip": "false"})
		tip, _ := world.BlockStateOf("pale_hanging_moss", map[string]string{"tip": "true"})
		for isAir(world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}) && s.rng.nextDouble() >= 0.5 {
			s.setNaturalBlock(d, p, mid)
			p.Y--
		}
		s.setNaturalBlock(d, p, tip)
	}
	for _, lp := range logs {
		below := world.Pos{X: lp.X, Y: lp.Y - 1, Z: lp.Z}
		if s.rng.nextDouble() < 0.4 && isAir(below) {
			hanger(below)
		}
	}
	for _, lf := range leaves {
		below := world.Pos{X: lf.X, Y: lf.Y - 1, Z: lf.Z}
		if s.rng.nextDouble() < 0.15 && isAir(below) {
			hanger(below)
		}
	}
}

// placeTreeLeaf is FoliagePlacer.tryPlaceLeaf: never overwrites
// persistent leaves or anything outside REPLACEABLE_BY_TREES (logs
// included), and folds waterlogged from the cell's fluid into the
// written state.
func (s *Server) placeTreeLeaf(d *dimension, p world.Pos, leafState world.Block, leaves *[]world.Pos) {
	cur := d.w.GetBlock(p)
	if pv, ok := world.StateProperty(cur, "persistent"); ok && pv == "true" {
		return
	}
	if !s.treeCanOccupy(d, p) {
		return
	}
	st := leafState
	if k, _, ok := fluidOf(cur); ok && k == fluidWater {
		if nb, ok := world.WithProperty(st, "waterlogged", "true"); ok {
			st = nb
		}
	}
	s.setNaturalBlock(d, p, st)
	*leaves = append(*leaves, p)
}

// fancyOakFeature — fancy_oak.json: the oak secondary feature. Trunk
// {base 3, randA 11, randB 0}, FancyFoliagePlacer {radius 2, offset 4,
// height 4}, decorators [], and two_layers {limit 0, upper_size 0,
// min_clipped_height 4} — the single-column space check may clip the
// tree to as little as 4 cells, and a clipped fancy oak still grows
// (unlike the main feature which fails when it can't reach its full
// sampled height).
func (s *Server) fancyOakFeature(d *dimension, pos world.Pos, bees bool) bool {
	treeHeight := 3 + s.rng.intn(12) + s.rng.intn(1)
	if pos.Y < d.minY+1 || pos.Y+treeHeight+1 > d.minY+d.height {
		return false
	}
	// getMaxFreeTreeHeight with getSizeAtHeight → 0 at every layer.
	clipped := treeHeight
	for y := 0; y <= treeHeight+1; y++ {
		if !s.treeSpaceFree(d, pos, y, 0, false) {
			clipped = y - 2
			break
		}
	}
	// clippedTreeHeight >= treeHeight || clippedTreeHeight >= 4.
	if clipped < treeHeight && clipped < 4 {
		return false
	}

	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
		if dirt, ok := world.BlockStateOf("dirt", nil); ok {
			s.setNaturalBlock(d, below, dirt)
		}
	}

	logState, _ := world.BlockStateOf("oak_log", map[string]string{"axis": "y"})
	var logs []world.Pos
	attachments := s.fancyTrunk(d, pos, clipped, logState, &logs)

	leafState, _ := world.BlockStateOf("oak_leaves", map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	var leaves []world.Pos
	// FancyFoliagePlacer.createFoliage per attachment: five rows at
	// yo = offset(4)..offset-foliageHeight(0) with radius 2 on the top
	// and bottom rows and 3 between, and the circular skip
	// (dx+.5)^2 + (dz+.5)^2 > r^2.
	for _, at := range attachments {
		for yo := 4; yo >= 0; yo-- {
			r := 2
			if yo != 4 && yo != 0 {
				r = 3
			}
			for dx := -r; dx <= r; dx++ {
				for dz := -r; dz <= r; dz++ {
					fx, fz := float64(dx)+0.5, float64(dz)+0.5
					if fx*fx+fz*fz > float64(r*r) {
						continue
					}
					s.placeTreeLeaf(d, world.Pos{X: at.pos.X + dx, Y: at.pos.Y + yo, Z: at.pos.Z + dz}, leafState, &leaves)
				}
			}
		}
	}
	// fancy_oak_bees_005's decorators = [beehive .05].
	if bees {
		s.beehiveDecor(d, logs, leaves, 0.05)
	}
	s.treeLeafDistances(d, logs, leaves)
	return true
}

// fancyCoord is FancyTrunkPlacer.FoliageCoords: an attachment cell and
// the trunk-column Y its branch leaves from.
type fancyCoord struct {
	attach      world.Pos
	branchBaseY int
}

// fancyTrunk — FancyTrunkPlacer.placeTrunk. height = clipped+2,
// trunkHeight = floor(height*0.618): candidate foliage spots are
// scanned from the crown down while two limbs pass collision checks
// (isFree — log cells count as free), then the trunk column and one
// limb per kept coord are written; coords whose branch base is below
// 20% of the tree are trimmed away.
func (s *Server) fancyTrunk(d *dimension, pos world.Pos, clipped int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	height := clipped + 2
	trunkHeight := int(math.Floor(float64(height) * 0.618))
	trunkTop := pos.Y + trunkHeight

	coords := []fancyCoord{{
		attach:      world.Pos{X: pos.X, Y: pos.Y + height - 5, Z: pos.Z},
		branchBaseY: trunkTop,
	}}
	// clustersPerY = min(1, floor(1.382 + (height/13)^2)) is 1 for every
	// treeHeight fancy reaches (height <= 16).
	for relY := height - 5; relY >= 0; relY-- {
		ts := fancyTreeShape(height, relY)
		if ts < 0 {
			continue
		}
		radius := float64(ts) * (s.rng.nextDouble() + 0.328)
		angle := s.rng.nextDouble() * 2 * math.Pi
		x := radius*math.Sin(angle) + 0.5
		z := radius*math.Cos(angle) + 0.5
		checkStart := world.Pos{
			X: pos.X + int(math.Floor(x)),
			Y: pos.Y + relY - 1,
			Z: pos.Z + int(math.Floor(z)),
		}
		checkEnd := world.Pos{X: checkStart.X, Y: checkStart.Y + 5, Z: checkStart.Z}
		if !s.fancyLimbFree(d, checkStart, checkEnd) {
			continue
		}
		dx := pos.X - checkStart.X
		dz := pos.Z - checkStart.Z
		branchTop := int(float64(checkStart.Y) - math.Sqrt(float64(dx*dx+dz*dz))*0.381)
		if branchTop > trunkTop {
			branchTop = trunkTop
		}
		base := world.Pos{X: pos.X, Y: branchTop, Z: pos.Z}
		if !s.fancyLimbFree(d, base, checkStart) {
			continue
		}
		coords = append(coords, fancyCoord{attach: checkStart, branchBaseY: base.Y})
	}

	s.fancyLimb(d, pos, world.Pos{X: pos.X, Y: pos.Y + trunkHeight, Z: pos.Z}, logState, logs)
	for _, c := range coords {
		base := world.Pos{X: pos.X, Y: c.branchBaseY, Z: pos.Z}
		if base != c.attach && fancyTrim(height, base.Y-pos.Y) {
			s.fancyLimb(d, base, c.attach, logState, logs)
		}
	}

	var attachments []foliageAttach
	for _, c := range coords {
		if fancyTrim(height, c.branchBaseY-pos.Y) {
			attachments = append(attachments, foliageAttach{pos: c.attach})
		}
	}
	return attachments
}

// fancyTreeShape — FancyTrunkPlacer.treeShape: -1 below 30% of the
// tree (never tries foliage there), 0 outside the ellipsoid shell,
// else half the chord radius at that layer.
func fancyTreeShape(height, y int) float64 {
	if float64(y) < float64(height)*0.3 {
		return -1
	}
	r := float64(height) / 2
	adj := r - float64(y)
	if adj == 0 {
		return r * 0.5
	}
	if math.Abs(adj) >= r {
		return 0
	}
	return math.Sqrt(r*r-adj*adj) * 0.5
}

// fancyTrim — FancyTrunkPlacer.trimBranches.
func fancyTrim(height, localY int) bool {
	return float64(localY) >= float64(height)*0.2
}

// fancyLimbFree — makeLimb with doPlace=false: every interpolated cell
// must be isFree (validTreePos or a #logs cell); a zero-length limb is
// always free.
func (s *Server) fancyLimbFree(d *dimension, start, end world.Pos) bool {
	if start == end {
		return true
	}
	dx, dy, dz := end.X-start.X, end.Y-start.Y, end.Z-start.Z
	steps := max(absi(dx), absi(dy), absi(dz))
	for i := 0; i <= steps; i++ {
		bp := fancyLimbCell(start, dx, dy, dz, steps, i)
		if !s.treeFree(d, bp) {
			return false
		}
	}
	return true
}

// fancyLimbCell is the interpolated limb cell at step i: Java's
// (int)(0.5 + i*d/steps) floor, where a zero-length limb keeps the
// start cell (Java's (int)NaN → 0).
func fancyLimbCell(start world.Pos, dx, dy, dz, steps, i int) world.Pos {
	if steps == 0 {
		return start
	}
	return world.Pos{
		X: start.X + int(math.Floor(0.5+float64(i)*float64(dx)/float64(steps))),
		Y: start.Y + int(math.Floor(0.5+float64(i)*float64(dy)/float64(steps))),
		Z: start.Z + int(math.Floor(0.5+float64(i)*float64(dz)/float64(steps))),
	}
}

// fancyLimb — makeLimb with doPlace=true: each interpolated cell writes
// a log when validTreePos, with the axis of the dominant direction of
// the limb's start point (the branch limbs' logs lie horizontal).
func (s *Server) fancyLimb(d *dimension, start, end world.Pos, logState world.Block, logs *[]world.Pos) {
	dx, dy, dz := end.X-start.X, end.Y-start.Y, end.Z-start.Z
	steps := max(absi(dx), absi(dy), absi(dz))
	for i := 0; i <= steps; i++ {
		bp := fancyLimbCell(start, dx, dy, dz, steps, i)
		if !s.treeCanOccupy(d, bp) {
			continue
		}
		st := logState
		ax, az := absi(bp.X-start.X), absi(bp.Z-start.Z)
		switch {
		case ax == 0 && az == 0:
		case ax >= az:
			st, _ = world.WithProperty(st, "axis", "x")
		default:
			st, _ = world.WithProperty(st, "axis", "z")
		}
		s.setNaturalBlock(d, bp, st)
		*logs = append(*logs, bp)
	}
}

// treeFree — TrunkPlacer.isFree: validTreePos or a #logs cell — trunks
// weave through other logs on both the space check and limb checks.
func (s *Server) treeFree(d *dimension, p world.Pos) bool {
	if s.treeCanOccupy(d, p) {
		return true
	}
	n, _ := world.StateOwner(d.w.GetBlock(p))
	return treeLogTag(strings.TrimPrefix(n, "minecraft:"))
}

// spruceCanopy — SpruceFoliagePlacer.createFoliage: the zigzag cone.
// foliageHeight = max(4, treeHeight - U(1..2)) and leafRadius = U(2..3)
// are sampled up front; the row loop runs yo = offset(U(0..2)) down to
// -foliageHeight. The row radius starts at U(0..1), counts up while
// under maxRadius, then drops to the running minimum while maxRadius
// grows toward the sampled leaf radius — the classic spruce spire.
// Corners are skipped whenever the radius is positive (there is no
// random corner roll like the blob's).
func (s *Server) spruceCanopy(d *dimension, top world.Pos, treeHeight int, placeLeaf func(world.Pos)) {
	fH := treeHeight - 1 - s.rng.intn(2) // treeHeight - U(1..2), floored at 4
	if fH < 4 {
		fH = 4
	}
	leafR := 2 + s.rng.intn(2)
	off := s.rng.intn(3)
	cur, maxR, minR := s.rng.intn(2), 1, 0
	for yo := off; yo >= -fH; yo-- {
		for dx := -cur; dx <= cur; dx++ {
			for dz := -cur; dz <= cur; dz++ {
				if absi(dx) == cur && absi(dz) == cur && cur > 0 {
					continue
				}
				placeLeaf(world.Pos{X: top.X + dx, Y: top.Y + yo, Z: top.Z + dz})
			}
		}
		if cur >= maxR {
			cur = minR
			minR = 1
			maxR = min(maxR+1, leafR)
		} else {
			cur++
		}
	}
}

// acaciaCanopy — AcaciaFoliagePlacer.createFoliage: one flat
// umbrella per trunk attachment. The skirt row sits one below the
// attachment at radius leafRadius(2) + radiusOffset (3 on the main
// top, 2 on the branch tip) with its corners skipped; the cap row at
// the attachment level is radius leafRadius-1 = 1 where the skip
// predicate (dx>1||dz>1)&&dx!=0&&dz!=0 never fires → a full 3x3.
func (s *Server) acaciaCanopy(d *dimension, attachments []foliageAttach, placeLeaf func(world.Pos)) {
	for _, at := range attachments {
		// skirt: attach-1, radius 2+radiusOffset, corners skipped.
		r := 2 + at.radiusOffset
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if absi(dx) == r && absi(dz) == r && r > 0 {
					continue
				}
				placeLeaf(world.Pos{X: at.pos.X + dx, Y: at.pos.Y - 1, Z: at.pos.Z + dz})
			}
		}
		// cap: attach level, radius 1, no cell ever skipped.
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				placeLeaf(world.Pos{X: at.pos.X + dx, Y: at.pos.Y, Z: at.pos.Z + dz})
			}
		}
	}
}

// forkingTrunk — ForkingTrunkPlacer.placeTrunk: the column leans one
// horizontal step per level for the top leanSteps(1-3) levels from
// leanHeight(treeHeight - U(0..3) - 1), and a second staircase branch
// climbs from leanHeight - U(0..1) - 1 for 1-3 logs when its random
// direction differs from the lean. Attachments: the main tip carries
// radiusOffset 1, the branch tip 0.
var horizDirs = [4]world.Pos{
	{X: 0, Z: -1}, {X: 0, Z: 1}, {X: -1, Z: 0}, {X: 1, Z: 0},
}

func (s *Server) forkingTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	var attachments []foliageAttach
	lean := horizDirs[s.rng.intn(4)]
	leanHeight := treeHeight - s.rng.intn(4) - 1
	leanSteps := 3 - s.rng.intn(3)
	tx, tz := pos.X, pos.Z
	var top world.Pos
	hasTop := false
	for yo := 0; yo < treeHeight; yo++ {
		yy := pos.Y + yo
		if yo >= leanHeight && leanSteps > 0 {
			tx += lean.X
			tz += lean.Z
			leanSteps--
		}
		p := world.Pos{X: tx, Y: yy, Z: tz}
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			*logs = append(*logs, p)
			top = world.Pos{X: tx, Y: yy + 1, Z: tz}
			hasTop = true
		}
	}
	if hasTop {
		attachments = append(attachments, foliageAttach{
			pos: top, radiusOffset: 1})
	}

	tx, tz = pos.X, pos.Z
	branch := horizDirs[s.rng.intn(4)]
	if branch != lean {
		branchPos := leanHeight - s.rng.intn(2) - 1
		branchSteps := 1 + s.rng.intn(3)
		hasTop = false
		for yo := branchPos; yo < treeHeight && branchSteps > 0; yo++ {
			if yo >= 1 {
				yy := pos.Y + yo
				tx += branch.X
				tz += branch.Z
				p := world.Pos{X: tx, Y: yy, Z: tz}
				if s.treeCanOccupy(d, p) {
					s.setNaturalBlock(d, p, logState)
					*logs = append(*logs, p)
					top = world.Pos{X: tx, Y: yy + 1, Z: tz}
					hasTop = true
				}
			}
			branchSteps--
		}
		if hasTop {
			attachments = append(attachments, foliageAttach{
				pos: top, radiusOffset: 0})
		}
	}
	return attachments
}

// vineDir is one horizontal neighbor cell and the vine face written
// there so the vine attaches back toward the source cell.
type vineDir struct {
	dx, dz int
	face   string
}

// trunkVineDirs — the four directions both TrunkVineDecorator and
// LeaveVineDecorator walk (west, east, north, south in code order).
var trunkVineDirs = [4]vineDir{
	{-1, 0, "east"}, {1, 0, "west"}, {0, -1, "south"}, {0, 1, "north"},
}

// cocoaDirs — Plane.HORIZONTAL order (north, south, west, east): the
// pod lands on the opposite side of the direction and faces back.
var cocoaDirs = [4]vineDir{
	{0, -1, "north"}, {0, 1, "south"}, {-1, 0, "west"}, {1, 0, "east"},
}

// jungleDecorators — the jungle decorator family: CocoaDecorator
// (0.2 tree gate + 0.25 per bottom-log face — jungle_tree only,
// which no sapling reaches in 26.2), TrunkVineDecorator (nextInt(3)>0
// per log face), LeaveVineDecorator (0.25 per leaf face, hanging down
// through air up to 4). The 2x2 mega_jungle_tree feature uses the two
// vine decorators; jungle_tree adds cocoa.
func (s *Server) jungleDecorators(d *dimension, logs, leaves []world.Pos) {
	isAir := func(p world.Pos) bool {
		return isAirCell(d.w.GetBlock(p))
	}
	placeVine := func(p world.Pos, face string) {
		if v, ok := world.BlockStateOf("vine", map[string]string{face: "true"}); ok {
			s.setNaturalBlock(d, p, v)
		}
	}

	// CocoaDecorator: one probability gate for the whole tree, then
	// every log within three of the base rolls 0.25 per direction.
	if s.rng.nextDouble() < 0.2 && len(logs) > 0 {
		treeY := logs[0].Y
		for _, lp := range logs {
			if lp.Y-treeY > 2 {
				continue
			}
			for _, cd := range cocoaDirs {
				if s.rng.nextDouble() <= 0.25 {
					cp := world.Pos{X: lp.X - cd.dx, Y: lp.Y, Z: lp.Z - cd.dz}
					if isAir(cp) {
						if c, ok := world.BlockStateOf("cocoa", map[string]string{
							"age": strconv.Itoa(s.rng.intn(3)), "facing": cd.face}); ok {
							s.setNaturalBlock(d, cp, c)
						}
					}
				}
			}
		}
	}

	// TrunkVineDecorator: each direction rolls nextInt(3) > 0.
	for _, lp := range logs {
		for _, vd := range trunkVineDirs {
			if s.rng.intn(3) > 0 {
				vp := world.Pos{X: lp.X + vd.dx, Y: lp.Y, Z: lp.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
				}
			}
		}
	}

	// LeaveVineDecorator: 0.25 per leaf per direction, then the vine
	// hangs downward through air up to four more cells.
	for _, lf := range leaves {
		for _, vd := range trunkVineDirs {
			if s.rng.nextDouble() < 0.25 {
				vp := world.Pos{X: lf.X + vd.dx, Y: lf.Y, Z: lf.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
					down := world.Pos{X: vp.X, Y: vp.Y - 1, Z: vp.Z}
					for i := 0; i < 4 && isAir(down); i++ {
						placeVine(down, vd.face)
						down.Y--
					}
				}
			}
		}
	}
}

// treeSpaceFree runs one layer of getMaxFreeTreeHeight: every cell in
// the ±r box at (pos + y) must satisfy isFree — validTreePos (air or
// REPLACEABLE_BY_TREES) or a log.
// treeSpaceFree — getMaxFreeTreeHeight's per-cell gate:
// trunkPlacer.isFree (validTreePos or #logs) and, unless the feature
// ignores vines, not a vine cell.
func (s *Server) treeSpaceFree(d *dimension, pos world.Pos, y, r int, ignoreVines bool) bool {
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			p := world.Pos{X: pos.X + dx, Y: pos.Y + y, Z: pos.Z + dz}
			if s.treeCanOccupy(d, p) {
				continue
			}
			if n, _ := world.StateOwner(d.w.GetBlock(p)); treeLogTag(strings.TrimPrefix(n, "minecraft:")) {
				continue
			}
			return false
		}
	}
	return true
}

// treeLogTag is the minecraft:logs tag contents — TrunkPlacer.isFree
// counts these as space-free without being placement targets. The tag
// expands to every *_log, *_wood, *_stem and *_hyphae state including
// the stripped variants (mangrove_roots is not a member).
func treeLogTag(name string) bool {
	return strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_hyphae")
}

// treeCanOccupy — TreeFeature.validTreePos: air or anything in the
// replaceable_by_trees tag (measured from the 26.2 tag data: the whole
// #leaves and #small_flowers tags plus the grass/vine/water families).
func (s *Server) treeCanOccupy(d *dimension, p world.Pos) bool {
	n, _ := world.StateOwner(d.w.GetBlock(p))
	return treeReplaceable[strings.TrimPrefix(n, "minecraft:")]
}

var treeReplaceable = map[string]bool{
	"air": true, "cave_air": true, "void_air": true,
	"water": true, "seagrass": true, "tall_seagrass": true,
	"short_grass": true, "tall_grass": true, "fern": true, "large_fern": true,
	"dead_bush": true, "vine": true, "glow_lichen": true,
	"sunflower": true, "lilac": true, "rose_bush": true, "peony": true,
	"hanging_roots": true, "pitcher_plant": true, "bush": true,
	"firefly_bush": true, "warped_roots": true, "nether_sprouts": true,
	"crimson_roots": true, "leaf_litter": true, "pale_moss_carpet": true,
	"short_dry_grass": true, "tall_dry_grass": true,
	"allium": true, "azure_bluet": true, "blue_orchid": true,
	"closed_eyeblossom": true, "cornflower": true, "dandelion": true,
	"golden_dandelion": true, "lily_of_the_valley": true,
	"open_eyeblossom": true, "orange_tulip": true, "oxeye_daisy": true,
	"pink_tulip": true, "poppy": true, "red_tulip": true,
	"torchflower": true, "white_tulip": true, "wither_rose": true,
	"acacia_leaves": true, "azalea_leaves": true, "birch_leaves": true,
	"cherry_leaves": true, "dark_oak_leaves": true,
	"flowering_azalea_leaves": true, "jungle_leaves": true,
	"mangrove_leaves": true, "oak_leaves": true, "pale_oak_leaves": true,
	"spruce_leaves": true,
}

// dirtTagName — the cannot_replace_below_tree_trunk tag (#dirt {dirt,
// coarse_dirt, rooted_dirt}, #mud {mud, muddy_mangrove_roots},
// #moss_blocks {moss_block, pale_moss_block}, podzol). Grass is NOT in
// it — a tree roots grass into dirt.
func dirtTagName(n string) bool {
	switch n {
	case "dirt", "coarse_dirt", "rooted_dirt", "mud",
		"muddy_mangrove_roots", "moss_block", "pale_moss_block", "podzol":
		return true
	}
	return false
}

// treeLeafDistances replays TreeFeature.updateLeaves: a BFS seeded at
// every written log cell walks the written leaf cells and sets
// distance = min(neighbour+1, 7). The seed is the collected trunk set —
// leaning/forked trunks leave the origin column, so scanning the column
// would miss them.
func (s *Server) treeLeafDistances(d *dimension, logs, leaves []world.Pos) {
	leafSet := make(map[world.Pos]bool, len(leaves))
	for _, p := range leaves {
		leafSet[p] = true
	}
	var queue []world.Pos
	dist := make(map[world.Pos]int, len(leaves))
	for _, p := range logs {
		for _, nb := range posNeighbors(p) {
			if leafSet[nb] {
				delete(leafSet, nb)
				dist[nb] = 1
				queue = append(queue, nb)
			}
		}
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		dv := dist[p]
		if blk := d.w.GetBlock(p); dv < 7 {
			if nb, ok := world.WithProperty(blk, "distance", strconv.Itoa(dv)); ok {
				s.setSilentBlock(d, p, nb)
			}
		}
		if dv+1 >= 7 {
			continue
		}
		for _, nb := range posNeighbors(p) {
			if leafSet[nb] {
				delete(leafSet, nb)
				dist[nb] = dv + 1
				queue = append(queue, nb)
			}
		}
	}
}

func posNeighbors(p world.Pos) [6]world.Pos {
	return [6]world.Pos{
		{X: p.X + 1, Y: p.Y, Z: p.Z}, {X: p.X - 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y + 1, Z: p.Z}, {X: p.X, Y: p.Y - 1, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z + 1}, {X: p.X, Y: p.Y, Z: p.Z - 1},
	}
}

// mossReplaceable is #minecraft:moss_replaceable — the ground cells a
// vegetation patch may overwrite: base_stone_overworld + cave_vines +
// dirt + mud + moss_blocks + grass_blocks.
var mossReplaceable = map[string]bool{
	"stone": true, "granite": true, "diorite": true, "andesite": true,
	"tuff": true, "deepslate": true,
	"cave_vines": true, "cave_vines_plant": true,
	"dirt": true, "coarse_dirt": true, "rooted_dirt": true,
	"mud": true, "muddy_mangrove_roots": true,
	"moss_block": true, "pale_moss_block": true,
	"grass_block": true, "podzol": true, "mycelium": true,
}

// paleMossPatch — VegetationPatchFeature (pale_moss_patch, surface
// floor): radius U{2..4}+1 per axis; corner columns skipped,
// edge-not-corner columns kept at 0.75; per surviving column descend
// through air (<=5) then ascend through solid (<=5) to the first air
// cell; when that cell is empty and the ground below has a sturdy UP
// face, depth 1 writes pale_moss_block on #moss_replaceable ground and
// the ground cell joins the surface set. Each surface cell then rolls
// 0.3 for the pale_moss_vegetation simple_block (moss carpet 25 /
// short_grass 25 / tall_grass 10) in the air cell above it.
func (s *Server) paleMossPatch(d *dimension, origin world.Pos) {
	rx := s.rng.intn(3) + 3
	rz := s.rng.intn(3) + 3
	var surface []world.Pos
	for x := -rx; x <= rx; x++ {
		edgeX := x == -rx || x == rx
		for z := -rz; z <= rz; z++ {
			edgeZ := z == -rz || z == rz
			if edgeX && edgeZ {
				continue
			}
			if (edgeX || edgeZ) && s.rng.nextDouble() >= 0.75 {
				continue
			}
			p := world.Pos{X: origin.X + x, Y: origin.Y, Z: origin.Z + z}
			n := 0
			for isAirCell(d.w.GetBlock(p)) && n < 5 {
				p.Y--
				n++
			}
			n = 0
			for !isAirCell(d.w.GetBlock(p)) && n < 5 {
				p.Y++
				n++
			}
			below := world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}
			if !isAirCell(d.w.GetBlock(p)) || !speleoSturdy(d, below) {
				continue
			}
			gn, _ := world.StateOwner(d.w.GetBlock(below))
			if gn != "pale_moss_block" {
				if !mossReplaceable[gn] {
					continue
				}
				mb, _ := world.BlockStateOf("pale_moss_block", nil)
				s.setNaturalBlock(d, below, mb)
			}
			surface = append(surface, below)
		}
	}
	for _, g := range surface {
		if s.rng.nextDouble() >= 0.3 {
			continue
		}
		s.paleMossVegetation(d, world.Pos{X: g.X, Y: g.Y + 1, Z: g.Z})
	}
}

// paleMossVegetation — pale_moss_vegetation simple_block: weighted
// state pick (carpet 25, short_grass 25, tall_grass lower 10).
func (s *Server) paleMossVegetation(d *dimension, p world.Pos) {
	var props map[string]string
	name := "tall_grass"
	switch r := s.rng.intn(60); {
	case r < 25:
		name, props = "pale_moss_carpet", map[string]string{"bottom": "true"}
	case r < 50:
		name = "short_grass"
	default:
		props = map[string]string{"half": "lower"}
	}
	if b, ok := world.BlockStateOf(name, props); ok {
		s.setNaturalBlock(d, p, b)
	}
}

// creakingHeartDecor — CreakingHeartDecorator (p=1.0): shuffle the log
// list, take the first log whose six neighbours are all #logs, and
// replace it with creaking_heart{creaking_heart_state=dormant,
// natural=true}.
func (s *Server) creakingHeartDecor(d *dimension, logs []world.Pos) {
	if len(logs) == 0 || s.rng.nextDouble() >= 1.0 {
		return
	}
	sh := append([]world.Pos(nil), logs...)
	for i := len(sh) - 1; i > 0; i-- {
		j := s.rng.intn(i + 1)
		sh[i], sh[j] = sh[j], sh[i]
	}
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for _, lp := range sh {
		all := true
		for _, o := range dirs {
			n, _ := world.StateOwner(d.w.GetBlock(world.Pos{X: lp.X + o[0], Y: lp.Y + o[1], Z: lp.Z + o[2]}))
			if !treeLogTag(n) {
				all = false
				break
			}
		}
		if all {
			if hb, ok := world.BlockStateOf("creaking_heart", map[string]string{"creaking_heart_state": "dormant", "natural": "true"}); ok {
				s.setNaturalBlock(d, lp, hb)
			}
			return
		}
	}
}

// mangroveLogsGrowThrough — #mangrove_logs_can_grow_through:
// UpwardsBranchingTrunkPlacer widens validTreePos with these.
var mangroveLogsGrowThrough = map[string]bool{
	"mud": true, "muddy_mangrove_roots": true, "mangrove_roots": true,
	"mangrove_leaves": true, "mangrove_log": true, "mangrove_propagule": true,
	"moss_carpet": true, "vine": true,
}

// mangroveRootsGrowThrough — #mangrove_roots_can_grow_through: the
// root placer's extra pass-through set.
var mangroveRootsGrowThrough = map[string]bool{
	"mud": true, "muddy_mangrove_roots": true, "mangrove_roots": true,
	"moss_carpet": true, "vine": true, "mangrove_propagule": true,
	"snow": true,
}

// mangroveTrunk — UpwardsBranchingTrunkPlacer: a vertical column at
// the (raised) trunk origin; each placed log below the top rolls
// 0.5 for a horizontal branch. The branch's walk starts
// max(0, extraBranchLength − extraBranchLength − 1) — two samples of
// the U{0..1} provider — and steps a random direction for
// extraBranchSteps = U{1..max} placements. Every step attaches
// foliage at its own cell; a branch ending more than one above its
// base also attaches one and three cells above its tip. The column
// top attaches one cell up.
func (s *Server) mangroveTrunk(d *dimension, origin world.Pos, treeHeight int, shape treeShape, logState world.Block, logs *[]world.Pos) []foliageAttach {
	var attachments []foliageAttach
	for hp := 0; hp < treeHeight; hp++ {
		y := origin.Y + hp
		lp := world.Pos{X: origin.X, Y: y, Z: origin.Z}
		if s.mangrovePlaceLog(d, lp, logState, logs) && hp < treeHeight-1 && s.rng.nextDouble() < 0.5 {
			dir := horizDirs[s.rng.intn(4)]
			branchPos := s.rng.intn(2) - s.rng.intn(2) - 1
			if branchPos < 0 {
				branchPos = 0
			}
			branchSteps := 1 + s.rng.intn(shape.branchStepsMax)
			s.mangroveBranch(d, lp, y, treeHeight, dir, branchPos, branchSteps, logState, logs, &attachments)
		}
		if hp == treeHeight-1 {
			attachments = append(attachments, foliageAttach{
				pos: world.Pos{X: origin.X, Y: y + 1, Z: origin.Z}})
		}
	}
	return attachments
}

func (s *Server) mangroveBranch(d *dimension, logPos world.Pos, currentHeight, treeHeight int, dir world.Pos, branchPos, branchSteps int, logState world.Block, logs *[]world.Pos, attachments *[]foliageAttach) {
	heightAlong := currentHeight + branchPos
	logX, logZ := logPos.X, logPos.Z
	i := branchPos
	for i < treeHeight && branchSteps > 0 {
		if i >= 1 {
			placementHeight := currentHeight + i
			logX += dir.X
			logZ += dir.Z
			heightAlong = placementHeight
			if s.mangrovePlaceLog(d, world.Pos{X: logX, Y: placementHeight, Z: logZ}, logState, logs) {
				heightAlong = placementHeight + 1
			}
			*attachments = append(*attachments, foliageAttach{
				pos: world.Pos{X: logX, Y: placementHeight, Z: logZ}})
		}
		i++
		branchSteps--
	}
	if heightAlong-currentHeight > 1 {
		fp := world.Pos{X: logX, Y: heightAlong, Z: logZ}
		*attachments = append(*attachments, foliageAttach{pos: fp})
		*attachments = append(*attachments, foliageAttach{
			pos: world.Pos{X: fp.X, Y: fp.Y - 2, Z: fp.Z}})
	}
}

// placeLogIfFree: the upwards placer's validTreePos is
// TreeFeature.validTreePos (air || #replaceable_by_trees) OR'ed with
// canGrowThrough(#mangrove_logs_can_grow_through).
func (s *Server) mangrovePlaceLog(d *dimension, p world.Pos, logState world.Block, logs *[]world.Pos) bool {
	bn, _ := world.StateOwner(d.w.GetBlock(p))
	name := strings.TrimPrefix(bn, "minecraft:")
	if !treeReplaceable[name] && !mangroveLogsGrowThrough[name] {
		return false
	}
	s.setNaturalBlock(d, p, logState)
	*logs = append(*logs, p)
	return true
}

// mangroveRoots — MangroveRootPlacer.placeRoots: every column cell
// between the feature origin and the raised trunk origin must pass
// canPlaceRoot or the whole placement fails. The written set is the
// cell below the trunk plus, per horizontal direction, a simulated
// root walk seeded one step out from the trunk origin (the seed cell
// itself is always written). A root chain reaching layer 15 or a
// direction accumulating over 15 cells fails the feature.
func (s *Server) mangroveRoots(d *dimension, origin, trunkOrigin world.Pos) bool {
	canRoot := func(p world.Pos) bool {
		bn, _ := world.StateOwner(d.w.GetBlock(p))
		name := strings.TrimPrefix(bn, "minecraft:")
		return treeReplaceable[name] || mangroveRootsGrowThrough[name]
	}
	for p := origin; p.Y < trunkOrigin.Y; p.Y++ {
		if !canRoot(p) {
			return false
		}
	}
	positions := []world.Pos{{X: trunkOrigin.X, Y: trunkOrigin.Y - 1, Z: trunkOrigin.Z}}
	for _, dir := range horizDirs {
		seed := world.Pos{X: trunkOrigin.X + dir.X, Y: trunkOrigin.Y, Z: trunkOrigin.Z + dir.Z}
		var dirPositions []world.Pos
		if !s.simMangroveRoots(d, seed, dir, trunkOrigin, &dirPositions, 0, canRoot) {
			return false
		}
		positions = append(positions, dirPositions...)
		positions = append(positions, seed)
	}
	for _, rp := range positions {
		s.placeMangroveRoot(d, rp)
	}
	return true
}

// simulateRoots: from each candidate the walk recurses; failing any
// candidate's subtree fails the whole direction.
func (s *Server) simMangroveRoots(d *dimension, pos, dir, rootOrigin world.Pos, positions *[]world.Pos, layer int, canRoot func(world.Pos) bool) bool {
	if layer == 15 || len(*positions) > 15 {
		return false
	}
	for _, np := range s.mangroveRootCandidates(pos, dir, rootOrigin) {
		if canRoot(np) {
			*positions = append(*positions, np)
			if !s.simMangroveRoots(d, np, dir, rootOrigin, positions, layer+1, canRoot) {
				return false
			}
		}
	}
	return true
}

// potentialRootPositions: Manhattan distance from the trunk origin
// picks the candidate set — past width 5 the walk can only drop;
// inside it can skew downward at 0.2 or continue horizontally at a
// coin flip.
func (s *Server) mangroveRootCandidates(pos, dir, rootOrigin world.Pos) []world.Pos {
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	nextTo := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
	nextToBelow := world.Pos{X: nextTo.X, Y: nextTo.Y - 1, Z: nextTo.Z}
	w := absi(pos.X-rootOrigin.X) + absi(pos.Y-rootOrigin.Y) + absi(pos.Z-rootOrigin.Z)
	switch {
	case w > 5 && w <= 8:
		if s.rng.nextDouble() < 0.2 {
			return []world.Pos{below, nextToBelow}
		}
		return []world.Pos{below}
	case w > 8:
		return []world.Pos{below}
	case s.rng.nextDouble() < 0.2:
		return []world.Pos{below}
	default:
		if s.rng.nextDouble() < 0.5 {
			return []world.Pos{nextTo}
		}
		return []world.Pos{below}
	}
}

// placeMangroveRoot — RootPlacer.placeRoot: in mud the
// muddy_roots_provider writes muddy_mangrove_roots{axis:y} (the root
// placer checks MUD and MUDDY_MANGROVE_ROOTS); anywhere else the
// roots_provider writes mangrove_roots, waterlogged when the cell
// held water. Then the above_root roll (0.5) drops moss_carpet on
// the cell above when it is air.
func (s *Server) placeMangroveRoot(d *dimension, p world.Pos) {
	cur := d.w.GetBlock(p)
	bn, _ := world.StateOwner(cur)
	name := strings.TrimPrefix(bn, "minecraft:")
	if name == "mud" || name == "muddy_mangrove_roots" {
		if r, ok := world.BlockStateOf("muddy_mangrove_roots", map[string]string{"axis": "y"}); ok {
			s.setNaturalBlock(d, p, s.foldWaterlogged(d, p, r))
		}
	} else {
		if r, ok := world.BlockStateOf("mangrove_roots", map[string]string{"waterlogged": "false"}); ok {
			s.setNaturalBlock(d, p, s.foldWaterlogged(d, p, r))
		}
	}
	if s.rng.nextDouble() < 0.5 {
		above := world.Pos{X: p.X, Y: p.Y + 1, Z: p.Z}
		if isAirCell(d.w.GetBlock(above)) {
			if c, ok := world.BlockStateOf("moss_carpet", nil); ok {
				s.setNaturalBlock(d, above, c)
			}
		}
	}
}

// foldWaterlogged — getPotentiallyWaterloggedState: a roots_provider
// state gains waterlogged=true when the cell's fluid is water.
func (s *Server) foldWaterlogged(d *dimension, p world.Pos, blk world.Block) world.Block {
	if bn, _ := world.StateOwner(d.w.GetBlock(p)); strings.TrimPrefix(bn, "minecraft:") == "water" {
		if nb, ok := world.WithProperty(blk, "waterlogged", "true"); ok {
			return nb
		}
	}
	return blk
}

// randomSpreadCanopy — RandomSpreadFoliagePlacer: per attachment,
// leaf_placement_attempts cells at ±(intn(radius+1)−intn(radius+1))
// xz and ±(intn(height)−intn(height)) y.
func (s *Server) randomSpreadCanopy(d *dimension, attachments []foliageAttach, shape treeShape, placeLeaf func(world.Pos)) {
	r, h := shape.spreadRadius+1, shape.spreadHeight
	for _, at := range attachments {
		for i := 0; i < shape.spreadAttempts; i++ {
			p := world.Pos{
				X: at.pos.X + s.rng.intn(r) - s.rng.intn(r),
				Y: at.pos.Y + s.rng.intn(h) - s.rng.intn(h),
				Z: at.pos.Z + s.rng.intn(r) - s.rng.intn(r),
			}
			placeLeaf(p)
		}
	}
}

// bendingTrunk — BendingTrunkPlacer.placeTrunk: one horizontal bend
// direction up front; the column rises treeHeight−1 levels, each
// stepping sideways when level+1 < branchesHeight+U{0..1}, attaching
// foliage at every level >= min_height_for_leaves; then a final bend
// walks bendLength+1 more cells sideways attaching each. placeLog
// runs only through validTreePos.
func (s *Server) bendingTrunk(d *dimension, pos world.Pos, treeHeight int, shape treeShape, logState world.Block, logs *[]world.Pos) []foliageAttach {
	dir := horizDirs[s.rng.intn(4)]
	branchesHeight := treeHeight - 1
	var attachments []foliageAttach
	p := pos
	for i := 0; i <= branchesHeight; i++ {
		if i+1 < branchesHeight+s.rng.intn(2) {
			p = world.Pos{X: p.X + dir.X, Y: p.Y, Z: p.Z + dir.Z}
		}
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			*logs = append(*logs, p)
		}
		if i >= shape.minLeafLevel {
			attachments = append(attachments, foliageAttach{pos: p, radiusOffset: 0})
		}
		p = world.Pos{X: p.X, Y: p.Y + 1, Z: p.Z}
	}
	bendLen := shape.bendMin + s.rng.intn(shape.bendMax-shape.bendMin+1)
	for j := 0; j <= bendLen; j++ {
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			*logs = append(*logs, p)
		}
		attachments = append(attachments, foliageAttach{pos: p, radiusOffset: 0})
		p = world.Pos{X: p.X + dir.X, Y: p.Y, Z: p.Z + dir.Z}
	}
	return attachments
}

// mangroveDecorators — the mangrove decorator set:
// {leave_vine 0.125, attached_to_leaves mangrove_propagule 0.14,
// beehive 0.01}.
func (s *Server) mangroveDecorators(d *dimension, logs, leaves []world.Pos) {
	s.leaveVines(d, leaves, 0.125)
	s.attachedPropagules(d, leaves)
	s.beehiveDecor(d, logs, leaves, 0.01)
}

// attachedPropagules — AttachedToLeavesDecorator(p 0.14, directions
// [down]): shuffled leaves each roll the chance; a hit needs two air
// cells along the direction, writes mangrove_propagule{age U{0..4},
// hanging:true, stage:0, waterlogged:false} at the first, and
// blacklists the placement cell's horizontal ring for the rest of
// the pass.
func (s *Server) attachedPropagules(d *dimension, leaves []world.Pos) {
	shuffled := make([]world.Pos, len(leaves))
	copy(shuffled, leaves)
	for i := len(shuffled) - 1; i > 0; i-- {
		j := s.rng.intn(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	blacklist := map[world.Pos]bool{}
	for _, lf := range shuffled {
		pp := world.Pos{X: lf.X, Y: lf.Y - 1, Z: lf.Z}
		if blacklist[pp] || s.rng.nextDouble() >= 0.14 {
			continue
		}
		pp2 := world.Pos{X: pp.X, Y: pp.Y - 1, Z: pp.Z}
		if !isAirCell(d.w.GetBlock(pp)) || !isAirCell(d.w.GetBlock(pp2)) {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				blacklist[world.Pos{X: pp.X + dx, Y: pp.Y, Z: pp.Z + dz}] = true
			}
		}
		age := []string{"0", "1", "2", "3", "4"}[s.rng.intn(5)]
		if nb, ok := world.BlockStateOf("mangrove_propagule", map[string]string{
			"age": age, "hanging": "true", "stage": "0", "waterlogged": "false",
		}); ok {
			s.setNaturalBlock(d, pp, nb)
		}
	}
}

// beehiveDecor — BeehiveDecorator(probability): with leaves, the
// nest row is max(leaves.get(0).Y−1, logs.get(0).Y+1); without,
// logs.get(0).Y+1+U{0..2} capped at the last log. The decorator sees
// the placement sets through java.util.HashSet order — endpoints are
// bucket-ordered, so leaves.get(0) is an arbitrary canopy cell, not
// the first row written (javaSetOrder). Candidates are the
// horizontal neighbors of every log at that height; the first
// shuffled air cell takes the nest's default state.
func (s *Server) beehiveDecor(d *dimension, logs, leaves []world.Pos, p float64) {
	if s.rng.nextDouble() >= p || len(logs) == 0 {
		return
	}
	leaf0, _ := javaSetOrder(leaves)
	log0, logLast := javaSetOrder(logs)
	var y int
	if len(leaves) > 0 {
		y = leaf0.Y - 1
		if log0.Y+1 > y {
			y = log0.Y + 1
		}
	} else {
		y = log0.Y + 1 + s.rng.intn(3)
		if y > logLast.Y {
			y = logLast.Y
		}
	}
	var candidates []world.Pos
	for _, lp := range logs {
		if lp.Y != y {
			continue
		}
		for _, dir := range horizDirs {
			candidates = append(candidates, world.Pos{X: lp.X + dir.X, Y: lp.Y, Z: lp.Z + dir.Z})
		}
	}
	for i := len(candidates) - 1; i > 0; i-- {
		j := s.rng.intn(i + 1)
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
	for _, cp := range candidates {
		if !isAirCell(d.w.GetBlock(cp)) {
			continue
		}
		if nb, ok := world.BlockStateOf("bee_nest", nil); ok {
			s.setNaturalBlock(d, cp, nb)
		}
		return
	}
}

// javaSetOrder returns the first and last elements a
// java.util.HashSet<BlockPos> would iterate in — the order
// TreeDecorator.Context lists arrive in (it copies Sets.newHashSet()
// into ObjectArrayLists). HashMap spreads h = h^(h>>>16) and iterates
// buckets h&(cap-1) ascending, ties in insert order; capacity is the
// smallest power of two holding n at load 0.75 (starts 16).
// Vec3i.hashCode() = x + 31*y + 961*z.
func javaSetOrder(pos []world.Pos) (world.Pos, world.Pos) {
	if len(pos) == 0 {
		return world.Pos{}, world.Pos{}
	}
	first, last := pos[0], pos[0]
	cap := 16
	for len(pos) > cap*3/4 {
		cap <<= 1
	}
	fb, lb := uint32(math.MaxUint32), uint32(0)
	for _, p := range pos {
		h := uint32(int32(p.X) + int32(p.Y)*31 + int32(p.Z)*961)
		h ^= h >> 16
		b := h & uint32(cap-1)
		if b < fb {
			fb = b
			first = p
		}
		if b >= lb {
			lb = b
			last = p
		}
	}
	return first, last
}

// propaguleRule — MangrovePropaguleBlock.randomTick: a hanging
// propagule only cycles age toward 4; a planted one grows at the
// SaplingBlock 1/7 rate with no light check.
func propaguleRule(s *Server, d *dimension, pos world.Pos, blk world.Block) {
	if hanging, _ := world.StateProperty(blk, "hanging"); hanging == "true" {
		if age, _ := world.StateProperty(blk, "age"); age != "4" {
			if nb, ok := world.WithProperty(blk, "age", string(age[0]+1)); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		}
		return
	}
	if s.rng.intn(7) != 0 {
		return
	}
	s.advanceTree(d, pos, blk)
}
