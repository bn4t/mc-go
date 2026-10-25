package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// catMorningGift is the cat_morning_gift loot pool — one roll of
// rabbit_hide/rabbit_foot/chicken/feather/rotten_flesh/string at
// weight 10 and phantom_membrane at weight 2 (total 62).
var catMorningGiftTable = []struct {
	name   string
	weight int
}{
	{"rabbit_hide", 10},
	{"rabbit_foot", 10},
	{"chicken", 10},
	{"feather", 10},
	{"rotten_flesh", 10},
	{"string", 10},
	{"phantom_membrane", 2},
}

// catGiftChance is the minecraft:gameplay/cat_waking_up_gift_chance
// environment attribute on the vanilla day timeline: constant 0.7
// from tick 23667 through 362 (the night window), 0.0 in daylight —
// the maximum modifier picks the higher keyframe. A wake gift roll
// lands inside the 0.7 span because the skip always fires at dawn.
func (s *Server) catGiftChance() float64 {
	day := s.dayTime() % 24000
	if day >= 23667 || day < 362 {
		return 0.7
	}
	return 0.0
}

// catBedGoalPos is the goal's target pos: ownerPos (the bed head the
// sleeper lies on) offset one cell in FACING's opposite direction —
// onto the foot half — or the head itself when the state exposes no
// facing.
func (s *Server) catBedGoalPos(owner *Session) (world.Pos, bool) {
	head, ok := owner.sleepingPos.Load().(world.Pos)
	if !ok || head == (world.Pos{}) {
		return world.Pos{}, false
	}
	w := s.dimWorld(owner.dimension().name)
	if w == nil {
		return head, true
	}
	cur := w.GetBlock(head)
	switch face, _ := world.StateProperty(cur, "facing"); face {
	case "north":
		head.Z++
	case "south":
		head.Z--
	case "west":
		head.X++
	case "east":
		head.X--
	}
	return head, true
}

// catSpaceOccupied is the goal's spaceIsOccupied: another cat already
// lying or relaxing inside the bed-goal cell inflated by two; the
// goal's own cat is skipped.
func (s *Server) catSpaceOccupied(self *Entity, goal world.Pos) bool {
	x0, x1 := float64(goal.X)-2, float64(goal.X)+3
	y0, y1 := float64(goal.Y)-2, float64(goal.Y)+3
	z0, z1 := float64(goal.Z)-2, float64(goal.Z)+3
	occupied := false
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o == self || o.Dim != self.Dim {
			continue
		}
		if on, _ := EntityTypeName(o.Type); on != "cat" {
			continue
		}
		if !o.CatLying && !o.CatRelax {
			continue
		}
		if o.X >= x0 && o.X <= x1 && o.Y >= y0 && o.Y <= y1 && o.Z >= z0 && o.Z <= z1 {
			occupied = true
			break
		}
	}
	s.entities.mu.RUnlock()
	return occupied
}

// catMorningGiftDrop spawns the gift item entity at the cat — the
// lambda in giveMorningGift builds an ItemEntity at the cat's block
// position after the small random teleport.
func (s *Server) catMorningGiftDrop(e *Entity) {
	roll, total := rand.Intn(62), 0
	pick := "string"
	for _, ent := range catMorningGiftTable {
		total += ent.weight
		if roll < total {
			pick = ent.name
			break
		}
	}
	id, ok := world.ItemIDs[pick]
	if !ok {
		return
	}
	s.spawnItemDrop(e.Dim, world.Pos{X: int(e.X), Y: int(e.Y), Z: int(e.Z)},
		ItemStack{ID: int32(id), Count: 1})
}

// catRelaxStep is Cat$CatRelaxOnOwnerGoal: a tamed, unsitting cat whose
// owner player is sleeping within distanceToSqr<100 paths to the bed
// head's facing offset. While the cat is within 1.58 blocks of the
// owner it settles — RELAX_STATE_ONE (22) for the first 16 ticks,
// IS_LYING (21) after — and stop() clears both when the owner wakes,
// the sit flag flips, or another lying cat takes the spot. An owner
// who slept ≥100 ticks rolls the waking-up gift. Runs inside the
// effective-AI gate like every goal; the flags themselves are
// Cat's entityData.
func (s *Server) catRelaxStep(e *Entity) bool {
	owner := s.sessionByName(e.owner)
	if e.CatGoalSet {
		if owner == nil || !owner.sleeping.Load() || e.Sitting || !e.tamed ||
			s.catSpaceOccupied(e, e.CatGoalPos) {
			// stop(): lying clears first, then the gift roll on
			// sleepTimer>=100, then relax clears and the walk ends.
			if e.CatLying || e.CatRelax {
				e.CatLying, e.CatRelax = false, false
				e.MetaDirty = true
			}
			if owner != nil && owner.sleepTicks.Load() >= 100 &&
				rand.Float64() < s.catGiftChance() {
				s.catMorningGiftDrop(e)
			}
			e.CatOnBedTicks, e.CatGoalSet = 0, false
			return false
		}
		ox, oy, oz, _, _, _ := owner.Pos()
		dx, dy, dz := ox-e.X, oy-e.Y, oz-e.Z
		if dx*dx+dy*dy+dz*dz < 2.5 {
			e.CatOnBedTicks++
			if e.CatOnBedTicks > 16 {
				if !e.CatLying {
					e.CatLying = true
					e.MetaDirty = true
				}
				if e.CatRelax {
					e.CatRelax = false
					e.MetaDirty = true
				}
			} else {
				// lookAt(owner,45,45) — face the sleeper while
				// settling in.
				e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
				e.HeadYaw = e.Yaw
				if !e.CatRelax {
					e.CatRelax = true
					e.MetaDirty = true
				}
			}
		} else if e.CatLying {
			e.CatLying = false
			e.MetaDirty = true
		}
		// PathNavigation.moveTo(goalPos,1.1) continues while the
		// goal runs: walk until the goal cell centre is reached.
		gx, gz := float64(e.CatGoalPos.X)+0.5, float64(e.CatGoalPos.Z)+0.5
		if gdx, gdz := gx-e.X, gz-e.Z; math.Hypot(gdx, gdz) > 0.15 {
			speed := 0.3
			if mv, ok := entityAttrBases["cat"]["movement_speed"]; ok {
				speed = mv
			}
			s.mobWalkToSpeed(e, gx, gz, 1.1*speed)
		}
		return true
	}
	if !e.tamed || e.Sitting || owner == nil || !owner.sleeping.Load() {
		return false
	}
	ox, oy, oz, _, _, _ := owner.Pos()
	dx, dy, dz := ox-e.X, oy-e.Y, oz-e.Z
	if dx*dx+dy*dy+dz*dz >= 100 {
		return false
	}
	goal, ok := s.catBedGoalPos(owner)
	if !ok || s.catSpaceOccupied(e, goal) {
		return false
	}
	e.CatGoalPos, e.CatGoalSet = goal, true
	return true
}
