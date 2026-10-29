package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

func structureVector(v any) [3]int32 {
	var out [3]int32
	l, _ := v.(nbt.List)
	for i, v := range l.Items {
		if i == 3 {
			break
		}
		out[i] = nbtNumberOr(v, 0)
	}
	return out
}
func structureIntList(v [3]int32) nbt.List {
	return nbt.List{Elem: nbt.TagInt, Items: []any{v[0], v[1], v[2]}}
}
func structureFloatList(v [3]float64) nbt.List {
	return nbt.List{Elem: nbt.TagDouble, Items: []any{v[0], v[1], v[2]}}
}
func structureTransform(v [3]int32, mirror, rotation int32) [3]int32 {
	x, z := v[0], v[2]
	if mirror == 1 {
		z = -z
	}
	if mirror == 2 {
		x = -x
	}
	switch rotation {
	case 1:
		x, z = -z, x
	case 2:
		x, z = -x, -z
	case 3:
		x, z = z, -x
	}
	return [3]int32{x, v[1], z}
}

// The file converter prefixes structure/ and appends .nbt before validating
// path segments; consequently a final raw dot is a safe ..nbt filename.
func (s *Server) structureTemplatePath(name string) (string, bool) {
	if name == "" || structureName(name) != name || s.WorldDir == "" {
		return "", false
	}
	ns, path, _ := strings.Cut(name, ":")
	converted := "structure/" + path + ".nbt"
	for _, part := range strings.Split(converted, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return "", false
		}
		stem, _, _ := strings.Cut(strings.ToLower(part), ".")
		switch stem {
		case "com", "clock$", "con", "prn", "aux", "nul":
			return "", false
		}
		if len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '1' && stem[3] <= '9' {
			return "", false
		}
	}
	root := filepath.Join(s.WorldDir, "generated")
	result := filepath.Join(root, ns, filepath.FromSlash(converted))
	rel, err := filepath.Rel(root, result)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return result, true
}
func (s *Server) structureTemplateFile(name string, create bool) (*os.File, error) {
	path, ok := s.structureTemplatePath(name)
	if !ok {
		return nil, os.ErrInvalid
	}
	rootPath := filepath.Join(s.WorldDir, "generated")
	if create {
		if err := os.MkdirAll(rootPath, 0755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := filepath.Rel(rootPath, path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := root.MkdirAll(filepath.Dir(rel), 0755); err != nil {
			return nil, err
		}
		return root.Create(rel)
	}
	return root.Open(rel)
}
func (s *Server) structureTemplateGet(name string) map[string]any {
	if t := s.structureTemplates[name]; t != nil {
		return t
	}
	var raw []byte
	if _, ok := s.structureTemplatePath(name); ok {
		if f, e := s.structureTemplateFile(name, false); e == nil {
			zr, e := gzip.NewReader(f)
			if e == nil {
				raw, _ = io.ReadAll(io.LimitReader(zr, 64<<20))
				_ = zr.Close()
			}
			_ = f.Close()
		}
	}
	if raw == nil && strings.HasPrefix(name, "minecraft:") {
		raw, _ = worldgen.BuiltinTemplateNBT(strings.TrimPrefix(name, "minecraft:"))
	}
	if len(raw) == 0 {
		return nil
	}
	_, v, e := nbt.ReadNamed(bytes.NewReader(raw))
	if e != nil {
		return nil
	}
	t, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	t["author"] = "?"
	if s.structureTemplates == nil {
		s.structureTemplates = map[string]map[string]any{}
	}
	s.structureTemplates[name] = t
	return t
}
func (s *Server) structureTemplateCapture(d *dimension, origin world.Pos, size [3]int32, includeEntities bool, author string) map[string]any {
	palette := nbt.List{Elem: nbt.TagCompound}
	blocks := nbt.List{Elem: nbt.TagCompound}
	entities := nbt.List{Elem: nbt.TagCompound}
	indices := map[world.Block]int32{}
	// Native templates serialize full blocks before dependent shapes and NBT.
	type entry struct {
		pos   [3]int32
		state world.Block
		tag   map[string]any
		kind  int
	}
	var captured []entry
	if size[0] > 0 && size[1] > 0 && size[2] > 0 {
		for z := int32(0); z < size[2]; z++ {
			for y := int32(0); y < size[1]; y++ {
				for x := int32(0); x < size[0]; x++ {
					p := world.Pos{X: origin.X + int(x), Y: origin.Y + int(y), Z: origin.Z + int(z)}
					state := d.w.GetBlock(p)
					name, _ := world.StateOwner(state)
					if name == "structure_void" {
						continue
					}
					e := entry{pos: [3]int32{x, y, z}, state: state}
					if tag, ok := s.blockEntitySaveMap(d, p); ok {
						e.tag = cloneNBTValue(tag).(map[string]any)
						delete(e.tag, "x")
						delete(e.tag, "y")
						delete(e.tag, "z")
						e.kind = 2
					} else if !worldgen.FullCollision(state) {
						e.kind = 1
					}
					captured = append(captured, e)
				}
			}
		}
	}
	sort.SliceStable(captured, func(i, j int) bool {
		a, b := captured[i], captured[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		for _, axis := range []int{1, 0, 2} {
			if a.pos[axis] != b.pos[axis] {
				return a.pos[axis] < b.pos[axis]
			}
		}
		return false
	})
	for _, e := range captured {
		index, ok := indices[e.state]
		if !ok {
			index = int32(len(palette.Items))
			indices[e.state] = index
			name, _ := world.StateOwner(e.state)
			tag := map[string]any{"Name": "minecraft:" + name}
			if props := world.BlockProperties(e.state); len(props) > 0 {
				p := map[string]any{}
				for k, v := range props {
					p[k] = v
				}
				tag["Properties"] = p
			}
			palette.Items = append(palette.Items, tag)
		}
		tag := map[string]any{"pos": structureIntList(e.pos), "state": index}
		if e.tag != nil {
			tag["nbt"] = e.tag
		}
		blocks.Items = append(blocks.Items, tag)
	}
	if includeEntities {
		for _, e := range s.entities.All() {
			name, ok := EntityTypeName(e.Type)
			dims := entityBBDims(&e)
			if !ok || name == "player" || e.Dim != d.name || e.Dying || e.Riding != 0 || e.X+dims[0]/2 <= float64(origin.X) || e.X-dims[0]/2 >= float64(origin.X)+float64(size[0]) || e.Y+dims[1] <= float64(origin.Y) || e.Y >= float64(origin.Y)+float64(size[1]) || e.Z+dims[0]/2 <= float64(origin.Z) || e.Z-dims[0]/2 >= float64(origin.Z)+float64(size[2]) {
				continue
			}
			native := entitySaveMap(&e, s.clock.count, s.entities).m
			native["id"] = "minecraft:" + name
			rel := [3]float64{e.X - float64(origin.X), e.Y - float64(origin.Y), e.Z - float64(origin.Z)}
			block := [3]int32{int32(math.Floor(rel[0])), int32(math.Floor(rel[1])), int32(math.Floor(rel[2]))}
			entities.Items = append(entities.Items, map[string]any{"pos": structureFloatList(rel), "blockPos": structureIntList(block), "nbt": cloneNBTValue(native)})
		}
	}
	return map[string]any{"DataVersion": int32(world.DataVersion26_2), "size": structureIntList(size), "palette": palette, "blocks": blocks, "entities": entities, "author": author}
}
func (s *Server) structureBlockSave(d *dimension, pos world.Pos, g *genericBlockEntity, toDisk bool) bool {
	if g.fields["mode"] != "SAVE" {
		return false
	}
	name, _ := g.fields["name"].(string)
	if name == "" {
		return false
	}
	size := [3]int32{nbtNumberOr(g.fields["sizeX"], 0), nbtNumberOr(g.fields["sizeY"], 0), nbtNumberOr(g.fields["sizeZ"], 0)}
	origin := world.Pos{X: pos.X + int(nbtNumberOr(g.fields["posX"], 0)), Y: pos.Y + int(nbtNumberOr(g.fields["posY"], 1)), Z: pos.Z + int(nbtNumberOr(g.fields["posZ"], 0))}
	author, _ := g.fields["author"].(string)
	native := s.structureTemplateCapture(d, origin, size, !nbtTruth(g.fields["ignoreEntities"]), author)
	if s.structureTemplates == nil {
		s.structureTemplates = map[string]map[string]any{}
	}
	s.structureTemplates[name] = native
	if !toDisk {
		return true
	}
	f, e := s.structureTemplateFile(name, true)
	if e != nil {
		return false
	}
	zw := gzip.NewWriter(f)
	disk := cloneNBTValue(native).(map[string]any)
	delete(disk, "author") // Template author is repository-only in26.2.
	e = nbt.WriteNamed(zw, "", disk)
	if close := zw.Close(); e == nil {
		e = close
	}
	if close := f.Close(); e == nil {
		e = close
	}
	return e == nil
}
func (s *Server) structureBlockLoad(d *dimension, pos world.Pos, g *genericBlockEntity) string {
	if g.fields["mode"] != "LOAD" {
		return "structure_block.load_not_found"
	}
	name, _ := g.fields["name"].(string)
	native := s.structureTemplateGet(name)
	if native == nil {
		return "structure_block.load_not_found"
	}
	size := structureVector(native["size"])
	current := [3]int32{nbtNumberOr(g.fields["sizeX"], 0), nbtNumberOr(g.fields["sizeY"], 0), nbtNumberOr(g.fields["sizeZ"], 0)}
	author, _ := native["author"].(string)
	g.fields["author"] = author
	if current != size {
		for i, k := range []string{"sizeX", "sizeY", "sizeZ"} {
			g.fields[k] = size[i]
		}
		return "structure_block.load_prepare"
	}
	s.structureTemplatePlace(d, pos, g, native)
	return "structure_block.load_success"
}

// structureTemplatePlacement separates shape handling from the stronger editor
// strict policy. Runtime template users can share placement randomness while
// choosing an independent stream for palette/settings decisions.
type structureTemplatePlacement struct {
	origin                                                    world.Pos
	mirror, rotation                                          int32
	integrity                                                 float32
	knownShape, skipSideEffects, includeEntities, keepLiquids bool
	settingsRandom, placementRandom                           *worldgen.Legacy
}

func (s *Server) structureTemplatePlace(d *dimension, pos world.Pos, g *genericBlockEntity, native map[string]any) {
	origin := world.Pos{X: pos.X + int(nbtNumberOr(g.fields["posX"], 0)), Y: pos.Y + int(nbtNumberOr(g.fields["posY"], 1)), Z: pos.Z + int(nbtNumberOr(g.fields["posZ"], 0))}
	mirror, rotation := int32(0), int32(0)
	for i, v := range structureMirrors {
		if g.fields["mirror"] == v {
			mirror = int32(i)
		}
	}
	for i, v := range structureRotations {
		if g.fields["rotation"] == v {
			rotation = int32(i)
		}
	}
	seed := commandLong(g.fields["seed"], 0)
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	integrity := float32(1)
	if f, ok := g.fields["integrity"].(float32); ok {
		integrity = f
	}
	strict := nbtTruth(g.fields["strict"])
	s.structureTemplatePlaceWithOptions(d, native, structureTemplatePlacement{
		origin: origin, mirror: mirror, rotation: rotation, integrity: integrity,
		knownShape: strict, skipSideEffects: strict, includeEntities: !nbtTruth(g.fields["ignoreEntities"]), keepLiquids: true,
		settingsRandom: worldgen.NewLegacy(seed), placementRandom: worldgen.NewLegacy(seed),
	})
}

func (s *Server) structureTemplatePlaceWithOptions(d *dimension, native map[string]any, opts structureTemplatePlacement) {
	origin, mirror, rotation := opts.origin, opts.mirror, opts.rotation
	random, lootRandom := opts.settingsRandom, opts.placementRandom
	palette, _ := native["palette"].(nbt.List)
	if palettes, ok := native["palettes"].(nbt.List); ok && len(palettes.Items) > 0 {
		palette, _ = palettes.Items[random.NextIntN(int32(len(palettes.Items)))].(nbt.List)
	} else if len(palette.Items) > 0 {
		_ = random.NextIntN(1)
	}
	states := make([]world.Block, len(palette.Items))
	for i, v := range palette.Items {
		m, _ := v.(map[string]any)
		name, _ := m["Name"].(string)
		props := map[string]string{}
		if p, ok := m["Properties"].(map[string]any); ok {
			for k, v := range p {
				if text, ok := v.(string); ok {
					props[k] = text
				}
			}
		}
		states[i], _ = world.BlockStateOf(strings.TrimPrefix(name, "minecraft:"), props)
	}
	integrity := opts.integrity
	var placed, pendingWater []world.Pos
	introducedSources := map[world.Pos]bool{}
	blocks, _ := native["blocks"].(nbt.List)
	for _, v := range blocks.Items {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		index := nbtNumberOr(m["state"], -1)
		if index < 0 || int(index) >= len(states) {
			continue
		}
		if integrity < 1 && random.NextFloat() > integrity {
			continue
		}
		rel := structureTransform(structureVector(m["pos"]), mirror, rotation)
		p := world.Pos{X: origin.X + int(rel[0]), Y: origin.Y + int(rel[1]), Z: origin.Z + int(rel[2])}
		if p.Y < d.minY || p.Y >= d.minY+d.height {
			continue
		}
		state := worldgen.TransformStructureState(states[index], int(mirror), int(rotation))
		oldFluid := d.w.GetBlock(p)
		_, hasNBT := m["nbt"].(map[string]any)
		if !hasNBT && oldFluid == state {
			continue
		}
		if tag, ok := m["nbt"].(map[string]any); ok {
			barrier, _ := world.BlockState("barrier")
			s.setSilentBlockWithPolicy(d, p, barrier, true, false)
			s.setSilentBlockWithPolicy(d, p, state, opts.skipSideEffects, true)
			copy := cloneNBTValue(tag).(map[string]any)
			if worldgen.TemplateRandomizesLoot(state) {
				copy["LootTableSeed"] = lootRandom.NextLong()
			}
			spec, exists := blockEntityOf(state)
			if exists {
				copy["id"] = spec.name
				copy["x"], copy["y"], copy["z"] = int32(p.X), int32(p.Y), int32(p.Z)
				s.restoreBlockEntity(d, copy)
				s.structureLoadName(d, p, state, copy)
			}
		} else {
			restoreNames := structureKeepNames(d, []world.Pos{p})
			s.setSilentBlockWithPolicy(d, p, state, opts.skipSideEffects, true)
			restoreNames()
		}
		placed = append(placed, p)
		if !opts.keepLiquids {
			continue
		}
		if fluidIsSourceState(state) {
			introducedSources[p] = true
		} else if fluidContainer(d.w.GetBlock(p)) {
			kind, _, hasFluid := fluidOf(oldFluid)
			if hasFluid && kind == fluidWater && fluidIsSourceState(oldFluid) {
				s.structurePlaceLiquid(d, p, d.w.GetBlock(p))
			} else if hasFluid && kind == fluidWater {
				pendingWater = append(pendingWater, p)
			}
		}
	}
	s.structureRestorePendingWater(d, pendingWater, introducedSources)
	if !opts.knownShape {
		s.structureUpdateShapes(d, placed)
	}
	if opts.includeEntities {
		s.structureTemplateEntities(d, origin, mirror, rotation, native)
	}

}

func structureEntityTransform(v [3]float64, mirror, rotation int32) [3]float64 {
	x, z := v[0], v[2]
	if mirror == 1 {
		z = 1 - z
	}
	if mirror == 2 {
		x = 1 - x
	}
	switch rotation {
	case 1:
		x, z = 1-z, x
	case 2:
		x, z = 1-x, 1-z
	case 3:
		x, z = z, 1-x
	}
	return [3]float64{x, v[1], z}
}
func (s *Server) structureTemplateEntities(d *dimension, origin world.Pos, mirror, rotation int32, native map[string]any) {
	list, _ := native["entities"].(nbt.List)
	for _, v := range list.Items {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		rel, ok := nbtVec3(item["pos"])
		if !ok {
			continue
		}
		tag, ok := item["nbt"].(map[string]any)
		if !ok {
			continue
		}
		save := cloneNBTValue(tag).(map[string]any)
		delete(save, "UUID")
		rel = structureEntityTransform(rel, mirror, rotation)
		at := [3]float64{float64(origin.X) + rel[0], float64(origin.Y) + rel[1], float64(origin.Z) + rel[2]}
		save["Pos"] = structureFloatList(at)
		if angles, ok := nbtVec2f(save["Rotation"]); ok {
			yaw := angles[0]
			if mirror == 1 {
				yaw = 180 - yaw
			}
			if mirror == 2 {
				yaw = -yaw
			}
			yaw += float32(rotation) * 90
			if rotation == 3 {
				yaw -= 360
			}
			save["Rotation"] = nbt.List{Elem: nbt.TagFloat, Items: []any{yaw, angles[1]}}
		}
		s.structureSpawnEntity(d, save)
	}
}
func (s *Server) structureSpawnEntity(d *dimension, save map[string]any) *Entity {
	id, _ := save["id"].(string)
	name := strings.TrimPrefix(id, "minecraft:")
	at, ok := nbtVec3(save["Pos"])
	if !ok || name == "player" {
		return nil
	}
	e, ok := s.summonMobRaw(d, name, at[0], at[1], at[2], false, false)
	if !ok {
		typeID, exists := EntityTypeID(name)
		if !exists {
			return nil
		}
		e = s.entities.Spawn(&Entity{Type: typeID, Dim: d.name, X: at[0], Y: at[1], Z: at[2]})
	}
	if name == "item" {
		if it, ok := save["Item"].(map[string]any); ok {
			e.Item = itemFromNBT(it)
		}
	}
	delete(save, "UUID")
	s.applyEntitySave(e, save)
	e.HeadYaw, e.LookYaw = e.Yaw, e.Yaw
	e.dataSync = nil
	e.MetaDirty = false
	s.broadcastEntitySpawn(nil, *e)
	if passengers, ok := save["Passengers"].(nbt.List); ok {
		for _, value := range passengers.Items {
			tag, ok := value.(map[string]any)
			if !ok {
				continue
			}
			tag = cloneNBTValue(tag).(map[string]any)
			tag["Pos"] = structureFloatList(at)
			if child := s.structureSpawnEntity(d, tag); child != nil {
				e.Passengers = append(e.Passengers, child.ID)
				child.Riding = e.ID
				child.X, child.Y, child.Z = passengerSeatPos(e, child)
			}
		}
		if len(e.Passengers) > 0 {
			s.broadcastSetPassengers(e)
		}
	}
	return e
}
