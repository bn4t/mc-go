package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func writeStructureEditBB(t *testing.T, w *protocol.Writer, pos [3]int32, action, mode int32, name string, offset, size [3]byte, mirror, rotation int32, data string, integrity float32, seed int64, flags byte) {
	t.Helper()
	var b protocol.Buffer
	b.Position(pos[0], pos[1], pos[2])
	b.VarInt(action)
	b.VarInt(mode)
	b.String(name)
	for _, v := range offset {
		b.Byte(v)
	}
	for _, v := range size {
		b.Byte(v)
	}
	b.VarInt(mirror)
	b.VarInt(rotation)
	b.String(data)
	b.Float(integrity)
	b.VarLong(seed)
	b.Byte(flags)
	if e := w.WritePacket(server.IDPlaySetStructureBlockSB, b.Bytes()); e != nil {
		t.Fatal(e)
	}
}

func TestStructureBlockEditorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructureEditor")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureEditor")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			counter := 0
			barrier := func() []string {
				marker := fmt.Sprintf("STRUCTURE_DONE_%d", counter)
				counter++
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				marker := fmt.Sprintf("STRUCTURE_PASS_%d", counter)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !structureSaidBB(barrier(), marker) {
					t.Errorf("failed%s", condition)
				}
			}
			feedback := func(key string) {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range sink.take() {
						if strings.Contains(line, key) {
							return
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Errorf("missing %s", key)
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 4 -60 4 minecraft:structure_block[mode=save]")
			barrier()
			check(`if data block 4 -60 4 {name:"",author:"",metadata:"",posX:0,posY:1,posZ:0,sizeX:0,sizeY:0,sizeZ:0,rotation:"NONE",mirror:"NONE",mode:"SAVE",ignoreEntities:1b,strict:0b,powered:0b,showair:0b,showboundingbox:1b,integrity:1.0f,seed:0L}`)
			edit := func(action, mode int32, name string, offset, size [3]byte, flags byte) {
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, action, mode, name, offset, size, 2, 3, "metadata", 2, -123456789012345, flags)
			}
			edit(0, 2, "example:piece", [3]byte{127, 128, 49}, [3]byte{127, 128, 49}, 15)
			barrier()
			check(`if block 4 -60 4 minecraft:structure_block[mode=corner]`)
			check(`if data block 4 -60 4 {name:"example:piece",metadata:"metadata",posX:48,posY:-48,posZ:48,sizeX:48,sizeY:0,sizeZ:48,rotation:"COUNTERCLOCKWISE_90",mirror:"FRONT_BACK",mode:"CORNER",ignoreEntities:1b,strict:1b,showair:1b,showboundingbox:1b,integrity:1.0f,seed:-123456789012345L}`)
			sendCommand(t, pw, "gamemode survival @s")
			barrier()
			edit(0, 0, "denied", [3]byte{}, [3]byte{}, 0)
			barrier()
			check(`if data block 4 -60 4 {name:"example:piece",mode:"CORNER"}`)
			sendCommand(t, pw, "gamemode creative @s")
			barrier()
			edit(0, 0, "Bad Name", [3]byte{}, [3]byte{}, 0)
			feedback("structure_block.invalid_structure_name")
			check(`if data block 4 -60 4 {name:"",mode:"SAVE"}`)
			sendCommand(t, pw, `setblock 9 -55 10 minecraft:structure_block[mode=corner]{mode:"CORNER",name:"scan_piece"}`)
			barrier()
			edit(3, 0, "scan_piece", [3]byte{}, [3]byte{}, 1)
			feedback("structure_block.size_success")
			check(`if data block 4 -60 4 {name:"minecraft:scan_piece",posX:1,posY:1,posZ:1,sizeX:4,sizeY:4,sizeZ:5}`)
			sendCommand(t, pw, `setblock 1 -62 2 minecraft:structure_block{mode:"CORNER",name:"scan_piece"}`)
			barrier()
			edit(3, 0, "scan_piece", [3]byte{}, [3]byte{}, 1)
			feedback("structure_block.size_success")
			check(`if data block 4 -60 4 {posX:-2,posY:-1,posZ:-1,sizeX:7,sizeY:6,sizeZ:7}`)
			edit(3, 1, "scan_piece", [3]byte{}, [3]byte{}, 1)
			feedback("structure_block.size_failure")
			sendCommand(t, pw, `data merge block 4 -60 4 {name:"native",posX:100,posY:-100,sizeX:100,sizeY:-10,mode:"LOAD",integrity:2.0f}`)
			barrier()
			check(`if data block 4 -60 4 {name:"minecraft:native",posX:48,posY:-48,sizeX:48,sizeY:0,mode:"LOAD",integrity:2.0f}`)
			check(`if block 4 -60 4 minecraft:structure_block[mode=load]`)
			sendCommand(t, pw, "tp @s 2.5 -60 2.5")
			sendCommand(t, pw, "setblock 1 -61 1 minecraft:stone")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:structure_block")
			barrier()
			sendUseItemOn(t, pw, 1, -61, 1, 1, 1)
			barrier()
			check(`if data block 1 -60 1 {author:"StructureEditor"}`)

		})
	}
}
