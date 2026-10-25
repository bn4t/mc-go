// Package anticheat is an example sophisticated anticheat built only on
// the public plugin API: event observation, player-state reads,
// violation reports, and kick/teleport enforcement. It demonstrates
// the patterns real detectors use:
//
//   - speed: horizontal distance over server-receipt time between
//     consecutive moves, with teleport and lag forgiveness;
//   - timer: movement-packet rate over a rolling 1s window;
//   - reach: eye-to-block distance on every block change;
//   - ledger: per-player per-check counts, auto-kick at a threshold,
//     operator introspection via /ac.
//
// Detectors never trust the client clock: they use the server receipt
// time (PlayerMove.At) and the last keep-alive round trip
// (PlayerMove.Latency). Server-initiated teleports are excused via the
// teleport event instead of punished as speed.
//
// Thresholds assume creative flight is legal (the core currently joins
// everyone creative): the speed limit only catches the absurd, and a
// survival fly check should additionally gate on PlayerState.Gamemode
// once survival lands.
package anticheat

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"mc-go/api/event"
	"mc-go/api/plugin"
)

func init() { plugin.Register(&anticheat{}) }

// Thresholds. Generous on purpose: a false kick is worse than a missed
// flag, and violations are always reported for human review.
const (
	// maxSpeedMS flags horizontal movement faster than any legal
	// creative flight (~11 m/s) plus wide margin.
	maxSpeedMS = 30.0
	// maxMovesPerSec flags packet-timer cheats; a vanilla client
	// moves at most ~20-30 packets/s.
	maxMovesPerSec = 60
	// maxReachM flags block breaks beyond creative reach (5m) plus
	// margin, measured eye-to-block-center.
	maxReachM = 6.5
	// violationsToKick auto-kicks after this many total violations.
	violationsToKick = 8
	// teleportGrace excuses movement near a server teleport target.
	teleportGrace = 2 * time.Second
	// lagForgive skips speed flags above this round trip.
	lagForgive = time.Second
)

const (
	checkSpeed = "speed"
	checkTimer = "timer"
	checkReach = "reach"
)

type anticheat struct{}

func (a *anticheat) Name() string { return "anticheat" }

func (a *anticheat) Init(api plugin.API) error {
	d := newDetector(api, violationsToKick)
	go d.run()
	api.Commands().Register("ac", func(player string, args []string) string {
		return d.command(args)
	})
	return nil
}

// tracked is one player's rolling detector state.
type tracked struct {
	haveLast     bool
	lastX, lastZ float64
	lastAt       time.Time
	moves        []time.Time // receipt times inside the rolling window
	counts       map[string]int64
	total        int64
	kicked       bool
	lastTeleport time.Time
	teleX, teleZ float64
}

// detector owns the ledger; all methods are safe for concurrent use.
type detector struct {
	api      plugin.API
	punishAt int64
	mu       sync.Mutex
	players  map[string]*tracked
}

func newDetector(api plugin.API, punishAt int64) *detector {
	return &detector{api: api, punishAt: punishAt, players: make(map[string]*tracked)}
}

func (d *detector) track(name string) *tracked {
	t := d.players[name]
	if t == nil {
		t = &tracked{counts: make(map[string]int64)}
		d.players[name] = t
	}
	return t
}

// run subscribes to core events; one goroutine per topic keeps slow
// checks from blocking each other (Publish drops on full buffers, so
// detectors degrade by missing samples, never by stalling the tick).
func (d *detector) run() {
	bus := d.api.Bus()
	mv, unsubMv := bus.SubscribeContext(d.api.Context(), event.TopicPlayerMove, 64)
	defer unsubMv()
	bl, unsubBl := bus.SubscribeContext(d.api.Context(), event.TopicBlockChange, 64)
	defer unsubBl()
	tp, unsubTp := bus.SubscribeContext(d.api.Context(), event.TopicPlayerTeleport, 64)
	defer unsubTp()
	lv, unsubLv := bus.SubscribeContext(d.api.Context(), event.TopicPlayerLeave, 64)
	defer unsubLv()
	for {
		select {
		case <-d.api.Context().Done():
			return
		case e, ok := <-mv:
			if !ok {
				return
			}
			if m, ok := e.Data.(event.PlayerMove); ok {
				d.onMove(m)
			}
		case e, ok := <-bl:
			if !ok {
				return
			}
			if b, ok := e.Data.(event.BlockChange); ok {
				d.onBlock(b)
			}
		case e, ok := <-tp:
			if !ok {
				return
			}
			if t, ok := e.Data.(event.PlayerTeleport); ok {
				d.onTeleport(t)
			}
		case e, ok := <-lv:
			if !ok {
				return
			}
			if m, ok := e.Data.(map[string]string); ok {
				d.onLeave(m["name"])
			}
		}
	}
}

func (d *detector) onMove(m event.PlayerMove) {
	d.mu.Lock()
	t := d.track(m.Name)
	// Excuse the landing after a server teleport: rebase the speed
	// baseline instead of flagging the spike.
	if !m.At.IsZero() && m.At.Sub(t.lastTeleport) < teleportGrace {
		if math.Hypot(m.X-t.teleX, m.Z-t.teleZ) < 1.5 {
			t.lastX, t.lastZ, t.lastAt = m.X, m.Z, m.At
			t.haveLast = true
			t.moves = append(t.moves, m.At)
			d.mu.Unlock()
			return
		}
	}
	at := m.At
	if at.IsZero() {
		at = time.Now()
	}
	t.moves = append(t.moves, at)
	cutoff := at.Add(-time.Second)
	kept := t.moves[:0]
	for _, ts := range t.moves {
		if !ts.Before(cutoff) {
			kept = append(kept, ts)
		}
	}
	t.moves = kept
	rate := len(t.moves)
	var speed float64 = -1
	if t.haveLast && !m.At.IsZero() && !t.lastAt.IsZero() {
		if dt := m.At.Sub(t.lastAt).Seconds(); dt > 0 && m.Latency < lagForgive {
			speed = math.Hypot(m.X-t.lastX, m.Z-t.lastZ) / dt
		}
	}
	if !m.At.IsZero() {
		t.lastX, t.lastZ, t.lastAt = m.X, m.Z, m.At
		t.haveLast = true
	}
	d.mu.Unlock()

	if speed > maxSpeedMS {
		d.violate(m.Name, checkSpeed,
			fmt.Sprintf("%.1f m/s horizontal (limit %.0f)", speed, maxSpeedMS))
	}
	if rate > maxMovesPerSec {
		d.violate(m.Name, checkTimer,
			fmt.Sprintf("%d moves/s (limit %d)", rate, maxMovesPerSec))
	}
}

func (d *detector) onBlock(b event.BlockChange) {
	eyeY := b.PY + 1.62
	dist := math.Sqrt(
		(b.PX-(float64(b.X)+0.5))*(b.PX-(float64(b.X)+0.5)) +
			(eyeY-(float64(b.Y)+0.5))*(eyeY-(float64(b.Y)+0.5)) +
			(b.PZ-(float64(b.Z)+0.5))*(b.PZ-(float64(b.Z)+0.5)))
	// A zero player snapshot means the change came from console/tests
	// with no actor to judge; skip rather than false-positive.
	if b.PX == 0 && b.PY == 0 && b.PZ == 0 {
		return
	}
	if dist > maxReachM {
		d.violate(b.Player, checkReach,
			fmt.Sprintf("broke (%d,%d,%d) from %.1fm (limit %.1f)", b.X, b.Y, b.Z, dist, maxReachM))
	}
}

func (d *detector) onTeleport(t event.PlayerTeleport) {
	d.mu.Lock()
	tr := d.track(t.Name)
	tr.lastTeleport = time.Now()
	tr.teleX, tr.teleZ = t.X, t.Z
	d.mu.Unlock()
}

func (d *detector) onLeave(name string) {
	d.mu.Lock()
	delete(d.players, name)
	d.mu.Unlock()
}

// violate records one finding, reports it for other plugins, and kicks
// past the punish threshold (once per player).
func (d *detector) violate(player, check, details string) {
	d.mu.Lock()
	t := d.track(player)
	t.counts[check]++
	t.total++
	count := t.counts[check]
	already := t.kicked
	if t.total >= d.punishAt {
		t.kicked = true
	}
	d.mu.Unlock()

	d.api.ReportViolation(plugin.Violation{
		Player: player, Check: check, Details: details,
		At: time.Now(), Count: count,
	})
	if !already && t.total >= d.punishAt {
		d.api.Kick(player, "Cheating detected ("+check+")")
	}
}

// command implements "/ac [violations [player] | status]".
func (d *detector) command(args []string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(args) == 0 || args[0] == "status" {
		n := len(d.players)
		var total int64
		for _, t := range d.players {
			total += t.total
		}
		return fmt.Sprintf("anticheat: tracking %d players, %d violations", n, total)
	}
	if args[0] == "violations" {
		if len(args) > 1 {
			t, ok := d.players[args[1]]
			if !ok || t.total == 0 {
				return "no violations for " + args[1]
			}
			return fmt.Sprintf("%s: %s", args[1], formatCounts(t.counts))
		}
		if len(d.players) == 0 {
			return "no violations recorded"
		}
		names := make([]string, 0, len(d.players))
		for n := range d.players {
			names = append(names, n)
		}
		sort.Strings(names)
		var rows []string
		for _, n := range names {
			if d.players[n].total > 0 {
				rows = append(rows, fmt.Sprintf("%s: %s", n, formatCounts(d.players[n].counts)))
			}
		}
		if len(rows) == 0 {
			return "no violations recorded"
		}
		return strings.Join(rows, "; ")
	}
	return "Usage: /ac [status | violations [player]]"
}

func formatCounts(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}
