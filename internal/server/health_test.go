package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestDamageSyncsHealth(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, pw := joinPlay(t, s, "Victim")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Victim")
	if sess == nil {
		t.Fatal("session missing")
	}
	s.damage(sess, 5, literal("test"), srcGeneric)
	// The sync lands on the next player tick, not on the damage call.
	s.playerTick()
	p, err := waitPacket(t, pr, IDPlaySetHealth)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	health, err := r.Float()
	if err != nil || health != 15 {
		t.Fatalf("health = %v, err %v (want 15)", health, err)
	}
	if food, err := r.VarInt(); err != nil || food != 20 {
		t.Fatalf("food = %d, err %v", food, err)
	}
	if sat, err := r.Float(); err != nil || sat != 5 {
		t.Fatalf("saturation = %v, err %v", sat, err)
	}
	_ = pw
}

func TestDeathAndRespawnFlow(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, pw := joinPlay(t, s, "Mortal")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Mortal")
	if sess == nil {
		t.Fatal("session missing")
	}
	s.killPlayer(sess, literal("Died for science"))
	if !sess.dead {
		t.Fatal("player not marked dead")
	}

	// Health 0, then Combat Death with the message component.
	hp, err := waitPacket(t, pr, IDPlaySetHealth)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := protocol.NewPacketReader(hp.Data).Float(); h != 0 {
		t.Fatalf("death health = %v, want 0", h)
	}
	cd, err := waitPacket(t, pr, IDPlayCombatDeath)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(cd.Data)
	if eid, err := r.VarInt(); err != nil || eid != sess.entityID.Load() {
		t.Fatalf("combat death eid = %d, err %v", eid, err)
	}
	// The message is an NBT text component; decode the remainder.
	root, err := readComponent(r)
	if err != nil || root != "Died for science" {
		t.Fatalf("combat death message = %q, err %v", root, err)
	}

	// Client Command 0 performs the respawn.
	var cc protocol.Buffer
	cc.VarInt(0)
	if err := pw.WritePacket(IDPlayClientStatusSB, cc.Bytes()); err != nil {
		t.Fatal(err)
	}
	rp, err := waitPacket(t, pr, IDPlayRespawn)
	if err != nil {
		t.Fatal(err)
	}
	rr := protocol.NewPacketReader(rp.Data)
	if dimType, err := rr.VarInt(); err != nil || dimType != 0 {
		t.Fatalf("respawn dimension type = %d, err %v", dimType, err)
	}
	if dim, err := rr.String(); err != nil || dim != "minecraft:overworld" {
		t.Fatalf("respawn dimension = %q, err %v", dim, err)
	}
	if _, err := rr.Long(); err != nil { // hashed seed
		t.Fatal(err)
	}
	if mode, err := rr.Byte(); err != nil || mode != GameModeSurvival {
		t.Fatalf("respawn game mode = %d, err %v", mode, err)
	}
	if prev, err := rr.Byte(); err != nil || prev != 255 {
		t.Fatalf("respawn previous mode = %d, err %v", prev, err)
	}
	if debug, err := rr.Bool(); err != nil || debug {
		t.Fatalf("respawn debug = %v, err %v", debug, err)
	}
	if flat, err := rr.Bool(); err != nil || !flat {
		t.Fatalf("respawn flat = %v, err %v", flat, err)
	}
	if death, err := rr.Bool(); err != nil || death {
		t.Fatalf("respawn death location = %v, err %v", death, err)
	}
	if cd, err := rr.VarInt(); err != nil || cd != 0 {
		t.Fatalf("respawn portal cooldown = %d, err %v", cd, err)
	}
	if sea, err := rr.VarInt(); err != nil || sea != -63 {
		t.Fatalf("respawn sea level = %d, err %v", sea, err)
	}
	if kept, err := rr.Byte(); err != nil || kept != 0 {
		t.Fatalf("respawn data kept = %d, err %v", kept, err)
	}

	hp2, err := waitPacket(t, pr, IDPlaySetHealth)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := protocol.NewPacketReader(hp2.Data).Float(); h != 20 {
		t.Fatalf("respawn health = %v, want 20", h)
	}
	if sess.dead {
		t.Fatal("player still dead after respawn")
	}
}

func TestFallDamageThreshold(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Faller")

	sess.fallDistance = 3.0
	s.applyFallDamage(sess)
	if sess.health != 20 {
		t.Fatalf("3-block fall dealt damage: health %v", sess.health)
	}
	sess.fallDistance = 8.4
	s.applyFallDamage(sess)
	// Vanilla floors the excess distance: floor(8.4 + 1e-6 - 3) = 5.
	if sess.health != 15 {
		t.Fatalf("8.4-block fall health = %v, want 15", sess.health)
	}
	if sess.fallDistance != 0 {
		t.Fatal("fall distance not reset")
	}
}

// readComponent decodes one unnamed NBT text component from a packet
// reader.
func readComponent(r *protocol.PacketReader) (string, error) {
	rest, err := r.Read(r.Remaining())
	if err != nil {
		return "", err
	}
	root, err := nbt.ReadUnnamed(bytes.NewReader(rest))
	if err != nil {
		return "", err
	}
	s, _ := root.(string)
	return s, nil
}

func attackPacket(t *testing.T, target int32) protocol.Packet {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(target)
	return protocol.Packet{ID: IDPlayAttackSB, Data: b.Bytes()}
}

func TestAttackDamagesPlayer(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	if alice == nil || bob == nil {
		t.Fatal("sessions missing")
	}
	// Both spawn at the same point: within attack reach. Charge the
	// cooldown so the scripted swing lands at full strength.
	bob.attackTicker = 200
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))

	if alice.health != 19 {
		t.Fatalf("alice health = %v, want 19", alice.health)
	}
	s.playerTick()
	// The hurt animation goes out with the attack; the health sync only
	// lands on the player tick, so the two can arrive in either order.
	var hp, ha protocol.Packet
	for i := 0; i < 256 && (hp.Data == nil || ha.Data == nil); i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if p.ID == IDPlaySetHealth {
			hp = p
		}
		if p.ID == IDPlayHurtAnimation {
			ha = p
		}
	}
	if hp.Data == nil || ha.Data == nil {
		t.Fatalf("health=%v anim=%v never both arrived", hp.Data != nil, ha.Data != nil)
	}
	if h, _ := protocol.NewPacketReader(hp.Data).Float(); h != 19 {
		t.Fatalf("set health = %v, want 19", h)
	}
	r := protocol.NewPacketReader(ha.Data)
	if eid, err := r.VarInt(); err != nil || eid != alice.entityID.Load() {
		t.Fatalf("hurt animation eid = %d, err %v", eid, err)
	}
	if _, err := r.Float(); err != nil {
		t.Fatalf("hurt yaw: %v", err)
	}
}

func TestAttackOutOfReachIgnored(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	alice.setPos(40, -60, 40, 0, 0, true)
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))
	if alice.health != 20 {
		t.Fatalf("out-of-reach attack dealt damage: %v", alice.health)
	}
}

func TestAttackLethalSendsCombatDeath(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	alice.health = 0.5
	bob.attackTicker = 200
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))
	if !alice.dead {
		t.Fatal("lethal attack did not kill")
	}
	if _, err := waitPacket(t, pra, IDPlayCombatDeath); err != nil {
		t.Fatalf("combat death: %v", err)
	}
}

func TestAttackSendsDamageEvent(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))

	p, err := waitPacket(t, pra, IDPlayDamageEvent)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if eid, err := r.VarInt(); err != nil || eid != alice.entityID.Load() {
		t.Fatalf("damage eid = %d, err %v", eid, err)
	}
	typeID, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	wantType := registryEntryID("minecraft:damage_type", "minecraft:player_attack")
	if typeID != wantType {
		t.Fatalf("damage type = %d, want player_attack %d", typeID, wantType)
	}
	cause, err := r.VarInt()
	if err != nil || cause != bob.entityID.Load()+1 {
		t.Fatalf("cause = %d, err %v (want %d)", cause, err, bob.entityID.Load()+1)
	}
	direct, err := r.VarInt()
	if err != nil || direct != bob.entityID.Load()+1 {
		t.Fatalf("direct = %d, err %v", direct, err)
	}
}

func TestFallSendsDamageEvent(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, _ := joinPlay(t, s, "Faller")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Faller")
	sess.fallDistance = 9
	s.applyFallDamage(sess)

	p, err := waitPacket(t, pr, IDPlayDamageEvent)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // entity
		t.Fatal(err)
	}
	typeID, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	if want := registryEntryID("minecraft:damage_type", "minecraft:fall"); typeID != want {
		t.Fatalf("fall damage type = %d, want %d", typeID, want)
	}
}

func TestCombatStatePackets(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))

	// Both fighters enter combat.
	if p, err := waitPacket(t, pra, IDPlayEnterCombat); err != nil || len(p.Data) != 0 {
		t.Fatalf("alice enter combat = %#x, err %v", p.ID, err)
	}
	if p, err := waitPacket(t, prb, IDPlayEnterCombat); err != nil || len(p.Data) != 0 {
		t.Fatalf("bob enter combat = %#x, err %v", p.ID, err)
	}
	if alice.combatTicks != combatTimeoutTicks || bob.combatTicks != combatTimeoutTicks {
		t.Fatalf("combat ticks = %d/%d", alice.combatTicks, bob.combatTicks)
	}

	// The timer expires and both receive End Combat with the duration.
	for i := 0; i < combatTimeoutTicks; i++ {
		s.combatTickStep()
	}
	p, err := waitPacket(t, pra, IDPlayEndCombat)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := protocol.NewPacketReader(p.Data).VarInt(); err != nil || d != combatTimeoutTicks {
		t.Fatalf("end combat duration = %d, err %v", d, err)
	}
	if alice.combatTicks != 0 {
		t.Fatal("alice still in combat")
	}
}

func TestVoidDamage(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, _ := joinPlay(t, s, "Void")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Void")
	sess.setPos(0, float64(int32(MinY-64))-5, 0, 0, 0, false)

	for i := 0; i < 10; i++ {
		s.playerFluidTickStep()
	}
	if sess.health != 16 {
		t.Fatalf("health = %v, want 16 after one void tick", sess.health)
	}
	p, err := waitPacket(t, pr, IDPlayDamageEvent)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil {
		t.Fatal(err)
	}
	typeID, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	if want := registryEntryID("minecraft:damage_type", "minecraft:out_of_world"); typeID != want {
		t.Fatalf("void damage type = %d, want %d", typeID, want)
	}
}

func TestVoidDamageKills(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Void")
	sess.setPos(0, float64(int32(MinY-64))-5, 0, 0, 0, false)
	// The void lives on the entity tick (baseTick's onBelowWorld);
	// post-hit invulnerability paces the hits to roughly every ten
	// ticks, so sixty ticks is lethal.
	for i := 0; i < 60; i++ {
		s.playerFluidTickStep()
	}
	if !sess.dead {
		t.Fatalf("player survived the void: health %v", sess.health)
	}
}

func TestWeaponDamage(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)
	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	bob.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["iron_sword"], Count: 1})
	bob.held.Store(0)

	bob.attackTicker = 200
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))
	if alice.health != 14 {
		t.Fatalf("alice health = %v, want 14 (iron sword 6)", alice.health)
	}
}

func TestArmorReducesDamage(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)
	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	armor := []string{"iron_helmet", "iron_chestplate", "iron_leggings", "iron_boots"}
	for i, name := range armor {
		alice.setStack(int16(slotArmorStart+i), ItemStack{ID: world.ItemIDs[name], Count: 1})
	}
	if got := alice.armorDefense(); got != 15 {
		t.Fatalf("armor = %v, want 15", got)
	}

	bob.attackTicker = 200
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))
	want := float32(20) - reduceDamage(1, 15, alice.armorToughness(), 0)
	if alice.health != want {
		t.Fatalf("alice health = %v, want %v", alice.health, want)
	}
}
