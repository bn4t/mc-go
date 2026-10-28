package worldgen

import (
	"math"
	"sync"
)

// End features, measured from the 26.2 feature classes.

func init() {
	featureKinds["minecraft:end_spike"] = decodeEndSpike
	featureKinds["minecraft:end_island"] = func(any) func(*featureCtx) bool { return placeEndIsland }
	featureKinds["minecraft:chorus_plant"] = func(any) func(*featureCtx) bool { return placeChorusPlant }
	featureKinds["minecraft:end_gateway"] = decodeEndGateway
}

// decodeEndGateway is EndGatewayFeature: a gateway block, its exit set
// when configured, in a 3×5×3 frame: bedrock above and below it and as
// the arms of a cross around the columns over and under it, air
// elsewhere.
func decodeEndGateway(cfg any) func(*featureCtx) bool {
	var exit *Point
	if v := ffield(cfg, "exit"); v != nil {
		l := flist(v)
		p := Point{fint(l[0], 0), fint(l[1], 0), fint(l[2], 0)}
		exit = &p
	}
	exact := fbool(ffield(cfg, "exact"), false)
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		bedrock := defaultState("bedrock")
		for z := o.Z - 1; z <= o.Z+1; z++ {
			for y := o.Y - 2; y <= o.Y+2; y++ {
				for x := o.X - 1; x <= o.X+1; x++ {
					p := Point{x, y, z}
					sameX, sameY, sameZ := x == o.X, y == o.Y, z == o.Z
					yEdge := abs(y-o.Y) == 2
					switch {
					case sameX && sameY && sameZ:
						r.set(p, defaultState("end_gateway"))
						r.setBlockEntity(p, &GenBlockEntity{ID: "minecraft:end_gateway", ExitPortal: exit, ExactTeleport: exact && exit != nil})
					case sameY:
						r.set(p, stAir)
					case yEdge && sameX && sameZ, (sameX || sameZ) && !yEdge:
						r.set(p, bedrock)
					default:
						r.set(p, stAir)
					}
				}
			}
		}
		return true
	}
}

// placeChorusPlant grows a chorus plant, as a flower grows one, from an
// air origin on a block that supports it.
func placeChorusPlant(fc *featureCtx) bool {
	r, o := fc.r, fc.origin
	if !r.get(o).isAir() || !r.get(o.offset(dirDown)).inTag("supports_chorus_plant") {
		return false
	}
	setChorus(r, o)
	growChorus(r, fc.rand, o, o, 8, 0)
	return true
}

// setChorus sets a chorus plant joined to the chorus around it, and down
// to a block that supports it.
func setChorus(r *decoRegion, p Point) {
	isChorus := func(s StateID) bool { return s.is("chorus_plant") || s.is("chorus_flower") }
	below := r.get(p.offset(dirDown))
	s := defaultState("chorus_plant").with("down", boolStr(isChorus(below) || below.inTag("supports_chorus_plant")))
	for _, d := range []int{dirUp, dirNorth, dirEast, dirSouth, dirWest} {
		s = s.with(faceProps[d], boolStr(isChorus(r.get(p.offset(d)))))
	}
	r.set(p, s)
}

// chorusClear is whether p's horizontal neighbours, but the one towards
// except, are all air.
func chorusClear(r *decoRegion, p Point, except int) bool {
	for _, d := range horizontalDirs {
		if d != except && !r.get(p.offset(d)).isAir() {
			return false
		}
	}
	return true
}

// growChorus is ChorusFlowerBlock.growTreeRecursive: a stem of one to
// four (one more at the root), then up to three branches (four at the
// root) that each grow on within the spread of the root, or a dead
// flower when none does.
func growChorus(r *decoRegion, random Random, p, root Point, spread, depth int32) {
	height := random.NextIntn(4) + 1
	if depth == 0 {
		height++
	}
	for i := int32(0); i < height; i++ {
		q := p.add(Point{0, i + 1, 0})
		if !chorusClear(r, q, -1) {
			return
		}
		setChorus(r, q)
		setChorus(r, q.offset(dirDown))
	}
	branched := false
	if depth < 4 {
		branches := random.NextIntn(4)
		if depth == 0 {
			branches++
		}
		top := p.add(Point{0, height, 0})
		for range branches {
			d := horizontalDirs[random.NextIntn(4)]
			q := top.offset(d)
			if abs(q.X-root.X) >= spread || abs(q.Z-root.Z) >= spread {
				continue
			}
			if !r.get(q).isAir() || !r.get(q.offset(dirDown)).isAir() || !chorusClear(r, q, d^1) {
				continue
			}
			branched = true
			setChorus(r, q)
			setChorus(r, q.offset(d^1))
			growChorus(r, random, q, root, spread, depth+1)
		}
	}
	if !branched {
		r.set(p.add(Point{0, height, 0}), defaultState("chorus_flower").with("age", "5"))
	}
}

// placeEndIsland is EndIslandFeature: discs of end stone stacked
// downward, the first of radius 4 to 6, each next smaller by a half to
// one and a half, while the radius exceeds a half.
func placeEndIsland(fc *featureCtx) bool {
	r, random, o := fc.r, fc.rand, fc.origin
	stone := defaultState("end_stone")
	rad := float32(random.NextIntn(3)) + 4
	for y := int32(0); rad > 0.5; y-- {
		lo, hi := mthFloorF(-rad), mthCeilF(rad)
		reach := float32((rad + 1) * (rad + 1))
		for x := lo; x <= hi; x++ {
			for z := lo; z <= hi; z++ {
				if float32(x*x+z*z) <= reach {
					r.set(o.add(Point{x, y, z}), stone)
				}
			}
		}
		rad -= float32(random.NextIntn(2)) + 0.5
	}
	return true
}

// EndSpike is one obsidian pillar around the main island.
type EndSpike struct {
	CenterX, CenterZ int32
	Radius, Height   int32
	Guarded          bool
}

var (
	endSpikesMu    sync.Mutex
	endSpikesCache = map[int64][]EndSpike{}
)

// EndSpikes are the world's ten pillars: on a circle of radius 42, each
// slot taking a size from a shuffle of 0..9 keyed by 16 bits drawn from
// the world seed. Size k is radius 2+k/3 and height 76+3k; sizes 1 and
// 2 are caged.
func EndSpikes(seed int64) []EndSpike {
	key := NewLegacy(seed).NextLong() & 0xffff
	endSpikesMu.Lock()
	defer endSpikesMu.Unlock()
	if s, ok := endSpikesCache[key]; ok {
		return s
	}
	order := [10]int32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	r := NewLegacy(key)
	for i := int32(len(order)); i > 1; i-- {
		j := r.NextIntn(i)
		order[i-1], order[j] = order[j], order[i-1]
	}
	spikes := make([]EndSpike, 10)
	for i := range spikes {
		a := 2 * (-math.Pi + float64(0.3141592653589793*float64(i)))
		k := order[i]
		spikes[i] = EndSpike{
			CenterX: mthFloorD(float64(42 * jcos(a))),
			CenterZ: mthFloorD(float64(42 * jsin(a))),
			Radius:  2 + k/3,
			Height:  76 + k*3,
			Guarded: k == 1 || k == 2,
		}
	}
	endSpikesCache[key] = spikes
	return spikes
}

func decodeEndSpike(cfg any) func(*featureCtx) bool {
	invulnerable := fbool(ffield(cfg, "crystal_invulnerable"), false)
	var beam *Point
	if v := ffield(cfg, "crystal_beam_target"); v != nil {
		l := flist(v)
		p := Point{fint(l[0], 0), fint(l[1], 0), fint(l[2], 0)}
		beam = &p
	}
	var custom []EndSpike
	for _, s := range flist(ffield(cfg, "spikes")) {
		custom = append(custom, EndSpike{
			CenterX: fint(ffield(s, "centerX"), 0), CenterZ: fint(ffield(s, "centerZ"), 0),
			Radius: fint(ffield(s, "radius"), 0), Height: fint(ffield(s, "height"), 0),
			Guarded: fbool(ffield(s, "guarded"), false),
		})
	}
	return func(fc *featureCtx) bool {
		spikes := custom
		if len(spikes) == 0 {
			spikes = EndSpikes(fc.r.seed)
		}
		for _, s := range spikes {
			if s.CenterX>>4 == fc.origin.X>>4 && s.CenterZ>>4 == fc.origin.Z>>4 {
				placeEndSpike(fc, s, invulnerable, beam)
			}
		}
		return true
	}
}

func placeEndSpike(fc *featureCtx, s EndSpike, invulnerable bool, beam *Point) {
	r := fc.r
	rad := s.Radius
	limit := float64(rad*rad + 1)
	obsidian := defaultState("obsidian")
	for z := s.CenterZ - rad; z <= s.CenterZ+rad; z++ {
		for y := r.minY; y <= s.Height+10; y++ {
			for x := s.CenterX - rad; x <= s.CenterX+rad; x++ {
				p := Point{x, y, z}
				dx, dz := float64(x-s.CenterX), float64(z-s.CenterZ)
				switch {
				case float64(dx*dx)+float64(dz*dz) <= limit && y < s.Height:
					r.set(p, obsidian)
				case y > 65:
					r.set(p, stAir)
				}
			}
		}
	}
	if s.Guarded {
		bars := defaultState("iron_bars")
		for dx := int32(-2); dx <= 2; dx++ {
			for dz := int32(-2); dz <= 2; dz++ {
				for dy := int32(0); dy <= 3; dy++ {
					xEdge, zEdge, top := dx == 2 || dx == -2, dz == 2 || dz == -2, dy == 3
					if !xEdge && !zEdge && !top {
						continue
					}
					ns, ew := xEdge || top, zEdge || top
					b := bars.with("north", boolStr(ns && dz != -2)).with("south", boolStr(ns && dz != 2)).
						with("west", boolStr(ew && dx != -2)).with("east", boolStr(ew && dx != 2))
					r.set(Point{s.CenterX + dx, s.Height + dy, s.CenterZ + dz}, b)
				}
			}
		}
	}
	yaw := fc.rand.NextFloat() * 360
	r.addEntity(GenEntity{
		ID: "minecraft:end_crystal", X: float64(s.CenterX) + 0.5, Y: float64(s.Height + 1), Z: float64(s.CenterZ) + 0.5,
		Yaw: yaw, Invulnerable: invulnerable, BeamTarget: beam,
	})
	base := Point{s.CenterX, s.Height + 1, s.CenterZ}
	r.set(base.offset(dirDown), defaultState("bedrock"))
	r.set(base, defaultState("fire"))
}
