package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// isBoatEntityName reports whether the entity type is one of
// #minecraft:boat — every boat, raft, and chest variant ends in
// _boat or _raft in the registry.
func isBoatEntityName(name string) bool {
	return strings.HasSuffix(name, "_boat") || strings.HasSuffix(name, "_raft")
}

// entityTagHas reports whether an entity type name belongs to a
// registry tag (minecraft:<name> without the # in callers).
func entityTypeTagHas(tag, name string) bool {
	switch tag {
	case "boat":
		return isBoatEntityName(name)
	}
	return tag == name
}

// mountCap is vanilla's max passenger count per vehicle type.
func mountCap(name string) int {
	switch {
	case isBoatEntityName(name):
		return 2 // Boat.canAddPassenger
	case isSteerableName(name), isEquineName(name):
		return 1
	}
	return 0
}

// isSteerableName is the saddle-gated steerable set — pig, strider
// and happy_ghast mount only when their saddle/harness is on.
func isSteerableName(name string) bool {
	return name == "pig" || name == "strider" || name == "happy_ghast"
}

// isEquineName is the AbstractHorse family — a tamed adult mounts a
// rider bareback (the saddle gates control, not mounting).
func isEquineName(name string) bool {
	switch name {
	case "horse", "zombie_horse", "skeleton_horse",
		"donkey", "mule", "llama", "trader_llama", "camel":
		return true
	}
	return false
}

// mountAllowed is the per-family interact gate: steerables need the
// saddle (SaddleableEntity.isSaddled); equines need Tamed and an
// adult body (a foal's isSaddleable is false and tame-untamed alike
// refuse).
func mountAllowed(name string, e *Entity) bool {
	switch {
	case isBoatEntityName(name):
		return true
	case isSteerableName(name):
		return e.Saddle
	case isEquineName(name):
		return e.Tamed && !e.Baby
	}
	return false
}

// rideVehicle handles an entity Interact on a vehicle (vanilla
// Entity.interact → startRiding): a right-click on a boat with a free
// seat mounts the player and fires started_riding. Sneaking passes
// through without mounting, like vanilla's isSecondaryUseActive gate.
func (s *Server) rideVehicle(sess *Session, eid int32, sneaking bool) {
	if sneaking {
		return
	}
	if sess.riding.Load() != 0 {
		return
	}
	s.entities.mu.RLock()
	veh, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || veh.Dying {
		return
	}
	name, ok := EntityTypeName(veh.Type)
	if !ok {
		return
	}
	cap := mountCap(name)
	if cap == 0 || !mountAllowed(name, veh) {
		return
	}
	if len(veh.Passengers) >= cap {
		return
	}
	rider := sess.entityID.Load()
	veh.Passengers = append(veh.Passengers, rider)
	// vehicleFirstGood anchors at the mount position — the driver's
	// first move_vehicle measures its delta from here.
	veh.VehAnchor = [3]float64{veh.X, veh.Y, veh.Z}
	sess.riding.Store(eid)
	s.broadcastSetPassengers(veh)
	s.fireStartedRiding(sess)
}

// dismount is Entity.stopRiding for a player: drop the rider off the
// vehicle's passenger list and tell trackers. Vanilla runs it from
// Player.tick when wantsToStopRiding (shift held) is true.
func (s *Server) dismount(sess *Session) {
	vid := sess.riding.Swap(0)
	if vid == 0 {
		return
	}
	p, ok := s.entities.GetPtr(vid)
	if !ok {
		return
	}
	rider := sess.entityID.Load()
	out := p.Passengers[:0]
	for _, pid := range p.Passengers {
		if pid != rider {
			out = append(out, pid)
		}
	}
	p.Passengers = out
	s.broadcastSetPassengers(p)
}

// rideTickStep is the per-player-tick riding work vanilla runs in
// ServerPlayer.tick: the rider's position follows the vehicle, and
// while the vehicle is in lava the ride_entity_in_lava distance
// trigger fires against the latched lava-entry position.
func (s *Server) rideTickStep(sess *Session) {
	vid := sess.riding.Load()
	if vid == 0 {
		sess.lavaRideSet = false
		return
	}
	s.entities.mu.RLock()
	veh, ok := s.entities.byID[vid]
	s.entities.mu.RUnlock()
	if !ok || veh.Dying {
		sess.lavaRideSet = false
		sess.riding.Store(0)
		return
	}
	// A passenger's position is the vehicle's (vanilla syncs it in
	// positionRider); only horizontal distance matters for the trigger.
	_, _, _, yaw, pitch, og := sess.Pos()
	sess.setPos(veh.X, veh.Y, veh.Z, yaw, pitch, og)
	if !s.entityInLava(sess, veh) {
		sess.lavaRideSet = false
		return
	}
	if !sess.lavaRideSet {
		sess.lavaRideSet = true
		sess.lavaRidePos = [3]float64{veh.X, veh.Y, veh.Z}
	}
	s.fireRideInLava(sess)
}

// useFoodOnAStick is FoodOnAStickItem.use: while the packet's hand
// holds a *_on_a_stick and the player controls a saddled steerable
// (strider or pig — vanilla's getControlledVehicle means the first
// passenger), the stick takes 1 durability of wear and the vehicle
// boosts (boost travel itself is not modeled).
func (s *Server) useFoodOnAStick(sess *Session, hand int32) {
	vid := sess.riding.Load()
	if vid == 0 {
		return
	}
	s.entities.mu.RLock()
	veh, ok := s.entities.byID[vid]
	s.entities.mu.RUnlock()
	if !ok || veh.Dying || !veh.Saddle {
		return
	}
	name, ok := EntityTypeName(veh.Type)
	if !ok || (name != "strider" && name != "pig") {
		return
	}
	// Only the controlling passenger's use damages the stick —
	// Strider.getControllingPassenger is the first rider.
	if len(veh.Passengers) == 0 || veh.Passengers[0] != sess.entityID.Load() {
		return
	}
	slot := invSlotForHotbar(sess.held.Load())
	if hand == 1 {
		slot = slotOffhand
	}
	s.wearSlot(sess, slot, 1)
}

// entityInLava reports whether any block under the entity's bounding
// box is lava — vanilla's Entity.isInLava (fluidHeight on lava > 0).
func (s *Server) entityInLava(sess *Session, e *Entity) bool {
	w := sess.world()
	bb := mobBounds(e.Type)
	half := bb[0] / 2
	for bx := int(math.Floor(e.X - half)); bx <= int(math.Floor(e.X+half)); bx++ {
		for by := int(math.Floor(e.Y)); by <= int(math.Floor(e.Y+bb[1])); by++ {
			for bz := int(math.Floor(e.Z - half)); bz <= int(math.Floor(e.Z+half)); bz++ {
				if name, ok := world.StateOwner(w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})); ok && name == "lava" {
					return true
				}
			}
		}
	}
	return false
}
