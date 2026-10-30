package server

import "testing"

func TestMobKnownSpeedNativeSamples(t *testing.T) {
	e := &Entity{X: 1, Y: 2, Z: 3, MotionX: .3, MotionY: .2, MotionZ: .1}
	sampleMobKnownSpeed(e)
	if e.knownSpeed != [3]float64{} {
		t.Fatal("first sample", e.knownSpeed)
	}
	e.X, e.Y, e.Z = 2, 4, 6
	if e.knownSpeed != [3]float64{} {
		t.Fatal("position change recomputed before tick", e.knownSpeed)
	}
	sampleMobKnownSpeed(e)
	if e.knownSpeed != [3]float64{1, 2, 3} {
		t.Fatal("displacement sample", e.knownSpeed)
	}
	e.X, e.Y, e.Z = 3, 5, 7
	if e.knownSpeed != [3]float64{1, 2, 3} {
		t.Fatal("unticked cache changed", e.knownSpeed)
	}
	sampleMobKnownSpeed(e)
	if e.knownSpeed != [3]float64{1, 1, 1} {
		t.Fatal("resumed sample", e.knownSpeed)
	}
	sampleMobKnownSpeed(e)
	if e.knownSpeed != [3]float64{} {
		t.Fatal("second stationary tick", e.knownSpeed)
	}
	if [3]float64{e.MotionX, e.MotionY, e.MotionZ} != [3]float64{.3, .2, .1} {
		t.Fatal("sampling changed velocity")
	}
}

func TestMobKnownSpeedTickGates(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("pig"), Living: true, NoAI: true, NoGravity: true, Health: 10, Dim: dimOverworld, X: 8.5, Y: -60, Z: 8.5})
	s.mobTickStep()
	if !e.knownSpeedInit || e.knownSpeed != [3]float64{} {
		t.Fatal("NoAI first tick did not sample", e.knownSpeed)
	}
	e.X += 2
	e.Y++
	e.Z += 3
	s.mobTickStep()
	if e.knownSpeed != [3]float64{2, 1, 3} {
		t.Fatal("NoAI displacement", e.knownSpeed)
	}
	e.Dormant = true
	e.X += 5
	s.mobTickStep()
	if e.knownSpeed != [3]float64{2, 1, 3} {
		t.Fatal("parked body sampled", e.knownSpeed)
	}
	e.Dormant = false
	s.mobTickStep()
	if e.knownSpeed != [3]float64{5, 0, 0} {
		t.Fatal("resumed body", e.knownSpeed)
	}
	e.X++
	s.playerTickOwned() // Frozen wall ticks run this pass without the mob pass.
	if e.knownSpeed != [3]float64{5, 0, 0} {
		t.Fatal("player-only frozen tick sampled body", e.knownSpeed)
	}
	s.mobTickStep()
	if e.knownSpeed != [3]float64{1, 0, 0} {
		t.Fatal("resumed game tick", e.knownSpeed)
	}
	s.mobTickStep()
	if e.knownSpeed != [3]float64{} {
		t.Fatal("stationary game tick", e.knownSpeed)
	}
}

func TestMobKnownSpeedPositionReapplication(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("pig"), Living: true, NoAI: true, NoGravity: true, Health: 10, Dim: dimOverworld, X: 1, Y: -60, Z: 3})
	s.tpMountTeleport(e, 5, -59, 7, 0, 0, 0)
	e.X++
	e.Y += 2
	e.Z += 3 // Movement after teleport, before the first base tick.
	s.mobTickStep()
	if e.knownSpeed != [3]float64{} {
		t.Fatal("teleport before first tick contributed displacement", e.knownSpeed)
	}
	e.X++
	e.Y++
	e.Z++
	s.mobTickStep()
	if e.knownSpeed != [3]float64{1, 1, 1} {
		t.Fatal("ordinary position change", e.knownSpeed)
	}
	s.tpMountTeleport(e, 8, -57, 10, 0, 0, 0)
	e.X++
	e.Y += 2
	e.Z += 3 // Native reset discards even this before-tick displacement.
	if e.knownSpeed != [3]float64{1, 1, 1} {
		t.Fatal("teleport recomputed cached speed before tick", e.knownSpeed)
	}
	s.mobTickStep()
	if e.knownSpeed != [3]float64{} {
		t.Fatal("teleport did not reset baseline", e.knownSpeed)
	}
	e.X++
	s.mobTickStep()
	if e.knownSpeed != [3]float64{1, 0, 0} {
		t.Fatal("post-teleport displacement", e.knownSpeed)
	}
	s.applyEntitySave(e, entitySaveMap(e, s.gameTime(), s.entities).m)
	e.X++
	e.Y += 2
	e.Z += 3
	if e.knownSpeed != [3]float64{1, 0, 0} {
		t.Fatal("load recomputed cached speed", e.knownSpeed)
	}
	s.mobTickStep()
	if e.knownSpeed != [3]float64{} {
		t.Fatal("load did not reset baseline", e.knownSpeed)
	}
}
