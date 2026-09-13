package blackbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestCaptureVanillaWorld records the vanilla flat-world play burst:
// the first Chunk Data and Update Light packet and the first Update
// Light packet, plus the packet ID order of the first 160 play packets.
// Data captured here is the reference for exact chunk/light parity.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaWorld
func TestCaptureVanillaWorld(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla world packets")
	}
	tg := requireBlackbox(t)
	defer tg.cleanup()

	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2", "world")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	c, pr, _ := joinPlayBlackbox(t, tg.vanilla, "WorldProbe")
	defer c.Close()
	// The join burst is read to its end, and the shared client's deadline
	// is ten seconds from the dial: under load that is not enough for the
	// real server to finish generating and streaming the columns this
	// capture is waiting for, and the read that runs out reports a
	// timeout rather than a missing packet.
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))
	var order []byte
	gotChunk, gotLight := false, false
	for i := 0; i < 400; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("vanilla stream at %d: %v", i, err)
		}
		if len(order) < 160 {
			order = append(order, byte(p.ID))
		}
		switch p.ID {
		case server.IDPlayChunkData:
			if !gotChunk {
				if err := os.WriteFile(filepath.Join(dir, "chunk_0_0.bin"), p.Data, 0o644); err != nil {
					t.Fatal(err)
				}
				gotChunk = true
			}
		case server.IDPlayUpdateLight:
			if !gotLight {
				if err := os.WriteFile(filepath.Join(dir, "light_0_0.bin"), p.Data, 0o644); err != nil {
					t.Fatal(err)
				}
				gotLight = true
			}
		}
		if gotChunk && gotLight {
			break
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "join_order.bin"), order, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("captured chunk=%v light=%v order=%d packets", gotChunk, gotLight, len(order))
}
