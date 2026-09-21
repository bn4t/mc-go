package server

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// /data entity — get, merge, remove, modify — against live entities,
// mirroring vanilla's EntityAccessor: the target selector must resolve
// to exactly one entity (multi-match fails argument.entity.toomany
// marked at the input start; no match fails argument.entity.notfound.entity).
//
// The entity's editable state is its save map — the same field set
// Entity.saveWithId writes: Pos/Motion/Rotation/Health plus the shared
// mob fields and per-type extras (a pig carries Brain.memories, Age,
// variant, ...). Fields the entity model does not track land in
// Entity.ExtraNBT and round-trip back into the map untouched.
//
// Mutations apply the merged save back the way Entity.load does —
// absent fields revert to their type defaults (data remove ... Health
// restores max health) — then resync trackers: a set_entity_data pack
// carrying only the entries the write dirtied, followed by
// update_attributes, like vanilla's sendChanges after load.
//
// Reply shapes measured on 26.2: commands.data.entity.query carries
// the decorated entity display name plus the pretty SNBT;
// commands.data.entity.modified carries just the name. The name
// component is vanilla's entity display name: text = the raw
// CustomName string (not its parsed component) or
// entity.minecraft.<type>, insertion = the UUID, hover show_entity
// whose name mirrors the text (string for custom names, translate
// component otherwise) — and no click event, unlike the player form.
func (s *Server) runDataEntity(player string, args []string) reply {
	if len(args) < 3 {
		return componentReply(commandError("command.unknown.command"))
	}
	tgt, rep := s.resolveDataEntity(player, args[2])
	if tgt == nil {
		return *rep
	}
	// Players are queryable but not writable: vanilla's EntityAccessor
	// throws commands.data.entity.invalid for the mutation verbs.
	if tgt.sess != nil && args[0] != "get" {
		return componentReply(commandError("commands.data.entity.invalid"))
	}
	save := tgt.saveMap()

	switch args[0] {
	case "get":
		var target any = save.m
		seq := save.seq
		if len(args) > 3 {
			steps, ok := parseNBTPath(args[3])
			if !ok {
				return componentReply(commandError("command.unknown.command"))
			}
			target, ok = nbtGet(save.m, steps)
			if !ok {
				return componentReply(commandError("arguments.nbtpath.nothing_found", args[3]))
			}
			seq = nil
		}
		if len(args) > 4 {
			scale, err := strconv.ParseFloat(args[4], 64)
			if err != nil {
				return componentReply(commandError("command.unknown.command"))
			}
			if scaled, ok := nbtScaleValue(target, scale); ok {
				target = scaled
			}
		}
		var comp component
		if m, ok := target.(map[string]any); ok {
			comp = translatable("commands.data.entity.query",
				tgt.nameComp(), component{extra: nbtPrettyMapSeq(m, seq), text: ""})
		} else {
			comp = translatable("commands.data.entity.query",
				tgt.nameComp(), component{extra: nbtPrettyElems(target), text: ""})
		}
		r := componentReply(comp)
		r.result, r.hasResult = nbtResult(target), true
		return r

	case "merge":
		if len(args) < 4 {
			return componentReply(commandError("command.unknown.command"))
		}
		tg, _, perr := parseSNBTCompound(args[3], 0)
		if perr != nil || tg == nil || tg.kind != snbtCompound {
			return componentReply(commandError("command.unknown.command"))
		}
		nbtMergeInto(save.m, snbtToValue(tg))
		s.applyDataEntity(tgt, save)
		return componentReply(translatable("commands.data.entity.modified", tgt.nameComp()))

	case "remove":
		if len(args) < 4 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[3])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		if !nbtRemove(save.m, steps) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		s.applyDataEntity(tgt, save)
		return componentReply(translatable("commands.data.entity.modified", tgt.nameComp()))

	case "modify":
		if len(args) < 7 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[3])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		op := args[4]
		rest := args[5:]
		var insertIdx int
		if op == "insert" {
			v, err := strconv.Atoi(rest[0])
			if err != nil || len(rest) < 2 {
				return componentReply(commandError("command.unknown.command"))
			}
			insertIdx = v
			rest = rest[1:]
		}
		val, serr := s.modifySource(player, rest)
		if serr != nil {
			return *serr
		}
		if !nbtModify(save.m, steps, op, insertIdx, val) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		s.applyDataEntity(tgt, save)
		return componentReply(translatable("commands.data.entity.modified", tgt.nameComp()))
	}
	return componentReply(commandError("command.unknown.command"))
}

// modifySource parses a data modify source: `value <snbt>`, or
// `from`/`string` <entity sel|block x y z|storage id> [path]. `string`
// needs the resolved value to be a string tag — anything else throws
// commands.data.modify.expected_value with the value's SNBT form.
func (s *Server) modifySource(player string, args []string) (any, *reply) {
	if len(args) < 2 {
		r := componentReply(commandError("command.unknown.command"))
		return nil, &r
	}
	var val any
	switch args[0] {
	case "value":
		tg, _, perr := parseSNBTTag(args[1], 0)
		if perr != nil || tg == nil {
			r := componentReply(commandError("command.unknown.command"))
			return nil, &r
		}
		return snbtToValue(tg), nil
	case "from", "string":
		wantStr := args[0] == "string"
		switch args[1] {
		case "entity":
			tgt, rep := s.resolveDataEntity(player, args[2])
			if tgt == nil {
				return nil, rep
			}
			val = tgt.saveMap().m
			if len(args) > 3 {
				steps, ok := parseNBTPath(args[3])
				if !ok {
					r := componentReply(commandError("command.unknown.command"))
					return nil, &r
				}
				val, ok = nbtGet(val, steps)
				if !ok {
					r := componentReply(commandError("arguments.nbtpath.nothing_found", args[3]))
					return nil, &r
				}
			}
		case "block":
			if len(args) < 5 {
				r := componentReply(commandError("command.unknown.command"))
				return nil, &r
			}
			d := s.dimOf(player)
			bx, by, bz := s.baseBlockPos(player)
			x, err1 := parseCoord(args[2], bx)
			y, err2 := parseCoord(args[3], by)
			z, err3 := parseCoord(args[4], bz)
			if err1 != nil || err2 != nil || err3 != nil {
				r := componentReply(commandError("command.unknown.command"))
				return nil, &r
			}
			be, ok := s.blockEntitySaveMap(d, world.Pos{X: x, Y: y, Z: z})
			if !ok {
				r := componentReply(commandError("commands.data.block.invalid"))
				return nil, &r
			}
			delete(be, "keepPacked")
			val = be
			if len(args) > 5 {
				steps, ok := parseNBTPath(args[5])
				if !ok {
					r := componentReply(commandError("command.unknown.command"))
					return nil, &r
				}
				val, ok = nbtGet(val, steps)
				if !ok {
					r := componentReply(commandError("arguments.nbtpath.nothing_found", args[5]))
					return nil, &r
				}
			}
		case "storage":
			if len(args) < 3 {
				r := componentReply(commandError("command.unknown.command"))
				return nil, &r
			}
			// Copied: `from`/`string` sources carry a snapshot — a `set`
			// must not alias the live compound it was read out of.
			val = nbtDeepCopy(s.storageCompound(args[2]))
			if len(args) > 3 {
				steps, ok := parseNBTPath(args[3])
				if !ok {
					r := componentReply(commandError("command.unknown.command"))
					return nil, &r
				}
				val, ok = nbtGet(val, steps)
				if !ok {
					r := componentReply(commandError("arguments.nbtpath.nothing_found", args[3]))
					return nil, &r
				}
			}
		default:
			r := componentReply(commandError("command.unknown.command"))
			return nil, &r
		}
		if wantStr {
			// DataCommands.getAsText: a StringTag yields its value, a
			// PrimitiveTag its SNBT form ("9b", "3.0f"), anything else
			// throws expected_value.
			switch sv := val.(type) {
			case string:
				return sv, nil
			case int8, int16, int32, int64, float32, float64:
				return snbtCompact(sv), nil
			}
			r := componentReply(commandError("commands.data.modify.expected_value", snbtCompact(val)))
			return nil, &r
		}
		return val, nil
	}
	r := componentReply(commandError("command.unknown.command"))
	return nil, &r
}

// snbtCompact renders a value in the compact SNBT form vanilla's
// Tag.toString produces — the expected_value error's argument.
func snbtCompact(v any) string {
	var sb strings.Builder
	writeSNBTCompact(&sb, v)
	return sb.String()
}

func writeSNBTCompact(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case map[string]any:
		sb.WriteByte('{')
		for i, k := range nbt.OrderedKeys(t) {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeSNBTString(sb, k)
			sb.WriteByte(':')
			writeSNBTCompact(sb, t[k])
		}
		sb.WriteByte('}')
	case nbt.List:
		sb.WriteByte('[')
		for i, it := range t.Items {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeSNBTCompact(sb, it)
		}
		sb.WriteByte(']')
	case []byte:
		sb.WriteString("[B;")
		for i, b := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(strconv.Itoa(int(b)))
		}
		sb.WriteByte(']')
	case []int32:
		sb.WriteString("[I;")
		for i, n := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(strconv.Itoa(int(n)))
		}
		sb.WriteByte(']')
	case []int64:
		sb.WriteString("[L;")
		for i, n := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(strconv.FormatInt(n, 10))
		}
		sb.WriteByte(']')
	case string:
		writeSNBTString(sb, t)
	case int8:
		sb.WriteString(strconv.Itoa(int(t)) + "b")
	case int16:
		sb.WriteString(strconv.Itoa(int(t)) + "s")
	case int32:
		sb.WriteString(strconv.Itoa(int(t)))
	case int64:
		sb.WriteString(strconv.FormatInt(t, 10) + "L")
	case float32:
		sb.WriteString(javaFloatStr(float64(t)) + "f")
	case float64:
		sb.WriteString(javaFloatStr(t) + "d")
	default:
		sb.WriteString("0")
	}
}

// writeSNBTString writes a quoted SNBT string — bare when the value
// is already a clean identifier, single-quoted when it carries a
// double quote, double-quoted otherwise.
func writeSNBTString(sb *strings.Builder, s string) {
	plain := s != ""
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '_' || r == '-' ||
			r == '.' || r == '+' || r == ':'
		if !ok {
			plain = false
			break
		}
	}
	if plain {
		sb.WriteString(s)
		return
	}
	q := byte('"')
	if strings.Contains(s, "\"") && !strings.Contains(s, "'") {
		q = '\''
	}
	sb.WriteByte(q)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == q || c == '\\' {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	sb.WriteByte(q)
}

// nbtScaleValue applies `data get ... <scale>` to numeric values.
func nbtScaleValue(v any, scale float64) (any, bool) {
	switch n := v.(type) {
	case int8:
		return int8(float64(n) * scale), true
	case int16:
		return int16(float64(n) * scale), true
	case int32:
		return int32(float64(n) * scale), true
	case int64:
		return int64(float64(n) * scale), true
	case float32:
		return float32(float64(n) * scale), true
	case float64:
		return n * scale, true
	}
	return nil, false
}

// dataEntity is a resolved /data entity target: a world entity or a
// player session.
type dataEntity struct {
	mob  *Entity
	sess *Session
}

// nameComp is the reply's first argument: the entity display name for
// a mob, the click-able player name for a session.
func (d *dataEntity) nameComp() component {
	if d.sess != nil {
		return playerChatComponent(d.sess)
	}
	return entityDisplayName(d.mob)
}

// entitySave is a save compound plus its write order — the order
// vanilla's saveWithoutId puts the keys, which breaks ties when two
// keys share a HashMap bucket (the pretty printer's key order).
type entitySave struct {
	m   map[string]any
	seq []string
}

// saveMap produces the entity's editable compound.
func (d *dataEntity) saveMap() entitySave {
	if d.sess != nil {
		return entitySave{m: playerSaveMap(d.sess)}
	}
	return entitySaveMap(d.mob)
}

// applyDataEntity writes the merged save map back and resyncs trackers
// with the dirtied entries, like vanilla's post-load sendChanges.
func (s *Server) applyDataEntity(d *dataEntity, save entitySave) {
	s.applyEntitySave(d.mob, save.m)
}

// entityDisplayName builds vanilla's decorated entity name component:
// text = CustomName verbatim (the stored component string) or the
// entity.<type> translation, insertion = the UUID string, and a
// show_entity hover whose name mirrors the text form — the raw string
// for custom names, a translate compound otherwise.
func entityDisplayName(e *Entity) component {
	typeName, ok := EntityTypeName(e.Type)
	if !ok {
		typeName = "pig"
	}
	u := e.UUID
	uuidInts := []int32{
		int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3]),
		int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7]),
		int32(u[8])<<24 | int32(u[9])<<16 | int32(u[10])<<8 | int32(u[11]),
		int32(u[12])<<24 | int32(u[13])<<16 | int32(u[14])<<8 | int32(u[15]),
	}
	uuidStr := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	var nameArg any
	var comp component
	if e.CustomName != "" {
		nameArg = e.CustomName
		comp = component{text: e.CustomName}
	} else {
		key := "entity.minecraft." + typeName
		nameArg = map[string]any{"translate": key}
		comp = component{translate: key}
	}
	comp.insertion = uuidStr
	comp.hover = map[string]any{
		"name":   nameArg,
		"action": "show_entity",
		"id":     "minecraft:" + typeName,
		"uuid":   uuidInts,
	}
	return comp
}

// entitySaveMap builds the mob save compound. The field set matches
// what a summoned entity reports: the shared entity fields, the living
// fields, and the type's extras. Keys absent from the entity model
// (Invulnerable, Air, ...) emit their summon defaults; ExtraNBT then
// overlays any data-written extras.
func entitySaveMap(e *Entity) entitySave {
	typeName, _ := EntityTypeName(e.Type)
	m := map[string]any{
		"AbsorptionAmount": float32(0),
		"Air":              int16(300),
		"CanPickUpLoot":    nbtBool(e.CanPickUpLoot),
		"DeathTime":        int16(0),
		"FallFlying":       int8(0),
		"Fire":             int16(0),
		"Health":           e.Health,
		"HurtTime":         int16(0),
		"Invulnerable":     int8(0),
		"LeftHanded":       int8(0),
		"Motion": nbt.List{Elem: nbt.TagDouble, Items: []any{
			e.MotionX, e.MotionY, e.MotionZ,
		}},
		"NoAI":                nbtBool(e.NoAI),
		"OnGround":            nbtBool(e.Grounded),
		"PersistenceRequired": int8(0),
		"PortalCooldown":      int32(0),
		"Pos":                 nbt.List{Elem: nbt.TagDouble, Items: []any{e.X, e.Y, e.Z}},
		"Rotation":            nbt.List{Elem: nbt.TagFloat, Items: []any{e.Yaw, e.Pitch}},
		"UUID": []int32{
			int32(e.UUID[0])<<24 | int32(e.UUID[1])<<16 | int32(e.UUID[2])<<8 | int32(e.UUID[3]),
			int32(e.UUID[4])<<24 | int32(e.UUID[5])<<16 | int32(e.UUID[6])<<8 | int32(e.UUID[7]),
			int32(e.UUID[8])<<24 | int32(e.UUID[9])<<16 | int32(e.UUID[10])<<8 | int32(e.UUID[11]),
			int32(e.UUID[12])<<24 | int32(e.UUID[13])<<16 | int32(e.UUID[14])<<8 | int32(e.UUID[15]),
		},
		"current_impulse_context_reset_grace_time": int32(0),
		"fall_distance": float64(0),
	}
	if e.CustomName != "" {
		m["CustomName"] = e.CustomName
	}
	if e.Saddle {
		m["equipment"] = map[string]any{
			"saddle": map[string]any{"id": "minecraft:saddle", "count": int32(1)},
		}
	}
	if speed, ok := mobSyncableSpeed[typeName]; ok {
		m["attributes"] = nbt.List{Elem: nbt.TagCompound, Items: []any{
			map[string]any{"id": "minecraft:movement_speed", "base": speed},
		}}
	}
	if extra, ok := entitySaveExtras[typeName]; ok {
		for k, v := range extra {
			// Deep-copied: a /data write into an extra (say
			// Brain.memories.zzk) must not mutate the shared table —
			// vanilla's load drops it before the next save anyway.
			m[k] = nbtDeepCopy(v)
		}
	}
	extraKeys := nbt.OrderedKeys(e.ExtraNBT)
	for k, v := range e.ExtraNBT {
		m[k] = v
	}
	seq := append(append([]string{}, entitySaveSeq...), extraKeys...)
	return entitySave{m: m, seq: seq}
}

// nbtDeepCopy clones a save-map value — maps and lists share tables
// between the per-type extras and a live save, so writes must not
// leak through the reference.
func nbtDeepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = nbtDeepCopy(vv)
		}
		return m
	case nbt.List:
		items := make([]any, len(t.Items))
		for i, it := range t.Items {
			items[i] = nbtDeepCopy(it)
		}
		return nbt.List{Elem: t.Elem, Items: items}
	case []any:
		out := make([]any, len(t))
		for i, it := range t {
			out[i] = nbtDeepCopy(it)
		}
		return out
	case []byte:
		return append([]byte{}, t...)
	case []int32:
		return append([]int32{}, t...)
	case []int64:
		return append([]int64{}, t...)
	}
	return v
}

// entitySaveSeq is the order vanilla's Entity.saveWithoutId writes
// the shared fields — bucket order is fixed by hash, but same-bucket
// keys come out in write order, so the pretty printer's key order
// needs this sequence as the tie-break.
var entitySaveSeq = []string{
	"Pos", "Motion", "Rotation", "fall_distance", "Fire", "Air",
	"OnGround", "Invulnerable", "PortalCooldown",
	"current_impulse_context_reset_grace_time", "UUID", "Tags",
	"CustomName", "Silent", "Health", "HurtTime", "DeathTime",
	"AbsorptionAmount", "attributes", "active_effects", "Brain",
	"FallFlying", "PersistenceRequired", "CanPickUpLoot",
	"LeftHanded", "NoAI", "equipment", "Age", "ForcedAge",
	"InLove", "AgeLocked", "variant", "sound_variant",
}

// entitySaveExtras are the per-type save fields beyond the shared
// living-mob set, measured per summoned entity. Types without an entry
// emit the shared fields only.
var entitySaveExtras = map[string]map[string]any{
	"pig": {
		"AgeLocked":     int8(0),
		"Age":           int32(0),
		"Brain":         map[string]any{"memories": map[string]any{}},
		"ForcedAge":     int32(0),
		"InLove":        int32(0),
		"sound_variant": "minecraft:classic",
		"variant":       "minecraft:temperate",
	},
}

// nbtBool renders a bool as vanilla's byte tag.
func nbtBool(v bool) int8 {
	if v {
		return 1
	}
	return 0
}

// applyEntitySave writes a /data-edited save map back onto the entity
// the way Entity.load reads it: fields absent from the map revert to
// their defaults, unknown keys are kept in ExtraNBT, and the metadata
// entries whose value changed resync as a dirty set_entity_data pack
// plus the movement_speed update_attributes vanilla emits on every
// mutation.
func (s *Server) applyEntitySave(e *Entity, save map[string]any) {
	prevHealth, prevName, prevNoAI := e.Health, e.CustomName, e.NoAI
	name, _ := EntityTypeName(e.Type)
	if v, ok := nbtFloat(save["Health"]); ok {
		e.Health = v
	} else {
		e.Health = mobHealth[name]
	}
	if v, ok := save["CustomName"].(string); ok {
		e.CustomName = v
	} else {
		e.CustomName = ""
	}
	e.NoAI = nbtTruth(save["NoAI"])
	e.CanPickUpLoot = nbtTruth(save["CanPickUpLoot"])
	if pos, ok := nbtVec3(save["Pos"]); ok {
		if e.X != pos[0] || e.Y != pos[1] || e.Z != pos[2] {
			e.X, e.Y, e.Z = pos[0], pos[1], pos[2]
			e.PosDirty = true
		}
	}
	if mot, ok := nbtVec3(save["Motion"]); ok {
		e.MotionX, e.MotionY, e.MotionZ = mot[0], mot[1], mot[2]
		e.VX = int16(mot[0] * 8000)
		e.VY = int16(mot[1] * 8000)
		e.VZ = int16(mot[2] * 8000)
	}
	if rot, ok := nbtVec2f(save["Rotation"]); ok {
		e.Yaw, e.Pitch = rot[0], rot[1]
		e.PosDirty = true
	}
	e.Grounded = nbtTruth(save["OnGround"])

	known := map[string]bool{
		"AbsorptionAmount": true, "Air": true, "CanPickUpLoot": true,
		"CustomName": true, "DeathTime": true, "FallFlying": true,
		"Fire": true, "Health": true, "HurtTime": true,
		"Invulnerable": true, "LeftHanded": true, "Motion": true,
		"NoAI": true, "OnGround": true, "PersistenceRequired": true,
		"PortalCooldown": true, "Pos": true, "Rotation": true,
		"UUID": true, "current_impulse_context_reset_grace_time": true,
		"fall_distance": true, "id": true, "equipment": true,
		"attributes": true,
	}
	if extra, ok := entitySaveExtras[name]; ok {
		for k := range extra {
			known[k] = true
		}
	}
	extras := map[string]any{}
	for k, v := range save {
		if !known[k] {
			extras[k] = v
		}
	}
	e.ExtraNBT = extras

	// Dirty pack: only the entries whose tracked value moved, like
	// SynchedEntityData.packDirty() after Entity.load.
	var b protocol.Buffer
	if prevName != e.CustomName {
		b.Byte(2)
		b.Byte(6) // optional component serializer
		if e.CustomName == "" {
			b.Byte(0)
		} else {
			b.Byte(1)
			writeComponentTag(&b, e.CustomName)
		}
	}
	if prevHealth != e.Health {
		b.Byte(9)
		b.Byte(3)
		b.Float(e.Health)
	}
	if prevNoAI != e.NoAI {
		b.Byte(15)
		b.Byte(0)
		b.Byte(mobFlagsByte(e))
	}
	if body := b.Bytes(); len(body) > 0 {
		e.dataSync = append(body, 0xFF)
	}
	e.dataSyncAttrs = true
}

// nbtVec3 reads a double list of at least three elements (Pos,
// Motion) — a longer list applies its first three like vanilla's
// load, which reads the head triple and resaves it.
func nbtVec3(v any) ([3]float64, bool) {
	l, ok := v.(nbt.List)
	if !ok || len(l.Items) < 3 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i := range out {
		f, ok := l.Items[i].(float64)
		if !ok {
			return [3]float64{}, false
		}
		out[i] = f
	}
	return out, true
}

// nbtVec2f reads a float list of at least two elements (Rotation).
func nbtVec2f(v any) ([2]float32, bool) {
	l, ok := v.(nbt.List)
	if !ok || len(l.Items) < 2 {
		return [2]float32{}, false
	}
	var out [2]float32
	for i := range out {
		f, ok := l.Items[i].(float32)
		if !ok {
			return [2]float32{}, false
		}
		out[i] = f
	}
	return out, true
}

// nbtFloat reads an NBT number as a float32.
func nbtFloat(v any) (float32, bool) {
	switch n := v.(type) {
	case float32:
		return n, true
	case float64:
		return float32(n), true
	case int8:
		return float32(n), true
	case int16:
		return float32(n), true
	case int32:
		return float32(n), true
	case int64:
		return float32(n), true
	}
	return 0, false
}

// nbtTruth reads a numeric/bool-ish NBT value as a flag.
func nbtTruth(v any) bool {
	switch n := v.(type) {
	case int8:
		return n != 0
	case int16:
		return n != 0
	case int32:
		return n != 0
	case bool:
		return n
	case string:
		return n == "true"
	}
	return false
}

// playerSaveMap is the modeled subset of the player save form: the
// position/motion block, health, food, xp and the shared entity flags.
// The full player save (inventory, abilities, recipes, ...) stays a
// gap — path gets and mutations resolve on the modeled fields.
func playerSaveMap(sess *Session) map[string]any {
	x, y, z, yaw, pitch, onGround := sess.Pos()
	u := sess.getUUID()
	return map[string]any{
		"AbsorptionAmount":    sess.absorb,
		"Air":                 int16(300),
		"DeathTime":           int16(0),
		"Fire":                int16(0),
		"Health":              sess.health,
		"HurtTime":            int16(0),
		"Invulnerable":        int8(0),
		"Motion":              nbt.List{Elem: nbt.TagDouble, Items: []any{0.0, 0.0, 0.0}},
		"OnGround":            nbtBool(onGround),
		"PortalCooldown":      int32(0),
		"Pos":                 nbt.List{Elem: nbt.TagDouble, Items: []any{x, y, z}},
		"Rotation":            nbt.List{Elem: nbt.TagFloat, Items: []any{yaw, pitch}},
		"SelectedItemSlot":    int32(0),
		"fall_distance":       sess.fallDistance,
		"foodExhaustionLevel": sess.exhaustion,
		"foodLevel":           sess.food,
		"foodSaturationLevel": sess.saturation,
		"foodTickTimer":       int32(sess.foodTickTimer),
		"XpLevel":             sess.xpLevel,
		"XpP":                 sess.xpProgress,
		"XpSeed":              sess.enchantSeed,
		"XpTotal":             sess.xpTotal,
		"UUID": []int32{
			int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3]),
			int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7]),
			int32(u[8])<<24 | int32(u[9])<<16 | int32(u[10])<<8 | int32(u[11]),
			int32(u[12])<<24 | int32(u[13])<<16 | int32(u[14])<<8 | int32(u[15]),
		},
	}
}

// resolveDataEntity parses the target token — @s/@p/@a/@r/@e
// selectors or a player name — into the single entity it names.
func (s *Server) resolveDataEntity(player, tok string) (*dataEntity, *reply) {
	sender := s.findSessionByName(player)
	cands, rep := s.selectDataEntities(sender, tok, true)
	if rep != nil {
		return nil, rep
	}
	if len(cands) == 0 {
		r := componentReply(commandError("argument.entity.notfound.entity"))
		return nil, &r
	}
	if len(cands) > 1 {
		r := errToken("argument.entity.toomany", 0)
		return nil, &r
	}
	return cands[0], nil
}

// selectorSpec is the parsed @x[...] argument list.
type selectorSpec struct {
	variable byte // 'e','a','p','r','s'
	typeName string
	typeNeg  bool
	limit    int
	hasLimit bool
	sortBy   string
	distMin  float64
	distMax  float64
	hasDist  bool
	name     string
	nameNeg  bool
	hasName  bool
	tag      string
	tagNeg   bool
	hasTag   bool
	x, y, z  float64
	hasPos   bool
}

// parseSelector decodes `@e[type=pig,limit=1]` style arguments.
func parseSelector(tok string) (selectorSpec, bool) {
	if len(tok) < 2 || tok[0] != '@' {
		return selectorSpec{}, false
	}
	spec := selectorSpec{variable: tok[1], sortBy: "nearest"}
	switch spec.variable {
	case 'e', 'a', 'p', 'r', 's':
	default:
		return selectorSpec{}, false
	}
	rest := tok[2:]
	if rest == "" {
		return spec, true
	}
	if len(rest) < 2 || rest[0] != '[' || rest[len(rest)-1] != ']' {
		return selectorSpec{}, false
	}
	for _, part := range strings.Split(rest[1:len(rest)-1], ",") {
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return selectorSpec{}, false
		}
		key, val := kv[0], kv[1]
		neg := strings.HasPrefix(val, "!")
		if neg {
			val = val[1:]
		}
		switch key {
		case "type":
			spec.typeName = strings.TrimPrefix(val, "minecraft:")
			spec.typeNeg = neg
		case "limit":
			n, err := strconv.Atoi(val)
			if err != nil {
				return selectorSpec{}, false
			}
			spec.limit, spec.hasLimit = n, true
		case "sort":
			spec.sortBy = val
		case "distance":
			spec.hasDist = true
			if i := strings.Index(val, ".."); i >= 0 {
				if i > 0 {
					lo, err := strconv.ParseFloat(val[:i], 64)
					if err != nil {
						return selectorSpec{}, false
					}
					spec.distMin = lo
				}
				if i+2 < len(val) {
					hi, err := strconv.ParseFloat(val[i+2:], 64)
					if err != nil {
						return selectorSpec{}, false
					}
					spec.distMax = hi
				} else {
					spec.distMax = math.Inf(1)
				}
			} else {
				n, err := strconv.ParseFloat(val, 64)
				if err != nil {
					return selectorSpec{}, false
				}
				spec.distMin, spec.distMax = n, n
			}
		case "x", "y", "z":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return selectorSpec{}, false
			}
			switch key {
			case "x":
				spec.x = f
			case "y":
				spec.y = f
			default:
				spec.z = f
			}
			spec.hasPos = true
		case "name":
			spec.name, spec.nameNeg, spec.hasName = val, neg, true
		case "tag":
			spec.tag, spec.tagNeg, spec.hasTag = val, neg, true
		default:
			return selectorSpec{}, false
		}
	}
	return spec, true
}

// selectDataEntities gathers the entities a selector names: sessions
// plus dimension entities filtered by the spec. Players carry type
// minecraft:player for @e.
func (s *Server) selectDataEntities(sender *Session, tok string, single bool) ([]*dataEntity, *reply) {
	if len(tok) == 0 || tok[0] != '@' {
		if sess := s.findSessionByName(tok); sess != nil {
			return []*dataEntity{{sess: sess}}, nil
		}
		r := componentReply(commandError("argument.entity.notfound.player"))
		return nil, &r
	}
	spec, ok := parseSelector(tok)
	if !ok {
		r := componentReply(commandError("command.unknown.argument"))
		return nil, &r
	}
	// A single-entity argument (/data) rejects a multi-target selector
	// upfront unless it caps at limit=1; multi-target commands like
	// /kill resolve whatever matches.
	if single && (spec.variable == 'e' || spec.variable == 'a') &&
		(!spec.hasLimit || spec.limit > 1) {
		r := errToken("argument.entity.toomany", 0)
		return nil, &r
	}
	if spec.variable == 's' {
		if sender == nil {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		return []*dataEntity{{sess: sender}}, nil
	}
	ax, ay, az := float64(0), float64(0), float64(0)
	if spec.hasPos {
		ax, ay, az = spec.x, spec.y, spec.z
	} else if sender != nil {
		ax, ay, az, _, _, _ = sender.Pos()
	}
	dim := ""
	if sender != nil {
		dim = sender.dimName()
	}
	var cands []*dataEntity
	addSess := func(sess *Session) {
		if spec.typeName != "" {
			if spec.typeNeg != (spec.typeName != "player") {
				return
			}
		}
		if spec.hasName {
			if spec.nameNeg == (sess.getName() == spec.name) {
				return
			}
		}
		// No scoreboard-tag store is modeled for players yet — a
		// session never carries tags, so tag= filters reject and
		// tag=! filters keep it.
		if spec.hasTag && !spec.tagNeg {
			return
		}
		if spec.hasDist {
			sx, sy, sz, _, _, _ := sess.Pos()
			dx, dy, dz := sx-ax, sy-ay, sz-az
			d := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if d < spec.distMin || d > spec.distMax {
				return
			}
		}
		cands = append(cands, &dataEntity{sess: sess})
	}
	switch spec.variable {
	case 'a', 'p', 'r', 'e':
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay && sess.dimName() == dim {
				addSess(sess)
			}
			return true
		})
	}
	if spec.variable == 'e' {
		// Dying-but-unremoved corpses still match — vanilla's selector
		// counts them and /kill reports them in the total.
		for _, e := range s.entities.All() {
			if e.Dim != dim {
				continue
			}
			ent := e
			name, _ := EntityTypeName(ent.Type)
			if spec.typeName != "" {
				if spec.typeNeg != (spec.typeName != name) {
					continue
				}
			}
			if spec.hasName {
				if spec.nameNeg == (ent.CustomName == spec.name) {
					continue
				}
			}
			if spec.hasTag {
				has := false
				if tg, ok := ent.ExtraNBT["Tags"]; ok {
					if l, ok := tg.(nbt.List); ok {
						for _, it := range l.Items {
							if it == spec.tag {
								has = true
							}
						}
					}
				}
				if spec.tagNeg == has {
					continue
				}
			}
			if spec.hasDist {
				dx, dy, dz := ent.X-ax, ent.Y-ay, ent.Z-az
				d := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if d < spec.distMin || d > spec.distMax {
					continue
				}
			}
			if ptr, ok := s.entities.GetPtr(ent.ID); ok {
				cands = append(cands, &dataEntity{mob: ptr})
			}
		}
	}
	distOf := func(d *dataEntity) float64 {
		var x, y, z float64
		if d.sess != nil {
			x, y, z, _, _, _ = d.sess.Pos()
		} else {
			x, y, z = d.mob.X, d.mob.Y, d.mob.Z
		}
		dx, dy, dz := x-ax, y-ay, z-az
		return dx*dx + dy*dy + dz*dz
	}
	switch spec.sortBy {
	case "furthest":
		sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) > distOf(cands[j]) })
	case "arbitrary":
		// Gather order stands.
	default:
		sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) < distOf(cands[j]) })
	}
	if spec.variable == 'p' || spec.variable == 'r' {
		spec.limit, spec.hasLimit = 1, true
	}
	if spec.hasLimit && spec.limit >= 0 && len(cands) > spec.limit {
		cands = cands[:spec.limit]
	}
	return cands, nil
}
