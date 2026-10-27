package worldgen

import "math"

// Climate spawn search. The world spawn starts from the point whose
// surface climate fits the settings' spawn target best, distance from
// the origin breaking near-ties: fitness is the target distance (depth
// taken as zero) weighted by 2048², plus the squared block distance.
// The search scores the origin, then walks two rings of samples around
// the best point so far — out to 2048 blocks every 512, then out to 512
// every 32 — each ring's angular step its spacing over its radius, and
// takes a sample only when it is strictly fitter.

type spawnFit struct {
	x, z    int32
	fitness int64
}

func (rs *RandomState) spawnFitness(targets []ClimateSpan, x, z int32) spawnFit {
	t := rs.Climate(x>>2, 0, z>>2)
	t[4] = 0
	best := int64(math.MaxInt64)
	for i := range targets {
		sp := &targets[i]
		var d int64
		for a := 0; a < 6; a++ {
			g := axisGap(t[a], quantizeClimate(sp.Lo[a]), quantizeClimate(sp.Hi[a]))
			d += g * g
		}
		o := quantizeClimate(sp.Offset)
		best = min(best, d+o*o)
	}
	return spawnFit{x, z, best*2048*2048 + int64(x)*int64(x) + int64(z)*int64(z)}
}

// FindSpawn is the block column (y unspecified) the climate spawn
// search settles on; the origin when the settings name no target.
func (rs *RandomState) FindSpawn() (x, z int32) {
	targets := rs.Settings.SpawnTarget
	if len(targets) == 0 {
		return 0, 0
	}
	best := rs.spawnFitness(targets, 0, 0)
	ring := func(maxRadius, step float32) {
		bx, bz := best.x, best.z
		angle, radius := float32(0), step
		for radius <= maxRadius {
			x := bx + int32(math.Sin(float64(angle))*float64(radius))
			z := bz + int32(math.Cos(float64(angle))*float64(radius))
			if f := rs.spawnFitness(targets, x, z); f.fitness < best.fitness {
				best = f
			}
			angle += step / radius
			if float64(angle) > 2*math.Pi {
				angle = 0
				radius += step
			}
		}
	}
	ring(2048, 512)
	ring(512, 32)
	return best.x, best.z
}
