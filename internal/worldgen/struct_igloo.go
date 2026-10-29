package worldgen

import (
	"strings"
	"sync"

	"mc-go/internal/world"
)

// TemplateStructurePiece for the legacy structures (igloo, woodland
// mansion), measured from the 26.2 bytecode: a template placed at
// templatePosition with the piece's place settings, orientation north,
// saved TPX / TPY / TPZ / Template. postProcess clips the settings to
// the chunk box, recomputes the box from the template, places it with
// the start pivot and then hands every structure block in DATA mode
// (palette order, inside the box) to the piece's handleDataMarker.

type legacyTemplatePiece struct {
	legacyPiece
	name     string // the saved "Template" (namespaced)
	tpl      *structTemplate
	settings func() *placeSettings
	pos      BlockPos
	marker   func(c *PieceContext, meta string, pos BlockPos)
	mu       sync.Mutex
}

func (p *legacyTemplatePiece) initTemplate(typ, name string, settings func() *placeSettings, pos BlockPos) {
	p.name, p.tpl, p.settings, p.pos = name, loadTemplate(name), settings, pos
	p.legacyPiece = newLegacyPiece(typ, 0, bbFrom(p.tpl.boundingBox(settings(), pos)))
	p.setOrientation(dirNorth)
}

func (p *legacyTemplatePiece) loadTemplateBase(typ string, t map[string]any, settings func(name string) func() *placeSettings) error {
	lp, err := loadLegacyBase(typ, t)
	if err != nil {
		return err
	}
	name, _ := t["Template"].(string)
	p.name = name
	p.pos = BlockPos{tagInt(t, "TPX"), tagInt(t, "TPY"), tagInt(t, "TPZ")}
	p.tpl = loadTemplate(name)
	p.settings = settings(name)
	p.legacyPiece = lp
	p.setOrientation(dirNorth)
	p.Box = bbFrom(p.tpl.boundingBox(p.settings(), p.pos))
	return nil
}

func (p *legacyTemplatePiece) BoundingBox() BoundingBox {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Box
}

func (p *legacyTemplatePiece) Move(dx, dy, dz int32) {
	p.Box = p.Box.Moved(dx, dy, dz)
	p.pos = offset(p.pos, dx, dy, dz)
}

func (p *legacyTemplatePiece) saveTemplate(t map[string]any) {
	t["TPX"], t["TPY"], t["TPZ"] = p.pos[0], p.pos[1], p.pos[2]
	t["Template"] = p.name
}

// placeTemplate is TemplateStructurePiece.postProcess (caller holds mu).
func (p *legacyTemplatePiece) placeTemplate(c *PieceContext) {
	s := p.settings()
	b := c.ChunkBox.internal()
	s.bb = &b
	p.Box = bbFrom(p.tpl.boundingBox(s, p.pos))
	if !p.tpl.placeInWorld(c.Region, p.pos, c.Pivot, s, c.Random) {
		return
	}
	if p.marker == nil || len(p.tpl.palettes) == 0 {
		return
	}
	pal := p.tpl.palettes[s.randomAt(p.pos).NextIntN(int32(len(p.tpl.palettes)))]
	for _, blk := range pal {
		if !isBlock(blk.state, "structure_block") || blk.nbt == nil {
			continue
		}
		wp := offset(tplTransform(blk.pos, s.mirror, s.rotation, s.pivot), p.pos[0], p.pos[1], p.pos[2])
		if !c.ChunkBox.IsInside(wp) {
			continue
		}
		if mode, _ := blk.nbt["mode"].(string); mode != "DATA" {
			continue
		}
		meta, _ := blk.nbt["metadata"].(string)
		p.marker(c, meta, wp)
	}
}

// Igloos (IglooStructure + IglooPieces): a random rotation, the top on
// the chunk's (0, 90, 0) and, half the time, a laboratory 4 to 11 ladder
// sections below with the ladder pieces between. Each piece is placed
// with its template's pivot, structure blocks ignored, waterlogging
// ignored, after moving (for that placement only) to the WORLD_SURFACE_WG
// height at a pivot-relative column minus 91; the top then puts a snow
// block over its hatch unless air or a ladder lies below it. The
// "chest" data marker turns to air and gives the chest under it the
// igloo loot table.

var iglooPivots = map[string]BlockPos{"igloo/top": {3, 5, 5}, "igloo/middle": {1, 3, 1}, "igloo/bottom": {3, 6, 7}}
var iglooOffsets = map[string]BlockPos{"igloo/top": {0, 0, 0}, "igloo/middle": {2, -3, 4}, "igloo/bottom": {0, -3, -2}}

type iglooPiece struct {
	legacyTemplatePiece
	rot int
}

func iglooSettings(rot int, name string) func() *placeSettings {
	return func() *placeSettings {
		s := newPlaceSettings()
		s.rotation = rot
		s.mirror = mirNone
		s.pivot = iglooPivots[trimNS(name)]
		s.processors = append(s.processors, ignoreBlocksProc(map[string]bool{"structure_block": true}))
		s.keepLiquids = false
		return s
	}
}

func newIglooPiece(name string, base BlockPos, rot int, down int32) *iglooPiece {
	p := &iglooPiece{rot: rot}
	o := iglooOffsets[name]
	pos := offset(base, o[0], o[1]-down, o[2])
	p.initTemplate("iglu", "minecraft:"+name, iglooSettings(rot, name), pos)
	p.marker = iglooMarker
	return p
}

// iglooMarker is IglooPiece.handleDataMarker.
func iglooMarker(c *PieceContext, meta string, pos BlockPos) {
	if meta != "chest" {
		return
	}
	c.Region.set(pos, world.Air)
	q := offset(pos, 0, -1, 0)
	if isBlock(c.Region.at(q), "chest") {
		setStructLootTable(c.Region, q, "minecraft:chests/igloo_chest", c.Random.NextLong())
	}
}

func (p *iglooPiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveTemplate(t)
	t["Rot"] = rotationEnumNames[p.rot]
}

func (p *iglooPiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name := trimNS(p.name)
	s := iglooSettings(p.rot, name)()
	o := iglooOffsets[name]
	q := offset(p.pos, 0, 0, 0)
	rel := tplTransform(BlockPos{3 - o[0], 0, -o[2]}, s.mirror, s.rotation, s.pivot)
	q = offset(q, rel[0], rel[1], rel[2])
	h := c.Region.Height(HMWorldSurfaceWG, q[0], q[2])
	saved := p.pos
	p.pos = offset(p.pos, 0, h-90-1, 0)
	p.placeTemplate(c)
	if name == "igloo/top" {
		r := tplTransform(BlockPos{3, 0, 5}, s.mirror, s.rotation, s.pivot)
		hatch := offset(p.pos, r[0], r[1], r[2])
		below := c.Region.at(offset(hatch, 0, -1, 0))
		if !isAir(below) && !isBlock(below, "ladder") {
			c.Region.set(hatch, blockNamed("snow_block"))
		}
	}
	p.pos = saved
}

func init() {
	structureTypes["igloo"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			x0, z0 := c.ChunkX*16, c.ChunkZ*16
			mx, mz := x0+8, z0+8
			y := g.firstOccupiedHeight(mx, mz, HMWorldSurfaceWG)
			return &GenerationStub{Pos: BlockPos{mx, y, mz}, Generate: func(add func(StructurePiece)) {
				r := c.Random
				base := BlockPos{x0, 90, z0}
				rot := int(r.NextIntN(4))
				if r.NextDouble() < 0.5 {
					n := r.NextIntN(8) + 4
					add(newIglooPiece("igloo/bottom", base, rot, n*3))
					for i := int32(0); i < n-1; i++ {
						add(newIglooPiece("igloo/middle", base, rot, i*3))
					}
				}
				add(newIglooPiece("igloo/top", base, rot, 0))
			}}, true
		}
	}
	pieceLoaders["iglu"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &iglooPiece{}
		rot, _ := t["Rot"].(string)
		for i, n := range rotationEnumNames {
			if strings.EqualFold(rot, n) {
				p.rot = i
			}
		}
		if err := p.loadTemplateBase("iglu", t, func(name string) func() *placeSettings { return iglooSettings(p.rot, trimNS(name)) }); err != nil {
			return nil, err
		}
		p.marker = iglooMarker
		return p, nil
	}
}
