package externalavatar

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/api/player"
	"mc-go/api/plugin"
	"mc-go/server"
)

// This test adapter receives only the public Core capability. It admits and
// controls its own avatars; it never obtains a concrete server/session pointer.
// Its listener provides the actual attachment lifetime. Java observers below
// are independent real TCP clients, not adapter callbacks or internal fixtures.
type adapter struct{ ready chan frontend.Core }

func (*adapter) Name() string { return "external-avatar-test" }
func (a *adapter) Serve(_ context.Context, c frontend.Core, ln net.Listener) error {
	a.ready <- c
	for {
		conn, e := ln.Accept()
		if e != nil {
			return e
		}
		conn.Close()
	}
}

type scope struct {
	core frontend.Core
	ln   net.Listener
	done chan struct{}
	err  error
}

func attach(t *testing.T, s *server.Server) *scope {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := &adapter{ready: make(chan frontend.Core, 1)}
	out := &scope{ln: ln, done: make(chan struct{})}
	go func() { out.err = s.Attach(a, ln); close(out.done) }()
	select {
	case out.core = <-a.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("external attachment did not start")
	}
	t.Cleanup(func() { out.close(t) })
	return out
}
func (s *scope) close(t *testing.T) {
	t.Helper()
	s.ln.Close()
	select {
	case <-s.done:
		if !errors.Is(s.err, net.ErrClosed) {
			t.Fatalf("attachment exit: %v", s.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attachment did not exit")
	}
}

type host struct {
	s     *server.Server
	scope *scope
	addr  string
}

func newHost(t *testing.T) *host {
	t.Helper()
	s := server.New("127.0.0.1:0")
	s.ViewDistance = 2
	s.SimulationDistance = 2
	s.StartFrozen = true
	s.TimeSyncEvery = -1
	t.Cleanup(s.Stop)
	if e := s.InitPlugins(); e != nil {
		t.Fatal(e)
	}
	sc := attach(t, s)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ln) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Java listener did not stop")
		}
	})
	return &host{s: s, scope: sc, addr: ln.Addr().String()}
}
func profile(name string, seed byte) player.Profile {
	var id [16]byte
	id[0], id[6], id[8], id[15] = 0x42, 0x40, 0x80, seed
	return player.Profile{Name: name, UUID: id}
}
func offline(name string) [16]byte {
	id := md5.Sum([]byte("OfflinePlayer:" + name))
	id[6] = id[6]&15 | 0x30
	id[8] = id[8]&63 | 0x80
	return id
}

var vetoCalls atomic.Int64

type vetoPlugin struct{}

func (vetoPlugin) Name() string { return "external-avatar-veto" }
func (vetoPlugin) Init(api plugin.API) error {
	api.Hooks().OnMove(api.Context(), 0, func(_ context.Context, m *plugin.Move) bool {
		if m.Player == "NeutralBefore" && m.To.Y == 123.25 {
			vetoCalls.Add(1)
			return false
		}
		return true
	})
	return nil
}
func init() { plugin.Register(vetoPlugin{}) }

type peer struct {
	connection player.Connection
	snapshot   []player.State
	self       player.State
	last       uint64
	seen       bool
}

func admit(t *testing.T, ctx context.Context, c frontend.Core, p player.Profile, o player.Options) *peer {
	t.Helper()
	conn, e := c.Admit(ctx, p, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	a := &peer{connection: conn}
	u := a.next(t, func(u player.Update) bool { return true })
	if u.Kind != player.Snapshot {
		t.Fatalf("first update%d, wantSnapshot", u.Kind)
	}
	a.snapshot = append([]player.State(nil), u.Players...)
	ids := map[player.ID]bool{}
	found := false
	for _, state := range u.Players {
		if ids[state.ID] {
			t.Fatal("duplicate incarnation in snapshot")
		}
		ids[state.ID] = true
		if state.ID == conn.ID() {
			a.self = state
			found = true
		}
	}
	if !found || a.self.Profile != p || a.self.EntityID <= 0 || !finite(a.self.Pose) {
		t.Fatalf("invalid admitted self: %+v", a.self)
	}
	return a
}
func (a *peer) next(t *testing.T, match func(player.Update) bool) player.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		u, e := a.connection.Next(ctx)
		if e != nil {
			t.Fatalf("neutral Next: %v", e)
		}
		if a.seen && u.Revision <= a.last {
			t.Fatalf("non-increasing stream revision%d after%d", u.Revision, a.last)
		}
		a.seen, a.last = true, u.Revision
		if match(u) {
			return u
		}
	}
}
func (a *peer) move(t *testing.T, p player.Pose) {
	t.Helper()
	ok, e := a.connection.Move(context.Background(), p)
	if e != nil || !ok {
		t.Fatalf("neutral move applied=%v error=%v", ok, e)
	}
	u := a.next(t, func(u player.Update) bool {
		return u.Kind == player.Moved && u.Player.ID == a.self.ID && u.Player.Pose == p
	})
	if u.Player.Profile != a.self.Profile || u.Player.EntityID != a.self.EntityID || u.Player.Pose != p {
		t.Fatalf("committed neutral movement %+v want%+v", u.Player, p)
	}
	a.self = u.Player
}
func command(t *testing.T, c frontend.Core, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, e := c.Execute(ctx, frontend.Action{Kind: frontend.Command, Command: text})
	if e != nil || !r.Applied {
		t.Fatalf("setup command%q applied=%v error=%v", text, r.Applied, e)
	}
}
func fence(t *testing.T, c frontend.Core, java []*javaPeer, eid int32, bad player.Pose) {
	t.Helper()
	token := fmt.Sprintf("avatar-fence-%d", time.Now().UnixNano())
	command(t, c, "tellraw @a \""+token+"\"")
	for _, j := range java {
		j.fence(t, token, eid, bad)
	}
}
func publicPose(t *testing.T, c frontend.Core, name string, want player.Pose) {
	t.Helper()
	snap, e := c.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range snap.Players {
		if p.Name == name {
			if p.X != want.X || p.Y != want.Y || p.Z != want.Z {
				t.Fatalf("snapshot pose%+v want%+v", p, want)
			}
			return
		}
	}
	t.Fatalf("public snapshot missing%q", name)
}
func closedMove(t *testing.T, c player.Connection, p player.Pose) {
	t.Helper()
	ok, e := c.Move(context.Background(), p)
	if ok || !errors.Is(e, player.ErrClosed) {
		t.Fatalf("stale move applied=%v error=%v", ok, e)
	}
}

func TestPublicAvatarSharedJavaLifecycle(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	before := admit(t, ctx, h.scope.core, profile("NeutralBefore", 1), player.Options{})
	if len(before.snapshot) != 1 {
		t.Fatalf("initial neutral-only snapshot count%d", len(before.snapshot))
	}
	canceled, stopRead := context.WithCancel(ctx)
	stopRead()
	if _, e := before.connection.Next(canceled); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled read error%v", e)
	}
	publicPose(t, h.scope.core, before.self.Profile.Name, before.self.Pose)
	if conn, e := h.scope.core.Admit(ctx, profile("Unsupported", 9), player.Options{Capabilities: player.Capability(1 << 63)}); !errors.Is(e, player.ErrUnsupportedCapabilities) || conn != nil {
		t.Fatalf("unsupported capability returned%v,%v", conn, e)
	}

	java := []*javaPeer{openJava(t, ctx, h.addr, 775), openJava(t, ctx, h.addr, 776)}
	for _, j := range java {
		j.presence(t, before.self)
		u := before.next(t, func(u player.Update) bool {
			return u.Kind == player.Added && u.Player.Profile.Name == j.client.Username()
		})
		if u.Player.EntityID != j.client.EntityID || u.Player.Profile.UUID != offline(j.client.Username()) {
			t.Fatalf("Java and neutral identity disagree: %+v", u.Player)
		}
	}
	after := admit(t, ctx, h.scope.core, profile("NeutralAfter", 2), player.Options{})
	if len(after.snapshot) != 4 {
		t.Fatalf("atomic initial snapshot players%d want4", len(after.snapshot))
	}
	for _, j := range java {
		found := false
		for _, p := range after.snapshot {
			if p.EntityID == j.client.EntityID && p.Profile.UUID == offline(j.client.Username()) {
				found = true
			}
		}
		if !found {
			t.Fatal("snapshot omitted existing Java player")
		}
	}

	for _, j := range java {
		j.presence(t, after.self)
	}
	before.next(t, func(u player.Update) bool { return u.Kind == player.Added && u.Player.ID == after.self.ID })
	moved := before.self.Pose
	moved.X += .25
	moved.Yaw = 90
	moved.OnGround = true
	before.move(t, moved)
	for _, j := range java {
		j.moved(t, before.self)
	}
	after.next(t, func(u player.Update) bool {
		return u.Kind == player.Moved && u.Player.ID == before.self.ID && u.Player.Pose == moved
	})
	// Java775 and776 inputs must appear in the same neutral stream, with the
	// exact entity identity each Java codec assigned, rather than surrogate NPCs.
	for _, j := range java {
		p := before.self.Pose
		p.X -= .5
		p.Z += float64(j.client.Protocol()-774) * .125
		p.Yaw = 30
		if e := j.client.Move(p.X, p.Y, p.Z, p.Yaw, p.Pitch, p.OnGround); e != nil {
			t.Fatal(e)
		}
		u := before.next(t, func(u player.Update) bool {
			return u.Kind == player.Moved && u.Player.EntityID == j.client.EntityID && u.Player.Pose == p
		})
		if u.Player.Profile.UUID != offline(j.client.Username()) || u.Player.Pose != p {
			t.Fatalf("neutral Java update%+v want%+v", u.Player, p)
		}
	}
	// Existing privileged Java teleports and dimension travel also mutate the
	// shared record. They must reach the neutral feed without client Move.
	j := java[0]
	target := before.self.Pose
	target.X -= .625
	target.Yaw = 30
	result, e := h.scope.core.Execute(ctx, frontend.Action{Kind: frontend.Teleport, Player: j.client.Username(), Movement: target})
	if e != nil || !result.Applied {
		t.Fatalf("public Java teleport%+v %v", result, e)
	}
	teleported := before.next(t, func(u player.Update) bool {
		return u.Kind == player.Moved && u.Player.EntityID == j.client.EntityID && u.Player.Pose == target
	}).Player
	if teleported.Pose != target || teleported.Profile.UUID != offline(j.client.Username()) {
		t.Fatalf("neutral feed missed exact Java teleport%+v want%+v", teleported, target)
	}
	j.synchronized(t, target)
	for _, dimension := range []string{"minecraft:the_nether", "minecraft:overworld"} {
		destination := player.Pose{X: target.X, Y: target.Y, Z: target.Z}
		if dimension == "minecraft:the_nether" {
			destination.Y = 70
		}
		command(t, h.scope.core, fmt.Sprintf("execute in %s run tp %s %.6f %.6f %.6f", dimension, j.client.Username(), destination.X, destination.Y, destination.Z))
		crossed := before.next(t, func(u player.Update) bool {
			return u.Kind == player.Moved && u.Player.ID == teleported.ID && u.Player.Dimension == dimension
		}).Player
		if crossed.Profile != teleported.Profile || crossed.EntityID != teleported.EntityID || crossed.Pose != destination {
			t.Fatalf("neutral dimension state%+v want%+v in%s", crossed, destination, dimension)
		}
		j.respawn(t, dimension)
	}
	// Java mode changes are state deltas too, without exposing a neutral
	// avatar mode/inventory action outside the presence/movement capability.
	for _, mode := range []struct {
		name string
		id   int32
	}{{"spectator", 3}, {"creative", 1}} {
		if e := j.client.Command("gamemode " + mode.name + " @s"); e != nil {
			t.Fatal(e)
		}
		j.mode(t, mode.id)
		updated := before.next(t, func(u player.Update) bool {
			return u.Kind == player.Moved && u.Player.ID == teleported.ID && u.Player.Gamemode == mode.id
		}).Player
		if updated.Profile != teleported.Profile || updated.EntityID != teleported.EntityID || updated.Dimension != "minecraft:overworld" {
			t.Fatalf("Java mode update changed identity%+v", updated)
		}
	}
	// The wall is entirely outside the prior0.6-wide player body. Existing
	// overlap is intentionally tolerated by the canonical collision rules.
	wall := int(math.Floor(before.self.Pose.X)) + 2
	y := int(math.Floor(before.self.Pose.Y))
	z := int(math.Floor(before.self.Pose.Z))
	command(t, h.scope.core, fmt.Sprintf("setblock %d %d %d minecraft:stone", wall, y, z))
	bad := before.self.Pose
	bad.X = float64(wall) + .5
	ok, e := before.connection.Move(ctx, bad)
	if e != nil || ok {
		t.Fatalf("collision move applied=%v error=%v", ok, e)
	}
	correction := before.next(t, func(u player.Update) bool { return u.Kind == player.Correction && u.Player.ID == before.self.ID })
	if correction.Player.Pose != before.self.Pose {
		t.Fatal("collision correction changed committed pose")
	}
	fence(t, h.scope.core, java, before.self.EntityID, bad)
	command(t, h.scope.core, fmt.Sprintf("setblock %d %d %d minecraft:air", wall, y, z))
	calls := vetoCalls.Load()
	bad = before.self.Pose
	bad.Y = 123.25
	ok, e = before.connection.Move(ctx, bad)
	if e != nil || ok || vetoCalls.Load() != calls+1 {
		t.Fatalf("plugin veto bypassed applied=%v error=%v calls%d", ok, e, vetoCalls.Load()-calls)
	}
	correction = before.next(t, func(u player.Update) bool { return u.Kind == player.Correction && u.Player.ID == before.self.ID })
	if correction.Player.Pose != before.self.Pose {
		t.Fatal("plugin correction changed committed pose")
	}
	publicPose(t, h.scope.core, before.self.Profile.Name, before.self.Pose)
	fence(t, h.scope.core, java, before.self.EntityID, bad)
	moved = before.self.Pose
	moved.Z += .25
	before.move(t, moved)
	after.next(t, func(u player.Update) bool {
		if u.Kind == player.Moved && u.Player.ID == before.self.ID && u.Player.Pose == bad {
			t.Fatal("vetoed movement leaked into neutral stream")
		}
		return u.Kind == player.Moved && u.Player.ID == before.self.ID && u.Player.Pose == moved
	})
	for _, j := range java {
		j.moved(t, before.self)
	}
	if e := after.connection.Close(); e != nil {
		t.Fatal(e)
	}
	if e := after.connection.Close(); e != nil {
		t.Fatal(e)
	}
	before.next(t, func(u player.Update) bool { return u.Kind == player.Removed && u.Player.ID == after.self.ID })
	for _, j := range java {
		j.removed(t, after.self)
	}
	oldID := before.connection.ID()
	oldState := before.self
	// Use a valid, unvetoed target for stale-handle checks so neither the
	// collision guard nor the plugin can hide accidental incarnation reuse.
	staleTarget := oldState.Pose
	staleTarget.X += .75
	h.scope.close(t)
	for _, j := range java {
		j.removed(t, oldState)
	}
	closedMove(t, before.connection, staleTarget)
	// Reusing an authenticated account creates a new incarnation. Captured old
	// handles must not control it, even when its name and UUID are identical.
	freshScope := attach(t, h.s)
	fresh := admit(t, ctx, freshScope.core, oldState.Profile, player.Options{})
	if fresh.connection.ID() == oldID {
		t.Fatal("reconnect reused closed incarnation")
	}
	for _, j := range java {
		j.presence(t, fresh.self)
	}
	closedMove(t, before.connection, staleTarget)
	publicPose(t, freshScope.core, fresh.self.Profile.Name, fresh.self.Pose)
	fence(t, freshScope.core, java, fresh.self.EntityID, staleTarget)
	moved = fresh.self.Pose
	moved.X += .125
	fresh.move(t, moved)
	for _, j := range java {
		j.moved(t, fresh.self)
	}
	h.s.Stop()
	closedMove(t, fresh.connection, moved)
}

func TestPublicAvatarOverflowIsolation(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	java := []*javaPeer{openJava(t, ctx, h.addr, 775), openJava(t, ctx, h.addr, 776)}
	healthy := admit(t, ctx, h.scope.core, profile("HealthyAvatar", 3), player.Options{})
	slow := admit(t, ctx, h.scope.core, profile("StalledAvatar", 4), player.Options{Limits: player.Limits{Messages: 2, Bytes: 4096}})
	for _, j := range java {
		j.presence(t, healthy.self)
		j.presence(t, slow.self)
	}
	// The stalled receiver has consumed its initial snapshot, then stops Next.
	// Every source move is drained by the healthy receiver, keeping that peer
	// below its own budget. Only the stalled avatar may be retired.
	p := healthy.self.Pose
	for i := 0; i < 16; i++ {
		p.X += .03125
		healthy.move(t, p)
	}
	readCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	for attempts := 0; ; attempts++ {
		_, e := slow.connection.Next(readCtx)
		if errors.Is(e, player.ErrOverflow) {
			break
		}
		if e != nil || attempts > 3 {
			t.Fatalf("stalled stream terminal%v after%d reads", e, attempts)
		}
	}
	closedMove(t, slow.connection, p)
	for _, j := range java {
		j.removed(t, slow.self)
		j.moved(t, healthy.self)
	}
	snapshot, e := h.scope.core.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tick := snapshot.Tick
	for _, p := range snapshot.Players {
		if p.Name == slow.self.Profile.Name {
			t.Fatal("overflow avatar remained active")
		}
	}
	h.s.StepTicks(1)
	snapshot, e = h.scope.core.Snapshot(ctx)
	if e != nil || snapshot.Tick != tick+1 {
		t.Fatalf("stalled receiver blocked tick: %+v %v", snapshot, e)
	}
	p.Z += .125
	healthy.move(t, p)
	for _, j := range java {
		j.moved(t, healthy.self)
	}
}
