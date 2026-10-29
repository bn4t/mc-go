package blackbox

import (
	"fmt"
	"strings"
	"testing"
)

func TestJukeboxModeledComponentsParity(t *testing.T) {
	specs := []struct{ name, patch, item string }{
		{"stored_enchantments", `"minecraft:stored_enchantments":{"minecraft:mending":1}`, ""},
		{"map_id", `"minecraft:map_id":4`, ""},
		{"map_post", `"minecraft:map_post_processing":"scale"`, ""},
		{"banner_patterns", `"minecraft:banner_patterns":[{pattern:"minecraft:cross",color:"red"}]`, ""},
		{"base_color", `"minecraft:base_color":"blue"`, ""},
		{"firework_explosion", `"minecraft:firework_explosion":{shape:"star",colors:[I;16711680],fade_colors:[I;65280],has_trail:1b,has_twinkle:1b}`, ""},
		{"unsigned_fireworks", `"minecraft:fireworks":{flight_duration:255}`, ""},
		{"fireworks", `"minecraft:fireworks":{flight_duration:2,explosions:[{shape:"burst",colors:[I;255]}]}`, ""},
		{"dye", `"minecraft:dye":"orange"`, ""},
		{"dyed_color", `"minecraft:dyed_color":1234567`, ""},
		{"stew", `"minecraft:suspicious_stew_effects":[{id:"minecraft:speed",duration:123}]`, ""},
		{"bottle_amplifier", `"minecraft:ominous_bottle_amplifier":2`, ""},
		{"pot_decorations", `"minecraft:pot_decorations":["minecraft:brick","minecraft:angler_pottery_sherd","minecraft:archer_pottery_sherd","minecraft:brick"]`, ""},
		{"instrument", `"minecraft:instrument":"minecraft:ponder_goat_horn"`, ""},
		{"removed_instrument", `"!minecraft:instrument":{}`, "goat_horn"},
		{"trim", `"minecraft:trim":{material:"minecraft:gold",pattern:"minecraft:sentry"}`, ""},
		{"block_state", `"minecraft:block_state":{honey_level:"4"}`, ""},
		{"bees", `"minecraft:bees":[{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:600}]`, ""},
		{"potion_color", `"minecraft:potion_contents":{custom_color:1234567}`, ""},
		{"potion", `"minecraft:potion_contents":{potion:"minecraft:swiftness"}`, ""},
		{"potion_scale", `"minecraft:potion_duration_scale":1.5f`, ""},
		{"writable_book", `"minecraft:writable_book_content":{pages:["Page"]}`, ""},
		{"written_book", `"minecraft:written_book_content":{title:"Title",author:"A",pages:["Page"],generation:1,resolved:1b}`, ""},
		{"container_loot", `"minecraft:container_loot":{loot_table:"minecraft:chests/simple_dungeon",seed:3L}`, ""},
		{"container_nested_repair", `"minecraft:container":[{slot:0,item:{id:"minecraft:diamond",count:2,components:{"minecraft:repair_cost":3}}}]`, ""},
		{"bundle_nested_repair", `"minecraft:bundle_contents":[{id:"minecraft:diamond",count:2,components:{"minecraft:repair_cost":3}}]`, ""},
		{"damage", `"minecraft:damage":3`, ""},
		{"ominous_banner", `"minecraft:banner_patterns":[{pattern:"minecraft:rhombus",color:"cyan"},{pattern:"minecraft:stripe_bottom",color:"light_gray"},{pattern:"minecraft:stripe_center",color:"gray"},{pattern:"minecraft:border",color:"light_gray"},{pattern:"minecraft:stripe_middle",color:"black"},{pattern:"minecraft:half_horizontal",color:"light_gray"},{pattern:"minecraft:circle",color:"light_gray"},{pattern:"minecraft:border",color:"black"}],"minecraft:tooltip_display":{hidden_components:["minecraft:banner_patterns"]},"minecraft:item_name":{translate:"block.minecraft.ominous_banner"},"minecraft:rarity":"uncommon"`, "white_banner"},
	}
	cases := make([]handUseCase, 0, len(specs))
	for _, spec := range specs {
		item := spec.item
		if item == "" {
			item = "music_disc_cat"
		}
		record := fmt.Sprintf(`{RecordItem:{id:"minecraft:%s",count:1,components:{%s}}}`, item, spec.patch)
		expected := spec.patch
		if spec.name == "firework_explosion" || spec.name == "fireworks" {
			expected = strings.ReplaceAll(expected, "[I;", "[")
		}
		if spec.name == "unsigned_fireworks" {
			expected = `"minecraft:fireworks":{flight_duration:-1b}`
		}
		if spec.name == "fireworks" {
			expected = strings.ReplaceAll(expected, "flight_duration:2", "flight_duration:2b")
		}
		if spec.name == "writable_book" || spec.name == "written_book" {
			expected = strings.ReplaceAll(expected, `pages:["Page"]`, `pages:[{raw:"Page"}]`)
		}
		if spec.name == "written_book" {
			expected = strings.ReplaceAll(expected, `title:"Title"`, `title:{raw:"Title"}`)
		}
		condition := "execute if data block 0 159 0 " + fmt.Sprintf(`{RecordItem:{id:"minecraft:%s",count:1,components:{%s}}}`, item, expected)
		if spec.name == "map_post" {
			condition = `execute if data block 0 159 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1}} unless data block 0 159 0 RecordItem.components."minecraft:map_post_processing"`
		}
		cases = append(cases, handUseCase{name: spec.name, main: "dirt", mainStart: 3, mainEnd: 3, hand: 1, clicked: "jukebox[has_record=true]" + record, wantBlock: "air", before: []string{condition}, checks: []string{condition}})
	}
	checkHandUseCases(t, cases)
}
