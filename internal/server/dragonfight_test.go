package server

import (
	"fmt"
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// A crystal's and a dragon's save keys read back through their loaders.
func TestEndEntitySaveRoundTrip(t *testing.T) {
	c := newEndCrystal(dimEnd, 1.5, 70, -2.5)
	c.Invulnerable = true
	c.crystal.showBottom = false
	crystalSetBeam(c, &world.Pos{X: 0, Y: 128, Z: 0})
	m := map[string]any{}
	crystalSaveFields(c, m)
	m["Invulnerable"] = nbtBool(true)
	if fmt.Sprint(m["beam_target"]) != "[0 128 0]" || m["ShowBottom"] != int8(0) {
		t.Fatalf("crystal save %v", m)
	}
	back := newEndCrystal(dimEnd, 0, 0, 0)
	applyCrystalSave(back, m, false)
	if !back.Invulnerable || back.crystal.showBottom || back.crystal.beam == nil || *back.crystal.beam != (world.Pos{Y: 128}) {
		t.Errorf("crystal load %+v %+v", back, back.crystal)
	}

	s := &Server{}
	d := &dimension{name: dimEnd}
	e := s.newEnderDragon(d, 0, 128, 0)
	e.dragon.phase = phaseSittingFlaming
	e.dragon.deathTime = 7
	e.dragon.sittingDamage = 12.5
	m = map[string]any{}
	dragonSaveFields(e, m)
	f := s.newEnderDragon(d, 0, 0, 0)
	applyDragonSave(f, m)
	if f.dragon.phase != phaseSittingFlaming || f.dragon.deathTime != 7 || f.dragon.sittingDamage != 12.5 {
		t.Errorf("dragon load %+v", f.dragon)
	}
}

// The fight's saved data reads back what it wrote; a default fight
// omits the optional fields.
func TestDragonFightSaveRoundTrip(t *testing.T) {
	u := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	p := world.Pos{X: 0, Y: 64, Z: 0}
	f := &dragonFight{needsStateScanning: false, dragonKilled: true, previouslyKilled: true,
		respawnStage: respawnSummoningPillars, respawnTime: 41, dragonUUID: &u, exitPortal: &p,
		gateways: []int32{3, 1, 2}, respawnCrystals: [][16]byte{u}}
	root := f.saveNBT()
	if root["DataVersion"] != int32(4903) {
		t.Fatalf("DataVersion %v", root["DataVersion"])
	}
	g := &dragonFight{}
	g.load(root["data"].(map[string]any))
	if g.needsStateScanning || !g.dragonKilled || !g.previouslyKilled || g.respawnStage != respawnSummoningPillars ||
		g.respawnTime != 41 || g.dragonUUID == nil || *g.dragonUUID != u || g.exitPortal == nil || *g.exitPortal != p ||
		fmt.Sprint(g.gateways) != "[3 1 2]" || len(g.respawnCrystals) != 1 || g.respawnCrystals[0] != u {
		t.Errorf("round trip lost state: %+v", g)
	}
	data := (&dragonFight{respawnStage: -1, needsStateScanning: true}).saveNBT()["data"].(map[string]any)
	for _, k := range []string{"respawn_stage", "dragon_uuid", "exit_portal_location", "respawn_crystals"} {
		if _, ok := data[k]; ok {
			t.Errorf("default fight wrote %s", k)
		}
	}
}

// The gateway order is Util.shuffle(0..19) over the level seed; the
// lists below are the ender_dragon_fight.dat gateways vanilla 26.2
// wrote for these seeds.
func TestDragonFightGatewayShuffle(t *testing.T) {
	for seed, want := range map[int64]string{
		12345: "[12 5 19 16 14 8 2 3 6 7 10 18 17 9 4 13 0 15 1 11]",
		1:     "[2 17 6 15 11 13 19 8 7 12 14 10 9 0 4 3 16 1 18 5]",
	} {
		gw := make([]int32, 20)
		for i := range gw {
			gw[i] = int32(i)
		}
		r := worldgen.NewLegacy(seed)
		for i := int32(len(gw)); i > 1; i-- {
			j := r.NextIntN(i)
			gw[i-1], gw[j] = gw[j], gw[i-1]
		}
		if got := fmt.Sprint(gw); got != want {
			t.Errorf("seed %d: gateways %s, want %s", seed, got, want)
		}
	}
}

// spawnNewGateway's ring: 96 blocks out at y 75, slot 0 at angle -2pi.
func TestDragonFightGatewayPos(t *testing.T) {
	if p := gatewayPos(0); p.X != 96 || p.Y != 75 || p.Z != 0 {
		t.Errorf("slot 0 at %+v", p)
	}
	// Slots 11 and 1 are the first two a seed-12345 fight opens; the
	// blackbox pin finds their gateways at these positions on vanilla.
	for slot, want := range map[int32]world.Pos{11: {X: -92, Y: 75, Z: -30}, 1: {X: 91, Y: 75, Z: 29}, 5: {X: -1, Y: 75, Z: 96}} {
		if p := gatewayPos(slot); p != want {
			t.Errorf("slot %d at %+v, want %+v", slot, p, want)
		}
	}
}

func TestOrbValueSplit(t *testing.T) {
	var got []int32
	for v := int32(960); v > 0; {
		o := orbValueFor(v)
		v -= o
		got = append(got, o)
	}
	if fmt.Sprint(got) != "[617 307 17 17 1 1]" {
		t.Errorf("960 splits into %v", got)
	}
}
