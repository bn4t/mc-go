package blackbox

import "testing"

func TestToolEffectAudienceParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{
		{name: "strip", main: "dirt", off: "diamond_axe", clicked: "oak_log", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, sound: "item.axe.strip", checks: []string{"execute if block 0 159 0 stripped_oak_log"}},
		{name: "scrape", main: "dirt", off: "diamond_axe", clicked: "weathered_copper", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, sound: "item.axe.scrape", levelEvent: 3005, checks: []string{"execute if block 0 159 0 exposed_copper"}},
		{name: "unwax", main: "dirt", off: "diamond_axe", clicked: "waxed_copper_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, sound: "item.axe.wax_off", levelEvent: 3004, checks: []string{"execute if block 0 159 0 copper_block"}},
		{name: "flatten", main: "dirt", off: "diamond_shovel", clicked: "grass_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, sound: "item.shovel.flatten", checks: []string{"execute if block 0 159 0 dirt_path"}},
		{name: "till", main: "dirt", off: "diamond_hoe", clicked: "dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, sound: "item.hoe.till", checks: []string{"execute if block 0 159 0 farmland"}},
		{name: "wax", main: "dirt", off: "honeycomb", clicked: "copper_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, levelEvent: 3003, checks: []string{"execute if block 0 159 0 waxed_copper_block"}},
		{name: "dowse", main: "dirt", off: "diamond_shovel", clicked: "campfire[lit=true]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, levelEvent: 1009, eventActor: true, checks: []string{"execute if block 0 159 0 campfire[lit=false]"}},
	})
}
