package blackbox

import "testing"

func TestBlockApplicatorHandParity(t *testing.T) {
	text := `data merge block 0 159 0 {front_text:{messages:["hi","","",""],color:"black",has_glowing_text:0b},is_waxed:0b}`
	glow := `data merge block 0 159 0 {front_text:{messages:["hi","","",""],color:"black",has_glowing_text:1b},is_waxed:0b}`
	loaded := `data merge block 0 159 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1}}`
	checkHandUseCases(t, []handUseCase{
		{name: "off_sign_same_dye", main: "dirt", off: "red_dye", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, setup: []string{`data merge block 0 159 0 {front_text:{messages:["hi","","",""],color:"red"}}`}, checks: []string{`execute if data block 0 159 0 {front_text:{color:"red"}}`}},
		{name: "off_sign_already_glow", main: "dirt", off: "glow_ink_sac", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, setup: []string{glow}, checks: []string{`execute if data block 0 159 0 {front_text:{has_glowing_text:1b}}`}},
		{name: "off_sign_waxed", main: "dirt", off: "red_dye", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, setup: []string{text, `data merge block 0 159 0 {is_waxed:1b}`}, checks: []string{`execute if data block 0 159 0 {front_text:{color:"black"},is_waxed:1b}`}},

		{name: "off_sign_dye", main: "dirt", off: "red_dye", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, setup: []string{text}, checks: []string{`execute if data block 0 159 0 {front_text:{color:"red"}}`}},
		{name: "off_sign_glow", main: "dirt", off: "glow_ink_sac", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, setup: []string{text}, checks: []string{`execute if data block 0 159 0 {front_text:{has_glowing_text:1b}}`}},
		{name: "off_sign_ink", main: "dirt", off: "ink_sac", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, setup: []string{glow}, checks: []string{`execute if data block 0 159 0 {front_text:{has_glowing_text:0b}}`}},
		{name: "off_sign_wax", main: "dirt", off: "honeycomb", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, setup: []string{text}, checks: []string{`execute if data block 0 159 0 {is_waxed:1b}`}},
		{name: "off_sign_dye_creative", main: "dirt", off: "red_dye", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, setup: []string{text}, checks: []string{`execute if data block 0 159 0 {front_text:{color:"red"}}`}},
		{name: "off_sign_empty_dye", main: "dirt", off: "red_dye", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5},
		{name: "main_sign_empty_dye", main: "red_dye", off: "dirt", clicked: "oak_wall_sign[facing=north]", wantBlock: "air", mainStart: 5, offStart: 3, mainEnd: 5, offEnd: 3, editor: true},
		{name: "off_hive_shears", main: "dirt", off: "shears", clicked: "beehive[honey_level=5]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offDamage: 1, checks: []string{"execute if block 0 159 0 beehive[honey_level=0]"}},
		{name: "off_hive_shears_notfull", main: "dirt", off: "shears", clicked: "beehive[honey_level=4]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 beehive[honey_level=4]"}},
		{name: "off_hive_shears_creative", main: "dirt", off: "shears", clicked: "beehive[honey_level=5]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 beehive[honey_level=0]"}},
		{name: "off_hive_bottle_last", main: "dirt", off: "glass_bottle", clicked: "beehive[honey_level=5]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, offResult: "honey_bottle", checks: []string{"execute if block 0 159 0 beehive[honey_level=0]"}},
		{name: "off_hive_bottles", main: "dirt", off: "glass_bottle", clicked: "beehive[honey_level=5]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if block 0 159 0 beehive[honey_level=0]"}},
		{name: "off_hive_bottles_creative", main: "dirt", off: "glass_bottle", clicked: "beehive[honey_level=5]", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, checks: []string{"execute if block 0 159 0 beehive[honey_level=0]"}},
		{name: "off_disc_insert", main: "dirt", off: "music_disc_cat", clicked: "jukebox", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 0, checks: []string{"execute if block 0 159 0 jukebox[has_record=true]", `execute if data block 0 159 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1}}`}},
		{name: "off_disc_creative", main: "dirt", off: "music_disc_cat", clicked: "jukebox", wantBlock: "air", hand: 1, creative: true, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, checks: []string{"execute if block 0 159 0 jukebox[has_record=true]", `execute if data block 0 159 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1}}`}},
		{name: "off_disc_loaded", main: "dirt", off: "music_disc_cat", clicked: "jukebox[has_record=true]", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, setup: []string{loaded}, checks: []string{"execute if block 0 159 0 jukebox[has_record=true]", `execute if data block 0 159 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1}}`}},
		{name: "main_loaded_eject", main: "dirt", off: "music_disc_cat", clicked: "jukebox[has_record=true]", wantBlock: "air", mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, setup: []string{loaded}, checks: []string{"execute if block 0 159 0 jukebox[has_record=false]"}},
	})
}
