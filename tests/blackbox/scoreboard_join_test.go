//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func scoreboardJoinEventsBB(lines []string) []string {
	var events []string
	for _, line := range lines {
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		r := protocol.NewPacketReader([]byte(line[i+1:]))
		switch line[:i] {
		case fmt.Sprintf("0x%x", server.IDPlayObjectiveCB):
			name, _ := r.String()
			mode, _ := r.Byte()
			events = append(events, fmt.Sprintf("objective:%s:%d", name, mode))
		case fmt.Sprintf("0x%x", server.IDPlayDisplayObjective):
			slot, _ := r.VarInt()
			name, _ := r.String()
			events = append(events, fmt.Sprintf("display:%d:%s", slot, name))
		case fmt.Sprintf("0x%x", server.IDPlayUpdateScore):
			holder, _ := r.String()
			obj, _ := r.String()
			value, _ := r.VarInt()
			events = append(events, fmt.Sprintf("score:%s:%s:%d", holder, obj, value))
		case fmt.Sprintf("0x%x", server.IDPlayResetScore):
			events = append(events, "unexpected-reset")
		}
	}
	return events
}
func TestScoreboardLateJoinParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("ScoreOwner", "ScoreLate")}, nil)
	defer tg.cleanup()
	var nativeSnapshot []string
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "ScoreOwner")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				sendCommand(t, pw, cmd)
				marker := fmt.Sprintf("SCORE_JOIN_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			for _, cmd := range []string{"tick freeze", "scoreboard objectives add alpha dummy", "scoreboard objectives add beta dummy", "scoreboard objectives add hidden dummy", "scoreboard players set one alpha 7", "scoreboard players set one beta 19", "scoreboard players set one hidden 31", "scoreboard objectives modify alpha displayname \"ShownAlpha\"", "scoreboard objectives setdisplay sidebar alpha", "scoreboard objectives setdisplay below_name alpha", "scoreboard objectives setdisplay list beta"} {
				command(cmd)
			}
			late, lr, lw := joinPlayBlackbox(t, target.addr, "ScoreLate")
			defer late.Close()
			late.SetDeadline(time.Now().Add(30 * time.Second))
			var lines []string
			for {
				p, e := lr.ReadPacket()
				if e != nil {
					t.Fatal(e)
				}
				lines = append(lines, fmt.Sprintf("0x%x:%s", p.ID, p.Data))
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					if e = lw.WritePacket(server.IDPlayKeepAliveSB, p.Data); e != nil {
						t.Fatal(e)
					}
				case server.IDPlayChunkBatchDone:
					if e = ackChunkBatch(lw); e != nil {
						t.Fatal(e)
					}
				case server.IDPlaySyncPos:
					id, e := protocol.NewPacketReader(p.Data).VarInt()
					if e != nil {
						t.Fatal(e)
					}
					var b protocol.Buffer
					b.VarInt(id)
					if e = lw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); e != nil {
						t.Fatal(e)
					}
				case server.IDPlayUpdateAttributes:
					if e = lw.WritePacket(server.IDPlayPlayerLoadedSB, nil); e != nil {
						t.Fatal(e)
					}
					goto ready
				}
			}
		ready:
			late.SetDeadline(time.Time{})
			lateSink := newChatSinkTP(lr, lw)
			sendCommand(t, lw, "say SCORE_LATE_READY")
			lines = append(lines, waitForExactSayBB(t, lateSink, "SCORE_LATE_READY", 10*time.Second)...)
			want := []string{"objective:beta:0", "display:0:beta", "score:one:beta:19", "objective:alpha:0", "display:1:alpha", "display:2:alpha", "score:one:alpha:7"}
			if got := scoreboardJoinEventsBB(lines); !reflect.DeepEqual(got, want) {
				t.Errorf("snapshot got%v want%v", got, want)
			}
			var raw []string
			for _, line := range lines {
				if len(scoreboardJoinEventsBB([]string{line})) > 0 {
					raw = append(raw, line)
				}
			}
			if target.name == "vanilla" {
				nativeSnapshot = raw
			} else if !reflect.DeepEqual(raw, nativeSnapshot) {
				t.Errorf("snapshot packet contents differ: vanilla%x Go%x", nativeSnapshot, raw)
			}
			if got := scoreboardJoinEventsBB(command("say SCORE_OWNER_AFTER_JOIN")); len(got) != 0 {
				t.Errorf("snapshot rebroadcast to existing viewer: %v", got)
			}
			if got := scoreboardJoinEventsBB(command("scoreboard players set one alpha 8")); !reflect.DeepEqual(got, []string{"score:one:alpha:8"}) {
				t.Errorf("existing viewer update %v", got)
			}
			sendCommand(t, lw, "say SCORE_LATE_UPDATED")
			if got := scoreboardJoinEventsBB(waitForExactSayBB(t, lateSink, "SCORE_LATE_UPDATED", 10*time.Second)); !reflect.DeepEqual(got, []string{"score:one:alpha:8"}) {
				t.Errorf("late viewer update %v", got)
			}
			command("scoreboard players set one hidden 32")
			sendCommand(t, lw, "say SCORE_LATE_HIDDEN")
			if got := scoreboardJoinEventsBB(waitForExactSayBB(t, lateSink, "SCORE_LATE_HIDDEN", 10*time.Second)); len(got) != 0 {
				t.Errorf("hidden score sent %v", got)
			}
		})
	}
}
