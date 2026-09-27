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

// isMinecartName covers the whole minecart family — the plain cart
// and every container/special variant share AbstractMinecart's
// bounds and dismount scan.
func isMinecartName(name string) bool {
	return name == "minecart" || strings.HasSuffix(name, "_minecart")
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
	case name == "minecart":
		return 1 // Minecart.interact -> startRiding; container carts open UI instead
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
	case isBoatEntityName(name) || name == "minecart":
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
	// positionRider seats the rider on mount: the server's player pos
	// is the seat from the first ride tick, so the seat-follow never
	// counts the mount hop as ridden distance. set_passengers carries
	// the render — no teleport packet.
	_, _, _, yaw, pitch, og := sess.Pos()
	sess.setPos(veh.X, veh.Y+passengerSeatY(veh)-0.6, veh.Z, yaw, pitch, og)
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
	w := sess.world()
	if isMinecartName(name) {
		return s.minecartDismountPos(w, veh, topY)
	}
	if !isBoatEntityName(name) {
		return veh.X, topY, veh.Z
	}
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

// passengerSeatY is the vehicle's PASSENGER attachment height: the
// declared passengerAttachments offset where an entity type sets one
// (minecart 0.1875), else the AT_HEIGHT fallback half the bounding
// box.
func passengerSeatY(veh *Entity) float64 {
	if name, ok := EntityTypeName(veh.Type); ok && isMinecartName(name) {
		return 0.1875
	}
	return mobBounds(veh.Type)[1] / 2
}

// minecartDismountPos is AbstractMinecart's dismount-location scan:
// it walks DismountHelper.offsetsForDirection of the cart's motion
// direction over POSE_DISMOUNT_HEIGHTS {0,1,-1} around the cart's
// block position, returning the block-bottom-center of the first
// candidate whose floor is valid and whose rider AABB is free.
//
// The effective scan base is not the facing itself: a live yaw sweep
// (summon yaw 0/90/180/270 -> landing N/E/S/W of the cart) shows the
// cart's motion direction resolves to the facing rotated one step
// clockwise, so the first preferred offset drops the rider behind
// the cart.
func (s *Server) minecartDismountPos(w *world.World, veh *Entity, topY float64) (float64, float64, float64) {
	dir := horizDir(veh.Yaw)
	// offsetsForDirection: [clockwise, counterclockwise,
	// opposite+cw, opposite+ccw, dir+cw, dir+ccw, opposite, dir] of
	// the scan direction; the scan direction is the facing rotated
	// clockwise (see comment above).
	d0 := horizClockwise(dir)
	d1 := horizClockwise(d0)
	d2 := horizOpposite(d1)
	d3 := horizOpposite(d0)
	offsets := [8][2]int{
		d1, d2,
		{d3[0] + d1[0], d3[1] + d1[1]},
		{d3[0] + d2[0], d3[1] + d2[1]},
		{d0[0] + d1[0], d0[1] + d1[1]},
		{d0[0] + d2[0], d0[1] + d2[1]},
		d3, d0,
	}
	bx, by, bz := int(math.Floor(veh.X)), int(math.Floor(veh.Y)), int(math.Floor(veh.Z))
	for _, prefH := range [3]int{0, 1, -1} {
		for _, off := range offsets {
			mp := world.Pos{X: bx + off[0], Y: by + prefH, Z: bz + off[1]}
			h, ok := s.minecartFloorHeight(w, mp)
			if !ok {
				continue
			}
			dx, dy, dz := float64(mp.X)+0.5, float64(mp.Y)+h, float64(mp.Z)+0.5
			if s.riderBoxCollides(w, dx, dy, dz) {
				continue
			}
			return dx, dy, dz
		}
	}
	return veh.X, topY, veh.Z
}

// minecartFloorHeight is the shape-supplier overload of
// getBlockFloorHeight used by the cart scan: a solid block supplies
// its collision top (always rejected by isBlockFloorValid's h<1
// gate); a non-solid block reaches the block below and reports that
// shape's top minus 1 — a solid floor gives 0, an empty one gives
// -infinity and fails.
func (s *Server) minecartFloorHeight(w *world.World, mp world.Pos) (float64, bool) {
	if !world.NonSolid(w.GetBlock(mp)) {
		return 1, false
	}
	below := world.Pos{X: mp.X, Y: mp.Y - 1, Z: mp.Z}
	if !world.NonSolid(w.GetBlock(below)) {
		return 0, true
	}
	return 0, false
}

// horizDir is Direction.fromYRot — yaw quarters map to the
// {SOUTH,WEST,NORTH,EAST} 2D index order, returned as an (x,z) step.
func horizDir(yaw float32) [2]int {
	switch int(math.Floor(float64(yaw)/90+0.5)) & 3 {
	case 1:
		return [2]int{-1, 0} // WEST
	case 2:
		return [2]int{0, -1} // NORTH
	case 3:
		return [2]int{1, 0} // EAST
	}
	return [2]int{0, 1} // SOUTH
}

func horizClockwise(d [2]int) [2]int {
	// N->E->S->W->N
	switch d {
	case [2]int{0, -1}:
		return [2]int{1, 0}
	case [2]int{1, 0}:
		return [2]int{0, 1}
	case [2]int{0, 1}:
		return [2]int{-1, 0}
	}
	return [2]int{0, -1}
}

func horizOpposite(d [2]int) [2]int { return [2]int{-d[0], -d[1]} }

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
	// attachment minus the rider's VEHICLE attachment (0.6 for a
	// player — EntityType.PLAYER declares vehicleAttachment(0.6)).
	// The PASSENGER point is the declared passengerAttachments offset
	// or the AT_HEIGHT fallback (0, h/2, 0). Measured live: a
	// minecart's declared 0.1875 puts the rider at vehY-0.4125.
	ox, oy, oz, yaw, pitch, og := sess.Pos()
	sess.setPos(veh.X, veh.Y+passengerSeatY(veh)-0.6, veh.Z, yaw, pitch, og)
	s.rideMoveStats(sess, veh, veh.X-ox, veh.Y+passengerSeatY(veh)-0.6-oy, veh.Z-oz)
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

// vehicleMoveStat maps a ridden vehicle's type to its *_one_cm
// stat — vanilla's checkRidingStatistics instanceof chain:
// AbstractMinecart, AbstractBoat, Pig, AbstractHorse, Strider,
// HappyGhast, AbstractNautilus; anything else banks nothing.
func vehicleMoveStat(name string) string {
	switch {
	case strings.HasSuffix(name, "minecart"):
		return "minecart_one_cm"
	case isBoatEntityName(name):
		return "boat_one_cm"
	case name == "pig":
		return "pig_one_cm"
	case name == "strider":
		return "strider_one_cm"
	case name == "happy_ghast":
		return "happy_ghast_one_cm"
	case name == "nautilus" || name == "zombie_nautilus":
		return "nautilus_one_cm"
	}
	switch name {
	case "horse", "donkey", "mule", "llama", "trader_llama",
		"camel", "skeleton_horse", "zombie_horse":
		return "horse_one_cm"
	}
	return ""
}

// rideMoveStats is ServerPlayer.rideTick's checkRidingStatistics: the
// rider's seat-followed delta banks into the vehicle's own distance
// stat at full 3D magnitude, and nothing else.
func (s *Server) rideMoveStats(sess *Session, veh *Entity, dx, dy, dz float64) {
	if s.GameMode == GameModeSpectator {
		return
	}
	name, ok := EntityTypeName(veh.Type)
	if !ok {
		return
	}
	stat := vehicleMoveStat(name)
	if stat == "" {
		return
	}
	cm := int32(math.Round(math.Sqrt(dx*dx+dy*dy+dz*dz) * 100))
	if cm <= 0 {
		return
	}
	sess.addCustomStat(stat, cm)
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

// railBlockNames is BaseRailBlock.isRail's block set — a minecart on
// any of these takes moveAlongTrack, not comeOffTrack.
var railBlockNames = map[string]bool{
	"rail": true, "powered_rail": true, "detector_rail": true,
	"activator_rail": true,
}

// minecartTickStep is OldMinecartBehavior.tick — the 26.2 default
// (MINECART_IMPROVEMENTS is not in the default flag set): applyGravity
// pulls dy by 0.08, the cart takes moveAlongTrack when its block pos
// (or the block below) is a rail and comeOffTrack otherwise, the yaw
// tail chases the displacement, and pushAndPickupEntities closes the
// tick.
func (s *Server) minecartTickStep() {
	for _, e := range s.entities.All() {
		name, ok := EntityTypeName(e.Type)
		if !ok || !isMinecartName(name) {
			continue
		}
		ep, ok := s.entities.GetPtr(e.ID)
		if !ok || ep.Dying {
			continue
		}
		w := s.dimWorld(e.Dim)
		ox, oz := ep.X, ep.Z
		ep.MotionY -= 0.08 // applyGravity
		// getCurrentBlockPosOrRailBelow: the rail can sit at the cart's
		// own block pos or one block below.
		fx, fz := int(math.Floor(ep.X)), int(math.Floor(ep.Z))
		fy := int(math.Floor(ep.Y))
		railPos := world.Pos{X: fx, Y: fy, Z: fz}
		rb := w.GetBlock(railPos)
		ron, _ := world.StateOwner(rb)
		if !railBlockNames[ron] {
			railPos.Y--
			rb = w.GetBlock(railPos)
			ron, _ = world.StateOwner(rb)
		}
		if railBlockNames[ron] {
			shape, _ := world.StateProperty(rb, "shape")
			powered, _ := world.StateProperty(rb, "powered")
			s.minecartOnRail(ep, w, railPos, ron, shape, powered)
			// activateMinecart: a powered activator rail ejects riders
			// and hits the cart with the same hurt wobble + 50 damage
			// a player attack would.
			if ron == "activator_rail" && powered == "true" {
				s.minecartActivatorHit(ep)
			}
		} else {
			s.minecartOffRail(ep, w, fx, fy, fz)
		}
		s.minecartYawTail(ep, ox, oz)
		s.minecartPushPickup(ep, name)
		// positionRider: every non-player passenger rides welded to
		// the seat attachment — x/z = the cart's, y = +0.1875
		// (villagers and wandering traders sit at the lowered 0.0).
		for _, pid := range ep.Passengers {
			rp, ok := s.entities.GetPtr(pid)
			if !ok {
				continue
			}
			rname, _ := EntityTypeName(rp.Type)
			if rname == "player" {
				continue
			}
			seat := 0.1875
			if rname == "villager" || rname == "wandering_trader" {
				seat = 0
			}
			if rp.X != ep.X || rp.Z != ep.Z || rp.Y != ep.Y+seat {
				rp.X, rp.Z = ep.X, ep.Z
				rp.Y = ep.Y + seat
				rp.PosDirty = true
				s.mobSyncFlush(rp)
			}
		}
		s.mobSyncFlush(ep)
	}
}

// minecartOffRail is comeOffTrack for a cart standing off the rails.
func (s *Server) minecartOffRail(ep *Entity, w *world.World, fx, fy, fz int) {
	const maxSpeed = 0.4
	ep.MotionX = math.Max(-maxSpeed, math.Min(maxSpeed, ep.MotionX))
	ep.MotionZ = math.Max(-maxSpeed, math.Min(maxSpeed, ep.MotionZ))
	if ep.Grounded {
		ep.MotionX *= 0.5
		ep.MotionY *= 0.5
		ep.MotionZ *= 0.5
	}
	ox, oy, oz := ep.X, ep.Y, ep.Z
	// move(SELF, delta): axis-wise block collision cancels the
	// component outright (collideBoundingBox has no entity-shape
	// contributors in vanilla — carts sail through mobs, the
	// resistance is the mob's own pushEntities momentum, not a
	// clip); then a swept floor-landing on y.
	if ep.MotionX != 0 {
		if !s.entityBoxCollides(w, ep.X+ep.MotionX, ep.Y, ep.Z, 0.49, 0.7) {
			ep.X += ep.MotionX
		} else {
			ep.MotionX = 0
		}
	} else {
		ep.MotionX = 0
	}
	if ep.MotionZ != 0 {
		if !s.entityBoxCollides(w, ep.X, ep.Y, ep.Z+ep.MotionZ, 0.49, 0.7) {
			ep.Z += ep.MotionZ
		} else {
			ep.MotionZ = 0
		}
	}
	ep.Grounded = false
	ny := ep.Y + ep.MotionY
	if ep.MotionY <= 0 {
		for by := int(math.Floor(oy)); by >= int(math.Floor(ny)); by-- {
			if !world.NonSolid(w.GetBlock(world.Pos{X: fx, Y: by, Z: fz})) {
				ny = float64(by) + 1
				ep.MotionY = 0
				ep.Grounded = true
				break
			}
		}
	}
	if !ep.Grounded {
		ep.MotionX *= 0.95
		ep.MotionY *= 0.95
		ep.MotionZ *= 0.95
	}
	ep.Y = ny
	if ep.X != ox || ep.Y != oy || ep.Z != oz {
		ep.PosDirty = true
	}
}

// minecartYawTail is tick()'s rotation tail, shared by the on- and
// off-rail branches: pitch zeroed, then the yaw chases
// atan2(zo-z, xo-x)*180/pi — the NEGATED displacement (a +X slide
// gives 180, not 90) — with the flip added only while moving, then
// the 170-degree wrap-flip every tick, then frem-360 normalization.
// ox/oz are the cart's position at tick start (xo/zo).
func (s *Server) minecartYawTail(ep *Entity, ox, oz float64) {
	ep.Pitch = 0
	mx, mz := ox-ep.X, oz-ep.Z
	if mx*mx+mz*mz > 0.001 {
		ep.Yaw = float32(math.Atan2(mz, mx) * 180 / math.Pi)
		if ep.CartFlipped {
			ep.Yaw += 180
		}
	}
	delta := ep.Yaw - ep.CartYawO
	for delta > 180 {
		delta -= 360
	}
	for delta < -180 {
		delta += 360
	}
	if delta < -170 || delta >= 170 {
		ep.Yaw += 180
		ep.CartFlipped = !ep.CartFlipped
	}
	ep.Pitch = float32(math.Mod(float64(ep.Pitch), 360))
	ep.Yaw = float32(math.Mod(float64(ep.Yaw), 360))
	ep.CartYawO = ep.Yaw
}

// railVec is AbstractMinecart.EXITS' Vec3i — an exit offset relative
// to the rail block; a nonzero y marks the DOWNHILL exit (the cart
// steps y+=that when it crosses into the exit's block).
type railVec struct{ x, y, z int }

// railExits is AbstractMinecart.EXITS verbatim (javap static init).
func railExits(shape string) (railVec, railVec, bool) {
	switch shape {
	case "north_south":
		return railVec{0, 0, -1}, railVec{0, 0, 1}, true
	case "east_west":
		return railVec{-1, 0, 0}, railVec{1, 0, 0}, true
	case "ascending_east":
		return railVec{-1, -1, 0}, railVec{1, 0, 0}, true
	case "ascending_west":
		return railVec{-1, 0, 0}, railVec{1, -1, 0}, true
	case "ascending_north":
		return railVec{0, 0, -1}, railVec{0, -1, 1}, true
	case "ascending_south":
		return railVec{0, -1, -1}, railVec{0, 0, 1}, true
	case "south_east":
		return railVec{0, 0, 1}, railVec{1, 0, 0}, true
	case "south_west":
		return railVec{0, 0, 1}, railVec{-1, 0, 0}, true
	case "north_west":
		return railVec{0, 0, -1}, railVec{-1, 0, 0}, true
	case "north_east":
		return railVec{0, 0, -1}, railVec{1, 0, 0}, true
	}
	return railVec{}, railVec{}, false
}

// cartTrackPoint is OldMinecartBehavior.getPos(x,y,z): the point on
// the rail's track line under (x,y,z), with the rail surface 0.0625
// above the block bottom and slope interpolation through the
// (a2y-a1y)*2 term. Returns ok=false when no rail sits under the
// position (own block or the one below).
func cartTrackPoint(w *world.World, x, y, z float64) (float64, float64, float64, bool) {
	fx, fy, fz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if on, _ := world.StateOwner(w.GetBlock(world.Pos{X: fx, Y: fy - 1, Z: fz})); railBlockNames[on] {
		fy--
	}
	rb := w.GetBlock(world.Pos{X: fx, Y: fy, Z: fz})
	ron, _ := world.StateOwner(rb)
	if !railBlockNames[ron] {
		return 0, 0, 0, false
	}
	shape, _ := world.StateProperty(rb, "shape")
	e1, e2, ok := railExits(shape)
	if !ok {
		return 0, 0, 0, false
	}
	// The y anchors ride from the downhill exit's rail surface to the
	// uphill exit's: for a slope the downhill exit (e.y=-1) sits at
	// this block's surface (fy+0.0625) and the uphill exit one block
	// higher (fy+1.0625); flat shapes anchor flat. Vanilla's dip to
	// fy-0.4375 predicted by the raw e.y*0.5 anchors does NOT happen
	// live — a cart climbing an ascending rail banks smoothly from
	// -59.9375 to -58.9375 and never leaves the rail's own floor y.
	slope := e1.y != 0 || e2.y != 0
	a1y := float64(fy) + 0.0625 + float64(e1.y)
	a2y := float64(fy) + 0.0625 + float64(e2.y)
	if slope {
		a1y += 1
		a2y += 1
	}
	a1x := float64(fx) + 0.5 + float64(e1.x)*0.5
	a1z := float64(fz) + 0.5 + float64(e1.z)*0.5
	a2x := float64(fx) + 0.5 + float64(e2.x)*0.5
	a2z := float64(fz) + 0.5 + float64(e2.z)*0.5
	dx, dy, dz := a2x-a1x, a2y-a1y, a2z-a1z
	var t float64
	switch {
	case dx == 0:
		t = z - float64(fz)
	case dz == 0:
		t = x - float64(fx)
	default:
		t = ((x-a1x)*dx + (z-a1z)*dz) * 2
	}
	return a1x + dx*t, a1y + dy*t, a1z + dz*t, true
}

// minecartOnRail is OldMinecartBehavior.moveAlongTrack (javap): the
// cart's velocity is projected onto the rail's exit-to-exit axis, the
// position snapped onto the same line, the move runs axis-clamped to
// getMaxSpeed (0.75x when ridden), the cart steps down onto the
// downhill neighbor when crossing a block edge, applyNaturalSlowdown
// scales (f,0,f), getPos re-reads the track point to bank y and
// convert the height change into speed, the delta re-points along
// the entered block's offset, and a powered powered_rail boosts
// (or a dead one brakes). A player rider's lastClientMoveIntent
// nudges a slow cart (+intent*0.001 when |v|^2<0.01) and suppresses
// the unpowered-rail brake that tick.
// moveIntentVec is Entity.getInputVector((x,0,z), 1.0, yawDeg):
// input axes rotate into world space by the rider's view yaw.
func moveIntentVec(x, z, yawDeg float64) (float64, float64) {
	l := x*x + z*z
	if l < 1e-7 {
		return 0, 0
	}
	if l > 1 {
		n := math.Sqrt(l)
		x, z = x/n, z/n
	}
	sn := math.Sin(yawDeg * 0.017453292)
	cs := math.Cos(yawDeg * 0.017453292)
	return x*cs - z*sn, z*cs + x*sn
}

func (s *Server) minecartOnRail(ep *Entity, w *world.World, bp world.Pos, railName, shape, powered string) {
	mx0, my0, mz0 := ep.X, ep.Y, ep.Z
	_, pos0y, _, pos0ok := cartTrackPoint(w, ep.X, ep.Y, ep.Z)
	ty := float64(bp.Y)
	flag := railName == "powered_rail" && powered == "true"
	flag1 := railName == "powered_rail" && powered != "true"
	slope := 0.0078125
	inWater := s.mobInWater(ep)
	if inWater {
		slope *= 0.2
	}
	switch shape {
	case "ascending_east":
		ep.MotionX -= slope
		ty++
	case "ascending_west":
		ep.MotionX += slope
		ty++
	case "ascending_north":
		ep.MotionZ += slope
		ty++
	case "ascending_south":
		ep.MotionZ -= slope
		ty++
	}
	e1, e2, ok := railExits(shape)
	if !ok {
		return
	}
	dx := float64(e2.x - e1.x)
	dz := float64(e2.z - e1.z)
	dl := math.Hypot(dx, dz)
	if dl == 0 {
		dl = 1
	}
	if ep.MotionX*dx+ep.MotionZ*dz < 0 {
		dx, dz = -dx, -dz
	}
	spd := math.Min(2, math.Hypot(ep.MotionX, ep.MotionZ))
	ep.MotionX = spd * dx / dl
	ep.MotionZ = spd * dz / dl
	// Player intent kick: first passenger a player with live input
	// and the cart near-stopped -> delta += intent*0.001 and the
	// unpowered brake is skipped this tick.
	brake := flag1
	if len(ep.Passengers) > 0 {
		if rs := s.sessionByEntityID(ep.Passengers[0]); rs != nil {
			_, _, _, ryaw, _, _ := rs.Pos()
			ix, iz := moveIntentVec(float64(rs.moveInputX), float64(rs.moveInputZ), float64(ryaw))
			if ix*ix+iz*iz > 0 && ep.MotionX*ep.MotionX+ep.MotionZ*ep.MotionZ < 0.01 {
				ep.MotionX += ix * 0.001
				ep.MotionZ += iz * 0.001
				brake = false
			}
		}
	}
	// unpowered powered_rail brakes: <0.03 stops dead, else halves.
	if brake {
		if h := math.Hypot(ep.MotionX, ep.MotionZ); h < 0.03 {
			ep.MotionX, ep.MotionY, ep.MotionZ = 0, 0, 0
		} else {
			ep.MotionX *= 0.5
			ep.MotionZ *= 0.5
		}
	}
	// The track snap: position lands on the exit-to-exit line at the
	// parameter t measured along it (raw axis offset for straight
	// rails, dot*2 for curves), y = the rail block's y (+1 when the
	// rail itself ascends).
	a1x := float64(bp.X) + 0.5 + float64(e1.x)*0.5
	a1z := float64(bp.Z) + 0.5 + float64(e1.z)*0.5
	a2x := float64(bp.X) + 0.5 + float64(e2.x)*0.5
	a2z := float64(bp.Z) + 0.5 + float64(e2.z)*0.5
	tdx, tdz := a2x-a1x, a2z-a1z
	var t float64
	switch {
	case tdx == 0:
		t = ep.Z - float64(bp.Z)
	case tdz == 0:
		t = ep.X - float64(bp.X)
	default:
		t = ((ep.X-a1x)*tdx + (ep.Z-a1z)*tdz) * 2
	}
	ep.X = a1x + tdx*t
	ep.Z = a1z + tdz*t
	ep.Y = ty
	// move(SELF, (clamp(adj*vx,-ms,ms), 0, clamp(adj*vz,-ms,ms))).
	adj := 1.0
	if len(ep.Passengers) > 0 {
		adj = 0.75
	}
	ms := 0.4
	if inWater {
		ms = 0.2
	}
	mx := math.Max(-ms, math.Min(ms, adj*ep.MotionX))
	mz := math.Max(-ms, math.Min(ms, adj*ep.MotionZ))
	if mx != 0 && !s.entityBoxCollides(w, ep.X+mx, ep.Y, ep.Z, 0.49, 0.7) {
		ep.X += mx
	}
	if mz != 0 && !s.entityBoxCollides(w, ep.X, ep.Y, ep.Z+mz, 0.49, 0.7) {
		ep.Z += mz
	}
	// The downhill-exit step: when the post-move block offset equals
	// a nonzero-y exit, y picks that exit's offset (a descending
	// rail hands the cart to the lower block's rail).
	efx, efz := int(math.Floor(ep.X))-bp.X, int(math.Floor(ep.Z))-bp.Z
	if e1.y != 0 && efx == e1.x && efz == e1.z {
		ep.Y += float64(e1.y)
	} else if e2.y != 0 && efx == e2.x && efz == e2.z {
		ep.Y += float64(e2.y)
	}
	// applyNaturalSlowdown: multiply (f,0,f), 0.96 unridden / 0.997
	// ridden — y is zeroed outright — then water drags x0.95.
	f := 0.96
	if len(ep.Passengers) > 0 {
		f = 0.997
	}
	ep.MotionX *= f
	ep.MotionY = 0
	ep.MotionZ *= f
	if inWater {
		ep.MotionX *= 0.95
		ep.MotionZ *= 0.95
	}
	// The post-move getPos: the track point at the new pos banks the
	// cart's y onto the rail surface; the height delta between the
	// pre- and post-move track points converts to speed at 0.05 per
	// block (downhill accelerates, uphill brakes).
	_, p1y, _, p1ok := cartTrackPoint(w, ep.X, ep.Y, ep.Z)
	if pos0ok && p1ok {
		drop := (pos0y - p1y) * 0.05
		if h := math.Hypot(ep.MotionX, ep.MotionZ); h > 0 {
			k := (h + drop) / h
			ep.MotionX *= k
			ep.MotionZ *= k
		}
		ep.Y = p1y
	}
	// Crossing the rail block's boundary re-points the whole delta at
	// the entered block's offset (curves turn the cart).
	nfx, nfz := int(math.Floor(ep.X)), int(math.Floor(ep.Z))
	if nfx != bp.X || nfz != bp.Z {
		h := math.Hypot(ep.MotionX, ep.MotionZ)
		ep.MotionX = h * float64(nfx-bp.X)
		ep.MotionZ = h * float64(nfz-bp.Z)
	}
	// A powered powered_rail boosts +0.06 along the motion while the
	// cart is moving; stopped carts get a 0.02 kick AWAY from a solid
	// conductor side (the classic stop-block release).
	if flag {
		if h := math.Hypot(ep.MotionX, ep.MotionZ); h > 0.01 {
			ep.MotionX += ep.MotionX / h * 0.06
			ep.MotionZ += ep.MotionZ / h * 0.06
		} else {
			ndx, ndz := ep.MotionX, ep.MotionZ
			switch shape {
			case "east_west":
				if !world.NonSolid(w.GetBlock(world.Pos{X: bp.X - 1, Y: bp.Y, Z: bp.Z})) {
					ndx = 0.02
				} else if !world.NonSolid(w.GetBlock(world.Pos{X: bp.X + 1, Y: bp.Y, Z: bp.Z})) {
					ndx = -0.02
				}
			case "north_south":
				if !world.NonSolid(w.GetBlock(world.Pos{X: bp.X, Y: bp.Y, Z: bp.Z - 1})) {
					ndz = 0.02
				} else if !world.NonSolid(w.GetBlock(world.Pos{X: bp.X, Y: bp.Y, Z: bp.Z + 1})) {
					ndz = -0.02
				}
			}
			ep.MotionX, ep.MotionZ = ndx, ndz
		}
	}
	if ep.X != mx0 || ep.Y != my0 || ep.Z != mz0 {
		ep.PosDirty = true
	}
}

// minecartActivatorHit is Minecart.activateMinecart for a plain cart
// on a powered activator rail: riders eject, and when the hurt
// cooldown is down the cart takes the same hurtDir flip, 10-tick
// hurtTime and 50 damage a hit applies (VehicleEntity.markHurt).
func (s *Server) minecartActivatorHit(ep *Entity) {
	if len(ep.Passengers) > 0 {
		var mobs []int32
		for _, pid := range ep.Passengers {
			if rp, ok := s.entities.GetPtr(pid); ok {
				rp.Riding = 0
				mobs = append(mobs, pid)
			}
		}
		ep.Passengers = ep.Passengers[:0]
		// An ejected mob dismounts at the vehicle's position — the
		// cart's own Pos, not the welded seat.
		for _, pid := range mobs {
			if rp, ok := s.entities.GetPtr(pid); ok {
				rp.X, rp.Y, rp.Z = ep.X, ep.Y, ep.Z
				rp.PosDirty = true
				s.mobSyncFlush(rp)
			}
		}
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay && sess.riding.Load() == ep.ID {
				s.dismount(sess)
			}
			return true
		})
		s.broadcastSetPassengers(ep)
	}
	if ep.VehHurtTime == 0 {
		ep.VehHurtDir = -ep.VehHurtDir
		ep.VehHurtTime = 10
		ep.VehDamage = 50
	}
}

// minecartPushPickup is pushAndPickupEntities — the tail of
// OldMinecartBehavior.tick running for every cart every tick: the
// sweep box is the cart's bounding box inflated 0.2 on x/z only.
// A rideable (plain) minecart moving at horizontal speed²>=0.01
// pushes players, iron golems, other carts and any passenger aside
// and PICKS UP every other pushable entity (startRiding); a
// stationary or non-rideable cart only answers cart-vs-cart pushes.
func (s *Server) minecartPushPickup(ep *Entity, name string) {
	moving := name == "minecart" &&
		ep.MotionX*ep.MotionX+ep.MotionZ*ep.MotionZ >= 0.01
	overlaps := func(ox, oy, oz, ow, oh float64) bool {
		return math.Abs(ox-ep.X) < 0.69+ow/2 &&
			math.Abs(oz-ep.Z) < 0.69+ow/2 &&
			oy < ep.Y+0.7 && ep.Y < oy+oh
	}
	hasPassenger := func(id int32) bool {
		for _, p := range ep.Passengers {
			if p == id {
				return true
			}
		}
		return false
	}
	for _, o := range s.entities.All() {
		if o.ID == ep.ID || o.Dim != ep.Dim || o.Riding == ep.ID ||
			hasPassenger(o.ID) {
			continue
		}
		oname, _ := EntityTypeName(o.Type)
		isCart := isMinecartName(oname)
		if !moving {
			// The stationary branch only does cart-vs-cart.
			if !isCart {
				continue
			}
		} else if !(isCart || (o.Living && o.Health > 0)) {
			// pushableBy: non-pushable entities (items, orbs) skip.
			continue
		}
		ow, oh := mobBounds(o.Type)[0], mobBounds(o.Type)[1]
		if !overlaps(o.X, o.Y, o.Z, ow, oh) {
			continue
		}
		if moving && !isCart && oname != "iron_golem" &&
			len(ep.Passengers) == 0 && o.Riding == 0 {
			// startRiding: the moving cart scoops the mob onto its
			// seat (the attachment point at vehY+0.1875).
			if op, ok := s.entities.GetPtr(o.ID); ok {
				op.Riding = ep.ID
				op.X, op.Z = ep.X, ep.Z
				op.Y = ep.Y + 0.1875
				ep.Passengers = append(ep.Passengers, o.ID)
				op.PosDirty = true
				s.mobSyncFlush(op)
				s.broadcastSetPassengers(ep)
			}
			continue
		}
		if isCart {
			// e.push(cart) resolves to AbstractMinecart.push: the
			// facing-gated cart-vs-cart momentum exchange.
			if op, ok := s.entities.GetPtr(o.ID); ok {
				minecartPushCart(op, ep)
			}
			continue
		}
		// Generic Entity.push (mobs, golems): both sides repel at the
		// shared 0.05 strength while neither carries a rider.
		dx, dz := ep.X-o.X, ep.Z-o.Z
		m := math.Max(math.Abs(dx), math.Abs(dz))
		if m < 0.01 {
			continue
		}
		m = math.Sqrt(m)
		dx, dz = dx/m, dz/m
		inv := 1.0 / m
		if inv > 1 {
			inv = 1
		}
		dx, dz = dx*inv*0.05, dz*inv*0.05
		// push(DDD) lands on deltaMovement only — the position moves
		// when each side's own move() integrates it next tick.
		if op, ok := s.entities.GetPtr(o.ID); ok && len(op.Passengers) == 0 {
			op.MotionX -= dx
			op.MotionZ -= dz
		}
		if len(ep.Passengers) == 0 {
			ep.MotionX += dx
			ep.MotionZ += dz
		}
	}
	if moving {
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != ep.Dim ||
				sess.gameMode == 3 || sess.riding.Load() == ep.ID {
				return true
			}
			px, py, pz, _, _, _ := sess.Pos()
			if !overlaps(px, py, pz, 0.6, 1.8) {
				return true
			}
			// Generic Entity.push against the player: the player gets
			// pushed away (set_entity_motion) and the cart brakes on
			// the player when it carries nobody.
			dx, dz := px-ep.X, pz-ep.Z
			m := math.Max(math.Abs(dx), math.Abs(dz))
			if m < 0.01 {
				return true
			}
			m = math.Sqrt(m)
			dx, dz = dx/m, dz/m
			inv := 1.0 / m
			if inv > 1 {
				inv = 1
			}
			dx, dz = dx*inv*0.05, dz*inv*0.05
			_ = sess.write(sess.ids().PlaySetEntityMotion,
				encodeEntityMotion(sess.entityID.Load(), dx, 0, dz))
			if len(ep.Passengers) == 0 {
				ep.MotionX -= dx
				ep.MotionZ -= dz
			}
			return true
		})
	}
}

// minecartPushCart is AbstractMinecart.push(pusher) run on the other
// cart: pushOtherMinecart's non-experimental path. The push fires
// only when the pusher faces the target within ~37 degrees —
// |unit(dir-to-target) . unit(cos yRot, sin yRot)| >= 0.8 — then
// both carts shed 80% of their speed and take half the summed
// momentum plus the 0.05-scaled repel along the shared axis.
func minecartPushCart(pusher, target *Entity) {
	dx, dz := target.X-pusher.X, target.Z-pusher.Z
	d6 := dx*dx + dz*dz
	if d6 < 1e-4 {
		return
	}
	l := math.Sqrt(d6)
	dx, dz = dx/l, dz/l
	inv := 1.0 / l
	if inv > 1 {
		inv = 1
	}
	px, pz := dx*inv*0.05, dz*inv*0.05
	fx, fz := math.Cos(float64(pusher.Yaw)*0.017453292),
		math.Sin(float64(pusher.Yaw)*0.017453292)
	if math.Abs(dx*fx+dz*fz) < 0.8 {
		return
	}
	avx, avz := (target.MotionX+pusher.MotionX)/2, (target.MotionZ+pusher.MotionZ)/2
	pusher.MotionX = pusher.MotionX*0.2 + avx - px
	pusher.MotionZ = pusher.MotionZ*0.2 + avz - pz
	target.MotionX = target.MotionX*0.2 + avx + px
	target.MotionZ = target.MotionZ*0.2 + avz + pz
	pusher.PosDirty = true
	target.PosDirty = true
}

// entityBoxCollides is riderBoxCollides parametrized by the entity's
// box: any solid block intersecting (x±half) × (y,y+h) × (z±half)
// cancels the component move.
func (s *Server) entityBoxCollides(w *world.World, x, y, z, half, h float64) bool {
	minY := int(math.Floor(y))
	maxY := int(math.Floor(y + h))
	for bx := int(math.Floor(x - half)); bx <= int(math.Floor(x+half)); bx++ {
		for bz := int(math.Floor(z - half)); bz <= int(math.Floor(z+half)); bz++ {
			for by := minY; by <= maxY; by++ {
				if !world.NonSolid(w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})) {
					return true
				}
			}
		}
	}
	return false
}
