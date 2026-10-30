package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// The default holder is a value, not registry index0: black is1, tabby9.
// A fresh summon provides an actual pairing receipt before the mutation.
func TestCatDefaultMetadataParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("CatFresh")}, nil)
	defer targets.cleanup()
	catType, _ := server.EntityTypeID("cat")
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "CatFresh")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			sendCommand(t, pw, `summon minecraft:cat ~ ~ ~ {Tags:["catFreshMeta"],NoAI:1b,NoGravity:1b,PersistenceRequired:1b,variant:"minecraft:black",sound_variant:"minecraft:classic"}`)
			id := int32(-1)
			initial := false
			deadline := time.Now().Add(20 * time.Second)
			spawnPrefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
			for !initial && time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.HasPrefix(line, spawnPrefix) {
						reader := protocol.NewPacketReader([]byte(line[len(spawnPrefix):]))
						eid, e1 := reader.VarInt()
						_, e2 := reader.Read(16)
						typ, e3 := reader.VarInt()
						if e1 == nil && e2 == nil && e3 == nil && typ == catType {
							id = eid
						}
					}
					if id >= 0 && strings.HasPrefix(line, "0x63:") {
						reader := protocol.NewPacketReader([]byte(line[len("0x63:"):]))
						eid, err := reader.VarInt()
						if err != nil || eid != id {
							continue
						}
						body, err := reader.Read(reader.Remaining())
						if err != nil {
							continue
						}
						entries := decodeMetaEntries(t, body)
						if _, present := entries[20]; present {
							t.Error("native-defined black holder redundantly packed")
						}
						if _, present := entries[24]; present {
							t.Error("native-defined classic sound redundantly packed")
						}
						if _, present := entries[15]; present {
							initial = true
						}
					}
				}
				if !initial {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if !initial {
				t.Fatal("fresh cat spawn/metadata receipt absent", id, sink.health())
			}
			sendCommand(t, pw, `data merge entity @e[type=minecraft:cat,tag=catFreshMeta,limit=1] {variant:"minecraft:tabby"}`)
			updated := false
			deadline = time.Now().Add(12 * time.Second)
			for !updated && time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if !strings.HasPrefix(line, "0x63:") {
						continue
					}
					reader := protocol.NewPacketReader([]byte(line[len("0x63:"):]))
					eid, err := reader.VarInt()
					if err != nil || eid != id {
						continue
					}
					body, err := reader.Read(reader.Remaining())
					if err != nil {
						continue
					}
					entry, present := decodeMetaEntries(t, body)[20]
					if present && entry.typ == 21 && len(entry.val) == 1 && entry.val[0] == 9 {
						updated = true
					}
				}
				if !updated {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if !updated {
				t.Fatal("paired fresh cat black→tabby holder update absent")
			}
		})
	}
}
