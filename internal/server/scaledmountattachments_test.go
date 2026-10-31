package server

import (
	"math"
	"testing"
)

// Independent public getter rows, reproduced by tools/probeseatattachments.
func TestConsumedScaledAttachmentFacts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scale    float32
		point    [3]float64
		vehicleY float64
	}{
		{"chicken", .5, [3]float64{0, .35, -.05}, 0},
		{"chicken", 1, [3]float64{0, .7, -.1}, 0},
		{"chicken", 2, [3]float64{0, 1.4, -.2}, 0},
		{"horse", .5, [3]float64{0, .721875011920929, 0}, 0},
		{"horse", 1, [3]float64{0, 1.443750023841858, 0}, 0},
		{"horse", 2, [3]float64{0, 2.887500047683716, 0}, 0},
		{"zombie", .5, [3]float64{0, 1.006250023841858, 0}, .3499999940395355},
		{"zombie", 1, [3]float64{0, 2.012500047683716, 0}, .699999988079071},
		{"zombie", 2, [3]float64{0, 4.025000095367432, 0}, 1.399999976158142},
	} {
		e := &Entity{Type: mustEntityType(tc.name), Living: true, mobCollisionScale: tc.scale}
		if got := passengerAttach(e); got != tc.point {
			t.Errorf("%s scale%g passenger%v want%v", tc.name, tc.scale, got, tc.point)
		}
		if got := riderVehicleAttachY(e); got != tc.vehicleY {
			t.Errorf("%s scale%g vehicleY%.17g want%.17g", tc.name, tc.scale, got, tc.vehicleY)
		}
	}
}

func TestScaledAttachmentPendingCacheAndPlayerPoint(t *testing.T) {
	vehicle := &Entity{Type: mustEntityType("chicken"), Living: true, X: 8.5, Y: 162, Z: .5, AttrBase: map[string]float64{"scale": 2}}
	if got := passengerAttach(vehicle); got != [3]float64{0, .7, -.1} {
		t.Fatal("raw attribute changed unconsumed attachment", got)
	}
	for _, tc := range []struct {
		scale float32
		y     float64
	}{{0, 162.1}, {.5, 162.4}, {1, 162.1}, {2, 161.5}} {
		x, y, z := playerSeatPos(vehicle, 7, tc.scale)
		if x != 8.5 || math.Abs(y-tc.y) > 1e-12 || z != .4 {
			t.Fatalf("player scale%g seat%v wantY%g", tc.scale, [3]float64{x, y, z}, tc.y)
		}
	}
	vehicle.mobCollisionScale = 2
	if got := passengerAttach(vehicle); got != [3]float64{0, 1.4, -.2} {
		t.Fatal("consumed attachment", got)
	}
}

func TestScaledNPCSeatNativeStack(t *testing.T) {
	chicken := &Entity{Type: mustEntityType("chicken"), Living: true, X: 8.5, Y: 161.4750000089407, Z: .5, mobCollisionScale: 2}
	zombie := &Entity{Type: mustEntityType("zombie"), Living: true, mobCollisionScale: 2}
	x, y, z := passengerSeatPos(chicken, zombie)
	if x != 8.5 || math.Abs(y-161.47500003278256) > 1e-12 || z != .3 {
		t.Fatalf("scaled zombie seat%v", [3]float64{x, y, z})
	}
	zombie.X, zombie.Y, zombie.Z = x, y, z
	x, y, z = playerSeatPos(zombie, 7, 2)
	if x != 8.5 || math.Abs(y-164.30000012815) > 1e-12 || z != .3 {
		t.Fatalf("nested player seat%v", [3]float64{x, y, z})
	}
}

func TestPlayerSeatUsesConsumedDimensionScale(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess := placeSession(t, s, "ScaleSeat", .5, 162, .5)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: sess.getUUID(), AttrBase: map[string]float64{"scale": 2}})
	sess.playerRecord.setEntityID(e.ID)
	sess.collisionScale = 1
	vehicle := s.entities.Spawn(&Entity{Type: mustEntityType("chicken"), Living: true, X: 8.5, Y: 162, Z: .5})
	sess.riding.Store(vehicle.ID)
	e.Riding = vehicle.ID
	vehicle.Passengers = []int32{e.ID}
	s.rideTickStep(sess)
	_, y, z, _, _, _ := sess.Pos()
	if y != 162.1 || z != .4 {
		t.Fatalf("pending player seatY%g Z%g", y, z)
	}
	s.refreshPlayerCollisionScale(&sess.playerRecord)
	s.rideTickStep(sess)
	_, y, z, _, _, _ = sess.Pos()
	if y != 161.5 || z != .4 {
		t.Fatalf("consumed player seatY%g Z%g", y, z)
	}
}
