package blackbox

// Recipe book command parity: /recipe give|take on a single recipe, a
// duplicate, the wildcard form, and a special recipe, checking that the
// RecipeBookAdd/Remove packet bodies and command-reply components are
// byte-identical between vanilla and mc-go.

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

type recipeCapture struct {
	adds    [][]byte
	removes [][]byte
	replies [][]byte
}

func runRecipeScript(t *testing.T, addr, name string) *recipeCapture {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))

	collect := func(marker string) *recipeCapture {
		sec := &recipeCapture{}
		for i := 0; i < 1200; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("recipe parity %s: read: %v", marker, err)
			}
			if bytes.Contains(p.Data, []byte(marker)) {
				return sec
			}
			switch p.ID {
			case server.IDPlayRecipeBookAdd:
				sec.adds = append(sec.adds, p.Data)
			case server.IDPlayRecipeBookRemove:
				sec.removes = append(sec.removes, p.Data)
			case server.IDPlaySystemChatCB:
				sec.replies = append(sec.replies, p.Data)
			}
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
	return mergeRecipeSections(sections)
}

func mergeRecipeSections(sections []*recipeCapture) *recipeCapture {
	out := &recipeCapture{}
	for _, s := range sections {
		out.adds = append(out.adds, s.adds...)
		out.removes = append(out.removes, s.removes...)
		out.replies = append(out.replies, s.replies...)
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
	cmp("adds", van.adds, mc.adds)
	cmp("removes", van.removes, mc.removes)
	cmp("replies", van.replies, mc.replies)
	fmt.Printf("recipe parity: %d adds, %d removes, %d replies byte-equal\n",
		len(van.adds), len(van.removes), len(van.replies))
}
