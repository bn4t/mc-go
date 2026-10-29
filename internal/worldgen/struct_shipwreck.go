package worldgen

// Shipwrecks (ShipwreckStructure, ShipwreckPieces): one template piece
// at (chunk min, 90, chunk min) with a random rotation about the pivot
// (4, 0, 15), structure blocks and air ignored. Templates wider than
// 32 or taller than 32 (isTooBigToFitInWorldGenRegion) take their
// height at start generation from the noise corner heights; the
// others average the ocean-floor (or surface, beached) heightmap over
// their unrotated footprint when first post-processed. Beached ones
// sink to the lowest height minus half the template height and 0..2.

var shipwreckBeached = []string{
	"with_mast", "sideways_full", "sideways_fronthalf", "sideways_backhalf", "rightsideup_full",
	"rightsideup_fronthalf", "rightsideup_backhalf", "with_mast_degraded", "rightsideup_full_degraded",
	"rightsideup_fronthalf_degraded", "rightsideup_backhalf_degraded",
}

var shipwreckOcean = []string{
	"with_mast", "upsidedown_full", "upsidedown_fronthalf", "upsidedown_backhalf", "sideways_full",
	"sideways_fronthalf", "sideways_backhalf", "rightsideup_full", "rightsideup_fronthalf",
	"rightsideup_backhalf", "with_mast_degraded", "upsidedown_full_degraded", "upsidedown_fronthalf_degraded",
	"upsidedown_backhalf_degraded", "sideways_full_degraded", "sideways_fronthalf_degraded",
	"sideways_backhalf_degraded", "rightsideup_full_degraded", "rightsideup_fronthalf_degraded",
	"rightsideup_backhalf_degraded",
}

var shipwreckLoot = map[string]string{
	"map_chest":      "minecraft:chests/shipwreck_map",
	"treasure_chest": "minecraft:chests/shipwreck_treasure",
	"supply_chest":   "minecraft:chests/shipwreck_supply",
}

func init() {
	structureTypes["shipwreck"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		var beached bool
		jsonUnmarshal(d.raw["is_beached"], &beached)
		hm := HMOceanFloorWG
		if beached {
			hm = HMWorldSurfaceWG
		}
		return func(c *GenContext) (*GenerationStub, bool) {
			return onTopOfChunkCenter(c, hm, func(add func(StructurePiece)) {
				rot := int(c.Random.NextIntN(4))
				list := shipwreckOcean
				if beached {
					list = shipwreckBeached
				}
				name := "minecraft:shipwreck/" + list[c.Random.NextIntN(int32(len(list)))]
				p := newShipwreckPiece(name, BlockPos{c.ChunkX * 16, 90, c.ChunkZ * 16}, rot, beached)
				add(p)
				if p.tooBig() {
					bb := p.bb
					var y int32
					if beached {
						// ShipwreckStructure passes (minX, xSpan, minZ,
						// zSpan) to getLowestY(x, z, width, depth).
						low := wreckLowestCornerY(c, bb.MinX, bb.MaxX-bb.MinX+1, bb.MinZ, bb.MaxZ-bb.MinZ+1)
						y = p.beachedY(low, c.Random)
					} else {
						y = meanCornerY(c, bb.MinX, bb.MaxX-bb.MinX+1, bb.MinZ, bb.MaxZ-bb.MinZ+1)
					}
					p.adjustHeight(y)
				}
			}), true
		}
	}
	pieceLoaders["shipwreck"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		rot := rotationFromName(fmt3str(t["Rot"]))
		tp, err := loadTplPiece(t, func(string) placeSettings { return shipwreckSettings(rot) })
		if err != nil {
			return nil, err
		}
		return &shipwreckPiece{tplPiece: tp, rot: rot, beached: tagInt(t, "isBeached") != 0, adjusted: tagInt(t, "height_adjusted") != 0}, nil
	}
}

func fmt3str(v any) string { s, _ := v.(string); return s }

// cornerHeights is Structure.getCornerHeights: first occupied
// surface heights at (x, z), (x, z+d), (x+w, z), (x+w, z+d).
func cornerHeights(c *GenContext, x, w, z, d int32) [4]int32 {
	g := c.G
	return [4]int32{
		g.firstOccupiedHeight(x, z, HMWorldSurfaceWG),
		g.firstOccupiedHeight(x, z+d, HMWorldSurfaceWG),
		g.firstOccupiedHeight(x+w, z, HMWorldSurfaceWG),
		g.firstOccupiedHeight(x+w, z+d, HMWorldSurfaceWG),
	}
}

// meanCornerY is Structure.getMeanFirstOccupiedHeight(x, w, z, d).
func meanCornerY(c *GenContext, x, w, z, d int32) int32 {
	h := cornerHeights(c, x, w, z, d)
	return (h[0] + h[1] + h[2] + h[3]) / 4
}

// wreckLowestCornerY is Structure.getLowestY(ctx, a, b, c, d), which reads
// its arguments as (x, z, width, depth).
func wreckLowestCornerY(c *GenContext, a, b, cc, d int32) int32 {
	h := cornerHeights(c, a, cc, b, d)
	return min(h[0], min(h[1], min(h[2], h[3])))
}

func shipwreckSettings(rot int) placeSettings {
	s := *newPlaceSettings()
	s.rotation = rot
	s.mirror = mirNone
	s.pivot = BlockPos{4, 0, 15}
	s.processors = []tplProcessor{ignoreBlocksProc(map[string]bool{"structure_block": true, "air": true})}
	return s
}

type shipwreckPiece struct {
	tplPiece
	rot      int
	beached  bool
	adjusted bool
}

func newShipwreckPiece(name string, pos BlockPos, rot int, beached bool) *shipwreckPiece {
	p := &shipwreckPiece{tplPiece: newTplPiece("shipwreck", name, shipwreckSettings(rot), pos, 0), rot: rot, beached: beached}
	return p
}

// SaveData is ShipwreckPiece.addAdditionalSaveData.
func (p *shipwreckPiece) SaveData(t map[string]any) {
	p.saveTpl(t)
	t["isBeached"] = boolByte(p.beached)
	t["Rot"] = rotationEnumNames[p.rot]
	t["height_adjusted"] = boolByte(p.adjusted)
}


// tooBig is ShipwreckPiece.isTooBigToFitInWorldGenRegion (x or y size
// over 32).
func (p *shipwreckPiece) tooBig() bool { return p.tpl.size[0] > 32 || p.tpl.size[1] > 32 }

// beachedY is ShipwreckPiece.calculateBeachedPosition.
func (p *shipwreckPiece) beachedY(low int32, r Random) int32 {
	return low - p.tpl.size[1]/2 - r.NextIntN(3)
}

// adjustHeight is ShipwreckPiece.adjustPositionHeight: the template
// position moves (the box follows on placement).
func (p *shipwreckPiece) adjustHeight(y int32) {
	p.adjusted = true
	p.pos = BlockPos{p.pos[0], y, p.pos[2]}
}

// PostProcess is ShipwreckPiece.postProcess.
func (p *shipwreckPiece) PostProcess(c *PieceContext) {
	if !p.adjusted && !p.tooBig() {
		rg := c.Region
		low := rg.MaxY() + 1
		sum := int32(0)
		size := p.tpl.size
		hm := HMOceanFloorWG
		if p.beached {
			hm = HMWorldSurfaceWG
		}
		if n := size[0] * size[2]; n == 0 {
			sum = rg.Height(hm, p.pos[0], p.pos[2])
		} else {
			// BlockPos.betweenClosed over the unrotated footprint.
			for z := int32(0); z < size[2]; z++ {
				for x := int32(0); x < size[0]; x++ {
					h := rg.Height(hm, p.pos[0]+x, p.pos[2]+z)
					sum += h
					low = min(low, h)
				}
			}
			sum /= n
		}
		if p.beached {
			p.adjustHeight(p.beachedY(low, c.Random))
		} else {
			p.adjustHeight(sum)
		}
	}
	p.dataMarker = func(c *PieceContext, meta string, pos BlockPos) {
		if t, ok := shipwreckLoot[meta]; ok {
			setContainerLoot(c, c.Random, offset(pos, 0, -1, 0), t)
		}
	}
	p.placeTemplate(c)
}
