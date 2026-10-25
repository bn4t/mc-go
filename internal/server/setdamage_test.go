package server

import (
	"testing"

	"mc-go/internal/world"
)

// Vanilla 26.2's set_damage fractions read back exact damages on an
// iron pickaxe (max 250): 0.0/0.1/0.2/0.5/0.8/0.9 wear to
// 250/225/200/125/49/25. The float32 truncation is the only rounding
// fitting 0.8 -> 49 and 0.1 -> 225 at once; the rest of the behavior
// (clamp, damageable-only, no-op shapes) is documented guesses.
func TestApplySetDamage(t *testing.T) {
	pick, ok := world.ItemID("iron_pickaxe")
	if !ok {
		t.Fatal("no iron_pickaxe id")
	}
	dirt, ok := world.ItemID("dirt")
	if !ok {
		t.Fatal("no dirt id")
	}
	for _, tc := range []struct {
		f    string
		want int32
	}{
		{"0.0", 250},
		{"0.1", 225},
		{"0.2", 200},
		{"0.5", 125},
		{"0.8", 49},
		{"0.9", 25},
		{"1.0", 0},
		{"1", 0},
	} {
		got, ok := applySetDamage(`{"function":"set_damage","damage":`+tc.f+`}`,
			ItemStack{ID: pick, Count: 1})
		if !ok {
			t.Fatalf("f=%s did not apply", tc.f)
		}
		if got.Damage != tc.want {
			t.Errorf("f=%s: damage=%d want %d", tc.f, got.Damage, tc.want)
		}
		if got.Count != 1 || got.ID != pick {
			t.Errorf("f=%s: stack rewritten to %+v", tc.f, got)
		}
	}
	for _, tok := range []string{
		`{"function":"set_count","count":3}`,
		`[{"function":"set_damage","damage":0.5}`,
		`{"function":"set_damage"}`,
		`{"function":"set_damage","damage":0.5,"conditions":[]}`,
		`{"function":"minecraft:set_damage","damage":"0.5"}`,
	} {
		if _, ok := applySetDamage(tok, ItemStack{ID: pick, Count: 1}); ok {
			t.Errorf("%s: applied, want no-op", tok)
		}
	}
	if _, ok := applySetDamage(`{"function":"set_damage","damage":0.5}`,
		ItemStack{ID: dirt, Count: 1}); ok {
		t.Error("dirt: applied, want no-op")
	}
	if _, ok := applySetDamage(`{"function":"set_damage","damage":0.5}`,
		ItemStack{}); ok {
		t.Error("empty: applied, want no-op")
	}
	if got, ok := applySetDamage(`{"function":"set_damage","damage":9}`,
		ItemStack{ID: pick, Count: 1}); !ok || got.Damage != 0 {
		t.Errorf("damage 9: got %+v,%v want damage 0 clamp", got, ok)
	}
}
