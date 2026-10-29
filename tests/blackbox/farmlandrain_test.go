package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// Rain hydrates exposed farmland but cannot hydrate a roofed bed. Crops
// hold the dry bed as farmland after its moisture reaches zero.
func TestFarmlandRainHydrationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) { s.SimulationDistance = 2 })
	defer targets.cleanup()
	run := func(addr string) []bool {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(3 * time.Second)
		sendCommand(t, pw, "tp @s 44 -60 44")
		time.Sleep(3 * time.Second)
		for _, cmd := range []string{
			"gamerule random_tick_speed 0", "fillbiome 32 -64 32 60 -48 56 plains", "weather rain",
			"setblock 40 -60 44 farmland[moisture=0]", "setblock 40 -59 44 wheat[age=7]",
			"setblock 48 -60 44 farmland[moisture=7]", "setblock 48 -59 44 wheat[age=7]", "setblock 48 -57 44 stone",
		} {
			sendCommand(t, pw, cmd)
		}
		sendCommand(t, pw, "say FR-ready")
		ready, biomeOK := false, false
		setupDeadline := time.Now().Add(20 * time.Second)
		for !ready && time.Now().Before(setupDeadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, "commands.fillbiome.success") {
					biomeOK = true
				}
				if strings.Contains(line, "argument.pos.unloaded") || strings.Contains(line, "commands.fillbiome.toobig") {
					t.Logf("setup error: %s", line)
				}
				if strings.Contains(line, "FR-ready") {
					ready = true
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !ready || !biomeOK {
			t.Fatalf("farmland setup: barrier=%v biome fill succeeded=%v", ready, biomeOK)
		}
		sendCommand(t, pw, "gamerule random_tick_speed 512")
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sink.take()
		sendCommand(t, pw, "execute if block 40 -60 44 farmland[moisture=7] run say FR-exposed")
		sendCommand(t, pw, "execute if block 48 -60 44 farmland[moisture=0] run say FR-roofed")
		sendCommand(t, pw, "say FR-done")
		got := []bool{false, false}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, "FR-exposed") {
					got[0] = true
				}
				if strings.Contains(line, "FR-roofed") {
					got[1] = true
				}
				if strings.Contains(line, "FR-done") {
					return got
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("timed out probing farmland")
		return got
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo}} {
		got := run(target.addr)
		t.Logf("%s exposed hydrated=%v roofed dry=%v", target.name, got[0], got[1])
		if !got[0] || !got[1] {
			t.Errorf("%s hydration mismatch: %v", target.name, got)
		}
	}
}
