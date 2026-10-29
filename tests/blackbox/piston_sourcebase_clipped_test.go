package blackbox

import (
	"fmt"
	"math"
	"testing"
)

// Floor support isolates horizontal block clipping from the standing
// entities' ordinary gravity travel and the separate noPhysics probes.
func TestPistonSourceBaseClippedBodyParity(t *testing.T) {
	const player = "BaseClipReader"
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer tg.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -2 160 3")
		sendCommand(t, pw, "tick freeze")
		var out []float64
		// Literal axis goldens recorded from the native offset/wall baseline.
		goldens := map[string][9][2]float64{
			"armor_stand": {{-.01, -.26}, {1.01, 1.27}, {1.26, 1.27}, {1.27, 1.27}, {1.29, 1.27}, {-.01, -.26}, {1.01, 1.27}, {1.27, 1.27}, {1.01, 1.27}},
			"cow":         {{-.209999988, -.459999988}, {-.01, -.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}, {-.16, -.459999988}, {.14, -.37}, {1.459999988, 1.469999988}, {.959999988, 1.459999988}},
		}
		for _, kind := range []string{"armor_stand", "cow"} {
			for i, tc := range []struct {
				x    float64
				wall bool
			}{{.2, false}, {.5, false}, {.8, false}, {1.4, false}, {1.8, false}, {.35, false}, {.65, false}, {1.05, false}, {.5, true}} {
				initial := tc.x
				sendCommand(t, pw, "kill @e[type=item]")
				sendCommand(t, pw, "fill -3 161 -2 4 165 2 air")
				sendCommand(t, pw, "fill -3 161 -2 4 161 2 stone")
				sendCommand(t, pw, "setblock 0 162 0 piston[facing=east]")
				sendCommand(t, pw, "setblock 0 162 -1 redstone_block")
				shulkerStepTicks(t, pw, sink, 3)
				tag := fmt.Sprintf("BaseClip_%s_%d", kind, i)
				sel := "@e[type=" + kind + ",tag=" + tag + ",limit=1]"
				sendCommand(t, pw, fmt.Sprintf(`summon %s %.5f 162.0 0.5 {Tags:["%s"],NoAI:1b,Invulnerable:1b,PortalCooldown:20,Motion:[0d,0d,0d]}`, kind, initial, tag))
				warmed := false
				for warm := 0; warm < 3; warm++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, remaining := dataGetSel(t, sink, pw, sel, "PortalCooldown")
					if state == "query" && remaining < 20 {
						warmed = true
						break
					}
				}
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				if !warmed || !ok || x != initial || y != 162 || z != .5 {
					t.Fatalf("%s %s initial%v,%v,%v warmed%t ok%t", addr, tag, x, y, z, warmed, ok)
				}
				if tc.wall {
					sendCommand(t, pw, "setblock -1 162 0 stone")
				}
				sendCommand(t, pw, "setblock 0 162 -1 air")
				for phase := 1; phase <= 4; phase++ {
					shulkerStepTicks(t, pw, sink, 1)
					x, y, z, ok = dataGetPos(t, sink, pw, sel)
					if !ok {
						t.Fatal("missing body position")
					}
					parsed, parsedOK := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
					values, listOK := parsed.([]any)
					if !parsedOK || !listOK || len(values) != 3 {
						t.Fatal("missing body motion")
					}
					var motion [3]float64
					for j := range motion {
						var valid bool
						motion[j], valid = hrNum(values[j])
						if !valid {
							t.Fatal("invalid body motion")
						}
					}
					if want := goldens[kind][i][min(phase-1, 1)]; math.Abs(x-want) > 1e-6 {
						t.Errorf("%s %s case%d phase%d X%v want%v", addr, kind, i, phase, x, want)
					}
					if y != 162 || z != .5 || motion[0] != 0 || motion[2] != 0 {
						t.Errorf("%s %s phase%d transverse pos%v,%v,%v motion%v", addr, tag, phase, x, y, z, motion)
					}
					// Gravity's stored Y velocity is logged, not confused with the piston axis.
					t.Logf("%s %s start%.2f wall%t phase%d pos%.9f,%.9f,%.9f motion%v", addr, kind, initial, tc.wall, phase, x, y, z, motion)
					out = append(out, x, y, z, motion[0], motion[2])
				}
				sendCommand(t, pw, "tp "+sel+" 10 170 10")
				sendCommand(t, pw, "kill "+sel)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	for i := range v {
		if math.Abs(v[i]-g[i]) > 1e-6 {
			t.Errorf("observation%d vanilla%v mcgo%v", i, v[i], g[i])
		}
	}
}
