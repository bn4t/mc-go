package worldgen

import "testing"

// TestBundledTemplatesDecode loads every bundled structure template and
// checks the transform helpers' corner cases.
func TestBundledTemplatesDecode(t *testing.T) {
	ids := TemplateIDs()
	if len(ids) < 1000 {
		t.Fatalf("only %d bundled templates", len(ids))
	}
	for _, id := range ids {
		tp := loadTemplate(id)
		if len(tp.palettes) == 0 {
			t.Errorf("%s: no palettes", id)
		}
	}
	sk := loadTemplate("fossil/skull_1")
	if sk.size != (BlockPos{6, 5, 7}) {
		t.Errorf("skull_1 size %v", sk.size)
	}
	// Rotating the far corner about the origin, then the zero position,
	// must land the rotated box back on the unrotated footprint.
	for rot := rotNone; rot <= rotCCW90; rot++ {
		s := newPlaceSettings()
		s.rotation = rot
		z := sk.zeroPosition(BlockPos{}, mirNone, rot)
		bb := sk.boundingBox(s, z)
		sz := sk.sizeFor(rot)
		if bb.x0 != 0 || bb.z0 != 0 || bb.x1 != sz[0]-1 || bb.z1 != sz[2]-1 {
			t.Errorf("rotation %d: box %+v, size %v", rot, bb, sz)
		}
	}
}
