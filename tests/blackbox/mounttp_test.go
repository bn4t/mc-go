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
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
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
	targets := requireBlackboxFiles(t, map[string]string{"command-spam-threshold-seconds": "0"}, map[string]string{
		"ops.json": opsJSONFor("RdrA", "RdrB"),
	}, nil)
	defer targets.cleanup()

	type leg struct {
		ctrl125, other125 []string
		posBoat           string
		posA, posB        string
		actual            [3][3]float64
	}
	run := func(addr string) *leg {
		ca, pra, pwa, aid := joinPlayEid(t, addr, "RdrA")
		awaitPlayReadyBlackbox(t, addr+"/A", ca, pra, pwa)
		cb, prb, pwb, bid := joinPlayEid(t, addr, "RdrB")
		awaitPlayReadyBlackbox(t, addr+"/B", cb, prb, pwb)
		defer ca.Close()
		defer cb.Close()
		sa, sb := newChatSinkResolvedTP(pra, pwa), newChatSinkResolvedTP(prb, pwb)
		out := &leg{}
		capture := false
		poseA, poseB := int32(-1), int32(-1)
		boatID := int32(0)
		var passengers []int32
		fold := func(first bool, lines []string) []string {
			for _, line := range lines {
				prefix, raw, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				if int32(id) == server.IDPlayEntityMetadata {
					eid, entries := fwDecodeMeta([]byte(raw))
					for _, entry := range entries {
						if entry.idx == 6 && entry.typ == 20 {
							if first && eid == aid {
								poseA = int32(entry.i)
							}
							if !first && eid == bid {
								poseB = int32(entry.i)
							}
						}
					}
				}
				if first && int32(id) == server.IDPlaySpawnEntity {
					r := protocol.NewPacketReader([]byte(raw))
					eid, e := r.VarInt()
					_, e2 := r.UUID()
					typ, e3 := r.VarInt()
					if e == nil && e2 == nil && e3 == nil && typ == mustEType(t, "oak_boat") {
						boatID = eid
					}
				}
				if int32(id) == server.LatestVersion().IDs.PlaySetPassengers {
					r := protocol.NewPacketReader([]byte(raw))
					eid, e := r.VarInt()
					n, e2 := r.VarInt()
					if e == nil && e2 == nil && eid == boatID && n >= 0 && n <= 16 {
						var ids []int32
						for i := int32(0); i < n; i++ {
							value, err := r.VarInt()
							if err != nil {
								t.Fatal(err)
							}
							ids = append(ids, value)
						}
						if len(ids) >= len(passengers) {
							passengers = ids
						}
					}
				}
			}
			if capture {
				if first {
					out.ctrl125 = append(out.ctrl125, tp125Lines(lines)...)
				} else {
					out.other125 = append(out.other125, tp125Lines(lines)...)
				}
			}
			return lines
		}
		collectA := func(marker string) []string {
			lines := fold(true, waitForExactSayBB(t, sa, marker, 30*time.Second))
			fold(false, sb.take())
			return lines
		}
		collectB := func(marker string) []string {
			lines := fold(false, waitForExactSayBB(t, sb, marker, 30*time.Second))
			fold(true, sa.take())
			return lines
		}
		serial := 0
		command := func(cmd string) []string {
			serial++
			marker := fmt.Sprintf("MTP_RECEIPT_%d", serial)
			sendCommand(t, pwa, cmd)
			sendCommand(t, pwa, "say "+marker)
			return collectA(marker)
		}
		position := func(target string) [3]float64 {
			for _, line := range command("data get entity " + target + " Pos") {
				if !strings.Contains(line, "data.entity.query") {
					continue
				}
				value, ok := parseSNBT(line)
				list, yes := value.([]any)
				if !ok || !yes || len(list) != 3 {
					continue
				}
				var pos [3]float64
				for axis := range pos {
					v, ok := hrNum(list[axis])
					if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
						t.Fatal("nonfinite position receipt")
					}
					pos[axis] = v
				}
				return pos
			}
			t.Fatalf("%s missing actual Pos for %s", addr, target)
			return [3]float64{}
		}
		near := func(got, want [3]float64) bool {
			for axis := range got {
				if math.Abs(got[axis]-want[axis]) > 1e-6 {
					return false
				}
			}
			return true
		}
		frames := func(tag string) {
			waitPlayerTicksBB(t, pwa, collectA, tag+"A", 2)
			waitPlayerTicksBB(t, pwb, collectB, tag+"B", 2)
		}
		command("gamemode survival RdrA")
		command("gamemode survival RdrB")
		command("fill 5 -61 25 25 -61 45 minecraft:stone")
		command("tp RdrA 12.0 -60.0 31.5 0 0")
		command("tp RdrB 14.0 -60.0 31.5 0 0")
		setupPlayerTickBarrierBB(t, pwa)
		frames("MTPInitial")
		for _, input := range []byte{0x20, 0} {
			if err := pwa.WritePacket(server.IDPlayPlayerInputSB, []byte{input}); err != nil {
				t.Fatal(err)
			}
			if err := pwb.WritePacket(server.IDPlayPlayerInputSB, []byte{input}); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if input != 0 {
				want = 5
			}
			deadline := time.Now().Add(10 * time.Second)
			for attempt := 0; (poseA != want || poseB != want) && time.Now().Before(deadline); attempt++ {
				frames(fmt.Sprintf("MTPPose%d_%d", input, attempt))
			}
			if poseA != want || poseB != want {
				t.Fatalf("%s actual poses%d/%d want%d", addr, poseA, poseB, want)
			}
		}
		if !near(position("RdrA"), [3]float64{12, -60, 31.5}) || !near(position("RdrB"), [3]float64{14, -60, 31.5}) {
			t.Fatal("waypoint positions not established")
		}
		command("summon minecraft:oak_boat 10.5 -60 31.5")
		boat := "@e[type=minecraft:oak_boat,limit=1]"
		settle := func(tag string, want [3]float64, seated bool) {
			deadline := time.Now().Add(15 * time.Second)
			var bp, ap, bp2 [3]float64
			ground := false
			for attempt := 0; time.Now().Before(deadline); attempt++ {
				frames(fmt.Sprintf("%s%d", tag, attempt))
				bp = position(boat)
				ground = structureSaidBB(command("execute if entity @e[type=minecraft:oak_boat,nbt={OnGround:1b},limit=1] run say MTP_GROUNDED"), "MTP_GROUNDED")
				ready := near(bp, want) && (seated || ground) && boatID != 0 && poseA == 0 && poseB == 0
				if seated {
					ap = position("RdrA")
					bp2 = position("RdrB")
					ready = ready && len(passengers) == 2 && passengers[0] == aid && passengers[1] == bid
				}
				if ready {
					t.Logf("%s %s boat%v ground%t riders%v/%v poses%d/%d passengers%v", addr, tag, bp, ground, ap, bp2, poseA, poseB, passengers)
					return
				}
			}
			t.Fatalf("%s %s did not settle: boat%v ground%t riders%v/%v poses%d/%d passengers%v", addr, tag, bp, ground, ap, bp2, poseA, poseB, passengers)
		}
		settle("MTPUnridden", [3]float64{10.5, -60, 31.5}, false)
		command("ride RdrA mount " + boat)
		command("ride RdrB mount " + boat)
		settle("MTPMounted", [3]float64{10.5, -60, 31.5}, true)
		fold(true, sa.take())
		fold(false, sb.take())
		capture = true
		// Original transition commands and packet comparisons are preserved.
		sendCommand(t, pwa, "tp "+boat+" 20 -60 41")
		time.Sleep(600 * time.Millisecond)
		fold(true, sa.take())
		fold(false, sb.take())
		settle("MTPAbsolute", [3]float64{20.5, -60, 41.5}, true)
		sendCommand(t, pwa, "tp "+boat+" ~ ~1 ~")
		time.Sleep(600 * time.Millisecond)
		fold(true, sa.take())
		fold(false, sb.take())
		frames("MTPFinal")
		out.actual = [3][3]float64{position(boat), position("RdrA"), position("RdrB")}
		// Retain the original raw Pos receipt comparisons as well.
		sendCommand(t, pwa, "data get entity "+boat+" Pos")
		sendCommand(t, pwa, "data get entity RdrA Pos")
		sendCommand(t, pwa, "data get entity RdrB Pos")
		time.Sleep(600 * time.Millisecond)
		lines := fold(true, sa.take())
		fold(false, sb.take())
		var posLines []string
		for _, line := range lines {
			if strings.Contains(line, "Pos") {
				posLines = append(posLines, line)
			}
		}
		if len(posLines) >= 3 {
			out.posBoat = posLines[len(posLines)-3]
			out.posA = posLines[len(posLines)-2]
			out.posB = posLines[len(posLines)-1]
		}
		t.Logf("%s full controlling=%v other=%v actualPos=%v", addr, out.ctrl125, out.other125, out.actual)
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
