package blackbox

import "testing"

func TestGrowthEndHandParity(t *testing.T) {
	checkHandUseCases(t, []handUseCase{
		{name: "off_bonemeal", main: "dirt", off: "bone_meal", clicked: "wheat[age=0]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, pre: []string{"setblock 0 158 0 farmland[moisture=7]"}, checks: []string{"execute if block 0 159 0 wheat", "execute unless block 0 159 0 wheat[age=0]"}},
		{name: "off_bonemeal_mature", main: "dirt", off: "bone_meal", clicked: "wheat[age=7]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, pre: []string{"setblock 0 158 0 farmland[moisture=7]"}, checks: []string{"execute if block 0 159 0 wheat[age=7]"}},
		{name: "off_bonemeal_creative", main: "dirt", off: "bone_meal", clicked: "wheat[age=0]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, pre: []string{"setblock 0 158 0 farmland[moisture=7]"}, checks: []string{"execute if block 0 159 0 wheat", "execute unless block 0 159 0 wheat[age=0]"}},
		{name: "off_eye", main: "dirt", off: "ender_eye", clicked: "end_portal_frame[eye=false]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if block 0 159 0 end_portal_frame[eye=true]"}},
		{name: "off_eye_filled", main: "dirt", off: "ender_eye", clicked: "end_portal_frame[eye=true]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if block 0 159 0 end_portal_frame[eye=true]"}},
		{name: "off_eye_creative", main: "dirt", off: "ender_eye", clicked: "end_portal_frame[eye=false]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if block 0 159 0 end_portal_frame[eye=true]"}},
		{name: "off_crystal", main: "dirt", off: "end_crystal", clicked: "obsidian", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if entity @e[type=end_crystal,distance=..8]"}},
		{name: "off_crystal_creative", main: "dirt", off: "end_crystal", clicked: "bedrock", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute if entity @e[type=end_crystal,distance=..8]"}},
		{name: "off_crystal_wrong_base", main: "dirt", off: "end_crystal", clicked: "stone", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, checks: []string{"execute unless entity @e[type=end_crystal,distance=..8]"}},
	})
}
