package server

// PotionContents payload facts were measured with JDK25 javap on the pinned
// 26.2 jar. Extra fields use canonical encoded NBT so item equality remains by value.
import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"strings"
)

func potionContentsValue(it ItemStack) map[string]any {
	m := map[string]any{}
	if it.PotionExtra != "" {
		if raw, e := base64.StdEncoding.DecodeString(it.PotionExtra); e == nil {
			if v, e := nbt.ReadUnnamed(bytes.NewReader(raw)); e == nil {
				if values, ok := v.(map[string]any); ok {
					m = values
				}
			}
		}
	}
	if it.Potion != "" {
		m["potion"] = "minecraft:" + it.Potion
	}
	// Details' optional show_icon encoder always emits a present value,
	// including where it equals the show_particles-dependent read default.
	if effects, ok := m["custom_effects"].(nbt.List); ok {
		for _, value := range effects.Items {
			if details, ok := value.(map[string]any); ok {
				potionIconsNBT(details)
			}
		}
	}
	return m
}
func potionBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
		n, _ := nbtNumber(v)
		return n != 0
	}
	return def
}
func potionEffectDetails(m map[string]any, depth int) (map[string]any, error) {
	if depth > 16 {
		return nil, fmt.Errorf("potion effect nesting")
	}
	out := map[string]any{}
	for _, key := range []string{"amplifier", "duration"} {
		if v, ok := m[key]; ok {
			if _, numeric := nbtNumber(v); !numeric {
				return nil, fmt.Errorf("invalid potion %s", key)
			}
		}
	}
	for _, key := range []string{"ambient", "show_particles", "show_icon"} {
		if v, ok := m[key]; ok {
			if _, boolean := v.(bool); !boolean {
				if _, numeric := nbtNumber(v); !numeric {
					return nil, fmt.Errorf("invalid potion %s", key)
				}
			}
		}
	}
	amp := potionAmplifier(m["amplifier"])
	dur := nbtNumberOr(m["duration"], 0)
	if dur < -1 {
		return nil, fmt.Errorf("invalid potion effect")
	}
	if amp != 0 {
		out["amplifier"] = int8(amp)
	}
	if dur != 0 {
		out["duration"] = dur
	}
	if potionBool(m, "ambient", false) {
		out["ambient"] = int8(1)
	}
	visible := potionBool(m, "show_particles", true)
	if !visible {
		out["show_particles"] = int8(0)
	}
	icon := potionBool(m, "show_icon", visible)
	if icon != visible {
		if icon {
			out["show_icon"] = int8(1)
		} else {
			out["show_icon"] = int8(0)
		}
	}
	if h, ok := m["hidden_effect"]; ok {
		v, ok := h.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid hidden potion effect")
		}
		v, e := potionEffectDetails(v, depth+1)
		if e != nil {
			return nil, e
		}
		out["hidden_effect"] = v
	}
	return out, nil
}
func setPotionContents(it *ItemStack, v any) error {
	if name, ok := v.(string); ok {
		v = map[string]any{"potion": name}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid potion contents")
	}
	potion := ""
	out := map[string]any{}
	if v, ok := m["potion"]; ok {
		name, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid potion")
		}
		potion = strings.TrimPrefix(name, "minecraft:")
		if _, ok := potionNamesByIDReverse(potion); !ok {
			return fmt.Errorf("unknown potion")
		}
	}
	if v, ok := m["custom_color"]; ok {
		n, ok := nbtNumber(v)
		if !ok || n < -2147483648 || n > 2147483647 {
			return fmt.Errorf("invalid potion color")
		}
		out["custom_color"] = int32(n)
	}
	if v, ok := m["custom_name"]; ok {
		name, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid potion name")
		}
		out["custom_name"] = name
	}
	if v, ok := m["custom_effects"]; ok {
		list, ok := v.(nbt.List)
		if !ok || len(list.Items) > 256 {
			return fmt.Errorf("invalid potion effects")
		}
		effects := nbt.List{Elem: 10}
		for _, item := range list.Items {
			effect, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid potion effect")
			}
			name, ok := effect["id"].(string)
			if !ok {
				return fmt.Errorf("missing potion effect id")
			}
			name = strings.TrimPrefix(name, "minecraft:")
			if _, ok := MobEffectID(name); !ok {
				return fmt.Errorf("unknown potion effect")
			}
			details, e := potionEffectDetails(effect, 0)
			if e != nil {
				return e
			}
			details["id"] = "minecraft:" + name
			effects.Items = append(effects.Items, details)
		}
		if len(effects.Items) > 0 {
			out["custom_effects"] = effects
		}
	}
	it.Potion = potion
	it.PotionExtra = ""
	if len(out) > 0 {
		var raw bytes.Buffer
		if e := nbt.WriteUnnamed(&raw, out); e != nil {
			return e
		}
		it.PotionExtra = base64.StdEncoding.EncodeToString(raw.Bytes())
	}
	return nil
}
func potionNamesByIDReverse(name string) (int32, bool) {
	for id, n := range potionNamesByID {
		if n == name {
			return id, true
		}
	}
	return 0, false
}
func writePotionDetails(b *protocol.Buffer, m map[string]any) {
	b.VarInt(potionAmplifier(m["amplifier"]))
	b.VarInt(nbtNumberOr(m["duration"], 0))
	b.Bool(potionBool(m, "ambient", false))
	visible := potionBool(m, "show_particles", true)
	b.Bool(visible)
	b.Bool(potionBool(m, "show_icon", visible))
	h, ok := m["hidden_effect"].(map[string]any)
	b.Bool(ok)
	if ok {
		writePotionDetails(b, h)
	}
}
func writePotionContents(b *protocol.Buffer, it ItemStack) {
	m := potionContentsValue(it)
	pid, ok := potionNamesByIDReverse(it.Potion)
	b.Bool(ok)
	if ok {
		b.VarInt(pid)
	}
	color, present := m["custom_color"]
	b.Bool(present)
	if present {
		b.Int(nbtNumberOr(color, 0))
	}
	effects, _ := m["custom_effects"].(nbt.List)
	b.VarInt(int32(len(effects.Items)))
	for _, v := range effects.Items {
		e := v.(map[string]any)
		id, _ := MobEffectID(strings.TrimPrefix(e["id"].(string), "minecraft:"))
		b.VarInt(id)
		writePotionDetails(b, e)
	}
	name, present := m["custom_name"].(string)
	b.Bool(present)
	if present {
		b.String(name)
	}
}
func readPotionDetails(r *protocol.PacketReader, depth int) (map[string]any, error) {
	if depth > 16 {
		return nil, fmt.Errorf("potion effect nesting")
	}
	m := map[string]any{}
	amp, e := r.VarInt()
	if e != nil {
		return nil, e
	}
	dur, e := r.VarInt()
	if e != nil {
		return nil, e
	}
	m["amplifier"], m["duration"] = min(max(amp, 0), 255), dur
	for _, key := range []string{"ambient", "show_particles", "show_icon"} {
		v, e := slotBool(r)
		if e != nil {
			return nil, e
		}
		if v {
			m[key] = int8(1)
		} else {
			m[key] = int8(0)
		}
	}
	hidden, e := slotBool(r)
	if e != nil {
		return nil, e
	}
	if hidden {
		h, e := readPotionDetails(r, depth+1)
		if e != nil {
			return nil, e
		}
		m["hidden_effect"] = h
	}
	return m, nil
}
func readPotionContents(r *protocol.PacketReader, it *ItemStack) error {
	m := map[string]any{}
	present, e := slotBool(r)
	if e != nil {
		return e
	}
	if present {
		id, e := r.VarInt()
		if e != nil {
			return e
		}
		name, ok := potionNamesByID[id]
		if !ok {
			return fmt.Errorf("unknown potion")
		}
		m["potion"] = "minecraft:" + name
	}
	present, e = slotBool(r)
	if e != nil {
		return e
	}
	if present {
		color, e := r.Int()
		if e != nil {
			return e
		}
		m["custom_color"] = color
	}
	n, e := slotWireCount(r, 256)
	if e != nil {
		return e
	}
	list := nbt.List{Elem: 10}
	for ; n > 0; n-- {
		id, e := r.VarInt()
		if e != nil {
			return e
		}
		name, ok := MobEffectName(id)
		if !ok {
			return fmt.Errorf("unknown potion effect")
		}
		details, e := readPotionDetails(r, 0)
		if e != nil {
			return e
		}
		details["id"] = "minecraft:" + name
		list.Items = append(list.Items, details)
	}
	m["custom_effects"] = list
	present, e = slotBool(r)
	if e != nil {
		return e
	}
	if present {
		name, e := r.String()
		if e != nil {
			return e
		}
		m["custom_name"] = name
	}
	return setPotionContents(it, m)
}

func potionStackEffects(it ItemStack) []splashEffect {
	out := append([]splashEffect(nil), splashPotionContents[it.Potion]...)
	list, _ := potionContentsValue(it)["custom_effects"].(nbt.List)
	for _, v := range list.Items {
		m := v.(map[string]any)
		name := strings.TrimPrefix(m["id"].(string), "minecraft:")
		flags := byte(8)
		if potionBool(m, "ambient", false) {
			flags |= 1
		}
		visible := potionBool(m, "show_particles", true)
		if visible {
			flags |= 2
		}
		if potionBool(m, "show_icon", visible) {
			flags |= 4
		}
		out = append(out, splashEffect{name: name, dur: nbtNumberOr(m["duration"], 0), amp: potionAmplifier(m["amplifier"]), flags: flags, custom: true})
	}
	for i := range out {
		out[i].scale = float64(potionDurationScale(it))
	}
	return out
}
func projectilePotionStack(e *Entity) ItemStack {
	it := e.Item
	if it.Potion == "" {
		it.Potion = e.splashPotion
	}
	return it
}

// AbstractThrownPotion selects event2007 from the base holder's instant
// effects only; custom instant effects still use the ordinary2002 event.
func potionStackEvent(it ItemStack) int32 { return potionLevelEvent(it.Potion) }

func potionStackColor(it ItemStack) int32 {
	m := potionContentsValue(it)
	if c, ok := m["custom_color"]; ok {
		return nbtNumberOr(c, 0)
	}
	var red, green, blue, weight int32
	for _, e := range potionStackEffects(it) {
		if e.custom && e.flags&2 == 0 {
			continue
		}
		c, w := mobEffectColors[e.name], e.amp+1
		red += ((c >> 16) & 255) * w
		green += ((c >> 8) & 255) * w
		blue += (c & 255) * w
		weight += w
	}
	if weight == 0 {
		return -13083194
	}
	return int32(uint32(0xff000000) | uint32((red/weight)<<16) | uint32((green/weight)<<8) | uint32(blue/weight))
}

// Splash constructs a fresh dose from ambient/visible; the original custom
// show_icon flag belongs to the item but the dose's icon follows visibility.
func potionSplashFlags(e splashEffect) byte {
	flags := e.flags & 9
	if e.flags&2 != 0 {
		flags |= 6
	}
	return flags
}

func potionDurationScale(it ItemStack) float32 {
	if it.PotionScaleSet {
		return it.PotionScale
	}
	if name, _ := itemNameOf(it.ID); name == "lingering_potion" {
		return .25
	}
	return 1
}
func setPotionScale(it *ItemStack, v any) bool {
	var n float64
	switch x := v.(type) {
	case float32:
		n = float64(x)
	case float64:
		n = x
	default:
		k, ok := nbtNumber(v)
		if !ok {
			return false
		}
		n = float64(k)
	}
	if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || math.IsInf(float64(float32(n)), 0) {
		return false
	}
	it.PotionScaleSet = true
	it.PotionScale = float32(n)
	return true
}
func readPotionScale(r *protocol.PacketReader, it *ItemStack) error {
	v, e := r.Float()
	if e != nil {
		return e
	}
	if !setPotionScale(it, v) {
		return fmt.Errorf("invalid potion duration scale")
	}
	return nil
}
func scaledPotionTicks(duration int32, scale float32) int32 {
	if duration <= 0 {
		return duration
	}
	return max(1, potionDurationInt(math.Floor(float64(float32(duration)*scale))))
}

type potionCloudDose struct {
	id, amplifier, ticks int32
	hide                 bool
	flags                byte
	custom               bool
}

// Bottled doses retain list order, including repeated effect IDs. Collapsing
// that list into the cloud's rendering map would lose addEffect precedence.
func potionCloudDoses(e *Entity, now int64) []potionCloudDose {
	var out []potionCloudDose
	if e.CloudPotionContents != "" || e.Item.PotionExtra != "" {
		it := e.Item
		it.Potion = e.CloudPotionContents
		if e.Item.Empty() && !it.PotionScaleSet {
			it.PotionScaleSet = true
			it.PotionScale = .25
		}
		for _, effect := range potionStackEffects(it) {
			id, ok := MobEffectID(effect.name)
			if !ok {
				continue
			}
			flags := byte(14)
			if effect.custom {
				flags = effect.flags
			}
			out = append(out, potionCloudDose{id, effect.amp, scaledPotionTicks(effect.dur, potionDurationScale(it)), flags&2 == 0, flags, effect.custom})
		}
		return out
	}
	for id, inst := range e.Effects {
		out = append(out, potionCloudDose{id, inst.amplifier, potionCloudTicks(e, id, inst, now), inst.hide, inst.flags, inst.flagsSet})
	}
	return out
}

func setCloudPotionContents(e *Entity, contents any, scale float32, now int64) {
	it := ItemStack{PotionScaleSet: true, PotionScale: scale}
	if setPotionContents(&it, contents) != nil {
		return
	}
	e.Item = it
	e.CloudPotionContents = it.Potion
	e.Effects = map[int32]mobEffectInst{}
	for _, effect := range potionStackEffects(it) {
		id, ok := MobEffectID(effect.name)
		if !ok {
			continue
		}
		flags := byte(14)
		if effect.custom {
			flags = effect.flags
		}
		expiry := int64(-1)
		if effect.dur >= 0 {
			expiry = now + int64(effect.dur)
		}
		e.Effects[id] = mobEffectInst{amplifier: effect.amp, expiry: expiry, born: now, hide: flags&2 == 0, flags: flags, flagsSet: true}
	}
}

// JVM f2i/d2i saturate rather than wrapping when a valid duration scale
// exceeds the signed duration range.
func potionDurationInt(value float64) int32 {
	if math.IsNaN(value) {
		return 0
	}
	if value >= 2147483647 {
		return 2147483647
	}
	if value <= -2147483648 {
		return -2147483648
	}
	return int32(value)
}

func potionStoredFlags(inst mobEffectInst) byte {
	if inst.flagsSet || inst.flags != 0 {
		return inst.flags
	}
	return mobEffectFlags(inst.hide)
}

// MobEffectInstance.update replaces visibility/icon even when a weaker dose
// leaves amplitude/duration alone. Ambient can be cleared by that declined
// dose, but can only become true when the scalar instance changes.
func potionMergeFlags(inst *mobEffectInst, incoming byte, applied bool, amplifier, ticks int32, now int64) bool {
	previous := potionStoredFlags(*inst)
	flags := incoming
	if !applied {
		flags = (incoming & 6) | (previous & 1)
		if incoming&1 == 0 {
			flags &^= 1
		}
	}
	if applied {
		flags = (flags &^ 8) | (previous & 8)
	}
	inst.flags = flags
	inst.flagsSet = true
	inst.hide = flags&2 == 0
	expiry := effectExpiry(now, ticks)
	for hidden := inst.hidden; hidden != nil; hidden = hidden.hidden {
		if hidden.amplifier == amplifier && hidden.expiry == expiry {
			hidden.flags = incoming
			hidden.flagsSet = true
			hidden.hide = incoming&2 == 0
		}
	}
	return previous&7 != flags&7
}
func (s *Server) potionMobFlags(e *Entity, id, amp, ticks int32, flags byte, applied bool) {
	inst, ok := e.Effects[id]
	if !ok {
		return
	}
	changed := potionMergeFlags(&inst, flags, applied, amp, ticks, s.clock.count)
	e.Effects[id] = inst
	if changed {
		e.MetaDirty = true
	}
}
func (s *Server) potionPlayerFlags(sess *Session, id, amp, ticks int32, flags byte, applied bool) bool {
	sess.effMu.Lock()
	inst, ok := sess.effects[id]
	changed := false
	if ok {
		changed = potionMergeFlags(&inst, flags, applied, amp, ticks, s.clock.count)
		sess.effects[id] = inst
	}
	sess.effMu.Unlock()
	if changed && !applied {
		s.sendMobEffectUpdate(sess, id, inst)
		s.fireEffectsChanged(sess)
	}
	return changed
}

func potionIconsNBT(m map[string]any) {
	icon := potionBool(m, "show_icon", potionBool(m, "show_particles", true))
	m["show_icon"] = int8(0)
	if icon {
		m["show_icon"] = int8(1)
	}
	if hidden, ok := m["hidden_effect"].(map[string]any); ok {
		potionIconsNBT(hidden)
	}
}

// ExtraCodecs.UNSIGNED_BYTE decodes the NBT byte modulo256; the network
// MobEffectInstance constructor instead clamps its raw VarInt to0..255.
func potionAmplifier(value any) int32 {
	switch number := value.(type) {
	case float32:
		return int32(uint8(potionDurationInt(float64(number))))
	case float64:
		return int32(uint8(potionDurationInt(number)))
	default:
		n, _ := nbtNumber(value)
		return int32(uint8(n))
	}
}
