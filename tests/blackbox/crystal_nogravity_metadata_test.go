//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCrystalNoGravityDirtyLatchParity(t *testing.T) {
	const actor = "CrystalFlagRead"
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(actor)}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"crystal gravity latch","min_format":107,"max_format":107}}`}
	for name, values := range map[string][]int{"same_true": {1, 0, 1}, "same_false": {0, 1, 0}, "repeat_true": {1, 1, 1}, "repeat_false": {0, 0, 0}} {
		body := "execute store result score frame_before gravframe run time query gametime\n"
		for _, value := range values {
			body += fmt.Sprintf("data merge entity @e[type=end_crystal,limit=1] {NoGravity:%db}\n", value)
		}
		body += "execute store result score frame_after gravframe run time query gametime\nscoreboard players add calls gravframe 1\n"
		bodies["data/gravframe/function/"+name+".mcfunction"] = body
	}
	for path, body := range bodies {
		rel := "datapacks/gravframe/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, files, func(s *server.Server) { s.WorldDir = directory })
	defer targets.cleanup()
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		eid := int32(-1)
		var flags []bool
		serial := 0
		process := func(lines []string) {
			for _, line := range lines {
				if body, ok := gravityPacketBody(line, server.IDPlaySpawnEntity); ok {
					r := protocol.NewPacketReader(body)
					id, err := r.VarInt()
					if err != nil {
						t.Fatal(err)
					}
					_, _ = r.UUID()
					typ, err := r.VarInt()
					if err != nil {
						t.Fatal(err)
					}
					name, _ := server.EntityTypeName(typ)
					if name == "end_crystal" {
						eid = id
					}
				}
				if body, ok := gravityPacketBody(line, server.IDPlayEntityMetadata); ok {
					id, entries := fwDecodeMeta(body)
					if id == eid {
						for _, entry := range entries {
							if entry.idx == 5 {
								if entry.typ != 8 {
									t.Fatal("crystal NoGravity serializer")
								}
								flags = append(flags, entry.b)
							}
						}
					}
				}
			}
		}
		command := func(cmd string) []string {
			serial++
			mark := fmt.Sprintf("CRYSTAL_FLAG_BARRIER_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+mark)
			lines := waitForExactSayBB(t, sink, mark, 30*time.Second)
			process(lines)
			return lines
		}
		clock := func() int64 {
			for _, line := range command("time query gametime") {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) > 0 {
					if n, ok := args[0].(int64); ok {
						return n
					}
				}
			}
			t.Fatal("missing crystal clock")
			return 0
		}
		step := func() {
			before := clock()
			deadline := time.Now().Add(30 * time.Second)
			for {
				time.Sleep(150 * time.Millisecond)
				now := clock()
				if now == before {
					break
				}
				before = now
				if time.Now().After(deadline) {
					t.Fatal("crystal freeze not stable")
				}
			}
			command("tick step 1")
			for time.Now().Before(deadline) {
				now := clock()
				if now > before+1 {
					t.Fatal("crystal tick overshoot")
				}
				if now == before+1 {
					time.Sleep(150 * time.Millisecond)
					if clock() != now {
						t.Fatal("crystal extra tick")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("crystal tick timeout")
		}
		command("gamemode creative @s")
		command("tp @s 4.0 165.0 4.0")
		command("tick freeze")
		command("scoreboard objectives add gravframe dummy")
		command(`summon end_crystal 0.500000000 165.000000000 0.500000000 {Tags:["GravityCrystal"],NoGravity:1b,Invulnerable:1b}`)
		step()
		if eid < 0 || len(flags) != 2 || !flags[0] || !flags[1] {
			t.Fatalf("%s crystal initial id%d flags%v", addr, eid, flags)
		}
		var out []string
		calls := 0
		for _, tc := range []struct {
			name, cmd string
			count     int
			value     bool
			function  bool
		}{{"same_true", "function gravframe:same_true", 1, true, true}, {"repeat_true", "function gravframe:repeat_true", 0, true, true}, {"single_false", "data merge entity @e[type=end_crystal,limit=1] {NoGravity:0b}", 1, false, false}, {"same_false", "function gravframe:same_false", 1, false, true}, {"repeat_false", "function gravframe:repeat_false", 0, false, true}, {"single_true", "data merge entity @e[type=end_crystal,limit=1] {NoGravity:1b}", 1, true, false}} {
			start := len(flags)
			command(tc.cmd)
			step()
			values := flags[start:]
			valid := len(values) == tc.count
			for _, v := range values {
				valid = valid && v == tc.value
			}
			t.Logf("%s %s flags%v", addr, tc.name, values)
			if !valid {
				t.Errorf("%s %s flags%v want%d of%t", addr, tc.name, values, tc.count, tc.value)
			}
			out = append(out, fmt.Sprintf("%s:%t", tc.name, valid))
			witness := "CRYSTAL_FINAL_" + tc.name
			conditional := "if"
			if !tc.value {
				conditional = "unless"
			}
			lines := command(fmt.Sprintf("execute %s entity @e[type=end_crystal,limit=1,nbt={NoGravity:1b}] run say %s", conditional, witness))
			if !structureSaidBB(lines, witness) {
				t.Fatalf("%s %s final NBT flag did not apply", addr, tc.name)
			}
			if tc.function {
				calls++
				mark := fmt.Sprintf("CRYSTAL_ATOMIC_%d", calls)
				lines := command(fmt.Sprintf("execute if score frame_before gravframe = frame_after gravframe if score calls gravframe matches %d run say %s", calls, mark))
				if !structureSaidBB(lines, mark) {
					t.Fatalf("%s %s callback not atomic/executed", addr, tc.name)
				}
			}
		}
		command("tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(g) {
		t.Errorf("crystal NoGravity differs vanilla%v mcgo%v", v, g)
	}
}
