//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestMobFallDamageParity pins LivingEntity.causeFallDamage: mobs
// accumulate fallDistance on the way down and on landing take
// floor(fallDistance + 1e-6 - safe_fall_distance) fall damage
// (attributes from the supplier table), with the entity-anchored
// Fallsounds pair — hostile types get entity.hostile.*_fall,
// everyone else entity.generic.*_fall, damage > 4 picking "big" —
// and the #fall_damage_immune tag (golems, flyers, slime) skips it
// entirely.
func TestMobFallDamageParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	awaitKey := func(sink *chatSink, key string) bool {
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			for _, line := range sink.take() {
				if strings.Contains(line, key) {
					return true
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		return false
	}

	run := func(addr string) map[string]float64 {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "tp P 5 -60 4 0 0")
		sendCommand(t, pw, "time set 18000") // no daylight burn noise
		sendCommand(t, pw, "weather clear")

		fillb := func(cmd string) {
			for range 30 {
				sendCommand(t, pw, cmd)
				if awaitKey(sink, "fill.") {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("fill never landed: %s", cmd)
		}
		fillb("fill -2 -62 -2 12 -55 3 minecraft:air")
		fillb("fill -2 -62 -2 12 -62 3 minecraft:stone")

		// AI stays ON — vanilla gates travel() itself on
		// isEffectiveAi, so NoAI mobs never fall at all. Fall
		// distances are exact integers; P is creative so nothing
		// aggros onto her.
		cases := []struct {
			tag  string
			mob  string
			y    float64
			want float64 // Health after landing
			snd  int32   // expected fall sound holder id, 0 = none
		}{
			{"f4", "zombie", -57, 19, 874},  // 4 blocks, dmg 1, hostile small
			{"f10", "zombie", -51, 13, 871}, // 10 blocks, dmg 7, hostile big
			{"fp", "pig", -51, 3, 694},      // 10 blocks, dmg 7, generic big
			{"fc", "chicken", -51, 4, 0},    // fall_damage_immune tag
		}
		for i, c := range cases {
			sendCommand(t, pw, fmt.Sprintf(
				"summon minecraft:%s %d %v 0 {Tags:[\"%s\"]}",
				c.mob, i*3, c.y, c.tag))
			awaitKey(sink, "summon")
		}
		time.Sleep(1500 * time.Millisecond)

		out := map[string]float64{}
		// Harvest sound ids FIRST — take() drains, so the Health
		// queries' internal polls would otherwise eat the window.
		// Each line is "<hexid>:<raw bytes>"; the positional sound
		// packet = 0x75 and its first varint is the holder id
		// (registry id + 1).
		seen := map[int32]bool{}
		for _, l := range sink.take() {
			if !strings.HasPrefix(l, "0x75:") {
				continue
			}
			data := l[strings.IndexByte(l, ':')+1:]
			r := protocol.NewPacketReader([]byte(data))
			if id, err := r.VarInt(); err == nil {
				seen[id-1] = true
			}
		}
		for _, c := range cases {
			out[c.tag+".health"] = nbtFloatOf(t, sink, pw,
				"@e[tag="+c.tag+",limit=1]", "Health")
		}
		for _, c := range cases {
			if c.snd != 0 {
				out[c.tag+".snd"] = 0
				if seen[c.snd] {
					out[c.tag+".snd"] = 1
				}
			}
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for k, v := range van {
		if mc[k] != v {
			t.Errorf("%s: vanilla=%v mc-go=%v", k, v, mc[k])
		}
	}
	for k, v := range mc {
		if _, ok := van[k]; !ok {
			t.Errorf("%s: mc-go=%v (absent on vanilla)", k, v)
		}
	}
}
