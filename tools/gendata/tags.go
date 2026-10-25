package main

import (
	"archive/zip"
	"fmt"
	"log"
	"sort"
	"strings"
)

// Tags: data/minecraft/tags/{item,block}/<name>.json, each a
// {"values": [...]} list whose entries are item ids, "#tag" references
// or {"id": ..., "required": bool} objects. Nested references are
// expanded here so the runtime sees a flat membership list.

func genTags(zr *zip.Reader) []byte {
	var b strings.Builder
	writeHeader(&b, "tools/gendata")
	b.WriteString("package data\n\n")

	for _, kind := range []string{"item", "block", "entity_type"} {
		raw := readJSON(zr, "tags/"+kind)
		tags := map[string][]string{}
		for k, v := range raw {
			vals := asList(asMap(v)["values"])
			out := make([]string, 0, len(vals))
			for _, e := range vals {
				switch t := e.(type) {
				case string:
					out = append(out, t)
				case map[string]any:
					if id := asString(t["id"]); id != "" {
						out = append(out, id)
					}
				}
			}
			tags[k] = out
		}
		resolved, err := resolveTags(tags)
		if err != nil {
			log.Fatalf("resolve %s tags: %v", kind, err)
		}
		names := make([]string, 0, len(resolved))
		for k := range resolved {
			names = append(names, k)
		}
		sort.Strings(names)
		varName := "ItemTags"
		doc := "// ItemTags maps a tag name (minecraft namespace implied, prefix\n// dropped) to the item names it contains, with nested #tag entries\n// expanded. Generated from the bundled data pack."
		switch kind {
		case "block":
			varName = "BlockTags"
			doc = "// BlockTags is ItemTags for the block tag registry."
		case "entity_type":
			varName = "EntityTypeTags"
			doc = "// EntityTypeTags is ItemTags for the entity_type tag registry."
		}
		fmt.Fprintf(&b, "%s\nvar %s = map[string][]string{\n", doc, varName)
		for _, n := range names {
			fmt.Fprintf(&b, "\t%s: %s,\n", gostr(n), goStrSlice(resolved[n]))
		}
		b.WriteString("}\n\n")
	}
	return []byte(b.String())
}

// resolveTags flattens #tag references transitively. Cycles are a data
// pack error; the real server rejects them, so we fail loudly.
func resolveTags(tags map[string][]string) (map[string][]string, error) {
	norm := func(s string) string { return name(s) }
	inProgress := map[string]bool{}
	out := map[string][]string{}

	var expand func(tag string, stack []string) ([]string, error)
	expand = func(tag string, stack []string) ([]string, error) {
		if v, ok := out[tag]; ok {
			return v, nil
		}
		if inProgress[tag] {
			return nil, fmt.Errorf("tag cycle: %s", strings.Join(append(stack, tag), " -> "))
		}
		inProgress[tag] = true
		seen := map[string]bool{}
		var items []string
		add := func(s string) {
			if !seen[s] {
				seen[s] = true
				items = append(items, s)
			}
		}
		for _, e := range tags[tag] {
			if strings.HasPrefix(e, "#") {
				sub, err := expand(norm(strings.TrimPrefix(e, "#")), append(stack, tag))
				if err != nil {
					return nil, err
				}
				for _, s := range sub {
					add(s)
				}
				continue
			}
			add(norm(e))
		}
		delete(inProgress, tag)
		sort.Strings(items)
		out[tag] = items
		return items, nil
	}
	for t := range tags {
		if _, err := expand(t, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}
