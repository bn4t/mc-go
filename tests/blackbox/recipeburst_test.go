package blackbox

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"mc-go/internal/server"
)

// canonRecipes canonicalizes an Update Recipes payload by sorting its
// leading property-set block by set id, and the item ids inside each
// set numerically. Both orders are per-boot nondeterministic on the
// live server, while the sets as multisets and the recipe tail after
// them are stable, so byte equality is only meaningful after this
// normalization.
func canonRecipes(p []byte) []byte {
	i := 0
	rd := func() int {
		s, sh := 0, uint(0)
		for {
			b := p[i]
			i++
			s |= int(b&0x7f) << sh
			if b&0x80 == 0 {
				return s
			}
			sh += 7
		}
	}
	enc := func(v int) []byte {
		var b []byte
		for {
			c := byte(v & 0x7f)
			v >>= 7
			if v != 0 {
				b = append(b, c|0x80)
			} else {
				return append(b, c)
			}
		}
	}
	n := rd()
	headEnd := i
	type pset struct {
		id  string
		raw []byte
	}
	sets := make([]pset, 0, n)
	for range n {
		m := rd()
		id := string(p[i : i+m])
		idraw := append([]byte(nil), p[i-len(enc(m)):i+m]...)
		i += m
		k := rd()
		items := make([]int, 0, k)
		for ; k > 0; k-- {
			items = append(items, rd())
		}
		slices.Sort(items)
		raw := append(idraw, enc(len(items))...)
		for _, it := range items {
			raw = append(raw, enc(it)...)
		}
		sets = append(sets, pset{id, raw})
	}
	slices.SortFunc(sets, func(a, b pset) int { return strings.Compare(a.id, b.id) })
	res := append([]byte(nil), p[:headEnd]...)
	for _, s := range sets {
		res = append(res, s.raw...)
	}
	return append(res, p[i:]...)
}

// TestRecipeBurstParity compares the recipe packets of a fresh join
// byte for byte: the Update Recipes registry, the book settings, and
// the first book add. The registry is a static capture served
// verbatim, so any drift from the live server fails here.
func TestRecipeBurstParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	grab := func(addr string) map[int32][]byte {
		t.Helper()
		c, pr, _ := joinPlayBlackbox(t, addr, "RecipeBurst")
		defer c.Close()
		out := map[int32][]byte{}
		for i := 0; i < 40 && len(out) < 3; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			switch p.ID {
			case server.IDPlayUpdateRecipes, server.IDPlayRecipeBookAdd, server.IDPlayRecipeBookSet:
				if _, ok := out[p.ID]; !ok {
					out[p.ID] = append([]byte(nil), p.Data...)
				}
			}
		}
		return out
	}
	want := grab(tg.vanilla)
	got := grab(tg.mcgo)
	for _, id := range []int32{server.IDPlayUpdateRecipes, server.IDPlayRecipeBookSet, server.IDPlayRecipeBookAdd} {
		w, g := want[id], got[id]
		if id == server.IDPlayUpdateRecipes {
			w, g = canonRecipes(w), canonRecipes(g)
		}
		if !bytes.Equal(w, g) {
			n := min(len(w), len(g))
			at := n
			for i := range n {
				if w[i] != g[i] {
					at = i
					break
				}
			}
			lo := max(0, at-8)
			t.Errorf("packet %#x: %d vanilla bytes vs %d mc-go bytes, first diff at %d:\nwant %x\ngot  %x", id, len(w), len(g), at, w[lo:min(len(w), at+8)], g[lo:min(len(g), at+8)])
		}
	}
}
