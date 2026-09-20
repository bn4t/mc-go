// Command genenchant builds the enchantment-offer table the enchanting
// table menu needs: per-enchantment weight, level range, cost curves,
// supported/primary item sets, and exclusive-set membership, plus the
// per-item enchantable value. Sources are the committed datapack export
// (vanilla/generated/data/minecraft/enchantment/) and the item
// component reports (vanilla/reports/minecraft/components/item/).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	enchantDir    = "vanilla/generated/data/minecraft/enchantment"
	enchantTagDir = "vanilla/generated/data/minecraft/tags/enchantment"
	itemTagDir    = "vanilla/generated/data/minecraft/tags/item"
	itemCompDir   = "vanilla/reports/minecraft/components/item"
	out           = "internal/data/enchant_gen.go"
)

type cost struct {
	Base   int `json:"base"`
	PerLvl int `json:"per_level_above_first"`
}

type enchantment struct {
	Weight    int    `json:"weight"`
	MaxLevel  int    `json:"max_level"`
	MinCost   cost   `json:"min_cost"`
	MaxCost   cost   `json:"max_cost"`
	Supported any    `json:"supported_items"` // string "#tag" or list of items/tags
	Primary   any    `json:"primary_items"`
	Exclusive string `json:"exclusive_set"` // "#minecraft:exclusive_set/name"
}

func readTagValues(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return t.Values, nil
}

// expandTag resolves a "#name" reference inside dir's tag files,
// following nested tag refs.
func expandTag(dir, ref string, seen map[string]bool) ([]string, error) {
	name := strings.TrimPrefix(strings.TrimPrefix(ref, "#"), "minecraft:")
	if seen[name] {
		return nil, nil
	}
	seen[name] = true
	vals, err := readTagValues(filepath.Join(dir, name+".json"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range vals {
		if strings.HasPrefix(v, "#") {
			sub, err := expandTag(dir, v, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		out = append(out, strings.TrimPrefix(v, "minecraft:"))
	}
	return out, nil
}

// expandItemRef resolves a supported_items/primary_items value: either
// a "#tag" into the item tag registry or a literal item list.
func expandItemRef(v any) ([]string, error) {
	switch s := v.(type) {
	case string:
		if strings.HasPrefix(s, "#") {
			return expandTag(itemTagDir, s, map[string]bool{})
		}
		return []string{strings.TrimPrefix(s, "minecraft:")}, nil
	case []any:
		var out []string
		for _, e := range s {
			sub, err := expandItemRef(e)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported item ref %T", v)
}

func shortList(ss []string) string {
	set := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !set[s] {
			set[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return fmt.Sprintf("%#v", out)
}

func main() {
	files, err := filepath.Glob(filepath.Join(enchantDir, "*.json"))
	if err != nil || len(files) == 0 {
		panic("no enchantment data: " + enchantDir)
	}

	// in_enchanting_table (tag of tags: non_treasure).
	inTableList, err := expandTag(enchantTagDir, "#minecraft:in_enchanting_table", map[string]bool{})
	if err != nil {
		panic(err)
	}
	inTable := map[string]bool{}
	for _, e := range inTableList {
		inTable[e] = true
	}

	// exclusive_set/* tag members keyed by set name.
	sets := map[string][]string{}
	setFiles, _ := filepath.Glob(filepath.Join(enchantTagDir, "exclusive_set", "*.json"))
	for _, f := range setFiles {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		members, err := expandTag(enchantTagDir, "#minecraft:exclusive_set/"+name, map[string]bool{})
		if err != nil {
			panic(err)
		}
		sets[name] = members
	}

	type row struct {
		name           string
		weight, maxLvl int
		minB, minP     int
		maxB, maxP     int
		supported      []string
		primary        []string
		exclusive      string
		inTable        bool
	}
	var rows []row
	for _, f := range files {
		var e enchantment
		if err := json.Unmarshal(must(os.ReadFile(f)), &e); err != nil {
			panic(f + ": " + err.Error())
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		sup, err := expandItemRef(e.Supported)
		if err != nil {
			panic(f + " supported_items: " + err.Error())
		}
		pri := sup
		if e.Primary != nil {
			pri, err = expandItemRef(e.Primary)
			if err != nil {
				panic(f + " primary_items: " + err.Error())
			}
		}
		excl := strings.TrimPrefix(strings.TrimPrefix(e.Exclusive, "#minecraft:exclusive_set/"), "minecraft:exclusive_set/")
		rows = append(rows, row{
			name: name, weight: e.Weight, maxLvl: e.MaxLevel,
			minB: e.MinCost.Base, minP: e.MinCost.PerLvl,
			maxB: e.MaxCost.Base, maxP: e.MaxCost.PerLvl,
			supported: sup, primary: pri,
			exclusive: excl, inTable: inTable[name],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	var b strings.Builder
	b.WriteString("// Code generated by tools/genenchant; DO NOT EDIT.\n\n")
	b.WriteString("package data\n\n")
	b.WriteString(`// EnchantmentMeta is one vanilla enchantment's offer-table data:
// weight in the weighted pick, its max level, the min/max cost curve
// (base + per_level_above_first*(level-1)), the supported and primary
// item sets (supported defaults primary when primary_items is absent),
// the exclusive_set name it conflicts through, and whether the
// enchanting table may offer it (in_enchanting_table tag).
type EnchantmentMeta struct {
	Weight    int
	MaxLevel  int
	MinBase   int
	MinPerLvl int
	MaxBase   int
	MaxPerLvl int
	Supported []string
	Primary   []string
	Exclusive string
	InTable   bool
}

var Enchantments = map[string]EnchantmentMeta{
`)
	for _, r := range rows {
		fmt.Fprintf(&b, "\t%q: {Weight: %d, MaxLevel: %d, MinBase: %d, MinPerLvl: %d, MaxBase: %d, MaxPerLvl: %d, Supported: %s, Primary: %s, Exclusive: %q, InTable: %v},\n",
			r.name, r.weight, r.maxLvl, r.minB, r.minP, r.maxB, r.maxP,
			shortList(r.supported), shortList(r.primary), r.exclusive, r.inTable)
	}
	b.WriteString("}\n\n")

	b.WriteString("// ExclusiveSets resolves one exclusive_set tag to its member\n// enchantments (compat is symmetric through either side's set).\nvar ExclusiveSets = map[string][]string{\n")
	var setNames []string
	for k := range sets {
		setNames = append(setNames, k)
	}
	sort.Strings(setNames)
	for _, k := range setNames {
		fmt.Fprintf(&b, "\t%q: %s,\n", k, shortList(sets[k]))
	}
	b.WriteString("}\n\n")

	// Per-item enchantable value from the component reports.
	items, _ := filepath.Glob(filepath.Join(itemCompDir, "*.json"))
	b.WriteString("// ItemEnchantable is the minecraft:enchantable component's value\n// per item; items absent from the map have no component at all.\nvar ItemEnchantable = map[string]int{\n")
	var itemNames []string
	vals := map[string]int{}
	for _, f := range items {
		var d struct {
			Components map[string]json.RawMessage `json:"components"`
		}
		if err := json.Unmarshal(must(os.ReadFile(f)), &d); err != nil {
			continue
		}
		raw, ok := d.Components["minecraft:enchantable"]
		if !ok {
			continue
		}
		var e struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		vals[name] = e.Value
		itemNames = append(itemNames, name)
	}
	sort.Strings(itemNames)
	for _, n := range itemNames {
		fmt.Fprintf(&b, "\t%q: %d,\n", n, vals[n])
	}
	b.WriteString("}\n")

	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s: %d enchantments (%d in-table), %d exclusive sets, %d enchantable items\n",
		out, len(rows), len(inTable), len(sets), len(itemNames))
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
