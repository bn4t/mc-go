package server

import "testing"

func TestAdvancementPlayerProgressPredicatesUseSessionRegistry(t *testing.T) {
	const id = "test:control"
	s := &Server{advancementPack: &advancementRegistry{entries: map[string]*advMeta{id: {id: id, criteria: []string{"a", "b"}}}}}
	sess := &Session{server: s, advDone: map[string]bool{}, advCrits: map[string]map[string]int64{id: {"a": 1}}}
	for _, c := range []struct {
		body string
		want advancementPredicateResult
	}{
		{`{"test:control":false}`, advancementPredicateTrue},
		{`{"test:control":true}`, advancementPredicateFalse},
		{`{"test:control":{"a":true,"b":false}}`, advancementPredicateTrue},
		{`{"test:control":{}}`, advancementPredicateTrue},
		{`{"test:missing":false}`, advancementPredicateFalse},
		{`{"test:control":{"missing":false}}`, advancementPredicateFalse},
	} {
		predicate, err := compileAdvancementProgressPredicate(advancementPredicateJSON(t, c.body))
		if err != nil {
			t.Fatal(err)
		}
		if got := predicate(nil, sess); got != c.want {
			t.Errorf("%s got%d want%d", c.body, got, c.want)
		}
	}
	predicate, err := compileAdvancementProgressPredicate(advancementPredicateJSON(t, `{"test:control":false}`))
	if err != nil {
		t.Fatal(err)
	}
	sess.server = &Server{advancementPack: &advancementRegistry{entries: map[string]*advMeta{}}}
	if predicate(nil, sess) != advancementPredicateFalse {
		t.Fatal("unknown false state crossed server registry scope")
	}
}

func TestAdvancementProgressPredicateRejectsMalformedStates(t *testing.T) {
	for _, body := range []string{`[]`, `{"test:control":7}`, `{"test:control":null}`, `{"test:control":{"a":"false"}}`, `{"BAD ID":false}`} {
		if _, err := compileAdvancementProgressPredicate(advancementPredicateJSON(t, body)); err == nil {
			t.Errorf("accepted malformed progress predicate%s", body)
		}
	}
}

func TestAdvancementReferenceIdentifierCodecFacts(t *testing.T) {
	for _, c := range []struct{ raw, want string }{{"foo", "minecraft:foo"}, {":foo", "minecraft:foo"}, {"", "minecraft:"}, {"ns:", "ns:"}, {"ns:a/../b", "ns:a/../b"}} {
		got, err := advancementReferenceID(c.raw)
		if err != nil || got != c.want {
			t.Errorf("%q got%q err%v want%q", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []string{"Upper:foo", "bad id", "a:b:c", "..:foo"} {
		if _, err := advancementReferenceID(raw); err == nil {
			t.Errorf("accepted malformed reference%q", raw)
		}
	}
}
