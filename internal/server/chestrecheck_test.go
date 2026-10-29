package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestChestValidityReachStrictBoundaryAndModifiers(t *testing.T) {
	s := New("unused")
	entity := s.entities.Spawn(&Entity{AttrBase: map[string]float64{}, AttrMods: map[string]map[string]attrMod{}})
	sess := &Session{server: s}
	sess.entityID.Store(entity.ID)
	pos := world.Pos{X: 10}
	sess.setMode(GameModeCreative)
	feetY := -float64(float32(1.62))
	sess.setPos(1, feetY, 0.5, 0, 0, true)
	if chestMenuInRange(sess, pos) {
		t.Fatal("creative exact9 boundary allowed")
	}
	sess.setPos(1.000001, feetY, 0.5, 0, 0, true)
	if !chestMenuInRange(sess, pos) {
		t.Fatal("creative inside9 boundary refused")
	}
	sess.setMode(GameModeSurvival)
	sess.setPos(1.5, feetY, 0.5, 0, 0, true)
	if chestMenuInRange(sess, pos) {
		t.Fatal("survival exact8.5 boundary allowed")
	}
	sess.setPos(1.500001, feetY, 0.5, 0, 0, true)
	if !chestMenuInRange(sess, pos) {
		t.Fatal("survival inside8.5 boundary refused")
	}
	entity.AttrBase["block_interaction_range"] = 2
	entity.AttrMods["block_interaction_range"] = map[string]attrMod{"double": {value: 1, op: 2}}
	sess.setMode(GameModeCreative)
	if got := chestInteractionRange(sess); got != 5 {
		t.Fatalf("creative modifier ordering reach%g want5", got)
	}
	entity.AttrBase["block_interaction_range"] = math.Inf(1)
	if got := chestInteractionRange(sess); got != 64 {
		t.Fatalf("range upper clamp%g", got)
	}
	entity.AttrBase["block_interaction_range"] = math.NaN()
	if got := chestInteractionRange(sess); got != 0 {
		t.Fatalf("NaN range clamp%g", got)
	}
}

func TestChestEyeHeightFixedPoses(t *testing.T) {
	s := New("unused")
	entity := s.entities.Spawn(&Entity{AttrBase: map[string]float64{"scale": 2}})
	sess := &Session{server: s}
	sess.entityID.Store(entity.ID)
	for _, tc := range []struct {
		pose int32
		eye  float64
	}{{0, float64(float32(1.62) * 2)}, {2, float64(float32(.2))}, {7, float64(float32(1.62))}} {
		entity.Pose = tc.pose
		bounds := chestBoundsOf(sess)
		if bounds.eyeY != tc.eye {
			t.Errorf("pose%d scaled eye%g want%g", tc.pose, bounds.eyeY, tc.eye)
		}
	}
}

func TestChestCurrentModeStopAndNegativeCount(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 3, Y: 10}
	block, _ := world.BlockState("trapped_chest")
	d.w.SetBlock(pos, block)
	storage := s.chestAt(d, pos)
	sess := &Session{server: s}
	w := &openWindow{dim: d, pos: pos, chest: storage, chestBlockName: "trapped_chest"}
	sess.setMode(GameModeSpectator)
	s.chestViewOpen(sess, w)
	if storage.openers != 0 {
		t.Fatal("spectator incremented openers")
	}
	sess.setMode(GameModeCreative)
	s.chestViewClose(sess, w)
	if storage.openers != -1 || trappedChestPower(d, pos) != 0 {
		t.Fatalf("stop uses current mode, raw=%d visible=%d", storage.openers, trappedChestPower(d, pos))
	}
	s.chestViewClose(sess, w)
	if storage.openers != -1 {
		t.Fatal("same menu retired twice")
	}
	next := &openWindow{dim: d, pos: pos, chest: storage, chestBlockName: "trapped_chest"}
	s.chestViewOpen(sess, next)
	if storage.openers != 0 || s.ticks.hasScheduled(schedKey{dim: d.name, pos: pos, kind: tickTrappedChestRecheck}) {
		t.Fatal("negative-to-zero increment incorrectly scheduled a first-open recount")
	}
}
