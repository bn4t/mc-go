//go:build blackbox

package blackbox

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCommandLocalCoordinatesParity(t *testing.T) {
	type reference struct {
		rotation [2]float32
		basis    [3][3]float64
	}
	input, err := os.Open("../../tools/probecommandmath/public-math.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var references []reference
	indices := map[string]int{}
	scan := bufio.NewScanner(input)
	number := func(text string) float64 {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for scan.Scan() {
		a := strings.Split(scan.Text(), "\t")
		if a[0] != "basis" {
			continue
		}
		yaw, pitch := float32(number(a[1])), float32(number(a[2]))
		if yaw < -180 || yaw >= 180 || pitch < -90 || pitch > 90 {
			continue
		}
		key := a[1] + "," + a[2]
		index, ok := indices[key]
		if !ok {
			index = len(references)
			indices[key] = index
			references = append(references, reference{rotation: [2]float32{yaw, pitch}})
		}
		axis, err := strconv.Atoi(a[3])
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			references[index].basis[axis][i] = number(a[4+i])
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if len(references) < 24 {
		t.Fatalf("insufficient bounded reference rotations%d", len(references))
	}
	references = references[:24]
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("LocalMathProbe")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "LocalMathProbe")
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("LOCAL_MATH_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
			}
			command("tick freeze")
			command("tp @s 10.5 -58 20.5 0 0")
			command("scoreboard objectives add local_math dummy")
			command(`summon minecraft:armor_stand 0 -58 0 {Tags:["local_math_out"],NoGravity:1b,Marker:1b}`)
			for index, ref := range references {
				yaw := strconv.FormatFloat(float64(ref.rotation[0]), 'f', -1, 32)
				pitch := strconv.FormatFloat(float64(ref.rotation[1]), 'f', -1, 32)
				success := false
				for _, line := range command("execute positioned 10 -58 20 rotated " + yaw + " " + pitch + " run tp @e[tag=local_math_out,limit=1] ^1 ^2 ^3") {
					key, _, ok := componentArgs(line)
					if ok && strings.HasPrefix(key, "commands.teleport.success") {
						success = true
					}
				}
				if !success {
					t.Fatalf("rotation%d no actual TP success", index)
				}
				for axis, base := range []float64{10.5, -58, 20.5} {
					want := base
					for basis := 0; basis < 3; basis++ {
						want += ref.basis[basis][axis] * float64(basis+1)
					}
					scaled := int(math.Floor(want * 1000000))
					command(fmt.Sprintf("execute store result score v local_math run data get entity @e[tag=local_math_out,limit=1] Pos[%d] 1000000", axis))
					marker := fmt.Sprintf("LOCAL_MATH_EXPECT_%d_%d", index, axis)
					if !structureSaidBB(command(fmt.Sprintf("execute if score v local_math matches %d..%d run say %s", scaled-1, scaled+1, marker)), marker) {
						t.Errorf("rotation%v axis%d want%d (+/-1)", ref.rotation, axis, scaled)
					}
				}
			}
		})
	}
}
