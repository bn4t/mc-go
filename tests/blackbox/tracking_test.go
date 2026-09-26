package blackbox

// TestEntityTrackingParity exercises vanilla's tracked-entity model
// (ChunkMap.TrackedEntity.updatePlayer): a player is paired with an
// entity while dx²+dz² <= min(viewDistance, clientTrackingRange)*16
// AND the entity's chunk is in the player's tracking view; leaving
// that window sends remove_entities, re-entering pairs again with the
// add_entity + set_entity_data + update_attributes pairing stream.
// Each leg asserts the pair/unpair/repair state, which is all that is
// stable: vanilla's sendChanges emit cadence races the read window, so
// the trailing move packet from a second sendChanges is timing luck —
// the assertions match on packet presence, never count or order.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// trackEvent records one packet that named the summoned entity — the
// leading varint of every entity-scoped clientbound packet is the
// entity id (remove_entities carries them after the count).
type trackEvent struct {
	id  byte
	ent int32
}

// trackRead collects entity packets naming entID for d. Keep-alives
// and chunk batches are answered so the window keeps streaming.
func trackRead(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, entID int32, d time.Duration) []trackEvent {
	t.Helper()
	var got []trackEvent
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			default:
				r := protocol.NewPacketReader(p.Data)
				if byte(p.ID) == 0x4d { // remove_entities: {count, ids[]}
					n, err := r.VarInt()
					if err != nil {
						break
					}
					for i := int32(0); i < n; i++ {
						id, err := r.VarInt()
						if err != nil {
							break
						}
						if id == entID {
							got = append(got, trackEvent{byte(p.ID), id})
						}
					}
					continue
				}
				id, err := r.VarInt()
				if err == nil && id == entID {
					got = append(got, trackEvent{byte(p.ID), id})
				}
			}
		}
	}
	return got
}

// trackHas reports whether any event of the given packet id was seen.
func trackHas(evs []trackEvent, ids ...byte) bool {
	for _, e := range evs {
		for _, id := range ids {
			if e.id == id {
				return true
			}
		}
	}
	return false
}

// The position-sync packet class: entity_position_sync plus the three
// move packets a delta emit picks instead.
var trackPosIds = []byte{0x23, 0x35, 0x36, 0x37}

func TestEntityTrackingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trk"),
	}, nil)
	defer targets.cleanup()

	run := func(tag, addr string) [][]trackEvent {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trk")
		defer c.Close()
		// Stream the chunk window so the entity's home column is
		// delivered before the spawn traffic starts.
		drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
		// Let the last chunk batch flush so isChunkTracked can't race
		// the first legs (vanilla pendingChunks clears on write).
		time.Sleep(600 * time.Millisecond)

		sendCommand(t, pw, "summon minecraft:armor_stand 8.5 -60 8.5 {Marker:1b,NoGravity:1b}")
		time.Sleep(400 * time.Millisecond)
		// The add_entity carrying the new entity's id: the first
		// spawn packet whose id isn't a player we know.
		var entID int32 = -1
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) && entID < 0 {
			_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				if p.ID == server.IDPlaySpawnEntity {
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					if _, err := r.UUID(); err != nil {
						break
					}
					ty, _ := r.VarInt()
					if name, ok := server.EntityTypeName(ty); ok && name == "armor_stand" {
						entID = id
					}
				}
				if p.ID == server.IDPlayChunkBatchDone {
					_ = ackChunkBatch(pw)
				}
			}
		}
		if entID < 0 {
			t.Fatalf("%s: no armor_stand add_entity after summon", tag)
		}
		// Drain leftover burst so each leg starts quiet.
		trackRead(t, tag, c, pr, pw, entID, 300*time.Millisecond)

		var legs [][]trackEvent
		for _, dest := range []string{
			"10.5 -60 10.5",   // same chunk — position sync, stays paired
			"25.5 -60 25.5",   // a chunk over — still inside range+view
			"140.5 -60 140.5", // in the 8-chunk view but past 128 blocks
			"8.5 -60 8.5",     // home again — re-pairs with a fresh add
		} {
			sendCommand(t, pw, "tp @e[type=minecraft:armor_stand,limit=1] "+dest)
			legs = append(legs, trackRead(t, tag, c, pr, pw, entID, 1800*time.Millisecond))
		}
		return legs
	}

	vLegs := run("vanilla", targets.vanilla)
	mLegs := run("mc-go", targets.mcgo)

	// The deterministic leg: 2 blocks in the same chunk always emits
	// the position packet and never unpairs.
	if !trackHas(vLegs[0], trackPosIds...) {
		t.Errorf("same-chunk leg: vanilla sent no position packet (%v)", vLegs[0])
	}
	if !trackHas(mLegs[0], trackPosIds...) {
		t.Errorf("same-chunk leg: mc-go sent no position packet (%v)", mLegs[0])
	}
	if trackHas(vLegs[0], 0x4d) || trackHas(mLegs[0], 0x4d) {
		t.Errorf("same-chunk leg: unexpected unpair (vanilla %v, mc-go %v)", vLegs[0], mLegs[0])
	}
	// The out-of-range leg unpairs for sure — on vanilla it may fire
	// one leg early when the chunk-pending race loses, so assert on
	// legs 1+2 together.
	if !trackHas(vLegs[1], 0x4d) && !trackHas(vLegs[2], 0x4d) {
		t.Errorf("out-of-range: vanilla sent no remove_entities (%v | %v)", vLegs[1], vLegs[2])
	}
	if !trackHas(mLegs[1], 0x4d) && !trackHas(mLegs[2], 0x4d) {
		t.Errorf("out-of-range: mc-go sent no remove_entities (%v | %v)", mLegs[1], mLegs[2])
	}
	// The return leg re-pairs — a fresh add_entity on both.
	if !trackHas(vLegs[3], 0x01) {
		t.Errorf("back leg: vanilla sent no add_entity (%v)", vLegs[3])
	}
	if !trackHas(mLegs[3], 0x01) {
		t.Errorf("back leg: mc-go sent no add_entity (%v)", mLegs[3])
	}
	for i := range vLegs {
		t.Logf("leg%d: vanilla=%v mc-go=%v", i, vLegs[i], mLegs[i])
	}
}
