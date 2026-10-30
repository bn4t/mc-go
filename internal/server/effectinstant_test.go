package server

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestInstantaneousEffectPublicMetadata(t *testing.T) {
	data, err := os.ReadFile("testdata/vanilla-26.2/effect_instantaneous.tsv")
	if err != nil {
		t.Fatal(err)
	}
	rows, instant := 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			t.Fatal("invalid metadata row")
		}
		id, ok := MobEffectID(strings.TrimPrefix(f[0], "minecraft:"))
		if !ok {
			t.Fatal(f[0])
		}
		want, err := strconv.ParseBool(f[1])
		if err != nil {
			t.Fatal(err)
		}
		if isInstantaneousEffect(id) != want {
			t.Fatal("instantaneous flag", f[0])
		}
		if want {
			instant++
			if f[2] != "false" || f[3] != "true" {
				t.Fatal("instantaneous tick predicate", f[0])
			}
		}
		rows++
	}
	if rows != 40 || instant != 3 {
		t.Fatal("metadata coverage", rows, instant)
	}
}

func TestActiveInstantStatusTicksWithoutAddDose(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "InstantUnit")
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: p.getUUID(), Dim: dimOverworld, AttrBase: map[string]float64{"max_health": 1000}})
		p.setEntityID(e.ID)
		s.clock.count = 100
		p.health = 20
		if !s.playerGiveEffect(p, effectInstantHealth, 0, 3, false) {
			t.Fatal("fresh status declined")
		}
		if p.health != 20 {
			t.Fatal("stored status applied an add-time dose")
		}
		for i := 0; i < 3; i++ {
			s.clock.count++
			s.playerFluidTickStep()
			s.playerEffectTickStep()
			if p.health != float32(24+4*i) {
				t.Fatalf("tick%d health%v", i, p.health)
			}
		}
		if len(p.effects) != 0 {
			t.Fatal("finite status did not expire")
		}
		p.health = 10
		s.playerGiveEffect(p, effectInstantHealth, 1, 100, false)
		s.playerGiveEffect(p, effectInstantHealth, 0, 1, false)
		if p.health != 10 || p.effects[effectInstantHealth].amplifier != 1 {
			t.Fatal("declined status added a dose or replaced amplifier")
		}
		// A separate direct dose retains immediate, scaled delivery.
		s.applyInstantEffectScaled(p, effectInstantHealth, 0, .5)
		if p.health != 12 {
			t.Fatal("direct scaled potion dose changed")
		}
	})
}

func TestSaturationCommandUsesOneTick(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "SatUnit")
	s.simulationAction(func() {
		s.clock.count = 100
		s.runEffectGive(p.getName(), []string{"@s", "saturation"})
		inst, ok := p.effects[effectSaturation]
		if !ok || inst.expiry != 101 {
			t.Fatalf("default saturation deadline %+v", inst)
		}
		if p.saturation != 5 {
			t.Fatal("saturation applied before its tick")
		}
		s.clock.count++
		s.playerFluidTickStep()
		s.playerEffectTickStep()
		if p.saturation != 7 || len(p.effects) != 0 {
			t.Fatalf("one-tick saturation %v/%v", p.saturation, p.effects)
		}
	})
}

func TestActiveInstantMobTicksAndInversion(t *testing.T) {
	for _, c := range []struct {
		name        string
		effect      int32
		ticks       int32
		start, want float32
	}{{"villager", effectInstantHealth, 3, 1, 13}, {"villager", effectInstantDamage, 21, 20, 2}, {"zombie", effectInstantDamage, 3, 1, 13}, {"zombie", effectInstantHealth, 21, 20, 2}} {
		t.Run(c.name+"/"+strconv.Itoa(int(c.effect)), func(t *testing.T) {
			s := simulationTestServer(t)
			s.simulationAction(func() {
				s.clock.count = 100
				e := s.entities.Spawn(&Entity{Type: mustEntityType(c.name), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, PersistenceRequired: true, Health: c.start, MaxHealth: 20})
				s.mobGiveEffect(e, c.effect, 0, c.ticks, false)
				if e.Health != c.start {
					t.Fatal("mob status applied an add-time dose")
				}
				for i := int32(0); i < c.ticks; i++ {
					s.clock.count++
					s.mobTickStep()
					s.mobEffectTickStep()
				}
				if e.Health != c.want || len(e.Effects) != 0 {
					t.Fatalf("mob final health/effects %v/%v", e.Health, e.Effects)
				}
			})
		})
	}
}
