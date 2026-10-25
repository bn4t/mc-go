package blackbox

// Lectern book parity: the same script runs on both servers — a book
// placed with a use writes has_book and plays item.book.put, the lectern
// menu opens on the next use, its page buttons broadcast Container
// Property 0 plus the 1043 page-turn effect with the powered pulse, the
// comparator behind the lectern reports the page-scaled OutputSignal,
// and the take-book button, a click pick-up of the slot, a data merge
// of Book+Page and a malformed component give all behave identically.
// The verdict strings folded per leg are compared verbatim between the
// vanilla and system-under-test runs.

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestLecternParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("LectV", "LectM"),
	}, nil)
	defer tg.cleanup()

	lectern := [3]int32{8, -61, 0}
	comp := [3]int32{7, -61, 0}

	run := func(addr, name string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		t.Cleanup(func() { c.Close() })
		_ = c.SetDeadline(time.Time{})
		d := &clickDriver{
			t:     t,
			name:  name,
			sink:  newUseClient(pr, pw, true),
			pw:    pw,
			snap:  clickSnap{carried: "-", menu: map[int32]string{}, p0: map[int32]string{}},
			debug: os.Getenv("MCGO_CLICKDEBUG") != "",
		}
		sendClientInformation(t, pw)
		if !digSentinel(t, pw, d.sink, "READY", 30*time.Second) {
			t.Fatalf("%s: never ready: %v", name, d.sink.err)
		}
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		setup := func(tag, cmd string) {
			t.Helper()
			sendCommand(t, pw, cmd)
			if !digSentinel(t, pw, d.sink, tag, 10*time.Second) {
				t.Fatalf("%s: no %s ack for %q", name, tag, cmd)
			}
		}
		setup("TP", "tp @s 6 -60 2 0 0")
		setup("SB", "setblock 8 -61 0 minecraft:lectern")
		setup("CB", "setblock 7 -61 0 minecraft:comparator[facing=west]")
		d.sink.take()

		var out []string
		// dataGet sends one block query and returns the query chat's
		// raw payload — the whole translated line, NBT print and all.
		dataGet := func(path string) []byte {
			t.Helper()
			sendCommand(t, pw, fmt.Sprintf("data get block 8 -61 0%s", path))
			sendCommand(t, pw, "say ZDMARK")
			var got []byte
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) && got == nil {
				time.Sleep(40 * time.Millisecond)
				for _, p := range d.sink.take() {
					if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("data.block.query")) {
						got = p.data
					}
					d.foldPkts([]usePkt{p})
				}
			}
			return got
		}
		compGet := func() []byte {
			t.Helper()
			sendCommand(t, pw, "data get block 7 -61 0")
			sendCommand(t, pw, "say ZCMARK")
			var got []byte
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) && got == nil {
				time.Sleep(40 * time.Millisecond)
				for _, p := range d.sink.take() {
					if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("data.block.query")) {
						got = p.data
					}
					d.foldPkts([]usePkt{p})
				}
			}
			return got
		}
		// snds renders the named-sound tally deterministically.
		snds := func() string {
			ids := make([]int, 0, len(d.soundIDs))
			for id := range d.soundIDs {
				ids = append(ids, int(id))
			}
			sort.Ints(ids)
			var parts []string
			for _, id := range ids {
				parts = append(parts, fmt.Sprintf("%d:%d", id, d.soundIDs[int32(id)]))
			}
			return strings.Join(parts, ",")
		}
		snap := func() string {
			t.Helper()
			d.foldPkts(d.sink.take())
			return fmt.Sprintf("props=%v lect=%d comp=%d ev1043=%d snd=%s menu0=%s carried=%s inv=%d close=%v",
				d.props, d.blkUps[lectern], d.blkUps[comp], d.pageTurns,
				snds(), d.snap.menu[0], d.snap.carried, d.invSets, d.sawClose)
		}
		use := func(tag string, seq int32) {
			t.Helper()
			var pkts []usePkt
			sendUseItemOn(t, pw, 8, -61, 0, 1, seq)
			settleUse(t, d.sink, func(b []usePkt) { pkts = append(pkts, b...) }, tag)
			d.foldPkts(pkts)
			v := judgeUse(pkts)
			var title string
			if v.title != nil {
				title = string(v.title)
			}
			out = append(out, fmt.Sprintf("%s:ack=%v open=%v menu=%d title=%s|%s", tag, v.acks, v.open, v.menu, title, snap()))
		}

		out = append(out, "empty:"+string(dataGet("")))
		out = append(out, "comp0:"+string(compGet()))

		setup("IT", `item replace entity @s weapon.mainhand with minecraft:written_book[minecraft:written_book_content={title:"T",author:"A",generation:2,pages:["p1","p2","p3"]}]`)
		d.sink.take()
		d.props, d.blkUps, d.soundIDs = nil, nil, nil
		use("place", 20)
		out = append(out, "booked:"+string(dataGet("")))
		out = append(out, "analog1:"+string(compGet()))
		use("open", 21)
		d.button("pg2", 102)
		out = append(out, "pg2:"+snap())
		out = append(out, "page2:"+string(dataGet(" Page")))
		out = append(out, "analog15:"+string(compGet()))
		d.button("prev", 1)
		out = append(out, "prev:"+snap())
		out = append(out, "analog8:"+string(compGet()))
		d.button("take", 3)
		out = append(out, "take:"+snap())
		out = append(out, "took:"+string(dataGet("")))
		out = append(out, "analog0:"+string(compGet()))

		setup("IT2", `item replace entity @s weapon.mainhand with minecraft:writable_book[minecraft:writable_book_content={pages:["x","y"]}]`)
		d.sink.take()
		use("place2", 22)
		out = append(out, "booked2:"+string(dataGet("")))
		use("open2", 23)
		d.click("pick", 0, 0, 0, false, false)
		out = append(out, "pick:"+snap())
		d.click("park", 20, 0, 0, false, false)
		out = append(out, "park:"+snap())
		out = append(out, "picked:"+string(dataGet("")))
		d.close()
		out = append(out, "closed:"+snap())

		setup("MG", `data merge block 8 -61 0 {Book:{id:"minecraft:written_book",count:1,components:{"minecraft:written_book_content":{title:"M",author:"B",pages:["x","y"]}}},Page:1}`)
		d.sink.take()
		out = append(out, "merged:"+string(dataGet("")))
		setup("RM", "data remove block 8 -61 0 Book")
		d.sink.take()
		out = append(out, "removed:"+string(dataGet("")))

		setup("BAD", `item replace entity @s weapon.mainhand with minecraft:written_book[minecraft:written_book_content={title:"X"}]`)
		d.sink.take()
		deadline := time.Now().Add(5 * time.Second)
		var bad []byte
		for time.Now().Before(deadline) && bad == nil {
			time.Sleep(40 * time.Millisecond)
			for _, p := range d.sink.take() {
				if p.id == server.IDPlaySystemChatCB && bytes.Contains(p.data, []byte("component.malformed")) {
					bad = p.data
				}
				d.foldPkts([]usePkt{p})
			}
		}
		out = append(out, "bad:"+string(bad))
		return out
	}

	v := run(tg.vanilla, "LectV")
	m := run(tg.mcgo, "LectM")
	if len(v) != len(m) {
		t.Fatalf("leg count differs: vanilla %d vs mcgo %d\nvanilla: %v\nmcgo: %v", len(v), len(m), v, m)
	}
	for i := range v {
		if v[i] != m[i] {
			t.Errorf("leg %d differs:\n vanilla: %s\n    mcgo: %s", i, v[i], m[i])
		}
	}
}
