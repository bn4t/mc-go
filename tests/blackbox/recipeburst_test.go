package blackbox

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// progCrit is one criterion's progress in an Update Advancements packet.
type progCrit struct {
	name     string
	achieved bool
	date     int64
}

// parseProgress splits an Update Advancements payload into its static
// definition bytes and its progress entries. The definitions are an
// opaque static blob on both servers; only the progress map is
// compared structurally, because the live server shuffles the entry
// order per boot and stamps the award time with its own clock.
func parseProgress(t *testing.T, p []byte) (defs []byte, entries map[string][]progCrit, show byte) {
	t.Helper()
	const progAt = 148 // measured: reset + definitions + remove-count
	i := progAt
	rd := func() int {
		s, sh := 0, uint(0)
		for {
			if i >= len(p) {
				t.Fatalf("advancements payload ends at %d/%d", i, len(p))
			}
			b := p[i]
			i++
			s |= int(b&0x7f) << sh
			if b&0x80 == 0 {
				return s
			}
			sh += 7
		}
	}
	str := func() string {
		m := rd()
		if i+m > len(p) {
			t.Fatalf("advancements string overruns payload")
		}
		s := string(p[i : i+m])
		i += m
		return s
	}
	defs = p[:progAt]
	entries = map[string][]progCrit{}
	for n := rd(); n > 0; n-- {
		id := str()
		var crits []progCrit
		for c := rd(); c > 0; c-- {
			nm := str()
			if i >= len(p) {
				t.Fatalf("advancements criterion overruns payload")
			}
			ach := p[i] != 0
			i++
			var d int64
			if ach {
				if i+8 > len(p) {
					t.Fatalf("advancements date overruns payload")
				}
				for k := range 8 {
					d = d<<8 | int64(p[i+k])
				}
				i += 8
			}
			crits = append(crits, progCrit{nm, ach, d})
		}
		entries[id] = crits
	}
	if i != len(p)-1 {
		t.Fatalf("advancements entries end at %d, want %d (layout changed, re-measure)", i, len(p)-1)
	}
	return defs, entries, p[len(p)-1]
}

// TestSpawnUnlockParity compares the post-spawn recipe unlock and the
// advancement sync fresh-join to fresh-join. The unlock add is
// boot-stable bytes; the advancement definitions are boot-stable while
// the progress entry order shuffles per boot and the award timestamp
// follows each server's own clock, so progress is compared by entry
// with the timestamp checked for recency instead of equality.
func TestSpawnUnlockParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	start := time.Now().UnixMilli()
	collect := func(addr string) (adds [][]byte, adv []byte) {
		t.Helper()
		c, pr, _ := joinPlayBlackbox(t, addr, "SpawnUnlock")
		defer c.Close()
		for i := 0; i < 60 && (len(adds) < 2 || adv == nil); i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			switch p.ID {
			case server.IDPlayRecipeBookAdd:
				adds = append(adds, append([]byte(nil), p.Data...))
			case server.IDPlayUpdateAdvance:
				if adv == nil {
					adv = append([]byte(nil), p.Data...)
				}
			}
		}
		return adds, adv
	}
	vAdds, vAdv := collect(tg.vanilla)
	mAdds, mAdv := collect(tg.mcgo)
	if len(vAdds) < 2 || len(mAdds) < 2 {
		t.Fatalf("book adds: vanilla %d, mc-go %d, want 2 each", len(vAdds), len(mAdds))
	}
	if !bytes.Equal(vAdds[1], mAdds[1]) {
		t.Errorf("unlock add: %d vanilla bytes vs %d mc-go bytes", len(vAdds[1]), len(mAdds[1]))
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2", "play", "recipe_book_unlock.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vAdds[1], committed) {
		t.Errorf("unlock add drifted from the committed capture")
	}
	vDefs, vEnt, vShow := parseProgress(t, vAdv)
	mDefs, mEnt, mShow := parseProgress(t, mAdv)
	if !bytes.Equal(vDefs, mDefs) {
		t.Errorf("advancement definitions: %d vanilla bytes vs %d mc-go bytes", len(vDefs), len(mDefs))
	}
	if vShow != mShow {
		t.Errorf("advancements show flag: vanilla %d, mc-go %d", vShow, mShow)
	}
	for _, id := range []string{"minecraft:recipes/root", "minecraft:recipes/decorations/crafting_table"} {
		vc, vok := vEnt[id]
		mc, mok := mEnt[id]
		if !vok || !mok {
			t.Errorf("progress entry %s: vanilla %v, mc-go %v", id, vok, mok)
			continue
		}
		if len(vc) != len(mc) {
			t.Errorf("progress entry %s: %d vanilla criteria vs %d mc-go", id, len(vc), len(mc))
			continue
		}
		byName := func(cs []progCrit) map[string]progCrit {
			m := map[string]progCrit{}
			for _, c := range cs {
				m[c.name] = c
			}
			return m
		}
		vm, mm := byName(vc), byName(mc)
		for nm, v := range vm {
			m, ok := mm[nm]
			if !ok {
				t.Errorf("progress %s: criterion %s missing on mc-go", id, nm)
				continue
			}
			if v.achieved != m.achieved {
				t.Errorf("progress %s/%s: vanilla achieved=%v, mc-go %v", id, nm, v.achieved, m.achieved)
			}
			now := time.Now().UnixMilli()
			for tag, d := range map[string]int64{"vanilla": v.date, "mc-go": m.date} {
				if v.achieved && (d < start || d > now) {
					t.Errorf("progress %s/%s %s award time %d outside join window [%d, %d]", id, nm, tag, d, start, now)
				}
			}
		}
	}
}
