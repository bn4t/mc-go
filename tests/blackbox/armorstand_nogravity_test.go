package blackbox

import (
	"fmt"
	"math"
	"testing"
)

func TestArmorStandNoGravityTravelParity(t *testing.T) {
	const player = "StandPhysics"
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer tg.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -3 160 3")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "fill -1 159 -1 12 159 1 stone")
		cases := []struct {
			tag, nbt string
			x        float64
			ground   bool
		}{{"NoGravStill", "NoGravity:1b", .5, false}, {"NoGravDelta", "NoGravity:1b,Motion:[0.2d,0.3d,0.1d]", 3.5, false}, {"MarkerDelta", "Marker:1b,Motion:[0.2d,0.3d,0.1d]", 6.5, false}, {"OrdinaryGravity", "", 9.5, true}}
		selectors := make([]string, len(cases))
		for i, tc := range cases {
			selectors[i] = "@e[type=armor_stand,tag=" + tc.tag + ",limit=1]"
			fields := "Tags:[\"" + tc.tag + "\"],PortalCooldown:20"
			if tc.nbt != "" {
				fields += "," + tc.nbt
			}
			sendCommand(t, pw, fmt.Sprintf("summon armor_stand %.5f 165.0 0.5 {%s}", tc.x, fields))
			x, y, z, ok := dataGetPos(t, sink, pw, selectors[i])
			if !ok || x != tc.x || y != 165 || z != .5 {
				t.Fatalf("%s %s initial%v,%v,%v ok%t", addr, tc.tag, x, y, z, ok)
			}
		}
		shulkerStepTicks(t, pw, sink, 21)
		var out []float64
		for i, tc := range cases {
			x, y, z, ok := dataGetPos(t, sink, pw, selectors[i])
			if !ok {
				t.Fatal("stand query missing")
			}
			wantY := 165.0
			if tc.ground {
				wantY = 160
			}
			if math.Abs(x-tc.x) > 1e-6 || math.Abs(y-wantY) > 1e-6 || math.Abs(z-.5) > 1e-6 {
				t.Errorf("%s %s pos%v,%v,%v want%v,%v,.5", addr, tc.tag, x, y, z, tc.x, wantY)
			}
			state, cooldown := dataGetSel(t, sink, pw, selectors[i], "PortalCooldown")
			if state != "query" || cooldown != 0 {
				t.Errorf("%s %s did not tick cooldown%v state%s", addr, tc.tag, cooldown, state)
			}
			out = append(out, x, y, z, cooldown)
		}

		sendCommand(t, pw, "data merge entity "+selectors[0]+" {NoGravity:0b}")
		shulkerStepTicks(t, pw, sink, 21)
		x, y, z, ok := dataGetPos(t, sink, pw, selectors[0])
		if !ok || x != .5 || y != 160 || z != .5 {
			t.Errorf("%s unsetNoGravity position%v,%v,%v ok%t", addr, x, y, z, ok)
		}
		out = append(out, x, y, z)
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
