package server

import (
	_ "embed"
	"sort"
	"time"

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

//go:embed testdata/vanilla-26.2/play/update_recipes.bin
var updateRecipesBody []byte

// sendUpdateRecipes sends the recipe registry: the seven property sets
// (furnace, smoker, blast furnace and campfire inputs, smithing base,
// addition and template) and the 319 stonecutting recipes, captured
// from a default live 26.2 server and served verbatim like the
// configuration registries. The registry is static per version (mc-go
// has no data-pack reload), and TestRecipeBurstParity compares the
// bytes against the live server on every run. The furnace_input set
// decodes to exactly the data pack's smelting inputs, which is the
// check the capture is the default registry and not server state.
func (s *Server) sendUpdateRecipes(sess *Session) error {
	return sess.write(sess.ids().PlayUpdateRecipes, updateRecipesBody)
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
	// The tree carries only the commands the player's level may run —
	// vanilla builds the client the same filtered view.
	level := sess.opLevel()
	all := s.helpNames()
	names := all[:0]
	for _, n := range all {
		if commandLevel(n) <= level {
			names = append(names, n)
		}
	}
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

// sendRecipeBookSettings syncs the recipe book UI toggles — the
// restored per-category open/filtering flags for a returning player.
func (s *Server) sendRecipeBookSettings(sess *Session) error {
	return s.sendRecipeBookSettingsFor(sess)
}

// sendRecipeBookAdd syncs known recipes: empty with replace for a
// fresh player, which is what the real server sends at this point in
// the burst, or the returning player's persisted book — each entry
// with flags 0 (known recipes carry no toast and no new badge).
func (s *Server) sendRecipeBookAdd(sess *Session) error {
	sess.recipesMu.Lock()
	idxs := make([]int, 0, len(sess.unlockedRecipes))
	for idx := range sess.unlockedRecipes {
		idxs = append(idxs, idx)
	}
	sess.recipesMu.Unlock()
	sort.Ints(idxs)
	var b, entries protocol.Buffer
	n := 0
	for _, idx := range idxs {
		n += recipeEntry(&entries, idx, 0)
	}
	b.VarInt(int32(n))
	b.Raw(entries.Bytes())
	b.Bool(true)
	return sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
}

//go:embed testdata/vanilla-26.2/play/recipe_book_unlock.bin
var recipeBookUnlock []byte

// sendRecipeBookUnlock syncs the fresh-spawn recipe unlock: the single
// crafting_table entry (display id 318), captured from a default live
// 26.2 server and served verbatim. The bytes are boot-stable; later
// unlocks arrive as their own adds once the unlock engine exists. A
// returning player whose persisted book already contains the recipe
// got it inside sendRecipeBookAdd instead.
func (s *Server) sendRecipeBookUnlock(sess *Session) error {
	if idx, ok := recipeIndex["crafting_table"]; ok {
		sess.recipesMu.Lock()
		known := sess.unlockedRecipes[idx]
		sess.recipesMu.Unlock()
		if known {
			return nil
		}
	}
	sess.markRecipesUnlocked("crafting_table")
	return sess.write(sess.ids().PlayRecipeBookAdd, recipeBookUnlock)
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
	bd := s.borderFor(sess.dimName())
	var b protocol.Buffer
	b.Double(bd.centerX)
	b.Double(bd.centerZ)
	b.Double(bd.size)
	b.Double(bd.size)
	b.VarLong(0)       // speed
	b.VarInt(29999984) // portal teleport boundary
	b.VarInt(bd.warnBlocks)
	b.VarInt(bd.warnTime)
	return sess.write(sess.ids().PlayInitializeBorder, b.Bytes())
}

// sendUpdateTime sends the join-time clock state, matching the real
// server field for field on a young world: the world age, then two
// clock entries - the overworld clock (id 0) carrying the day time
// and the the_end clock (id 1) carrying the age - each with a
// fractional part and a rate of one tick per tick. The 26.2
// world_clock registry has only those two entries; a clock id
// outside it kicks the client on decode. (Measured with day != age:
// id 0 took the day time, id 1 the age.)
func (s *Server) sendUpdateTime(sess *Session) error {
	return sess.write(sess.ids().PlayUpdateTime, s.updateTimeBody())
}

// updateTimeBody builds the join-time Update Time body.
func (s *Server) updateTimeBody() []byte {
	age := s.worldAgeTicks()
	var b protocol.Buffer
	b.Long(age)
	b.VarInt(2)
	b.VarInt(0)
	b.VarLong(s.dayTime())
	b.Float(0) // fractional time
	b.Float(1) // rate: clock ticks per client tick
	b.VarInt(1)
	b.VarLong(age)
	b.Float(0)
	b.Float(1)
	return b.Bytes()
}

// updateTimeTickBody builds the clockless periodic Update Time: the
// world age with no clock entries, which is what the real server
// broadcasts once per second (9 bytes, age advancing 20 ticks a
// second).
func (s *Server) updateTimeTickBody() []byte {
	var b protocol.Buffer
	b.Long(s.worldAgeTicks())
	b.VarInt(0)
	return b.Bytes()
}

// updateTimeSetBody builds the /time set and /time add broadcast: the
// world age with the single day clock (id 0) at the new day time.
func (s *Server) updateTimeSetBody(day int64) []byte {
	var b protocol.Buffer
	b.Long(s.worldAgeTicks())
	b.VarInt(1)
	b.VarInt(0)
	b.VarLong(day)
	b.Float(0)
	b.Float(1)
	return b.Bytes()
}

// broadcastUpdateTime pushes the clockless tick to every play-stage
// session: the once-a-second sync.
func (s *Server) broadcastUpdateTime() {
	s.broadcastUpdateTimeBody(s.updateTimeTickBody())
}

// broadcastTimeSet pushes a /time set/add day change to every
// play-stage session.
func (s *Server) broadcastTimeSet(day int64) {
	s.broadcastUpdateTimeBody(s.updateTimeSetBody(day))
}

// broadcastUpdateTimeBody fans one Update Time body out to every
// play-stage session.
func (s *Server) broadcastUpdateTimeBody(body []byte) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay {
			_ = sess.write(sess.ids().PlayUpdateTime, body)
		}
		return true
	})
}

// sendDefaultSpawn syncs the world spawn point, including any angle a
// /setworldspawn set.
func (s *Server) sendDefaultSpawn(sess *Session) error {
	s.spawnMu.Lock()
	pos, yaw, pitch := s.spawnPos, s.spawnYaw, s.spawnPitch
	have := s.haveSpawn
	s.spawnMu.Unlock()
	if !have {
		x, y, z := s.WorldSpawn()
		pos = [3]int{int(x - 0.5), int(y + 1), int(z - 0.5)}
	}
	var b protocol.Buffer
	b.String("minecraft:overworld")
	b.Position(int32(pos[0]), int32(pos[1]), int32(pos[2]))
	b.Float(yaw)
	b.Float(pitch)
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

//go:embed testdata/vanilla-26.2/play/advancements.bin
var advancementsBody []byte

// The Update Advancements capture is static except for one field: the
// award timestamp of the crafting_table criterion, a big-endian long at
// these offsets. Measured layout of the 275 bytes: reset, the two
// recipe advancement definitions, remove-count, then the progress map
// (recipes/root locked on impossible; crafting_table with
// unlock_right_away achieved and has_the_recipe locked), then show.
// Only the progress entry order varies per boot, so the timestamp sits
// at one fixed offset in this capture; buildAdvancements splices the
// live time in and advancementSplitTest pins the splice.
const (
	advancementTimeAt = 250
	advancementTimeLn = 8
)

// buildAdvancements returns the fresh-join advancement sync with the
// criterion award time set to nowMillis, matching vanilla's invariant
// that a fresh player's recipe advancements date from the join.
func buildAdvancements(nowMillis int64) []byte {
	var b protocol.Buffer
	b.Raw(advancementsBody[:advancementTimeAt])
	b.Long(nowMillis)
	b.Raw(advancementsBody[advancementTimeAt+advancementTimeLn:])
	return b.Bytes()
}

// sendUpdateAdvancements syncs advancement progress. Fresh players get
// the two recipe advancements (root plus crafting_table), with a live
// award timestamp.
func (s *Server) sendUpdateAdvancements(sess *Session) error {
	now := time.Now().UnixMilli()
	sess.initAdvancements(now)
	return sess.write(sess.ids().PlayUpdateAdvance, buildAdvancements(now))
}
