package server

import "testing"

func TestArmorStandMarkerNative(t *testing.T) {
	e := &Entity{Type: mustEntityType("armor_stand"), Small: true, ShowArms: true, ArmorStandMarker: true, Health: 20, Living: true}
	if got := entityBBDims(e); got != [2]float64{} {
		t.Fatalf("marker box %v", got)
	}
	if encodeArmorStandFlags(e) != 21 || !entityIgnoresPistonPush(e) {
		t.Fatal("marker flags/reaction")
	}
	m := map[string]any{"Marker": int8(0)}
	armorStandSavedFields(e, m)
	if !nbtTruth(m["Marker"]) {
		t.Fatal("marker save")
	}
	armorStandLoadFields(e, map[string]any{"Small": int8(1), "ShowArms": int8(1)})
	armorStandSavedFields(e, m)
	if _, ok := m["Marker"]; ok {
		t.Fatal("false marker must be omitted")
	}
	if encodeArmorStandFlags(e) != 5 || entityIgnoresPistonPush(e) {
		t.Fatal("cleared marker flags/reaction")
	}
	if got := entityBBDims(e); got != [2]float64{.25, float64(float32(1.975) * .5)} {
		t.Fatalf("small box %v", got)
	}
	e.ArmorStandMarker = true
	ents := NewManager()
	ents.Spawn(e)
	if r := ents.Damage(e.ID, 2, "generic", 0); !r.Exists || !r.Graced || r.Health != 20 {
		t.Fatalf("marker damage %+v", r)
	}
	if r := ents.Damage(e.ID, 100, "generic_kill", 0); !r.Exists || r.Graced {
		t.Fatalf("bypass damage %+v", r)
	}
}

func TestArmorStandTravelPhysicsGate(t *testing.T) {
	e := &Entity{Type: mustEntityType("armor_stand")}
	if armorStandSkipsTravel(e) {
		t.Fatal("ordinary stand must travel")
	}
	e.NoGravity = true
	if !armorStandSkipsTravel(e) || entityIgnoresPistonPush(e) || entityBBDims(e) == [2]float64{} {
		t.Fatal("NoGravity only disables travel, not its normal push reaction or box")
	}
	e.NoGravity = false
	e.ArmorStandMarker = true
	if !armorStandSkipsTravel(e) || !entityIgnoresPistonPush(e) {
		t.Fatal("Marker physics/reaction")
	}
	e.Type = mustEntityType("cow")
	if armorStandSkipsTravel(e) {
		t.Fatal("armor-stand gate applied to another living type")
	}
}
