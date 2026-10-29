//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func TestChestLidCallbacksParity(t *testing.T) {
	names := []string{"LidActorV", "LidActorM", "LidWatchV", "LidWatchM"}
	for _, i := range []int{0, 1} {
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
	run := func(addr, name, watchName string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, nil, nil)
		d.close()
		command := func(view *clickDriver, text string) {
			sendCommand(t, view.pw, text)
			settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "lid-setup")
		}
		command(d, "setblock 9 -61 0 minecraft:chest[facing=north,type=right]")
		command(d, "tick freeze")
		other := newMenuLifecycleObserver(t, addr, watchName)
		command(other, "tp @s 6 -60 2 0 0")
		d.soundIDs = nil
		other.soundIDs = nil
		d.sink.take()
		other.sink.take()
		var events []string
		var out []string
		sample := func(tag string) {
			settleUse(t, other.sink, func(ps []usePkt) {
				other.foldPkts(ps)
				for _, p := range ps {
					if p.id != 0x75 {
						continue
					}
					r := protocol.NewPacketReader(p.data)
					id, e := r.VarInt()
					if e != nil || id == 0 {
						continue
					}
					src, _ := r.VarInt()
					x, _ := r.Int()
					y, _ := r.Int()
					z, _ := r.Int()
					vol, _ := r.Float()
					pitch, _ := r.Float()
					if src != 4 || x != 72 || y != -484 || z != 4 || vol != 0.5 || pitch < 0.9 || pitch >= 1 {
						t.Errorf("%s invalid lid sound id%d source%d at%d,%d,%d volume%g pitch%g", name, id, src, x, y, z, vol, pitch)
					}
					events = append(events, fmt.Sprintf("%d/%d@%d,%d,%d volume=%g pitchOk=%v", id, src, x, y, z, vol, pitch >= 0.9 && pitch < 1.0))
				}
			}, "lid-callback")
			wantOpen := []int{1, 1, 1, 1, 1, 1, 2, 2}[len(out)]
			wantClose := []int{0, 0, 0, 1, 1, 1, 1, 2}[len(out)]
			if other.soundIDs[350] != wantOpen || other.soundIDs[348] != wantClose || len(events) != wantOpen+wantClose {
				t.Errorf("%s %s lid callbacks counts=%v events=%d wantOpen%d wantClose%d", name, tag, other.soundIDs, len(events), wantOpen, wantClose)
			}
			out = append(out, fmt.Sprintf("%s counts=%v events=%v", tag, other.soundIDs, events))
		}
		d.reopen(8, -61, 0, 60, "lid-first")
		sample("first")
		other.reopen(9, -61, 0, 61, "lid-second")
		sample("second")
		other.close()
		sample("first-close")
		d.close()
		sample("last-close")
		command(other, "gamemode spectator @s")
		other.reopen(9, -61, 0, 62, "lid-spectator")
		sample("spectator-open")
		other.close()
		sample("spectator-close")
		d.reopen(9, -61, 0, 63, "lid-reopen")
		sample("reopen")
		sendCommand(t, other.pw, "kick "+name)
		sample("disconnect")
		command(other, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0], names[2]), run(tg.mcgo, names[1], names[3])
	for i := range v {
		t.Logf("step%d vanilla=%s mcgo=%s", i, v[i], g[i])
		if !reflect.DeepEqual(v[i], g[i]) {
			t.Errorf("step%d chest lid callbacks differ", i)
		}
	}
}

func TestChestFirstLastGameEventsParity(t *testing.T) {
	names := []string{"EventActorV", "EventActorM", "EventWatchV", "EventWatchM"}
	for _, i := range []int{0, 1} {
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
	run := func(addr, name, watchName string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, nil, nil)
		d.close()
		command := func(view *clickDriver, text string) {
			sendCommand(t, view.pw, text)
			settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "chest-event-setup")
		}
		command(d, "setblock 9 -61 0 minecraft:chest[facing=north,type=right]")
		command(d, "tick freeze")
		other := newMenuLifecycleObserver(t, addr, watchName)
		command(other, "tp @s 6 -60 2 0 0")
		command(d, "setblock 7 -61 0 minecraft:sculk_sensor")
		command(d, "setblock 10 -61 0 minecraft:sculk_sensor")
		advance := func(n int) {
			d.sink.take()
			sendCommand(t, d.pw, fmt.Sprintf("tick sprint %d", n))
			deadline := time.Now().Add(45 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				ps := d.sink.take()
				d.foldPkts(ps)
				for _, p := range ps {
					if bytes.Contains(p.data, []byte("tick.sprint.report")) || bytes.Contains(p.data, []byte("Sprint completed")) {
						done = true
					}
				}
				if done {
					return
				}
				time.Sleep(40 * time.Millisecond)
			}
			t.Fatal("chest event sprint did not complete")
		}
		query := 0
		var out []string
		sample := func(tag, phase string, power int) {
			var answers []bool
			for _, x := range []int{7, 10} {
				yes := fmt.Sprintf("CHEVENT_Y_%d", query)
				no := fmt.Sprintf("CHEVENT_N_%d", query)
				query++
				condition := fmt.Sprintf("block %d -61 0 minecraft:sculk_sensor[sculk_sensor_phase=%s,power=%d]", x, phase, power)
				sendCommand(t, d.pw, "execute if "+condition+" run say "+yes)
				sendCommand(t, d.pw, "execute unless "+condition+" run say "+no)
				got := false
				settleUse(t, d.sink, func(ps []usePkt) {
					d.foldPkts(ps)
					for _, p := range ps {
						if bytes.Contains(p.data, []byte(yes)) {
							got = true
						}
					}
				}, "chest-event-sensor")
				answers = append(answers, got)
				if !got {
					t.Errorf("%s %s sensor%d wanted %s power%d", name, tag, x, phase, power)
				}
			}
			out = append(out, fmt.Sprintf("%s %v", tag, answers))
		}
		d.reopen(8, -61, 0, 70, "event-first")
		advance(4)
		sample("first-both-halves", "active", 14)
		advance(60)
		other.reopen(9, -61, 0, 71, "event-second")
		advance(4)
		sample("second-is-silent", "inactive", 0)
		other.close()
		advance(4)
		sample("nonlast-close-silent", "inactive", 0)
		d.close()
		advance(4)
		sample("last-close-both-halves", "active", 14)
		advance(60)
		command(other, "gamemode spectator @s")
		other.reopen(9, -61, 0, 72, "event-spectator")
		advance(4)
		sample("spectator-silent", "inactive", 0)
		other.close()
		d.reopen(9, -61, 0, 73, "event-right-first")
		advance(4)
		sample("opposite-half-first", "active", 14)
		d.close()
		command(other, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0], names[2]), run(tg.mcgo, names[1], names[3])
	t.Logf("vanilla=%v mcgo=%v", v, g)
	if !reflect.DeepEqual(v, g) {
		t.Error("first/last chest gameevents differ")
	}
}
