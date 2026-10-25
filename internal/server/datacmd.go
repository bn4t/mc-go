package server

import (
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// /data block — get, merge, remove, modify — against the per-dimension
// block entity stores. Vanilla renders a queried tag through NbtUtils'
// pretty printer: keys in aqua, numbers in gold, their byte/short/long/
// float suffix letters in red, quoted strings in green, punctuation
// unstyled, each element preceded by an empty literal spacer. The tag
// order is the CompoundTag's own bucket order, reproduced by
// nbt.OrderedKeys.
//
// Replies measured on 26.2: a full read answers
// commands.data.block.query with the block position wrapped as {"":i}
// compounds and the pretty tag; merge/remove/modify answer
// commands.data.block.modified with bare int coordinates. A position
// with no block entity fails commands.data.block.invalid, a missing
// path arguments.nbtpath.nothing_found, a no-change write
// commands.data.merge.failed. A mutation marks the block entity
// changed, which for a sign sends block_entity_data to trackers
// (SignBlockEntity.setChanged calls sendBlockUpdated); chest and
// furnace mutations send nothing.
func (s *Server) runData(player string, args []string) reply {
	if len(args) > 1 && args[1] == "entity" {
		return s.runDataEntity(player, args)
	}
	if len(args) > 1 && args[1] == "storage" {
		return s.runDataStorage(player, args)
	}
	if len(args) < 5 || args[1] != "block" {
		return componentReply(commandError("command.unknown.command"))
	}
	d := s.dimOf(player)
	bx, by, bz := s.baseBlockPos(player)
	x, err := parseCoord(args[2], bx)
	if err != nil {
		return componentReply(commandError("command.unknown.command"))
	}
	y, err := parseCoord(args[3], by)
	if err != nil {
		return componentReply(commandError("command.unknown.command"))
	}
	z, err := parseCoord(args[4], bz)
	if err != nil {
		return componentReply(commandError("command.unknown.command"))
	}
	pos := world.Pos{X: x, Y: y, Z: z}

	be, ok := s.blockEntitySaveMap(d, pos)
	if !ok {
		return componentReply(commandError("commands.data.block.invalid"))
	}
	delete(be, "keepPacked")

	posArgs := []any{
		nestedCompound{key: "", value: int32(x)},
		nestedCompound{key: "", value: int32(y)},
		nestedCompound{key: "", value: int32(z)},
	}
	modified := func() reply {
		return opsEchoReply(translatable("commands.data.block.modified",
			int32(x), int32(y), int32(z)))
	}
	// applyBlockEntity writes the edited map back into the stores and
	// marks the position's block entity dirty — the update packet rides
	// the tick-end flush, after the reply chat like vanilla's
	// sendBlockUpdated.
	apply := func() {
		s.restoreBlockEntity(d, be)
		s.queueBlockEntityDirty(d, pos)
	}

	switch args[0] {
	case "get":
		var target any = be
		if len(args) > 5 {
			steps, ok := parseNBTPath(args[5])
			if !ok {
				return componentReply(commandError("command.unknown.command"))
			}
			target, ok = nbtGet(be, steps)
			if !ok {
				return componentReply(commandError("arguments.nbtpath.nothing_found", args[5]))
			}
		}
		pretty := translatable("commands.data.block.query", append(posArgs,
			component{extra: nbtPrettyElems(target), text: ""})...)
		r := componentReply(pretty)
		r.result, r.hasResult = nbtResult(target), true
		return r

	case "merge":
		if len(args) < 6 {
			return componentReply(commandError("command.unknown.command"))
		}
		tg, _, perr := parseSNBTCompound(args[5], 0)
		if perr != nil || tg == nil || tg.kind != snbtCompound {
			return componentReply(commandError("command.unknown.command"))
		}
		nbtMergeInto(be, snbtToValue(tg))
		apply()
		return modified()

	case "remove":
		if len(args) < 6 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[5])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		if !nbtRemove(be, steps) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		apply()
		return modified()

	case "modify":
		// data modify block <pos> <path> <op> <source>: the ops are
		// set/merge/append/prepend/insert with the same value|from|
		// string source forms the entity scope takes.
		if len(args) < 9 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[5])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		op := args[6]
		rest := args[7:]
		var insertIdx int
		if op == "insert" {
			v, err := strconv.Atoi(rest[0])
			if err != nil || len(rest) < 2 {
				return componentReply(commandError("command.unknown.command"))
			}
			insertIdx = v
			rest = rest[1:]
		}
		val, serr := s.modifySource(player, rest)
		if serr != nil {
			return *serr
		}
		if !nbtModify(be, steps, op, insertIdx, val) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		apply()
		return modified()
	}
	return componentReply(commandError("command.unknown.command"))
}

// nbtPathStep is one leg of a data path: a compound key or a list index.
type nbtPathStep struct {
	key     string
	index   int
	isIndex bool
}

// parseNBTPath reads a vanilla NBT path — `a.b`, `a[0]`, `a.b[0].c`,
// quoted keys — into steps. Compound matchers ({k:v}) and the all
// elements form ([]) are not modeled.
func parseNBTPath(s string) ([]nbtPathStep, bool) {
	var steps []nbtPathStep
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '[':
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(s[i+1 : i+j])
			if err != nil {
				return nil, false
			}
			steps = append(steps, nbtPathStep{index: n, isIndex: true})
			i += j + 1
		case s[i] == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, false
			}
			steps = append(steps, nbtPathStep{key: s[i+1 : i+1+j]})
			i += j + 2
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if j == i {
				return nil, false
			}
			steps = append(steps, nbtPathStep{key: s[i:j]})
			i = j
		}
		if i < len(s) && s[i] == '.' {
			i++
			if i >= len(s) {
				return nil, false
			}
		}
	}
	return steps, true
}

// nbtGet follows steps into root: map[string]any compounds and
// nbt.List / []any lists.
func nbtGet(root any, steps []nbtPathStep) (any, bool) {
	cur := root
	for _, st := range steps {
		if st.isIndex {
			// CompoundTag path resolution indexes typed arrays the
			// same as lists — [B;]/[I;]/[L;] elements get by index.
			var items []any
			switch l := cur.(type) {
			case nbt.List:
				items = l.Items
			case []any:
				items = l
			case []byte:
				if st.index < 0 || st.index >= len(l) {
					return nil, false
				}
				cur = int8(l[st.index])
				continue
			case []int32:
				if st.index < 0 || st.index >= len(l) {
					return nil, false
				}
				cur = l[st.index]
				continue
			case []int64:
				if st.index < 0 || st.index >= len(l) {
					return nil, false
				}
				cur = l[st.index]
				continue
			default:
				return nil, false
			}
			if st.index < 0 || st.index >= len(items) {
				return nil, false
			}
			cur = items[st.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[st.key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// nbtSet replaces the value steps point at, or writes the final key.
// Vanilla's set fails when the path's parents do not resolve.
func nbtSet(root any, steps []nbtPathStep, val any) bool {
	if len(steps) == 0 {
		return false
	}
	parent, ok := nbtGet(root, steps[:len(steps)-1])
	if !ok {
		return false
	}
	last := steps[len(steps)-1]
	if last.isIndex {
		var items []any
		switch l := parent.(type) {
		case nbt.List:
			items = l.Items
		case []any:
			items = l
		default:
			return false
		}
		if last.index < 0 || last.index >= len(items) {
			return false
		}
		items[last.index] = val
		return true
	}
	m, ok := parent.(map[string]any)
	if !ok {
		return false
	}
	m[last.key] = val
	return true
}

// nbtRemove drops the value steps point at; false when it never
// resolved — vanilla's commands.data.merge.failed. The root map is
// updated in place.
func nbtRemove(root map[string]any, steps []nbtPathStep) bool {
	_, ok := nbtRemoveRec(root, steps)
	return ok
}

// nbtRemoveRec rewrites cur minus steps[1:], returning the new subtree
// so list shrinks propagate back into the parent map.
func nbtRemoveRec(cur any, steps []nbtPathStep) (any, bool) {
	st := steps[0]
	if len(steps) == 1 {
		if st.isIndex {
			switch l := cur.(type) {
			case nbt.List:
				if st.index < 0 || st.index >= len(l.Items) {
					return nil, false
				}
				l.Items = append(l.Items[:st.index:st.index], l.Items[st.index+1:]...)
				return l, true
			case []any:
				if st.index < 0 || st.index >= len(l) {
					return nil, false
				}
				return append(l[:st.index:st.index], l[st.index+1:]...), true
			}
			return nil, false
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if _, ok := m[st.key]; !ok {
			return nil, false
		}
		delete(m, st.key)
		return m, true
	}
	var child any
	if st.isIndex {
		switch l := cur.(type) {
		case nbt.List:
			if st.index < 0 || st.index >= len(l.Items) {
				return nil, false
			}
			nv, ok := nbtRemoveRec(l.Items[st.index], steps[1:])
			if !ok {
				return nil, false
			}
			l.Items[st.index] = nv
			return l, true
		default:
			return nil, false
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false
	}
	child, ok = m[st.key]
	if !ok {
		return nil, false
	}
	nv, ok := nbtRemoveRec(child, steps[1:])
	if !ok {
		return nil, false
	}
	m[st.key] = nv
	return m, true
}

// nbtModify runs one `data modify` op — set, merge, append, prepend,
// insert — writing val into the path target. List results are written
// back into the parent compound so growth/shrink propagates.
func nbtModify(root map[string]any, steps []nbtPathStep, op string, idx int, val any) bool {
	if op == "set" {
		return nbtSet(root, steps, val)
	}
	_, ok := nbtWriteRec(root, steps, func(target any) (any, bool) {
		switch op {
		case "merge":
			dm, ok1 := target.(map[string]any)
			sm, ok2 := val.(map[string]any)
			if !ok1 || !ok2 {
				return nil, false
			}
			nbtMergeInto(dm, sm)
			return dm, true
		case "append", "prepend", "insert":
			var items []any
			var elem byte
			switch l := target.(type) {
			case nbt.List:
				items, elem = l.Items, l.Elem
			case []any:
				items = l
			default:
				return nil, false
			}
			at := len(items)
			switch op {
			case "prepend":
				at = 0
			case "insert":
				at = idx
				if at < 0 || at > len(items) {
					return nil, false
				}
			}
			items = append(items, nil)
			copy(items[at+1:], items[at:])
			items[at] = val
			if _, isList := target.(nbt.List); isList {
				return nbt.List{Elem: elem, Items: items}, true
			}
			return items, true
		}
		return nil, false
	})
	return ok
}

// nbtWriteRec descends cur to steps and replaces the target with
// fn(target), rebuilding list headers on the way back so a replaced
// slice reaches the parent map.
func nbtWriteRec(cur any, steps []nbtPathStep, fn func(any) (any, bool)) (any, bool) {
	if len(steps) == 0 {
		return fn(cur)
	}
	st := steps[0]
	if st.isIndex {
		switch l := cur.(type) {
		case nbt.List:
			if st.index < 0 || st.index >= len(l.Items) {
				return nil, false
			}
			nv, ok := nbtWriteRec(l.Items[st.index], steps[1:], fn)
			if !ok {
				return nil, false
			}
			l.Items[st.index] = nv
			return l, true
		case []any:
			if st.index < 0 || st.index >= len(l) {
				return nil, false
			}
			nv, ok := nbtWriteRec(l[st.index], steps[1:], fn)
			if !ok {
				return nil, false
			}
			l[st.index] = nv
			return l, true
		}
		return nil, false
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false
	}
	nv, ok := nbtWriteRec(m[st.key], steps[1:], fn)
	if !ok {
		return nil, false
	}
	m[st.key] = nv
	return m, true
}

// nbtMergeInto merges src's compound keys into dst — nested compounds
// merge recursively, every other value replaces. Vanilla's
// CompoundTag.merge.
func nbtMergeInto(dst map[string]any, src any) {
	m, ok := src.(map[string]any)
	if !ok {
		return
	}
	for k, v := range m {
		if dm, ok1 := dst[k].(map[string]any); ok1 {
			if sm, ok2 := v.(map[string]any); ok2 {
				nbtMergeInto(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// snbtToValue converts a parsed SNBT tag to the value form the block
// entity stores carry: compounds to maps, lists to nbt.List (or the
// typed Go slices for [B;]/[I;]/[L;] arrays), numbers to the suffix's
// Go width.
func snbtToValue(t *snbtTag) any {
	switch t.kind {
	case snbtCompound:
		m := make(map[string]any, len(t.vals))
		for k, v := range t.vals {
			m[k] = snbtToValue(v)
		}
		return m
	case snbtList:
		switch t.str {
		case "B":
			out := make([]byte, 0, len(t.items))
			for _, it := range t.items {
				n, _ := strconv.ParseInt(strings.TrimSuffix(it.num, "b"), 10, 8)
				out = append(out, byte(n))
			}
			return out
		case "I":
			out := make([]int32, 0, len(t.items))
			for _, it := range t.items {
				n, _ := strconv.ParseInt(strings.TrimRight(it.num, "bBsSiI"), 10, 32)
				out = append(out, int32(n))
			}
			return out
		case "L":
			out := make([]int64, 0, len(t.items))
			for _, it := range t.items {
				n, _ := strconv.ParseInt(strings.TrimSuffix(it.num, "L"), 10, 64)
				out = append(out, int64(n))
			}
			return out
		}
		items := make([]any, 0, len(t.items))
		var elem byte
		for _, it := range t.items {
			v := snbtToValue(it)
			if elem == 0 {
				elem = snbtListTagOf(v)
			}
			items = append(items, v)
		}
		if elem == 0 {
			elem = nbt.TagEnd
		}
		return nbt.List{Elem: elem, Items: items}
	case snbtString:
		return t.str
	case snbtNumber:
		return snbtNumValue(t.num)
	}
	return nil
}

// snbtNumValue parses a canonical SNBT number — "3b", "5", "1.5d",
// "-2L" — into its typed Go value, defaulting to int or double.
func snbtNumValue(num string) any {
	if len(num) == 0 {
		return int32(0)
	}
	last := num[len(num)-1]
	body := num
	if strings.IndexByte("bBsSlLfFdD", last) >= 0 {
		body = num[:len(num)-1]
	}
	switch last {
	case 'b', 'B':
		n, _ := strconv.ParseInt(body, 10, 8)
		return int8(n)
	case 's', 'S':
		n, _ := strconv.ParseInt(body, 10, 16)
		return int16(n)
	case 'l', 'L':
		n, _ := strconv.ParseInt(body, 10, 64)
		return int64(n)
	case 'f', 'F':
		f, _ := strconv.ParseFloat(body, 32)
		return float32(f)
	case 'd', 'D':
		f, _ := strconv.ParseFloat(body, 64)
		return f
	}
	if strings.IndexAny(num, ".eE") >= 0 {
		f, _ := strconv.ParseFloat(num, 64)
		return f
	}
	n, err := strconv.ParseInt(num, 10, 32)
	if err != nil {
		f, _ := strconv.ParseFloat(num, 64)
		return f
	}
	return int32(n)
}

// snbtListTagOf reports a Go value's NBT tag id for list typing.
func snbtListTagOf(v any) byte {
	switch v.(type) {
	case int8:
		return nbt.TagByte
	case int16:
		return nbt.TagShort
	case int32:
		return nbt.TagInt
	case int64:
		return nbt.TagLong
	case float32:
		return nbt.TagFloat
	case float64:
		return nbt.TagDouble
	case []byte:
		return nbt.TagByteArray
	case string:
		return nbt.TagString
	case nbt.List:
		return nbt.TagList
	case map[string]any:
		return nbt.TagCompound
	case []int32:
		return nbt.TagIntArray
	case []int64:
		return nbt.TagLongArray
	}
	return nbt.TagString
}

// nbtPretty renders a queried tag the way the real server prints it:
// the flat element list — punctuation as {"":txt} literals, keys in
// aqua, numbers in gold with a red suffix letter, strings in green
// inside quote literals — wrapped by the caller's {"":""} spacer.
func nbtPretty(v any) []component {
	return nbtPrettyElems(v)
}

func nbtPrettyElems(v any) []component {
	switch t := v.(type) {
	case map[string]any:
		return nbtPrettyMapSeq(t, nil)
	case nbt.List:
		out := []component{{emptyKey: "["}}
		for i, it := range t.Items {
			// String and compound elements carry the same {"": ""}
			// preamble a compound's keys do; numbers and arrays don't.
			switch it.(type) {
			case string, map[string]any:
				out = append(out, component{emptyKey: "", forceCompound: true})
			}
			out = append(out, nbtPrettyElems(it)...)
			if i+1 < len(t.Items) {
				out = append(out, component{emptyKey: ","}, component{emptyKey: " "})
			}
		}
		out = append(out, component{emptyKey: "]"})
		return out
	case []any:
		return nbtPrettyElems(nbt.List{Elem: 0, Items: t})
	case string:
		// The pretty printer quotes strings with the delimiter that
		// needs no escapes: double quotes normally, single quotes when
		// the text carries a double quote.
		q := "\""
		if strings.Contains(t, "\"") && !strings.Contains(t, "'") {
			q = "'"
		}
		return []component{
			{emptyKey: q},
			{color: "green", text: t},
			{emptyKey: q},
		}
	case int8:
		return nbtNumPretty(strconv.FormatInt(int64(t), 10), "b")
	case int16:
		return nbtNumPretty(strconv.FormatInt(int64(t), 10), "s")
	case int32:
		return nbtNumPretty(strconv.FormatInt(int64(t), 10), "")
	case int64:
		return nbtNumPretty(strconv.FormatInt(t, 10), "L")
	case float32:
		return nbtNumPretty(javaFloatStr32(t), "f")
	case float64:
		return nbtNumPretty(javaFloatStr(t), "d")
	case []byte:
		return nbtArrayPretty("B", len(t), func(i int) string { return strconv.Itoa(int(t[i])) })
	case []int32:
		return nbtArrayPretty("I", len(t), func(i int) string { return strconv.Itoa(int(t[i])) })
	case []int64:
		return nbtArrayPretty("L", len(t), func(i int) string { return strconv.FormatInt(t[i], 10) })
	}
	return []component{{emptyKey: strconv.Itoa(0)}}
}

// nbtPrettyMapSeq renders a compound in HashMap bucket order — seq is
// the write order for same-bucket ties (nil = alphabetical fallback).
func nbtPrettyMapSeq(t map[string]any, seq []string) []component {
	out := []component{{emptyKey: "{"}}
	var keys []string
	if seq != nil {
		keys = nbt.OrderedKeysSeq(t, seq)
	} else {
		keys = nbt.OrderedKeys(t)
	}
	for i, k := range keys {
		out = append(out, component{emptyKey: "", forceCompound: true})
		// A key outside SNBT's unquoted-name alphabet ([0-9a-zA-Z_.+-])
		// prints quoted: vanilla emits text:"\"" with the aqua key and
		// the closing quote as extra children.
		if !nbtUnquotedKey(k) {
			out = append(out, component{text: "\"", extra: []component{
				{color: "aqua", text: k},
				{emptyKey: "\""},
			}})
		} else {
			out = append(out, component{color: "aqua", text: k})
		}
		out = append(out,
			component{emptyKey: ":"},
			component{emptyKey: " "})
		out = append(out, nbtPrettyElems(t[k])...)
		if i+1 < len(keys) {
			out = append(out, component{emptyKey: ","}, component{emptyKey: " "})
		}
	}
	out = append(out, component{emptyKey: "}"})
	return out
}

// nbtUnquotedKey is vanilla's isAllowedInUnquotedName: digits, ASCII
// letters, and _ - . + print bare; anything else quotes the key.
func nbtUnquotedKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' ||
			c >= 'A' && c <= 'Z' || c == '_' || c == '-' ||
			c == '.' || c == '+') {
			return false
		}
	}
	return true
}

// javaFloatStr formats like Float/Double.toString: the shortest
// round-trip digits, always carrying a decimal point (3.0, not 3).
func javaFloatStr(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if strings.IndexAny(s, ".eENnI") < 0 {
		s += ".0"
	}
	return s
}

// javaFloatStr32 mirrors Java's Float.toString: the shortest decimal
// that parses back to the same float32. Formatting the float64
// widening instead would print the long tail (94.88f ->
// 94.87999725341797).
func javaFloatStr32(f float32) string {
	s := strconv.FormatFloat(float64(f), 'g', -1, 32)
	if strings.IndexAny(s, ".eENnI") < 0 {
		s += ".0"
	}
	return s
}

// nbtNumPretty renders a number: the digits in gold, then the suffix
// letter in red when the tag carries one (ints and shorts share "s"? —
// vanilla prints the raw suffix the tag writes: byte b, short s,
// long l, float f, double d; ints carry none).
func nbtNumPretty(digits, suffix string) []component {
	out := []component{{color: "gold", text: digits}}
	if suffix != "" {
		out = append(out, component{color: "red", text: suffix})
	}
	return out
}

// nbtArrayPretty renders a typed array [I;-1,2]: the element letter
// in red, "; " then the bare gold digits with no element preamble.
func nbtArrayPretty(letter string, n int, digits func(int) string) []component {
	out := []component{
		{emptyKey: "["},
		{color: "red", text: letter},
		{emptyKey: ";"},
		{emptyKey: " "},
	}
	suffix := strings.ToLower(letter)
	if letter == "I" {
		suffix = ""
	}
	for i := 0; i < n; i++ {
		out = append(out, nbtNumPretty(digits(i), suffix)...)
		if i+1 < n {
			out = append(out, component{emptyKey: ","}, component{emptyKey: " "})
		}
	}
	out = append(out, component{emptyKey: "]"})
	return out
}

// nbtResult is the numeric result a `data get` reports — vanilla's
// DataCommands.getNumeric: numbers pass through truncated, collections
// and compounds report their element count, strings parse or yield 0.
func nbtResult(v any) int {
	switch t := v.(type) {
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	case nbt.List:
		return len(t.Items)
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return int(f)
		}
		return 0
	}
	return 0
}
