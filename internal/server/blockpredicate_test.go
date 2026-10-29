package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

func TestBlockPredicateTypedNBTAndMetadata(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	s.simulationAction(func() {
		state, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "save"})
		s.setSilentBlock(d, p, state)
		g := s.structureBlockAt(d, p)
		g.fields["sizeX"] = int32(2)
		g.fields["metadata"] = "MiXeD"
		g.fields["powered"] = int8(1)
		for _, tc := range []struct {
			spec string
			want bool
		}{
			{`structure_block[mode=save]{sizeX:2,metadata:"MiXeD"}`, true},
			{`structure_block{sizeX:7}`, false}, {`structure_block{sizeX:2b}`, false},
			{`structure_block{powered:true}`, true}, {`structure_block{powered:1}`, false},
			{`structure_block{metadata:"mixed"}`, false}, {`structure_block{}`, true},
			{`structure_block{id:"minecraft:structure_block",x:4,y:-60,z:4}`, true},
			{`structure_block{keepPacked:0b}`, false},
		} {
			_, _, pattern, err := parseBlockPredicateNBT(tc.spec, 4)
			if err != nil {
				t.Fatalf("parse%s", tc.spec)
			}
			if got := s.blockPredicateNBTMatches(d, p, pattern); got != tc.want {
				t.Errorf("%s got%v want%v", tc.spec, got, tc.want)
			}
		}
		stone, _ := world.BlockState("stone")
		s.setSilentBlock(d, p, stone)
		if s.blockPredicateNBTMatches(d, p, map[string]any{}) {
			t.Fatal("empty compound matched non-BE")
		}
	})
}
func TestNBTSubsetEmptyAndExistentialLists(t *testing.T) {
	list := func(items ...any) nbt.List { return nbt.List{Elem: nbt.TagCompound, Items: items} }
	a := map[string]any{"x": int32(1)}
	b := map[string]any{"x": int32(2)}
	for _, tc := range []struct {
		target, pattern nbt.List
		want            bool
	}{
		{list(), list(), true}, {list(a), list(), false}, {list(), list(a), false},
		{list(a, b), list(b, a), true}, {list(a, b), list(a, a), true}, {list(a, b), list(a, a, a), false},
	} {
		if got := nbtPartialEqual(tc.target, tc.pattern); got != tc.want {
			t.Errorf("target%v pattern%v got%v", tc.target, tc.pattern, got)
		}
	}
}
func TestBlockPredicateSNBTWhitespaceAndBoundaries(t *testing.T) {
	for _, spec := range []string{
		`structure_block{ sizeX : 2 , metadata : "MiXeD" }`,
		`chest{ Items : [ { Slot : 0b } , { Slot : 2b } ] }`,
		`structure_block{ missing : [ I; 1 , 2 ] }`,
		"structure_block{\u2003sizeX:\u20032}",
	} {
		if _, _, _, err := parseBlockPredicateNBT(spec, 4); err != nil {
			t.Errorf("whitespace parse failed%s", spec)
		}
	}
	for _, spec := range []string{`structure_block{sizeX:1 2}`, `structure_block{sizeX:2}trailing`, `structure_block{`} {
		if _, _, _, err := parseBlockPredicateNBT(spec, 4); err == nil {
			t.Errorf("invalid accepted%s", spec)
		}
	}
	for _, r := range []rune{'\t', '\n', 0x1c, 0x1680, 0x2003, 0x2028, 0x3000} {
		s := string(r) + "x"
		if snbtSkipWhitespace(s, 0) != len(string(r)) {
			t.Errorf("Java whitespace%U not skipped", r)
		}
	}
	for _, r := range []rune{0x85, 0xa0, 0x2007, 0x202f} {
		if snbtSkipWhitespace(string(r)+"x", 0) != 0 {
			t.Errorf("nonbreaking/NEL%U skipped", r)
		}
	}
}
