package server

import (
	"fmt"
	"io"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

const (
	slotItemDomain = iota
	slotComponentDomain
	slotPotionDomain
	slotEffectDomain
	slotEntityDomain
	slotBuiltinDomainCount
)
const (
	slotEnchantmentRegistry = iota
	slotTrimMaterialRegistry
	slotTrimPatternRegistry
	slotBannerPatternRegistry
	slotInstrumentRegistry
	slotSyncedRegistryCount
)

type slotRegistryProjection struct{ from776, to776 []int32 }
type slotDialect struct {
	protocol int32
	builtin  [slotBuiltinDomainCount]*javaBuiltinDomain
	synced   [slotSyncedRegistryCount]slotRegistryProjection
}

var slotDialects sync.Map

func slotDialectFor(p *javaProfile) (*slotDialect, error) {
	if value, ok := slotDialects.Load(p.version.Protocol); ok {
		return value.(*slotDialect), nil
	}
	config := p.config
	if config == nil {
		var err error
		config, err = javaConfigFor(p.version.Protocol)
		if err != nil {
			return nil, err
		}
	}
	d := &slotDialect{protocol: p.version.Protocol}
	for i, name := range []string{"minecraft:item", "minecraft:data_component_type", "minecraft:potion", "minecraft:mob_effect", "minecraft:entity_type"} {
		d.builtin[i] = builtinDomain(p.version.Protocol, name)
		if d.builtin[i] == nil {
			return nil, fmt.Errorf("slot: missing builtin registry %s for%d", name, p.version.Protocol)
		}
	}
	for i, name := range []string{"minecraft:enchantment", "minecraft:trim_material", "minecraft:trim_pattern", "minecraft:banner_pattern", "minecraft:instrument"} {
		d.synced[i] = slotRegistryProjection{from776: config.toWire[name], to776: config.fromWire[name]}
	}
	actual, _ := slotDialects.LoadOrStore(p.version.Protocol, d)
	return actual.(*slotDialect), nil
}
func slotMapID(from, to []int32, wire int32) (int32, error) {
	if wire < 0 || int64(wire) >= int64(len(from)) {
		return -1, fmt.Errorf("slot: unknown source registry ID %d", wire)
	}
	canonical := from[wire]
	if canonical < 0 || int64(canonical) >= int64(len(to)) || to[canonical] < 0 {
		return -1, fmt.Errorf("slot: registry entry %d has no target representation", wire)
	}
	return to[canonical], nil
}
func (s *slotDialect) projectBuiltin(target *slotDialect, domain int, wire int32) (int32, error) {
	return slotMapID(s.builtin[domain].to776, target.builtin[domain].from776, wire)
}
func (s *slotDialect) projectSynced(target *slotDialect, domain int, wire int32) (int32, error) {
	return slotMapID(s.synced[domain].to776, target.synced[domain].from776, wire)
}

type slotProjector struct {
	source, target *slotDialect
	nodes          int
}

func (t *slotProjector) enter(depth int) error {
	t.nodes++
	if depth > 16 || t.nodes > 4096 {
		return fmt.Errorf("slot: nesting or node budget exceeded")
	}
	return nil
}
func slotWireCount(r *protocol.PacketReader, max int32) (int32, error) {
	n, err := r.VarInt()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > max {
		return 0, fmt.Errorf("slot: count %d outside0..%d", n, max)
	}
	return n, nil
}
func slotBool(r *protocol.PacketReader) (bool, error) {
	b, err := r.Byte()
	if err != nil {
		return false, err
	}
	if b > 1 {
		return false, fmt.Errorf("slot: invalid boolean %d", b)
	}
	return b == 1, nil
}
func (t *slotProjector) slot(r *protocol.PacketReader, b *protocol.Buffer, depth int, untrusted bool) error {
	if err := t.enter(depth); err != nil {
		return err
	}
	count, err := slotWireCount(r, 127)
	if err != nil {
		return err
	}
	b.VarInt(count)
	if count == 0 {
		return nil
	}
	return t.stackTail(r, b, count, depth, untrusted, false)
}
func (t *slotProjector) template(r *protocol.PacketReader, b *protocol.Buffer, depth int) error {
	if err := t.enter(depth); err != nil {
		return err
	}
	return t.stackTail(r, b, 0, depth, false, true)
}
func (t *slotProjector) stackTail(r *protocol.PacketReader, b *protocol.Buffer, count int32, depth int, untrusted, template bool) error {
	sourceID, err := r.VarInt()
	if err != nil {
		return err
	}
	targetID, err := t.source.projectBuiltin(t.target, slotItemDomain, sourceID)
	if err != nil {
		return err
	}
	b.VarInt(targetID)
	if template {
		count, err = slotWireCount(r, 127)
		if err != nil || count == 0 {
			if err != nil {
				return err
			}
			return fmt.Errorf("slot: empty item template")
		}
		b.VarInt(count)
	}
	add, err := slotWireCount(r, 256)
	if err != nil {
		return err
	}
	remove, err := slotWireCount(r, 256)
	if err != nil {
		return err
	}
	b.VarInt(add)
	b.VarInt(remove)
	seen := map[int32]bool{}
	for i := int32(0); i < add; i++ {
		wireType, err := r.VarInt()
		if err != nil {
			return err
		}
		domain := t.source.builtin[slotComponentDomain]
		if wireType < 0 || int64(wireType) >= int64(len(domain.to776)) || domain.to776[wireType] < 0 {
			return fmt.Errorf("slot: unknown component ID %d", wireType)
		}
		canonicalType := domain.to776[wireType]
		if seen[canonicalType] {
			return fmt.Errorf("slot: duplicate component %d", canonicalType)
		}
		seen[canonicalType] = true
		targetType, err := t.source.projectBuiltin(t.target, slotComponentDomain, wireType)
		if err != nil {
			return err
		}
		b.VarInt(targetType)
		payload := r
		if untrusted {
			size, err := slotWireCount(r, 1<<20)
			if err != nil {
				return err
			}
			if int64(size) > int64(r.Remaining()) {
				return fmt.Errorf("slot: component payload exceeds remaining packet")
			}
			raw, err := r.Read(int(size))
			if err != nil {
				return err
			}
			payload = protocol.NewPacketReader(raw)
		}
		if err = t.component(payload, b, canonicalType, depth); err != nil {
			return err
		}
		if untrusted && payload.Remaining() != 0 {
			return fmt.Errorf("slot: trailing untrusted component bytes")
		}
	}
	for i := int32(0); i < remove; i++ {
		wireType, err := r.VarInt()
		if err != nil {
			return err
		}
		targetType, err := t.source.projectBuiltin(t.target, slotComponentDomain, wireType)
		if err != nil {
			return err
		}
		canonical := t.source.builtin[slotComponentDomain].to776[wireType]
		if seen[canonical] {
			return fmt.Errorf("slot: duplicate or both added/removed component %d", canonical)
		}
		seen[canonical] = true
		b.VarInt(targetType)
	}
	return nil
}
func (p *javaProfile) projectSlotFrom776(r *protocol.PacketReader, b *protocol.Buffer) error {
	sourceProfile, _ := javaProfileFor(776)
	source, err := slotDialectFor(sourceProfile)
	if err != nil {
		return err
	}
	target, err := slotDialectFor(p)
	if err != nil {
		return err
	}
	t := slotProjector{source: source, target: target}
	return t.slot(r, b, 0, false)
}
func (p *javaProfile) projectParticleItemFrom776(r *protocol.PacketReader, b *protocol.Buffer) error {
	sourceProfile, _ := javaProfileFor(776)
	source, err := slotDialectFor(sourceProfile)
	if err != nil {
		return err
	}
	target, err := slotDialectFor(p)
	if err != nil {
		return err
	}
	t := slotProjector{source: source, target: target}
	return t.template(r, b, 0)
}

// writeSlot is the target-profile boundary. Plain slots avoid scratch buffers;
// component-bearing stacks use the canonical writer and a shape-aware projector.
func (p *javaProfile) writeSlot(b *protocol.Buffer, it ItemStack) error {
	if it.Empty() {
		b.VarInt(0)
		return nil
	}
	dialect, err := slotDialectFor(p)
	if err != nil {
		return err
	}
	if it.Count < 1 || it.Count > 127 {
		return fmt.Errorf("slot: invalid stack count %d", it.Count)
	}
	plain := it
	plain.ID = 0
	plain.Count = 0
	if plain == (ItemStack{}) {
		canonical := dialect.builtin[slotItemDomain]
		if it.ID < 0 || int64(it.ID) >= int64(len(canonical.from776)) || canonical.from776[it.ID] < 0 {
			return fmt.Errorf("slot: item %d absent from target%d", it.ID, p.version.Protocol)
		}
		b.VarInt(it.Count)
		b.VarInt(canonical.from776[it.ID])
		b.VarInt(0)
		b.VarInt(0)
		return nil
	}
	if it.InstrumentRemoved && it.Instrument != "" {
		return fmt.Errorf("slot: instrument both added and removed")
	}
	if err := validateSlotName(it); err != nil {
		return err
	}
	if it.Contents != nil {
		nodes := 0
		if err := validateSlotContents(it, 0, &nodes, map[*[shulkerSlots]ItemStack]bool{}); err != nil {
			return err
		}
	}
	var canonical protocol.Buffer
	writeSlot(&canonical, it)
	return p.projectSlotFrom776(protocol.NewPacketReader(canonical.Bytes()), b)
}
func (sess *Session) writeSlot(b *protocol.Buffer, it ItemStack) {
	profile, err := sess.javaProfile()
	if err == nil {
		err = profile.writeSlot(b, it)
	}
	// A builder may contain a partial slot after failure, but no packet can be
	// emitted: closing the queue makes every subsequent sess.write reject.
	if err != nil {
		sess.closeQueue()
	}
}
func (p *javaProfile) readSlot(r *protocol.PacketReader) (ItemStack, error) {
	return p.readSlotInput(r, false)
}
func (p *javaProfile) readUntrustedSlot(r *protocol.PacketReader) (ItemStack, error) {
	return p.readSlotInput(r, true)
}
func (p *javaProfile) readSlotInput(r *protocol.PacketReader, untrusted bool) (ItemStack, error) {
	source, err := slotDialectFor(p)
	if err != nil {
		return ItemStack{}, err
	}
	canonicalProfile, _ := javaProfileFor(776)
	target, err := slotDialectFor(canonicalProfile)
	if err != nil {
		return ItemStack{}, err
	}
	var output protocol.Buffer
	t := slotProjector{source: source, target: target}
	if err = t.slot(r, &output, 0, untrusted); err != nil {
		return ItemStack{}, err
	}
	decoded := protocol.NewPacketReader(output.Bytes())
	value, err := readCanonicalSlot(decoded, 0)
	if err == nil && decoded.Remaining() != 0 {
		return ItemStack{}, fmt.Errorf("slot: trailing canonical bytes")
	}
	return value, err
}

func (p *javaProfile) readHashedSlot(r *protocol.PacketReader) (ItemStack, bool) {
	present, err := slotBool(r)
	if err != nil {
		return ItemStack{}, false
	}
	if !present {
		return ItemStack{}, true
	}
	source, err := slotDialectFor(p)
	if err != nil {
		return ItemStack{}, false
	}
	id, err := r.VarInt()
	if err != nil || id < 0 || int64(id) >= int64(len(source.builtin[slotItemDomain].to776)) {
		return ItemStack{}, false
	}
	canonical := source.builtin[slotItemDomain].to776[id]
	if canonical < 0 {
		return ItemStack{}, false
	}
	count, err := slotWireCount(r, 127)
	if err != nil || count == 0 {
		return ItemStack{}, false
	}
	added, err := slotWireCount(r, 256)
	if err != nil {
		return ItemStack{}, false
	}
	seen := map[int32]bool{}
	for i := int32(0); i < added; i++ {
		typ, e := r.VarInt()
		if e != nil || typ < 0 || int64(typ) >= int64(len(source.builtin[slotComponentDomain].to776)) {
			return ItemStack{}, false
		}
		component := source.builtin[slotComponentDomain].to776[typ]
		if component < 0 || seen[component] {
			return ItemStack{}, false
		}
		seen[component] = true
		if _, e = r.Int(); e != nil {
			return ItemStack{}, false
		}
	}
	removed, err := slotWireCount(r, 256)
	if err != nil {
		return ItemStack{}, false
	}
	for i := int32(0); i < removed; i++ {
		typ, e := r.VarInt()
		if e != nil || typ < 0 || int64(typ) >= int64(len(source.builtin[slotComponentDomain].to776)) {
			return ItemStack{}, false
		}
		component := source.builtin[slotComponentDomain].to776[typ]
		if component < 0 || seen[component] {
			return ItemStack{}, false
		}
		seen[component] = true
	}
	return ItemStack{ID: canonical, Count: count}, true
}
func (sess *Session) readHashedSlot(r *protocol.PacketReader) (ItemStack, bool) {
	p, err := sess.javaProfile()
	if err != nil {
		return ItemStack{}, false
	}
	return p.readHashedSlot(r)
}
func (sess *Session) readChangedSlots(r *protocol.PacketReader) (int, bool) {
	n, err := slotWireCount(r, 128)
	if err != nil {
		return 0, false
	}
	for i := int32(0); i < n; i++ {
		if _, err = r.Short(); err != nil {
			return 0, false
		}
		if _, ok := sess.readHashedSlot(r); !ok {
			return 0, false
		}
	}
	return int(n), true
}

// projectEquipment776 keeps entry order and continuation bits; every embedded
// slot is parsed before its registry IDs are changed. Session.write owns this
// projection once, while inventory/menu packets are already profile-encoded.
func (p *javaProfile) projectEquipment776(body []byte) ([]byte, error) {
	r := protocol.NewPacketReader(body)
	var b protocol.Buffer
	entity, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	b.VarInt(entity)
	seen := map[byte]bool{}
	for entries := 0; entries < 8; entries++ {
		slot, err := r.Byte()
		if err != nil {
			return nil, err
		}
		ordinal := slot & 127
		if ordinal > 7 || seen[ordinal] {
			return nil, fmt.Errorf("slot: invalid/duplicate equipment entry%d", ordinal)
		}
		seen[ordinal] = true
		b.Byte(slot)
		if err = p.projectSlotFrom776(r, &b); err != nil {
			return nil, err
		}
		if slot&128 == 0 {
			if r.Remaining() != 0 {
				return nil, fmt.Errorf("slot: trailing equipment bytes")
			}
			return b.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("slot: equipment continuation limit exceeded")
}

// validateSlotName rejects malformed or oversized Java NBT names before
// the canonical writer, whose historical signature cannot return an error.
func validateSlotName(it ItemStack) error {
	if !it.Empty() && it.Name != "" {
		if err := nbt.WriteUnnamed(io.Discard, it.Name); err != nil {
			return fmt.Errorf("slot: custom name: %w", err)
		}
	}
	return nil
}

func validateSlotContents(it ItemStack, depth int, nodes *int, path map[*[shulkerSlots]ItemStack]bool) error {
	*nodes++
	if depth > 16 || *nodes > 4096 {
		return fmt.Errorf("slot: content nesting budget exceeded")
	}
	if err := validateSlotName(it); err != nil {
		return err
	}
	if it.Empty() || it.Contents == nil {
		return nil
	}
	if path[it.Contents] {
		return fmt.Errorf("slot: cyclic container contents")
	}
	path[it.Contents] = true
	defer delete(path, it.Contents)
	for _, child := range it.Contents {
		if err := validateSlotContents(child, depth+1, nodes, path); err != nil {
			return err
		}
	}
	return nil
}

// projectItemTemplateFrom776 is the streaming item-template boundary used by
// advancement icons and particle options. Unlike Slot, item ID precedes count.
func (p *javaProfile) projectItemTemplateFrom776(r *protocol.PacketReader, b *protocol.Buffer) error {
	return p.projectParticleItemFrom776(r, b)
}
