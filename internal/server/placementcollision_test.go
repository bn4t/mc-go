package server

import (
	"context"
	"testing"

	"mc-go/api/player"
	"mc-go/internal/world"
)

// A frontend-owned avatar is a building blocker just like a Java player.
// Drive the ordinary use-on packet and inspect placement and consumption.
func TestPlacementNeutralAvatarObstruction(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	builder := placeSession(t, s, "Builder", 3.5, 160, .5)
	builder.setMode(GameModeSurvival)
	builder.inv[slotHotbarStart] = ItemStack{ID: ItemStone, Count: 2}
	observer, observed := admitSharedAvatar(t, s, "WatchBlocker")
	away := observed.Pose
	away.X = 8
	away.Y = 160
	away.Z = 8
	if ok, err := observer.Move(context.Background(), away); err != nil || !ok {
		t.Fatalf("observer move%v %v", ok, err)
	}
	avatar, initial := admitSharedAvatar(t, s, "Blocker")
	pose := initial.Pose
	pose.X = .5
	pose.Y = 160
	pose.Z = .5
	if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
		t.Fatalf("blocker move%v %v", ok, err)
	}
	d := builder.dimension()
	target := world.Pos{Y: 160}
	s.simulationAction(func() { d.w.SetBlock(world.Pos{Y: 159}, world.Stone) })
	seq := int32(2500)
	s.simulationAction(func() { s.handleUseItem(builder, useItemPkt(0, 0, 159, 0, 1, &seq)) })
	if d.w.GetBlock(target) != world.Air || builder.inv[slotHotbarStart].Count != 2 {
		t.Fatal("neutral blocker allowed placement or consumed held item")
	}
	if err := avatar.Close(); err != nil {
		t.Fatal(err)
	}
	// Close revokes the handle before asynchronous canonical retirement. Wait
	// for the public observation that the blocker has actually left the world.
	for nextAvatarUpdate(t, observer, player.Removed).Player.ID != avatar.ID() {
	}
	seq++
	s.simulationAction(func() { s.handleUseItem(builder, useItemPkt(0, 0, 159, 0, 1, &seq)) })
	if d.w.GetBlock(target) != world.Stone || builder.inv[slotHotbarStart].Count != 1 {
		t.Fatalf("retired blocker control block%d count%d", d.w.GetBlock(target), builder.inv[slotHotbarStart].Count)
	}
}
