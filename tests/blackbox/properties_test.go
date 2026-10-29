//go:build blackbox

package blackbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc-go/internal/server"
)

// TestServerPropertiesParity boots the real server on a harness
// server.properties carrying values that need escaping, lets it
// complete and rewrite the file, and requires mc-go's
// LoadServerProperties on the same input to write the same file: every
// default key, sorted, Java Properties escaping (colons, equals, a
// leading space, non-ASCII as \uXXXX). The store-time comment and the
// random management secret are the only lines allowed to differ.
func TestServerPropertiesParity(t *testing.T) {
	jar := blackboxJar(t)
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	dir := t.TempDir()
	extra := map[string]string{
		"motd":       `  Café: a=b #1 !`,
		"level-seed": "hello world",
	}
	_, stop := startVanillaInDir(t, jar, port, extra, nil, dir)
	stop()
	want, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	// mc-go reads the input the harness wrote (re-created here).
	mdir := t.TempDir()
	writeVanillaServerDir(t, mdir, port, extra, nil)
	if _, err := server.LoadServerProperties(filepath.Join(mdir, "server.properties")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(mdir, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	norm := func(b []byte) []string {
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		var out []string
		for i, l := range lines {
			if i == 1 && strings.HasPrefix(l, "#") {
				continue // store time
			}
			if strings.HasPrefix(l, "management-server-secret=") {
				continue
			}
			out = append(out, l)
		}
		return out
	}
	w, g := norm(want), norm(got)
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			t.Errorf("line %d: vanilla %q mc-go %q", i, a, b)
		}
	}
	if seed, ok := server.ParseSeed("hello world"); !ok || seed != int64(int32(1794106052)) {
		t.Errorf("ParseSeed(hello world) = %d, want String.hashCode 1794106052", seed)
	}
}
