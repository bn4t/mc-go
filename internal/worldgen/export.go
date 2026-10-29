package worldgen

import (
	"encoding/json"

	"mc-go/internal/world"
)

// MotionBlocking is the MOTION_BLOCKING heightmap predicate.
func MotionBlocking(b world.Block) bool { return motionBlocking(b) }

// MotionBlockingNoLeaves is the MOTION_BLOCKING_NO_LEAVES predicate.
func MotionBlockingNoLeaves(b world.Block) bool { return motionBlockingNoLeaves(b) }

// BlocksMotion is BlockState.blocksMotion (the OCEAN_FLOOR predicate).
func BlocksMotion(b world.Block) bool { return blocksMotionState(b) }

// IsAir is BlockState.isAir.
func IsAir(b world.Block) bool { return isAir(b) }

// HasFluid reports a non-empty fluid state.
func HasFluid(b world.Block) bool { return hasFluid(b) }

// SturdyUp reports a full sturdy top face.
func SturdyUp(b world.Block) bool { return sturdyFull(b, dirUp) }

// LightEmission is BlockState.getLightEmission.
func LightEmission(b world.Block) int { return int(lightEmission(b)) }

// FindSpawnPosition is Climate.Sampler.findSpawnPosition over the
// settings' spawn_target points: the block x/z (y 0) of best fitness
// found by the two radial searches.
func (rs *RandomState) FindSpawnPosition() (int32, int32) {
	raw, ok := rs.settings.raw["spawn_target"]
	if !ok {
		return 0, 0
	}
	var pts []map[string]json.RawMessage
	jsonUnmarshal(raw, &pts)
	if len(pts) == 0 {
		return 0, 0
	}
	names := [climAxes]string{"temperature", "humidity", "continentalness", "erosion", "depth", "weirdness"}
	var targets []biomeEntry
	for _, p := range pts {
		var e biomeEntry
		for i, n := range names {
			lo, hi, err := parseSpan(p[n])
			if err != nil {
				panic(err)
			}
			e.params[i] = climParam{quantizeCoord(lo), quantizeCoord(hi)}
		}
		off, _, _ := parseSpan(p["offset"])
		e.offset = quantizeCoord(off)
		targets = append(targets, e)
	}
	fitness := func(x, z int32) int64 {
		t := rs.sampleClimate(nil, quartFromBlock(x), 0, quartFromBlock(z))
		t[climDepth] = 0
		best := int64(1<<63 - 1)
		for i := range targets {
			best = min(best, targets[i].fitness(&t))
		}
		d := int64(x)*int64(x) + int64(z)*int64(z)
		return best*2048*2048 + d
	}
	bx, bz := int32(0), int32(0)
	bestF := fitness(0, 0)
	search := func(maxR, step float32) {
		ang := float32(0)
		r := step
		ox, oz := bx, bz
		for r <= maxR {
			x := ox + int32(float64(javaSin(float64(ang)))*float64(r))
			z := oz + int32(float64(javaCos(float64(ang)))*float64(r))
			if f := fitness(x, z); f < bestF {
				bestF, bx, bz = f, x, z
			}
			ang += step / r
			if float64(ang) > 6.283185307179586 {
				ang = 0
				r += step
			}
		}
	}
	search(2048, 512)
	search(512, 32)
	return bx, bz
}

func init() {
	// Exact per-state heightmap predicates for every world, generated
	// or flat.
	world.HeightmapPredicates.WorldSurface = func(b world.Block) bool { return !isAir(b) }
	world.HeightmapPredicates.OceanFloor = blocksMotionState
	world.HeightmapPredicates.MotionBlocking = motionBlocking
	world.HeightmapPredicates.MotionBlockingNoLeaves = motionBlockingNoLeaves
}

// LightDampening is BlockState.getLightDampening.
func LightDampening(b world.Block) int { return int(props(b) >> bpDampeningShift & 15) }

// PropagatesSkylightDown is BlockState.propagatesSkylightDown.
func PropagatesSkylightDown(b world.Block) bool { return props(b)&(1<<bpSkyDown) != 0 }

// CanBeReplaced is BlockState.canBeReplaced().
func CanBeReplaced(b world.Block) bool { return isReplaceable(b) }

// IsSolid is the legacy BlockState.isSolid().
func IsSolid(b world.Block) bool { return isSolidState(b) }

// Support types for FaceSturdy (SupportType FULL, CENTER, RIGID).
const (
	SupportFull = iota
	SupportCenter
	SupportRigid
)

// FaceSturdy is BlockState.isFaceSturdy for direction dir (0 down, 1
// up, 2 north, 3 south, 4 west, 5 east) under a support type.
func FaceSturdy(b world.Block, dir, support int) bool {
	switch support {
	case SupportCenter:
		return sturdyCenter(b, dir)
	case SupportRigid:
		return sturdyRigid(b, dir)
	}
	return sturdyFull(b, dir)
}

// CollisionFaceFull is Block.isFaceFull(state.getCollisionShape(), dir).
func CollisionFaceFull(b world.Block, dir int) bool { return collisionFaceFull(b, dir) }

// InBlockTag reports b's block in a block tag (namespace trimmed).
func InBlockTag(b world.Block, tag string) bool { return blockInTag(b, tag) }

// BlockClasses is a block's behaviour class chain, most specific first.
func BlockClasses(name string) []string { return blockClass(name) }

// HasBlockClass reports whether a block's chain holds class.
func HasBlockClass(name, class string) bool { return hasClass(name, class) }

// MthSin and MthCos are Mth.sin / Mth.cos (the 65536-entry table).
func MthSin(d float64) float32 { return mthSin(d) }
func MthCos(d float64) float32 { return mthCos(d) }

// FluidIsWater is FluidState.is(Fluids.WATER): a still water source
// (waterlogged states included).
func FluidIsWater(b world.Block) bool { return fluidID(b) == "water" }

// FluidInWaterTag is FluidState.is(FluidTags.WATER).
func FluidInWaterTag(b world.Block) bool { return isWaterAt(b) }

// FluidAmount is FluidState.getAmount (8 for a source).
func FluidAmount(b world.Block) int { return int(fluidAmount(b)) }

// NoCollision reports an empty collision shape.
func NoCollision(b world.Block) bool { return props(b)&(1<<bpNoCollision) != 0 }

// FullCollision reports a full-cube collision shape.
func FullCollision(b world.Block) bool { return fullCollision(b) }

// MultifaceCanAttach is MultifaceBlock.canAttachTo toward dir from p.
func MultifaceCanAttach(g FuncGetter, p BlockPos, dir int) bool { return multifaceCanAttach(g, p, dir) }
