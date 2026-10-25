package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"mc-go/api/player"
)

// A removal caused by one overflowing observer is itself a reliable update.
// It can overflow a new admission's initial snapshot. That terminal admission
// must never announce a Java tab row after its removal has already gone out.
func TestPlayerReplicationAdmissionOverflowCascadeHasNoGhost(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	core := attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}
	admit := func(name string, messages int) (player.Connection, error) {
		return core.Admit(ctx, player.Profile{Name: name, UUID: offlineUUID(name)}, player.Options{Limits: player.Limits{Messages: messages}})
	}
	healthy, err := admit("CascadeHealthy", 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = healthy.Close() })
	if first := nextAvatarUpdate(t, healthy, player.Snapshot); len(first.Players) != 1 {
		t.Fatalf("initial healthy players=%d", len(first.Players))
	}
	slow, err := admit("CascadeSlow", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slow.Close() })
	if added := nextAvatarUpdate(t, healthy, player.Added); added.Player.ID != slow.ID() {
		t.Fatal("missing old slow avatar")
	}
	// A genuine network Session is used only as a packet sink in this test;
	// neutral admissions allocate no Java transport or fake player Session.
	java, _ := outboundPipe(t, s, false)
	s.sessions.Store(java.id, java)
	t.Cleanup(func() { s.sessions.Delete(java.id) })
	connection, err := admit("CascadeNew", 1)
	if connection != nil || !errors.Is(err, player.ErrOverflow) {
		t.Fatalf("overflowed admission=%v,%v", connection, err)
	}
	var updates []player.Update
	for range 3 {
		readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
		update, readErr := healthy.Next(readCtx)
		readCancel()
		if readErr != nil {
			t.Fatal(readErr)
		}
		updates = append(updates, update)
	}
	if updates[0].Kind != player.Added || updates[0].Player.Profile.Name != "CascadeNew" || updates[1].Kind != player.Removed || updates[1].Player.ID != slow.ID() || updates[2].Kind != player.Removed || updates[2].Player.ID != updates[0].Player.ID {
		t.Fatalf("cascade ordering=%+v", updates)
	}
	if updates[0].Revision >= updates[1].Revision || updates[1].Revision >= updates[2].Revision {
		t.Fatal("cascade revisions rewound")
	}
	if _, err := slow.Next(context.Background()); !errors.Is(err, player.ErrOverflow) {
		t.Fatalf("slow terminal=%v", err)
	}
	s.simulationAction(func() {
		if s.OnlineCount() != 1 || len(s.activePlayersOwned()) != 1 || s.findPlayerByNameOwned("CascadeNew") != nil || s.findPlayerByNameOwned("CascadeSlow") != nil {
			t.Errorf("cascade left ghost actors/count: online=%d", s.OnlineCount())
		}
	})
	if healthy.(*avatarConnection).mailbox.Err() != nil {
		t.Fatal("healthy observer was retired")
	}
	for len(java.sendCh) > 0 {
		packet := <-java.sendCh
		if packet.id == java.ids().PlayPlayerInfoUpd && bytes.Contains(packet.data, []byte("CascadeNew")) {
			t.Fatal("retired admission announced a ghost Java tab row")
		}
		java.retireOutbound(packet)
	}
}
