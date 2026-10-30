package server

// Inventory-item commands measured against the vanilla 26.2 server
// jar:
//
//	enchant <targets> <enchant> [<level>]   enchantment on the held
//	                                      stack, with the itemless /
//	                                      incompatible / level edges
//	clear [targets] [item] [maxCount]     clears matching stacks;
//	                                      maxCount 0 counts without
//	                                      removing, targets resolve
//	                                      through the player selector

import (
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

func (s *Server) registerInvCommands() {
	s.registerComponentCommand("enchant", func(player string, args []string) reply {
		return s.runEnchant(player, args)
	})
	s.registerComponentCommand("clear", func(player string, args []string) reply {
		return s.runClear(player, args)
	})
}

// selectPlayers parses the player-selector argument /clear and friends
// take: bare names hit the player table, @e fails player.entities, and
// the other variables filter live sessions through selMatch.
func (s *Server) selectPlayers(sender *Session, tok string, tokIdx int) ([]*Session, *reply) {
	if len(tok) == 0 || tok[0] != '@' {
		if sess := s.findSessionByName(tok); sess != nil {
			return []*Session{sess}, nil
		}
		r := componentReply(commandError("argument.entity.notfound.player"))
		return nil, &r
	}
	spec, serr := parseSelector(tok, tokIdx)
	if serr != nil {
		return nil, serr
	}
	if spec.variable == 'e' {
		r := errToken("argument.player.entities", 0)
		return nil, &r
	}
	if spec.variable == 's' {
		target := sender
		source := s.activeCommandSource
		if sender != nil {
			source = s.commandFrame(sender.getName())
		}
		if source != nil {
			target = nil
			if source.executor != nil {
				target = source.executor.sess
			}
		} else if sender != nil && sender.execEnt != nil {
			target = sender.execEnt.sess
		}
		if target == nil {
			r := componentReply(commandError("argument.entity.notfound.player"))
			return nil, &r
		}
		return []*Session{target}, nil
	}
	ax, ay, az := float64(0), float64(0), float64(0)
	if spec.hasPos {
		ax, ay, az = spec.x, spec.y, spec.z
	} else if sender != nil {
		ax, ay, az, _, _, _ = sender.Pos()
	}
	var out []*Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		if s.selMatch(&spec, &dataEntity{sess: sess}, ax, ay, az) {
			out = append(out, sess)
		}
		return true
	})
	if len(out) == 0 {
		r := componentReply(commandError("argument.entity.notfound.player"))
		return nil, &r
	}
	return out, nil
}

// ---- enchant ----

// enchantLevelComp renders the level tail of the enchantment component:
// roman numerals have translation keys through ten.
func enchantLevelComp(level int) component {
	if level >= 1 && level <= 10 {
		return component{translate: "enchantment.level." + strconv.Itoa(level)}
	}
	return component{text: strconv.Itoa(level)}
}

func (s *Server) runEnchant(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	targets, rep := s.selectDataEntities(sender, args[0], 1, false, nil)
	if rep != nil {
		if rep.comp.extra != nil && len(rep.comp.extra) == 1 && rep.comp.extra[0].translate == "argument.entity.notfound.player" {
			return componentReply(commandError("argument.entity.notfound.entity"))
		}
		return *rep
	}
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	name := strings.TrimPrefix(args[1], "minecraft:")
	meta, ok := data.Enchantments[name]
	if !ok {
		return errTokenEnd("argument.resource.not_found", 2, args[1], "minecraft:enchantment")
	}
	level := 1
	if len(args) > 2 {
		lv, err := strconv.Atoi(args[2])
		if err != nil {
			return errToken("parsing.int.expected", 3)
		}
		if lv < 0 {
			return errToken("argument.integer.low", 3, int32(0), int32(lv))
		}
		level = lv
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	enchComp := component{
		translate: "enchantment.minecraft." + name,
		color:     "gray",
		extra: []component{
			{emptyKey: " "},
			enchantLevelComp(level),
		},
	}
	n := 0
	var lastName component
	for _, t := range targets {
		var held ItemStack
		var slot int16
		if t.sess != nil {
			slot = invSlotForHotbar(t.sess.held.Load())
			held = t.sess.stackAt(slot)
		}
		if held.Empty() {
			targetArg := t.selName()
			if t.sess == nil && t.mob.CustomName == "" {
				if disp, ok := entityDisplayNameText[targetArg]; ok {
					targetArg = disp
				}
			}
			return componentReply(commandError("commands.enchant.failed.itemless", targetArg))
		}
		itemName, _ := itemNameOf(held.ID)
		itemDisp := itemDisplayName[itemName]
		if _, ok := world.BlockStateOf(itemName, nil); ok {
			if bd, ok := blockDisplayName[itemName]; ok {
				itemDisp = bd
			}
		}
		supported := false
		for _, it := range meta.Supported {
			if it == itemName {
				supported = true
				break
			}
		}
		if !supported {
			return componentReply(commandError("commands.enchant.failed.incompatible", itemDisp))
		}
		if level > meta.MaxLevel {
			return componentReply(commandError("commands.enchant.failed.level", int32(level), int32(meta.MaxLevel)))
		}
		for _, e := range strings.Split(held.Ench, ",") {
			if en, _, ok := strings.Cut(e, ":"); ok && en != "" && !enchantsCompatible(en, name) {
				return componentReply(commandError("commands.enchant.failed.incompatible", itemDisp))
			}
		}
		held.Ench = mergeEnch(held.Ench, []enchantInstance{{name: name, level: level}})
		if t.sess != nil {
			t.sess.invMu.Lock()
			t.sess.inv[slot] = held
			t.sess.invMu.Unlock()
			_ = s.sendContainerSlot(t.sess, slot, held)
		}
		n++
		lastName = t.nameComp()
	}
	if n == 1 {
		return opsEchoReply(translatable("commands.enchant.success.single", enchComp, lastName))
	}
	return opsEchoReply(translatable("commands.enchant.success.multiple",
		enchComp, nestedCompound{key: "", value: int32(n)}))
}

// ---- clear ----

func (s *Server) runClear(player string, args []string) reply {
	sender := s.findSessionByName(player)
	targets := []*Session{}
	if len(args) == 0 {
		var rep *reply
		targets, rep = s.selectPlayers(sender, "@s", 0)
		if rep != nil {
			return componentReply(commandError("permissions.requires.player"))
		}
	} else {
		t, rep := s.selectPlayers(sender, args[0], 1)
		if rep != nil {
			return *rep
		}
		targets = t
	}
	itemName := ""
	if len(args) > 1 {
		short := strings.TrimPrefix(args[1], "minecraft:")
		if _, ok := world.ItemIDs[short]; !ok {
			return errTokenEnd("argument.item.id.invalid", 2, args[1])
		}
		itemName = short
	}
	maxCount := int32(-1)
	testOnly := false
	if len(args) > 2 {
		n, err := strconv.Atoi(args[2])
		if err != nil {
			return errToken("parsing.int.expected", 3)
		}
		if n < 0 {
			return errToken("argument.integer.low", 3, int32(0), int32(n))
		}
		maxCount = int32(n)
		testOnly = maxCount == 0
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	// Vanilla's clear walks the inventory container — the 36 main
	// slots, armor, and offhand; the crafting grid is not part of it.
	total := 0
	for _, sess := range targets {
		remaining := maxCount
		for slot := int16(slotMainStart); slot < slotCount; slot++ {
			it := sess.stackAt(slot)
			if it.Empty() {
				continue
			}
			nm, ok := itemNameOf(it.ID)
			if !ok || (itemName != "" && nm != itemName) {
				continue
			}
			matched := it.Count
			if !testOnly && remaining >= 0 && matched > remaining {
				matched = remaining
			}
			total += int(matched)
			if testOnly {
				continue
			}
			remove := it.Count
			if remaining >= 0 && it.Count > remaining {
				remove = remaining
			}
			if remove >= it.Count {
				it = ItemStack{}
			} else {
				it.Count -= remove
			}
			if remaining >= 0 {
				remaining -= remove
			}
			sess.invMu.Lock()
			sess.inv[slot] = it
			sess.invMu.Unlock()
			_ = s.sendContainerSlot(sess, slot, it)
			if remaining == 0 && maxCount >= 0 {
				break
			}
		}
	}
	result := func(r reply) reply { r.result, r.hasResult = total, true; return r }

	if len(targets) == 1 {
		name := targets[0].getName()
		if total == 0 {
			return result(componentReply(commandError("clear.failed.single", name)))
		}
		if testOnly {
			return result(componentReply(translatable("commands.clear.test.single",
				nestedCompound{key: "", value: int32(total)},
				playerChatComponent(targets[0]))))
		}
		return result(opsEchoReply(translatable("commands.clear.success.single",
			nestedCompound{key: "", value: int32(total)},
			playerChatComponent(targets[0]))))
	}
	if total == 0 {
		return result(componentReply(commandError("clear.failed.multiple", int32(len(targets)))))
	}
	key := "commands.clear.success.multiple"
	if testOnly {
		key = "commands.clear.test.multiple"
	}
	return result(opsEchoReply(translatable(key,
		nestedCompound{key: "", value: int32(total)},
		nestedCompound{key: "", value: int32(len(targets))})))
}
