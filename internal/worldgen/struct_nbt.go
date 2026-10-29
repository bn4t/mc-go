package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"mc-go/internal/nbt"
)

// Saved structure data in the chunk format vanilla writes
// (StructureStart.createTag / loadStaticStart, StructurePiece.createTag
// with PoolElementStructurePiece.addAdditionalSaveData, and the
// "structures" compound of a chunk: "starts" by structure id and
// "References" as packed chunk positions).
//
// Pieces of other types register a loader in pieceLoaders and provide
// their extra fields by implementing pieceSaver.

// pieceSaver adds a piece type's own fields to its saved compound.
type pieceSaver interface {
	SaveData(tag map[string]any)
}

// pieceLoaders rebuild a piece from its saved compound, by piece type id
// (namespace trimmed).
var pieceLoaders = map[string]func(g *Generator, tag map[string]any) (StructurePiece, error){
	"jigsaw": loadJigsawPiece,
}

var rotationEnumNames = [4]string{"NONE", "CLOCKWISE_90", "CLOCKWISE_180", "COUNTERCLOCKWISE_90"}

func bbArray(b BoundingBox) []int32 { return []int32{b.MinX, b.MinY, b.MinZ, b.MaxX, b.MaxY, b.MaxZ} }

// StartTag is StructureStart.createTag for start s.
func StartTag(s *StructureStart) map[string]any {
	kids := nbt.List{Elem: nbt.TagCompound}
	for _, p := range s.Pieces {
		t := map[string]any{
			"id": "minecraft:" + p.Type(),
			"BB": bbArray(p.BoundingBox()),
			"O":  p.Orientation(),
			"GD": p.GenDepth(),
		}
		if ps, ok := p.(pieceSaver); ok {
			ps.SaveData(t)
		}
		kids.Items = append(kids.Items, t)
	}
	return map[string]any{
		"id":         "minecraft:" + s.Structure,
		"ChunkX":     s.ChunkX,
		"ChunkZ":     s.ChunkZ,
		"references": s.References,
		"Children":   kids,
	}
}

// StructuresTag is a chunk's "structures" compound: its starts and
// the references StructureReferences computes.
func (g *Generator) StructuresTag(cx, cz int32) map[string]any {
	starts := map[string]any{}
	for _, s := range g.StructureStarts(cx, cz) {
		starts["minecraft:"+s.Structure] = StartTag(s)
	}
	refs := map[string]any{}
	for _, r := range g.StructureReferences(cx, cz) {
		l := make([]int64, len(r.Starts))
		for i, c := range r.Starts {
			l[i] = packChunk(c[0], c[1])
		}
		refs["minecraft:"+r.Structure] = l
	}
	return map[string]any{"starts": starts, "References": refs}
}

// SaveData is PoolElementStructurePiece.addAdditionalSaveData.
func (p *JigsawPiece) SaveData(t map[string]any) {
	t["PosX"], t["PosY"], t["PosZ"] = p.Position[0], p.Position[1], p.Position[2]
	t["ground_level_delta"] = p.GroundLvl
	var el any
	json.Unmarshal(p.element.raw(), &el)
	t["pool_element"] = jsonToNBT(el)
	t["rotation"] = rotationEnumNames[p.Rotation]
	js := nbt.List{Elem: nbt.TagCompound}
	for _, j := range p.Junctions {
		js.Items = append(js.Items, map[string]any{
			"source_x": j.SourceX, "source_ground_y": j.SourceGroundY, "source_z": j.SourceZ,
			"delta_y": j.DeltaY, "dest_proj": projectionNames[j.DestProjection],
		})
	}
	t["junctions"] = js
	if !p.liquid {
		t["liquid_settings"] = "ignore_waterlogging"
	}
}

// jsonToNBT maps decoded datapack JSON to NBT values: objects to
// compounds, arrays to lists, integral numbers to ints, other numbers
// to doubles, booleans to bytes.
func jsonToNBT(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = jsonToNBT(e)
		}
		return out
	case []any:
		l := nbt.List{Elem: nbt.TagEnd}
		for _, e := range x {
			n := jsonToNBT(e)
			l.Items = append(l.Items, n)
			l.Elem = nbtTagOf(n)
		}
		return l
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<31 {
			return int32(x)
		}
		return x
	case bool:
		if x {
			return int8(1)
		}
		return int8(0)
	}
	return v
}

func nbtTagOf(v any) byte {
	switch v.(type) {
	case map[string]any:
		return nbt.TagCompound
	case nbt.List:
		return nbt.TagList
	case int32:
		return nbt.TagInt
	case float64:
		return nbt.TagDouble
	case int8:
		return nbt.TagByte
	case string:
		return nbt.TagString
	}
	return nbt.TagEnd
}

// nbtToJSON is the inverse of jsonToNBT for loaded compounds.
func nbtToJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = nbtToJSON(e)
		}
		return out
	case nbt.List:
		out := []any{}
		for _, e := range x.Items {
			out = append(out, nbtToJSON(e))
		}
		return out
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	}
	return v
}

func tagInt(t map[string]any, k string) int32 { return nbtInt(t[k]) }

// LoadStart is StructureStart.loadStaticStart: nil for an "INVALID"
// start or an unknown structure.
func (g *Generator) LoadStart(tag map[string]any) (*StructureStart, error) {
	id, _ := tag["id"].(string)
	if id == "" || id == "INVALID" {
		return nil, nil
	}
	name := trimNS(id)
	if _, ok := g.RS.data.Get("structure/" + name); !ok {
		return nil, fmt.Errorf("worldgen: unknown structure %s", id)
	}
	g.structureState()
	s := &StructureStart{Structure: name, ChunkX: tagInt(tag, "ChunkX"), ChunkZ: tagInt(tag, "ChunkZ"),
		References: tagInt(tag, "references"), def: g.structure(name)}
	kids, _ := tag["Children"].(nbt.List)
	for _, k := range kids.Items {
		kt, _ := k.(map[string]any)
		pid, _ := kt["id"].(string)
		load, ok := pieceLoaders[trimNS(pid)]
		if !ok {
			return nil, fmt.Errorf("worldgen: no loader for structure piece %s", pid)
		}
		p, err := load(g, kt)
		if err != nil {
			return nil, err
		}
		s.Pieces = append(s.Pieces, p)
	}
	return s, nil
}

func loadJigsawPiece(g *Generator, t map[string]any) (StructurePiece, error) {
	raw, err := json.Marshal(nbtToJSON(t["pool_element"]))
	if err != nil {
		return nil, err
	}
	p := &JigsawPiece{element: g.compileElement(raw, -1), liquid: true,
		Position: BlockPos{tagInt(t, "PosX"), tagInt(t, "PosY"), tagInt(t, "PosZ")}, GroundLvl: tagInt(t, "ground_level_delta")}
	rot, _ := t["rotation"].(string)
	for i, n := range rotationEnumNames {
		if strings.EqualFold(rot, n) {
			p.Rotation = i
		}
	}
	// PoolElementStructurePiece's load recomputes the box from the
	// element.
	p.Box = p.element.boundingBox(p.Position, p.Rotation)
	if l, ok := t["junctions"].(nbt.List); ok {
		for _, it := range l.Items {
			m, _ := it.(map[string]any)
			proj := projTerrainMatching
			if m["dest_proj"] == "rigid" {
				proj = projRigid
			}
			p.Junctions = append(p.Junctions, JigsawJunction{tagInt(m, "source_x"), tagInt(m, "source_ground_y"), tagInt(m, "source_z"), tagInt(m, "delta_y"), proj})
		}
	}
	if ls, _ := t["liquid_settings"].(string); ls == "ignore_waterlogging" {
		p.liquid = false
	}
	return p, nil
}

// SetStructureStarts installs a chunk's starts loaded from a save, so
// references, the beardifier and decoration use them instead of
// regenerating the chunk's starts.
func (g *Generator) SetStructureStarts(cx, cz int32, starts []*StructureStart) {
	st := g.structureState()
	cs := &chunkStarts{done: make(chan struct{}), starts: starts}
	close(cs.done)
	st.mu.Lock()
	st.starts[[2]int32{cx, cz}] = cs
	// References computed before may have missed these starts.
	st.refs = map[[2]int32][]StructureRef{}
	st.mu.Unlock()
}
