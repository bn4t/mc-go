//go:build blackbox

package blackbox

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestEnderEyeThrowParity throws ender eyes in a generated world (seed
// 12345, structures on): EnderEyeItem.use locates the nearest stronghold
// from the player's block, spawns the eye at mid height and signals it
// (a far target becomes 12 blocks toward it and 8 up); EyeOfEnder.tick
// steers it. With ticks frozen and stepped, the eye's spawn point, its
// position and motion after 20 ticks and the held count are compared.
func TestEnderEyeThrowParity(t *testing.T) {
	const seed = 12345
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(seed),
		"generate-structures": "true",
		"view-distance":       "2",
	}
	tg := requireBlackboxFiles(t, props, map[string]string{"ops.json": opsJSONFor("EyeProbe")}, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.GenerateStructures = true
		s.ViewDistance = 2
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	spots := [][3]int{{500, 160, -300}, {-1200, 160, 2100}, {40, 160, 40}}
	num := regexp.MustCompile(`-?\d+\.\d+`)
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "EyeProbe")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamemode creative @s")
		time.Sleep(time.Second)
		eyeType, _ := server.EntityTypeID("eye_of_ender")
		var out []string
		wait := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(120 * time.Second)
			for time.Now().Before(deadline) {
				for _, l := range sink.take() {
					if strings.Contains(l, marker) {
						return lines
					}
					lines = append(lines, l)
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("%s: marker %s never arrived", addr, marker)
			return nil
		}
		for i, sp := range spots {
			sendCommand(t, pw, "tick unfreeze")
			sendCommand(t, pw, fmt.Sprintf("tp @s %d.5 %d %d.5 0 0", sp[0], sp[1], sp[2]))
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:ender_eye 4")
			time.Sleep(3 * time.Second)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, fmt.Sprintf("say READY%dX", i))
			wait(fmt.Sprintf("READY%dX", i))
			sendUseItemAim(t, pw, 0, int32(500+i), 0, -10)
			time.Sleep(time.Second)
			sendCommand(t, pw, fmt.Sprintf("say THROWN%dX", i))
			spawn := "none"
			for _, l := range wait(fmt.Sprintf("THROWN%dX", i)) {
				if strings.HasPrefix(l, "0x1:") {
					if s, ok := dfDecodeSpawn([]byte(l[4:])); ok && s.typ == eyeType {
						spawn = fmt.Sprintf("(%.3f,%.3f,%.3f)", s.x-float64(sp[0]), s.y-float64(sp[1]), s.z-float64(sp[2]))
					}
				}
			}
			sendCommand(t, pw, "tick step 20")
			time.Sleep(3 * time.Second)
			sink.take()
			field := func(path string) string {
				sendCommand(t, pw, "data get entity @e[type=minecraft:eye_of_ender,limit=1] "+path)
				sendCommand(t, pw, fmt.Sprintf("say F%s%dX", path, i))
				for _, l := range wait(fmt.Sprintf("F%s%dX", path, i)) {
					if strings.Contains(l, "commands.data.entity.query") {
						var vs []string
						for _, m := range num.FindAllString(l, -1) {
							var f float64
							fmt.Sscanf(m, "%g", &f)
							vs = append(vs, fmt.Sprintf("%.3f", f))
						}
						return strings.Join(vs, ",")
					}
					if strings.Contains(l, "argument.entity.notfound") {
						return "no eye"
					}
				}
				return "?"
			}
			pos, motion := field("Pos"), field("Motion")
			sendCommand(t, pw, "kill @e[type=minecraft:eye_of_ender]")
			sendCommand(t, pw, "clear @s minecraft:ender_eye 0")
			sendCommand(t, pw, fmt.Sprintf("say C%dX", i))
			held := "?"
			for _, l := range wait(fmt.Sprintf("C%dX", i)) {
				if strings.Contains(l, "commands.clear.test") {
					held = fmt.Sprintf("%q", l[strings.Index(l, "commands.clear.test"):])
					held = held[:min(len(held), 40)]
				}
			}
			out = append(out, fmt.Sprintf("spawn=%s pos=%s motion=%s held=%s", spawn, pos, motion, held))
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	for i := range spots {
		if want[i] != got[i] {
			t.Errorf("spot %v:\n  vanilla %s\n  mc-go   %s", spots[i], want[i], got[i])
		} else {
			t.Logf("spot %v: %s", spots[i], got[i])
		}
	}
}

