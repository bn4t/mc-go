//go:build blackbox

// TestChickenJockeyParity pins the living-vehicle riding chain:
// /summon Passengers mounts a baby zombie on a flagged chicken,
// Entity.tick's positionRider welds it to the rotated PASSENGER
// attachment (chicken: (0,0.7,-0.1), summon yaw 0 -> x+0, y+0.7,
// z-0.1, then minus the baby's VEHICLE attach 0.1875 -> seat
// (44.5,-59.4875,44.4)), killing the vehicle drops the rider back
// to the ground, and Chicken.aiStep's egg block lays an egg at
// --EggLayTime<=0 unless the jockey flag suppresses it. A standing
// baby summons alongside to pin BABY_DIMENSIONS (0.49x0.98) vs the
// adult box.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestChickenJockeyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		jockeyFlag  bool // chicken nbt IsChickenJockey:1b
		vehPass     bool // chicken Passengers lists the zombie
		seatNear    bool // rider pos within 0.05 of the welded seat
		seatTight   bool // ..0.005 around the same point misses
		babyTopMiss bool // standing baby's bb clears the [-59,-57.5] band
		babyBandHit bool // a band clipping the baby bb top hits it
		afterAlive  bool // rider survives the vehicle's death
		afterGround bool // rider returns to ground level post-kill
		eggLay      bool // EggLayTime:3 lays an egg item nearby
		eggSupp     bool // a jockey-flagged chicken suppresses the lay
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)

		probe := func(sel string, i int) bool {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZCJ-ok-%d", sel, i))
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, fmt.Sprintf("ZCJ-ok-%d", i)) {
						return true
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			return false
		}
		miss := func(sel string, i int) bool { return !probe(sel, i) }

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, "fill 40 -61 40 56 -61 48 minecraft:stone")
		sendCommand(t, pw, `summon minecraft:chicken 44.5 -60 44.5 {NoAI:1b,IsChickenJockey:1b,Passengers:[{id:"minecraft:zombie",IsBaby:1b,NoAI:1b}]}`)
		sendCommand(t, pw, `summon minecraft:zombie 55 -60 44.5 {IsBaby:1b,NoAI:1b}`)
		time.Sleep(500 * time.Millisecond)

		r.jockeyFlag = probe(`@e[type=minecraft:chicken,nbt={IsChickenJockey:1b}]`, 1)
		r.vehPass = probe(`@e[type=minecraft:chicken,nbt={Passengers:[{id:"minecraft:zombie"}]}]`, 2)
		// Welded seat (44.5,-59.4875,44.4): 0.0125 under the probe
		// point — inside ..0.05, outside ..0.005. A standing rider at
		// (44.5,-60,44.4) is 0.5 out — the pair pins the seat offset.
		r.seatNear = probe(`@e[type=minecraft:zombie,x=44.5,y=-59.5,z=44.4,distance=..0.05]`, 3)
		r.seatTight = miss(`@e[type=minecraft:zombie,x=44.5,y=-59.5,z=44.4,distance=..0.005]`, 4)
		// Standing baby bb is BABY_DIMENSIONS 0.49x0.98 -> top -59.02:
		// the y[-59,-57.5] band sits above it (miss), a band clipping
		// just under the top hits.
		r.babyTopMiss = miss(`@e[type=minecraft:zombie,x=55,y=-59,z=44,dx=1,dy=0.5,dz=1]`, 5)
		r.babyBandHit = probe(`@e[type=minecraft:zombie,x=55,y=-59.15,z=44,dx=1,dy=0.1,dz=1]`, 6)

		sendCommand(t, pw, "kill @e[type=minecraft:chicken]")
		time.Sleep(1500 * time.Millisecond)
		r.afterAlive = probe(`@e[type=minecraft:zombie,x=44,y=-61,z=44,dx=1,dy=3,dz=1]`, 7)
		// The dismounted rider falls to the ground near the weld (the
		// ~1-block horizontal drift varies run to run — vanilla pushes
		// the still-overlapping corpse's bb apart; pin the envelope:
		// bb intersects the ground band y[-60.05,-59] inside x/z[43,48]).
		r.afterGround = probe(`@e[type=minecraft:zombie,x=43,y=-60.05,z=43,dx=4,dy=0.05,dz=4]`, 8)

		sendCommand(t, pw, `summon minecraft:chicken 48.5 -60 44.5 {NoAI:1b,EggLayTime:3}`)
		time.Sleep(900 * time.Millisecond)
		r.eggLay = probe(`@e[type=minecraft:item,nbt={Item:{id:"minecraft:egg"}}]`, 9)

		sendCommand(t, pw, `summon minecraft:chicken 52.5 -60 44.5 {NoAI:1b,EggLayTime:3,IsChickenJockey:1b}`)
		time.Sleep(900 * time.Millisecond)
		r.eggSupp = miss(`@e[type=minecraft:item,nbt={Item:{id:"minecraft:egg"}},x=52,dx=2,z=44,dz=2]`, 10)
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	if van != mc {
		t.Fatalf("mismatch: vanilla %+v vs mc-go %+v", van, mc)
	}
	if !van.jockeyFlag || !van.vehPass || !van.seatNear || !van.seatTight ||
		!van.babyTopMiss || !van.babyBandHit || !van.afterAlive || !van.afterGround ||
		!van.eggLay || !van.eggSupp {
		t.Fatalf("positive legs failed on vanilla: %+v", van)
	}
}
