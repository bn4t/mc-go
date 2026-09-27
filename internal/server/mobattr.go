package server

import (
	"math/rand/v2"
	"sort"
	"strings"

	"mc-go/internal/nbt"
)

// mobZombieFamily are the Zombie subclasses that run
// Zombie.handleAttributes at finalizeSpawn: spawn_reinforcements gets
// a fresh base of rand*0.1 and knockback_resistance an add_value
// random_spawn_bonus of rand*0.05. Membership is the supplier itself
// — only the zombie family's table carries spawn_reinforcements.
func mobZombieFamily(name string) bool {
	_, ok := entityAttrBases[name]["spawn_reinforcements"]
	return ok
}

// horseAttrRolls is the per-type randomizeAttributes call set
// AbstractHorse.finalizeSpawn makes — each rolled attribute's instance
// materializes before the pairing burst. Horse rolls all three, the
// chested family (donkey/mule/llama/trader_llama) only max_health,
// skeleton_horse only jump_strength, and zombie_horse jump_strength +
// movement_speed on its own formulas. Camel is not an AbstractHorse —
// vanilla rolls nothing for it.
var horseAttrRolls = map[string][]string{
	"horse":          {"max_health", "jump_strength", "movement_speed"},
	"donkey":         {"max_health"},
	"mule":           {"max_health"},
	"llama":          {"max_health"},
	"trader_llama":   {"max_health"},
	"skeleton_horse": {"jump_strength"},
	"zombie_horse":   {"jump_strength", "movement_speed"},
}

// summonAttrTouches applies the attribute-instance side effects of a
// /summon: vanilla's AttributeMap packs only instances that were
// touched. max_health is touched for tamables (setTame re-asserts the
// wild base) and movement_speed for the zombie family (its setBaby
// path runs outside finalizeSpawn). init is true only when the summon
// carried no NBT argument — vanilla then calls Mob.finalizeSpawn, which
// rolls follow_range's random_spawn_bonus, the zombie family's
// reinforcement/knockback touches, and the equines' three randomized
// attributes. A summon with NBT skips finalizeSpawn entirely. For every
// other living mob, movement_speed materializes lazily on the first
// living tick (the travel read), which only happens inside simulation
// distance.
func summonAttrTouches(e *Entity, name string, init bool) {
	if _, ok := tamableWildHealth[name]; ok {
		e.markAttrTouched("max_health")
	}
	if mobZombieFamily(name) {
		// Zombie's setBaby path (invoked even outside finalizeSpawn)
		// touches movement_speed, so the instance exists at pair time
		// for every summoned member of the family — NBT or not.
		e.markAttrTouched("movement_speed")
	}
	if !init {
		return
	}
	// Mob.finalizeSpawn tail: the 5% left-handed roll.
	if rand.Float64() < 0.05 {
		e.LeftHanded = true
	}
	if _, ok := entityAttrBases[name]["follow_range"]; ok {
		e.markAttrTouched("follow_range")
		// Mob.finalizeSpawn: random_spawn_bonus is a triangle
		// distribution around 0 with deviation 0.11485.
		if e.AttrMods == nil {
			e.AttrMods = map[string]map[string]attrMod{}
		}
		mods := e.AttrMods["follow_range"]
		if mods == nil {
			mods = map[string]attrMod{}
			e.AttrMods["follow_range"] = mods
		}
		mods["minecraft:random_spawn_bonus"] = attrMod{
			value: 0.11485 * (rand.Float64() + rand.Float64() - 1),
			op:    1, // add_multiplied_base
		}
	}
	if mobZombieFamily(name) {
		// Zombie.handleAttributes: spawn_reinforcements base rolls
		// rand*0.1, knockback_resistance takes a rand*0.05 add_value
		// random_spawn_bonus modifier. Neither is client-syncable, so
		// they never appear in update_attributes packets.
		e.markAttrTouched("spawn_reinforcements")
		e.markAttrTouched("knockback_resistance")
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		e.AttrBase["spawn_reinforcements"] = rand.Float64() * 0.1
		if e.AttrMods == nil {
			e.AttrMods = map[string]map[string]attrMod{}
		}
		mods := e.AttrMods["knockback_resistance"]
		if mods == nil {
			mods = map[string]attrMod{}
			e.AttrMods["knockback_resistance"] = mods
		}
		mods["minecraft:random_spawn_bonus"] = attrMod{
			value: rand.Float64() * 0.05,
			op:    0, // add_value
		}
	}
	if rolls, ok := horseAttrRolls[name]; ok {
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		for _, attr := range rolls {
			e.markAttrTouched(attr)
			horseAttrRoll(e, name, attr)
		}
	}
}

// horseAttrRoll applies one attribute's randomizeAttributes formula.
// The spawn health follows a rolled max_health — a horse reporting
// MaxHealth 18 carries Health 18 too.
func horseAttrRoll(e *Entity, name, attr string) {
	switch {
	case attr == "max_health":
		// generateMaxHealth: 15 + nextInt(8) + nextInt(9).
		e.AttrBase[attr] = float64(15 + rand.IntN(8) + rand.IntN(9))
		e.Health = float32(e.AttrBase[attr])
		e.MaxHealth = e.Health
	case attr == "jump_strength" && name == "zombie_horse":
		// generateZombieHorseJumpStrength: 0.5 + 3·rand/15.
		e.AttrBase[attr] = 0.5 + (rand.Float64()+rand.Float64()+rand.Float64())/15
	case attr == "jump_strength":
		// generateJumpStrength: 0.4 + 3·rand·0.2.
		e.AttrBase[attr] = 0.4 + 0.2*(rand.Float64()+rand.Float64()+rand.Float64())
	case attr == "movement_speed" && name == "zombie_horse":
		// generateZombieHorseSpeed: (9 + 3·rand)/42.16.
		e.AttrBase[attr] = (9 + rand.Float64() + rand.Float64() + rand.Float64()) / 42.16
	default: // movement_speed
		// generateSpeed: (0.45 + 3·rand·0.3)·0.25.
		e.AttrBase[attr] = (0.45 + 0.3*(rand.Float64()+rand.Float64()+rand.Float64())) * 0.25
	}
}

// mobAttrSupplierBase is the supplier default for one attribute on
// one type — the value a fresh instance packs before overrides.
func mobAttrSupplierBase(name, attr string) float64 {
	if v, ok := entityAttrBases[name][attr]; ok {
		return v
	}
	if attr == "max_health" {
		return float64(mobHealth[name])
	}
	return livingAttrBases[attr]
}

var attrOpNames = []string{"add_value", "add_multiplied_base", "add_multiplied_total"}

// mobAttrPack serializes the attributes save list — one packed
// instance per touched attribute: {id, base, modifiers?}. Vanilla's
// iteration order is the fastutil identity-hash bucket order, which
// varies run to run; the emitted order here is sorted for stability
// and tests compare the pack as a set.
func mobAttrPack(e *Entity, name string) []any {
	if len(e.AttrTouched) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.AttrTouched))
	for a := range e.AttrTouched {
		names = append(names, a)
	}
	sort.Strings(names)
	var items []any
	for _, a := range names {
		base, has := e.AttrBase[a]
		if !has {
			base = mobAttrSupplierBase(name, a)
		}
		ent := map[string]any{
			"id":   "minecraft:" + a,
			"base": base,
		}
		if mods := e.AttrMods[a]; len(mods) > 0 {
			ids := make([]string, 0, len(mods))
			for id := range mods {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			var list []any
			for _, id := range ids {
				m := mods[id]
				op := "add_value"
				if m.op >= 0 && m.op < len(attrOpNames) {
					op = attrOpNames[m.op]
				}
				list = append(list, map[string]any{
					"amount":    m.value,
					"id":        id,
					"operation": op,
				})
			}
			ent["modifiers"] = nbt.List{Elem: nbt.TagCompound, Items: list}
		}
		items = append(items, ent)
	}
	return items
}

// applyPackedAttrs applies an attributes save list the way
// AttributeMap.apply does: every packed entry's base is written raw
// and its permanent modifiers replace the instance's set. Unknown or
// malformed entries are ignored.
func applyPackedAttrs(e *Entity, v any) {
	items, ok := v.(nbt.List)
	if !ok {
		if items2, ok2 := v.([]any); ok2 {
			items = nbt.List{Items: items2}
		} else {
			return
		}
	}
	for _, it := range items.Items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		idv, _ := m["id"].(string)
		short := strings.TrimPrefix(idv, "minecraft:")
		if short == "" {
			continue
		}
		e.markAttrDirty(short)
		if base, ok := nbtDouble(m["base"]); ok {
			if e.AttrBase == nil {
				e.AttrBase = map[string]float64{}
			}
			e.AttrBase[short] = base
		}
		if m["modifiers"] != nil {
			if e.AttrMods == nil {
				e.AttrMods = map[string]map[string]attrMod{}
			}
			mods := map[string]attrMod{}
			if ml, ok := m["modifiers"].(nbt.List); ok {
				for _, mv := range ml.Items {
					mm, ok := mv.(map[string]any)
					if !ok {
						continue
					}
					mid, _ := mm["id"].(string)
					amt, _ := nbtDouble(mm["amount"])
					opName, _ := mm["operation"].(string)
					op := 0
					for i, n := range attrOpNames {
						if n == opName {
							op = i
						}
					}
					mods[mid] = attrMod{value: amt, op: op}
				}
			}
			e.AttrMods[short] = mods
		}
	}
}

// nbtDouble reads a numeric NBT value at double precision — attribute
// bases and modifier amounts are stored as doubles.
func nbtDouble(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
