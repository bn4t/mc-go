package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCaptureVanillaConfig records vanilla's configuration burst —
// every Registry Data packet body plus the Update Tags body — into
// internal/server/testdata/vanilla-26.2/config. The server serves these
// captured bodies verbatim, which is exact configuration parity without
// any derived code. Run:
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaConfig
//
// The test skips unless MCGO_CAPTURE=1 so normal runs never write.
func TestCaptureVanillaConfig(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla config packets")
	}
	tg := requireBlackbox(t)
	defer tg.cleanup()

	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2", "config")
	if err := os.MkdirAll(filepath.Join(dir, "registries"), 0o755); err != nil {
		t.Fatal(err)
	}

	c, pr, pw := loginStart(t, tg.vanilla, "CaptureProbe")
	defer c.Close()
	readLoginSuccess(t, pr, pw, "vanilla")
	if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	// A pack version that cannot match forces vanilla to inline every
	// registry entry's NBT instead of sourcing it from the client pack.
	sendKnownPacks(t, pw, "99.9-capture")

	var keys []string
	regIdx := 0
	for i := 0; i < 200; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("vanilla config stream: %v", err)
		}
		switch p.ID {
		case server.IDConfigRegistryData:
			key := readRegistryKey(t, p.Data)
			keys = append(keys, key)
			name := fmt.Sprintf("%03d-%s.bin", regIdx, sanitize(key))
			if err := os.WriteFile(filepath.Join(dir, "registries", name), p.Data, 0o644); err != nil {
				t.Fatal(err)
			}
			regIdx++
		case server.IDConfigUpdateTags:
			if err := os.WriteFile(filepath.Join(dir, "tags.bin"), p.Data, 0o644); err != nil {
				t.Fatal(err)
			}
		case server.IDConfigFinish:
			manifest, _ := json.MarshalIndent(map[string]any{
				"protocol":   server.ProtocolVersion,
				"registries": keys,
			}, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("captured %d registries + tags into %s", len(keys), dir)
			return
		}
	}
	t.Fatal("vanilla never sent Finish Configuration")
}

func readRegistryKey(t *testing.T, body []byte) string {
	t.Helper()
	r := protocol.NewPacketReader(body)
	key, err := r.String()
	if err != nil {
		t.Fatalf("registry key: %v", err)
	}
	return key
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}
