package server

import (
	"context"
	"fmt"
	"testing"

	"mc-go/internal/world"
)

func TestPlayerTickSkipsUnpublishedSessions(t *testing.T) {
	for _, phase := range []int32{StateConfiguration, StatePlay} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			s.GameMode = GameModeSurvival
			sess, _ := outboundPipe(t, s, false)
			sess.playerRecord.java, sess.playerRecord.core = sess, s
			sess.setIdentity("PendingTicks", offlineUUID("PendingTicks"))
			sess.state.Store(phase)
			sess.setPos(8.5, 80, 8.5, 0, 0, true)
			sess.initSurvival()
			s.simulationAction(func() {
				if err := s.reservePlayerOwned(context.Background(), &sess.playerRecord); err != nil {
					t.Fatal(err)
				}
			})
			s.track(sess)
			t.Cleanup(func() { s.simulationAction(func() { s.untrack(sess) }) })
			sess.attackTicker, sess.locTicks = 7, 19
			sess.combatTicks, sess.eatingTicks = 1, 1
			sess.food, sess.saturation, sess.takeXpDelay = 10, 0, 3
			bread := ItemStack{ID: world.ItemIDs["bread"], Count: 1}
			sess.inv[slotHotbarStart], sess.eatingItem = bread, bread.ID
			sess.healthDirty.Store(true)
			s.playerTick()
			if sess.attackTicker != 7 || sess.locTicks != 19 || sess.combatTicks != 1 || sess.eatingTicks != 1 ||
				sess.food != 10 || sess.saturation != 0 || sess.takeXpDelay != 3 || sess.inv[slotHotbarStart] != bread ||
				!sess.healthDirty.Load() || sess.invSnapOK || len(sess.sendCh) != 0 {
				t.Fatalf("unpublished player tick mutated gameplay: attack=%d location=%d combat=%d eating=%d food=%d saturation=%v xpDelay=%d bread=%+v healthDirty=%v inventorySnapshot=%v packets=%d",
					sess.attackTicker, sess.locTicks, sess.combatTicks, sess.eatingTicks, sess.food, sess.saturation, sess.takeXpDelay,
					sess.inv[slotHotbarStart], sess.healthDirty.Load(), sess.invSnapOK, len(sess.sendCh))
			}
			// The same gameplay clocks become eligible at actual admission.
			s.simulationAction(func() {
				sess.state.Store(StatePlay)
				s.commitPlayerAdmissionOwned(&sess.playerRecord)
			})
			s.playerTick()
			if sess.attackTicker != 8 || sess.locTicks != 20 || sess.combatTicks != 0 || sess.eatingTicks != 0 ||
				sess.food != 15 || !sess.inv[slotHotbarStart].Empty() || !sess.invSnapOK || len(sess.sendCh) == 0 {
				t.Fatal("admitted player's gameplay clocks did not resume")
			}
		})
	}
}
