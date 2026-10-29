package server

import "testing"

func TestCustomAdvancementSelectorsUseServerRegistry(t *testing.T) {
	const id = "test:custom"
	first := &Server{advancementPack: &advancementRegistry{entries: map[string]*advMeta{id: {id: id, criteria: []string{"a", "b"}}}}}
	second := &Server{advancementPack: &advancementRegistry{entries: map[string]*advMeta{}}}
	sess := &Session{server: first, advCrits: map[string]map[string]int64{id: {"a": 1}}, advDone: map[string]bool{}}
	no, yes := false, true
	for _, c := range []struct {
		name  string
		query selAdv
		want  bool
	}{
		{"partial", selAdv{id: id, done: &no}, true},
		{"criteria", selAdv{id: id, crits: []selTerm{{val: "a"}, {val: "b", neg: true}}}, true},
		{"empty criteria", selAdv{id: id}, true},
		{"missing criterion false", selAdv{id: id, crits: []selTerm{{val: "missing", neg: true}}}, false},
		{"unknown id false", selAdv{id: "test:missing", done: &no}, false},
		{"unknown empty", selAdv{id: "test:missing"}, false},
	} {
		if got := sess.advMatch(c.query); got != c.want {
			t.Errorf("%s got%v want%v", c.name, got, c.want)
		}
	}
	sess.advDone[id] = true
	if !sess.advMatch(selAdv{id: id, done: &yes}) {
		t.Fatal("completed server-owned advancement did not match")
	}
	sess.server = second
	if sess.advMatch(selAdv{id: id, done: &yes}) || sess.advMatch(selAdv{id: id, done: &no}) {
		t.Fatal("progress matched an id outside the session's server registry")
	}
}
