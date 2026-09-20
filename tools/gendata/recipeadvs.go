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
	Rewards      struct {
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
	type entry struct {
		id, recipe, parent string
		criteria           []string
		reqs               [][]string
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
		for _, recipe := range parsed.Rewards.Recipes {
			entries = append(entries, entry{id, recipe, parsed.Parent, criteria, parsed.Requirements})
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
		b.WriteString("}},\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}
