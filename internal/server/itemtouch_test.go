package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestPickupInventoryOrderingAndMainSlots(t *testing.T) {
	s := New("unused")
	p := placeSession(t, s, "Touch", .5, 70, .5)
	p.setMode(GameModeSurvival)
	p.health = 20
	p.spawned.Store(true)
	stone := ItemStack{ID: world.ItemIDs["stone"], Count: 60}
	p.inv[36] = stone
	p.inv[38] = stone
	p.inv[45] = stone
	p.held.Store(2)
	rem, changed := p.insertPickupStack(stone.withCount(10))
	if !changed || !rem.Empty() || p.inv[38].Count != 64 || p.inv[45].Count != 64 || p.inv[36].Count != 62 {
		t.Fatalf("merge order rem%+v selected%+v offhand%+v first%+v", rem, p.inv[38], p.inv[45], p.inv[36])
	}
	for i := slotMainStart; i < slotOffhand; i++ {
		p.inv[i] = ItemStack{ID: world.ItemIDs["dirt"], Count: 64}
	}
	p.inv[45] = ItemStack{}
	rem, changed = p.insertPickupStack(stone.withCount(3))
	if changed || rem.Count != 3 || !p.inv[45].Empty() {
		t.Fatal("empty offhand used as fresh inventory space")
	}
	p.inv[36] = stone
	rem, changed = p.insertPickupStack(stone.withCount(10))
	if !changed || rem.Count != 6 || p.inv[36].Count != 64 || !p.inv[45].Empty() {
		t.Fatal("partial transfer lost remainder")
	}
}

func TestPlayerItemTouchGatesAndPartialRemainder(t *testing.T) {
	s := New("unused")
	p := placeSession(t, s, "Touch", .5, 70, .5)
	p.health = 20
	p.spawned.Store(true)
	p.setMode(GameModeSurvival)
	spawn := func() *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: p.dimName(), X: .5, Y: 70.1, Z: .5, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 10}})
	}
	for i := slotMainStart; i <= slotOffhand; i++ {
		p.inv[i] = ItemStack{ID: world.ItemIDs["dirt"], Count: 64}
	}
	p.inv[36] = ItemStack{ID: world.ItemIDs["stone"], Count: 60}
	e := spawn()
	s.playerItemTouchTick(p)
	if e.Item.Count != 6 || p.inv[36].Count != 64 {
		t.Fatalf("partial item%+v slot%+v", e.Item, p.inv[36])
	}
	if _, ok := s.entities.GetPtr(e.ID); !ok {
		t.Fatal("partial entity discarded")
	}
	s.entities.Remove(e.ID)
	for i := range p.inv {
		p.inv[i] = ItemStack{}
	}
	e = spawn()
	e.PickupDelay = -1
	s.playerItemTouchTick(p)
	if e.Item.Count != 10 || !p.inv[36].Empty() {
		t.Fatal("negative delay accepted")
	}
	e.PickupDelay = 0
	e.HasOwner = true
	e.Owner = [16]byte{1}
	s.playerItemTouchTick(p)
	if !p.inv[36].Empty() {
		t.Fatal("foreign owner accepted")
	}
	e.Owner = p.getUUID()
	p.dead = true
	s.playerItemTouchTick(p)
	if !p.inv[36].Empty() {
		t.Fatal("dead player accepted")
	}
	p.dead = false
	p.setMode(GameModeSpectator)
	s.playerItemTouchTick(p)
	if !p.inv[36].Empty() {
		t.Fatal("spectator accepted")
	}
	p.setMode(GameModeCreative)
	s.playerItemTouchTick(p)
	if p.inv[36].Count != 10 {
		t.Fatal("creative pickup rejected")
	}
	if _, ok := s.entities.GetPtr(e.ID); ok {
		t.Fatal("full pickup retained entity")
	}
}

func TestItemTouchTargetSaveAndDelay(t *testing.T) {
	e := &Entity{}
	owner := [16]byte{1, 2, 3}
	applyItemTouchSave(e, map[string]any{"Owner": uuidInts(owner), "PickupDelay": int16(32767)})
	if !e.HasOwner || e.Owner != owner || e.PickupDelay != 32767 {
		t.Fatal("target/delay load")
	}
	applyItemTouchSave(e, map[string]any{})
	if e.HasOwner || e.Owner != ([16]byte{}) || e.PickupDelay != 0 {
		t.Fatal("absent target/delay did not reset")
	}
}

func TestItemTouchNativeTargetRoundTrip(t *testing.T) {
	original := New("unused")
	original.WorldDir = t.TempDir()
	d := original.dims[dimOverworld]
	owner := offlineUUID("Target")
	original.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: 2, Y: 72, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 3}, HasOwner: true, Owner: owner, PickupDelay: 32767})
	if err := original.saveEntityRegions(d); err != nil {
		t.Fatal(err)
	}
	restored := New("unused")
	restored.WorldDir = original.WorldDir
	restored.restoreEntities(restored.dims[d.name])
	items := restored.entities.All()
	if len(items) != 1 || !items[0].HasOwner || items[0].Owner != owner || items[0].PickupDelay != 32767 {
		t.Fatalf("native target/delay lost: %+v", items)
	}
}

func TestPlayerItemTouchStrictBoundingContact(t *testing.T) {
	s := New("unused")
	p := placeSession(t, s, "Touch", .5, 70, .5)
	p.health = 20
	p.spawned.Store(true)
	p.setMode(GameModeSurvival)
	half, _ := s.playerCollisionDims(&p.playerRecord)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: p.dimName(), X: .5 + half + 1 + .125, Y: 70.1, Z: .5, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 3}})
	s.playerItemTouchTick(p)
	if !p.inv[36].Empty() {
		t.Fatal("touching bounding-box faces should not overlap")
	}
	e.X -= .001
	s.playerItemTouchTick(p)
	if p.inv[36].Count != 3 {
		t.Fatal("overlapping bounding boxes did not touch")
	}
}

func TestItemTouchNativeNumericShortBoundaries(t *testing.T) {
	// ItemEntity.readAdditionalSaveData uses getShortOr for both counters,
	// including numeric tags whose stored type is not TAG_Short.
	cases := []struct {
		delay, age any
		wantDelay  int
		wantAge    int32
	}{
		{int32(65538), int32(32768), 2, -32768},
		{int32(-32769), int64(65539), 32767, 3},
		{float32(65538.75), float64(-32769.75), 2, 32766},
		{int64(-65537), int16(-1), -1, -1},
		{"bad", "bad", 0, 0},
		{int64(math.MaxInt64), int64(math.MinInt64), -1, 0},
		{math.Inf(1), math.Inf(-1), -1, 0},
		{math.NaN(), math.NaN(), 0, 0},
	}
	original := New("unused")
	original.WorldDir = t.TempDir()
	d := original.dims[dimOverworld]
	for i := range cases {
		original.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: float64(i + 2), Y: 72, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 3}})
	}
	groups := original.captureEntityRegionsOwned(d)
	for _, records := range groups {
		for _, save := range records {
			pos, ok := nbtVec3(save["Pos"])
			if !ok {
				t.Fatal("missing native position")
			}
			test := cases[int(pos[0])-2]
			save["Health"] = int64(math.MaxInt64)
			save["PickupDelay"] = test.delay
			save["Age"] = test.age
		}
	}
	if err := world.SaveEntityRegions(world.DimensionDir(original.WorldDir, d.name), groups); err != nil {
		t.Fatal(err)
	}
	restored := New("unused")
	restored.WorldDir = original.WorldDir
	restored.restoreEntities(restored.dims[d.name])
	loaded := restored.entities.All()
	if len(loaded) != len(cases) {
		t.Fatalf("loaded%d", len(loaded))
	}
	var immortal *Entity
	for _, e := range loaded {
		test := cases[int(e.X)-2]
		if e.PickupDelay != test.wantDelay || e.ItemAge != test.wantAge || e.ItemHP != -1 {
			t.Fatalf("raw delay%#v age%#v became delay%d age%d", test.delay, test.age, e.PickupDelay, e.ItemAge)
		}
		if e.ItemAge == -32768 {
			immortal, _ = restored.entities.GetPtr(e.ID)
		}
	}
	restored.itemTickStep()
	if immortal == nil || immortal.ItemAge != -32768 || immortal.PickupDelay != 1 {
		t.Fatal("immortal age or ordinary pickup-delay tick changed")
	}
	for _, e := range restored.entities.All() {
		if e.X == 3 && e.PickupDelay != 32767 {
			t.Fatal("never-pick-up delay decremented")
		}
	}
}
