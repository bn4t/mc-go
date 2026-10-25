package blackbox

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

// TestAngerNBTParity pins NeutralMob's persistent-anger save keys:
// anger_end_time is NO_ANGER_END_TIME (-1) while unarmed and the
// absolute game-time end once the target goal arms it; angry_at rides
// only while a provocation target exists (EntityReference -> UUID
// [I;4]). Measured emission set: zombified_piglin, bee, enderman,
// wolf, polar_bear, iron_golem — piglin/hoglin/zoglin do NOT emit.
func TestAngerNBTParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("Angry")}, nil)
	defer targets.cleanup()

	numRe := regexp.MustCompile(`-?\d+`)
	lastInts := func(line string, n int) []string {
		all := numRe.FindAllString(line, -1)
		if len(all) < n {
			return nil
		}
		return all[len(all)-n:]
	}

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla},
		{"mcgo", targets.mcgo},
	} {
		c, pr, pw := joinPlayBlackbox(t, s.addr, "Angry")
		defer c.Close()
		sendClientInformation(t, pw)
		time.Sleep(500 * time.Millisecond)
		drainChunkWindow(t, s.label, c, pr, pw, 300*time.Millisecond)
		sink := newChatSink(pr, pw)

		// Unarmed: every NeutralMob type reads anger_end_time -1L,
		// no angry_at; zoglin emits neither key.
		for _, mob := range []string{
			"zombified_piglin", "bee", "enderman", "wolf",
			"polar_bear", "zoglin",
		} {
			sendCommand(t, pw,
				fmt.Sprintf("summon minecraft:%s ~ ~ ~ {NoAI:1b}", mob))
		}
		// The iron golem arms universal anger at monsters even under
		// NoAI (updatePersistentAnger isn't goal-gated for it) — probe
		// it 60 blocks up, out of every follow_range.
		sendCommand(t, pw, "summon minecraft:iron_golem ~ ~60 ~ {NoAI:1b}")
		time.Sleep(500 * time.Millisecond)
		for _, mob := range []string{
			"zombified_piglin", "bee", "enderman", "wolf",
			"polar_bear", "iron_golem",
		} {
			sel := fmt.Sprintf("@e[type=minecraft:%s,limit=1]", mob)
			l := dataGetLine(t, sink, pw, sel, "anger_end_time")
			if v := lastInts(l, 1); len(v) != 1 || v[0] != "-1" {
				t.Errorf("%s: %s unarmed anger_end_time = %q, want -1", s.label, mob, tailStr(l))
			}
			if l := dataGetLine(t, sink, pw, sel, "angry_at"); l != "" {
				t.Errorf("%s: %s unarmed angry_at present = %q", s.label, mob, tailStr(l))
			}
		}
		if l := dataGetLine(t, sink, pw, "@e[type=minecraft:zoglin,limit=1]", "anger_end_time"); l != "" {
			t.Errorf("%s: zoglin emitted anger_end_time = %q", s.label, tailStr(l))
		}

		// Armed: a live (AI-on) piglin provoked by the player arms
		// persistent anger inside a few ticks — angry_at reads the
		// player's UUID as [I;4], anger_end_time goes positive.
		sendCommand(t, pw, "kill @e[type=minecraft:zombified_piglin]")
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "summon minecraft:zombified_piglin ~4 ~ ~")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw,
			"damage @e[type=minecraft:zombified_piglin,limit=1] 1 minecraft:mob_attack by @s")

		zpSel := "@e[type=minecraft:zombified_piglin,limit=1]"
		var atLine, endLine string
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && atLine == "" {
			atLine = dataGetLine(t, sink, pw, zpSel, "angry_at")
			if atLine == "" {
				time.Sleep(200 * time.Millisecond)
			}
		}
		endLine = dataGetLine(t, sink, pw, zpSel, "anger_end_time")
		if atLine == "" {
			t.Errorf("%s: provoked zombified_piglin never emitted angry_at", s.label)
		}
		if v := lastInts(endLine, 1); len(v) != 1 || v[0] == "-1" {
			t.Errorf("%s: provoked zombified_piglin anger_end_time = %q, want positive", s.label, tailStr(endLine))
		}
		// angry_at is the provoking player's UUID int array.
		uuidLine := dataGetLine(t, sink, pw, "@s", "UUID")
		if want, got := lastInts(uuidLine, 4), lastInts(atLine, 4); len(want) != 4 || !equalStrs(want, got) {
			t.Errorf("%s: angry_at ints %v != player UUID ints %v", s.label, got, want)
		}
		sendCommand(t, pw, "kill @e[type=minecraft:zombified_piglin]")
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tailStr(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}
