package blackbox

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFurnaceOutputAwardsParity(t *testing.T) {
	names := []string{"FOPV", "FOPM", "FOSV", "FOSM", "FOAV", "FOAM", "FOHV", "FOHM", "FOOV", "FOOM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, mode := range []int32{0, 1, 6, 2, 40} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			run := func(addr, name string) string {
				d := newClickDriverOn(t, addr, name, "minecraft:furnace", 14, [][2]string{{"hotbar.0", "minecraft:diamond 1"}}, nil)
				command := func(cmd string) {
					sendCommand(t, d.pw, cmd)
					settleUse(t, d.sink, func(packets []usePkt) {
						d.foldPkts(packets)
						for _, p := range packets {
							if p.id == server.IDPlaySystemChatCB {
								t.Logf("%s command %s: %s", name, cmd, chatTextBlackbox(t, p.data))
							}
						}
					}, cmd)
				}
				command("tick freeze")
				off := 0
				for _, c := range []byte(name) {
					off = (off*31 + int(c)) & 15
				}
				x := 8 + 32*off
				command(fmt.Sprintf("forceload add %d 0", x))
				command(fmt.Sprintf("item replace block %d -61 0 container.0 with minecraft:diamond_ore 2", x))
				command(fmt.Sprintf("item replace block %d -61 0 container.1 with minecraft:coal 1", x))
				command("recipe take @s minecraft:diamond_from_smelting_diamond_ore")
				sendCommand(t, d.pw, "tick sprint 410")
				report := false
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) && !report {
					time.Sleep(50 * time.Millisecond)
					packets := d.sink.take()
					d.foldPkts(packets)
					for _, p := range packets {
						if bytes.Contains(p.data, []byte("commands.tick.sprint.report")) {
							report = true
						}
					}
				}
				if !report {
					t.Fatal("missing furnace sprint completion")
				}
				d.click("resync-cooked", 3, 0, 0, false, true)
				t.Logf("%s cooked slots=%v state=%d", name, d.snap.menu, d.state)
				if mode == 6 {
					d.click("seed", 30, 0, 0, false, false)
				}
				var b protocol.Buffer
				b.VarInt(d.win)
				b.VarInt(d.state)
				slot := int16(2)
				button := byte(1)
				if mode == 1 {
					button = 0
				}
				if mode == 2 {
					button = 1
				}
				if mode == 40 {
					button = 40
				}
				if mode == 6 {
					slot = 3
					button = 0
				}
				b.Short(slot)
				b.Byte(button)
				wireMode := mode
				if mode == 2 || mode == 40 {
					wireMode = 2
				}
				b.VarInt(wireMode)
				b.VarInt(0)
				b.VarInt(0)
				if err := d.pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				var award []byte
				total := int32(-1)
				settleUse(t, d.sink, func(packets []usePkt) {
					d.foldPkts(packets)
					for _, p := range packets {
						if p.id == server.IDPlaySetExperience {
							r := protocol.NewPacketReader(p.data)
							_, _ = r.Float()
							_, _ = r.VarInt()
							total, _ = r.VarInt()
						}
						if p.id == server.IDPlayRecipeBookAdd {
							award = append(award, p.data...)
						}
					}
				}, "take-output")
				if len(award) == 0 {
					t.Error("furnace output did not unlock smelting recipe")
				}
				// Orbs spawn at the player's feet. Resume ticks to collect the complete
				// two-smelt ledger even when the first right-click takes only one item.
				sendCommand(t, d.pw, "tick unfreeze")
				deadline = time.Now().Add(4 * time.Second)
				for time.Now().Before(deadline) && total < 2 {
					time.Sleep(50 * time.Millisecond)
					for _, p := range d.sink.take() {
						if p.id == server.IDPlaySetExperience {
							r := protocol.NewPacketReader(p.data)
							_, _ = r.Float()
							_, _ = r.VarInt()
							total, _ = r.VarInt()
						}
					}
				}
				if total != 2 {
					t.Errorf("output take XP=%d want 2", total)
				}
				command("tick freeze")
				var request protocol.Buffer
				request.VarInt(1)
				if err := d.pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
					t.Fatal(err)
				}
				crafted := int32(0)
				settleUse(t, d.sink, func(packets []usePkt) {
					for _, p := range packets {
						if p.id == 0x03 {
							r := protocol.NewPacketReader(p.data)
							n, _ := r.VarInt()
							for j := int32(0); j < n; j++ {
								cat, _ := r.VarInt()
								id, _ := r.VarInt()
								value, _ := r.VarInt()
								if cat == 1 && id == 926 {
									crafted = value
								}
							}
						}
					}
				}, "furnace-stat")
				expected := int32(2)
				if mode == 0 {
					expected = 1
				}
				if mode == 2 || mode == 40 {
					expected = 0
				}
				if crafted != expected {
					t.Errorf("%s crafted stat=%d want %d", name, crafted, expected)
				}
				sendCommand(t, d.pw, "tick unfreeze")
				if !digSentinel(t, d.pw, d.sink, "FURNACE-RUNNING", 10*time.Second) {
					t.Fatal("unfreeze barrier")
				}
				return fmt.Sprintf("recipe=%x xp=%d crafted=%d menu=%v cursor=%s", award, total, crafted, d.snap.menu, d.snap.carried)
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			t.Logf("vanilla %s; mcgo %s", want, got)
			if want != got {
				t.Error("furnace output awards differ")
			}
		})
	}
}
