package server

import (
	"path/filepath"
	"testing"

	"mc-go/internal/world"
)

func TestServerDataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")

	s := New("unused")
	s.DataPath = path
	c, pr, _ := joinPlay(t, s, "Saver")
	drainJoinBurst(t, pr)
	sess := s.findSessionByName("Saver")
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 4})
	sess.addXP(25)
	sign, _ := world.BlockState("oak_sign")
	s.setBlockAndBroadcast("Saver", world.Pos{X: 1, Y: 2, Z: 3}, sign)
	var b [0]byte
	_ = b
	s.dims[dimOverworld].signs.mu.Lock()
	s.dims[dimOverworld].signs.signs[world.Pos{X: 1, Y: 2, Z: 3}].Front = [4]string{"saved", "", "", ""}
	s.dims[dimOverworld].signs.mu.Unlock()
	chest, _ := world.BlockState("chest")
	s.setBlockAndBroadcast("Saver", world.Pos{X: 4, Y: 5, Z: 6}, chest)
	s.chestAt(s.dims[dimOverworld], world.Pos{X: 4, Y: 5, Z: 6}).slots[2] = ItemStack{ID: world.ItemIDs["stone"], Count: 9}
	_ = c

	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}

	s2 := New("unused")
	s2.DataPath = path
	if err := s2.LoadServerData(); err != nil {
		t.Fatal(err)
	}
	p, ok := s2.savedPlayers["Saver"]
	if !ok {
		t.Fatal("player not restored")
	}
	if p.Inv[slotHotbarStart] != (ItemStack{ID: world.ItemIDs["dirt"], Count: 4}) {
		t.Fatalf("restored inventory hotbar = %+v", p.Inv[slotHotbarStart])
	}
	if p.XPTotal != 25 {
		t.Fatalf("restored xp = %d", p.XPTotal)
	}
	st, ok := s2.signAt(s2.dims[dimOverworld], world.Pos{X: 1, Y: 2, Z: 3})
	if !ok || st.Front[0] != "saved" {
		t.Fatalf("restored sign = %+v", st)
	}
	chestData := s2.chestAt(s2.dims[dimOverworld], world.Pos{X: 4, Y: 5, Z: 6})
	if chestData.slots[2] != (ItemStack{ID: world.ItemIDs["stone"], Count: 9}) {
		t.Fatalf("restored chest = %+v", chestData.slots[2])
	}
}

func TestReconnectAppliesSavedPlayer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s := New("unused")
	s.DataPath = path

	c1, pr1, _ := joinPlay(t, s, "Returner")
	drainJoinBurst(t, pr1)
	sess := s.findSessionByName("Returner")
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 4})
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	_ = c1

	s2 := New("unused")
	s2.DataPath = path
	if err := s2.LoadServerData(); err != nil {
		t.Fatal(err)
	}
	c2, pr2, _ := joinPlay(t, s2, "Returner")
	defer c2.Close()
	drainJoinBurst(t, pr2)
	sess2 := s2.findSessionByName("Returner")
	if got := sess2.stackAt(invSlotForHotbar(0)); got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 4}) {
		t.Fatalf("reconnect inventory = %+v", got)
	}
}
