package server

import (
	"encoding/json"
	"testing"
)

func TestAdvancementJSONIntegerRetainsDecimalPrecision(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int32
	}{
		{"2.5", 2}, {"-2.5", -2}, {"4294967303", 7}, {"9007199254740993", 1}, {"9.007199254740993e15", 1}, {"1e40", 0}, {"123.456e2", 12345}, {"4294967295", -1}, {"-4294967297", -1}, {"-2147483649", 2147483647},
	} {
		got, err := advancementJSONInt(json.Number(c.raw))
		if err != nil || got != c.want {
			t.Errorf("%s got%d err%v want%d", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []any{true, "2", json.Number("1e2147483648"), json.Number("1.0e-2147483648")} {
		if _, err := advancementJSONInt(raw); err == nil {
			t.Errorf("accepted invalid JSON integer%v", raw)
		}
	}
}

func TestAdvancementJSONKeepsIntegerTokensForMetadata(t *testing.T) {
	raw, err := decodeAdvancementJSON([]byte(`{"criteria":{"clock":{"trigger":"minecraft:tick","conditions":{"player":{"minecraft:type_specific/player":{"level":{"min":9007199254740994,"max":9007199254740995}}}}}},"rewards":{"experience":9007199254740999},"display":{"title":"number codec","description":"exact tokens","icon":{"id":"minecraft:paper","count":9007199254740995}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !validPackAdvancement(raw) {
		t.Fatal("valid integer codec definition rejected")
	}
	token, ok := raw["rewards"].(map[string]any)["experience"].(json.Number)
	if !ok || token.String() != "9007199254740999" {
		t.Fatalf("experience token rounded: %T %v", token, token)
	}
	meta := parseGameAdvs(map[string]map[string]any{"test:numbers": raw})["test:numbers"]
	if meta.experience != 7 {
		t.Fatalf("reward XP metadata%d wanted7", meta.experience)
	}
	display, err := parseAdvancementDisplay(raw["display"])
	if err != nil || display.icon.Count != 3 {
		t.Fatalf("icon metadata%v err%v wantedcount3", display, err)
	}
	criteria := raw["criteria"].(map[string]any)["clock"].(map[string]any)
	bounds := criteria["conditions"].(map[string]any)["player"].(map[string]any)["minecraft:type_specific/player"].(map[string]any)["level"].(map[string]any)
	if token, ok := bounds["min"].(json.Number); !ok || token.String() != "9007199254740994" {
		t.Fatalf("tick bound token rounded: %T %v", token, token)
	}
}
