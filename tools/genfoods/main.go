// Command genfoods extracts the vanilla consume-side tables from the
// 26.2 server jar bytecode:
//
//   - Foods.<clinit>: every food's nutrition, saturationModifier and
//     alwaysEdible flag (stew(n) expands to nutrition(n)+0.6).
//   - Consumables.<clinit>: per constant the drink flag, consumeSeconds
//     and the onConsume effect list (apply_status_effects with its one
//     probability roll, remove_effects, clear_all, teleport_randomly).
//   - Items.<clinit>: which items bind a food and/or consumable, their
//     use_cooldown seconds and use_remainder (usingConvertsTo) target.
//   - Potions.<clinit>: each potion's MobEffectInstance list.
//
// Usage: go run ./tools/genfoods <vanilla-jar-extract-dir>
// Writes internal/server/foodtable_gen.go.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// effect is one vanilla onConsume/MobEffectInstance application.
type effect struct {
	effect    string
	duration  int
	amplifier int
	prob      float64 // 1.0 unless an apply_status_effects chance
}

// consumable mirrors one Consumables constant.
type consumable struct {
	drink    bool
	seconds  float64
	effects  []effect // apply_status_effects list (all share one prob roll)
	applyAll bool     // effects applied as one list (no per-prob semantics differs; prob lives on each entry anyway)
	remove   []string
	clear    bool
	teleport bool
}

// item binds an item id to its consume-side data.
type item struct {
	food          string
	cons          string
	cooldownTicks int
	convertsTo    string
}

func javap(dir, class string) string {
	out, err := exec.Command("javap", "-classpath", dir, "-c", "-p", strings.TrimSuffix(strings.ReplaceAll(class, "/", "."), ".class")).Output()
	if err != nil {
		panic(fmt.Sprintf("javap %s: %v", class, err))
	}
	return string(out)
}

var (
	putstaticFood = regexp.MustCompile(`putstatic\s+\S+\s+// Field ([A-Z_]+):Lnet/minecraft/world/food/FoodProperties;`)
	putstaticCons = regexp.MustCompile(`putstatic\s+\S+\s+// Field ([A-Z_]+):Lnet/minecraft/world/item/component/Consumable;`)
	putstaticItem = regexp.MustCompile(`putstatic\s+\S+\s+// Field ([A-Z_]+):Lnet/minecraft/world/item/Item;`)
	getItemIds    = regexp.MustCompile(`getstatic\s+\S+\s+// Field net/minecraft/references/(ItemIds|BlockItemIds)\.([A-Z_]+)`)
	getFoods      = regexp.MustCompile(`getstatic\s+\S+\s+// Field net/minecraft/world/food/Foods\.([A-Z_]+)`)
	getCons       = regexp.MustCompile(`getstatic\s+\S+\s+// Field net/minecraft/world/item/component/Consumables\.([A-Z_]+)`)
	getItems      = regexp.MustCompile(`getstatic\s+\S+\s+// Field ([A-Z_]+):Lnet/minecraft/world/item/Item;`)
	getEffect     = regexp.MustCompile(`getstatic\s+\S+\s+// Field net/minecraft/world/effect/MobEffects\.([A-Z_]+)`)
	floatRe       = regexp.MustCompile(`// float ([0-9.]+)f`)
	potionName    = regexp.MustCompile(`// String (?:minecraft:)?([a-z_]+)`)
)

func iconst(ln string) (int, bool) {
	switch {
	case strings.HasPrefix(ln, "iconst_m1"):
		return -1, true
	case strings.HasPrefix(ln, "iconst_"):
		return int(ln[len("iconst_")] - '0'), true
	case strings.HasPrefix(ln, "bipush"):
		n, _ := strconv.Atoi(strings.Fields(ln)[1])
		return n, true
	case strings.HasPrefix(ln, "sipush"):
		n, _ := strconv.Atoi(strings.Fields(ln)[1])
		return n, true
	}
	return 0, false
}

func fconst(ln string) (float64, bool) {
	if strings.HasPrefix(ln, "fconst_") {
		return float64(ln[len("fconst_")] - '0'), true
	}
	if m := floatRe.FindStringSubmatch(ln); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		return f, true
	}
	return 0, false
}

// bytecode lines without the "  123: " address prefix.
func ops(src string) []string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		ln = strings.TrimSpace(ln)
		if i := strings.Index(ln, ": "); i > 0 && ln[i-1] >= '0' && ln[i-1] <= '9' {
			ln = ln[i+2:]
		}
		out = append(out, ln)
	}
	return out
}

func main() {
	dir := os.Args[1]
	foods := parseFoods(javap(dir, "net/minecraft/world/food/Foods.class"))
	cons := parseConsumables(javap(dir, "net/minecraft/world/item/component/Consumables.class"))
	blockIDs := parseBlockItemIds(javap(dir, "net/minecraft/references/BlockItemIds.class"))
	items := parseItems(javap(dir, "net/minecraft/world/item/Items.class"), blockIDs)
	potions := parsePotions(javap(dir, "net/minecraft/world/item/alchemy/Potions.class"))
	emit(foods, cons, items, potions)
}

// food is one extracted FoodProperties.
type food struct {
	nutrition int
	satMod    float64
	always    bool
}

func parseFoods(src string) map[string]food {
	out := map[string]food{}
	cur := food{}
	closed := func(name string) { out[strings.ToLower(name)] = cur; cur = food{} }
	pendingNutrition := false
	for _, ln := range ops(src) {
		switch {
		case strings.Contains(ln, ".nutrition:(I)"):
			pendingNutrition = true
		case pendingNutrition:
			// The operand precedes the call — handled below via intOps.
			pendingNutrition = false
		}
	}
	// Simpler state machine: walk forward tracking last int/float pushes.
	cur = food{}
	var lastInt int
	var lastFloat float64
	haveInt, haveFloat := false, false
	for _, ln := range ops(src) {
		if n, ok := iconst(ln); ok {
			lastInt, haveInt = n, true
			continue
		}
		if f, ok := fconst(ln); ok {
			lastFloat, haveFloat = f, true
			continue
		}
		switch {
		case strings.Contains(ln, ".nutrition:(I)") && haveInt:
			cur.nutrition = lastInt
			haveInt = false
		case strings.Contains(ln, ".saturationModifier:(F)") && haveFloat:
			cur.satMod = lastFloat
			haveFloat = false
		case strings.Contains(ln, "Method stew:(I)") && haveInt:
			cur.nutrition, cur.satMod = lastInt, 0.6
			haveInt = false
		case strings.Contains(ln, ".alwaysEdible:()"):
			cur.always = true
		default:
			if m := putstaticFood.FindStringSubmatch(ln); m != nil {
				closed(m[1])
			}
		}
	}
	return out
}

func parseConsumables(src string) map[string]consumable {
	out := map[string]consumable{}
	var cur *consumable
	var lastInt int
	var lastFloat float64
	haveFloat := false
	var pending []effect // MobEffectInstances collected before the effect ctor
	mode := ""
	var lastEff effect
	haveEff := false
	effName := ""
	effDur, effAmp := 0, 0
	effStage := 0 // 0 none, 1 saw holder, 2 saw duration int
	flushInst := func() {
		if effStage > 0 && effName != "" {
			lastEff = effect{effect: strings.ToLower(effName), duration: effDur, amplifier: effAmp}
			haveEff = true
		}
		effName, effDur, effAmp, effStage = "", 0, 0, 0
	}
	for _, ln := range ops(src) {
		if n, ok := iconst(ln); ok {
			if effStage == 1 {
				effDur, effStage = n, 2
			} else if effStage == 2 {
				effAmp, effStage = n, 3
			} else {
				lastInt = n
			}
			continue
		}
		if f, ok := fconst(ln); ok {
			lastFloat, haveFloat = f, true
			continue
		}
		if m := getEffect.FindStringSubmatch(ln); m != nil {
			flushInst()
			effName, effStage = m[1], 1
			continue
		}
		switch {
		case strings.Contains(ln, "Method net/minecraft/world/effect/MobEffectInstance.\"<init>\""):
			flushInst() // sets lastEff from the captured fields
			if mode == "apply" && haveEff {
				pending = append(pending, lastEff)
			}
		case strings.Contains(ln, "class net/minecraft/world/item/consume_effects/ApplyStatusEffectsConsumeEffect"):
			mode = "apply"
			if cur == nil {
				cur = &consumable{seconds: 1.6}
			}
		case strings.Contains(ln, "ApplyStatusEffectsConsumeEffect.\"<init>\":(Lnet/minecraft/world/effect/MobEffectInstance;F)V"):
			e := lastEff
			if haveEff {
				e.prob = lastFloat
			}
			if haveEff && cur != nil {
				cur.effects = append(cur.effects, e)
			}
			if len(pending) > 0 {
				pending = pending[:len(pending)-1] // single-instance ctor, not a list
			}
			mode, haveEff = "", false
		case strings.Contains(ln, "ApplyStatusEffectsConsumeEffect.\"<init>\":(Lnet/minecraft/world/effect/MobEffectInstance;)"):
			if haveEff && cur != nil {
				lastEff.prob = 1
				cur.effects = append(cur.effects, lastEff)
			}
			if len(pending) > 0 {
				pending = pending[:len(pending)-1]
			}
			mode, haveEff = "", false
		case strings.Contains(ln, "ApplyStatusEffectsConsumeEffect.\"<init>\":(Ljava/util/List;)V"):
			for _, e := range pending {
				e.prob = 1.0
				cur.effects = append(cur.effects, e)
			}
			pending, mode = nil, ""
		case strings.Contains(ln, "class net/minecraft/world/item/consume_effects/RemoveStatusEffectsConsumeEffect"):
			mode = "remove"
		case strings.Contains(ln, "RemoveStatusEffectsConsumeEffect.\"<init>\""):
			if mode == "remove" && cur != nil {
				cur.remove = append(cur.remove, strings.ToLower(effName))
			}
			mode = ""
		case strings.Contains(ln, "ClearAllStatusEffectsConsumeEffect"):
			if cur != nil {
				cur.clear = true
			}
		case strings.Contains(ln, "class net/minecraft/world/item/consume_effects/TeleportRandomlyConsumeEffect"):
			if cur != nil {
				cur.teleport = true
			}
		case strings.Contains(ln, "defaultDrink:()"):
			if cur == nil {
				cur = &consumable{seconds: 1.6}
			}
			cur.drink = true
		case strings.Contains(ln, "defaultFood:()"):
			if cur == nil {
				cur = &consumable{seconds: 1.6}
			}
		case strings.Contains(ln, "invokestatic") && strings.Contains(ln, "Builder;"):
			// start of a builder call — new consumable follows .build()
			if cur == nil {
				cur = &consumable{seconds: 1.6}
			}
		case strings.Contains(ln, ".consumeSeconds:(F)") && haveFloat:
			cur.seconds = lastFloat
			haveFloat = false
		default:
			if m := putstaticCons.FindStringSubmatch(ln); m != nil {
				name := strings.ToLower(m[1])
				if cur != nil {
					out[name] = *cur
				} else {
					out[name] = consumable{seconds: 1.6}
				}
				cur, pending, mode, haveEff = nil, nil, "", false
			}
		}
		if mode == "remove" && strings.Contains(ln, "MobEffects.") {
			// already handled via effName tracking? remove list uses the
			// same getstatic pattern caught above — capture it here.
		}
		_ = lastInt
	}
	return out
}

// parseBlockItemIds resolves each BlockItemId constant to the item
// name inside its create("block", "item") call — the constant name
// is the *block* name (CARROT_CROP → item "carrot").
func parseBlockItemIds(src string) map[string]string {
	out := map[string]string{}
	var strs []string
	strRe := regexp.MustCompile(`// String ([a-z_]+)`)
	for _, ln := range ops(src) {
		if strings.HasPrefix(ln, "ldc") {
			if m := strRe.FindStringSubmatch(ln); m != nil {
				strs = append(strs, m[1])
			}
			continue
		}
		if m := regexp.MustCompile(`putstatic\s+\S+\s+// Field ([A-Z_]+)`).FindStringSubmatch(ln); m != nil {
			if len(strs) >= 2 {
				out[m[1]] = strs[1]
			}
			strs = nil
		}
	}
	return out
}

func parseItems(src string, blockIDs map[string]string) map[string]item {
	out := map[string]item{}
	cur := ""
	var lastFloat float64
	var lastGetItem string
	var pendingFood, pendingCons string
	haveFloat := false
	for _, ln := range ops(src) {
		if m := getItemIds.FindStringSubmatch(ln); m != nil {
			if m[1] == "BlockItemIds" {
				cur = blockIDs[m[2]]
				if cur == "" {
					cur = strings.ToLower(m[2])
				}
			} else {
				cur = strings.ToLower(m[2])
			}
			continue
		}
		if f, ok := fconst(ln); ok {
			lastFloat, haveFloat = f, true
			continue
		}
		if m := getItems.FindStringSubmatch(ln); m != nil {
			lastGetItem = strings.ToLower(m[1])
			continue
		}
		if m := getFoods.FindStringSubmatch(ln); m != nil {
			pendingFood = strings.ToLower(m[1])
			continue
		}
		if m := getCons.FindStringSubmatch(ln); m != nil {
			pendingCons = strings.ToLower(m[1])
			continue
		}
		if cur == "" {
			continue
		}
		it := out[cur]
		switch {
		case strings.Contains(ln, ".food:(Lnet/minecraft/world/food/FoodProperties;"):
			// food(FoodProperties) or food(FoodProperties, Consumable):
			// both add a CONSUMABLE — the 1-arg form defaults to
			// Consumables.DEFAULT_FOOD. A bare .component(FOOD, x)
			// (mob buckets) adds no consumable and is not edible.
			it.food = pendingFood
			it.cons = pendingCons
			if it.cons == "" {
				it.cons = "default_food"
			}
			pendingFood, pendingCons = "", ""
		case strings.Contains(ln, ".component:(Lnet/minecraft/core/component/DataComponentType;"):
			// A CONSUMABLE component arg; the Foods.X arg of
			// .component(FOOD, x) is metadata only, not edible.
			if pendingCons != "" {
				it.cons = pendingCons
			}
			pendingFood, pendingCons = "", ""
		}
		if strings.Contains(ln, ".useCooldown:(F)") && haveFloat {
			it.cooldownTicks = int(lastFloat * 20)
			haveFloat = false
		}
		if strings.Contains(ln, ".usingConvertsTo:(Lnet/minecraft/world/item/Item;)") {
			it.convertsTo = lastGetItem
		}
		out[cur] = it
	}
	return out
}

var getPotionIds = regexp.MustCompile(`getstatic\s+\S+\s+// Field net/minecraft/world/item/alchemy/PotionIds\.([A-Z_]+)`)

// parsePotions keys each registration by its PotionIds constant — the
// registry id — since the Potion ctor's name string is only the
// internal/base name (long/strong variants reuse it).
func parsePotions(src string) map[string][]effect {
	out := map[string][]effect{}
	cur := ""
	var effName string
	effDur, effAmp := 0, 0
	stage := 0
	for _, ln := range ops(src) {
		if m := getPotionIds.FindStringSubmatch(ln); m != nil {
			cur = strings.ToLower(m[1])
			out[cur] = out[cur] // entry even when the effect list is empty
			continue
		}
		if m := getEffect.FindStringSubmatch(ln); m != nil {
			effName, stage = m[1], 1
			effDur, effAmp = 0, 0
			continue
		}
		if n, ok := iconst(ln); ok {
			if stage == 1 {
				effDur, stage = n, 2
			} else if stage == 2 {
				effAmp, stage = n, 3
			}
			continue
		}
		if strings.Contains(ln, "MobEffectInstance.\"<init>\"") && cur != "" && effName != "" {
			out[cur] = append(out[cur], effect{effect: strings.ToLower(effName), duration: effDur, amplifier: effAmp, prob: 1})
			effName, stage = "", 0
			continue
		}
	}
	return out
}

func emit(foods map[string]food, cons map[string]consumable, items map[string]item, potions map[string][]effect) {
	var b strings.Builder
	b.WriteString(`// Code generated by tools/genfoods from the vanilla 26.2 jar
// (Foods, Consumables, Items and Potions clinit bytecode); DO NOT EDIT.

package server

// foodConsume is the server-side half of one item's consumable data:
// hunger/saturation restore plus the onConsume effect list read at
// finishUsingItem time.
type foodConsume struct {
	nutrition int
	satMod    float64
	always    bool
	eatTicks  int32
	drink     bool
	// foodless marks consumable-only items (milk, potions, the ominous
	// bottle): no FOOD component, so canConsume skips the hunger gate
	// entirely and they are always consumable.
	foodless  bool
	cooldown  int // use_cooldown component, ticks
	converts  string
	effects   []consumeEffect
	remove    []string
	clear     bool
	teleport  bool
}

// consumeEffect is one MobEffectInstance an onConsume effect applies.
type consumeEffect struct {
	effect    string
	duration  int
	amplifier int
	prob      float64 // the apply_status_effects list's shared roll
}

// foodTable maps item ids to their consume data. Items absent here
// have no food/consumable component. Vanilla's consumeSeconds is always
// 1.6 except dried_kelp (0.8) and honey_bottle (2.0); the table stores
// eatTicks = seconds*20.
var foodTable = map[string]foodConsume{
`)
	names := make([]string, 0, len(items))
	for n := range items {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		it := items[n]
		c, isCons := cons[it.cons]
		f, isFood := foods[it.food]
		if !isCons && !isFood && it.cooldownTicks == 0 && it.convertsTo == "" {
			continue
		}
		eatTicks := 32
		drink := false
		var effects []effect
		var remove []string
		clear, teleport := false, false
		if isCons {
			eatTicks = int(c.seconds * 20)
			drink = c.drink
			effects = c.effects
			remove = c.remove
			clear = c.clear
			teleport = c.teleport
		}
		fmt.Fprintf(&b, "\t%q: {nutrition: %d, satMod: %g, always: %v, eatTicks: %d, drink: %v",
			n, f.nutrition, f.satMod, f.always, eatTicks, drink)
		if isCons && !isFood {
			b.WriteString(", foodless: true")
		}
		if it.cooldownTicks > 0 {
			fmt.Fprintf(&b, ", cooldown: %d", it.cooldownTicks)
		}
		if it.convertsTo != "" {
			fmt.Fprintf(&b, ", converts: %q", it.convertsTo)
		}
		if len(effects) > 0 {
			b.WriteString(", effects: []consumeEffect{")
			for _, e := range effects {
				fmt.Fprintf(&b, "{effect: %q, duration: %d, amplifier: %d, prob: %g},", e.effect, e.duration, e.amplifier, e.prob)
			}
			b.WriteString("}")
		}
		if len(remove) > 0 {
			b.WriteString(", remove: []string{")
			for _, r := range remove {
				fmt.Fprintf(&b, "%q,", r)
			}
			b.WriteString("}")
		}
		if clear {
			b.WriteString(", clear: true")
		}
		if teleport {
			b.WriteString(", teleport: true")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n\n// potionEffects maps a potion_contents potion id to its\n// MobEffectInstance list (amplifier index 0-based).\nvar potionEffects = map[string][]consumeEffect{\n")
	pnames := make([]string, 0, len(potions))
	for n := range potions {
		pnames = append(pnames, n)
	}
	sort.Strings(pnames)
	for _, n := range pnames {
		fmt.Fprintf(&b, "\t%q: {", n)
		for i, e := range potions[n] {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "{effect: %q, duration: %d, amplifier: %d}", e.effect, e.duration, e.amplifier)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	if err := os.WriteFile("internal/server/foodtable_gen.go", []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote foodtable_gen.go: %d items, %d potions\n", len(items), len(potions))
}
