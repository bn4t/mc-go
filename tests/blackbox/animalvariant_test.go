package blackbox

// TestAnimalVariantParity pins the small-animal variant chain on the
// wire: AgeableMob's shared pair leads every ageable pack (baby at 16,
// age_locked at 17 — subtype data only ever rides 18+), then the
// per-family variant slots measured on 26.2's defineSynchedData chain:
// Axolotl's DATA_VARIANT int at 18 + DATA_FROM_BUCKET bool at 20,
// Parrot's DATA_VARIANT_ID int at 20 (after TamableAnimal's 18/19),
// Rabbit's DATA_TYPE_ID int at 18, TropicalFish's packed variant int
// at 17 (Fish, not AgeableMob), Fox's DATA_TYPE_ID int at 18 plus the
// DATA_FLAGS_ID pose byte at 19 (0x01 sitting, 0x04 crouching, 0x20
// sleeping). /data merge lands the same reads — attach packs and dirty
// packs compare verbatim with the leading VarInt entity id stripped.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAnimalVariantParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("av%d", time.Now().UnixNano()%1_000_000)
	sel := func(ty string) string {
		return fmt.Sprintf("@e[type=minecraft:%s,tag=%s,limit=1]", ty, tag)
	}
	cmds := []string{
		// Bucket-caught cyan axolotl — variant int at 18 + the
		// from_bucket bool at 20.
		"summon minecraft:axolotl 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Variant:2,FromBucket:1b}",
		// Gray parrot — TamableAnimal's defaults stay unpacked; the
		// variant int rides 20.
		"summon minecraft:parrot 11.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Variant:4}",
		// Packed tropical fish variant at 17.
		"summon minecraft:tropical_fish 13.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Variant:655361}",
		// Killer bunny — RabbitType 99 lands at 18 and renames the
		// entity (optional-component name at 2).
		"summon minecraft:rabbit 15.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],RabbitType:99}",
		// Snow fox asleep — variant int at 18 + pose byte 0x20 at 19.
		"summon minecraft:fox 17.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Type:\"snow\",Sleeping:1b}",
		// Age-locked baby cow — the shared pair at 16/17 leads the
		// pack behind the NoAI mob-flags byte at 15.
		"summon minecraft:cow 19.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000,AgeLocked:1b}",
		// Diff merges: each emits only the touched slot.
		"data merge entity " + sel("axolotl") + " {Variant:4}",
		"data merge entity " + sel("parrot") + " {Variant:0}",
		"data merge entity " + sel("tropical_fish") + " {Variant:7}",
		"data merge entity " + sel("rabbit") + " {RabbitType:2}",
		"data merge entity " + sel("fox") + " {Sleeping:0b,Crouching:1b}",
		"data merge entity " + sel("fox") + " {Type:\"red\"}",
		"data merge entity " + sel("cow") + " {AgeLocked:0b}",
		"data merge entity " + sel("cow") + " {AgeLocked:1b}",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		// Ambient spawn cycles keep adding mobs whose metadata
		// updates would pollute the capture — freeze spawning first.
		sendCommand(t, pw, "gamerule doMobSpawning false")
		time.Sleep(200 * time.Millisecond)
		sendCommand(t, pw, "difficulty peaceful")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(550 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				// Only entity-metadata packets compare — ambient
				// entities' move packets batch into bundles and
				// carry no variant-parity signal.
				if strings.HasPrefix(line, "0x63:") {
					body := stripVarInt(line[5:])
					// Kill leftovers drop items/orbs whose index-8
					// metadata is rolled per kill. The air-supply
					// entry is stripped (ambient ticks and the
					// fish's drain jitter by a tick or two); a
					// packet left empty is ambient noise.
					if strings.HasPrefix(body, "\x08\x01") || strings.HasPrefix(body, "\x08\x07") {
						continue
					}
					if body = stripAirEntry(body); body == "" {
						continue
					}
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", body)))
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}

// stripAirEntry removes a leading index-1 air-supply entry from a
// stripped metadata body — the entry pairs into any packet the
// draining entity emits, so byte comparisons need it gone. Returns ""
// when nothing else remains.
func stripAirEntry(body string) string {
	if !strings.HasPrefix(body, "\x01\x01") || len(body) < 3 {
		return body
	}
	i := 2
	for i < len(body) && body[i]&0x80 != 0 {
		i++
	}
	i++ // past the varint's last byte
	if i >= len(body) {
		return ""
	}
	if body[i:] == "\xff" {
		return ""
	}
	return body[i:]
}
