package server

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Bundle facts measured from 26.2 BundleContents, its Mutable view and
// BundleItem using Temurin25 javap. List order is newest-first; selecting an
// already-selected or invalid index clears selection. Selection itself is
// deliberately absent from the list-only wire/save component.
const compBundleContents = 50

func isBundle(it ItemStack) bool {
	name, ok := world.ItemName(it.ID)
	return !it.Empty() && ok && (name == "bundle" || strings.HasSuffix(name, "_bundle"))
}
func bundleItems(it ItemStack) []ItemStack {
	if it.Bundle == "" {
		return nil
	}
	var out []ItemStack
	if len(it.Bundle) > 1<<20 || json.Unmarshal([]byte(it.Bundle), &out) != nil || len(out) > 1024 {
		return nil
	}
	return out
}
func bundleSetItems(it *ItemStack, items []ItemStack) {
	if len(items) == 0 {
		it.Bundle = ""
		it.BundleSelected = 0
		return
	}
	raw, _ := json.Marshal(items)
	it.Bundle = string(raw)
}
func bundleToggle(it *ItemStack, index int32) {
	if !isBundle(*it) && it.Bundle == "" {
		return
	}
	if index < 0 || int(index) >= len(bundleItems(*it)) || it.BundleSelected == index+1 {
		it.BundleSelected = 0
	} else {
		it.BundleSelected = index + 1
	}
}
func bundleRemove(it *ItemStack) ItemStack {
	items := bundleItems(*it)
	if len(items) == 0 {
		return ItemStack{}
	}
	index := int(it.BundleSelected) - 1
	if index < 0 || index >= len(items) {
		index = 0
	}
	out := items[index]
	items = append(items[:index], items[index+1:]...)
	bundleSetItems(it, items)
	it.BundleSelected = 0
	return out
}
func bundleWeight(it ItemStack, depth int) *big.Rat {
	if depth > 16 {
		return big.NewRat(1<<30, 1)
	}
	if isBundle(it) || it.Bundle != "" {
		weight := big.NewRat(1, 16)
		for _, child := range bundleItems(it) {
			weight.Add(weight, new(big.Rat).Mul(bundleWeight(child, depth+1), big.NewRat(int64(child.Count), 1)))
		}
		return weight
	}
	if it.Bees > 0 {
		return big.NewRat(1, 1)
	}
	return big.NewRat(1, int64(maxStackOfID(it.ID)))
}
func bundleInsert(bundle *ItemStack, source *ItemStack) int32 {
	if source.Empty() {
		return 0
	}
	name, _ := world.ItemName(source.ID)
	if name == "shulker_box" || strings.HasSuffix(name, "_shulker_box") {
		return 0
	}
	items := bundleItems(*bundle)
	remaining := big.NewRat(1, 1)
	for _, child := range items {
		remaining.Sub(remaining, new(big.Rat).Mul(bundleWeight(child, 0), big.NewRat(int64(child.Count), 1)))
	}
	weight := bundleWeight(*source, 0)
	if remaining.Sign() <= 0 || weight.Sign() <= 0 {
		return 0
	}
	quotient := new(big.Rat).Quo(remaining, weight)
	capacity := new(big.Int).Quo(quotient.Num(), quotient.Denom())
	n := source.Count
	if capacity.IsInt64() && capacity.Int64() < int64(n) {
		n = int32(capacity.Int64())
	}
	if n <= 0 {
		return 0
	}
	inserted := source.withCount(n)
	for i, it := range items {
		if inserted.Stackable(it) {
			inserted.Count += it.Count
			items = append(items[:i], items[i+1:]...)
			break
		}
	}
	items = append([]ItemStack{inserted}, items...)
	bundleSetItems(bundle, items)
	source.Count -= n
	if source.Count <= 0 {
		*source = ItemStack{}
	}
	return n
}
func writeBundleContents(b *protocol.Buffer, it ItemStack) {
	items := bundleItems(it)
	b.VarInt(int32(len(items)))
	for _, child := range items {
		b.VarInt(child.ID)
		b.VarInt(child.Count)
		writeSlotPatch(b, child)
	}
}
func readBundleContents(r *protocol.PacketReader, depth int) (string, error) {
	n, err := slotWireCount(r, 1024)
	if err != nil {
		return "", err
	}
	items := make([]ItemStack, 0, n)
	for range n {
		id, err := r.VarInt()
		if err != nil {
			return "", err
		}
		count, err := slotWireCount(r, 127)
		if err != nil || count == 0 {
			return "", fmt.Errorf("bundle: invalid item count")
		}
		child, err := readCanonicalStackTail(r, ItemStack{ID: id, Count: count}, depth+1)
		if err != nil {
			return "", err
		}
		items = append(items, child)
	}
	var out ItemStack
	bundleSetItems(&out, items)
	return out.Bundle, nil
}
func bundleContentsNBT(it ItemStack) nbt.List {
	items := bundleItems(it)
	out := nbt.List{Elem: nbt.TagCompound, Items: make([]any, 0, len(items))}
	for _, child := range items {
		out.Items = append(out.Items, itemNBT(child))
	}
	return out
}
func bundleContentsFromNBT(v any) (string, error) {
	list, ok := v.(nbt.List)
	if !ok || len(list.Items) > 1024 {
		return "", fmt.Errorf("bundle: expected item list")
	}
	items := make([]ItemStack, 0, len(list.Items))
	for _, value := range list.Items {
		m, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("bundle: expected item")
		}
		name, _ := m["id"].(string)
		id, ok := world.ItemID(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			return "", fmt.Errorf("bundle: unknown item")
		}
		child := ItemStack{ID: id, Count: nbtNumberOr(m["count"], 1)}
		if child.Count < 1 || child.Count > maxStackOfID(id) {
			return "", fmt.Errorf("bundle: invalid count")
		}
		if components, ok := m["components"].(map[string]any); ok {
			keys := make([]string, 0, len(components))
			for key := range components {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, key := range keys {
				parts = append(parts, key+"="+componentSNBT(components[key]))
			}
			patch, _, ok := parseItemPatch(strings.Join(parts, ","), 0)
			if !ok {
				return "", fmt.Errorf("bundle: unmodeled component")
			}
			patch.ID, patch.Count = id, child.Count
			child = patch
		}
		items = append(items, child)
	}
	var out ItemStack
	bundleSetItems(&out, items)
	return out.Bundle, nil
}
