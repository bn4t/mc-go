package blackbox

// TestFarmlandTrampleParity pins FarmlandBlock.fallOn's trample half
// (26.2 bytecode): before super.fallOn's damage tail runs, a landing
// LivingEntity turns the cell to dirt when
// level.getRandom().nextFloat() < fallDistance-0.5 — always true past
// a 1.5m drop — its bounding volume bbW²·bbH exceeds 0.512, and a
// non-player entity needs the mob_griefing game rule (a player never
// checks it). turnToDirt writes the dirt state and posts
// GameEvent.BLOCK_CHANGE.
//
// Legs, all from >5m so the roll is deterministic:
//   - zombie (0.6²·1.95 = 0.702) — tramples to dirt.
//   - chicken (0.4²·0.7 = 0.112) — volume gate fails, farmland stays.
//   - player (0.6²·1.8 = 0.648) — tramples to dirt.
//
// randomTickSpeed is frozen so the dry farmland can't self-revert to
// dirt before the landings land.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFarmlandTrampleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("Tramp")},
		func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer targets.cleanup()

	move := func(pw *protocol.Writer, x, y, z float64, onGround bool) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		var f byte
		if onGround {
			f = 0x01
		}
		b.Byte(f)
		_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
	}

	run := func(addr string) map[string]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "Tramp")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamerule randomTickSpeed 0")
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "fill 0 -62 0 14 -62 6 minecraft:stone")
		sendCommand(t, pw, "fill 0 -61 0 14 -50 6 minecraft:air")
		sendCommand(t, pw, "setblock 2 -61 2 minecraft:farmland")
		sendCommand(t, pw, "setblock 6 -61 2 minecraft:farmland")
		sendCommand(t, pw, "setblock 10 -61 2 minecraft:farmland")
		time.Sleep(500 * time.Millisecond)

		// Mob legs: summoned mid-air, AI on (vanilla's travel() needs
		// isEffectiveAi to fall at all). ~5m drop makes the roll
		// deterministic.
		sendCommand(t, pw, `summon minecraft:zombie 2.5 -55 2.5`)
		sendCommand(t, pw, `summon minecraft:chicken 6.5 -55 6.5`)

		// pushEntitiesUp leg (TR3): an item resting on a 4th farmland
		// cell sits in the gained slab the turnToDirt push lifts —
		// feet -60.0625 → -60.0 on the new dirt top. The zombie
		// trampler lands in the slab too and pushes itself up the
		// same 1/16.
		sendCommand(t, pw, "setblock 14 -61 2 minecraft:farmland")
		sendCommand(t, pw, `summon minecraft:item 14.5 -60.0625 2.5 {Item:{id:"minecraft:stone",count:1b}}`)
		sendCommand(t, pw, `summon minecraft:zombie 14.5 -55 2.5`)

		// Player leg: tp above the third cell, then descend with real
		// move packets so fallDistance accumulates server-side.
		sendCommand(t, pw, "tp @s 10.5 -55 2.5 0 0")
		time.Sleep(400 * time.Millisecond)
		for y := -55.5; y > -60.0625; y -= 0.7 {
			move(pw, 10.5, y, 2.5, false)
			time.Sleep(30 * time.Millisecond)
		}
		move(pw, 10.5, -60.0625, 2.5, true)
		time.Sleep(1500 * time.Millisecond)

		out := map[string]string{}
		if _, iy, _, okI := dataGetPos(t, sink, pw, "@e[type=minecraft:item,limit=1]"); okI {
			out["IY"] = fmt.Sprintf("%.4f", iy)
		}
		if _, zy, _, okZ := dataGetPos(t, sink, pw,
			"@e[type=minecraft:zombie,x=13,y=-63,z=0,dx=4,dy=5,dz=5,limit=1]"); okZ {
			out["ZY"] = fmt.Sprintf("%.4f", zy)
		}
		for i, x := range []int{2, 6, 10, 14} {
			mk := fmt.Sprintf("TR%d", i)
			sendCommand(t, pw, fmt.Sprintf(
				"execute if block %d -61 2 minecraft:dirt run say %s-dirt", x, mk))
			sendCommand(t, pw, fmt.Sprintf(
				"execute if block %d -61 2 minecraft:farmland run say %s-farm", x, mk))
			sendCommand(t, pw, fmt.Sprintf("say %sK", mk))
			hit := "none"
			end := time.Now().Add(4 * time.Second)
			for time.Now().Before(end) {
				stop := false
				for _, l := range sink.take() {
					if strings.Contains(l, mk+"-dirt") {
						hit = "dirt"
					}
					if strings.Contains(l, mk+"-farm") {
						hit = "farmland"
					}
					if strings.Contains(l, mk+"K") {
						stop = true
					}
				}
				if stop && hit != "none" {
					break
				}
			}
			out[mk] = hit
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for i, mk := range []string{"TR0", "TR1", "TR2", "TR3"} {
		leg := []string{"zombie", "chicken", "player", "push"}[i]
		want := "dirt"
		if leg == "chicken" {
			want = "farmland"
		}
		if van[mk] != want {
			t.Errorf("%s vanilla=%s want %s", leg, van[mk], want)
		}
		if mc[mk] != van[mk] {
			t.Errorf("%s: vanilla=%s mc-go=%s", leg, van[mk], mc[mk])
		}
	}
	// pushEntitiesUp: the resting item's feet lift from the farmland
	// top -60.0625 onto the dirt top -60.0 (the zombie trampler
	// self-pushes the same 1/16 — ZY is informational since the
	// zombie wanders).
	for name, out := range map[string]map[string]string{"vanilla": van, "mc-go": mc} {
		iy, err := strconv.ParseFloat(out["IY"], 64)
		if err != nil || iy < -60.02 || iy > -59.97 {
			t.Errorf("%s item feet = %q, want ≈ -60.0 (pushed)", name, out["IY"])
		}
	}
}
