package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestPlayerAttachMetaParity pins the pairing metadata vanilla ships for
// a player entity: index 9 (health, float — 20.0 on a fresh join) and
// index 16 (DATA_PLAYER_MODE_CUSTOMISATION, byte 0x7f — skin parts are
// dirty-marked by client information). mc-go previously sent no
// set_entity_data at all on player attach.
func TestPlayerAttachMetaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("PMeta")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla},
		{"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "PMeta")
		defer cA.Close()
		sendClientInformation(t, pwA)
		time.Sleep(600 * time.Millisecond)
		drainChunkWindow(t, s.label, cA, prA, pwA, 300*time.Millisecond)

		cB, prB, pwB := joinPlayBlackbox(t, s.addr, "PMetaB")
		defer cB.Close()
		sendClientInformation(t, pwB)
		_ = prB

		// B's eid comes off A's spawn_entity for B; the pairing
		// set_entity_data follows inside the same attach window.
		var beid int32 = -1
		var health float32 = -1
		skinSeen := false
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && (!skinSeen || health < 0) {
			_ = cA.SetReadDeadline(time.Now().Add(time.Until(deadline)))
			p, err := prA.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case 0x01: // add_entity
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					beid = id
				}
			case 0x63: // set_entity_data
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || eid != beid {
					continue
				}
			entries:
				for r.Remaining() > 1 {
					idx, err := r.Byte()
					if err != nil || idx == 0xFF {
						break
					}
					typ, err := r.VarInt()
					if err != nil {
						break
					}
					switch typ {
					case 0: // byte
						v, err := r.Byte()
						if err != nil {
							break entries
						}
						if idx == 16 && v == 0x7f {
							skinSeen = true
						}
					case 3: // float
						v, err := r.Float()
						if err != nil {
							break entries
						}
						if idx == 9 {
							health = v
						}
					default:
						// Unparsed serializer — the entries we pin
						// ride their own packets on vanilla, so
						// abandoning the rest of this one is safe.
						break entries
					}
				}
			}
		}
		if beid < 0 {
			t.Fatalf("%s: never saw spawn_entity for second player", s.label)
		}
		if health != 20 {
			t.Errorf("%s: player attach meta missing index 9 health=20.0 (got %v)", s.label, health)
		}
		if !skinSeen {
			t.Errorf("%s: player attach meta missing index 16 skin parts=0x7f", s.label)
		}
	}
}
