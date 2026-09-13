package data

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
)

// decodeBlob unpacks one embedded data-pack tree, which is stored as a
// gzipped base64 JSON object keyed by path inside data/minecraft.
func decodeBlob(blob string) map[string]map[string]any {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		panic("data: bad embedded blob: " + err.Error())
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		panic("data: bad embedded blob: " + err.Error())
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		panic("data: bad embedded blob: " + err.Error())
	}
	out := map[string]map[string]any{}
	if err := json.Unmarshal(plain, &out); err != nil {
		panic("data: bad embedded blob: " + err.Error())
	}
	return out
}
