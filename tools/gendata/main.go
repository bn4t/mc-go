// Command gendata reads the bundled vanilla data pack (the data/
// tree inside the real server jar) and emits Go tables for the
// registries mc-go needs at runtime: item and block tags, crafting and
// cooking recipes, and the loot tables behind block and mob drops.
//
// The jar is a data source, not source code: GOAL.md allows the
// bundled data pack, so everything here is JSON that the real server
// would load from disk anyway. Nothing in this tool reads class files.
//
// Usage:
//
//	go run ./tools/gendata -jar vanilla/server-26.2.jar -out internal/data
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	jar := flag.String("jar", filepath.Join("vanilla", "server-26.2.jar"), "path to the vanilla server jar")
	out := flag.String("out", filepath.Join("internal", "data"), "directory for generated Go files")
	only := flag.String("only", "", "comma-separated subset to emit: tags,recipes,loot,advancements,trades (default all)")
	flag.Parse()

	zr := openDataPack(*jar)
	sections := map[string]bool{}
	for _, s := range []string{"tags", "recipes", "loot", "advancements", "trades"} {
		sections[s] = true
	}
	if *only != "" {
		sections = map[string]bool{}
		for _, s := range strings.Split(*only, ",") {
			sections[strings.TrimSpace(s)] = true
		}
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	written := 0
	emit := func(name string, body []byte) {
		if formatted, err := format.Source(body); err == nil {
			body = formatted
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s is not valid Go: %v\n", name, err)
		}
		p := filepath.Join(*out, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", p, len(body))
		written++
	}

	if sections["tags"] {
		emit("tags_gen.go", genTags(zr))
	}
	if sections["recipes"] {
		emit("recipes_gen.go", genRecipes(zr))
	}
	if sections["loot"] {
		emit("loot_gen.go", genBlob(zr, blobSpec{
			dir: "loot_table", varName: "lootBlob", funcName: "LootTables",
			doc: "// LootTables returns the vanilla loot table registry, keyed by table\n// path inside data/minecraft/loot_table (\"blocks/stone\",\n// \"entities/pig\", \"chests/simple_dungeon\", ...). Tables are decoded\n// from the embedded data pack on first use; the raw JSON shape is what\n// the interpreter in internal/loot walks.",
		}))
	}
	if sections["advancements"] {
		emit("advancements_gen.go", genBlob(zr, blobSpec{
			dir: "advancement", varName: "advancementBlob", funcName: "Advancements",
			doc: "// Advancements returns the vanilla advancement tree, keyed by path\n// inside data/minecraft/advancement.",
		}))
		emit("recipeadvs_gen.go", genRecipeAdvs(zr))
	}
	if sections["trades"] {
		emit("trades_gen.go", genBlob(zr, blobSpec{
			dir: "villager_trade", varName: "tradeBlob", funcName: "VillagerTrades",
			doc: "// VillagerTrades returns the vanilla villager trade tables, keyed by\n// path inside data/minecraft/villager_trade.",
		}))
	}
	fmt.Printf("%d files written\n", written)
}

// ---------------------------------------------------------------------
// jar plumbing

// dataPack returns a reader over the data/ tree of the vanilla data
// pack. The server jar is a bundler: the real jar (and its data/
// directory) sits inside META-INF/versions/<version>/.
func openDataPack(jar string) *zip.Reader {
	outer, err := zip.OpenReader(jar)
	if err != nil {
		log.Fatalf("open %s: %v", jar, err)
	}
	for _, f := range outer.File {
		if !strings.HasPrefix(f.Name, "META-INF/versions/") || !strings.HasSuffix(f.Name, ".jar") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			log.Fatalf("open nested %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Fatalf("read nested %s: %v", f.Name, err)
		}
		nested, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			log.Fatalf("parse nested %s: %v", f.Name, err)
		}
		fmt.Printf("data pack: %s (%d entries)\n", f.Name, len(nested.File))
		return nested
	}
	log.Fatalf("%s has no nested server jar", jar)
	return nil
}

// readJSON loads every data/minecraft/<dir>/**/*.json file, mapping the
// path relative to dir (without the .json suffix) to its decoded body.
func readJSON(zr *zip.Reader, dir string) map[string]any {
	prefix := "data/minecraft/" + dir + "/"
	out := map[string]any{}
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
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			log.Fatalf("parse %s: %v", f.Name, err)
		}
		key := strings.TrimSuffix(strings.TrimPrefix(f.Name, prefix), ".json")
		out[key] = v
	}
	return out
}

// ---------------------------------------------------------------------
// small helpers over the decoded JSON

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// name strips the minecraft: prefix; other namespaces are kept whole.
func name(s string) string {
	return strings.TrimPrefix(s, "minecraft:")
}

// gostr renders a Go string literal.
func gostr(s string) string { return strconv.Quote(s) }

func goStrSlice(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = gostr(s)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

func writeHeader(b *strings.Builder, tool string) {
	b.WriteString("// Code generated by " + tool + " from the vanilla data pack; DO NOT EDIT.\n\n")
}

// goByte renders a one-character pattern key as a Go rune literal.
func goByte(s string) string {
	if len(s) != 1 {
		return "0"
	}
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}
