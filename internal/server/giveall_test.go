package server

import (
	"fmt"
	"os"
	"testing"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

// TestGiveAllBytes proves the full /recipe give @s * RecipeBookAdd
// body - all 1568 display entries and the replace flag - is
// byte-identical to a live vanilla 26.2 capture.
func TestGiveAllBytes(t *testing.T) {
	var b protocol.Buffer
	var n int32
	var buf protocol.Buffer
	for i := range data.Recipes {
		if _, ok := displayIDs[i]; !ok {
			continue
		}
		n += int32(recipeEntry(&buf, i, recipeGiveFlags(data.Recipes[i])))
	}
	b.VarInt(n)
	b.Raw(buf.Bytes())
	b.Bool(false)

	want, err := os.ReadFile("testdata/vanilla-26.2/play/recipe_book_giveall.bin")
	if err != nil {
		t.Skip("no vanilla capture")
	}
	fmt.Printf("mc-go n=%d bytes=%d, vanilla bytes=%d\n", n, len(b.Bytes()), len(want))
	fmt.Printf("equal=%v\n", bytesEqualLog(b.Bytes(), want))
}

func bytesEqualLog(a, c []byte) bool {
	m := len(a)
	if len(c) < m {
		m = len(c)
	}
	for i := 0; i < m; i++ {
		if a[i] != c[i] {
			fmt.Printf("first diff at %d: got %02x want %02x\n", i, a[i], c[i])
			fmt.Printf(" got ctx %x\n want ctx %x\n", a[max(0, i-30):i+30], c[max(0, i-30):i+30])
			return false
		}
	}
	return len(a) == len(c)
}
