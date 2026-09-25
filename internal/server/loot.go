package server

import (
	"math/rand"
	"sort"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// lootCtx carries the LootParams a table sees. Block drops set
// tool/block/pos/dim/player; chest loot leaves them zero, which makes
// tool/block conditions fail — matching vanilla, where the params
// simply aren't in the context.
type lootCtx struct {
	tool  ItemStack   // tool=empty models the player's bare hand
	block string      // mined block id (block_state_property target)
	state world.Block // pre-break state; drops roll after removal
	pos   world.Pos
	dim   *dimension
	// bees is the block entity's occupant count captured before the
	// world write removed it — copy_components[bees] reads it.
	bees   int32
	rng    *rand.Rand
	player *Session // "this" entity for entity_properties
	// explosionRadius > 0 means the roll is inside an explosion and
	// carries vanilla's EXPLOSION_RADIUS loot param — the
	// survives_explosion condition then drops at 1/radius.
	explosionRadius float64
}

// rollLootTable draws one chest's contents from a vanilla loot table.
// Item entries are weighted picks; set_count's uniform provider sets
// the stack size. Functions without an item effect (set_damage,
// enchant_*) are skipped — the generated item set is what the
// client-visible slots get. Nested loot_table entries recurse into
// the named table (gameplay/fishing's fish/junk/treasure split),
// group/sequence children roll their member entries, and
// alternatives entries roll the first child whose conditions pass
// and that rolls anything.
func rollLootTable(table string, seed int64) []ItemStack {
	ctx := &lootCtx{rng: rand.New(rand.NewSource(seed))}
	return rollLootDepth(ctx, table, 0)
}

// rollBlockLoot draws drops for breaking block bname at pos: the
// mined block's identity and state feed block_state_property and
// location_check conditions, and the held tool feeds match_tool,
// table_bonus, and apply_bonus.
func rollBlockLoot(table string, sess *Session, pos world.Pos, bname string, state world.Block, bees int32) []ItemStack {
	ctx := &lootCtx{
		tool:   sess.heldStack(),
		block:  bname,
		state:  state,
		pos:    pos,
		dim:    sess.dimension(),
		bees:   bees,
		rng:    rand.New(rand.NewSource(rand.Int63())),
		player: sess,
	}
	return rollLootDepth(ctx, table, 0)
}

// rollExplosionLoot draws drops for a block destroyed by an explosion:
// vanilla builds the params with TOOL=empty and EXPLOSION_RADIUS set,
// which is what the survives_explosion condition reads.
func rollExplosionLoot(table string, state world.Block, pos world.Pos, d *dimension, radius float32) []ItemStack {
	ctx := &lootCtx{
		block:           table[len("blocks/"):],
		state:           state,
		pos:             pos,
		dim:             d,
		rng:             rand.New(rand.NewSource(rand.Int63())),
		explosionRadius: float64(radius),
	}
	return rollLootDepth(ctx, table, 0)
}

func rollLootDepth(ctx *lootCtx, table string, depth int) []ItemStack {
	if depth > 8 {
		return nil
	}
	lt, ok := data.LootTables()[table]
	if !ok {
		return nil
	}
	pools, _ := lt["pools"].([]any)
	var out []ItemStack
	for _, pv := range pools {
		pool, _ := pv.(map[string]any)
		if !lootCondsOK(pool["conditions"], ctx) {
			continue
		}
		rolls := lootNumber(pool["rolls"], ctx.rng)
		entries, _ := pool["entries"].([]any)
		if rolls <= 0 || len(entries) == 0 {
			continue
		}
		poolFns, _ := pool["functions"].([]any)
		for i := 0; i < int(rolls); i++ {
			for _, st := range rollPoolPick(entries, ctx, depth) {
				st = lootApplyFuncs(poolFns, st, ctx)
				if !st.Empty() {
					out = append(out, st)
				}
			}
		}
	}
	return out
}

// lootEntry picks one weighted entry whose conditions pass and
// evaluates it.
func rollPoolPick(entries []any, ctx *lootCtx, depth int) []ItemStack {
	var live []map[string]any
	total := 0.0
	for _, ev := range entries {
		em, _ := ev.(map[string]any)
		if !lootCondsOK(em["conditions"], ctx) {
			continue
		}
		w, _ := em["weight"].(float64)
		if w <= 0 {
			w = 1
		}
		total += w
		live = append(live, em)
	}
	if total <= 0 {
		return nil
	}
	pick := ctx.rng.Float64() * total
	for _, em := range live {
		w, _ := em["weight"].(float64)
		if w <= 0 {
			w = 1
		}
		pick -= w
		if pick > 0 {
			continue
		}
		return lootEntryValue(em, ctx, depth)
	}
	return nil
}

// lootEntryValue evaluates a chosen entry by type.
func lootEntryValue(em map[string]any, ctx *lootCtx, depth int) []ItemStack {
	switch ty, _ := em["type"].(string); ty {
	case "minecraft:loot_table", "loot_table":
		name, _ := em["value"].(string)
		return rollLootDepth(ctx, strings.TrimPrefix(name, "minecraft:"), depth+1)
	case "minecraft:alternatives", "alternatives", "minecraft:sequence", "sequence", "minecraft:group", "group":
		children, _ := em["children"].([]any)
		if ty == "minecraft:alternatives" || ty == "alternatives" {
			// First child whose conditions pass and that produces
			// anything — vanilla picks the first successful child.
			for _, cv := range children {
				cm, _ := cv.(map[string]any)
				if !lootCondsOK(cm["conditions"], ctx) {
					continue
				}
				if got := lootEntryValue(cm, ctx, depth); len(got) > 0 {
					return got
				}
			}
			return nil
		}
		var out []ItemStack
		for _, cv := range children {
			cm, _ := cv.(map[string]any)
			if !lootCondsOK(cm["conditions"], ctx) {
				continue
			}
			out = append(out, lootEntryValue(cm, ctx, depth)...)
		}
		return out
	case "minecraft:item", "item":
		name, _ := em["name"].(string)
		id, ok := world.ItemID(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			return nil
		}
		st := ItemStack{ID: id, Count: 1}
		fns, _ := em["functions"].([]any)
		st = lootApplyFuncs(fns, st, ctx)
		if st.Empty() || st.Count < 1 {
			return nil
		}
		return []ItemStack{st}
	case "minecraft:dynamic", "dynamic":
		// Block-entity contents (decorated_pot sherds); the pot drops
		// itself via the surviving sibling entry.
		return nil
	}
	return nil
}

// lootCondsOK evaluates a conditions array — every entry must pass.
func lootCondsOK(v any, ctx *lootCtx) bool {
	conds, _ := v.([]any)
	for _, cv := range conds {
		cm, _ := cv.(map[string]any)
		if !lootCondOK(cm, ctx) {
			return false
		}
	}
	return true
}

func lootCondOK(cm map[string]any, ctx *lootCtx) bool {
	switch c, _ := cm["condition"].(string); c {
	case "minecraft:survives_explosion", "survives_explosion":
		// EXPLOSION_RADIUS absent → always survives. Present →
		// vanilla's 1/radius chance (LootItemRandomChanceCondition
		// with NumberProviders.constant(1/radius)).
		if ctx.explosionRadius <= 0 {
			return true
		}
		return ctx.rng.Float64() < 1.0/ctx.explosionRadius
	case "minecraft:entity_properties", "entity_properties":
		// Only used with an empty predicate against "this" — the
		// digging/command player.
		return ctx.player != nil
	case "minecraft:block_state_property", "block_state_property":
		want, _ := cm["block"].(string)
		want = strings.TrimPrefix(want, "minecraft:")
		if ctx.block == "" || want != ctx.block {
			return false
		}
		props, _ := cm["properties"].(map[string]any)
		for prop, val := range props {
			got, ok := world.StateProperty(ctx.state, prop)
			vs, _ := val.(string)
			if !ok || got != vs {
				return false
			}
		}
		return true
	case "minecraft:match_tool", "match_tool":
		pred, _ := cm["predicate"].(map[string]any)
		return lootMatchTool(pred, ctx)
	case "minecraft:any_of", "any_of":
		terms, _ := cm["terms"].([]any)
		for _, tv := range terms {
			tm, _ := tv.(map[string]any)
			if lootCondOK(tm, ctx) {
				return true
			}
		}
		return false
	case "minecraft:inverted", "inverted":
		term, _ := cm["term"].(map[string]any)
		return !lootCondOK(term, ctx)
	case "minecraft:table_bonus", "table_bonus":
		ench, _ := cm["enchantment"].(string)
		lvl := ctx.tool.enchLevel(strings.TrimPrefix(ench, "minecraft:"))
		chances, _ := cm["chances"].([]any)
		if len(chances) == 0 {
			return false
		}
		i := int(lvl)
		if i >= len(chances) {
			i = len(chances) - 1
		}
		p, _ := chances[i].(float64)
		return ctx.rng.Float64() < p
	case "minecraft:random_chance", "random_chance":
		return ctx.rng.Float64() < lootNumber(cm["chance"], ctx.rng)
	case "minecraft:location_check", "location_check":
		return lootLocationCheck(cm, ctx)
	}
	// Unknown conditions are treated as passing (a datapack
	// condition we don't model shouldn't silently drop loot).
	return true
}

// lootMatchTool evaluates an item predicate against the held tool:
// `items` (id, list, or "#tag") and `predicates` (item sub-predicates;
// the block tables only use minecraft:enchantments).
func lootMatchTool(pred map[string]any, ctx *lootCtx) bool {
	if ctx.tool.Empty() {
		return false
	}
	tname, _ := world.ItemName(ctx.tool.ID)
	if v, ok := pred["items"]; ok {
		if !lootItemIn(v, tname) {
			return false
		}
	}
	if inner, ok := pred["predicates"].(map[string]any); ok {
		for k, pv := range inner {
			switch k {
			case "minecraft:enchantments", "enchantments":
				for _, ev := range pv.([]any) {
					em, _ := ev.(map[string]any)
					if !lootEnchMatch(em, ctx) {
						return false
					}
				}
			}
		}
	}
	return true
}

// lootItemIn matches an item-id value, string list, or "#tag"
// reference against a held item name.
func lootItemIn(v any, tname string) bool {
	switch t := v.(type) {
	case string:
		if strings.HasPrefix(t, "#") {
			return data.HasItemTag(strings.TrimPrefix(strings.TrimPrefix(t, "#"), "minecraft:"), tname)
		}
		return strings.TrimPrefix(t, "minecraft:") == tname
	case []any:
		for _, e := range t {
			if lootItemIn(e, tname) {
				return true
			}
		}
	}
	return false
}

// lootEnchMatch evaluates one enchantments[] sub-predicate: the tool
// must carry one of the listed enchantments at a level inside
// `levels` ({"min"/"max"} or a bare int).
func lootEnchMatch(em map[string]any, ctx *lootCtx) bool {
	lvls, _ := em["levels"]
	for _, name := range lootNameList(em["enchantments"]) {
		lvl := ctx.tool.enchLevel(strings.TrimPrefix(name, "minecraft:"))
		if lvl <= 0 && ctx.tool.StoredEnch != "" {
			st := ItemStack{Ench: ctx.tool.StoredEnch}
			lvl = st.enchLevel(strings.TrimPrefix(name, "minecraft:"))
		}
		if lvl <= 0 {
			continue
		}
		switch lv := lvls.(type) {
		case float64:
			if int32(lv) == lvl {
				return true
			}
		case map[string]any:
			min, _ := lv["min"].(float64)
			max, ok := lv["max"].(float64)
			if float64(lvl) >= min && (!ok || float64(lvl) <= max) {
				return true
			}
		}
	}
	return false
}

func lootNameList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// lootLocationCheck matches the block at pos+offset against
// predicate.block (name + state properties).
func lootLocationCheck(cm map[string]any, ctx *lootCtx) bool {
	if ctx.dim == nil {
		return false
	}
	pos := ctx.pos
	if x, ok := cm["offsetX"].(float64); ok {
		pos.X += int(x)
	}
	if y, ok := cm["offsetY"].(float64); ok {
		pos.Y += int(y)
	}
	if z, ok := cm["offsetZ"].(float64); ok {
		pos.Z += int(z)
	}
	pred, _ := cm["predicate"].(map[string]any)
	bpred, _ := pred["block"].(map[string]any)
	b := ctx.dim.w.GetBlock(pos)
	name, _ := world.StateOwner(b)
	if v, ok := bpred["blocks"]; ok {
		if !lootBlockIn(v, name) {
			return false
		}
	}
	state, _ := bpred["state"].(map[string]any)
	for prop, val := range state {
		got, ok := world.StateProperty(b, prop)
		vs, _ := val.(string)
		if !ok || got != vs {
			return false
		}
	}
	return true
}

func lootBlockIn(v any, bname string) bool {
	switch t := v.(type) {
	case string:
		if strings.HasPrefix(t, "#") {
			tag := strings.TrimPrefix(strings.TrimPrefix(t, "#"), "minecraft:")
			for _, m := range data.BlockTags[tag] {
				if m == bname {
					return true
				}
			}
			return false
		}
		return strings.TrimPrefix(t, "minecraft:") == bname
	case []any:
		for _, e := range t {
			if lootBlockIn(e, bname) {
				return true
			}
		}
	}
	return false
}

// lootApplyFuncs runs the entry/pool functions list against a
// produced stack. Unmodeled functions (copy_state, copy_components,
// set_*) are skipped, matching the generated-item approximation.
func lootApplyFuncs(fns []any, st ItemStack, ctx *lootCtx) ItemStack {
	for _, fv := range fns {
		fm, _ := fv.(map[string]any)
		// Functions can carry their own conditions.
		if !lootCondsOK(fm["conditions"], ctx) {
			continue
		}
		switch fn, _ := fm["function"].(string); fn {
		case "minecraft:set_count", "set_count":
			n := int32(lootNumber(fm["count"], ctx.rng))
			if add, _ := fm["add"].(bool); add {
				st.Count += n
			} else {
				st.Count = n
			}
		case "minecraft:apply_bonus", "apply_bonus":
			st = lootApplyBonus(fm, st, ctx)
		case "minecraft:limit_count", "limit_count":
			if lim, ok := fm["limit"].(map[string]any); ok {
				if mn, ok := lim["min"].(float64); ok && st.Count < int32(mn) {
					st.Count = int32(mn)
				}
				if mx, ok := lim["max"].(float64); ok && st.Count > int32(mx) {
					st.Count = int32(mx)
				}
			} else if mx, ok := fm["limit"].(float64); ok && st.Count > int32(mx) {
				st.Count = int32(mx)
			}
		case "minecraft:sequence", "sequence":
			if inner, ok := fm["functions"].([]any); ok {
				st = lootApplyFuncs(inner, st, ctx)
			}
		case "minecraft:filtered", "filtered":
			if inner, ok := fm["functions"].([]any); ok {
				if lootCondsOK(fm["item_filter"], ctx) {
					st = lootApplyFuncs(inner, st, ctx)
				}
			}
		case "minecraft:explosion_decay", "explosion_decay":
			// No explosion context: decay never fires.
		case "minecraft:copy_components", "copy_components":
			st = lootCopyComponents(fm, st, ctx)
		case "minecraft:copy_state", "copy_state":
			st = lootCopyState(fm, st, ctx)
		}
	}
	return st
}

// lootCopyComponents is CopyComponentsFunction's block_entity source:
// each include name maps onto the modeled slice of the block entity's
// components — a nest's bees occupant list. Includes our stacks do
// not model (lock, profile, tooltip_display, …) contribute nothing.
func lootCopyComponents(fm map[string]any, st ItemStack, ctx *lootCtx) ItemStack {
	if src, _ := fm["source"].(string); src != "block_entity" && src != "minecraft:block_entity" {
		return st
	}
	inc, _ := fm["include"].([]any)
	for _, iv := range inc {
		switch name, _ := iv.(string); strings.TrimPrefix(name, "minecraft:") {
		case "bees":
			st.Bees = ctx.bees
		}
	}
	return st
}

// lootCopyState is CopyBlockState: each named property of the mined
// block folds into the stack's minecraft:block_state component so a
// silk-touched nest keeps its honey_level through the drop. The
// function's own "block" field must name the mined block.
func lootCopyState(fm map[string]any, st ItemStack, ctx *lootCtx) ItemStack {
	if blk, _ := fm["block"].(string); blk != "" {
		if strings.TrimPrefix(blk, "minecraft:") != ctx.block {
			return st
		}
	}
	var extra []string
	for _, pv := range fm["properties"].([]any) {
		prop, _ := pv.(string)
		if val, ok := world.StateProperty(ctx.state, prop); ok {
			extra = append(extra, prop+"="+val)
		}
	}
	if len(extra) == 0 {
		return st
	}
	if st.BlockState != "" {
		extra = append(extra, strings.Split(st.BlockState, ",")...)
	}
	sort.Strings(extra)
	st.BlockState = strings.Join(extra, ",")
	return st
}

// lootApplyBonus implements ApplyBonusCount: the tool's fortune level
// scales the stack via one of three formulas.
func lootApplyBonus(fm map[string]any, st ItemStack, ctx *lootCtx) ItemStack {
	ench, _ := fm["enchantment"].(string)
	lvl := int(ctx.tool.enchLevel(strings.TrimPrefix(ench, "minecraft:")))
	if lvl <= 0 {
		return st
	}
	params, _ := fm["parameters"].(map[string]any)
	switch f, _ := fm["formula"].(string); f {
	case "minecraft:ore_drops", "ore_drops":
		j := ctx.rng.Intn(lvl+2) - 1
		if j < 0 {
			j = 0
		}
		st.Count *= int32(j + 1)
	case "minecraft:uniform_bonus_count", "uniform_bonus_count":
		mult, _ := params["bonusMultiplier"].(float64)
		st.Count += int32(ctx.rng.Intn(int(mult) * (lvl + 1)))
	case "minecraft:binomial_with_bonus_count", "binomial_with_bonus_count":
		extra, _ := params["extraRounds"].(float64)
		prob, _ := params["probability"].(float64)
		for i := 0; i < lvl+int(extra); i++ {
			if ctx.rng.Float64() < prob {
				st.Count++
			}
		}
	}
	return st
}

// toolCorrectForDrops mirrors Player.hasCorrectToolForDrops: the
// tool component's first matching rule decides. Vanilla tool rules
// are uniform — diggers deny incorrect_for_<tier> and mine-and-drop
// mineable/<kind>; swords and shears only mine-and-drop cobweb;
// everything else (hands, tridents, spears, maces) is never correct.
func toolCorrectForDrops(tool ItemStack, block string) bool {
	if tool.Empty() {
		return false
	}
	name, ok := world.ItemName(tool.ID)
	if !ok {
		return false
	}
	for _, kind := range []string{"pickaxe", "axe", "shovel", "hoe"} {
		if !strings.HasSuffix(name, "_"+kind) {
			continue
		}
		tier := strings.TrimSuffix(name, "_"+kind)
		if tier == "golden" {
			tier = "gold"
		}
		if blockInTag("incorrect_for_"+tier+"_tool", block) {
			return false
		}
		return blockInTag("mineable/"+kind, block)
	}
	if name == "shears" || strings.HasSuffix(name, "_sword") {
		return block == "cobweb"
	}
	return false
}

func blockInTag(tag, block string) bool {
	for _, m := range data.BlockTags[tag] {
		if m == block {
			return true
		}
	}
	return false
}

// lootNumber resolves a loot number provider: a bare float or the
// uniform/binomial shapes vanilla uses in tables.
func lootNumber(v any, rng *rand.Rand) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	case map[string]any:
		switch ty, _ := t["type"].(string); ty {
		case "minecraft:uniform", "uniform":
			min, _ := t["min"].(float64)
			max, _ := t["max"].(float64)
			return min + rng.Float64()*(max-min)
		case "minecraft:binomial", "binomial":
			n, _ := t["n"].(float64)
			p, _ := t["p"].(float64)
			acc := 0.0
			for i := 0; i < int(n); i++ {
				if rng.Float64() < p {
					acc++
				}
			}
			return acc
		case "minecraft:constant", "constant":
			c, _ := t["value"].(float64)
			return c
		}
		if c, ok := t["value"]; ok {
			return lootNumber(c, rng)
		}
	}
	return 0
}
