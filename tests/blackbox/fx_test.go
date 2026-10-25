package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestFxParity covers /playsound, /stopsound and /particle: success
// replies, the sound/stop_sound/level_particles packet streams (the
// random playsound seed is masked off), selector and bounds errors,
// and the option-key failures of data-carrying particles.
func TestFxParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tp @s 8.5 -60 8.5`,
		`playsound minecraft:block.stone.place`,
		`playsound minecraft:block.stone.place master`,
		`playsound minecraft:block.stone.place master @s`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 2`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 2 1.5`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 1 0`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 1 100`,
		`playsound minecraft:bogus.sound master @s`,
		`playsound minecraft:block.stone.place bogus @s`,
		`playsound minecraft:block.stone.place master nosuchplayer`,
		`playsound minecraft:block.stone.place master @e[type=minecraft:pig]`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ -1`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 0`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 2.5`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 1 x`,
		`playsound minecraft:block.stone.place master @s ~ ~ ~ 1 1 1 extra`,
		`stopsound`,
		`stopsound @s`,
		`stopsound @s master`,
		`stopsound @s master minecraft:block.stone.place`,
		`stopsound @s master minecraft:bogus`,
		`stopsound @s bogus`,
		`stopsound nosuchplayer`,
		`stopsound @s master minecraft:block.stone.place extra`,
		`particle minecraft:flame`,
		`particle minecraft:flame ~ ~ ~`,
		`particle minecraft:flame ~ ~ ~ 1 1 1`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10 force`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10 normal`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10 force @s`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10 force nosuchplayer`,
		`particle minecraft:bogus`,
		`particle minecraft:block minecraft:stone`,
		`particle minecraft:dust 1 0 0 1 ~ ~ ~`,
		`particle minecraft:flame x`,
		`particle minecraft:flame ~ ~ ~ x`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 x`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 x`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 10 bogus`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 -1 10`,
		`particle minecraft:flame ~ ~ ~ 1 1 1 0 -1`,
		`particle`,
		`playsound`,
		`stopsound @s all minecraft:block.stone.place`,
	}

	keep := func(line string) bool {
		return strings.HasPrefix(line, "0x2f:") ||
			strings.HasPrefix(line, "0x75:") ||
			strings.HasPrefix(line, "0x77:")
	}
	mask := func(line string) string {
		// playsound's trailing seed is random
		if strings.HasPrefix(line, "0x75:") && len(line) > 12 {
			return line[:len(line)-8] + "<seed>"
		}
		return line
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				} else if keep(line) {
					evs = append(evs, "pkt "+mask(line))
				}
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
