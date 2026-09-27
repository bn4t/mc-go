package blackbox

// TestAgeableMetaParity pins the AgeableMob metadata layer on the wire:
// DATA_BABY_ID rides index 16 as a boolean for the animal family (pig,
// cow, sheep, chicken, wolf), the attach pack carries it, and a /data
// merge that flips Age through the baby threshold dirty-sends just the
// baby entry — vanilla's SynchedEntityData.packDirty emits exactly the
// changed entries, so a Health merge on a sheared sheep emits health
// alone, not the whole non-default pack.
//
// Entity ids differ between runs, so raw packet bodies compare with
// the leading VarInt entity id stripped.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAgeableMetaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("am%d", time.Now().UnixNano()%1_000_000)
	sel := func(ty string) string {
		return fmt.Sprintf("@e[type=minecraft:%s,tag=%s,limit=1]", ty, tag)
	}
	cmds := []string{
		// Adult pig, flip to baby via Age, health merge while baby
		// (dirty pack carries health alone), flip back to adult.
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"data merge entity " + sel("pig") + " {Age:-24000}",
		"data get entity " + sel("pig") + " IsBaby",
		"data merge entity " + sel("pig") + " {Health:3f}",
		"data get entity " + sel("pig") + " Health",
		"data merge entity " + sel("pig") + " {Age:0}",
		"data get entity " + sel("pig") + " IsBaby",
		// Baby cow at summon — the attach pack carries the flag.
		"summon minecraft:cow 11.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000}",
		"data get entity " + sel("cow") + " IsBaby",
		// Sheared sheep: a Health merge's dirty pack must NOT resend
		// the wool byte — only the health entry changed.
		"summon minecraft:sheep 13.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Sheared:1b}",
		"data merge entity " + sel("sheep") + " {Health:3f}",
		"data merge entity " + sel("sheep") + " {Age:-24000}",
		// Chicken, frog and villager baby attach packs.
		"summon minecraft:chicken 15.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000}",
		"summon minecraft:frog 17.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000}",
		"summon minecraft:villager 19.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"],Age:-24000}",
		"data merge entity " + sel("frog") + " {Age:0}",
		// A compound merge flipping both entries emits index-ascending.
		"data merge entity " + sel("pig") + " {Age:-24000,Health:2f}",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x63:") || strings.HasPrefix(line, "0x83:") {
					body := stripVarInt(line[5:])
					// Kill leftovers drop items/orbs whose index-8
					// metadata is rolled per kill.
					if strings.HasPrefix(body, "\x08\x01") || strings.HasPrefix(body, "\x08\x07") {
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
