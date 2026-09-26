package server

import (
	"math"
	"strings"

	"mc-go/internal/protocol"
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
// vehicle's passenger list, place them at the dismount position, and
// tell trackers. Vanilla runs it from Player.tick when
// wantsToStopRiding (shift held) is true.
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
	// removeVehicle pushes the rider to the vehicle's dismount
	// location and syncs it — the rider gets a position packet and
	// trackers see an entity teleport (probe: 0x23 with absolute
	// coordinates).
	_, _, _, yaw, pitch, og := sess.Pos()
	dx, dy, dz := s.dismountPos(sess, p, yaw)
	sess.setPos(dx, dy, dz, yaw, pitch, og)
	s.entities.Update(rider, dx, dy, dz, yaw, pitch, og)
	s.broadcastEntityTeleport(sess, rider, dx, dy, dz, yaw, pitch, og)
	_ = s.sendSyncPos(sess, dx, dy, dz)
}

// dismountPos is Entity.getDismountLocationForPassenger: the default
// is the top of the vehicle's bounding box; AbstractBoat overrides it
// with a side-escape search that drops the rider alongside the boat
// (measured live: (8.5,-60,9.7723) for a boat at (8.5,-60,8.5) and a
// south-facing rider).
func (s *Server) dismountPos(sess *Session, veh *Entity, riderYaw float32) (float64, float64, float64) {
	name, _ := EntityTypeName(veh.Type)
	bb := mobBounds(veh.Type)
	topY := veh.Y + bb[1]
	if !isBoatEntityName(name) {
		return veh.X, topY, veh.Z
	}
	w := sess.world()
	// getCollisionHorizontalEscapeVector(vehW*sqrt2, riderW, yaw):
	// half the summed widths pushed along the rider's facing, the
	// vector normalized so its longest axis keeps the full length.
	esc := (bb[0]*math.Sqrt2 + 0.6 + 1e-5) / 2
	rad := float64(riderYaw) * math.Pi / 180
	fs, fc := -math.Sin(rad), math.Cos(rad)
	m := math.Max(math.Abs(fs), math.Abs(fc))
	nx, nz := fs*esc/m, fc*esc/m
	cx, cz := veh.X+nx, veh.Z+nz
	bpY := int(math.Floor(topY))
	below := world.Pos{X: int(math.Floor(cx)), Y: bpY - 1, Z: int(math.Floor(cz))}
	if bn, ok := world.StateOwner(w.GetBlock(below)); ok && bn == "water" {
		return veh.X, topY, veh.Z
	}
	// Floor candidates at the escaped column's block pos and below;
	// our collision model has no partial floors, so a non-solid
	// block's floor height is 0 and a solid block's is 1 (rejected
	// by isBlockFloorValid's h<1 gate).
	for _, py := range [2]int{bpY, bpY - 1} {
		bp := world.Pos{X: below.X, Y: py, Z: below.Z}
		h := 0.0
		if !world.NonSolid(w.GetBlock(bp)) {
			h = 1
		}
		if h >= 1 {
			continue
		}
		dy := float64(py) + h
		if !s.riderBoxCollides(w, cx, dy, cz) {
			return cx, dy, cz
		}
	}
	return veh.X, topY, veh.Z
}

// riderBoxCollides is DismountHelper.canDismountTo: the rider's
// standing AABB (0.6 wide, 1.8 tall) at the candidate must not
// intersect any block with a collision shape.
func (s *Server) riderBoxCollides(w *world.World, x, y, z float64) bool {
	minY := int(math.Floor(y))
	maxY := int(math.Floor(y + 1.8))
	for bx := int(math.Floor(x - 0.3)); bx <= int(math.Floor(x+0.3)); bx++ {
		for bz := int(math.Floor(z - 0.3)); bz <= int(math.Floor(z+0.3)); bz++ {
			for by := minY; by <= maxY; by++ {
				if !world.NonSolid(w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})) {
					return true
				}
			}
		}
	}
	return false
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
	// positionRider seats the rider at the vehicle's PASSENGER
	// attachment minus their VEHICLE attachment; with the fallback
	// attachments boats and mobs all use, that is the vehicle center
	// at half its height: (vehX, vehY + h/2, vehZ).
	_, _, _, yaw, pitch, og := sess.Pos()
	bb := mobBounds(veh.Type)
	sess.setPos(veh.X, veh.Y+bb[1]/2, veh.Z, yaw, pitch, og)
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

// isVehicleEntityName covers every VehicleEntity type — the
// #minecraft:boat family (boats, rafts, chest variants) plus every
// *minecart. These take VehicleEntity.hurtServer damage, not
// LivingEntity damage.
func isVehicleEntityName(name string) bool {
	return isBoatEntityName(name) || strings.HasSuffix(name, "minecart")
}

// vehicleHurt is VehicleEntity.hurtServer: each melee hit flips the
// wobble direction (DATA_ID_HURTDIR flips sign), resets the wobble
// countdown (DATA_ID_HURT = 10), and banks amount*10 into
// DATA_ID_DAMAGE. A hit lands only when the entity is in reach; the
// caller runs the reach gate. When banked damage passes 40 the
// vehicle breaks and drops its item; a player in instabuild
// (creative) instead discards it with no drop — matching vanilla's
// !canDestroy && damage>40 || canDestroy branch (shouldSourceDestroy
// is false for boats and minecarts). SynchedEntityData.set marks
// only entries whose value actually changed, so the dirty pack
// carries just the entries this hit moved.
func (s *Server) vehicleHurt(sess *Session, ep *Entity, amount float32) {
	if ep.VehHurtDir == 0 {
		// DATA_ID_HURTDIR defaults to +1 (defined with value 1);
		// a fresh entity starts there so the first flip reads -1.
		ep.VehHurtDir = 1
	}
	oldTime, oldDir, oldDmg := ep.VehHurtTime, ep.VehHurtDir, ep.VehDamage
	ep.VehHurtDir = -ep.VehHurtDir
	ep.VehHurtTime = 10
	ep.VehDamage += amount * 10
	var b protocol.Buffer
	if ep.VehHurtTime != oldTime {
		b.Byte(8)
		b.Byte(1) // INT serializer
		b.VarInt(ep.VehHurtTime)
	}
	if ep.VehHurtDir != oldDir {
		b.Byte(9)
		b.Byte(1) // INT serializer
		b.VarInt(ep.VehHurtDir)
	}
	if ep.VehDamage != oldDmg {
		b.Byte(10)
		b.Byte(3) // FLOAT serializer
		b.Float(ep.VehDamage)
	}
	if b.Len() > 0 {
		b.Byte(0xFF)
		s.broadcastEntityMetadata(ep.ID, b.Bytes())
	}
	creative := sess.mode() == GameModeCreative
	switch {
	case !creative && ep.VehDamage > 40:
		s.vehicleDestroy(ep)
	case creative:
		// instabuild hits discard the vehicle: removed, no drop.
		s.entities.Remove(ep.ID)
		s.broadcastEntityRemove([]int32{ep.ID})
	}
}

// vehicleDestroy is VehicleEntity.destroy: the vehicle is removed and
// its drop item spawns where it sat. A chest vehicle would also spill
// its contents — unmodelled, contents are empty in practice.
func (s *Server) vehicleDestroy(ep *Entity) {
	s.entities.Remove(ep.ID)
	s.broadcastEntityRemove([]int32{ep.ID})
	name, ok := EntityTypeName(ep.Type)
	if !ok {
		return
	}
	if itemID, ok := world.ItemID(name); ok {
		s.spawnItemDrop(ep.Dim, world.Pos{
			X: int(math.Floor(ep.X)),
			Y: int(math.Floor(ep.Y)),
			Z: int(math.Floor(ep.Z)),
		}, ItemStack{ID: itemID, Count: 1})
	}
}

// vehicleTickStep is VehicleEntity.tick's wobble countdown: while
// positive, DATA_ID_HURT decrements 1/tick and DATA_ID_DAMAGE decays
// 1/tick, each marking its entity-data entry dirty; the dirty values
// ship on the entity's sendChanges interval, so observers see the
// current value at interval boundaries rather than every decrement.
func (s *Server) vehicleTickStep() {
	for _, e := range s.entities.All() {
		if e.VehHurtTime <= 0 && e.VehDamage <= 0 {
			continue
		}
		ep, ok := s.entities.GetPtr(e.ID)
		if !ok {
			continue
		}
		dirtyTime, dirtyDamage := false, false
		if ep.VehHurtTime > 0 {
			ep.VehHurtTime--
			dirtyTime = true
		}
		if ep.VehDamage > 0 {
			ep.VehDamage--
			if ep.VehDamage < 0 {
				ep.VehDamage = 0
			}
			dirtyDamage = true
		}
		ep.SyncCount++
		// The live probe against 26.2 shows vanilla shipping this
		// dirty pack every tick through the countdown, not on the
		// boat's 3-tick sendChanges interval — the countdown entries
		// ride a more eager path. Emit per tick while dirty.
		if dirtyTime || dirtyDamage {
			var b protocol.Buffer
			if dirtyTime {
				b.Byte(8)
				b.Byte(1) // INT serializer
				b.VarInt(ep.VehHurtTime)
			}
			if dirtyDamage {
				b.Byte(10)
				b.Byte(3) // FLOAT serializer
				b.Float(ep.VehDamage)
			}
			b.Byte(0xFF)
			s.broadcastEntityMetadata(ep.ID, b.Bytes())
		}
	}
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
