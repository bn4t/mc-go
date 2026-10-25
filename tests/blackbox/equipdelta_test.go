// LivingEntity.detectEquipmentUpdates — the per-tick equipment diff,
// measured on the vanilla 26.2 jar (LivingEntity.java):
//
//   - each slot's stack is diffed against lastEquipmentItems with
//     ItemStack.matches (item + components, count-insensitive); only
//     changed slots pack into ONE ClientboundSetEquipmentPacket, in
//     slot-ordinal order.
//   - handleHandSwap: when mainhand AND offhand both changed and each
//     new stack matches the other's previous, the two entries leave
//     changedItems and tracking players get entity_event 55
//     (SWAP_HANDS) instead — no set_equipment at all.
//   - a count-only change (eat, pickup) or a switch between like
//     stacks emits nothing.
//
// Pinned here: player A mutates equipment while player B watches A's
// entity — held-slot switches emit a one-entry delta, an
// empty-to-empty switch emits nothing, an armor write emits one
// entry, and SWAP_HELD_ITEMS emits entity_event 55 with no
// set_equipment.
package blackbox

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// equipObs is what one observation window collected about set_equipment
// and entity_event traffic: per-eid lists of (slot,item) packet entries
// and entity_event statuses.
type equipObs struct {
	equips map[int32][][]equipEntry // eid -> packets -> entries
	events map[int32][]byte         // eid -> statuses
	addEID map[[16]byte]int32       // uuid -> eid from add_entity
}

type equipEntry struct {
	slot int
	name string
}

func equipWatch(t *testing.T, v *vehPair, window time.Duration) equipObs {
	t.Helper()
	o := equipObs{
		equips: map[int32][][]equipEntry{},
		events: map[int32][]byte{},
		addEID: map[[16]byte]int32{},
	}
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		for _, c := range []struct {
			c  net.Conn
			pr *protocol.Reader
			pw *protocol.Writer
		}{{v.ca, v.pra, v.pwa}, {v.cb, v.prb, v.pwb}} {
			_ = c.c.SetReadDeadline(time.Now().Add(15 * time.Millisecond))
			for {
				p, err := c.pr.ReadPacket()
				if err != nil {
					break
				}
				switch int32(p.ID) {
				case server.IDPlayKeepAliveCB:
					_ = c.pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(c.pw)
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					id, _ := r.VarInt()
					r.Double()
					r.Double()
					r.Double()
					var b protocol.Buffer
					b.VarInt(id)
					_ = c.pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				case 1: // add_entity
					r := protocol.NewPacketReader(p.Data)
					eid, _ := r.VarInt()
					ub, _ := r.UUID()
					o.addEID[ub] = eid
				case server.IDPlaySetEquipment:
					r := protocol.NewPacketReader(p.Data)
					ee, _ := r.VarInt()
					var entries []equipEntry
					for {
						slot, err := r.Byte()
						if err != nil {
							break
						}
						last := slot&0x80 == 0
						slot &= 0x7F
						st, _, _, _, err := readSlotBBComps(r)
						if err != nil {
							break
						}
						name := ""
						if !st.Empty() {
							name, _ = world.ItemName(st.ID)
						}
						entries = append(entries, equipEntry{int(slot), name})
						if last {
							break
						}
					}
					if len(entries) > 0 {
						o.equips[ee] = append(o.equips[ee], entries)
					}
				case server.IDPlayEntityEvent:
					r := protocol.NewPacketReader(p.Data)
					ee, _ := r.Int()
					st, _ := r.Byte()
					o.events[ee] = append(o.events[ee], st)
				}
			}
			_ = c.c.SetReadDeadline(time.Time{})
		}
	}
	return o
}

// sendHeldSwitch writes a set_carried_item selecting hotbar slot n.
func sendHeldSwitch(pw *protocol.Writer, n int16) {
	var b protocol.Buffer
	b.Short(n)
	_ = pw.WritePacket(server.IDPlayHeldChangeSB, b.Bytes())
}

// sendSwapHands writes player_action SWAP_ITEM_WITH_OFFHAND — the
// {action, pos, face, sequence} shape vanilla 26.2 expects.
func sendSwapHands(pw *protocol.Writer) {
	var b protocol.Buffer
	b.VarInt(6)
	b.Position(0, 0, 0)
	b.Byte(0)
	b.VarInt(0)
	_ = pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes())
}

func TestPlayerEquipDeltaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		drainChunkWindow(t, "equipdelta", v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "fill 2 -61 6 12 -61 12 minecraft:stone")
		sendCommand(t, v.pwa, "tp BOATA 4.5 -60 8 0 0")
		sendCommand(t, v.pwa, "tp BOATB 8.5 -60 8 0 0")
		// Two distinct held stacks; offhand stays empty for now.
		sendCommand(t, v.pwa, "item replace entity BOATA hotbar.0 with minecraft:cobblestone")
		sendCommand(t, v.pwa, "item replace entity BOATA hotbar.1 with minecraft:dirt")

		// Find A's entity id on B's conn via its add_entity uuid.
		uuidA := server.OfflineUUID("BOATA")
		var aEID int32
		for tries := 0; tries < 30 && aEID == 0; tries++ {
			o := equipWatch(t, v, 300*time.Millisecond)
			aEID = o.addEID[uuidA]
		}
		if aEID == 0 {
			t.Fatalf("never saw BOATA's add_entity on BOATB's conn")
		}
		sendHeldSwitch(v.pwa, 0)

		// Probe 1: held 0 -> 1 swaps cobblestone for dirt in the
		// mainhand. Vanilla emits exactly one set_equipment carrying
		// the single changed slot.
		sendHeldSwitch(v.pwa, 1)
		o := equipWatch(t, v, 1500*time.Millisecond)
		got := o.equips[aEID]
		if len(got) != 1 || len(got[0]) != 1 || got[0][0].slot != 0 || got[0][0].name != "dirt" {
			t.Fatalf("held switch -> equips=%v; want one packet with {slot0,dirt}", got)
		}

		// Probe 2: held 1 -> 2 changes dirt for empty — one entry,
		// empty stack.
		sendHeldSwitch(v.pwa, 2)
		o = equipWatch(t, v, 1500*time.Millisecond)
		got = o.equips[aEID]
		if len(got) != 1 || len(got[0]) != 1 || got[0][0].slot != 0 || got[0][0].name != "" {
			t.Fatalf("held->empty switch -> equips=%v; want one packet with {slot0,empty}", got)
		}

		// Probe 3: held 2 -> 3 is empty -> empty — no emit at all.
		sendHeldSwitch(v.pwa, 3)
		o = equipWatch(t, v, 1200*time.Millisecond)
		if got := o.equips[aEID]; len(got) != 0 {
			t.Fatalf("empty->empty switch emitted set_equipment: %v", got)
		}

		// Probe 4: an armor write emits the one changed slot.
		sendCommand(t, v.pwa, "item replace entity BOATA armor.head with minecraft:leather_helmet")
		o = equipWatch(t, v, 1500*time.Millisecond)
		got = o.equips[aEID]
		if len(got) != 1 || len(got[0]) != 1 || got[0][0].slot != 5 || got[0][0].name != "leather_helmet" {
			t.Fatalf("armor.head write -> equips=%v; want one packet with {slot5,leather_helmet}", got)
		}

		// Probe 5: arm the offhand, then SWAP_HELD_ITEMS. The held
		// slot is 3 (empty mainhand); give it a stack first so the
		// swap is stack-for-stack.
		sendCommand(t, v.pwa, "item replace entity BOATA hotbar.3 with minecraft:cobblestone")
		sendCommand(t, v.pwa, "item replace entity BOATA weapon.offhand with minecraft:torch")
		_ = equipWatch(t, v, 1500*time.Millisecond) // drain both deltas
		sendSwapHands(v.pwa)
		o = equipWatch(t, v, 1500*time.Millisecond)
		if got := o.equips[aEID]; len(got) != 0 {
			t.Fatalf("hand swap emitted set_equipment: %v; want entity_event 55 only", got)
		}
		saw55 := false
		for _, st := range o.events[aEID] {
			if st == 55 {
				saw55 = true
			}
		}
		if !saw55 {
			t.Fatalf("hand swap emitted no entity_event 55; events=%v", o.events[aEID])
		}
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}
