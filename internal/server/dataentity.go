package server

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/data"
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
	tgt, rep := s.resolveDataEntity(player, args[2], 3)
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
			tgt, rep := s.resolveDataEntity(player, args[2], 3)
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
func (s *Server) resolveDataEntity(player, tok string, tokIdx int) (*dataEntity, *reply) {
	sender := s.findSessionByName(player)
	cands, rep := s.selectDataEntities(sender, tok, tokIdx, true)
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

// selectorSpec is the parsed @x[...] argument list. Each option the
// vanilla parser accepts lands on a term list: repeatable options
// (gamemode, tag, nbt, predicate, scores, advancements) accumulate;
// single options (type, name, team, sort, limit, the position set)
// error `argument.entity.options.inapplicable` on a repeat.
type selectorSpec struct {
	variable   byte // 'e','a','p','r','s'
	types      []selTerm
	modes      []selTerm
	names      []selTerm
	tags       []selTerm
	team       *selTerm
	limit      int
	hasLimit   bool
	sortBy     string
	dist       *selRange
	level      *selRange
	xrot       *selRange
	yrot       *selRange
	scores     []selScore
	nbts       []selNBT
	preds      []string
	advs       []selAdv
	x, y, z    float64
	hasPos     bool
	dx, dy, dz float64
	hasVol     bool
}

// selTerm is a value with a leading-! negation flag.
type selTerm struct {
	val string
	tag bool // type= only: the value started with #
	neg bool
}

// selRange is `lo..hi` with either bound optional; lo/hi default to
// the double extremes.
type selRange struct {
	lo, hi float64
}

func (r selRange) test(v float64) bool { return v >= r.lo && v <= r.hi }

// selScore is one scores={objective=range} entry.
type selScore struct {
	obj string
	r   selRange
}

// selNBT is one nbt={...} term; the value is the parsed compound.
type selNBT struct {
	m   map[string]any
	neg bool
}

// selAdv is one advancements={id=...} entry: a bare id or =true asks
// the advancement done, =false asks it not done, and ={crit=bool,...}
// asks each criterion's progress.
type selAdv struct {
	id    string
	done  *bool
	crits []selTerm
}

// parseRangeF64 reads `a`, `a..`, `..b`, `a..b`.
func parseRangeF64(v string) (selRange, bool) {
	if i := strings.Index(v, ".."); i >= 0 {
		var r selRange
		r.lo, r.hi = math.Inf(-1), math.Inf(1)
		if i > 0 {
			f, err := strconv.ParseFloat(v[:i], 64)
			if err != nil {
				return selRange{}, false
			}
			r.lo = f
		}
		if i+2 < len(v) {
			f, err := strconv.ParseFloat(v[i+2:], 64)
			if err != nil {
				return selRange{}, false
			}
			r.hi = f
		}
		return r, true
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return selRange{}, false
	}
	return selRange{f, f}, true
}

// splitSelectorArgs splits the bracket body on top-level commas,
// tracking {} nesting and " quotes; it returns each piece with its
// byte offset in the original body for error cursors.
func splitSelectorArgs(body string) ([]string, []int, bool) {
	var parts []string
	var offs []int
	depth := 0
	inQuote := false
	esc := false
	start := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case esc:
			esc = false
		case inQuote:
			switch c {
			case '\\':
				esc = true
			case '"':
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, body[start:i])
			offs = append(offs, start)
			start = i + 1
		}
	}
	if inQuote || depth != 0 {
		return nil, nil, false
	}
	parts = append(parts, body[start:])
	offs = append(offs, start)
	return parts, offs, true
}

// parseSelector decodes `@e[type=pig,limit=1]` style arguments. It
// returns a spec plus nil, or a parse error reply on failure — keys
// and withs copied off the real server's messages.
func parseSelector(tok string, tokIdx int) (selectorSpec, *reply) {
	bad := func(key string, off int, with ...any) (selectorSpec, *reply) {
		r := errTokenOff(key, tokIdx, off, with...)
		return selectorSpec{}, &r
	}
	if len(tok) < 2 || tok[0] != '@' {
		return bad("command.unknown.argument", 0)
	}
	spec := selectorSpec{variable: tok[1], sortBy: ""}
	switch spec.variable {
	case 'e', 'a', 'p', 'r', 's':
	default:
		return bad("command.unknown.argument", 0)
	}
	rest := tok[2:]
	if rest == "" {
		return spec, nil
	}
	if len(rest) < 2 || rest[0] != '[' || rest[len(rest)-1] != ']' {
		return bad("command.unknown.argument", 0)
	}
	// The failMark offset is measured from the whole input; the
	// selector is one token, so the reported offset is the index of
	// the bad text inside it.
	inner := rest[1 : len(rest)-1]
	parts, offs, ok := splitSelectorArgs(inner)
	if !ok {
		return bad("command.unknown.argument", 2)
	}
	singleUsed := map[string]bool{}
	for pi, part := range parts {
		if part == "" {
			continue
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			return bad("command.unknown.argument", 2+1+offs[pi])
		}
		key, val := part[:eq], part[eq+1:]
		voff := 2 + 1 + offs[pi] + eq + 1 // tok index of val's first byte
		neg := strings.HasPrefix(val, "!")
		if neg {
			val = val[1:]
			voff++
		}
		markSingle := func() *reply {
			if singleUsed[key] {
				r := errTokenOff("argument.entity.options.inapplicable", tokIdx, 2+1+offs[pi], key)
				return &r
			}
			singleUsed[key] = true
			return nil
		}
		switch key {
		case "type":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			t := selTerm{neg: neg}
			if strings.HasPrefix(val, "#") {
				t.tag = true
				val = val[1:]
			}
			t.val = strings.TrimPrefix(val, "minecraft:")
			if !t.tag {
				if _, ok := EntityTypeID(t.val); !ok && t.val != "player" {
					return bad("argument.entity.options.type.invalid", voff+1, val)
				}
			}
			spec.types = append(spec.types, t)
		case "gamemode":
			switch val {
			case "survival", "creative", "adventure", "spectator":
			default:
				return bad("argument.entity.options.mode.invalid", voff, val)
			}
			spec.modes = append(spec.modes, selTerm{val: val, neg: neg})
		case "limit":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return bad("parsing.int.expected", voff)
			}
			spec.limit, spec.hasLimit = n, true
		case "sort":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			switch val {
			case "nearest", "furthest", "random", "arbitrary":
			default:
				return bad("argument.entity.options.sort.irreversible", voff, val)
			}
			spec.sortBy = val
		case "distance":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			r, ok := parseRangeF64(val)
			if !ok {
				return bad("argument.range.empty", voff)
			}
			spec.dist = &r
		case "level":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			r, ok := parseRangeF64(val)
			if !ok {
				return bad("argument.range.empty", voff)
			}
			spec.level = &r
		case "x_rotation":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			r, ok := parseRangeF64(val)
			if !ok {
				return bad("argument.range.empty", voff)
			}
			spec.xrot = &r
		case "y_rotation":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			r, ok := parseRangeF64(val)
			if !ok {
				return bad("argument.range.empty", voff)
			}
			spec.yrot = &r
		case "x", "y", "z", "dx", "dy", "dz":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return bad("parsing.double.expected", voff)
			}
			switch key {
			case "x":
				spec.x = f
			case "y":
				spec.y = f
			case "z":
				spec.z = f
			case "dx":
				spec.dx = f
			case "dy":
				spec.dy = f
			case "dz":
				spec.dz = f
			}
			if key[0] == 'd' {
				spec.hasVol = true
			} else {
				spec.hasPos = true
			}
		case "name":
			spec.names = append(spec.names, selTerm{val: unquote(val), neg: neg})
		case "tag":
			spec.tags = append(spec.tags, selTerm{val: val, neg: neg})
		case "team":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			spec.team = &selTerm{val: val, neg: neg}
		case "scores":
			if len(val) < 2 || val[0] != '{' || val[len(val)-1] != '}' {
				return bad("parsing.expected", voff, "{")
			}
			for _, e := range strings.Split(val[1:len(val)-1], ",") {
				if e == "" {
					continue
				}
				kv := strings.SplitN(e, "=", 2)
				if len(kv) != 2 {
					return bad("command.unknown.argument", voff)
				}
				r, ok := parseRangeF64(kv[1])
				if !ok {
					return bad("argument.range.empty", voff+strings.Index(val, "=")+1)
				}
				spec.scores = append(spec.scores, selScore{obj: kv[0], r: r})
			}
		case "nbt":
			tg, _, err := parseSNBTTag(val, 0)
			if err != nil || tg == nil || tg.kind != snbtCompound {
				return bad("argument.nbt.expected.compound", voff)
			}
			m, _ := snbtToValue(tg).(map[string]any)
			spec.nbts = append(spec.nbts, selNBT{m: m, neg: neg})
		case "advancements":
			if len(val) < 2 || val[0] != '{' || val[len(val)-1] != '}' {
				return bad("parsing.expected", voff, "{")
			}
			entries, _, ok := splitSelectorArgs(val[1 : len(val)-1])
			if !ok {
				return bad("command.unknown.argument", voff)
			}
			for _, e := range entries {
				if e == "" {
					continue
				}
				ad := selAdv{}
				if eq2 := strings.Index(e, "="); eq2 >= 0 {
					ad.id = e[:eq2]
					v := e[eq2+1:]
					if strings.HasPrefix(v, "{") {
						for _, c := range splitKV(v[1 : len(v)-1]) {
							ck, cv, cok := strings.Cut(c, "=")
							_ = ck
							want := true
							if cok {
								want = cv != "false"
							}
							ad.crits = append(ad.crits, selTerm{val: ck, neg: !want})
						}
					} else {
						b := v != "false"
						ad.done = &b
					}
				} else {
					ad.id = e
					b := true
					ad.done = &b
				}
				spec.advs = append(spec.advs, ad)
			}
		case "predicate":
			spec.preds = append(spec.preds, val)
		default:
			return bad("argument.entity.options.unknown", 2+1+offs[pi], key)
		}
	}
	return spec, nil
}

// splitKV splits an SNBT-ish entry list on commas — criteria maps are
// shallow, no nested braces expected.
func splitKV(body string) []string {
	var out []string
	for _, e := range strings.Split(body, ",") {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// selectDataEntities gathers the entities a selector names: sessions
// plus dimension entities filtered by the spec. Players carry type
// minecraft:player for @e.
func (s *Server) selectDataEntities(sender *Session, tok string, tokIdx int, single bool) ([]*dataEntity, *reply) {
	if len(tok) == 0 || tok[0] != '@' {
		if sess := s.findSessionByName(tok); sess != nil {
			return []*dataEntity{{sess: sess}}, nil
		}
		r := componentReply(commandError("argument.entity.notfound.player"))
		return nil, &r
	}
	spec, serr := parseSelector(tok, tokIdx)
	if serr != nil {
		return nil, serr
	}
	// A single-entity argument (/data) rejects a multi-target selector
	// upfront unless it caps at limit=1; multi-target commands like
	// /kill resolve whatever matches.
	if single {
		maxRes := 1
		if spec.variable == 'e' || spec.variable == 'a' {
			maxRes = 1 << 30
		}
		if spec.hasLimit {
			maxRes = spec.limit
		}
		if maxRes > 1 {
			r := errToken("argument.entity.toomany", 0)
			return nil, &r
		}
	}
	if spec.variable == 's' {
		if sender == nil {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		// Inside an execute fork the sender's @s is the bound executor.
		if sender.execEnt != nil {
			return []*dataEntity{sender.execEnt}, nil
		}
		return []*dataEntity{{sess: sender}}, nil
	}
	ax, ay, az := float64(0), float64(0), float64(0)
	if spec.hasPos {
		ax, ay, az = spec.x, spec.y, spec.z
	} else if sender != nil && sender.execPos != nil {
		ax, ay, az = sender.execPos[0], sender.execPos[1], sender.execPos[2]
	} else if sender != nil && sender.execEnt != nil && sender.execEnt.mob != nil {
		ax, ay, az = sender.execEnt.mob.X, sender.execEnt.mob.Y, sender.execEnt.mob.Z
	} else if sender != nil {
		ax, ay, az, _, _, _ = sender.Pos()
	}
	dim := ""
	if sender != nil {
		dim = sender.dimName()
	}
	var cands []*dataEntity
	addSess := func(sess *Session) {
		if !s.selMatch(&spec, &dataEntity{sess: sess}, ax, ay, az) {
			return
		}
		cands = append(cands, &dataEntity{sess: sess})
	}
	switch spec.variable {
	case 'a', 'p', 'r', 'e':
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay && sess.dimName() == dim {
				// @e runs the entity list, which drops dead players;
				// @a/@p/@r walk the player list that still holds them.
				if spec.variable == 'e' && sess.dead {
					return true
				}
				addSess(sess)
			}
			return true
		})
	}
	if spec.variable == 'e' {
		// Dying-but-unremoved corpses still match — vanilla's selector
		// counts them and /kill reports them in the total.
		var mobs []*dataEntity
		for _, e := range s.entities.All() {
			if e.Dim != dim {
				continue
			}
			if n, _ := EntityTypeName(e.Type); n == "player" {
				// Player entity records double their session — the
				// session candidates already cover them.
				continue
			}
			if ptr, ok := s.entities.GetPtr(e.ID); ok {
				mobs = append(mobs, &dataEntity{mob: ptr})
			}
		}
		sort.Slice(mobs, func(i, j int) bool { return mobs[i].mob.ID < mobs[j].mob.ID })
		for _, de := range mobs {
			if s.selMatch(&spec, de, ax, ay, az) {
				cands = append(cands, de)
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
	case "nearest":
		sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) < distOf(cands[j]) })
	case "random":
		rand.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	default:
		// @e/@a gather order stands unless sort names one; @p/@r
		// carry their own implicit ordering.
		if spec.variable == 'p' {
			sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) < distOf(cands[j]) })
		}
	}
	if spec.variable == 'p' || spec.variable == 'r' {
		spec.limit, spec.hasLimit = 1, true
	}
	if spec.hasLimit && spec.limit >= 0 && len(cands) > spec.limit {
		cands = cands[:spec.limit]
	}
	return cands, nil
}

// selMatch applies every parsed option to one candidate: sessions and
// dimension mobs share the checks, with non-players failing the
// player-bound options (gamemode, level, advancements) the way the
// real server's instanceof tests exclude them.
func (s *Server) selMatch(spec *selectorSpec, d *dataEntity, ax, ay, az float64) bool {
	px, py, pz, pitch, yaw := d.posRot()
	name := d.selName()
	holder := d.selHolder()

	for _, t := range spec.types {
		matched := false
		if t.tag {
			matched = d.entityInTag(t.val)
		} else {
			matched = d.typeName() == t.val
		}
		if matched == t.neg {
			return false
		}
	}
	// gamemode/level bind to players only — non-players fail even the
	// negated forms.
	if len(spec.modes) > 0 || spec.level != nil {
		if d.sess == nil {
			return false
		}
		for _, m := range spec.modes {
			if (gamemodeNameOf(d.sess) == m.val) == m.neg {
				return false
			}
		}
		if spec.level != nil && !spec.level.test(float64(d.sess.xpLevel)) {
			return false
		}
	}
	for _, n := range spec.names {
		if (name == n.val) == n.neg {
			return false
		}
	}
	for _, t := range spec.tags {
		if d.hasTag(t.val) == t.neg {
			return false
		}
	}
	if spec.team != nil {
		tm := s.teamOf(holder)
		tn := ""
		if tm != nil {
			tn = tm.name
		}
		switch {
		case spec.team.val == "" && spec.team.neg:
			if tn == "" {
				return false
			}
		case spec.team.val == "":
			if tn != "" {
				return false
			}
		case spec.team.neg:
			if tn == spec.team.val {
				return false
			}
		default:
			if tn != spec.team.val {
				return false
			}
		}
	}
	for _, sc := range spec.scores {
		scv, ok := s.board.scores[holder][sc.obj]
		if !ok || !sc.r.test(float64(scv)) {
			return false
		}
	}
	for _, nb := range spec.nbts {
		if d.nbtMatches(nb.m) == nb.neg {
			return false
		}
	}
	// No predicate table exists — a predicate term fails everything.
	if len(spec.preds) > 0 {
		return false
	}
	for _, ad := range spec.advs {
		if d.sess == nil || !d.sess.advMatch(ad) {
			return false
		}
	}
	if spec.dist != nil {
		dx, dy, dz := px-ax, py-ay, pz-az
		if !spec.dist.test(math.Sqrt(dx*dx + dy*dy + dz*dz)) {
			return false
		}
	}
	if spec.hasVol {
		lo := func(a, dv float64) float64 {
			if dv < 0 {
				return a + dv
			}
			return a
		}
		hi := func(a, dv float64) float64 {
			if dv < 0 {
				return a
			}
			return a + dv
		}
		if px < lo(ax, spec.dx) || py < lo(ay, spec.dy) || pz < lo(az, spec.dz) ||
			px > hi(ax, spec.dx) || py > hi(ay, spec.dy) || pz > hi(az, spec.dz) {
			return false
		}
	}
	if spec.xrot != nil && !selRotMatch(spec.xrot, pitch) {
		return false
	}
	if spec.yrot != nil && !selRotMatch(spec.yrot, yaw) {
		return false
	}
	return true
}

// selRotMatch is vanilla's rotation predicate: the range bounds are
// themselves wrapped into (-180,180] — missing ends default to 0 and
// 359 — and a wrapped lo>hi tests the wrap-around union.
func selRotMatch(r *selRange, v float64) bool {
	wrap := func(f float64) float64 {
		f = math.Mod(f, 360)
		if f >= 180 {
			f -= 360
		}
		if f < -180 {
			f += 360
		}
		return f
	}
	lo, hi := 0.0, wrap(359)
	if r.lo != math.Inf(-1) {
		lo = wrap(r.lo)
	}
	if r.hi != math.Inf(1) {
		hi = wrap(r.hi)
	}
	wv := wrap(v)
	if lo <= hi {
		return wv >= lo && wv <= hi
	}
	return wv >= lo || wv <= hi
}

// posRot reads the candidate's position and rotation.
func (d *dataEntity) posRot() (x, y, z, pitch, yaw float64) {
	if d.sess != nil {
		x, y, z, yw, pt, _ := d.sess.Pos()
		return x, y, z, float64(pt), float64(yw)
	}
	return d.mob.X, d.mob.Y, d.mob.Z, float64(d.mob.Pitch), float64(d.mob.Yaw)
}

// selName is the name name= matches: a player's name, a mob's custom
// name, else the mob's type display.
func (d *dataEntity) selName() string {
	if d.sess != nil {
		return d.sess.getName()
	}
	if d.mob.CustomName != "" {
		return d.mob.CustomName
	}
	n, _ := EntityTypeName(d.mob.Type)
	return n
}

// selHolder is the scoreboard/team lookup key — the player name, or
// the mob's UUID string like vanilla's getScoreboardName.
func (d *dataEntity) selHolder() string {
	if d.sess != nil {
		return d.sess.getName()
	}
	u := d.mob.UUID
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// typeName is the entity's namespaced type (player included).
func (d *dataEntity) typeName() string {
	if d.sess != nil {
		return "player"
	}
	n, _ := EntityTypeName(d.mob.Type)
	return n
}

// entityInTag reports whether the entity's type belongs to a named
// entity-type tag — the subset the suite exercises is a literal table.
var entityTags = map[string][]string{
	"skeletons":                       {"skeleton", "stray", "wither_skeleton", "skeleton_horse", "bogged"},
	"zombies":                         {"zombie", "zombie_villager", "husk", "drowned", "zombified_piglin", "zombie_horse"},
	"raiders":                         {"evoker", "illusioner", "pillager", "ravager", "vindicator", "witch"},
	"undead":                          {"drowned", "husk", "phantom", "skeleton", "skeleton_horse", "stray", "wither_skeleton", "zombie", "zombie_horse", "zombie_villager", "zombified_piglin", "zoglin"},
	"arthropods":                      {"bee", "endermite", "silverfish", "spider", "cave_spider"},
	"aquatic":                         {"axolotl", "cod", "dolphin", "elder_guardian", "glow_squid", "guardian", "pufferfish", "salmon", "squid", "tadpole", "tropical_fish", "turtle"},
	"immune_to_melting":               {"stray", "polar_bear", "snow_golem", "wither", "blaze"},
	"sensitive_to_bane_of_arthropods": {"bee", "endermite", "silverfish", "spider", "cave_spider"},
}

func (d *dataEntity) entityInTag(tag string) bool {
	for _, t := range entityTags[strings.TrimPrefix(tag, "minecraft:")] {
		if t == d.typeName() {
			return true
		}
	}
	return false
}

// hasTag reports scoreboard-tag membership: mobs carry it in Tags;
// players carry the tags /tag gave the session.
func (d *dataEntity) hasTag(tag string) bool {
	if d.sess != nil {
		d.sess.tagMu.Lock()
		defer d.sess.tagMu.Unlock()
		for _, t := range d.sess.tags {
			if t == tag {
				return true
			}
		}
		return false
	}
	for _, t := range mobTagList(d.mob) {
		if t == tag {
			return true
		}
	}
	return false
}

// nbtMatches is vanilla's compareNbt partial match: every pattern key
// must be present with an equal tag type and value; compounds and
// lists recurse, lists comparing elementwise.
func (d *dataEntity) nbtMatches(pattern map[string]any) bool {
	var target map[string]any
	if d.sess != nil {
		target = playerSaveMap(d.sess)
	} else {
		target = entitySaveMap(d.mob).m
	}
	return nbtSubset(target, pattern)
}

func nbtSubset(target, pattern map[string]any) bool {
	for k, pv := range pattern {
		tv, ok := target[k]
		if !ok || !nbtPartialEqual(tv, pv) {
			return false
		}
	}
	return true
}

func nbtPartialEqual(tv, pv any) bool {
	tm, tok := tv.(map[string]any)
	pm, pok := pv.(map[string]any)
	if tok && pok {
		return nbtSubset(tm, pm)
	}
	tl, tok := tv.(nbt.List)
	pl, pok := pv.(nbt.List)
	if tok && pok {
		if len(pl.Items) > len(tl.Items) {
			return false
		}
		for i := range pl.Items {
			if !nbtPartialEqual(tl.Items[i], pl.Items[i]) {
				return false
			}
		}
		return true
	}
	return tv == pv
}

// advMatch evaluates an advancements={...} selector term against the
// session's advancement progress; an id the registry lacks fails
// regardless of the requested state.
func (sess *Session) advMatch(ad selAdv) bool {
	if _, ok := data.Advancements()[strings.TrimPrefix(ad.id, "minecraft:")]; !ok {
		return false
	}
	if len(ad.crits) > 0 {
		for _, c := range ad.crits {
			got := false
			if sess.advCrits != nil {
				_, got = sess.advCrits[ad.id][c.val]
			}
			if got == c.neg {
				return false
			}
		}
		return true
	}
	return sess.advDone[ad.id] == *ad.done
}

// gamemodeNameOf is the selector-visible mode name.
func gamemodeNameOf(sess *Session) string {
	return []string{"survival", "creative", "adventure", "spectator"}[sess.gameMode]
}
