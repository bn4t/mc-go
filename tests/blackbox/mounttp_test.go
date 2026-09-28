package blackbox

// TestMountTeleportParity covers Entity.teleport(TeleportTransition)
// for a mounted mob, measured from 26.2 bytecode: teleportSameDimension
// moves every direct passenger through its own calculated transition
// (the seat offset survives on axes the transition doesn't mark
// position-relative, and the rider's rotation slides by the mount's rot
// delta), then sendTeleportTransitionToRidingPlayers emits
// teleport_entity (0x7d) to every player anywhere in the rider stack —
// transition PMR + relatives for the controlling passenger (the first
// living rider), mount PMR + empty set for the rest.
//
// Two scripted clients share an oak_boat: the first rider is the
// controlling passenger and sees the command's relatives bitmask
// (direction bits for ~-relative axes), the second sees an empty set.
// Entity ids are normalized out (spawn ids differ across servers); the
// final Pos[] of mount and riders comes back through /data get.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// parseTeleportEntity decodes one 0x7d payload into a comparable
// summary: entity id normalized out and everything else verbatim.
func parseTeleportEntity(data []byte) string {
	r := protocol.NewPacketReader(data)
	_, _ = r.VarInt()
	x, _ := r.Double()
	y, _ := r.Double()
	z, _ := r.Double()
	dx, _ := r.Double()
	dy, _ := r.Double()
	dz, _ := r.Double()
	yaw, _ := r.Float()
	pitch, _ := r.Float()
	rel, _ := r.Int()
	og, _ := r.Bool()
	return fmt.Sprintf("pos %.4f %.4f %.4f delta %.4f %.4f %.4f rot %.2f %.2f rel %d og %v",
		x, y, z, dx, dy, dz, yaw, pitch, rel, og)
}

// tp125Lines collects the 0x7d summaries a chat sink captured.
func tp125Lines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "0x7d:") {
			out = append(out, parseTeleportEntity([]byte(line[5:])))
		}
	}
	return out
}

func TestMountTeleportParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("RdrA", "RdrB"),
	}, nil)
	defer targets.cleanup()

	type leg struct {
		ctrl125, other125 []string
		posBoat           string
		posA, posB        string
	}
	run := func(addr string) *leg {
		ca, pra, pwa := joinPlayBlackbox(t, addr, "RdrA")
		cb, prb, pwb := joinPlayBlackbox(t, addr, "RdrB")
		sa := newChatSinkTP(pra, pwa)
		sb := newChatSinkTP(prb, pwb)
		defer ca.Close()
		defer cb.Close()

		sendCommand(t, pwa, "fill 5 -61 25 25 -61 45 minecraft:stone")
		sendCommand(t, pwa, "summon minecraft:oak_boat 10.5 -60 31.5")
		sendCommand(t, pwa, "tp @s 12 -60 31.5 0 0")
		time.Sleep(400 * time.Millisecond)
		sa.take()
		sb.take()

		sendCommand(t, pwa, "ride RdrA mount @e[type=minecraft:oak_boat,limit=1]")
		sendCommand(t, pwa, "ride RdrB mount @e[type=minecraft:oak_boat,limit=1]")
		time.Sleep(500 * time.Millisecond)
		sa.take()
		sb.take()

		out := &leg{}
		// Probe 1 — absolute transition: both riders hear rel=0.
		sendCommand(t, pwa, "tp @e[type=minecraft:oak_boat,limit=1] 20 -60 41")
		time.Sleep(600 * time.Millisecond)
		out.ctrl125 = tp125Lines(sa.take())
		out.other125 = tp125Lines(sb.take())

		// Probe 2 — ~-relative axes: the controlling rider sees the
		// direction bitmask (32|64|128 = 224), the other rider stays 0.
		sendCommand(t, pwa, "tp @e[type=minecraft:oak_boat,limit=1] ~ ~1 ~")
		time.Sleep(600 * time.Millisecond)
		out.ctrl125 = append(out.ctrl125, tp125Lines(sa.take())...)
		out.other125 = append(out.other125, tp125Lines(sb.take())...)

		sendCommand(t, pwa, "data get entity @e[type=minecraft:oak_boat,limit=1] Pos")
		sendCommand(t, pwa, "data get entity RdrA Pos")
		sendCommand(t, pwa, "data get entity RdrB Pos")
		time.Sleep(600 * time.Millisecond)
		var posLines []string
		for _, line := range sa.take() {
			if strings.Contains(line, "Pos") {
				posLines = append(posLines, line)
			}
		}
		if len(posLines) >= 3 {
			out.posBoat = posLines[len(posLines)-3]
			out.posA = posLines[len(posLines)-2]
			out.posB = posLines[len(posLines)-1]
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if len(want.ctrl125) == 0 {
		t.Fatalf("vanilla emitted no teleport_entity to the controlling rider")
	}
	if len(got.ctrl125) == 0 {
		t.Fatalf("mc-go emitted no teleport_entity to the controlling rider")
	}
	if len(want.other125) == 0 {
		t.Fatalf("vanilla emitted no teleport_entity to the second rider")
	}
	if len(got.other125) == 0 {
		t.Fatalf("mc-go emitted no teleport_entity to the second rider")
	}
	if len(want.ctrl125) != len(got.ctrl125) {
		t.Fatalf("controlling teleport_entity count: vanilla %d vs mc-go %d\nvanilla=%v\nmc-go=%v",
			len(want.ctrl125), len(got.ctrl125), want.ctrl125, got.ctrl125)
	}
	if len(want.other125) != len(got.other125) {
		t.Fatalf("second-rider teleport_entity count: vanilla %d vs mc-go %d\nvanilla=%v\nmc-go=%v",
			len(want.other125), len(got.other125), want.other125, got.other125)
	}
	for i := 0; i < len(want.ctrl125) && i < len(got.ctrl125); i++ {
		if want.ctrl125[i] != got.ctrl125[i] {
			t.Fatalf("controlling teleport_entity %d: vanilla %q vs mc-go %q", i, want.ctrl125[i], got.ctrl125[i])
		}
	}
	for i := 0; i < len(want.other125) && i < len(got.other125); i++ {
		if want.other125[i] != got.other125[i] {
			t.Fatalf("second-rider teleport_entity %d: vanilla %q vs mc-go %q", i, want.other125[i], got.other125[i])
		}
	}
	if want.posBoat != got.posBoat {
		t.Fatalf("boat Pos: vanilla %q vs mc-go %q", want.posBoat, got.posBoat)
	}
	if want.posA != got.posA {
		t.Fatalf("rider A Pos: vanilla %q vs mc-go %q", want.posA, got.posA)
	}
	if want.posB != got.posB {
		t.Fatalf("rider B Pos: vanilla %q vs mc-go %q", want.posB, got.posB)
	}
}
