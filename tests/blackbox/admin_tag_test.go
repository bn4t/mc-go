package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestAdminTagParity pins /tag and the operator commands /op /deop
// /kick /ban /pardon /ban-ip /pardon-ip against the real server: reply
// components and argument-error cursors, plus the disconnect packet a
// kicked or banned client receives.
func TestAdminTagParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tag P add ztag1`,
		`tag P add ztag2`,
		`tag P list`,
		`tag P remove ztag1`,
		`tag P list`,
		`tag P remove ztag1`,
		`tag P add ztag1`,
		`tag P add ztag1`,
		`tag P add`,
		`tag nosuchplayer add x`,
		`tag @e[type=minecraft:pig] add x`,
		`tag`,
		`tag P`,
		`tag P remove`,
		`tag P bogus x`,
		`op Q`,
		`op Q`,
		`deop Q`,
		`deop Q`,
		`ban nosuchplayer2`,
		`ban nosuchplayer2`,
		`pardon nosuchplayer2`,
		`pardon nosuchplayer2`,
		`ban-ip 1.2.3.4`,
		`ban-ip nosuchplayer`,
		`ban-ip 1.2.3.4 again`,
		`pardon-ip 1.2.3.4`,
		`pardon-ip 9.9.9.9`,
		`pardon-ip`,
		`ban`,
		`kick`,
		`op`,
		`deop`,
		`pardon`,
		`kick Q bye reason`,
		`ban Q`,
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "Q")
		defer c2.Close()
		_ = c2.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		sink2 := newChatSink(pr2, pw2)
		sink2.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sink2.take()
			func() {
				defer func() { recover() }()
				sendCommand(t, pw, cmd)
			}()
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			// Of Q's stream only the disconnect packet carries
			// command-visible content; the rest is join noise and
			// clocks.
			for _, line := range sink2.take() {
				i := strings.Index(line, ":")
				if i < 0 {
					continue
				}
				id, err := strconv.ParseInt(line[:i], 0, 32)
				if err != nil || id != 0x20 {
					continue
				}
				evs = append(evs, "Q:"+maskUUID(line))
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if len(want[i]) != len(got[i]) {
			t.Errorf("cmd %q: vanilla %v\n  mc-go %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("cmd %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}
}
