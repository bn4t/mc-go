package server

import (
	_ "embed"
	"fmt"
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

// These are protocol775's own measured payloads, not rewritten776 captures.
// Names/display associations come from the official775 datapack and were
// checked for every bookable name by individual live recipe grants.
//
//go:embed testdata/vanilla-26.1/play/update_recipes.bin
var java775UpdateRecipes []byte

//go:embed testdata/vanilla-26.1/play/recipe_book_giveall.bin
var java775RecipeBook []byte

type javaRecipeAssets struct {
	entries     map[int32][]byte
	byCanonical map[int][]int32
	byDisplay   map[int32]int
}

var java775Recipes struct {
	sync.Once
	assets *javaRecipeAssets
	err    error
}

func load775RecipeAssets() (*javaRecipeAssets, error) {
	java775Recipes.Do(func() {
		entries, err := splitRecipeBook(java775RecipeBook)
		if err != nil {
			java775Recipes.err = err
			return
		}
		if len(entries) != len(java775RecipeDisplayNames) {
			java775Recipes.err = fmt.Errorf("775recipe display count mismatch")
			return
		}
		a := &javaRecipeAssets{entries: entries, byCanonical: map[int][]int32{}, byDisplay: map[int32]int{}}
		for wire, name := range java775RecipeDisplayNames {
			index, ok := recipeIndex[name]
			if !ok || entries[int32(wire)] == nil {
				java775Recipes.err = fmt.Errorf("775recipe %q has no canonical/display identity", name)
				return
			}
			a.byCanonical[index] = append(a.byCanonical[index], int32(wire))
			a.byDisplay[int32(wire)] = index
		}
		java775Recipes.assets = a
	})
	return java775Recipes.assets, java775Recipes.err
}
func (p *javaProfile) recipeDisplayIDs(index int) ([]int32, error) {
	if index < 0 || index >= len(data.Recipes) {
		return nil, nil
	}
	if p.version.Protocol == 775 {
		a, err := load775RecipeAssets()
		if err != nil {
			return nil, err
		}
		return a.byCanonical[index], nil
	}
	first, ok := displayIDs[index]
	if !ok {
		return nil, nil
	}
	ids := make([]int32, recipeDisplayCount(data.Recipes[index]))
	for i := range ids {
		ids[i] = first + int32(i)
	}
	return ids, nil
}
func (p *javaProfile) recipeCanonicalIndex(display int32) (int, bool) {
	if p.version.Protocol == 775 {
		a, err := load775RecipeAssets()
		if err != nil {
			return 0, false
		}
		index, ok := a.byDisplay[display]
		return index, ok
	}
	for index, first := range displayIDs {
		if display >= first && display < first+recipeDisplayCount(data.Recipes[index]) {
			return index, true
		}
	}
	return 0, false
}
func recipeEntryForSession(sess *Session, b *protocol.Buffer, index int, flags byte) int {
	p, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return 0
	}
	if p.version.Protocol != 775 {
		return recipeEntry(b, index, flags)
	}
	a, err := load775RecipeAssets()
	if err != nil {
		sess.closeQueue()
		return 0
	}
	ids := a.byCanonical[index]
	for _, id := range ids {
		entry := a.entries[id]
		b.Raw(entry[:len(entry)-1])
		b.Byte(flags)
	}
	return len(ids)
}
func recipeDisplayIDsForSession(sess *Session, index int) []int32 {
	p, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return nil
	}
	ids, err := p.recipeDisplayIDs(index)
	if err != nil {
		sess.closeQueue()
		return nil
	}
	return ids
}

// recipeBodyReader validates the bounded display grammar before taking slices
// of the immutable capture. Only the component shape present in stock recipe
// templates (suspicious-stew effects) is accepted; unknown layouts fail closed.
type recipeBodyReader struct {
	r    *protocol.PacketReader
	body []byte
	err  error
}

func (r *recipeBodyReader) fail(s string) {
	if r.err == nil {
		r.err = fmt.Errorf("recipe body: %s", s)
	}
}
func (r *recipeBodyReader) vi() int32 {
	if r.err != nil {
		return 0
	}
	n, e := r.r.VarInt()
	r.err = e
	return n
}
func (r *recipeBodyReader) count() int32 {
	n := r.vi()
	if n < 0 || int64(n) > int64(r.r.Remaining()) {
		r.fail("invalid count")
	}
	return n
}
func (r *recipeBodyReader) str() {
	if r.err == nil {
		_, r.err = r.r.String()
	}
}
func (r *recipeBodyReader) byte() byte {
	if r.err != nil {
		return 0
	}
	v, e := r.r.Byte()
	r.err = e
	return v
}
func (r *recipeBodyReader) flag() bool {
	v := r.byte()
	if v > 1 {
		r.fail("invalid boolean")
	}
	return v == 1
}
func (r *recipeBodyReader) fixed(n int) {
	if r.err == nil {
		_, r.err = r.r.Read(n)
	}
}
func (r *recipeBodyReader) holderSet() {
	n := r.count()
	if n == 0 {
		r.str()
		return
	}
	for i := int32(1); i < n && r.err == nil; i++ {
		r.vi()
	}
}
func (r *recipeBodyReader) slot(depth int) {
	if r.err != nil {
		return
	}
	if depth > 64 {
		r.fail("display nesting limit")
		return
	}
	switch r.vi() {
	case 0, 1:
	case 2:
		r.slot(depth + 1)
	case 3:
		r.slot(depth + 1)
		r.vi()
	case 4:
		r.vi()
	case 5:
		r.vi()
		r.vi()
		added, removed := r.count(), r.count()
		for i := int32(0); i < added && r.err == nil; i++ {
			if r.vi() != 53 {
				r.fail("unmeasured recipe-template component")
				return
			}
			for j, n := int32(0), r.count(); j < n && r.err == nil; j++ {
				r.vi()
				r.vi()
			}
		}
		for i := int32(0); i < removed && r.err == nil; i++ {
			r.vi()
		}
	case 6:
		r.str()
	case 7, 9:
		r.slot(depth + 1)
		r.slot(depth + 1)
	case 8:
		r.slot(depth + 1)
		r.slot(depth + 1)
		if r.vi() <= 0 {
			r.fail("unmeasured inline trim pattern")
		}
	case 10:
		for i, n := int32(0), r.count(); i < n && r.err == nil; i++ {
			r.slot(depth + 1)
		}
	default:
		r.fail("unknown slot display type")
	}
}
func (r *recipeBodyReader) display() {
	typ := r.vi()
	switch typ {
	case 0, 1:
		if typ == 1 {
			r.vi()
			r.vi()
		}
		for i, n := int32(0), r.count(); i < n && r.err == nil; i++ {
			r.slot(0)
		}
		r.slot(0)
		r.slot(0)
	case 2:
		for range 4 {
			r.slot(0)
		}
		r.vi()
		r.fixed(4)
	case 3:
		for range 3 {
			r.slot(0)
		}
	case 4:
		for range 5 {
			r.slot(0)
		}
	default:
		r.fail("unknown recipe display type")
	}
}
func splitRecipeBook(body []byte) (map[int32][]byte, error) {
	r := &recipeBodyReader{r: protocol.NewPacketReader(body), body: body}
	entries := map[int32][]byte{}
	for i, n := int32(0), r.count(); i < n && r.err == nil; i++ {
		begin := len(body) - r.r.Remaining()
		id := r.vi()
		r.display()
		r.vi()
		category := r.vi()
		if category < 0 || category > 12 {
			r.fail("unknown recipe category")
		}
		if r.flag() {
			for j, m := int32(0), r.count(); j < m && r.err == nil; j++ {
				r.holderSet()
			}
		}
		flags := r.byte()
		if flags&^byte(3) != 0 {
			r.fail("unknown recipe flag")
		}
		end := len(body) - r.r.Remaining()
		if id < 0 || entries[id] != nil {
			r.fail("duplicate/negative display ID")
		}
		entries[id] = body[begin:end]
	}
	r.flag()
	if r.err != nil {
		return nil, r.err
	}
	if r.r.Remaining() != 0 {
		return nil, fmt.Errorf("recipe body: trailing bytes")
	}
	return entries, nil
}
