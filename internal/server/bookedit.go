package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"unicode/utf8"
)

// applyBookEdit implements the measured26.2 edit-book state transition.
// Source: Temurin25 javap facts from ServerGamePacketListenerImpl's
// updateBookContents/signBook and ServerboundEditBookPacket's stream codec.
// Hotbar slots and offhand40 qualify; the writable component gates both
// transitions. Signing preserves the stack's other components and count.
func (s *Server) applyBookEdit(sess *Session, slot int32, pages []string, title *string) bool {
	if slot < 0 || slot > 8 && slot != 40 || len(pages) > 100 {
		return false
	}
	for _, page := range pages {
		if !validBookText(page, 1024) {
			return false
		}
	}
	if title != nil && !validBookText(*title, 32) {
		return false
	}
	index := int16(slotHotbarStart + slot)
	if slot == 40 {
		index = slotOffhand
	}
	sess.invMu.Lock()
	it := sess.inv[index]
	name, _ := world.ItemName(it.ID)
	if it.Empty() || name != "writable_book" && it.BookWritable == "" {
		sess.invMu.Unlock()
		return false
	}
	list := nbt.List{Elem: nbt.TagCompound, Items: make([]any, 0, len(pages))}
	for _, page := range pages {
		list.Items = append(list.Items, map[string]any{"raw": page})
	}
	if title == nil {
		it.BookWritable = componentSNBT(map[string]any{"pages": list})
		if name == "writable_book" && len(pages) == 0 {
			it.BookWritable = ""
		}
	} else {
		it.ID = world.ItemIDs["written_book"]
		it.BookWritable = ""
		it.BookWritten = componentSNBT(map[string]any{"title": map[string]any{"raw": *title}, "author": sess.getName(), "generation": int32(0), "pages": list, "resolved": int8(1)})
	}
	sess.inv[index] = it
	sess.invMu.Unlock()
	sess.invState.Add(1)
	_ = s.sendContainerSlot(sess, index, it)
	return true
}
func validBookText(text string, maxUnits int) bool {
	if !utf8.ValidString(text) || len(text) > maxUnits*3 {
		return false
	}
	units := 0
	for _, r := range text {
		units++
		if r > 0xffff {
			units++
		}
		if units > maxUnits {
			return false
		}
	}
	return true
}
func (s *Server) handleBookEditPacket(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.VarInt()
	if err != nil {
		return
	}
	count, err := r.VarInt()
	if err != nil || count < 0 || count > 100 {
		return
	}
	pages := make([]string, 0, count)
	for range count {
		page, err := r.String()
		if err != nil || !validBookText(page, 1024) {
			return
		}
		pages = append(pages, page)
	}
	signed, err := r.Bool()
	if err != nil {
		return
	}
	var title *string
	if signed {
		text, err := r.String()
		if err != nil || !validBookText(text, 32) {
			return
		}
		title = &text
	}
	if r.Remaining() != 0 {
		return
	}
	s.applyBookEdit(sess, slot, pages, title)
}
