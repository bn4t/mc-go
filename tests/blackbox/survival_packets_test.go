package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// drainToQuietBlackbox reads until the connection is quiet for gap.
func drainToQuietBlackbox(t *testing.T, c interface{ SetReadDeadline(time.Time) error }, pr *protocol.Reader, gap time.Duration) {
	t.Helper()
	for {
		_ = c.SetReadDeadline(time.Now().Add(gap))
		if _, err := pr.ReadPacket(); err != nil {
			_ = c.SetReadDeadline(time.Time{})
			return
		}
	}
}

// syncClickState returns a window's live container state: it sends a
// stale click, which both vanilla and mc-go ignore, answering with a
// full resync that carries the current state id.
func syncClickState(t *testing.T, pr *protocol.Reader, pw *protocol.Writer, win int32) int32 {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(win)
	b.VarInt(-1) // stale by construction
	b.Short(0)
	b.Byte(0)
	b.VarInt(0)
	b.VarInt(0)
	b.VarInt(0)
	if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting click resync: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, err1 := r.VarInt()
		st, err2 := r.VarInt()
		if err1 != nil || err2 != nil || wid != win {
			continue
		}
		return st
	}
}

// confirmSyncsBlackbox reads until the connection is quiet, confirming
// every Synchronize Player Position on the way. Menu uses are denied
// until the client confirms its teleports (vanilla 26.2 answers
// build.tooHigh), so tests that open menus run this after joining and
// after every teleport.
func confirmSyncsBlackbox(t *testing.T, c interface {
	SetDeadline(time.Time) error
}, pr *protocol.Reader, pw *protocol.Writer) {
	t.Helper()
	for {
		_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			_ = c.SetDeadline(time.Time{})
			return
		}
		if p.ID == server.IDPlaySyncPos {
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// sendMovePosRotPitch writes a full position+rotation update.
func sendMovePosRotPitch(pw *protocol.Writer, x, y, z float64, yaw, pitch float32) error {
	var b protocol.Buffer
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(yaw)
	b.Float(pitch)
	b.Byte(0x01) // on ground
	return pw.WritePacket(server.IDPlayMovePosRotSB, b.Bytes())
}

// sendDig writes one Player Action digging packet.
func sendDig(t *testing.T, pw *protocol.Writer, status int32, x, y, z int32, seq int32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(status)
	b.Position(x, y, z)
	b.VarInt(1) // face: top
	b.VarInt(seq)
	if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// TestSurvivalBreakDropPickupPackets proves the survival break -> item
// entity -> pickup flow at the packet level on a standalone server:
// exact IDs, exact field encodings, no timing assertions. Waits are
// event-driven (poll until the expected packet) with a generous cap.
func TestSurvivalBreakDropPickupPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Miner")
	defer c.Close()
	drainToJoinBlackbox(t, "miner", pr)

	var tp protocol.Buffer
	tp.VarInt(1)
	if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, tp.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	if err := sendMovePosRotPitch(pw, 8.5, -60, 0.5, 0, 90); err != nil {
		t.Fatal(err)
	}

	// Start the dig, let one server tick pass, then finish. The wait
	// is for the server's dig-state tick, not a behavior assertion.
	sendDig(t, pw, 0, 8, -61, 0, 1)
	time.Sleep(120 * time.Millisecond)
	sendDig(t, pw, 2, 8, -61, 0, 2)

	deadline := time.Now().Add(10 * time.Second)
	_ = c.SetReadDeadline(deadline)

	// Steps 1-3 collect the break's three packets in whatever order
	// they land: popResource's pairing bundle (bundle_delimiter,
	// add_entity, set_entity_data, bundle_delimiter) writes the
	// moment the drop spawns, while the block_update queues on the
	// chunk's dirty set and flushes at the end-of-tick broadcast —
	// the same split vanilla makes between sendPairingData and
	// ChunkHolder.broadcastChanges.
	var itemEID int32
	var metaEID int32 = -1
	sawBlock, sawSpawn, sawMeta := false, false, false
	for !(sawBlock && sawSpawn && sawMeta) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting break packets (block=%v spawn=%v meta=%v): %v", sawBlock, sawSpawn, sawMeta, err)
		}
		switch p.ID {
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err != nil {
				t.Fatalf("block update position: %v", err)
			}
			state, err := r.VarInt()
			if err != nil {
				t.Fatalf("block update state: %v", err)
			}
			if x != 8 || y != -61 || z != 0 {
				continue
			}
			if state != int32(world.Air) {
				t.Fatalf("block update state = %d, want air", state)
			}
			sawBlock = true
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				t.Fatalf("spawn entity id: %v", err)
			}
			if _, err := r.UUID(); err != nil {
				t.Fatalf("spawn uuid: %v", err)
			}
			typ, err := r.VarInt()
			if err != nil {
				t.Fatalf("spawn type: %v", err)
			}
			wantType, _ := server.EntityTypeID("item")
			if typ != wantType {
				continue
			}
			itemEID = eid
			sawSpawn = true
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				t.Fatalf("metadata eid: %v", err)
			}
			index, err := r.Byte()
			if err != nil || index != 8 {
				continue
			}
			typ, err := r.Byte()
			if err != nil || typ != 7 {
				continue
			}
			count, err := r.VarInt()
			if err != nil || count != 1 {
				t.Fatalf("item count = %d, err %v", count, err)
			}
			id, err := r.VarInt()
			if err != nil || id != world.ItemIDs["dirt"] {
				t.Fatalf("item id = %d, err %v (want dirt %d)", id, err, world.ItemIDs["dirt"])
			}
			if a, err := r.VarInt(); err != nil || a != 0 {
				t.Fatalf("components to add = %d, err %v", a, err)
			}
			if rm, err := r.VarInt(); err != nil || rm != 0 {
				t.Fatalf("components to remove = %d, err %v", rm, err)
			}
			if term, err := r.Byte(); err != nil || term != 0xFF {
				t.Fatalf("metadata terminator = %#x, err %v", term, err)
			}
			metaEID = eid
			sawMeta = true
		}
	}
	if metaEID != itemEID {
		t.Fatalf("item metadata eid = %d, want %d", metaEID, itemEID)
	}

	// 4. After the pickup delay, Take Item Entity then Remove Entities
	// then the inventory slot sync.
	sawTake := false
	sawRemove := false
	sawSlot := false
	for !(sawTake && sawRemove && sawSlot) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting pickup (take=%v remove=%v slot=%v): %v", sawTake, sawRemove, sawSlot, err)
		}
		switch p.ID {
		case server.IDPlayTakeItem:
			r := protocol.NewPacketReader(p.Data)
			ie, err1 := r.VarInt()
			ce, err2 := r.VarInt()
			am, err3 := r.VarInt()
			if err1 != nil || err2 != nil || err3 != nil {
				t.Fatalf("take item fields: %v/%v/%v", err1, err2, err3)
			}
			if ie != itemEID {
				t.Fatalf("take item entity = %d, want %d", ie, itemEID)
			}
			if ce == 0 {
				t.Fatal("take item collector = 0")
			}
			if am != 1 {
				t.Fatalf("take item amount = %d, want 1", am)
			}
			sawTake = true
		case server.IDPlayRemoveEntity:
			r := protocol.NewPacketReader(p.Data)
			n, err := r.VarInt()
			if err != nil || n != 1 {
				t.Fatalf("remove count = %d, err %v", n, err)
			}
			id, err := r.VarInt()
			if err != nil || id != itemEID {
				t.Fatalf("remove id = %d, err %v (want %d)", id, err, itemEID)
			}
			sawRemove = true
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			win, _ := r.VarInt()
			state, _ := r.VarInt()
			slot, err := r.Short()
			if err != nil {
				t.Fatalf("container slot index: %v", err)
			}
			count, _ := r.VarInt()
			id, _ := r.VarInt()
			if win != 0 {
				t.Fatalf("container window = %d, want 0", win)
			}
			_ = state
			if slot < 36 || slot > 44 {
				t.Fatalf("picked-up slot = %d, want hotbar 36-44", slot)
			}
			if count != 1 || id != world.ItemIDs["dirt"] {
				t.Fatalf("picked-up stack = %d x%d, want dirt x1", id, count)
			}
			sawSlot = true
		}
	}
}

// TestSurvivalDeathRespawnPackets proves the death and respawn packet
// flow over the wire: /kill produces Set Health 0 and Combat Death,
// and Client Command perform-respawn produces Respawn, Set Health, and
// a fresh position sync.
func TestSurvivalDeathRespawnPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Mortal")
	defer c.Close()
	drainToJoinBlackbox(t, "mortal", pr)
	drainToQuietBlackbox(t, c, pr, 500*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "kill")

	// Set Health 0 and Combat Death arrive after the command reply.
	var sawDeathHealth, sawCombat bool
	for !(sawDeathHealth && sawCombat) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting death packets (health=%v combat=%v): %v", sawDeathHealth, sawCombat, err)
		}
		switch p.ID {
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			h, err := r.Float()
			if err != nil {
				t.Fatalf("health: %v", err)
			}
			if h == 0 {
				sawDeathHealth = true
			}
		case server.IDPlayCombatDeath:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("combat death eid: %v", err)
			}
			rest, err := r.Read(r.Remaining())
			if err != nil {
				t.Fatalf("combat death message: %v", err)
			}
			if !bytes.Contains(rest, []byte("death.attack.genericKill")) {
				t.Fatalf("combat death message lacks kill text: %x", rest)
			}
			sawCombat = true
		}
	}

	// Perform respawn.
	var cc protocol.Buffer
	cc.VarInt(0)
	if err := pw.WritePacket(server.IDPlayClientStatusSB, cc.Bytes()); err != nil {
		t.Fatal(err)
	}
	sawRespawn, sawHealth20, sawPos := false, false, false
	for !(sawRespawn && sawHealth20 && sawPos) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting respawn packets: %v", err)
		}
		switch p.ID {
		case server.IDPlayRespawn:
			r := protocol.NewPacketReader(p.Data)
			if dimType, err := r.VarInt(); err != nil || dimType != 0 {
				t.Fatalf("respawn dim type = %d, err %v", dimType, err)
			}
			if dim, err := r.String(); err != nil || dim != "minecraft:overworld" {
				t.Fatalf("respawn dim = %q, err %v", dim, err)
			}
			sawRespawn = true
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			if h, err := r.Float(); err == nil && h == 20 {
				sawHealth20 = true
			}
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if tid, err := r.VarInt(); err == nil && tid > 0 {
				sawPos = true
			}
		}
	}
}

// TestSurvivalAttackPackets proves player melee at the packet level:
// Bob attacks Alice (entity id learned from her spawn in his join
// burst); Alice receives Set Health 19 and Hurt Animation, and Bob
// receives the hurt Entity Event.
func TestSurvivalAttackPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	ca, pra, _ := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)

	cb, prb, pwb := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	// Bob's join burst carries Alice's Spawn Entity: capture her id.
	var aliceEID int32
	wantUUID := offlineUUIDBlackbox("Alice")
	_ = cb.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("waiting alice spawn: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, err := r.VarInt()
		if err != nil {
			t.Fatalf("spawn eid: %v", err)
		}
		u, err := r.UUID()
		if err != nil {
			t.Fatalf("spawn uuid: %v", err)
		}
		if u != wantUUID {
			continue
		}
		aliceEID = eid
		break
	}

	// Bob attacks Alice (both stand at spawn, within reach).
	var atk protocol.Buffer
	atk.VarInt(aliceEID)
	if err := pwb.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}

	// The attacker is not sent the hurt entity event — measured on
	// vanilla, the attacker's client gets nothing at all. Nothing
	// arriving inside the window is the pass.
	_ = cb.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
	for {
		p, err := prb.ReadPacket()
		if err != nil {
			break
		}
		if p.ID == server.IDPlayEntityEvent {
			t.Fatal("attacker received a hurt entity event vanilla never sends")
		}
	}
	_ = cb.SetReadDeadline(time.Time{})

	// Alice sees Set Health 19 and Hurt Animation for herself.
	sawHealth, sawHurt := false, false
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	for !(sawHealth && sawHurt) {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice waiting (health=%v hurt=%v): %v", sawHealth, sawHurt, err)
		}
		switch p.ID {
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			if h, err := r.Float(); err == nil && h == 19 {
				sawHealth = true
			}
		case server.IDPlayHurtAnimation:
			r := protocol.NewPacketReader(p.Data)
			if eid, err := r.VarInt(); err == nil && eid == aliceEID {
				sawHurt = true
			}
		}
	}
}

// TestSurvivalPlaceConsumesPackets proves survival placement at the
// packet level: /give stocks the hotbar, Use Item On places the block,
// and the consumed stack is synced back to the client.
func TestSurvivalPlaceConsumesPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Builder")
	defer c.Close()
	drainToJoinBlackbox(t, "builder", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give dirt 2")
	// The hotbar slot sync carries the granted dirt.
	foundGive := false
	for !foundGive {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting give: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot && p.ID != server.IDPlayContainerContent {
			continue
		}
		if p.ID == server.IDPlayContainerContent {
			foundGive = true
			break
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt() // window
		_, _ = r.VarInt() // state
		slot, err := r.Short()
		if err != nil || slot != 36 {
			continue
		}
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if count == 2 && id == world.ItemIDs["dirt"] {
			foundGive = true
		}
	}

	// Aim at the grass under the player and place against its top.
	var tp protocol.Buffer
	tp.VarInt(1)
	_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tp.Bytes())
	_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
	if err := sendMovePosRotPitch(pw, 8.5, -60, 0.5, 0, 90); err != nil {
		t.Fatal(err)
	}
	var use protocol.Buffer
	use.VarInt(0)           // hand
	use.Position(8, -61, 0) // clicked grass
	use.VarInt(1)           // face: top
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false) // inside block
	use.Bool(false) // world border
	use.VarInt(7)   // sequence
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}

	sawBlock, sawConsume := false, false
	for !(sawBlock && sawConsume) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting place (block=%v consume=%v): %v", sawBlock, sawConsume, err)
		}
		switch p.ID {
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err != nil {
				t.Fatalf("block update: %v", err)
			}
			st, _ := r.VarInt()
			if x == 8 && y == -60 && z == 0 && st == int32(world.Dirt) {
				sawBlock = true
			}
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.VarInt()
			_, _ = r.VarInt()
			slot, err := r.Short()
			if err != nil || slot != 36 {
				continue
			}
			count, _ := r.VarInt()
			id, _ := r.VarInt()
			if count == 1 && id == world.ItemIDs["dirt"] {
				sawConsume = true
			}
		}
	}
}

// TestMobSummonAttackPackets proves the mob lifecycle at the packet
// level: /summon emits Spawn Entity for the pig type plus health
// metadata, each landed attack emits a Damage Event and updated health
// metadata, and the tenth hit removes the entity.
func TestMobSummonAttackPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Hunter")
	defer c.Close()
	drainToJoinBlackbox(t, "hunter", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "summon minecraft:pig ~ ~ ~ {NoAI:1b}")

	// Spawn Entity: pig type; capture the id.
	pigType, _ := server.EntityTypeID("pig")
	var pigEID int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting summon: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, err := r.VarInt()
		if err != nil {
			t.Fatalf("spawn eid: %v", err)
		}
		if _, err := r.UUID(); err != nil {
			t.Fatalf("spawn uuid: %v", err)
		}
		typ, err := r.VarInt()
		if err != nil {
			t.Fatalf("spawn type: %v", err)
		}
		if typ != pigType {
			t.Fatalf("spawn type = %d, want pig %d", typ, pigType)
		}
		pigEID = eid
		break
	}

	// Health metadata: index 9, float, 10.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting health metadata: %v", err)
		}
		if p.ID != server.IDPlayEntityMetadata {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		if eid != pigEID {
			continue
		}
		index, _ := r.Byte()
		typ, _ := r.VarInt()
		health, err := r.Float()
		if index != 9 || typ != 3 || err != nil || health != 10 {
			t.Fatalf("spawn metadata = index %d type %d health %v err %v", index, typ, health, err)
		}
		break
	}

	// Past the tick-1 dirty-resend, then hit it ten times spaced past
	// the 10-tick post-hit invulnerability window so every hit lands.
	drainToQuietBlackbox(t, c, pr, 500*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	go func() {
		for hit := 1; hit <= 10; hit++ {
			var atk protocol.Buffer
			atk.VarInt(pigEID)
			if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
				return
			}
			time.Sleep(650 * time.Millisecond)
		}
	}()
	// One collection loop: the pig's damage events, falling health
	// metadata, its removal (after the twenty-tick death animation),
	// and the porkchop drop's spawn + metadata all interleave —
	// vacuumed drops remove other entity ids along the way.
	itemType, _ := server.EntityTypeID("item")
	sawHurt, sawRemove, sawDropMeta := false, false, false
	var lastHealth float32 = 10
	var dropEID int32
	for !sawHurt || !sawRemove || !sawDropMeta {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting combat (hurt=%v remove=%v dropmeta=%v): %v", sawHurt, sawRemove, sawDropMeta, err)
		}
		switch p.ID {
		case server.IDPlayDamageEvent:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err == nil && eid == pigEID {
				sawHurt = true
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			if _, err := r.UUID(); err != nil {
				continue
			}
			if typ, err := r.VarInt(); err == nil && typ == itemType {
				dropEID = eid
			}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			index, _ := r.Byte()
			typ, _ := r.VarInt()
			switch eid {
			case pigEID:
				h, err := r.Float()
				if err != nil {
					t.Fatalf("health metadata: %v", err)
				}
				if h >= lastHealth {
					t.Fatalf("health did not drop: %v -> %v", lastHealth, h)
				}
				lastHealth = h
			case dropEID:
				if dropEID == 0 || index != 8 || typ != 7 {
					t.Fatalf("loot metadata = index %d type %d", index, typ)
				}
				count, err := r.VarInt()
				if err != nil || count < 1 || count > 3 {
					t.Fatalf("porkchop count = %d, err %v", count, err)
				}
				id, err := r.VarInt()
				if err != nil || id != world.ItemIDs["porkchop"] {
					t.Fatalf("loot item = %d, err %v (want porkchop %d)", id, err, world.ItemIDs["porkchop"])
				}
				sawDropMeta = true
			}
		case server.IDPlayRemoveEntity:
			r := protocol.NewPacketReader(p.Data)
			n, err := r.VarInt()
			if err != nil || n != 1 {
				t.Fatalf("remove count = %d, err %v", n, err)
			}
			id, err := r.VarInt()
			if err != nil {
				t.Fatalf("remove id: %v", err)
			}
			if id == pigEID {
				sawRemove = true
			}
		}
	}
	if lastHealth > 1 {
		t.Fatalf("final health = %v, want <= 1", lastHealth)
	}
}

// TestDeathDropsInventoryPackets proves death drops at the packet
// level: /give stocks dirt, /kill emits the death flow plus an item
// entity carrying the whole stack, and the respawned inventory is
// empty.
func TestDeathDropsInventoryPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Mortal")
	defer c.Close()
	drainToJoinBlackbox(t, "mortal", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give dirt 5")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "kill")

	itemType, _ := server.EntityTypeID("item")
	sawDeath, sawDrop, sawMetadata := false, false, false
	for !(sawDeath && sawDrop && sawMetadata) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting death (death=%v drop=%v meta=%v): %v", sawDeath, sawDrop, sawMetadata, err)
		}
		switch p.ID {
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			if h, err := r.Float(); err == nil && h == 0 {
				sawDeath = true
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("drop eid: %v", err)
			}
			if _, err := r.UUID(); err != nil {
				t.Fatalf("drop uuid: %v", err)
			}
			if typ, err := r.VarInt(); err == nil && typ == itemType {
				sawDrop = true
			}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err != nil {
				t.Fatal(err)
			}
			index, _ := r.Byte()
			typ, _ := r.VarInt()
			if index != 8 || typ != 7 {
				continue
			}
			count, _ := r.VarInt()
			id, _ := r.VarInt()
			if id != world.ItemIDs["dirt"] || count != 5 {
				t.Fatalf("drop = %d x%d, want dirt x5", id, count)
			}
			sawMetadata = true
		}
	}
}

// TestMobXPOrbPackets proves mob experience at the packet level:
// killing a pig spawns an experience orb (type 49) with value metadata
// (index 8, VarInt 1..3), and collecting it emits Set Experience with
// the matching total and the entity removed.
func TestMobXPOrbPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Learner")
	defer c.Close()
	drainToJoinBlackbox(t, "learner", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))

	// A netherite axe one-shots the pig; burst bare-hand hits would be
	// swallowed by the 10-tick post-hit invulnerability window.
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "item replace entity @s weapon.mainhand with netherite_axe")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "summon minecraft:pig ~ ~ ~ {NoAI:1b}")

	orbType, _ := server.EntityTypeID("experience_orb")
	// Wait for the pig spawn, learn its id, then kill it.
	var pigEID int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting pig: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		if _, err := r.UUID(); err != nil {
			t.Fatal(err)
		}
		typ, _ := r.VarInt()
		if typ == orbType {
			continue
		}
		pigEID = eid
		break
	}
	go func() {
		for i := 0; i < 3; i++ {
			var atk protocol.Buffer
			atk.VarInt(pigEID)
			if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
				return
			}
			time.Sleep(650 * time.Millisecond)
		}
	}()

	// The orb spawns after the pig's removal; attacks spaced past the
	// 10-tick post-hit invulnerability window so every hit lands.
	var orbEID, orbValue int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting orb: %v", err)
		}
		if p.ID == server.IDPlaySpawnEntity {
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			if _, err := r.UUID(); err != nil {
				t.Fatal(err)
			}
			typ, _ := r.VarInt()
			if typ == orbType {
				orbEID = eid
			}
			continue
		}
		if p.ID != server.IDPlayEntityMetadata || orbEID == 0 {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		if eid != orbEID {
			continue
		}
		index, _ := r.Byte()
		typ, _ := r.VarInt()
		if index != 8 || typ != 1 {
			t.Fatalf("orb metadata = index %d type %d", index, typ)
		}
		val, err := r.VarInt()
		if err != nil || val < 1 || val > 3 {
			t.Fatalf("orb value = %d, err %v", val, err)
		}
		orbValue = val
		break
	}

	// Collecting the orb emits Set Experience and removes it.
	sawXP, sawRemove := false, false
	for !(sawXP && sawRemove) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting pickup (xp=%v remove=%v): %v", sawXP, sawRemove, err)
		}
		switch p.ID {
		case server.IDPlaySetExperience:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.Float(); err != nil { // bar
				t.Fatal(err)
			}
			if _, err := r.VarInt(); err != nil { // level
				t.Fatal(err)
			}
			total, err := r.VarInt()
			if err != nil || total != orbValue {
				t.Fatalf("xp total = %d, err %v (want %d)", total, err, orbValue)
			}
			sawXP = true
		case server.IDPlayRemoveEntity:
			r := protocol.NewPacketReader(p.Data)
			n, _ := r.VarInt()
			if n != 1 {
				continue
			}
			id, _ := r.VarInt()
			if id == orbEID {
				sawRemove = true
			}
		}
	}
}

// TestMobWanderPackets proves the wander AI reaches the wire: after
// /summon, the pig emits Entity Position Sync packets with its new
// position and yaw. The test waits for the event, not a duration.
func TestMobWanderPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Observer")
	defer c.Close()
	drainToJoinBlackbox(t, "observer", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "summon pig")

	var pigEID int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting pig: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		if _, err := r.UUID(); err != nil {
			t.Fatal(err)
		}
		if _, err := r.VarInt(); err != nil { // type
			t.Fatal(err)
		}
		pigEID = eid
		break
	}

	// Wait for a movement update for the pig: vanilla syncs live mobs as
	// move_entity_pos/pos_rot deltas every few ticks, not teleports.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting wander: %v", err)
		}
		if p.ID != server.IDPlayEntityUpdatePos && p.ID != server.IDPlayEntityUpdateRot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, err := r.VarInt()
		if err != nil {
			t.Fatalf("sync eid: %v", err)
		}
		if eid != pigEID {
			continue
		}
		dx, _ := r.Short()
		dy, _ := r.Short()
		dz, _ := r.Short()
		// A wander step is a fraction of a block; the fixed-point delta
		// stays well inside a block in each axis.
		if math.Abs(float64(dx)/4096) > 1 || math.Abs(float64(dz)/4096) > 1 {
			t.Fatalf("pig strayed: dxyz=%d,%d,%d", dx, dy, dz)
		}
		if dy != 0 {
			t.Fatalf("pig left the ground: dy=%d", dy)
		}
		return
	}
}

// TestMobMovePacketFamilyParity watches a zombie chase a survival
// player on both servers: every position update must ride the
// fixed-point delta family (move_entity_pos / move_entity_pos_rot /
// move_entity_rot / rotate_head) — never teleport_entity — and
// entity_position_sync may appear at most once (vanilla emits it for
// the spawn-time onGround flip).
func TestMobMovePacketFamilyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Prey"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type familyCount struct {
		pos, posRot, rot, rotHead, posSync, teleport int
	}
	run := func(tag, addr string) familyCount {
		c, pr, pw := joinPlayBlackbox(t, addr, "Prey")
		defer c.Close()
		drainToJoinBlackbox(t, tag, pr)
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 20 -60 8")
		sendCommand(t, pw, "summon minecraft:zombie 8 -60 8")
		var zom int32 = -1
		for zom < 0 {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s spawn wait: %v", tag, err)
			}
			if p.ID != server.IDPlaySpawnEntity {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			_, _ = r.UUID()
			typ, _ := r.VarInt()
			if id, ok := server.EntityTypeID("zombie"); ok && typ == id {
				zom = eid
			}
		}
		var fc familyCount
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if len(p.Data) < 1 {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			eid, eerr := r.VarInt()
			if eerr != nil || eid != zom {
				continue
			}
			switch p.ID {
			case 0x35:
				fc.pos++
				dx, _ := r.Short()
				dy, _ := r.Short()
				dz, _ := r.Short()
				if math.Abs(float64(dx)/4096) >= 8 || math.Abs(float64(dy)/4096) >= 8 || math.Abs(float64(dz)/4096) >= 8 {
					t.Fatalf("%s pos delta out of delta range: %d,%d,%d", tag, dx, dy, dz)
				}
			case 0x36:
				fc.posRot++
			case 0x38:
				fc.rot++
			case 0x53:
				fc.rotHead++
			case 0x23:
				fc.posSync++
			case 0x7d:
				fc.teleport++
			}
		}
		return fc
	}

	v := run("vanilla", targets.vanilla)
	g := run("mc-go", targets.mcgo)
	t.Logf("vanilla=%+v mc-go=%+v", v, g)
	for tag, fc := range map[string]familyCount{"vanilla": v, "mc-go": g} {
		if fc.pos+fc.posRot == 0 {
			t.Fatalf("%s emitted no move delta packets for a chasing zombie: %+v", tag, fc)
		}
		if fc.teleport > 0 {
			t.Fatalf("%s emitted teleport_entity for a walking zombie: %+v", tag, fc)
		}
		if fc.posSync > 1 {
			t.Fatalf("%s emitted %d entity_position_sync for a walking zombie, want ≤1", tag, fc.posSync)
		}
	}
}

// TestSignEditorPackets proves sign block entities at the packet level:
// /setblock oak_sign emits Block Entity Data (type 7, sign NBT), using
// the sign opens the editor, and Update Sign fans the new text back as
// Block Entity Data.
func TestSignEditorPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Signer")
	defer c.Close()
	drainToJoinBlackbox(t, "signer", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 -61 0 oak_sign")

	// Block Entity Data for the sign: position, type 7, NBT with
	// front_text.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting sign data: %v", err)
		}
		if p.ID != server.IDPlayBlockEntityData {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		x, y, z, err := r.Position()
		if err != nil {
			t.Fatalf("sign pos: %v", err)
		}
		typ, err := r.VarInt()
		if err != nil {
			t.Fatalf("sign type: %v", err)
		}
		rest, err := r.Read(r.Remaining())
		if err != nil {
			t.Fatalf("sign nbt: %v", err)
		}
		if x != 8 || y != -61 || z != 0 {
			t.Fatalf("sign pos = %d,%d,%d", x, y, z)
		}
		if typ != 7 {
			t.Fatalf("sign block entity type = %d, want 7", typ)
		}
		if !bytes.Contains(rest, []byte("front_text")) {
			t.Fatalf("sign nbt lacks front_text: %x", rest)
		}
		break
	}

	// Use the sign: the editor opens.
	var use protocol.Buffer
	use.VarInt(0)
	use.Position(8, -61, 0)
	use.VarInt(1)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(9)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting editor: %v", err)
		}
		if p.ID != server.IDPlayOpenSignEditor {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		if x, y, z, err := r.Position(); err != nil || x != 8 || y != -61 || z != 0 {
			t.Fatalf("editor pos = %d,%d,%d err %v", x, y, z, err)
		}
		if front, err := r.Bool(); err != nil || !front {
			t.Fatalf("editor front = %v, err %v", front, err)
		}
		break
	}

	// Edit the sign; the new text comes back as Block Entity Data.
	var up protocol.Buffer
	up.Position(8, -61, 0)
	up.Bool(true)
	for _, line := range []string{"hello", "parity", "", "world"} {
		up.String(line)
	}
	if err := pw.WritePacket(server.IDPlayUpdateSignSB, up.Bytes()); err != nil {
		t.Fatal(err)
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting sign update: %v", err)
		}
		if p.ID != server.IDPlayBlockEntityData {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		if _, _, _, err := r.Position(); err != nil {
			t.Fatal(err)
		}
		if typ, err := r.VarInt(); err != nil || typ != 7 {
			t.Fatalf("update type = %d, err %v", typ, err)
		}
		rest, err := r.Read(r.Remaining())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(rest, []byte("parity")) {
			t.Fatalf("sign update lacks edited text: %x", rest)
		}
		return
	}
}

// TestChestPackets proves chest containers at the packet level:
// placing a chest and using it opens the generic 9x3 screen with 63
// slots, clicking moves items between the player and the chest, and
// closing acknowledges with Container Close.
func TestChestPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Trader")
	defer c.Close()
	drainToJoinBlackbox(t, "trader", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 -61 0 chest")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give dirt 2")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "tp @s 8 -60 1")
	confirmSyncsBlackbox(t, c, pr, pw)

	// Use the chest: Open Screen (window 1, generic 9x3) then the menu.
	var use protocol.Buffer
	use.VarInt(0)
	use.Position(8, -61, 0)
	use.VarInt(1)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(11)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}
	var window int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting open screen: %v", err)
		}
		if p.ID != server.IDPlayOpenScreen {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, err := r.VarInt()
		if err != nil {
			t.Fatalf("window id: %v", err)
		}
		typ, err := r.VarInt()
		if err != nil || typ != 2 {
			t.Fatalf("window type = %d, err %v (want 2 generic_9x3)", typ, err)
		}
		rest, _ := r.Read(r.Remaining())
		if !bytes.Contains(rest, []byte("container.chest")) {
			t.Fatalf("window title = %x, want Chest", rest)
		}
		window = id
		break
	}
	if window == 0 {
		t.Fatal("window id 0 used for a chest")
	}
	// Container content: 63 slots, all empty.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting chest content: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt() // state
		n, err := r.VarInt()
		if err != nil || n != 63 {
			t.Fatalf("chest slots = %d, err %v (want 63)", n, err)
		}
		break
	}

	// Click the hotbar stack onto the cursor, then into chest slot 0.
	// Container states advance per click, so the clicks carry the live
	// state instead of a fixed zero.
	st := syncClickState(t, pr, pw, window)
	click := func(slot int16) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(window)
		b.VarInt(st)
		b.Short(slot)
		b.Byte(0)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	click(54) // hotbar 0
	click(0)  // chest slot 0

	sawChest := false
	for !sawChest {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting chest slot: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt() // state
		slot, err := r.Short()
		if err != nil || slot != 0 {
			continue
		}
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if id != world.ItemIDs["dirt"] || count != 2 {
			t.Fatalf("chest slot 0 = id %d x%d, want dirt x2", id, count)
		}
		sawChest = true
	}

	// Close the window: the real server never echoes a client-initiated
	// close, so the close is done once the answer settles instead of
	// waiting for a Container Close that never comes.
	var close protocol.Buffer
	close.VarInt(window)
	if err := pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes()); err != nil {
		t.Fatal(err)
	}
	drainToQuietBlackbox(t, c, pr, 800*time.Millisecond)
}

// TestPersistenceReconnectPackets proves player persistence at the
// packet level: items granted to a player survive a reconnect on the
// same server (the join Container Content carries them back).
func TestPersistenceReconnectPackets(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data.json")
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.DataPath = dataFile
	})

	c1, pr1, pw1 := joinPlayBlackbox(t, addr, "Returner")
	drainToJoinBlackbox(t, "returner-1", pr1)
	_ = c1.SetDeadline(time.Now().Add(10 * time.Second))
	sendChatBlackbox(t, pw1, server.IDPlayChatCommandSB, "give dirt 3")
	// Wait until the grant lands in the hotbar.
	for {
		p, err := pr1.ReadPacket()
		if err != nil {
			t.Fatalf("waiting give: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		slot, _ := r.Short()
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if slot == 36 && count == 3 && id == world.ItemIDs["dirt"] {
			break
		}
	}
	_ = c1.Close()

	// Wait for the server to retire the session before rejoining.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v := fetchStatus(t, addr); v.Players.Online == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	c2, pr2, _ := joinPlayBlackbox(t, addr, "Returner")
	defer c2.Close()
	drainToJoinBlackbox(t, "returner-2", pr2)
	_ = c2.SetDeadline(time.Now().Add(10 * time.Second))
	for {
		p, err := pr2.ReadPacket()
		if err != nil {
			t.Fatalf("waiting restored inventory: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		win, _ := r.VarInt()
		if win != 0 {
			continue
		}
		_, _ = r.VarInt() // state
		n, _ := r.VarInt()
		if n != 46 {
			continue
		}
		for i := int32(0); i < n; i++ {
			count, err := r.VarInt()
			if err != nil {
				t.Fatalf("slot %d: %v", i, err)
			}
			if count <= 0 {
				continue
			}
			id, _ := r.VarInt()
			if i == 36 && id == world.ItemIDs["dirt"] && count == 3 {
				return
			}
		}
		t.Fatal("rejoined inventory lacks the persisted dirt")
	}
}

// TestMcGoOnlyRecipeBookPersistence proves the recipe book survives a
// rejoin: a granted recipe re-adds itself inside the join burst's
// recipe_book_add and a repeat /recipe give fails with vanilla's
// already-known error instead of re-adding.
func TestMcGoOnlyRecipeBookPersistence(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data.json")
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.DataPath = dataFile
	})

	c1, pr1, pw1 := joinPlayBlackbox(t, addr, "Bookworm")
	drainToJoinBlackbox(t, "bookworm-1", pr1)
	_ = c1.SetDeadline(time.Now().Add(10 * time.Second))
	sendChatBlackbox(t, pw1, server.IDPlayChatCommandSB, "recipe give Bookworm barrel")
	if out := readSystemChatBlackbox(t, pr1, 10*time.Second); !strings.Contains(out, "commands.recipe.give.success") {
		t.Fatalf("/recipe give = %q", out)
	}
	_ = c1.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v := fetchStatus(t, addr); v.Players.Online == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	c2, pr2, pw2 := joinPlayBlackbox(t, addr, "Bookworm")
	defer c2.Close()
	_ = c2.SetDeadline(time.Now().Add(15 * time.Second))
	// The join burst must carry the restored book: a recipe_book_add
	// with at least one entry (fresh players get an empty one).
	foundAdd := false
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p, err := pr2.ReadPacket()
		if err != nil {
			break
		}
		if p.ID != server.IDPlayRecipeBookAdd {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		if n, err := r.VarInt(); err == nil && n > 0 {
			foundAdd = true
			break
		}
	}
	if !foundAdd {
		t.Fatal("rejoin burst carried no restored recipe-book entries")
	}
	// A repeat grant must fail the same way it would mid-session —
	// only possible if the book state survived.
	sendChatBlackbox(t, pw2, server.IDPlayChatCommandSB, "recipe give Bookworm barrel")
	if out := readSystemChatBlackbox(t, pr2, 10*time.Second); !strings.Contains(out, "commands.recipe.give.failed") {
		t.Fatalf("repeat /recipe give after rejoin = %q, want give.failed", out)
	}
}

// TestFurnacePackets proves furnace containers at the packet level:
// using a furnace opens the furnace menu (type 14) with 39 slots and
// the four burn/cook properties, and items move into the input and
// fuel slots through Click Container.
func TestFurnacePackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Smelter")
	defer c.Close()
	drainToJoinBlackbox(t, "smelter", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 -61 0 furnace")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give sand 1")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give coal 1")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "tp @s 8 -60 1")
	confirmSyncsBlackbox(t, c, pr, pw)

	var use protocol.Buffer
	use.VarInt(0)
	use.Position(8, -61, 0)
	use.VarInt(1)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(13)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}

	var window int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting furnace screen: %v", err)
		}
		if p.ID != server.IDPlayOpenScreen {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, _ := r.VarInt()
		typ, err := r.VarInt()
		if err != nil || typ != 14 {
			t.Fatalf("menu type = %d, err %v (want 14 furnace)", typ, err)
		}
		window = id
		break
	}
	// Contents: 39 slots.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting furnace content: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt()
		n, err := r.VarInt()
		if err != nil || n != 39 {
			t.Fatalf("furnace slots = %d, err %v (want 39)", n, err)
		}
		break
	}

	// Move the hotbar sand (menu 30) into the input (0) and the coal
	// (menu 31) into the fuel (1). Container states advance per click.
	st := syncClickState(t, pr, pw, window)
	click := func(slot int16) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(window)
		b.VarInt(st)
		b.Short(slot)
		b.Byte(0)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	move := func(from, to int16) {
		click(from)
		click(to)
	}
	move(30, 0)
	move(31, 1)

	sawInput, sawFuel := false, false
	for !(sawInput && sawFuel) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting transfers (input=%v fuel=%v): %v", sawInput, sawFuel, err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt()
		slot, err := r.Short()
		if err != nil {
			continue
		}
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		switch {
		case slot == 0 && count == 1 && id == world.ItemIDs["sand"]:
			sawInput = true
		case slot == 1 && count == 1 && id == world.ItemIDs["coal"]:
			sawFuel = true
		}
	}
}

// TestCraftingPackets proves the 2x2 player crafting grid at the
// packet level: moving a log into the grid makes the result slot carry
// four planks, and taking the result clears the grid and moves the
// planks to the cursor.
func TestCraftingPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Crafter")
	defer c.Close()
	drainToJoinBlackbox(t, "crafter", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give oak_log 1")

	// The give bumped the window-0 state, so sync it before clicking.
	st := syncClickState(t, pr, pw, 0)
	click := func(slot int16) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(0) // player inventory window
		b.VarInt(st)
		b.Short(slot)
		b.Byte(0)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	// Give lands in hotbar slot 36; move it into the grid's top-left.
	click(36)
	click(1)

	// The result slot now shows four planks.
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting result: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		slot, _ := r.Short()
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if slot == 0 {
			if id != world.ItemIDs["oak_planks"] || count != 4 {
				t.Fatalf("result = id %d x%d, want oak_planks x4", id, count)
			}
			break
		}
	}

	// Take the result: cursor gets the planks, grid clears.
	click(0)
	sawGrid, sawCursor, sawClear := false, false, false
	for !(sawGrid && sawCursor && sawClear) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting craft take (grid=%v cursor=%v clear=%v): %v", sawGrid, sawCursor, sawClear, err)
		}
		switch p.ID {
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.VarInt()
			_, _ = r.VarInt()
			slot, _ := r.Short()
			count, _ := r.VarInt()
			id, _ := r.VarInt()
			if slot == 1 && count == 0 {
				sawGrid = true
			}
			if slot == 0 && count == 0 {
				sawClear = true
			}
			_ = id
		case server.IDPlaySetCarriedItem:
			r := protocol.NewPacketReader(p.Data)
			count, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			if count > 0 {
				id, _ := r.VarInt()
				if id == world.ItemIDs["oak_planks"] && count == 4 {
					sawCursor = true
				}
			}
		}
	}
}

// TestCraftingTablePackets proves the 3x3 crafting menu at the packet
// level: using a crafting table opens menu type 12 with 46 slots,
// right-clicking four planks into the grid yields a crafting table in
// the result slot, and taking it clears the grid.
func TestCraftingTablePackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Crafter")
	defer c.Close()
	drainToJoinBlackbox(t, "crafter", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 -61 0 crafting_table")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give oak_planks 4")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "tp @s 8 -60 1")
	confirmSyncsBlackbox(t, c, pr, pw)

	var use protocol.Buffer
	use.VarInt(0)
	use.Position(8, -61, 0)
	use.VarInt(1)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(17)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}

	var window int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting crafting screen: %v", err)
		}
		if p.ID != server.IDPlayOpenScreen {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, _ := r.VarInt()
		typ, err := r.VarInt()
		if err != nil || typ != 12 {
			t.Fatalf("menu type = %d, err %v (want 12 crafting)", typ, err)
		}
		window = id
		break
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting crafting content: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt()
		n, err := r.VarInt()
		if err != nil || n != 46 {
			t.Fatalf("crafting slots = %d, err %v (want 46)", n, err)
		}
		break
	}

	// Container states advance per click, so these carry the live state.
	st := syncClickState(t, pr, pw, window)
	click := func(slot int16, button byte) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(window)
		b.VarInt(st)
		b.Short(slot)
		b.Byte(button)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	click(37, 0) // pick the planks from the hotbar
	for _, slot := range []int16{1, 2, 4, 5} {
		click(slot, 1) // right-click one plank into each grid cell
	}

	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting result: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		slot, _ := r.Short()
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if slot != 0 {
			continue
		}
		if id != world.ItemIDs["crafting_table"] || count != 1 {
			t.Fatalf("result = id %d x%d, want crafting_table x1", id, count)
		}
		break
	}

	// Take the result and confirm the grid clears.
	click(0, 0)
	sawGrid, sawResult := false, false
	for !(sawGrid && sawResult) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting take (grid=%v result=%v): %v", sawGrid, sawResult, err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		slot, _ := r.Short()
		count, _ := r.VarInt()
		if slot == 1 && count == 0 {
			sawGrid = true
		}
		if slot == 0 && count == 0 {
			sawResult = true
		}
	}
}

// TestHungerSprintPackets proves hunger drain reaches the wire:
// sprinting across enough blocks consumes a food point and the server
// syncs Set Health with the new food level.
func TestHungerSprintPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Sprinter")
	defer c.Close()
	drainToJoinBlackbox(t, "sprinter", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	// Start sprinting.
	var in protocol.Buffer
	in.Byte(0x40)
	if err := pw.WritePacket(server.IDPlayPlayerInputSB, in.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Sprint 45 blocks (0.1 exhaustion each = 4.5): enough to consume
	// one saturation point.
	for i := 1; i <= 45; i++ {
		if err := sendMovePosRotPitch(pw, 8.5+float64(i), -60, 0.5, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting hunger sync: %v", err)
		}
		if p.ID != server.IDPlaySetHealth {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		health, err := r.Float()
		if err != nil {
			t.Fatal(err)
		}
		food, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		saturation, err := r.Float()
		if err != nil {
			t.Fatal(err)
		}
		// Saturation drains before food; the first sync shows 4 left.
		if saturation < 5 {
			if health != 20 || food != 20 || saturation != 4 {
				t.Fatalf("hunger sync = health %v food %d sat %v, want 20/20/4", health, food, saturation)
			}
			return
		}
	}
}

// readSlotBB reads one protocol Slot (count, then item id and component
// patches when non-empty). Component payloads are skipped so the stream
// stays aligned; only minecraft:damage (type 3, measured on vanilla 26.2
// as a bare VarInt, e.g. a half-worn iron pickaxe reads damage 125) is
// understood, anything else errors loudly instead of silently desyncing
// every slot after it.
func readSlotBB(r *protocol.PacketReader) (server.ItemStack, error) {
	it, _, _, err := readSlotBBFull(r)
	return it, err
}

// readSlotBBFull is readSlotBB plus the damage value: hasDamage reports a
// minecraft:damage component, dmg is its value. Repair cost
// (minecraft:repair_cost, type 19, bare VarInt, measured on 26.2)
// decodes into the stack; any other component still fails, so a click
// carrying an unmodeled component (enchantments, a custom name) stays
// a loud parse error instead of a silent desync.
func readSlotBBFull(r *protocol.PacketReader) (server.ItemStack, int32, bool, error) {
	it, dmg, hasDmg, _, err := readSlotBBComps(r)
	return it, dmg, hasDmg, err
}

// readSlotBBComps is readSlotBBFull plus the emitted component-type id
// list — tests asserting the exact patch shape (the captain banner's
// 4-component set, the bottle amplifier) read it.
// bbTrimMaterials / bbTrimPatterns are the captured 26.2 registry
// orders — trim_material: alphabetical over its 11 entries,
// trim_pattern: alphabetical over its 18 (measured on the vanilla
// registries packet).
var bbTrimMaterials = []string{
	"amethyst", "copper", "diamond", "emerald", "gold", "iron",
	"lapis", "netherite", "quartz", "redstone", "resin",
}
var bbTrimPatterns = []string{
	"bolt", "coast", "dune", "eye", "flow", "host", "raiser", "rib",
	"sentry", "shaper", "silence", "snout", "spire", "tide", "vex",
	"ward", "wayfinder", "wild",
}

// bbBannerPatterns is the 26.2 banner_pattern registry order — the
// sorted datapack file list; bbDyeNames indexes DyeColor ordinals.
var bbBannerPatterns = []string{
	"base", "border", "bricks", "circle", "creeper", "cross",
	"curly_border", "diagonal_left", "diagonal_right",
	"diagonal_up_left", "diagonal_up_right", "flow",
	"flower", "globe", "gradient", "gradient_up", "guster",
	"half_horizontal",
	"half_horizontal_bottom", "half_vertical", "half_vertical_right",
	"mojang", "piglin", "rhombus", "skull", "small_stripes",
	"square_bottom_left", "square_bottom_right", "square_top_left",
	"square_top_right", "straight_cross", "stripe_bottom",
	"stripe_center", "stripe_downleft", "stripe_downright",
	"stripe_left", "stripe_middle", "stripe_right", "stripe_top",
	"triangle_bottom", "triangle_top", "triangles_bottom",
	"triangles_top",
}
var bbDyeNames = []string{
	"white", "orange", "magenta", "light_blue", "yellow", "lime",
	"pink", "gray", "light_gray", "cyan", "purple", "blue", "brown",
	"green", "red", "black",
}

// bbFireworkShapeNames is the FireworkExplosion.Shape wire order.
var bbFireworkShapeNames = []string{"small_ball", "large_ball", "star", "creeper", "burst"}

// bbFireworkExplosion decodes one explosion to the canonical
// "shape|c1,c2|f1,f2|trail|twinkle" the server model uses.
func bbFireworkExplosion(r *protocol.PacketReader) (string, error) {
	shape, err := r.VarInt()
	if err != nil || shape < 0 || int(shape) >= len(bbFireworkShapeNames) {
		return "", fmt.Errorf("blackbox: firework shape %d", shape)
	}
	rd := func() ([]int32, error) {
		n, err := r.VarInt()
		if err != nil || n < 0 {
			return nil, err
		}
		out := make([]int32, 0, n)
		for ; n > 0; n-- {
			v, err := r.Int()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	join := func(v []int32) string {
		parts := make([]string, len(v))
		for i, c := range v {
			parts[i] = strconv.Itoa(int(c))
		}
		return strings.Join(parts, ",")
	}
	colors, err := rd()
	if err != nil {
		return "", err
	}
	fades, err := rd()
	if err != nil {
		return "", err
	}
	trail, err := r.Byte()
	if err != nil {
		return "", err
	}
	twinkle, err := r.Byte()
	if err != nil {
		return "", err
	}
	b01 := func(b byte) string {
		if b != 0 {
			return "1"
		}
		return "0"
	}
	return bbFireworkShapeNames[shape] + "|" + join(colors) + "|" + join(fades) + "|" + b01(trail) + "|" + b01(twinkle), nil
}

func readSlotBBComps(r *protocol.PacketReader) (server.ItemStack, int32, bool, []int32, error) {
	count, err := r.VarInt()
	if err != nil || count <= 0 {
		return server.ItemStack{}, 0, false, nil, err
	}
	id, err := r.VarInt()
	if err != nil {
		return server.ItemStack{}, 0, false, nil, err
	}
	nadd, err := r.VarInt() // components to add
	if err != nil {
		return server.ItemStack{}, 0, false, nil, err
	}
	nrem, err := r.VarInt() // components to remove (types only, no payload)
	if err != nil {
		return server.ItemStack{}, 0, false, nil, err
	}
	var dmg int32
	hasDmg := false
	var comps []int32
	it := server.ItemStack{}
	for ; nadd > 0; nadd-- {
		typ, err := r.VarInt()
		if err != nil {
			return server.ItemStack{}, 0, false, nil, err
		}
		comps = append(comps, typ)
		switch typ {
		case 3: // minecraft:damage, VarInt payload (measured 26.2)
			dmg, err = r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.Damage = dmg
			hasDmg = true
		case 6: // minecraft:custom_name, NBT string tag (measured 26.2)
			tag, terr := r.Byte()
			if terr != nil {
				return server.ItemStack{}, 0, false, nil, terr
			}
			if tag != 0x08 {
				return server.ItemStack{}, 0, false, nil, fmt.Errorf("blackbox: custom_name tag %d is not a string", tag)
			}
			n, terr2 := r.UShort()
			if terr2 != nil {
				return server.ItemStack{}, 0, false, nil, terr2
			}
			raw, terr3 := r.Read(int(n))
			if terr3 != nil {
				return server.ItemStack{}, 0, false, nil, terr3
			}
			it.Name = string(raw)
		case 19: // minecraft:repair_cost, VarInt payload (measured 26.2)
			if it.RepairCost, err = r.VarInt(); err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
		case 51: // minecraft:potion_contents (measured 26.2): holder bool+id,
			// then absent custom_color / effects list / custom_name.
			if tag, terr := r.Byte(); terr != nil {
				return server.ItemStack{}, 0, false, nil, terr
			} else if tag != 0 {
				pid, terr := r.VarInt()
				if terr != nil {
					return server.ItemStack{}, 0, false, nil, terr
				}
				it.Potion = fmt.Sprintf("h%d", pid)
			}
			if _, terr := r.Byte(); terr != nil { // custom_color
				return server.ItemStack{}, 0, false, nil, terr
			}
			if n, terr := r.VarInt(); terr != nil { // custom_effects
				return server.ItemStack{}, 0, false, nil, terr
			} else if n != 0 {
				return server.ItemStack{}, 0, false, nil, fmt.Errorf("blackbox: potion custom_effects %d", n)
			}
			if _, terr := r.Byte(); terr != nil { // custom_name
				return server.ItemStack{}, 0, false, nil, terr
			}
		case 53: // minecraft:suspicious_stew_effects: VarInt count, then
			// (effect holder id, duration) VarInt pairs.
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var parts []string
			for ; n > 0; n-- {
				eid, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				dur, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				parts = append(parts, fmt.Sprintf("e%d:%d", eid, dur))
			}
			it.StewEffects = strings.Join(parts, ",")
		case 74: // minecraft:pot_decorations: VarInt count then four
			// item registry ids (back, left, right, front).
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var deco []string
			for ; n > 0; n-- {
				iid, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				deco = append(deco, fmt.Sprintf("i%d", iid))
			}
			it.PotDeco = strings.Join(deco, ",")
		case 13, 42: // minecraft:enchantments / stored_enchantments: VarInt map of enchant id to level (measured 26.2)
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var pairs []string
			for ; n > 0; n-- {
				eid, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				lv, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				pairs = append(pairs, fmt.Sprintf("%d:%d", eid, lv))
			}
			// ItemEnchantments is an Object2IntOpenHashMap on the
			// real server: wire order is its bucket order, not the
			// insert order. Sort the key so equal sets read equal.
			sort.Strings(pairs)
			if typ == 13 {
				it.Ench = strings.Join(pairs, ",")
			} else {
				it.StoredEnch = strings.Join(pairs, ",")
			}
		case 56: // minecraft:trim: material holder then pattern holder,
			// each a registry id +1 (measured 26.2 — a lapis bolt trim
			// emits 07 01). Names resolve against the captured orders
			// below.
			mid, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			pid, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			if mid < 1 || int(mid) > len(bbTrimMaterials) || pid < 1 || int(pid) > len(bbTrimPatterns) {
				return server.ItemStack{}, 0, false, nil, fmt.Errorf("blackbox: trim holder ids %d/%d", mid, pid)
			}
			it.Trim = bbTrimMaterials[mid-1] + "/" + bbTrimPatterns[pid-1]
		case 72: // minecraft:banner_patterns: VarInt layer count, then
			// (pattern holder VarInt, dye ordinal VarInt) pairs — the
			// captain banner writes 8, a loom take appends one.
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var layers []string
			for ; n > 0; n-- {
				ph, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				col, err := r.VarInt()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				if ph < 1 || int(ph) > len(bbBannerPatterns) || col < 0 || int(col) > 15 {
					return server.ItemStack{}, 0, false, nil, fmt.Errorf("blackbox: banner layer %d/%d", ph, col)
				}
				layers = append(layers, bbBannerPatterns[ph-1]+":"+bbDyeNames[col])
			}
			if len(layers) > 0 {
				it.BannerPatterns = strings.Join(layers, ",")
			}
			if n8 := len(layers); n8 == 8 {
				it.Ominous = true
			}
		case 18: // minecraft:tooltip_display: hide byte then a VarInt
			// holder list of hidden component types.
			if _, err := r.Byte(); err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			for ; n > 0; n-- {
				if _, err := r.VarInt(); err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
			}
		case 9: // minecraft:item_name: an NBT text component — the
			// captain banner ships a {translate:} compound; string form
			// is legal too. ReadUnnamed consumes the tag byte itself.
			if _, terr := nbt.ReadUnnamed(pktReader{r}); terr != nil {
				return server.ItemStack{}, 0, false, nil, terr
			}
		case 12: // minecraft:rarity, enum VarInt
			if _, err := r.VarInt(); err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
		case 63: // minecraft:ominous_bottle_amplifier, VarInt
			amp, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.BottleAmpSet = true
			it.BottleAmp = amp
		case 46: // minecraft:map_id: a bare VarInt MapId — stored +1
			// internally so 0 keeps meaning absent.
			id, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.MapID = id + 1
		case 48: // minecraft:map_post_processing: a bare VarInt —
			// lock 0, scale 1; stored +1 like MapID.
			post, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.MapPost = post + 1
		case 73: // minecraft:base_color: a bare DyeColor ordinal —
			// stored +1 like MapID so 0 means absent.
			bc, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.BaseColor = bc + 1
		case 43: // minecraft:dye — a bare DyeColor VarInt; stored +1
			// like base_color so 0 means absent.
			d, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.Dye = d + 1
		case 44: // minecraft:dyed_color — one rgb int.
			c, err := r.Int()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.DyedColor = c
		case 68: // minecraft:firework_explosion
			exp, err := bbFireworkExplosion(r)
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.FireworkExp = exp
		case 69: // minecraft:fireworks — flight VarInt then a
			// VarInt-counted explosion list.
			fl, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var sb strings.Builder
			sb.WriteString(strconv.Itoa(int(fl)))
			for ; n > 0; n-- {
				exp, err := bbFireworkExplosion(r)
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				sb.WriteString(";" + exp)
			}
			it.Fireworks = sb.String()
		case 54: // minecraft:writable_book_content: VarInt page count,
			// then Filterable<String> leaves (raw string + optional
			// filtered string), measured on 26.2.
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var pages []string
			for ; n > 0; n-- {
				raw, err := r.String()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				has, err := r.Bool()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				fs := ""
				if has {
					if fs, err = r.String(); err != nil {
						return server.ItemStack{}, 0, false, nil, err
					}
				}
				pages = append(pages, fmt.Sprintf("%q/%q", raw, fs))
			}
			it.BookWritable = strings.Join(pages, ";")
		case 55: // minecraft:written_book_content: Filterable<String>
			// title, author string, generation VarInt, a list of
			// Filterable<Component> pages (each an unnamed NBT tag),
			// then the resolved flag.
			title, err := r.String()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			thas, err := r.Bool()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			tfs := ""
			if thas {
				if tfs, err = r.String(); err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
			}
			author, err := r.String()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			gen, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			n, err := r.VarInt()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			var pages []string
			for ; n > 0; n-- {
				raw, terr := nbt.ReadUnnamed(pktReader{r})
				if terr != nil {
					return server.ItemStack{}, 0, false, nil, terr
				}
				has, err := r.Bool()
				if err != nil {
					return server.ItemStack{}, 0, false, nil, err
				}
				var fv any
				if has {
					if fv, err = nbt.ReadUnnamed(pktReader{r}); err != nil {
						return server.ItemStack{}, 0, false, nil, err
					}
				}
				pages = append(pages, fmt.Sprintf("%v/%v", raw, fv))
			}
			res, err := r.Bool()
			if err != nil {
				return server.ItemStack{}, 0, false, nil, err
			}
			it.BookWritten = fmt.Sprintf("%q/%q|%s|%d|%s|%v",
				title, tfs, author, gen, strings.Join(pages, ";"), res)
		default:
			return server.ItemStack{}, 0, false, nil, fmt.Errorf("blackbox: unknown added component type %d on item %d", typ, id)
		}
	}
	for ; nrem > 0; nrem-- {
		if _, err := r.VarInt(); err != nil { // removed types carry no payload
			return server.ItemStack{}, 0, false, nil, err
		}
	}
	it.ID = id
	it.Count = count
	return it, dmg, hasDmg, comps, nil
}

// pktReader adapts PacketReader to io.Reader so nbt.ReadUnnamed can
// consume a mid-stream compound.
type pktReader struct{ r *protocol.PacketReader }

func (p pktReader) Read(b []byte) (int, error) {
	d, err := p.r.Read(len(b))
	n := copy(b, d)
	return n, err
}

// TestEquipmentPackets proves armor sync at the packet level: equipping
// a helmet is broadcast to online observers, and a joining observer
// receives existing players' equipment with their spawns.
func TestEquipmentPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, pwa := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)
	_ = ca.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pwa, server.IDPlayChatCommandSB, "give iron_helmet 1")
	aliceUUID := offlineUUIDBlackbox("Alice")

	// The give bumped Alice's window-0 state, so sync it before clicking.
	st := syncClickState(t, pra, pwa, 0)
	click := func(slot int16) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(st)
		b.Short(slot)
		b.Byte(0)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pwa.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	// Move the helmet from the hotbar into the helmet armor slot.
	click(36)
	click(5)

	cb, prb, _ := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	_ = cb.SetDeadline(time.Now().Add(10 * time.Second))

	// Bob's join carries Alice's equipment: helmet slot with the item.
	var aliceEID int32
	helmetSeen := false
	for !(aliceEID != 0 && helmetSeen) {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob waiting equipment (eid=%d helmet=%v): %v", aliceEID, helmetSeen, err)
		}
		switch p.ID {
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			u, _ := r.UUID()
			if u == aliceUUID {
				aliceEID = eid
			}
		case server.IDPlaySetEquipment:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			for {
				slot, err := r.Byte()
				if err != nil {
					t.Fatal(err)
				}
				last := slot&0x80 == 0
				slot &= 0x7F
				stack, err := readSlotBB(r)
				if err != nil {
					t.Fatal(err)
				}
				if eid == aliceEID && slot == 5 {
					if stack.ID != world.ItemIDs["iron_helmet"] || stack.Count != 1 {
						t.Fatalf("helmet equipment = %+v", stack)
					}
					helmetSeen = true
				}
				if last {
					break
				}
			}
		}
	}
}

// TestDamageEventPackets proves the Damage Event packet content: Bob's
// attack on Alice carries the damage type, and both the cause and
// direct ids are Bob's entity id plus one.
func TestDamageEventPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	ca, pra, _ := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()

	// Bob learns Alice's entity id from her spawn; meanwhile Alice
	// learns Bob's from his.
	aliceUUID := offlineUUIDBlackbox("Alice")
	bobUUID := offlineUUIDBlackbox("Bob")
	var aliceEID int32
	deadline := time.Now().Add(10 * time.Second)
	_ = cb.SetDeadline(deadline)
	for aliceEID == 0 {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob waiting alice spawn: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		u, _ := r.UUID()
		if u == aliceUUID {
			aliceEID = eid
		}
	}
	var bobEID int32
	_ = ca.SetDeadline(deadline)
	for bobEID == 0 {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice waiting bob spawn: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		u, _ := r.UUID()
		if u == bobUUID {
			bobEID = eid
		}
	}

	var atk protocol.Buffer
	atk.VarInt(aliceEID)
	if err := pwb.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}

	for {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice waiting damage event: %v", err)
		}
		if p.ID != server.IDPlayDamageEvent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, err := r.VarInt()
		if err != nil || eid != aliceEID {
			t.Fatalf("damage eid = %d, err %v (want %d)", eid, err, aliceEID)
		}
		typeID, err := r.VarInt()
		if err != nil || typeID == 0 {
			t.Fatalf("damage type = %d, err %v", typeID, err)
		}
		cause, err := r.VarInt()
		if err != nil || cause != bobEID+1 {
			t.Fatalf("damage cause = %d, err %v (want %d)", cause, err, bobEID+1)
		}
		direct, err := r.VarInt()
		if err != nil || direct != bobEID+1 {
			t.Fatalf("damage direct = %d, err %v (want %d)", direct, err, bobEID+1)
		}
		hasPos, err := r.Bool()
		if err != nil {
			t.Fatal(err)
		}
		if !hasPos {
			t.Fatal("player-attack damage event lacks a source position")
		}
		return
	}
}

// TestCombatEnterPackets proves Enter Combat reaches both fighters: Bob
// attacks Alice and both sessions receive the empty Enter Combat
// packet.
func TestCombatEnterPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	ca, pra, _ := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()

	aliceUUID := offlineUUIDBlackbox("Alice")
	var aliceEID int32
	deadline := time.Now().Add(10 * time.Second)
	_ = cb.SetDeadline(deadline)
	for aliceEID == 0 {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob waiting alice: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		u, _ := r.UUID()
		if u == aliceUUID {
			aliceEID = eid
		}
	}
	var atk protocol.Buffer
	atk.VarInt(aliceEID)
	if err := pwb.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}

	sawAlice, sawBob := false, false
	_ = ca.SetDeadline(deadline)
	for !(sawAlice && sawBob) && time.Now().Before(deadline) {
		if p, ok := readWithin(pra, 100*time.Millisecond); ok && p.ID == server.IDPlayEnterCombat {
			if len(p.Data) != 0 {
				t.Fatalf("enter combat body = %x", p.Data)
			}
			sawAlice = true
		}
		if p, ok := readWithin(prb, 100*time.Millisecond); ok && p.ID == server.IDPlayEnterCombat {
			if len(p.Data) != 0 {
				t.Fatalf("enter combat body = %x", p.Data)
			}
			sawBob = true
		}
	}
	if !(sawAlice && sawBob) {
		t.Fatalf("enter combat: alice=%v bob=%v", sawAlice, sawBob)
	}
}

// TestHopperPackets proves hopper containers at the packet level: using
// a hopper opens the five-slot menu (type 16, 41 slots), and moving an
// item from the hotbar into the hopper syncs the slot.
func TestHopperPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Hopper")
	defer c.Close()
	drainToJoinBlackbox(t, "hopper", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "setblock 8 -61 0 hopper")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give stone 2")
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "tp @s 8 -60 1")
	confirmSyncsBlackbox(t, c, pr, pw)

	var use protocol.Buffer
	use.VarInt(0)
	use.Position(8, -61, 0)
	use.VarInt(1)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(19)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}

	var window int32
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting hopper screen: %v", err)
		}
		if p.ID != server.IDPlayOpenScreen {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, _ := r.VarInt()
		typ, err := r.VarInt()
		if err != nil || typ != 16 {
			t.Fatalf("menu type = %d, err %v (want 16 hopper)", typ, err)
		}
		window = id
		break
	}
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting hopper content: %v", err)
		}
		if p.ID != server.IDPlayContainerContent {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		wid, _ := r.VarInt()
		if wid != window {
			continue
		}
		_, _ = r.VarInt()
		n, err := r.VarInt()
		if err != nil || n != 41 {
			t.Fatalf("hopper slots = %d, err %v (want 41)", n, err)
		}
		break
	}

	// Container states advance per click, so these carry the live state.
	st := syncClickState(t, pr, pw, window)
	click := func(slot int16) {
		t.Helper()
		var b protocol.Buffer
		b.VarInt(window)
		b.VarInt(st)
		b.Short(slot)
		b.Byte(0)
		b.VarInt(0)
		b.VarInt(0)
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		st++
	}
	click(32) // hotbar 0 in the hopper menu
	click(0)  // hopper slot 0

	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting hopper slot: %v", err)
		}
		if p.ID != server.IDPlayContainerSlot {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		_, _ = r.VarInt()
		_, _ = r.VarInt()
		slot, _ := r.Short()
		count, _ := r.VarInt()
		id, _ := r.VarInt()
		if slot == 0 {
			if id != world.ItemIDs["stone"] || count != 2 {
				t.Fatalf("hopper slot 0 = id %d x%d, want stone x2", id, count)
			}
			return
		}
	}
}

// TestEatingPackets proves food consumption at the packet level: a
// golden apple is always edible, eating syncs Set Health with the
// restored saturation, and the item leaves the hotbar.
func TestEatingPackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Eater")
	defer c.Close()
	drainToJoinBlackbox(t, "eater", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "give golden_apple 1")

	var use protocol.Buffer
	use.VarInt(0) // hand
	use.VarInt(1) // sequence
	use.Float(0)
	use.Float(0)
	if err := pw.WritePacket(server.IDPlayUseItemSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}

	sawHealth, sawConsumed := false, false
	for !(sawHealth && sawConsumed) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting eat (health=%v consumed=%v): %v", sawHealth, sawConsumed, err)
		}
		switch p.ID {
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.Float() // health
			_, _ = r.VarInt()
			sat, err := r.Float()
			if err != nil {
				t.Fatal(err)
			}
			if sat > 5 {
				if sat != 14.6 {
					t.Fatalf("saturation = %v, want 14.6", sat)
				}
				sawHealth = true
			}
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			_, _ = r.VarInt()
			_, _ = r.VarInt()
			slot, _ := r.Short()
			count, _ := r.VarInt()
			if slot == 36 && count == 0 {
				sawConsumed = true
			}
		}
	}
}

// TestVoidDamagePackets proves out-of-world damage at the packet
// level: /tp below the void threshold produces Damage Event and health
// loss. Waits are event-driven, not timing assertions.
func TestVoidDamagePackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Void")
	defer c.Close()
	drainToJoinBlackbox(t, "void", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))

	var tp protocol.Buffer
	tp.VarInt(1)
	_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tp.Bytes())
	_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "tp 0 -200 0")

	sawDamage, sawHealth := false, false
	for !(sawDamage && sawHealth) {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting void damage (damage=%v health=%v): %v", sawDamage, sawHealth, err)
		}
		switch p.ID {
		case server.IDPlayDamageEvent:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.VarInt(); err != nil {
				t.Fatal(err)
			}
			typeID, err := r.VarInt()
			if err != nil || typeID == 0 {
				t.Fatalf("void damage type = %d, err %v", typeID, err)
			}
			sawDamage = true
		case server.IDPlaySetHealth:
			r := protocol.NewPacketReader(p.Data)
			if h, err := r.Float(); err == nil && h < 20 {
				sawHealth = true
			}
		}
	}
}

// TestWeaponDamagePackets proves held-weapon damage on the wire: Bob
// attacks with an iron sword and Alice's health syncs to 14 (6 damage
// from full).
func TestWeaponDamagePackets(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	ca, pra, _ := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()

	aliceUUID := offlineUUIDBlackbox("Alice")
	var aliceEID int32
	deadline := time.Now().Add(10 * time.Second)
	_ = cb.SetDeadline(deadline)
	for aliceEID == 0 {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob waiting alice: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		u, _ := r.UUID()
		if u == aliceUUID {
			aliceEID = eid
		}
	}
	sendChatBlackbox(t, pwb, server.IDPlayChatCommandSB, "give iron_sword 1")
	// Select the sword (hotbar slot 0).
	var held protocol.Buffer
	held.Short(0)
	if err := pwb.WritePacket(server.IDPlayHeldChangeSB, held.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Switching items starts the attack-strength cooldown: an iron
	// sword at 1.6 attack speed needs 20/1.6 = 12.5 ticks ≈ 625 ms to
	// reach full charge, on vanilla as much as on mc-go. Hit early and
	// the scaled damage is a fraction of 6.
	time.Sleep(750 * time.Millisecond)
	var atk protocol.Buffer
	atk.VarInt(aliceEID)
	if err := pwb.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}

	_ = ca.SetDeadline(deadline)
	for {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice waiting health: %v", err)
		}
		if p.ID != server.IDPlaySetHealth {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		h, err := r.Float()
		if err != nil {
			t.Fatal(err)
		}
		if h == 14 {
			return
		}
		if h != 20 {
			t.Fatalf("health = %v, want 14 (iron sword)", h)
		}
	}
}

// TestScoreboardPackets proves the scoreboard packet flow over the
// wire: objective creation, score updates, sidebar display, and reset.
func TestScoreboardPackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Scorer")
	defer c.Close()
	drainToJoinBlackbox(t, "scorer", pr)
	drainToQuietBlackbox(t, c, pr, 400*time.Millisecond)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "scoreboard objectives add test Test Score")
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting objective: %v", err)
		}
		if p.ID != server.IDPlayObjectiveCB {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		name, _ := r.String()
		mode, _ := r.Byte()
		if name != "test" || mode != 0 {
			t.Fatalf("objective = %q mode %d", name, mode)
		}
		present, _ := r.Bool()
		if !present {
			t.Fatal("objective display missing")
		}
		rest, _ := r.Read(r.Remaining())
		if !bytes.Contains(rest, []byte("Test Score")) {
			t.Fatalf("display component lacks text: %x", rest)
		}
		break
	}

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "scoreboard players set Scorer test 9")
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting score: %v", err)
		}
		if p.ID != server.IDPlayUpdateScore {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		entity, _ := r.String()
		objective, _ := r.String()
		value, _ := r.VarInt()
		if entity != "Scorer" || objective != "test" || value != 9 {
			t.Fatalf("score = %s/%s/%d", entity, objective, value)
		}
		break
	}

	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "scoreboard objectives setdisplay sidebar test")
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting display: %v", err)
		}
		if p.ID != server.IDPlayDisplayObjective {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		position, _ := r.VarInt()
		name, _ := r.String()
		if position != 1 || name != "test" {
			t.Fatalf("display = %d/%q", position, name)
		}
		return
	}
}
