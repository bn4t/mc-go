package server

import (
	"strings"
	"time"

	"mc-go/internal/data"
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
	deaths     []killCond // entity_killed_player — conditions apply to the killer
	locs       []locCond  // location — only biome checks are modeled
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
	dx, dz := vx-ax, vz-az
	distSq := dx*dx + dz*dz
	for _, meta := range gameAdvs {
		for _, kc := range meta.kills {
			if kc.impossible || kc.needsProj || kc.direct != nil {
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

// fireLocation runs the location trigger, which vanilla ticks per
// player every 20 ticks (ServerPlayer tickCount % 20). The flat
// overworld is plains everywhere; other dimensions have no modeled
// biomes, so nothing matches off the overworld.
func (s *Server) fireLocation(sess *Session) {
	if sess.dimName() != "minecraft:overworld" {
		return
	}
	for _, meta := range gameAdvs {
		for _, lc := range meta.locs {
			if lc.impossible || !lc.biomes["plains"] {
				continue
			}
			sess.grantGameCriterion(meta, lc.crit)
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
	sess.advMu.Lock()
	if sess.advDone == nil {
		sess.advDone = map[string]bool{}
	}
	if sess.advDone[meta.id] {
		sess.advMu.Unlock()
		return
	}
	if sess.advCrits == nil {
		sess.advCrits = map[string]map[string]int64{}
	}
	if sess.advCrits[meta.id] == nil {
		sess.advCrits[meta.id] = map[string]int64{}
	}
	if _, done := sess.advCrits[meta.id][cname]; done {
		sess.advMu.Unlock()
		return
	}
	sess.advCrits[meta.id][cname] = time.Now().UnixMilli()
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
	if sess.advDirty == nil {
		sess.advDirty = map[string]bool{}
	}
	if !sess.advDirty[meta.id] {
		sess.advDirty[meta.id] = true
		sess.advDirtyOrder = append(sess.advDirtyOrder, meta.id)
	}
	sess.advMu.Unlock()
	if complete {
		sess.server.announceAdvancement(sess, meta)
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
	name := sess.getName()
	u := sess.getUUID()
	color := advFrameColor(meta.frame)
	descKey := meta.descKey
	if descKey == "" {
		descKey = strings.TrimSuffix(meta.titleKey, ".title") + ".description"
	}
	playerName := component{
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
	advName := component{
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
		return res
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
