package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestZZBeeProbe(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("Var")}, nil)
	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		_, pr, pw := joinPlayBlackbox(t, addr, "Var")
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "gamemode spectator Var")
		sendCommand(t, pw, "tp @s 0 -60 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()
		summons := []string{
			`{HasNectar:1b,NoAI:1b}`,
			`{HasStung:1b,NoAI:1b}`,
			`{anger_end_time:600L,NoAI:1b}`,
			`{CannotEnterHiveTicks:99,TicksSincePollination:5,CropsGrownSincePollination:7,NoAI:1b}`,
			`{hive_pos:[I;1,2,3],flower_pos:[I;4,5,6],NoAI:1b}`,
		}
		for i, nbt := range summons {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:bee %d -60 3 %s", i*2, nbt))
			time.Sleep(120 * time.Millisecond)
		}
		time.Sleep(1200 * time.Millisecond)
		seen := map[int]bool{}
		for _, l := range sink.take() {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x63" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			eid, _ := r.VarInt()
			if eid < 2 || eid > 6 || seen[int(eid)] {
				continue
			}
			seen[int(eid)] = true
			rest, _ := r.Read(r.Remaining())
			t.Logf("[%s] eid=%d metas=%v", addr, eid, decodeMetaEntries(t, rest))
		}

	}
}
