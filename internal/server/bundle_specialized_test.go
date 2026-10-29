package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestBundleFurnaceTakeCapacityAndCallbacks(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "BundleSmelter", 8.5, -60, 2.5)
	pos := world.Pos{X: 8, Y: -61, Z: 0}
	block, _ := world.BlockState("furnace")
	s.setBlockAndBroadcast("BundleSmelter", pos, block)
	f := s.furnaceAt(sess.dimension(), pos)
	f.slots[furnaceOutput] = ItemStack{ID: world.ItemIDs["iron_ingot"], Count: 8}
	s.openFurnace(sess, pos)
	for len(sess.sendCh) > 0 {
		materializeOutboundPacket(t, sess, <-sess.sendCh)
	}
	cur := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	bundleSetItems(&cur, []ItemStack{{ID: world.ItemIDs["stone"], Count: 60}})
	sess.carriedMuSet(cur)
	s.handleClickContainer(sess, clickWindowPacket(t, sess, sess.window.id, furnaceOutput, 0, 0))
	if f.slots[furnaceOutput].Count != 8 || sess.carriedBuffer().Bundle != cur.Bundle {
		t.Fatal("partially took a take-only result")
	}
	f.slots[furnaceOutput].Count = 4
	s.handleClickContainer(sess, clickWindowPacket(t, sess, sess.window.id, furnaceOutput, 0, 0))
	if !f.slots[furnaceOutput].Empty() {
		t.Fatal("whole fitting result not taken")
	}
	if got := sess.ensureStats().counts[statKey{category: statCrafted, id: world.ItemIDs["iron_ingot"]}]; got != 4 {
		t.Fatalf("crafted=%d want4", got)
	}
}
