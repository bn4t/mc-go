//go:build blackbox

package blackbox

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestAdminFilesParity boots vanilla and mc-go with a shared seeded
// ops.json, runs the admin commands in game, and diffs the
// ops.json / whitelist.json / banned-*.json files each server wrote —
// the byte shape is vanilla's, so the files must agree.
func TestAdminFilesParity(t *testing.T) {
	jar := blackboxJar(t)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}

	vDir, mDir := t.TempDir(), t.TempDir()
	ops := opsJSONFor("P")
	for _, d := range []string{vDir, mDir} {
		if err := os.WriteFile(filepath.Join(d, "ops.json"), []byte(ops), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vAddr, stopVanilla := startVanillaInDir(t, jar, vPort, nil, nil, vDir)
	defer stopVanilla()
	mAddr := startMcGoCfg(t, mPort, func(s *server.Server) {
		s.ServerDir = mDir
		s.OpsRequired = true
		if err := s.LoadAdminFiles(); err != nil {
			t.Errorf("load admin files: %v", err)
		}
	})

	stampRe := regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4}`)

	// run drives one server: P and Q join, P runs the admin command
	// list, then a fresh R login must be rejected as not whitelisted.
	run := func(addr string) map[string]string {
		pc, pr, pw := joinPlayQuiet(t, addr, "P")
		defer pc.Close()
		qc, _, _ := joinPlayQuiet(t, addr, "Q")
		defer qc.Close()
		_ = pc.SetDeadline(time.Time{})
		_ = qc.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range []string{
			"whitelist on",
			"whitelist add P",
			"whitelist add Q",
			"whitelist remove Q",
			"op Q",
			"ban-ip 10.1.2.3 botnet",
			"ban-ip 10.9.8.7",
			"pardon-ip 10.9.8.7",
			"ban Q bad actor",
		} {
			sendCommand(t, pw, cmd)
			time.Sleep(600 * time.Millisecond)
			sink.take()
		}
		time.Sleep(500 * time.Millisecond)

		// R is not whitelisted: the login must be rejected.
		rc, rpr, _ := loginStart(t, addr, "R")
		defer rc.Close()
		var dc string
		for i := 0; i < 4; i++ {
			p, err := rpr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDLoginDisconnect {
				r := protocol.NewPacketReader(p.Data)
				dc, _ = r.String()
				break
			}
		}
		out := map[string]string{"r_disconnect": dc}
		return out
	}

	van := run(vAddr)
	got := run(mAddr)
	if van["r_disconnect"] != got["r_disconnect"] {
		t.Errorf("R login disconnect:\n  vanilla %s\n  mc-go   %s", van["r_disconnect"], got["r_disconnect"])
	}
	if van["r_disconnect"] == "" {
		t.Error("vanilla did not reject the unwhitelisted login")
	}

	for _, name := range []string{"ops.json", "whitelist.json", "banned-players.json", "banned-ips.json"} {
		vb, verr := os.ReadFile(filepath.Join(vDir, name))
		mb, merr := os.ReadFile(filepath.Join(mDir, name))
		if verr != nil {
			t.Errorf("vanilla %s: %v", name, verr)
			continue
		}
		if merr != nil {
			t.Errorf("mc-go %s: %v", name, merr)
			continue
		}
		v := stampRe.ReplaceAllString(string(vb), "TS")
		m := stampRe.ReplaceAllString(string(mb), "TS")
		if v != m {
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", name, v, m)
		}
	}
}
