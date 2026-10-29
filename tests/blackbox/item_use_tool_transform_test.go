package blackbox

import "testing"

func TestToolTransformHandParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{
		{name: "off_axe_adventure", gameMode: "adventure", main: "dirt", off: "diamond_axe", clicked: "oak_log", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 oak_log"}},
		{name: "off_shovel_adventure", gameMode: "adventure", main: "dirt", off: "diamond_shovel", clicked: "grass_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 grass_block"}},
		{name: "off_hoe_adventure", gameMode: "adventure", main: "dirt", off: "diamond_hoe", clicked: "dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 dirt"}},
		{name: "off_axe_spectator", gameMode: "spectator", main: "dirt", off: "diamond_axe", clicked: "oak_log", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 oak_log"}},

		{name: "off_wax_copper", main: "dirt", off: "honeycomb", clicked: "exposed_cut_copper_stairs[facing=east,half=top]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if block 0 159 0 waxed_exposed_cut_copper_stairs[facing=east,half=top]"}},
		{name: "off_wax_creative", main: "dirt", off: "honeycomb", clicked: "copper_block", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if block 0 159 0 waxed_copper_block"}},
		{name: "off_wax_already", main: "dirt", off: "honeycomb", clicked: "waxed_copper_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if block 0 159 0 waxed_copper_block"}},
		{name: "off_hoe_roots_no_drops", main: "dirt", off: "diamond_hoe", clicked: "rooted_dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, setup: []string{"gamerule minecraft:block_drops false"}, checks: []string{"execute if block 0 159 0 dirt", `execute unless entity @e[type=item,distance=..8,nbt={Item:{id:"minecraft:hanging_roots"}}]`}},

		{name: "off_axe_strip_axis", main: "dirt", off: "diamond_axe", clicked: "oak_log[axis=x]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 stripped_oak_log[axis=x]"}},
		{name: "main_axe_shield_intent", main: "diamond_axe", off: "shield", clicked: "oak_log", wantBlock: "air", mainStart: 1, offStart: 1, mainEnd: 1, offEnd: 1, checks: []string{"execute if block 0 159 0 oak_log"}},
		{name: "main_axe_shift_shield", main: "diamond_axe", off: "shield", clicked: "oak_log", wantBlock: "air", sneak: true, mainStart: 1, offStart: 1, mainEnd: 1, offEnd: 1, mainDamage: 1, checks: []string{"execute if block 0 159 0 stripped_oak_log"}},
		{name: "off_axe_strip_wood", main: "dirt", off: "iron_axe", clicked: "warped_hyphae[axis=z]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 stripped_warped_hyphae[axis=z]"}},
		{name: "off_axe_scrape", main: "dirt", off: "diamond_axe", clicked: "exposed_copper", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 copper_block"}},
		{name: "off_axe_scrape_state", main: "dirt", off: "diamond_axe", clicked: "weathered_cut_copper_stairs[facing=east,half=top]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 exposed_cut_copper_stairs[facing=east,half=top]"}},
		{name: "off_axe_wax_before_scrape", main: "dirt", off: "diamond_axe", clicked: "waxed_oxidized_copper", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 oxidized_copper"}},
		{name: "off_axe_invalid", main: "dirt", off: "diamond_axe", clicked: "stone", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 stone"}},
		{name: "off_axe_creative", main: "dirt", off: "diamond_axe", clicked: "bamboo_block[axis=x]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 stripped_bamboo_block[axis=x]"}},
		{name: "off_shovel_path", main: "dirt", off: "diamond_shovel", clicked: "grass_block", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 dirt_path"}},
		{name: "off_shovel_rooted", main: "dirt", off: "diamond_shovel", clicked: "rooted_dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 dirt_path"}},
		{name: "off_shovel_blocked", main: "dirt", off: "diamond_shovel", clicked: "grass_block", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, setup: []string{"setblock 0 160 0 stone"}, checks: []string{"execute if block 0 159 0 grass_block"}},
		{name: "off_shovel_down", main: "dirt", off: "diamond_shovel", clicked: "grass_block", wantBlock: "air", hand: 1, down: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 grass_block"}},
		{name: "off_shovel_dowse", main: "dirt", off: "diamond_shovel", clicked: "campfire[lit=true]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 campfire[lit=false]"}},
		{name: "off_hoe_farmland", main: "dirt", off: "diamond_hoe", clicked: "dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 farmland[moisture=0]"}},
		{name: "off_hoe_coarse", main: "dirt", off: "diamond_hoe", clicked: "coarse_dirt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 dirt"}},
		{name: "off_hoe_blocked", main: "dirt", off: "diamond_hoe", clicked: "dirt", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, setup: []string{"setblock 0 160 0 stone"}, checks: []string{"execute if block 0 159 0 dirt"}},
		{name: "off_hoe_down", main: "dirt", off: "diamond_hoe", clicked: "dirt", wantBlock: "air", hand: 1, down: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 dirt"}},
		{name: "off_hoe_roots_blocked_down", main: "dirt", off: "diamond_hoe", clicked: "rooted_dirt", wantBlock: "stone", hand: 1, down: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, setup: []string{"setblock 0 160 0 stone"}, checks: []string{"execute if block 0 159 0 dirt", `execute if entity @e[type=item,distance=..8,nbt={Item:{id:"minecraft:hanging_roots",count:1}}]`}},
		{name: "off_hoe_creative", main: "dirt", off: "diamond_hoe", clicked: "grass_block", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 farmland[moisture=0]"}},
	})
}
