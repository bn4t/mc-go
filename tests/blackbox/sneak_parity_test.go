package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// readMetaValue reads one metadata value for the supported primitive
// types, returning the numeric value for VarInt-like types.
func readMetaValue(r *protocol.PacketReader, typ int) (int32, bool) {
	switch typ {
	case 0: // Byte
		v, err := r.Byte()
		return int32(v), err == nil
	case 1: // VarInt
		v, err := r.VarInt()
		return v, err == nil
	case 3: // Float
		_, err := r.Float()
		return 0, err == nil
	case 7: // Slot
		count, err := r.VarInt()
		if err != nil {
			return 0, false
		}
		if count <= 0 {
			return 0, true
		}
		if _, err := r.VarInt(); err != nil {
			return 0, false
		}
		if _, err := r.VarInt(); err != nil {
			return 0, false
		}
		if _, err := r.VarInt(); err != nil {
			return 0, false
		}
		return 0, true
	case 8: // Boolean
		_, err := r.Bool()
		return 0, err == nil
	case 19: // Optional VarInt
		v, err := r.VarInt()
		return v, err == nil
	case 20: // Pose
		v, err := r.VarInt()
		return v, err == nil
	default:
		return 0, false
	}
}

// waitPose reads until a Set Entity Data packet for eid carries the
// pose entry, returning its value.
func waitPose(t *testing.T, c interface{ SetReadDeadline(time.Time) error }, pr *protocol.Reader, eid int32, timeout time.Duration) int32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting pose: %v", err)
		}
		if p.ID != server.IDPlayEntityMetadata {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		got, err := r.VarInt()
		if err != nil || got != eid {
			continue
		}
		for {
			index, err := r.Byte()
			if err != nil {
				t.Fatalf("metadata index: %v", err)
			}
			if index == 0xFF {
				break
			}
			typ, err := r.VarInt()
			if err != nil {
				t.Fatalf("metadata type: %v", err)
			}
			val, ok := readMetaValue(r, int(typ))
			if !ok {
				t.Fatalf("unsupported metadata type %d", typ)
			}
			if typ == 20 {
				if index != 6 {
					t.Fatalf("pose metadata index = %d, want 6 (base Entity pose)", index)
				}
				return val
			}
		}
	}
	t.Fatalf("no pose metadata within timeout")
	return -1
}

// TestSneakPosePackets proves the sneak pose metadata at the packet
// level: Player Input with the sneak bit makes both the subject and
// observers receive Set Entity Data with the pose entry (index 6,
// type 20, SNEAKING=5), and clearing the bit restores STANDING=0.
func TestSneakPosePackets(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, pwa := joinPlayBlackbox(t, addr, "SneakAlice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	drainToQuietBlackbox(t, ca, pra, 400*time.Millisecond)

	cb, prb, _ := joinPlayBlackbox(t, addr, "SneakBob")
	defer cb.Close()

	// Bob's burst carries Alice's spawn: capture her entity id.
	wantUUID := offlineUUIDBlackbox("SneakAlice")
	var aliceEID int32
	_ = cb.SetDeadline(time.Now().Add(10 * time.Second))
	for {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("waiting alice spawn: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		eid, _ := r.VarInt()
		u, _ := r.UUID()
		if u == wantUUID {
			aliceEID = eid
			break
		}
	}

	// Sneak on.
	var in protocol.Buffer
	in.Byte(0x20)
	if err := pwa.WritePacket(server.IDPlayPlayerInputSB, in.Bytes()); err != nil {
		t.Fatal(err)
	}
	if pose := waitPose(t, cb, prb, aliceEID, 5*time.Second); pose != 5 {
		t.Fatalf("observer pose = %d, want 5 (sneaking)", pose)
	}
	if pose := waitPose(t, ca, pra, aliceEID, 5*time.Second); pose != 5 {
		t.Fatalf("subject pose = %d, want 5 (sneaking)", pose)
	}

	// Sneak off.
	in.Reset()
	in.Byte(0x00)
	if err := pwa.WritePacket(server.IDPlayPlayerInputSB, in.Bytes()); err != nil {
		t.Fatal(err)
	}
	if pose := waitPose(t, cb, prb, aliceEID, 5*time.Second); pose != 0 {
		t.Fatalf("observer pose = %d, want 0 (standing)", pose)
	}
}
