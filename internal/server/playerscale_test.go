package server

import (
	"math"
	"testing"
)

func TestPlayerScaleFloatDimensions(t *testing.T) {
	for _, tc := range []struct {
		pose         int32
		scale        float32
		half, height float64
	}{
		{0, .7, .21000000834465027, 1.2599999904632568},
		{5, .7, .21000000834465027, 1.0499999523162842},
		{3, .7, .21000000834465027, .42000001668930054},
		{99, .7, .21000000834465027, 1.2599999904632568},
		{0, .0625, .01875000074505806, .11249999701976776},
		{2, 16, .10000000149011612, .20000000298023224},
		{7, .5, .10000000149011612, .20000000298023224},
	} {
		half, height := playerPoseDimsAtScale(tc.pose, tc.scale)
		if half != tc.half || height != tc.height {
			t.Errorf("pose%d scale%v got%v,%v want%v,%v", tc.pose, tc.scale, half, height, tc.half, tc.height)
		}
	}
}

func TestPlayerEffectiveScaleDimensions(t *testing.T) {
	s := New("unused")
	entity := s.entities.Spawn(&Entity{AttrBase: map[string]float64{"scale": .25}, AttrMods: map[string]map[string]attrMod{"scale": {"add": {value: .25}, "total": {value: 3, op: 2}}}})
	player := &playerRecord{}
	player.entityID.Store(entity.ID)
	half, height := s.playerCollisionDims(player)
	if half != .6000000238418579 || height != 3.5999999046325684 {
		t.Fatalf("effective modifier scale dimensions%v,%v", half, height)
	}
	entity.AttrMods = nil
	entity.AttrBase["scale"] = .70000000001
	half, height = s.playerCollisionDims(player)
	if half != .21000000834465027 || height != 1.2599999904632568 {
		t.Fatalf("double-to-float scale dimensions%v,%v", half, height)
	}
	for _, value := range []float64{0, math.NaN(), math.Inf(-1)} {
		entity.AttrBase["scale"] = value
		half, height = s.playerCollisionDims(player)
		if half != .01875000074505806 || height != .11249999701976776 {
			t.Errorf("minimum scale%v dimensions%v,%v", value, half, height)
		}
	}
	entity.AttrBase["scale"] = 32
	half, height = s.playerCollisionDims(player)
	if half != 4.800000190734863 || height != 28.799999237060547 {
		t.Fatalf("maximum scale dimensions%v,%v", half, height)
	}
	for _, pose := range []int32{2, 7} {
		entity.Pose = pose
		half, height = s.playerCollisionDims(player)
		if half != .10000000149011612 || height != .20000000298023224 {
			t.Errorf("fixed pose%d scale dimensions%v,%v", pose, half, height)
		}
	}
}
