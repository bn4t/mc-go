package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Keep capture active across command/clock barriers so a correct saved position
// cannot hide movement or scale updates shifted into another entity frame.
func TestMobResizePacketPhaseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("ResizeWire")}, nil)
	defer targets.cleanup()
	run := func(addr string) map[string][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "ResizeWire")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		ids := server.LatestVersion().IDs
		horseID := int32(0)
		horseSel := ""
		phase := "setup"
		out := map[string][]string{}
		fold := func(lines []string) []string {
			for _, line := range lines {
				prefix, raw, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(raw))
				if int32(id) == server.IDPlaySpawnEntity {
					eid, e1 := r.VarInt()
					_, e2 := r.UUID()
					typ, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && typ == mustEType(t, "horse") {
						horseID = eid
					}
					continue
				}
				if phase == "setup" || horseID == 0 {
					continue
				}
				eid, e := r.VarInt()
				if e != nil || eid != horseID {
					continue
				}
				switch int32(id) {
				case ids.PlayMoveEntityPos, ids.PlayMoveEntityPosRot:
					x, e1 := r.Short()
					y, e2 := r.Short()
					z, e3 := r.Short()
					if e1 != nil || e2 != nil || e3 != nil {
						t.Fatal("short resize movement")
					}
					out[phase] = append(out[phase], fmt.Sprintf("move %d %d %d", x, y, z))
				case ids.PlayEntityTeleportCB:
					x, e1 := r.Double()
					y, e2 := r.Double()
					z, e3 := r.Double()
					if e1 != nil || e2 != nil || e3 != nil {
						t.Fatal("short resize absolute movement")
					}
					out[phase] = append(out[phase], fmt.Sprintf("absolute %.9f %.9f %.9f", x, y, z))
				case server.IDPlayUpdateAttributes:
					n, e := r.VarInt()
					if e != nil || n < 0 || n > 256 {
						t.Fatal("invalid resize attributes")
					}
					for i := int32(0); i < n; i++ {
						key, e1 := r.VarInt()
						base, e2 := r.Double()
						mods, e3 := r.VarInt()
						if e1 != nil || e2 != nil || e3 != nil || mods < 0 || mods > 128 {
							t.Fatal("short resize attribute")
						}
						for j := int32(0); j < mods; j++ {
							_, e1 := r.String()
							_, e2 := r.Double()
							_, e3 := r.Byte()
							if e1 != nil || e2 != nil || e3 != nil {
								t.Fatal("short resize modifier")
							}
						}
						if key == 30 {
							out[phase] = append(out[phase], fmt.Sprintf("scale %.9f", base))
						}
					}
				}
			}
			return lines
		}
		serial := 0
		command := func(cmd string) []string {
			serial++
			tag := fmt.Sprintf("RSPacketReceipt%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+tag)
			return fold(waitForExactSayBB(t, sink, tag, 30*time.Second))
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
			t.Fatal("missing resize game clock")
			return 0
		}
		step := func(n int) {
			old := clock()
			command(fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				got := clock()
				if got > old+int64(n) {
					t.Fatal("resize phase overstepped")
				}
				if got == old+int64(n) {
					time.Sleep(150 * time.Millisecond)
					if clock() != got {
						t.Fatal("resize clock did not settle")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("resize step missing")
		}
		position := func(wantY float64) {
			for _, line := range command("data get entity " + horseSel + " Pos") {
				if !strings.Contains(line, "data.entity.query") {
					continue
				}
				value, ok := parseSNBT(line)
				list, yes := value.([]any)
				if !ok || !yes || len(list) != 3 {
					continue
				}
				y, ok := hrNum(list[1])
				if !ok || math.IsNaN(y) || math.IsInf(y, 0) || math.Abs(y-wantY) > 1e-8 {
					t.Errorf("%s %s horseY%.17g want%.17g", addr, phase, y, wantY)
				}
				return
			}
			t.Fatal("resize position receipt missing")
		}
		command("gamemode creative @s")
		command("tp @s 0.5 162 0.5")
		command("tick freeze")
		for _, tc := range []struct {
			name           string
			admit          int
			floor, ceiling bool
			y              float64
		}{
			{"airAge3", 3, false, false, 161.19999998807907},
			{"airAge1", 1, false, false, 161.19999998807907},
			{"floor", 3, true, false, 162},
			{"ceiling", 3, false, true, 160.79999995231628},
			{"sealed", 3, true, true, 162.000001},
		} {
			phase = "setup"
			horseID = 0
			command("kill @e[type=horse]")
			command("fill 3 157 -5 14 170 5 air")
			if tc.floor {
				command("fill 3 161 -5 14 161 5 stone")
			}
			if tc.ceiling {
				command("fill 3 164 -5 14 164 5 stone")
			}
			if tc.floor && !structureSaidBB(command("execute if block 8 161 0 stone run say RSPFloor"), "RSPFloor") {
				t.Fatal("packet floor missing")
			}
			if tc.ceiling && !structureSaidBB(command("execute if block 8 164 0 stone run say RSPCeiling"), "RSPCeiling") {
				t.Fatal("packet ceiling missing")
			}
			tag := "RSPacket" + tc.name
			horseSel = "@e[type=horse,tag=" + tag + ",limit=1]"
			command("summon horse 8.5 162 0.5 {Tags:[" + tag + "],NoAI:1b,NoGravity:1b,Rotation:[0f,0f]}")
			step(tc.admit)
			position(162)
			if horseID == 0 {
				t.Fatal("horse tracking admission missing")
			}
			phase = tc.name + "/write"
			command("attribute " + horseSel + " minecraft:scale base set 2")
			position(162)
			for i := 1; i <= 3; i++ {
				phase = fmt.Sprintf("%s/tick%d", tc.name, i)
				step(1)
				position(tc.y)
			}
			phase = tc.name + "/end"
			command("time query gametime")
		}
		t.Logf("%s source%d phases%v", addr, horseID, out)
		return out
	}
	want := run(targets.vanilla)
	got := run(targets.mcgo)
	moves, attrs := 0, 0
	for _, events := range want {
		for _, event := range events {
			if (strings.HasPrefix(event, "move ") && event != "move 0 0 0") || strings.HasPrefix(event, "absolute ") {
				moves++
			}
			if event == "scale 2.000000000" {
				attrs++
			}
		}
	}
	if moves != 3 || attrs != 5 {
		t.Fatalf("native resize witnesses movement%d scaleUpdates%d", moves, attrs)
	}
	// Native scale updates can arrive during frozen server frames after the
	// write, or with tick1; wire logs retain that arrival variation. Require
	// exactly the same values/counts by tick1, and keep every movement packet
	// (including zero deltas) bound to its exact world-step phase.
	normalize := func(input map[string][]string) map[string][]string {
		result := map[string][]string{}
		for phase, events := range input {
			for _, event := range events {
				key := phase
				if strings.HasPrefix(event, "scale ") && (strings.HasSuffix(phase, "/write") || strings.HasSuffix(phase, "/tick1")) {
					key = strings.Split(phase, "/")[0] + "/scaleByTick1"
				}
				result[key] = append(result[key], event)
			}
		}
		// Native observations also vary order within one frame.
		for _, events := range result {
			sort.Strings(events)
		}
		return result
	}
	want, got = normalize(want), normalize(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resize packet phases native%v Go%v", want, got)
	}
}
