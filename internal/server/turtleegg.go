package server

import (
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// TurtleEggBlock trample mechanics, measured from 26.2 bytecode:
//
//	fallOn:  !(entity instanceof Zombie) -> destroyEgg(chance=3)
//	         — the whole Zombie class family (zombie, husk, drowned,
//	         zombie_villager, zombified_piglin) skips the landing
//	         roll; they break eggs by trampling instead.
//	stepOn:  !entity.isSteppingCarefully() -> destroyEgg(chance=100)
//	         — applyEffectsFromBlocks runs the support block's
//	         stepOn every grounded tick, so a mob standing on eggs
//	         rolls 1% per tick; sneaking entities are exempt.
//	destroyEgg: state is turtle_egg && ServerLevel &&
//	         canDestroyEgg && rand.nextInt(chance) == 0 ->
//	         decreaseEggs.
//	canDestroyEgg: LivingEntity && !(Turtle || Bat) &&
//	         (Player || mobGriefing).
//	decreaseEggs: playSound(pos, TURTLE_EGG_BREAK, BLOCKS, 0.7,
//	         0.9+rand*0.2) — then eggs>1: setBlock(eggs-1, flag 2)
//	         + gameEvent(BLOCK_DESTROY, old state) + levelEvent
//	         (2001, old state id); eggs==1: destroyBlock(pos, no
//	         drops) whose own levelEvent(2001) carries the break.

// turtleEggCanDestroy is TurtleEggBlock.canDestroyEgg: the entity
// must be a LivingEntity that is neither a turtle nor a bat, and
// non-player entities additionally need mobGriefing. e and sess
// are alternative entity handles — a mob record or a player.
func (s *Server) turtleEggCanDestroy(e *Entity, sess *Session) bool {
	if e == nil && sess == nil {
		return false
	}
	if sess != nil {
		return true // players are LivingEntity and pass the rule gate
	}
	name, _ := EntityTypeName(e.Type)
	if name == "turtle" || name == "bat" {
		return false
	}
	return s.gameRuleBool("minecraft:mob_griefing")
}

// turtleEggDestroy is TurtleEggBlock.destroyEgg: the block must
// still be a turtle egg, the entity must pass canDestroyEgg, and
// rand.nextInt(chance) has to land on zero.
func (s *Server) turtleEggDestroy(d *dimension, pos world.Pos, e *Entity, sess *Session, chance int) {
	b := d.w.GetBlock(pos)
	name, _ := world.StateOwner(b)
	if strings.TrimPrefix(name, "minecraft:") != "turtle_egg" {
		return
	}
	if !s.turtleEggCanDestroy(e, sess) {
		return
	}
	if rand.Intn(chance) != 0 {
		return
	}
	s.decreaseTurtleEggs(d, pos, b)
}

// turtleEggStepOn is TurtleEggBlock.stepOn for a mob: the support
// block's stepOn roll. Mobs never step carefully, so the gate is
// the raw nextInt(100) roll.
func (s *Server) turtleEggStepOn(d *dimension, pos world.Pos, e *Entity) {
	s.turtleEggDestroy(d, pos, e, nil, 100)
}

// decreaseTurtleEggs is TurtleEggBlock.decreaseEggs: the break
// sound first, then eggs>1 writes eggs-1 with the BLOCK_DESTROY
// game event and the 2001 break particles riding the OLD state id;
// a last egg goes through destroyBlock, whose own levelEvent(2001)
// is the particles + block break sound.
func (s *Server) decreaseTurtleEggs(d *dimension, pos world.Pos, old world.Block) {
	x, y, z := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	s.playSoundSrc(d.name, x, y, z, 1659, 4, 0.7, 0.9+rand.Float32()*0.2, nil)
	eggsS, _ := world.StateProperty(old, "eggs")
	eggs, _ := strconv.Atoi(eggsS)
	if eggs <= 1 {
		s.setNaturalBlock(d, pos, world.Air)
		s.emitGameEvent(d, "block_destroy", x, y, z, nil, "turtle_egg")
		s.broadcastLevelEvent(d.name, 2001, pos, int32(old))
		return
	}
	hatch, _ := world.StateProperty(old, "hatch")
	if nb, ok := world.BlockStateOf("turtle_egg", map[string]string{
		"eggs":  strconv.Itoa(eggs - 1),
		"hatch": hatch,
	}); ok {
		s.setNaturalBlock(d, pos, nb)
	}
	s.emitGameEvent(d, "block_destroy", x, y, z, nil, "turtle_egg")
	s.broadcastLevelEvent(d.name, 2001, pos, int32(old))
}
