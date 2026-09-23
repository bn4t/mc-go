package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestMountScreenParity pins AbstractHorse.mobInteract's tamed+sneak
// branch: sneak-interact on a tamed chested donkey opens the mount
// inventory — mount_screen_open(containerId, inventoryColumns=5,
// entityId int32) followed by container_set_content of the full
// 53-slot menu (17 mount + 36 player).
func TestMountScreenParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		open      bool
		cid       int32
		cols      int32
		eid       int32
		content   bool
		menuSlots int32
		state     int32
		donkeyEID int32
	}

	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(40 * time.Second))
		obs := &fleeObs{}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 0.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		obs.chats = nil
		sendCommand(t, pw, "summon minecraft:donkey 3.5 -60 0.5 {NoAI:1b,Tame:1b,ChestedHorse:1b}")

		start := time.Now()
		for time.Since(start) < 3*time.Second {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySystemChatCB:
				obs.chats = append(obs.chats, chatTextBlackbox(t, p.Data))
			case server.IDPlaySpawnEntity:
				rd := protocol.NewPacketReader(p.Data)
				eid, _ := rd.VarInt()
				rd.UUID()
				typ, _ := rd.VarInt()
				if r.donkeyEID == 0 {
					if tn, _ := server.EntityTypeName(typ); tn == "donkey" {
						r.donkeyEID = eid
					}
				}
			}
		}
		if r.donkeyEID == 0 {
			t.Fatalf("%s: donkey spawn never tracked (chats %v)", addr, obs.chats)
		}

		// ServerboundInteractPacket: entityId, hand MAIN, LpVec3
		// ZERO, usingSecondaryAction (sneaking).
		var b protocol.Buffer
		b.VarInt(r.donkeyEID)
		b.VarInt(0)
		b.Byte(0)
		b.Byte(1)
		_ = pw.WritePacket(server.IDPlayInteractSB, b.Bytes())

		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case 0x29: // mount_screen_open
				rd := protocol.NewPacketReader(p.Data)
				r.cid, _ = rd.VarInt()
				r.cols, _ = rd.VarInt()
				var raw [4]byte
				if rb, err := rd.Read(4); err == nil {
					copy(raw[:], rb)
					r.eid = int32(raw[0])<<24 | int32(raw[1])<<16 | int32(raw[2])<<8 | int32(raw[3])
				}
				r.open = true
			case 0x12: // container_set_content
				rd := protocol.NewPacketReader(p.Data)
				cid, _ := rd.VarInt()
				st, _ := rd.VarInt()
				n, _ := rd.VarInt()
				if cid == r.cid && r.open {
					r.content = true
					r.menuSlots = n
					r.state = st
				}
			}
		}
		return r
	}

	van, mc := run(targets.vanilla), run(targets.mcgo)
	for i, leg := range []result{van, mc} {
		if !leg.open {
			t.Fatalf("leg %d: no mount_screen_open", i)
		}
	}
	if van.cid != mc.cid || van.cols != mc.cols {
		t.Fatalf("open packet diverged: vanilla cid=%d cols=%d, mc cid=%d cols=%d", van.cid, van.cols, mc.cid, mc.cols)
	}
	if van.cols != 5 {
		t.Fatalf("vanilla columns=%d, want 5", van.cols)
	}
	if van.eid != van.donkeyEID || mc.eid != mc.donkeyEID {
		t.Fatalf("entity id on wire diverged: van wire=%d tracked=%d; mc wire=%d tracked=%d", van.eid, van.donkeyEID, mc.eid, mc.donkeyEID)
	}
	if !van.content || !mc.content {
		t.Fatalf("container_set_content missing: van=%v mc=%v", van.content, mc.content)
	}
	if van.menuSlots != mc.menuSlots || van.state != mc.state {
		t.Fatalf("content packet diverged: van slots=%d state=%d; mc slots=%d state=%d", van.menuSlots, van.state, mc.menuSlots, mc.state)
	}
	t.Logf("van cid=%d cols=%d eid=%d slots=%d state=%d | mc cid=%d cols=%d eid=%d slots=%d state=%d",
		van.cid, van.cols, van.eid, van.menuSlots, van.state,
		mc.cid, mc.cols, mc.eid, mc.menuSlots, mc.state)
}
