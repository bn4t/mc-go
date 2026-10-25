package server

import (
	"fmt"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Canonical presentation builders retain 776 IDs. Session.write projects the
// complete measured option value once for each viewer. Unsupported option
// shapes fail before broadcasting rather than emitting a truncated packet.
func canonicalParticleCommandOptions(name string, options map[string]*snbtTag) ([]byte, error) {
	var b protocol.Buffer
	number := func(key string) (float64, error) {
		t := options[key]
		if t == nil || t.kind != snbtNumber {
			return 0, fmt.Errorf("expected number %s", key)
		}
		return snbtNum(t), nil
	}
	color := func(key string, vector bool) (int32, error) {
		t := options[key]
		if t != nil && t.kind == snbtNumber {
			return int32(snbtNum(t)), nil
		}
		if vector && t != nil && t.kind == snbtList && len(t.items) == 3 {
			v := uint32(0xff000000)
			for i, c := range t.items {
				if c.kind != snbtNumber || snbtNum(c) < 0 || snbtNum(c) > 1 {
					return 0, fmt.Errorf("invalid color %s", key)
				}
				v |= uint32(snbtNum(c)*255) << uint(16-i*8)
			}
			return int32(v), nil
		}
		return 0, fmt.Errorf("invalid color %s", key)
	}
	vector := func(t *snbtTag) ([3]float64, error) {
		var v [3]float64
		if t == nil || t.kind != snbtList || len(t.items) != 3 {
			return v, fmt.Errorf("expected three coordinates")
		}
		for i, c := range t.items {
			if c.kind != snbtNumber {
				return v, fmt.Errorf("expected coordinate number")
			}
			v[i] = snbtNum(c)
		}
		return v, nil
	}
	switch name {
	case "effect", "instant_effect":
		b.Int(-1)
		b.Float(1) // Live default-option probes on both profiles.
	case "block", "block_marker", "falling_dust", "dust_pillar", "block_crumble":
		t := options["block_state"]
		if t == nil {
			return nil, fmt.Errorf("missing block_state")
		}
		var state world.Block
		var ok bool
		if t.kind == snbtString {
			state, _, ok = parseBlockSpec(t.str)
		} else if t.kind == snbtCompound {
			n := t.vals["Name"]
			props := map[string]string{}
			if ps := t.vals["Properties"]; ps != nil {
				for k, v := range ps.vals {
					props[k] = v.str
				}
			}
			if n != nil {
				state, ok = world.BlockStateOf(strings.TrimPrefix(n.str, "minecraft:"), props)
			}
		}
		if !ok {
			return nil, fmt.Errorf("unknown particle block state")
		}
		p, _ := javaProfileFor(776)
		id, err := p.blocks.Encode(state)
		if err != nil {
			return nil, err
		}
		b.VarInt(id)
	case "dust", "dust_color_transition":
		keys := []string{"color"}
		if name == "dust_color_transition" {
			keys = []string{"from_color", "to_color"}
		}
		for _, key := range keys {
			v, err := color(key, true)
			if err != nil {
				return nil, err
			}
			b.Int(v)
		}
		v, err := number("scale")
		if err != nil {
			return nil, err
		}
		b.Float(float32(v))
	case "entity_effect", "tinted_leaves", "flash":
		v, err := color("color", false)
		if err != nil {
			return nil, err
		}
		b.Int(v)
	case "sculk_charge", "dragon_breath":
		key := "roll"
		if name == "dragon_breath" {
			key = "power"
		}
		v, err := number(key)
		if err != nil {
			return nil, err
		}
		b.Float(float32(v))
	case "shriek":
		v, err := number("delay")
		if err != nil {
			return nil, err
		}
		b.VarInt(int32(v))
	case "trail":
		v, err := vector(options["target"])
		if err != nil {
			return nil, err
		}
		for _, c := range v {
			b.Double(c)
		}
		c, err := color("color", false)
		if err != nil {
			return nil, err
		}
		b.Int(c)
		d, err := number("duration")
		if err != nil {
			return nil, err
		}
		b.VarInt(int32(d))
	case "vibration":
		d := options["destination"]
		if d == nil || d.kind != snbtCompound || d.vals["type"] == nil || d.vals["type"].str != "minecraft:block" {
			return nil, fmt.Errorf("unsupported vibration destination")
		}
		v, err := vector(d.vals["pos"])
		if err != nil {
			return nil, err
		}
		b.VarInt(0)
		b.Position(int32(v[0]), int32(v[1]), int32(v[2]))
		n, err := number("arrival_in_ticks")
		if err != nil {
			return nil, err
		}
		b.VarInt(int32(n))
	case "item":
		t := options["item"]
		if t == nil || t.kind != snbtCompound {
			return nil, fmt.Errorf("unsupported item particle template")
		}
		if t.vals["components"] != nil {
			return nil, fmt.Errorf("unsupported item particle component options")
		}
		m, ok := snbtToValue(t).(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid item particle")
		}
		it := itemFromNBT(m)
		if it.ID <= 0 || it.Count <= 0 {
			return nil, fmt.Errorf("unknown item particle")
		}
		b.VarInt(it.ID)
		b.VarInt(it.Count)
		writeSlotPatch(&b, it)
	default:
		if _, need := particleOptionKeys[name]; need || options != nil {
			return nil, fmt.Errorf("unsupported particle options for %s", name)
		}
	}
	return b.Bytes(), nil
}
