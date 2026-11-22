package blackbox

// TestSustainedTickRateParity: the game loop's pacing under a
// vanilla-like movement load. Vanilla anchors its tick loop on one
// absolute grid — MinecraftServer.runServer arms nextTickTimeNanos
// before the loop (offset 11), advances it by exactly one
// nanosPerTick per iteration (offsets 236-243) and parks until that
// absolute target — so unmeasured iteration work (packet handling,
// timer overshoot) is refunded by the next wait. mc-go's loop used to
// refund only the last tick's measured duration (wait = period -
// since), and everything that measurement missed — the player tick
// riding the same iteration, ownership acquisition, timer overshoot
// — accumulated forever: measured pre-fix on this host with four
// scripted movers it sustained 18.48 TPS while its ticks cost 11.5ms
// of the 50ms budget, where vanilla held 19.98; with eight movers
// 17.45 against 20.01.
//
// The pin drives the same offered load at both servers — scripted
// movers sending real-client 20 MovePosRot packets per second each,
// close enough to stay in every viewer's tracking range — and
// requires each server independently to hold the tick target over
// the window: game time advances at >= 19.5 ticks per second of wall
// time. It also witnesses the load itself (the probe receives entity
// movement broadcasts from the movers) and that every mover
// connection survived the window. The sustained-rate claim is
// bounded to this fixture: flat world, distance 8, four movers at
// client cadence — it establishes no tail-latency or capacity
// envelope beyond it.

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// sustainedWindow is the measurement window: 20 seconds = 400 target
// ticks, enough to average scheduler noise on the shared host while
// keeping the pin under a minute of load past the joins.
const sustainedWindow = 20 * time.Second

// sustainedFloor is the tick-rate floor both servers must hold on
// their own leg: half a tick per second of slack (2.5%) over the 20
// TPS target for timer and scheduler noise. Vanilla pins 20.0 on this
// fixture; pre-fix mc-go sat at 18.5.
const sustainedFloor = 19.5

// sustainedMovers is the offered load: four movers at client cadence
// forced 80 accepted movement packets per second through the loop's
// pacing (pre-fix loss measured 1.5 ticks/s at this level).
const sustainedMovers = 4

// sustainedArgString renders one translatable-component argument the
// way the /tick query mspt numbers arrive: an NBT number or a literal
// text component.
func sustainedArgString(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	case int32:
		return fmt.Sprintf("%d", v)
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			return t
		}
	}
	return fmt.Sprintf("%v", a)
}

// sustainedClock sends /time query gametime and waits for the receipt.
func sustainedClock(t *testing.T, pw *protocol.Writer, sink *chatSink) int64 {
	t.Helper()
	sendCommand(t, pw, "time query gametime")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range sink.take() {
			key, args, ok := componentArgs(line)
			if ok && key == "commands.time.query.gametime" && len(args) == 1 {
				if v, ok := args[0].(int64); ok {
					return v
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("gametime receipt missing (%s)", sink.health())
	return 0
}

// sustainedTickQuery sends /tick query and waits for both reply lines,
// returning the average-mspt and percentile texts for the log.
func sustainedTickQuery(t *testing.T, pw *protocol.Writer, sink *chatSink) (avg, p50, p95 string) {
	t.Helper()
	sendCommand(t, pw, "tick query")
	var avgFound, pctFound bool
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && (!avgFound || !pctFound) {
		for _, line := range sink.take() {
			key, args, ok := componentArgs(line)
			if !ok {
				continue
			}
			switch key {
			case "commands.tick.query.rate.running":
				if len(args) >= 2 {
					avg, avgFound = sustainedArgString(args[1]), true
				}
			case "commands.tick.query.percentiles":
				if len(args) >= 4 {
					p50, p95, pctFound = sustainedArgString(args[0]), sustainedArgString(args[1]), true
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !avgFound || !pctFound {
		t.Fatalf("tick query receipts missing (avg=%v pct=%v) (%s)", avgFound, pctFound, sink.health())
	}
	return avg, p50, p95
}

func TestSustainedTickRateParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("RateProbe")}, nil)
	defer tg.cleanup()
	for _, leg := range []struct{ tag, addr string }{{"mc-go", tg.mcgo}, {"vanilla", tg.vanilla}} {
		sustainedRateLeg(t, leg.tag, leg.addr)
	}
}

func sustainedRateLeg(t *testing.T, tag, addr string) {
	_, ppr, ppw := joinPlayBlackbox(t, addr, "RateProbe")
	probe := newChatSink(ppr, ppw)

	// Movers walk a Lissajous around the spawn at client cadence.
	// The 4s/6s periods keep per-packet displacement above the
	// movement-broadcast threshold, so the load is observable on the
	// probe's wire, not just processed.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writeErrs atomic.Int32
	movers := make([]*chatSink, sustainedMovers)
	for i := 0; i < sustainedMovers; i++ {
		_, pr, pw := joinPlayBlackbox(t, addr, fmt.Sprintf("RateMover%02d", i))
		var mu sync.Mutex
		// TP-confirming sink: it answers keep-alives and chunk batches
		// and confirms the join teleport — without the confirmation a
		// server never accepts the scripted movement.
		movers[i] = newChatSinkWriteGuard(pr, pw, true, &mu)
		wg.Add(1)
		go func(i int, pw *protocol.Writer, mu *sync.Mutex) {
			defer wg.Done()
			start := time.Now()
			for {
				select {
				case <-stop:
					return
				default:
				}
				el := time.Since(start).Seconds()
				x := 0.5 + 2.5*math.Sin(2*math.Pi*el/4+float64(i))
				z := 0.5 + 2.5*math.Sin(2*math.Pi*el/6+float64(i)*1.7)
				mu.Lock()
				err := sendMovePosRot(pw, x, -60, z)
				mu.Unlock()
				if err != nil {
					writeErrs.Add(1)
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(i, pw, &mu)
	}
	time.Sleep(2 * time.Second)

	g0 := sustainedClock(t, ppw, probe)
	t0 := time.Now()
	time.Sleep(sustainedWindow)
	g1 := sustainedClock(t, ppw, probe)
	dt := time.Since(t0).Seconds()
	avg, p50, p95 := sustainedTickQuery(t, ppw, probe)

	// The load must have been real: the probe saw the movers' tracked
	// movement broadcasts during the window. mc-go broadcasts a moving
	// player as entity_position_sync absolutes (0x23, the recorded
	// moving-player tracking divergence) while vanilla rides
	// move_entity_pos/_rot deltas, so all three broadcast ids count.
	ids := server.LatestVersion().IDs
	probe.mu.Lock()
	moveSeen := probe.ids[ids.PlayMoveEntityPos] + probe.ids[ids.PlayMoveEntityPosRot] + probe.ids[server.IDPlayEntityTeleportCB]
	probeErr := probe.err
	probe.mu.Unlock()
	if moveSeen < 50 {
		t.Errorf("%s: load was not observable — probe saw %d entity movement broadcasts (want >= 50) (%s)", tag, moveSeen, probe.health())
	}
	if probeErr != nil {
		t.Errorf("%s: probe connection failed mid-window: %v", tag, probeErr)
	}
	for i, m := range movers {
		m.mu.Lock()
		if m.err != nil {
			t.Errorf("%s: mover %d connection failed mid-window: %v", tag, i, m.err)
		}
		m.mu.Unlock()
	}
	close(stop)
	wg.Wait()
	if n := writeErrs.Load(); n > 0 {
		t.Errorf("%s: %d mover(s) stopped sending mid-window", tag, n)
	}

	tps := float64(g1-g0) / dt
	t.Logf("%s sustained=%.3f TPS (gametime +%d over %.2fs) movers=%d avg_mspt=%s p50=%s p95=%s",
		tag, tps, g1-g0, dt, sustainedMovers, avg, p50, p95)
	if tps < sustainedFloor {
		t.Errorf("%s: sustained %.3f TPS under %d movers over %.0fs, floor is %.1f (gametime +%d over %.2fs, avg mspt %s) — the loop lost %.2f ticks/s to pacing",
			tag, tps, sustainedMovers, sustainedWindow.Seconds(), sustainedFloor, g1-g0, dt, avg, 20-tps)
	}
	if !strings.Contains(probe.health(), "err=<nil>") {
		t.Errorf("%s: probe stream unhealthy: %s", tag, probe.health())
	}
}
