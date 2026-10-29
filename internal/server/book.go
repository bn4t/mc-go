package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"strconv"
	"strings"
)

// A book's component payload lives on ItemStack.BookWritable and
// ItemStack.BookWritten as the canonical SNBT of the component value —
// {pages:["a","b"]} for minecraft:writable_book_content and
// {author:"a",generation:0,pages:[...],resolved:0b,title:{raw:"t"}} for
// minecraft:written_book_content — keeping the flat stack
// ==-comparable while the helpers below re-parse it into the NBT or
// wire shape wherever it re-emits: the slot patch, the lectern's
// Book tag, and the show_item hover.

// bookContentValue unpacks one of the stack's stored book payloads —
// the writable component or the written one — or nil when it carries
// neither.
func bookContentValue(it ItemStack, written bool) map[string]any {
	raw := it.BookWritable
	if written {
		raw = it.BookWritten
	}
	if raw == "" {
		return nil
	}
	t, _, err := parseSNBTTag(raw, 0)
	if err != nil || t == nil || t.kind != snbtCompound {
		return nil
	}
	m, _ := snbtToValue(t).(map[string]any)
	return m
}

// bookContentNBT is a component's emission shape — what the save codec
// and data get print. WrittenBookContent's generation, pages and
// resolved are optionalFieldOf defaults the codec leaves out at their
// zero values; WritableBookContent's pages is a required field and
// always emits.
func bookContentNBT(it ItemStack, written bool) map[string]any {
	v := bookContentValue(it, written)
	if v == nil {
		return nil
	}
	if !written {
		return map[string]any{"pages": v["pages"]}
	}
	out := map[string]any{
		"title":  v["title"],
		"author": v["author"],
	}
	if gen, _ := nbtNumber(v["generation"]); gen != 0 {
		out["generation"] = v["generation"]
	}
	if l, ok := v["pages"].(nbt.List); ok && len(l.Items) > 0 {
		out["pages"] = l
	}
	if res, _ := nbtNumber(v["resolved"]); res != 0 {
		out["resolved"] = v["resolved"]
	}
	return out
}

// bookContentFromNBT loads the book components out of a saved stack's
// components map — the inverse of bookContentNBT, refilling the
// defaults the codec omitted. Either component may be absent.
func bookContentFromNBT(comps map[string]any) (writable, written string) {
	if v, ok := comps["minecraft:writable_book_content"].(map[string]any); ok {
		pages := nbt.List{Elem: nbt.TagCompound}
		if p, ok := v["pages"].(nbt.List); ok {
			pages = p
		}
		writable = componentSNBT(map[string]any{"pages": filterableWrapList(pages)})
	}
	if v, ok := comps["minecraft:written_book_content"].(map[string]any); ok {
		out := map[string]any{
			"title":      filterableWrap(v["title"]),
			"author":     v["author"],
			"generation": int32(0),
			"pages":      nbt.List{Elem: nbt.TagCompound},
			"resolved":   int8(0),
		}
		if g := v["generation"]; g != nil {
			out["generation"] = g
		}
		if p, ok := v["pages"].(nbt.List); ok {
			out["pages"] = filterableWrapList(p)
		}
		if r := v["resolved"]; r != nil {
			out["resolved"] = r
		}
		written = componentSNBT(out)
	}
	return writable, written
}

// filterableWrap normalizes a Filterable leaf to its saved form: the
// codec always encodes {raw:X} (plus filtered when set) — the bare
// leaf only ever appears as decode input.
func filterableWrap(v any) any {
	if m, ok := v.(map[string]any); ok && len(m) <= 2 {
		if _, has := m["raw"]; has {
			return v
		}
	}
	return map[string]any{"raw": v}
}

// filterableWrapList wraps every element of a pages list.
func filterableWrapList(l nbt.List) nbt.List {
	for i, e := range l.Items {
		l.Items[i] = filterableWrap(e)
	}
	if len(l.Items) > 0 {
		l.Elem = nbt.TagCompound
	}
	return l
}

// bookCanCopy is BookCloningRecipe.canCraftCopy: the written content's
// generation must sit inside the recipe's allowedGenerations — the
// serializer default between(0,1) since the JSON omits the field.
func bookCanCopy(it ItemStack) bool {
	v := bookContentValue(it, true)
	if v == nil {
		return false
	}
	gen := nbtNumberOr(v["generation"], 0)
	return gen >= 0 && gen <= 1
}

// bookCraftCopy is WrittenBookContent.craftCopy: the same content a
// generation up.
func bookCraftCopy(raw string) string {
	t, _, err := parseSNBTTag(raw, 0)
	if err != nil || t == nil || t.kind != snbtCompound {
		return raw
	}
	m, ok := snbtToValue(t).(map[string]any)
	if !ok {
		return raw
	}
	m["generation"] = int32(nbtNumberOr(m["generation"], 0) + 1)
	return componentSNBT(m)
}

// bookResolve is LecternBlockEntity.resolveBook: the written content's
// resolved flag flips true (vanilla resolves on setBook and on load).
func bookResolve(it ItemStack) ItemStack {
	v := bookContentValue(it, true)
	if v == nil {
		return it
	}
	if r, _ := nbtNumber(v["resolved"]); r != 0 {
		return it
	}
	v["resolved"] = int8(1)
	it.BookWritten = componentSNBT(v)
	return it
}

// filterableParts unpacks a Filterable<T> codec value: a
// {raw:...,filtered:...} compound splits into its parts while any
// other value is the raw entry alone — the codec's inline form.
func filterableParts(v any) (raw, filtered any, has bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) > 2 {
		return v, nil, false
	}
	r, ok := m["raw"]
	if !ok {
		return v, nil, false
	}
	raw = r
	f, has := m["filtered"]
	return raw, f, has
}

// writeBookContentPatch emits the book-content slot-patch component —
// which rides the stack is field-level: each stored component emits
// under its own id, ascending like every other patch entry.
func writeBookContentPatch(b *protocol.Buffer, it ItemStack) {
	if v := bookContentValue(it, false); v != nil {
		b.VarInt(compWritableBook)
		writeFilterableUTFList(b, v["pages"])
	}
	if v := bookContentValue(it, true); v != nil {
		b.VarInt(compWrittenBook)
		writeFilterableUTF(b, v["title"])
		author, _ := v["author"].(string)
		b.String(author)
		gen, _ := nbtNumber(v["generation"])
		b.VarInt(int32(gen))
		writeFilterableCompList(b, v["pages"])
		res, _ := nbtNumber(v["resolved"])
		b.Bool(res != 0)
	}
}

// writeFilterableUTF writes one Filterable<String>: the raw utf8 then
// the optional filtered utf8.
func writeFilterableUTF(b *protocol.Buffer, v any) {
	raw, filtered, has := filterableParts(v)
	s, _ := raw.(string)
	b.String(s)
	b.Bool(has)
	if has {
		fs, _ := filtered.(string)
		b.String(fs)
	}
}

func writeFilterableUTFList(b *protocol.Buffer, v any) {
	l, _ := v.(nbt.List)
	b.VarInt(int32(len(l.Items)))
	for _, e := range l.Items {
		writeFilterableUTF(b, e)
	}
}

// writeFilterableCompList writes a List<Filterable<Component>> — each
// raw value is a text component (NBT), then the optional filtered
// component.
func writeFilterableCompList(b *protocol.Buffer, v any) {
	l, _ := v.(nbt.List)
	b.VarInt(int32(len(l.Items)))
	for _, e := range l.Items {
		raw, filtered, has := filterableParts(e)
		_ = nbt.WriteUnnamed(b, raw)
		b.Bool(has)
		if has {
			_ = nbt.WriteUnnamed(b, filtered)
		}
	}
}

// bookContentFromTag normalizes a writable_book_content or
// written_book_content bracket component into the canonical SNBT the
// stack stores, applying the record defaults vanilla's codec fills
// (generation 0, pages [], resolved false). Failures report the
// vanilla malformed-component error.
func bookContentFromTag(written bool, t *snbtTag) (string, bool) {
	if t.kind != snbtCompound {
		return "", false
	}
	if written {
		// title is Filterable<String>: the leaf is a plain string,
		// not a component.
		title, ok := filterableTagValue(t.vals["title"], false)
		author := t.vals["author"]
		if !ok || author == nil || author.kind != snbtString {
			return "", false
		}
		m := map[string]any{
			"title":      title,
			"author":     author.str,
			"generation": int32(0),
			"pages":      nbt.List{Elem: nbt.TagCompound},
			"resolved":   int8(0),
		}
		if g, ok := t.vals["generation"]; ok {
			if g.kind != snbtNumber {
				return "", false
			}
			n, err := strconv.Atoi(strings.TrimRight(g.num, "bBsSiIlLfFdD"))
			if err != nil {
				return "", false
			}
			m["generation"] = int32(n)
		}
		if p, ok := t.vals["pages"]; ok {
			pages, ok := filterableTagList(p, true)
			if !ok {
				return "", false
			}
			m["pages"] = pages
		}
		if r, ok := t.vals["resolved"]; ok {
			if r.kind != snbtNumber {
				return "", false
			}
			n, err := strconv.Atoi(strings.TrimRight(r.num, "bB"))
			if err != nil {
				return "", false
			}
			if n != 0 {
				m["resolved"] = int8(1)
			}
		}
		return componentSNBT(m), true
	}
	pages, ok := filterableTagList(t.vals["pages"], false)
	if !ok {
		return "", false
	}
	return componentSNBT(map[string]any{"pages": pages}), true
}

// filterableTagList converts a pages list tag into the value form.
// comp=false wants Filterable<String> leaves (writable pages), true
// Filterable<Component> leaves (written pages, any NBT shape).
func filterableTagList(t *snbtTag, comp bool) (nbt.List, bool) {
	if t == nil {
		return nbt.List{Elem: nbt.TagEnd}, false
	}
	if t.kind != snbtList {
		return nbt.List{}, false
	}
	out := nbt.List{Elem: nbt.TagEnd}
	for _, e := range t.items {
		v, ok := filterableTagValue(e, comp)
		if !ok {
			return nbt.List{}, false
		}
		out.Items = append(out.Items, v)
	}
	if len(out.Items) > 0 {
		out.Elem = nbt.TagCompound
	}
	return out, true
}

// filterableTagValue converts one Filterable entry: a compound with a
// raw key (and at most the filtered sibling) fills both slots, any
// other tag decodes into raw alone — the stored form is always the
// {raw:...} wrapper, matching how the codec encodes it.
func filterableTagValue(t *snbtTag, comp bool) (any, bool) {
	m := map[string]any{}
	if t.kind == snbtCompound {
		if raw, ok := t.vals["raw"]; ok && len(t.vals) <= 2 {
			rv, ok := filterableLeafValue(raw, comp)
			if !ok {
				return nil, false
			}
			m["raw"] = rv
			if f, ok := t.vals["filtered"]; ok {
				fv, ok := filterableLeafValue(f, comp)
				if !ok {
					return nil, false
				}
				m["filtered"] = fv
			}
			return m, true
		}
	}
	rv, ok := filterableLeafValue(t, comp)
	if !ok {
		return nil, false
	}
	m["raw"] = rv
	return m, true
}

// filterableLeafValue converts a raw/filtered leaf: comp=true accepts
// any component shape (compound, string, list — the text-component
// codec's NBT forms), comp=false wants a plain string.
func filterableLeafValue(t *snbtTag, comp bool) (any, bool) {
	if !comp {
		if t.kind != snbtString {
			return nil, false
		}
		return t.str, true
	}
	return snbtToValue(t), true
}
