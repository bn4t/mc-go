package worldgen

// The structures of one template or one hand-built piece: buried
// treasure, igloos, shipwrecks and the three scattered features (desert
// pyramids, jungle temples, swamp huts).

func init() {
	structureGenerators["minecraft:buried_treasure"] = buriedTreasure
	structureGenerators["minecraft:igloo"] = igloo
	structureGenerators["minecraft:shipwreck"] = shipwreck
	structureGenerators["minecraft:desert_pyramid"] = scatteredFeature("minecraft:tedp", 21, 15, 21, true)
	structureGenerators["minecraft:jungle_temple"] = scatteredFeature("minecraft:tejp", 12, 10, 15, true)
	structureGenerators["minecraft:swamp_hut"] = scatteredFeature("minecraft:tesh", 7, 7, 9, false)
}

// buriedTreasure is one block at (9, 90, 9) in the chunk, on the ocean
// floor at the chunk's middle.
func buriedTreasure(c *structureContext) (genStub, bool) {
	return c.onTopOfChunkCenter(oceanFloorWG, func() []structurePiece {
		p := &plainPiece{pieceBase: newPieceBase("minecraft:btp", 0, boxAt(Point{c.minX() + 9, 90, c.minZ() + 9}))}
		return []structurePiece{p}
	})
}

// plainPiece is a piece that saves nothing of its own.
type plainPiece struct{ pieceBase }

func (*plainPiece) saveTag(map[string]any) {}

// iglooPiece is one of an igloo's templates.
type iglooPiece struct{ templatePiece }

func (p *iglooPiece) saveTag(tag map[string]any) {
	p.saveTemplate(tag)
	tag["Rot"] = rotationNames[p.settings.rot]
}

// The igloo's templates: where each turns about, and where each sits
// from the top's position.
var (
	iglooPivots  = map[string]Point{"minecraft:igloo/top": {3, 5, 5}, "minecraft:igloo/middle": {1, 3, 1}, "minecraft:igloo/bottom": {3, 6, 7}}
	iglooOffsets = map[string]Point{"minecraft:igloo/top": {}, "minecraft:igloo/middle": {2, -3, 4}, "minecraft:igloo/bottom": {0, -3, -2}}
)

// igloo is the dome at y 90 of the chunk's corner, turned at random;
// half of them have a basement laboratory 4 to 11 ladder sections down.
func igloo(c *structureContext) (genStub, bool) {
	return c.onTopOfChunkCenter(worldSurfaceWG, func() []structurePiece {
		pos := Point{c.minX(), 90, c.minZ()}
		rot := randomRotation(c.random)
		piece := func(name string, down int32) structurePiece {
			at := pos.add(iglooOffsets[name])
			at.Y -= down
			return &iglooPiece{newTemplatePiece("minecraft:iglu", 0, name, placeSettings{rot: rot, pivot: iglooPivots[name]}, at)}
		}
		var out []structurePiece
		if c.random.NextDouble() < 0.5 {
			n := c.random.NextIntn(8) + 4
			out = append(out, piece("minecraft:igloo/bottom", n*3))
			for i := int32(0); i < n-1; i++ {
				out = append(out, piece("minecraft:igloo/middle", i*3))
			}
		}
		return append(out, piece("minecraft:igloo/top", 0))
	})
}

var (
	shipwrecksBeached = []string{"with_mast", "sideways_full", "sideways_fronthalf", "sideways_backhalf",
		"rightsideup_full", "rightsideup_fronthalf", "rightsideup_backhalf", "with_mast_degraded",
		"rightsideup_full_degraded", "rightsideup_fronthalf_degraded", "rightsideup_backhalf_degraded"}
	shipwrecksOcean = []string{"with_mast", "upsidedown_full", "upsidedown_fronthalf", "upsidedown_backhalf",
		"sideways_full", "sideways_fronthalf", "sideways_backhalf", "rightsideup_full", "rightsideup_fronthalf",
		"rightsideup_backhalf", "with_mast_degraded", "upsidedown_full_degraded", "upsidedown_fronthalf_degraded",
		"upsidedown_backhalf_degraded", "sideways_full_degraded", "sideways_fronthalf_degraded",
		"sideways_backhalf_degraded", "rightsideup_full_degraded", "rightsideup_fronthalf_degraded",
		"rightsideup_backhalf_degraded"}
)

type shipwreckPiece struct {
	templatePiece
	beached, heightAdjusted bool
}

func (p *shipwreckPiece) saveTag(tag map[string]any) {
	p.saveTemplate(tag)
	tag["isBeached"] = boolTag(p.beached)
	tag["Rot"] = rotationNames[p.settings.rot]
	tag["height_adjusted"] = boolTag(p.heightAdjusted)
}

func boolTag(b bool) int8 {
	if b {
		return 1
	}
	return 0
}

// shipwreck is a random wreck at y 90 of the chunk's corner, turned at
// random about (4, 0, 15). A wreck too big for a generation region is
// settled now: a beached one into the lowest ground under it (as the
// game measures it), any other at its corners' mean height.
func shipwreck(c *structureContext) (genStub, bool) {
	beached, _ := c.def.cfg["is_beached"].(bool)
	heightmap := oceanFloorWG
	if beached {
		heightmap = worldSurfaceWG
	}
	return c.onTopOfChunkCenter(heightmap, func() []structurePiece {
		rot := randomRotation(c.random)
		pos := Point{c.minX(), 90, c.minZ()}
		names := shipwrecksOcean
		if beached {
			names = shipwrecksBeached
		}
		name := "minecraft:shipwreck/" + names[c.random.NextIntn(int32(len(names)))]
		p := &shipwreckPiece{templatePiece: newTemplatePiece("minecraft:shipwreck", 0, name, placeSettings{rot: rot, pivot: Point{4, 0, 15}}, pos), beached: beached}
		if size := p.template.size; size.X > 32 || size.Y > 32 {
			b := p.box
			var y int32
			if beached {
				// The game passes the box's x span where the z goes and
				// its min z as the width.
				low := c.lowestY(b.minX, b.spanX(), b.minZ, b.spanZ())
				y = low - size.Y/2 - c.random.NextIntn(3)
			} else {
				y = c.meanFirstOccupiedHeight(b.minX, b.spanX(), b.minZ, b.spanZ())
			}
			p.heightAdjusted = true
			p.pos.Y = y
		}
		return []structurePiece{p}
	})
}

// scatteredPiece is a hand-built scattered feature: a box facing a
// random way at y 64 of the chunk's corner.
type scatteredPiece struct {
	pieceBase
	width, height, depth int32
	heightPos            int32
	extra                func(tag map[string]any)
}

func (p *scatteredPiece) saveTag(tag map[string]any) {
	tag["Width"], tag["Height"], tag["Depth"], tag["HPos"] = p.width, p.height, p.depth, p.heightPos
	switch p.kind {
	case "minecraft:tedp":
		for _, k := range []string{"hasPlacedChest0", "hasPlacedChest1", "hasPlacedChest2", "hasPlacedChest3"} {
			tag[k] = int8(0)
		}
	case "minecraft:tejp":
		for _, k := range []string{"placedMainChest", "placedHiddenChest", "placedTrap1", "placedTrap2"} {
			tag[k] = int8(0)
		}
	case "minecraft:tesh":
		tag["Witch"], tag["Cat"] = int8(0), int8(0)
	}
}

// scatteredFeature builds a one-piece feature of the given size. Those
// that need dry ground start only where the lowest corner of the piece's
// footprint stands at or above sea level.
func scatteredFeature(kind string, width, height, depth int32, aboveSea bool) func(*structureContext) (genStub, bool) {
	return func(c *structureContext) (genStub, bool) {
		if aboveSea && c.lowestY(c.minX(), c.minZ(), width, depth) < int32(c.g.rs.Settings.SeaLevel) {
			return genStub{}, false
		}
		return c.onTopOfChunkCenter(worldSurfaceWG, func() []structurePiece {
			dir := randomHorizontal(c.random)
			p := &scatteredPiece{pieceBase: newPieceBase(kind, 0, makeBoundingBox(c.minX(), 64, c.minZ(), dir, width, height, depth)),
				width: width, height: height, depth: depth, heightPos: -1}
			p.setOrientation(dir)
			return []structurePiece{p}
		})
	}
}
