package blackbox

import "testing"

func TestCopperChestHandPriorityParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{
		{name: "main_axe_copper_default", main: "diamond_axe", off: "dirt", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", mainStart: 1, offStart: 3, mainEnd: 1, offEnd: 3, menu: true, menuTitle: "literal:Cargo", menuItem: "diamond", menuCount: 7, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 copper_chest[facing=east]", toolChestChecksBB[0]}},
		{name: "main_wax_copper_default", main: "honeycomb", off: "dirt", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", mainStart: 5, offStart: 3, mainEnd: 5, offEnd: 3, menu: true, menuTitle: "literal:Cargo", menuItem: "diamond", menuCount: 7, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 copper_chest[facing=east]", toolChestChecksBB[0]}},
		{name: "main_empty_offaxe_copper_default", off: "diamond_axe", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", offStart: 1, offEnd: 1, menu: true, menuTitle: "literal:Cargo", menuItem: "diamond", menuCount: 7, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 copper_chest[facing=east]", toolChestChecksBB[0]}},
		{name: "main_shift_wax_copper", main: "honeycomb", off: "dirt", clicked: "copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", sneak: true, mainStart: 5, offStart: 3, mainEnd: 4, offEnd: 3, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 waxed_copper_chest[facing=east]", toolChestChecksBB[0]}},
		{name: "main_shift_scrape_copper", main: "diamond_axe", off: "dirt", clicked: "weathered_copper_chest[facing=east]" + toolChestContentsBB, wantBlock: "air", sneak: true, mainStart: 1, offStart: 3, mainEnd: 1, offEnd: 3, mainDamage: 1, before: toolChestChecksBB, checks: []string{"execute if block 0 159 0 exposed_copper_chest[facing=east]", toolChestChecksBB[0]}},
	})
}
