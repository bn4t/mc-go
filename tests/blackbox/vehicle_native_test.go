package blackbox

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Native saves cross both implementations. The original vanilla save supplies
// valid normalized subtype data, and the second vanilla boot reads Go's output.
func TestVehicleNativeParity(t *testing.T) {
	jar := blackboxJar(t)
	vdir := t.TempDir()
	vp, ok := freePort()
	if !ok {
		t.Fatal("port unavailable")
	}
	_, say, wait := vanillaConsole(t, jar, vp, map[string]string{"level-name": "world", "spawn-protection": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, vdir)
	say("tick freeze")
	say("forceload add 0 0")
	for _, command := range []string{
		`summon minecraft:oak_boat 2 -55 2 {NoGravity:1b,CustomName:"Boat",Tags:["vboat"],Passengers:[{id:"minecraft:pig",NoAI:1b,Tags:["rider"]}]}`,
		`summon minecraft:oak_chest_boat 4 -55 2 {NoGravity:1b,CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:26b,id:"minecraft:apple",count:2},{Slot:27b,id:"minecraft:stone",count:9}],Tags:["vchestboat"]}`,
		`summon minecraft:chest_minecart 6 -55 2 {NoGravity:1b,Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:26b,id:"minecraft:apple",count:2}],Tags:["vchestcart"]}`,
		`summon minecraft:hopper_minecart 8 -55 2 {NoGravity:1b,Enabled:0b,Items:[{Slot:4b,id:"minecraft:apple",count:2},{Slot:5b,id:"minecraft:stone",count:3}],Tags:["vhopper"]}`,
		`summon minecraft:furnace_minecart 10 -55 2 {NoGravity:1b,Fuel:123s,PushX:0.25d,PushZ:-0.5d,Tags:["vfurnace"]}`,
		`summon minecraft:tnt_minecart 12 -55 2 {NoGravity:1b,fuse:100000,Tags:["vtnt"]}`,
		`summon minecraft:minecart 14 -55 2 {NoGravity:1b,HasTicked:1b,FlippedRotation:1b,Tags:["vplain"]}`,
	} {
		say(command)
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("first vanilla exit%d", code)
	}
	worldDir := filepath.Join(vdir, "world")
	read := func(dir string) map[string]map[string]any {
		t.Helper()
		list, err := world.LoadEntityRegions(filepath.Join(dir, "dimensions", "minecraft", "overworld"))
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 7 {
			t.Fatalf("native roots=%d want7 (riders must be nested once)", len(list))
		}
		found := map[string]map[string]any{}
		for _, e := range list {
			tags, ok := e["Tags"].(nbt.List)
			if !ok || len(tags.Items) != 1 {
				t.Fatalf("root tags=%#v", e["Tags"])
			}
			found[tags.Items[0].(string)] = e
		}
		return found
	}
	before := read(worldDir)
	// Independently pin the input arena, including rejected out-of-range slots.
	for tag, want := range map[string]int{"vchestboat": 2, "vchestcart": 2, "vhopper": 1} {
		if n := len(before[tag]["Items"].(nbt.List).Items); n != want {
			t.Fatalf("vanilla %s items%d want%d", tag, n, want)
		}
	}
	if n := len(before["vboat"]["Passengers"].(nbt.List).Items); n != 1 {
		t.Fatalf("vanilla riders=%d", n)
	}
	mp, ok := freePort()
	if !ok {
		t.Fatal("port unavailable")
	}
	var goServer *server.Server
	addr := startMcGoCfg(t, mp, func(s *server.Server) {
		goServer = s
		s.WorldDir = worldDir
		s.SetFrozen(true)
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	probe := func(addr string) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "tick freeze")

		sendCommand(t, pw, "execute if entity @e[tag=vehicle_native_missing_witness] run say VN_FALSE_WITNESS")
		sendCommand(t, pw, "say VN_FALSE_WITNESS_END")
		if structureSaidBB(waitForExactSayBB(t, sink, "VN_FALSE_WITNESS_END", 10*time.Second), "VN_FALSE_WITNESS") {
			t.Fatal("false native predicate produced a success marker")
		}
		for i, condition := range []string{
			`if data entity @e[tag=vboat,limit=1] {NoGravity:1b,CustomName:"Boat",Passengers:[{id:"minecraft:pig",NoAI:1b,Tags:["rider"]}]}`,
			`if data entity @e[tag=vchestboat,limit=1] {CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:26b,id:"minecraft:apple",count:2}]}`,
			`if data entity @e[tag=vchestcart,limit=1] {Items:[{Slot:26b,id:"minecraft:apple",count:2}]}`,
			`if data entity @e[tag=vhopper,limit=1] {Enabled:0b,Items:[{Slot:4b,id:"minecraft:apple",count:2}]}`,
			`if entity @e[tag=vfurnace] if entity @e[tag=vtnt] if entity @e[tag=vplain]`,
		} {
			marker, end := fmt.Sprintf("VN_PASS%d", i), fmt.Sprintf("VN_DONE%d", i)
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(waitForExactSayBB(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s failed %s", addr, condition)
			}
		}
	}
	probe(addr)
	if err := goServer.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	after := read(worldDir)
	for tag, want := range before {
		got, ok := after[tag]
		if !ok {
			t.Fatalf("missing %s", tag)
		}
		for _, key := range []string{"id", "UUID", "Pos", "Rotation", "NoGravity", "CustomName", "Items", "Enabled", "Fuel", "PushX", "PushZ", "fuse", "HasTicked", "FlippedRotation"} {
			if !reflect.DeepEqual(got[key], want[key]) {
				t.Errorf("%s saved%s=%#v want%#v", tag, key, got[key], want[key])
			}
		}
		if tag == "vboat" {
			a := got["Passengers"].(nbt.List)
			b := want["Passengers"].(nbt.List)
			if len(a.Items) != 1 || len(b.Items) != 1 {
				t.Fatalf("saved rider count %d/%d", len(a.Items), len(b.Items))
			}
			am, bm := a.Items[0].(map[string]any), b.Items[0].(map[string]any)
			for _, key := range []string{"id", "UUID", "Pos", "Rotation", "NoAI", "Tags"} {
				if !reflect.DeepEqual(am[key], bm[key]) {
					t.Errorf("rider %s=%#v want%#v", key, am[key], bm[key])
				}
			}
		}

	}
	v2dir := t.TempDir()
	cpWorld(t, worldDir, filepath.Join(v2dir, "world"))
	vp2, ok := freePort()
	if !ok {
		t.Fatal("port unavailable")
	}
	addr2, say2, wait2 := vanillaConsole(t, jar, vp2, map[string]string{"level-name": "world", "spawn-protection": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, v2dir)
	say2("tick freeze")
	probe(addr2)
	say2("stop")
	if code := wait2(); code != 0 {
		t.Fatalf("second vanilla exit%d", code)
	}
}
