package server

import (
	"fmt"
	"math"
	"mc-go/internal/world"
	"strconv"
	"testing"
)

func TestSetblockItemComponentBridge(t *testing.T) {
	tag, _, err := parseSNBTTag(`{Items:[{Slot:26b,id:"minecraft:leather_helmet",count:1,components:{"minecraft:damage":2,"minecraft:repair_cost":5,"minecraft:custom_name":"minecraft:123"}}]}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	stack := snbtItems(tag)[26]
	id, _ := world.ItemID("leather_helmet")
	if stack.ID != id || stack.Count != 1 || stack.Damage != 2 || stack.RepairCost != 5 || stack.Name != "minecraft:123" {
		t.Fatalf("loaded stack %#v", stack)
	}
}

func TestSetblockTypedLongLootSeeds(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	for i, row := range []struct {
		token string
		value int64
	}{
		{"42L", 42}, {"-42l", -42}, {"9223372036854775807L", math.MaxInt64}, {"-9223372036854775808L", math.MinInt64}, {"42", 42},
	} {
		s.runSetblock("", []string{strconv.Itoa(i + 1), "-60", "0", fmt.Sprintf(`chest{LootTable:"minecraft:chests/simple_dungeon",LootTableSeed:%s}`, row.token)})
		chest := s.peekChest(s.dims[dimOverworld], world.Pos{X: i + 1, Y: -60, Z: 0})
		if chest == nil || chest.lootSeed != row.value {
			t.Fatalf("seed%s got%#v want%d", row.token, chest, row.value)
		}
	}
}
