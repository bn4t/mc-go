package server

import (
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func TestNBTPathEmptyRootMatcher(t *testing.T) {
	root := map[string]any{"kept": int32(1)}
	steps, ok := parseNBTPath("{}")
	if !ok || len(steps) != 0 {
		t.Fatal("empty root matcher was not recognized")
	}
	if !nbtModify(root, steps, "merge", 0, map[string]any{"added": int32(2)}) || root["kept"] != int32(1) || root["added"] != int32(2) {
		t.Fatalf("root merge=%#v", root)
	}
	// Nonempty matchers remain outside this narrow parser addition.
	other, ok := parseNBTPath("{kept:1}")
	if ok && len(other) == 0 {
		t.Fatal("nonempty matcher was relaxed into a root match")
	}
}

func TestNBTPathLookupRegression(t *testing.T) {
	root := map[string]any{"Items": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"components": map[string]any{"minecraft:custom_data": map[string]any{"a": int32(7)}}}}}, "plain": map[string]any{"a": int32(3)}}
	for path, want := range map[string]int32{`Items[0].components."minecraft:custom_data".a`: 7, "plain.a": 3} {
		steps, ok := parseNBTPath(path)
		if !ok {
			t.Fatalf("valid path %s rejected", path)
		}
		got, found := nbtGet(root, steps)
		if !found || got != want {
			t.Errorf("%s=%#v found=%v", path, got, found)
		}
	}
}

func TestDataBlockAccessorKeepsTargetIdentity(t *testing.T) {
	s, d := randSim(t)
	s.runSetblock("P", []string{"1", "0", "1", `command_block{CustomName:"Target"}`})
	s.runSetblock("P", []string{"3", "0", "1", `command_block{CustomName:"Donor",SuccessCount:999}`})
	s.runCommandAt("RCON", 4, "data", []string{"modify", "block", "1", "0", "1", "{}", "merge", "from", "block", "3", "0", "1"})
	target := s.commandBlockAt(d, world.Pos{X: 1, Y: 0, Z: 1})
	donor := s.commandBlockAt(d, world.Pos{X: 3, Y: 0, Z: 1})
	if target.fields["CustomName"] != "Donor" || target.fields["SuccessCount"] != int32(999) || donor.fields["CustomName"] != "Donor" || donor.fields["SuccessCount"] != int32(999) {
		t.Fatal("root copy redirected the target or damaged the donor")
	}
	s.runCommandAt("RCON", 4, "data", []string{"merge", "block", "1", "0", "1", `{id:"minecraft:beehive",x:30,y:0,z:1,CustomName:"Canonical"}`})
	fields, ok := s.blockEntitySaveMap(d, world.Pos{X: 1, Y: 0, Z: 1})
	if !ok || fields["id"] != "minecraft:command_block" || fields["x"] != int32(1) || fields["CustomName"] != "Canonical" || d.genericBE[world.Pos{X: 30, Y: 0, Z: 1}] != nil {
		t.Fatalf("supplied identity escaped the accessor: %#v", fields)
	}
}
