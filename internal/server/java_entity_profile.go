package server

import (
	"fmt"
	"io"

	"mc-go/api/registry"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// javaMetadataField is a measured canonical-776 accessor identity. The generated
// schema includes inherited accessors; entity types stay canonical in gameplay.
type javaMetadataField struct {
	index      byte
	serializer int32
	name       string
}

func canonicalEntityKey(entityType int32) (string, error) {
	domain := builtinDomain(776, "minecraft:entity_type")
	if domain == nil || entityType < 0 || int64(entityType) >= int64(len(domain.names)) {
		return "", fmt.Errorf("unknown canonical entity type %d", entityType)
	}
	return domain.names[entityType], nil
}

// encodeSpawn projects the detached pairing values. Entity.Type and Data stay
// untouched; Data is a legacy 776 block-state scalar only for falling blocks.
func (p *javaProfile) encodeSpawn(entity *Entity) ([]byte, error) {
	typeID, err := p.projectBuiltinRegistryID("minecraft:entity_type", entity.Type)
	if err != nil {
		return nil, err
	}
	copy := *entity
	copy.Type = typeID
	name, err := canonicalEntityKey(entity.Type)
	if err != nil {
		return nil, err
	}
	if name == "minecraft:falling_block" {
		copy.Data, err = p.projectBlockState776(entity.Data)
		if err != nil {
			return nil, err
		}
	}
	return encodeSpawn(&copy), nil
}

func (p *javaProfile) projectBlockState776(wire int32) (int32, error) {
	source, ok := world.BlockMapping(776)
	if !ok || p.blocks == nil {
		return 0, registry.ErrUnknownBlockState
	}
	canonical, ok := source.FromWire(wire)
	if !ok {
		return 0, registry.ErrUnknownBlockState
	}
	return p.blocks.Encode(canonical)
}

// projectAttributes776 preserves modifiers and bases while projecting registry
// IDs. Five measured newer-version attributes have no 775 wire entry; they are
// omitted from that viewer. Canonical 776 rules, including physics-related
// attributes, remain unchanged; omission does not establish equivalent client
// prediction or introduce an invented old-version attribute.
// Unknown canonical IDs and every other missing mapping remain errors.
func (p *javaProfile) projectAttributes776(body []byte) ([]byte, error) {
	if p.version.Protocol == 776 {
		return body, nil
	}
	r := protocol.NewPacketReader(body)
	eid, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	count, err := entityWireCount(r, 1024)
	if err != nil {
		return nil, err
	}
	var entries protocol.Buffer
	kept := int32(0)
	for i := int32(0); i < count; i++ {
		id, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		canonical := builtinDomain(776, "minecraft:attribute")
		if canonical == nil || id < 0 || int64(id) >= int64(len(canonical.names)) {
			return nil, fmt.Errorf("unknown canonical attribute %d", id)
		}
		wire, projectionErr := p.projectBuiltinRegistryID("minecraft:attribute", id)
		omit := false
		if projectionErr != nil {
			name := canonical.names[id]
			omit = p.version.Protocol == 775 && (name == "minecraft:air_drag_modifier" || name == "minecraft:below_name_distance" || name == "minecraft:bounciness" || name == "minecraft:friction_modifier" || name == "minecraft:name_tag_distance")
			if !omit {
				return nil, projectionErr
			}
		}
		var entry protocol.Buffer
		entry.VarInt(wire)
		if err := entityCopyFixed(r, &entry, 8); err != nil {
			return nil, err
		}
		mods, err := entityWireCount(r, 1024)
		if err != nil {
			return nil, err
		}
		entry.VarInt(mods)
		for j := int32(0); j < mods; j++ {
			key, err := r.String()
			if err != nil {
				return nil, err
			}
			entry.String(key)
			if err := entityCopyFixed(r, &entry, 8); err != nil {
				return nil, err
			}
			op, err := r.VarInt()
			if err != nil {
				return nil, err
			}
			if op < 0 || op > 2 {
				return nil, fmt.Errorf("invalid attribute modifier operation %d", op)
			}
			entry.VarInt(op)
		}
		if !omit {
			entries.Raw(entry.Bytes())
			kept++
		}
	}
	if r.Remaining() != 0 {
		return nil, fmt.Errorf("trailing attribute packet bytes")
	}
	if kept == 0 {
		return nil, nil
	}
	var out protocol.Buffer
	out.VarInt(eid)
	out.VarInt(kept)
	out.Raw(entries.Bytes())
	return out.Bytes(), nil
}

func (p *javaProfile) projectMobEffect776(body []byte, remove bool) ([]byte, error) {
	if p.version.Protocol == 776 {
		return body, nil
	}
	r := protocol.NewPacketReader(body)
	eid, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	id, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	wire, err := p.projectBuiltinRegistryID("minecraft:mob_effect", id)
	if err != nil {
		return nil, err
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(wire)
	if !remove {
		amplifier, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		b.VarInt(amplifier)
		duration, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		b.VarInt(duration)
		flags, err := r.Byte()
		if err != nil {
			return nil, err
		}
		b.Byte(flags)
	}
	if r.Remaining() != 0 {
		return nil, fmt.Errorf("trailing mob-effect bytes")
	}
	return b.Bytes(), nil
}

// projectMetadata776 consumes a complete canonical packet before any enqueue.
// Its type comes from the owner; transport workers never look up live entities.
// Every serializer/pose/enum is measured equal across 775/776. The generated
// accessor deltas handle the slime class-hierarchy change explicitly.
func (p *javaProfile) projectMetadata776(entityType int32, body []byte) ([]byte, error) {
	if p.version.Protocol == 776 {
		return body, nil
	}
	name, err := canonicalEntityKey(entityType)
	if err != nil {
		return nil, err
	}
	if _, err = p.projectBuiltinRegistryID("minecraft:entity_type", entityType); err != nil {
		return nil, err
	}
	schema := javaMetadataSchema776[name]
	if len(schema) == 0 {
		return nil, fmt.Errorf("no measured metadata schema for %s", name)
	}
	r := protocol.NewPacketReader(body)
	eid, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	var out protocol.Buffer
	out.VarInt(eid)
	kept := 0
	var seen, targetSeen [255]bool
	for {
		index, err := r.Byte()
		if err != nil {
			return nil, err
		}
		if index == 255 {
			break
		}
		if seen[index] {
			return nil, fmt.Errorf("duplicate metadata accessor %d", index)
		}
		seen[index] = true
		serializer, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		var field *javaMetadataField
		for i := range schema {
			if schema[i].index == index {
				field = &schema[i]
				break
			}
		}
		if field == nil || field.serializer != serializer {
			return nil, fmt.Errorf("invalid %s metadata accessor %d serializer %d", name, index, serializer)
		}
		var value protocol.Buffer
		if err := p.projectMetadataValue776(r, &value, serializer); err != nil {
			return nil, fmt.Errorf("%s metadata %s: %w", name, field.name, err)
		}
		target := int16(index)
		if p.version.Protocol == 775 {
			if changed, ok := javaMetadataIndices775[name][index]; ok {
				target = changed
			}
		}
		if target < 0 {
			continue
		} // Precisely measured absent slime baby/age-lock fields.
		if target >= 255 || targetSeen[target] {
			return nil, fmt.Errorf("metadata target accessor collision")
		}
		targetSeen[target] = true
		out.Byte(byte(target))
		out.VarInt(serializer)
		out.Raw(value.Bytes())
		kept++
	}
	if r.Remaining() != 0 {
		return nil, fmt.Errorf("trailing entity metadata bytes")
	}
	if kept == 0 {
		return nil, nil
	}
	out.Byte(255)
	return out.Bytes(), nil
}

var javaMetadataHolderDomains = [...]string{
	21: "minecraft:cat_variant", 22: "minecraft:cat_sound_variant",
	23: "minecraft:cow_variant", 24: "minecraft:cow_sound_variant",
	25: "minecraft:wolf_variant", 26: "minecraft:wolf_sound_variant",
	27: "minecraft:frog_variant", 28: "minecraft:pig_variant",
	29: "minecraft:pig_sound_variant", 30: "minecraft:chicken_variant",
	31: "minecraft:chicken_sound_variant", 32: "minecraft:zombie_nautilus_variant",
	34: "minecraft:painting_variant",
}

func (p *javaProfile) projectMetadataValue776(r *protocol.PacketReader, b *protocol.Buffer, serializer int32) error {
	integer := func() error {
		v, err := r.VarInt()
		if err != nil {
			return err
		}
		b.VarInt(v)
		return nil
	}
	optional := func(value func() error) error {
		present, err := entityWireBool(r)
		if err != nil {
			return err
		}
		b.Bool(present)
		if present {
			return value()
		}
		return nil
	}
	text := func() error {
		v, err := r.String()
		if err != nil {
			return err
		}
		b.String(v)
		return nil
	}
	if values, ok := javaMetadataEnums[serializer]; ok {
		id, err := r.VarInt()
		if err != nil {
			return err
		}
		if id < 0 || int64(id) >= int64(len(values)) {
			return fmt.Errorf("invalid metadata enum %d value %d", serializer, id)
		}
		b.VarInt(id)
		return nil
	}
	switch serializer {
	case 0:
		return entityCopyFixed(r, b, 1)
	case 1, 19:
		return integer()
	case 2:
		v, err := r.VarLong()
		if err != nil {
			return err
		}
		b.VarLong(v)
		return nil
	case 3:
		return entityCopyFixed(r, b, 4)
	case 4:
		return text()
	case 5:
		return entityCopyNBT(r, b)
	case 6:
		return optional(func() error { return entityCopyNBT(r, b) })
	case 7:
		return p.projectSlotFrom776(r, b)
	case 8:
		v, err := entityWireBool(r)
		if err != nil {
			return err
		}
		b.Bool(v)
		return nil
	case 9, 39:
		return entityCopyFixed(r, b, 12)
	case 10:
		return entityCopyFixed(r, b, 8)
	case 11:
		return optional(func() error { return entityCopyFixed(r, b, 8) })
	case 13:
		return optional(func() error { return entityCopyFixed(r, b, 16) })
	case 14, 15:
		id, err := r.VarInt()
		if err != nil {
			return err
		}
		if serializer == 15 && id == 0 {
			b.VarInt(0)
			return nil
		}
		wire, err := p.projectBlockState776(id)
		if err != nil {
			return err
		}
		b.VarInt(wire)
		return nil
	case 16:
		return p.projectParticleValue(r, b)
	case 17:
		n, err := entityWireCount(r, 1024)
		if err != nil {
			return err
		}
		b.VarInt(n)
		for i := int32(0); i < n; i++ {
			if err := p.projectParticleValue(r, b); err != nil {
				return err
			}
		}
		return nil
	case 18:
		for _, key := range []string{"minecraft:villager_type", "minecraft:villager_profession"} {
			id, err := r.VarInt()
			if err != nil {
				return err
			}
			wire, err := p.projectBuiltinRegistryID(key, id)
			if err != nil {
				return err
			}
			b.VarInt(wire)
		}
		return integer()
	case 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 34:

		id, err := r.VarInt()
		if err != nil {
			return err
		}
		if id <= 0 {
			return fmt.Errorf("inline metadata variant holder is not emitted by the canonical runtime")
		}
		wire, err := p.projectRegistryID(javaMetadataHolderDomains[serializer], id-1)
		if err != nil {
			return err
		}
		b.VarInt(wire + 1)
		return nil
	case 33:
		return optional(func() error {
			if err := text(); err != nil {
				return err
			}
			return entityCopyFixed(r, b, 8)
		})
	case 40:
		return entityCopyFixed(r, b, 16)
	case 41:
		return projectMetadataProfile(r, b)
	default:
		return fmt.Errorf("unmeasured metadata serializer %d", serializer)
	}
}

func entityWireCount(r *protocol.PacketReader, max int32) (int32, error) {
	n, err := r.VarInt()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > max {
		return 0, fmt.Errorf("entity list count %d exceeds %d", n, max)
	}
	return n, nil
}
func entityWireBool(r *protocol.PacketReader) (bool, error) {
	v, err := r.Byte()
	if err != nil {
		return false, err
	}
	if v > 1 {
		return false, fmt.Errorf("invalid entity boolean %d", v)
	}
	return v == 1, nil
}
func entityCopyFixed(r *protocol.PacketReader, b *protocol.Buffer, n int) error {
	raw, err := r.Read(n)
	if err != nil {
		return err
	}
	b.Raw(raw)
	return nil
}

type entityNBTReader struct{ reader *protocol.PacketReader }

func (r entityNBTReader) Read(dst []byte) (int, error) {
	raw, err := r.reader.Read(len(dst))
	if err != nil {
		return 0, err
	}
	return copy(dst, raw), nil
}
func entityCopyNBT(r *protocol.PacketReader, b *protocol.Buffer) error {
	_, err := nbt.ReadUnnamed(io.TeeReader(entityNBTReader{r}, b))
	return err
}

// ResolvableProfile is measured byte-for-byte equal in 775/776. A resolved
// profile carries UUID/name; an unresolved one carries optional name/UUID.
// Both continue with signed properties and three optional texture keys plus
// an optional model ID. Copy bounded fields instead of truncating an opaque tail.
func projectMetadataProfile(r *protocol.PacketReader, b *protocol.Buffer) error {
	text := func(max int) error {
		value, err := r.String()
		if err != nil {
			return err
		}
		if len(value) > max {
			return fmt.Errorf("profile string exceeds %d bytes", max)
		}
		b.String(value)
		return nil
	}
	optional := func(value func() error) error {
		present, err := entityWireBool(r)
		if err != nil {
			return err
		}
		b.Bool(present)
		if present {
			return value()
		}
		return nil
	}
	resolved, err := entityWireBool(r)
	if err != nil {
		return err
	}
	b.Bool(resolved)
	if resolved {
		if err := entityCopyFixed(r, b, 16); err != nil {
			return err
		}
		if err := text(16); err != nil {
			return err
		}
	} else {
		if err := optional(func() error { return text(16) }); err != nil {
			return err
		}
		if err := optional(func() error { return entityCopyFixed(r, b, 16) }); err != nil {
			return err
		}
	}
	properties, err := entityWireCount(r, 1024)
	if err != nil {
		return err
	}
	b.VarInt(properties)
	for i := int32(0); i < properties; i++ {
		if err := text(32767); err != nil {
			return err
		}
		if err := text(1 << 20); err != nil {
			return err
		}
		if err := optional(func() error { return text(1 << 20) }); err != nil {
			return err
		}
	}
	for texture := 0; texture < 3; texture++ {
		if err := optional(func() error { return text(32767) }); err != nil {
			return err
		}
	}
	return optional(func() error {
		model, err := r.VarInt()
		if err != nil {
			return err
		}
		if model < 0 || model > 1 {
			return fmt.Errorf("invalid profile model %d", model)
		}
		b.VarInt(model)
		return nil
	})
}

type javaProjectedEntityBody struct {
	body []byte
	err  error
}
type javaEntityBodyCache map[*javaProfile]javaProjectedEntityBody

func (sess *Session) writeMetadata776(entityType int32, body []byte) error {
	return (javaEntityBodyCache{}).writeMetadata(sess, entityType, body)
}
func (cache javaEntityBodyCache) writeMetadata(sess *Session, entityType int32, body []byte) error {
	profile, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return err
	}
	projected, ok := cache[profile]
	if !ok {
		projected.body, projected.err = profile.projectMetadata776(entityType, body)
		cache[profile] = projected
	}
	if projected.err != nil {
		sess.closeQueue()
		return projected.err
	}
	if len(projected.body) == 0 {
		return nil
	}
	return sess.write(sess.ids().PlayEntityMetadata, projected.body)
}
func (sess *Session) writeAttributes776(body []byte) error {
	return (javaEntityBodyCache{}).writeAttributes(sess, body)
}
func (cache javaEntityBodyCache) writeAttributes(sess *Session, body []byte) error {
	profile, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return err
	}
	projected, ok := cache[profile]
	if !ok {
		projected.body, projected.err = profile.projectAttributes776(body)
		cache[profile] = projected
	}
	if projected.err != nil {
		sess.closeQueue()
		return projected.err
	}
	if len(projected.body) == 0 {
		return nil
	}
	return sess.write(sess.ids().PlayUpdateAttributes, projected.body)
}
func (sess *Session) writeMobEffect776(body []byte, remove bool) error {
	return (javaEntityBodyCache{}).writeMobEffect(sess, body, remove)
}
func (cache javaEntityBodyCache) writeMobEffect(sess *Session, body []byte, remove bool) error {
	profile, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return err
	}
	projected, ok := cache[profile]
	if !ok {
		projected.body, projected.err = profile.projectMobEffect776(body, remove)
		cache[profile] = projected
	}
	if projected.err != nil {
		sess.closeQueue()
		return projected.err
	}
	id := sess.ids().PlayUpdateMobEffect
	if remove {
		id = sess.ids().PlayRemoveMobEffect
	}
	return sess.write(id, projected.body)
}
