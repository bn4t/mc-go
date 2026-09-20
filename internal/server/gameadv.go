package server

import (
	"strings"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Gameplay (non-recipe) advancements. Recipe advancements stay on the
// RecipeAdv path — this layer covers the gameplay tree that syncs
// through the same update_advancements packet: defs replayed verbatim
// from the captured wire table (data.AdvDefWire), progress emitted as
// criteria/timestamp records, and visibility following vanilla's
// AdvancementVisibilityEvaluator rule.

// advMeta is one advancement's runtime shape: identity for the tree
// walk, criteria for progress emission, and the kill-trigger conditions
// decoded into a matchable form.
type advMeta struct {
	id, parent string
	criteria   []string // names; emit order is computed, never JSON order
	reqs       [][]string
	hasDisplay bool
	hidden     bool
	announce   bool   // display.announce_to_chat (default true)
	titleKey   string // display.title translate key
	descKey    string // display.description translate key
	frame      string // display.frame: task/goal/challenge
	kills      []killCond
	deaths     []killCond    // entity_killed_player — conditions apply to the killer
	locs       []locCond     // location — only biome checks are modeled
	hurts      []hurtCond    // player_hurt_entity — a player dealt damage
	inv        []invCond     // inventory_changed — an item reached inventory
	consumed   []consumeCond // consume_item — a food finished eating
	placed     []placedCond  // placed_block — a block landed in the world
	dims       []dimCond     // changed_dimension — crossed dimensions
	usedOn     []usedOnCond  // item_used_on_block — tool met block
	travel     []travelCond  // nether_travel — portal loop distance
	summons    []summonCond  // summoned_entity — a built entity spawned
}

// locCond is a location trigger criterion reduced to its biome check:
// the flat world is plains throughout, so biomes is the only condition
// mc-go can satisfy. Anything else (structures, stepping_on, dimension,
// equipment on the player predicate) leaves the criterion locked.
type locCond struct {
	crit       string
	biomes     map[string]bool
	impossible bool
}

// killCond is one criterion's player_killed_entity condition reduced to
// what mc-go can evaluate: the victim's entity type and the horizontal
// distance between victim and killer. Anything else in the condition
// (equipment, entity tags beyond type, mob effects, ...) sets impossible
// so the criterion stays locked — the same as vanilla when the predicate
// does not match.
type killCond struct {
	crit       string
	types      map[string]bool // nil = any entity
	minDist    float64         // conditions.entity[].distance.horizontal.min
	needsProj  bool            // killing_blow.tags contains is_projectile
	direct     map[string]bool // killing_blow.direct_entity entity types
	impossible bool
}

// hurtCond is one player_hurt_entity criterion reduced to what mc-go
// evaluates: damage.type.direct_entity's entity type set, the
// is_projectile damage tag, and dealt.min. Blocked damage, item
// predicates beyond entity type, and unmodeled condition fields leave
// the criterion locked.
type hurtCond struct {
	crit       string
	direct     map[string]bool // direct_entity entity types (nil = any)
	needsProj  bool            // damage.type.tags contains is_projectile
	minDealt   float64         // damage.dealt.min (raw hearts, not half)
	impossible bool
}

// invCond is one inventory_changed criterion reduced to its items[]
// OR-list: each entry a concrete item name or an expanded item tag. The
// slots and equipment condition shapes are not modeled, so criteria
// that use them stay locked.
type invCond struct {
	crit       string
	items      map[string]bool
	impossible bool
}

// consumeCond is one consume_item criterion: the eaten item's name set
// (empty = any item, e.g. husbandry/root's consumed_item).
type consumeCond struct {
	crit       string
	items      map[string]bool
	impossible bool
}

// dimCond is one changed_dimension criterion: optional from/to
// dimension names (minecraft:overworld / the_nether / the_end). The
// condition shape is flat strings, not location predicates.
type dimCond struct {
	crit       string
	from       string
	to         string
	impossible bool
}

// usedOnCond is one item_used_on_block criterion reduced to the
// clicked block's name set and the used item's name set. Block state
// checks, biome predicates, and component predicates (the jukebox's
// playable check) are unmodeled and lock the criterion.
type usedOnCond struct {
	crit       string
	blocks     map[string]bool
	toolItems  map[string]bool
	impossible bool
}

// travelCond is one nether_travel criterion: the horizontal
// overworld-space distance between the portal the player entered and
// the portal they exited. Other distance shapes (absolute, y, ...)
// lock the criterion.
type travelCond struct {
	crit       string
	minDist    float64
	impossible bool
}

// summonCond is one summoned_entity criterion: the constructed
// entity's type name (iron golem today; the wither's pattern is
// unmodeled). conditions.entity reduces to its entity_type predicate.
type summonCond struct {
	crit       string
	entityType string
	impossible bool
}

// placedCond is one placed_block criterion reduced to the placed
// block's name. The location predicate's richer shapes — block
// properties, all_of/any_of terms, biome/position checks — stay
// locked; only a plain {"block": name} single-term location matches.
type placedCond struct {
	crit       string
	block      string
	impossible bool
}

var (
	gameAdvs     = loadGameAdvs()
	gameAdvChild = func() map[string][]string {
		m := map[string][]string{}
		for _, a := range gameAdvs {
			if a.parent != "" {
				m[a.parent] = append(m[a.parent], a.id)
			}
		}
		return m
	}()
	gameAdvRoots []advMetaRoot
)

type advMetaRoot = string

func init() {
	for _, a := range gameAdvs {
		if a.parent == "" || gameAdvs[a.parent] == nil {
			gameAdvRoots = append(gameAdvRoots, a.id)
		}
	}
}

// loadGameAdvs decodes the bundled data pack's advancement tree into
// advMeta records. keys in data.Advancements() are datapack paths
// ("adventure/kill_a_mob") — wire ids carry the minecraft: namespace.
func loadGameAdvs() map[string]*advMeta {
	out := map[string]*advMeta{}
	for path, raw := range data.Advancements() {
		id := "minecraft:" + path
		m := &advMeta{id: id}
		if p, ok := raw["parent"].(string); ok {
			m.parent = p
		}
		if crit, ok := raw["criteria"].(map[string]any); ok {
			for cname, cv := range crit {
				m.criteria = append(m.criteria, cname)
				cm, _ := cv.(map[string]any)
				switch cm["trigger"] {
				case "minecraft:player_killed_entity":
					m.kills = append(m.kills, parseKillCond(cname, cm["conditions"]))
				case "minecraft:entity_killed_player":
					m.deaths = append(m.deaths, parseKillCond(cname, cm["conditions"]))
				case "minecraft:player_hurt_entity":
					m.hurts = append(m.hurts, parseHurtCond(cname, cm["conditions"]))
				case "minecraft:inventory_changed":
					m.inv = append(m.inv, parseInvCond(cname, cm["conditions"]))
				case "minecraft:consume_item":
					m.consumed = append(m.consumed, parseConsumeCond(cname, cm["conditions"]))
				case "minecraft:placed_block":
					m.placed = append(m.placed, parsePlacedCond(cname, cm["conditions"]))
				case "minecraft:changed_dimension":
					m.dims = append(m.dims, parseDimCond(cname, cm["conditions"]))
				case "minecraft:item_used_on_block":
					m.usedOn = append(m.usedOn, parseUsedOnCond(cname, cm["conditions"]))
				case "minecraft:nether_travel":
					m.travel = append(m.travel, parseTravelCond(cname, cm["conditions"]))
				case "minecraft:summoned_entity":
					m.summons = append(m.summons, parseSummonCond(cname, cm["conditions"]))
				case "minecraft:location":
					m.locs = append(m.locs, parseLocCond(cname, cm["conditions"]))
				}
			}
		}
		m.reqs = asGroups(raw["requirements"])
		// Vanilla folds single-string requirement entries into one-name
		// groups at JSON load; emit sees the same [][]string either way.
		if len(m.reqs) == 0 {
			for _, c := range m.criteria {
				m.reqs = append(m.reqs, []string{c})
			}
		}
		if disp, ok := raw["display"].(map[string]any); ok {
			m.hasDisplay = true
			m.announce = true
			if h, ok := disp["hidden"].(bool); ok {
				m.hidden = h
			}
			if a, ok := disp["announce_to_chat"].(bool); ok {
				m.announce = a
			}
			if t, ok := disp["title"].(map[string]any); ok {
				if tr, ok := t["translate"].(string); ok {
					m.titleKey = tr
				}
			}
			if d, ok := disp["description"].(map[string]any); ok {
				if tr, ok := d["translate"].(string); ok {
					m.descKey = tr
				}
			}
			if f, ok := disp["frame"].(string); ok {
				m.frame = f
			}
		}
		if m.frame == "" {
			m.frame = "task"
		}
		out[id] = m
	}
	return out
}

func asGroups(v any) [][]string {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	var out [][]string
	for _, g := range l {
		gl, _ := g.([]any)
		var grp []string
		for _, c := range gl {
			grp = append(grp, asJSONString(c))
		}
		out = append(out, grp)
	}
	return out
}

func asJSONString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// parseKillCond reduces a criterion's `conditions` object to killCond.
// conditions.entity is a list of "minecraft:entity_properties" loot
// conditions; we support entity_type (single or #tag) and
// distance.horizontal.min. killing_blow supports direct_entity entity
// type and the is_projectile tag.
func parseKillCond(cname string, conds any) killCond {
	kc := killCond{crit: cname}
	cm, ok := conds.(map[string]any)
	if !ok {
		return kc // null conditions — always match
	}
	ents, _ := cm["entity"].([]any)
	for _, ev := range ents {
		em, _ := ev.(map[string]any)
		// Only entity_properties predicates against `this` (the victim)
		// are modeled; anything else stays locked like a predicate that
		// does not match.
		if c, _ := em["condition"].(string); c != "minecraft:entity_properties" {
			kc.impossible = true
			continue
		}
		if e, _ := em["entity"].(string); e != "" && e != "this" {
			kc.impossible = true
			continue
		}
		pred, _ := em["predicate"].(map[string]any)
		if pred == nil {
			continue
		}
		for k, v := range pred {
			switch k {
			case "minecraft:entity_type":
				if kc.types == nil {
					kc.types = map[string]bool{}
				}
				if s, ok := v.(string); ok {
					if strings.HasPrefix(s, "#") {
						tag := strings.TrimPrefix(strings.TrimPrefix(s, "#"), "minecraft:")
						for _, m := range data.EntityTypeTags[tag] {
							kc.types[m] = true
						}
					} else {
						kc.types[strings.TrimPrefix(s, "minecraft:")] = true
					}
				}
			case "minecraft:distance":
				if dm, ok := v.(map[string]any); ok {
					if hm, ok := dm["horizontal"].(map[string]any); ok {
						if mn, ok := hm["min"].(float64); ok {
							kc.minDist = mn
						}
					}
				}
			default:
				kc.impossible = true
			}
		}
	}
	if kb, ok := cm["killing_blow"].(map[string]any); ok {
		if tags, ok := kb["tags"].([]any); ok {
			for _, tv := range tags {
				if tm, ok := tv.(map[string]any); ok {
					if tm["id"] == "minecraft:is_projectile" && tm["expected"] == true {
						kc.needsProj = true
					}
				}
			}
		}
		if de, ok := kb["direct_entity"].(map[string]any); ok {
			if et, ok := de["minecraft:entity_type"].(string); ok {
				kc.direct = map[string]bool{}
				if strings.HasPrefix(et, "#") {
					for _, m := range data.EntityTypeTags[strings.TrimPrefix(et, "#minecraft:")] {
						kc.direct[m] = true
					}
				} else {
					kc.direct[strings.TrimPrefix(et, "minecraft:")] = true
				}
			}
		}
	}
	return kc
}

// firePlayerKilledEntity evaluates every player_killed_entity criterion
// against a kill: victimType is the entity's registry name and (vx,vz vs
// ax,az) the horizontal distance between victim and attacker. Melee has
// no projectile/direct entity, so criteria that require either never
// match.
func (s *Server) firePlayerKilledEntity(sess *Session, victimType string, vx, vz, ax, az float64) {
	s.firePlayerKilledEntityDamage(sess, victimType, vx, vz, ax, az, false, "")
}

// firePlayerKilledEntityDamage is firePlayerKilledEntity carrying the
// killing blow's shape: proj is the is_projectile damage tag and
// directType the direct entity's name ("arrow", "trident"; empty for a
// plain melee strike, which has no direct entity separate from the
// attacker).
func (s *Server) firePlayerKilledEntityDamage(sess *Session, victimType string, vx, vz, ax, az float64, proj bool, directType string) {
	dx, dz := vx-ax, vz-az
	distSq := dx*dx + dz*dz
	for _, meta := range gameAdvs {
		for _, kc := range meta.kills {
			if kc.impossible {
				continue
			}
			if kc.needsProj && !proj {
				continue
			}
			if kc.direct != nil && !kc.direct[directType] {
				continue
			}
			if kc.types != nil && !kc.types[victimType] {
				continue
			}
			if kc.minDist > 0 && distSq < kc.minDist*kc.minDist {
				continue
			}
			sess.grantGameCriterion(meta, kc.crit)
		}
	}
}

// parseLocCond reduces a location criterion to its biome set.
// conditions.player lists entity_properties predicates on `this`; only
// predicate.location.biomes is interpreted.
func parseLocCond(cname string, conds any) locCond {
	lc := locCond{crit: cname, biomes: map[string]bool{}}
	cm, ok := conds.(map[string]any)
	if !ok {
		lc.impossible = true
		return lc
	}
	ents, _ := cm["player"].([]any)
	for _, ev := range ents {
		em, _ := ev.(map[string]any)
		if c, _ := em["condition"].(string); c != "minecraft:entity_properties" {
			lc.impossible = true
			continue
		}
		if e, _ := em["entity"].(string); e != "" && e != "this" {
			lc.impossible = true
			continue
		}
		pred, _ := em["predicate"].(map[string]any)
		for k, v := range pred {
			if k != "minecraft:location" {
				lc.impossible = true
				continue
			}
			loc, _ := v.(map[string]any)
			for lk, lv := range loc {
				switch lk {
				case "biomes":
					switch bv := lv.(type) {
					case string:
						lc.biomes[strings.TrimPrefix(bv, "minecraft:")] = true
					case []any:
						for _, sv := range bv {
							if bs, ok := sv.(string); ok {
								lc.biomes[strings.TrimPrefix(bs, "minecraft:")] = true
							}
						}
					default:
						lc.impossible = true
					}
				default:
					lc.impossible = true
				}
			}
		}
	}
	if len(lc.biomes) == 0 {
		lc.impossible = true
	}
	return lc
}

// dimBiome is the single biome a flat dimension reports everywhere —
// mc-go's generated terrain is flat per dimension, so a location check
// reduces to the dimension's fixed biome.
func dimBiome(dimName string) string {
	switch dimName {
	case "minecraft:overworld":
		return "plains"
	case "minecraft:the_nether":
		return "nether_wastes"
	case "minecraft:the_end":
		return "the_end"
	}
	return ""
}

// fireLocation runs the location trigger, which vanilla ticks per
// player every 20 ticks (ServerPlayer tickCount % 20). Each flat
// dimension reports its single biome; criteria asking for anything
// else stay locked.
func (s *Server) fireLocation(sess *Session) {
	biome := dimBiome(sess.dimName())
	if biome == "" {
		return
	}
	for _, meta := range gameAdvs {
		for _, lc := range meta.locs {
			if lc.impossible || !lc.biomes[biome] {
				continue
			}
			sess.grantGameCriterion(meta, lc.crit)
		}
	}
}

// parseHurtCond reduces a player_hurt_entity criterion's conditions to
// a hurtCond. Only damage.type.{direct_entity,tags} and damage.dealt.min
// are read — a criterion needing anything more is marked impossible.
func parseHurtCond(cname string, conds any) hurtCond {
	hc := hurtCond{crit: cname}
	cm, ok := conds.(map[string]any)
	if !ok {
		hc.impossible = true
		return hc
	}
	dm, _ := cm["damage"].(map[string]any)
	if dm == nil {
		return hc
	}
	if tm, ok := dm["type"].(map[string]any); ok {
		for k, v := range tm {
			switch k {
			case "direct_entity":
				hc.direct = map[string]bool{}
				if em, ok := v.(map[string]any); ok {
					if et, ok := em["minecraft:entity_type"].(string); ok {
						if strings.HasPrefix(et, "#") {
							for _, m := range data.EntityTypeTags[strings.TrimPrefix(et, "#minecraft:")] {
								hc.direct[m] = true
							}
						} else {
							hc.direct[strings.TrimPrefix(et, "minecraft:")] = true
						}
					}
				}
			case "tags":
				for _, tv := range v.([]any) {
					if tm, ok := tv.(map[string]any); ok {
						if tm["id"] == "minecraft:is_projectile" && tm["expected"] == true {
							hc.needsProj = true
						}
					}
				}
			default:
				hc.impossible = true
			}
		}
	}
	if dealt, ok := dm["dealt"].(map[string]any); ok {
		if mn, ok := dealt["min"].(float64); ok {
			hc.minDealt = mn
		}
	}
	return hc
}

// firePlayerHurtEntity evaluates every player_hurt_entity criterion
// against a hit: directType is the damage's direct entity (the arrow
// itself, or "player" for melee), proj the is_projectile tag on the
// damage type, and dealt the post-absorb damage in half-hearts.
func (s *Server) firePlayerHurtEntity(sess *Session, directType string, proj bool, dealt float32) {
	for _, meta := range gameAdvs {
		for _, hc := range meta.hurts {
			if hc.impossible || (hc.needsProj && !proj) {
				continue
			}
			if hc.direct != nil && !hc.direct[directType] {
				continue
			}
			if float64(dealt) < hc.minDealt {
				continue
			}
			sess.grantGameCriterion(meta, hc.crit)
		}
	}
}

// parseInvCond reduces an inventory_changed criterion to its items[]
// OR-set. Any condition key mc-go does not model (slots, equipment on
// the player predicate, ...) locks the criterion — matching vanilla
// where an unmatched predicate can never grant.
func parseInvCond(cname string, conds any) invCond {
	ic := invCond{crit: cname, items: map[string]bool{}}
	cm, _ := conds.(map[string]any)
	items, _ := cm["items"].([]any)
	for _, iv := range items {
		im, _ := iv.(map[string]any)
		if im == nil {
			ic.impossible = true
			continue
		}
		for k, v := range im {
			if k != "items" {
				ic.impossible = true
				continue
			}
			addItem := func(name string) {
				if strings.HasPrefix(name, "#") {
					for _, m := range data.ItemTags[strings.TrimPrefix(strings.TrimPrefix(name, "#"), "minecraft:")] {
						ic.items[m] = true
					}
					return
				}
				ic.items[strings.TrimPrefix(name, "minecraft:")] = true
			}
			switch it := v.(type) {
			case string:
				addItem(it)
			case []any:
				for _, e := range it {
					if es, ok := e.(string); ok {
						addItem(es)
					}
				}
			}
		}
	}
	for k := range cm {
		if k != "items" {
			ic.impossible = true
		}
	}
	return ic
}

// parseConsumeCond reduces a consume_item criterion to its item name
// set; an absent conditions block means any consumed item matches.
func parseConsumeCond(cname string, conds any) consumeCond {
	cc := consumeCond{crit: cname, items: map[string]bool{}}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		if k != "item" {
			cc.impossible = true
			continue
		}
		im, _ := v.(map[string]any)
		if im == nil {
			cc.impossible = true
			continue
		}
		for ik, iv := range im {
			if ik != "items" {
				cc.impossible = true
				continue
			}
			addItem := func(name string) {
				if strings.HasPrefix(name, "#") {
					for _, m := range data.ItemTags[strings.TrimPrefix(strings.TrimPrefix(name, "#"), "minecraft:")] {
						cc.items[m] = true
					}
					return
				}
				cc.items[strings.TrimPrefix(name, "minecraft:")] = true
			}
			switch it := iv.(type) {
			case string:
				addItem(it)
			case []any:
				for _, e := range it {
					if es, ok := e.(string); ok {
						addItem(es)
					}
				}
			}
		}
	}
	return cc
}

// fireConsumeItem grants consume_item criteria for one finished food.
func (s *Server) fireConsumeItem(sess *Session, item string) {
	for _, meta := range gameAdvs {
		for _, cc := range meta.consumed {
			if cc.impossible {
				continue
			}
			if len(cc.items) > 0 && !cc.items[item] {
				continue
			}
			sess.grantGameCriterion(meta, cc.crit)
		}
	}
}

// parsePlacedCond reduces a placed_block criterion to the block name
// of its first location term; nested predicates and property checks
// are unmodeled, so criteria using them lock.
func parsePlacedCond(cname string, conds any) placedCond {
	pc := placedCond{crit: cname}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		if k != "location" {
			pc.impossible = true
			continue
		}
		terms, _ := v.([]any)
		if len(terms) != 1 {
			pc.impossible = true
			continue
		}
		tm, _ := terms[0].(map[string]any)
		if tm == nil {
			pc.impossible = true
			continue
		}
		if c, _ := tm["condition"].(string); c != "minecraft:block_state_property" {
			pc.impossible = true
			continue
		}
		for tk, tv := range tm {
			switch tk {
			case "condition":
			case "block":
				if bs, ok := tv.(string); ok {
					pc.block = strings.TrimPrefix(bs, "minecraft:")
				}
			default:
				pc.impossible = true
			}
		}
	}
	if pc.block == "" {
		pc.impossible = true
	}
	return pc
}

// firePlacedBlock grants placed_block criteria for one placed block.
func (s *Server) firePlacedBlock(sess *Session, blockName string) {
	for _, meta := range gameAdvs {
		for _, pc := range meta.placed {
			if pc.impossible || pc.block != blockName {
				continue
			}
			sess.grantGameCriterion(meta, pc.crit)
		}
	}
}

// parseDimCond reads changed_dimension's flat from/to dimension names.
func parseDimCond(cname string, conds any) dimCond {
	dc := dimCond{crit: cname}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		switch k {
		case "from", "to":
			name, ok := v.(string)
			if !ok {
				dc.impossible = true
				continue
			}
			if k == "from" {
				dc.from = name
			} else {
				dc.to = name
			}
		default:
			dc.impossible = true
		}
	}
	return dc
}

// fireChangedDimension grants changed_dimension criteria for one
// completed hop; from/to are bare dimension names.
func (s *Server) fireChangedDimension(sess *Session, from, to string) {
	for _, meta := range gameAdvs {
		for _, dc := range meta.dims {
			if dc.impossible {
				continue
			}
			if dc.from != "" && dc.from != from {
				continue
			}
			if dc.to != "" && dc.to != to {
				continue
			}
			sess.grantGameCriterion(meta, dc.crit)
		}
	}
}

// parseUsedOnCond reduces item_used_on_block to a (block set, tool
// set) pair gathered from the location term list: location_check terms
// contribute the block's name/tag set (state and biome keys lock the
// criterion) and match_tool terms contribute the used item's set.
func parseUsedOnCond(cname string, conds any) usedOnCond {
	uc := usedOnCond{crit: cname, blocks: map[string]bool{}, toolItems: map[string]bool{}}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		if k != "location" {
			uc.impossible = true
			continue
		}
		terms, _ := v.([]any)
		for _, tv := range terms {
			tm, _ := tv.(map[string]any)
			if tm == nil {
				uc.impossible = true
				continue
			}
			cond, _ := tm["condition"].(string)
			pred, _ := tm["predicate"].(map[string]any)
			switch cond {
			case "minecraft:location_check":
				for pk, pv := range pred {
					switch pk {
					case "block":
						bm, _ := pv.(map[string]any)
						for bk, bv := range bm {
							if bk != "blocks" {
								uc.impossible = true
								continue
							}
							addUsedOnBlocks(uc.blocks, bv)
						}
					default:
						uc.impossible = true
					}
				}
			case "minecraft:match_tool":
				for pk, pv := range pred {
					switch pk {
					case "items":
						addUsedOnBlocks(uc.toolItems, pv)
					default:
						uc.impossible = true
					}
				}
			default:
				uc.impossible = true
			}
		}
	}
	if len(uc.blocks) == 0 || len(uc.toolItems) == 0 {
		uc.impossible = true
	}
	return uc
}

// addUsedOnBlocks expands a name | list | #tag entry into the set.
func addUsedOnBlocks(dst map[string]bool, v any) {
	switch it := v.(type) {
	case string:
		if strings.HasPrefix(it, "#") {
			tag := strings.TrimPrefix(strings.TrimPrefix(it, "#"), "minecraft:")
			for _, m := range data.BlockTags[tag] {
				dst[m] = true
			}
			return
		}
		dst[strings.TrimPrefix(it, "minecraft:")] = true
	case []any:
		for _, e := range it {
			if es, ok := e.(string); ok {
				dst[strings.TrimPrefix(es, "minecraft:")] = true
			}
		}
	}
}

// fireItemUsedOnBlock grants item_used_on_block criteria for one
// successful use-on-block interaction: the clicked block's name and
// the used item's name must each satisfy their sets.
func (s *Server) fireItemUsedOnBlock(sess *Session, blockName, itemName string) {
	for _, meta := range gameAdvs {
		for _, uc := range meta.usedOn {
			if uc.impossible || !uc.blocks[blockName] || !uc.toolItems[itemName] {
				continue
			}
			sess.grantGameCriterion(meta, uc.crit)
		}
	}
}

// parseTravelCond reduces nether_travel to distance.horizontal.min;
// any other key locks the criterion.
func parseTravelCond(cname string, conds any) travelCond {
	tc := travelCond{crit: cname}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		if k != "distance" {
			tc.impossible = true
			continue
		}
		dm, _ := v.(map[string]any)
		for dk, dv := range dm {
			if dk != "horizontal" {
				tc.impossible = true
				continue
			}
			hm, _ := dv.(map[string]any)
			for hk, hv := range hm {
				if hk != "min" {
					tc.impossible = true
					continue
				}
				if f, ok := hv.(float64); ok {
					tc.minDist = f
				}
			}
		}
	}
	return tc
}

// fireNetherTravel evaluates nether_travel criteria on the return leg:
// distSq is the horizontal distance squared between the overworld
// portal used to leave and the one the player reappeared at.
func (s *Server) fireNetherTravel(sess *Session, distSq float64) {
	for _, meta := range gameAdvs {
		for _, tc := range meta.travel {
			if tc.impossible || distSq < tc.minDist*tc.minDist {
				continue
			}
			sess.grantGameCriterion(meta, tc.crit)
		}
	}
}

// parseSummonCond reduces summoned_entity's conditions.entity to the
// summoned entity's type name.
func parseSummonCond(cname string, conds any) summonCond {
	sc := summonCond{crit: cname}
	cm, _ := conds.(map[string]any)
	for k, v := range cm {
		if k != "entity" {
			sc.impossible = true
			continue
		}
		ents, _ := v.([]any)
		for _, ev := range ents {
			em, _ := ev.(map[string]any)
			if c, _ := em["condition"].(string); c != "minecraft:entity_properties" {
				sc.impossible = true
				continue
			}
			pred, _ := em["predicate"].(map[string]any)
			for pk, pv := range pred {
				if pk != "minecraft:entity_type" {
					sc.impossible = true
					continue
				}
				if s, ok := pv.(string); ok {
					sc.entityType = strings.TrimPrefix(s, "minecraft:")
				}
			}
		}
	}
	if sc.entityType == "" {
		sc.impossible = true
	}
	return sc
}

// fireSummonedEntity grants summoned_entity criteria for one
// constructed entity spawn (iron golem from the pumpkin T today).
func (s *Server) fireSummonedEntity(sess *Session, entityType string) {
	for _, meta := range gameAdvs {
		for _, sc := range meta.summons {
			if sc.impossible || sc.entityType != entityType {
				continue
			}
			sess.grantGameCriterion(meta, sc.crit)
		}
	}
}

// fireGameInventoryChanged evaluates every game-tree inventory_changed
// criterion against the session's current inventory — the same shape
// the recipe-tree listener in advancements.go uses.
func (s *Server) fireGameInventoryChanged(sess *Session) {
	sess.invMu.RLock()
	inv := append([]ItemStack(nil), sess.inv[:]...)
	sess.invMu.RUnlock()
	items := map[string]bool{}
	for _, it := range inv {
		if it.Empty() {
			continue
		}
		if n, ok := world.ItemName(it.ID); ok {
			items[n] = true
		}
	}
	for _, meta := range gameAdvs {
		for _, ic := range meta.inv {
			if ic.impossible {
				continue
			}
			for name := range ic.items {
				if items[name] {
					sess.grantGameCriterion(meta, ic.crit)
					break
				}
			}
		}
	}
}

// fireEntityKilledPlayer is firePlayerKilledEntity for the victim's
// side of a death: entity_killed_player conditions describe the killer
// (killerType is its entity name, (kx,kz) vs (vx,vz) the distance).
func (s *Server) fireEntityKilledPlayer(sess *Session, killerType string, vx, vz, kx, kz float64) {
	dx, dz := kx-vx, kz-vz
	distSq := dx*dx + dz*dz
	for _, meta := range gameAdvs {
		for _, kc := range meta.deaths {
			if kc.impossible || kc.needsProj || kc.direct != nil {
				continue
			}
			if kc.types != nil && !kc.types[killerType] {
				continue
			}
			if kc.minDist > 0 && distSq < kc.minDist*kc.minDist {
				continue
			}
			sess.grantGameCriterion(meta, kc.crit)
		}
	}
}

// grantGameCriterion is grantAdvCriterion for the gameplay tree — same
// semantics: mark criterion, recompute done over requirement groups,
// queue the advancement for the dirty flush.
func (sess *Session) grantGameCriterion(meta *advMeta, cname string) {
	sess.grantGameCriterionChanged(meta, cname)
}

// grantGameCriterionChanged is grantGameCriterion reporting whether the
// criterion moved — the /advancement command counts its targets on that.
func (sess *Session) grantGameCriterionChanged(meta *advMeta, cname string) bool {
	return sess.grantCriterion(meta, cname, true)
}

// grantCriterion marks one criterion obtained. skipDone gates the
// trigger path (a finished advancement stops listening); the
// /advancement command passes false since vanilla's award("*") obtains
// every criterion even when the advancement is already done.
func (sess *Session) grantCriterion(meta *advMeta, cname string, skipDone bool) bool {
	sess.advMu.Lock()
	if sess.advDone == nil {
		sess.advDone = map[string]bool{}
	}
	if skipDone && sess.advDone[meta.id] {
		sess.advMu.Unlock()
		return false
	}
	if sess.advCrits == nil {
		sess.advCrits = map[string]map[string]int64{}
	}
	if sess.advCrits[meta.id] == nil {
		sess.advCrits[meta.id] = map[string]int64{}
	}
	if _, done := sess.advCrits[meta.id][cname]; done {
		sess.advMu.Unlock()
		return false
	}
	sess.advCrits[meta.id][cname] = time.Now().UnixMilli()
	wasDone := sess.advDone[meta.id]
	complete := len(meta.reqs) > 0
	for _, group := range meta.reqs {
		met := false
		for _, c := range group {
			if _, ok := sess.advCrits[meta.id][c]; ok {
				met = true
				break
			}
		}
		if !met {
			complete = false
			break
		}
	}
	if complete {
		sess.advDone[meta.id] = true
	}
	// announce only on the transition into done — a done advancement's
	// remaining criteria (granted via /advancement ... "*") are silent.
	if sess.advDirty == nil {
		sess.advDirty = map[string]bool{}
	}
	if !sess.advDirty[meta.id] {
		sess.advDirty[meta.id] = true
		sess.advDirtyOrder = append(sess.advDirtyOrder, meta.id)
	}
	sess.advMu.Unlock()
	if complete && !wasDone {
		sess.server.announceAdvancement(sess, meta)
	}
	return true
}

// advChatComponent renders the advancement's display name the way
// vanilla's Advancement.getChatComponent does — square brackets in the
// frame color over a title hover showing title, newline, description.
func advChatComponent(meta *advMeta) component {
	color := advFrameColor(meta.frame)
	descKey := meta.descKey
	if descKey == "" {
		descKey = strings.TrimSuffix(meta.titleKey, ".title") + ".description"
	}
	return component{
		translate: "chat.square_brackets",
		color:     color,
		with: []any{component{
			translate: meta.titleKey,
			hover: map[string]any{
				"action": "show_text",
				"value": component{
					translate: meta.titleKey,
					color:     color,
					extra: []component{
						{emptyKey: "\n"},
						{translate: descKey},
					},
				}.nested(),
			},
		}},
	}
}

// playerChatComponent is the player's name component vanilla puts in
// command replies and announce chat: insertion, /tell click, and a
// show_entity hover with the UUID as int32[4].
func playerChatComponent(sess *Session) component {
	name := sess.getName()
	u := sess.getUUID()
	return component{
		text:      name,
		insertion: name,
		click:     "/tell " + name + " ",
		hover: map[string]any{
			"name":   name,
			"action": "show_entity",
			"id":     "minecraft:player",
			"uuid": []int32{
				int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3]),
				int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7]),
				int32(u[8])<<24 | int32(u[9])<<16 | int32(u[10])<<8 | int32(u[11]),
				int32(u[12])<<24 | int32(u[13])<<16 | int32(u[14])<<8 | int32(u[15]),
			},
		},
	}
}

// announceAdvancement broadcasts vanilla's "X has made the advancement"
// chat for a completed gameplay advancement — measured wire shape:
// chat.type.advancement.<frame> with the player's display-name component
// (insertion + suggest_command click + show_entity hover) and the
// advancement name as chat.square_brackets in the frame color, hovering
// title + \n + description.
func (s *Server) announceAdvancement(sess *Session, meta *advMeta) {
	if meta.titleKey == "" || !meta.hasDisplay || !meta.announce {
		return
	}
	playerName := playerChatComponent(sess)
	advName := advChatComponent(meta)
	c := component{
		translate: "chat.type.advancement." + meta.frame,
		with:      []any{playerName, advName},
	}
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() == StatePlay {
			s.sendSystemChatComponent(other, c)
		}
		return true
	})
}

func advFrameColor(frame string) string {
	if frame == "challenge" {
		return "dark_purple"
	}
	return "green"
}

// computeAdvVisibility runs vanilla's AdvancementVisibilityEvaluator
// over the whole tree. An advancement is visible when it is done, when
// any descendant is visible (the result bubbles up the parents), or —
// for an unfinished node — when the nearest rule-bearing node in the
// ≤3-deep stack of self+ancestors says SHOW (a done parent lights its
// children and grandchildren; a hidden or display-less ancestor blocks).
func computeAdvVisibility(done map[string]bool) map[string]bool {
	vis := map[string]bool{}
	var walk func(id string, rules []int) bool
	walk = func(id string, rules []int) bool {
		m := gameAdvs[id]
		if m == nil {
			return false
		}
		d := done[id]
		rule := advRuleNoChange
		switch {
		case !m.hasDisplay:
			rule = advRuleHide
		case d:
			rule = advRuleShow
		case m.hidden:
			rule = advRuleHide
		}
		r2 := append(append([]int{}, rules...), rule)
		res := d
		for _, ch := range gameAdvChild[id] {
			if walk(ch, r2) {
				res = true
			}
		}
		childRes := res
		if !res {
			// Unfinished node: peek self then parent then grandparent.
			for depth := 0; depth < 3 && depth < len(r2); depth++ {
				switch r2[len(r2)-1-depth] {
				case advRuleShow:
					res = true
				case advRuleHide:
					res = false
				default:
					continue
				}
				break
			}
		}
		if res {
			vis[id] = true
		}
		// The recursion's parent-facing result counts only obtained
		// or obtained-descendant nodes: visibility inherited through
		// a rule does not bubble further up (javap: evaluateVisibility
		// returns the pre-rule value).
		return childRes
	}
	for _, root := range gameAdvRoots {
		walk(root, nil)
	}
	return vis
}

const (
	advRuleNoChange = iota
	advRuleShow
	advRuleHide
)

// onPlayerKilledMob fires player_killed_entity for a mob the player
// just killed (direct attack or sweep splash).
func (s *Server) onPlayerKilledMob(sess *Session, e Entity) {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return
	}
	ax, _, az, _, _, _ := sess.Pos()
	s.firePlayerKilledEntity(sess, name, e.X, e.Z, ax, az)
}
