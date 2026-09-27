package server

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"slices"
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
	save := tgt.saveMap(s.clock.count, s.entities)

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
		return opsEchoReply(translatable("commands.data.entity.modified", tgt.nameComp()))

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
		return opsEchoReply(translatable("commands.data.entity.modified", tgt.nameComp()))

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
		return opsEchoReply(translatable("commands.data.entity.modified", tgt.nameComp()))
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
			val = tgt.saveMap(s.clock.count, s.entities).m
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
func (d *dataEntity) saveMap(now int64, ents *Manager) entitySave {
	if d.sess != nil {
		return entitySave{m: playerSaveMap(d.sess, now, ents)}
	}
	return entitySaveMap(d.mob, now, ents)
}

// applyDataEntity writes the merged save map back and resyncs trackers
// with the dirtied entries, like vanilla's post-load sendChanges. A
// merge that leaves Health at zero kills the mob outright — vanilla's
// load pushes it through die() on the next tick.
func (s *Server) applyDataEntity(d *dataEntity, save entitySave) {
	s.applyEntitySave(d.mob, save.m)
	if d.mob != nil && d.mob.Health <= 0 {
		s.mobDie(d.mob, nil, false)
	}
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
		// An item entity's display name is its stack's: dirt names
		// itself block.minecraft.dirt, not entity.minecraft.item.
		if typeName == "item" && !e.Item.Empty() {
			key = itemDisplayKey(mustItemName(e.Item.ID))
		}
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
// overlays any data-written extras. ents resolves leash holders — the
// leash key's shape depends on the holder being a knot or an entity.
func entitySaveMap(e *Entity, now int64, ents *Manager) entitySave {
	typeName, _ := EntityTypeName(e.Type)
	m := map[string]any{
		"Air":          int16(300),
		"Fire":         int16(e.FireTicks),
		"Invulnerable": nbtBool(e.Invulnerable),
		"Motion": nbt.List{Elem: nbt.TagDouble, Items: []any{
			e.MotionX, e.MotionY, e.MotionZ,
		}},
		"OnGround":       nbtBool(e.Grounded),
		"PortalCooldown": int32(0),
		"Pos":            nbt.List{Elem: nbt.TagDouble, Items: []any{e.X, e.Y, e.Z}},
		"Rotation":       nbt.List{Elem: nbt.TagFloat, Items: []any{e.Yaw, e.Pitch}},
		"UUID": []int32{
			int32(e.UUID[0])<<24 | int32(e.UUID[1])<<16 | int32(e.UUID[2])<<8 | int32(e.UUID[3]),
			int32(e.UUID[4])<<24 | int32(e.UUID[5])<<16 | int32(e.UUID[6])<<8 | int32(e.UUID[7]),
			int32(e.UUID[8])<<24 | int32(e.UUID[9])<<16 | int32(e.UUID[10])<<8 | int32(e.UUID[11]),
			int32(e.UUID[12])<<24 | int32(e.UUID[13])<<16 | int32(e.UUID[14])<<8 | int32(e.UUID[15]),
		},
		"fall_distance": float64(0),
	}
	// Entity.save writes TicksFrozen only while above zero.
	if e.TicksFrozen > 0 {
		m["TicksFrozen"] = e.TicksFrozen
	}
	// LivingEntity.save adds the living block — carts, boats and
	// other non-living entities emit the shared set only.
	if e.Living {
		m["AbsorptionAmount"] = float32(0)
		m["CanPickUpLoot"] = nbtBool(e.CanPickUpLoot)
		m["DeathTime"] = int16(0)
		m["FallFlying"] = int8(0)
		m["Health"] = e.Health
		m["HurtTime"] = e.HurtTime
		m["LeftHanded"] = nbtBool(e.LeftHanded)
		m["NoAI"] = nbtBool(e.NoAI)
		m["PersistenceRequired"] = nbtBool(e.PersistenceRequired)
		m["current_impulse_context_reset_grace_time"] = int32(0)
	}
	if e.CustomName != "" {
		m["CustomName"] = e.CustomName
	}
	if e.Living {
		equipMob := map[string]any{}
		for slot, name := range map[int32]string{
			equipMainHand: "mainhand", equipOffHand: "offhand",
			equipBoots: "feet", equipLegs: "legs",
			equipChest: "chest", equipHelmet: "head",
		} {
			if it := e.Equips[slot]; !it.Empty() {
				equipMob[name] = itemNBT(it)
			}
		}
		// EquipmentSlot order: BODY after HEAD, SADDLE last.
		if !e.BodyItem.Empty() {
			equipMob["body"] = itemNBT(e.BodyItem)
		}
		if e.Saddle {
			equipMob["saddle"] = map[string]any{"id": "minecraft:saddle", "count": int32(1)}
		}
		if len(equipMob) > 0 {
			m["equipment"] = equipMob
		}
	}
	if e.Living {
		// AbstractHorse writes Tame; AbstractChestedHorse writes
		// ChestedHorse always and the flat Items list when chested —
		// every non-empty inventory cell (saddle slot 0, body slot 1,
		// chest cells 2..). The llama family adds Strength and Variant.
		if mountScreenTypes[typeName] {
			m["Tame"] = nbtBool(e.Tamed)
		}
		if chestedMountTypes[typeName] {
			m["ChestedHorse"] = nbtBool(e.HasChest)
			if e.HasChest {
				var items []any
				if e.Saddle {
					if sid, ok := world.ItemID("saddle"); ok {
						items = append(items, itemNBTSlotted(ItemStack{ID: sid, Count: 1}, 0))
					}
				}
				if !e.BodyItem.Empty() {
					items = append(items, itemNBTSlotted(e.BodyItem, 1))
				}
				for i, st := range e.MountInv {
					if !st.Empty() {
						items = append(items, itemNBTSlotted(st, int8(i+2)))
					}
				}
				if len(items) > 0 {
					m["Items"] = nbt.List{Elem: nbt.TagCompound, Items: items}
				}
			}
		}
		if llamaDecorTypes[typeName] {
			m["Strength"] = e.Strength
			m["Variant"] = e.LlamaVariant
		}
		// The Zombie/AbstractPiglin/Zoglin hierarchies declare IsBaby —
		// AgeableMob types carry Age instead.
		if isBabyTypes[typeName] {
			m["IsBaby"] = nbtBool(e.Baby)
		}
		// Sheep.writeAdditionalSaveData: Sheared + the dye Color byte —
		// mc-go sheep are always white (0) until a dye field exists.
		if typeName == "sheep" {
			m["Sheared"] = nbtBool(e.Sheared)
			m["Color"] = int8(0)
		}
		// SnowGolem.writeAdditionalSaveData carries the Pumpkin flag.
		if typeName == "snow_golem" {
			m["Pumpkin"] = nbtBool(e.Pumpkin)
		}
		// Bogged.addAdditionalSaveData writes the lowercase "sheared" tag.
		if typeName == "bogged" {
			m["sheared"] = nbtBool(e.Sheared)
		}
		// AbstractPiglin and Hoglin declare the zombification pair.
		if overworldZombifyTypes[typeName] {
			m["IsImmuneToZombification"] = nbtBool(e.ImmuneToZombification)
			m["TimeInOverworld"] = e.TimeInOverworld
		}
		// PatrollingMonster declares PatrolLeader/Patrolling and the
		// nullable patrol_target BlockPos; the leader head slot's 2.0 drop
		// chance writes a drop_chances override compound.
		if patrollingTypes[typeName] {
			m["PatrolLeader"] = nbtBool(e.PatrolLeader)
			m["Patrolling"] = nbtBool(e.Patrolling)
			if e.HasPatrolTarget {
				m["patrol_target"] = []int32{
					int32(e.PatrolTarget.X), int32(e.PatrolTarget.Y), int32(e.PatrolTarget.Z),
				}
			}
		}
	}
	// Entity.saveWithoutId writes Silent/NoGravity/CustomNameVisible
	// only while set.
	if e.Silent {
		m["Silent"] = int8(1)
	}
	if e.NoGravity {
		m["NoGravity"] = int8(1)
	}
	if e.CustomNameVisible {
		m["CustomNameVisible"] = int8(1)
	}
	if e.Living {
		// Bee.addAdditionalSaveData writes the bee fields always:
		// HasNectar/HasStung bytes, TicksSincePollination and
		// CannotEnterHiveTicks ints. mc-go doesn't pollinate — the pair
		// stays 0b/0.
		if typeName == "bee" {
			m["HasNectar"] = int8(0)
			m["HasStung"] = nbtBool(e.Stung)
			m["TicksSincePollination"] = int32(0)
			m["CannotEnterHiveTicks"] = e.CannotEnterHive
		}
		// Mob.addAdditionalSaveData writes the home pair only while
		// hasHome — home_radius int plus the home_pos BlockPos list.
		if e.HasHome {
			m["home_radius"] = e.HomeRadius
			m["home_pos"] = []int32{
				int32(e.HomePos.X), int32(e.HomePos.Y), int32(e.HomePos.Z),
			}
		}
		// Raider.addAdditionalSaveData writes Wave + CanJoinRaid always
		// and RaidId only while the raider is flagged into a raid.
		if raidRaiderType(typeName) {
			m["Wave"] = e.Wave
			if e.CanJoinRaid {
				m["CanJoinRaid"] = int8(1)
			} else {
				m["CanJoinRaid"] = int8(0)
			}
			if e.Raid != nil {
				m["RaidId"] = int32(e.Raid.id)
			}
		}
		if e.PatrolLeader {
			m["drop_chances"] = map[string]any{"head": float32(2)}
		}
	}
	if typeName == "item" && !e.Item.Empty() {
		// ItemEntity.addAdditionalSaveData: the carried stack and the
		// pickup-delay countdown; Owner/Thrower only serialize when set.
		m["Item"] = itemNBT(e.Item)
		m["PickupDelay"] = int16(e.PickupDelay)
	}
	// The Zombie hierarchy writes CanBreakDoors plus the live
	// water-conversion pair; NeutralMob types carry anger_end_time.
	if zombieTypes[typeName] {
		m["CanBreakDoors"] = nbtBool(e.CanBreakDoors)
		m["InWaterTime"] = e.inWaterTime
		if e.WaterConv {
			m["DrownedConversionTime"] = e.ConversionTime
		} else {
			m["DrownedConversionTime"] = int32(-1)
		}
	}
	if neutralAngryTypes[typeName] {
		m["anger_end_time"] = int64(0)
	}
	if e.Living {
		// Mob.addAdditionalSaveData serializes the brain for every
		// Mob — always an empty memories compound on mc-go.
		if typeName != "armor_stand" {
			m["Brain"] = map[string]any{"memories": map[string]any{}}
		}
		if items := mobAttrPack(e, typeName); len(items) > 0 {
			m["attributes"] = nbt.List{Elem: nbt.TagCompound, Items: items}
		}
		// LivingEntity.save adds active_effects after attributes; it
		// is absent entirely when the effect map is empty.
		if len(e.Effects) > 0 {
			m["active_effects"] = mobEffectsNBT(e.Effects, now)
		}
	}
	// The cart family rides VehicleEntity — FlippedRotation/HasTicked
	// come from AbstractMinecart.save; the variant fields each
	// subtype's addAdditionalSaveData appends.
	if isMinecartName(typeName) {
		m["FlippedRotation"] = int8(0)
		m["HasTicked"] = int8(0)
	}
	if extra, ok := entitySaveExtras[typeName]; ok {
		for k, v := range extra {
			// Deep-copied: a /data write into an extra (say
			// Brain.memories.zzk) must not mutate the shared table —
			// vanilla's load drops it before the next save anyway.
			m[k] = nbtDeepCopy(v)
		}
		// Age reads the live counter, not the template's 0.
		if _, has := extra["Age"]; has {
			m["Age"] = e.Age
		}
	}
	extraKeys := nbt.OrderedKeys(e.ExtraNBT)
	for k, v := range e.ExtraNBT {
		m[k] = v
	}
	// LeashData.save: leash:[I;x,y,z] for a knot holder,
	// leash:[I;a,b,c,d] (the UUID int-array) for an entity holder.
	if e.Living && e.LeashHolder != 0 && ents != nil {
		if h, ok := ents.Get(e.LeashHolder); ok {
			if hn, _ := EntityTypeName(h.Type); hn == "leash_knot" {
				m["leash"] = []int32{int32(h.X), int32(h.Y), int32(h.Z)}
			} else {
				m["leash"] = []int32{
					int32(h.UUID[0])<<24 | int32(h.UUID[1])<<16 | int32(h.UUID[2])<<8 | int32(h.UUID[3]),
					int32(h.UUID[4])<<24 | int32(h.UUID[5])<<16 | int32(h.UUID[6])<<8 | int32(h.UUID[7]),
					int32(h.UUID[8])<<24 | int32(h.UUID[9])<<16 | int32(h.UUID[10])<<8 | int32(h.UUID[11]),
					int32(h.UUID[12])<<24 | int32(h.UUID[13])<<16 | int32(h.UUID[14])<<8 | int32(h.UUID[15]),
				}
			}
		}
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
	"CustomName", "Silent", "NoGravity", "Health", "HurtTime", "DeathTime",
	"AbsorptionAmount", "attributes", "active_effects", "Brain",
	"FallFlying", "PersistenceRequired", "CanPickUpLoot",
	"LeftHanded", "NoAI", "equipment", "Age", "ForcedAge",
	"InLove", "AgeLocked", "variant", "sound_variant",
	"PatrolLeader", "drop_chances", "IsBaby",
	"IsImmuneToZombification", "TimeInOverworld",
	"home_radius", "home_pos", "Wave", "CanJoinRaid", "RaidId",
}

// overworldZombifyTypes are the mc-side types declaring the
// IsImmuneToZombification/TimeInOverworld save keys.
var overworldZombifyTypes = map[string]bool{
	"piglin": true, "piglin_brute": true, "hoglin": true,
}

// patrollingTypes are PatrollingMonster's mc-side summonable types —
// the PatrolLeader save key only exists on that hierarchy.
var patrollingTypes = map[string]bool{
	"pillager": true, "vindicator": true, "evoker": true,
	"illusioner": true, "witch": true, "ravager": true,
}

// tamableWildHealth is the wild max_health that
// TamableAnimal.setTame(false) re-asserts on load — the vanilla taming
// tables bump it back up when the entity reads an Owner.
var tamableWildHealth = map[string]float64{
	"wolf": 8,
}

// entitySaveExtras are the per-type save fields beyond the shared
// living-mob set, measured per summoned entity. Types without an entry
// emit the shared fields only.
var entitySaveExtras = map[string]map[string]any{
	// The minecart variants' addAdditionalSaveData fields — the
	// shared FlippedRotation/HasTicked pair rides the isMinecartName
	// emit in entitySaveMap.
	"chest_minecart": {
		"Items": nbt.List{Elem: nbt.TagCompound},
	},
	"hopper_minecart": {
		"Enabled": int8(1),
		"Items":   nbt.List{Elem: nbt.TagCompound},
	},
	"furnace_minecart": {
		"Fuel":  int16(0),
		"PushX": float64(0),
		"PushZ": float64(0),
	},
	"tnt_minecart": {
		"fuse": int32(-1),
	},
	"command_block_minecart": {
		"Command":             "",
		"SuccessCount":        int32(0),
		"UpdateLastExecution": int8(1),
		"TrackOutput":         int8(1),
	},
	"spawner_minecart": {
		"Delay":               int16(16),
		"MinSpawnDelay":       int16(200),
		"MaxSpawnDelay":       int16(800),
		"SpawnCount":          int16(4),
		"MaxNearbyEntities":   int16(6),
		"RequiredPlayerRange": int16(16),
		"SpawnRange":          int16(4),
		"SpawnPotentials": nbt.List{Elem: nbt.TagCompound, Items: []any{
			map[string]any{"data": map[string]any{"entity": map[string]any{}}, "weight": int32(1)},
		}},
	},
	"pig": {
		"AgeLocked":     int8(0),
		"Age":           int32(0),
		"Brain":         map[string]any{"memories": map[string]any{}},
		"ForcedAge":     int32(0),
		"InLove":        int32(0),
		"sound_variant": "minecraft:classic",
		"variant":       "minecraft:temperate",
	},
	// Piglin.addAdditionalSaveData: CannotHunt byte and the always-
	// present (possibly empty) picked-up-item Inventory.
	"piglin": {
		"CannotHunt": int8(0),
		"Inventory":  nbt.List{Elem: nbt.TagCompound},
	},
}

// neutralAngryTypes are the NeutralMob types whose save carries
// anger_end_time — ZombifiedPiglin and Zoglin among the summons.
var neutralAngryTypes = map[string]bool{
	"zombified_piglin": true,
	"zoglin":           true,
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
	prevSheared := e.Sheared
	prevPumpkin := e.Pumpkin
	name, _ := EntityTypeName(e.Type)
	// attributes:[{id,base,modifiers?}] — AttributeInstance.apply
	// writes baseValue directly (no clamp, no dirty) and replaces the
	// permanent modifier set outright; each entry also creates the
	// instance, so it joins the touched set.
	applyPackedAttrs(e, save["attributes"])
	// Tamable reload quirk: vanilla's TamableAnimal.setTame(false)
	// — run because the merged save carries no Owner — re-asserts
	// the wild max_health base, undoing any /attribute override
	// before the Health write clamps against it. A wolf merged with
	// {Health:100f} ends at 8, not 40, even after base set 40. This
	// runs after applyPackedAttrs because vanilla applies the packed
	// attributes first (LivingEntity.load) and reverts the base in
	// readAdditionalSaveData.
	if wild, ok := tamableWildHealth[name]; ok {
		if save["Owner"] == nil {
			if e.AttrBase == nil {
				e.AttrBase = map[string]float64{}
			}
			e.AttrBase["max_health"] = wild
			e.markAttrDirty("max_health")
		}
	}
	if v, ok := nbtFloat(save["Health"]); ok {
		// setHealth clamps to [0, getMaxHealth] — the attribute's
		// effective value — so a merged {Health:0f} marks the mob
		// dead and {Health:100f} lands at max, not over it.
		max := attrValue(mobAttrBase(e, "max_health"), e.AttrMods["max_health"])
		e.Health = v
		if e.Health > float32(max) {
			e.Health = float32(max)
		}
		if e.Health < 0 {
			e.Health = 0
		}
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
	e.PersistenceRequired = nbtTruth(save["PersistenceRequired"])
	e.Invulnerable = nbtTruth(save["Invulnerable"])
	e.CustomNameVisible = nbtTruth(save["CustomNameVisible"])
	if zombieTypes[name] {
		e.CanBreakDoors = nbtTruth(save["CanBreakDoors"])
		if iv, ok := nbtInt32(save["InWaterTime"]); ok {
			e.inWaterTime = iv
		}
		// DrownedConversionTime restarts the countdown via
		// startUnderWaterConversion; -1 clears the flag.
		if iv, ok := nbtInt32(save["DrownedConversionTime"]); ok {
			if iv != -1 {
				e.WaterConv = true
				e.ConversionTime = iv
				e.MetaDirty = true
			} else {
				e.WaterConv = false
				e.MetaDirty = true
			}
		}
	}
	// Skeleton's StrayConversionTime restarts the freeze countdown —
	// it converts only while the mob stays in powder snow.
	if name == "skeleton" {
		if iv, ok := nbtInt32(save["StrayConversionTime"]); ok && iv != -1 {
			e.FreezeConv = true
			e.ConversionTime = iv
			e.MetaDirty = true
		}
	}
	// Mob.readAdditionalSaveData — getIntOr defaults homeRadius to
	// -1, so an absent/-1 key clears hasHome; a non-negative one
	// also reads the home_pos BlockPos (ZERO when absent).
	r, _ := nbtInt32(save["home_radius"])
	e.HasHome = r != -1
	e.HomeRadius = r
	if r >= 0 {
		if pos, ok := nbtIntVec3(save["home_pos"]); ok {
			e.HomePos = pos
		}
	}
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
		// Entity.load sets needsSync after reading Motion — the stored
		// velocity re-emits at the next sendChanges gate.
		e.PosDirty = true
	}
	if rot, ok := nbtVec2f(save["Rotation"]); ok {
		e.Yaw, e.Pitch = rot[0], rot[1]
		e.PosDirty = true
	}
	e.Grounded = nbtTruth(save["OnGround"])
	e.Silent = nbtTruth(save["Silent"])
	e.NoGravity = nbtTruth(save["NoGravity"])
	// Sheep.readAdditionalSaveData: Sheared + Color (mc-go ignores
	// Color — sheep stay white; Sheared dirties the wool byte).
	if name == "sheep" {
		if v, ok := save["Sheared"]; ok {
			e.Sheared = nbtTruth(v)
		}
	}
	if name == "snow_golem" {
		// SnowGolem.readAdditionalSaveData: absent Pumpkin reads
		// as the default (true).
		if v, ok := save["Pumpkin"]; ok {
			e.Pumpkin = nbtTruth(v)
		} else {
			e.Pumpkin = true
		}
	}
	if name == "bogged" {
		if v, ok := save["sheared"]; ok {
			e.Sheared = nbtTruth(v)
		}
	}
	// Raider.readAdditionalSaveData: Wave int, CanJoinRaid bool,
	// and RaidId re-joins the raid when it still exists.
	if raidRaiderType(name) {
		if w, ok := nbtInt32(save["Wave"]); ok {
			e.Wave = w
		}
		e.CanJoinRaid = nbtTruth(save["CanJoinRaid"])
		if rid, ok := nbtInt32(save["RaidId"]); ok {
			if d := s.dimensionByName(e.Dim); d != nil {
				if r := d.raids[int(rid)]; r != nil {
					e.Raid = r
					r.raiders[e.ID] = true
				}
			}
		}
	}

	known := map[string]bool{
		"AbsorptionAmount": true, "Air": true, "CanPickUpLoot": true,
		"CustomName": true, "DeathTime": true, "FallFlying": true,
		"Fire": true, "Health": true, "HurtTime": true,
		"Invulnerable": true, "LeftHanded": true, "Motion": true,
		"NoAI": true, "OnGround": true, "PersistenceRequired": true,
		"PortalCooldown": true, "Pos": true, "Rotation": true,
		"UUID": true, "current_impulse_context_reset_grace_time": true,
		"fall_distance": true, "id": true, "equipment": true,
		"attributes": true, "home_radius": true, "home_pos": true,
		"NoGravity": true, "Silent": true, "Wave": true,
		"CanJoinRaid": true, "RaidId": true,
		"Sheared": true, "Color": true, "Pumpkin": true,
		"sheared": true, "CustomNameVisible": true,
		"CanBreakDoors": true, "InWaterTime": true,
		"DrownedConversionTime": true, "anger_end_time": true,
		"Brain": true, "Inventory": true, "CannotHunt": true,
		"IsBaby": true, "IsImmuneToZombification": true,
		"TimeInOverworld": true, "StrayConversionTime": true,
		"TicksFrozen": true,
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
	if prevSheared != e.Sheared && name == "sheep" {
		b.Byte(18)
		b.Byte(0)
		v := byte(0)
		if e.Sheared {
			v = 0x10
		}
		b.Byte(v)
	}
	if prevPumpkin != e.Pumpkin && name == "snow_golem" {
		b.Byte(16)
		b.Byte(0)
		v := byte(0)
		if e.Pumpkin {
			v = 1
		}
		b.Byte(v)
	}
	if prevSheared != e.Sheared && name == "bogged" {
		// Bogged's DATA_SHEARED sits at index 16 as a boolean.
		b.Byte(16)
		b.Byte(8) // type: boolean
		v := byte(0)
		if e.Sheared {
			v = 1
		}
		b.Byte(v)
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

// nbtInt32 reads an NBT number as an int32.
func nbtInt32(v any) (int32, bool) {
	switch n := v.(type) {
	case int8:
		return int32(n), true
	case int16:
		return int32(n), true
	case int32:
		return n, true
	case int64:
		return int32(n), true
	case float32:
		return int32(n), true
	case float64:
		return int32(n), true
	}
	return 0, false
}

// nbtIntVec3 reads an IntArray of at least three elements
// (BlockPos codec shape).
func nbtIntVec3(v any) (world.Pos, bool) {
	l, ok := v.([]int32)
	if !ok || len(l) < 3 {
		return world.Pos{}, false
	}
	return world.Pos{X: int(l[0]), Y: int(l[1]), Z: int(l[2])}, true
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
func playerSaveMap(sess *Session, now int64, ents *Manager) map[string]any {
	x, y, z, yaw, pitch, onGround := sess.Pos()
	u := sess.getUUID()
	m := map[string]any{
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
	// Equipment: the vanilla save compound carries the worn slots by
	// name — mainhand excepted, it serializes as SelectedItem — with
	// empty slots left out.
	sess.invMu.Lock()
	equip := map[string]any{}
	add := func(key string, st ItemStack) {
		if !st.Empty() {
			equip[key] = itemNBT(st)
		}
	}
	held := sess.inv[invSlotForHotbar(sess.held.Load())]
	add("offhand", sess.inv[slotOffhand])
	add("feet", sess.inv[slotArmorStart+3])
	add("legs", sess.inv[slotArmorStart+2])
	add("chest", sess.inv[slotArmorStart+1])
	add("head", sess.inv[slotArmorStart])
	add("body", sess.bodyArmor)
	if len(equip) > 0 {
		m["equipment"] = equip
	}
	if !held.Empty() {
		m["SelectedItem"] = itemNBT(held)
	}
	// Inventory answers in vanilla's slot numbering: hotbar 0-8, main
	// 9-35. EnderItems numbers 0-26.
	inv := nbt.List{Elem: nbt.TagCompound}
	for slot := slotMainStart; slot < slotCount-1; slot++ {
		it := sess.inv[slot]
		if it.Empty() {
			continue
		}
		vanilla := slot
		if slot >= slotHotbarStart {
			vanilla = slot - slotHotbarStart
		}
		inv.Items = append(inv.Items, itemNBTSlotted(it, int8(vanilla)))
	}
	// While mounted, Entity.saveWithoutId adds RootVehicle:
	// {Entity: the vehicle's full save — id included — Attach:
	// the vehicle's UUID int-array}. Player passengers never appear
	// in the vehicle's own Passengers list.
	if vid := sess.riding.Load(); vid != 0 && ents != nil {
		if ve, ok := ents.GetPtr(vid); ok {
			vm := entitySaveMap(ve, now, ents).m
			if typeName, ok := EntityTypeName(ve.Type); ok {
				vm["id"] = "minecraft:" + typeName
			}
			m["RootVehicle"] = map[string]any{
				"Entity": vm,
				"Attach": []int32{
					int32(ve.UUID[0])<<24 | int32(ve.UUID[1])<<16 | int32(ve.UUID[2])<<8 | int32(ve.UUID[3]),
					int32(ve.UUID[4])<<24 | int32(ve.UUID[5])<<16 | int32(ve.UUID[6])<<8 | int32(ve.UUID[7]),
					int32(ve.UUID[8])<<24 | int32(ve.UUID[9])<<16 | int32(ve.UUID[10])<<8 | int32(ve.UUID[11]),
					int32(ve.UUID[12])<<24 | int32(ve.UUID[13])<<16 | int32(ve.UUID[14])<<8 | int32(ve.UUID[15]),
				},
			}
		}
	}
	m["Inventory"] = inv
	ender := nbt.List{Elem: nbt.TagCompound}
	for i, it := range sess.ender {
		if !it.Empty() {
			ender.Items = append(ender.Items, itemNBTSlotted(it, int8(i)))
		}
	}
	m["EnderItems"] = ender
	sess.invMu.Unlock()
	sess.effMu.Lock()
	if len(sess.effects) > 0 {
		m["active_effects"] = mobEffectsNBT(sess.effects, sess.server.clock.count)
	}
	sess.effMu.Unlock()
	return m
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
	typePos    bool // a non-negated type= was used
	x, y, z    float64
	posSet     [3]bool // which of x/y/z were given
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

// singleMaxRes is the static match cap the single-entity argument
// types check at parse: @e/@a and explicit limits above one reject
// with argument.entity.toomany before the selector ever runs.
func (spec selectorSpec) singleMaxRes() int {
	if spec.hasLimit {
		return spec.limit
	}
	switch spec.variable {
	case 'e', 'a':
		return 1 << 30
	}
	return 1
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
	if inQuote {
		return nil, nil, false
	}
	// An unclosed { still splits fine — the option parser catches the
	// real error inside the value.
	if depth < 0 {
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
	case 'e', 'a', 'p', 'r', 's', 'n':
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
	typeSeen := false
	for pi, part := range parts {
		if part == "" {
			continue
		}
		optOff := 2 + 1 + offs[pi] // tok index of the option's key
		eq := strings.Index(part, "=")
		key := part
		if eq >= 0 {
			key = part[:eq]
		}
		// Vanilla resolves the option name before it ever looks for
		// the `=`: an unknown name errors options.unknown, a known
		// name without a value errors options.valueless.
		if !selOptionNames[key] {
			return bad("argument.entity.options.unknown", optOff, key)
		}
		if eq < 0 {
			return bad("argument.entity.options.valueless", optOff, key)
		}
		val := part[eq+1:]
		voff := optOff + eq + 1 // tok index of val's first byte
		neg := strings.HasPrefix(val, "!")
		negFree := val
		if neg {
			negFree = val[1:]
		}
		markSingle := func() *reply {
			if singleUsed[key] {
				r := errTokenOff("argument.entity.options.inapplicable", tokIdx, optOff, key)
				return &r
			}
			singleUsed[key] = true
			return nil
		}
		switch key {
		case "type":
			// type= may repeat only while every use is negated. The
			// error cursor differs by the history: once a positive
			// type was seen the next type= errors at its key, but a
			// positive arriving after only negatives lands the cursor
			// on the value — the handler reads it before complaining.
			if typeSeen {
				if spec.typePos {
					return bad("argument.entity.options.inapplicable", optOff, "type")
				}
				if !neg {
					return bad("argument.entity.options.inapplicable", voff, "type")
				}
			}
			typeSeen = true
			if !neg {
				spec.typePos = true
			}
			t := selTerm{neg: neg}
			if strings.HasPrefix(negFree, "#") {
				t.tag = true
				negFree = negFree[1:]
			}
			if t.tag {
				t.val = strings.TrimPrefix(negFree, "minecraft:")
				spec.types = append(spec.types, t)
				break
			}
			// A character that cannot be in an id ends the value:
			// junk after a valid prefix is "unterminated" at the
			// junk, a value that starts invalid - or is empty - is
			// type.invalid naming what the id parse produced.
			idEnd := 0
			for idEnd < len(negFree) && isSelIDChar(negFree[idEnd]) {
				idEnd++
			}
			if idEnd < len(negFree) && idEnd > 0 {
				return bad("argument.entity.options.unterminated", voff+idEnd)
			}
			norm := negFree[:idEnd]
			shown := "minecraft:"
			if norm != "" && idEnd == len(negFree) {
				shown = norm
				if !strings.Contains(norm, ":") {
					shown = "minecraft:" + norm
				}
			}
			lookup := strings.TrimPrefix(shown, "minecraft:")
			if _, ok := EntityTypeID(lookup); !ok && lookup != "player" {
				return bad("argument.entity.options.type.invalid", voff, shown)
			}
			t.val = lookup
			spec.types = append(spec.types, t)
		case "gamemode":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			switch negFree {
			case "survival", "creative", "adventure", "spectator":
			default:
				return bad("argument.entity.options.mode.invalid", voff, negFree)
			}
			spec.modes = append(spec.modes, selTerm{val: negFree, neg: neg})
		case "limit":
			// @s takes no shaping options.
			if spec.variable == 's' {
				return bad("argument.entity.options.inapplicable", optOff, "limit")
			}
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				return bad("parsing.int.expected", voff)
			}
			if n < 1 {
				return bad("argument.entity.options.limit.toosmall", voff)
			}
			spec.limit, spec.hasLimit = n, true
		case "sort":
			if spec.variable == 's' {
				return bad("argument.entity.options.inapplicable", optOff, "sort")
			}
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
			r, loSet, hiSet, rerr := parseRangeStrict(val)
			if rerr != "" {
				return bad("argument.range."+rerr, voff)
			}
			if (loSet && r.lo < 0) || (hiSet && r.hi < 0) {
				return bad("argument.entity.options.distance.negative", voff)
			}
			spec.dist = &r
		case "level":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			r, _, _, rerr := parseRangeStrict(val)
			if rerr != "" {
				return bad("argument.range."+rerr, voff)
			}
			spec.level = &r
		case "x_rotation", "y_rotation":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			// Rotations wrap degrees, so a lo>hi pair is legal — the
			// range parses again without the swap check.
			r, _, _, rerr := parseRangeStrict(val)
			if rerr == "swapped" {
				r, _ = parseRangeF64(val)
			} else if rerr != "" {
				return bad("argument.range."+rerr, voff)
			}
			if key == "x_rotation" {
				spec.xrot = &r
			} else {
				spec.yrot = &r
			}
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
				spec.x, spec.posSet[0] = f, true
			case "y":
				spec.y, spec.posSet[1] = f, true
			case "z":
				spec.z, spec.posSet[2] = f, true
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
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			spec.names = append(spec.names, selTerm{val: unquote(negFree), neg: neg})
		case "tag":
			spec.tags = append(spec.tags, selTerm{val: negFree, neg: neg})
		case "team":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			spec.team = &selTerm{val: negFree, neg: neg}
		case "scores":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
			if len(val) < 2 || val[0] != '{' || val[len(val)-1] != '}' {
				return bad("parsing.expected", voff, "{")
			}
			for _, e := range strings.Split(val[1:len(val)-1], ",") {
				if e == "" {
					continue
				}
				eo := voff + strings.Index(val, e)
				kv := strings.SplitN(e, "=", 2)
				if len(kv) != 2 {
					return bad("parsing.expected", eo+len(kv[0]), "=")
				}
				r, _, _, rerr := parseRangeStrict(kv[1])
				if rerr != "" {
					return bad("argument.range."+rerr, eo+len(kv[0])+1)
				}
				spec.scores = append(spec.scores, selScore{obj: kv[0], r: r})
			}
		case "nbt":
			tg, _, serr := parseSNBTTag(negFree, 0)
			if serr != nil {
				// The snbt failure reports its own cursor past the
				// value start; expected.compound lands at value end.
				return bad(serr.key, voff+boolOff(neg)+serr.pos, serr.with...)
			}
			if tg == nil || tg.kind != snbtCompound {
				return bad("argument.nbt.expected.compound", voff+len(val))
			}
			m, _ := snbtToValue(tg).(map[string]any)
			spec.nbts = append(spec.nbts, selNBT{m: m, neg: neg})
		case "advancements":
			if r := markSingle(); r != nil {
				return selectorSpec{}, r
			}
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
					} else if v == "true" || v == "false" {
						b := v == "true"
						ad.done = &b
					} else {
						return bad("parsing.bool.invalid", voff+strings.Index(val, e)+eq2+1, v)
					}
				} else {
					ad.id = e
					b := true
					ad.done = &b
				}
				spec.advs = append(spec.advs, ad)
			}
		case "predicate":
			if strings.HasPrefix(negFree, "#") {
				return bad("argument.entity.options.unterminated", voff+boolOff(neg))
			}
			spec.preds = append(spec.preds, val)
		}
	}
	return spec, nil
}

// selOptionNames is the vanilla selector-option registry; the lookup
// runs before the value so `foo` and `foo=bar` both report it unknown.
var selOptionNames = map[string]bool{
	"x": true, "y": true, "z": true, "dx": true, "dy": true, "dz": true,
	"distance": true, "limit": true, "sort": true, "gamemode": true,
	"level": true, "name": true, "type": true, "team": true, "tag": true,
	"nbt": true, "scores": true, "predicate": true, "advancements": true,
	"x_rotation": true, "y_rotation": true,
}

// isSelIDChar covers the characters a namespaced id may contain.
func isSelIDChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '/' || c == '-' || c == ':'
}

func boolOff(neg bool) int {
	if neg {
		return 1
	}
	return 0
}

// parseRangeStrict parses `a`, `a..`, `..b`, `a..b` like vanilla's
// MinMaxBounds: no digits at all or a bad numeral is "empty", a lo>hi
// pair is "swapped". It reports which ends were present so callers can
// check signedness only on given bounds (`..-1` and bare `..` differ).
func parseRangeStrict(v string) (selRange, bool, bool, string) {
	r := selRange{math.Inf(-1), math.Inf(1)}
	var loSet, hiSet bool
	if i := strings.Index(v, ".."); i >= 0 {
		if len(v) == 2 {
			return selRange{}, false, false, "empty"
		}
		if i > 0 {
			f, err := strconv.ParseFloat(v[:i], 64)
			if err != nil {
				return selRange{}, false, false, "empty"
			}
			r.lo, loSet = f, true
		}
		if i+2 < len(v) {
			f, err := strconv.ParseFloat(v[i+2:], 64)
			if err != nil {
				return selRange{}, false, false, "empty"
			}
			r.hi, hiSet = f, true
		}
		if loSet && hiSet && r.lo > r.hi {
			return selRange{}, false, false, "swapped"
		}
		return r, loSet, hiSet, ""
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return selRange{}, false, false, "empty"
	}
	return selRange{f, f}, true, true, ""
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
		// The executor is still filtered by the options — @s[type=pig]
		// on a player fails rather than matching the player.
		var self *dataEntity
		if sender.execEnt != nil {
			self = sender.execEnt
		} else {
			self = &dataEntity{sess: sender}
		}
		sx, sy, sz, _, _ := self.posRot()
		if !s.selMatch(&spec, self, sx, sy, sz) {
			return []*dataEntity{}, nil
		}
		return []*dataEntity{self}, nil
	}
	ax, ay, az := float64(0), float64(0), float64(0)
	if sender != nil && sender.execPos != nil {
		ax, ay, az = sender.execPos[0], sender.execPos[1], sender.execPos[2]
	} else if sender != nil && sender.execEnt != nil && sender.execEnt.mob != nil {
		ax, ay, az = sender.execEnt.mob.X, sender.execEnt.mob.Y, sender.execEnt.mob.Z
	} else if sender != nil {
		ax, ay, az, _, _, _ = sender.Pos()
	}
	// x/y/z override their own axis of the anchor only.
	if spec.posSet[0] {
		ax = spec.x
	}
	if spec.posSet[1] {
		ay = spec.y
	}
	if spec.posSet[2] {
		az = spec.z
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
	case 'a', 'p', 'r', 'e', 'n':
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay && sess.dimName() == dim {
				// @e/@n run the entity list, which drops dead players;
				// @a/@p/@r walk the player list that still holds them.
				if (spec.variable == 'e' || spec.variable == 'n') && sess.dead {
					return true
				}
				addSess(sess)
			}
			return true
		})
	}
	if spec.variable == 'e' || spec.variable == 'n' {
		var mobs []*dataEntity
		for _, e := range s.entities.All() {
			if e.Dim != dim || e.Dying {
				// Vanilla's selector never returns a dying-but-unremoved
				// corpse: measured on 26.2 — a dead pig's @e pick went
				// empty while the corpse still ticked its 20-tick window.
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
	sortBy := spec.sortBy
	if sortBy == "" {
		// @p/@n default to nearest, @r to random, @e/@a to gather order.
		switch spec.variable {
		case 'p', 'n':
			sortBy = "nearest"
		case 'r':
			sortBy = "random"
		}
	}
	switch sortBy {
	case "furthest":
		sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) > distOf(cands[j]) })
	case "nearest":
		sort.SliceStable(cands, func(i, j int) bool { return distOf(cands[i]) < distOf(cands[j]) })
	case "random":
		rand.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	}
	if !spec.hasLimit && (spec.variable == 'p' || spec.variable == 'r' || spec.variable == 'n') {
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
		// An empty value tests tag-lessness itself: tag= wants no
		// tags at all, tag=! wants at least one.
		if t.val == "" {
			if (d.tagCount() == 0) == t.neg {
				return false
			}
			continue
		}
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
		if !ok || !sc.r.test(float64(scv.value)) {
			return false
		}
	}
	for _, nb := range spec.nbts {
		if d.nbtMatches(nb.m, s.clock.count, s.entities) == nb.neg {
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
		if d := math.Sqrt(dx*dx + dy*dy + dz*dz); d < spec.dist.lo || d > spec.dist.hi {
			return false
		}
	}
	if spec.hasVol {
		// Vanilla's box per axis is [a+d, a] when d<0 else
		// [a, a+d+1], moved to the anchor; an entity matches when
		// its bounding box intersects, not when its position is in.
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
			return a + dv + 1
		}
		w, h := d.aabbDims()
		ex0, ey0, ez0 := px-w/2, py, pz-w/2
		ex1, ey1, ez1 := px+w/2, py+h, pz+w/2
		vx0, vy0, vz0 := lo(ax, spec.dx), lo(ay, spec.dy), lo(az, spec.dz)
		vx1, vy1, vz1 := hi(ax, spec.dx), hi(ay, spec.dy), hi(az, spec.dz)
		if vx0 >= ex1 || vx1 <= ex0 || vy0 >= ey1 || vy1 <= ey0 || vz0 >= ez1 || vz1 <= ez0 {
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

// aabbDims is the entity's bounding-box size — vanilla anchors the
// box at the feet, centered on x/z.
func (d *dataEntity) aabbDims() (w, h float64) {
	if d.sess != nil {
		return 0.6, 1.8
	}
	b := mobBounds(d.mob.Type)
	return b[0], b[1]
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
	"accepts_iron_golem_gift":         {"copper_golem"},
	"aquatic":                         {"axolotl", "cod", "dolphin", "elder_guardian", "glow_squid", "guardian", "nautilus", "pufferfish", "salmon", "squid", "tadpole", "tropical_fish", "turtle", "zombie_nautilus"},
	"arrows":                          {"arrow", "spectral_arrow"},
	"arthropod":                       {"bee", "cave_spider", "endermite", "silverfish", "spider"},
	"axolotl_always_hostiles":         {"drowned", "elder_guardian", "guardian"},
	"axolotl_hunt_targets":            {"cod", "glow_squid", "pufferfish", "salmon", "squid", "tadpole", "tropical_fish"},
	"beehive_inhabitors":              {"bee"},
	"boat":                            {"acacia_boat", "bamboo_raft", "birch_boat", "cherry_boat", "dark_oak_boat", "jungle_boat", "mangrove_boat", "oak_boat", "pale_oak_boat", "spruce_boat"},
	"burn_in_daylight":                {"bogged", "drowned", "phantom", "skeleton", "stray", "wither_skeleton", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager"},
	"can_breathe_under_water":         {"armor_stand", "axolotl", "bogged", "camel_husk", "cod", "copper_golem", "drowned", "elder_guardian", "frog", "glow_squid", "guardian", "husk", "nautilus", "parched", "phantom", "pufferfish", "salmon", "skeleton", "skeleton_horse", "squid", "stray", "tadpole", "tropical_fish", "turtle", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"can_equip_harness":               {"happy_ghast"},
	"can_equip_saddle":                {"camel", "camel_husk", "donkey", "horse", "mule", "nautilus", "pig", "skeleton_horse", "strider", "zombie_horse", "zombie_nautilus"},
	"can_float_while_ridden":          {"camel", "camel_husk", "donkey", "horse", "mule", "zombie_horse"},
	"can_turn_in_boats":               {"breeze"},
	"can_wear_horse_armor":            {"horse", "zombie_horse"},
	"can_wear_nautilus_armor":         {"nautilus", "zombie_nautilus"},
	"candidate_for_iron_golem_gift":   {"copper_golem", "villager"},
	"cannot_be_age_locked":            {"skeleton_horse", "villager", "zombie_horse"},
	"cannot_be_pushed_onto_boats":     {"cod", "creaking", "dolphin", "elder_guardian", "glow_squid", "nautilus", "player", "pufferfish", "salmon", "squid", "sulfur_cube", "tadpole", "tropical_fish", "zombie_nautilus"},
	"deflects_projectiles":            {"breeze"},
	"dismounts_underwater":            {"camel", "chicken", "donkey", "happy_ghast", "horse", "llama", "mule", "pig", "ravager", "spider", "strider", "trader_llama", "zombie_horse"},
	"fall_damage_immune":              {"allay", "bat", "bee", "blaze", "breeze", "cat", "chicken", "copper_golem", "ghast", "happy_ghast", "iron_golem", "magma_cube", "ocelot", "parrot", "phantom", "shulker", "snow_golem", "wither"},
	"followable_friendly_mobs":        {"armadillo", "bee", "camel", "cat", "chicken", "cow", "donkey", "fox", "goat", "happy_ghast", "horse", "llama", "mule", "ocelot", "panda", "parrot", "pig", "polar_bear", "rabbit", "sheep", "skeleton_horse", "sniffer", "strider", "villager", "wolf"},
	"freeze_hurts_extra_types":        {"blaze", "magma_cube", "strider"},
	"freeze_immune_entity_types":      {"polar_bear", "snow_golem", "stray", "wither"},
	"frog_food":                       {"magma_cube", "slime"},
	"ignores_poison_and_regen":        {"bogged", "camel_husk", "drowned", "husk", "parched", "phantom", "skeleton", "skeleton_horse", "stray", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"illager":                         {"evoker", "illusioner", "pillager", "vindicator"},
	"illager_friends":                 {"evoker", "illusioner", "pillager", "vindicator"},
	"immune_to_infested":              {"silverfish"},
	"immune_to_oozing":                {"slime"},
	"impact_projectiles":              {"arrow", "breeze_wind_charge", "dragon_fireball", "egg", "fireball", "firework_rocket", "small_fireball", "snowball", "spectral_arrow", "trident", "wind_charge", "wither_skull"},
	"inverted_healing_and_harm":       {"bogged", "camel_husk", "drowned", "husk", "parched", "phantom", "skeleton", "skeleton_horse", "stray", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"nautilus_hostiles":               {"pufferfish"},
	"no_anger_from_wind_charge":       {"bogged", "breeze", "cave_spider", "husk", "skeleton", "slime", "spider", "stray", "zombie"},
	"non_controlling_rider":           {"magma_cube", "slime", "sulfur_cube"},
	"not_affected_by_geysers":         {"ender_dragon"},
	"not_scary_for_pufferfish":        {"cod", "dolphin", "elder_guardian", "glow_squid", "guardian", "nautilus", "pufferfish", "salmon", "squid", "sulfur_cube", "tadpole", "tropical_fish", "turtle", "zombie_nautilus"},
	"powder_snow_walkable_mobs":       {"endermite", "fox", "rabbit", "silverfish"},
	"raiders":                         {"evoker", "illusioner", "pillager", "ravager", "vindicator", "witch"},
	"redirectable_projectile":         {"breeze_wind_charge", "fireball", "wind_charge"},
	"sensitive_to_bane_of_arthropods": {"bee", "cave_spider", "endermite", "silverfish", "spider"},
	"sensitive_to_impaling":           {"axolotl", "cod", "dolphin", "elder_guardian", "glow_squid", "guardian", "nautilus", "pufferfish", "salmon", "squid", "tadpole", "tropical_fish", "turtle", "zombie_nautilus"},
	"sensitive_to_smite":              {"bogged", "camel_husk", "drowned", "husk", "parched", "phantom", "skeleton", "skeleton_horse", "stray", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"skeletons":                       {"bogged", "parched", "skeleton", "skeleton_horse", "stray", "wither_skeleton"},
	"undead":                          {"bogged", "camel_husk", "drowned", "husk", "parched", "phantom", "skeleton", "skeleton_horse", "stray", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"wither_friends":                  {"bogged", "camel_husk", "drowned", "husk", "parched", "phantom", "skeleton", "skeleton_horse", "stray", "wither", "wither_skeleton", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
	"zombies":                         {"camel_husk", "drowned", "husk", "zoglin", "zombie", "zombie_horse", "zombie_nautilus", "zombie_villager", "zombified_piglin"},
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

// tagCount is the size of the entity's tag set — an empty tag=
// selector tests against it rather than against a tag named "".
func (d *dataEntity) tagCount() int {
	if d.sess != nil {
		d.sess.tagMu.Lock()
		defer d.sess.tagMu.Unlock()
		return len(d.sess.tags)
	}
	return len(mobTagList(d.mob))
}

// nbtMatches is vanilla's compareNbt partial match: every pattern key
// must be present with an equal tag type and value; compounds and
// lists recurse, lists comparing elementwise.
func (d *dataEntity) nbtMatches(pattern map[string]any, now int64, ents *Manager) bool {
	var target map[string]any
	if d.sess != nil {
		target = playerSaveMap(d.sess, now, ents)
	} else {
		target = entitySaveMap(d.mob, now, ents).m
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
		// Vanilla's compareNbt list rule is existential, not
		// positional: every pattern element must match SOME target
		// element (inner compares stay partial-recursive).
		if len(pl.Items) > len(tl.Items) {
			return false
		}
		for _, pe := range pl.Items {
			found := false
			for _, te := range tl.Items {
				if nbtPartialEqual(te, pe) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	// Typed arrays are slices — == would panic; vanilla compares
	// element-wise and requires the same tag type.
	switch t := tv.(type) {
	case []byte:
		p, ok := pv.([]byte)
		return ok && bytes.Equal(t, p)
	case []int32:
		p, ok := pv.([]int32)
		return ok && slices.Equal(t, p)
	case []int64:
		p, ok := pv.([]int64)
		return ok && slices.Equal(t, p)
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
