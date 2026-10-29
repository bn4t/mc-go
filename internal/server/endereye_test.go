package server

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// EnderEyeItem.use starts a zero-duration use even without a locate result.
// The active hand remains selected until release, item replacement, or a
// carried-slot switch; another use while active does not switch its hand.
func TestEnderEyeUseLifecycle(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "EyeUser", 0.5, 160, 0.5)
	eye := world.ItemIDs["ender_eye"]
	sess.setStack(slotOffhand, ItemStack{ID: eye, Count: 4})
	s.throwEnderEye(sess, 1, 0, -90)
	if sess.eyeUseHand != 2 || sess.usingItemID != eye {
		t.Fatalf("no-result offhand use: hand=%d item=%d", sess.eyeUseHand, sess.usingItemID)
	}
	s.enderEyeUseTick(sess)
	if sess.eyeUseHand != 2 {
		t.Fatal("zero duration completed the use")
	}
	sess.setStack(slotOffhand, ItemStack{ID: world.ItemIDs["stick"], Count: 1})
	s.enderEyeUseTick(sess)
	if sess.eyeUseHand != 0 || sess.usingItemID != 0 {
		t.Fatal("item replacement retained eye use")
	}
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: eye, Count: 4})
	sess.setStack(invSlotForHotbar(1), ItemStack{ID: eye, Count: 4})
	s.throwEnderEye(sess, 0, 0, -90)
	var b protocol.Buffer
	b.Short(1)
	s.handleHeld(sess, protocol.Packet{Data: b.Bytes()})
	if sess.eyeUseHand != 0 {
		t.Fatal("switching to an identical stack retained main-hand use")
	}
	sess.held.Store(0)
	s.throwEnderEye(sess, 0, 0, -90)
	s.handlePlayerActionExtras(sess, actionPacket(t, actionReleaseUse))
	if sess.eyeUseHand != 0 || sess.usingItemID != 0 {
		t.Fatal("release retained eye use")
	}
}

// Pairing sends nondefault living-use flags even when the observer missed
// the original dirty update. Idle/released use omits the default byte.
func TestEnderEyeUseAttachSnapshot(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "EyeOwner", 0.5, 160, 0.5)
	sess.setEntityID(123)
	e := &Entity{ID: 123}
	for _, tc := range []struct {
		hand  uint8
		off   bool
		flags byte
	}{{0, false, 0}, {1, false, 1}, {2, true, 3}, {0, true, 2}, {1, false, 1}, {0, false, 0}} {
		sess.eyeUseHand = tc.hand
		sess.eyeUseOffhand = tc.off
		got := s.playerAttachEyeUseFlags(e)
		if got != tc.flags {
			t.Fatalf("hand%d attachflags%d want%d", tc.hand, got, tc.flags)
		}
		payload := encodePlayerAttachMeta(20, 0, got)
		if got == 0 {
			if payload[0] != 9 {
				t.Fatalf("idle attach has extra metadata: %v", payload)
			}
		} else if payload[0] != 8 || payload[1] != 0 || payload[2] != got {
			t.Fatalf("active attach missing living-use flags: %v", payload)
		}
	}
	if got := s.playerAttachEyeUseFlags(&Entity{ID: 456}); got != 0 {
		t.Fatalf("unowned player flags=%d", got)
	}
}

func TestEnderEyeLaunchSyncedPose(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "EyePose", 0.5, 160, 0.5)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess.setEntityID(e.ID)
	for _, tc := range []struct {
		pose   int32
		height float32
	}{{0, 1.8}, {5, 1.5}, {3, 0.6}, {1, 0.6}} {
		e.Pose = tc.pose
		// Keep the requested input inconsistent with the actual fitted pose.
		sess.sneaking = tc.pose == 0
		if got := s.enderEyeLaunchHeight(sess); got != float64(tc.height) {
			t.Fatalf("synced pose%d height%v want%v", tc.pose, got, tc.height)
		}
	}
}
