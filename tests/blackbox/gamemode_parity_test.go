package blackbox

// /gamemode parity: two same-named operators on each server run an
// identical /gamemode script, and every answer packet is compared
// across servers. Chats compare as canonicalized NBT trees (compounds
// sorted by name with every key, value, style flag, and nesting level
// pinned: mc-go encodes compounds in Go map order, which is
// nondeterministic, while vanilla follows builder insertion order, so
// field byte order is out of scope); GameEvent compares byte for byte;
// Update Attributes compares parsed as a set
// (vanilla shuffles entry order per send); the Player Info Update
// compares parsed (uuid plus mode), and abilities compare as a flag
// set (the switch answers the same flags twice; the count is not
// pinned). Offline UUIDs are deterministic per name, so player refs
// match. A mode switch is proved by its observable effects -
// refreshed abilities, reach attributes, tab-list mode - rather than
// by reopening menus on a second connection, which would relog the
// player and conflate persistence with the switch; clone behavior
// itself is covered by TestSurvivalClickRestrictions through the same
// mode gate. Vanilla joins in survival and mc-go in creative, so the
// script normalizes both players to creative first and proves it with
// a silent repeat before anything else is compared.

import (
	"bytes"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// modePlayer is one scripted client in the gamemode test.
type modePlayer struct {
	name string
	pw   *protocol.Writer
	sink *useClient
}

// modeMod is one parsed attribute modifier triple.
type modeMod struct {
	key    string
	amount float64
	op     int32
}

// modeAttr is one parsed Update Attributes entry; the entity id is
// dropped, since each server assigns its own.
type modeAttr struct {
	id   int32
	base float64
	mods []modeMod
}

// modeInfo is one parsed gamemode-only Player Info Update entry.
type modeInfo struct {
	uuid [16]byte
	mode int32
}

// modeCap is everything one /gamemode command answers on one
// connection: chats as canonical trees, events raw, attributes and
// playerinfo parsed, abilities as the flag byte of each packet.
type modeCap struct {
	chats  []string
	events [][]byte
	attrs  []modeAttr
	infos  []modeInfo
	abil   []byte
}

// joinMode joins one player with a capturing sink and settles the
// join burst, including join attributes that can land late.
func joinMode(t *testing.T, addr, name string) modePlayer {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Time{})
	sink := newUseClient(pr, pw, true)
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, sink, "RDY"+name, 30*time.Second) {
		t.Fatalf("%s: never ready", name)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	sink.take()
	return modePlayer{name: name, pw: pw, sink: sink}
}

// parseModeAttrs parses one Update Attributes body, dropping the
// entity id both servers assign differently.
func parseModeAttrs(t *testing.T, data []byte) []modeAttr {
	t.Helper()
	r := protocol.NewPacketReader(data)
	if _, err := r.VarInt(); err != nil {
		t.Fatalf("attrs eid: %v (%s)", err, hex.EncodeToString(data))
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("attrs count: %v (%s)", err, hex.EncodeToString(data))
	}
	var out []modeAttr
	for i := int32(0); i < n; i++ {
		var a modeAttr
		if a.id, err = r.VarInt(); err != nil {
			t.Fatalf("attr id: %v (%s)", err, hex.EncodeToString(data))
		}
		if a.base, err = r.Double(); err != nil {
			t.Fatalf("attr base: %v (%s)", err, hex.EncodeToString(data))
		}
		m, err := r.VarInt()
		if err != nil {
			t.Fatalf("attr mods: %v (%s)", err, hex.EncodeToString(data))
		}
		for j := int32(0); j < m; j++ {
			var md modeMod
			if md.key, err = r.String(); err != nil {
				t.Fatalf("mod key: %v (%s)", err, hex.EncodeToString(data))
			}
			if md.amount, err = r.Double(); err != nil {
				t.Fatalf("mod amount: %v (%s)", err, hex.EncodeToString(data))
			}
			if md.op, err = r.VarInt(); err != nil {
				t.Fatalf("mod op: %v (%s)", err, hex.EncodeToString(data))
			}
			a.mods = append(a.mods, md)
		}
		out = append(out, a)
	}
	if r.Remaining() != 0 {
		t.Fatalf("attrs trailing %d bytes (%s)", r.Remaining(), hex.EncodeToString(data))
	}
	return out
}

// parseModeInfo parses one gamemode-only Player Info Update body. Any
// other mask shape fails loudly with the raw bytes, so a wrong guess
// about the update shape reads as a test failure, not a skip.
func parseModeInfo(t *testing.T, data []byte) []modeInfo {
	t.Helper()
	if len(data) == 0 || data[0] != 0x04 {
		t.Fatalf("playerinfo mask = %s, want gamemode-only 04", hex.EncodeToString(data))
	}
	r := protocol.NewPacketReader(data[1:])
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("info count: %v (%s)", err, hex.EncodeToString(data))
	}
	var out []modeInfo
	for i := int32(0); i < n; i++ {
		var e modeInfo
		if e.uuid, err = r.UUID(); err != nil {
			t.Fatalf("info uuid: %v (%s)", err, hex.EncodeToString(data))
		}
		if e.mode, err = r.VarInt(); err != nil {
			t.Fatalf("info mode: %v (%s)", err, hex.EncodeToString(data))
		}
		out = append(out, e)
	}
	if r.Remaining() != 0 {
		t.Fatalf("info trailing %d bytes (%s)", r.Remaining(), hex.EncodeToString(data))
	}
	return out
}

// chatTree renders one System Chat body as its canonical NBT tree,
// failing loudly on undecodable payloads.
func chatTree(t *testing.T, data []byte) string {
	t.Helper()
	root, err := nbt.ReadUnnamed(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("chat nbt: %v (%s)", err, hex.EncodeToString(data))
	}
	return renderNBT(root, true)
}

// foldMode sorts one capture into a modeCap. Player Info Updates with
// any other mask (latency sync and friends) belong to other parity
// areas and are left out, as are attributes for any other id: the
// /gamemode resync carries exactly the two reach entries, and mob
// tracking traffic for other entities can cross the capture window.
func foldMode(t *testing.T, pkts []usePkt) modeCap {
	t.Helper()
	var c modeCap
	for _, p := range pkts {
		switch p.id {
		case server.IDPlaySystemChatCB:
			c.chats = append(c.chats, chatTree(t, p.data))
		case server.IDPlayGameEvent:
			c.events = append(c.events, append([]byte(nil), p.data...))
		case server.IDPlayUpdateAttributes:
			for _, a := range parseModeAttrs(t, p.data) {
				if a.id == 8 || a.id == 13 {
					c.attrs = append(c.attrs, a)
				}
			}
		case server.IDPlayPlayerInfoUpdate:
			if len(p.data) > 0 && p.data[0] == 0x04 {
				c.infos = append(c.infos, parseModeInfo(t, p.data)...)
			}
		case server.IDPlayAbilitiesCB:
			if len(p.data) > 0 {
				c.abil = append(c.abil, p.data[0])
			}
		}
	}
	sort.Slice(c.attrs, func(i, j int) bool { return c.attrs[i].id < c.attrs[j].id })
	return c
}

// modeStep runs one command on one player and captures both sides'
// answers after a settle sleep. There is no chat sentinel here: the
// sentinel's own chat echo would land in the compared traffic.
func modeStep(t *testing.T, a, b modePlayer, sender modePlayer, cmd string) (modeCap, modeCap) {
	t.Helper()
	a.sink.take()
	b.sink.take()
	sendCommand(t, sender.pw, cmd)
	time.Sleep(1200 * time.Millisecond)
	return foldMode(t, a.sink.take()), foldMode(t, b.sink.take())
}

// equalAttrs reports whether two parsed attribute sets match.
func equalAttrs(a, b []modeAttr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].id != b[i].id || a[i].base != b[i].base || len(a[i].mods) != len(b[i].mods) {
			return false
		}
		for j := range a[i].mods {
			if a[i].mods[j] != b[i].mods[j] {
				return false
			}
		}
	}
	return true
}

// equalInfos reports whether two parsed playerinfo entry lists match.
func equalInfos(a, b []modeInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalFlagSets reports whether two ability-flag sequences carry the
// same set. The switch answers the same flags twice; the count is not
// pinned, so both sides are deduplicated first.
func equalFlagSets(a, b []byte) bool {
	seen := func(f []byte) []byte {
		var out []byte
		for _, v := range f {
			if !bytes.Contains(out, []byte{v}) {
				out = append(out, v)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	sa, sb := seen(a), seen(b)
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// checkModeCap compares one side's capture across servers: chats and
// events byte for byte, attributes and playerinfo parsed, abilities
// as flag sets.
func checkModeCap(t *testing.T, tag, side string, want, got modeCap) {
	t.Helper()
	if len(want.chats) != len(got.chats) {
		t.Errorf("%s %s: chats %d, want %d (%v vs %v)", tag, side, len(got.chats), len(want.chats), got.chats, want.chats)
	} else {
		for i := range want.chats {
			if want.chats[i] != got.chats[i] {
				t.Errorf("%s %s chat %d:\n got %s\nwant %s", tag, side, i, got.chats[i], want.chats[i])
			}
		}
	}
	if len(want.events) != len(got.events) {
		t.Errorf("%s %s: events %d, want %d", tag, side, len(got.events), len(want.events))
	} else {
		for i := range want.events {
			if !bytes.Equal(want.events[i], got.events[i]) {
				t.Errorf("%s %s event %d:\n got %s\nwant %s", tag, side, i,
					hex.EncodeToString(got.events[i]), hex.EncodeToString(want.events[i]))
			}
		}
	}
	if !equalAttrs(want.attrs, got.attrs) {
		t.Errorf("%s %s: attrs got %+v want %+v", tag, side, got.attrs, want.attrs)
	}
	if !equalInfos(want.infos, got.infos) {
		t.Errorf("%s %s: infos got %+v want %+v", tag, side, got.infos, want.infos)
	}
	if !equalFlagSets(want.abil, got.abil) {
		t.Errorf("%s %s: ability flags got %x want %x", tag, side, got.abil, want.abil)
	}
}

// dropTracked clears attrs on sides whose mode did not change in the
// step. Whether a bystander sees the target's reach resync depends on
// entity tracking, not on /gamemode - the differential shows vanilla
// delivering it to a bystander on some switches and not others - so
// only the changed side's attrs compare.
func dropTracked(c modeCap) modeCap {
	c.attrs = nil
	return c
}

// assertModeSilent fails when either side's capture holds any
// gamemode traffic at all.
func assertModeSilent(t *testing.T, tag string, a, b modeCap) {
	t.Helper()
	for _, s := range []struct {
		name string
		c    modeCap
	}{{"A", a}, {"B", b}} {
		if len(s.c.chats)+len(s.c.events)+len(s.c.attrs)+len(s.c.infos)+len(s.c.abil) != 0 {
			t.Errorf("%s %s: want silence, got %+v", tag, s.name, s.c)
		}
	}
}

func TestGamemodeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ModeA", "ModeB"),
	}, nil)
	defer tg.cleanup()

	vA := joinMode(t, tg.vanilla, "ModeA")
	vB := joinMode(t, tg.vanilla, "ModeB")
	mA := joinMode(t, tg.mcgo, "ModeA")
	mB := joinMode(t, tg.mcgo, "ModeB")

	// Normalize: vanilla joins in survival, mc-go in creative. The
	// normalization itself is unsettled traffic, so a silent repeat
	// proves the known state instead.
	for _, p := range []modePlayer{vA, vB} {
		p.sink.take()
		sendCommand(t, p.pw, "gamemode creative")
	}
	time.Sleep(1200 * time.Millisecond)
	vA.sink.take()
	vB.sink.take()
	for _, p := range []modePlayer{mA, mB} {
		p.sink.take()
		sendCommand(t, p.pw, "gamemode creative")
	}
	time.Sleep(1200 * time.Millisecond)
	mA.sink.take()
	mB.sink.take()

	// Both creative now: a repeat must be silent on both servers.
	wA, wB := modeStep(t, vA, vB, vA, "gamemode creative")
	gA, gB := modeStep(t, mA, mB, mA, "gamemode creative")
	assertModeSilent(t, "repeat-creative", wA, wB)
	assertModeSilent(t, "repeat-creative", gA, gB)

	// Self switch to survival and back, with the error surface
	// between the two directions.
	wA, wB = modeStep(t, vA, vB, vA, "gamemode survival")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode survival")
	checkModeCap(t, "self-survival", "A", wA, gA)
	wB, gB = dropTracked(wB), dropTracked(gB)
	checkModeCap(t, "self-survival", "B", wB, gB)

	wA, wB = modeStep(t, vA, vB, vA, "gamemode survival")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode survival")
	assertModeSilent(t, "repeat-survival", wA, wB)
	assertModeSilent(t, "repeat-survival", gA, gB)

	wA, wB = modeStep(t, vA, vB, vA, "gamemode creative")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode creative")
	checkModeCap(t, "self-creative", "A", wA, gA)
	wB, gB = dropTracked(wB), dropTracked(gB)
	checkModeCap(t, "self-creative", "B", wB, gB)

	for _, cmd := range []string{"gamemode bogus", "gamemode", "gamemode Survival"} {
		wA, wB = modeStep(t, vA, vB, vA, cmd)
		gA, gB = modeStep(t, mA, mB, mA, cmd)
		checkModeCap(t, cmd, "A", wA, gA)
		assertModeSilent(t, cmd, modeCap{}, wB)
		assertModeSilent(t, cmd, modeCap{}, gB)
	}

	// Remote target: the sender names the target, the target hears
	// the change and the admin broadcast, and the tab-list update
	// goes to everyone but the changed player.
	wA, wB = modeStep(t, vA, vB, vA, "gamemode survival ModeB")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode survival ModeB")
	wA, gA = dropTracked(wA), dropTracked(gA)
	checkModeCap(t, "other-survival", "A", wA, gA)
	checkModeCap(t, "other-survival", "B", wB, gB)

	wA, wB = modeStep(t, vA, vB, vA, "gamemode creative Nobody")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode creative Nobody")
	checkModeCap(t, "unknown-target", "A", wA, gA)
	assertModeSilent(t, "unknown-target", modeCap{}, wB)
	assertModeSilent(t, "unknown-target", modeCap{}, gB)

	wA, wB = modeStep(t, vA, vB, vA, "gamemode survival ModeB extra")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode survival ModeB extra")
	checkModeCap(t, "trailing-arg", "A", wA, gA)
	assertModeSilent(t, "trailing-arg", modeCap{}, wB)
	assertModeSilent(t, "trailing-arg", modeCap{}, gB)

	// Restore through the same remote path, then cover the two modes
	// whose switches resend no attributes at all.
	wA, wB = modeStep(t, vA, vB, vA, "gamemode creative ModeB")
	gA, gB = modeStep(t, mA, mB, mA, "gamemode creative ModeB")
	wA, gA = dropTracked(wA), dropTracked(gA)
	checkModeCap(t, "other-creative", "A", wA, gA)
	checkModeCap(t, "other-creative", "B", wB, gB)

	wA, wB = modeStep(t, vA, vB, vB, "gamemode adventure")
	gA, gB = modeStep(t, mA, mB, mB, "gamemode adventure")
	wA, gA = dropTracked(wA), dropTracked(gA)
	checkModeCap(t, "self-adventure", "A", wA, gA)
	checkModeCap(t, "self-adventure", "B", wB, gB)

	wA, wB = modeStep(t, vA, vB, vB, "gamemode spectator")
	gA, gB = modeStep(t, mA, mB, mB, "gamemode spectator")
	wA, gA = dropTracked(wA), dropTracked(gA)
	checkModeCap(t, "adventure-spectator", "A", wA, gA)
	checkModeCap(t, "adventure-spectator", "B", wB, gB)
}
