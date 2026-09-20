package blackbox

// Probe: why does vanilla emit no regen SetHealth after a void dip
// under /tick step? Reads the player's Health NBT and the natural
// regen gamerule after a 220-tick window to split "regen did not run"
// from "regen ran silently".

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func collectTextUntil(t *testing.T, pr *protocol.Reader, marker string, timeout time.Duration) []string {
	t.Helper()
	var out []string
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("text scan %s: %v", marker, err)
		}
		if bytes.Contains(p.Data, []byte("commands.data")) || bytes.Contains(p.Data, []byte("argument")) ||
			bytes.Contains(p.Data, []byte(marker)) {
			out = append(out, fmt.Sprintf("id=%x %q", p.ID, string(p.Data)))
		}
		if bytes.Contains(p.Data, []byte(marker)) {
			return out
		}
	}
	t.Fatalf("marker %s never arrived", marker)
	return nil
}

func TestHungerProbe(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ProbeV"),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, "ProbeV")
	defer c.Close()

	sendCommand(t, pw, "say READY")
	collectTextUntil(t, pr, "READY", 30*time.Second)
	sendCommand(t, pw, "gamemode survival @s")
	sendCommand(t, pw, "tick freeze")
	sendCommand(t, pw, "say FROZEN")
	for _, l := range collectTextUntil(t, pr, "FROZEN", 15*time.Second) {
		t.Logf("freeze: %s", l)
	}
	// Park frozen in the void for 3 wall seconds with no stepping:
	// any hits here are wall-clock driven, not step driven.
	sendCommand(t, pw, "tp @s 6 -140 2")
	sendCommand(t, pw, "say TPD")
	collectTextUntil(t, pr, "TPD", 15*time.Second)
	time.Sleep(3 * time.Second)
	sendCommand(t, pw, "data get entity @s Health")
	sendCommand(t, pw, "say VOIDPARK")
	for _, l := range collectTextUntil(t, pr, "VOIDPARK", 15*time.Second) {
		t.Logf("void park (frozen, no step): %s", l)
	}
	// Back up and wait frozen for 4 wall seconds: regen driven
	// wall-clock would heal here, step-driven would not.
	sendCommand(t, pw, "tp @s 6 -60 2")
	sendCommand(t, pw, "say BACK")
	collectTextUntil(t, pr, "BACK", 15*time.Second)
	time.Sleep(4 * time.Second)
	sendCommand(t, pw, "data get entity @s Health")
	sendCommand(t, pw, "say WALLWAIT")
	for _, l := range collectTextUntil(t, pr, "WALLWAIT", 15*time.Second) {
		t.Logf("wall wait (frozen, no step): %s", l)
	}
	// Now unfreeze and wait: regen should heal on the normal loop.
	sendCommand(t, pw, "tick unfreeze")
	sendCommand(t, pw, "say LIVE")
	collectTextUntil(t, pr, "LIVE", 15*time.Second)
	time.Sleep(4 * time.Second)
	sendCommand(t, pw, "data get entity @s Health")
	sendCommand(t, pw, "say LIVEWAIT")
	for _, l := range collectTextUntil(t, pr, "LIVEWAIT", 15*time.Second) {
		t.Logf("live wait (unfrozen): %s", l)
	}
}
