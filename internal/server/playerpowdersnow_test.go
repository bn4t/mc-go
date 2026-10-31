package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerPowderFramesAndWearables(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "PowderFrames")
	s.simulationAction(func() {
		record := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld, X: .5, Y: .5, Z: .5})
		p.entityID.Store(record.ID)
		p.setPos(.5, 0, .5, 0, 0, false)
		pe, ok := s.entities.GetPtr(p.entityID.Load())
		if !ok {
			t.Fatal("player record absent")
		}
		s.world.SetBlock(world.Pos{}, world.BlockStateMust("powder_snow"))
		// The retained two-contact fixture is an unsupported column: falling
		// travel enters the lower powder row, as in the native legacy pin.
		s.world.SetBlock(world.Pos{Y: -1}, world.BlockStateMust("powder_snow"))
		for frame := 0; frame < 5; frame++ {
			s.playerInsideBlocks(p)
			s.playerPowderTick(p, pe)
			s.entityInsideStep()
		}
		if pe.TicksFrozen != 10 {
			t.Fatalf("five frames plus global passes froze%d, want10", pe.TicksFrozen)
		}
		p.setStack(slotArmorStart+3, ItemStack{ID: world.ItemIDs["leather_boots"], Count: 1})
		s.playerInsideBlocks(p)
		s.playerPowderTick(p, pe)
		if pe.TicksFrozen != 8 || !pe.IsInPowderSnow {
			t.Fatal("protected contact did not thaw two", pe.TicksFrozen, pe.IsInPowderSnow)
		}
		p.setStack(slotArmorStart+3, ItemStack{})
		p.bodyArmor = ItemStack{ID: world.ItemIDs["leather_horse_armor"], Count: 1}
		if playerCanFreeze(p) {
			t.Fatal("ARMOR group body slot ignored")
		}
		p.bodyArmor = ItemStack{}
		p.setStack(slotOffhand, ItemStack{ID: world.ItemIDs["leather_boots"], Count: 1})
		if !playerCanFreeze(p) {
			t.Fatal("offhand incorrectly grants wearable immunity")
		}
		p.setMode(GameModeSpectator)
		if playerCanFreeze(p) {
			t.Fatal("spectator can freeze")
		}
	})
}

func TestPlayerPowderUsesCachedBounds(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "PowderBounds")
	s.simulationAction(func() {
		record := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld})
		p.entityID.Store(record.ID)
		s.world.SetBlock(world.Pos{X: 1}, world.BlockStateMust("powder_snow"))
		p.setPos(.8, .5, .5, 0, 0, true)
		pe, ok := s.entities.GetPtr(p.entityID.Load())
		if !ok {
			t.Fatal("player absent")
		}
		p.collisionScale = .5
		s.playerInsideBlocks(p)
		if pe.IsInPowderSnow || pe.TicksFrozen != 0 {
			t.Fatal("small cached box reached adjacent powder")
		}
		p.collisionScale = 1
		s.playerInsideBlocks(p)
		if !pe.IsInPowderSnow || pe.TicksFrozen != 1 {
			t.Fatal("ordinary box missed adjacent powder")
		}
		pe.TicksFrozen = 0
		p.setPos(.700005, .5, .5, 0, 0, true)
		s.playerInsideBlocks(p)
		if pe.IsInPowderSnow {
			t.Fatal("inside deflation ignored near face")
		}
		p.setPos(.70002, .5, .5, 0, 0, true)
		s.playerInsideBlocks(p)
		if !pe.IsInPowderSnow {
			t.Fatal("inside deflation hid real overlap")
		}
	})
}
