package blackbox

import "testing"

func TestToolBlockEntityPreservationParity(t *testing.T) {
	statue := `{components:{"minecraft:custom_name":"Statue"}}`
	statueCheck := `execute if data block 0 159 0 {components:{"minecraft:custom_name":"Statue"}}`
	checkHandUseCases(t, []handUseCase{
		{name: "wax_chest_cargo", main: "dirt", off: "honeycomb", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 waxed_copper_chest[facing=east]", toolChestChecksBB[0], "execute unless entity @e[type=item,distance=..8]"}},
		{name: "unwax_chest_cargo", main: "dirt", off: "diamond_axe", clicked: "waxed_weathered_copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 weathered_copper_chest[facing=east]", toolChestChecksBB[0], "execute unless entity @e[type=item,distance=..8]"}},
		{name: "scrape_chest_cargo", main: "dirt", off: "diamond_axe", clicked: "weathered_copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 exposed_copper_chest[facing=east]", toolChestChecksBB[0], "execute unless entity @e[type=item,distance=..8]"}},
		{name: "wax_statue_name", main: "dirt", off: "honeycomb", clicked: "copper_golem_statue[facing=east]" + statue, wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, before: []string{statueCheck}, checks: []string{"execute if block 0 159 0 waxed_copper_golem_statue[facing=east]", statueCheck}},
		{name: "unwax_statue_name", main: "dirt", off: "diamond_axe", clicked: "waxed_weathered_copper_golem_statue[facing=east]" + statue, wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, before: []string{statueCheck}, checks: []string{"execute if block 0 159 0 weathered_copper_golem_statue[facing=east]", statueCheck}},
		{name: "scrape_statue_name", main: "dirt", off: "diamond_axe", clicked: "weathered_copper_golem_statue[facing=east]" + statue, wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, before: []string{statueCheck}, checks: []string{"execute if block 0 159 0 exposed_copper_golem_statue[facing=east]", statueCheck}},
	})
}
