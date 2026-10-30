package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// First-tick attraction observations pin the standing player scale dependency.
func TestOrbScaledAttractionParity(t *testing.T) {
	const actor = "OrbPullRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("ORB_PULL_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode creative @s")
		command("tp @s 0.5 162.0 0.5")
		command("tick freeze")
		x, y, z, ok := dataGetPos(t, sink, pw, "@s")
		if !ok || x != .5 || y != 162 || z != .5 {
			t.Fatalf("actual actor position %v %v %v valid%t", x, y, z, ok)
		}
		command("fill -1 161 -1 5 166 1 air")
		command("scoreboard objectives add OrbPull dummy")
		for _, tc := range []struct{ scale, dx, dy float64 }{{.5, -.024911072661375933, -.0012144147848118791}, {1, -.024896901146477468, .0013070873250171644}, {2, -.022699191406771568, .0057882938357920466}} {
			scale := tc.scale
			command("kill @e[type=experience_orb]")
			command(fmt.Sprintf("attribute @s minecraft:scale base set %g", scale))
			shulkerStepTicks(t, pw, sink, 1)
			command("execute store result score #scale OrbPull run attribute @s minecraft:scale get 1000")
			witness := fmt.Sprintf("ORB_PULL_SCALE_%g", scale)
			if lines := command(fmt.Sprintf("execute if score #scale OrbPull matches %d run say %s", int(scale*1000), witness)); !structureSaidBB(lines, witness) {
				t.Fatal("missing effective scale witness")
			}
			command(`summon experience_orb 4.5 162.6 0.5 {Tags:["OrbPull"],Value:1s,NoGravity:1b,Motion:[0d,0d,0d],Age:0s}`)
			sel := "@e[type=experience_orb,tag=OrbPull,limit=1]"
			age := float64(0)
			for tick := 0; tick < 3 && age == 0; tick++ {
				shulkerStepTicks(t, pw, sink, 1)
				state, value := dataGetSel(t, sink, pw, sel, "Age")
				if state != "query" {
					t.Fatal("missing orb Age")
				}
				age = value
			}
			if age != 1 {
				t.Fatalf("first Age%v want1", age)
			}
			ox, oy, oz, exists := dataGetPos(t, sink, pw, sel)
			if !exists {
				t.Fatal("missing orb position")
			}
			parsed, valid := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
			values, list := parsed.([]any)
			if !valid || !list || len(values) != 3 {
				t.Fatal("missing orb Motion")
			}
			got := [3]float64{ox - 4.5, oy - 162.6, oz - .5}
			want := [3]float64{tc.dx, tc.dy, 0}
			for axis := range got {
				if math.IsNaN(got[axis]) || math.IsInf(got[axis], 0) || math.Abs(got[axis]-want[axis]) > 1e-10 {
					t.Errorf("%s scale%g axis%d delta%.17g want%.17g", addr, scale, axis, got[axis], want[axis])
				}
				motion, ok := hrNum(values[axis])
				if !ok || math.IsNaN(motion) || math.IsInf(motion, 0) {
					t.Fatal("invalid orb Motion")
				}
				if expected := got[axis] * float64(float32(.98)); math.Abs(motion-expected) > 1e-10 {
					t.Errorf("%s scale%g axis%d Motion%.17g want%.17g", addr, scale, axis, motion, expected)
				}
			}
			t.Logf("checked %s scale%g delta%v", addr, scale, got)
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
