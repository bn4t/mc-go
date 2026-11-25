package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestDispenserMobBucketParity pins MobBucketItem through the dispenser:
// the water lands at the mouth, the mob spawns at that cell's bottom
// center, and the dispenser keeps the empty bucket. The player-use half
// of the same behavior is covered by TestBucketParity's cod/axolotl/
// tadpole cases.
func TestDispenserMobBucketParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("DB"),
	}, nil)
	defer tg.cleanup()

	codType, _ := server.EntityTypeID("cod")

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "DB")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		sink := newChatSinkTP(pr, pw)
		take := func() []string { return sink.take() }
		var lines []string
		collect := func() {
			lines = append(lines, take()...)
		}
		waitFor := func(sel, marker string) string {
			end := time.Now().Add(20 * time.Second)
			for time.Now().Before(end) {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say %s", sel, marker))
				deadline := time.Now().Add(700 * time.Millisecond)
				for time.Now().Before(deadline) {
					for _, l := range take() {
						lines = append(lines, l)
						if strings.Contains(l, marker) {
							return "hit"
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			return "miss"
		}
		// Wait for the join burst and a first command round trip.
		out := map[string]string{}
		if waitFor("entity @s", "DBREADY") != "hit" {
			t.Fatalf("%s: commands never answered", addr)
		}
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "setblock 8 -60 8 minecraft:dispenser[facing=east]")
		sendCommand(t, pw, "item replace block 8 -60 8 container.0 with minecraft:cod_bucket")
		collect()
		sendCommand(t, pw, "setblock 8 -60 7 minecraft:redstone_block")
		// Dispense tick is +4; the spawn burst and the water write land
		// inside this window.
		time.Sleep(1500 * time.Millisecond)
		collect()
		if aniSpawnID(lines, codType, 9.5, 8.5) >= 0 {
			out["cod"] = "hit"
		} else {
			out["cod"] = "miss"
		}
		out["water"] = waitFor("block 9 -60 8 minecraft:water", "DBWATER")
		out["bucket"] = waitFor("items block 8 -60 8 container.0 minecraft:bucket", "DBBUCKET")
		return out
	}

	van := run(tg.vanilla)
	mc := run(tg.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for _, k := range []string{"cod", "water", "bucket"} {
		if van[k] != "hit" {
			t.Errorf("vanilla %s=%s, want hit", k, van[k])
		}
		if mc[k] != "hit" {
			t.Errorf("mc-go %s=%s, want hit", k, mc[k])
		}
	}
}
