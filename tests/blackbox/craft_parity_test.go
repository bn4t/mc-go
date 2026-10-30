package blackbox

// Crafting parity: the 2x2 player grid and the 3x3 crafting-table menu
// run the same recipe scripts on both servers, diffing the result slot,
// grid consumption, remainders, and the cursor after every step. Menu
// 12 geometry: 0 result, 1-9 grid, 10-36 main, 37-45 hotbar; window 0
// carries result 0, grid 1-4, main 9-35, hotbar 36-44.

import (
	"mc-go/internal/server"
	"mc-go/internal/world"
	"testing"
)

func TestCraftRecipeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CraftV1", "CraftM1", "CraftV2", "CraftM2",
			"CraftV3", "CraftM3", "CraftV4", "CraftM4",
			"CraftV5", "CraftM5", "CraftV6", "CraftM6",
			"CraftV7", "CraftM7", "CraftV8", "CraftM8",
			"CraftV9", "CraftM9", "CraftV10", "CraftM10",
			"CraftV11", "CraftM11", "CraftV12", "CraftM12",
			"CraftV13", "CraftM13", "CraftV14", "CraftM14",
			"CraftV15", "CraftM15", "CraftV16", "CraftM16",
			"CraftV17", "CraftM17", "CraftV18", "CraftM18",
			"CraftV19", "CraftM19", "CraftV20", "CraftM20",
			"CraftV21", "CraftM21", "CraftV22", "CraftM22",
			"CraftV23", "CraftM23", "CraftV24", "CraftM24",
			"CraftV25", "CraftM25", "CraftV26", "CraftM26",
			"CraftV27", "CraftM27", "CraftV28", "CraftM28",
			"CraftV29", "CraftM29", "CraftV30", "CraftM30",
			"CraftV31", "CraftM31", "CraftV32", "CraftM32",
			"CraftV33", "CraftM33", "CraftV34", "CraftM34",
			"CraftV35", "CraftM35", "CraftV36", "CraftM36",
			"CraftV37", "CraftM37", "CraftV38", "CraftM38",
			"CraftV39", "CraftM39", "CraftV40", "CraftM40",
			"CraftV41", "CraftM41", "CraftV42", "CraftM42",
			"CraftV43", "CraftM43", "CraftV44", "CraftM44",
			"CraftV45", "CraftM45", "CraftV46", "CraftM46",
			"CraftV47", "CraftM47", "CraftV48", "CraftM48",
			"CraftV49", "CraftM49", "CraftV50", "CraftM50",
			"CraftV51", "CraftM51", "CraftV52", "CraftM52"),
	}, nil)
	defer tg.cleanup()

	type step struct {
		tag    string
		slot   int16
		button byte
		mode   int32
		w0     bool
	}
	cases := []struct {
		name  string
		suf   string
		block string
		menu  int32
		gives [][2]string
		steps []step
	}{
		{
			// Shaped 3x3: diamond_pickaxe = "###" diamonds, " S ",
			// " S " sticks. Right-place one per slot, verify the
			// result, take it, verify the grid spent one each.
			name: "pickaxe", suf: "1", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:diamond 64"},
				{"hotbar.1", "minecraft:stick 64"},
			},
			steps: []step{
				{tag: "lift-dia", slot: 37},
				{tag: "dia1", slot: 1, button: 1},
				{tag: "dia2", slot: 2, button: 1},
				{tag: "dia3", slot: 3, button: 1},
				{tag: "dia-back", slot: 37},
				{tag: "lift-stick", slot: 38},
				{tag: "st5", slot: 5, button: 1},
				{tag: "st8", slot: 8, button: 1},
				{tag: "st-back", slot: 38},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Stair L: six planks produce four stairs.
			name: "stairs", suf: "2", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_planks 64"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "p1", slot: 1, button: 1},
				{tag: "p4", slot: 4, button: 1},
				{tag: "p5", slot: 5, button: 1},
				{tag: "p7", slot: 7, button: 1},
				{tag: "p8", slot: 8, button: 1},
				{tag: "p9", slot: 9, button: 1},
				{tag: "back", slot: 37},
				{tag: "take", slot: 0},
			},
		},
		{
			// Mirrored stair L: whether the pack carries a second
			// mirrored recipe or the mirror simply fails, the two
			// servers must answer the same.
			name: "stairs-mirror", suf: "3", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_planks 64"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "p3", slot: 3, button: 1},
				{tag: "p5", slot: 5, button: 1},
				{tag: "p6", slot: 6, button: 1},
				{tag: "p7", slot: 7, button: 1},
				{tag: "p8", slot: 8, button: 1},
				{tag: "p9", slot: 9, button: 1},
				{tag: "back", slot: 37},
			},
		},
		{
			// A lone log anywhere in the 3x3 bbox-normalizes to its
			// planks recipe; bottom-right corner pins the slide.
			name: "log-corner", suf: "4", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_log 16"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "log9", slot: 9, button: 1},
				{tag: "back", slot: 37},
				{tag: "take", slot: 0},
			},
		},
		{
			// Shapeless: diorite + cobblestone -> 2 andesite, in any
			// arrangement.
			name: "andesite", suf: "5", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:diorite 16"},
				{"hotbar.1", "minecraft:cobblestone 16"},
			},
			steps: []step{
				{tag: "lift-d", slot: 37},
				{tag: "d5", slot: 5, button: 1},
				{tag: "back-d", slot: 37},
				{tag: "lift-c", slot: 38},
				{tag: "c9", slot: 9, button: 1},
				{tag: "back-c", slot: 38},
				{tag: "take", slot: 0},
			},
		},
		{
			// Cake: three milk buckets over sugar/egg/sugar over
			// wheat. Whether the emptied buckets stay in the grid is
			// what this pins (the remainder source in 26.2 has no
			// dedicated component - measured here).
			name: "cake", suf: "6", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:milk_bucket 3"},
				{"hotbar.1", "minecraft:sugar 2"},
				{"hotbar.2", "minecraft:egg 1"},
				{"hotbar.3", "minecraft:wheat 3"},
			},
			steps: []step{
				{tag: "lift-milk", slot: 37},
				{tag: "m1", slot: 1, button: 1},
				{tag: "m2", slot: 2, button: 1},
				{tag: "m3", slot: 3, button: 1},
				{tag: "lift-sugar", slot: 38},
				{tag: "s4", slot: 4, button: 1},
				{tag: "s6", slot: 6, button: 1},
				{tag: "lift-egg", slot: 39},
				{tag: "e5", slot: 5, button: 1},
				{tag: "lift-wheat", slot: 40},
				{tag: "w7", slot: 7, button: 1},
				{tag: "w8", slot: 8, button: 1},
				{tag: "w9", slot: 9, button: 1},
				{tag: "take", slot: 0},
			},
		},
		{
			// Dispenser with a damaged bow: ingredient tests match
			// the item id, so damage is irrelevant. Grid:
			// cobble ring 1,2,3,4,6,7,9; bow at 5; redstone at 8.
			name: "dispenser", suf: "7", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:cobblestone 64"},
				{"hotbar.1", "minecraft:bow[minecraft:damage=100]"},
				{"hotbar.2", "minecraft:redstone 8"},
			},
			steps: []step{
				{tag: "lift-c", slot: 37},
				{tag: "c1", slot: 1, button: 1},
				{tag: "c2", slot: 2, button: 1},
				{tag: "c3", slot: 3, button: 1},
				{tag: "c4", slot: 4, button: 1},
				{tag: "c6", slot: 6, button: 1},
				{tag: "c7", slot: 7, button: 1},
				{tag: "c9", slot: 9, button: 1},
				{tag: "back-c", slot: 37},
				{tag: "lift-bow", slot: 38},
				{tag: "b5", slot: 5},
				{tag: "lift-red", slot: 39},
				{tag: "r8", slot: 8, button: 1},
				{tag: "back-red", slot: 39},
				{tag: "take", slot: 0},
			},
		},
		{
			// Window-0 2x2: a log in the player grid crafts planks,
			// and the take consumes through the window-0 path.
			name: "p2x2", suf: "8", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_log 16"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "log", slot: 1, button: 1},
				{tag: "back", slot: 37},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Special repair: two damaged pickaxes merge with the
			// 5%-of-max bonus; the result's damage component is the
			// whole comparison.
			name: "repair", suf: "9", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:stone_pickaxe[minecraft:damage=80]"},
				{"hotbar.1", "minecraft:stone_pickaxe[minecraft:damage=100]"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Repair only pairs same-id damageables: a pickaxe plus
			// a shovel must not produce a result.
			name: "repair-mismatch", suf: "10", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:stone_pickaxe[minecraft:damage=80]"},
				{"hotbar.1", "minecraft:stone_shovel[minecraft:damage=80]"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// The repair result is a FRESH stack: curses merge at max
			// level, every other component (sharpness, name) is dropped.
			name: "repair-curse", suf: "29", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:diamond_sword[minecraft:damage=100,minecraft:enchantments={"minecraft:binding_curse":1,"minecraft:sharpness":3}]`},
				{"hotbar.1", `minecraft:diamond_sword[minecraft:damage=200,minecraft:enchantments={"minecraft:vanishing_curse":1}]`},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// canCombine requires count 1 on both stacks: a 2-stack of
			// damaged pickaxes plus a single one refuses.
			name: "repair-stack", suf: "30", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:stone_pickaxe[minecraft:damage=80] 2"},
				{"hotbar.1", "minecraft:stone_pickaxe[minecraft:damage=80]"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Transmute: a bundle plus a dye recolors in place; the
			// input stack's components ride onto the result.
			name: "transmute-bundle", suf: "11", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:bundle"},
				{"hotbar.1", "minecraft:red_dye 4"},
			},
			steps: []step{
				{tag: "lift-bundle", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-dye", slot: 38},
				{tag: "d2", slot: 2, button: 1},
				{tag: "back-dye", slot: 38},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Special shielddecoration: banner + shield copies the
			// banner's layers and dye color onto the shield (the
			// shield's own patch survives — measured assemble).
			name: "shield-patterned", suf: "12", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:blue_banner[minecraft:banner_patterns=[{color:"red",pattern:"minecraft:creeper"}]]`},
				{"hotbar.1", "minecraft:shield"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-s", slot: 38},
				{tag: "s2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// A plain banner leaves only base_color on the shield
			// (no banner_patterns component).
			name: "shield-plain", suf: "13", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:lime_banner"},
				{"hotbar.1", "minecraft:shield"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-s", slot: 38},
				{tag: "s2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// A shield already carrying layers can't decorate
			// again — the result slot must stay empty.
			name: "shield-redo", suf: "14", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:white_banner"},
				{"hotbar.1", `minecraft:shield[minecraft:base_color="blue"]`},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-s", slot: 38},
				{tag: "s2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Two banners or two shields never produce a result:
			// the recipe needs exactly one of each.
			name: "shield-dupes", suf: "15", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:white_banner"},
				{"hotbar.1", "minecraft:black_banner"},
				{"hotbar.2", "minecraft:shield"},
				{"hotbar.3", "minecraft:shield"},
			},
			steps: []step{
				{tag: "lift-b1", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-b2", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
				{tag: "lift-b1back", slot: 1},
				{tag: "b1-park", slot: 37},
				{tag: "lift-s1", slot: 40},
				{tag: "s1", slot: 3},
				{tag: "lift-s2", slot: 39},
				{tag: "s2", slot: 4},
				{tag: "take2?", slot: 0},
			},
		},
		{
			// A banner plus a non-shield (or the other way round)
			// matches nothing.
			name: "shield-mismatch", suf: "16", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:white_banner"},
				{"hotbar.1", "minecraft:cobblestone 8"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-c", slot: 38},
				{tag: "c2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// The shield's own components ride along:
			// createWithOriginalComponents keeps a renamed shield's
			// custom_name — plus a damaged shield keeps its damage.
			name: "shield-named", suf: "17", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:pink_banner[minecraft:banner_patterns=[{color:"black",pattern:"minecraft:creeper"},{color:"white",pattern:"minecraft:skull"}]]`},
				{"hotbar.1", `minecraft:shield[minecraft:custom_name="Bulwark",minecraft:damage=30]`},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-s", slot: 38},
				{tag: "s2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Special bookcloning: written_book + writable_book copies
			// the source a generation up — and the source itself is
			// left in the grid (its count-1 remainder copy).
			name: "book-clone", suf: "18", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:written_book[minecraft:written_book_content={author:"Ben",title:"Diary",generation:0,pages:["first","second"]}]`},
				{"hotbar.1", "minecraft:writable_book"},
			},
			steps: []step{
				{tag: "lift-src", slot: 37},
				{tag: "src1", slot: 1},
				{tag: "lift-w", slot: 38},
				{tag: "w2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Two writable books plus the source in the 3x3 table:
			// one result per material stack — count 2 — and the
			// source survives the take.
			name: "book-clone-multi", suf: "19", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:written_book[minecraft:written_book_content={author:"Q",title:"Notes",generation:1,pages:["pg"]}]`},
				{"hotbar.1", "minecraft:writable_book"},
				{"hotbar.2", "minecraft:writable_book"},
			},
			steps: []step{
				{tag: "lift-src", slot: 37},
				{tag: "src1", slot: 1},
				{tag: "lift-w1", slot: 38},
				{tag: "w2", slot: 2},
				{tag: "lift-w2", slot: 39},
				{tag: "w3", slot: 3},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// generation 2 ("copy of a copy") is outside the
			// serializer's default allowed range — no result.
			name: "book-clone-tattered", suf: "20", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:written_book[minecraft:written_book_content={author:"Ben",title:"Old",generation:2,pages:["pg"]}]`},
				{"hotbar.1", "minecraft:writable_book"},
			},
			steps: []step{
				{tag: "lift-src", slot: 37},
				{tag: "src1", slot: 1},
				{tag: "lift-w", slot: 38},
				{tag: "w2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// A written_book without written_book_content isn't a
			// source — it falls to the material check and fails.
			name: "book-clone-bare", suf: "21", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:written_book"},
				{"hotbar.1", "minecraft:writable_book"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-w", slot: 38},
				{tag: "w2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Anything else in the grid fails the whole recipe:
			// source + writable + dirt produces nothing.
			name: "book-clone-extra", suf: "22", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:written_book[minecraft:written_book_content={author:"Ben",title:"Diary",pages:["x"]}]`},
				{"hotbar.1", "minecraft:writable_book"},
				{"hotbar.2", "minecraft:dirt"},
			},
			steps: []step{
				{tag: "lift-src", slot: 37},
				{tag: "src1", slot: 1},
				{tag: "lift-w", slot: 38},
				{tag: "w2", slot: 2},
				{tag: "lift-d", slot: 39},
				{tag: "d3", slot: 3},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Special bannerduplicate: a patterned red banner plus a
			// plain red banner yields a count-1 copy of the patterned
			// one (createWithOriginalComponents keeps the layers).
			name: "banner-dup", suf: "23", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"}]]`},
				{"hotbar.1", "minecraft:red_banner"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// The result count is the template's 1 — a 2-stack of the
			// patterned banner still gives a single copy.
			name: "banner-dup-multi", suf: "24", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"}]] 2`},
				{"hotbar.1", "minecraft:red_banner"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Both banners must be the same DyeColor — a patterned red
			// plus a plain blue never matches.
			name: "banner-dup-color", suf: "25", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"}]]`},
				{"hotbar.1", "minecraft:blue_banner"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Exactly one patterned and one plain: two patterned (or
			// two plain) banners of the same color refuse.
			name: "banner-dup-same", suf: "26", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"}]]`},
				{"hotbar.1", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:flower",color:"white"}]]`},
				{"hotbar.2", "minecraft:red_banner"},
				{"hotbar.3", "minecraft:red_banner"},
			},
			steps: []step{
				{tag: "lift-p1", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-p2", slot: 38},
				{tag: "p2", slot: 2},
				{tag: "take?a", slot: 0},
				{tag: "lift-b1", slot: 39},
				{tag: "b1", slot: 1},
				{tag: "lift-b2", slot: 40},
				{tag: "b2", slot: 2},
				{tag: "take?b", slot: 0},
			},
		},
		{
			// A banner over six layers can't be the patterned input —
			// the per-stack layers()>6 check rejects it.
			name: "banner-dup-7layer", suf: "27", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:creeper",color:"red"}]]`},
				{"hotbar.1", "minecraft:red_banner"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Exactly six layers is still inside the gate — the copy
			// is produced.
			name: "banner-dup-6layer", suf: "28", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:red_banner[minecraft:banner_patterns=[{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:flower",color:"white"},{pattern:"minecraft:skull",color:"black"},{pattern:"minecraft:mojang",color:"yellow"},{pattern:"minecraft:globe",color:"blue"},{pattern:"minecraft:piglin",color:"pink"}]]`},
				{"hotbar.1", "minecraft:red_banner"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Special firework_star: gunpowder + dyes yields a count-1
			// star carrying firework_explosion — small_ball shape and
			// the dyes' firework colors in grid order.
			name: "firework-star", suf: "31", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:gunpowder"},
				{"hotbar.1", "minecraft:red_dye"},
				{"hotbar.2", "minecraft:blue_dye"},
			},
			steps: []step{
				{tag: "lift-g", slot: 37},
				{tag: "g1", slot: 1},
				{tag: "lift-r", slot: 38},
				{tag: "r2", slot: 2},
				{tag: "lift-b", slot: 39},
				{tag: "b3", slot: 3},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// One each of trail (diamond), twinkle (glowstone_dust),
			// and a shape item (gold_nugget→star): the flags set and
			// the shape overrides small_ball. Needs the 3x3 table.
			name: "firework-star-flags", suf: "32", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:gunpowder"},
				{"hotbar.1", "minecraft:yellow_dye"},
				{"hotbar.2", "minecraft:diamond"},
				{"hotbar.3", "minecraft:glowstone_dust"},
				{"hotbar.4", "minecraft:gold_nugget"},
			},
			steps: []step{
				{tag: "lift-g", slot: 37},
				{tag: "g1", slot: 1},
				{tag: "lift-y", slot: 38},
				{tag: "y2", slot: 2},
				{tag: "lift-d", slot: 39},
				{tag: "d3", slot: 3},
				{tag: "lift-gs", slot: 40},
				{tag: "gs4", slot: 4},
				{tag: "lift-n", slot: 41},
				{tag: "n5", slot: 5},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// No fuel: dyes alone refuse the star recipe.
			name: "firework-star-nofuel", suf: "33", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:red_dye"},
				{"hotbar.1", "minecraft:blue_dye"},
			},
			steps: []step{
				{tag: "lift-r", slot: 37},
				{tag: "r1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// Special firework_star_fade: a star plus dyes copies the
			// star (createWithOriginalComponents) and REPLACES
			// fade_colors — shape and colors carry over.
			name: "firework-star-fade", suf: "34", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:firework_star[minecraft:firework_explosion={shape:"star",colors:[I;11546150],fade_colors:[I;],has_trail:0b,has_twinkle:1b}]`},
				{"hotbar.1", "minecraft:green_dye"},
				{"hotbar.2", "minecraft:yellow_dye"},
			},
			steps: []step{
				{tag: "lift-s", slot: 37},
				{tag: "s1", slot: 1},
				{tag: "lift-g", slot: 38},
				{tag: "g2", slot: 2},
				{tag: "lift-y", slot: 39},
				{tag: "y3", slot: 3},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Special firework_rocket: paper + two gunpowder + a star
			// yields a count-3 rocket whose fireworks component is
			// {flight_duration:2, explosions:[the star's explosion]}.
			name: "firework-rocket", suf: "35", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:paper"},
				{"hotbar.1", "minecraft:gunpowder 2"},
				{"hotbar.2", `minecraft:firework_star[minecraft:firework_explosion={shape:"creeper",colors:[I;2437522],fade_colors:[I;],has_trail:1b,has_twinkle:0b}]`},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-g", slot: 38},
				{tag: "g2", slot: 2, button: 1},
				{tag: "g3", slot: 3, button: 1},
				{tag: "g-back", slot: 38},
				{tag: "lift-s", slot: 39},
				{tag: "s4", slot: 4},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Paper + gunpowder alone: the special rocket recipe also
			// matches (stars optional) — vanilla prefers it over the
			// shapeless firework_rocket_simple, so the rocket carries
			// an empty-explosions fireworks component.
			name: "firework-rocket-plain", suf: "36", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:paper"},
				{"hotbar.1", "minecraft:gunpowder 2"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-g", slot: 38},
				{tag: "g2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Paper plus two gunpowder stacks and no star: too many
			// stacks for the shapeless firework_rocket_simple, so only
			// the special can match — discriminates whether the
			// special really matches starless grids.
			name: "firework-rocket-2fuel", suf: "38", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:paper"},
				{"hotbar.1", "minecraft:gunpowder 2"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p1", slot: 1},
				{tag: "lift-g", slot: 38},
				{tag: "g2", slot: 2, button: 1},
				{tag: "g3", slot: 3},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// A bare star (no explosion component) fades onto the
			// recipe's DEFAULT explosion: small_ball, empty colors.
			name: "firework-star-fade-bare", suf: "37", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:firework_star"},
				{"hotbar.1", "minecraft:cyan_dye"},
			},
			steps: []step{
				{tag: "lift-s", slot: 37},
				{tag: "s1", slot: 1},
				{tag: "lift-c", slot: 38},
				{tag: "c2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// crafting_dye (DyeRecipe): one dyeable + one dye mixes the
			// dye's textureDiffuseColor alone — an undyed target has no
			// dyed_color component, so the mix starts from the dye only.
			name: "dye-boots", suf: "39", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:leather_boots"},
				{"hotbar.1", "minecraft:red_dye"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-d", slot: 38},
				{tag: "d2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Two dyes average their textureDiffuseColor channels and
			// rescale by the max-channel ratio (applyDyes).
			name: "dye-mix", suf: "40", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:leather_helmet"},
				{"hotbar.1", "minecraft:red_dye"},
				{"hotbar.2", "minecraft:blue_dye"},
			},
			steps: []step{
				{tag: "lift-h", slot: 37},
				{tag: "h1", slot: 1},
				{tag: "lift-r", slot: 38},
				{tag: "r2", slot: 2},
				{tag: "lift-b", slot: 39},
				{tag: "b3", slot: 3},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Explicit RGB zero contributes black as an existing color.
			name: "dye-zero", suf: "52", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", `minecraft:leather_helmet[minecraft:dyed_color=0]`}, {"hotbar.1", "minecraft:red_dye"}},
			steps: []step{{tag: "lift-h", slot: 37}, {tag: "h1", slot: 1}, {tag: "lift-r", slot: 38}, {tag: "r2", slot: 2}, {tag: "take", slot: 0}, {tag: "park", slot: 10}},
		},
		{
			// A pre-dyed stack contributes its existing dyed_color to
			// the mix as one more entry (applyDyes counts it too).
			name: "dye-redye", suf: "41", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:leather_boots[minecraft:dyed_color=255]"},
				{"hotbar.1", "minecraft:white_dye"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-d", slot: 38},
				{tag: "d2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// wolf_armor dyes through the same recipe type.
			name: "dye-wolf", suf: "42", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:wolf_armor"},
				{"hotbar.1", "minecraft:green_dye"},
			},
			steps: []step{
				{tag: "lift-w", slot: 37},
				{tag: "w1", slot: 1},
				{tag: "lift-g", slot: 38},
				{tag: "g2", slot: 2},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Two stacks matching the target ingredient refuse (the
			// recipe wants exactly one).
			name: "dye-twotargets", suf: "43", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:leather_boots"},
				{"hotbar.1", "minecraft:leather_helmet"},
				{"hotbar.2", "minecraft:red_dye"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "lift-d", slot: 39},
				{tag: "d3", slot: 3},
				{tag: "take?", slot: 0},
			},
		},
		{
			// No dye slot: two dyeable pieces alone never match.
			name: "dye-targetonly", suf: "44", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:leather_boots"},
				{"hotbar.1", "minecraft:leather_helmet"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 38},
				{tag: "b2", slot: 2},
				{tag: "take?", slot: 0},
			},
		},
		{
			// crafting_imbue: a lingering potion ringed by eight arrows
			// produces eight tipped arrows carrying its potion_contents.
			name: "imbue-tipped", suf: "45", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:lingering_potion[minecraft:potion_contents="minecraft:harming"]`},
				{"hotbar.1", "minecraft:arrow"},
				{"hotbar.2", "minecraft:arrow"},
				{"hotbar.3", "minecraft:arrow"},
				{"hotbar.4", "minecraft:arrow"},
				{"hotbar.5", "minecraft:arrow"},
				{"hotbar.6", "minecraft:arrow"},
				{"hotbar.7", "minecraft:arrow"},
				{"hotbar.8", "minecraft:arrow"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p5", slot: 5},
				{tag: "lift-a", slot: 38},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 39},
				{tag: "b2", slot: 2},
				{tag: "lift-c", slot: 40},
				{tag: "c3", slot: 3},
				{tag: "lift-d", slot: 41},
				{tag: "d4", slot: 4},
				{tag: "lift-e", slot: 42},
				{tag: "e6", slot: 6},
				{tag: "lift-f", slot: 43},
				{tag: "f7", slot: 7},
				{tag: "lift-g", slot: 44},
				{tag: "g8", slot: 8},
				{tag: "lift-h", slot: 45},
				{tag: "h9", slot: 9},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// One empty ring slot refuses: all nine cells must be filled.
			name: "imbue-notfull", suf: "46", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:lingering_potion[minecraft:potion_contents="minecraft:harming"]`},
				{"hotbar.1", "minecraft:arrow"},
				{"hotbar.2", "minecraft:arrow"},
				{"hotbar.3", "minecraft:arrow"},
				{"hotbar.4", "minecraft:arrow"},
				{"hotbar.5", "minecraft:arrow"},
				{"hotbar.6", "minecraft:arrow"},
				{"hotbar.7", "minecraft:arrow"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p5", slot: 5},
				{tag: "lift-a", slot: 38},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 39},
				{tag: "b2", slot: 2},
				{tag: "lift-c", slot: 40},
				{tag: "c3", slot: 3},
				{tag: "lift-d", slot: 41},
				{tag: "d4", slot: 4},
				{tag: "lift-e", slot: 42},
				{tag: "e6", slot: 6},
				{tag: "lift-f", slot: 43},
				{tag: "f7", slot: 7},
				{tag: "lift-g", slot: 44},
				{tag: "g8", slot: 8},
				{tag: "take?", slot: 0},
			},
		},
		{
			// A splash potion in the center is not the source — a
			// splash potion ringed by arrows never matches.
			name: "imbue-splash", suf: "47", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", `minecraft:splash_potion[minecraft:potion_contents="minecraft:harming"]`},
				{"hotbar.1", "minecraft:arrow"},
				{"hotbar.2", "minecraft:arrow"},
				{"hotbar.3", "minecraft:arrow"},
				{"hotbar.4", "minecraft:arrow"},
				{"hotbar.5", "minecraft:arrow"},
				{"hotbar.6", "minecraft:arrow"},
				{"hotbar.7", "minecraft:arrow"},
				{"hotbar.8", "minecraft:arrow"},
			},
			steps: []step{
				{tag: "lift-p", slot: 37},
				{tag: "p5", slot: 5},
				{tag: "lift-a", slot: 38},
				{tag: "a1", slot: 1},
				{tag: "lift-b", slot: 39},
				{tag: "b2", slot: 2},
				{tag: "lift-c", slot: 40},
				{tag: "c3", slot: 3},
				{tag: "lift-d", slot: 41},
				{tag: "d4", slot: 4},
				{tag: "lift-e", slot: 42},
				{tag: "e6", slot: 6},
				{tag: "lift-f", slot: 43},
				{tag: "f7", slot: 7},
				{tag: "lift-g", slot: 44},
				{tag: "g8", slot: 8},
				{tag: "lift-h", slot: 45},
				{tag: "h9", slot: 9},
				{tag: "take?", slot: 0},
			},
		},
		{
			// A stew flower on the right recipe: the result carries
			// suspicious_stew_effects decoded from result.components.
			name: "stew-dandelion", suf: "48", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:bowl"},
				{"hotbar.1", "minecraft:brown_mushroom"},
				{"hotbar.2", "minecraft:red_mushroom"},
				{"hotbar.3", "minecraft:dandelion"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-m", slot: 38},
				{tag: "m2", slot: 2},
				{tag: "lift-r", slot: 39},
				{tag: "r3", slot: 3},
				{tag: "lift-d", slot: 40},
				{tag: "d4", slot: 4},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// A different flower -> a different stew effect id.
			name: "stew-wither", suf: "49", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:bowl"},
				{"hotbar.1", "minecraft:brown_mushroom"},
				{"hotbar.2", "minecraft:red_mushroom"},
				{"hotbar.3", "minecraft:wither_rose"},
			},
			steps: []step{
				{tag: "lift-b", slot: 37},
				{tag: "b1", slot: 1},
				{tag: "lift-m", slot: 38},
				{tag: "m2", slot: 2},
				{tag: "lift-r", slot: 39},
				{tag: "r3", slot: 3},
				{tag: "lift-w", slot: 40},
				{tag: "w4", slot: 4},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// crafting_decorated_pot: the four edge middles (back,
			// left, right, front) become pot_decorations on the pot.
			name: "pot-deco", suf: "50", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:flow_pottery_sherd"},
				{"hotbar.1", "minecraft:brick"},
				{"hotbar.2", "minecraft:brick"},
				{"hotbar.3", "minecraft:brick"},
			},
			steps: []step{
				{tag: "lift-s", slot: 37},
				{tag: "s2", slot: 2},
				{tag: "lift-a", slot: 38},
				{tag: "a4", slot: 4},
				{tag: "lift-b", slot: 39},
				{tag: "b6", slot: 6},
				{tag: "lift-c", slot: 40},
				{tag: "c8", slot: 8},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Four sherds: every side carries its own decoration id.
			name: "pot-sherds", suf: "51", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:angler_pottery_sherd"},
				{"hotbar.1", "minecraft:blade_pottery_sherd"},
				{"hotbar.2", "minecraft:mourner_pottery_sherd"},
				{"hotbar.3", "minecraft:snort_pottery_sherd"},
			},
			steps: []step{
				{tag: "lift-a", slot: 37},
				{tag: "a2", slot: 2},
				{tag: "lift-b", slot: 38},
				{tag: "b4", slot: 4},
				{tag: "lift-m", slot: 39},
				{tag: "m6", slot: 6},
				{tag: "lift-s", slot: 40},
				{tag: "s8", slot: 8},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(addr, name string) []clickSnap {
				d := newClickDriverOn(t, addr, name, c.block, c.menu, c.gives, nil)
				snaps := make([]clickSnap, 0, len(c.steps))
				for _, s := range c.steps {
					d.click(s.tag, s.slot, s.button, s.mode, s.w0, false)
					cp := clickSnap{carried: d.snap.carried, menu: map[int32]string{}, p0: map[int32]string{}}
					for k, v := range d.snap.menu {
						cp.menu[k] = v
					}
					for k, v := range d.snap.p0 {
						cp.p0[k] = v
					}
					snaps = append(snaps, cp)
				}
				return snaps
			}
			want := run(tg.vanilla, "CraftV"+c.suf)
			got := run(tg.mcgo, "CraftM"+c.suf)
			if c.name == "dye-zero" {
				expected := stackKey(server.ItemStack{ID: world.ItemIDs["leather_helmet"], Count: 1, DyedColor: 5773075})
				for name, snaps := range map[string][]clickSnap{"native": want, "go": got} {
					if value := snaps[len(snaps)-1].menu[10]; value != expected {
						t.Errorf("%s zero+dye result%s want%s", name, value, expected)
					}
				}
			}
			for i, s := range c.steps {
				if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
					for _, df := range diffs {
						t.Errorf("%s step %s: %s", c.name, s.tag, df)
					}
				}
			}
		})
	}
}
