package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestParseBlockName(t *testing.T) {
	cases := map[string]world.Block{
		"stone":                 world.Stone,
		"minecraft:stone":       world.Stone,
		"dirt":                  world.Dirt,
		"grass":                 world.Grass,
		"grass_block":           world.Grass,
		"minecraft:grass_block": world.Grass,
		"air":                   world.Air,
		"STONE":                 world.Stone,
	}
	for tok, want := range cases {
		if got, ok := parseBlockName(tok); !ok || got != want {
			t.Fatalf("parse %q = %v,%v want %v", tok, got, ok, want)
		}
	}
	if _, ok := parseBlockName("diamond"); ok {
		t.Fatal("unknown block accepted")
	}
}

func TestParseCoord(t *testing.T) {
	if v, _ := parseCoord("12", 8); v != 12 {
		t.Fatalf("abs = %d", v)
	}
	if v, _ := parseCoord("~", 8); v != 8 {
		t.Fatalf("tilde = %d", v)
	}
	if v, _ := parseCoord("~2", 8); v != 10 {
		t.Fatalf("rel = %d", v)
	}
	if v, _ := parseCoord("~-3", 8); v != 5 {
		t.Fatalf("rel neg = %d", v)
	}
	if _, err := parseCoord("abc", 8); err == nil {
		t.Fatal("bad coord accepted")
	}
	if _, err := parseCoord("~abc", 8); err == nil {
		t.Fatal("bad rel accepted")
	}
}

// expectPlayerSpawnAnnouncement consumes the Spawn Entity packet that
// announces a just-joined player to an existing session, asserting the
// announced entity is that player.
func expectPlayerSpawnAnnouncement(t *testing.T, pr *protocol.Reader, name string) {
	t.Helper()
	want := offlineUUID(name)
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("spawn announcement for %s: %v", name, err)
		}
		if p.ID != IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		if _, err := r.VarInt(); err != nil {
			continue
		}
		uuid, err := r.UUID()
		if err != nil {
			continue
		}
		if uuid != want {
			continue
		}
		if ty, err := r.VarInt(); err != nil || ty != EntityTypePlayer {
			t.Fatalf("spawn type = %d, err %v", ty, err)
		}
		return
	}
	t.Fatalf("spawn announcement for %s never arrived", name)
}

// drainToQuiet reads until the stream is silent for 250ms. Only safe
// when keep-alive is at its default (>=10s) cadence.
func drainToQuiet(t *testing.T, pr *protocol.Reader) {
	t.Helper()
	for {
		pr.SetDeadline(time.Now().Add(250 * time.Millisecond))
		if _, err := pr.ReadPacket(); err != nil {
			break
		}
	}
	pr.SetDeadline(time.Time{})
}

// finishJoin takes a freshly joined scripted session the rest of the way
// to where a real client is a moment after joining: it acknowledges
// chunk batches at the highest rate the protocol allows, runs the
// streaming tick, and drains what that wrote.
//
// Tests that act on blocks need this. The spawn window streams in two
// halves - the opening batch of nine columns the real server sends
// before any acknowledgement, then the rest once the client answers (see
// pumpChunks) - and the block, light, dig and sign fan-outs only reach
// columns the session has taken on.
func finishJoin(t *testing.T, s *Server, pr *protocol.Reader, pw *protocol.Writer) {
	t.Helper()
	var b protocol.Buffer
	b.Float(maxChunkBatch)
	if err := pw.WritePacket(IDPlayChunkBatchRecvSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	// The session reads the acknowledgement on its own goroutine; give
	// it a moment before the tick that acts on it. A whole default
	// window fits the session's outbound queue, so the tick can run
	// here rather than on a goroutine the reads would have to chase.
	time.Sleep(20 * time.Millisecond)
	s.chunkTickStep()
	drainToQuiet(t, pr)
}

// drainJoinBurst consumes the post-Join-Game burst through the
// joiner's own tab-list row (the second Player Info Update). Vanilla
// and mc-go both put the owner row before world state and chunks, so
// this marker is stable while the rest of the burst evolves.
func drainJoinBurst(t *testing.T, pr *protocol.Reader) {
	t.Helper()
	for i := 0; i < 128; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("join burst %d: %v", i, err)
		}
		if p.ID != IDPlayPlayerInfoUpdate {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		if _, err := r.Byte(); err != nil { // actions
			continue
		}
		if n, err := r.VarInt(); err == nil && n > 0 {
			return
		}
	}
	t.Fatal("join burst: self player-info row never arrived")
}

// waitPlayerInfo reads until a tab-list row mentioning name.
func waitPlayerInfo(t *testing.T, pr *protocol.Reader, name string) {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("wait info %q: %v", name, err)
		}
		if p.ID == IDPlayPlayerInfoUpdate && strings.Contains(string(p.Data), name) {
			return
		}
	}
	t.Fatalf("player-info row for %q never arrived", name)
}

// waitSystemChat reads until a system-chat message containing substr
// and returns its text.
func waitSystemChat(t *testing.T, pr *protocol.Reader, substr string) string {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("wait chat %q: %v", substr, err)
		}
		if p.ID != IDPlaySystemChatCB {
			continue
		}
		text := chatText(t, p.Data)
		if strings.Contains(text, substr) {
			return text
		}
	}
	t.Fatalf("system chat %q never arrived", substr)
	return ""
}

// waitEntitySpawn reads until a Spawn Entity packet for a player with
// the given name cannot be matched by name (spawns carry UUIDs), so it
// waits for the first spawn of any player entity after the caller has
// drained other traffic.
func waitEntitySpawn(t *testing.T, pr *protocol.Reader) {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("wait spawn: %v", err)
		}
		if p.ID == IDPlaySpawnEntity {
			return
		}
	}
	t.Fatal("entity spawn never arrived")
}

func TestSetblockUpdatesWorldAndBroadcasts(t *testing.T) {
	s := New("unused")
	ch, unsub := s.Bus().Subscribe(event.TopicBlockChange, 8)
	defer unsub()
	c, pr, pw := joinPlay(t, s, "Builder")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sendChat(t, pw, IDPlayChatCommandSB, "setblock 3 70 3 stone")
	// Sender gets a Block Update fan-out then the confirmation chat.
	upd, err := pr.ReadPacket()
	if err != nil || upd.ID != IDPlayBlockUpdateCB {
		t.Fatalf("block update = %#x, err %v", upd.ID, err)
	}
	ur := protocol.NewPacketReader(upd.Data)
	ux, uy, uz, err := ur.Position()
	if err != nil || ux != 3 || uy != 70 || uz != 3 {
		t.Fatalf("update pos = %d,%d,%d err %v", ux, uy, uz, err)
	}
	if st, err := ur.VarInt(); err != nil || st != StateStone {
		t.Fatalf("update state = %d, err %v (want stone %d)", st, err, StateStone)
	}
	// Success is the real server's component, measured off it live: the
	// setblock key with the three coordinates as plain ints.
	root := readSystemChatNBT(t, pr)
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("confirm root = %T, want compound", root)
	}
	if key, _ := m["translate"].(string); key != "commands.setblock.success" {
		t.Fatalf("confirm key = %q, want commands.setblock.success", key)
	}
	l, ok := m["with"].(nbt.List)
	if !ok || len(l.Items) != 3 {
		t.Fatalf("confirm with = %v, want three ints", m["with"])
	}
	for i, want := range []int32{3, 70, 3} {
		if got, ok := l.Items[i].(int32); !ok || got != want {
			t.Fatalf("confirm with[%d] = %v, want %d", i, l.Items[i], want)
		}
	}
	if got := s.World().GetBlock(world.Pos{X: 3, Y: 70, Z: 3}); got != world.Stone {
		t.Fatalf("world = %d, want stone", got)
	}
	select {
	case ev := <-ch:
		bc, ok := ev.Data.(BlockChange)
		if !ok || bc.X != 3 || bc.Y != 70 || bc.Z != 3 || bc.Block != world.Stone || bc.Player != "Builder" {
			t.Fatalf("event = %+v", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no BlockChange event")
	}
}

func TestSetblockRelativeAndOtherSessionGetsUpdate(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Watcher")
	defer ca.Close()
	drainJoinBurst(t, pra)
	cb, prb, pwb := joinPlay(t, s, "Builder")
	defer cb.Close()
	drainJoinBurst(t, prb)
	// Builder's burst carries Watcher's tab-list row next.
	waitPlayerInfo(t, prb, "Watcher")
	drainToQuiet(t, prb)
	// Watcher sees Builder's join: chat, tab-list row, spawn.
	waitSystemChat(t, pra, "Builder")
	waitPlayerInfo(t, pra, "Builder")
	expectPlayerSpawnAnnouncement(t, pra, "Builder")

	// Builder at spawn (0.5,-60,0.5) - the flat world's spawn, where the
	// real server puts it; ~ ~1 ~ targets (0,-59,0).
	sendChat(t, pwb, IDPlayChatCommandSB, "setblock ~ ~1 ~ dirt")
	// Builder gets its own update + confirm.
	for i := 0; i < 2; i++ {
		if _, err := prb.ReadPacket(); err != nil {
			t.Fatalf("builder update %d: %v", i, err)
		}
	}
	// Watcher holds chunk (0,0) and must get the Block Update.
	upd, err := waitPacket(t, pra, IDPlayBlockUpdateCB)
	if err != nil {
		t.Fatalf("watcher update: %v", err)
	}
	ur := protocol.NewPacketReader(upd.Data)
	if ux, uy, uz, err := ur.Position(); err != nil || ux != 0 || uy != -59 || uz != 0 {
		t.Fatalf("watcher pos = %d,%d,%d err %v", ux, uy, uz, err)
	}
	if got := s.World().GetBlock(world.Pos{X: 0, Y: -59, Z: 0}); got != world.Dirt {
		t.Fatalf("world (0,-59,0) = %d, want dirt", got)
	}
}

func TestSetblockRejectsBadInput(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Builder")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sendChat(t, pw, IDPlayChatCommandSB, "setblock 1 2 3 diamond")
	if got := readSystemChat(t, pr); !strings.Contains(got, "Unknown block") {
		t.Fatalf("bad block reply = %q", got)
	}
	sendChat(t, pw, IDPlayChatCommandSB, "setblock 0 9999 0 stone")
	if got := readSystemChat(t, pr); !strings.Contains(got, "out of range") {
		t.Fatalf("bad y reply = %q", got)
	}
	sendChat(t, pw, IDPlayChatCommandSB, "setblock 1 65")
	if got := readSystemChat(t, pr); !strings.Contains(got, "Usage") {
		t.Fatalf("bad arity reply = %q", got)
	}
}

func TestJoinLeaveBroadcast(t *testing.T) {
	s := New("unused")
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)

	cb, prb, _ := joinPlay(t, s, "Bob")
	drainJoinBurst(t, prb)
	// Bob's burst carries rows for players already online: Alice.
	waitPlayerInfo(t, prb, "Alice")
	drainToQuiet(t, prb)
	// Alice must see Bob's join: chat, then tab-list row.
	if text := waitSystemChat(t, pra, "Bob"); !strings.Contains(text, "joined") {
		t.Fatalf("join text = %q", text)
	}
	ip, err := waitPacket(t, pra, IDPlayPlayerInfoUpdate)
	if err != nil || !strings.Contains(string(ip.Data), "Bob") {
		t.Fatalf("join info = %#x, err %v", ip.ID, err)
	}
	// Bob must NOT see his own join (vanilla notifies existing players).
	_ = cb.SetDeadline(time.Now().Add(150 * time.Millisecond))
	if q, err := prb.ReadPacket(); err == nil && q.ID == IDPlaySystemChatCB {
		if text := chatText(t, q.Data); strings.Contains(text, "joined") {
			t.Fatalf("joiner saw own join: %q", text)
		}
	}
	_ = cb.SetDeadline(time.Time{})

	// Bob leaves: Alice must see the leave message.
	_ = cb.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = ca.SetDeadline(time.Now().Add(500 * time.Millisecond))
		q, err := pra.ReadPacket()
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("no leave message: %v", err)
			}
			continue
		}
		if q.ID != IDPlaySystemChatCB {
			continue
		}
		text := chatText(t, q.Data)
		if strings.Contains(text, "Bob") && strings.Contains(text, "left") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leave text never arrived, last = %q", text)
		}
	}
}

func TestOnlineCountExcludesStatusProbes(t *testing.T) {
	s := New("unused")
	if got := s.OnlineCount(); got != 0 {
		t.Fatalf("online = %d, want 0", got)
	}
	// Status probe must not change the count.
	cs, prs, pws := pipePair(t, s)
	if err := sendHandshake(pws, ProtocolVersion, "localhost", 25565, NextStatus); err != nil {
		t.Fatal(err)
	}
	if err := pws.WritePacket(IDStatusRequest, nil); err != nil {
		t.Fatal(err)
	}
	resp, err := prs.ReadPacket()
	if err != nil || resp.ID != IDStatusResponse {
		t.Fatalf("status = %#x, err %v", resp.ID, err)
	}
	var v struct {
		Players struct {
			Online int `json:"online"`
		} `json:"players"`
	}
	js, _ := protocol.NewPacketReader(resp.Data).String()
	_ = json.Unmarshal([]byte(js), &v)
	if v.Players.Online != 0 {
		t.Fatalf("status online = %d, want 0", v.Players.Online)
	}
	if got := s.OnlineCount(); got != 0 {
		t.Fatalf("online during status = %d, want 0", got)
	}
	_ = cs.Close()
	time.Sleep(100 * time.Millisecond)
	if got := s.OnlineCount(); got != 0 {
		t.Fatalf("online after status = %d, want 0", got)
	}

	// One play session -> online 1.
	c, pr, _ := joinPlay(t, s, "Solo")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	if got := s.OnlineCount(); got != 1 {
		t.Fatalf("online = %d, want 1", got)
	}
}

func TestBuiltinListAndHelp(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Scout")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sendChat(t, pw, IDPlayChatCommandSB, "list")
	root := readSystemChatNBT(t, pr)
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("list root = %T, want compound", root)
	}
	if m["translate"] != "commands.list.players" {
		t.Fatalf("list translate = %v", m["translate"])
	}
	got := fmt.Sprintf("%v", m)
	if !strings.Contains(got, "Scout") {
		t.Fatalf("list = %q", got)
	}
	sendChat(t, pw, IDPlayChatCommandSB, "help")
	// Vanilla /help emits one bare-string System Chat per root usage.
	if got := readSystemChat(t, pr); got != helpUsageLines[0] {
		t.Fatalf("help = %q", got)
	}
	drainToQuiet(t, pr)
	sendChat(t, pw, IDPlayChatCommandSB, "pos")
	if got := readSystemChat(t, pr); !strings.Contains(got, "0.50") {
		t.Fatalf("pos = %q", got)
	}
}
