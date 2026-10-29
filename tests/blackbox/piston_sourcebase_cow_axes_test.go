package blackbox

import (
	"fmt"
	"math"
	"testing"
)

func TestPistonSourceBaseCowAxesParity(t *testing.T) {
	const player = "BaseCowReader"
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
		// Literal native observations; each leg independently satisfies these.
		goldens := map[string][6][2]float64{
			"east":  {{-0.209999988, -0.459999988}, {-0.010000000, -0.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}, {2.800000000, 2.800000000}},
			"west":  {{1.209999988, 1.459999988}, {1.010000000, 1.459999988}, {-0.209999988, -0.469999988}, {-0.469999988, -0.469999988}, {-0.469999988, -0.469999988}, {-1.800000000, -1.800000000}},
			"up":    {{160.990000012, 160.590000024}, {161.290000012, 160.780000012}, {161.590000012, 161.080000012}, {163.010000000, 163.020000000}, {163.020000000, 163.020000000}, {164.100000012, 164.100000012}},
			"down":  {{162.610000012, 163.010000000}, {162.310000012, 162.820000012}, {162.010000012, 162.520000012}, {160.590000024, 160.580000024}, {160.580000024, 160.580000024}, {159.500000012, 159.500000012}},
			"south": {{-0.209999988, -0.459999988}, {-0.010000000, -0.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}, {2.800000000, 2.800000000}},
			"north": {{1.209999988, 1.459999988}, {1.010000000, 1.459999988}, {-0.209999988, -0.469999988}, {-0.469999988, -0.469999988}, {-0.469999988, -0.469999988}, {-1.800000000, -1.800000000}},
		}
		var out []float64
		cases := []float64{.2, .5, .8, 1.4, 1.8, 2.8}

		dirs := []struct {
			name string
			axis int
			sign float64
		}{{"east", 0, 1}, {"west", 0, -1}, {"up", 1, 1}, {"down", 1, -1}, {"south", 2, 1}, {"north", 2, -1}}

		for _, dir := range dirs {
			for i, tc := range cases {
				sendCommand(t, pw, "kill @e[type=item]")
				sendCommand(t, pw, "fill -3 159 -3 3 166 3 air")
				sendCommand(t, pw, fmt.Sprintf("setblock 0 162 0 piston[facing=%s]", dir.name))
				power := [3]int{0, 162, -1}
				if dir.axis == 2 {
					power = [3]int{-1, 162, 0}
				}
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d redstone_block", power[0], power[1], power[2]))
				shulkerStepTicks(t, pw, sink, 3)
				initial := [3]float64{.5, 162.375, .5}
				local := tc
				if dir.sign < 0 {
					local = 1 - local
				}
				initial[dir.axis] = local
				if dir.axis == 1 {
					initial[1] += 162 - float64(float32(1.4))/2
				}
				tag := fmt.Sprintf("BaseCow_%s_%d", dir.name, i)
				sel := "@e[type=cow,tag=" + tag + ",limit=1]"
				sendCommand(t, pw, fmt.Sprintf(`summon cow %.9f %.9f %.9f {Tags:["%s"],NoGravity:1b,NoAI:1b,PortalCooldown:20,Motion:[0d,0d,0d]}`, initial[0], initial[1], initial[2], tag))
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
				if !warmed || !ok || math.Abs(x-initial[0]) > 1e-7 || math.Abs(y-initial[1]) > 1e-7 || math.Abs(z-initial[2]) > 1e-7 {
					t.Fatalf("%s case%d initial%v,%v,%v warmed%t ok%t", addr, i, x, y, z, warmed, ok)
				}
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d air", power[0], power[1], power[2]))
				for phase := 1; phase <= 4; phase++ {
					shulkerStepTicks(t, pw, sink, 1)
					x, y, z, ok = dataGetPos(t, sink, pw, sel)
					if !ok {
						t.Fatal("missing cow position")
					}
					parsed, parsedOK := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
					values, listOK := parsed.([]any)
					if !parsedOK || !listOK || len(values) != 3 {
						t.Fatal("missing cow motion")
					}
					var motion [3]float64
					for j := range motion {
						var valid bool
						motion[j], valid = hrNum(values[j])
						if !valid {
							t.Fatal("invalid cow motion")
						}
					}
					position := [3]float64{x, y, z}
					if want := goldens[dir.name][i][min(phase-1, 1)]; math.Abs(position[dir.axis]-want) > 1e-6 {
						t.Errorf("%s %s case%d phase%d axis%v want%v", addr, dir.name, i, phase, position[dir.axis], want)
					}

					for axis := range position {
						if (axis != dir.axis || i == len(cases)-1) && math.Abs(position[axis]-initial[axis]) > 1e-7 {
							t.Errorf("%s %s case%d phase%d unexpectedaxis%d pos%v initial%v", addr, dir.name, i, phase, axis, position, initial)
						}
					}
					if motion != [3]float64{} {
						t.Errorf("%s %s case%d phase%d motion%v", addr, dir.name, i, phase, motion)
					}
					t.Logf("%s %s start%.2f phase%d pos%.9f,%.9f,%.9f motion%v", addr, dir.name, tc, phase, x, y, z, motion)
					out = append(out, x, y, z, motion[0], motion[1], motion[2])
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
