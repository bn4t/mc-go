package server

import "mc-go/internal/nbt"

// Crystal summons bypass the ordinary mob loader, but scoreboard tags
// still form a set and participate in selectors and saved entity data.
func crystalSummonTags(e *Entity, save map[string]any) {
	list, ok := save["Tags"].(nbt.List)
	if !ok || list.Elem != nbt.TagString {
		return
	}
	seen := make(map[string]bool, len(list.Items))
	tags := make([]any, 0, len(list.Items))
	for _, item := range list.Items {
		tag, ok := item.(string)
		if ok && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	if e.ExtraNBT == nil {
		e.ExtraNBT = map[string]any{}
	}
	e.ExtraNBT["Tags"] = nbt.List{Elem: nbt.TagString, Items: tags}
}
