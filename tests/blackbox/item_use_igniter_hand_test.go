package blackbox

import "testing"

func TestIgniterHandParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{
		{name: "main_charge_creative", main: "fire_charge", off: "dirt", clicked: "stone", wantBlock: "fire", creative: true, mainStart: 5, offStart: 3, mainEnd: 5, offEnd: 3},
		{name: "off_flint_campfire", main: "dirt", off: "flint_and_steel", clicked: "campfire[lit=false]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 campfire[lit=true]"}},
		{name: "main_flint", main: "flint_and_steel", off: "dirt", clicked: "stone", wantBlock: "fire", mainStart: 1, offStart: 3, mainEnd: 1, offEnd: 3, mainDamage: 1},
		{name: "off_flint", main: "dirt", off: "flint_and_steel", clicked: "stone", wantBlock: "fire", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1},
		{name: "off_flint_creative", main: "dirt", off: "flint_and_steel", clicked: "stone", wantBlock: "fire", hand: 1, creative: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1},
		{name: "off_charge", main: "dirt", off: "fire_charge", clicked: "stone", wantBlock: "fire", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "off_charge_creative", main: "dirt", off: "fire_charge", clicked: "stone", wantBlock: "fire", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5},
		{name: "off_tnt_flint", main: "dirt", off: "flint_and_steel", clicked: "tnt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 air", "execute if entity @e[type=tnt,distance=..8]"}},
		{name: "off_tnt_charge", main: "dirt", off: "fire_charge", clicked: "tnt", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if block 0 159 0 air", "execute if entity @e[type=tnt,distance=..8]"}},
		{name: "off_tnt_charge_creative", main: "dirt", off: "fire_charge", clicked: "tnt", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if block 0 159 0 air", "execute if entity @e[type=tnt,distance=..8]"}},
		{name: "off_tnt_flint_sneak", main: "dirt", off: "flint_and_steel", clicked: "tnt", wantBlock: "fire", hand: 1, sneak: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 tnt", "execute unless entity @e[type=tnt,distance=..8]"}},
	})
}
