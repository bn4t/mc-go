package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestBlockTagPredicateVaguePropertiesAndNBT(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	s.simulationAction(func() {
		chest, _ := world.BlockStateOf("chest", map[string]string{"facing": "north"})
		s.setSilentBlock(d, p, chest)
		for _, tc := range []struct {
			spec string
			want bool
		}{
			{`#guarded_by_piglins`, true}, {`#minecraft:guarded_by_piglins[facing=north]{}`, true},
			{`#guarded_by_piglins[facing=east]`, false}, {`#guarded_by_piglins[facing=sideways]`, false},
			{`#guarded_by_piglins[Facing=north]`, false}, {`#guarded_by_piglins[facing=North]`, false},
			{`#guarded_by_piglins["facing"="north"]`, true},
			{`#guarded_by_piglins["unknown,{"="x"]`, false},
			{`#guarded_by_piglins[ facing = north, waterlogged = false ]`, true},
			{`#guarded_by_piglins[facing=north,]`, true},
			{`#guarded_by_piglins[facing=]`, false}, {`#guarded_by_piglins[=north]`, false},
			{`#guarded_by_piglins{Items:[]}`, true},
		} {
			predicate, err := parseBlockTagPredicate(tc.spec, 4)
			if err != nil {
				t.Errorf("parse%s", tc.spec)
				continue
			}
			if got := predicate.matches(s, d, p); got != tc.want {
				t.Errorf("%s got%v want%v", tc.spec, got, tc.want)
			}
		}
		gold, _ := world.BlockState("gold_block")
		s.setSilentBlock(d, p, gold)
		predicate, err := parseBlockTagPredicate(`#guarded_by_piglins{}`, 4)
		if err != nil || predicate.matches(s, d, p) {
			t.Fatal("empty tag NBT matched a non-BE member")
		}
	})
}
func TestBlockTagPredicateParserFailures(t *testing.T) {
	for _, spec := range []string{
		`#minecraft:no_such_block_tag`, `#mine/craft:guarded_by_piglins`,
		`#guarded_by_piglins[facing=north,facing=south]`, `#guarded_by_piglins[facing]`,
		`#guarded_by_piglins[facing=north`, `#guarded_by_piglins[facing="nor\uth"]`,
	} {
		if _, err := parseBlockTagPredicate(spec, 4); err == nil {
			t.Errorf("invalid accepted%s", spec)
		}
	}
	value, next, err := readBlockPropertyString(`"a\\b"`, 0)
	if err != nil || value != "a\\b" || next != 6 {
		t.Fatalf("quoted backslash value%q next%d err%v", value, next, err)
	}
}

func TestBlockTagTrailingCommaWhitespaceBoundary(t *testing.T) {
	for _, spec := range []string{`#guarded_by_piglins[ ]`, "#guarded_by_piglins[\n]", "#guarded_by_piglins[facing=north\n]", `#guarded_by_piglins[facing=north,]`} {
		if _, err := parseBlockTagPredicate(spec, 4); err != nil {
			t.Errorf("valid whitespace rejected%q", spec)
		}
	}
	for _, spec := range []string{`#guarded_by_piglins[facing=north, ]`, "#guarded_by_piglins[facing=north,\n]"} {
		if _, err := parseBlockTagPredicate(spec, 4); err == nil {
			t.Errorf("comma-separated empty property accepted%q", spec)
		}
	}
}
