package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestBookEditSigningAndOffhand(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "BookAuthor", 0, 70, 0)
	source := ItemStack{ID: world.ItemIDs["writable_book"], Count: 1, Name: "Kept", RepairCost: 7}
	sess.setStack(slotOffhand, source)
	if !s.applyBookEdit(sess, 40, []string{"page 😀", "123", "minecraft:book", "true"}, nil) {
		t.Fatal("offhand edit rejected")
	}
	edited := sess.stackAt(slotOffhand)
	if edited.BookWritable == "" || edited.Name != "Kept" {
		t.Fatalf("edit lost components: %+v", edited)
	}
	title := "  Title 😀  "
	if !s.applyBookEdit(sess, 40, []string{"signed"}, &title) {
		t.Fatal("signing rejected")
	}
	signed := sess.stackAt(slotOffhand)
	if signed.ID != world.ItemIDs["written_book"] || signed.BookWritable != "" || signed.Name != "Kept" || signed.RepairCost != 7 {
		t.Fatalf("bad transmutation %+v", signed)
	}
	value := bookContentValue(signed, true)
	if raw, _, _ := filterableParts(value["title"]); raw != title || value["author"] != "BookAuthor" || nbtNumberOr(value["resolved"], 0) != 1 {
		t.Fatalf("signed content %+v", value)
	}
	if s.applyBookEdit(sess, 40, []string{"overwrite"}, nil) {
		t.Fatal("edited a signed book")
	}
	back := itemFromNBT(itemNBT(signed))
	if back.BookWritten != signed.BookWritten {
		t.Fatal("signed content not persistent")
	}
	var b protocol.Buffer
	writeSlot(&b, signed)
	wire, err := readCanonicalSlot(protocol.NewPacketReader(b.Bytes()), 0)
	if err != nil || wire.BookWritten != signed.BookWritten {
		t.Fatalf("signed wire mismatch %v", err)
	}
}
func TestBookEditLimitsAndComponentGate(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "BookLimits", 0, 70, 0)
	patch, _, ok := parseItemPatch("writable_book_content={pages:[]}", 0)
	if !ok || patch.BookWritable == "" {
		t.Fatal("empty writable component rejected")
	}
	st := ItemStack{ID: world.ItemIDs["stone"], Count: 4, BookWritable: patch.BookWritable}
	sess.setStack(slotHotbarStart+2, st)
	if !s.applyBookEdit(sess, 2, []string{strings.Repeat("😀", 512)}, nil) {
		t.Fatal("valid UTF16 page boundary rejected")
	}
	plain := ItemStack{ID: world.ItemIDs["writable_book"], Count: 1, BookWritable: st.BookWritable}
	sess.setStack(slotHotbarStart, plain)
	if !s.applyBookEdit(sess, 0, nil, nil) || sess.stackAt(slotHotbarStart).BookWritable != "" {
		t.Fatal("empty pages did not restore default component")
	}
	before := sess.stackAt(slotHotbarStart + 2)
	if s.applyBookEdit(sess, 2, []string{strings.Repeat("😀", 513)}, nil) || sess.stackAt(slotHotbarStart+2) != before {
		t.Fatal("oversized page mutated stack")
	}
	title := strings.Repeat("😀", 16)
	if !s.applyBookEdit(sess, 2, nil, &title) {
		t.Fatal("valid title boundary rejected")
	}
	if got := sess.stackAt(slotHotbarStart + 2); got.ID != world.ItemIDs["written_book"] || got.Count != 4 {
		t.Fatalf("component-bearing stack did not transmute %+v", got)
	}
	sess.setStack(slotMainStart, ItemStack{ID: world.ItemIDs["writable_book"], Count: 1})
	if s.applyBookEdit(sess, 9, []string{"bad slot"}, nil) {
		t.Fatal("accepted main inventory slot")
	}
}
