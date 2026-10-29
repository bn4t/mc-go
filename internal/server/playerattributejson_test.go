package server

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"mc-go/internal/nbt"
)

func TestPlayerAttributeJSONNonfiniteAndNativeBits(t *testing.T) {
	values := []float64{math.Float64frombits(0x7ff8000000000123), math.Inf(1), math.Inf(-1), math.Copysign(0, -1), 2048, .2}
	for _, value := range values {
		number := playerAttributeJSONNumber(value)
		p := playerPersist{WalkSpeed: &number, Attributes: []playerAttributePersist{{Name: "scale", Base: value, Modifiers: []playerAttributeModifierPersist{{ID: "test:raw", Amount: value, Operation: 2}}}}}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var decoded playerPersist
		if err = json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		for _, got := range []float64{decoded.Attributes[0].Base, decoded.Attributes[0].Modifiers[0].Amount, float64(*decoded.WalkSpeed)} {
			if math.Float64bits(got) != math.Float64bits(value) {
				t.Fatalf("JSON bits %x want%x", math.Float64bits(got), math.Float64bits(value))
			}
		}
		var raw bytes.Buffer
		if err = nbt.WriteNamed(&raw, "", playerDatCompound(p)); err != nil {
			t.Fatal(err)
		}
		_, compound, err := nbt.ReadNamed(&raw)
		if err != nil {
			t.Fatal(err)
		}
		native := playerFromDat(compound.(map[string]any))
		for _, got := range []float64{native.Attributes[0].Base, native.Attributes[0].Modifiers[0].Amount} {
			if math.Float64bits(got) != math.Float64bits(value) {
				t.Fatalf("NBT bits %x want%x", math.Float64bits(got), math.Float64bits(value))
			}
		}
	}
	var legacy playerPersist
	if err := json.Unmarshal([]byte(`{"attributes":[{"name":"scale","base":2,"modifiers":[{"id":"test:save","amount":0.5,"operation":1}]}]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Attributes[0].Base != 2 || legacy.Attributes[0].Modifiers[0].Amount != .5 {
		t.Fatal("finite legacy fields changed")
	}
}
