package main

import (
	"archive/zip"
	"fmt"
	"log"
	"sort"
	"strings"
)

// Recipes: data/minecraft/recipe/<name>.json. Each file carries a
// "type" discriminator; the fields below cover every shape the 26.2
// data pack uses. Ingredients are normalized to a list of acceptable
// item names, with "#tag" entries expanded through the item tag table
// so the runtime matcher is a plain membership test.

type recipeOut struct {
	Name        string
	Type        string // without the minecraft: prefix
	Group       string
	Category    string
	Pattern     []string
	Key         map[string]ingredient // rune -> acceptable item names
	Ingredients []ingredient          // shapeless / cooking / stonecutting inputs
	ResultName  string
	ResultCount int
	MaterialMax int
	MaterialMin int
	HideNotify  bool
	CookingTime int
	Experience  float64
	// Extra fields used by the dye/transmute/imbue/smithing families.
	Input    ingredient
	Material ingredient
	Template ingredient
	Base     ingredient
	Addition ingredient
}

// ingredient is one recipe input: the tag it came from (empty when the
// data pack named items directly) plus the flattened membership list.
type ingredient struct {
	Tag   string
	Items []string
}

func genRecipes(zr *zip.Reader) []byte {
	raw := readJSON(zr, "recipe")
	tags := itemTagTable(zr)

	names := make([]string, 0, len(raw))
	for k := range raw {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	writeHeader(&b, "tools/gendata")
	b.WriteString("package data\n\n")
	b.WriteString("// Recipes is the full vanilla recipe registry, ordered by recipe\n// path. Network recipe ids index this slice, so the order is part of\n// the wire contract.\nvar Recipes = []Recipe{\n")

	stats := map[string]int{}
	for _, n := range names {
		r, err := parseRecipe(n, asMap(raw[n]), tags)
		if err != nil {
			log.Fatalf("recipe %s: %v", n, err)
		}
		stats[r.Type]++
		writeRecipe(&b, r)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "// RecipeTypeCounts records how many recipes of each type the data\n// pack defines; the generator asserts nothing, but the number is a\n// useful cross-check against the report.\nvar RecipeTypeCounts = map[string]int{\n")
	types := make([]string, 0, len(stats))
	for t := range stats {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		fmt.Fprintf(&b, "\t%s: %d,\n", gostr(t), stats[t])
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

func parseRecipe(fileName string, m map[string]any, tags map[string][]string) (recipeOut, error) {
	r := recipeOut{Name: name(fileName), Category: asString(m["category"]), Group: asString(m["group"])}
	typ := asString(m["type"])
	if typ == "" {
		return r, fmt.Errorf("missing type")
	}
	r.Type = name(typ)
	res := asMap(m["result"])
	r.ResultName = name(asString(res["id"]))
	r.ResultCount = int(asInt(res["count"], 1))
	r.CookingTime = int(asInt(m["cookingtime"], 0))
	if sn, ok := m["show_notification"].(bool); ok && !sn {
		r.HideNotify = true
	}
	r.Experience = asFloat(m["experience"])

	switch r.Type {
	case "crafting_shaped":
		for _, row := range asList(m["pattern"]) {
			r.Pattern = append(r.Pattern, asString(row))
		}
		r.Key = map[string]ingredient{}
		for k, v := range asMap(m["key"]) {
			r.Key[k] = ingredientNames(v, tags)
		}
	case "crafting_shapeless":
		for _, ing := range asList(m["ingredients"]) {
			r.Ingredients = append(r.Ingredients, ingredientNames(ing, tags))
		}
	case "smelting", "blasting", "smoking", "campfire_cooking", "stonecutting":
		r.Ingredients = append(r.Ingredients, ingredientNames(m["ingredient"], tags))
		if r.ResultCount == 0 {
			r.ResultCount = 1
		}
	case "crafting_transmute", "crafting_dye":
		r.Input = ingredientNames(m["input"], tags)
		r.Material = ingredientNames(m["material"], tags)
		if mc := asMap(m["material_count"]); mc != nil {
			r.MaterialMax = int(asInt(mc["max"], 0))
			r.MaterialMin = int(asInt(mc["min"], 0))
		}
	case "crafting_imbue":
		r.Input = ingredientNames(m["input"], tags)
		r.Material = ingredientNames(m["material"], tags)
	case "smithing_transform", "smithing_trim":
		r.Template = ingredientNames(m["template"], tags)
		r.Base = ingredientNames(m["base"], tags)
		r.Addition = ingredientNames(m["addition"], tags)
	case "crafting_decorated_pot":
		r.Ingredients = append(r.Ingredients,
			ingredientNames(m["back"], tags), ingredientNames(m["front"], tags),
			ingredientNames(m["left"], tags), ingredientNames(m["right"], tags))
	case "crafting_special_bannerduplicate", "crafting_special_shielddecoration",
		"crafting_special_repairitem", "crafting_special_mapextending",
		"crafting_special_firework_star_fade", "crafting_special_firework_star",
		"crafting_special_firework_rocket", "crafting_special_bookcloning":
		// Special recipes have no declarative ingredients: the result
		// is computed from the grid contents by hand-written code.
	default:
		// Unknown-but-present types keep their result so the recipe is
		// at least visible to the book; log so drift is noticeable.
		fmt.Fprintf(log.Writer(), "note: recipe %s has unhandled type %s\n", fileName, r.Type)
	}
	if r.ResultCount == 0 {
		r.ResultCount = 1
	}
	if r.ResultName == "" && !strings.HasPrefix(r.Type, "crafting_special_") && r.Type != "smithing_trim" {
		return r, fmt.Errorf("missing result id")
	}
	return r, nil
}

// ingredientNames normalizes every ingredient shape to a list of item
// names: "minecraft:x", "#minecraft:tag", a list of either, or an
// object with item/items/tag keys.
func ingredientNames(v any, tags map[string][]string) ingredient {
	switch t := v.(type) {
	case nil:
		return ingredient{}
	case string:
		if ref := strings.TrimPrefix(t, "#"); ref != t {
			return ingredient{Tag: name(ref), Items: tags[name(ref)]}
		}
		return ingredient{Items: []string{name(t)}}
	case []any:
		seen := map[string]bool{}
		var out []string
		for _, e := range t {
			for _, n := range ingredientNames(e, tags).Items {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
		return ingredient{Items: out}
	case map[string]any:
		if tag := asString(t["tag"]); tag != "" {
			return ingredient{Tag: name(tag), Items: tags[name(tag)]}
		}
		if item := asString(t["item"]); item != "" {
			return ingredient{Items: []string{name(item)}}
		}
		if items, ok := t["items"]; ok {
			return ingredientNames(items, tags)
		}
		return ingredient{}
	}
	return ingredient{}
}

// itemTagTable resolves the item tag registry, expanding nested
// references, so "#minecraft:planks" becomes its twelve plank items.
func itemTagTable(zr *zip.Reader) map[string][]string {
	raw := readJSON(zr, "tags/item")
	tags := map[string][]string{}
	for k, v := range raw {
		for _, e := range asList(asMap(v)["values"]) {
			switch t := e.(type) {
			case string:
				tags[k] = append(tags[k], t)
			case map[string]any:
				if id := asString(t["id"]); id != "" {
					tags[k] = append(tags[k], id)
				}
			}
		}
	}
	resolved, err := resolveTags(tags)
	if err != nil {
		log.Fatalf("resolve item tags: %v", err)
	}
	return resolved
}

func asInt(v any, def int) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	}
	return int64(def)
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	}
	return 0
}

func writeRecipe(b *strings.Builder, r recipeOut) {
	fmt.Fprintf(b, "\t{ID: %s, Type: %s, ", gostr(r.Name), gostr(r.Type))
	if r.Group != "" {
		fmt.Fprintf(b, "Group: %s, ", gostr(r.Group))
	}
	if r.Category != "" {
		fmt.Fprintf(b, "Category: %s, ", gostr(r.Category))
	}
	if len(r.Pattern) > 0 {
		fmt.Fprintf(b, "Pattern: %s, ", goStrSlice(r.Pattern))
	}
	if len(r.Key) > 0 {
		keys := make([]string, 0, len(r.Key))
		for k := range r.Key {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(b, "Key: map[byte]Ingredient{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%s: %s", goByte(k), goIngredient(r.Key[k]))
		}
		b.WriteString("}, ")
	}
	if len(r.Ingredients) > 0 {
		fmt.Fprintf(b, "Ingredients: %s, ", goNested(r.Ingredients))
	}
	if len(r.Input.Items) > 0 {
		fmt.Fprintf(b, "Input: %s, ", goIngredient(r.Input))
	}
	if len(r.Material.Items) > 0 {
		fmt.Fprintf(b, "Material: %s, ", goIngredient(r.Material))
	}
	if r.MaterialMax > 0 {
		fmt.Fprintf(b, "MaterialMax: %d, MaterialMin: %d, ", r.MaterialMax, r.MaterialMin)
	}
	if len(r.Template.Items) > 0 {
		fmt.Fprintf(b, "Template: %s, ", goIngredient(r.Template))
	}
	if len(r.Base.Items) > 0 {
		fmt.Fprintf(b, "Base: %s, ", goIngredient(r.Base))
	}
	if len(r.Addition.Items) > 0 {
		fmt.Fprintf(b, "Addition: %s, ", goIngredient(r.Addition))
	}
	if r.HideNotify {
		fmt.Fprintf(b, "HideNotification: true, ")
	}
	fmt.Fprintf(b, "Result: ItemStack{Item: %s, Count: %d}", gostr(r.ResultName), r.ResultCount)
	if r.CookingTime != 0 {
		fmt.Fprintf(b, ", CookingTime: %d", r.CookingTime)
	}
	if r.Experience != 0 {
		fmt.Fprintf(b, ", Experience: %g", r.Experience)
	}
	b.WriteString("},\n")
}

func goNested(ss []ingredient) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = "{" + ingredientBody(s) + "}"
	}
	return "[]Ingredient{" + strings.Join(parts, ", ") + "}"
}

// goIngredient renders one ingredient literal, keeping the source tag
// so the recipe book can echo "#minecraft:planks" back to the client.
func goIngredient(in ingredient) string { return "Ingredient{" + ingredientBody(in) + "}" }

// ingredientBody renders an ingredient's fields without the type name,
// so slice literals stay readable.
func ingredientBody(in ingredient) string {
	if in.Tag != "" {
		return "Tag: " + gostr(in.Tag) + ", Items: " + goStrSlice(in.Items)
	}
	return "Items: " + goStrSlice(in.Items)
}

// camel turns "crafting_shaped" into "CraftingShaped" for the type
// constants the runtime switches on.
func camel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}
