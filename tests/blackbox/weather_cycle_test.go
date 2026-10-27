package blackbox

import (
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestWeatherCycleParity pins ServerLevel.advanceWeatherCycle and the
// weather half of PlayerList.sendLevelInfo on the wire. Every game
// event of types 1/2/7/8 (start/stop raining, rain/thunder level) is
// compared in order with its exact float bits, so the float32 0.01
// easing, the 0.2 start/stop threshold and the per-tick level packets
// all have to match:
//
//   - /weather clear 10t: the clear timer holds both flags off, then
//     its expiry arms both timers and rain and thunder start together.
//   - /weather rain 30 (a bare TimeArgument: ticks): rain on, thunder
//     off, and both timers share the 30 ticks — when they run out the
//     rain stops and the thunder starts in the same tick.
//   - advance_weather false + /weather rain 5t: nothing counts down, so
//     the rain never ends.
//   - a player joining into steady rain gets start raining plus both
//     levels (thunder scaled by rain) in the join burst, before the
//     chunk-wait event, and nothing after.
func TestWeatherCycleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("WX", "WY")}, nil)
	defer targets.cleanup()

	type result struct {
		phases []string
		join   string
	}
	run := func(label, addr string) result {
		var res result
		c, pr, pw := joinPlayBlackbox(t, addr, "WX")
		defer c.Close()
		sendClientInformation(t, pw)
		time.Sleep(400 * time.Millisecond)
		drainChunkWindow(t, label, c, pr, pw, 300*time.Millisecond)
		// Calm start: no rain, levels at zero, cycle running.
		for _, cmd := range []string{"gamerule advance_weather false", "weather clear", "gamerule advance_weather true"} {
			sendCommand(t, pw, cmd)
		}
		weatherEvents(c, pr, pw, 6*time.Second)
		for _, phase := range []struct {
			cmds []string
			wait time.Duration
		}{
			{[]string{"weather clear 10t"}, 9 * time.Second},
			{[]string{"weather rain 30"}, 9 * time.Second},
			{[]string{"gamerule advance_weather false", "weather rain 5t"}, 7 * time.Second},
		} {
			for _, cmd := range phase.cmds {
				sendCommand(t, pw, cmd)
			}
			res.phases = append(res.phases, strings.Join(weatherEvents(c, pr, pw, phase.wait), " "))
		}
		// A second player joins into the steady rain.
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "WY")
		defer c2.Close()
		res.join = strings.Join(weatherEvents(c2, pr2, pw2, 2*time.Second), " ")
		return res
	}

	want := run("vanilla", targets.vanilla)
	got := run("mcgo", targets.mcgo)
	for i := range want.phases {
		if want.phases[i] == "" {
			t.Fatalf("vanilla phase %d sent no weather events", i)
		}
		if got.phases[i] != want.phases[i] {
			w, g := strings.Fields(want.phases[i]), strings.Fields(got.phases[i])
			at := 0
			for at < len(w) && at < len(g) && w[at] == g[at] {
				at++
			}
			t.Errorf("phase %d diverges at event %d of %d/%d:\n  vanilla %v\n  mc-go   %v", i, at, len(w), len(g), w[at:min(at+8, len(w))], g[at:min(at+8, len(g))])
		}
	}
	if want.join != "1:0 7:1 8:0 13:0" {
		t.Errorf("vanilla join burst weather %q", want.join)
	}
	if got.join != want.join {
		t.Errorf("join burst:\n  vanilla %s\n  mc-go   %s", want.join, got.join)
	}
}

// weatherEvents reads traffic for d, answering keep-alives and chunk
// batches, and returns every weather-relevant game event as
// "type:value" with the float printed exactly (and the chunk-wait
// event 13, which bounds the join burst's weather).
func weatherEvents(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) []string {
	var out []string
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlayGameEvent:
			r := protocol.NewPacketReader(p.Data)
			ev, err1 := r.Byte()
			v, err2 := r.Float()
			if err1 != nil || err2 != nil {
				continue
			}
			switch ev {
			case 1, 2, 7, 8, 13:
				out = append(out, fmt.Sprintf("%d:%s", ev, exactFloat(v)))
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	return out
}

// exactFloat prints a float32 so that different bit patterns never
// print alike.
func exactFloat(v float32) string {
	s := fmt.Sprintf("%g", v)
	if float32(mustParse(s)) != v {
		return fmt.Sprintf("%#x", math.Float32bits(v))
	}
	return s
}

func mustParse(s string) float64 {
	var f float64
	_, _ = fmt.Sscan(s, &f)
	return f
}
