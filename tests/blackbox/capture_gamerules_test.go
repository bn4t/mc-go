package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// rulesProbeName is the scripted client both the capture and the parity
// test log in as. It is listed in ops.json so vanilla answers the
// op-gated packets the comparison needs.
const rulesProbeName = "RulesProbe"

// opsJSONFor builds a vanilla ops.json granting one offline-mode player
// full operator level.
func opsJSONFor(name string) string {
	u := server.OfflineUUID(name)
	return fmt.Sprintf(`[{"uuid":"%s","name":"%s","level":4,"bypassesPlayerLimit":false}]`,
		server.DashUUID(u), name)
}

// TestCaptureVanillaGameRules records the vanilla server's game-rule
// table by asking for it the way a client does: Client Status with
// action 2 (Request game rule values) makes the server reply with Game
// Rule Values. The captured bytes are the reference for mc-go's own
// table, so defaults never have to be guessed.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaGameRules
func TestCaptureVanillaGameRules(t *testing.T) {
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla game rules")
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(rulesProbeName),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, rulesProbeName)
	defer c.Close()
	// The join burst streams chunks before the reply lands; give the
	// read loop room beyond the helper's default deadline.
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))

	body, ok := requestGameRules(t, pr, pw)
	if !ok {
		t.Fatal("no Game Rule Values packet from vanilla")
	}
	dir := filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2", "world")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game_rules.bin"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("rule count: %v", err)
	}
	for j := int32(0); j < n; j++ {
		name, err := r.String()
		if err != nil {
			t.Fatalf("rule %d name: %v", j, err)
		}
		val, err := r.String()
		if err != nil {
			t.Fatalf("rule %d value: %v", j, err)
		}
		t.Logf("%q: %q,", name, val)
	}
	t.Logf("captured %d game rules", n)
}

// requestGameRules sends Client Status action 2 and returns the body of
// the Game Rule Values reply, keeping the session alive while the join
// burst drains (vanilla drops a client that never answers keep-alives
// or chunk batches).
func requestGameRules(t *testing.T, pr *protocol.Reader, pw *protocol.Writer) ([]byte, bool) {
	t.Helper()
	var req protocol.Buffer
	req.VarInt(2) // request game rule values
	if err := pw.WritePacket(server.IDPlayClientStatusSB, req.Bytes()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8000; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("stream at %d: %v", i, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.Long(); err == nil {
				var b protocol.Buffer
				b.Long(id)
				if err := pw.WritePacket(server.IDPlayKeepAliveSB, b.Bytes()); err != nil {
					t.Fatalf("keep-alive echo: %v", err)
				}
			}
		case server.IDPlayChunkBatchDone:
			var b protocol.Buffer
			b.Float(25)
			if err := pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes()); err != nil {
				t.Fatalf("chunk batch ack: %v", err)
			}
		case server.IDPlayGameRuleValuesCB:
			return p.Data, true
		}
	}
	return nil, false
}
