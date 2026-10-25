package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Blobs: data-pack trees that are consumed as data rather than as Go
// tables. Each is packed into one gzipped, base64 JSON object keyed by
// its path inside data/minecraft, so the generated source stays small
// and the runtime decodes lazily on first use.

type blobSpec struct {
	dir      string // data/minecraft/<dir>
	varName  string // Go const holding the base64 body
	funcName string // accessor
	doc      string
	keyDoc   string
}

func genBlob(zr *zip.Reader, spec blobSpec) []byte {
	raw := readJSON(zr, spec.dir)
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	// Stable ordering keeps the generated file byte-identical across
	// runs, which makes regeneration diffs reviewable.
	sortStrings(keys)

	var plain bytes.Buffer
	plain.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			plain.WriteString(",")
		}
		kb, err := json.Marshal(k)
		if err != nil {
			log.Fatal(err)
		}
		vb, err := json.Marshal(raw[k])
		if err != nil {
			log.Fatalf("%s/%s: %v", spec.dir, k, err)
		}
		plain.Write(kb)
		plain.WriteString(":")
		plain.Write(vb)
	}
	plain.WriteString("}")

	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(plain.Bytes()); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(gz.Bytes())

	var b strings.Builder
	writeHeader(&b, "tools/gendata")
	b.WriteString("package data\n\n")
	b.WriteString("import \"sync\"\n\n")
	fmt.Fprintf(&b, "// %s is the gzipped, base64-encoded %s tree from the bundled\n// data pack (%d entries, %d bytes of JSON).\nconst %s = %s\n\n",
		spec.varName, spec.dir, len(keys), plain.Len(), spec.varName, chunkString(b64))
	fmt.Fprintf(&b, "var (\n\t%sOnce sync.Once\n\t%sVal map[string]map[string]any\n)\n\n", spec.funcName, spec.funcName)
	fmt.Fprintf(&b, "%s\nfunc %s() map[string]map[string]any {\n\t%sOnce.Do(func() {\n\t\t%sVal = decodeBlob(%s)\n\t})\n\treturn %sVal\n}\n",
		spec.doc, spec.funcName, spec.funcName, spec.funcName, spec.varName, spec.funcName)
	return []byte(b.String())
}

// chunkString renders a long string literal as adjacent chunks so no
// single source line is unreadable.
func chunkString(s string) string {
	const width = 120
	var b strings.Builder
	for i := 0; i < len(s); i += width {
		end := i + width
		if end > len(s) {
			end = len(s)
		}
		if i > 0 {
			b.WriteString(" +\n\t")
		}
		b.WriteString(`"` + s[i:end] + `"`)
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
