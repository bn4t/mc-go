package blackbox

// TestTurtleEggTrampleParity pins TurtleEggBlock's two trample arms
// (26.2 bytecode):
//   - fallOn: a landing LivingEntity that is not in the Zombie class
//     family rolls destroyEgg(3) — 1/3 per landing, no distance floor.
//     Sneaking does not exempt fallOn.
//   - stepOn: every grounded tick the support cell's stepOn rolls
//     destroyEgg(100) — 1% per tick — unless the entity
//     isSteppingCarefully (sneaking).
//
// destroyEgg itself is gated by canDestroyEgg: turtles and bats are
// always exempt, every other LivingEntity needs to be a player or have
// the mobGriefing game rule on. Each accepted break plays
// TURTLE_EGG_BREAK (BLOCKS 0.7 / 0.9+r*0.2), emits BLOCK_DESTROY, and
// broadcasts levelEvent 2001 carrying the pre-decrement state; eggs>1
// decrements, eggs==1 removes the cell.
//
// Legs (all eggs=4 so a first break shows as a decrement):
//   - A x=-8: sneaked player falls onto the egg 16 times. Sneak
//     exempts stepOn only, so any decrement attributes cleanly to
//     fallOn: 1-(2/3)^16 ~= 99.9% per leg.
//   - B x=-4: zombie stands penned on the egg ~500 ticks. Zombies are
//     the fallOn-exempt class, so any decrement attributes cleanly to
//     stepOn: 1-0.99^500 ~= 99.3% per leg.
//   - C x=0: turtle stands penned on the egg the whole window —
//     canDestroyEgg exempts it outright; eggs must stay 4.
//   - D x=4: sneaked player jitter-stands on the egg — sneaking is
//     isSteppingCarefully; eggs must stay 4.
//   - E x=8: zombie stands penned with mobGriefing=false —
//     canDestroyEgg's game-rule gate; eggs must stay 4.
//
// A decrement rolls are random per leg, so the pin is the count
// reaching <4 (or air), never the exact value. randomTickSpeed 0
// freezes the egg's hatch randomTick arm.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTurtleEggTrampleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("EG")},
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

	run := func(addr string) map[string]int {
		c, pr, pw := joinPlayBlackbox(t, addr, "EG")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		var stash []string
		drain := func() { stash = append(stash, sink.take()...) }

		await := func(key string) bool {
			end := time.Now().Add(3 * time.Second)
			for time.Now().Before(end) {
				for _, l := range sink.take() {
					stash = append(stash, l)
					if strings.Contains(l, key) {
						return true
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			return false
		}
		send := func(cmd string) {
			for tries := 0; tries < 30; tries++ {
				sendCommand(t, pw, cmd)
				kind := "fill.success"
				switch {
				case strings.HasPrefix(cmd, "setblock"):
					kind = "setblock.success"
				case strings.HasPrefix(cmd, "summon"):
					kind = "summon.success"
				case strings.HasPrefix(cmd, "gamerule"):
					kind = "gamerule."
				case strings.HasPrefix(cmd, "time"):
					kind = "time."
				case strings.HasPrefix(cmd, "tp"):
					kind = "teleport."
				}
				if await(kind) {
					return
				}
				time.Sleep(150 * time.Millisecond)
			}
			t.Fatalf("command never landed: %s", cmd)
		}

		send("gamerule random_tick_speed 0")
		send("gamerule spawn_mobs false")
		send("time set 18000")
		send("fill -9 -62 0 9 -62 4 minecraft:stone")
		send("fill -9 -61 0 9 -50 4 minecraft:air")
		for _, x := range []int{-4, 0, 8} { // the penned cells B, C, E
			send(fmt.Sprintf("fill %d -61 1 %d -58 3 minecraft:stone", x-1, x+1))
			send(fmt.Sprintf("fill %d -61 2 %d -58 2 minecraft:air", x, x))
		}
		for _, x := range []int{-8, -4, 0, 4, 8} {
			send(fmt.Sprintf(
				"setblock %d -61 2 minecraft:turtle_egg[eggs=4,hatch=0]", x))
		}
		send(`summon minecraft:zombie -3.5 -60.4 2.5`)
		send(`summon minecraft:turtle 0.5 -60.4 2.5`)

		// A leg: sneak on, then 16 tp-and-descend falls onto the egg.
		if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x20}); err != nil {
			t.Fatalf("sneak packet: %v", err)
		}
		for i := 0; i < 16; i++ {
			sendCommand(t, pw, "tp @s -7.5 -55 2.5 0 0")
			time.Sleep(250 * time.Millisecond)
			for y := -55.5; y > -60.20; y -= 0.5 {
				move(pw, -7.5, y, 2.5, false)
				time.Sleep(25 * time.Millisecond)
			}
			move(pw, -7.5, -60.20, 2.5, true)
			time.Sleep(120 * time.Millisecond)
		}
		drain()

		// D leg: tp directly onto the cell, then sneak + horizontal-
		// only jitter for the standing window. Vanilla runs a remote
		// player's checkInsideBlocks (and so the support cell's stepOn)
		// per accepted move packet, while fallDistance only accumulates
		// on airborne downward deltas — X-jitter exercises stepOn
		// without ever rolling fallOn. Any vertical jitter instead
		// produces micro-landings: each one rolls fallOn.
		send("tp @s 4.5 -60.20 2.5 0 0")
		move(pw, 4.5, -60.20, 2.5, true)
		end := time.Now().Add(22 * time.Second)
		alt := 0.0
		for time.Now().Before(end) {
			alt = 0.006 - alt
			move(pw, 4.5+alt, -60.20, 2.5, true)
			time.Sleep(45 * time.Millisecond)
		}
		drain()

		// E leg: the game-rule gate. B's zombie stops rolling too —
		// its assertion is already taken below.
		send("gamerule mob_griefing false")
		send(`summon minecraft:zombie 8.5 -60.4 2.5`)
		end = time.Now().Add(12 * time.Second)
		for time.Now().Before(end) {
			alt = 0.006 - alt
			move(pw, 4.5+alt, -60.20, 2.5, true)
			time.Sleep(45 * time.Millisecond)
		}
		send("gamerule mob_griefing true")
		_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x00})
		drain()

		// Probe each cell's remaining egg count: eggs 1..4, or air.
		out := map[string]int{}
		cells := map[string]int{"A": -8, "B": -4, "C": 0, "D": 4, "E": 8}
		for leg, x := range cells {
			for n := 1; n <= 4; n++ {
				sendCommand(t, pw, fmt.Sprintf(
					"execute if block %d -61 2 minecraft:turtle_egg[eggs=%d] run say EGG%s%d",
					x, n, leg, n))
			}
			sendCommand(t, pw, fmt.Sprintf(
				"execute unless block %d -61 2 minecraft:turtle_egg run say EGG%sAIR", x, leg))
			sendCommand(t, pw, "say "+leg+"K")
			got := -1
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, l := range sink.take() {
					stash = append(stash, l)
					for n := 1; n <= 4; n++ {
						if strings.Contains(l, fmt.Sprintf("EGG%s%d", leg, n)) {
							got = n
						}
					}
					if strings.Contains(l, "EGG"+leg+"AIR") {
						got = 0
					}
				}
				if got != -1 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			out[leg] = got
		}
		drain()

		// Wire pin: every break broadcasts the positional
		// TURTLE_EGG_BREAK (registry 1659 + 1 = 1660, BLOCKS source 4)
		// and a 2001 level event.
		snd, ev := 0, 0
		for _, l := range stash {
			if strings.HasPrefix(l, "0x75:") {
				r := protocol.NewPacketReader([]byte(l[strings.IndexByte(l, ':')+1:]))
				id, err1 := r.VarInt()
				src, err2 := r.VarInt()
				x8, err3 := r.Int()
				y8, err4 := r.Int()
				z8, err5 := r.Int()
				vol, err6 := r.Float()
				pit, err7 := r.Float()
				if err1 != nil || err2 != nil || err3 != nil || err4 != nil ||
					err5 != nil || err6 != nil || err7 != nil {
					continue
				}
				if id == 1660 && src == 4 {
					snd++
					if vol != 0.7 || pit < 0.9 || pit > 1.1 {
						t.Errorf("egg break sound vol=%v pitch=%v @%v,%v,%v",
							vol, pit, x8, y8, z8)
					}
				}
			}
			if strings.HasPrefix(l, "0x2e:") {
				r := protocol.NewPacketReader([]byte(l[strings.IndexByte(l, ':')+1:]))
				if evID, err := r.Int(); err == nil && evID == 2001 {
					ev++
				}
			}
		}
		out["SND"] = snd
		out["EV"] = ev
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for _, leg := range []string{"A", "B", "C", "D", "E"} {
		switch leg {
		case "A", "B":
			if van[leg] >= 4 || van[leg] < 0 {
				t.Errorf("%s: vanilla eggs=%d, expected a decrement (oracle drifted?)", leg, van[leg])
			}
			if mc[leg] >= 4 || mc[leg] < 0 {
				t.Errorf("%s: mc-go eggs=%d, expected a decrement (vanilla=%d)", leg, mc[leg], van[leg])
			}
		default:
			if van[leg] != 4 {
				t.Errorf("%s: vanilla eggs=%d, want 4 (exemption violated)", leg, van[leg])
			}
			if mc[leg] != 4 {
				t.Errorf("%s: mc-go eggs=%d, want 4 (exemption violated)", leg, mc[leg])
			}
		}
	}
	if van["SND"] < 1 {
		t.Errorf("vanilla emitted no TURTLE_EGG_BREAK positional sound")
	}
	if mc["SND"] < 1 {
		t.Errorf("mc-go emitted no TURTLE_EGG_BREAK positional sound (vanilla=%d)", van["SND"])
	}
	if van["EV"] < 1 {
		t.Errorf("vanilla emitted no 2001 level event")
	}
	if mc["EV"] < 1 {
		t.Errorf("mc-go emitted no 2001 level event (vanilla=%d)", van["EV"])
	}
}
