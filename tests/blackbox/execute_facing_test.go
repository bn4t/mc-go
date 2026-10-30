//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExecuteFacingParity(t *testing.T) {
	const name = "FacingProbe"
	files := map[string]string{"ops.json": opsJSONFor(name), "world/datapacks/facepin/pack.mcmeta": `{"pack":{"description":"facing consumers","min_format":107,"max_format":107}}`,
		"world/datapacks/facepin/data/facepin/function/outer.mcfunction":   "function facepin:project\n",
		"world/datapacks/facepin/data/facepin/function/project.mcfunction": "tag @s add face_executed\ntp @e[tag=facing_out,limit=1] ^1 ^2 ^3 ~ ~\nexecute store result score projected_x facepin run data get entity @e[tag=facing_out,limit=1] Pos[0] 1000000\nexecute if score projected_x facepin matches 13000000..14000000 run scoreboard players add positive facepin 1\nexecute if score projected_x facepin matches 7000000..8000000 run scoreboard players add negative facepin 1\nscoreboard players add calls facepin 1\n",
	}
	directory := t.TempDir()
	for path, body := range files {
		if !strings.HasPrefix(path, "world/") {
			continue
		}
		local := filepath.Join(directory, strings.TrimPrefix(path, "world/"))
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("FACE_PIN_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
			}
			teleport := func(text string) {
				t.Helper()
				for _, line := range command(text) {
					key, _, ok := componentArgs(line)
					if ok && strings.HasPrefix(key, "commands.teleport.success") {
						return
					}
				}
				t.Fatalf("TP failed %s", text)
			}
			score := func(holder string, want int, tolerance int, label string) {
				t.Helper()
				marker := "FACE_PIN_EXPECT_" + label
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s facepin matches %d..%d run say %s", holder, want-tolerance, want+tolerance, marker)), marker) {
					t.Errorf("%s want%d", label, want)
				}
			}
			command("tick freeze")
			teleport("tp @s 10.5 -58 20.5 0 0")
			command("scoreboard objectives add facepin dummy")
			command(`summon minecraft:armor_stand 0 -58 0 {Tags:["facing_out"],NoGravity:1b,Marker:1b}`)
			command(`summon minecraft:cow 20.5 -58 20.5 {Tags:["facing_target","face_group"],NoGravity:1b,NoAI:1b}`)
			cases := []struct {
				source   string
				position [3]int
			}{
				{"execute positioned 10 -58 20 facing 10 -58 30", [3]int{11500287, -56000000, 23499904}},
				{"execute positioned 10 -58 20 facing 0 -58 20", [3]int{7500095, -56000000, 21500287}},
				{"execute positioned 10 -58 20 facing 20 -58 20", [3]int{13500000, -56000000, 19500000}},
				{"execute positioned 10 -58 20 facing 10 -48 20", [3]int{8500287, -55000000, 19500000}},
				{"execute positioned 10 -58 20 facing 10 -68 20", [3]int{12500000, -61000000, 19500000}},
				{"execute positioned 10 -58 20 facing 10 -58 20", [3]int{13500000, -56000000, 19500000}},
				{"execute positioned 10 -58 20 facing ~ ~ ~10", [3]int{11500287, -56000000, 23499904}},
				{"execute positioned 10 -58 20 anchored eyes facing 10 -58 30", [3]int{11500299, -54885462, 23781245}},
				{"execute positioned 10 -58 20 facing entity @e[tag=facing_target,limit=1] feet", [3]int{13500000, -56000000, 19500000}},
				{"execute positioned 10 -58 20 facing entity @e[tag=facing_target,limit=1] eyes", [3]int{13217224, -55630044, 19499999}},
				{"execute positioned 10 -58 20 anchored eyes facing entity @e[tag=facing_target,limit=1] eyes", [3]int{13562494, -54476787, 19500003}},
				{"execute positioned 10 -58 20 rotated 90 30", [3]int{6901908, -57767931, 21499958}},
			}
			for index, tc := range cases {
				teleport(tc.source + " run tp @e[tag=facing_out,limit=1] ^1 ^2 ^3 ~ ~")
				for axis, want := range tc.position {
					command(fmt.Sprintf("execute store result score v facepin run data get entity @e[tag=facing_out,limit=1] Pos[%d] 1000000", axis))
					score("v", want, 1, fmt.Sprintf("position_%d_%d", index, axis))
				}
			}
			// A nested file function retains the facing frame.
			command("scoreboard players set positive facepin 0")
			command("scoreboard players set negative facepin 0")
			command("scoreboard players set calls facepin 0")
			command("execute positioned 10 -58 20 facing 20 -58 20 run function facepin:outer")
			score("positive", 1, 0, "nested_direction")
			score("calls", 1, 0, "nested_calls")
			command(`summon minecraft:cow 0.5 -58 20.5 {Tags:["face_group"],NoGravity:1b,NoAI:1b}`)
			command("scoreboard players set positive facepin 0")
			command("scoreboard players set negative facepin 0")
			command("scoreboard players set calls facepin 0")
			command("execute store result score domain facepin run execute if entity @e[tag=face_group]")
			score("domain", 2, 0, "fork_domain")
			command("execute positioned 10 -58 20 facing entity @e[tag=face_group] feet run function facepin:project")
			score("positive", 1, 0, "fork_positive")
			score("negative", 1, 0, "fork_negative")
			score("calls", 2, 0, "fork_calls")
			for label, condition := range map[string]string{"executor_kept": "if entity @s[tag=face_executed]", "targets_unbound": "unless entity @e[tag=face_group,tag=face_executed]"} {
				marker := "FACE_PIN_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("%s", label)
				}
			}
			// Cardinal/signed-zero and near-vertical facing are also sampled through
			// the public-API float-angle corpus, independently of the local model.
			raw, err := os.ReadFile("../../tools/probefacingmath/public-facing.tsv")
			if err != nil {
				t.Fatal(err)
			}
			for index, target := range [][3]string{{"-0.0", "10.0", "0.0"}, {"0.000001", "10.0", "0.000001"}, {"-0.000001", "-10.0", "0.000001"}} {
				values := [3]float64{}
				for i, text := range target {
					values[i], err = strconv.ParseFloat(text, 64)
					if err != nil {
						t.Fatal(err)
					}
				}
				var angles [2]float32
				found := false
				for _, line := range strings.Split(string(raw), "\n") {
					a := strings.Split(line, "\t")
					if len(a) < 6 || a[0] != "face" {
						continue
					}
					match := true
					for i := 0; i < 3; i++ {
						n, _ := strconv.ParseFloat(a[1+i], 64)
						if n != values[i] {
							match = false
						}
					}
					if !match {
						continue
					}
					for i := 0; i < 2; i++ {
						bits, e := strconv.ParseUint(a[4+i], 16, 32)
						if e != nil {
							t.Fatal(e)
						}
						angles[i] = math.Float32frombits(uint32(bits))
					}
					found = true
					break
				}
				if !found {
					t.Fatal("edge reference missing")
				}
				teleport("tp @e[tag=facing_out,limit=1] 0 -58 0 0 0")
				teleport("execute positioned 0.0 0.0 0.0 facing " + strings.Join(target[:], " ") + " run tp @e[tag=facing_out,limit=1] ^1 ^2 ^3 ~ ~")
				for axis, want := range angles {
					command(fmt.Sprintf("execute store result score v facepin run data get entity @e[tag=facing_out,limit=1] Rotation[%d] 1000000", axis))
					score("v", int(math.Floor(float64(want)*1000000)), 1, fmt.Sprintf("edge_%d_%d", index, axis))
				}
			}
		})
	}
}
