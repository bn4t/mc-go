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

func TestDoorDirectionalMovementParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("DoorMove")}, nil)
	defer targets.cleanup()
	type fact struct{ facing, closed, left, right string }
	facts := []fact{{"north", "south", "west", "east"}, {"south", "north", "east", "west"}, {"west", "east", "south", "north"}, {"east", "west", "north", "south"}}
	type result struct {
		name             string
		coordinate, want float64
	}
	run := func(addr string) []result {
		conn, pr, pw := joinPlayBlackbox(t, addr, "DoorMove")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		await := func(marker string) {
			t.Helper()
			until := time.Now().Add(15 * time.Second)
			for time.Now().Before(until) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s marker%s missing", addr, marker)
		}
		sendCommand(t, pw, "gamemode creative @s")
		var out []result
		for _, f := range facts {
			for _, open := range []string{"false", "true"} {
				for _, hinge := range []string{"left", "right"} {
					side := f.closed
					if open == "true" {
						side = f.left
						if hinge == "right" {
							side = f.right
						}
					}
					sendCommand(t, pw, "tp @s 3 160 3")
					sendCommand(t, pw, "fill -2 160 -2 2 164 2 air")
					sendCommand(t, pw, "fill -2 159 -2 2 159 2 stone")
					properties := fmt.Sprintf("facing=%s,open=%s,hinge=%s,powered=false", f.facing, open, hinge)
					sendCommand(t, pw, "setblock 0 160 0 oak_door[half=lower,"+properties+"]")
					sendCommand(t, pw, "setblock 0 161 0 oak_door[half=upper,"+properties+"]")
					for _, blocked := range []bool{false, true} {
						from, to := [3]float64{.5, 160, .5}, [3]float64{.5, 160, .5}
						axis := 0
						switch side {
						case "west":
							from[0] = 1.3
							to[0] = .6
							if blocked {
								to[0] = .4
							}
						case "east":
							from[0] = -.3
							to[0] = .4
							if blocked {
								to[0] = .6
							}
						case "north":
							axis = 2
							from[2] = 1.3
							to[2] = .6
							if blocked {
								to[2] = .4
							}
						case "south":
							axis = 2
							from[2] = -.3
							to[2] = .4
							if blocked {
								to[2] = .6
							}
						}
						if blocked {
							from[1] = 161.9
							to[1] = 161.9
						}
						sendCommand(t, pw, fmt.Sprintf("tp @s %v %v %v", from[0], from[1], from[2]))
						marker := fmt.Sprintf("DOORPREP%d", len(out))
						sendCommand(t, pw, "say "+marker)
						await(marker)
						time.Sleep(150 * time.Millisecond)
						var b protocol.Buffer
						b.Double(to[0])
						b.Double(to[1])
						b.Double(to[2])
						b.Byte(0)
						if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
							t.Fatal(err)
						}
						time.Sleep(120 * time.Millisecond)
						position := dataGetVec3(t, sink, pw, "@s", "Pos")
						want := to[axis]
						if blocked {
							want = from[axis]
						}
						out = append(out, result{fmt.Sprintf("%s open%s hinge%s blocked%v", f.facing, open, hinge, blocked), position[axis], want})
					}
				}
			}
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, v := range vanilla {
		m := mcgo[i]
		if math.Abs(v.coordinate-v.want) > 1e-6 || math.Abs(m.coordinate-v.coordinate) > 1e-6 {
			t.Errorf("%s vanilla%v mcgo%v want%v", v.name, v.coordinate, m.coordinate, v.want)
		}
	}
}
