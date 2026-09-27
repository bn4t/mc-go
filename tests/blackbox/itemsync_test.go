// itemsync_test.go pins ItemEntity.tick's intra-tick needsSync: a
// thrown/landing item whose net delta change exceeds 0.1 emits
// immediately — bypassing the updateInterval-20 cadence — measured as
// the emit step-set over sprint-1 tick steps on both servers.
package blackbox

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// packetEvent is one interesting packet off the connection: id is the
// packet id (or -typeVarint for add_entity, with eid carried), eid the
// leading VarInt entity id, and data the raw body for text packets.
type packetEvent struct {
	id   int32
	eid  int32
	data []byte
}

// startPacketPump owns the connection reader and forwards every
// move/motion/metadata emit plus entity spawns onto the channel, so
// foreground test code never races a read or desyncs on deadlines.
func startPacketPump(pr *protocol.Reader, pw *protocol.Writer) chan packetEvent {
	ch := make(chan packetEvent, 4096)
	go func() {
		seen := map[int32]int{}
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				fmt.Fprintf(os.Stderr, "pump died: %v ids=%v\n", err, seen)
				close(ch)
				return
			}
			seen[p.ID]++
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				eid, e1 := r.VarInt()
				_, _ = r.Read(16)
				typ, e2 := r.VarInt()
				if e1 == nil && e2 == nil {
					ch <- packetEvent{id: -typ, eid: eid}
				}
			case server.IDPlayEntityUpdatePos, 0x36, server.IDPlayEntityMotion, 0x63:
				// move_entity_pos / move_entity_pos_rot / set_entity_motion /
				// set_entity_data: VarInt eid first.
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					ch <- packetEvent{id: p.ID, eid: id}
				}
			case 0x79:
				// system_chat — command feedback carries SNBT dumps.
				ch <- packetEvent{id: p.ID, data: p.Data}
			}
		}
	}()
	return ch
}

// drainEmits collects emit packets for `window`, keyed by packet id.
// The test world is empty of movers besides the item under test, so no
// entity-id filter is needed. It also returns any system_chat bodies
// seen (for /data get verification).
func drainEmits(ch chan packetEvent, window time.Duration) (map[int32]int, [][]byte) {
	emits := map[int32]int{}
	var chats [][]byte
	timer := time.After(window)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return emits, chats
			}
			if ev.id == 0x79 {
				chats = append(chats, ev.data)
			} else if ev.id >= 0 {
				emits[ev.id]++
			}
		case <-timer:
			return emits, chats
		}
	}
}

// summonItemRetry sends the summon command and verifies the item
// exists via /data get, retrying when vanilla drops it silently.
func summonItemRetry(t *testing.T, pw *protocol.Writer, ch chan packetEvent, cmd string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		sendCommand(t, pw, cmd)
		sendCommand(t, pw, "data get entity @e[type=item,limit=1]")
		_, chats := drainEmits(ch, 1200*time.Millisecond)
		for _, c := range chats {
			if strings.Contains(string(c), "minecraft:item") {
				return
			}
		}
		t.Logf("summon verify attempt %d: no item, retrying", i)
	}
	t.Fatal("item summon never produced a data-get dump")
}

func TestItemNeedsSyncParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("IS")}, nil)
	defer targets.cleanup()

	steps := map[string][]int{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			ca, pra, pwA, _ := joinPlayEid(t, s.addr, "IS")
			defer ca.Close()
			pump := startPacketPump(pra, pwA)
			sendCommand(t, pwA, "tp IS 0.5 -59 0.5")
			sendCommand(t, pwA, "setblock 30 -60 10 minecraft:stone")
			summonItemRetry(t, pwA, pump, `summon minecraft:item 30.5 -58.75 10.5 {Item:{id:"minecraft:stone",count:1},PickupDelay:32767s}`)
			// Let the item land and settle, then re-toss it live with a
			// data merge: the arc plus landing re-creates the intra-tick
			// needsSync signature. Watch 2s in 100ms buckets.
			time.Sleep(3 * time.Second)
			_, _ = drainEmits(pump, 200*time.Millisecond)
			sendCommand(t, pwA, `data merge entity @e[type=item,limit=1] {Motion:[0.32d,0.30d,-0.14d],OnGround:0b}`)
			buckets := []map[int32]int{}
			for i := 0; i < 20; i++ {
				b, _ := drainEmits(pump, 100*time.Millisecond)
				buckets = append(buckets, b)
			}
			emits := []int{}
			total := map[int32]int{}
			for i, b := range buckets {
				if len(b) > 0 {
					emits = append(emits, i)
					t.Logf("%s step %d: %v", s.label, i, b)
					for id, n := range b {
						total[id] += n
					}
				}
			}
			t.Logf("%s emit steps: %v total %v", s.label, emits, total)
			steps[s.label] = emits
		})
	}

	// The emit step-sets should carry the same signature: an interval
	// emit every ~10 steps plus the off-cadence needsSync emits at the
	// landing and each 4-tick clamp. Compare the sets with ±1 step of
	// window bleed.
	v, m := steps["vanilla"], steps["mcgo"]
	t.Logf("emit steps: vanilla=%v mcgo=%v", v, m)
	if len(v) != len(m) {
		t.Errorf("emit step count: vanilla=%v mcgo=%v", v, m)
		return
	}
	for i := range v {
		if d := v[i] - m[i]; d < -1 || d > 1 {
			t.Errorf("emit steps: vanilla=%v mcgo=%v", v, m)
			return
		}
	}
}
