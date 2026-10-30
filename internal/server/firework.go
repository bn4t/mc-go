package server

import (
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// fireworkDyeColors is DyeColor.getFireworkColor for each of the 16
// dyes, in loomDyeNames order (measured values).
var fireworkDyeColors = [16]int32{
	15790320, // white
	15435844, // orange
	12801229, // magenta
	6719955,  // light_blue
	14602026, // yellow
	4312372,  // lime
	14188952, // pink
	4408131,  // gray
	11250603, // light_gray
	2651799,  // cyan
	8073150,  // purple
	2437522,  // blue
	5320730,  // brown
	3887386,  // green
	11743532, // red
	1973019,  // black
}

// fireworkShapeNames is the FireworkExplosion.Shape wire order:
// SMALL_BALL, LARGE_BALL, STAR, CREEPER, BURST.
var fireworkShapeNames = [5]string{"small_ball", "large_ball", "star", "creeper", "burst"}

// fireworkShapeOrd maps the shape name to its VarInt ordinal.
func fireworkShapeOrd(name string) int32 {
	for i, s := range fireworkShapeNames {
		if s == name {
			return int32(i)
		}
	}
	return 0
}

// fireworkShapeOf is FireworkStarRecipe.findShape's shapes map:
// feather→burst, fire_charge→large_ball, gold_nugget→star, any
// #minecraft:skulls item→creeper. Anything else is not a shape item.
func fireworkShapeOf(item string) string {
	switch item {
	case "feather":
		return "burst"
	case "fire_charge":
		return "large_ball"
	case "gold_nugget":
		return "star"
	}
	if data.HasItemTag("skulls", item) {
		return "creeper"
	}
	return ""
}

// fireworkDyeFireColor gives a <color>_dye item's firework color.
func fireworkDyeFireColor(item string) (int32, bool) {
	for i, d := range loomDyeNames {
		if item == d+"_dye" {
			return fireworkDyeColors[i], true
		}
	}
	return 0, false
}

// fireworkExpCanon builds the canonical explosion string
// "shape|c1,c2|f1,f2|trail|twinkle" — colors and fades are int lists.
func fireworkExpCanon(shape string, colors, fades []int32, trail, twinkle bool) string {
	join := func(v []int32) string {
		parts := make([]string, len(v))
		for i, c := range v {
			parts[i] = strconv.Itoa(int(c))
		}
		return strings.Join(parts, ",")
	}
	b01 := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	return shape + "|" + join(colors) + "|" + join(fades) + "|" + b01(trail) + "|" + b01(twinkle)
}

// fireworkExpParse reads the canonical form back into fields.
func fireworkExpParse(exp string) (shape string, colors, fades []int32, trail, twinkle bool) {
	parts := strings.Split(exp, "|")
	if len(parts) != 5 {
		return "small_ball", nil, nil, false, false
	}
	parse := func(s string) []int32 {
		if s == "" {
			return nil
		}
		var out []int32
		for _, c := range strings.Split(s, ",") {
			v, err := strconv.Atoi(c)
			if err != nil {
				continue
			}
			out = append(out, int32(v))
		}
		return out
	}
	return parts[0], parse(parts[1]), parse(parts[2]), parts[3] == "1", parts[4] == "1"
}

// fireworkDyeName classifies a dye ingredient: the item must pass
// #minecraft:dyes — equivalent to the <color>_dye name set.
func fireworkDyeName(item string) bool {
	return data.HasItemTag("dyes", item)
}

// parseFireworkExplosionSNBT decodes an `item replace` explosion
// compound {shape:"star", colors:[I;..], fade_colors:[I;..],
// has_trail:0b, has_twinkle:1b}; missing keys take the codec defaults
// (small_ball, empty lists, false flags).
func parseFireworkExplosionSNBT(vtag *snbtTag) (string, bool) {
	if vtag == nil || vtag.kind != snbtCompound {
		return "", false
	}
	shape := "small_ball"
	if _, present := vtag.vals["shape"]; !present {
		return "", false
	}
	if sv, ok := vtag.vals["shape"]; ok {
		if sv.kind != snbtString {
			return "", false
		}
		shape = strings.TrimPrefix(sv.str, "minecraft:")
		valid := false
		for _, s := range fireworkShapeNames {
			if s == shape {
				valid = true
				break
			}
		}
		if !valid {
			return "", false
		}
	}
	list := func(key string) ([]int32, bool) {
		lv, ok := vtag.vals[key]
		if !ok {
			return nil, true
		}
		if lv.kind != snbtList {
			return nil, false
		}
		var out []int32
		for _, e := range lv.items {
			if e.kind != snbtNumber {
				return nil, false
			}
			n, err := strconv.Atoi(strings.TrimRight(e.num, "bBsSiIlLfFdD"))
			if err != nil {
				return nil, false
			}
			out = append(out, int32(n))
		}
		return out, true
	}
	flag := func(key string) (bool, bool) {
		fv, ok := vtag.vals[key]
		if !ok {
			return false, true
		}
		if fv.kind != snbtNumber {
			return false, false
		}
		n, err := strconv.Atoi(strings.TrimRight(fv.num, "bBsSiIlLfFdD"))
		if err != nil || (n != 0 && n != 1) {
			return false, false
		}
		return n == 1, true
	}
	colors, ok := list("colors")
	if !ok {
		return "", false
	}
	fades, ok := list("fade_colors")
	if !ok {
		return "", false
	}
	trail, ok := flag("has_trail")
	if !ok {
		return "", false
	}
	twinkle, ok := flag("has_twinkle")
	if !ok {
		return "", false
	}
	return fireworkExpCanon(shape, colors, fades, trail, twinkle), true
}

// matchFireworkStar handles crafting_special_firework_star
// (measured FireworkStarRecipe): ingredientCount≥2; per item —
// twinkle (glowstone_dust) once, trail (diamond) once, fuel
// (gunpowder) once, any number of dyes, one shape item; anything
// else fails. Needs exactly fuel AND at least one dye. assemble
// stamps FIREWORK_EXPLOSION on the star template: the found shape
// or SMALL_BALL, the dyes' firework colors in grid order, empty
// fades, has_trail/has_twinkle flags.
func matchFireworkStar(g craftGrid, filled []bool) (ItemStack, bool) {
	count := 0
	for _, f := range filled {
		if f {
			count++
		}
	}
	if count < 2 {
		return ItemStack{}, false
	}
	var (
		fuel, hasDye, shapeSet, trail, twinkle bool
		shape                                  = "small_ball"
		colors                                 []int32
	)
	for i, f := range filled {
		if !f {
			continue
		}
		st := g[i]
		name, ok := itemNameOf(st.ID)
		if !ok {
			return ItemStack{}, false
		}
		switch {
		case name == "glowstone_dust":
			if twinkle {
				return ItemStack{}, false
			}
			twinkle = true
		case name == "diamond":
			if trail {
				return ItemStack{}, false
			}
			trail = true
		case name == "gunpowder":
			if fuel {
				return ItemStack{}, false
			}
			fuel = true
		case fireworkDyeName(name):
			c, ok := fireworkDyeFireColor(name)
			if !ok {
				return ItemStack{}, false
			}
			colors = append(colors, c)
			hasDye = true
		default:
			s := fireworkShapeOf(name)
			if s == "" || shapeSet {
				return ItemStack{}, false
			}
			shapeSet = true
			shape = s
		}
	}
	if !fuel || !hasDye {
		return ItemStack{}, false
	}
	sid, ok := world.ItemID("firework_star")
	if !ok {
		return ItemStack{}, false
	}
	out := ItemStack{ID: sid, Count: 1}
	out.FireworkExp = fireworkExpCanon(shape, colors, nil, trail, twinkle)
	return out, true
}

// matchFireworkStarFade handles crafting_special_firework_star_fade
// (measured FireworkStarFadeRecipe): ingredientCount≥2; ≥1 dyes and
// exactly one firework_star target; anything else fails. assemble
// copies the target (createWithOriginalComponents) then sets
// FIREWORK_EXPLOSION = existing-or-DEFAULT.withFadeColors(dyes) —
// the fade list is REPLACED, shape/colors/trail/twinkle carry over.
func matchFireworkStarFade(g craftGrid, filled []bool) (ItemStack, bool) {
	count := 0
	for _, f := range filled {
		if f {
			count++
		}
	}
	if count < 2 {
		return ItemStack{}, false
	}
	var (
		target ItemStack
		hasDye bool
		fades  []int32
	)
	for i, f := range filled {
		if !f {
			continue
		}
		st := g[i]
		name, ok := itemNameOf(st.ID)
		if !ok {
			return ItemStack{}, false
		}
		switch {
		case fireworkDyeName(name):
			c, ok := fireworkDyeFireColor(name)
			if !ok {
				return ItemStack{}, false
			}
			fades = append(fades, c)
			hasDye = true
		case name == "firework_star":
			if !target.Empty() {
				return ItemStack{}, false
			}
			target = st
		default:
			return ItemStack{}, false
		}
	}
	if target.Empty() || !hasDye {
		return ItemStack{}, false
	}
	out := target
	out.Count = 1
	shape, colors, _, trail, twinkle := fireworkExpParse(target.FireworkExp)
	out.FireworkExp = fireworkExpCanon(shape, colors, fades, trail, twinkle)
	return out, true
}

// matchFireworkRocket handles crafting_special_firework_rocket
// (measured FireworkRocketRecipe): ingredientCount≥2; exactly one
// shell (paper), 1-3 fuel (gunpowder), any number of stars
// (firework_star); anything else fails. assemble applies FIREWORKS =
// {flight_duration: fuel count, explosions: each star's
// firework_explosion, in grid order} onto the count-3 rocket
// template.
func matchFireworkRocket(g craftGrid, filled []bool) (ItemStack, bool) {
	count := 0
	for _, f := range filled {
		if f {
			count++
		}
	}
	if count < 2 {
		return ItemStack{}, false
	}
	var (
		shell      bool
		fuel       int32
		explosions []string
	)
	for i, f := range filled {
		if !f {
			continue
		}
		st := g[i]
		name, ok := itemNameOf(st.ID)
		if !ok {
			return ItemStack{}, false
		}
		switch {
		case name == "paper":
			if shell {
				return ItemStack{}, false
			}
			shell = true
		case name == "gunpowder":
			fuel++
			if fuel > 3 {
				return ItemStack{}, false
			}
		case name == "firework_star":
			if st.FireworkExp != "" {
				explosions = append(explosions, st.FireworkExp)
			}
		default:
			return ItemStack{}, false
		}
	}
	if !shell || fuel < 1 {
		return ItemStack{}, false
	}
	rid, ok := world.ItemID("firework_rocket")
	if !ok {
		return ItemStack{}, false
	}
	out := ItemStack{ID: rid, Count: 3}
	fw := strconv.Itoa(int(fuel))
	for _, e := range explosions {
		fw += ";" + e
	}
	// firework_rocket's default FIREWORKS is {flight:1, []}: a
	// starless single-fuel result patches the default value, which
	// drops off the wire entirely — the stack reads bare.
	if fw != "1" {
		out.Fireworks = fw
	}
	return out, true
}
