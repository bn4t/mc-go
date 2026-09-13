package server

import (
	"testing"

	"mc-go/internal/protocol"
)

// readSpawn skips to the next Spawn Entity packet.
func readSpawn(t *testing.T, pr *protocol.Reader, who string) (id, ty int32, uuid [16]byte, x, y, z float64) {
	t.Helper()
	for i := 0; i < 40; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s spawn stream: %v", who, err)
		}
		if p.ID != IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		var err2 error
		if id, err2 = r.VarInt(); err2 != nil {
			t.Fatalf("%s spawn id: %v", who, err2)
		}
		if uuid, err2 = r.UUID(); err2 != nil {
			t.Fatalf("%s spawn uuid: %v", who, err2)
		}
		if ty, err2 = r.VarInt(); err2 != nil {
			t.Fatalf("%s spawn type: %v", who, err2)
		}
		if x, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s spawn x: %v", who, err2)
		}
		if y, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s spawn y: %v", who, err2)
		}
		if z, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s spawn z: %v", who, err2)
		}
		return id, ty, uuid, x, y, z
	}
	t.Fatalf("%s: no spawn entity", who)
	return 0, 0, [16]byte{}, 0, 0, 0
}

// readTeleport skips to the next Entity Teleport packet.
func readTeleport(t *testing.T, pr *protocol.Reader, who string) (id int32, x, y, z float64) {
	t.Helper()
	for i := 0; i < 60; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s teleport stream: %v", who, err)
		}
		if p.ID != IDPlayEntityTeleportCB {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		var err2 error
		if id, err2 = r.VarInt(); err2 != nil {
			t.Fatalf("%s teleport id: %v", who, err2)
		}
		if x, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s teleport x: %v", who, err2)
		}
		if y, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s teleport y: %v", who, err2)
		}
		if z, err2 = r.Double(); err2 != nil {
			t.Fatalf("%s teleport z: %v", who, err2)
		}
		return id, x, y, z
	}
	t.Fatalf("%s: no entity teleport", who)
	return 0, 0, 0, 0
}

// readRemove skips to the next Remove Entities packet and returns its ids.
func readRemove(t *testing.T, pr *protocol.Reader, who string) []int32 {
	t.Helper()
	for i := 0; i < 60; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s remove stream: %v", who, err)
		}
		if p.ID != IDPlayRemoveEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		n, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s remove count: %v", who, err)
		}
		var out []int32
		for j := int32(0); j < n; j++ {
			v, err := r.VarInt()
			if err != nil {
				t.Fatalf("%s remove id: %v", who, err)
			}
			out = append(out, v)
		}
		return out
	}
	t.Fatalf("%s: no entity remove", who)
	return nil
}

// TestPlayerVisibilityOverWire proves remote players render: Bob spawns
// for Alice on join with his profile UUID and player type, Alice's
// moves teleport for Bob, and Bob's leave removes his entity for Alice.
func TestPlayerVisibilityOverWire(t *testing.T) {
	s := New("unused")
	_, pra, pwa := joinPlayPipeConn(t, s, "Alice")
	cb, prb, pwb := joinPlayPipeConn(t, s, "Bob")
	defer cb.Close()

	// Bob's join announces to Alice: chat, tab-list row, then entity.
	if text := readChatPipe(t, pra); !containsStr(text, "Bob") {
		t.Fatalf("join text = %q", text)
	}
	if p, err := pra.ReadPacket(); err != nil || p.ID != IDPlayPlayerInfoUpdate {
		t.Fatalf("join info = %#x", p.ID)
	}
	bobID, bobTy, bobUUID, _, _, _ := readSpawn(t, pra, "alice sees bob")
	if bobTy != EntityTypePlayer {
		t.Fatalf("bob type = %d, want player %d", bobTy, EntityTypePlayer)
	}
	if bobUUID != offlineUUID("Bob") {
		t.Fatalf("bob uuid = %x, want profile", bobUUID)
	}
	if bobID < 2 {
		t.Fatalf("bob entity id = %d", bobID)
	}

	// Bob's burst carries Alice's earlier spawn after his own row.
	aliceID, aliceTy, aliceUUID, _, _, _ := readSpawn(t, prb, "bob sees alice")
	if aliceTy != EntityTypePlayer || aliceUUID != offlineUUID("Alice") {
		t.Fatalf("alice spawn = type %d uuid %x", aliceTy, aliceUUID)
	}
	if aliceID == bobID {
		t.Fatal("alice and bob share an entity id")
	}

	// Alice moves; Bob's client is teleported to match.
	var mv protocol.Buffer
	mv.Double(20.5)
	mv.Double(66)
	mv.Double(8.5)
	mv.Float(0)
	mv.Float(0)
	mv.Bool(true)
	if err := pwa.WritePacket(IDPlayMovePosRotSB, mv.Bytes()); err != nil {
		t.Fatal(err)
	}
	tid, tx, ty, tz := readTeleport(t, prb, "bob tracks alice")
	if tid != aliceID || tx != 20.5 || ty != 66 || tz != 8.5 {
		t.Fatalf("teleport = %d %v,%v,%v, want %d 20.5,66,8.5", tid, tx, ty, tz, aliceID)
	}

	// Bob leaves; Alice's tracker drops his entity.
	_ = pwb
	_ = cb.Close()
	removed := false
	for i := 0; i < 60 && !removed; i++ {
		for _, id := range readRemove(t, pra, "alice drops bob") {
			if id == bobID {
				removed = true
			}
		}
	}
	if !removed {
		t.Fatalf("bob entity %d never removed", bobID)
	}
}

func containsStr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
