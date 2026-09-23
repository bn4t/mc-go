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

// summonAttrTouches applies the attribute-instance side effects of a
// /summon: vanilla's AttributeMap packs only instances that were
// touched. max_health is touched for tamables (setTame re-asserts the
// wild base). init is true only when the summon carried no NBT
// argument — vanilla then calls Mob.finalizeSpawn, which rolls
// follow_range's random_spawn_bonus and the zombie family's
// reinforcement/knockback touches. A summon with NBT skips
// finalizeSpawn entirely. movement_speed is NOT touched here — it is
// lazily instantiated on the mob's first living tick (the travel read),
// which only happens inside simulation distance.
func summonAttrTouches(e *Entity, name string, init bool) {
	if _, ok := tamableWildHealth[name]; ok {
		e.markAttrTouched("max_health")
	}
	if !init {
		return
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
		// random_spawn_bonus modifier.
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
		e.markAttrTouched(short)
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
