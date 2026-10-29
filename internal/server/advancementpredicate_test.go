package server

import (
	"encoding/json"
	"testing"
)

func advancementPredicateJSON(t *testing.T, text string) any {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAdvancementPredicateUnknownDoesNotInvertToAward(t *testing.T) {
	unknown := advancementPredicateConstant(advancementPredicateUnknown)
	yes := advancementPredicateConstant(advancementPredicateTrue)
	no := advancementPredicateConstant(advancementPredicateFalse)
	for _, c := range []struct {
		name string
		test advancementPlayerPredicate
		want advancementPredicateResult
	}{
		{"and undecided", advancementPredicateAll([]advancementPlayerPredicate{yes, unknown}), advancementPredicateUnknown},
		{"and false", advancementPredicateAll([]advancementPlayerPredicate{unknown, no}), advancementPredicateFalse},
		{"or undecided", advancementPredicateAny([]advancementPlayerPredicate{no, unknown}), advancementPredicateUnknown},
		{"or true", advancementPredicateAny([]advancementPlayerPredicate{unknown, yes}), advancementPredicateTrue},
	} {
		if got := c.test(nil, nil); got != c.want {
			t.Errorf("%s got%d want%d", c.name, got, c.want)
		}
	}
	predicate, err := compileAdvancementContextCondition(advancementPredicateJSON(t, `{"condition":"minecraft:inverted","term":{"condition":"minecraft:weather_check"}}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := predicate(nil, nil); got != advancementPredicateUnknown {
		t.Fatalf("inverted unmodeled predicate became%d", got)
	}
}

func TestAdvancementTickPredicateRejectsInvalidNativeSchema(t *testing.T) {
	for _, raw := range []string{
		`7`,
		`{"player":7}`,
		`{"player":{"unknown:predicate":{}}}`,
		`{"player":[{"condition":"unknown:condition"}]}`,
		`{"player":{"minecraft:type_specific/player":{"gamemode":"survival"}}}`,
		`{"player":{"minecraft:type_specific/player":{"gamemode":["unknown"]}}}`,
		`{"player":{"minecraft:flags":{"is_baby":"false"}}}`,
		`{"player":{"minecraft:nbt":"[1,2]"}}`,
		`{"player":{"minecraft:entity_type":"unknown:not_registered"}}`,
		`{"player":{"minecraft:type_specific/player":{"level":{"min":3,"max":2}}}}`,
	} {
		if _, err := compileAdvancementTickPredicate(advancementPredicateJSON(t, raw)); err == nil {
			t.Errorf("accepted invalid predicate%s", raw)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"player":[]}`, `{"player":{}}`, `{"player":{"minecraft:type_specific/player":{"gamemode":["survival"]}}}`} {
		if _, err := compileAdvancementTickPredicate(advancementPredicateJSON(t, raw)); err != nil {
			t.Errorf("rejected valid predicate%s: %v", raw, err)
		}
	}
}

func TestAdvancementPredicateRangeIncludesEndpoints(t *testing.T) {
	test, err := compileAdvancementRange(advancementPredicateJSON(t, `{"min":2,"max":3}`), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		value float64
		want  bool
	}{{1, false}, {2, true}, {3, true}, {4, false}} {
		if got := test(c.value); got != c.want {
			t.Errorf("bound%g got%v want%v", c.value, got, c.want)
		}
	}
	for _, c := range []struct {
		input float64
		want  float64
	}{{2.5, 2}, {-2.5, -2}, {2147483648, -2147483648}} {
		test, err := compileAdvancementRange(c.input, true)
		if err != nil || !test(c.want) {
			t.Errorf("integer codec%g did not become%g: %v", c.input, c.want, err)
		}
	}
}

func TestAdvancementPredicateCompletedListenersAreNotEvaluated(t *testing.T) {
	calls := 0
	s := &Server{advancementPack: &advancementRegistry{tickers: map[string]map[string]advancementPlayerPredicate{"test:tick": {"clock": func(*Server, *Session) advancementPredicateResult { calls++; return advancementPredicateFalse }}}}}
	sess := &Session{advDone: map[string]bool{"test:tick": true}, advCrits: map[string]map[string]int64{}}
	s.fireTickAdvancements(sess)
	sess.advDone["test:tick"] = false
	sess.advCrits["test:tick"] = map[string]int64{"clock": 1}
	s.fireTickAdvancements(sess)
	if calls != 0 {
		t.Fatalf("completed/obtained listener evaluated%d times", calls)
	}
	delete(sess.advCrits["test:tick"], "clock")
	s.fireTickAdvancements(sess)
	if calls != 1 {
		t.Fatalf("revoked listener evaluated%d times", calls)
	}
}

func TestAdvancementBiomePredicateReadsPositionBiome(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "PredicateBiome", .5, 160, .5)
	d := sess.dimension()
	d.w.SetBiome("forest")
	if d.biomes == nil {
		d.biomes = map[[3]int]string{}
	}
	for x := -2; x <= 2; x++ {
		for y := 38; y <= 42; y++ {
			for z := -2; z <= 2; z++ {
				d.biomes[[3]int{x, y, z}] = "desert"
			}
		}
	}
	predicate, err := compileAdvancementTickPredicate(advancementPredicateJSON(t, `{"player":{"minecraft:location":{"biomes":"minecraft:desert"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := predicate(s, sess); got != advancementPredicateTrue {
		t.Fatalf("local biome predicate=%d wantedtrue", got)
	}
	sess.setPos(256.5, 160, 256.5, 0, 0, true)
	if got := predicate(s, sess); got != advancementPredicateFalse {
		t.Fatalf("distant biome predicate=%d wantedfalse", got)
	}
}

func TestAdvancementNBTPlayerTagsUseSharedSaveView(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "PredicateTags", .5, 160, .5)
	predicate, err := compileAdvancementTickPredicate(advancementPredicateJSON(t, `{"player":{"minecraft:nbt":"{Tags:[\"predicate_gate\"]}"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if predicate(s, sess) != advancementPredicateFalse {
		t.Fatal("missing tag matched")
	}
	sess.tags = []string{"unrelated", "predicate_gate"}
	if predicate(s, sess) != advancementPredicateTrue {
		t.Fatal("native NBT view omitted player tags")
	}
	sess.tags = nil
	if _, exists := playerSaveMap(sess, s.clock.count, s.entities)["Tags"]; exists {
		t.Fatal("empty tag set must be omitted")
	}
}

func TestAdvancementPositionNullAxisIsUnbounded(t *testing.T) {
	predicate, err := compileAdvancementTickPredicate(advancementPredicateJSON(t, `{"player":{"minecraft:location":{"position":{"x":null,"y":{"min":0},"z":null}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	sess := &Session{}
	sess.setPos(100, 1, -100, 0, 0, true)
	if predicate(nil, sess) != advancementPredicateTrue {
		t.Fatal("null axes constrained position")
	}
	sess.setPos(100, -1, -100, 0, 0, true)
	if predicate(nil, sess) != advancementPredicateFalse {
		t.Fatal("non-null axis constraint ignored")
	}
}
