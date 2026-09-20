package server

import (
	"bytes"
	_ "embed"
	"testing"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

//go:embed testdata/vanilla-26.2/play/recipe_book_unlock.bin
var unlockBlob []byte

// The crafting_table unlock captured from a live 26.2 server: one
// book-add entry for its display id with flag 2 and no replace.
func TestCraftingTableEntryBytes(t *testing.T) {
	idx := -1
	for i, r := range data.Recipes {
		if r.ID == "crafting_table" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no crafting_table recipe")
	}
	var b protocol.Buffer
	b.VarInt(1)
	recipeEntry(&b, idx, 2)
	b.Bool(false)
	if !bytes.Equal(b.Bytes(), unlockBlob) {
		t.Fatalf("mismatch:\n got %x\nwant %x", b.Bytes(), unlockBlob)
	}
}
