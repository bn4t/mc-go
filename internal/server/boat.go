package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// useBoatItem is BoatItem.use through ServerPlayerGameMode.useItem: a
// POV clip that stops on any fluid; a miss passes, and so does a use
// whose eye sits inside another pickable entity's box (pick radius 0)
// near the view. A block hit spawns the boat at the hit location with
// the player's yaw unless its box collides with blocks or a
// collidable entity (FAIL); survival consumes one and the use counts
// item_used.
func (s *Server) useBoatItem(sess *Session, hand int32, yaw, pitch float32, item string) {
	d := sess.dimension()
	if d == nil || sess.mode() == GameModeSpectator {
		return
	}
	hit := s.playerPOVHit(sess, d, clipFluidAny, yaw, pitch)
	if hit.miss {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	eye := [3]float64{x, y + 1.62, z}
	if sess.sneaking {
		eye[1] = y + 1.27
	}
	if s.eyeInsidePickable(d, sess, eye) {
		return
	}
	entName := dispenseBoatEntity[item]
	et, ok := EntityTypeID(entName)
	if !ok {
		return
	}
	e := &Entity{Type: et, Dim: d.name, X: hit.loc[0], Y: hit.loc[1], Z: hit.loc[2], Yaw: yaw}
	dims := entityBBDims(e)
	hw := dims[0] / 2
	if s.boxCollides(d, [6]float64{e.X - hw, e.Y, e.Z - hw, e.X + hw, e.Y + dims[1], e.Z + hw}, 0) {
		return
	}
	spawned := s.entities.Spawn(e)
	s.broadcastEntitySpawn(nil, *spawned)
	stack, slot := sess.stackForHand(hand)
	if sess.mode() != GameModeCreative {
		// The consumed hand stack differs from the one use started
		// with, so useItem writes it back and resends the inventory.
		sess.consumeSlot(slot, 1)
		_ = s.sendContainerContent(sess)
	}
	sess.addItemStat(statUsed, stack.ID, 1)
}

// eyeInsidePickable reports another pickable entity whose box holds the
// eye: living entities and players, vehicles, armor stands, hanging
// entities, end crystals, primed TNT and falling blocks.
func (s *Server) eyeInsidePickable(d *dimension, self *Session, eye [3]float64) bool {
	inside := func(minX, minY, minZ, maxX, maxY, maxZ float64) bool {
		return eye[0] >= minX && eye[0] < maxX && eye[1] >= minY && eye[1] < maxY && eye[2] >= minZ && eye[2] < maxZ
	}
	found := false
	s.sessions.Range(func(_, v any) bool {
		o := v.(*Session)
		if o == self || o.state.Load() != StatePlay || !o.spawned.Load() || o.dimName() != d.name || o.mode() == GameModeSpectator {
			return true
		}
		x, y, z, _, _, _ := o.Pos()
		if inside(x-0.3, y, z-0.3, x+0.3, y+1.8, z+0.3) {
			found = true
			return false
		}
		return true
	})
	if found {
		return true
	}
	playerType := mustEntityType("player")
	for _, e := range s.entities.All() {
		if e.Dim != d.name || e.Type == playerType || e.Dying {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		name = strings.TrimPrefix(name, "minecraft:")
		_, living := entityAttrBases[name]
		switch {
		case living, blocksBuildingType(name):
		case name == "painting", name == "item_frame", name == "glow_item_frame":
		default:
			continue
		}
		dims := entityBBDims(&e)
		hw := dims[0] / 2
		if inside(e.X-hw, e.Y, e.Z-hw, e.X+hw, e.Y+dims[1], e.Z+hw) {
			return true
		}
	}
	return false
}

// boxCollides is the block half of CollisionGetter.noCollision: any
// collision box (empty per the measured collision facts, else
// collisionBoxesOf, unlisted cells a full cube)
// overlapping the box by more than eps on every axis.
func (s *Server) boxCollides(d *dimension, b [6]float64, eps float64) bool {
	for bx := int(math.Floor(b[0])); float64(bx) < b[3]; bx++ {
		for by := int(math.Floor(b[1])) - 1; float64(by) < b[4]; by++ {
			for bz := int(math.Floor(b[2])); float64(bz) < b[5]; bz++ {
				p := world.Pos{X: bx, Y: by, Z: bz}
				st := d.w.GetBlock(p)
				if worldgen.NoCollision(st) {
					continue
				}
				boxes, has := collisionBoxesOf(st)
				if !has {
					boxes = []cellBox{{0, 0, 0, 1, 1, 1}}
				}
				for _, c := range boxes {
					if b[3] > float64(bx)+c[0]+eps && b[0] < float64(bx)+c[3]-eps &&
						b[4] > float64(by)+c[1]+eps && b[1] < float64(by)+c[4]-eps &&
						b[5] > float64(bz)+c[2]+eps && b[2] < float64(bz)+c[5]-eps {
						return true
					}
				}
			}
		}
	}
	return false
}
