package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestToolBlockEntityLiveIdentity(t *testing.T) {
	s := nativeSaveTestServer(t)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", `copper_chest{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`})
	original := s.peekChest(d, pos)
	if original == nil || original.slots[0].Count != 7 {
		t.Fatal("missing chest setup")
	}
	original.openers = 5
	s.setToolTransformBlock(d, pos, world.BlockStateMust("waxed_copper_chest"))
	if live := s.peekChest(d, pos); live != original || live.openers != 5 || live.slots[0].Count != 7 || d.beNames[pos] != "Cargo" {
		t.Fatal("transform replaced or altered live chest storage")
	}
	s.runSetblock("P", []string{"1", "0", "1", "exposed_copper_chest"})
	if live := s.peekChest(d, pos); live == nil || live == original || !live.slots[0].Empty() || d.beNames[pos] != "" {
		t.Fatal("command replacement inherited tool preservation")
	}
	s.runSetblock("P", []string{"1", "0", "1", `copper_golem_statue{components:{"minecraft:custom_name":"Statue"}}`})
	statue := d.genericBE[pos]
	if statue == nil {
		t.Fatal("missing statue setup")
	}
	s.setToolTransformBlock(d, pos, world.BlockStateMust("waxed_copper_golem_statue"))
	if d.genericBE[pos] != statue {
		t.Fatal("transform replaced live generic entity")
	}
	fields, _ := s.blockEntitySaveMap(d, pos)
	components, _ := fields["components"].(map[string]any)
	if components["minecraft:custom_name"] != "Statue" {
		t.Fatalf("statue fields=%#v", fields)
	}
	s.runSetblock("P", []string{"1", "0", "1", "exposed_copper_golem_statue"})
	if d.genericBE[pos] == statue {
		t.Fatal("command replacement retained statue object")
	}
	fields, _ = s.blockEntitySaveMap(d, pos)
	components, _ = fields["components"].(map[string]any)
	if components["minecraft:custom_name"] != nil {
		t.Fatal("command replacement retained statue name")
	}
}
