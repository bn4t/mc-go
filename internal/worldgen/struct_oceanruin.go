package worldgen

import (
	"encoding/json"
	"fmt"
)

// Ocean ruins (OceanRuinStructure, OceanRuinPieces): one template
// ("orp") at (chunk min, 90, chunk min), large with large_probability
// (integrity 0.9, else 0.8); a large one may get a cluster of 4..8
// small ruins (integrity 0.8) around it at shuffled candidate offsets
// that do not overlap its 16x16 footprint. Cold ruins stack three
// templates (brick, cracked at 0.7, mossy at 0.5) of one index. Each
// template rots by integrity, ignores structure blocks and air, and
// turns up to 5 sand / gravel blocks into suspicious ones with the
// archaeology loot table. On every placement the template drops to the
// ocean floor at its corner, then to the floor under its footprint when
// that lies more than 2 lower over most of the columns.
//
// Data markers: "chest" places a chest (waterlogged in water) with the
// big or small underwater ruin loot table; "drowned" leaves water
// (air above sea level) where vanilla also spawns a persistent drowned,
// which mc-go does not place (template entities are not placed).

var (
	oceanRuinWarm       = []string{"warm_1", "warm_2", "warm_3", "warm_4", "warm_5", "warm_6", "warm_7", "warm_8"}
	oceanRuinBigWarm    = []string{"big_warm_4", "big_warm_5", "big_warm_6", "big_warm_7"}
	oceanRuinBrick      = []string{"brick_1", "brick_2", "brick_3", "brick_4", "brick_5", "brick_6", "brick_7", "brick_8"}
	oceanRuinCracked    = []string{"cracked_1", "cracked_2", "cracked_3", "cracked_4", "cracked_5", "cracked_6", "cracked_7", "cracked_8"}
	oceanRuinMossy      = []string{"mossy_1", "mossy_2", "mossy_3", "mossy_4", "mossy_5", "mossy_6", "mossy_7", "mossy_8"}
	oceanRuinBigBrick   = []string{"big_brick_1", "big_brick_2", "big_brick_3", "big_brick_8"}
	oceanRuinBigCracked = []string{"big_cracked_1", "big_cracked_2", "big_cracked_3", "big_cracked_8"}
	oceanRuinBigMossy   = []string{"big_mossy_1", "big_mossy_2", "big_mossy_3", "big_mossy_8"}
)

type oceanRuinConfig struct {
	cold             bool
	large, cluster   float32
	settingsByRotInt func(rot int, integrity float32) placeSettings
}

func init() {
	structureTypes["ocean_ruin"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		cfg := &oceanRuinConfig{
			cold:    d.raw.str("biome_temp") == "cold",
			large:   parseF32(d.raw["large_probability"]),
			cluster: parseF32(d.raw["cluster_probability"]),
		}
		cold := cfg.cold
		cfg.settingsByRotInt = func(rot int, integrity float32) placeSettings { return oceanRuinSettings(g, rot, integrity, cold) }
		return func(c *GenContext) (*GenerationStub, bool) {
			return onTopOfChunkCenter(c, HMOceanFloorWG, func(add func(StructurePiece)) {
				pos := BlockPos{c.ChunkX * 16, 90, c.ChunkZ * 16}
				rot := int(c.Random.NextIntN(4))
				oceanRuinAddPieces(cfg, pos, rot, c.Random, add)
			}), true
		}
	}
	pieceLoaders["orp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		rot := rotationFromName(fmt3str(t["Rot"]))
		integrity := nbtFloat(t["Integrity"])
		bt := fmt3str(t["BiomeType"])
		cold := bt == "COLD"
		if !cold && bt != "WARM" {
			return nil, fmt.Errorf("worldgen: ocean ruin biome type %q", bt)
		}
		tp, err := loadTplPiece(t, func(string) placeSettings { return oceanRuinSettings(g, rot, integrity, cold) })
		if err != nil {
			return nil, err
		}
		return &oceanRuinPiece{tplPiece: tp, rot: rot, integrity: integrity, cold: cold, large: tagInt(t, "IsLarge") != 0}, nil
	}
}

func nbtFloat(v any) float32 {
	switch x := v.(type) {
	case float32:
		return x
	case float64:
		return float32(x)
	}
	return float32(nbtInt(v))
}

// oceanRuinSettings is OceanRuinPiece.makeSettings: rotation, block rot
// at integrity, structure blocks and air ignored, then the capped
// archaeology rule (5 of the template's gravel / sand).
func oceanRuinSettings(g *Generator, rot int, integrity float32, cold bool) placeSettings {
	from, to, loot := "sand", "suspicious_sand", "archaeology/ocean_ruin_warm"
	if cold {
		from, to, loot = "gravel", "suspicious_gravel", "archaeology/ocean_ruin_cold"
	}
	procs := fmt.Sprintf(`[{"processor_type":"minecraft:block_rot","integrity":%s},`+
		`{"processor_type":"minecraft:block_ignore","blocks":[{"Name":"minecraft:structure_block"},{"Name":"minecraft:air"}]},`+
		`{"processor_type":"minecraft:capped","limit":5,"delegate":{"processor_type":"minecraft:rule","rules":[`+
		`{"input_predicate":{"predicate_type":"minecraft:block_match","block":"minecraft:%s"},`+
		`"location_predicate":{"predicate_type":"minecraft:always_true"},`+
		`"output_state":{"Name":"minecraft:%s"},`+
		`"block_entity_modifier":{"type":"minecraft:append_loot","loot_table":"minecraft:%s"}}]}}]`,
		jsonFloat32(integrity), from, to, loot)
	spec := g.compileProcSpec(json.RawMessage(procs))
	s := *newPlaceSettings()
	s.rotation = rot
	s.mirror = mirNone
	s.processors = spec.procs
	s.finalizers = spec.fins
	s.entirePiece = spec.entire
	return s
}

// jsonFloat32 renders a float32 so parseF32 reads back the same value.
func jsonFloat32(f float32) string {
	b, _ := json.Marshal(float64(f))
	return string(b)
}

// oceanRuinAddPieces is OceanRuinPieces.addPieces.
func oceanRuinAddPieces(cfg *oceanRuinConfig, pos BlockPos, rot int, r Random, add func(StructurePiece)) {
	large := r.NextFloat() <= cfg.large
	integrity := float32(0.8)
	if large {
		integrity = 0.9
	}
	oceanRuinAddPiece(cfg, pos, rot, r, large, integrity, add)
	if large && r.NextFloat() <= cfg.cluster {
		oceanRuinCluster(cfg, r, rot, pos, add)
	}
}

// oceanRuinCluster is OceanRuinPieces.addClusterRuins.
func oceanRuinCluster(cfg *oceanRuinConfig, r Random, rot int, pos BlockPos, add func(StructurePiece)) {
	base := BlockPos{pos[0], 90, pos[2]}
	far := offset(tplTransform(BlockPos{15, 0, 15}, mirNone, rot, BlockPos{}), base[0], base[1], base[2])
	bigBox := bbFrom(boxCorners(base, far))
	corner := BlockPos{min(base[0], far[0]), base[1], min(base[2], far[2])}
	list := oceanRuinCandidates(r, corner)
	n := mthNextInt(r, 4, 8)
	for i := int32(0); i < n; i++ {
		if len(list) == 0 {
			continue
		}
		k := r.NextIntN(int32(len(list)))
		p := list[k]
		list = append(list[:k], list[k+1:]...)
		rr := int(r.NextIntN(4))
		q := offset(tplTransform(BlockPos{5, 0, 6}, mirNone, rr, BlockPos{}), p[0], p[1], p[2])
		if bbFrom(boxCorners(p, q)).Intersects(bigBox) {
			continue
		}
		oceanRuinAddPiece(cfg, p, rr, r, false, 0.8, add)
	}
}

// oceanRuinCandidates is OceanRuinPieces.allPositions: eight offsets
// around the corner, x drawn before z.
func oceanRuinCandidates(r Random, p BlockPos) []BlockPos {
	type rng struct{ base, lo, hi int32 }
	spec := [8][2]rng{
		{{-16, 1, 8}, {16, 1, 7}},
		{{-16, 1, 8}, {0, 1, 7}},
		{{-16, 1, 8}, {-16, 4, 8}},
		{{0, 1, 7}, {16, 1, 7}},
		{{0, 1, 7}, {-16, 4, 6}},
		{{16, 1, 7}, {16, 3, 8}},
		{{16, 1, 7}, {0, 1, 7}},
		{{16, 1, 7}, {-16, 4, 8}},
	}
	out := make([]BlockPos, 0, 8)
	for _, s := range spec {
		dx := s[0].base + mthNextInt(r, s[0].lo, s[0].hi)
		dz := s[1].base + mthNextInt(r, s[1].lo, s[1].hi)
		out = append(out, offset(p, dx, 0, dz))
	}
	return out
}

// oceanRuinAddPiece is OceanRuinPieces.addPiece.
func oceanRuinAddPiece(cfg *oceanRuinConfig, pos BlockPos, rot int, r Random, large bool, integrity float32, add func(StructurePiece)) {
	mk := func(name string, in float32) {
		add(newOceanRuinPiece(cfg, "minecraft:underwater_ruin/"+name, pos, rot, in, large))
	}
	if !cfg.cold {
		if large {
			mk(oceanRuinBigWarm[r.NextIntN(int32(len(oceanRuinBigWarm)))], integrity)
		} else {
			mk(oceanRuinWarm[r.NextIntN(int32(len(oceanRuinWarm)))], integrity)
		}
		return
	}
	brick, cracked, mossy := oceanRuinBrick, oceanRuinCracked, oceanRuinMossy
	if large {
		brick, cracked, mossy = oceanRuinBigBrick, oceanRuinBigCracked, oceanRuinBigMossy
	}
	i := r.NextIntN(int32(len(brick)))
	mk(brick[i], integrity)
	mk(cracked[i], 0.7)
	mk(mossy[i], 0.5)
}

type oceanRuinPiece struct {
	tplPiece
	rot       int
	integrity float32
	cold      bool
	large     bool
}

func newOceanRuinPiece(cfg *oceanRuinConfig, name string, pos BlockPos, rot int, integrity float32, large bool) *oceanRuinPiece {
	return &oceanRuinPiece{tplPiece: newTplPiece("orp", name, cfg.settingsByRotInt(rot, integrity), pos, 0),
		rot: rot, integrity: integrity, cold: cfg.cold, large: large}
}

// SaveData is OceanRuinPiece.addAdditionalSaveData.
func (p *oceanRuinPiece) SaveData(t map[string]any) {
	p.saveTpl(t)
	t["Rot"] = rotationEnumNames[p.rot]
	t["Integrity"] = p.integrity
	if p.cold {
		t["BiomeType"] = "COLD"
	} else {
		t["BiomeType"] = "WARM"
	}
	t["IsLarge"] = boolByte(p.large)
}

// PostProcess is OceanRuinPiece.postProcess: the template position
// moves to the ocean floor at its corner, then to floorHeight over the
// rotated footprint, before the template places.
func (p *oceanRuinPiece) PostProcess(c *PieceContext) {
	rg := c.Region
	h := rg.Height(HMOceanFloorWG, p.pos[0], p.pos[2])
	p.pos = BlockPos{p.pos[0], h, p.pos[2]}
	sz := p.tpl.size
	far := offset(tplTransform(BlockPos{sz[0] - 1, 0, sz[2] - 1}, mirNone, p.rot, BlockPos{}), p.pos[0], p.pos[1], p.pos[2])
	p.pos = BlockPos{p.pos[0], p.floorHeight(rg, p.pos, far), p.pos[2]}
	p.dataMarker = p.handleData
	p.placeTemplate(c)
}

// floorHeight is OceanRuinPiece.getHeight: every footprint column is
// followed down from below the template through air, water and ice;
// when the lowest floor is more than 2 below and more columns than the
// x extent minus 2 sit over 2 below, the template drops onto the
// lowest floor.
func (p *oceanRuinPiece) floorHeight(rg *Region, from, to BlockPos) int32 {
	y := from[1]
	lowest := int32(512)
	top := y - 1
	deep := int32(0)
	for _, q := range betweenClosedXZ(from, to) {
		yy := from[1] - 1
		for {
			st := rg.at(BlockPos{q[0], yy, q[2]})
			if !(isAir(st) || fluidID(st) == "water" || blockInTag(st, "ice")) || yy <= rg.MinY()+1 {
				break
			}
			yy--
		}
		lowest = min(lowest, yy)
		if yy < top-2 {
			deep++
		}
	}
	dx := from[0] - to[0]
	if dx < 0 {
		dx = -dx
	}
	if top-lowest > 2 && deep > dx-2 {
		y = lowest + 1
	}
	return y
}

// betweenClosedXZ lists BlockPos.betweenClosed over a one-high box.
func betweenClosedXZ(a, b BlockPos) []BlockPos {
	x0, x1 := min(a[0], b[0]), max(a[0], b[0])
	z0, z1 := min(a[2], b[2]), max(a[2], b[2])
	y0, y1 := min(a[1], b[1]), max(a[1], b[1])
	var out []BlockPos
	for y := y0; y <= y1; y++ {
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				out = append(out, BlockPos{x, y, z})
			}
		}
	}
	return out
}

// handleData is OceanRuinPiece.handleDataMarker.
func (p *oceanRuinPiece) handleData(c *PieceContext, meta string, pos BlockPos) {
	rg := c.Region
	switch meta {
	case "chest":
		st := chestBlock()
		if fluidID(rg.at(pos)) == "water" {
			st = withProp(st, "waterlogged", "true")
		}
		rg.set(pos, st)
		if isBlock(rg.at(pos), "chest") {
			table := "minecraft:chests/underwater_ruin_small"
			if p.large {
				table = "minecraft:chests/underwater_ruin_big"
			}
			seed := c.Random.NextLong()
			structLootBlockEntity(rg, pos, table, seed)
		}
	case "drowned":
		// The drowned itself is not spawned.
		if pos[1] > rg.seaLevel() {
			rg.set(pos, blockNamed("air"))
		} else {
			rg.set(pos, blockNamed("water"))
		}
	}
}
