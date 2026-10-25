package server

import (
	"testing"

	"mc-go/internal/protocol"
)

func actionPacket(t *testing.T, status int32) protocol.Packet {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(status)
	b.Position(0, 0, 0)
	b.VarInt(1)
	b.VarInt(0)
	return protocol.Packet{ID: IDPlayPlayerActionSB, Data: b.Bytes()}
}

func TestDropHeldItemAndStack(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Dropper", 8.5, -60, 8.5)
	sess.held.Store(2)
	sess.setStack(invSlotForHotbar(2), ItemStack{ID: 1, Count: 5})

	if !s.handlePlayerActionExtras(sess, actionPacket(t, actionDropItem)) {
		t.Fatal("drop item not handled")
	}
	if got := sess.stackAt(invSlotForHotbar(2)); got != (ItemStack{ID: 1, Count: 4}) {
		t.Fatalf("after drop one = %+v", got)
	}
	s.handlePlayerActionExtras(sess, actionPacket(t, actionDropStack))
	if !sess.stackAt(invSlotForHotbar(2)).Empty() {
		t.Fatalf("after drop stack = %+v", sess.stackAt(invSlotForHotbar(2)))
	}
}

func TestSwapHands(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Swapper", 8.5, -60, 8.5)
	sess.held.Store(0)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: 1, Count: 3})
	sess.setStack(slotOffhand, ItemStack{ID: 2, Count: 1})

	s.handlePlayerActionExtras(sess, actionPacket(t, actionSwapHands))
	if got := sess.stackAt(invSlotForHotbar(0)); got != (ItemStack{ID: 2, Count: 1}) {
		t.Fatalf("main hand = %+v", got)
	}
	if got := sess.stackAt(slotOffhand); got != (ItemStack{ID: 1, Count: 3}) {
		t.Fatalf("offhand = %+v", got)
	}
}

func TestDiggingStatusesNotStolen(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Digger", 8.5, -60, 8.5)
	if s.handlePlayerActionExtras(sess, actionPacket(t, digStart)) {
		t.Fatal("dig start must not be treated as an extra action")
	}
	if s.handlePlayerActionExtras(sess, actionPacket(t, digFinish)) {
		t.Fatal("dig finish must not be treated as an extra action")
	}
}

func TestSwingBroadcastReachesOthers(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	cb, prb, pwb := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	waitPlayerInfo(t, prb, "Alice")
	drainToQuiet(t, prb)
	waitSystemChat(t, pra, "Bob")

	var b protocol.Buffer
	b.VarInt(0) // main hand
	if err := pwb.WritePacket(IDPlaySwingArmSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	p, err := waitPacket(t, pra, IDPlayEntityAnimation)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // entity id
		t.Fatal(err)
	}
	if anim, err := r.Byte(); err != nil || anim != animSwingMainArm {
		t.Fatalf("animation = %d, err %v", anim, err)
	}
}

func TestClientInfoParsed(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Info", 8.5, -60, 8.5)
	var b protocol.Buffer
	b.String("en_US")
	b.Byte(12)
	b.VarInt(0) // chat mode: enabled
	b.Bool(true)
	b.Byte(0x7F)
	b.VarInt(1)
	b.Bool(false) // text filtering
	b.Bool(true)  // allow server listings
	b.VarInt(0)   // particle status: all
	s.handleClientInfo(sess, protocol.Packet{ID: IDPlayClientInfoSB, Data: b.Bytes()})
	sess.infoMu.Lock()
	info := sess.info
	sess.infoMu.Unlock()
	if info.Locale != "en_US" || info.ViewDistance != 12 || !info.ChatColors || info.MainHand != 1 {
		t.Fatalf("info = %+v", info)
	}
}

func TestFlightToggleEcho(t *testing.T) {
	s := New("unused") // creative: flight allowed
	c, pr, pw := joinPlay(t, s, "Flyer")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Flyer")
	if sess == nil {
		t.Fatal("session missing")
	}

	// Take off.
	var b protocol.Buffer
	b.Byte(abilityFlagFlying)
	if err := pw.WritePacket(IDPlayPlayerAbilitiesSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	p, err := waitPacket(t, pr, IDPlayAbilitiesCB)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	flags, err := r.Byte()
	if err != nil || flags&abilityFlagFlying == 0 {
		t.Fatalf("takeoff flags = %#x, err %v", flags, err)
	}

	// Land.
	b.Reset()
	b.Byte(0)
	if err := pw.WritePacket(IDPlayPlayerAbilitiesSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	p, err = waitPacket(t, pr, IDPlayAbilitiesCB)
	if err != nil {
		t.Fatal(err)
	}
	flags, _ = protocol.NewPacketReader(p.Data).Byte()
	if flags&abilityFlagFlying != 0 {
		t.Fatalf("landing flags = %#x", flags)
	}
}

func TestSurvivalCannotFly(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Walker")
	var b protocol.Buffer
	b.Byte(abilityFlagFlying)
	s.handlePlayerAbilities(sess, protocol.Packet{ID: IDPlayPlayerAbilitiesSB, Data: b.Bytes()})
	if sess.flying {
		t.Fatal("survival player started flying")
	}
}
