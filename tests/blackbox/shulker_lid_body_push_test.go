package blackbox

import (
	"fmt"
	"math"
	"testing"
)

func TestShulkerLidBodyPushParity(t *testing.T) {
	const actor, observer = "LidBodyPusher", "LidBodyReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor, observer)}, nil)
	defer targets.cleanup()
	type direction struct {
		name string
		axis int
		sign float64
		wall bool
	}
	dirs := []direction{{"east", 0, 1, false}, {"west", 0, -1, false}, {"up", 1, 1, false}, {"down", 1, -1, false}, {"south", 2, 1, false}, {"north", 2, -1, false}, {"east", 0, 1, true}}
	type experiment struct {
		kind   string
		marker bool
		dir    direction
	}
	var cases []experiment
	for _, kind := range []string{"armor_stand", "cow"} {
		for _, dir := range dirs {
			cases = append(cases, experiment{kind: kind, dir: dir})
		}
	}
	cases = append(cases, experiment{kind: "armor_stand", marker: true, dir: dirs[0]})
	run := func(addr string) []float64 {
		driver := newClickDriverOn(t, addr, actor, "minecraft:shulker_box", 20, nil, nil)
		driver.close()
		sendCommand(t, driver.pw, "gamemode creative @s")
		sendCommand(t, driver.pw, "tp @s -2 160 2")
		conn, pr, pw := joinPlayBlackbox(t, addr, observer)
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 4 160 3")
		sendCommand(t, pw, "tick freeze")
		// Literal native axis coordinates; each server must satisfy these.
		goldens := [15][10]float64{
			{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
			{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
			{163.110000001, 163.220000003, 163.330000012, 163.440000006, 163.550000000, 163.660000024, 163.770000048, 163.880000072, 163.990000095, 164.100000000},
			{159.914999975, 159.804999973, 159.694999964, 159.584999970, 159.474999976, 159.364999952, 159.254999928, 159.144999904, 159.034999881, 158.924999976},
			{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
			{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
			{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
			{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
			{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
			{163.110000001, 163.220000003, 163.330000012, 163.440000006, 163.550000000, 163.660000024, 163.770000048, 163.880000072, 163.990000095, 164.100000000},
			{160.490000023, 160.380000021, 160.270000012, 160.160000018, 160.050000024, 159.940000000, 159.829999976, 159.719999952, 159.609999929, 159.500000024},
			{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
			{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
			{1.360000001, 1.470000003, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012},
			{1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000},
		}
		var out []float64
		for ci, tc := range cases {
			dir := tc.dir
			sendCommand(t, pw, "fill -4 159 -4 5 166 5 air")
			sendCommand(t, pw, fmt.Sprintf("setblock 0 162 0 shulker_box[facing=%s]", dir.name))
			if dir.wall {
				sendCommand(t, pw, "setblock 2 162 0 stone")
			}
			near := [3]float64{.5, 162.5, .5}
			if dir.axis == 1 {
				height := float64(float32(1.975))
				if tc.kind == "cow" {
					height = float64(float32(1.4))
				}
				near[1] = 163
				if dir.sign < 0 {
					near[1] = 162 - height
				}
			} else {
				near[dir.axis] = .5 + dir.sign*.75
			}
			points := [][3]float64{near}
			labels := []string{"near"}
			if dir.name == "east" && !dir.wall && !tc.marker {
				far, behind := near, near
				far[0] += 2.5
				behind[0] -= 1.5
				points = append(points, far, behind)
				labels = append(labels, "far", "behind")
			}
			selectors := make([]string, len(points))
			for i, p := range points {
				tag := fmt.Sprintf("LidBody_%d_%s", ci, labels[i])
				selectors[i] = "@e[type=" + tc.kind + ",tag=" + tag + ",limit=1]"
				extra := ""
				if tc.marker {
					extra = ",Marker:1b"
				}
				sendCommand(t, pw, fmt.Sprintf(`summon %s %.9f %.9f %.9f {Tags:["%s"],NoGravity:1b,NoAI:1b,PortalCooldown:20,Motion:[0d,0d,0d]%s}`, tc.kind, p[0], p[1], p[2], tag, extra))
			}
			for i, sel := range selectors {
				warmed := false
				for warm := 0; warm < 3; warm++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, n := dataGetSel(t, sink, pw, sel, "PortalCooldown")
					if state == "query" && n < 20 {
						warmed = true
						break
					}
				}
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				p := points[i]
				if !warmed || !ok || math.Abs(x-p[0]) > 1e-7 || math.Abs(y-p[1]) > 1e-7 || math.Abs(z-p[2]) > 1e-7 {
					t.Fatalf("%s case%d %s invalid initial%v,%v,%v warmed%t", addr, ci, labels[i], x, y, z, warmed)
				}
			}
			driver.reopen(0, 162, 0, 70, "lid-body-open")
			if !judgeUse(driver.opened).open {
				t.Fatal("shulker failed to open")
			}
			var last [3]float64
			for phase := 1; phase <= 10; phase++ {
				shulkerStepTicks(t, pw, sink, 1)
				x, y, z, ok := dataGetPos(t, sink, pw, selectors[0])
				if !ok {
					t.Fatal("missing body position")
				}
				last = [3]float64{x, y, z}
				if want := goldens[ci][phase-1]; math.Abs(last[dir.axis]-want) > 1e-6 {
					t.Errorf("%s case%d phase%d axis%v want%v", addr, ci, phase, last[dir.axis], want)
				}
				for axis := range last {
					if (axis != dir.axis || tc.marker) && math.Abs(last[axis]-near[axis]) > 1e-7 {
						t.Errorf("%s case%d phase%d unexpected axis%d pos%v initial%v", addr, ci, phase, axis, last, near)
					}
				}
				t.Logf("%s case%d %s %s wall%t marker%t phase%d pos%.9f,%.9f,%.9f", addr, ci, tc.kind, dir.name, dir.wall, tc.marker, phase, x, y, z)
				out = append(out, x, y, z)
			}
			parsed, ok := parseSNBT(dataGetLine(t, sink, pw, selectors[0], "Motion"))
			values, listOK := parsed.([]any)
			if !ok || !listOK || len(values) != 3 {
				t.Fatal("missing body Motion")
			}
			for _, v := range values {
				n, valid := hrNum(v)
				if !valid || n != 0 {
					t.Errorf("%s case%d injected Motion%v", addr, ci, values)
				}
				out = append(out, n)
			}
			for i := 1; i < len(selectors); i++ {
				x, y, z, ok := dataGetPos(t, sink, pw, selectors[i])
				p := points[i]
				if !ok || math.Abs(x-p[0]) > 1e-7 || math.Abs(y-p[1]) > 1e-7 || math.Abs(z-p[2]) > 1e-7 {
					t.Errorf("%s case%d %s moved%v,%v,%v want%v", addr, ci, labels[i], x, y, z, p)
				}
				out = append(out, x, y, z)
			}
			driver.close()
			shulkerStepTicks(t, pw, sink, 10)
			x, y, z, ok := dataGetPos(t, sink, pw, selectors[0])
			if !ok || math.Abs(x-last[0]) > 1e-7 || math.Abs(y-last[1]) > 1e-7 || math.Abs(z-last[2]) > 1e-7 {
				t.Errorf("%s case%d closing dragged body%v,%v,%v want%v", addr, ci, x, y, z, last)
			}
			out = append(out, x, y, z)
			for _, sel := range selectors {
				sendCommand(t, pw, "tp "+sel+" 10 170 10")
				sendCommand(t, pw, "kill "+sel)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	for i := range v {
		if math.Abs(v[i]-g[i]) > 1e-6 {
			t.Errorf("observation%d vanilla%v mcgo%v", i, v[i], g[i])
		}
	}
}
