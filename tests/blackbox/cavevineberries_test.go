package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// Berry-bearing cave vine heads must roll berries independently for their
// offspring. Age 24 heads grow once and stop at 25, isolating that roll.
// Require at least 16 grown tips; the chance that all inherit fruit by a
// legitimate independent draw is below 0.11^16.
func TestCaveVineBerryOffspringParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"simulation-distance": "3"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) { s.SimulationDistance = 3 })
	defer targets.cleanup()
	run := func(addr string) (int, bool) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(3 * time.Second)
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, "tp @s 44 -60 44")
		for i := 0; i < 32; i++ {
			x, z := 36+i%8*2, 36+i/8*2
			sendCommand(t, pw, fmt.Sprintf("setblock %d -55 %d stone", x, z))
			sendCommand(t, pw, fmt.Sprintf("setblock %d -56 %d cave_vines[age=24,berries=true]", x, z))
		}
		sendCommand(t, pw, "gamerule random_tick_speed 128")
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sink.take()
		for i := 0; i < 32; i++ {
			x, z := 36+i%8*2, 36+i/8*2
			sendCommand(t, pw, fmt.Sprintf("execute if block %d -57 %d cave_vines[age=25] run say CV-grown-%d", x, z, i))
			sendCommand(t, pw, fmt.Sprintf("execute if block %d -57 %d cave_vines[berries=false] run say CV-unlit", x, z))
		}
		sendCommand(t, pw, "say CV-done")
		grown, unlit := 0, false
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, "CV-grown-") {
					grown++
				}
				if strings.Contains(line, "CV-unlit") {
					unlit = true
				}
				if strings.Contains(line, "CV-done") {
					return grown, unlit
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("timed out waiting for cave-vine probes")
		return grown, unlit
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo}} {
		grown, unlit := run(target.addr)
		t.Logf("%s: grown=%d, independently unlit offspring=%v", target.name, grown, unlit)
		if grown < 16 || !unlit {
			t.Errorf("%s: want at least 16 grown tips including berry-free offspring", target.name)
		}
	}
}
