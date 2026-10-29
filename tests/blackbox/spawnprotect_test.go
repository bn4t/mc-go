//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestSpawnProtectionParity drives a non-op survival player beside an
// op: with the world spawn at 0 -60 0 and the default radius 16, a
// placement and a dig at |dx| 0 and 16 are refused (the red
// build.spawn_protection overlay plus the use's two block updates),
// while |dx| 17 and 30 place and dig normally.
func TestSpawnProtectionParity(t *testing.T) {
	ops := opsJSONFor("Admin")
	mDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mDir, "ops.json"), []byte(ops), 0o644); err != nil {
		t.Fatal(err)
	}
	tg := requireBlackboxFiles(t, map[string]string{"spawn-protection": "16"}, map[string]string{"ops.json": ops}, func(s *server.Server) {
		s.SpawnProtection = 16
		s.ServerDir = mDir
		s.OpsRequired = true
		if err := s.LoadAdminFiles(); err != nil {
			t.Errorf("load admin files: %v", err)
		}
	})
	defer tg.cleanup()
	xs := []int32{0, 16, 17, 30}
	run := func(addr string) []string {
		ac, apr, apw := joinPlayQuiet(t, addr, "Admin")
		defer ac.Close()
		_ = ac.SetDeadline(time.Time{})
		asink := newChatSinkTP(apr, apw)
		_ = apw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, apw, "setworldspawn 0 -60 0")
		sendCommand(t, apw, "gamerule respawn_radius 0")
		sendCommand(t, apw, "gamerule random_tick_speed 0")
		time.Sleep(time.Second)
		vc, vpr, vpw := joinPlayQuiet(t, addr, "Visitor")
		defer vc.Close()
		_ = vc.SetDeadline(time.Time{})
		sink := newChatSinkTP(vpr, vpw)
		sink.cap = 1 << 16
		_ = vpw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		time.Sleep(2 * time.Second)
		sendCommand(t, apw, "gamemode survival Visitor")
		sendCommand(t, apw, "item replace entity Visitor hotbar.0 with minecraft:stone 8")
		time.Sleep(3 * time.Second)
		asink.take()
		var out []string
		for i, x := range xs {
			sendCommand(t, apw, fmt.Sprintf("tp Visitor %d.5 -60 -1.5 0 60", x))
			time.Sleep(1500 * time.Millisecond)
			sink.take()
			sendUseItemOn(t, vpw, x, -61, 0, 1, int32(100+i))
			time.Sleep(500 * time.Millisecond)
			sendDig(t, vpw, 0, x, -61, 1, int32(200+i))
			time.Sleep(1500 * time.Millisecond)
			sendDig(t, vpw, 2, x, -61, 1, int32(300+i))
			time.Sleep(1500 * time.Millisecond)
			overlays := 0
			updates := map[[3]int32]int32{}
			for _, l := range sink.take() {
				if strings.HasPrefix(l, fmt.Sprintf("%#x:", server.IDPlaySystemChatCB)) && strings.Contains(l, "build.spawn_protection") {
					overlays++
				}
				collectBlockUpdates(l, updates)
			}
			var cells []string
			for p, st := range updates {
				cells = append(cells, fmt.Sprintf("%d,%d,%d=%s", p[0]-x, p[1], p[2], stateLabel(st)))
			}
			sort.Strings(cells)
			out = append(out, fmt.Sprintf("dx=%d overlays=%d %s", x, overlays, strings.Join(cells, " ")))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	for i := range xs {
		if want[i] != got[i] {
			t.Errorf("case %d:\n  vanilla %s\n  mc-go   %s", i, want[i], got[i])
		} else {
			t.Logf("%s", got[i])
		}
	}
}

