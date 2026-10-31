package server

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func nativeDeathRowsByUUID(t *testing.T, s *Server) (map[[16]byte]map[string]any, map[[16]byte]int) {
	t.Helper()
	rows, err := world.LoadEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld))
	if err != nil {
		t.Fatal(err)
	}
	found := map[[16]byte]map[string]any{}
	depths := map[[16]byte]int{}
	var visit func(map[string]any, int)
	visit = func(row map[string]any, depth int) {
		if ints, ok := row["UUID"].([]int32); ok && len(ints) == 4 {
			uuid := uuidFromInts(ints)
			if _, dup := found[uuid]; dup {
				t.Errorf("duplicate saved UUID %x", uuid)
			}
			found[uuid] = row
			depths[uuid] = depth
		}
		if list, ok := row["Passengers"].(nbt.List); ok {
			for _, v := range list.Items {
				visit(v.(map[string]any), depth+1)
			}
		}
	}
	for _, row := range rows {
		visit(row, 0)
	}
	return found, depths
}

func TestNativeDyingEntitySaveAdmission(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.gameRules["minecraft:mob_drops"] = "false"
	root, ok := s.summonMobRaw(s.dims[dimOverworld], "zombie", 1, 0, 1, true, false)
	if !ok {
		t.Fatal("zombie")
	}
	root.NoGravity = true
	root.PersistenceRequired = true
	boat := s.restoreVehicleNative(s.dims[dimOverworld], "oak_boat", map[string]any{"NoGravity": int8(1)}, [3]float64{3, 0, 1}, [16]byte{})
	rider, ok := s.summonMobRaw(s.dims[dimOverworld], "pig", 3, 0, 1, true, false)
	if !ok {
		t.Fatal("pig")
	}
	rider.NoGravity = true
	rider.Riding = boat.ID
	boat.Passengers = []int32{rider.ID}
	liveChild, ok := s.summonMobRaw(s.dims[dimOverworld], "pig", 1, 0, 1, true, false)
	if !ok {
		t.Fatal("live child")
	}
	liveChild.NoGravity = true
	liveChild.Riding = root.ID
	root.Passengers = []int32{liveChild.ID}
	s.damageMob(root, 1000, "generic_kill", nil, nil)
	s.damageMob(rider, 1000, "generic_kill", nil, nil)
	if !root.Dying || !rider.Dying || root.Health != 0 || rider.Health != 0 || rider.Riding != boat.ID {
		t.Fatal("actual lethal damage/link precondition")
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	rows, depths := nativeDeathRowsByUUID(t, s)
	for _, tc := range []struct {
		e      *Entity
		depth  int
		health float32
	}{{root, 0, 0}, {rider, 1, 0}, {liveChild, 1, 10}} {
		row, ok := rows[tc.e.UUID]
		if !ok {
			t.Errorf("dying type%d absent from saved tree", tc.e.Type)
			continue
		}
		if row["Health"] != tc.health || row["DeathTime"] != int16(0) || depths[tc.e.UUID] != tc.depth {
			t.Errorf("saved corpse fields/depth: %#v depth%d", row, depths[tc.e.UUID])
		}
	}
}

// Saved Health/DeathTime and the root/passenger structure follow real frozen
// native lethal-damage saves and native 0->7->19->20 restart measurements.
func TestNativeDyingEntityRestartContinuation(t *testing.T) {
	for _, age := range []int16{0, 7, 19} {
		t.Run(fmt.Sprint(age), func(t *testing.T) {
			seed := nativeSaveTestServer(t)
			if err := seed.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			pos := nbt.List{Elem: nbt.TagDouble, Items: []any{float64(1), float64(0), float64(1)}}
			rootUUID := []int32{1, 2, 3, 4}
			boatUUID := []int32{5, 6, 7, 8}
			riderUUID := []int32{9, 10, 11, 12}
			corpse := func(name string, uuid []int32) map[string]any {
				return map[string]any{"id": "minecraft:" + name, "UUID": uuid, "Pos": pos, "Health": float32(0), "DeathTime": age, "NoAI": int8(1), "NoGravity": int8(1), "PersistenceRequired": int8(1)}
			}
			root := corpse("zombie", rootUUID)
			rider := corpse("pig", riderUUID)
			boat := map[string]any{"id": "minecraft:oak_boat", "UUID": boatUUID, "Pos": pos, "NoGravity": int8(1), "Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{rider}}}
			if err := world.SaveEntityRegions(world.DimensionDir(seed.WorldDir, dimOverworld), map[[2]int32][]map[string]any{{0, 0}: {root, boat}}); err != nil {
				t.Fatal(err)
			}
			loaded := nativeSaveTestServer(t)
			loaded.WorldDir = seed.WorldDir
			if err := loaded.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			loaded.gameRules["minecraft:mob_drops"] = "true"
			bodies := map[[16]byte]*Entity{}
			for _, copy := range loaded.entities.All() {
				e, _ := loaded.entities.GetPtr(copy.ID)
				bodies[e.UUID] = e
			}
			r, p, b := bodies[uuidFromInts(rootUUID)], bodies[uuidFromInts(riderUUID)], bodies[uuidFromInts(boatUUID)]
			if r == nil || p == nil || b == nil {
				t.Fatalf("restored bodies=%#v", bodies)
			}
			for _, e := range []*Entity{r, p} {
				if !e.Dying || e.Health != 0 || e.deathTicks != int32(age) {
					t.Errorf("restored corpse type%d dying%v health%v deathTicks%d want%d", e.Type, e.Dying, e.Health, e.deathTicks, age)
				}
			}
			if p.Riding != b.ID || !reflect.DeepEqual(b.Passengers, []int32{p.ID}) {
				t.Fatal("restored passenger link")
			}
			// Deliberately install a live credit sentinel after restore, rather than
			// claiming this credit is persisted by native NBT. Retirement must not invoke
			// death/loot/stat callbacks even when a valid credited session exists.
			player := placeSession(t, loaded, "CorpseCredit", .5, 0, .5)
			for _, e := range []*Entity{r, p} {
				e.LastHurtByPlayerTicks = 100
				e.LastHurtByPlayerUUID = player.getUUID()
				player.ensureStats().counts[statKey{statKilled, e.Type}] = 5
			}
			assertNoReplay := func() {
				t.Helper()
				for _, e := range loaded.entities.All() {
					name, _ := EntityTypeName(e.Type)
					if name == "item" || name == "experience_orb" {
						t.Fatalf("restored corpse replay spawned %s", name)
					}
				}
				for _, e := range []*Entity{r, p} {
					if got := player.ensureStats().counts[statKey{statKilled, e.Type}]; got != 5 {
						t.Fatalf("retirement replayed kill credit: %d", got)
					}
				}
			}
			assertNoReplay()
			for tick := int32(age) + 1; tick < 20; tick++ {
				loaded.mobTickStep()
				for _, e := range []*Entity{r, p} {
					if current, ok := loaded.entities.GetPtr(e.ID); !ok || current.deathTicks != tick {
						t.Errorf("corpse continuity tick%d present%v", tick, ok)
					}
				}
			}
			if err := loaded.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			saved, depths := nativeDeathRowsByUUID(t, loaded)
			for _, e := range []*Entity{r, p} {
				if row := saved[e.UUID]; row == nil || row["DeathTime"] != int16(19) {
					t.Errorf("save before retirement=%#v", row)
				}
			}
			if depths[p.UUID] != 1 {
				t.Error("dying passenger changed roots")
			}
			loaded.mobTickStep()
			for _, e := range []*Entity{r, p} {
				if _, ok := loaded.entities.GetPtr(e.ID); ok {
					t.Error("corpse retained at twentieth admitted tick")
				}
			}
			if len(b.Passengers) != 0 {
				t.Fatalf("retired passenger link remains: %v", b.Passengers)
			}
			assertNoReplay()
			if err := loaded.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			final, _ := nativeDeathRowsByUUID(t, loaded)
			if len(final) != 1 || final[b.UUID] == nil {
				t.Fatalf("retirement persisted corpse: %#v", final)
			}
			restarted := nativeSaveTestServer(t)
			restarted.WorldDir = loaded.WorldDir
			if err := restarted.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			for _, e := range restarted.entities.All() {
				if e.UUID == r.UUID || e.UUID == p.UUID {
					t.Fatal("retired corpse resurrected")
				}
			}
		})
	}
}
