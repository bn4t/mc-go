package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerBurningPowderMelt(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "PowderMelt")
	s.simulationAction(func() {
		record := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld})
		p.entityID.Store(record.ID)
		p.setPos(.5, .1, .5, 0, 0, true)
		pe, _ := s.entities.GetPtr(record.ID)
		s.gameRules["minecraft:mob_griefing"] = "false"
		s.world.SetBlock(world.Pos{}, world.BlockStateMust("powder_snow"))
		p.fireTicks = 100
		beforeHealth := p.health
		s.playerInsideBlocks(p)
		if s.world.GetBlock(world.Pos{}) != world.Air || p.fireTicks > 0 || p.health != beforeHealth {
			t.Fatalf("burning snow block/fire/health=%d/%d/%g", s.world.GetBlock(world.Pos{}), p.fireTicks, p.health)
		}
		for i := 0; i < 6; i++ {
			s.playerInsideBlocks(p)
			s.playerPowderTick(p, pe)
		}
		if pe.TicksFrozen != 0 {
			t.Fatal("melted block continued freezing", pe.TicksFrozen)
		}
		s.world.SetBlock(world.Pos{}, world.BlockStateMust("powder_snow"))
		p.fireTicks = -20
		s.playerInsideBlocks(p)
		if s.world.GetBlock(world.Pos{}) == world.Air || p.fireTicks != -20 || pe.TicksFrozen != 1 {
			t.Fatal("nonburning control", p.fireTicks, pe.TicksFrozen)
		}
		// Denied melting still extinguishes; destruction permission does not gate
		// the ordinary freeze/extinguish contact effects.
		s.SpawnProtection = 100000
		s.ops = map[string]int{"OtherOperator": 4}
		if s.mayInteract(p, p.dimension(), world.Pos{}) {
			t.Fatal("protected-cell control not established")
		}
		p.fireTicks = 100
		s.playerInsideBlocks(p)
		if s.world.GetBlock(world.Pos{}) == world.Air || p.fireTicks > 0 || pe.TicksFrozen != 2 {
			t.Fatal("protected powder contact", p.fireTicks, pe.TicksFrozen)
		}
	})
}
