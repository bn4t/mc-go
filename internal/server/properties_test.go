package server

import (
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/world"
)

// TestRegionCompressionProperty pins the server.properties key that
// selects the region record codec. The measured native behavior is an
// exact, case-sensitive name lookup over deflate/none/lz4, with any
// other value logged and the deflate default kept rather than a
// refusal to start.
func TestRegionCompressionProperty(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  world.RegionCodec
	}{
		{"deflate", world.RegionDeflate},
		{"none", world.RegionNone},
		{"lz4", world.RegionLZ4},
		{"LZ4", world.RegionDeflate},   // case-sensitive
		{"gzip", world.RegionDeflate},  // readable, never selectable
		{"bogus", world.RegionDeflate}, // logged, default kept
	} {
		t.Run(tc.value, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "server.properties")
			if err := os.WriteFile(path, []byte("region-file-compression="+tc.value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			props, err := LoadServerProperties(path)
			if err != nil {
				t.Fatalf("load server.properties: %v", err)
			}
			s := New("127.0.0.1:0")
			if err := s.ApplyProperties(props, dir, nil); err != nil {
				t.Fatalf("apply server.properties: %v", err)
			}
			if s.RegionCompression != tc.want {
				t.Errorf("region-file-compression=%q selects %d, want %d", tc.value, s.RegionCompression, tc.want)
			}
		})
	}
}
