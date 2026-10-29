package server

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// An unmodeled valid predicate is not false: inversion must not turn it into
// an award. AND/OR retain definite answers when another operand determines
// the result, otherwise propagate the unevaluated state.
type advancementPredicateResult int8

const (
	advancementPredicateUnknown advancementPredicateResult = -1
	advancementPredicateFalse   advancementPredicateResult = 0
	advancementPredicateTrue    advancementPredicateResult = 1
)

type advancementPlayerPredicate func(*Server, *Session) advancementPredicateResult

func advancementPredicateConstant(value advancementPredicateResult) advancementPlayerPredicate {
	return func(*Server, *Session) advancementPredicateResult { return value }
}
func advancementPredicateBool(value bool) advancementPredicateResult {
	if value {
		return advancementPredicateTrue
	}
	return advancementPredicateFalse
}
func advancementPredicateAll(parts []advancementPlayerPredicate) advancementPlayerPredicate {
	return func(s *Server, p *Session) advancementPredicateResult {
		unknown := false
		for _, part := range parts {
			switch part(s, p) {
			case advancementPredicateFalse:
				return advancementPredicateFalse
			case advancementPredicateUnknown:
				unknown = true
			}
		}
		if unknown {
			return advancementPredicateUnknown
		}
		return advancementPredicateTrue
	}
}
func advancementPredicateAny(parts []advancementPlayerPredicate) advancementPlayerPredicate {
	return func(s *Server, p *Session) advancementPredicateResult {
		unknown := false
		for _, part := range parts {
			switch part(s, p) {
			case advancementPredicateTrue:
				return advancementPredicateTrue
			case advancementPredicateUnknown:
				unknown = true
			}
		}
		if unknown {
			return advancementPredicateUnknown
		}
		return advancementPredicateFalse
	}
}

// Factual codec registry names measured from EntitySubPredicates and
// LootItemConditions in the original26.2 jar; implementations below are local.
func advancementPredicateNames(names string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(names) {
		out["minecraft:"+name] = true
	}
	return out
}

var advancementEntityPredicateNames = advancementPredicateNames(`entity_type location stepping_on movement_affected_by distance movement effects nbt flags equipment periodic_tick vehicle passenger targeted_entity team slots components predicates entity_tags type_specific/lightning type_specific/fishing_hook type_specific/player type_specific/cube_mob type_specific/raider type_specific/sheep`)
var advancementConditionNames = advancementPredicateNames(`inverted any_of all_of random_chance random_chance_with_enchanted_bonus entity_properties killed_by_player entity_scores block_state_property match_tool table_bonus survives_explosion damage_source_properties location_check weather_check reference time_check value_check enchantment_active_check environment_attribute_check`)

func compileAdvancementTickPredicate(raw any) (advancementPlayerPredicate, error) {
	if unconditionalTickCriterion(raw) {
		return advancementPredicateConstant(advancementPredicateTrue), nil
	}
	conditions, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tick conditions must be an object")
	}
	player := conditions["player"]
	switch player := player.(type) {
	case map[string]any:
		return compileAdvancementEntityPredicate(player, 0)
	case []any:
		var parts []advancementPlayerPredicate
		for _, value := range player {
			part, err := compileAdvancementContextCondition(value, 0)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return advancementPredicateAll(parts), nil
	}
	return nil, fmt.Errorf("invalid tick player predicate")
}

func compileAdvancementContextCondition(raw any, depth int) (advancementPlayerPredicate, error) {
	if depth > 64 {
		return nil, fmt.Errorf("predicate nesting too deep")
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("condition must be an object")
	}
	name, ok := object["condition"].(string)
	if !ok || !advancementConditionNames[normalizeID(name)] {
		return nil, fmt.Errorf("unknown advancement condition")
	}
	switch normalizeID(name) {
	case "minecraft:entity_properties":
		actor, ok := object["entity"].(string)
		if !ok {
			return nil, fmt.Errorf("missing entity target")
		}
		switch actor {
		case "this", "attacker", "direct_attacker", "attacking_player", "target_entity", "interacting_entity":
		default:
			return nil, fmt.Errorf("invalid entity target")
		}
		if raw, exists := object["predicate"]; !exists || raw == nil {
			// An omitted optional entity predicate succeeds even when the named
			// context actor is absent; an explicit predicate still needs an actor.
			return advancementPredicateConstant(advancementPredicateTrue), nil
		}
		predicate := map[string]any{}
		if raw, exists := object["predicate"]; exists && raw != nil {
			var ok bool
			predicate, ok = raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid entity predicate")
			}
		}
		part, err := compileAdvancementEntityPredicate(predicate, depth+1)
		if err != nil {
			return nil, err
		}
		if actor != "this" {
			return advancementPredicateConstant(advancementPredicateFalse), nil
		}
		return part, nil
	case "minecraft:inverted":
		part, err := compileAdvancementContextCondition(object["term"], depth+1)
		if err != nil {
			return nil, err
		}
		return func(s *Server, p *Session) advancementPredicateResult {
			value := part(s, p)
			if value == advancementPredicateUnknown {
				return value
			}
			return advancementPredicateBool(value == advancementPredicateFalse)
		}, nil
	case "minecraft:any_of", "minecraft:all_of":
		terms, ok := object["terms"].([]any)
		if !ok {
			return nil, fmt.Errorf("invalid condition terms")
		}
		var parts []advancementPlayerPredicate
		for _, term := range terms {
			part, err := compileAdvancementContextCondition(term, depth+1)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		if normalizeID(name) == "minecraft:any_of" {
			return advancementPredicateAny(parts), nil
		}
		return advancementPredicateAll(parts), nil
	case "minecraft:random_chance":
		chance, ok := advancementJSONFloat(object["chance"])
		if !ok {
			if _, isObject := object["chance"].(map[string]any); isObject {
				return advancementPredicateConstant(advancementPredicateUnknown), nil
			}
			return nil, fmt.Errorf("invalid random chance provider")
		}
		return func(*Server, *Session) advancementPredicateResult {
			return advancementPredicateBool(rand.Float64() < chance)
		}, nil
	}
	return advancementPredicateConstant(advancementPredicateUnknown), nil
}

func compileAdvancementEntityPredicate(object map[string]any, depth int) (advancementPlayerPredicate, error) {
	if depth > 64 {
		return nil, fmt.Errorf("predicate nesting too deep")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []advancementPlayerPredicate
	for _, key := range keys {
		name := normalizeID(key)
		if !advancementEntityPredicateNames[name] {
			return nil, fmt.Errorf("unknown entity predicate %s", key)
		}
		var part advancementPlayerPredicate
		var err error
		switch name {
		case "minecraft:entity_type":
			var matches func(string) bool
			matches, err = compileAdvancementHolderSet(object[key], true)
			part = func(*Server, *Session) advancementPredicateResult { return advancementPredicateBool(matches("player")) }
		case "minecraft:type_specific/player":
			part, err = compileAdvancementPlayerTypePredicate(object[key])
		case "minecraft:flags":
			part, err = compileAdvancementFlagsPredicate(object[key])
		case "minecraft:location":
			part, err = compileAdvancementLocationPredicate(object[key])
		case "minecraft:nbt":
			text, ok := object[key].(string)
			if !ok {
				return nil, fmt.Errorf("invalid entity NBT predicate")
			}
			tag, end, parseErr := parseSNBTTag(text, 0)
			if parseErr != nil || tag == nil || end != len(text) || tag.kind != snbtCompound {
				return nil, fmt.Errorf("invalid entity NBT predicate")
			}
			pattern := snbtToValue(tag).(map[string]any)
			part = func(s *Server, p *Session) advancementPredicateResult {
				return advancementPredicateBool((&dataEntity{sess: p}).nbtMatches(pattern, s.clock.count, s.entities))
			}
		case "minecraft:equipment":
			part, err = compileAdvancementEquipmentPredicate(object[key])
		default:
			part = advancementPredicateConstant(advancementPredicateUnknown)
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return advancementPredicateAll(parts), nil
}

func compileAdvancementHolderSet(raw any, entity bool) (func(string) bool, error) {
	var values []string
	switch value := raw.(type) {
	case string:
		values = []string{value}
	case []any:
		for _, item := range value {
			v, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("invalid holder set")
			}
			values = append(values, v)
		}
	default:
		return nil, fmt.Errorf("invalid holder set")
	}
	ids := map[string]bool{}
	for _, value := range values {
		if strings.HasPrefix(value, "#") {
			tag := strings.TrimPrefix(strings.TrimPrefix(value, "#"), "minecraft:")
			members, ok := data.ItemTags[tag]
			if entity {
				members, ok = data.EntityTypeTags[tag]
			}
			if !ok {
				return nil, fmt.Errorf("unknown holder tag")
			}
			for _, member := range members {
				ids[strings.TrimPrefix(member, "minecraft:")] = true
			}
			continue
		}
		name := strings.TrimPrefix(value, "minecraft:")
		valid := false
		if entity {
			_, valid = EntityTypeID(name)
		} else {
			_, valid = world.ItemID(name)
		}
		if !valid {
			return nil, fmt.Errorf("unknown holder %s", value)
		}
		ids[name] = true
	}
	return func(name string) bool { return ids[strings.TrimPrefix(name, "minecraft:")] }, nil
}

func compileAdvancementRange(raw any, integer bool) (func(float64) bool, error) {
	low, high := math.Inf(-1), math.Inf(1)
	number := func(raw any) (float64, bool) {
		if integer {
			value, err := advancementJSONInt(raw)
			return float64(value), err == nil
		}
		return advancementJSONFloat(raw)
	}
	switch value := raw.(type) {
	case float64, json.Number:
		converted, ok := number(value)
		if !ok {
			return nil, fmt.Errorf("invalid numeric bound")
		}
		low, high = converted, converted
	case map[string]any:
		if raw, exists := value["min"]; exists && raw != nil {
			var ok bool
			low, ok = number(raw)
			if !ok {
				return nil, fmt.Errorf("invalid lower bound")
			}
		}
		if raw, exists := value["max"]; exists && raw != nil {
			var ok bool
			high, ok = number(raw)
			if !ok {
				return nil, fmt.Errorf("invalid upper bound")
			}
		}
	default:
		return nil, fmt.Errorf("invalid range")
	}
	if low > high {
		return nil, fmt.Errorf("reversed range")
	}
	return func(value float64) bool { return value >= low && value <= high }, nil
}

func compileAdvancementPlayerTypePredicate(raw any) (advancementPlayerPredicate, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid player predicate")
	}
	var parts []advancementPlayerPredicate
	for key, value := range object {
		if value == nil {
			continue
		}
		switch key {
		case "gamemode":
			modes, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("gamemode predicate must be a list")
			}
			allowed := map[string]bool{}
			for _, raw := range modes {
				mode, ok := raw.(string)
				if !ok || !(mode == "survival" || mode == "creative" || mode == "adventure" || mode == "spectator") {
					return nil, fmt.Errorf("invalid predicate gamemode")
				}
				allowed[mode] = true
			}
			parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
				return advancementPredicateBool(allowed[gamemodeNameOf(p)])
			})
		case "advancements":
			part, err := compileAdvancementProgressPredicate(value)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "level":
			test, err := compileAdvancementRange(value, true)
			if err != nil {
				return nil, err
			}
			parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
				return advancementPredicateBool(test(float64(p.xpLevel)))
			})
		default:
			parts = append(parts, advancementPredicateConstant(advancementPredicateUnknown))
		}
	}
	return advancementPredicateAll(parts), nil
}

// Identifier.CODEC accepts an empty path and defaults an empty namespace;
// reference paths retain dot segments rather than becoming filesystem paths.
func advancementReferenceID(raw string) (string, error) {
	name, ok := jigsawIdentifier(raw)
	if !ok {
		return "", fmt.Errorf("invalid advancement reference")
	}
	return name, nil
}

func compileAdvancementProgressPredicate(raw any) (advancementPlayerPredicate, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid advancement progress map")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []advancementPlayerPredicate
	for _, key := range keys {
		id, err := advancementReferenceID(key)
		if err != nil {
			return nil, err
		}
		query := selAdv{id: id}
		switch state := object[key].(type) {
		case bool:
			want := state
			query.done = &want
		case map[string]any:
			names := make([]string, 0, len(state))
			for name := range state {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				want, ok := state[name].(bool)
				if !ok {
					return nil, fmt.Errorf("invalid advancement criterion state")
				}
				query.crits = append(query.crits, selTerm{val: name, neg: !want})
			}
		default:
			return nil, fmt.Errorf("invalid advancement completion state")
		}
		parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
			return advancementPredicateBool(p.advMatch(query))
		})
	}
	return advancementPredicateAll(parts), nil
}

func compileAdvancementFlagsPredicate(raw any) (advancementPlayerPredicate, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid flags predicate")
	}
	var parts []advancementPlayerPredicate
	for key, value := range object {
		if value == nil {
			continue
		}
		want, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid predicate flag")
		}
		flag := key
		parts = append(parts, func(s *Server, p *Session) advancementPredicateResult {
			var got bool
			switch flag {
			case "is_on_ground":
				_, _, _, _, _, got = p.Pos()
			case "is_on_fire":
				got = p.fireTicks > 0
			case "is_sneaking":
				if s.entities == nil {
					return advancementPredicateUnknown
				}
				entity, ok := s.entities.GetPtr(p.entityID.Load())
				if !ok {
					return advancementPredicateUnknown
				}
				got = entity.Pose == 5
			case "is_sprinting":
				got = p.sprinting
			case "is_swimming":
				got = p.swimming
			case "is_flying":
				got = p.flying || p.fallFlying
			case "is_fall_flying":
				got = p.fallFlying
			case "is_baby":
				got = false
			default:
				return advancementPredicateUnknown
			}
			return advancementPredicateBool(got == want)
		})
	}
	return advancementPredicateAll(parts), nil
}

func compileAdvancementLocationPredicate(raw any) (advancementPlayerPredicate, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid location predicate")
	}
	var parts []advancementPlayerPredicate
	for key, value := range object {
		if value == nil {
			continue
		}
		switch key {
		case "dimension":
			name, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid predicate dimension")
			}
			name = normalizeID(name)
			parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
				return advancementPredicateBool(p.dimName() == name)
			})
		case "biomes":
			names := map[string]bool{}
			var tags []string
			add := func(raw any) bool {
				name, ok := raw.(string)
				if !ok {
					return false
				}
				if strings.HasPrefix(name, "#") {
					tags = append(tags, strings.TrimPrefix(strings.TrimPrefix(name, "#"), "minecraft:"))
				} else {
					names[normalizeID(name)] = true
				}
				return true
			}
			switch raw := value.(type) {
			case string:
				if !add(raw) {
					return nil, fmt.Errorf("invalid predicate biome")
				}
			case []any:
				for _, name := range raw {
					if !add(name) {
						return nil, fmt.Errorf("invalid predicate biome")
					}
				}
			default:
				return nil, fmt.Errorf("invalid predicate biome")
			}
			parts = append(parts, func(s *Server, p *Session) advancementPredicateResult {
				dimension := p.dimension()
				if dimension == nil {
					return advancementPredicateUnknown
				}
				x, y, z, _, _, _ := p.Pos()
				biome := s.spawnBiomeAt(dimension, world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))})
				if names[normalizeID(biome)] {
					return advancementPredicateTrue
				}
				for _, tag := range tags {
					if s.biomeHasTag(tag, biome) {
						return advancementPredicateTrue
					}
				}
				return advancementPredicateFalse
			})
		case "position":
			axes, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid predicate position")
			}
			for axis, bounds := range axes {
				if bounds == nil {
					continue
				}
				index := map[string]int{"x": 0, "y": 1, "z": 2}
				i, ok := index[axis]
				if !ok {
					parts = append(parts, advancementPredicateConstant(advancementPredicateUnknown))
					continue
				}
				test, err := compileAdvancementRange(bounds, false)
				if err != nil {
					return nil, err
				}
				parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
					x, y, z, _, _, _ := p.Pos()
					return advancementPredicateBool(test([3]float64{x, y, z}[i]))
				})
			}
		default:
			parts = append(parts, advancementPredicateConstant(advancementPredicateUnknown))
		}
	}
	return advancementPredicateAll(parts), nil
}

func compileAdvancementEquipmentPredicate(raw any) (advancementPlayerPredicate, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid equipment predicate")
	}
	slots := map[string]int32{"mainhand": itemSlotMainhand, "offhand": itemSlotOffhand, "head": itemSlotHead, "chest": itemSlotChest, "legs": itemSlotLegs, "feet": itemSlotFeet, "body": itemSlotBody}
	var parts []advancementPlayerPredicate
	for slot, raw := range object {
		if raw == nil {
			continue
		}
		id, ok := slots[slot]
		if !ok {
			parts = append(parts, advancementPredicateConstant(advancementPredicateUnknown))
			continue
		}
		predicate, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid equipment item predicate")
		}
		var matchID func(string) bool
		var count func(float64) bool
		unsupported := false
		for key, value := range predicate {
			if value == nil {
				continue
			}
			var err error
			switch key {
			case "items":
				matchID, err = compileAdvancementHolderSet(value, false)
			case "count":
				count, err = compileAdvancementRange(value, true)
			default:
				unsupported = true
			}
			if err != nil {
				return nil, err
			}
		}
		parts = append(parts, func(_ *Server, p *Session) advancementPredicateResult {
			stack, ok := readEntitySlot(p, id)
			if !ok || !itemPredicateMatch(itemPredicate{}, stack) {
				return advancementPredicateFalse
			}
			name, _ := world.ItemName(stack.ID)
			if matchID != nil && !matchID(name) || count != nil && !count(float64(stack.Count)) {
				return advancementPredicateFalse
			}
			if unsupported {
				return advancementPredicateUnknown
			}
			return advancementPredicateTrue
		})
	}
	return advancementPredicateAll(parts), nil
}

func prepareAdvancementTickPredicates(registry *advancementRegistry) {
	registry.tickers = map[string]map[string]advancementPlayerPredicate{}
	for id := range registry.custom {
		meta := registry.entries[id]
		criteria, _ := meta.raw["criteria"].(map[string]any)
		for name, value := range criteria {
			criterion, _ := value.(map[string]any)
			trigger, _ := criterion["trigger"].(string)
			if normalizeID(trigger) != "minecraft:tick" {
				continue
			}
			predicate, err := compileAdvancementTickPredicate(criterion["conditions"])
			if err != nil {
				continue
			}
			if registry.tickers[id] == nil {
				registry.tickers[id] = map[string]advancementPlayerPredicate{}
			}
			registry.tickers[id][name] = predicate
		}
	}
}
