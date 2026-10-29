package server

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

type jigsawRuntimeData struct {
	base  worldgen.Data
	roots []string
}

func (d jigsawRuntimeData) Get(key string) (json.RawMessage, bool) {
	kind, id, ok := strings.Cut(key, "/")
	if ok && (kind == "template_pool" || kind == "processor_list") {
		if !strings.ContainsRune(id, ':') {
			id = "minecraft:" + id
		}
		ns, path, valid := strings.Cut(id, ":")
		if valid && structureName(id) == id {
			for i := len(d.roots) - 1; i >= 0; i-- {
				root, err := os.OpenRoot(d.roots[i])
				if err != nil {
					continue
				}
				raw, err := root.ReadFile(filepath.Join(ns, "worldgen", kind, filepath.FromSlash(path)+".json"))
				root.Close()
				if err == nil && json.Valid(raw) {
					return raw, true
				}
			}
		}
	}
	return d.base.Get(key)
}
func (s *Server) jigsawRuntimeData() worldgen.Data {
	data := worldgen.Vanilla()
	if len(s.WorldgenOverlay) > 0 {
		data = worldgen.Overlay(data, s.WorldgenOverlay)
	}
	result := jigsawRuntimeData{base: data}
	if s.WorldDir == "" {
		return result
	}
	packs, _ := os.ReadDir(filepath.Join(s.WorldDir, "datapacks"))
	for _, p := range packs {
		if !p.IsDir() {
			continue
		}
		root := filepath.Join(s.WorldDir, "datapacks", p.Name())
		if _, err := os.Stat(filepath.Join(root, "pack.mcmeta")); err == nil {
			result.roots = append(result.roots, filepath.Join(root, "data"))
		}
	}
	return result
}

func (s *Server) handleJigsawGenerate(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	depth, err := r.VarInt()
	if err != nil {
		return
	}
	keep, err := r.Bool()
	if err != nil || sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		return
	}
	d := sess.dimension()
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	g := s.jigsawAt(d, pos)
	if g == nil {
		return
	}
	orientation, _ := world.StateProperty(d.w.GetBlock(pos), "orientation")
	front, _, _ := strings.Cut(orientation, "_")
	start := worldgen.BlockPos{x, y, z}
	switch front {
	case "down":
		start[1]--
	case "up":
		start[1]++
	case "north":
		start[2]--
	case "south":
		start[2]++
	case "west":
		start[0]--
	case "east":
		start[0]++
	}
	pool, _ := g.fields["pool"].(string)
	target, _ := g.fields["target"].(string)
	lookup := func(id string) map[string]any {
		if !strings.ContainsRune(id, ':') {
			id = "minecraft:" + id
		}
		return s.structureTemplateGet(id)
	}
	var baseHeight func(int32, int32, int) int32
	if d.flat {
		baseHeight = func(int32, int32, int) int32 { return -60 }
	} else if d.terrain != nil {
		baseHeight = d.terrain.g.BaseHeight
	}
	pieces, err := worldgen.PlanRuntimeJigsaw(s.jigsawRuntimeData(), s.worldSeed(), start, pool, target, depth, int32(d.minY), int32(d.height), lookup, baseHeight)
	if err != nil {
		return
	}
	height := func(hm int, x, z int32) int32 {
		predicate := world.HeightmapPredicates.WorldSurface
		if hm == worldgen.HMOceanFloorWG {
			predicate = world.HeightmapPredicates.OceanFloor
		}
		for y := d.minY + d.height - 1; y >= d.minY; y-- {
			if predicate(d.w.GetBlock(world.Pos{X: int(x), Y: y, Z: int(z)})) {
				return int32(y + 1)
			}
		}
		return int32(d.minY)
	}
	block := func(x, y, z int32) world.Block { return d.w.GetBlock(world.Pos{X: int(x), Y: int(y), Z: int(z)}) }
	placementRandom := worldgen.NewLegacy(rand.Int63())
	for _, piece := range pieces {
		worldgen.VisitRuntimeJigsawTemplates(piece, start, keep, lookup, block, height, func(template worldgen.RuntimeJigsawTemplate) {
			at := template.Origin
			opts := structureTemplatePlacement{origin: world.Pos{X: int(at[0]), Y: int(at[1]), Z: int(at[2])}, rotation: int32(template.Rotation), integrity: 1, knownShape: true, includeEntities: true, keepLiquids: template.KeepLiquids, settingsRandom: worldgen.NewLegacy(worldgen.BlockSeed(at[0], at[1], at[2])), placementRandom: placementRandom}
			s.structureTemplatePlaceWithOptions(d, template.Native, opts)
		})
	}
}
