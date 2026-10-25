package server

import (
	"fmt"

	"mc-go/internal/protocol"
)

func (t *slotProjector) component(r *protocol.PacketReader, b *protocol.Buffer, typ int32, depth int) error {
	integer := func() error {
		v, e := r.VarInt()
		if e != nil {
			return e
		}
		b.VarInt(v)
		return nil
	}
	fixed := func(n int) error {
		raw, e := r.Read(n)
		if e != nil {
			return e
		}
		b.Raw(raw)
		return nil
	}
	text := func() error {
		s, e := r.String()
		if e != nil {
			return e
		}
		b.String(s)
		return nil
	}
	boolean := func() error {
		v, e := slotBool(r)
		if e != nil {
			return e
		}
		b.Bool(v)
		return nil
	}
	mapBuiltin := func(domain int, holder bool) error {
		id, e := r.VarInt()
		if e != nil {
			return e
		}
		if holder {
			if id == 0 {
				return fmt.Errorf("slot: inline registry holder unsupported")
			}
			id--
		}
		projected, e := t.source.projectBuiltin(t.target, domain, id)
		if e != nil {
			return e
		}
		if holder {
			projected++
		}
		b.VarInt(projected)
		return nil
	}
	mapSynced := func(domain int, holder bool) error {
		id, e := r.VarInt()
		if e != nil {
			return e
		}
		if holder {
			if id == 0 {
				return fmt.Errorf("slot: inline registry holder unsupported")
			}
			id--
		}
		projected, e := t.source.projectSynced(t.target, domain, id)
		if e != nil {
			return e
		}
		if holder {
			projected++
		}
		b.VarInt(projected)
		return nil
	}
	list := func(max int32, entry func() error) error {
		n, e := slotWireCount(r, max)
		if e != nil {
			return e
		}
		b.VarInt(n)
		for i := int32(0); i < n; i++ {
			if e = entry(); e != nil {
				return e
			}
		}
		return nil
	}
	switch typ {
	case compDamage, compRepairCost, compBaseColor, compOminousBottleAmplifier, compMapID, compMapPost, compDye, compRarity:
		return integer()
	case compDyedColor:
		return fixed(4)
	case compCustomName, compItemName:
		return t.nbt(r, b, false, depth+1)
	case compEnchantments, compStoredEnch:
		return list(256, func() error {
			if e := mapSynced(slotEnchantmentRegistry, false); e != nil {
				return e
			}
			return integer()
		})
	case compPotionContents:
		present, e := slotBool(r)
		if e != nil {
			return e
		}
		b.Bool(present)
		if present {
			if e = mapBuiltin(slotPotionDomain, false); e != nil {
				return e
			}
		}
		color, e := slotBool(r)
		if e != nil {
			return e
		}
		b.Bool(color)
		if color {
			if e = fixed(4); e != nil {
				return e
			}
		}
		if e = list(256, func() error {
			if e := mapBuiltin(slotEffectDomain, false); e != nil {
				return e
			}
			return t.effectDetails(r, b, depth+1)
		}); e != nil {
			return e
		}
		name, e := slotBool(r)
		if e != nil {
			return e
		}
		b.Bool(name)
		if name {
			return fmt.Errorf("slot: custom potion name is outside the modeled component subset")
		}
		return nil
	case compStewEffects:
		return list(256, func() error {
			if e := mapBuiltin(slotEffectDomain, false); e != nil {
				return e
			}
			return integer()
		})
	case compInstrument:
		// Actual775/776 nondefault sing-horn captures encode holder7 alone:
		// registry id6 +1, with no representation-presence wrapper.
		return mapSynced(slotInstrumentRegistry, true)
	case compPotDeco:
		return list(4, func() error { return mapBuiltin(slotItemDomain, false) })
	case compTooltipDisplay:
		if e := boolean(); e != nil {
			return e
		}
		return list(256, func() error { return mapBuiltin(slotComponentDomain, false) })
	case compBannerPatterns:
		return list(1024, func() error {
			if e := mapSynced(slotBannerPatternRegistry, true); e != nil {
				return e
			}
			return integer()
		})
	case compTrim:
		if e := mapSynced(slotTrimMaterialRegistry, true); e != nil {
			return e
		}
		return mapSynced(slotTrimPatternRegistry, true)
	case compWritableBook:
		return list(100, func() error {
			if e := text(); e != nil {
				return e
			}
			filtered, e := slotBool(r)
			if e != nil {
				return e
			}
			b.Bool(filtered)
			if filtered {
				return text()
			}
			return nil
		})
	case compWrittenBook:
		if e := text(); e != nil {
			return e
		}
		filtered, e := slotBool(r)
		if e != nil {
			return e
		}
		b.Bool(filtered)
		if filtered {
			if e = text(); e != nil {
				return e
			}
		}
		if e = text(); e != nil {
			return e
		}
		if e = integer(); e != nil {
			return e
		}
		if e = list(100, func() error {
			if e := t.nbt(r, b, false, depth+1); e != nil {
				return e
			}
			filtered, e := slotBool(r)
			if e != nil {
				return e
			}
			b.Bool(filtered)
			if filtered {
				return t.nbt(r, b, false, depth+1)
			}
			return nil
		}); e != nil {
			return e
		}
		return boolean()
	case compContainer:
		return list(27, func() error {
			present, e := slotBool(r)
			if e != nil {
				return e
			}
			b.Bool(present)
			if present {
				return t.template(r, b, depth+1)
			}
			return nil
		})
	case compBlockState:
		return list(128, func() error {
			if e := text(); e != nil {
				return e
			}
			return text()
		})
	case compBees:
		return list(1024, func() error { return t.bee(r, b, depth+1) })
	case compFireworkExp:
		return t.firework(r, b)
	case compFireworks:
		if e := integer(); e != nil {
			return e
		}
		return list(256, func() error { return t.firework(r, b) })
	default:
		return errUnknownComponent(typ, -1)
	}
}
func (t *slotProjector) effectDetails(r *protocol.PacketReader, b *protocol.Buffer, depth int) error {
	if err := t.enter(depth); err != nil {
		return err
	}
	for range 2 {
		value, err := r.VarInt()
		if err != nil {
			return err
		}
		b.VarInt(value)
	}
	for range 3 {
		value, err := slotBool(r)
		if err != nil {
			return err
		}
		b.Bool(value)
	}
	hidden, err := slotBool(r)
	if err != nil {
		return err
	}
	b.Bool(hidden)
	if hidden {
		return t.effectDetails(r, b, depth+1)
	}
	return nil
}
func (t *slotProjector) firework(r *protocol.PacketReader, b *protocol.Buffer) error {
	shape, err := slotWireCount(r, 4)
	if err != nil {
		return err
	}
	b.VarInt(shape)
	for range 2 {
		n, err := slotWireCount(r, 256)
		if err != nil {
			return err
		}
		b.VarInt(n)
		for i := int32(0); i < n; i++ {
			color, err := r.Int()
			if err != nil {
				return err
			}
			b.Int(color)
		}
	}
	for range 2 {
		v, err := slotBool(r)
		if err != nil {
			return err
		}
		b.Bool(v)
	}
	return nil
}

// Both official775/776 wire fixtures use TypedEntityData: holder+unnamed
// compound, then hive timers. Public third-party shape tables differed here;
// the live captures are the source of truth.
func (t *slotProjector) bee(r *protocol.PacketReader, b *protocol.Buffer, depth int) error {
	holder, err := r.VarInt()
	if err != nil || holder < 0 {
		return fmt.Errorf("slot: invalid typed bee entity ID")
	}
	projected, err := t.source.projectBuiltin(t.target, slotEntityDomain, holder)
	if err != nil {
		return err
	}
	b.VarInt(projected)
	if err = t.nbt(r, b, false, depth); err != nil {
		return err
	}
	for range 2 {
		value, err := r.VarInt()
		if err != nil {
			return err
		}
		b.VarInt(value)
	}
	return nil
}
