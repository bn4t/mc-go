package blackbox

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Oracle: official26.2 SHA-1 823e2250d24b3ddac457a60c92a6a941943fcd6a,
// wire and Temurin25 javap facts from ServerboundEditBookPacket and
// ServerGamePacketListenerImpl's edit/update/sign book methods.
func TestBookEditSignParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BookWriter")}, nil)
	defer tg.cleanup()
	run := func(addr string) []string {
		d := newClickDriverOn(t, addr, "BookWriter", "minecraft:chest", 2, [][2]string{
			{"hotbar.0", `minecraft:writable_book[custom_name='"Keep"']`},
			{"hotbar.1", "minecraft:writable_book"},
			{"hotbar.2", `minecraft:stone[writable_book_content={pages:[]}] 4`},
			{"inventory.0", "minecraft:writable_book"},
			{"weapon.offhand", "minecraft:writable_book"},
		}, nil)
		d.close()
		d.click("sync-player", 10, 0, 0, true, true)
		edit := func(slot int32, pages []string, title *string) {
			var b protocol.Buffer
			b.VarInt(slot)
			b.VarInt(int32(len(pages)))
			for _, page := range pages {
				b.String(page)
			}
			b.Bool(title != nil)
			if title != nil {
				b.String(*title)
			}
			if err := d.pw.WritePacket(server.IDPlayEditBookSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "edit-book")
		}
		var out []string
		sample := func(tag string, slot int32) { out = append(out, fmt.Sprintf("%s %s", tag, d.snap.p0[slot])) }
		edit(0, []string{"one", "two ♥😀", "123", "minecraft:book", "true"}, nil)
		sample("main", 36)
		edit(1, []string{"not selected"}, nil)
		sample("unselected", 37)
		edit(1, nil, nil)
		sample("clear-pages", 37)
		edit(40, []string{"offhand"}, nil)
		sample("offhand", 45)
		title := "  Title 😀  "
		edit(0, []string{"signed", "second", "123", "minecraft:book", "true"}, &title)
		sample("signed", 36)
		if !strings.Contains(d.snap.p0[36], "[bwr") {
			t.Fatalf("not signed: %s", d.snap.p0[36])
		}
		edit(0, []string{"overwrite signed"}, nil)
		sample("signed-reject", 36)
		numericTitle := "123"
		edit(1, []string{"42"}, &numericTitle)
		sample("numeric-title", 37)
		empty := ""
		edit(40, []string{"empty title"}, &empty)
		sample("blank-title", 45)
		edit(9, []string{"wrong slot"}, nil)
		sample("main-slot-reject", 9)
		edit(2, []string{"component carrier"}, nil)
		sample("carrier-edit", 38)
		edit(2, []string{"carrier signed"}, &title)
		sample("carrier-sign", 38)
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	for i := range want {
		t.Logf("vanilla %s; mcgo %s", want[i], got[i])
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("book edit/sign state differs")
	}
}
