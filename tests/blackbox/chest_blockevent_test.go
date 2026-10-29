//go:build blackbox

package blackbox

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestChestBlockEventParity(t *testing.T) {
	names := []string{"BlockEV", "BlockEM", "BlockWV", "BlockWM", "BlockTV", "BlockTM", "BlockTWV", "BlockTWM"}
	for _, i := range []int{0, 1, 4, 5} {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			off := 0
			for _, b := range []byte(candidate) {
				off = (31*off + int(b)) & 15
			}
			if off == 0 {
				names[i] = candidate
				break
			}
		}
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, kind := range []string{"chest", "trapped_chest"} {
		t.Run(kind, func(t *testing.T) {
			run := func(addr, name, watchName string) []string {
				d := newClickDriverOn(t, addr, name, "minecraft:"+kind, 2, nil, nil)
				d.close()
				command := func(view *clickDriver, text string) {
					sendCommand(t, view.pw, text)
					settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "chest-blockevent-setup")
				}
				command(d, fmt.Sprintf("setblock 9 -61 0 minecraft:%s[facing=north,type=right]", kind))
				command(d, "tick freeze")
				other := newMenuLifecycleObserver(t, addr, watchName)
				command(other, "tp @s 6 -60 2 0 0")
				wantBlock, _ := server.BlockID(kind)
				events := map[int32][]int32{8: {}, 9: {}}
				collect := func(ps []usePkt) {
					other.foldPkts(ps)
					for _, p := range ps {
						if p.id != server.LatestVersion().IDs.PlayBlockEvent {
							continue
						}
						r := protocol.NewPacketReader(p.data)
						x, y, z, e := r.Position()
						if e != nil || y != -61 || z != 0 || (x != 8 && x != 9) {
							continue
						}
						action, _ := r.Byte()
						count, _ := r.Byte()
						block, _ := r.VarInt()
						if action != 1 || block != wantBlock {
							t.Errorf("%s bad chest event action%d block%d want%d", name, action, block, wantBlock)
						}
						events[x] = append(events[x], int32(count))
					}
				}
				collect(other.sink.take())
				events[8] = nil
				events[9] = nil
				clock := func() int64 {
					sendCommand(t, other.pw, "time query gametime")
					deadline := time.Now().Add(10 * time.Second)
					for time.Now().Before(deadline) {
						ps := other.sink.take()
						collect(ps)
						for _, p := range ps {
							key, args, ok := componentArgs(fmt.Sprintf("%#x:%s", p.id, string(p.data)))
							if ok && key == "commands.time.query.gametime" && len(args) > 0 {
								if value, ok := args[0].(int64); ok {
									return value
								}
							}
						}
						time.Sleep(20 * time.Millisecond)
					}
					t.Fatal("chest event gametime missing")
					return 0
				}
				advance := func(ticks int) {
					before := clock()
					sendCommand(t, other.pw, fmt.Sprintf("tick step %d", ticks))
					deadline := time.Now().Add(30 * time.Second)
					for time.Now().Before(deadline) {
						after := clock()
						if after >= before+int64(ticks) {
							if after != before+int64(ticks) {
								t.Fatalf("chest event step advanced%d want%d", after-before, ticks)
							}
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
					t.Fatal("chest event step stalled")
				}
				var out []string
				sample := func(tag string, left, right []int32) {
					settleUse(t, other.sink, collect, "chest-event-observe")
					if !reflect.DeepEqual(events[8], left) || !reflect.DeepEqual(events[9], right) {
						t.Errorf("%s %s events=%v want left%v right%v", name, tag, events, left, right)
					}
					out = append(out, fmt.Sprintf("%s left=%v right=%v", tag, events[8], events[9]))
				}
				d.reopen(8, -61, 0, 100, "event-batch-first")
				other.reopen(9, -61, 0, 101, "event-batch-second")
				other.close()
				d.close()
				sample("frozen-batch", nil, nil)
				advance(1)
				sample("ordered-deduplicated", []int32{1, 2, 0}, []int32{1, 2, 0})
				advance(4)
				sample("closed-recheck", []int32{1, 2, 0, 0}, []int32{1, 2, 0, 0})
				d.reopen(9, -61, 0, 102, "event-count-reopen")
				advance(1)
				sample("reopened", []int32{1, 2, 0, 0, 1}, []int32{1, 2, 0, 0, 1})
				advance(4)
				sample("unchanged-open-recheck", []int32{1, 2, 0, 0, 1, 1}, []int32{1, 2, 0, 0, 1, 1})
				command(other, "gamemode spectator "+name)
				advance(5)
				sample("spectator-recount", []int32{1, 2, 0, 0, 1, 1, 0}, []int32{1, 2, 0, 0, 1, 1, 0})
				d.close()
				advance(1)
				sample("spectator-close-silent", []int32{1, 2, 0, 0, 1, 1, 0}, []int32{1, 2, 0, 0, 1, 1, 0})
				command(other, "gamemode creative "+name)
				d.reopen(8, -61, 0, 103, "event-stale-open")
				command(other, "setblock 8 -61 0 minecraft:air")
				advance(1)
				sample("removed-type-drops-pending", []int32{1, 2, 0, 0, 1, 1, 0}, []int32{1, 2, 0, 0, 1, 1, 0, 1, 0})
				command(other, "tick unfreeze")
				return out
			}
			v, g := run(tg.vanilla, names[n*4], names[n*4+2]), run(tg.mcgo, names[n*4+1], names[n*4+3])
			for i := range v {
				t.Logf("step%d vanilla=%s mcgo=%s", i, v[i], g[i])
				if v[i] != g[i] {
					t.Errorf("step%d chest block event differs", i)
				}
			}
		})
	}
}
