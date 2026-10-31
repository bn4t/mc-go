package blackbox

import (
	"encoding/binary"
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Linked entity IDs, effective scales, saved forms and live
// executor markers remain separate. Atomic functions preserve pre-player-frame
// snapshots even though admitted player frames continue in a frozen world.
func TestScaledMountAttachmentParity(t *testing.T) {
	// Exact native wire observations from linked, admitted default/.5/2
	// controls. Y is live in both forms; saved horizontal coordinates use the
	// direct vehicle. No packet-relative offset is treated as an entity Pos.
	type state struct{ rootY, childY, actorY, z float64 }
	expected := map[string]map[string]state{
		"chickenPlayer": {
			"default":             {162, 0, 162.1, .4},
			"vehicle0.5After":     {162, 0, 161.75, .45},
			"vehicle2After":       {161.4750000089407, 0, 162.2750000089407, .3},
			"actor2AfterFrames":   {161.4750000089407, 0, 161.6750000089407, .3},
			"actor0.5AfterFrames": {161.4750000089407, 0, 162.5750000089407, .3},
		},
		"horsePlayer": {
			"default":             {162, 0, 162.84375002384186, .5},
			"vehicle0.5After":     {162, 0, 162.12187501192093, .5},
			"vehicle2After":       {160.7999999821186, 0, 163.08750002980233, .5},
			"actor2AfterFrames":   {160.7999999821186, 0, 162.48750002980233, .5},
			"actor0.5AfterFrames": {160.7999999821186, 0, 163.3875000298023, .5},
		},
		"chickenZombiePlayer": {
			"default":             {162, 162.00000001192092, 163.41250005960464, .4},
			"vehicle0.5After":     {162, 161.65000001192092, 163.06250005960464, .45},
			"vehicle2After":       {161.4750000089407, 162.17500002086163, 163.58750006854535, .3},
			"mobRider2After":      {161.4750000089407, 161.47500003278256, 164.90000012815, .3},
			"actor2AfterFrames":   {161.4750000089407, 161.47500003278256, 164.30000012815, .3},
			"actor0.5AfterFrames": {161.4750000089407, 161.47500003278256, 165.20000012814998, .3},
		},
	}
	files := map[string]string{"ops.json": opsJSONFor("SeatRider")}
	directory := t.TempDir()
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"seat scale frames","min_format":107,"max_format":107}}`}
	snapshot := "data modify storage seats:probe actorSaved set from entity SeatRider Pos\ndata modify storage seats:probe rootSaved set from entity @e[tag=SSCurrentRoot,limit=1] Pos\nexecute at SeatRider run summon marker ~ ~ ~ {Tags:[SSAtomic,SSAtomicActor]}\nexecute at @e[tag=SSCurrentRoot,limit=1] run summon marker ~ ~ ~ {Tags:[SSAtomic,SSAtomicRoot]}\n"
	for name, scale := range map[string]string{"half": "0.5", "one": "1", "double": "2"} {
		bodies["data/seats/function/actor_"+name+".mcfunction"] = "attribute SeatRider minecraft:scale base set " + scale + "\n" + snapshot + "say SS_ATOMIC\n"
		if name != "one" {
			bodies["data/seats/function/vehicle_"+name+".mcfunction"] = "attribute @e[tag=SSCurrentRoot,limit=1] minecraft:scale base set " + scale + "\n" + snapshot + "say SS_ATOMIC\n"
		}
	}
	bodies["data/seats/function/child_double.mcfunction"] = "attribute @e[tag=SSCurrentChild,limit=1] minecraft:scale base set 2\n" + snapshot + "data modify storage seats:probe childSaved set from entity @e[tag=SSCurrentChild,limit=1] Pos\nexecute at @e[tag=SSCurrentChild,limit=1] run summon marker ~ ~ ~ {Tags:[SSAtomic,SSAtomicChild]}\nsay SS_ATOMIC\n"
	for path, body := range bodies {
		rel := "datapacks/seats/" + path
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
	run := func(addr string) {
		c, pr, pw, actorID := joinPlayEid(t, addr, "SeatRider")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		pose := int32(-1)
		type observed struct {
			id   int32
			uuid [16]byte
		}
		spawns := map[string]observed{}
		links := map[int32][]int32{}
		fold := func(lines []string) []string {
			for _, line := range lines {
				prefix, raw, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, e := strconv.ParseInt(prefix, 0, 32)
				if e != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(raw))
				switch int32(id) {
				case server.IDPlaySpawnEntity:
					eid, e1 := r.VarInt()
					uuid, e2 := r.UUID()
					typ, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil {
						for _, name := range []string{"chicken", "horse", "zombie"} {
							if typ == mustEType(t, name) {
								spawns[name] = observed{eid, uuid}
							}
						}
					}
				case server.IDPlayEntityMetadata:
					eid, entries := fwDecodeMeta([]byte(raw))
					if eid == actorID {
						for _, entry := range entries {
							if entry.idx == 6 && entry.typ == 20 {
								pose = int32(entry.i)
							}
						}
					}
				case server.LatestVersion().IDs.PlaySetPassengers:
					eid, e1 := r.VarInt()
					n, e2 := r.VarInt()
					if e1 != nil || e2 != nil || n < 0 || n > 16 {
						continue
					}
					row := make([]int32, n)
					for i := range row {
						row[i], e = r.VarInt()
						if e != nil {
							t.Fatal(e)
						}
					}
					links[eid] = row
				}
			}
			return lines
		}
		serial := 0
		command := func(cmd string) []string {
			serial++
			tag := fmt.Sprintf("SSReceipt%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+tag)
			return fold(waitForExactSayBB(t, sink, tag, 30*time.Second))
		}
		collect := func(tag string) []string { return fold(waitForExactSayBB(t, sink, tag, 30*time.Second)) }
		parsePosition := func(lines []string) ([3]float64, bool) {
			for _, line := range lines {
				if !strings.Contains(line, "data.entity.query") && !strings.Contains(line, "data.storage.query") {
					continue
				}
				value, ok := parseSNBT(line)
				list, yes := value.([]any)
				if !ok || !yes || len(list) != 3 {
					continue
				}
				var out [3]float64
				for axis := range out {
					v, ok := hrNum(list[axis])
					if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
						t.Fatal("invalid seat position")
					}
					out[axis] = v
				}
				return out, true
			}
			return [3]float64{}, false
		}
		position := func(target string) [3]float64 {
			p, ok := parsePosition(command("data get entity " + target + " Pos"))
			if !ok {
				t.Fatalf("%s missing saved Pos %s", addr, target)
			}
			return p
		}
		liveIndex := 0
		live := func(target string) [3]float64 {
			liveIndex++
			tag := fmt.Sprintf("SSLive%d", liveIndex)
			command("execute at " + target + " run summon marker ~ ~ ~ {Tags:[" + tag + "]}")
			return position("@e[type=marker,tag=" + tag + ",limit=1]")
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
			t.Fatal("seat clock missing")
			return 0
		}
		step := func() {
			old := clock()
			command("tick step 1")
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				got := clock()
				if got > old+1 {
					t.Fatal("seat world overstepped")
				}
				if got == old+1 {
					time.Sleep(150 * time.Millisecond)
					if clock() != got {
						t.Fatal("seat clock not settled")
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("seat step missing")
		}
		frames := func(tag string) { waitPlayerTicksBB(t, pw, collect, tag, 2) }
		scale := func(target string, want float64) {
			command("execute store result score #scale SSScale run attribute " + target + " minecraft:scale get 1000")
			if !structureSaidBB(command(fmt.Sprintf("execute if score #scale SSScale matches %d run say SS_SCALE", int(want*1000))), "SS_SCALE") {
				t.Fatalf("%s missing effective scale%g for%s", addr, want, target)
			}
		}
		command("gamemode creative @s")
		command("tp @s 0.5 162 0.5 0 0")
		command("tick freeze")
		setupPlayerTickBarrierBB(t, pw)
		frames("SSReady")
		command("scoreboard objectives add SSScale dummy")
		for _, input := range []byte{0x20, 0} {
			if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{input}); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if input != 0 {
				want = 5
			}
			deadline := time.Now().Add(10 * time.Second)
			for attempt := 0; pose != want && time.Now().Before(deadline); attempt++ {
				frames(fmt.Sprintf("SSPose%d_%d", input, attempt))
			}
			if pose != want {
				t.Fatalf("%s actual pose%d want%d", addr, pose, want)
			}
		}
		identity := func(target string, entry observed) {
			u := entry.uuid
			nbt := fmt.Sprintf("UUID:[I;%d,%d,%d,%d]", int32(binary.BigEndian.Uint32(u[0:4])), int32(binary.BigEndian.Uint32(u[4:8])), int32(binary.BigEndian.Uint32(u[8:12])), int32(binary.BigEndian.Uint32(u[12:16])))
			if !structureSaidBB(command("execute if entity "+strings.TrimSuffix(target, "]")+",nbt={"+nbt+"}] run say SS_ID"), "SS_ID") {
				t.Fatal("seat observed/saved identity mismatch")
			}
		}
		lastVehicle := int32(0)
		for ci, tc := range []struct {
			name, vehicle string
			stack         bool
		}{{"chickenPlayer", "chicken", false}, {"horsePlayer", "horse", false}, {"chickenZombiePlayer", "chicken", true}} {
			command("ride SeatRider dismount")
			frames(fmt.Sprintf("SSUnlink%d", ci))
			if lastVehicle != 0 && len(links[lastVehicle]) != 0 {
				t.Fatalf("previous actual passenger unlink missing%v", links[lastVehicle])
			}
			command("kill @e[tag=SSBody]")
			command("tp @s 0.5 162 0.5 0 0")
			command("attribute SeatRider minecraft:scale base set 1")
			frames(fmt.Sprintf("SSReset%d", ci))
			scale("SeatRider", 1)
			command("tag @e[tag=SSCurrentRoot] remove SSCurrentRoot")
			command("tag @e[tag=SSCurrentChild] remove SSCurrentChild")
			command("fill 3 155 -5 14 176 5 air")
			tag := fmt.Sprintf("SSBase%d", ci)
			root := "@e[type=" + tc.vehicle + ",tag=" + tag + ",limit=1]"
			command("summon " + tc.vehicle + " 8.5 162 0.5 {Tags:[SSBody,SSCurrentRoot," + tag + "],NoAI:1b,NoGravity:1b,Rotation:[0f,0f]}")
			rootInfo := spawns[tc.vehicle]
			if rootInfo.id == 0 {
				t.Fatal("root spawn receipt missing")
			}
			identity(root, rootInfo)
			scale(root, 1)
			if !structureSaidBB(command("execute if entity "+strings.TrimSuffix(root, "]")+",nbt={NoAI:1b,NoGravity:1b,Rotation:[0f,0f]}] run say SS_BODY"), "SS_BODY") {
				t.Fatal("actual root flags/rotation missing")
			}
			child := ""
			childInfo := observed{}
			if tc.stack {
				ztag := fmt.Sprintf("SSChild%d", ci)
				child = "@e[type=zombie,tag=" + ztag + ",limit=1]"
				command("summon zombie 10.5 162 0.5 {Tags:[SSBody,SSCurrentChild," + ztag + "],NoAI:1b,NoGravity:1b,IsBaby:0b,Rotation:[0f,0f]}")
				childInfo = spawns["zombie"]
				identity(child, childInfo)
				scale(child, 1)
				command("ride " + child + " mount " + root)
			}
			step()
			immediate := root
			if tc.stack {
				immediate = child
			}
			command("ride SeatRider mount " + immediate)
			frames(fmt.Sprintf("SSMount%d", ci))
			lastVehicle = rootInfo.id
			if tc.stack {
				lastVehicle = childInfo.id
			}
			checkLinks := func() {
				want := actorID
				if tc.stack {
					want = childInfo.id
				}
				if row := links[rootInfo.id]; len(row) != 1 || row[0] != want {
					t.Fatalf("%s %s root ordered links%v want%d", addr, tc.name, row, want)
				}
				if tc.stack {
					if row := links[childInfo.id]; len(row) != 1 || row[0] != actorID {
						t.Fatalf("child ordered links%v", row)
					}
				}
				if pose != 0 {
					t.Fatalf("mounted actual pose%d", pose)
				}
			}
			verify := func(label string, rootSaved, rootLive, childSaved, childLive, actorSaved, actorLive [3]float64, hasChild bool) {
				phase := label
				switch label {
				case "vehicle0.5Atomic":
					phase = "default"
				case "vehicle2Atomic":
					phase = "vehicle0.5After"
				case "mobRider2Atomic":
					phase = "vehicle2After"
				case "actor2Atomic":
					phase = "vehicle2After"
					if tc.stack {
						phase = "mobRider2After"
					}
				case "actor0.5Atomic":
					phase = "actor2AfterFrames"
				}
				want, ok := expected[tc.name][phase]
				if !ok {
					t.Fatalf("unmeasured seat phase%s/%s", tc.name, phase)
				}
				actorSavedZ := .5
				if tc.stack {
					actorSavedZ = want.z
				}
				for _, row := range []struct {
					name      string
					got, want [3]float64
					active    bool
				}{
					{"rootSaved", rootSaved, [3]float64{8.5, want.rootY, .5}, true},
					{"rootLive", rootLive, [3]float64{8.5, want.rootY, .5}, true},
					{"childSaved", childSaved, [3]float64{8.5, want.childY, .5}, hasChild},
					{"childLive", childLive, [3]float64{8.5, want.childY, want.z}, hasChild},
					{"actorSaved", actorSaved, [3]float64{8.5, want.actorY, actorSavedZ}, true},
					{"actorLive", actorLive, [3]float64{8.5, want.actorY, want.z}, true},
				} {
					if !row.active {
						continue
					}
					for axis := range row.got {
						if math.Abs(row.got[axis]-row.want[axis]) > 1e-9 {
							t.Errorf("%s %s/%s %s axis%d got%.17g want%.17g", addr, tc.name, label, row.name, axis, row.got[axis], row.want[axis])
						}
					}
				}
			}
			sample := func(label string) {
				checkLinks()
				rootSaved, rootLive := position(root), live(root)
				actorSaved, actorLive := position("SeatRider"), live("SeatRider")
				var childSaved, childLive [3]float64
				if tc.stack {
					childSaved, childLive = position(child), live(child)
				}
				verify(label, rootSaved, rootLive, childSaved, childLive, actorSaved, actorLive, tc.stack)
				t.Logf("%s %s/%s world%d rootSaved%v rootLive%v childSaved%v childLive%v actorSaved%v actorLive%v links%v/%v", addr, tc.name, label, clock(), rootSaved, rootLive, childSaved, childLive, actorSaved, actorLive, links[rootInfo.id], links[childInfo.id])
			}
			atomic := func(fn, label string, childSnapshot bool) int64 {
				command("kill @e[type=marker,tag=SSAtomic]")
				beforeClock := clock()
				if !structureSaidBB(command("function seats:"+fn), "SS_ATOMIC") {
					t.Fatal("seat atomic function not executed")
				}
				getSaved := func(key string) [3]float64 {
					p, ok := parsePosition(command("data get storage seats:probe " + key))
					if !ok {
						t.Fatalf("atomic %s saved receipt missing", key)
					}
					return p
				}
				actorSaved, rootSaved := getSaved("actorSaved"), getSaved("rootSaved")
				actorLive := position("@e[type=marker,tag=SSAtomicActor,limit=1]")
				rootLive := position("@e[type=marker,tag=SSAtomicRoot,limit=1]")
				var childSaved, childLive [3]float64
				if childSnapshot {
					childSaved = getSaved("childSaved")
					childLive = position("@e[type=marker,tag=SSAtomicChild,limit=1]")
				}
				verify(label+"Atomic", rootSaved, rootLive, childSaved, childLive, actorSaved, actorLive, childSnapshot)
				t.Logf("%s %s/%sAtomic world%d rootSaved%v rootLive%v childSaved%v childLive%v actorSaved%v actorLive%v", addr, tc.name, label, beforeClock, rootSaved, rootLive, childSaved, childLive, actorSaved, actorLive)
				if clock() != beforeClock {
					t.Fatal("atomic scale advanced world age")
				}
				return beforeClock
			}
			sample("default")
			for _, entry := range []struct {
				name  string
				scale float64
			}{{"half", .5}, {"double", 2}} {
				atomic("vehicle_"+entry.name, fmt.Sprintf("vehicle%g", entry.scale), false)
				scale(root, entry.scale)
				step()
				frames(fmt.Sprintf("SSVehicle%d_%g", ci, entry.scale))
				sample(fmt.Sprintf("vehicle%gAfter", entry.scale))
			}
			if tc.stack {
				atomic("child_double", "mobRider2", true)
				scale(child, 2)
				step()
				frames(fmt.Sprintf("SSMob%d", ci))
				sample("mobRider2After")
			}
			for _, entry := range []struct {
				name  string
				scale float64
			}{{"double", 2}, {"half", .5}} {
				beforeClock := atomic("actor_"+entry.name, fmt.Sprintf("actor%g", entry.scale), false)
				frames(fmt.Sprintf("SSActor%d_%s", ci, entry.name))
				scale("SeatRider", entry.scale)
				if clock() != beforeClock {
					t.Fatal("player scale advanced world age")
				}
				sample(fmt.Sprintf("actor%gAfterFrames", entry.scale))
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
