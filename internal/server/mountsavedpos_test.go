package server

import (
	"mc-go/internal/nbt"
	"testing"
)

func TestMountedSavedPositionUsesDirectVehicle(t *testing.T) {
	ents := NewManager()
	root := ents.Spawn(&Entity{X: 100, Y: 200, Z: 300})
	vehicle := ents.Spawn(&Entity{X: 10, Y: 20, Z: 30, Riding: root.ID})
	rider := ents.Spawn(&Entity{X: 11, Y: 22, Z: 33, Riding: vehicle.ID})
	check := func(label string, m map[string]any, want [3]float64) {
		t.Helper()
		p := m["Pos"].(nbt.List)
		for i, v := range want {
			if p.Items[i] != v {
				t.Fatalf("%s axis%d got%v want%v", label, i, p.Items[i], v)
			}
		}
	}
	check("mob", entitySaveMap(rider, 0, ents).m, [3]float64{10, 22, 30})
	sess := &Session{server: New("unused")}
	t.Cleanup(sess.server.Stop)
	sess.setPos(11, 22, 33, 0, 0, false)
	sess.riding.Store(vehicle.ID)
	check("player", playerSaveMap(sess, 0, ents), [3]float64{10, 22, 30})
	x, y, z, _, _, _ := sess.Pos()
	if x != 11 || y != 22 || z != 33 || rider.X != 11 || rider.Z != 33 {
		t.Fatal("save changed live seats")
	}
	check("unresolved", entitySaveMap(rider, 0, nil).m, [3]float64{11, 22, 33})
	rider.Riding = 0
	sess.riding.Store(0)
	check("unmounted mob", entitySaveMap(rider, 0, ents).m, [3]float64{11, 22, 33})
	check("unmounted player", playerSaveMap(sess, 0, ents), [3]float64{11, 22, 33})
}
