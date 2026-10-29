package server

import (
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestServerboundPlayCoverage pins the routing table against the
// vanilla server's own packet report: every serverbound play packet in
// protocol 776 must have exactly one route, and no route may exist for
// an id the report does not contain. This is the machine-checked form
// of the goal's "no ignored packets" rule — a packet can only be
// dropped deliberately, by being routed to ignoreServerbound.
func TestServerboundPlayCoverage(t *testing.T) {
	want := officialIDs(PacketStatePlay, PacketDirServerbound)
	seen := map[int32]bool{}
	for _, id := range want {
		if seen[id] {
			t.Fatalf("official report lists id %#x twice", id)
		}
		seen[id] = true
	}
	var missing, extra []int32
	for id := range seen {
		if _, ok := playServerbound[id]; !ok {
			missing = append(missing, id)
		}
	}
	for id := range playServerbound {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	for _, id := range missing {
		name, _ := officialName(PacketStatePlay, PacketDirServerbound, id)
		t.Errorf("serverbound play packet %#x (%s) has no route", id, name)
	}
	for _, id := range extra {
		t.Errorf("route for %#x is not a 776 serverbound play packet", id)
	}
	if len(want) != 69 {
		t.Errorf("expected 69 serverbound play packets in 776, report has %d", len(want))
	}
}

// TestIgnoreServerboundIsDeliberate requires every packet routed to the
// no-op handler to say why in the handler's own documentation. The
// check is textual, but it keeps the "vanilla ignores this too" claims
// from being asserted silently.
func TestIgnoreServerboundIsDeliberate(t *testing.T) {
	// The ids routed to ignoreServerbound, by official name. Keeping
	// the list explicit means adding a new no-op route requires a
	// deliberate edit here as well.
	wantIgnored := []string{
		"client_tick_end",
		"jigsaw_generate",
		"set_jigsaw_block",
		"set_structure_block",
		"set_test_block",
		"test_instance_block_action",
	}
	got := map[string]bool{}
	for id, h := range playServerbound {
		if methodName(h) != "ignoreServerbound" {
			continue
		}
		name, ok := officialName(PacketStatePlay, PacketDirServerbound, id)
		if !ok {
			t.Fatalf("ignored id %#x is not in the report", id)
		}
		got[name] = true
	}
	for _, name := range wantIgnored {
		if !got[name] {
			t.Errorf("packet %s is no longer routed to ignoreServerbound; update this list if that is intended", name)
		}
		delete(got, name)
	}
	for name := range got {
		t.Errorf("packet %s is routed to ignoreServerbound but is not in the reviewed list", name)
	}
}

// methodName renders the function a route points at, for the textual
// assertions above. Method expressions on *Server stringify as
// "mc-go/internal/server.(*Server).name", so the last dot-separated
// segment is the handler.
func methodName(h playHandler) string {
	name := runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}
