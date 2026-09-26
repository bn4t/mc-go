package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// golemOnPlace is CarvedPumpkinBlock.onPlace /
// WitherSkullBlock.checkSpawn: a block write that completes a golem
// pattern spawns the entity, airs every pattern cell with a 2001
// block-crackle level event, and notifies neighbours. Vanilla tries
// the three pumpkin patterns in snow, iron, copper order; the wither
// T runs off the skull block's own hook.
//
// Every pattern is a vertical aisle — the pumpkin/skull row on top —
// that can rotate around Y so a crossbar runs along X or Z. `~`
// cells must be air.
func (s *Server) golemOnPlace(d *dimension, pos world.Pos, blk world.Block) bool {
	name, ok := world.StateOwner(blk)
	if !ok {
		return false
	}
	switch strings.TrimPrefix(name, "minecraft:") {
	case "carved_pumpkin", "jack_o_lantern":
		if s.golemTrySnow(d, pos) {
			return true
		}
		if s.golemTryIron(d, pos) {
			return true
		}
		return s.golemTryCopper(d, pos, blk)
	}
	// WitherSkullBlock.checkSpawn hangs off setPlacedBy — an item-place
	// hook — so a /setblock'd skull never spawns the wither on vanilla.
	// place.go calls golemTryWither directly from the use-item path.
	return false
}

// blockAt is the name lookup a pattern cell needs.
func blockAt(w *world.World, pos world.Pos) string {
	n, _ := world.StateOwner(w.GetBlock(pos))
	return strings.TrimPrefix(n, "minecraft:")
}

// isAirAt reports whether the cell is empty — the `~` predicate.
func isAirAt(w *world.World, pos world.Pos) bool {
	return w.GetBlock(pos) == world.Air
}

// golemSpawn clears the pattern cells with vanilla's 2001 crackle
// events and spawns the mob at the anchor cell's bottom-center —
// spawnGolemInWorld snaps golems to +0.5/+0.05/+0.5 while the wither's
// own spawn path lifts it to +0.55.
func (s *Server) golemSpawn(d *dimension, cells []world.Pos, anchor world.Pos, yoff float64, mob string) {
	for _, c := range cells {
		old := d.w.GetBlock(c)
		d.w.SetBlock(c, world.Air)
		s.broadcastBlockState(d, c, world.Air)
		s.broadcastLevelEvent(d.name, 2001, c, int32(old))
	}
	s.summonMob(d, mob,
		float64(anchor.X)+0.5, float64(anchor.Y)+yoff, float64(anchor.Z)+0.5,
		false)
	// The summoned_entity trigger fires on every BlockPattern spawn —
	// setblock-, fill-, dispenser-, or use-placed heads alike — and
	// grants to every player in the dimension (vanilla's
	// CriteriaTriggers.SUMMONED_ENTITY fans out over the player list,
	// not the placer). Only summon_iron_golem uses it today; other mob
	// names filter out inside fireSummonedEntity.
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sess.dimName() == d.name {
			s.fireSummonedEntity(sess, mob)
		}
		return true
	})
}

// golemTrySnow is the snowGolemFull aisle "^","#","#": pumpkin over
// two snow blocks. The golem stands at the bottom cell.
func (s *Server) golemTrySnow(d *dimension, pos world.Pos) bool {
	b1 := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	b2 := world.Pos{X: pos.X, Y: pos.Y - 2, Z: pos.Z}
	if blockAt(d.w, b1) != "snow_block" || blockAt(d.w, b2) != "snow_block" {
		return false
	}
	s.golemSpawn(d, []world.Pos{pos, b1, b2}, b2, 0.05, "snow_golem")
	return true
}

// golemTryIron is the ironGolemFull aisle "~^~","###","~#~": the
// crossbar may run along X or Z, the top corners and the row under
// the ends must be air.
func (s *Server) golemTryIron(d *dimension, pos world.Pos) bool {
	for _, ax := range [2]bool{true, false} { // true: crossbar on X
		off := func(i int) world.Pos {
			if ax {
				return world.Pos{X: pos.X + i, Y: pos.Y, Z: pos.Z}
			}
			return world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + i}
		}
		mid, low := func(i int) world.Pos {
			p := off(i)
			p.Y--
			return p
		}, func(i int) world.Pos {
			p := off(i)
			p.Y -= 2
			return p
		}
		if !isAirAt(d.w, off(-1)) || !isAirAt(d.w, off(1)) {
			continue
		}
		if blockAt(d.w, mid(-1)) != "iron_block" || blockAt(d.w, mid(0)) != "iron_block" || blockAt(d.w, mid(1)) != "iron_block" {
			continue
		}
		if !isAirAt(d.w, low(-1)) || !isAirAt(d.w, low(1)) || blockAt(d.w, low(0)) != "iron_block" {
			continue
		}
		cells := []world.Pos{pos, off(-1), off(1), mid(-1), mid(0), mid(1), low(-1), low(0), low(1)}
		s.golemSpawn(d, cells, low(0), 0.05, "iron_golem")
		return true
	}
	return false
}

// copperToChest is CopperChestBlock.getFromCopperBlock — each
// #minecraft:copper block maps to the same-weathering chest.
var copperToChest = map[string]string{
	"copper_block":           "copper_chest",
	"exposed_copper":         "exposed_copper_chest",
	"weathered_copper":       "weathered_copper_chest",
	"oxidized_copper":        "oxidized_copper_chest",
	"waxed_copper_block":     "waxed_copper_chest",
	"waxed_exposed_copper":   "waxed_exposed_copper_chest",
	"waxed_weathered_copper": "waxed_weathered_copper_chest",
	"waxed_oxidized_copper":  "waxed_oxidized_copper_chest",
}

// golemTryCopper is the copperGolemFull aisle "^","#": pumpkin over
// any #minecraft:copper block. The copper block is REPLACED by the
// matching copper chest (facing copied off the pumpkin) instead of
// being aired, and the golem pops on the chest.
func (s *Server) golemTryCopper(d *dimension, pos world.Pos, pumpkin world.Block) bool {
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	chest, ok := copperToChest[blockAt(d.w, below)]
	if !ok {
		return false
	}
	facing, _ := world.StateProperty(pumpkin, "facing")
	spec := chest
	if facing != "" {
		spec += "[facing=" + facing + "]"
	}
	chestBlk, ok := parseBlockName(spec)
	if !ok {
		return false
	}
	s.golemSpawn(d, []world.Pos{pos, below}, pos, 0.05, "copper_golem")
	d.w.SetBlock(below, chestBlk)
	s.broadcastBlockState(d, below, chestBlk)
	// The copper chest becomes a block entity — same as a placed
	// chest — so it lands in the container index.
	s.containerAt(d, below)
	return true
}

// golemTryWither is the WitherSkullBlock pattern "^^^","###","~#~":
// three wither skeleton skulls in a row over a soul_sand/soul_soil T,
// on either axis, with the placed skull filling any of the three
// head cells. Souls under the ends are required; the cells under the
// outer skulls must be air.
func (s *Server) golemTryWither(d *dimension, pos world.Pos) bool {
	soul := func(n string) bool { return n == "soul_sand" || n == "soul_soil" }
	skull := func(n string) bool {
		return n == "wither_skeleton_skull" || n == "wither_skeleton_wall_skull"
	}
	for _, ax := range [2]bool{true, false} {
		for _, role := range [3]int{-1, 0, 1} { // placed skull's slot in the row
			center := pos
			if ax {
				center.X -= role
			} else {
				center.Z -= role
			}
			cell := func(row, i int) world.Pos {
				p := center
				p.Y -= row
				if ax {
					p.X += i
				} else {
					p.Z += i
				}
				return p
			}
			if !skull(blockAt(d.w, cell(0, -1))) || !skull(blockAt(d.w, cell(0, 0))) || !skull(blockAt(d.w, cell(0, 1))) {
				continue
			}
			if !soul(blockAt(d.w, cell(1, -1))) || !soul(blockAt(d.w, cell(1, 0))) || !soul(blockAt(d.w, cell(1, 1))) {
				continue
			}
			if !isAirAt(d.w, cell(2, -1)) || !isAirAt(d.w, cell(2, 1)) || !soul(blockAt(d.w, cell(2, 0))) {
				continue
			}
			cells := []world.Pos{
				cell(0, -1), cell(0, 0), cell(0, 1),
				cell(1, -1), cell(1, 0), cell(1, 1),
				cell(2, -1), cell(2, 0), cell(2, 1),
			}
			s.golemSpawn(d, cells, cell(0, 0), 0.55, "wither")
			return true
		}
	}
	return false
}

// snowMeltBiomes is the biome-level SNOW_GOLEM_MELTS=true set in the
// 26.2 datapack; the nether sets the same env attribute for the whole
// dimension.
var snowMeltBiomes = map[string]bool{
	"badlands": true, "desert": true, "eroded_badlands": true,
	"savanna": true, "savanna_plateau": true,
	"windswept_savanna": true, "wooded_badlands": true,
}

// snowGolemStep is SnowGolem.aiStep — runs on every living tick like
// the daylight burn (aiStep has no isEffectiveAi gate, so NoAI snow
// golems still trail and melt): the mobGriefing-gated trail drops a
// snow_layer in any air cell underfoot where a layer can survive
// (solid ground below), and the SNOW_GOLEM_MELTS env attribute ticks
// 1 fire damage in the hot-biome set and the nether.
func (s *Server) snowGolemStep(e *Entity, d *dimension, w *world.World) {
	if d == nil || w == nil {
		return
	}
	feet := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y)),
		Z: int(math.Floor(e.Z)),
	}
	if e.Dim == "the_nether" || snowMeltBiomes[s.biomeAt(d, feet)] {
		s.damageMob(e, 1, "on_fire", nil, &damageInfo{typeName: "on_fire"})
	}
	// Vanilla's aiStep has no onGround gate — a floating snow golem
	// still trails wherever the layer can survive (solid below).
	if !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	below := world.Pos{X: feet.X, Y: feet.Y - 1, Z: feet.Z}
	if !isAirAt(w, feet) || world.NonSolid(w.GetBlock(below)) {
		return
	}
	snow, ok := parseBlockName("minecraft:snow")
	if !ok {
		return
	}
	d.w.SetBlock(feet, snow)
	s.broadcastBlockState(d, feet, snow)
}
