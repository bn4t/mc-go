package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestMountClickParity pins the mount-inventory click path: picking
// the saddle out of the player's row echoes a container_set_slot plus
// a set_cursor_item for the carried stack; placing it in mount slot 0
// resyncs the whole menu (AbstractMountInventoryMenu's
// hasInventoryChanged → broadcastChanges full state) and broadcasts
// the donkey's set_equipment with the SADDLE wire slot.
func TestMountClickParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		// pickup echoes
		pickSetSlot  bool // container_set_slot empties the player slot
		pickCursor   bool // set_cursor_item carries the saddle
		pickStateSeq []int32
		// place effects
		placeSetSlot bool // set_slot puts the saddle in slot 0
		placeContent bool // or a full resync does
		placeSlot0   bool // resync shows saddle in slot 0
		placeState   int32
		equip        bool // set_equipment saddle slot landed
	}
	pigID, _ := server.EntityTypeID("donkey")

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		sendCommand(t, pw, "give P minecraft:saddle")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)
		sendCommand(t, pw, "summon minecraft:donkey 2.5 -60 0.5 {NoAI:1b,Tame:1b,ChestedHorse:1b}")

		var donkeyEID, cid, state, saddleWinSlot int32 = -1, -1, -1, -1
		var clicked int // 0 none, 1 pickup sent, 2 place sent
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySpawnEntity:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				rd.UUID()
				typ, _ := rd.VarInt()
				if typ == pigID && donkeyEID == -1 {
					donkeyEID = eid
					var ib protocol.Buffer
					ib.VarInt(eid)
					ib.VarInt(0)
					ib.Byte(0)
					ib.Byte(1)
					_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
				}
			case 0x29: // mount_screen_open
				rd := protocol.NewPacketReader(p.Data)
				cid, _ = rd.VarInt()
			case 0x12: // container_set_content
				rd := protocol.NewPacketReader(p.Data)
				wcid, _ := rd.VarInt()
				st, _ := rd.VarInt()
				n, _ := rd.VarInt()
				for i := int32(0); i < n; i++ {
					present, _ := rd.Bool()
					if !present {
						continue
					}
					id, _ := rd.VarInt()
					rd.VarInt() // count
					skipSlotData(rd)
					if iname, _ := world.ItemName(id); iname == "saddle" {
						if i >= 17 { // player row carries the saddle pre-place
							saddleWinSlot = i
						}
						if clicked == 3 && i == 0 {
							r.placeSlot0 = true
						}
					}
				}
				if wcid == cid {
					if clicked == 2 {
						r.placeContent = true
						r.placeState = st
					}
					state = st
					if clicked == 0 && saddleWinSlot >= 0 && donkeyEID >= 0 && cid >= 0 {
						clicked = 1
						writeMountClick(pw, cid, state, int16(saddleWinSlot), 0, 0)
						clicked = 2 // place fires when the pickup echo bumps state
					}
				}
			case 0x14: // container_set_slot
				rd := protocol.NewPacketReader(p.Data)
				rd.VarInt() // window id
				st, _ := rd.VarInt()
				slot, _ := rd.Short()
				present, _ := rd.Bool()
				if clicked >= 1 && slot == int16(saddleWinSlot) && !present {
					r.pickSetSlot = true
					r.pickStateSeq = append(r.pickStateSeq, st)
					// The place click must ride the state the echo bumped.
					if clicked == 2 {
						writeMountClick(pw, cid, st, 0, 0, 0)
						clicked = 3
					}
				} else if clicked == 3 && slot == 0 && present {
					id, _ := rd.VarInt()
					rd.VarInt()
					if iname, _ := world.ItemName(id); iname == "saddle" {
						r.placeSetSlot = true
						r.placeState = st
					}
				}
			case 0x60: // set_cursor_item — the carried saddle
				rd := protocol.NewPacketReader(p.Data)
				if present, _ := rd.Bool(); present {
					id, _ := rd.VarInt()
					rd.VarInt()
					if clicked >= 1 {
						if iname, _ := world.ItemName(id); iname == "saddle" {
							r.pickCursor = true
						}
					}
				}
			case 0x66: // set_equipment
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				if eid == donkeyEID {
					for {
						sb, err := rd.Byte()
						if err != nil {
							break
						}
						present, _ := rd.Bool()
						if present {
							id, _ := rd.VarInt()
							rd.VarInt()
							skipSlotData(rd)
							if sb == 7 {
								if iname, _ := world.ItemName(id); iname == "saddle" {
									r.equip = true
								}
							}
						}
						if sb&0x80 == 0 {
							break
						}
					}
				}
			}
			if r.pickSetSlot && r.pickCursor && (r.placeSetSlot || (r.placeContent && r.placeSlot0)) && r.equip {
				return r
			}
		}
		return r
	}

	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		r := run(addr)
		if !r.pickSetSlot {
			t.Fatalf("%s: no container_set_slot clearing the saddle slot", addr)
		}
		if !r.pickCursor {
			t.Fatalf("%s: no set_cursor_item for the carried saddle", addr)
		}
		if !r.placeSetSlot && !(r.placeContent && r.placeSlot0) {
			t.Fatalf("%s: place produced no slot-0 saddle echo", addr)
		}
		if !r.equip {
			t.Fatalf("%s: no set_equipment saddle slot on the donkey", addr)
		}
	}
}

// writeMountClick encodes a serverbound container_click with an empty
// changed-slots list and empty carried stack.
func writeMountClick(pw *protocol.Writer, wid, state int32, slot int16, button byte, mode int32) {
	var b protocol.Buffer
	b.VarInt(wid)
	b.VarInt(state)
	b.Short(slot)
	b.Byte(button)
	b.VarInt(mode)
	b.VarInt(0)
	b.Bool(false)
	_ = pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes())
}

// skipSlotData walks a slot's component diff tail after id+count.
func skipSlotData(rd *protocol.PacketReader) {
	add, _ := rd.VarInt()
	for i := int32(0); i < add; i++ {
		rd.VarInt()
		rd.VarInt()
		rd.VarInt()
		if n, _ := rd.VarInt(); n > 0 {
			for j := int32(0); j < n; j++ {
				rd.VarInt()
			}
		}
	}
	rm, _ := rd.VarInt()
	for i := int32(0); i < rm; i++ {
		rd.VarInt()
	}
}
