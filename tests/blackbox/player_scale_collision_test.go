package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlayerScalePoseMovementParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ScaleMove")}, nil)
	defer targets.cleanup()
	type result struct {
		x    float64
		pose int32
	}
	cases := []struct {
		name, scale   string
		commands      []string
		start, target float64
		sneak         bool
		pose          int32
		accepted      bool
	}{
		{"small_standing", "0.5", []string{"fill -2 161 -2 3 161 2 stone"}, .5, 1.1, false, 0, true},
		{"normal_crawl", "1", []string{"fill -2 161 -2 3 161 2 stone"}, .5, 1.1, false, 3, true},
		{"normal_crouch", "1", []string{"fill -2 161 -2 3 161 2 stone_slab[type=top]"}, .5, 1.1, false, 5, true},
		{"small_crouch_input", "0.5", []string{"fill -2 161 -2 3 161 2 stone"}, .5, 1.1, true, 5, true},
		{"large_crouch", "2", []string{"fill -2 163 -2 3 163 2 stone"}, .5, 1.1, false, 5, true},
		{"large_crawl", "2", []string{"fill -2 161 -2 3 161 2 stone_slab[type=top]"}, .5, 1.1, false, 3, true},
		{"modifier_scale", "0.25", []string{"attribute @s minecraft:scale modifier add minecraft:scale_add 0.25 add_value", "attribute @s minecraft:scale modifier add minecraft:scale_total 3 add_multiplied_total", "fill -2 163 -2 3 163 2 stone"}, .5, 1.1, false, 5, true},
		{"small_width", "0.5", []string{"setblock 1 160 0 chest"}, .4, .9, false, 0, true},
		{"large_width", "2", []string{"setblock 1 160 0 chest"}, 0, .5, false, 0, false},
		{"minimum_clamp", "0", []string{"setblock 1 160 0 stone"}, .9, .99, false, 0, false},
	}
	run := func(addr string) []result {
		conn, pr, pw := joinPlayBlackbox(t, addr, "ScaleMove")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		pose := int32(0)
		collect := func(marker string) []string {
			t.Helper()
			var lines []string
			until := time.Now().Add(15 * time.Second)
			for time.Now().Before(until) {
				batch := sink.take()
				lines = append(lines, batch...)
				done := false
				for _, line := range batch {
					if strings.Contains(line, marker) {
						done = true
					}
					prefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
					if strings.HasPrefix(line, prefix) {
						_, entries := fwDecodeMeta([]byte(strings.TrimPrefix(line, prefix)))
						for _, entry := range entries {
							if entry.idx == 6 && entry.typ == 20 {
								pose = int32(entry.i)
							}
						}
					}
				}
				if done {
					return lines
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s missing marker%s", addr, marker)
			return nil
		}
		queryX := func(marker string) float64 {
			sendCommand(t, pw, "data get entity @s Pos[0]")
			sendCommand(t, pw, "say "+marker)
			x := math.NaN()
			for _, line := range collect(marker) {
				if end := strings.Index(line, "data.entity.query"); end >= 0 {
					x = lastNumberSigned(line[:end])
				}
			}
			return x
		}
		sendCommand(t, pw, "gamemode creative @s")
		setupPlayerTickBarrierBB(t, pw)
		var out []result
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 8.0 160.0 8.0")
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0})
			waypointReady := false
			waypointDeadline := time.Now().Add(3 * time.Second)
			for attempt := 0; time.Now().Before(waypointDeadline); attempt++ {
				x := queryX(fmt.Sprintf("SCALEWAY%d_%d", i, attempt))
				if !math.IsNaN(x) && math.Abs(x-8) < 1e-6 && pose == 0 {
					waypointReady = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !waypointReady {
				t.Fatalf("%s %s acknowledged standing waypoint never established: pose%d", addr, tc.name, pose)
			}
			sendCommand(t, pw, "fill -2 159 -2 3 165 2 air")
			sendCommand(t, pw, "fill -2 159 -2 3 159 2 stone")
			sendCommand(t, pw, "attribute @s minecraft:scale modifier remove minecraft:scale_add")
			sendCommand(t, pw, "attribute @s minecraft:scale modifier remove minecraft:scale_total")
			sendCommand(t, pw, "attribute @s minecraft:scale base set "+tc.scale)
			for _, command := range tc.commands {
				sendCommand(t, pw, command)
			}
			sendCommand(t, pw, fmt.Sprintf("tp @s %.6f 160.0 0.5", tc.start))
			input := byte(0)
			if tc.sneak {
				input = 0x20
			}
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{input})
			marker := fmt.Sprintf("SCALEPREP%d", i)
			sendCommand(t, pw, "say "+marker)
			collect(marker)
			ready := false
			until := time.Now().Add(3 * time.Second)
			for attempt := 0; time.Now().Before(until); attempt++ {
				x := queryX(fmt.Sprintf("SCALESTART%d_%d", i, attempt))
				if !math.IsNaN(x) && math.Abs(x-tc.start) < 1e-6 && pose == tc.pose {
					ready = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !ready {
				t.Fatalf("%s %s start/pose never established: pose%d want%d", addr, tc.name, pose, tc.pose)
			}
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("SCALE%d", i), 2)
			var b protocol.Buffer
			b.Double(tc.target)
			b.Double(160)
			b.Double(.5)
			b.Byte(0)
			if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(250 * time.Millisecond)
			sendCommand(t, pw, "data get entity @s Pos[0]")
			marker = fmt.Sprintf("SCALEQUERY%d", i)
			sendCommand(t, pw, "say "+marker)
			lines := collect(marker)
			x := math.NaN()
			for _, line := range lines {
				if end := strings.Index(line, "data.entity.query"); end >= 0 {
					x = lastNumberSigned(line[:end])
				}
			}
			out = append(out, result{x, pose})
			t.Logf("%s %s x%v pose%d", addr, tc.name, x, pose)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		want := tc.start
		if tc.accepted {
			want = tc.target
		}
		v, m := vanilla[i], mcgo[i]
		if math.IsNaN(v.x) || math.IsNaN(m.x) || math.Abs(v.x-want) > 1e-6 || v.pose != tc.pose || math.Abs(m.x-v.x) > 1e-6 || m.pose != v.pose {
			t.Errorf("%s vanilla%+v mcgo%+v wantx%v pose%d", tc.name, v, m, want, tc.pose)
		}
	}
}
