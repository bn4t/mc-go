package blackbox

// Recipe book command parity: /recipe give|take on a single recipe, a
// duplicate, the wildcard form, and a special recipe, checking that the
// RecipeBookAdd/Remove packet bodies and command-reply components are
// byte-identical between vanilla and mc-go.

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type recipeCapture struct {
	adds    [][]byte
	removes [][]byte
	replies [][]byte
	advs    [][]byte
}

func runRecipeScript(t *testing.T, addr, name string) *recipeCapture {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))

	collect := func(marker string) *recipeCapture {
		sec := &recipeCapture{}
		found := false
		for i := 0; i < 1200; i++ {
			// Advancement progress flushes on a tick boundary after the
			// command reply, so keep draining for a beat once the marker
			// arrives.
			if found {
				_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			} else {
				_ = c.SetReadDeadline(time.Now().Add(90 * time.Second))
			}
			p, err := pr.ReadPacket()
			if err != nil {
				if found {
					return sec
				}
				t.Fatalf("recipe parity %s: read: %v", marker, err)
			}
			if p.ID == server.IDPlayKeepAliveCB {
				var b protocol.Buffer
				b.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, b.Bytes())
				continue
			}
			if bytes.Contains(p.Data, []byte(marker)) && !found {
				found = true
				continue
			}
			switch p.ID {
			case server.IDPlayRecipeBookAdd:
				sec.adds = append(sec.adds, p.Data)
			case server.IDPlayRecipeBookRemove:
				sec.removes = append(sec.removes, p.Data)
			case server.IDPlaySystemChatCB:
				sec.replies = append(sec.replies, p.Data)
			case server.IDPlayUpdateAdvance:
				sec.advs = append(sec.advs, p.Data)
			}
		}
		if found {
			return sec
		}
		t.Fatalf("recipe parity %s: marker never seen", marker)
		return nil
	}

	steps := []struct{ cmd, marker string }{
		{"say R1", "R1"}, // join burst ends here
		{"recipe give @s minecraft:oak_planks", "R2"},
		{"recipe give @s minecraft:oak_planks", "R3"},
		{"recipe take @s minecraft:oak_planks", "R4"},
		{"recipe take @s minecraft:oak_planks", "R5"},
		{"recipe take @s *", "R6"},
		{"recipe give @s *", "R7"},
		{"recipe give @s minecraft:book_cloning", "R8"},
		{"recipe take @s minecraft:map_cloning", "R9"},
		{"recipe give @s minecraft:tipped_arrow", "R10"},
		// inventory_changed: the given items complete has_* criteria,
		// each completed advancement pushes one RecipeBookAdd for its
		// reward recipe before the command reply goes out.
		{"give @s oak_log", "R11"},
		{"give @s stone", "R12"},
	}
	sections := make([]*recipeCapture, len(steps))
	for i, st := range steps {
		sendCommand(t, pw, st.cmd)
		if st.cmd != "say "+st.marker {
			sendCommand(t, pw, "say "+st.marker)
		}
		// the first marker also swallows the join-time unlock blob
		sections[i] = collect(st.marker)
	}
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))
	return mergeRecipeSections(sections)
}

func mergeRecipeSections(sections []*recipeCapture) *recipeCapture {
	out := &recipeCapture{}
	for _, s := range sections {
		out.adds = append(out.adds, s.adds...)
		out.removes = append(out.removes, s.removes...)
		out.replies = append(out.replies, s.replies...)
		out.advs = append(out.advs, s.advs...)
	}
	return out
}

func TestRecipeCommandParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("RcP"),
	}, nil)
	defer tg.cleanup()

	van := runRecipeScript(t, tg.vanilla, "RcP")
	mc := runRecipeScript(t, tg.mcgo, "RcP")

	cmp := func(label string, a, b [][]byte) {
		t.Helper()
		if len(a) != len(b) {
			t.Fatalf("%s count: vanilla %d, mc-go %d", label, len(a), len(b))
		}
		for i := range a {
			if !bytes.Equal(a[i], b[i]) {
				d := 0
				for d < len(a[i]) && d < len(b[i]) && a[i][d] == b[i][d] {
					d++
				}
				t.Fatalf("%s[%d] differ at byte %d (%d vs %d bytes)\nv %x\nm %x",
					label, i, d, len(a[i]), len(b[i]),
					a[i][max(0, d-8):min(len(a[i]), d+8)],
					b[i][max(0, d-8):min(len(b[i]), d+8)])
			}
		}
	}
	// Trigger unlocks emit one RecipeBookAdd per completed advancement,
	// in the order the vanilla trigger's listeners happen to run - an
	// identity-hash order that varies across vanilla launches - so adds
	// compare as a multiset. The give commands' own adds are single
	// packets anyway.
	if len(van.adds) != len(mc.adds) {
		t.Fatalf("adds count: vanilla %d, mc-go %d", len(van.adds), len(mc.adds))
	}
	multiset := func(b [][]byte) map[string]int {
		m := map[string]int{}
		for _, p := range b {
			m[string(p)]++
		}
		return m
	}
	vm, mm := multiset(van.adds), multiset(mc.adds)
	for k, n := range vm {
		if mm[k] != n {
			t.Fatalf("adds: vanilla has %d of %x, mc-go %d", n, k[:min(40, len(k))], mm[k])
		}
	}
	for k, n := range mm {
		if vm[k] != n {
			t.Fatalf("adds: mc-go has %d of %x, vanilla %d", n, k[:min(40, len(k))], vm[k])
		}
	}
	cmp("removes", van.removes, mc.removes)
	cmp("replies", van.replies, mc.replies)
	cmpAdv(t, "advs", van.advs, mc.advs)
	fmt.Printf("recipe parity: %d adds, %d removes, %d replies, %d adv syncs equal\n",
		len(van.adds), len(van.removes), len(van.replies), len(van.advs))
}

// advSync is a decoded ClientboundUpdateAdvancements body, kept
// timestamp-free so vanilla and mc-go compares survive the award times
// being real clock values.
type advSync struct {
	reset    bool
	added    []string
	reqs     map[string][][]string
	removed  []string
	progress []advProg
	show     bool
}

type advProg struct {
	id       string
	criteria [][2]any // name, then time-or-empty
}

func parseUpdateAdv(t *testing.T, d []byte) *advSync {
	t.Helper()
	r := protocol.NewPacketReader(d)
	out := &advSync{reqs: map[string][][]string{}}
	var err error
	if out.reset, err = r.Bool(); err != nil {
		t.Fatal(err)
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(n); i++ {
		id, err := r.String()
		if err != nil {
			t.Fatal(err)
		}
		out.added = append(out.added, id)
		if has, _ := r.Bool(); has {
			if _, err := r.String(); err != nil {
				t.Fatal(err)
			}
		}
		if has, _ := r.Bool(); has {
			t.Fatalf("adv %s: unexpected display info", id)
		}
		nr, _ := r.VarInt()
		var reqs [][]string
		for j := 0; j < int(nr); j++ {
			nc, _ := r.VarInt()
			var group []string
			for k := 0; k < int(nc); k++ {
				c, err := r.String()
				if err != nil {
					t.Fatal(err)
				}
				group = append(group, c)
			}
			reqs = append(reqs, group)
		}
		out.reqs[id] = reqs
		if _, err := r.Bool(); err != nil {
			t.Fatal(err)
		}
	}
	if n, err = r.VarInt(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(n); i++ {
		id, _ := r.String()
		out.removed = append(out.removed, id)
	}
	if n, err = r.VarInt(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(n); i++ {
		id, _ := r.String()
		nc, _ := r.VarInt()
		p := advProg{id: id}
		for j := 0; j < int(nc); j++ {
			cname, _ := r.String()
			done, _ := r.Bool()
			var got any = struct{}{}
			if done {
				if _, err := r.Long(); err != nil {
					t.Fatal(err)
				}
				got = true
			}
			p.criteria = append(p.criteria, [2]any{cname, got})
		}
		out.progress = append(out.progress, p)
	}
	if out.show, err = r.Bool(); err != nil {
		t.Fatal(err)
	}
	return out
}

// cmpAdv compares decoded UpdateAdvancements bodies. Vanilla builds the
// added/progress collections via identity-hashed sets, so their order
// varies run to run even between two vanilla launches: entries compare
// as sets, while criteria inside a progress entry stay ordered (those
// come from String-hash buckets, which are deterministic).
func cmpAdv(t *testing.T, label string, a, b [][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s count: vanilla %d, mc-go %d", label, len(a), len(b))
	}
	for i := range a {
		va, vb := parseUpdateAdv(t, a[i]), parseUpdateAdv(t, b[i])
		sort.Strings(va.added)
		sort.Strings(vb.added)
		sort.Strings(va.removed)
		sort.Strings(vb.removed)
		sort.Slice(va.progress, func(x, y int) bool { return va.progress[x].id < va.progress[y].id })
		sort.Slice(vb.progress, func(x, y int) bool { return vb.progress[x].id < vb.progress[y].id })
		if va.reset != vb.reset || va.show != vb.show {
			t.Fatalf("%s[%d]: reset/show %v,%v vs %v,%v", label, i, va.reset, va.show, vb.reset, vb.show)
		}
		diffSet := func(what string, a, b []string) {
			am, bm := map[string]int{}, map[string]int{}
			for _, s := range a {
				am[s]++
			}
			for _, s := range b {
				bm[s]++
			}
			for s, n := range am {
				if bm[s] != n {
					t.Fatalf("%s[%d] %s: vanilla has %dx %q, mc-go %dx", label, i, what, n, s, bm[s])
				}
			}
			for s, n := range bm {
				if am[s] != n {
					t.Fatalf("%s[%d] %s: mc-go has %dx %q, vanilla %dx", label, i, what, n, s, am[s])
				}
			}
		}
		diffSet("added", va.added, vb.added)
		diffSet("removed", va.removed, vb.removed)
		var vp, mp []string
		for _, p := range va.progress {
			vp = append(vp, p.id)
		}
		for _, p := range vb.progress {
			mp = append(mp, p.id)
		}
		diffSet("progress", vp, mp)
		for j := range va.progress {
			if va.progress[j].id != vb.progress[j].id {
				continue
			}
			if !reflect.DeepEqual(va.progress[j], vb.progress[j]) {
				t.Fatalf("%s[%d] progress %s:\nv %+v\nm %+v", label, i, va.progress[j].id, va.progress[j], vb.progress[j])
			}
			if !reflect.DeepEqual(va.reqs[va.progress[j].id], vb.reqs[vb.progress[j].id]) {
				t.Fatalf("%s[%d] reqs %s:\nv %v\nm %v", label, i, va.progress[j].id, va.reqs[va.progress[j].id], vb.reqs[vb.progress[j].id])
			}
		}
	}
}
