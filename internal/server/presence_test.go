package server

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// decodeInfoAdd mirrors encodePlayerInfoAdd field by field.
func decodeInfoAdd(t *testing.T, data []byte) (byte, []playerEntry) {
	t.Helper()
	r := protocol.NewPacketReader(data)
	act, err := r.Byte()
	if err != nil {
		t.Fatalf("actions: %v", err)
	}
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 128 {
		t.Fatalf("count = %d, err %v", n, err)
	}
	out := make([]playerEntry, 0, n)
	for i := int32(0); i < n; i++ {
		var e playerEntry
		if e.uuid, err = r.UUID(); err != nil {
			t.Fatalf("entry %d uuid: %v", i, err)
		}
		if e.name, err = r.String(); err != nil {
			t.Fatalf("entry %d name: %v", i, err)
		}
		if _, err := r.VarInt(); err != nil { // properties (empty)
			t.Fatalf("entry %d props: %v", i, err)
		}
		if _, err := r.Bool(); err != nil { // initialize chat: no signature data
			t.Fatalf("entry %d chat: %v", i, err)
		}
		if e.gamemode, err = r.VarInt(); err != nil {
			t.Fatalf("entry %d gamemode: %v", i, err)
		}
		if e.listed, err = r.Bool(); err != nil {
			t.Fatalf("entry %d listed: %v", i, err)
		}
		if e.latency, err = r.VarInt(); err != nil {
			t.Fatalf("entry %d latency: %v", i, err)
		}
		if _, err := r.Bool(); err != nil { // display name present
			t.Fatalf("entry %d display name: %v", i, err)
		}
		if _, err := r.VarInt(); err != nil { // list priority
			t.Fatalf("entry %d priority: %v", i, err)
		}
		if _, err := r.Bool(); err != nil { // hat visible
			t.Fatalf("entry %d hat: %v", i, err)
		}
		out = append(out, e)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing = %d", r.Remaining())
	}
	return act, out
}

func TestPlayerInfoEncodingRoundTrip(t *testing.T) {
	body := encodePlayerInfoAdd([]playerEntry{
		{uuid: offlineUUID("Alice"), name: "Alice", gamemode: 1, listed: true},
		{uuid: offlineUUID("Bob"), name: "Bob", gamemode: 1, listed: true, latency: 5},
	})
	act, entries := decodeInfoAdd(t, body)
	if act != infoActions {
		t.Fatalf("actions = %#x, want %#x", act, infoActions)
	}
	if len(entries) != 2 || entries[0].name != "Alice" || entries[1].name != "Bob" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].uuid != offlineUUID("Alice") || entries[0].gamemode != 1 || !entries[0].listed {
		t.Fatalf("alice = %+v", entries[0])
	}
	if entries[1].latency != 5 {
		t.Fatalf("bob latency = %d", entries[1].latency)
	}

	rm := encodePlayerInfoRemove([][16]byte{offlineUUID("Alice")})
	r := protocol.NewPacketReader(rm)
	n, err := r.VarInt()
	if err != nil || n != 1 {
		t.Fatalf("remove count = %d, err %v", n, err)
	}
	if u, err := r.UUID(); err != nil || u != offlineUUID("Alice") {
		t.Fatalf("remove uuid = %x, err %v", u, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("remove trailing = %d", r.Remaining())
	}
}

func TestPlayerInfoRemoveOnLeave(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)

	cb, prb, _ := joinPlay(t, s, "Bob")
	drainJoinBurst(t, prb)
	// Bob's burst tail: Alice's row.
	waitPlayerInfo(t, prb, "Alice")
	drainToQuiet(t, prb)
	// Alice: join chat + Bob's row.
	waitSystemChat(t, pra, "Bob")
	bobInfo, err := waitPacket(t, pra, IDPlayPlayerInfoUpdate)
	if err != nil {
		t.Fatalf("alice info: %v", err)
	}
	_, entries := decodeInfoAdd(t, bobInfo.Data)
	if len(entries) != 1 || entries[0].name != "Bob" {
		t.Fatalf("entries = %+v", entries)
	}
	var bobUUID [16]byte
	s.sessions.Range(func(_, v any) bool {
		if v.(*Session).getName() == "Bob" {
			bobUUID = v.(*Session).getUUID()
			return false
		}
		return true
	})
	if entries[0].uuid != bobUUID {
		t.Fatalf("uuid = %x, want %x", entries[0].uuid, bobUUID)
	}

	_ = cb.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = ca.SetDeadline(time.Now().Add(500 * time.Millisecond))
		q, err := pra.ReadPacket()
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("no remove: %v", err)
			}
			continue
		}
		if q.ID != IDPlayPlayerInfoRemove {
			continue
		}
		r := protocol.NewPacketReader(q.Data)
		n, err := r.VarInt()
		if err != nil || n != 1 {
			t.Fatalf("remove count = %d, err %v", n, err)
		}
		u, err := r.UUID()
		if err != nil || u != bobUUID {
			t.Fatalf("remove uuid = %x, err %v (want %x)", u, err, bobUUID)
		}
		return
	}
}

func TestLatencyBroadcast(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	drainToQuiet(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	waitPlayerInfo(t, prb, "Alice")
	drainToQuiet(t, prb)

	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	alice.rtt.Store(int64(42 * time.Millisecond))
	s.broadcastPlayerLatency(alice)

	// Both Alice and Bob receive the update-latency row.
	check := func(pr *protocol.Reader) {
		t.Helper()
		for i := 0; i < 64; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("latency read: %v", err)
			}
			if p.ID != IDPlayPlayerInfoUpdate {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			act, err := r.Byte()
			if err != nil || act != infoLatency {
				continue
			}
			n, err := r.VarInt()
			if err != nil || n != 1 {
				t.Fatalf("latency entries = %d, err %v", n, err)
			}
			u, err := r.UUID()
			if err != nil || u != alice.getUUID() {
				t.Fatalf("latency uuid = %x, err %v", u, err)
			}
			ms, err := r.VarInt()
			if err != nil || ms != 42 {
				t.Fatalf("latency = %d, err %v (want 42)", ms, err)
			}
			return
		}
		t.Fatal("latency update never arrived")
	}
	check(prb)
	check(pra)
	_ = bob
}
