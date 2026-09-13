package server

import (
	"sort"

	"mc-go/internal/protocol"
)

// Vanilla-ordered join burst encoders. Field shapes follow the 776
// protocol docs; TestJoinBurstSequence compares the resulting packet
// order with the real server.

// sendSetHeldSlot syncs the selected hotbar slot.
func (s *Server) sendSetHeldSlot(sess *Session, slot int32) error {
	var b protocol.Buffer
	b.VarInt(slot)
	return sess.write(sess.ids().PlaySetHeldItemCB, b.Bytes())
}

// sendUpdateRecipes sends the recipe registry. Empty until the crafting
// milestone; an empty registry is structurally valid.
func (s *Server) sendUpdateRecipes(sess *Session) error {
	var b protocol.Buffer
	b.VarInt(0) // property sets
	b.VarInt(0) // stonecutter recipes
	return sess.write(sess.ids().PlayUpdateRecipes, b.Bytes())
}

// sendEntityEvent triggers one entity status animation/event.
func (s *Server) sendEntityEvent(sess *Session, eid int32, status byte) error {
	var b protocol.Buffer
	b.Int(eid)
	b.Byte(status)
	return sess.write(sess.ids().PlayEntityEvent, b.Bytes())
}

// Command-node flags: type in the low two bits (0 root, 1 literal,
// 2 argument), 0x04 executable, 0x08 redirect, 0x10 suggestions.
const (
	cmdNodeLiteral    = 0x01
	cmdNodeExecutable = 0x04
)

// sendCommands sends the Brigadier command tree: a root with one
// executable literal per registered command. Argument parsers arrive
// with the full command milestone; literals let the client suggest and
// complete exact command names today.
func (s *Server) sendCommands(sess *Session) error {
	names := s.commands.Names()
	sort.Strings(names)

	var b protocol.Buffer
	b.VarInt(int32(len(names) + 1)) // node count: root + one per command
	// Root node.
	b.Byte(0)
	b.VarInt(int32(len(names)))
	for i := range names {
		b.VarInt(int32(i + 1))
	}
	// Literal nodes, in the same order.
	for _, name := range names {
		b.Byte(cmdNodeLiteral | cmdNodeExecutable)
		b.VarInt(0) // children count
		b.String(name)
	}
	b.VarInt(0) // root index
	return sess.write(sess.ids().PlayCommands, b.Bytes())
}

// sendRecipeBookSettings syncs the four recipe book UI toggles.
func (s *Server) sendRecipeBookSettings(sess *Session) error {
	var b protocol.Buffer
	for i := 0; i < 8; i++ {
		b.Bool(false)
	}
	return sess.write(sess.ids().PlayRecipeBookSet, b.Bytes())
}

// sendRecipeBookAdd syncs known recipes. Empty with replace=false until
// crafting lands.
func (s *Server) sendRecipeBookAdd(sess *Session) error {
	var b protocol.Buffer
	b.VarInt(0) // recipes
	b.Bool(true)
	return sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
}

// sendServerData sends the MOTD (and no icon).
func (s *Server) sendServerData(sess *Session) error {
	var b protocol.Buffer
	b.Raw(textComponentContent(s.MOTD))
	b.Bool(false) // no icon
	return sess.write(sess.ids().PlayServerData, b.Bytes())
}

// sendInitializeBorder sends the world border state: full vanilla
// border at the origin, static, with default warnings.
func (s *Server) sendInitializeBorder(sess *Session) error {
	const diameter = 59999968.0
	var b protocol.Buffer
	b.Double(0) // center X
	b.Double(0) // center Z
	b.Double(diameter)
	b.Double(diameter)
	b.VarLong(0)       // speed
	b.VarInt(29999984) // portal teleport boundary
	b.VarInt(5)        // warning blocks
	b.VarInt(300)      // warning time
	return sess.write(sess.ids().PlayInitializeBorder, b.Bytes())
}

// sendUpdateTime sends the world age and clock states. Two clocks
// (overworld, the_end) tick with the world, matching vanilla's flat
// world setup.
func (s *Server) sendUpdateTime(sess *Session) error {
	age := s.worldAgeTicks()
	var b protocol.Buffer
	b.Long(age)
	b.VarInt(2)
	for id := int32(0); id < 2; id++ {
		b.VarInt(id)
		b.VarLong(age)
		b.Float(0) // fractional time
		b.Float(1) // rate: clock ticks per client tick
	}
	return sess.write(sess.ids().PlayUpdateTime, b.Bytes())
}

// sendDefaultSpawn syncs the world spawn point.
func (s *Server) sendDefaultSpawn(sess *Session) error {
	x, y, z := s.world.Spawn()
	var b protocol.Buffer
	b.String("minecraft:overworld")
	b.Position(int32(x-0.5), int32(y+1), int32(z-0.5))
	b.Float(0)
	b.Float(0)
	return sess.write(sess.ids().PlayDefaultSpawn, b.Bytes())
}

// sendGameEvent emits one game event; 13 starts the client's chunk
// wait, which vanilla sends after clearing the level.
func (s *Server) sendGameEvent(sess *Session, event byte, value float32) error {
	var b protocol.Buffer
	b.Byte(event)
	b.Float(value)
	return sess.write(sess.ids().PlayGameEvent, b.Bytes())
}

// sendTickingState syncs the server tick rate.
func (s *Server) sendTickingState(sess *Session) error {
	var b protocol.Buffer
	b.Float(20)
	b.Bool(false)
	return sess.write(sess.ids().PlayTickingState, b.Bytes())
}

// sendTickingStep syncs the tick freeze step.
func (s *Server) sendTickingStep(sess *Session, step int32) error {
	var b protocol.Buffer
	b.VarInt(step)
	return sess.write(sess.ids().PlayTickingStep, b.Bytes())
}

// sendEntityHealthMetadata syncs the living-entity health metadata
// entry (index 9, float type 3).
func (s *Server) sendEntityHealthMetadata(sess *Session, eid int32, health float32) error {
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(9) // index: health
	b.Byte(3) // type: float
	b.Float(health)
	b.Byte(0xFF) // metadata terminator
	return sess.write(sess.ids().PlayEntityMetadata, b.Bytes())
}

// sendSetExperience syncs the XP bar, level, and total.
func (s *Server) sendSetExperience(sess *Session, bar float32, level, total int32) error {
	var b protocol.Buffer
	b.Float(bar)
	b.VarInt(level)
	b.VarInt(total)
	return sess.write(sess.ids().PlaySetExperience, b.Bytes())
}

// sendSetHealth syncs health, food level, and saturation.
func (s *Server) sendSetHealth(sess *Session, health float32, food int32, saturation float32) error {
	var b protocol.Buffer
	b.Float(health)
	b.VarInt(food)
	b.Float(saturation)
	return sess.write(sess.ids().PlaySetHealth, b.Bytes())
}

// sendUpdateAdvancements syncs advancement progress. Empty for now.
func (s *Server) sendUpdateAdvancements(sess *Session) error {
	var b protocol.Buffer
	b.Bool(false) // reset
	b.VarInt(0)   // advancement mapping
	b.VarInt(0)   // identifiers to remove
	b.VarInt(0)   // progress mapping
	b.Bool(false) // show advancements
	return sess.write(sess.ids().PlayUpdateAdvance, b.Bytes())
}
