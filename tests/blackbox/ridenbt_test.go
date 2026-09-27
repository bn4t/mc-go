//go:build blackbox

// TestRideNBTParity pins Entity.saveWithoutId's mount tail: a vehicle
// emits Passengers (each child through saveAsPassenger — own save +
// "id"), and a rider carries RootVehicle {Entity, Attach} — until
// dismount clears both.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRideNBTParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		riderRoot bool
		vehPass   bool
		passSave  bool
		afterRoot bool
		afterPass bool
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)

		probe := func(sel string, i int) bool {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZRN-ok-%d", sel, i))
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, fmt.Sprintf("ZRN-ok-%d", i)) {
						return true
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			return false
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, "fill 40 -61 40 48 -61 48 minecraft:stone")
		sendCommand(t, pw, "summon minecraft:pig 44.5 -60 44.5 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:minecart 44.5 -60 44.5")
		time.Sleep(600 * time.Millisecond)
		sendCommand(t, pw, "ride @e[type=minecraft:pig,limit=1] mount @e[type=minecraft:minecart,limit=1]")
		time.Sleep(500 * time.Millisecond)

		// The rider carries RootVehicle pointing at the cart; the
		// cart carries a Passengers list whose members are the
		// rider's own save + id (saveAsPassenger).
		r.riderRoot = probe(`@e[type=minecraft:pig,nbt={RootVehicle:{Entity:{id:"minecraft:minecart"}}}]`, 1)
		r.vehPass = probe(`@e[type=minecraft:minecart,nbt={Passengers:[{id:"minecraft:pig"}]}]`, 2)
		r.passSave = probe(`@e[type=minecraft:minecart,nbt={Passengers:[{NoAI:1b}]}]`, 3)

		sendCommand(t, pw, "ride @e[type=minecraft:pig,limit=1] dismount")
		time.Sleep(500 * time.Millisecond)
		// The same selectors must no longer match — both keys gone.
		r.afterRoot = probe(`@e[type=minecraft:pig,nbt={RootVehicle:{Entity:{id:"minecraft:minecart"}}}]`, 4)
		r.afterPass = probe(`@e[type=minecraft:minecart,nbt={Passengers:[{id:"minecraft:pig"}]}]`, 5)
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	if van != mc {
		t.Fatalf("mismatch: vanilla %+v vs mc-go %+v", van, mc)
	}
	if !van.vehPass || !van.passSave {
		t.Fatalf("mounted probes failed on vanilla: %+v", van)
	}
	// Vanilla's data get on a mounted mob emits no RootVehicle —
	// save() returns false for passengers, so the rider's Pos +
	// fields come through saveWithoutId only.
	if van.riderRoot {
		t.Fatalf("riderRoot unexpectedly hit on vanilla: %+v", van)
	}
	if van.afterRoot || van.afterPass {
		t.Fatalf("mount keys leaked after dismount on vanilla: %+v", van)
	}
}
