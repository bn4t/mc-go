package blackbox

import (
	"bytes"
	"math"
	"sort"
	"strconv"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Spawn-state sync parity: experience, attributes, join clocks, and
// server data, measured against a live 26.2 server.
//
// Fresh joins carry Set Experience (0,0,0) right after Set Health and
// one Update Attributes with the full creative set (block range 8,
// entity range 13, speed 26) after the opening chunk batch. Rejoins
// carry a reset stub (always the bare speed entry, plus bare ranges
// and/or degenerate fillers) followed by the two ranged modifiers.
// Entry order shuffles per send on vanilla, so entries compare
// canonically; the two filler shapes are byte-exact everywhere they
// appear.

// spanAttr is one decoded Update Attributes entry: either well-formed
// (id, base, modifiers) or degenerate (filler raw bytes, no modifier
// count on the wire).
type spanAttr struct {
	id    int32
	base  float64
	mods  []spanMod
	degen []byte // raw filler bytes when non-nil
}

type spanMod struct {
	key    string
	amount float64
	op     int32
}

// decodeVarint reads a VarInt from b at *off.
func decodeVarint(b []byte, off *int) (int32, bool) {
	var v int32
	for s := uint(0); ; s += 7 {
		if *off >= len(b) {
			return 0, false
		}
		x := b[*off]
		*off++
		v |= int32(x&0x7f) << s
		if x&0x80 == 0 {
			return v, true
		}
		if s >= 28 {
			return 0, false
		}
	}
}

func decodeDouble(b []byte, off *int) (float64, bool) {
	if *off+8 > len(b) {
		return 0, false
	}
	v := uint64(0)
	for i := 0; i < 8; i++ {
		v = v<<8 | uint64(b[*off+i])
	}
	*off += 8
	return math.Float64frombits(v), true
}

// parseSpanAttrs decodes an Update Attributes body. Well-formed and
// degenerate entries are told apart by trial: a well-formed reading
// must consume exactly to the packet end with the header's entry
// count. Modifier counts above 4 are never well-formed (vanilla sends
// 0 or 1), which separates a degenerate id's first base byte from a
// real count without backtracking across the whole packet.
func parseSpanAttrs(t *testing.T, body []byte) (int32, []spanAttr) {
	t.Helper()
	off := 0
	eid, ok := decodeVarint(body, &off)
	if !ok {
		t.Fatalf("attributes: bad eid in %x", body)
	}
	n, ok := decodeVarint(body, &off)
	if !ok {
		t.Fatalf("attributes: bad count in %x", body)
	}
	var out []spanAttr
	for off < len(body) {
		start := off
		id, ok := decodeVarint(body, &off)
		if !ok {
			t.Fatalf("attributes: bad id in %x", body)
		}
		// A degenerate filler is an id varint plus 8 base bytes with
		// nothing after. Only ids 84 and 132 ever appear degenerate;
		// anything else must parse well-formed.
		if id == 84 || id == 132 {
			if off+8 > len(body) {
				t.Fatalf("attributes: truncated filler in %x", body)
			}
			raw := append([]byte(nil), body[start:off+8]...)
			off += 8
			if off < len(body) {
				// A filler mid-packet is followed by more entries; a
				// well-formed reading would take the filler's base as
				// a modifier count, which never parses.
				out = append(out, spanAttr{id: id, degen: raw})
				continue
			}
			out = append(out, spanAttr{id: id, degen: raw})
			continue
		}
		base, ok := decodeDouble(body, &off)
		if !ok {
			t.Fatalf("attributes: bad base in %x", body)
		}
		m, ok := decodeVarint(body, &off)
		if !ok {
			t.Fatalf("attributes: bad modcount in %x", body)
		}
		var e spanAttr
		e.id, e.base = id, base
		for i := int32(0); i < m; i++ {
			var mo spanMod
			kl, ok := decodeVarint(body, &off)
			if !ok || kl < 0 || off+int(kl) > len(body) {
				t.Fatalf("attributes: bad mod key in %x", body)
			}
			mo.key = string(body[off : off+int(kl)])
			off += int(kl)
			if mo.amount, ok = decodeDouble(body, &off); !ok {
				t.Fatalf("attributes: bad mod amount in %x", body)
			}
			if mo.op, ok = decodeVarint(body, &off); !ok {
				t.Fatalf("attributes: bad mod op in %x", body)
			}
			e.mods = append(e.mods, mo)
		}
		out = append(out, e)
	}
	if int32(len(out)) != n {
		t.Fatalf("attributes: count %d but %d entries in %x", n, len(out), body)
	}
	return eid, out
}

// fbits renders a float by its IEEE bits, so widened float32 bases
// compare exactly.
func fbits(v float64) string { return strconv.FormatUint(math.Float64bits(v), 16) }

// canonAttr renders an entry for order-insensitive comparison. Floats
// compare by bits, so the widened float32 speed base is exact.
func canonAttr(e spanAttr) string {
	if e.degen != nil {
		return "filler:" + string(e.degen)
	}
	s := itoa(e.id) + ":" + fbits(e.base)
	for _, m := range e.mods {
		s += ";" + m.key + ":" + fbits(m.amount) + ":" + itoa(m.op)
	}
	return s
}

func attrMultiset(es []spanAttr) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, canonAttr(e))
	}
	sort.Strings(out)
	return out
}

func wantFullAttrs() []string {
	speed := "26:" + fbits(float64(float32(0.1)))
	block := "8:" + fbits(4.5) + ";minecraft:creative_mode_block_range:" + fbits(0.5) + ":0"
	entity := "13:" + fbits(3.0) + ";minecraft:creative_mode_entity_range:" + fbits(2.0) + ":0"
	out := []string{speed, block, entity}
	sort.Strings(out)
	return out
}

// checkFullAttrs requires the fresh-join sync: exactly the three
// entries with modifiers, no fillers.
func checkFullAttrs(t *testing.T, tag string, body []byte) {
	t.Helper()
	_, es := parseSpanAttrs(t, body)
	got := attrMultiset(es)
	want := wantFullAttrs()
	if len(got) != len(want) {
		t.Fatalf("%s: attributes %q, want %q", tag, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: attributes %q, want %q", tag, got, want)
		}
	}
}

// checkModsAttrs requires the rejoin follow-up: exactly the two ranged
// modifiers, which overwrite the stub and complete the set.
func checkModsAttrs(t *testing.T, tag string, body []byte) {
	t.Helper()
	_, es := parseSpanAttrs(t, body)
	got := attrMultiset(es)
	want := wantFullAttrs()
	// The follow-up carries no speed entry; the stub already did.
	want = want[:0:0]
	block := "8:" + fbits(4.5) + ";minecraft:creative_mode_block_range:" + fbits(0.5) + ":0"
	entity := "13:" + fbits(3.0) + ";minecraft:creative_mode_entity_range:" + fbits(2.0) + ":0"
	want = []string{block, entity}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s: attributes %q, want %q", tag, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: attributes %q, want %q", tag, got, want)
		}
	}
}

// checkStubAttrs requires the rejoin reset invariant: the bare speed
// entry is always present, every other slot is a bare range or a
// byte-exact filler, and the packet holds 3 or 4 slots.
func checkStubAttrs(t *testing.T, tag string, body []byte) {
	t.Helper()
	_, es := parseSpanAttrs(t, body)
	if len(es) != 3 && len(es) != 4 {
		t.Fatalf("%s: stub holds %d entries: %q", tag, len(es), attrMultiset(es))
	}
	speed := "26:" + fbits(float64(float32(0.1)))
	blockBare := "8:" + fbits(4.5)
	entityBare := "13:" + fbits(3.0)
	blockFill := "filler:" + string([]byte{0xd4, 0x00, 0x08, 0, 0, 0, 0, 0, 0, 0})
	entityFill := "filler:" + string([]byte{0x84, 0x01, 0x20, 0, 0, 0, 0, 0, 0, 0})
	seenSpeed := false
	for _, c := range attrMultiset(es) {
		switch c {
		case speed:
			seenSpeed = true
		case blockBare, entityBare, blockFill, entityFill:
		default:
			t.Fatalf("%s: stub has unexpected entry %q in %q", tag, c, attrMultiset(es))
		}
	}
	if !seenSpeed {
		t.Fatalf("%s: stub lacks bare speed in %q", tag, attrMultiset(es))
	}
}

// TestSpawnStateParity joins a fresh name on both servers and compares
// the spawn sync: experience zeros, the full attribute set, the join
// clock shape, and the server data shape.
func TestSpawnStateParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct {
		tag  string
		addr string
		motd string
	}{
		{"vanilla", tg.vanilla, "vanilla-baseline"},
		{"mc-go", tg.mcgo, server.MOTD},
	} {
		c, pr, _ := joinPlayBlackbox(t, tc.addr, "SpawnState")
		func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(60 * time.Second))
			var exp, clocked, attrs, serverdata []byte
			for i := 0; i < 200 && (exp == nil || clocked == nil || attrs == nil || serverdata == nil); i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("%s: %v", tc.tag, err)
				}
				switch p.ID {
				case server.IDPlaySetExperience:
					if exp == nil {
						exp = p.Data
					}
				case server.IDPlayUpdateTime:
					if clocked == nil && len(p.Data) != 9 {
						clocked = p.Data
					}
				case server.IDPlayUpdateAttributes:
					if attrs == nil {
						attrs = p.Data
					}
				case server.IDPlayServerData:
					if serverdata == nil {
						serverdata = p.Data
					}
				}
			}
			if exp == nil || clocked == nil || attrs == nil || serverdata == nil {
				t.Fatalf("%s: missing spawn packets exp=%v clocked=%v attrs=%v serverdata=%v",
					tc.tag, exp != nil, clocked != nil, attrs != nil, serverdata != nil)
			}
			if !bytes.Equal(exp, []byte{0, 0, 0, 0, 0, 0}) {
				t.Fatalf("%s: experience %x, want zeros", tc.tag, exp)
			}
			checkFullAttrs(t, tc.tag+" fresh", attrs)
			checkClocked(t, tc.tag, clocked)
			checkServerData(t, tc.tag, serverdata, tc.motd)
		}()
	}
}

// checkClocked requires the young-world join clock shape: the age,
// then the game-time clock (id 1) at the age and the day clock (id 0
// or 2 - vanilla shuffles the id per join) at the day, which equals
// the age on a fresh world give or take a tick. Entry order shuffles
// too, so the two clocks compare as a set.
func checkClocked(t *testing.T, tag string, body []byte) {
	t.Helper()
	if len(body) != 29 {
		t.Fatalf("%s: clocked length %d, want 29", tag, len(body))
	}
	r := protocol.NewPacketReader(body)
	age, _ := r.Long()
	n, _ := r.VarInt()
	if n != 2 {
		t.Fatalf("%s: clocked count %d, want 2", tag, n)
	}
	type clock struct {
		id   int32
		tm   int64
		fr   float32
		rate float32
	}
	var cs []clock
	for i := 0; i < 2; i++ {
		var c clock
		c.id, _ = r.VarInt()
		c.tm, _ = r.VarLong()
		c.fr, _ = r.Float()
		c.rate, _ = r.Float()
		cs = append(cs, c)
	}
	var game, day *clock
	for i := range cs {
		if cs[i].id == 1 {
			game = &cs[i]
		} else if cs[i].id == 0 || cs[i].id == 2 {
			day = &cs[i]
		} else {
			t.Fatalf("%s: clock id %d, want 1 plus 0 or 2", tag, cs[i].id)
		}
	}
	if game == nil || day == nil {
		t.Fatalf("%s: clocks %v, want game plus day", tag, cs)
	}
	if game.tm != age || game.fr != 0 || game.rate != 1 {
		t.Fatalf("%s: game clock %+v vs age %d", tag, *game, age)
	}
	if day.tm < age-2 || day.tm > age+2 || day.fr != 0 || day.rate != 1 {
		t.Fatalf("%s: day clock %+v vs age %d", tag, *day, age)
	}
}

// checkServerData requires the server data shape: an unnamed NBT
// string tag carrying the MOTD, then the icon-absent flag.
func checkServerData(t *testing.T, tag string, body []byte, motd string) {
	t.Helper()
	r := protocol.NewPacketReader(body)
	tagByte, err := r.Byte()
	if err != nil || tagByte != 8 {
		t.Fatalf("%s: nbt tag %#x, want string(8)", tag, tagByte)
	}
	// NBT strings carry a 2-byte big-endian length, not a VarInt.
	hi, err := r.Byte()
	if err != nil {
		t.Fatalf("%s: motd len: %v", tag, err)
	}
	lo, err := r.Byte()
	if err != nil {
		t.Fatalf("%s: motd len: %v", tag, err)
	}
	raw, err := r.Read(int(hi)<<8 | int(lo))
	if err != nil || string(raw) != motd {
		t.Fatalf("%s: motd %q, want %q", tag, raw, motd)
	}
	more, err := r.Bool()
	if err != nil || more {
		t.Fatalf("%s: icon flag %v, want absent", tag, more)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%s: server data trailing bytes: %d", tag, r.Remaining())
	}
}

// TestRejoinAttributesParity joins, leaves, and rejoins one name on
// both servers: the rejoin must draw the reset stub plus the modifier
// follow-up, combining to the full set.
func TestRejoinAttributesParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, _, _ := joinPlayBlackbox(t, tc.addr, "Returner")
		c.Close()
		time.Sleep(2 * time.Second)
		c2, pr2, _ := joinPlayBlackbox(t, tc.addr, "Returner")
		func() {
			defer c2.Close()
			_ = c2.SetDeadline(time.Now().Add(60 * time.Second))
			var stub, mods []byte
			for i := 0; i < 200 && (stub == nil || mods == nil); i++ {
				p, err := pr2.ReadPacket()
				if err != nil {
					t.Fatalf("%s rejoin: %v", tc.tag, err)
				}
				if p.ID == server.IDPlayUpdateAttributes {
					if stub == nil {
						stub = p.Data
					} else if mods == nil {
						mods = p.Data
					}
				}
			}
			if stub == nil || mods == nil {
				t.Fatalf("%s rejoin: stub=%v mods=%v", tc.tag, stub != nil, mods != nil)
			}
			checkStubAttrs(t, tc.tag+" stub", stub)
			checkModsAttrs(t, tc.tag+" mods", mods)
		}()
	}
}
