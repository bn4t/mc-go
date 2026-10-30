package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Witness the authoritative player pose before pinning first-tick orb attraction.
func TestOrbPoseGravityAttractionParity(t *testing.T) {
	const actor = "OrbPoseRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw, eid := joinPlayEid(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		pose := int32(-1)
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("ORB_POSE_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			lines := waitForExactSayBB(t, sink, receipt, 30*time.Second)
			for _, line := range lines {
				prefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
				if strings.HasPrefix(line, prefix) {
					id, entries := fwDecodeMeta([]byte(strings.TrimPrefix(line, prefix)))
					if id == eid {
						for _, entry := range entries {
							if entry.idx == 6 && entry.typ == 20 {
								pose = int32(entry.i)
							}
						}
					}
				}
			}
			return lines
		}
		clock := func() int64 {
			for _, line := range command("time query gametime") {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) > 0 {
					if v, ok := args[0].(int64); ok {
						return v
					}
				}
			}
			t.Fatal("missing clock")
			return 0
		}
		step := func() {
			before := clock()
			command("tick step 1")
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				got := clock()
				if got > before+1 {
					t.Fatalf("overshot exact tick: %d -> %d", before, got)
				}
				if got == before+1 {
					time.Sleep(150 * time.Millisecond)
					if clock() != got {
						t.Fatal("clock moved after step")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("step not complete")
		}
		command("gamemode creative @s")
		command("tp @s 0.5 162.0 0.5")
		command("tick freeze")
		command("attribute @s minecraft:scale base set 1")
		command("scoreboard objectives add OrbPose dummy")
		command("execute store result score #scale OrbPose run attribute @s minecraft:scale get 1000")
		if lines := command("execute if score #scale OrbPose matches 1000 run say ORB_POSE_SCALE"); !structureSaidBB(lines, "ORB_POSE_SCALE") {
			t.Fatal("scale witness missing")
		}
		for _, noGravity := range []bool{false, true} {
			for _, tc := range []struct {
				name, roof string
				pose       int32
			}{{"crouch", "stone_slab[type=top]", 5}, {"crawl", "stone", 3}, {"standing", "air", 0}, {"swimming", "air", 3}} {
				command("kill @e[type=experience_orb]")
				command("fill -1 161 -1 5 166 1 air")
				command("fill -1 161 -1 1 161 1 stone")
				command("setblock 0 163 0 " + tc.roof)
				witness := "ORB_POSE_ROOF_" + tc.name
				if lines := command("execute if block 0 163 0 " + tc.roof + " run say " + witness); !structureSaidBB(lines, witness) {
					t.Fatal("roof witness missing")
				}
				command("tp @s 0.5 162.0 0.5")
				var sprint protocol.Buffer
				sprint.VarInt(eid)
				if tc.name == "swimming" {
					command("fill 0 162 0 0 164 0 water")
					if lines := command("execute if block 0 163 0 water run say ORB_POSE_WATER"); !structureSaidBB(lines, "ORB_POSE_WATER") {
						t.Fatal("water witness missing")
					}
					sprint.VarInt(1)
				} else {
					sprint.VarInt(2)
				}
				sprint.VarInt(0)
				if err := pw.WritePacket(server.IDPlayPlayerCommandSB, sprint.Bytes()); err != nil {
					t.Fatal(err)
				}
				{
					for _, mx := range []float64{.51, .5} {
						var move protocol.Buffer
						move.Double(mx)
						move.Double(162)
						move.Double(.5)
						move.Byte(0)
						if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
							t.Fatal(err)
						}
					}
				}
				step()
				// Fluid and pose state can settle over multiple preparation ticks.
				// No orb exists yet; the measured orb transition still requires Age1.
				for prep := 0; prep < 3 && pose != tc.pose; prep++ {
					step()
				}
				if pose != tc.pose {
					t.Fatalf("%s %s actual pose%d want%d", addr, tc.name, pose, tc.pose)
				}
				x, y, z, ok := dataGetPos(t, sink, pw, "@s")
				if !ok || x != .5 || y != 162 || z != .5 {
					t.Fatalf("actual actor position %g %g %g valid%t", x, y, z, ok)
				}
				flag := 0
				if noGravity {
					flag = 1
				}
				command(fmt.Sprintf(`summon experience_orb 4.5 162.6 0.5 {Tags:["OrbPose"],Value:1s,NoGravity:%db,Motion:[0d,0d,0d],Age:0s}`, flag))
				sel := "@e[type=experience_orb,tag=OrbPose,limit=1]"
				age := float64(0)
				for tick := 0; tick < 3 && age == 0; tick++ {
					step()
					state, value := dataGetSel(t, sink, pw, sel, "Age")
					if state != "query" {
						t.Fatal("missing Age")
					}
					age = value
				}
				if age != 1 {
					t.Fatalf("Age%g want1", age)
				}
				ox, oy, oz, exists := dataGetPos(t, sink, pw, sel)
				if !exists {
					t.Fatal("missing orb position")
				}
				parsed, valid := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
				values, list := parsed.([]any)
				if !valid || !list || len(values) != 3 {
					t.Fatal("missing Motion")
				}
				var motion [3]float64
				for a := range motion {
					v, ok := hrNum(values[a])
					if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
						t.Fatal("invalid Motion")
					}
					motion[a] = v
				}
				expected := map[int32][3]float64{
					0: {-.024896901146477468, -.028692912674983972, .0013070873250171644},
					5: {-.024997129109300964, -.029781275179885824, .00021872482011531247},
					3: {-.024628408079287567, -.032462840789577285, -.0024628407895761484},
				}[tc.pose]
				wantY := expected[1]
				if noGravity {
					wantY = expected[2]
				}
				delta := [3]float64{ox - 4.5, oy - 162.6, oz - .5}
				want := [3]float64{expected[0], wantY, 0}
				for a := range delta {
					if math.IsNaN(delta[a]) || math.IsInf(delta[a], 0) || math.Abs(delta[a]-want[a]) > 1e-10 {
						t.Errorf("%s %s NoGravity%t axis%d displacement%.17g want%.17g", addr, tc.name, noGravity, a, delta[a], want[a])
					}
					if expectedMotion := want[a] * float64(float32(.98)); math.Abs(motion[a]-expectedMotion) > 1e-10 {
						t.Errorf("%s %s NoGravity%t axis%d Motion%.17g want%.17g", addr, tc.name, noGravity, a, motion[a], expectedMotion)
					}
				}
				t.Logf("checked %s pose%d %s NoGravity%t delta%v", addr, pose, tc.name, noGravity, delta)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
