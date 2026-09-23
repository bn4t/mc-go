package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// attrMod is one /attribute modifier instance: amount plus operation.
type attrMod struct {
	value float64
	op    int // 0 add_value, 1 add_multiplied_base, 2 add_multiplied_total
}

// playerAttrBases are the default attribute values vanilla's
// DefaultAttributes.player registers; a missing entry means the
// attribute does not exist on a player at all.
var playerAttrBases = map[string]float64{
	"armor":                          0,
	"armor_toughness":                0,
	"attack_damage":                  1,
	"attack_knockback":               0,
	"attack_speed":                   4,
	"block_break_speed":              1,
	"block_interaction_range":        4.5,
	"burning_time":                   1,
	"camera_distance":                4,
	"entity_interaction_range":       3,
	"explosion_knockback_resistance": 0,
	"fall_damage_multiplier":         1,
	"flying_speed":                   0.02,
	"gravity":                        0.08,
	"jump_strength":                  0.41999998688697815,
	"knockback_resistance":           0,
	"luck":                           0,
	"max_absorption":                 0,
	"max_health":                     20,
	"mining_efficiency":              0,
	"movement_efficiency":            0,
	"movement_speed":                 0.10000000149011612,
	"oxygen_bonus":                   0,
	"safe_fall_distance":             3,
	"scale":                          1,
	"sneaking_speed":                 0.3,
	"step_height":                    0.6,
	"submerged_mining_speed":         0.2,
	"sweeping_damage_ratio":          0,
	"tempt_range":                    10,
	"water_movement_efficiency":      0,
	"waypoint_receive_range":         0,
	"waypoint_transmit_range":        0,
}

// livingAttrBases is vanilla's LivingEntity.createLivingAttributes()
// supplier — every living entity carries it, then a per-type builder
// adds/overrides. Only attributes with a non-zero vanilla default
// carry their value; the rest exist (has=true) at default 0/1.
var livingAttrBases = map[string]float64{
	"max_health":                     20,
	"knockback_resistance":           0,
	"movement_speed":                 0.7,
	"armor":                          0,
	"armor_toughness":                0,
	"max_absorption":                 0,
	"step_height":                    0.6,
	"scale":                          1,
	"gravity":                        0.08,
	"safe_fall_distance":             3,
	"fall_damage_multiplier":         1,
	"jump_strength":                  0.41999998688697815,
	"entity_interaction_range":       3,
	"oxygen_bonus":                   0,
	"water_movement_efficiency":      0,
	"movement_efficiency":            0,
	"burning_time":                   1,
	"explosion_knockback_resistance": 0,
	"bounciness":                     0,
	"name_tag_distance":              64,
	"below_name_distance":            0,
	"camera_distance":                4,
	"waypoint_transmit_range":        0,
	"air_drag_modifier":              0,
}

// mobAttrBases are the per-type attribute tables for the mobs the
// sandbox can summon; vanilla's per-type DefaultAttributes builders.
var mobAttrBases = map[string]map[string]float64{
	"pig":      {"max_health": 10, "movement_speed": 0.25},
	"zombie":   {"max_health": 20, "movement_speed": 0.23000000417232513, "attack_damage": 3, "follow_range": 35, "armor": 2},
	"villager": {"max_health": 20, "movement_speed": 0.5, "follow_range": 48},
}

func (s *Server) registerAdminTwoCommands() {
	s.registerComponentCommand("stopwatch", func(player string, args []string) reply {
		return s.runStopwatch(player, args)
	})
	s.registerComponentCommand("return", func(player string, args []string) reply {
		return s.runReturn(player, args)
	})
	s.registerComponentCommand("schedule", func(player string, args []string) reply {
		return s.runSchedule(player, args)
	})
	s.registerComponentCommand("function", func(player string, args []string) reply {
		return s.runFunction(player, args)
	})
	s.registerComponentCommand("fetchprofile", func(player string, args []string) reply {
		return s.runFetchProfile(player, args)
	})
	s.registerComponentCommand("attribute", func(player string, args []string) reply {
		return s.runAttribute(player, args)
	})
	s.registerComponentCommand("dialog", func(player string, args []string) reply {
		return s.runDialog(player, args)
	})
	s.registerComponentCommand("locate", func(player string, args []string) reply {
		return s.runLocate(player, args)
	})
	s.registerComponentCommand("place", func(player string, args []string) reply {
		return s.runPlace(player, args)
	})
	s.registerComponentCommand("clone", func(player string, args []string) reply {
		return s.runClone(player, args)
	})
	s.registerComponentCommand("loot", func(player string, args []string) reply {
		return s.runLoot(player, args)
	})
}

// normalizeID applies the vanilla resource-location default:
// a name without a namespace is minecraft:<name>.
func normalizeID(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

// ---- stopwatch ----

func (s *Server) runStopwatch(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "create":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if strings.HasPrefix(args[1], `"`) || strings.HasPrefix(args[1], `'`) {
			return errToken("command.expected.separator", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		id := normalizeID(args[1])
		s.stopwatchMu.Lock()
		defer s.stopwatchMu.Unlock()
		if s.stopwatches == nil {
			s.stopwatches = map[string]time.Time{}
		}
		if _, ok := s.stopwatches[id]; ok {
			return componentReply(commandError("commands.stopwatch.already_exists", id))
		}
		s.stopwatches[id] = time.Now()
		return componentReply(translatable("commands.stopwatch.create.success", id))
	case "query", "restart", "remove":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		id := normalizeID(args[1])
		s.stopwatchMu.Lock()
		defer s.stopwatchMu.Unlock()
		start, ok := s.stopwatches[id]
		if !ok {
			return componentReply(commandError("commands.stopwatch.does_not_exist", id))
		}
		switch args[0] {
		case "query":
			return componentReply(translatable("commands.stopwatch.query",
				nestedCompound{key: "", value: id},
				nestedCompound{key: "", value: time.Since(start).Seconds()}))
		case "restart":
			s.stopwatches[id] = time.Now()
			return componentReply(translatable("commands.stopwatch.restart.success", id))
		default:
			delete(s.stopwatches, id)
			return componentReply(translatable("commands.stopwatch.remove.success", id))
		}
	}
	return errToken("command.unknown.argument", 1)
}

// ---- return ----

// runReturn is /return: outside a function it replies nothing; `run`
// forwards the inner command's reply, and a missing or unknown inner
// command reports the vanilla unknown.argument at the inner name.
func (s *Server) runReturn(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	if args[0] == "run" {
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		inner := args[1:]
		r, ok := s.runCommand(player, inner[0], inner[1:])
		if !ok {
			return errToken("command.unknown.argument", 2)
		}
		return r
	}
	if args[0] == "fail" {
		return reply{}
	}
	if _, err := strconv.Atoi(args[0]); err != nil {
		return errToken("parsing.int.expected", 1)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	return reply{}
}

// ---- schedule ----

// runSchedule is /schedule: no functions exist to queue, so a parsed
// call always reports arguments.function.unknown, and /schedule clear
// always finds nothing.
func (s *Server) runSchedule(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "function":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		fn := normalizeID(args[1])
		tok := args[2]
		ticks, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(tok, "t"), "s"), "d"))
		if err != nil {
			return errToken("parsing.int.expected", 3)
		}
		if ticks < 0 {
			return errTokenEnd("argument.time.tick_count_too_low", 3, int32(0), int32(ticks))
		}
		if len(args) > 3 {
			if args[3] != "append" && args[3] != "replace" {
				return errToken("command.unknown.argument", 4)
			}
			if len(args) > 4 {
				return errToken("command.unknown.argument", 5)
			}
		}
		// Every function name misses: no datapack registers functions.
		return componentReply(commandError("arguments.function.unknown", fn))
	case "clear":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		return componentReply(commandError("commands.schedule.cleared.failure", args[1]))
	}
	return errToken("command.unknown.argument", 1)
}

// ---- function ----

func (s *Server) runFunction(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	fn := normalizeID(args[0])
	if len(args) > 1 {
		rest := strings.Join(args[1:], " ")
		// The tail is an NBT compound argument or the `with` form; a
		// bare word fails the compound parse at the end of its token.
		if !strings.HasPrefix(rest, "{") && args[1] != "with" {
			return errTokenEnd("argument.nbt.expected.compound", 2)
		}
	}
	return componentReply(commandError("arguments.function.unknown", fn))
}

// ---- fetchprofile ----

// uuidInts renders a 16-byte uuid as the [I;a,b,c,d] int array vanilla
// embeds in profile SNBT.
func uuidInts(u [16]byte) []int32 {
	return []int32{
		int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3]),
		int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7]),
		int32(u[8])<<24 | int32(u[9])<<16 | int32(u[10])<<8 | int32(u[11]),
		int32(u[12])<<24 | int32(u[13])<<16 | int32(u[14])<<8 | int32(u[15]),
	}
}

// profileActions builds the four bracketed action components vanilla's
// fetchprofile success reply packs into its second argument.
func profileActions(profileSNBT string, u [16]byte, name string) component {
	spacer := component{emptyKey: " ", forceCompound: true}
	inner := component{extra: []component{
		{translate: "chat.square_brackets", with: []any{component{
			copyTo:    profileSNBT,
			color:     "green",
			translate: "commands.fetchprofile.copy_component",
		}}},
		spacer,
		{translate: "chat.square_brackets", with: []any{component{
			runCmd:    "give @s minecraft:player_head[profile=" + profileSNBT + "]",
			color:     "green",
			translate: "commands.fetchprofile.give_item",
		}}},
		spacer,
		{translate: "chat.square_brackets", with: []any{component{
			runCmd:    "summon minecraft:mannequin ~ ~ ~ {profile:" + profileSNBT + "}",
			color:     "green",
			translate: "commands.fetchprofile.summon_mannequin",
		}}},
		spacer,
		{translate: "chat.square_brackets", with: []any{component{
			copyTo:    "{player:" + profileSNBT + "}",
			color:     "green",
			translate: "commands.fetchprofile.copy_text",
			with: []any{map[string]any{
				"color":  "white",
				"player": map[string]any{"id": uuidInts(u), "name": name},
			}},
		}}},
	}}
	return inner
}

func (s *Server) runFetchProfile(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	sender := s.findSessionByName(player)
	switch args[0] {
	case "name":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		// Offline server: names that are not Mojang profiles fail the
		// lookup, matching vanilla when the API has no such profile.
		return componentReply(commandError("commands.fetchprofile.name.failure", args[1]))
	case "id":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		return componentReply(commandError("commands.fetchprofile.id.failure", args[1]))
	case "entity":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		t, rep := s.resolveSingleEntity(sender, args[1], 2)
		if rep != nil {
			return *rep
		}
		if t.sess == nil {
			return componentReply(commandError("commands.fetchprofile.no_profile", t.nameComp()))
		}
		u := t.sess.getUUID()
		name := t.sess.getName()
		ints := uuidInts(u)
		profileSNBT := fmt.Sprintf("{id:[I;%d,%d,%d,%d],name:%q}", ints[0], ints[1], ints[2], ints[3], name)
		return componentReply(translatable("commands.fetchprofile.entity.success",
			t.nameComp(), profileActions(profileSNBT, u, name)))
	}
	return errToken("command.unknown.argument", 1)
}

// ---- attribute ----

// attrNameArg renders the entity-name argument attribute replies use:
// a player's bare name or the mob type translation — not the hover
// name component /item-style replies carry.
func attrNameArg(t *dataEntity) any {
	if t.sess != nil {
		return t.sess.getName()
	}
	if t.mob.CustomName != "" {
		return t.mob.CustomName
	}
	n, _ := EntityTypeName(t.mob.Type)
	return component{translate: "entity.minecraft." + n}
}

// attrInstance resolves the entity's base for an attribute and whether
// the entity carries the attribute at all.
func (s *Server) attrInstance(t *dataEntity, name string) (e *Entity, base float64, has bool) {
	e = s.entityPtr(t)
	if e == nil {
		return nil, 0, false
	}
	var table map[string]float64
	if t.sess != nil {
		table = playerAttrBases
	} else if n, ok := EntityTypeName(e.Type); ok {
		table = mobAttrBases[n]
	}
	base, has = table[name]
	if !has {
		if v, ok2 := livingAttrBases[name]; ok2 && (t.sess != nil || e.Living) {
			base, has = v, true
		}
	}
	if v, ok := e.AttrBase[name]; ok {
		base, has = v, true
	}
	return e, base, has
}

// attrValue applies the modifier passes vanilla computes: add_value
// first, then multiplied_base against the base, then multiplied_total
// on the running total. Modifier ids iterate sorted for determinism.
func attrValue(base float64, mods map[string]attrMod) float64 {
	v := base
	for pass := 0; pass < 3; pass++ {
		ids := make([]string, 0, len(mods))
		for id, m := range mods {
			if m.op == pass {
				ids = append(ids, id)
			}
		}
		for _, id := range ids {
			switch pass {
			case 0:
				v += mods[id].value
			case 1:
				v += base * mods[id].value
			default:
				v *= 1 + mods[id].value
			}
		}
	}
	return v
}

func (s *Server) runAttribute(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	// The attribute resource argument is validated at parse time, so a
	// bad name wins over a runtime entity miss.
	attr := normalizeID(args[1])
	short := strings.TrimPrefix(attr, "minecraft:")
	if !attributeNames[short] {
		return errTokenEnd("argument.resource.not_found", 2, attr, "minecraft:attribute")
	}
	attrComp := component{translate: "attribute.name." + strings.ReplaceAll(short, ":", ".")}
	t, rep := s.resolveSingleEntity(sender, args[0], 1)
	if rep != nil {
		return *rep
	}
	e, base, has := s.attrInstance(t, short)
	if !has {
		return componentReply(commandError("commands.attribute.failed.no_attribute",
			attrNameArg(t), attrComp))
	}
	nameArg := attrNameArg(t)
	sub := args[2]
	switch sub {
	case "get":
		if len(args) > 3 {
			if _, err := strconv.ParseFloat(args[3], 64); err != nil {
				return errToken("parsing.double.expected", 4)
			}
			if len(args) > 4 {
				return errToken("command.unknown.argument", 5)
			}
		}
		return componentReply(translatable("commands.attribute.value.get.success",
			attrComp, nameArg, nestedCompound{key: "", value: attrValue(base, e.AttrMods[short])}))
	case "base":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", 4)
		}
		switch args[3] {
		case "get":
			if len(args) > 4 {
				if _, err := strconv.ParseFloat(args[4], 64); err != nil {
					return errToken("parsing.double.expected", 5)
				}
				if len(args) > 5 {
					return errToken("command.unknown.argument", 6)
				}
			}
			return componentReply(translatable("commands.attribute.base_value.get.success",
				attrComp, nameArg, nestedCompound{key: "", value: base}))
		case "set":
			if len(args) < 5 {
				return errTokenEnd("command.unknown.command", 5)
			}
			v, err := strconv.ParseFloat(args[4], 64)
			if err != nil {
				return errToken("parsing.double.expected", 5)
			}
			if len(args) > 5 {
				return errToken("command.unknown.argument", 6)
			}
			if e.AttrBase == nil {
				e.AttrBase = map[string]float64{}
			}
			e.AttrBase[short] = v
			e.markAttrTouched(short)
			return componentReply(translatable("commands.attribute.base_value.set.success",
				attrComp, nameArg, nestedCompound{key: "", value: v}))
		case "reset":
			delete(e.AttrBase, short)
			e.markAttrTouched(short)
			def := base
			if t.sess != nil {
				def = playerAttrBases[short]
			} else if n, ok := EntityTypeName(e.Type); ok {
				def = mobAttrBases[n][short]
			}
			return componentReply(translatable("commands.attribute.base_value.reset.success",
				attrComp, nameArg, nestedCompound{key: "", value: def}))
		}
		return errToken("command.unknown.argument", 4)
	case "modifier":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", 4)
		}
		switch args[3] {
		case "add":
			if len(args) < 7 {
				return errTokenEnd("command.unknown.command", len(args)+1)
			}
			id := normalizeID(args[4])
			v, err := strconv.ParseFloat(args[5], 64)
			if err != nil {
				return errToken("parsing.double.expected", 6)
			}
			var op int
			switch args[6] {
			case "add_value":
				op = 0
			case "add_multiplied_base":
				op = 1
			case "add_multiplied_total":
				op = 2
			default:
				return errToken("command.unknown.argument", 7)
			}
			if len(args) > 7 {
				return errToken("command.unknown.argument", 8)
			}
			if e.AttrMods == nil {
				e.AttrMods = map[string]map[string]attrMod{}
			}
			mods := e.AttrMods[short]
			if mods == nil {
				mods = map[string]attrMod{}
				e.AttrMods[short] = mods
			}
			if _, dup := mods[id]; dup {
				return componentReply(commandError("commands.attribute.failed.modifier_already_present",
					nestedCompound{key: "", value: id}, attrComp, nameArg))
			}
			mods[id] = attrMod{value: v, op: op}
			e.markAttrTouched(short)
			return componentReply(translatable("commands.attribute.modifier.add.success",
				nestedCompound{key: "", value: id}, attrComp, nameArg))
		case "remove":
			if len(args) < 5 {
				return errTokenEnd("command.unknown.command", 5)
			}
			id := normalizeID(args[4])
			mods := e.AttrMods[short]
			if _, ok := mods[id]; !ok {
				return componentReply(commandError("commands.attribute.failed.no_modifier",
					attrComp, nameArg, nestedCompound{key: "", value: id}))
			}
			delete(mods, id)
			e.markAttrTouched(short)
			return componentReply(translatable("commands.attribute.modifier.remove.success",
				nestedCompound{key: "", value: id}, attrComp, nameArg))
		case "value":
			if len(args) < 6 || args[4] != "get" {
				if len(args) >= 5 && args[4] != "get" {
					return errToken("command.unknown.argument", 5)
				}
				return errTokenEnd("command.unknown.command", len(args)+1)
			}
			id := normalizeID(args[5])
			m, ok := e.AttrMods[short][id]
			if !ok {
				return componentReply(commandError("commands.attribute.failed.no_modifier",
					attrComp, nameArg, nestedCompound{key: "", value: id}))
			}
			if len(args) > 6 {
				if _, err := strconv.ParseFloat(args[6], 64); err != nil {
					return errToken("parsing.double.expected", 7)
				}
				if len(args) > 7 {
					return errToken("command.unknown.argument", 8)
				}
			}
			return componentReply(translatable("commands.attribute.modifier.value.get.success",
				nestedCompound{key: "", value: id}, attrComp, nameArg,
				nestedCompound{key: "", value: m.value}))
		}
		return errToken("command.unknown.argument", 4)
	}
	return errToken("command.unknown.argument", 3)
}

// ---- dialog ----

func (s *Server) runDialog(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "show":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		// The entity argument parses before the dialog one, so a
		// players-only failure on it wins; a runtime entity miss waits
		// until after the dialog id validates.
		if specErr := playerSelectorParse(args[1]); specErr != nil {
			return *specErr
		}
		dlg := normalizeID(args[2])
		if !dialogNames[strings.TrimPrefix(dlg, "minecraft:")] {
			return errTokenEnd("argument.resource_or_id.no_such_element", 3, dlg, "minecraft:dialog")
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 4)
		}
		targets, rep := s.selectPlayers(sender, args[1], 1)
		if rep != nil {
			return *rep
		}
		if len(targets) == 1 {
			return componentReply(translatable("commands.dialog.show.single",
				(&dataEntity{sess: targets[0]}).nameComp()))
		}
		return componentReply(translatable("commands.dialog.show.multiple", int32(len(targets))))
	case "clear":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		targets, rep := s.selectPlayers(sender, args[1], 1)
		if rep != nil {
			return *rep
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		if len(targets) == 1 {
			return componentReply(translatable("commands.dialog.clear.single",
				(&dataEntity{sess: targets[0]}).nameComp()))
		}
		return componentReply(translatable("commands.dialog.clear.multiple", int32(len(targets))))
	}
	return errToken("command.unknown.argument", 1)
}

// playerSelectorParse replays the parse-time part of a players-only
// entity argument: selector syntax plus the @e rejection. Name and
// allowed-selector lookups stay a runtime concern for selectPlayers.
func playerSelectorParse(tok string) *reply {
	if len(tok) == 0 || tok[0] != '@' {
		return nil
	}
	spec, serr := parseSelector(tok, 2)
	if serr != nil {
		return serr
	}
	if spec.variable == 'e' {
		r := errToken("argument.player.entities", 0)
		return &r
	}
	return nil
}

// ---- locate ----

// reslocRead consumes the resource-location charset [a-z0-9_./-] plus
// ':' — the same characters vanilla's ResourceLocation.read accepts —
// and reports how much of s is readable.
func reslocRead(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '/' || c == '.' || c == '-' || c == ':' {
			i++
			continue
		}
		break
	}
	return i
}

// reslocID normalizes a fully-read resource token into "ns:path" form
// (ns defaults to minecraft). It reports false when the id is
// malformed — more than one ':', or a namespace/path char outside its
// allowed set — which vanilla reports as argument.id.invalid.
func reslocID(tok string) (string, bool) {
	ns, path := "minecraft", tok
	if i := strings.IndexByte(tok, ':'); i >= 0 {
		ns, path = tok[:i], tok[i+1:]
		if strings.IndexByte(path, ':') >= 0 {
			return "", false
		}
	}
	for i := 0; i < len(ns); i++ {
		c := ns[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' && c != '-' {
			return "", false
		}
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '/' && c != '.' && c != '-' {
			return "", false
		}
	}
	return ns + ":" + path, true
}

// reslocPrefixID renders a partially-consumed resource token the way
// the resource.not_found error prints it: the valid prefix gets the
// implied minecraft namespace when it has no ':'.
func reslocPrefixID(prefix string) string {
	if strings.IndexByte(prefix, ':') >= 0 {
		return prefix
	}
	return "minecraft:" + prefix
}

// tagsets used by /locate: the registry-tag names that exist in vanilla
// 26.2 (a missing tag fails differently than a tag that simply does not
// match anything nearby).
var structureTagNames = map[string]bool{
	"cats_spawn_as_black": true, "cats_spawn_in": true, "dolphin_located": true,
	"eye_of_ender_located": true, "mineshaft": true, "ocean_ruin": true,
	"on_desert_village_maps": true, "on_jungle_explorer_maps": true,
	"on_ocean_explorer_maps": true, "on_plains_village_maps": true,
	"on_savanna_village_maps": true, "on_snowy_village_maps": true,
	"on_swamp_explorer_maps": true, "on_taiga_village_maps": true,
	"on_treasure_maps": true, "on_trial_chambers_maps": true,
	"on_woodland_explorer_maps": true, "ruined_portal": true,
	"shipwreck": true, "village": true,
}

var biomeTagNames = map[string]bool{
	"allows_surface_slime_spawns": true, "allows_tropical_fish_spawns_at_any_height": true,
	"has_structure/ancient_city": true, "has_structure/bastion_remnant": true,
	"has_structure/buried_treasure": true, "has_structure/desert_pyramid": true,
	"has_structure/end_city": true, "has_structure/igloo": true,
	"has_structure/jungle_temple": true, "has_structure/mineshaft": true,
	"has_structure/mineshaft_mesa": true, "has_structure/nether_fortress": true,
	"has_structure/nether_fossil": true, "has_structure/ocean_monument": true,
	"has_structure/ocean_ruin_cold": true, "has_structure/ocean_ruin_warm": true,
	"has_structure/pillager_outpost": true, "has_structure/ruined_portal_desert": true,
	"has_structure/ruined_portal_jungle": true, "has_structure/ruined_portal_mountain": true,
	"has_structure/ruined_portal_nether": true, "has_structure/ruined_portal_ocean": true,
	"has_structure/ruined_portal_standard": true, "has_structure/ruined_portal_swamp": true,
	"has_structure/shipwreck": true, "has_structure/shipwreck_beached": true,
	"has_structure/stronghold": true, "has_structure/swamp_hut": true,
	"has_structure/trail_ruins": true, "has_structure/trial_chambers": true,
	"has_structure/village_desert": true, "has_structure/village_plains": true,
	"has_structure/village_savanna": true, "has_structure/village_snowy": true,
	"has_structure/village_taiga": true, "has_structure/woodland_mansion": true,
	"is_badlands": true, "is_beach": true, "is_deep_ocean": true, "is_end": true,
	"is_forest": true, "is_hill": true, "is_jungle": true, "is_mountain": true,
	"is_nether": true, "is_ocean": true, "is_overworld": true, "is_river": true,
	"is_savanna": true, "is_taiga": true, "mineshaft_blocking": true,
	"more_frequent_drowned_spawns": true, "polar_bears_spawn_on_alternate_blocks": true,
	"produces_corals_from_bonemeal": true, "reduce_water_ambient_spawns": true,
	"required_ocean_monument_surrounding": true, "spawns_cold_variant_farm_animals": true,
	"spawns_cold_variant_frogs": true, "spawns_coral_variant_zombie_nautilus": true,
	"spawns_gold_rabbits": true, "spawns_snow_foxes": true,
	"spawns_warm_variant_farm_animals": true, "spawns_warm_variant_frogs": true,
	"spawns_white_rabbits": true, "stronghold_biased_to": true,
	"water_on_map_outlines": true, "without_wandering_trader_spawns": true,
	"without_zombie_sieges": true,
}

var poiTagNames = map[string]bool{
	"acquirable_job_site": true, "bee_home": true, "village": true,
}

// biomeTagContains records, per biome tag, which of our dimensions'
// flat biomes it covers (nested tag refs resolved). the_void is
// overworld, nether_wastes the nether, the_end the end.
var biomeTagContains = map[string]map[string]bool{
	"without_wandering_trader_spawns":    {"minecraft:the_void": true},
	"is_nether":                          {"minecraft:nether_wastes": true},
	"spawns_warm_variant_frogs":          {"minecraft:nether_wastes": true},
	"spawns_warm_variant_farm_animals":   {"minecraft:nether_wastes": true},
	"has_structure/nether_fortress":      {"minecraft:nether_wastes": true},
	"has_structure/bastion_remnant":      {"minecraft:nether_wastes": true},
	"has_structure/ruined_portal_nether": {"minecraft:nether_wastes": true},
	"is_end":                             {"minecraft:the_end": true},
	"spawns_cold_variant_frogs":          {"minecraft:the_end": true},
	"spawns_cold_variant_farm_animals":   {"minecraft:the_end": true},
}

func (s *Server) runLocate(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "structure":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		tok := args[1]
		tag := strings.HasPrefix(tok, "#")
		body := strings.TrimPrefix(tok, "#")
		if n := reslocRead(body); n < len(body) {
			return errTokenOff("command.expected.separator", 2, len(tok)-len(body)+n)
		}
		id, ok := reslocID(body)
		if !ok {
			return errToken("argument.id.invalid", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		short := strings.TrimPrefix(id, "minecraft:")
		if tag {
			if !structureTagNames[short] {
				return componentReply(commandError("commands.locate.structure.invalid", "#"+id))
			}
			return componentReply(commandError("commands.locate.structure.not_found", "#"+id))
		}
		if !structureNames[short] {
			return componentReply(commandError("commands.locate.structure.invalid", id))
		}
		// The flat comparison world has no structures.
		return componentReply(commandError("commands.locate.structure.not_found", id))
	case "biome":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		sender := s.findSessionByName(player)
		d := s.dims[sender.dimName()]
		worldBiome := normalizeID(d.w.Biome())
		tok := args[1]
		reg := "minecraft:worldgen/biome"
		var print string // the registry argument as vanilla prints it
		matches := false
		if strings.HasPrefix(tok, "#") {
			inner := tok[1:]
			n := reslocRead(inner)
			if n < len(inner) {
				return errTokenOff("argument.resource_tag.not_found", 2, 1+n, reslocPrefixID(inner[:n]), reg)
			}
			id, ok := reslocID(inner)
			if !ok {
				return errToken("argument.id.invalid", 2)
			}
			if len(args) > 2 {
				return errToken("command.unknown.argument", 3)
			}
			short := strings.TrimPrefix(id, "minecraft:")
			if !biomeTagNames[short] {
				return errTokenEnd("argument.resource_tag.not_found", 2, id, reg)
			}
			print = "#" + id
			if members := biomeTagContains[short]; members != nil && members[worldBiome] {
				matches = true
				print += " (" + worldBiome + ")"
			}
		} else {
			n := reslocRead(tok)
			if n < len(tok) {
				if tok[n] == '#' {
					return errTokenOff("command.expected.separator", 2, n)
				}
				return errTokenOff("argument.resource.not_found", 2, n, reslocPrefixID(tok[:n]), reg)
			}
			id, ok := reslocID(tok)
			if !ok {
				return errToken("argument.id.invalid", 2)
			}
			if len(args) > 2 {
				return errToken("command.unknown.argument", 3)
			}
			if !biomeNames[strings.TrimPrefix(id, "minecraft:")] {
				return errTokenEnd("argument.resource.not_found", 2, id, reg)
			}
			print = id
			matches = worldBiome == id
		}
		if matches {
			// findClosestBiome3d's first sample is the source block pos
			// with Y clamped into [minY+1, maxY+1] — always a hit in a
			// uniform-biome world.
			x, y, z, _, _, _ := sender.Pos()
			bx, by, bz := int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z))
			fy := min(max(by, int32(d.minY+1)), int32(d.minY+d.height+1))
			dy := int64(fy - by)
			dist := int32(math.Sqrt(float64(dy * dy)))
			return componentReply(translatable("commands.locate.biome.success",
				nestedCompound{key: "", value: print},
				locatePosComp(bx, fy, bz),
				nestedCompound{key: "", value: dist}))
		}
		return componentReply(commandError("commands.locate.biome.not_found", print))
	case "poi":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		tok := args[1]
		reg := "minecraft:point_of_interest_type"
		tag := strings.HasPrefix(tok, "#")
		body := strings.TrimPrefix(tok, "#")
		n := reslocRead(body)
		if n < len(body) {
			if !tag && body == tok && tok[n] == '#' {
				return errTokenOff("command.expected.separator", 2, n)
			}
			key := "argument.resource.not_found"
			off := n
			if tag {
				key = "argument.resource_tag.not_found"
				off = n + 1
			}
			return errTokenOff(key, 2, off, reslocPrefixID(body[:n]), reg)
		}
		id, ok := reslocID(body)
		if !ok {
			return errToken("argument.id.invalid", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		short := strings.TrimPrefix(id, "minecraft:")
		if tag {
			if !poiTagNames[short] {
				return errTokenEnd("argument.resource_tag.not_found", 2, id, reg)
			}
			return componentReply(commandError("commands.locate.poi.not_found", "#"+id))
		}
		if !poiNames[short] {
			return errTokenEnd("argument.resource.not_found", 2, id, reg)
		}
		return componentReply(commandError("commands.locate.poi.not_found", id))
	}
	return errToken("command.unknown.argument", 1)
}

// locatePosComp is the clickable coordinates component locate
// replies embed: green [x, y, z] suggesting a teleport.
func locatePosComp(x, y, z int32) component {
	return component{
		click: "/tp @s " + strconv.Itoa(int(x)) + " " + strconv.Itoa(int(y)) + " " + strconv.Itoa(int(z)),
		color: "green",
		hover: map[string]any{
			"action": "show_text",
			"value":  map[string]any{"translate": "chat.coordinates.tooltip"},
		},
		translate: "chat.square_brackets",
		with: []any{component{translate: "chat.coordinates", with: []any{
			nestedCompound{key: "", value: x},
			nestedCompound{key: "", value: y},
			nestedCompound{key: "", value: z},
		}}},
	}
}

// ---- place ----

func (s *Server) runPlace(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "feature":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		name := normalizeID(args[1])
		short := strings.TrimPrefix(name, "minecraft:")
		if !featureNames[short] {
			return componentReply(commandError("commands.place.feature.invalid", name))
		}
		if len(args) > 2 {
			bp, rep := s.parseBlockPos(sender, args[2:], 3)
			if rep != nil {
				return *rep
			}
			if len(args) > 5 {
				return errToken("command.unknown.argument", 6)
			}
			return componentReply(translatable("commands.place.feature.success",
				nestedCompound{key: "", value: name},
				nestedCompound{key: "", value: int32(bp[0])},
				nestedCompound{key: "", value: int32(bp[1])},
				nestedCompound{key: "", value: int32(bp[2])}))
		}
		x, y, z, _, _, _ := sender.Pos()
		return componentReply(translatable("commands.place.feature.success",
			nestedCompound{key: "", value: name},
			nestedCompound{key: "", value: int32(x)},
			nestedCompound{key: "", value: int32(y)},
			nestedCompound{key: "", value: int32(z)}))
	case "template":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		name := normalizeID(args[1])
		return componentReply(commandError("commands.place.template.invalid", name))
	case "jigsaw":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		pool := normalizeID(args[1])
		short := strings.TrimPrefix(pool, "minecraft:")
		if !templatePoolNames[short] {
			return componentReply(commandError("commands.place.jigsaw.invalid", pool))
		}
		return componentReply(commandError("commands.place.jigsaw.failed"))
	case "structure":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		name := normalizeID(args[1])
		short := strings.TrimPrefix(name, "minecraft:")
		if !structureNames[short] {
			return componentReply(commandError("commands.place.structure.invalid", name))
		}
		return componentReply(commandError("commands.place.structure.failed"))
	}
	return errToken("command.unknown.argument", 1)
}
