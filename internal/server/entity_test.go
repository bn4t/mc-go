package server

import (
	"testing"

	"mc-go/internal/protocol"
)

func TestManagerLifecycle(t *testing.T) {
	m := NewManager()
	a := m.Spawn(&Entity{Type: EntityTypePig, X: 1, Y: 65, Z: 1})
	b := m.Spawn(&Entity{Type: EntityTypePig, X: 2, Y: 65, Z: 2})
	if a.ID == b.ID || a.UUID == b.UUID {
		t.Fatalf("ids not unique: %+v %+v", a, b)
	}
	if m.Count() != 2 {
		t.Fatalf("count = %d", m.Count())
	}
	got, ok := m.Get(a.ID)
	if !ok || got.X != 1 {
		t.Fatalf("get = %+v,%v", got, ok)
	}
	if !m.Remove(a.ID) || m.Remove(a.ID) {
		t.Fatal("remove semantics wrong")
	}
	if m.Count() != 1 {
		t.Fatalf("count = %d", m.Count())
	}
}

func TestSpawnEncodingRoundTrip(t *testing.T) {
	e := &Entity{ID: 7, Type: EntityTypePig, X: 10.5, Y: 65, Z: -3.25, Yaw: 90, Pitch: 5, HeadYaw: 180}
	copy(e.UUID[:], []byte("0123456789abcdef"))
	body := encodeSpawn(e)
	r := protocol.NewPacketReader(body)
	if id, err := r.VarInt(); err != nil || id != 7 {
		t.Fatalf("id = %d, err %v", id, err)
	}
	if u, err := r.UUID(); err != nil || u != e.UUID {
		t.Fatalf("uuid = %x, err %v", u, err)
	}
	if ty, err := r.VarInt(); err != nil || ty != EntityTypePig {
		t.Fatalf("type = %d, err %v", ty, err)
	}
	if x, err := r.Double(); err != nil || x != 10.5 {
		t.Fatalf("x = %v, err %v", x, err)
	}
	if _, err := r.Double(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Double(); err != nil {
		t.Fatal(err)
	}
	// LpVec3 velocity: zero is a single 0x00 byte.
	if v, err := r.Byte(); err != nil || v != 0 {
		t.Fatalf("velocity = %d, err %v (want zero LpVec3)", v, err)
	}
	if p, err := r.Byte(); err != nil || p != angleByte(5) {
		t.Fatalf("pitch = %d, err %v", p, err)
	}
	if yw, err := r.Byte(); err != nil || yw != angleByte(90) {
		t.Fatalf("yaw = %d, err %v", yw, err)
	}
	if h, err := r.Byte(); err != nil || h != angleByte(180) {
		t.Fatalf("head yaw = %d, err %v", h, err)
	}
	if d, err := r.VarInt(); err != nil || d != 0 {
		t.Fatalf("data = %d, err %v", d, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing = %d", r.Remaining())
	}
}

func TestRemoveEncoding(t *testing.T) {
	r := protocol.NewPacketReader(encodeRemove(9))
	if n, err := r.VarInt(); err != nil || n != 1 {
		t.Fatalf("count = %d, err %v", n, err)
	}
	if id, err := r.VarInt(); err != nil || id != 9 {
		t.Fatalf("id = %d, err %v", id, err)
	}
}

func TestAngleByte(t *testing.T) {
	cases := map[float32]byte{0: 0, 360: 0, 180: 128, 90: 64, -90: 192, 45: 32}
	for deg, want := range cases {
		if got := angleByte(deg); got != want {
			t.Fatalf("angle(%v) = %d, want %d", deg, got, want)
		}
	}
}

func TestManagerUpdate(t *testing.T) {
	m := NewManager()
	e := m.Spawn(&Entity{Type: EntityTypePig, X: 1, Y: 2, Z: 3})
	if !m.Update(e.ID, 4, 5, 6, 90, 0, true) {
		t.Fatal("update failed")
	}
	got, ok := m.Get(e.ID)
	if !ok || got.X != 4 || got.Y != 5 || got.Z != 6 || got.Yaw != 90 {
		t.Fatalf("updated = %+v,%v", got, ok)
	}
	if m.Update(9999, 0, 0, 0, 0, 0, false) {
		t.Fatal("update of missing entity succeeded")
	}
}

func TestSpawnKeepsPresetUUID(t *testing.T) {
	m := NewManager()
	var preset [16]byte
	preset[0] = 0xAB
	e := m.Spawn(&Entity{UUID: preset, Type: EntityTypePig})
	if e.UUID != preset {
		t.Fatalf("uuid = %x, want preset", e.UUID)
	}
	got, _ := m.Get(e.ID)
	if got.UUID != preset {
		t.Fatalf("stored uuid = %x", got.UUID)
	}
	plain := m.Spawn(&Entity{Type: EntityTypePig})
	if plain.UUID == ([16]byte{}) {
		t.Fatal("synthetic uuid missing")
	}
	if plain.ID == e.ID {
		t.Fatal("duplicate entity ids")
	}
}

func TestEncodeEntityTeleport(t *testing.T) {
	body := encodeEntityTeleport(3, 20.5, 66, 8.5, 90, 0, true)
	r := protocol.NewPacketReader(body)
	if id, err := r.VarInt(); err != nil || id != 3 {
		t.Fatalf("id = %d err %v", id, err)
	}
	if x, err := r.Double(); err != nil || x != 20.5 {
		t.Fatalf("x = %v err %v", x, err)
	}
	if _, err := r.Double(); err != nil {
		t.Fatalf("y: %v", err)
	}
	if _, err := r.Double(); err != nil {
		t.Fatalf("z: %v", err)
	}
	for i := 0; i < 3; i++ {
		if v, err := r.Double(); err != nil || v != 0 {
			t.Fatalf("velocity %d = %v err %v", i, v, err)
		}
	}
	if yaw, err := r.Float(); err != nil || yaw != 90 {
		t.Fatalf("yaw = %v err %v", yaw, err)
	}
	if _, err := r.Float(); err != nil {
		t.Fatalf("pitch: %v", err)
	}
	if g, err := r.Bool(); err != nil || !g {
		t.Fatalf("ground = %v err %v", g, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes", r.Remaining())
	}
}
