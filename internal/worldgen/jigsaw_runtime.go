package worldgen

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"strings"
)

// PlanRuntimeJigsaw assembles the native jigsaw editor's pieces at an exact
// block position. Planning shares the terrain generator's pool matching,
// priorities, fallback/depth limits and collision constraints. The returned
// pieces are not installed as persistent terrain structure starts.
func PlanRuntimeJigsaw(data Data, seed int64, start BlockPos, pool, target string, depth, minY, height int32, templates func(string) map[string]any, baseHeight func(int32, int32, int) int32) ([]*JigsawPiece, error) {
	// The measured editor's deferred builder emits no pieces at zero depth.
	if depth <= 0 {
		return nil, nil
	}
	g, err := NewGenerator(data, "overworld", "overworld", seed)
	if err != nil {
		return nil, err
	}
	g.RS.SetLevelBounds(minY, height)
	g.runtimeTemplate = templates
	g.runtimeBaseHeight = baseHeight
	r := NewLegacy(0)
	setLargeFeatureSeed(r, seed, start[0]>>4, start[2]>>4)
	c := &GenContext{G: g, Random: r, ChunkX: start[0] >> 4, ChunkZ: start[2] >> 4, Seed: seed}
	cfg := &jigsawConfig{startPool: trimNS(pool), startJigsawName: trimNS(target), maxDepth: depth, projectHM: -1, maxH: 128, maxV: 128, liquid: true}
	cfg.requireStartJigsaw = true
	stub, ok := jigsawFindAt(c, cfg, start)
	if !ok {
		return nil, nil
	}
	var pieces []*JigsawPiece
	stub.Generate(func(piece StructurePiece) {
		if p, ok := piece.(*JigsawPiece); ok {
			pieces = append(pieces, p)
		}
	})
	return pieces, nil
}

// RuntimeJigsawTemplate is a processed native template. Coordinates remain
// local to Origin under Rotation, while processors have already observed the
// transformed positions and the live world's blocks and heightmaps.
type RuntimeJigsawTemplate struct {
	Native      map[string]any
	Origin      BlockPos
	Rotation    int
	KeepLiquids bool
}

func VisitRuntimeJigsawTemplates(piece *JigsawPiece, pivot BlockPos, keep bool, templates func(string) map[string]any, block func(int32, int32, int32) world.Block, height func(int, int32, int32) int32, place func(RuntimeJigsawTemplate)) {
	rg := &Region{runtimeBlock: block, runtimeHeight: height}
	var prepare func(poolElement)
	prepare = func(element poolElement) {
		switch e := element.(type) {
		case *listElement:
			for _, child := range e.elems {
				prepare(child)
			}
		case *singleElement:
			native := templates(e.location)
			if native == nil || len(e.template().palettes) == 0 {
				return
			}
			settings := e.settings(piece.Rotation, BoundingBox{-30000000, -20000000, -30000000, 30000000, 20000000, 30000000}, piece.liquid, keep)
			settings.bb = nil
			palette := e.template().palettes[settings.randomAt(piece.Position).NextIntN(int32(len(e.template().palettes)))]
			processed := processBlocks(rg, piece.Position, pivot, settings, palette)
			states := nbt.List{Elem: nbt.TagCompound}
			blocks := nbt.List{Elem: nbt.TagCompound}
			for _, b := range processed {
				name, props, ok := world.DescribeState(b.state)
				if !ok {
					continue
				}
				state := map[string]any{"Name": "minecraft:" + strings.TrimPrefix(name, "minecraft:")}
				if len(props) > 0 {
					p := map[string]any{}
					for k, v := range props {
						p[k] = v
					}
					state["Properties"] = p
				}
				relative := BlockPos{b.pos[0] - piece.Position[0], b.pos[1] - piece.Position[1], b.pos[2] - piece.Position[2]}
				relative = tplTransform(relative, mirNone, (4-piece.Rotation)%4, BlockPos{})
				entry := map[string]any{"state": int32(len(states.Items)), "pos": nbt.List{Elem: nbt.TagInt, Items: []any{relative[0], relative[1], relative[2]}}}
				if b.nbt != nil {
					entry["nbt"] = b.nbt
				}
				states.Items = append(states.Items, state)
				blocks.Items = append(blocks.Items, entry)
			}
			result := map[string]any{}
			for k, v := range native {
				result[k] = v
			}
			delete(result, "palettes")
			result["palette"] = states
			result["blocks"] = blocks
			place(RuntimeJigsawTemplate{Native: result, Origin: piece.Position, Rotation: piece.Rotation, KeepLiquids: settings.keepLiquids})
		}
	}
	prepare(piece.element)
}
