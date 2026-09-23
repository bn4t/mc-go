//go:build blackbox

package blackbox

// TestEquipBodyParity pins the mob BODY equipment slot: a wolf summoned
// with equipment.body=wolf_armor answers `data get entity ... equipment.body`
// with the same compound on both servers, and the spawn-time
// set_equipment packet carries wire slot 6 (EquipmentSlot.BODY after
// the six held/armor entries).

import (
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

func TestEquipBodyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("A", "C")}, nil)
	defer targets.cleanup()

	wolf := "@e[type=minecraft:wolf,limit=1]"
	wolfArmorID, _ := world.ItemID("wolf_armor")
	wolfTypeID, _ := server.EntityTypeID("wolf")
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "A")
			defer ca.Close()
			cc, prc, pwc := joinPlayBlackbox(t, s.addr, "C")
			defer cc.Close()
			sinkC := newChatSinkTP(prc, pwc)
			sendCommand(t, pwc, "tp A 0.5 -60 0.5")
			// Spawn + attach burst: capture the wolf's eid and the
			// set_equipment it shipped in one drain.
			sendCommand(t, pwc, `summon minecraft:wolf 8.5 -60 8.5 {NoAI:1b,equipment:{body:{id:"minecraft:wolf_armor",count:1b}}}`)
			obs := drainEquipSpawn(t, ca, pra, pwA, 2500*time.Millisecond)
			wolfEid, ok := obs.spawns[wolfTypeID]
			if !ok {
				t.Fatalf("%s: no wolf spawn packet", s.label)
			}
			it, ok := obs.equips[wolfEid][6]
			if !ok || it.count != 1 || it.id != wolfArmorID {
				t.Fatalf("%s set_equipment body slot: %+v (seen %v), want wolf_armor x1 at slot 6",
					s.label, it, obs.equips[wolfEid])
			}
			// Save surface: equipment.body serializes the stack.
			if st, _ := dataGetSel(t, sinkC, pwc, wolf, "equipment.body.id"); st != "query" {
				t.Fatalf("%s equipment.body.id: %q, want query", s.label, st)
			}
			line := dataGetLine(t, sinkC, pwc, wolf, "equipment.body")
			if !strings.Contains(line, "wolf_armor") {
				t.Fatalf("%s equipment.body reply %q lacks wolf_armor", s.label, line)
			}
		})
	}
}

// equipSpawnObs folds one drain's worth of spawn + set_equipment traffic.
type equipSpawnObs struct {
	spawns map[int32]int32                               // entity type -> last eid
	equips map[int32]map[int32]struct{ count, id int32 } // eid -> slot -> stack
}

// drainEquipSpawn reads the tracker conn for d, answering keepalives /
// teleports / chunk batches, recording every add_entity spawn and every
// set_equipment slot entry.
func drainEquipSpawn(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) *equipSpawnObs {
	t.Helper()
	obs := &equipSpawnObs{
		spawns: map[int32]int32{},
		equips: map[int32]map[int32]struct{ count, id int32 }{},
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
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
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, e1 := r.VarInt()
			_, e2 := r.Read(16)
			typ, e3 := r.VarInt()
			if e1 == nil && e2 == nil && e3 == nil {
				obs.spawns[typ] = eid
			}
		case server.IDPlaySetEquipment:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			slots := obs.equips[eid]
			if slots == nil {
				slots = map[int32]struct{ count, id int32 }{}
				obs.equips[eid] = slots
			}
			for {
				slotByte, err := r.Byte()
				if err != nil {
					break
				}
				more := slotByte&0x80 != 0
				slot := int32(slotByte & 0x7f)
				count, e1 := r.VarInt()
				var itemID int32
				if e1 == nil && count > 0 {
					itemID, _ = r.VarInt()
				}
				slots[slot] = struct{ count, id int32 }{count, itemID}
				// A non-empty stack tails component diffs we don't
				// decode — and the wolf body entry is the only one.
				if count > 0 || !more {
					break
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return obs
}

// dataGetLine returns the raw reply line for `data get entity <sel> <path>`.
func dataGetLine(t *testing.T, sink *chatSink, pw *protocol.Writer, sel, path string) string {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel+" "+path)
	sendCommand(t, pw, "say ZEND")
	deadline := time.Now().Add(6 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		end := false
		for _, line := range sink.take() {
			if strings.Contains(line, "data.entity.query") {
				got = line
			}
			if strings.Contains(line, "ZEND") {
				end = true
			}
		}
		if end {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	return got
}
