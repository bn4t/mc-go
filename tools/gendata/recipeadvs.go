package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
)

// Recipe advancements drive the UpdateAdvancements packets emitted when
// recipes unlock: each grants its "recipe_unlocked" criterion, and the
// wire def carries the requirements plus every criterion name in the
// JSON's original order. The data-pack blob loses object order, so this
// table is emitted separately, sorted by advancement id.

type recipeAdvFile struct {
	Parent       string     `json:"parent"`
	Requirements [][]string `json:"requirements"`
	Criteria     map[string]struct {
		Trigger    string `json:"trigger"`
		Conditions struct {
			Items []struct {
				Items string `json:"items"`
			} `json:"items"`
			Block string `json:"block"`
			Slots struct {
				Occupied struct {
					Min int `json:"min"`
				} `json:"occupied"`
			} `json:"slots"`
		} `json:"conditions"`
	} `json:"criteria"`
	Rewards struct {
		Recipes []string `json:"recipes"`
	} `json:"rewards"`
}

// orderedKeys returns the keys of the named object's first-level members
// in JSON order.
func orderedKeys(raw json.RawMessage, field string) []string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		log.Fatal(err)
	}
	obj, ok := fields[field]
	if !ok {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(obj))
	if _, err := dec.Token(); err != nil { // {
		log.Fatal(err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			log.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			log.Fatal(err)
		}
	}
	return keys
}

func genRecipeAdvs(zr *zip.Reader) []byte {
	const prefix = "data/minecraft/advancement/recipes/"
	type advCond struct {
		Name     string
		Trigger  string
		Items    []string // item ids or "#tag"
		Block    string
		SlotsMin int
	}
	type entry struct {
		id, recipe, parent string
		criteria           []string
		reqs               [][]string
		conds              []advCond
	}
	var entries []entry
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			log.Fatalf("open %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Fatalf("read %s: %v", f.Name, err)
		}
		var parsed recipeAdvFile
		if err := json.Unmarshal(raw, &parsed); err != nil {
			log.Fatalf("parse %s: %v", f.Name, err)
		}
		if len(parsed.Rewards.Recipes) == 0 {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(f.Name, "data/minecraft/advancement/"), ".json")
		criteria := orderedKeys(raw, "criteria")
		var conds []advCond
		for _, c := range criteria {
			cc := parsed.Criteria[c]
			var cond advCond
			cond.Name = c
			cond.Trigger = strings.TrimPrefix(cc.Trigger, "minecraft:")
			for _, pred := range cc.Conditions.Items {
				cond.Items = append(cond.Items, pred.Items)
			}
			cond.Block = strings.TrimPrefix(cc.Conditions.Block, "minecraft:")
			cond.SlotsMin = cc.Conditions.Slots.Occupied.Min
			conds = append(conds, cond)
		}
		for _, recipe := range parsed.Rewards.Recipes {
			entries = append(entries, entry{id, recipe, parsed.Parent, criteria, parsed.Requirements, conds})
		}
	}
	sortEntries := func(s []entry) {
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j].id < s[j-1].id; j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
	}
	sortEntries(entries)

	var b strings.Builder
	writeHeader(&b, "tools/gendata")
	b.WriteString("package data\n\n")
	b.WriteString(`// RecipeAdv is a recipe advancement: the wire definition synced to the
// client (parent, requirements) and the criteria whose progress reports
// when the recipe unlocks. Criteria carries the JSON member order; Reqs
// is the requirements array verbatim.
type RecipeAdv struct {
	Recipe   string   // e.g. "minecraft:oak_planks"
	ID       string   // e.g. "minecraft:recipes/building_blocks/oak_planks"
	Parent   string
	Criteria []string
	Reqs     [][]string
	Conds    []AdvCond // aligned with Criteria
}

// AdvCond is one recipe-advancement criterion's grant condition:
// inventory_changed fires when a matching item occupies an inventory
// slot, enter_block when the player stands inside the block, and
// slots.occupied when that many inventory slots are filled.
type AdvCond struct {
	Name     string
	Trigger  string
	Items    []string // item ids or "#tag" item tag
	Block    string
	SlotsMin int
}

// RecipeAdvs lists every recipe advancement in the vanilla data pack,
// sorted by advancement id.
var RecipeAdvs = []RecipeAdv{
`)
	for _, e := range entries {
		fmt.Fprintf(&b, "\t{Recipe: %q, ID: \"minecraft:%s\", Parent: %q, Criteria: []string{", e.recipe, e.id, e.parent)
		for i, c := range e.criteria {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", c)
		}
		b.WriteString("}, Reqs: [][]string{")
		for i, r := range e.reqs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("{")
			for j, c := range r {
				if j > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", c)
			}
			b.WriteString("}")
		}
		b.WriteString("}, Conds: []AdvCond{")
		for i, c := range e.conds {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "{Name: %q, Trigger: %q", c.Name, c.Trigger)
			if len(c.Items) > 0 {
				b.WriteString(", Items: []string{")
				for j, it := range c.Items {
					if j > 0 {
						b.WriteString(", ")
					}
					fmt.Fprintf(&b, "%q", it)
				}
				b.WriteString("}")
			}
			if c.Block != "" {
				fmt.Fprintf(&b, ", Block: %q", c.Block)
			}
			if c.SlotsMin != 0 {
				fmt.Fprintf(&b, ", SlotsMin: %d", c.SlotsMin)
			}
			b.WriteString("}")
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}
