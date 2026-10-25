package server

import (
	"bytes"
	"errors"
	"fmt"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// ErrJavaUnsupportedCosmetic reports an intentionally omitted presentation
// event that the target does not represent. It never substitutes another ID.
// Mandatory state failures retire the affected viewer instead.
var ErrJavaUnsupportedCosmetic = errors.New("Java profile cannot represent cosmetic event")

// projectJavaOutbound is a transitional adapter: these legacy builders still
// produce canonical-776 presentation bodies. It is not a protocol-neutral
// gameplay event model. Stateful entity metadata uses explicit typed helpers;
// inventory slots already select their profile and must not pass here twice.
func (sess *Session) projectJavaOutbound(id int32, data []byte) ([]byte, error) {
	if sess.state.Load() != StatePlay {
		return data, nil
	}
	p := sess.profile
	if p == nil {
		if sess.ver.Protocol != 775 {
			return data, nil
		}
		p, _ = javaProfileFor(775)
	}
	if p.version.Protocol != 775 {
		return data, nil
	}
	ids := LatestVersion().IDs
	switch id {
	case ids.PlaySound:
		return p.projectSound776(data, false)
	case ids.PlaySoundEntity:
		return p.projectSound776(data, true)
	case ids.PlayLevelParticles:
		return p.projectLevelParticles776(data)
	case ids.PlayExplode:
		return p.projectExplosion776(data)
	case ids.PlayDamageEvent:
		return p.projectDamage776(data)
	case ids.PlayBlockEntityData:
		return p.projectBlockEntity776(data)
	case ids.PlaySetEquipment:
		return p.projectEquipment776(data)
	case ids.PlayBlockEvent:
		return p.projectBlockEvent776(data)
	default:
		return data, nil
	}
}

// writeProjectedPacket is restricted to already-target-encoded presentation
// packets, notably synthesized 775-only bed block entities. Ordinary callers
// use write so their source domain is projected once before byte admission.
func (sess *Session) writeProjectedPacket(id int32, data []byte) error {
	return sess.enqueue(sendPkt{id: id, data: data, charge: int64(len(data)) + int64(protocol.VarIntLen(id))})
}

func copyJavaBytes(r *protocol.PacketReader, b *protocol.Buffer, count int) error {
	data, err := r.Read(count)
	if err == nil {
		b.Raw(data)
	}
	return err
}

func copyJavaVarInt(r *protocol.PacketReader, b *protocol.Buffer) (int32, error) {
	id, err := r.VarInt()
	if err == nil {
		b.VarInt(id)
	}
	return id, err
}

func copyJavaBool(r *protocol.PacketReader, b *protocol.Buffer) (bool, error) {
	v, err := r.Byte()
	if err != nil {
		return false, err
	}
	if v > 1 {
		return false, fmt.Errorf("invalid Java boolean %d", v)
	}
	b.Byte(v)
	return v == 1, nil
}

func requireJavaEnd(r *protocol.PacketReader) error {
	if r.Remaining() != 0 {
		return fmt.Errorf("Java presentation body has %d trailing bytes", r.Remaining())
	}
	return nil
}

func (p *javaProfile) projectCosmeticID(key string, canonicalID int32) (int32, error) {
	current := builtinDomain(776, key)
	if current == nil || canonicalID < 0 || int(canonicalID) >= len(current.names) {
		return -1, fmt.Errorf("invalid canonical %s ID %d", key, canonicalID)
	}
	id, err := p.projectBuiltinRegistryID(key, canonicalID)
	if err != nil {
		return -1, fmt.Errorf("%w: %s %s", ErrJavaUnsupportedCosmetic, key, current.names[canonicalID])
	}
	return id, nil
}

func (p *javaProfile) projectSoundHolder776(r *protocol.PacketReader, b *protocol.Buffer) error {
	holder, err := r.VarInt()
	if err != nil {
		return err
	}
	if holder < 0 {
		return fmt.Errorf("negative sound holder %d", holder)
	}
	if holder > 0 {
		id, err := p.projectCosmeticID("minecraft:sound_event", holder-1)
		if err != nil {
			return err
		}
		b.VarInt(id + 1)
		return nil
	}
	name, err := r.String()
	if err != nil {
		return err
	}
	b.VarInt(0)
	b.String(name)
	hasRange, err := copyJavaBool(r, b)
	if err != nil {
		return err
	}
	if hasRange {
		return copyJavaBytes(r, b, 4)
	}
	return nil
}

func (p *javaProfile) projectSound776(data []byte, entity bool) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	if err := p.projectSoundHolder776(r, &b); err != nil {
		return nil, err
	}
	source, err := copyJavaVarInt(r, &b)
	if err != nil {
		return nil, err
	}
	if source < 0 || source > 10 {
		return nil, fmt.Errorf("invalid sound source %d", source)
	}
	if entity {
		if _, err := copyJavaVarInt(r, &b); err != nil {
			return nil, err
		}
	} else if err := copyJavaBytes(r, &b, 12); err != nil {
		return nil, err
	}
	if err := copyJavaBytes(r, &b, 16); err != nil {
		return nil, err
	}
	if err := requireJavaEnd(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *javaProfile) projectDamage776(data []byte) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	if _, err := copyJavaVarInt(r, &b); err != nil {
		return nil, err
	}
	typeID, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	wire, err := p.projectRegistryID("minecraft:damage_type", typeID)
	if err != nil {
		return nil, err
	}
	b.VarInt(wire)
	for range 2 {
		if _, err := copyJavaVarInt(r, &b); err != nil {
			return nil, err
		}
	}
	hasPosition, err := copyJavaBool(r, &b)
	if err != nil {
		return nil, err
	}
	if hasPosition {
		if err := copyJavaBytes(r, &b, 24); err != nil {
			return nil, err
		}
	}
	if err := requireJavaEnd(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *javaProfile) projectBlockEntity776(data []byte) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	if err := copyJavaBytes(r, &b, 8); err != nil {
		return nil, err
	}
	id, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	wire, err := p.projectBuiltinRegistryID("minecraft:block_entity_type", id)
	if err != nil {
		return nil, err
	}
	b.VarInt(wire)
	tag, err := r.Read(r.Remaining())
	if err != nil {
		return nil, err
	}
	if len(tag) == 0 || (tag[0] != 0 && tag[0] != 10) {
		return nil, fmt.Errorf("block entity data is not optional compound NBT")
	}
	if tag[0] == 0 {
		if len(tag) != 1 {
			return nil, fmt.Errorf("trailing optional block entity NBT bytes")
		}
	} else {
		nbtReader := bytes.NewReader(tag)
		if _, err := nbt.ReadUnnamed(nbtReader); err != nil {
			return nil, err
		}
		if nbtReader.Len() != 0 {
			return nil, fmt.Errorf("trailing block entity NBT bytes")
		}
	}
	b.Raw(tag)
	return b.Bytes(), nil
}

func (p *javaProfile) projectLevelParticles776(data []byte) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	for range 2 {
		if _, err := copyJavaBool(r, &b); err != nil {
			return nil, err
		}
	}
	if err := copyJavaBytes(r, &b, 44); err != nil {
		return nil, err
	}
	if err := p.projectParticleValue(r, &b); err != nil {
		return nil, err
	}
	if err := requireJavaEnd(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *javaProfile) projectExplosion776(data []byte) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	// Both live 26.1 and 26.2 captures carry center, power, affected-block
	// count, optional knockback, primary particle/sound and a weighted list.
	if err := copyJavaBytes(r, &b, 32); err != nil {
		return nil, err
	}
	hasKnockback, err := copyJavaBool(r, &b)
	if err != nil {
		return nil, err
	}
	if hasKnockback {
		if err := copyJavaBytes(r, &b, 24); err != nil {
			return nil, err
		}
	}
	if err := p.projectParticleValue(r, &b); err != nil {
		return nil, fmt.Errorf("explosion primary particle: %v", err)
	}
	if err := p.projectSoundHolder776(r, &b); err != nil {
		return nil, fmt.Errorf("explosion sound: %v", err)
	}
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || int64(count) > int64(r.Remaining())/10 {
		return nil, fmt.Errorf("invalid weighted explosion particle count %d", count)
	}
	b.VarInt(count)
	for range count {
		if err := p.projectParticleValue(r, &b); err != nil {
			return nil, fmt.Errorf("weighted explosion particle: %v", err)
		}
		if err := copyJavaBytes(r, &b, 8); err != nil {
			return nil, err
		}
		weight, err := copyJavaVarInt(r, &b)
		if err != nil {
			return nil, err
		}
		if weight < 1 {
			return nil, fmt.Errorf("invalid explosion particle weight %d", weight)
		}
	}
	if err := requireJavaEnd(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *javaProfile) projectParticleValue(r *protocol.PacketReader, b *protocol.Buffer) error {
	id, err := r.VarInt()
	if err != nil {
		return err
	}
	domain := builtinDomain(776, "minecraft:particle_type")
	if id < 0 || int(id) >= len(domain.names) {
		return fmt.Errorf("invalid canonical particle ID %d", id)
	}
	wire, err := p.projectCosmeticID("minecraft:particle_type", id)
	if err != nil {
		return err
	}
	b.VarInt(wire)
	switch domain.names[id] {
	case "minecraft:block", "minecraft:block_marker", "minecraft:falling_dust", "minecraft:dust_pillar", "minecraft:block_crumble":
		source, err := r.VarInt()
		if err != nil {
			return err
		}
		base, _ := javaProfileFor(776)
		canonical, ok := base.blocks.FromWire(source)
		if !ok {
			return fmt.Errorf("unknown canonical particle block state %d", source)
		}
		target, err := p.blocks.Encode(canonical)
		if err != nil {
			return err
		}
		b.VarInt(target)
		return nil
	case "minecraft:item":
		return p.projectParticleItemFrom776(r, b)
	case "minecraft:dust":
		return copyJavaBytes(r, b, 8) // Int ARGB + Float scale, both live profiles.
	case "minecraft:effect", "minecraft:instant_effect":
		return copyJavaBytes(r, b, 8) // Int color + Float power, both live profiles.
	case "minecraft:dust_color_transition":
		return copyJavaBytes(r, b, 12) // Two Int ARGB values + Float scale.
	case "minecraft:entity_effect", "minecraft:tinted_leaves", "minecraft:flash", "minecraft:sculk_charge", "minecraft:dragon_breath":
		return copyJavaBytes(r, b, 4)
	case "minecraft:shriek":
		_, err := copyJavaVarInt(r, b)
		return err
	case "minecraft:trail":
		if err := copyJavaBytes(r, b, 28); err != nil {
			return err
		}
		_, err := copyJavaVarInt(r, b)
		return err
	case "minecraft:vibration":
		source, err := r.VarInt()
		if err != nil {
			return err
		}
		name := builtinDomain(776, "minecraft:position_source_type").names
		if source < 0 || int(source) >= len(name) {
			return fmt.Errorf("unknown vibration position source %d", source)
		}
		target, err := p.projectBuiltinRegistryID("minecraft:position_source_type", source)
		if err != nil {
			return err
		}
		b.VarInt(target)
		switch name[source] {
		case "minecraft:block":
			if err := copyJavaBytes(r, b, 8); err != nil {
				return err
			}
		case "minecraft:entity":
			if _, err := copyJavaVarInt(r, b); err != nil {
				return err
			}
			if err := copyJavaBytes(r, b, 4); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unmeasured vibration position source %s", name[source])
		}
		_, err = copyJavaVarInt(r, b)
		return err
	default:
		return nil // The remaining measured built-in options are data-less.
	}
}

func (p *javaProfile) projectBlockEvent776(data []byte) ([]byte, error) {
	r := protocol.NewPacketReader(data)
	var b protocol.Buffer
	if err := copyJavaBytes(r, &b, 10); err != nil {
		return nil, err
	}
	id, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	wire, err := p.projectBuiltinRegistryID("minecraft:block", id)
	if err != nil {
		return nil, err
	}
	b.VarInt(wire)
	if err := requireJavaEnd(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
