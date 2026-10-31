package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A free armor stand supplies physical movement independently of
// resize. Compare exact relative packet integers after witnessing actual Pos.
func TestEntityPositionQuantizerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("PositionRead")}, nil)
	defer targets.cleanup()
	run := func(addr string) [][][3]int16 {
		c, pr, pw := joinPlayBlackbox(t, addr, "PositionRead")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		ids := server.LatestVersion().IDs
		standID := int32(0)
		record := false
		var moves [][3]int16
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
					if e1 == nil && e2 == nil && e3 == nil && typ == mustEType(t, "armor_stand") {
						standID = eid
					}
					continue
				}
				if !record {
					continue
				}
				if int32(id) != ids.PlayMoveEntityPos && int32(id) != ids.PlayMoveEntityPosRot && int32(id) != ids.PlayEntityTeleportCB {
					continue
				}
				eid, err := r.VarInt()
				if err != nil || eid != standID {
					continue
				}
				if int32(id) == ids.PlayEntityTeleportCB {
					t.Fatal("unexpected absolute stand motion")
				}
				x, e1 := r.Short()
				y, e2 := r.Short()
				z, e3 := r.Short()
				if e1 != nil || e2 != nil || e3 != nil {
					t.Fatal("short stand movement")
				}
				moves = append(moves, [3]int16{x, y, z})
			}
			return lines
		}
		serial := 0
		command := func(cmd string) []string {
			serial++
			tag := fmt.Sprintf("QReceipt%d", serial)
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
			t.Fatal("missing quantizer clock")
			return 0
		}
		step := func() {
			old := clock()
			command("tick step 1")
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				got := clock()
				if got > old+1 {
					t.Fatal("quantizer step overrun")
				}
				if got == old+1 {
					time.Sleep(150 * time.Millisecond)
					if clock() != got {
						t.Fatal("quantizer clock not stable")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("quantizer step missing")
		}
		command("gamemode creative @s")
		command("tp @s 0.5 162 0.5")
		command("tick freeze")
		command("fill 3 155 -5 14 170 5 air")
		var out [][][3]int16
		for i, v := range []float64{-1.2000000476837158, .800000011920929, -.800000011920929} {
			record = false
			standID = 0
			moves = nil
			tag := fmt.Sprintf("QStand%d", i)
			selector := "@e[type=armor_stand,tag=" + tag + ",limit=1]"
			command(fmt.Sprintf("summon armor_stand 8.5 162 0.5 {Tags:[%s],NoGravity:0b,Motion:[0d,%.17gd,0d],Rotation:[0f,0f]}", tag, v))
			if standID == 0 {
				t.Fatal("stand tracking admission missing")
			}
			record = true
			step()
			valid := false
			for _, line := range command("data get entity " + selector + " Pos") {
				if !strings.Contains(line, "data.entity.query") {
					continue
				}
				value, ok := parseSNBT(line)
				list, yes := value.([]any)
				if !ok || !yes || len(list) != 3 {
					continue
				}
				y, ok := hrNum(list[1])
				if !ok || math.IsNaN(y) || math.IsInf(y, 0) || math.Abs(y-(162+v)) > 1e-8 {
					t.Errorf("%s sample%d y%.17g want%.17g", addr, i, y, 162+v)
				}
				valid = true
				t.Logf("%s sample%d y%.17g moves%v", addr, i, y, moves)
			}
			var displacement [][3]int16
			for _, move := range moves {
				if move != [3]int16{} {
					displacement = append(displacement, move)
				}
			}
			if !valid || len(displacement) != 1 {
				t.Fatalf("%s sample%d physical displacement count%d want1 (raw%v)", addr, i, len(displacement), moves)
			}
			// Frozen network frames can emit a zero spawn resend before
			// movement. Raw packets remain logged; the quantizer pin requires
			// one exact nonzero physical displacement, not tracker-clock parity.
			out = append(out, append([][3]int16(nil), displacement...))
		}
		return out
	}
	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("position quantizer native%v Go%v", want, got)
	}
}
