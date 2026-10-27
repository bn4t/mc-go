package worldgen

import "math"

// Ore blobs: a run of spheres along a random segment through the
// origin, their radii swelling toward the middle, spheres swallowed by a
// bigger neighbour dropped; each block of the target states inside the
// spheres becomes the ore, unless it touches air and the air-exposure
// discard chance says no. Writes go straight to the sections, as bulk
// section access does, leaving the heightmaps alone.

func init() {
	featureKinds["minecraft:ore"] = func(cfg any) func(*featureCtx) bool {
		c := decodeOreConfig(cfg)
		return c.place
	}
}

type oreTarget struct {
	test  ruleTest
	state StateID
}

type oreConfig struct {
	size    int32
	discard float32
	targets []oreTarget
}

func decodeOreConfig(cfg any) *oreConfig {
	c := &oreConfig{size: fint(ffield(cfg, "size"), 0), discard: ffloat(ffield(cfg, "discard_chance_on_air_exposure"), 0)}
	for _, t := range flist(ffield(cfg, "targets")) {
		c.targets = append(c.targets, oreTarget{decodeRuleTest(ffield(t, "target")), blockStateOf(ffield(t, "state"))})
	}
	return c
}

// mthCeil is the game's float ceiling to int.
func mthCeil(v float32) int32 {
	i := int32(v)
	if v > float32(i) {
		return i + 1
	}
	return i
}

func mthLerp(t, a, b float64) float64 { return a + float64(t*(b-a)) }

func (c *oreConfig) place(fc *featureCtx) bool {
	r, o := fc.rand, fc.origin
	angle := float32(r.NextFloat() * 3.1415927)
	spread := float32(c.size) / 8
	half := mthCeil((float32(float32(c.size)/16*2) + 1) / 2)
	sin, cos := math.Sin(float64(angle)), math.Cos(float64(angle))
	x1 := float64(o.X) + float64(sin*float64(spread))
	x2 := float64(o.X) - float64(sin*float64(spread))
	z1 := float64(o.Z) + float64(cos*float64(spread))
	z2 := float64(o.Z) - float64(cos*float64(spread))
	y1 := float64(o.Y + r.NextIntn(3) - 2)
	y2 := float64(o.Y + r.NextIntn(3) - 2)
	minX := o.X - mthCeil(spread) - half
	minY := o.Y - 2 - half
	minZ := o.Z - mthCeil(spread) - half
	width := 2 * (mthCeil(spread) + half)
	height := 2 * (2 + half)
	for x := minX; x <= minX+width; x++ {
		for z := minZ; z <= minZ+width; z++ {
			if minY <= fc.r.heightAt(hmOceanFloorWG, x, z) {
				return c.fill(fc, x1, x2, z1, z2, y1, y2, minX, minY, minZ, width, height)
			}
		}
	}
	return false
}

func (c *oreConfig) fill(fc *featureCtx, x1, x2, z1, z2, y1, y2 float64, minX, minY, minZ, width, height int32) bool {
	r := fc.rand
	n := int(c.size)
	spheres := make([]float64, 4*n)
	for i := 0; i < n; i++ {
		t := float32(i) / float32(n)
		px := mthLerp(float64(t), x1, x2)
		py := mthLerp(float64(t), y1, y2)
		pz := mthLerp(float64(t), z1, z2)
		reach := float64(r.NextDouble()*float64(n)) / 16
		radius := (float64(float64(mthSin(float64(float32(3.1415927*t)))+1)*reach) + 1) / 2
		spheres[4*i], spheres[4*i+1], spheres[4*i+2], spheres[4*i+3] = px, py, pz, radius
	}
	for i := 0; i < n-1; i++ {
		if !(spheres[4*i+3] > 0) {
			continue
		}
		for j := i + 1; j < n; j++ {
			if !(spheres[4*j+3] > 0) {
				continue
			}
			dx := spheres[4*i] - spheres[4*j]
			dy := spheres[4*i+1] - spheres[4*j+1]
			dz := spheres[4*i+2] - spheres[4*j+2]
			dr := spheres[4*i+3] - spheres[4*j+3]
			if float64(dr*dr) > float64(float64(dx*dx)+float64(dy*dy))+float64(dz*dz) {
				if dr > 0 {
					spheres[4*j+3] = -1
				} else {
					spheres[4*i+3] = -1
				}
			}
		}
	}
	done := make([]bool, width*height*width)
	placed := 0
	floor := func(v float64) int32 { return int32(math.Floor(v)) }
	for i := 0; i < n; i++ {
		rad := spheres[4*i+3]
		if rad < 0 {
			continue
		}
		px, py, pz := spheres[4*i], spheres[4*i+1], spheres[4*i+2]
		x0 := max(floor(px-rad), minX)
		y0 := max(floor(py-rad), minY)
		z0 := max(floor(pz-rad), minZ)
		xe := max(floor(px+rad), x0)
		ye := max(floor(py+rad), y0)
		ze := max(floor(pz+rad), z0)
		for x := x0; x <= xe; x++ {
			ex := (float64(x) + 0.5 - px) / rad
			if !(float64(ex*ex) < 1) {
				continue
			}
			for y := y0; y <= ye; y++ {
				ey := (float64(y) + 0.5 - py) / rad
				if !(float64(ex*ex)+float64(ey*ey) < 1) {
					continue
				}
				for z := z0; z <= ze; z++ {
					ez := (float64(z) + 0.5 - pz) / rad
					if !(float64(float64(ex*ex)+float64(ey*ey))+float64(ez*ez) < 1) || fc.r.outside(y) {
						continue
					}
					idx := (x - minX) + (y-minY)*width + (z-minZ)*width*height
					if done[idx] {
						continue
					}
					done[idx] = true
					p := Point{x, y, z}
					if !fc.r.canWrite(p) {
						continue
					}
					cur := fc.r.get(p)
					for _, t := range c.targets {
						if c.canPlace(fc, cur, t, p) {
							fc.r.chunk(x, z).setRaw(x&15, y, z&15, t.state)
							placed++
							break
						}
					}
				}
			}
		}
	}
	return placed > 0
}

func (c *oreConfig) canPlace(fc *featureCtx, cur StateID, t oreTarget, p Point) bool {
	if !t.test(cur, fc.rand) {
		return false
	}
	if skipAirCheck(fc.rand, c.discard) {
		return true
	}
	return !adjacentToAir(fc.r, p)
}

func skipAirCheck(r Random, chance float32) bool {
	if chance <= 0 {
		return true
	}
	if chance >= 1 {
		return false
	}
	return r.NextFloat() >= chance
}

func adjacentToAir(r *decoRegion, p Point) bool {
	for d := 0; d < 6; d++ {
		q := p.offset(d)
		if r.outside(q.Y) || r.get(q).isAir() {
			return true
		}
	}
	return false
}
