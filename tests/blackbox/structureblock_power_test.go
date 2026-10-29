package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestStructureBlockPowerParity(t *testing.T) {
	mcDir := t.TempDir()
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructurePower")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr, dir string }{{"vanilla", tg.vanilla, filepath.Join(vanillaDirForDebug, "world")}, {"mc-go", tg.mcgo, mcDir}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructurePower")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			counter := 0
			barrier := func() []string {
				counter++
				marker := fmt.Sprintf("STRUCTURE_POWER_END_%d", counter)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				counter++
				marker := fmt.Sprintf("STRUCTURE_POWER_OK_%d", counter)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !structureSaidBB(barrier(), marker) {
					t.Errorf("failed%s", condition)
					sendCommand(t, pw, "data get block 4 -60 4")
					for _, line := range barrier() {
						if strings.Contains(line, "data.block.query") {
							t.Logf("actual%q", line)
						}
					}
				}
			}
			edit := func(mode int32, size byte) {
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 0, mode, "test:power_memory", [3]byte{0, 1, 0}, [3]byte{size, 1, 1}, 0, 0, "", 1, 17, 1)
				// A chat marker does not prove a preceding editor packet has
				// reached its tick-end update. Observe the actual applied tag.
				wantMode := []string{"SAVE", "LOAD", "CORNER", "DATA"}[mode]
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					for _, u := range structureBEUpdatesBB(t, sink.take()) {
						if u.pos == [3]int32{4, -60, 4} && u.tag["mode"] == wantMode && u.tag["name"] == "test:power_memory" && u.tag["sizeX"] == int32(size) {
							return
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("no applied structure edit receipt mode%s size%d", wantMode, size)
			}
			pulse := func() {
				sendCommand(t, pw, "setblock 5 -60 4 air")
				barrier()
				sendCommand(t, pw, "setblock 5 -60 4 redstone_block")
				barrier()
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 5 -60 4 air")
			sendCommand(t, pw, "setblock 4 -59 4 stone")
			sendCommand(t, pw, "setblock 4 -60 4 structure_block[mode=save]")
			// Establish creation before sending a different packet family:
			// an editor arriving first would be ignored for lack of a BE.
			created := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) && !created {
				for _, u := range structureBEUpdatesBB(t, sink.take()) {
					created = created || u.pos == [3]int32{4, -60, 4} && u.tag["mode"] == "SAVE"
				}
				if !created {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !created {
				t.Fatal("no controller creation receipt")
			}
			edit(0, 1)
			check(`if block 4 -60 4 minecraft:structure_block{mode:"SAVE",name:"test:power_memory",sizeX:1}`)
			pulse()
			check("if block 4 -60 4 minecraft:structure_block{powered:1b}")
			if _, err := os.Stat(filepath.Join(tc.dir, "generated/test/structure/power_memory.nbt")); !os.IsNotExist(err) {
				t.Errorf("redstone SAVE exported native file: %v", err)
			}
			sendCommand(t, pw, "setblock 5 -60 4 air")
			barrier()
			check("if block 4 -60 4 minecraft:structure_block{powered:0b}")
			sendCommand(t, pw, "setblock 4 -59 4 dirt")
			edit(1, 0)
			pulse()
			check("if block 4 -59 4 stone")
			check("if block 4 -60 4 minecraft:structure_block{sizeX:1,sizeY:1,sizeZ:1}")
			sendCommand(t, pw, "setblock 4 -59 4 dirt")
			sendCommand(t, pw, "setblock 4 -60 5 stone")
			barrier()
			check("if block 4 -59 4 dirt")
			pulse()
			check("if block 4 -59 4 stone")
			sendCommand(t, pw, "setblock 5 -60 4 air")
			barrier()
			edit(3, 1)
			sendCommand(t, pw, "setblock 4 -59 4 dirt")
			pulse()
			check("if block 4 -59 4 dirt")
			sendCommand(t, pw, "setblock 5 -60 4 air")
			barrier()
			edit(2, 1)
			pulse() // CORNER unloads the memory-only template.
			sendCommand(t, pw, "setblock 5 -60 4 air")
			barrier()
			edit(1, 1)
			pulse()
			check("if block 4 -59 4 dirt")
		})
	}
}
