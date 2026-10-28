package worldgen

import "math"

// Sculk patches: charge cursors start at the origin and wander over the
// sculk they create, turning replaceable blocks into sculk, growing
// veins over neighbouring faces and, now and then, sensors and
// shriekers; a catalyst and rare shriekers finish the patch.

func init() {
	featureKinds["minecraft:sculk_patch"] = decodeSculkPatch
}

// sculkSpreader is the world-generation sculk spreader's settings and
// its cursors.
type sculkSpreader struct {
	cursors []*chargeCursor
}

const (
	sculkGrowthSpawnCost = 50
	sculkNoGrowthRadius  = 1
	sculkChargeDecayRate = 5
	sculkAdditionalDecay = 10
	sculkMaxCursors      = 32
)

type chargeCursor struct {
	pos                     Point
	charge                  int32
	updateDelay, decayDelay int32
	facings                 []int // nil until the cursor sits on sculk
	haveFacings             bool
}

var sculkVein = multiface{block: "sculk_vein", sculk: true}

// nonCornerNeighbours are the 18 offsets in the 3×3×3 cube with a zero
// coordinate, x fastest then y then z.
var nonCornerNeighbours = func() []Point {
	var out []Point
	for z := int32(-1); z <= 1; z++ {
		for y := int32(-1); y <= 1; y++ {
			for x := int32(-1); x <= 1; x++ {
				if (x == 0 || y == 0 || z == 0) && (x != 0 || y != 0 || z != 0) {
					out = append(out, Point{x, y, z})
				}
			}
		}
	}
	return out
}()

func (s *sculkSpreader) addCursors(p Point, charge int32) {
	for charge > 0 {
		c := min32(charge, 1000)
		if len(s.cursors) < sculkMaxCursors {
			s.cursors = append(s.cursors, &chargeCursor{pos: p, charge: c, decayDelay: 1})
		}
		charge -= c
	}
}

// update moves every cursor once; cursors out of charge (or too far
// out) drop away.
func (s *sculkSpreader) update(r *decoRegion, origin Point, rand Random, spreadVeins bool) {
	kept := s.cursors[:0]
	for _, c := range s.cursors {
		if chessboard(c.pos, origin) > 1024 {
			continue
		}
		c.update(r, origin, rand, spreadVeins)
		if c.charge > 0 {
			kept = append(kept, c)
		}
	}
	s.cursors = kept
}

func chessboard(a, b Point) int32 { return max32(abs(a.X-b.X), max32(abs(a.Y-b.Y), abs(a.Z-b.Z))) }

// sculkKind is the sculk behaviour of a block: sculk itself, a vein, or
// anything else.
type sculkKind int

const (
	sculkOther sculkKind = iota
	sculkBlock
	sculkVeinKind
)

func sculkKindOf(s StateID) sculkKind {
	switch {
	case s.is("sculk"):
		return sculkBlock
	case s.is("sculk_vein"):
		return sculkVeinKind
	}
	return sculkOther
}

func (c *chargeCursor) update(r *decoRegion, origin Point, rand Random, spreadVeins bool) {
	if c.charge <= 0 {
		return
	}
	if c.updateDelay > 0 {
		c.updateDelay--
		return
	}
	s := r.get(c.pos)
	kind := sculkKindOf(s)
	if spreadVeins && attemptSpreadVein(r, c.pos, s, kind, c.facings, c.haveFacings) && kind != sculkBlock {
		s = r.get(c.pos)
		kind = sculkKindOf(s)
	}
	c.charge = attemptUseCharge(r, c, kind, origin, rand, spreadVeins)
	if c.charge <= 0 {
		dischargeAt(r, s, kind, c.pos)
		return
	}
	if p, ok := validMovementPos(r, c.pos, rand); ok {
		dischargeAt(r, s, kind, c.pos)
		c.pos = p
		dx, dz := float64(p.X-origin.X), float64(p.Z-origin.Z)
		if float64(dx*dx)+float64(dz*dz) >= 15*15 {
			c.charge = 0
			return
		}
		s = r.get(p)
	}
	if sculkKindOf(s) != sculkOther {
		c.facings, c.haveFacings = availableFaces(s), true
	}
	// The delays follow the behaviour the cursor started the step on.
	if kind == sculkOther {
		c.decayDelay = max32(c.decayDelay-1, 0)
	} else {
		c.decayDelay = 1
	}
	c.updateDelay = 1
}

// availableFaces is a vein's faces in direction order; other sculk has
// none.
func availableFaces(s StateID) []int {
	faces := []int{}
	if !s.is("sculk_vein") {
		return faces
	}
	for d := 0; d < 6; d++ {
		if hasFace(s, d) {
			faces = append(faces, d)
		}
	}
	return faces
}

// attemptSpreadVein grows veins at p: from the block's own faces for
// sculk and veins; elsewhere into the block's own space, regrowing the
// cursor's remembered faces, or (with none remembered) from all faces.
func attemptSpreadVein(r *decoRegion, p Point, s StateID, kind sculkKind, facings []int, have bool) bool {
	if kind == sculkOther {
		switch {
		case !have:
			return sculkVein.spreadAll(r, r.get(p), p, true) > 0
		case len(facings) > 0:
			if s.isAir() || s.isWaterFluid() {
				return regrowVein(r, p, s, facings)
			}
			return false
		}
	}
	return sculkVein.spreadAll(r, s, p, false) > 0
}

// regrowVein sets a vein at p with each remembered face that can still
// attach, waterlogged in fluid.
func regrowVein(r *decoRegion, p Point, s StateID, facings []int) bool {
	vein := defaultState("sculk_vein")
	any := false
	for _, d := range facings {
		if canAttachTo(r, d, p.offset(d)) {
			vein = vein.with(faceProps[d], "true")
			any = true
		}
	}
	if !any {
		return false
	}
	if s.hasFluid() {
		vein = vein.with("waterlogged", "true")
	}
	r.set(p, vein)
	return true
}

func attemptUseCharge(r *decoRegion, c *chargeCursor, kind sculkKind, origin Point, rand Random, spreadVeins bool) int32 {
	switch kind {
	case sculkBlock:
		return sculkBlockUseCharge(r, c, origin, rand)
	case sculkVeinKind:
		if spreadVeins && attemptPlaceSculk(r, c.pos, rand) {
			return c.charge - 1
		}
		if rand.NextIntn(sculkChargeDecayRate) == 0 {
			return int32(math.Floor(float64(float32(c.charge) * 0.5)))
		}
		return c.charge
	}
	if c.decayDelay > 0 {
		return c.charge
	}
	return 0
}

// sculkBlockUseCharge spends charge on sculk: one roll in five does
// anything; near the origin or where growth cannot go, charge decays
// (one roll in ten); otherwise a growth may sprout above for 50 charge.
func sculkBlockUseCharge(r *decoRegion, c *chargeCursor, origin Point, rand Random) int32 {
	charge := c.charge
	if charge == 0 || rand.NextIntn(sculkChargeDecayRate) != 0 {
		return charge
	}
	p := c.pos
	near := distSqr(p, origin) < float64(sculkNoGrowthRadius*sculkNoGrowthRadius)
	if near || !canPlaceGrowth(r, p) {
		if rand.NextIntn(sculkAdditionalDecay) != 0 {
			return charge
		}
		if near {
			return charge - 1
		}
		return charge - decayPenalty(p, origin, charge)
	}
	if rand.NextIntn(sculkGrowthSpawnCost) < charge {
		up := p.offset(dirUp)
		r.set(up, randomGrowthState(r, up, rand))
	}
	return max32(0, charge-sculkGrowthSpawnCost)
}

// distSqr is Vec3i.distSqr: the squared distance as a double.
func distSqr(a, b Point) float64 {
	dx, dy, dz := float64(a.X-b.X), float64(a.Y-b.Y), float64(a.Z-b.Z)
	return dx*dx + dy*dy + dz*dz
}

func decayPenalty(p, origin Point, charge int32) int32 {
	d := float32(math.Sqrt(distSqr(p, origin))) - sculkNoGrowthRadius
	f := d * d
	g := float32((24 - sculkNoGrowthRadius) * (24 - sculkNoGrowthRadius))
	h := min(float32(1), f/g)
	return max32(1, int32(float32(float32(charge)*h)*0.5))
}

func randomGrowthState(r *decoRegion, p Point, rand Random) StateID {
	var s StateID
	if rand.NextIntn(11) == 0 {
		s = defaultState("sculk_shrieker").with("can_summon", "true")
	} else {
		s = defaultState("sculk_sensor")
	}
	if s.hasProp("waterlogged") && r.get(p).hasFluid() {
		s = s.with("waterlogged", "true")
	}
	return s
}

// canPlaceGrowth is whether a sensor or shrieker may sprout over p: air
// or still water above, and at most two growths in the 9×3×9 around.
func canPlaceGrowth(r *decoRegion, p Point) bool {
	above := r.get(p.offset(dirUp))
	if !above.isAir() && !(above.is("water") && above.isWaterFluid() && above.isSourceFluid()) {
		return false
	}
	n := 0
	for z := p.Z - 4; z <= p.Z+4; z++ {
		for y := p.Y; y <= p.Y+2; y++ {
			for x := p.X - 4; x <= p.X+4; x++ {
				if s := r.get(Point{x, y, z}); s.is("sculk_sensor") || s.is("sculk_shrieker") {
					if n++; n > 2 {
						return false
					}
				}
			}
		}
	}
	return true
}

// attemptPlaceSculk turns the first replaceable block behind one of the
// vein's faces, in shuffled direction order, into sculk, spreads veins
// from it and discharges the veins around it.
func attemptPlaceSculk(r *decoRegion, p Point, rand Random) bool {
	s := r.get(p)
	for _, d := range shuffleDirs(rand, []int{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}) {
		if !hasFace(s, d) {
			continue
		}
		q := p.offset(d)
		if !r.get(q).inTag("sculk_replaceable_world_gen") {
			continue
		}
		sculk := defaultState("sculk")
		r.set(q, sculk)
		sculkVein.spreadAll(r, sculk, q, false)
		for e := 0; e < 6; e++ {
			if e == d^1 {
				continue
			}
			n := q.offset(e)
			if ns := r.get(n); ns.is("sculk_vein") {
				dischargeAt(r, ns, sculkVeinKind, n)
			}
		}
		return true
	}
	return false
}

// dischargeAt is a vein letting go of the faces it has on sculk; with
// none left it gives way to air or water.
func dischargeAt(r *decoRegion, s StateID, kind sculkKind, p Point) {
	if kind != sculkVeinKind || !s.is("sculk_vein") {
		return
	}
	for d := 0; d < 6; d++ {
		if hasFace(s, d) && r.get(p.offset(d)).is("sculk") {
			s = s.with(faceProps[d], "false")
		}
	}
	if !hasAnyFace(s) {
		if r.get(p).hasFluid() {
			s = defaultState("water")
		} else {
			s = stAirState
		}
	}
	r.set(p, s)
}

func hasAnyFace(s StateID) bool {
	for d := 0; d < 6; d++ {
		if hasFace(s, d) {
			return true
		}
	}
	return false
}

// validMovementPos is where a cursor moves: over the shuffled
// non-corner neighbours, the last sculk block it can reach unobstructed,
// stopping early at a vein with replaceable blocks behind a face.
func validMovementPos(r *decoRegion, p Point, rand Random) (Point, bool) {
	offsets := append([]Point(nil), nonCornerNeighbours...)
	for i := int32(len(offsets)); i > 1; i-- {
		j := rand.NextIntn(i)
		offsets[i-1], offsets[j] = offsets[j], offsets[i-1]
	}
	best := p
	for _, o := range offsets {
		q := Point{p.X + o.X, p.Y + o.Y, p.Z + o.Z}
		s := r.get(q)
		if sculkKindOf(s) == sculkOther || !movementUnobstructed(r, p, q) {
			continue
		}
		best = q
		if hasSubstrateAccess(r, s, q) {
			break
		}
	}
	return best, best != p
}

func hasSubstrateAccess(r *decoRegion, s StateID, p Point) bool {
	if !s.is("sculk_vein") {
		return false
	}
	for d := 0; d < 6; d++ {
		if hasFace(s, d) && r.get(p.offset(d)).inTag("sculk_replaceable") {
			return true
		}
	}
	return false
}

// movementUnobstructed is whether a cursor may step from a to b: always
// to a face neighbour; across an edge, if one of the two faces it would
// pass is open.
func movementUnobstructed(r *decoRegion, a, b Point) bool {
	if manhattan(a, b) == 1 {
		return true
	}
	dx, dy, dz := b.X-a.X, b.Y-a.Y, b.Z-a.Z
	pick := func(v int32, neg, pos int) int {
		if v < 0 {
			return neg
		}
		return pos
	}
	x, y, z := pick(dx, dirWest, dirEast), pick(dy, dirDown, dirUp), pick(dz, dirNorth, dirSouth)
	open := func(d int) bool { return !r.get(a.offset(d)).sturdy(d ^ 1) }
	switch {
	case dx == 0:
		return open(y) || open(z)
	case dy == 0:
		return open(x) || open(z)
	}
	return open(x) || open(y)
}

// spreadAll spreads a multiface block from every face the source can
// spread from toward every direction, returning how many spread.
// sameSpace limits each spread to the block's own position.
func (m multiface) spreadAll(r *decoRegion, s StateID, p Point, sameSpace bool) int {
	n := 0
	otherSource := m.sculk && !s.is(m.block)
	for face := 0; face < 6; face++ {
		if !otherSource && !hasFace(s, face) {
			continue
		}
		for d := 0; d < 6; d++ {
			if dirAxis(d) == dirAxis(face) || !otherSource && hasFace(s, d) {
				continue
			}
			targets := []struct {
				p    Point
				face int
			}{{p, d}, {p.offset(d), face}, {p.offset(d).offset(face), d ^ 1}}
			if sameSpace {
				targets = targets[:1]
			}
			for _, t := range targets {
				cur := r.get(t.p)
				if !m.replaceable(r, p, t.p, t.face, cur) || !m.validFor(r, cur, t.p, t.face) {
					continue
				}
				if ns, ok := m.stateFor(r, cur, t.p, t.face); ok && r.set(t.p, ns) {
					n++
				}
				break
			}
		}
	}
	return n
}

func decodeSculkPatch(cfg any) func(*featureCtx) bool {
	chargeCount := fint(ffield(cfg, "charge_count"), 0)
	amount := fint(ffield(cfg, "amount_per_charge"), 0)
	spreadAttempts := fint(ffield(cfg, "spread_attempts"), 0)
	growthRounds := fint(ffield(cfg, "growth_rounds"), 0)
	spreadRounds := fint(ffield(cfg, "spread_rounds"), 0)
	extraRare := decodeIntProvider(ffield(cfg, "extra_rare_growths"))
	catalystChance := ffloat(ffield(cfg, "catalyst_chance"), 0)
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		if !canSculkSpreadFrom(r, o) {
			return false
		}
		var s sculkSpreader
		for i := int32(0); i < spreadRounds+growthRounds; i++ {
			for j := int32(0); j < chargeCount; j++ {
				s.addCursors(o, amount)
			}
			veins := i < spreadRounds
			for k := int32(0); k < spreadAttempts; k++ {
				s.update(r, o, rand, veins)
			}
			s.cursors = nil
		}
		below := o.offset(dirDown)
		if rand.NextFloat() <= catalystChance && r.get(below).isCollisionFullBlock() {
			r.set(o, defaultState("sculk_catalyst"))
		}
		n := extraRare.sample(rand)
		for i := int32(0); i < n; i++ {
			dx := rand.NextIntn(5) - 2
			dz := rand.NextIntn(5) - 2
			p := Point{o.X + dx, o.Y, o.Z + dz}
			if r.get(p).isAir() && r.get(p.offset(dirDown)).sturdy(dirUp) {
				r.set(p, defaultState("sculk_shrieker").with("can_summon", "true"))
			}
		}
		return true
	}
}

// canSculkSpreadFrom is a sculk block, or air or still water beside a
// full block.
func canSculkSpreadFrom(r *decoRegion, p Point) bool {
	s := r.get(p)
	if sculkKindOf(s) != sculkOther {
		return true
	}
	if !s.isAir() && !(s.is("water") && s.isSourceFluid()) {
		return false
	}
	for d := 0; d < 6; d++ {
		if r.get(p.offset(d)).isCollisionFullBlock() {
			return true
		}
	}
	return false
}
