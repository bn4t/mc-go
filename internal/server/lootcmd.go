package server

import (
	"math/rand"
	"strconv"
	"strings"
	"sync"

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
		hasCount  bool
	}
	var tgt lootTarget
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
				tgt.hasCount = true
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
	var mineTool ItemStack
	mineHand := -2 // -2: no tool arg → source's main hand; -1: item arg
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
			if args[i] == "mainhand" {
				mineHand = 0
				i++
			} else if args[i] == "offhand" {
				mineHand = 1
				i++
			} else {
				st, r, ok := s.parseItemStack(args, i+1)
				if !ok {
					return r
				}
				mineTool, mineHand = st, -1
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

	// Execute: resolve drops from the source — rolled once; the
	// replace count arg is the number of slots to fill, not rolls.
	var drops []ItemStack
	var written []ItemStack
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
		drops = append(drops, rollLootTable(table, rand.Int63())...)
	case mineCheck:
		if !s.blockPosLoaded(srcDim, sender, minePos[0], minePos[1], minePos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		wpos := world.Pos{X: minePos[0], Y: minePos[1], Z: minePos[2]}
		blk := srcDim.w.GetBlock(wpos)
		bname, _ := world.StateOwner(blk)
		table = "blocks/" + bname
		tableComp = component{emptyKey: "minecraft:" + table}
		if _, ok := data.LootTables()[table]; !ok {
			return componentReply(commandError("commands.drop.no_loot_table.block",
				component{translate: "block.minecraft." + bname}))
		}
		// Vanilla getDrops takes the tool from the arg (or the
		// source's main hand) and applies no requires-tool gate —
		// that lives in ServerPlayerGameMode.destroyBlock only.
		tool := mineTool
		if mineHand >= 0 {
			tool, _ = sender.stackForHand(int32(mineHand))
		} else if mineHand == -2 {
			tool = sender.heldStack()
		}
		ctx := &lootCtx{
			tool:   tool,
			block:  bname,
			state:  blk,
			pos:    wpos,
			dim:    srcDim,
			player: sender,
		}
		ctx.rng = rand.New(rand.NewSource(rand.Int63()))
		drops = append(drops, rollLootDepth(ctx, table, 0)...)
	case fishCheck:
		if !s.blockPosLoaded(srcDim, sender, fishPos[0], fishPos[1], fishPos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		drops = append(drops, rollLootTable(table, rand.Int63())...)
	default:
		drops = append(drops, rollLootTable(table, rand.Int63())...)
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
		slots, mu, ok := s.lootBlockSlots(srcDim, pos)
		if !ok {
			return componentReply(commandError("commands.item.target.not_a_container",
				int32(tgt.pos[0]), int32(tgt.pos[1]), int32(tgt.pos[2])))
		}
		mu.Lock()
		for _, st := range drops {
			moving := st
			for i := range slots {
				if insertStack(&slots[i], &moving) {
					break
				}
			}
		}
		mu.Unlock()
	case 3: // replace entity — vanilla setSlots fills the count
		// consecutive slot ids from the start, empties past the
		// produced stacks; the written list drives the reply. No
		// count arg means exactly the produced stack count.
		count := tgt.count
		if !tgt.hasCount {
			count = len(drops)
		}
		for _, e := range tgt.entities {
			if e.sess == nil {
				continue
			}
			for i := 0; i < count; i++ {
				var put ItemStack
				if i < len(drops) {
					put = drops[i]
				}
				if s.writeEntitySlot(e.sess, tgt.slot+int32(i), put) {
					written = append(written, put)
				}
			}
		}
	case 4: // replace block — same consecutive-slot fill.
		if !s.blockPosLoaded(srcDim, sender, tgt.pos[0], tgt.pos[1], tgt.pos[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
		pos := world.Pos{X: tgt.pos[0], Y: tgt.pos[1], Z: tgt.pos[2]}
		slots, mu, ok := s.lootBlockSlots(srcDim, pos)
		if !ok {
			return componentReply(commandError("commands.item.target.not_a_container",
				int32(tgt.pos[0]), int32(tgt.pos[1]), int32(tgt.pos[2])))
		}
		if tgt.slot < 0 || int(tgt.slot) >= len(slots) {
			return componentReply(commandError("commands.item.target.no_such_slot", tgt.slot))
		}
		count := tgt.count
		if !tgt.hasCount {
			count = len(drops)
		}
		mu.Lock()
		for i := 0; i < count; i++ {
			n := int(tgt.slot) + i
			if n < 0 || n >= len(slots) {
				break
			}
			var put ItemStack
			if i < len(drops) {
				put = drops[i]
			}
			slots[n] = put
			written = append(written, put)
		}
		mu.Unlock()
	}

	// Reply: replace reports the written stacks, everything else the
	// produced drops. multiple's arg is the stack count, not items.
	list := drops
	if tgt.kind == 3 || tgt.kind == 4 {
		list = written
	}
	if killOk || mineCheck {
		if len(list) == 1 {
			return opsEchoReply(translatable("commands.drop.success.single_with_table",
				int32(list[0].Count), itemBrackets(list[0]), tableComp))
		}
		return opsEchoReply(translatable("commands.drop.success.multiple_with_table",
			int32(len(list)), tableComp))
	}
	if len(list) == 1 {
		return opsEchoReply(translatable("commands.drop.success.single",
			int32(list[0].Count), itemBrackets(list[0])))
	}
	return opsEchoReply(translatable("commands.drop.success.multiple",
		int32(len(list))))
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
	ents, r := s.selectDataEntities(sender, tok, tokIdx, single, nil)
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

// lootBlockSlots resolves a /loot block target (insert, replace block)
// to the live slot storage. Chests, furnaces, and hoppers keep their
// own stores; generic menu blocks share containerData. The returned
// mutex guards the store's map; lock it around writes.
func (s *Server) lootBlockSlots(d *dimension, pos world.Pos) ([]ItemStack, *sync.Mutex, bool) {
	blk := d.w.GetBlock(pos)
	switch {
	case isChestBlock(blk):
		c := s.chestAt(d, pos)
		return c.slots[:], &d.chests.mu, true
	case isFurnaceBlock(blk):
		f := s.furnaceAt(d, pos)
		return f.slots[:], &d.furnaces.mu, true
	case isHopperBlock(blk):
		h := s.hopperAt(d, pos)
		return h.slots[:], &d.hoppers.mu, true
	}
	if _, ok := containerSpecFor(blk); !ok {
		return nil, nil, false
	}
	c := s.containerAt(d, pos)
	if c == nil {
		return nil, nil, false
	}
	return c.slots, &d.containers.mu, true
}
