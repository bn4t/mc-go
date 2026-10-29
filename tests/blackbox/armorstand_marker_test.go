package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestArmorStandMarkerParity(t *testing.T) {
	const player = "MarkerReader"
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer tg.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		ids := map[int]int32{}
		flags := map[int32]int64{}
		serial := 0
		highestAck := int32(-1)
		collect := func(marker string) []string {
			var out []string
			until := time.Now().Add(15 * time.Second)
			for time.Now().Before(until) {
				batch := sink.take()
				out = append(out, batch...)
				done := false
				for _, line := range batch {
					if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)) {
						ack, _ := protocol.ReadVarInt(bytes.NewReader([]byte(strings.TrimPrefix(line, fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)))))
						if ack > highestAck {
							highestAck = ack
						}
					}
					if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)) {
						sp, ok := fwDecodeSpawn(fwPkt{data: []byte(strings.TrimPrefix(line, fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)))})
						if ok && sp.typ == 5 {
							ids[int(math.Round(sp.x))] = sp.eid
						}
					}
					if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)) {
						eid, entries := fwDecodeMeta([]byte(strings.TrimPrefix(line, fmt.Sprintf("%#x:", server.IDPlayEntityMetadata))))
						for _, e := range entries {
							if e.idx == 15 && e.typ == 0 {
								flags[eid] = e.i
							}
						}
					}
					if strings.Contains(line, marker) {
						done = true
					}
				}
				if done {
					return out
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing%s", addr, marker)
			return nil
		}

		clock := func() int64 {
			serial++
			marker := fmt.Sprintf("MARKCLOCK%d", serial)
			sendCommand(t, pw, "time query gametime")
			sendCommand(t, pw, "say "+marker)
			for _, line := range collect(marker) {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) > 0 {
					if v, ok := args[0].(int64); ok {
						return v
					}
				}
			}
			t.Fatal("missing marker clock")
			return 0
		}
		step := func(ticks int) {
			before := clock()
			until := time.Now().Add(10 * time.Second)
			for {
				time.Sleep(150 * time.Millisecond)
				next := clock()
				if next == before {
					break
				}
				if time.Now().After(until) {
					t.Fatal("marker clock did not freeze")
				}
				before = next
			}
			want := before + int64(ticks)
			sendCommand(t, pw, fmt.Sprintf("tick step %d", ticks))
			until = time.Now().Add(30 * time.Second)
			for time.Now().Before(until) {
				got := clock()
				if got > want {
					t.Fatalf("marker step%d overshot clock%d want%d", ticks, got, want)
				}
				if got == want {
					time.Sleep(150 * time.Millisecond)
					if clock() != want {
						t.Fatal("marker pending extra tick")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("marker step did not complete")
		}
		scalar := func(selector, path string) float64 {
			serial++
			marker := fmt.Sprintf("MARKVALUE%d", serial)
			sendCommand(t, pw, "data get entity "+selector+" "+path)
			sendCommand(t, pw, "say "+marker)
			for _, line := range collect(marker) {
				if i := strings.Index(line, "data.entity.query"); i >= 0 {
					return lastNumberSigned(line[:i])
				}
			}
			t.Fatalf("no%s", path)
			return 0
		}
		condition := func(cmd string) bool {
			serial++
			hit, end := fmt.Sprintf("MARKHIT%d", serial), fmt.Sprintf("MARKEND%d", serial)
			sendCommand(t, pw, cmd+" run say "+hit)
			sendCommand(t, pw, "say "+end)
			for _, line := range collect(end) {
				if strings.Contains(line, hit) {
					return true
				}
			}
			return false
		}
		selector := func(tag string) string { return "@e[type=armor_stand,tag=" + tag + ",limit=1]" }
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 3 165 3")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "fill -2 159 -2 15 159 2 stone")
		sendCommand(t, pw, `summon armor_stand 0.0 165.0 0.0 {Tags:["StandMark"],Marker:1b,Small:1b,ShowArms:1b}`)
		sendCommand(t, pw, `summon armor_stand 3.0 165.0 0.0 {Tags:["StandNormal"],NoGravity:1b}`)
		sendCommand(t, pw, `summon armor_stand 6.0 165.0 0.0 {Tags:["StandSmall"],Small:1b,NoGravity:1b}`)
		step(1)
		sendCommand(t, pw, "say MARKSPAWN")
		collect("MARKSPAWN")
		var out []float64
		mark := selector("StandMark")
		for _, tc := range []struct {
			tag          string
			x            float64
			wide, narrow bool
		}{{"StandMark", 0, false, false}, {"StandNormal", 3, true, true}, {"StandSmall", 6, false, true}} {
			for j, offset := range []float64{.2, .1} {
				got := condition(fmt.Sprintf("execute if entity @e[type=armor_stand,tag=%s,x=%.2f,y=164,z=-0.5,dx=0,dy=1,dz=0]", tc.tag, tc.x+offset))
				want := tc.wide
				if j == 1 {
					want = tc.narrow
				}
				out = append(out, boolFloat(got))
				if got != want {
					t.Errorf("%s %s bbox offset%.1f got%t want%t", addr, tc.tag, offset, got, want)
				}
			}
		}
		present := condition("execute if data entity " + mark + " Marker")
		out = append(out, boolFloat(present))
		if !present {
			t.Errorf("%s missing native Marker", addr)
		}
		f := flags[ids[0]]
		out = append(out, float64(f))
		if f != 21 {
			t.Errorf("%s spawn flags%d want21 ids%v", addr, f, ids)
		}
		step(20)
		y := scalar(mark, "Pos[1]")
		out = append(out, y)
		if math.Abs(y-165) > 1e-6 {
			t.Errorf("%s marker gravity moved y%v", addr, y)
		}
		sendCommand(t, pw, "damage "+mark+" 2 generic")
		health := scalar(mark, "Health")
		out = append(out, health)
		if health != 20 {
			t.Errorf("%s marker damaged health%v", addr, health)
		}
		for _, value := range []int{0, 1} {
			sendCommand(t, pw, fmt.Sprintf("data merge entity %s {Marker:%db,NoGravity:1b,Motion:[0d,0d,0d]}", mark, value))
			sendCommand(t, pw, "tp "+mark+" 0 165 0")
			step(1)
			sendCommand(t, pw, "say MARKMERGE")
			collect("MARKMERGE")
			f = flags[ids[0]]
			want := int64(5 + 16*value)
			out = append(out, float64(f))
			if f != want {
				t.Errorf("%s mergedMarker%d flags%d want%d", addr, value, f, want)
			}
			got := condition("execute if data entity " + mark + " Marker")
			out = append(out, boolFloat(got))
			if got != (value != 0) {
				t.Errorf("%s mergedMarker%d presence%t", addr, value, got)
			}
		}
		sendCommand(t, pw, `summon armor_stand 12.5 165.5 0.5 {Tags:["StandPushed"],Marker:1b}`)
		sendCommand(t, pw, "setblock 11 165 0 stone")
		sendCommand(t, pw, "setblock 10 165 0 piston[facing=east]")
		sendCommand(t, pw, "setblock 10 165 -1 redstone_block")
		step(3)
		x := scalar(selector("StandPushed"), "Pos[0]")
		out = append(out, x)
		if math.Abs(x-12.5) > 1e-6 {
			t.Errorf("%s piston moved Marker x%v", addr, x)
		}

		// Packet acknowledgement establishes placement processing before querying cells.
		sendCommand(t, pw, "tp @s 1.5 165 3.5")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with stone")
		sendCommand(t, pw, "tp "+mark+" 0.5 165 0.5")
		sendCommand(t, pw, "tp "+selector("StandNormal")+" 3.5 165 0.5")
		sendCommand(t, pw, "fill 0 164 0 3 164 0 stone")
		clock()
		for i, x := range []int32{0, 3} {
			seq := int32(100 + i)
			sendUseItemOn(t, pw, x, 164, 0, 1, seq)
			until := time.Now().Add(5 * time.Second)
			for highestAck < seq && time.Now().Before(until) {
				clock()
			}
			if highestAck < seq {
				t.Fatalf("%s placement ack%d absent", addr, seq)
			}
			got := condition(fmt.Sprintf("execute if block %d 165 0 stone", x))
			out = append(out, boolFloat(got))
			if got != (i == 0) {
				t.Errorf("%s marker placement x%d got%t want%t", addr, x, got, i == 0)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	for i := range v {
		if math.Abs(v[i]-g[i]) > 1e-6 {
			t.Errorf("observation%d vanilla=%v mcgo=%v", i, v[i], g[i])
		}
	}
}
