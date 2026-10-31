package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlayerPowderPlacementParity(t *testing.T) {
	type shape struct {
		name    string
		x, y, z float64
		cells   [][3]int
		legacy  bool
	}
	shapes := []shape{
		{"foot", 8.5, -50, 2.5, [][3]int{{8, -50, 2}}, false},
		{"column", 8.5, -50, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}}, false},
		{"edge", 8.75, -50, 2.5, [][3]int{{8, -50, 2}, {9, -50, 2}}, false},
		{"edge_column", 8.75, -50, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {9, -50, 2}, {9, -49, 2}}, false},
		{"floating", 8.5, -49, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}, {8, -47, 2}}, false},
		{"legacy", 33.75, -59, 33.5, nil, true},
	}
	for x := 33; x <= 34; x++ {
		for y := -60; y <= -56; y++ {
			for z := 33; z <= 34; z++ {
				shapes[len(shapes)-1].cells = append(shapes[len(shapes)-1].cells, [3]int{x, y, z})
			}
		}
	}
	shapes = append(shapes, shape{"floating_zero", 8.5, -49, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}, {8, -47, 2}}, false}, shape{"floating_levitation", 8.5, -49, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}, {8, -47, 2}}, false})
	shapes = append(shapes, shape{"floating_levitation_default", 8.5, -49, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}, {8, -47, 2}}, false})
	for _, gap := range []struct {
		name string
		y    float64
	}{{"gap_tiny", -49.999999}, {"gap_small", -49.9999}, {"gap_below", -49.999684}, {"gap_above", -49.999683}, {"gap_visible", -49.99}} {
		shapes = append(shapes, shape{gap.name, 8.5, gap.y, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}}, false})
	}
	shapes = append(shapes,
		shape{"floating_fractional", 8.5, -49.5, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}, {8, -47, 2}}, false},
		shape{"floating_no_lower", 8.5, -49, 2.5, [][3]int{{8, -49, 2}, {8, -48, 2}}, false},
		shape{"levitation_top_edge", 8.5, -49.801, 2.5, [][3]int{{8, -50, 2}, {8, -49, 2}, {8, -48, 2}}, false},
	)
	shapes = append(shapes, shape{"upper_only_tripwire", .5, 10.4, .5, [][3]int{{0, 11, 0}}, false}, shape{"upper_only_tripwire_creative", .5, 10.4, .5, [][3]int{{0, 11, 0}}, false})
	fields := []string{"Frozen", "Tick", "Health", "X", "Y", "Z", "World", "Ground", "Gravity", "MotionY"}
	snapBody := "scoreboard players set Frozen PPState 0\nexecute store result score Frozen PPState run data get entity @s TicksFrozen\nscoreboard players operation Tick PPState = @s BBPlayerTick\nexecute store result score Health PPState run data get entity @s Health 1000\nexecute store result score X PPState run data get entity @s Pos[0] 1000000\nexecute store result score Y PPState run data get entity @s Pos[1] 1000000\nexecute store result score Z PPState run data get entity @s Pos[2] 1000000\nexecute store result score World PPState run time query gametime\nexecute store result score Ground PPState run data get entity @s OnGround\nexecute store result score Gravity PPState run attribute @s gravity get 1000000\nexecute store result score MotionY PPState run data get entity @s Motion[1] 1000000\nsay PPATOM\n"
	dir := t.TempDir()
	files := map[string]string{}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"powder/frame atomic observations","min_format":107,"max_format":107}}`, "data/pp/function/snapshot.mcfunction": snapBody}
	var names []string
	for mode := 0; mode < 2; mode++ {
		for i := range shapes {
			names = append(names, fmt.Sprintf("PowP%d%d", mode, i))
		}
	}
	files["ops.json"] = opsJSONFor(names...)
	for _, s := range shapes {
		body := ""
		if strings.HasPrefix(s.name, "upper_only_tripwire") {
			body = "setblock 0 10 0 tripwire\n"
		}
		for _, p := range s.cells {
			body += fmt.Sprintf("setblock %d %d %d powder_snow\n", p[0], p[1], p[2])
		}
		conditions := "execute"
		if strings.HasPrefix(s.name, "upper_only_tripwire") {
			conditions += " if block 0 10 0 tripwire"
		}
		for _, p := range s.cells {
			conditions += fmt.Sprintf(" if block %d %d %d powder_snow", p[0], p[1], p[2])
		}
		body += conditions + " run say PPCELLS\n"
		bodies["data/pp/function/begin_"+s.name+".mcfunction"] = body + "function pp:snapshot\nsay PPBEGIN\n"
	}
	for name, body := range bodies {
		rel := "datapacks/pp/" + name
		files["world/"+rel] = body
		p := filepath.Join(dir, rel)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, files, func(s *server.Server) {
		s.WorldDir = dir
		s.OpsRequired = true
		for _, n := range names {
			s.SetOpLevel(n, 4)
		}
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			for mode := 0; mode < 2; mode++ {
				for i, shape := range shapes {
					label := fmt.Sprintf("mode%d_%s", mode, shape.name)
					t.Run(label, func(t *testing.T) {
						rate := int64(1)
						if shape.name == "floating" || shape.name == "legacy" || shape.name == "levitation_top_edge" {
							rate = 2
						}
						name := fmt.Sprintf("PowP%d%d", mode, i)
						c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
						defer c.Close()
						awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
						sink := newChatSinkTP(pr, pw)
						collect := func(marker string) []string {
							var lines []string
							until := time.Now().Add(15 * time.Second)
							for !structureSaidBB(lines, marker) && time.Now().Before(until) {
								lines = append(lines, sink.take()...)
								time.Sleep(10 * time.Millisecond)
							}
							if !structureSaidBB(lines, marker) {
								t.Fatal("powder receipt missing", marker)
							}
							return lines
						}
						seq := 0
						expect := func(cond string) {
							seq++
							m := fmt.Sprintf("PPEXPECT%d", seq)
							sendCommand(t, pw, cond+" run say "+m)
							sendCommand(t, pw, "say "+m+"END")
							if !structureSaidBB(collect(m+"END"), m) {
								t.Fatal("powder apparatus", cond)
							}
						}
						advance := func(n int) { seq++; waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("PPFRAME%d", seq), n) }
						type state struct{ f, t, h, x, y, z, w, g, gravity, motionY int64 }
						snapshot := func(phase, command string) state {
							sendCommand(t, pw, command)
							for _, field := range fields {
								sendCommand(t, pw, "scoreboard players get "+field+" PPState")
							}
							seq++
							m := fmt.Sprintf("PPSNAP%d", seq)
							sendCommand(t, pw, "say "+m)
							lines := collect(m)
							if !structureSaidBB(lines, "PPATOM") {
								t.Fatal("atomic snapshot absent")
							}
							if command != "function pp:snapshot" && !structureSaidBB(lines, "PPCELLS") {
								t.Fatal("source geometry was not applied")
							}
							if command != "function pp:snapshot" && !structureSaidBB(lines, "PPBEGIN") {
								t.Fatal("atomic insertion absent")
							}
							var vals []int64
							for _, line := range lines {
								key, args, ok := componentArgs(line)
								if !ok || key != "commands.scoreboard.players.get.success" || len(args) < 2 {
									continue
								}
								v := args[1]
								if q, ok := v.(map[string]any); ok {
									v = q[""]
								}
								n, e := strconv.ParseInt(fmt.Sprint(v), 10, 64)
								if e != nil {
									t.Fatal(v)
								}
								vals = append(vals, n)
							}
							if len(vals) != 10 {
								t.Fatal("typed tuple missing", vals)
							}
							got := state{vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7], vals[8], vals[9]}
							if got.f < 0 || got.f >= 140 || got.t < 0 || got.h != 100000 {
								t.Fatal("uncapped/live tuple invalid", got)
							}
							if phase != "source-before-entry" {
								wantX := shape.x
								if strings.HasPrefix(phase, "moved-") {
									wantX += .05
								}
								expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,%gd,%gd]}", wantX, shape.y, shape.z))
							}
							t.Logf("%s %s %s: Frozen=%d Tick=%d PosMicro=[%d,%d,%d] World=%d Ground=%d GravityMicro=%d MotionYMicro=%d", leg.name, label, phase, got.f, got.t, got.x, got.y, got.z, got.w, got.g, got.gravity, got.motionY)
							return got
						}
						setupPlayerTickBarrierBB(t, pw)
						for _, cmd := range []string{"tick freeze", "gamemode survival @s", "difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "forceload add 0 0", "forceload add 32 32", "scoreboard objectives add PPState dummy"} {
							sendCommand(t, pw, cmd)
						}
						until := time.Now().Add(20 * time.Second)
						for {
							sendCommand(t, pw, "execute if loaded 33 -59 33 if loaded 8 -50 2 run say PPLOADED")
							sendCommand(t, pw, "say PPLOADEND")
							if structureSaidBB(collect("PPLOADEND"), "PPLOADED") {
								break
							}
							if time.Now().After(until) {
								t.Fatal("powder arena absent")
							}
						}
						if strings.HasPrefix(shape.name, "upper_only_tripwire") {
							sendCommand(t, pw, "fill -1 9 -1 2 14 2 air")
						}
						for _, cmd := range []string{"fill 7 -50 1 10 -47 4 air", "fill 32 -60 32 35 -55 35 air", "fill 0 -51 0 14 -51 4 stone", "fill 32 -61 32 35 -61 35 stone", "attribute @s max_health base set 100", "effect give @s instant_health 1 10 true"} {
							sendCommand(t, pw, cmd)
						}
						if shape.name == "floating_zero" || shape.name == "floating_levitation" {
							sendCommand(t, pw, "attribute @s gravity base set 0")
							expect(`execute if data entity @s {attributes:[{id:"minecraft:gravity",base:0.0d}]}`)
						}
						if strings.Contains(shape.name, "levitation") {
							sendCommand(t, pw, "effect give @s levitation 10 0 true")
							expect(`execute if data entity @s {active_effects:[{id:"minecraft:levitation"}]}`)
						}
						wantMode := 0
						if strings.HasSuffix(shape.name, "_creative") {
							sendCommand(t, pw, "gamemode creative @s")
							wantMode = 1
						}
						advance(3)
						expect(fmt.Sprintf(`execute if data entity @s {Health:100.0f,playerGameType:%d,Fire:-20s} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`, wantMode))
						if mode == 1 {
							sendCommand(t, pw, "tick unfreeze")
						}
						if shape.legacy {
							snapshot("source-before-entry", "function pp:begin_"+shape.name)
						}
						sendCommand(t, pw, fmt.Sprintf("tp @s %.9f %.9f %.9f", shape.x, shape.y, shape.z))
						expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,%gd,%gd]}", shape.x, shape.y, shape.z))
						advance(6)
						if !shape.legacy {
							snapshot("insertion", "function pp:begin_"+shape.name)
						}
						a := snapshot("stationary-A", "function pp:snapshot")
						advance(3)
						b := snapshot("stationary-B", "function pp:snapshot")
						advance(3)
						d := snapshot("stationary-C", "function pp:snapshot")
						for _, pair := range [][2]state{{a, b}, {b, d}} {
							aa, bb := pair[0], pair[1]
							if bb.t <= aa.t {
								t.Fatal("player frames absent", aa, bb)
							}
							if mode == 0 && bb.w != aa.w {
								t.Error("frozen world advanced")
							}
							if mode == 1 && bb.w <= aa.w {
								t.Error("ordinary world did not advance")
							}
							if bb.f-aa.f != rate*(bb.t-aa.t) {
								t.Errorf("stationary native rate%d/frame: Frozen%d ->%d over%d actual frames", rate, aa.f, bb.f, bb.t-aa.t)
							}
							if aa.x != bb.x || aa.y != bb.y || aa.z != bb.z {
								t.Error("stationary body position changed", aa, bb)
							}
							t.Logf("%s %s stationary deltaFrozen=%d deltaFrames=%d bodySame=%v", leg.name, label, bb.f-aa.f, bb.t-aa.t, aa.x == bb.x && aa.y == bb.y && aa.z == bb.z)
						}
						// One packet only; read-only Pos polling proves applied displacement.
						movedX := shape.x + .05
						var move protocol.Buffer
						move.Double(movedX)
						move.Double(shape.y)
						move.Double(shape.z)
						move.Byte(0)
						if e := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); e != nil {
							t.Fatal(e)
						}
						accepted := false
						until = time.Now().Add(5 * time.Second)
						for !accepted && time.Now().Before(until) {
							seq++
							m := fmt.Sprintf("PPMOVED%d", seq)
							sendCommand(t, pw, fmt.Sprintf("execute if data entity @s {Pos:[%gd,%gd,%gd]} run say %s", movedX, shape.y, shape.z, m))
							sendCommand(t, pw, "say "+m+"END")
							accepted = structureSaidBB(collect(m+"END"), m)
						}
						if !accepted {
							t.Fatal("single movement not applied")
						}
						advance(3)
						a = snapshot("moved-A", "function pp:snapshot")
						advance(3)
						b = snapshot("moved-B", "function pp:snapshot")
						advance(3)
						d = snapshot("moved-C", "function pp:snapshot")
						for _, pair := range [][2]state{{a, b}, {b, d}} {
							aa, bb := pair[0], pair[1]
							if bb.t <= aa.t {
								t.Fatal("moved player frames absent")
							}
							if bb.f-aa.f != rate*(bb.t-aa.t) {
								t.Errorf("settled moved native rate%d/frame: Frozen%d ->%d over%d actual frames", rate, aa.f, bb.f, bb.t-aa.t)
							}
							if aa.x != bb.x || aa.y != bb.y || aa.z != bb.z {
								t.Error("settled body position changed", aa, bb)
							}
							if mode == 0 && bb.w != aa.w {
								t.Error("frozen moved world advanced")
							}
							if mode == 1 && bb.w <= aa.w {
								t.Error("ordinary moved world did not advance")
							}
							t.Logf("%s %s moved deltaFrozen=%d deltaFrames=%d bodySame=%v", leg.name, label, bb.f-aa.f, bb.t-aa.t, aa.x == bb.x && aa.y == bb.y && aa.z == bb.z)
						}
					})
				}
			}
		})
	}
}
