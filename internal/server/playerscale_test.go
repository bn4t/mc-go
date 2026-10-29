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
	half, height := s.playerRecordPoseDims(player, entity.Pose)
	if half != .6000000238418579 || height != 3.5999999046325684 {
		t.Fatalf("effective modifier scale dimensions%v,%v", half, height)
	}
	entity.AttrMods = nil
	entity.AttrBase["scale"] = .70000000001
	half, height = s.playerRecordPoseDims(player, entity.Pose)
	if half != .21000000834465027 || height != 1.2599999904632568 {
		t.Fatalf("double-to-float scale dimensions%v,%v", half, height)
	}
	for _, value := range []float64{0, math.NaN(), math.Inf(-1)} {
		entity.AttrBase["scale"] = value
		half, height = s.playerRecordPoseDims(player, entity.Pose)
		if half != .01875000074505806 || height != .11249999701976776 {
			t.Errorf("minimum scale%v dimensions%v,%v", value, half, height)
		}
	}
	entity.AttrBase["scale"] = 32
	half, height = s.playerRecordPoseDims(player, entity.Pose)
	if half != 4.800000190734863 || height != 28.799999237060547 {
		t.Fatalf("maximum scale dimensions%v,%v", half, height)
	}
	for _, pose := range []int32{2, 7} {
		entity.Pose = pose
		half, height = s.playerRecordPoseDims(player, entity.Pose)
		if half != .10000000149011612 || height != .20000000298023224 {
			t.Errorf("fixed pose%d scale dimensions%v,%v", pose, half, height)
		}
	}
}

func TestPlayerCurrentScaleWaitsForFrameRefresh(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	_, state := admitSharedAvatar(t, s, "ScaleFrame")
	s.simulationAction(func() {
		record := s.findPlayerByIDOwned(state.ID)
		entity, _ := s.entities.GetPtr(state.EntityID)
		entity.AttrBase = map[string]float64{"scale": 1}
		full, _ := playerPoseDimsAtScale(0, 1)
		small, _ := playerPoseDimsAtScale(0, .5)
		current := func() float64 { half, _ := s.playerCollisionDims(record); return half }
		if current() != full {
			t.Fatal("constructor dimensions do not use scale one")
		}
		entity.AttrBase["scale"] = .5
		if current() != full {
			t.Fatal("attribute write changed current box before a frame")
		}
		if half, _ := s.playerRecordPoseDims(record, 0); half != small {
			t.Fatal("hypothetical pose did not use live effective scale")
		}
		s.playerTickOwned()
		if current() != small {
			t.Fatal("connection frame did not refresh current avatar box")
		}
		record.setDimension(s.dims[dimNether])
		s.entities.SetDimension(entity.ID, dimNether)
		if current() != small {
			t.Fatal("dimension transfer changed the existing current box")
		}
		entity.AttrBase["scale"] = 1
		if current() != small {
			t.Fatal("growth changed current box before a frame")
		}
		s.playerTickOwned()
		if current() != full {
			t.Fatal("connection frame did not apply growth")
		}
		entity.AttrBase["scale"] = .5
		s.playerTickOwned()
		replacement := s.entities.Spawn(&Entity{Type: mustEntityType("player"), AttrBase: map[string]float64{"scale": 2}, presenceOnly: true})
		s.entities.Remove(entity.ID)
		record.setEntityID(replacement.ID)
		if current() != full {
			t.Fatal("new physical incarnation retained old cached dimensions")
		}
		s.playerTickOwned()
		twice, _ := playerPoseDimsAtScale(0, 2)
		if current() != twice {
			t.Fatal("new physical incarnation did not refresh copied scale")
		}

	})
}

func TestPlayerRespawnScaleStartsAtConstructorDimensions(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	sess := placeSession(t, s, "ScaleRespawn", .5, 10, .5)
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld, AttrBase: map[string]float64{"scale": 2}})
	sess.setEntityID(entity.ID)
	s.simulationAction(func() {
		s.playerTickOwned()
		twice, _ := playerPoseDimsAtScale(0, 2)
		full, _ := playerPoseDimsAtScale(0, 1)
		if half, _ := s.playerCollisionDims(&sess.playerRecord); half != twice {
			t.Fatal("fixture did not apply initial scale")
		}
		sess.dead = true
		s.respawnPlayer(sess)
		if half, _ := s.playerCollisionDims(&sess.playerRecord); half != full {
			t.Fatal("respawn retained the dead body's cached scale")
		}
		if entity.AttrBase["scale"] != 2 {
			t.Fatal("respawn lost the copied base scale")
		}
		s.playerTickOwned()
		if half, _ := s.playerCollisionDims(&sess.playerRecord); half != twice {
			t.Fatal("respawn living tick did not refresh copied scale")
		}
	})
}

func TestPlayerPoseChangeRefreshesPendingScale(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	sess := placeSession(t, s, "PoseScale", .5, 10, .5)
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld, X: .5, Y: 10, Z: .5, AttrBase: map[string]float64{"scale": .5}})
	sess.setEntityID(entity.ID)
	s.simulationAction(func() {
		sess.sneaking = true
		s.updatePlayerPose(sess)
		wantHalf, wantHeight := playerPoseDimsAtScale(5, .5)
		half, height := s.playerCollisionDims(&sess.playerRecord)
		if entity.Pose != 5 || half != wantHalf || height != wantHeight {
			t.Fatalf("pose change pending scale pose%d dims%v,%v want%v,%v", entity.Pose, half, height, wantHalf, wantHeight)
		}
	})
}
