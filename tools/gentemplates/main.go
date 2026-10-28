// Command gentemplates reads the structure templates the official 26.2
// server jar ships (data/minecraft/structure/**/*.nbt) and writes them
// to internal/worldgen/templates_gen.bin in a compact form the
// generator loads on demand: per template its size, palettes, blocks
// (position, palette index, block entity tag) and entities (position,
// block position, entity tag), in the file's own order. Nothing is
// interpreted here; tags are re-encoded as NBT.
//
// Layout (gzip): the line "mcgo templates 1", then a uvarint count and,
// per template sorted by id, its id and its body, each a uvarint length
// and bytes. A body is the size as three varints, a uvarint palette
// count with per palette a uvarint state count and per state its block
// name and properties (a uvarint count of name/value pairs, as written),
// the blocks (uvarint count; per block three varints, a uvarint palette
// index and a length-prefixed NBT compound or a zero length), and the
// entities (uvarint count; per entity three float64 bit patterns, three
// varints and a length-prefixed NBT compound).
//
// Usage: go run ./tools/gentemplates [-root .]
package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mc-go/internal/nbt"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	jarPath := filepath.Join(*root, "vanilla", "versions", "26.2", "server-26.2.jar")
	raw, err := os.ReadFile(jarPath)
	must(err)
	jar, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	must(err)
	const prefix = "data/minecraft/structure/"
	files := map[string]*zip.File{}
	for _, f := range jar.File {
		if strings.HasPrefix(f.Name, prefix) && strings.HasSuffix(f.Name, ".nbt") {
			files["minecraft:"+strings.TrimSuffix(strings.TrimPrefix(f.Name, prefix), ".nbt")] = f
		}
	}
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var body bytes.Buffer
	body.WriteString("mcgo templates 1\n")
	putUvarint(&body, uint64(len(ids)))
	for _, id := range ids {
		rc, err := files[id].Open()
		must(err)
		zr, err := gzip.NewReader(rc)
		must(err)
		_, v, err := nbt.ReadNamed(zr)
		must(err)
		rc.Close()
		t, ok := v.(map[string]any)
		if !ok {
			fatal("%s: root is not a compound", id)
		}
		putString(&body, id)
		putBytes(&body, encodeTemplate(id, t))
	}
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	must(err)
	_, err = zw.Write(body.Bytes())
	must(err)
	must(zw.Close())
	dst := filepath.Join(*root, "internal", "worldgen", "templates_gen.bin")
	must(os.WriteFile(dst, out.Bytes(), 0o644))
	fmt.Printf("%d templates from jar SHA-256 %x, %d bytes\n", len(ids), sha256.Sum256(raw), out.Len())
}

func encodeTemplate(id string, t map[string]any) []byte {
	var b bytes.Buffer
	size := ints(id, t["size"])
	if len(size) != 3 {
		fatal("%s: bad size", id)
	}
	for _, v := range size {
		putVarint(&b, int64(v))
	}
	var palettes []any
	if p, ok := t["palettes"].(nbt.List); ok {
		palettes = p.Items
	} else if p, ok := t["palette"].(nbt.List); ok {
		palettes = []any{p}
	}
	putUvarint(&b, uint64(len(palettes)))
	for _, p := range palettes {
		states := p.(nbt.List).Items
		putUvarint(&b, uint64(len(states)))
		for _, s := range states {
			sm := s.(map[string]any)
			putString(&b, sm["Name"].(string))
			props, _ := sm["Properties"].(map[string]any)
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			putUvarint(&b, uint64(len(keys)))
			for _, k := range keys {
				putString(&b, k)
				putString(&b, props[k].(string))
			}
		}
	}
	blocks, _ := t["blocks"].(nbt.List)
	putUvarint(&b, uint64(len(blocks.Items)))
	for _, e := range blocks.Items {
		m := e.(map[string]any)
		pos := ints(id, m["pos"])
		for _, v := range pos {
			putVarint(&b, int64(v))
		}
		state, _ := m["state"].(int32)
		putUvarint(&b, uint64(state))
		putTag(&b, m["nbt"])
	}
	entities, _ := t["entities"].(nbt.List)
	putUvarint(&b, uint64(len(entities.Items)))
	for _, e := range entities.Items {
		m := e.(map[string]any)
		pos, _ := m["pos"].(nbt.List)
		for i := 0; i < 3; i++ {
			var v float64
			if i < len(pos.Items) {
				v = pos.Items[i].(float64)
			}
			var w [8]byte
			binary.LittleEndian.PutUint64(w[:], math.Float64bits(v))
			b.Write(w[:])
		}
		for _, v := range ints(id, m["blockPos"]) {
			putVarint(&b, int64(v))
		}
		putTag(&b, m["nbt"])
	}
	return b.Bytes()
}

func ints(id string, v any) []int32 {
	l, ok := v.(nbt.List)
	if !ok {
		fatal("%s: expected an int list, got %T", id, v)
	}
	out := make([]int32, len(l.Items))
	for i, e := range l.Items {
		out[i] = e.(int32)
	}
	return out
}

func putTag(b *bytes.Buffer, v any) {
	m, ok := v.(map[string]any)
	if !ok {
		putUvarint(b, 0)
		return
	}
	var t bytes.Buffer
	must(nbt.WriteUnnamed(&t, m))
	putBytes(b, t.Bytes())
}

func putUvarint(b *bytes.Buffer, v uint64) {
	var w [binary.MaxVarintLen64]byte
	b.Write(w[:binary.PutUvarint(w[:], v)])
}

func putVarint(b *bytes.Buffer, v int64) {
	var w [binary.MaxVarintLen64]byte
	b.Write(w[:binary.PutVarint(w[:], v)])
}

func putBytes(b *bytes.Buffer, p []byte) {
	putUvarint(b, uint64(len(p)))
	b.Write(p)
}

func putString(b *bytes.Buffer, s string) { putBytes(b, []byte(s)) }

func must(err error) {
	if err != nil && err != io.EOF {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gentemplates: "+format+"\n", args...)
	os.Exit(1)
}
