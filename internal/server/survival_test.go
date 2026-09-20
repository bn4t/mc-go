package server

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func survivalSession(t *testing.T, s *Server, name string) *Session {
	t.Helper()
	s.GameMode = GameModeSurvival
	sess := placeSession(t, s, name, 0.5, 0.5, 0.5)
	// Look straight down so the block below is the interaction target.
	sess.setPos(0.5, 0.5, 0.5, 0, 90, true)
	sess.initSurvival()
	sess.spawned.Store(true)
	return sess
}

func TestSurvivalDigDropsItemAfterBreakTime(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Miner")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digFinish)
	// Dirt is hardness 0.5 and bare hands harvest it, so vanilla's
	// formula gives 15 ticks of progress.
	for i := 0; i < 14; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Dirt {
		t.Fatalf("block broke after 14 of 15 ticks: %d", got)
	}
	s.digTickStep()

	if got := s.world.GetBlock(pos); got != world.Air {
		t.Fatalf("block = %d, want air", got)
	}
	// The dirt drop is now an item entity.
	s.entities.mu.RLock()
	var found *Entity
	for _, e := range s.entities.byID {
		if e.Item.ID == world.ItemIDs["dirt"] {
			found = e
		}
	}
	s.entities.mu.RUnlock()
	if found == nil {
		t.Fatal("no dirt item entity spawned")
	}
	if found.PickupDelay != 10 {
		t.Fatalf("pickup delay = %d, want 10", found.PickupDelay)
	}
}

func TestSurvivalFinishBreaksTrackedStart(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Miner")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digFinish)
	for i := 0; i < 15; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Air {
		t.Fatal("tracked dig did not break on finish")
	}
}

// A dig that is never committed by a finish is not destroyed, however
// long the player stares at it (verified against the real server).
func TestSurvivalStartAloneNeverBreaks(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Starr")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	for i := 0; i < 600; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Dirt {
		t.Fatal("an uncommitted dig broke the block")
	}
}

// An abort discards the dig entirely, and the finish that follows has
// nothing to commit (the real server ignores a stop with no start).
func TestSurvivalAbortResetsProgress(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Fickle")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digCancel)
	s.handleDigSurvival(sess, pos, digFinish)
	for i := 0; i < 200; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Dirt {
		t.Fatal("cancelled dig still broke the block")
	}
	if sess.digActive {
		t.Fatal("a finish with no start opened a dig")
	}
}

// A second start on the same block cancels the dig, as the real server
// does (it resyncs the client instead of completing the break).
func TestSurvivalDuplicateStartCancels(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Twitchy")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digFinish)
	for i := 0; i < 8; i++ {
		s.digTickStep()
	}
	s.handleDigSurvival(sess, pos, digStart)
	for i := 0; i < 200; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Dirt {
		t.Fatal("restarted dig completed without a finish")
	}
}

// Out-of-reach digs are refused outright.
func TestSurvivalDigBeyondReachIgnored(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Farmer")
	pos := world.Pos{X: 12, Y: 0, Z: 12}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digStart)
	if sess.digActive {
		t.Fatal("started a dig twelve blocks away")
	}
}

func TestSurvivalFinishWithoutStartIgnored(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Miner")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Dirt)

	s.handleDigSurvival(sess, pos, digFinish)
	if got := s.world.GetBlock(pos); got != world.Dirt {
		t.Fatal("finish without a start broke the block")
	}
}

func TestSurvivalStoneNeedsTool(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Miner")
	pos := world.Pos{X: 0, Y: 0, Z: 0}
	s.world.SetBlock(pos, world.Stone)

	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digFinish)
	// Stone needs a pickaxe, so bare hands run at the slow rate: 150
	// ticks, 7.5 seconds.
	for i := 0; i < 149; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != world.Stone {
		t.Fatal("stone broke early without a pickaxe")
	}
	s.digTickStep()
	if got := s.world.GetBlock(pos); got != world.Air {
		t.Fatalf("stone did not break: %d", got)
	}
	s.entities.mu.RLock()
	n := len(s.entities.byID)
	s.entities.mu.RUnlock()
	if n != 0 {
		t.Fatalf("stone dropped %d entities without a pickaxe", n)
	}
}

func TestSurvivalBedrockUnbreakable(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Miner")
	pos := world.Pos{X: 4, Y: -64, Z: 4}
	blk := s.world.GetBlock(pos)
	if name, _ := world.StateOwner(blk); name != "bedrock" {
		t.Fatalf("expected bedrock at floor, got %q", name)
	}
	s.handleDigSurvival(sess, pos, digStart)
	s.handleDigSurvival(sess, pos, digFinish)
	for i := 0; i < 400; i++ {
		s.digTickStep()
	}
	if got := s.world.GetBlock(pos); got != blk {
		t.Fatal("bedrock broke")
	}
}

func TestItemPickup(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Collector")
	e := s.spawnItemDrop(dimOverworld, world.Pos{X: 0, Y: 0, Z: 0}, ItemStack{ID: world.ItemIDs["dirt"], Count: 3})
	if e == nil {
		t.Fatal("no item entity")
	}
	e.PickupDelay = 0
	s.itemTickStep()
	if s.entities.Count() != 0 {
		t.Fatalf("entity not removed after pickup: %d", s.entities.Count())
	}
	if got := sess.stackAt(invSlotForHotbar(0)); got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 3}) {
		t.Fatalf("inventory = %+v", got)
	}
}

func TestSurvivalJoinMode(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, _ := joinPlay(t, s, "Survivor")
	defer c.Close()
	join, err := waitPacket(t, pr, IDPlayJoinGame)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(join.Data)
	if _, err := r.Int(); err != nil { // entity id
		t.Fatal(err)
	}
	if _, err := r.Bool(); err != nil { // hardcore
		t.Fatal(err)
	}
	n, _ := r.VarInt()
	for i := int32(0); i < n; i++ {
		if _, err := r.String(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ { // players, view, sim
		if _, err := r.VarInt(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ { // reduced debug, respawn, limited crafting
		if _, err := r.Bool(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.VarInt(); err != nil { // dimension type
		t.Fatal(err)
	}
	if _, err := r.String(); err != nil { // dimension name
		t.Fatal(err)
	}
	if _, err := r.Long(); err != nil { // seed
		t.Fatal(err)
	}
	mode, err := r.Byte()
	if err != nil || mode != GameModeSurvival {
		t.Fatalf("game mode = %d, err %v", mode, err)
	}
}

func TestSurvivalPlaceConsumesItem(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Builder")
	s.world.SetBlock(world.Pos{X: 0, Y: 0, Z: 0}, world.Stone)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 2})
	sess.held.Store(0)

	seq := int32(9)
	// Click the stone's top face: target (0,1,0).
	s.handleUseItem(sess, useItemPkt(0, 0, 0, 0, 1, &seq))

	if got := s.world.GetBlock(world.Pos{X: 0, Y: 1, Z: 0}); got != world.Dirt {
		t.Fatalf("placed block = %d, want dirt", got)
	}
	if got := sess.stackAt(invSlotForHotbar(0)); got.Count != 1 || got.ID != world.ItemIDs["dirt"] {
		t.Fatalf("held stack = %+v, want 1 dirt", got)
	}
}

func TestCreativePlaceKeepsItem(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Builder", 0.5, 0.5, 0.5)
	s.world.SetBlock(world.Pos{X: 0, Y: 0, Z: 0}, world.Stone)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 2})
	sess.held.Store(0)

	seq := int32(3)
	s.handleUseItem(sess, useItemPkt(0, 0, 0, 0, 1, &seq))
	if got := sess.stackAt(invSlotForHotbar(0)).Count; got != 2 {
		t.Fatalf("creative consumed item: count %d", got)
	}
}

func TestSummonAndKillMob(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Hunter")
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 0.5, 0, 0.5, false)
	if !ok {
		t.Fatal("summon failed")
	}
	if e.Health != 10 || !e.Living {
		t.Fatalf("pig = %+v", e)
	}
	for i := 0; i < 10; i++ {
		e.invulnTicks = 0 // bypass the post-hit grace window
		s.damageMob(e, 1)
	}
	// die() leaves a twenty-tick corpse; tick the poof through.
	for i := 0; i < 20; i++ {
		s.mobTickStep()
	}
	if _, ok := s.entities.Get(e.ID); ok {
		t.Fatal("pig survived ten hits")
	}
	_ = sess
}

func TestPigDropsPorkchop(t *testing.T) {
	s := New("unused")
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 4.5, 4, 4.5, false)
	if !ok {
		t.Fatal("summon failed")
	}
	for i := 0; i < 10; i++ {
		e.invulnTicks = 0 // bypass the post-hit grace window
		s.damageMob(e, 1)
	}
	s.entities.mu.RLock()
	var drop *Entity
	for _, ent := range s.entities.byID {
		if ent.Item.ID == world.ItemIDs["porkchop"] {
			drop = ent
		}
	}
	s.entities.mu.RUnlock()
	if drop == nil {
		t.Fatal("pig dropped no porkchop")
	}
	if drop.Item.Count < 1 || drop.Item.Count > 3 {
		t.Fatalf("porkchop count = %d, want 1..3", drop.Item.Count)
	}
}

func TestSummonRejectsUnknown(t *testing.T) {
	s := New("unused")
	if _, ok := s.summonMob(s.dims[dimOverworld], "ender_dragon_egg", 0, 0, 0, false); ok {
		t.Fatal("summoned an unsupported entity")
	}
}

func TestMobGravity(t *testing.T) {
	s := New("unused")
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 0, 100, 0, false)
	if !ok {
		t.Fatal("summon failed")
	}
	before := e.Y
	s.mobTickStep()
	if e.Y >= before {
		t.Fatalf("mob did not fall: %v -> %v", before, e.Y)
	}
}

func TestSummonCommand(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Summoner", 0.5, 0.5, 0.5)
	if out := s.runSummon("Summoner", nil); out.text != "Usage: /summon <entity>" {
		t.Fatalf("usage = %q", out.text)
	}
	if out := s.runSummon("Summoner", []string{"nope"}); out.text != "Unknown entity: nope" {
		t.Fatalf("unknown = %q", out.text)
	}
	if out := s.runSummon("Summoner", []string{"pig"}); out.comp.translate != "commands.summon.success" {
		t.Fatalf("summon = %q", out.comp.translate)
	}
	if s.entities.Count() != 1 {
		t.Fatalf("entities = %d, want 1", s.entities.Count())
	}
	_ = sess
}

func TestDeathDropsInventory(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Mortal")
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 5})
	sess.setStack(slotMainStart, ItemStack{ID: world.ItemIDs["stone"], Count: 12})

	s.killPlayer(sess, literal("test"))
	if !sess.dead {
		t.Fatal("not dead")
	}
	if !sess.stackAt(invSlotForHotbar(0)).Empty() || !sess.stackAt(slotMainStart).Empty() {
		t.Fatal("inventory survived death")
	}
	found := map[int32]int32{}
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Item.ID != 0 {
			found[e.Item.ID] += e.Item.Count
		}
	}
	s.entities.mu.RUnlock()
	if found[world.ItemIDs["dirt"]] != 5 || found[world.ItemIDs["stone"]] != 12 {
		t.Fatalf("drops = %v", found)
	}
}

func TestXPOrbPickup(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	sess := survivalSession(t, s, "Learner")
	e := s.spawnXPOrb(dimOverworld, 0.5, 0.5, 0.5, 5)
	if e == nil {
		t.Fatal("no orb")
	}
	s.itemTickStep()
	if s.entities.Count() != 0 {
		t.Fatalf("orb not collected: %d entities", s.entities.Count())
	}
	if sess.xpTotal != 5 {
		t.Fatalf("xp total = %d, want 5", sess.xpTotal)
	}
	if sess.xpLevel != 0 {
		t.Fatalf("xp level = %d, want 0", sess.xpLevel)
	}
}

func TestExperienceLevelThresholds(t *testing.T) {
	cases := []struct {
		total  int32
		level  int32
		prog   float32
		approx bool
	}{
		{0, 0, 0, false},
		{7, 1, 0, false},
		{8, 1, 1.0 / 9.0, true},
		{16, 2, 0, false},
		{315, 15, 0, false},
		{352, 16, 0, false},
	}
	for _, c := range cases {
		level, prog := experienceLevel(c.total)
		if level != c.level {
			t.Fatalf("xp %d level = %d, want %d", c.total, level, c.level)
		}
		if c.approx {
			continue
		}
		if prog != c.prog {
			t.Fatalf("xp %d progress = %v, want %v", c.total, prog, c.prog)
		}
	}
}

func TestMobDeathSpawnsXPOrb(t *testing.T) {
	s := New("unused")
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 2.5, 2, 2.5, false)
	if !ok {
		t.Fatal("summon failed")
	}
	for i := 0; i < 10; i++ {
		e.invulnTicks = 0 // bypass the post-hit grace window
		s.damageMob(e, 1)
	}
	orbType := mustEntityType("experience_orb")
	found := false
	s.entities.mu.RLock()
	for _, ent := range s.entities.byID {
		if ent.Type == orbType && ent.Data >= 1 && ent.Data <= 3 {
			found = true
		}
	}
	s.entities.mu.RUnlock()
	if !found {
		t.Fatal("no XP orb dropped")
	}
}

func TestMobWanderMovesOnGround(t *testing.T) {
	s := New("unused")
	// Flat grass at y=-61; spawn the pig on top.
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 4.5, -60, 4.5, false)
	if !ok {
		t.Fatal("summon failed")
	}
	startX, startZ := e.X, e.Z
	for i := 0; i < 60; i++ {
		s.mobTickStep()
	}
	if e.X == startX && e.Z == startZ {
		t.Fatal("mob never wandered")
	}
	if e.Y != -60 {
		t.Fatalf("mob left the ground: y=%v", e.Y)
	}
	// It must stay over solid ground.
	below := world.Pos{X: int(e.X), Y: int(e.Y) - 1, Z: int(e.Z)}
	if s.world.GetBlock(below) == world.Air {
		t.Fatalf("mob walked off a ledge at %v,%v", e.X, e.Z)
	}
}

func TestMobFallsBeforeWalking(t *testing.T) {
	s := New("unused")
	e, ok := s.summonMob(s.dims[dimOverworld], "pig", 4.5, 100, 4.5, false)
	if !ok {
		t.Fatal("summon failed")
	}
	beforeX := e.X
	for i := 0; i < 10; i++ {
		s.mobTickStep()
	}
	if e.Y >= 100 {
		t.Fatalf("mob did not fall: y=%v", e.Y)
	}
	if e.X != beforeX {
		t.Fatal("mob walked while falling")
	}
}

func TestHungerExhaustionDrainsFood(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Runner")
	sess.food = 20
	sess.saturation = 0
	sess.exhaustion = 3.9
	// Exhaustion only accumulates; the food tick spends it.
	sess.addExhaustion(0.2)
	s.hungerTickStep()
	if sess.food != 19 {
		t.Fatalf("food = %d, want 19", sess.food)
	}
	// Saturation absorbs exhaustion before food does.
	sess.saturation = 5
	sess.exhaustion = 3.9
	sess.addExhaustion(0.2)
	s.hungerTickStep()
	if sess.saturation != 4 || sess.food != 19 {
		t.Fatalf("saturation = %v food = %d, want 4/19", sess.saturation, sess.food)
	}
}

func TestHungerRegeneration(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, _ := joinPlay(t, s, "Healer")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Healer")
	sess.health = 10
	sess.food = 20
	sess.saturation = 5
	// Vanilla's fed regen heals min(saturation,6)/6 every ten ticks;
	// eighty ticks at saturation five lands eight heals.
	for i := 0; i < 80; i++ {
		s.playerTick()
	}
	if sess.health <= 10 || sess.health >= 20 {
		t.Fatalf("health = %v, want a partial heal", sess.health)
	}
	// The regeneration syncs a Set Health per heal; the last one reports
	// the final health.
	var last float32
	deadline := time.Now().Add(2 * time.Second)
	for {
		p, err := waitPacket(t, pr, IDPlaySetHealth)
		if err != nil {
			break
		}
		if h, _ := protocol.NewPacketReader(p.Data).Float(); h > 0 {
			last = h
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if last != sess.health {
		t.Fatalf("last set health = %v, want %v", last, sess.health)
	}
}

func TestStarvationDamages(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Starver")
	sess.food = 0
	sess.health = 5
	for i := 0; i < 80; i++ {
		s.hungerTickStep()
	}
	if sess.health != 4 {
		t.Fatalf("health = %v, want 4", sess.health)
	}
	// Never starves below 1.
	sess.health = 1
	for i := 0; i < 160; i++ {
		s.hungerTickStep()
	}
	if sess.health != 1 {
		t.Fatalf("health = %v, want 1 (floor)", sess.health)
	}
}

func TestSprintDrainsHungerOverMovement(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, pw := joinPlay(t, s, "Sprinter")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Sprinter")
	sess.sprinting = true
	sess.saturation = 0
	sess.food = 20
	sess.exhaustion = 3.5

	// Sprint far enough to drain one food point (0.1 per block); the
	// spend happens on the food tick, not inside the move handler.
	for i := 0; i < 6; i++ {
		sendPosRot(t, pw, float64(10+i), -60, 0.5, 0, 0, true)
	}
	deadline := time.Now().Add(2 * time.Second)
	for sess.exhaustion < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.playerTick()
	if sess.food != 19 {
		t.Fatalf("food = %d, want 19 after sprinting", sess.food)
	}
}

func sendUseItem(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(0) // hand
	b.VarInt(1) // sequence
	b.Float(0)
	b.Float(0)
	if err := pw.WritePacket(IDPlayUseItemSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestEatingRestoresHunger(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, pw := joinPlay(t, s, "Eater")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Eater")
	sess.health = 20
	sess.food = 10
	sess.saturation = 0
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["bread"], Count: 1})

	sendUseItem(t, pw)
	deadline := time.Now().Add(time.Second)
	for sess.eatingTicks == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if sess.eatingTicks == 0 {
		t.Fatal("eating did not start")
	}
	for i := 0; i < 32; i++ {
		s.eatTickStep()
	}
	if sess.food != 15 {
		t.Fatalf("food = %d, want 15", sess.food)
	}
	if sess.saturation != 6 {
		t.Fatalf("saturation = %v, want 6", sess.saturation)
	}
	if !sess.stackAt(invSlotForHotbar(0)).Empty() {
		t.Fatal("bread not consumed")
	}
	// The health sync lands on the player tick after the flag is set.
	s.playerTick()
	if _, err := waitPacket(t, pr, IDPlaySetHealth); err != nil {
		t.Fatalf("health sync: %v", err)
	}
}

func TestGoldenAppleAlwaysEdible(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	c, pr, pw := joinPlay(t, s, "Eater")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Eater")
	sess.food = 20
	sess.saturation = 5
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["golden_apple"], Count: 1})

	sendUseItem(t, pw)
	deadline := time.Now().Add(time.Second)
	for sess.eatingTicks == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if sess.eatingTicks == 0 {
		t.Fatal("golden apple not edible at full hunger")
	}
}
