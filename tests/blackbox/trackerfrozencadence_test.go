package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A frozen world keeps running the tracker pass. Vanilla's ServerLevel.tick
// runs ServerChunkCache.tick even when the tick rate manager freezes the
// level, so every tracked entity's ServerEntity.sendChanges keeps firing
// once per server tick: its own tickCount gate opens every updateInterval,
// the tickCount%60 forced-position branch emits a zero-delta periodic while
// the body stays still, and the per-gate teleportDelay promotes to one
// absolute entity_position_sync after 400 gate-open calls. mc-go ran no
// tracker pass at all while frozen. This fixture pins the whole cadence
// through two stationary bodies in one frozen session: a NoAI camel
// (updateInterval 3 — pairing response at tick 0, periodics every 60
// ticks, exactly one absolute at tick 1200) and an item (updateInterval 20
// — the non-living tracker, same zero-delta periodic grid, but the
// teleportDelay needs 400 gate-open calls = tick 8000, so the window holds
// no absolute at all and the camel's absolute tick is a plain periodic
// here). Cadence is normalized by each leg's own measured frame period;
// the pairing tick's own work is inflated by the summon and the
// post-teleport chunk stream, so the grid's constant offset from the
// pairing response is bounded (measured −1.03 and −0.77 frames across two
// vanilla runs, +0.4..+1.1 on mc-go's rate-managed loop) rather than
// pinned, while the grid's own spacing — consecutive events exactly 60
// frames apart — is exact.
func TestStationaryFrozenTrackerCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t,
		map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor("FT_Observer")},
		nil)
	defer targets.cleanup()

	const (
		camelX, camelY, camelZ = 2.5, 162.0, 0.5
		itemX, itemY, itemZ    = -2.5, 162.0, 0.5
		// The window must cover the camel's absolute at tick 1200 (60s) plus
		// the resumed periodic at 1260, and reach the next one at 1320 —
		// vanilla 66s, mc-go ~70s at its measured ~52.5ms frame — so it
		// is scaled per leg from the measured frame period, capped for
		// safety. Slots past 22 are extra periodics, not failures.
		captureFloor = 20 * time.Second
		captureCap   = 80 * time.Second
		capturePad   = 3500 * time.Millisecond
	)

	// Each leg captures both bodies' tracker grids inside one window.
	type legData struct {
		camel, item []ftEvent
		window      time.Duration
	}

	run := func(addr string) legData {
		c, pr, pw, _ := joinPlayEid(t, addr, "FT_Observer")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)

		sink := newFtCadenceSink(pr, pw)
		camelType := mustEType(t, "camel")
		itemType := mustEType(t, "item")
		seq := 0
		cmd := func(s string) []string {
			seq++
			tag := fmt.Sprintf("FT_RECEIPT%d", seq)
			sendCommand(t, pw, s)
			sendCommand(t, pw, "say "+tag)
			return ftWaitSay(t, sink, tag)
		}

		cmd("tick freeze")
		cmd("tp @s 0.5 162 -4.5")
		cmd(fmt.Sprintf("summon camel %g %g %g {NoAI:1b,NoGravity:1b,Invulnerable:1b,Silent:1b,PersistenceRequired:1b,Age:10000,Tags:[FTCamel]}", camelX, camelY, camelZ))
		cmd(fmt.Sprintf("summon item %g %g %g {Tags:[FTItem],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:\"minecraft:stone\",count:1b}}", itemX, itemY, itemZ))

		// Latch both entity ids from the spawn packets the summons
		// produced; every later pos-family packet names those ids.
		var camel, item int32
		spawnTypes := map[int32]int32{}
		deadline := time.Now().Add(10 * time.Second)
		for (camel == 0 || item == 0) && time.Now().Before(deadline) {
			for _, ev := range sink.snapshot() {
				if ev.kind != "spawn" {
					continue
				}
				if _, done := spawnTypes[ev.id]; done {
					continue
				}
				spawnTypes[ev.id] = -1
				r := protocol.NewPacketReader(ev.raw)
				id, a := r.VarInt()
				_, b := r.UUID()
				typ, d := r.VarInt()
				if a == nil && b == nil && d == nil {
					spawnTypes[id] = typ
					switch typ {
					case camelType:
						camel = id
					case itemType:
						item = id
					}
				}
			}
			if camel == 0 || item == 0 {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if camel == 0 {
			t.Fatalf("%s camel spawn never arrived; sink %s", addr, sink.health())
		}
		if item == 0 {
			t.Fatalf("%s item spawn never arrived; sink %s", addr, sink.health())
		}

		// The pairing response: sendChanges call 0 rides the tick-0
		// forced position and answers the attach burst with a
		// zero-delta move_entity_pos.
		deadline = time.Now().Add(10 * time.Second)
		waitPair := func(id int32, what string) (time.Time, string, []byte) {
			for {
				for _, ev := range sink.snapshot() {
					if ev.id == id && ev.kind != "spawn" {
						return ev.at, ev.kind, ev.raw
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s no %s pairing response within 10s of summon; sink %s", addr, what, sink.health())
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		pairTime, pairKind, pairRaw := waitPair(camel, "camel")
		if pairKind != "movepos" || !ftZeroDelta(pairRaw) {
			t.Fatalf("%s camel pairing response was %s, want zero-delta move_entity_pos", addr, pairKind)
		}
		spawnAt := sink.spawnTime(camel)
		if !spawnAt.IsZero() && pairTime.Sub(spawnAt) > 350*time.Millisecond {
			t.Errorf("%s camel pairing response %v after spawn, want within ~3 frames", addr, pairTime.Sub(spawnAt))
		}
		itemPairTime, itemPairKind, itemPairRaw := waitPair(item, "item")
		if itemPairKind != "movepos" || !ftZeroDelta(itemPairRaw) {
			t.Fatalf("%s item pairing response was %s, want zero-delta move_entity_pos", addr, itemPairKind)
		}
		itemSpawnAt := sink.spawnTime(item)
		if !itemSpawnAt.IsZero() && itemPairTime.Sub(itemSpawnAt) > 350*time.Millisecond {
			t.Errorf("%s item pairing response %v after spawn, want within ~3 frames", addr, itemPairTime.Sub(itemSpawnAt))
		}

		// Passive capture: the sink keeps the session alive while the
		// frozen tracker emits the camel's cadence. The window is
		// scaled by the leg's own measured frame period, measured from
		// the first seconds of periodics, so a slow rate-managed loop
		// still captures the resumed periodic at multiple 22.
		time.Sleep(captureFloor)
		period := ftFramePeriod(ftTrackerEvents(sink, camel, pairTime, captureFloor))
		window := time.Duration(22*60)*period + capturePad
		if window > captureCap {
			window = captureCap
		}
		time.Sleep(window - captureFloor)

		events := ftTrackerEvents(sink, camel, pairTime, window)
		if len(events) == 0 || events[0].at != pairTime {
			t.Fatalf("%s camel event capture lost the pairing response (%d events)", addr, len(events))
		}
		itemEvents := ftTrackerEvents(sink, item, itemPairTime, window)
		if len(itemEvents) == 0 || itemEvents[0].at != itemPairTime {
			t.Fatalf("%s item event capture lost the pairing response (%d events)", addr, len(itemEvents))
		}
		period = ftFramePeriod(events)
		t.Logf("%s camel %d spawn→pair %v item %d spawn→pair %v period %.2fms window %v camel events %d item events %d",
			addr, camel, pairTime.Sub(spawnAt), item, itemPairTime.Sub(itemSpawnAt), float64(period)/1e6, window, len(events), len(itemEvents))
		return legData{camel: events, item: itemEvents, window: window}
	}

	vanillaLeg := run(targets.vanilla)
	mcgoLeg := run(targets.mcgo)

	// assertGrid pins one body's tracker cadence: the periodics sit on
	// an exact 60-tick grid with sub-frame residuals around one fitted
	// phase, exactly one event per 60-tick multiple, and only the
	// teleportDelay boundary produces an absolute — the camel's slot 20
	// (updateInterval 3), never the item's (updateInterval 20 needs 400
	// gate-open calls, tick 8000, far past this window).
	assertGrid := func(name string, ev []ftEvent, window time.Duration, absoluteSlot int, ax, ay, az float64) {
		period := ftFramePeriod(ev)
		if period < 35*time.Millisecond || period > 70*time.Millisecond {
			t.Errorf("%s frame period %.2fms outside the 20tps band", name, float64(period)/1e6)
		}
		frame := period
		type slot struct {
			kind string
			off  float64
			zero bool
			raw  []byte
		}
		slots := map[int]slot{}
		var dev []float64
		for i, e := range ev {
			if i == 0 {
				continue // the pairing response itself
			}
			off := float64(e.at.Sub(ev[0].at)) / float64(frame)
			k := int(off/60 + .5)
			if k < 1 {
				t.Errorf("%s event %d (%s) at %.2f frames before the first periodic slot", name, i, e.kind, off)
				continue
			}
			dev = append(dev, off-float64(60*k))
			if prev, dup := slots[k]; dup {
				t.Errorf("%s two events at multiples slot %d: %s and %s", name, k, prev.kind, e.kind)
			}
			slots[k] = slot{e.kind, off, ftZeroDelta(e.raw), e.raw}
		}
		// The periodics sit on an exact 60-tick grid: consecutive
		// events are 60 frames apart and each event's deviation from
		// its grid multiple is one per-leg constant plus sub-frame
		// wobble. Only the constant's bound is pinned — the pairing
		// tick's own work is inflated by the summon and the
		// post-teleport chunk stream, so the pairing response lags its
		// tick's flush phase by a per-run margin (measured −1.03 and
		// −0.77 frames across two vanilla runs, +0.4..+1.1 on mc-go's
		// rate-managed loop) — while the grid spacing itself is exact.
		// The wobble bound covers one measured sustained sag: with the
		// frozen pass covering both bodies, one run sagged 0.53 frames
		// across three consecutive events on BOTH grids at once (a
		// shared paced-loop wobble, not per-body drift), so the bound
		// sits just above it at .6 frames.
		phase := ftMedian(dev)
		for i, d := range dev {
			if d-phase > .6 || d-phase < -.6 {
				t.Errorf("%s event %d sits %.2f frames off its leg's fitted grid", name, i+1, d-phase)
			}
		}
		if phase > 1.5 || phase < -1.5 {
			t.Errorf("%s periodic grid %.2f frames from the pairing response, want within 1.5", name, phase)
		}
		for i := 2; i < len(ev); i++ {
			gap := float64(ev[i].at.Sub(ev[i-1].at)) / float64(frame)
			if gap < 59.65 || gap > 60.35 {
				t.Errorf("%s events %d..%d sit %.2f frames apart, want one per 60-tick multiple", name, i, i+1, gap)
			}
		}
		// Every 60-tick multiple through the window carries exactly one
		// event; only the absoluteSlot boundary is the teleportDelay>400
		// absolute. Slots past 22 are extra periodics inside the scaled
		// window and stay unchecked beyond the duplicate map above.
		for k := 1; k <= 21; k++ {
			got, ok := slots[k]
			if !ok {
				t.Errorf("%s missing periodic at multiple %d (tick %d)", name, k, 60*k)
				continue
			}
			want := "movepos"
			if k == absoluteSlot {
				want = "absolute"
			}
			if got.kind != want {
				t.Errorf("%s multiple %d was %s, want %s", name, k, got.kind, want)
			}
			if got.kind == "movepos" && !got.zero {
				t.Errorf("%s periodic at multiple %d carried a nonzero delta", name, k)
			}
		}
		if got, ok := slots[22]; ok {
			// Past the camel's absolute the reset teleportDelay leaves
			// the next forced branch a plain zero-delta movepos again;
			// for the item slot 22 is just the next periodic.
			if got.kind != "movepos" || !got.zero {
				t.Errorf("%s multiple 22 was %s, want zero-delta movepos", name, got.kind)
			}
		} else {
			t.Logf("%s window %v closed before multiple 22 (period %.2fms)", name, window, float64(period)/1e6)
		}
		// Exactly the expected absolutes at the teleportDelay boundary,
		// carrying the summoned position; a stationary body never emits
		// PosRot.
		absCount := 0
		for _, e := range ev[1:] {
			if e.kind == "absolute" {
				absCount++
				x, y, z, ok := ftAbsolutePos(e.raw)
				if !ok || x != ax || y != ay || z != az {
					t.Errorf("%s absolute carried (%v,%v,%v), want (%g,%g,%g)", name, x, y, z, ax, ay, az)
				}
			}
			if e.kind == "moveposrot" {
				t.Errorf("%s move_entity_pos_rot arrived for a stationary body", name)
			}
		}
		wantAbs := 0
		if absoluteSlot != 0 {
			wantAbs = 1
		}
		if absCount != wantAbs {
			t.Errorf("%s absolute count %d, want %d", name, absCount, wantAbs)
		}
	}

	for _, leg := range []struct {
		name string
		data legData
	}{{"vanilla", vanillaLeg}, {"mc-go", mcgoLeg}} {
		assertGrid(leg.name+" camel", leg.data.camel, leg.data.window, 20, camelX, camelY, camelZ)
		// The non-living tracker rides the same pass: every %60
		// multiple is a gate-open for the item too, so its grid is the
		// camel's — except no absolute exists anywhere in the window.
		assertGrid(leg.name+" item", leg.data.item, leg.data.window, 0, itemX, itemY, itemZ)
	}
}

// ftWaitSay blocks until the exact /say marker arrives, like
// waitForExactSayBB but on the cadence sink's chat log.
func ftWaitSay(t *testing.T, sink *ftCadenceSink, marker string) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lines []string
	for time.Now().Before(deadline) {
		lines = append(lines, sink.take()...)
		if structureSaidBB(lines, marker) {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("exact /say marker %q never arrived; sink %s", marker, sink.health())
	return lines
}

// ftTrackerEvents collects one entity's tracker events after the pairing
// response within a capture window.
func ftTrackerEvents(sink *ftCadenceSink, id int32, pairTime time.Time, window time.Duration) []ftEvent {
	var events []ftEvent
	for _, ev := range sink.snapshot() {
		if ev.id != id || ev.kind == "spawn" || ev.at.Before(pairTime) {
			continue
		}
		if ev.at.Sub(pairTime) > window {
			continue
		}
		events = append(events, ev)
	}
	return events
}

// ftMedian returns the median of a float slice, or 0 when empty.
func ftMedian(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// ftFramePeriod derives one frame's wall duration from the periodic gaps:
// consecutive events sit 60 frames apart, so the median gap over sixty
// normalizes each leg's own tick rate without trusting absolute wall time.
func ftFramePeriod(events []ftEvent) time.Duration {
	var gaps []float64
	for i := 1; i < len(events); i++ {
		gaps = append(gaps, float64(events[i].at.Sub(events[i-1].at))/60)
	}
	if len(gaps) == 0 {
		return 50 * time.Millisecond
	}
	sort.Float64s(gaps)
	return time.Duration(gaps[len(gaps)/2])
}

// ftZeroDelta decodes a move_entity_pos body (id + three shorts + flags)
// and reports whether the delta is exactly zero.
func ftZeroDelta(raw []byte) bool {
	r := protocol.NewPacketReader(raw)
	if _, err := r.VarInt(); err != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		d, err := r.Short()
		if err != nil || d != 0 {
			return false
		}
	}
	return true
}

// ftAbsolutePos decodes an entity_position_sync body (id + three doubles +
// rotation + flags) far enough to read the absolute position.
func ftAbsolutePos(raw []byte) (float64, float64, float64, bool) {
	r := protocol.NewPacketReader(raw)
	if _, err := r.VarInt(); err != nil {
		return 0, 0, 0, false
	}
	x, a := r.Double()
	y, b := r.Double()
	z, c := r.Double()
	if a != nil || b != nil || c != nil {
		return 0, 0, 0, false
	}
	return x, y, z, true
}

// ftEvent is one spawn or pos-family packet with the wall time it arrived.
// Arrival time stands in for send time: both legs loop back over the same
// host, and per-packet jitter (a few ms) is far below the 50ms frame the
// phase assertions resolve.
type ftEvent struct {
	at   time.Time
	id   int32
	kind string
	raw  []byte
}

// ftCadenceSink keeps the session alive (keepalive, chunk-batch, SyncPos
// confirm — with no movement echo, so the observer stays where the teleport
// put it) while timestamping every spawn and pos-family packet.
type ftCadenceSink struct {
	mu        sync.Mutex
	ids       map[int32]int
	chat      []string
	total     int
	err       error
	events    []ftEvent
	spawnSeen map[int32]time.Time
}

func newFtCadenceSink(pr *protocol.Reader, pw *protocol.Writer) *ftCadenceSink {
	s := &ftCadenceSink{ids: map[int32]int{}, spawnSeen: map[int32]time.Time{}}
	posIDs := server.LatestVersion().IDs
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			s.mu.Lock()
			s.total++
			s.ids[p.ID]++
			if len(p.Data) < 4096 {
				s.chat = append(s.chat, fmt.Sprintf("%#x:%s", p.ID, string(p.Data)))
			}
			switch p.ID {
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, a := r.VarInt()
				if a == nil {
					s.events = append(s.events, ftEvent{time.Now(), id, "spawn", append([]byte(nil), p.Data...)})
					if _, seen := s.spawnSeen[id]; !seen {
						s.spawnSeen[id] = time.Now()
					}
				}
			case posIDs.PlayMoveEntityPos, posIDs.PlayMoveEntityPosRot,
				posIDs.PlayEntityTeleportCB, posIDs.PlayMoveEntityRot:
				r := protocol.NewPacketReader(p.Data)
				id, a := r.VarInt()
				if a == nil {
					kind := "movepos"
					switch p.ID {
					case posIDs.PlayMoveEntityPosRot:
						kind = "moveposrot"
					case posIDs.PlayEntityTeleportCB:
						kind = "absolute"
					case posIDs.PlayMoveEntityRot:
						kind = "moverot"
					}
					s.events = append(s.events, ftEvent{time.Now(), id, kind, append([]byte(nil), p.Data...)})
				}
			}
			s.mu.Unlock()
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				err = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				err = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				r := protocol.NewPacketReader(p.Data)
				var id int32
				id, err = r.VarInt()
				if err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					err = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			}
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
		}
	}()
	return s
}

// snapshot returns the recorded packets in arrival order without clearing,
// so a poll can rescan without consuming what a later window still needs.
func (s *ftCadenceSink) snapshot() []ftEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ftEvent(nil), s.events...)
}

func (s *ftCadenceSink) spawnTime(id int32) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnSeen[id]
}

// take hands the chat log to marker waits.
func (s *ftCadenceSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.chat
	s.chat = nil
	return out
}

func (s *ftCadenceSink) health() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, n := range s.ids {
		ids = append(ids, strconv.Itoa(int(id))+":"+strconv.Itoa(n))
	}
	sort.Strings(ids)
	return fmt.Sprintf("packets=%d err=%v ids=[%s]", s.total, s.err, strings.Join(ids, " "))
}
