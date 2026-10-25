package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSkeletonStrafeParity pins RangedBowAttackGoal + MoveControl's
// STRAFE tick, measured off the 26.2 bytecode: after 20 ticks of
// continuous sight inside the 15-block attack radius the skeleton
// stops navigation and strafes — moveControl.strafe(±0.5, ±0.5)
// scaled to movement_speed, rotated by body yaw, gated on the
// isWalkable node probe, and carried through travel's
// friction-influenced input chain (NOT a direct position write). The
// observable signature: with the player still on the +x axis, the
// skeleton oscillates laterally along z while its travel input runs
// through floor friction, and the draw fires an arrow every
// attack interval.
func TestSkeletonStrafeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("StA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "StA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		drainChunkWindow(t, s.label, cA, prA, pwA, 400*time.Millisecond)
		sinkA := newChatSinkTP(prA, pwA)
		time.Sleep(500 * time.Millisecond)

		posOf := func(sel string) (x, y, z float64) {
			l := dataGetLine(t, sinkA, pwA, sel, "Pos")
			nums := sleepPosRe.FindAllString(l, -1)
			if len(nums) < 3 {
				t.Fatalf("%s: no %s Pos in %q", s.label, sel, l)
			}
			fmt.Sscanf(nums[len(nums)-3], "%f", &x)
			fmt.Sscanf(nums[len(nums)-2], "%f", &y)
			fmt.Sscanf(nums[len(nums)-1], "%f", &z)
			return
		}
		healthOf := func() float64 {
			l := dataGetLine(t, sinkA, pwA,
				"@e[type=minecraft:player,name=StA,limit=1]", "Health")
			nums := sleepPosRe.FindAllString(l, -1)
			if len(nums) == 0 {
				return 20
			}
			var h float64
			fmt.Sscanf(nums[len(nums)-1], "%f", &h)
			return h
		}

		// Night (the skeleton doesn't burn), flat ground, no mob
		// spawns to foul the arena.
		sendCommand(t, pwA, "gamerule doMobSpawning false")
		sendCommand(t, pwA, "time set 13000")
		sendCommand(t, pwA, "kill @e[type=!minecraft:player]")
		// The comparison world joins players creative — survival is
		// what makes the player a target at all.
		sendCommand(t, pwA, "gamemode survival StA")
		sendCommand(t, pwA, "tp StA 54.5 -60 44.5")
		sendCommand(t, pwA, "summon minecraft:skeleton 44.5 -60 44.5 {PersistenceRequired:1b}")
		// /summon runs no equipment populate — hand the bow over
		// explicitly on both servers.
		sendCommand(t, pwA, "item replace entity @e[type=minecraft:skeleton] weapon.mainhand with minecraft:bow")
		sel := "@e[type=minecraft:skeleton,limit=1]"
		sx, _, _ := posOf(sel)
		if sx < 44 || sx > 45 {
			t.Fatalf("%s: skeleton did not land at spawn, x=%v", s.label, sx)
		}
		t.Logf("%s: mainhand = %s", s.label,
			dataGetLine(t, sinkA, pwA, sel, "equipment.mainhand"))
		time.Sleep(500 * time.Millisecond)

		// seeTime reaches 20 after ~1s of sight; track the skeleton's
		// z (lateral) and x (axis) spread for 9s — a strafing skeleton
		// sweeps z well past ±1 while x stays near the player line.
		var zMin, zMax, xMin, xMax float64
		first := true
		hurt := false
		deadline := time.Now().Add(9 * time.Second)
		for time.Now().Before(deadline) {
			x, _, z := posOf(sel)
			if first {
				zMin, zMax, xMin, xMax = z, z, x, x
				first = false
			}
			if z < zMin {
				zMin = z
			}
			if z > zMax {
				zMax = z
			}
			if x < xMin {
				xMin = x
			}
			if x > xMax {
				xMax = x
			}
			// An arrow that hits the player despawns on impact — the
			// draw→release chain shows up as the player's Health dip
			// (saturation regen heals it back within ~2s, so sample
			// every pass rather than once at the end).
			if healthOf() < 19.9 {
				hurt = true
			}
			sendCommand(t, pwA, "execute if entity @e[type=#minecraft:arrows,limit=1] run say ARRSEEN")
			time.Sleep(300 * time.Millisecond)
		}
		arrSeen := false
		lines := sinkA.take()
		for _, l := range lines {
			if strings.Contains(l, "ARRSEEN") {
				arrSeen = true
			}
		}
		zRange, xRange := zMax-zMin, xMax-xMin
		t.Logf("%s: skeleton z range %.2f, x range %.2f, hurt=%v arr=%v",
			s.label, zRange, xRange, hurt, arrSeen)
		// Strafe oscillates laterally: the ±0.5 lat input carries the
		// skeleton sideways through the friction chain. A chasing or
		// standing skeleton stays near z≈44.5 (range <<1).
		if zRange < 1.0 {
			t.Fatalf("%s: skeleton did not strafe laterally: z range %.2f", s.label, zRange)
		}
		if !hurt {
			for _, l := range lines {
				if strings.Contains(l, "error") || strings.Contains(l, "rror") ||
					strings.Contains(l, "ARRSEEN") {
					t.Logf("%s: sink %s", s.label, l)
				}
			}
			t.Fatalf("%s: player never took an arrow hit in 9s (Health stayed 20, arrSeen=%v)", s.label, arrSeen)
		}
	}
}
