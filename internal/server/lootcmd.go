package server

import (
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// runLoot implements /loot:
//
//	loot (spawn <pos> | replace entity <entities> <slot> [<count>] |
//	    replace block <pos> <slot> [<count>] | give <players> |
//	    insert <pos>) (fish <table> <pos> [<tool>|mainhand|offhand] |
//	    loot <table> | kill <entity> | mine <pos> [<tool>|mainhand|offhand])
//
// Entity arguments resolve at execute time here, so a miss answers a
// bare component with no context echo; resource arguments (loot table,
// slot, item) fail at parse and carry the end-of-token cursor.
func (s *Server) runLoot(player string, args []string) reply {
	sender := s.findSessionByName(player)
	srcDim := s.dimOf(player)

	// Target half.
	type lootTarget struct {
		kind      int // 0 spawn, 1 give, 2 insert, 3 replaceEntity, 4 replaceBlock
		pos       [3]int
		entities  []*dataEntity
		players   []*Session
		entTok    string
		entIdx    int
		playerTok string
		playerIdx int
		slot      int32
		count     int
	}
	var tgt lootTarget
	tgt.count = 0
	i := 0
	if i >= len(args) {
		return errTokenEnd("command.unknown.command", i+1)
	}
	targetTok := i + 1
	switch args[i] {
	case "give":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		tgt.kind = 1
		tgt.playerTok, tgt.playerIdx = args[i], i+1
		if strings.HasPrefix(tgt.playerTok, "@") {
			if _, r := parseSelector(tgt.playerTok, tgt.playerIdx); r != nil {
				return *r
			}
		}
		i++
	case "spawn":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		pos, r := s.parseBlockPos(sender, args[i:], i+1)
		if r != nil {
			return *r
		}
		i += 3
		tgt.pos = pos
	case "insert":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		pos, r := s.parseBlockPos(sender, args[i:], i+1)
		if r != nil {
			return *r
		}
		i += 3
		tgt.kind = 2
		tgt.pos = pos
	case "replace":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		switch args[i] {
		case "entity":
			i++
			if i >= len(args) {
				return errTokenEnd("command.unknown.command", i+1)
			}
			tgt.entTok, tgt.entIdx = args[i], i+1
			if strings.HasPrefix(tgt.entTok, "@") {
				if _, r := parseSelector(tgt.entTok, tgt.entIdx); r != nil {
					return *r
				}
			}
			tgt.kind = 3
			i++
		case "block":
			i++
			if i >= len(args) {
				return errTokenEnd("command.unknown.command", i+1)
			}
			pos, r := s.parseBlockPos(sender, args[i:], i+1)
			if r != nil {
				return *r
			}
			i += 3
			tgt.pos = pos
			tgt.kind = 4
		default:
			return errToken("command.unknown.argument", i+1)
		}
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		id, ok := parseItemSlot(args[i])
		if !ok {
			return errTokenEnd("slot.unknown", i+1, args[i])
		}
		tgt.slot = id
		i++
		if i < len(args) {
			// Optional count is a separate grammar branch: a token
			// that isn't a small int falls through to the source
			// literal instead of erroring.
			if n, err := strconv.ParseInt(args[i], 10, 32); err == nil {
				tgt.count = int(n)
				i++
			}
		}
	default:
		return errToken("command.unknown.argument", targetTok)
	}

	// Source half.
	if i >= len(args) {
		return errTokenEnd("command.unknown.command", i+1)
	}
	sourceTok := i + 1
	var table, tableErrName string
	var killEnts []*dataEntity
	var killTok string
	var killIdx int
	var minePos [3]int
	var mineCheck bool
	var fishPos [3]int
	var fishCheck bool
	switch args[i] {
	case "fish":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		table, tableErrName = args[i], args[i]
		i++
		pos, r := s.parseBlockPos(sender, args[i:], i+1)
		if r != nil {
			return *r
		}
		i += 3
		fishPos, fishCheck = pos, true
		if i < len(args) {
			// Tool literal or item stack.
			if args[i] == "mainhand" || args[i] == "offhand" {
				i++
			} else {
				if _, r, ok := s.parseItemStack(args, i+1); !ok {
					return r
				}
				i++
			}
		}
	case "loot":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		table, tableErrName = args[i], args[i]
		i++
	case "kill":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		killTok, killIdx = args[i], i+1
		if strings.HasPrefix(killTok, "@") {
			if _, r := parseSelector(killTok, killIdx); r != nil {
				return *r
			}
		}
		i++
	case "mine":
		i++
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		pos, r := s.parseBlockPos(sender, args[i:], i+1)
		if r != nil {
			return *r
		}
		i += 3
		minePos, mineCheck = pos, true
		if i < len(args) {
			if args[i] == "mainhand" || args[i] == "offhand" {
				i++
			} else {
				if _, r, ok := s.parseItemStack(args, i+1); !ok {
					return r
				}
				i++
			}
		}
	default:
		return errToken("command.unknown.argument", sourceTok)
	}
	if i < len(args) {
		return errToken("command.unknown.argument", i+1)
	}

	// Loot-table resolution for fish/loot happens at parse time in
	// vanilla (resource_or_id arg); kill/mine defer to execute.
	if table != "" {
		name := normalizeID(table)
		if _, ok := data.LootTables()[strings.TrimPrefix(name, "minecraft:")]; !ok {
			return errTokenEnd("argument.resource_or_id.no_such_element",
				sourceTok+1, name, "minecraft:loot_table")
		}
		table = strings.TrimPrefix(name, "minecraft:")
		_ = tableErrName
	}

	// Entities resolve inside the command body, target first. A bare
	// miss is a bare component — vanilla sends no context echo for
	// execute-time lookups.
	if tgt.playerTok != "" {
		players, r := s.lootPlayers(sender, tgt.playerTok, tgt.playerIdx)
		if r != nil {
			return *r
		}
		tgt.players = players
	}
	if tgt.entTok != "" {
		ents, r := s.lootEntities(sender, tgt.entTok, tgt.entIdx, false)
		if r != nil {
			return *r
		}
		tgt.entities = ents
	}
	if killTok != "" {
		ents, r := s.lootEntities(sender, killTok, killIdx, true)
		if r != nil {
			return *r
		}
		killEnts = ents
	}

	// Execute: resolve drops from the source. The replace count arg is
	// the number of stacks to produce.
	rolls := tgt.count
	if rolls < 1 {
		rolls = 1
	}
	var drops []ItemStack
	var tableComp component
	var killOk bool
	switch {
	case len(killEnts) > 0:
		e := killEnts[0]
		tname := "player"
		if e.mob != nil {
			if n, ok := EntityTypeName(e.mob.Type); ok {
				tname = n
			}
		}
		table = "entities/" + tname
		tableComp = component{emptyKey: "minecraft:" + table}
		killOk = true
		for r := 0; r < rolls; r++ {
			drops = append(drops, rollLootTable(table, rand.Int63())...)
		}
	case mineCheck:
		if !s.blockPosLoaded(srcDim, sender, minePos[0], minePos[1], minePos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		blk := srcDim.w.GetBlock(world.Pos{X: minePos[0], Y: minePos[1], Z: minePos[2]})
		bname, _ := world.StateOwner(blk)
		table = "blocks/" + bname
		if _, ok := data.LootTables()[table]; !ok {
			return componentReply(commandError("commands.drop.no_loot_table.block",
				component{translate: "block.minecraft." + bname}))
		}
		for r := 0; r < rolls; r++ {
			drops = append(drops, rollLootTable(table, rand.Int63())...)
		}
	case fishCheck:
		if !s.blockPosLoaded(srcDim, sender, fishPos[0], fishPos[1], fishPos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		for r := 0; r < rolls; r++ {
			drops = append(drops, rollLootTable(table, rand.Int63())...)
		}
	default:
		for r := 0; r < rolls; r++ {
			drops = append(drops, rollLootTable(table, rand.Int63())...)
		}
	}

	// Target application.
	switch tgt.kind {
	case 1: // give
		for _, st := range drops {
			for _, p := range tgt.players {
				chunk := st
				remainder, _ := p.addStack(chunk)
				if !remainder.Empty() {
					if e := s.dropItemLocked(p, remainder); e != nil {
						e.PickupDelay = 0
						e.Owner = p.getUUID()
						e.HasOwner = true
					}
				}
			}
		}
	case 0: // spawn — drops appear at the position; no loaded check.
		for _, st := range drops {
			s.spawnItemDrop(srcDim.name, world.Pos{X: tgt.pos[0], Y: tgt.pos[1], Z: tgt.pos[2]}, st)
		}
	case 2: // insert
		if !s.blockPosLoaded(srcDim, sender, tgt.pos[0], tgt.pos[1], tgt.pos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		pos := world.Pos{X: tgt.pos[0], Y: tgt.pos[1], Z: tgt.pos[2]}
		if _, ok := containerSpecFor(srcDim.w.GetBlock(pos)); !ok {
			return componentReply(commandError("commands.item.target.not_a_container",
				int32(tgt.pos[0]), int32(tgt.pos[1]), int32(tgt.pos[2])))
		}
		if c := s.containerAt(srcDim, pos); c != nil {
			for _, st := range drops {
				moving := st
				for i := range c.slots {
					if insertStack(&c.slots[i], &moving) {
						break
					}
				}
			}
		}
	case 3: // replace entity — apply to each resolved entity's slot.
		for _, e := range tgt.entities {
			if e.sess == nil {
				continue
			}
			for _, st := range drops {
				put := st
				if tgt.count >= 1 {
					put.Count = int32(tgt.count)
				}
				s.writeEntitySlot(e.sess, tgt.slot, put)
			}
		}
	case 4: // replace block
		if !s.blockPosLoaded(srcDim, sender, tgt.pos[0], tgt.pos[1], tgt.pos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		pos := world.Pos{X: tgt.pos[0], Y: tgt.pos[1], Z: tgt.pos[2]}
		if _, ok := containerSpecFor(srcDim.w.GetBlock(pos)); !ok {
			return componentReply(commandError("commands.item.target.not_a_container",
				int32(tgt.pos[0]), int32(tgt.pos[1]), int32(tgt.pos[2])))
		}
		if c := s.containerAt(srcDim, pos); c != nil {
			if n := int(tgt.slot); n >= 0 && n < len(c.slots) {
				count := tgt.count
				if count < 1 {
					count = 1
				}
				for _, st := range drops {
					put := st
					put.Count = int32(count)
					c.slots[n] = put
				}
			}
		}
	}

	// Reply: single vs multiple is the number of produced stacks.
	total := int32(0)
	for _, st := range drops {
		total += st.Count
	}
	if killOk {
		if len(drops) == 1 {
			return componentReply(translatable("commands.drop.success.single_with_table",
				int32(drops[0].Count), itemBrackets(drops[0]), tableComp))
		}
		return componentReply(translatable("commands.drop.success.multiple_with_table",
			int32(total), tableComp))
	}
	if len(drops) == 1 {
		return componentReply(translatable("commands.drop.success.single",
			int32(drops[0].Count), itemBrackets(drops[0])))
	}
	return componentReply(translatable("commands.drop.success.multiple",
		int32(total)))
}

// lootEntities resolves an entity argument for /loot: a bare name is
// a notfound.entity miss (unlike the players argument's
// notfound.player), while a selector goes through the shared
// selector machinery and loses its cursor mark — vanilla resolves
// these inside the command body, so the error is a bare component.
func (s *Server) lootEntities(sender *Session, tok string, tokIdx int, single bool) ([]*dataEntity, *reply) {
	if len(tok) > 0 && tok[0] != '@' {
		if sess := s.findSessionByName(tok); sess != nil {
			return []*dataEntity{{sess: sess}}, nil
		}
		r := componentReply(commandError("argument.entity.notfound.entity"))
		return nil, &r
	}
	ents, r := s.selectDataEntities(sender, tok, tokIdx, single)
	if r != nil {
		return nil, r
	}
	if len(ents) == 0 {
		r := componentReply(commandError("argument.entity.notfound.entity"))
		return nil, &r
	}
	return ents, nil
}

func (s *Server) lootPlayers(sender *Session, tok string, tokIdx int) ([]*Session, *reply) {
	return s.selectPlayers(sender, tok, tokIdx)
}
