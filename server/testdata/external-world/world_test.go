package externalavatar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/api/player"
	"mc-go/api/plugin"
	"mc-go/api/registry"
	"mc-go/api/worldview"
)

const nether = "minecraft:the_nether"
const overworld = "minecraft:overworld"
const airKey = "minecraft:air"
const stoneKey = "minecraft:stone"
const copperKey = "minecraft:waxed_copper_block"
const bulbKey = "minecraft:waxed_copper_bulb[lit=true,powered=false]"

var box = worldview.Box{Min: worldview.Pos{X: -2, Y: 70, Z: -2}, Max: worldview.Pos{X: 2, Y: 73, Z: 2}}
var trap = worldview.Pos{X: 1, Y: 72, Z: 1}
var editAPIs = make(chan plugin.API, 4)
var worldVeto atomic.Int64

type worldPlugin struct{}

func (worldPlugin) Name() string { return "external-world-policy" }
func (worldPlugin) Init(api plugin.API) error {
	api.Hooks().OnBlockEdit(api.Context(), 0, func(_ context.Context, p *plugin.BlockEdit) bool {
		if p.Dimension == nether && p.Position == trap && p.Target.Name() == "minecraft:diamond_block" {
			worldVeto.Add(1)
			return false
		}
		return true
	})
	editAPIs <- api
	return nil
}
func init() { plugin.Register(worldPlugin{}) }
func key(t *testing.T, s string) registry.StateKey {
	t.Helper()
	k, e := registry.ParseStateKey(s)
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func blocksCapability(t *testing.T, c frontend.Core) frontend.Blocks {
	t.Helper()
	b, ok := c.(frontend.Blocks)
	if !ok {
		t.Fatal("public block capability unavailable")
	}
	return b
}

type materialized struct {
	sub      worldview.Subscription
	request  worldview.Request
	catalog  *registry.BlockCatalog
	cells    []registry.BlockStateID
	returned []registry.BlockStateID
	revision uint64
}

func watch(t *testing.T, ctx context.Context, b frontend.Blocks, r worldview.Request) *materialized {
	t.Helper()
	sub, e := b.WatchBlocks(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	return snapshot(t, sub, r)
}
func snapshot(t *testing.T, sub worldview.Subscription, r worldview.Request) *materialized {
	t.Helper()
	t.Cleanup(func() { sub.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, e := sub.Next(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if u.Kind != worldview.Snapshot || u.Dimension != r.Dimension || u.Bounds != r.Bounds || u.Catalog == nil || len(u.Blocks) != len(positions(r.Bounds)) || len(u.Changes) != 0 {
		t.Fatalf("invalid fixed snapshot kind%d dim%s bounds%+v cells%d", u.Kind, u.Dimension, u.Bounds, len(u.Blocks))
	}
	v := &materialized{sub: sub, request: r, catalog: u.Catalog, cells: append([]registry.BlockStateID(nil), u.Blocks...), returned: u.Blocks, revision: u.Revision}
	for _, id := range v.cells {
		if _, ok := v.catalog.State(id); !ok {
			t.Fatal("snapshot has unknown catalog handle")
		}
	}
	return v
}
func (v *materialized) state(t *testing.T, p worldview.Pos) string {
	t.Helper()
	s, ok := v.catalog.State(v.cells[at(v.request.Bounds, p)])
	if !ok {
		t.Fatal("unknown materialized state")
	}
	return s.String()
}
func (v *materialized) delta(t *testing.T) worldview.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, e := v.sub.Next(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if u.Kind != worldview.Delta || u.Revision <= v.revision || u.Dimension != v.request.Dimension || u.Bounds != v.request.Bounds || u.Catalog != v.catalog || len(u.Blocks) != 0 || len(u.Changes) == 0 {
		t.Fatalf("invalid delta kind%d revision%d after%d dim%s bounds%+v changes%d", u.Kind, u.Revision, v.revision, u.Dimension, u.Bounds, len(u.Changes))
	}
	seen := map[worldview.Pos]bool{}
	for _, c := range u.Changes {
		if !inside(v.request.Bounds, c.Position) || seen[c.Position] {
			t.Fatalf("out-of-view/duplicate delta%+v", c)
		}
		seen[c.Position] = true
		if _, ok := v.catalog.State(c.State); !ok {
			t.Fatal("delta has unknown catalog handle")
		}
		if v.cells[at(v.request.Bounds, c.Position)] == c.State {
			t.Fatal("delta repeated unchanged state")
		}
		v.cells[at(v.request.Bounds, c.Position)] = c.State
	}
	v.revision = u.Revision
	return u
}
func (v *materialized) until(t *testing.T, p worldview.Pos, want string, inspect func(worldview.Change)) {
	t.Helper()
	for {
		u := v.delta(t)
		found := false
		for _, c := range u.Changes {
			if inspect != nil {
				inspect(c)
			}
			if c.Position == p && v.state(t, p) == want {
				found = true
			}
		}
		if found {
			return
		}
	}
}
func (v *materialized) expect(t *testing.T, p worldview.Pos, want string) {
	t.Helper()
	if got := v.state(t, p); got != want {
		t.Fatalf("%s at%+v=%s want%s", v.request.Dimension, p, got, want)
	}
}
func sameImage(t *testing.T, a, b *materialized) {
	t.Helper()
	if a.request.Dimension != b.request.Dimension || a.request.Bounds != b.request.Bounds || len(a.cells) != len(b.cells) {
		t.Fatal("materialized views describe different volumes")
	}
	for _, p := range positions(a.request.Bounds) {
		a.expect(t, p, b.state(t, p))
	}
}
func nativeImage(t *testing.T, v *materialized, java []*nativeBlocks) {
	t.Helper()
	for _, p := range positions(v.request.Bounds) {
		for _, n := range java {
			n.expect(t, p, v.state(t, p))
		}
	}
}
func edit(t *testing.T, ctx context.Context, b frontend.Blocks, dim string, p worldview.Pos, state string, expected *registry.StateKey) worldview.EditResult {
	t.Helper()
	r, e := b.EditBlock(ctx, worldview.Edit{Dimension: dim, Position: p, State: key(t, state), Expected: expected})
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestPublicWorldFixedVolume(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
	defer cancel()
	b := blocksCapability(t, h.scope.core)
	var pluginAPI plugin.API
	select {
	case pluginAPI = <-editAPIs:
	case <-time.After(time.Second):
		t.Fatal("plugin API not initialized")
	}
	j0, j1 := openJava(t, ctx, h.addr, 775), openJava(t, ctx, h.addr, 776)
	java := []*nativeBlocks{newNative(t, j0, box), newNative(t, j1, box)}
	readyNether(t, java[0])
	readyNether(t, java[1])
	// Small negative coordinates straddle four chunk columns. Setup and all
	// state assertions are in the skylight-free dimension, with full Java acks.
	javaBlockCommand(t, h, j0, "fill -2 69 -2 1 69 1 minecraft:stone", java)
	javaBlockCommand(t, h, j0, "fill -2 70 -2 1 72 1 minecraft:air", java)
	initial := map[worldview.Pos]string{{X: -2, Y: 70, Z: -2}: stoneKey, {X: -1, Y: 70, Z: -2}: copperKey, {X: -2, Y: 70, Z: -1}: "minecraft:oak_planks", {X: -2, Y: 71, Z: -2}: bulbKey}
	for p, s := range initial {
		javaBlockCommand(t, h, j0, fmt.Sprintf("setblock %d %d %d %s", p.X, p.Y, p.Z, s), java)
	}
	r := worldview.Request{Dimension: nether, Bounds: box}
	v := watch(t, ctx, b, r)
	for _, p := range positions(box) {
		want := airKey
		if s, ok := initial[p]; ok {
			want = s
		}
		v.expect(t, p, want)
	}
	nativeImage(t, v, java)
	// A snapshot's returned cells are consumer-owned. Mutating another returned
	// copy cannot alter the world or this independently materialized image.
	copyView := watch(t, ctx, b, r)
	copyView.returned[0] = copyView.returned[len(copyView.returned)-1]
	fresh := watch(t, ctx, b, r)
	sameImage(t, v, fresh)
	copyView.sub.Close()
	fresh.sub.Close()
	command(t, h.scope.core, "list")
	for i, j := range []*javaPeer{j0, j1} {
		p := worldview.Pos{X: int32(i - 1), Y: 70, Z: int32(i - 1)}
		creativeBlock(t, j, p, int32(100+i))
		javaBlockCommand(t, h, j, "list", java)
		v.until(t, p, copperKey, nil)
		nativeImage(t, v, java)
		creativeDig(t, j, p, int32(110+i))
		javaBlockCommand(t, h, j, "list", java)
		v.until(t, p, airKey, nil)
		nativeImage(t, v, java)
	}
	p := worldview.Pos{X: -2, Y: 70, Z: 0}
	air := key(t, airKey)
	result := edit(t, ctx, b, nether, p, bulbKey, &air)
	if !result.Applied || result.Before != v.cells[at(box, p)] {
		t.Fatal("typed edit CAS/before result")
	}
	v.until(t, p, bulbKey, nil)
	if result.After != v.cells[at(box, p)] {
		t.Fatal("typed edit After not actual state")
	}
	blockFence(t, h, java)
	nativeImage(t, v, java)
	editor, ok := pluginAPI.(plugin.BlockEditor)
	if !ok {
		t.Fatal("plugin block editor unavailable")
	}
	pp := worldview.Pos{X: 1, Y: 70, Z: -2}
	receipt, e := editor.SubmitBlockEdit(ctx, worldview.Edit{Dimension: nether, Position: pp, State: key(t, copperKey), Expected: &air})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case outcome, ok := <-receipt:
		if !ok || outcome.Err != nil || !outcome.Result.Applied {
			t.Fatalf("plugin edit receipt%+v present%v", outcome, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("plugin receipt did not finish")
	}
	select {
	case _, ok := <-receipt:
		if ok {
			t.Fatal("multiple plugin outcomes")
		}
	case <-time.After(time.Second):
		t.Fatal("receipt channel remained open")
	}
	v.until(t, pp, copperKey, nil)
	blockFence(t, h, java)
	nativeImage(t, v, java)
	// One Java fill is one complete owner commit: every affected position
	// appears once in one coalesced batch, not a torn sequence of cell writes.
	javaBlockCommand(t, h, j1, "fill 0 71 0 1 72 1 minecraft:stone", java)
	batch := v.delta(t)
	if len(batch.Changes) != 8 {
		t.Fatalf("coalesced fill changes%d want8", len(batch.Changes))
	}
	for _, c := range batch.Changes {
		if c.Position.X < 0 || c.Position.Z < 0 || c.Position.Y < 71 || v.state(t, c.Position) != stoneKey {
			t.Fatal("fill batch contains wrong final state/position")
		}
	}
	nativeImage(t, v, java)
	javaBlockCommand(t, h, j1, "fill 0 71 0 1 72 1 minecraft:stone", java)
	marker := worldview.Pos{X: -2, Y: 72, Z: 1}
	edit(t, ctx, b, nether, marker, copperKey, nil)
	v.until(t, marker, copperKey, func(c worldview.Change) {
		if c.Position.X >= 0 && c.Position.Z >= 0 && c.Position.Y >= 71 {
			t.Fatal("no-op fill emitted a state change")
		}
	})
	blockFence(t, h, java)
	// Same coordinates in another dimension belong to another view and must not
	// contaminate the Nether stream or either Nether client's native image.
	other := watch(t, ctx, b, worldview.Request{Dimension: overworld, Bounds: box})
	q := worldview.Pos{X: 1, Y: 70, Z: 1}
	other.expect(t, q, airKey)
	edit(t, ctx, b, overworld, q, stoneKey, nil)
	other.until(t, q, stoneKey, nil)
	marker2 := worldview.Pos{X: -2, Y: 72, Z: 0}
	edit(t, ctx, b, nether, marker2, copperKey, nil)
	v.until(t, marker2, copperKey, func(c worldview.Change) {
		if c.Position == q {
			t.Fatal("wrong dimension leaked into stream")
		}
	})
	blockFence(t, h, java)
	v.expect(t, q, airKey)
	nativeImage(t, v, java)
	other.sub.Close()
	command(t, h.scope.core, "list")
	// Rejections/no-ops must leave the target and all consumers unchanged. Native
	// observers reject even transient forbidden writes before the explicit fence.
	for _, n := range java {
		n.onChange = func(pos worldview.Pos, state int32) {
			if pos == trap && state != n.facts.States[stoneKey] {
				t.Fatal("rejected edit reached native viewer")
			}
		}
	}
	stone := key(t, stoneKey)
	no := edit(t, ctx, b, nether, trap, stoneKey, &stone)
	if no.Applied || no.Before != no.After {
		t.Fatal("identical edit was not a no-op")
	}
	tests := []struct {
		name   string
		edit   worldview.Edit
		cancel bool
		want   error
	}{{"CAS", worldview.Edit{Dimension: nether, Position: trap, State: key(t, copperKey), Expected: &air}, false, worldview.ErrRejected}, {"policy", worldview.Edit{Dimension: nether, Position: trap, State: key(t, "minecraft:diamond_block"), Expected: &stone}, false, worldview.ErrRejected}, {"canceled", worldview.Edit{Dimension: nether, Position: trap, State: key(t, copperKey)}, true, context.Canceled}, {"unknown", worldview.Edit{Dimension: nether, Position: trap, State: key(t, "minecraft:no_such_fixture_block")}, false, worldview.ErrRejected}}
	calls := worldVeto.Load()
	for _, tc := range tests {
		requestCtx := ctx
		if tc.cancel {
			c, stop := context.WithCancel(ctx)
			stop()
			requestCtx = c
		}
		out, e := b.EditBlock(requestCtx, tc.edit)
		if out.Applied || !errors.Is(e, tc.want) {
			t.Fatalf("%s applied%v error%v want%v", tc.name, out.Applied, e, tc.want)
		}
	}
	if worldVeto.Load() != calls+1 {
		t.Fatal("policy veto did not run exactly once")
	}
	rejectFence := worldview.Pos{X: -1, Y: 72, Z: -2}
	edit(t, ctx, b, nether, rejectFence, copperKey, nil)
	v.until(t, rejectFence, copperKey, func(c worldview.Change) {
		if c.Position == trap {
			t.Fatal("rejected/no-op edit entered stream")
		}
	})
	blockFence(t, h, java)
	nativeImage(t, v, java)
	for _, n := range java {
		n.onChange = nil
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, e := v.sub.Next(canceled); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled read%v", e)
	}
	// Capture races a committed edit on another attachment. Either linearization
	// is valid; applying every delta through the final marker must equal a fresh
	// canonical snapshot of the complete same dimension and box.
	raceScope := attach(t, h.s)
	raceBlocks := blocksCapability(t, raceScope.core)
	racePos := worldview.Pos{X: -1, Y: 71, Z: -1}
	raceMarker := worldview.Pos{X: 0, Y: 70, Z: -1}
	type opened struct {
		sub worldview.Subscription
		err error
	}
	openedCh := make(chan opened, 1)
	go func() { sub, e := raceBlocks.WatchBlocks(ctx, r); openedCh <- opened{sub, e} }()
	edit(t, ctx, b, nether, racePos, stoneKey, nil)
	var raceView *materialized
	select {
	case o := <-openedCh:
		if o.err != nil {
			t.Fatal(o.err)
		}
		raceView = snapshot(t, o.sub, r)
	case <-time.After(10 * time.Second):
		t.Fatal("racing watch setup blocked")
	}
	edit(t, ctx, b, nether, raceMarker, copperKey, nil)
	raceView.until(t, raceMarker, copperKey, nil)
	v.until(t, raceMarker, copperKey, nil)
	fresh = watch(t, ctx, b, r)
	sameImage(t, raceView, fresh)
	sameImage(t, v, fresh)
	blockFence(t, h, java)
	nativeImage(t, fresh, java)
	fresh.sub.Close()
	raceView.sub.Close()
	raceScope.close(t)
	// Queue exhaustion terminates only that view. The other watch, admitted
	// avatar, Java clients and clock must all continue on this attachment.
	avatar := admit(t, ctx, h.scope.core, profile("WorldAvatar", 8), player.Options{})
	slow := watch(t, ctx, b, worldview.Request{Dimension: nether, Bounds: box, Limits: worldview.Limits{Messages: 1, Bytes: 4096}})
	for i := 0; i < 4; i++ {
		state := airKey
		if i%2 == 1 {
			state = stoneKey
		}
		edit(t, ctx, b, nether, racePos, state, nil)
		v.until(t, racePos, state, nil)
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRead()
	if _, e := slow.sub.Next(readCtx); !errors.Is(e, worldview.ErrOverflow) {
		t.Fatalf("slow view terminal%v", e)
	}
	blockFence(t, h, java)
	nativeImage(t, v, java)
	move := avatar.self.Pose
	move.X += .125
	avatar.move(t, move)
	snap, e := h.scope.core.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tick := snap.Tick
	h.s.StepTicks(1)
	snap, e = h.scope.core.Snapshot(ctx)
	if e != nil || snap.Tick != tick+1 {
		t.Fatal("overflow blocked runtime clock")
	}
	expected := append([]registry.BlockStateID(nil), v.cells...)
	h.scope.close(t)
	if _, e := v.sub.Next(ctx); !errors.Is(e, worldview.ErrClosed) {
		t.Fatalf("detached view%v", e)
	}
	out, e := b.EditBlock(ctx, worldview.Edit{Dimension: nether, Position: trap, State: key(t, "minecraft:diamond_block")})
	if out.Applied || !errors.Is(e, worldview.ErrClosed) {
		t.Fatalf("detached edit%+v %v", out, e)
	}
	h.scope = attach(t, h.s)
	b = blocksCapability(t, h.scope.core)
	fresh = watch(t, ctx, b, r)
	for i, id := range expected {
		oldKey, _ := v.catalog.State(id)
		newKey, _ := fresh.catalog.State(fresh.cells[i])
		if oldKey != newKey {
			t.Fatal("detached edit altered world")
		}
	}
	blockFence(t, h, java)
	nativeImage(t, fresh, java)
	// A newer block remains canonical and reaches776, while only the775 viewer
	// is retired for lacking an exact wire representation. This watch stays live.
	newer := worldview.Pos{X: -1, Y: 72, Z: -1}
	edit(t, ctx, b, nether, newer, "minecraft:cinnabar", nil)
	fresh.until(t, newer, "minecraft:cinnabar", nil)
	// Native Java block replication retains its tick-end publication boundary.
	// Advance that boundary before measuring retirement in this frozen host.
	h.s.StepTicks(1)
	deadline, cancelClose := context.WithTimeout(ctx, 10*time.Second)
	defer cancelClose()
	for {
		_, e := j0.client.ReadPacket(deadline)
		if e != nil {
			if deadline.Err() != nil {
				t.Fatal("missing775 state did not retire viewer")
			}
			if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
				t.Fatal("timeout is not viewer retirement")
			}
			break
		}
	}
	blockFence(t, h, java[1:])
	java[1].expect(t, newer, "minecraft:cinnabar")
	fresh.expect(t, newer, "minecraft:cinnabar")
}
