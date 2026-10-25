package server

import (
	"reflect"
	"testing"

	"mc-go/internal/world"
)

// The emit orders below were captured from a vanilla 26.2 server: join
// (four ticked time stats), then a dirt drop adds drop+dropped entries.
// They pin the HashSet-copy → Object2IntOpenHashMap emit model.
func TestStatEmitOrder(t *testing.T) {
	ids := CustomStatIDs
	tsr := statKey{statCustom, ids["time_since_rest"]}
	pt := statKey{statCustom, ids["play_time"]}
	tsd := statKey{statCustom, ids["time_since_death"]}
	twt := statKey{statCustom, ids["total_world_time"]}
	drop := statKey{statCustom, ids["drop"]}
	dirtItem, ok := world.ItemID("dirt")
	if !ok {
		t.Fatal("dirt item id missing")
	}
	dirt := statKey{statDropped, dirtItem}

	// Join: the four time stats award in tick order.
	got := statEmitOrder([]statKey{pt, twt, tsd, tsr})
	want := []statKey{tsr, pt, tsd, twt}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("join order %v, want %v", got, want)
	}

	// After a drop: tsd entered the dirty set on tick one, the drop
	// stats much later — the shared HashSet bucket keeps tsd ahead of
	// dirt, and that feeds the collision probe.
	got = statEmitOrder([]statKey{pt, twt, tsd, tsr, drop, dirt})
	want = []statKey{tsr, dirt, pt, tsd, twt, drop}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drop order %v, want %v", got, want)
	}
}
