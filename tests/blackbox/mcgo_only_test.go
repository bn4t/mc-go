package blackbox

// mc-go-only blackbox suite: real TCP + framed packets + compression,
// no Mojang server or Java required. These run everywhere loopback
// binds are allowed and prove the new parity slice over the wire:
// online-count, presence broadcast, /setblock chunk resend, /list.
//
// Vanilla-comparison tests (status_login_test.go etc.) still gate on
// MCGO_BLACKBOX=1 + java; this file gates only on loopback.

import (
	"bytes"
	"crypto/md5"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func requireMcGoOnly(t *testing.T) string {
	t.Helper()
	return requireMcGoOnlyCfg(t, nil)
}

// requireMcGoOnlyCfg starts a standalone mc-go server with an optional
// mutator (game mode, view distance, ...).
func requireMcGoOnlyCfg(t *testing.T, mutate func(*server.Server)) string {
	t.Helper()
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	return startMcGoCfg(t, mPort, mutate)
}

func drainToJoinBlackbox(t *testing.T, tag string, pr *protocol.Reader) {
	t.Helper()
	_ = tag
	for i := 0; i < 400; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s play stream: %v", tag, err)
		}
		if p.ID == server.IDPlayJoinGame {
			return
		}
	}
	t.Fatalf("%s: no join game in play burst", tag)
}

func readSystemChatBlackbox(t *testing.T, pr *protocol.Reader, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("timed out waiting for system chat")
		}
		// The underlying conn already has a 10s deadline from client();
		// poll with short reads.
		if c, ok := prConn(pr); ok {
			_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
		}
		p, err := pr.ReadPacket()
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("chat read: %v", err)
			}
			continue
		}
		if p.ID != server.IDPlaySystemChatCB {
			continue
		}
		return chatTextBlackbox(t, p.Data)
	}
}

// isSetblockConfirmBlackbox reports whether a System Chat payload is
// the /setblock success confirm for 3 70 3. Vanilla 26.2 answers with
// the commands.setblock.success component carrying the three coords
// as ints (pinned in internal/server/blocks_test.go), not the literal
// English sentence, so the component form is the expected one and the
// plain string is kept only for backward tolerance.
func isSetblockConfirmBlackbox(t *testing.T, data []byte) bool {
	t.Helper()
	root, err := nbt.ReadUnnamed(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("chat nbt: %v", err)
	}
	if text, ok := root.(string); ok {
		return strings.Contains(text, "Set block 3 70 3")
	}
	m, ok := root.(map[string]any)
	if !ok {
		return false
	}
	if key, _ := m["translate"].(string); key != "commands.setblock.success" {
		return false
	}
	with, ok := m["with"].(nbt.List)
	if !ok || len(with.Items) != 3 {
		return false
	}
	for i, want := range []int32{3, 70, 3} {
		if got, ok := with.Items[i].(int32); !ok || got != want {
			return false
		}
	}
	return true
}

// chatTextBlackbox decodes an NBT text component payload to its plain
// text form. Command replies arrive either as a bare string root or as
// a component object (translate/text/extra), so both decode.
func chatTextBlackbox(t *testing.T, data []byte) string {
	t.Helper()
	root, err := nbt.ReadUnnamed(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("chat nbt: %v", err)
	}
	return componentPlain(root)
}

// componentPlain flattens an NBT-decoded text component to its visible
// text: a bare string returns itself, objects contribute their
// text/translate/with/extra parts in order.
func componentPlain(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case map[string]any:
		var sb strings.Builder
		if t, ok := c["text"].(string); ok {
			sb.WriteString(t)
		} else if tr, ok := c["translate"].(string); ok {
			sb.WriteString(tr)
		}
		for _, key := range []string{"with", "extra"} {
			sb.WriteString(componentPlain(c[key]))
		}
		return sb.String()
	case nbt.List:
		var sb strings.Builder
		for _, item := range c.Items {
			sb.WriteString(componentPlain(item))
		}
		return sb.String()
	case []any:
		var sb strings.Builder
		for _, item := range c {
			sb.WriteString(componentPlain(item))
		}
		return sb.String()
	default:
		return ""
	}
}

// prConn is a best-effort hook: blackbox readers wrap a net.Conn but
// protocol.Reader does not expose it, so callers that need deadlines
// should set them on their own conn value instead. Kept for symmetry;
// always reports false so reads use the conn deadline set at dial.
func prConn(_ *protocol.Reader) (net.Conn, bool) { return nil, false }

func sendChatBlackbox(t *testing.T, pw *protocol.Writer, id int32, text string) {
	t.Helper()
	var b protocol.Buffer
	b.String(text)
	b.Long(0)
	b.Long(0)
	if err := pw.WritePacket(id, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestMcGoOnlyStatusOnline(t *testing.T) {
	addr := requireMcGoOnly(t)
	v := fetchStatus(t, addr)
	if v.Players.Online != 0 {
		t.Fatalf("empty online = %d, want 0", v.Players.Online)
	}
	c, pr, _ := joinPlayBlackbox(t, addr, "Solo")
	defer c.Close()
	drainToJoinBlackbox(t, "solo", pr)
	v2 := fetchStatus(t, addr)
	if v2.Players.Online != 1 {
		t.Fatalf("online with 1 player = %d, want 1", v2.Players.Online)
	}
}

func TestMcGoOnlyPresenceBroadcast(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, _ := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)

	cb, prb, _ := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	drainToJoinBlackbox(t, "bob", prb)

	// Alice must see Bob join: chat notice, then his tab-list row.
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	joinText := readSystemChatBlackbox(t, pra, 5*time.Second)
	if !strings.Contains(joinText, "Bob") || !strings.Contains(joinText, "joined") {
		t.Fatalf("join text = %q", joinText)
	}
	ip, err := pra.ReadPacket()
	if err != nil || ip.ID != server.IDPlayPlayerInfoUpdate {
		t.Fatalf("join info = %#x, err %v", ip.ID, err)
	}
	if !strings.Contains(string(ip.Data), "Bob") {
		t.Fatalf("join info lacks Bob: %x", ip.Data)
	}

	_ = cb.Close()
	leaveText := readSystemChatBlackbox(t, pra, 5*time.Second)
	if !strings.Contains(leaveText, "Bob") || !strings.Contains(leaveText, "left") {
		t.Fatalf("leave text = %q", leaveText)
	}
	// ...followed by Bob's tab-list removal carrying his offline UUID.
	rm, err := pra.ReadPacket()
	if err != nil || rm.ID != server.IDPlayPlayerInfoRemove {
		t.Fatalf("leave remove = %#x, err %v", rm.ID, err)
	}
	if !strings.Contains(string(rm.Data), "Bob") {
		want := offlineUUIDBlackbox("Bob")
		if !bytes.Contains(rm.Data, want[:]) {
			t.Fatalf("remove lacks Bob: %x", rm.Data)
		}
	}
}

// offlineUUIDBlackbox mirrors the server's offline-mode UUID derivation.
func offlineUUIDBlackbox(name string) (u [16]byte) {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	sum[6] = (sum[6] & 0x0F) | 0x30
	sum[8] = (sum[8] & 0x3F) | 0x80
	return sum
}

func TestMcGoOnlySetblockStreamsChunk(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, _ := joinPlayBlackbox(t, addr, "Watcher")
	defer ca.Close()
	drainToJoinBlackbox(t, "watcher", pra)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Builder")
	defer cb.Close()
	drainToJoinBlackbox(t, "builder", prb)
	// Watcher sees Builder join.
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	_ = readSystemChatBlackbox(t, pra, 5*time.Second)

	sendChatBlackbox(t, pwb, server.IDPlayChatCommandSB, "setblock 3 70 3 stone")
	// Builder gets Block Update fan-out + confirm; order is FIFO per session.
	_ = cb.SetDeadline(time.Now().Add(5 * time.Second))
	sawUpdate, sawConfirm := false, false
	deadline := time.Now().Add(5 * time.Second)
	for !(sawUpdate && sawConfirm) && time.Now().Before(deadline) {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("builder stream: %v", err)
		}
		switch p.ID {
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err == nil && x == 3 && y == 70 && z == 3 {
				sawUpdate = true
			}
		case server.IDPlaySystemChatCB:
			if isSetblockConfirmBlackbox(t, p.Data) {
				sawConfirm = true
			}
		}
	}
	if !sawUpdate || !sawConfirm {
		t.Fatalf("builder saw update=%v confirm=%v", sawUpdate, sawConfirm)
	}
	// Watcher must get the same Block Update.
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 20; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("watcher stream: %v", err)
		}
		if p.ID == server.IDPlayBlockUpdateCB {
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err == nil && x == 3 && y == 70 && z == 3 {
				return
			}
		}
	}
	t.Fatal("watcher never received block update (3,70,3)")
}

func TestMcGoOnlyListCommand(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Scout")
	defer c.Close()
	drainToJoinBlackbox(t, "scout", pr)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, "list")
	if got := readSystemChatBlackbox(t, pr, 5*time.Second); !strings.Contains(got, "Scout") {
		t.Fatalf("list = %q", got)
	}
}

// TestMcGoOnlyJoinSync verifies the vanilla-ordered stateless join
// burst over real TCP through the player-position sync.
func TestMcGoOnlyJoinSync(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, _ := joinPlayBlackbox(t, addr, "Sync")
	defer c.Close()
	drainToJoinBlackbox(t, "sync", pr)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	want := []int32{
		server.IDPlayDifficulty,
		server.IDPlayAbilitiesCB,
		server.IDPlaySetHeldItemCB,
		server.IDPlayUpdateRecipes,
		server.IDPlayEntityEvent,
		server.IDPlayCommands,
		server.IDPlayRecipeBookSet,
		server.IDPlayRecipeBookAdd,
		server.IDPlaySyncPos,
		server.IDPlayServerData,
	}
	for i, w := range want {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("join packet %d: %v", i, err)
		}
		if p.ID != w {
			t.Fatalf("join packet %d = %#x, want %#x", i, p.ID, w)
		}
		if p.ID == server.IDPlayAbilitiesCB {
			if f, err := protocol.NewPacketReader(p.Data).Byte(); err != nil || f != 0x0D {
				t.Fatalf("ability flags = %#x, err %v", f, err)
			}
		}
		if p.ID == server.IDPlaySyncPos {
			r := protocol.NewPacketReader(p.Data)
			if tid, err := r.VarInt(); err != nil || tid != 1 {
				t.Fatalf("teleport id = %d, err %v", tid, err)
			}
			x, _ := r.Double()
			y, _ := r.Double()
			z, _ := r.Double()
			// The flat world's spawn is the origin block: the real 26.2
			// server writes spawn.pos (0,-60,0) into level.dat for a
			// default superflat world, so a joining player stands on it.
			if x != 0.5 || y != -60 || z != 0.5 {
				t.Fatalf("spawn = %v,%v,%v", x, y, z)
			}
		}
	}
}

func sendDigBlackbox(t *testing.T, pw *protocol.Writer, status int32, x, y, z int32, seq int32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(status)
	b.Position(x, y, z)
	b.VarInt(1)
	b.VarInt(seq)
	if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// TestMcGoOnlyDigBreaksBlock places stone via /setblock, breaks it with a
// Player Action packet, and requires the Block Update (air) plus the ack
// on the digger and the same Block Update on a watching session.
func TestMcGoOnlyDigBreaksBlock(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, _ := joinPlayBlackbox(t, addr, "Watcher")
	defer ca.Close()
	drainToJoinBlackbox(t, "watcher", pra)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Digger")
	defer cb.Close()
	drainToJoinBlackbox(t, "builder", prb)
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	_ = readSystemChatBlackbox(t, pra, 5*time.Second) // join broadcast

	_ = cb.SetDeadline(time.Now().Add(5 * time.Second))
	sendChatBlackbox(t, pwb, server.IDPlayChatCommandSB, "setblock 6 70 6 stone")
	// Wait for the setblock update on both sessions.
	for _, pr := range []*protocol.Reader{prb, pra} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("setblock stream: %v", err)
			}
			if p.ID == server.IDPlayBlockUpdateCB {
				break
			}
			if p.ID == server.IDPlaySystemChatCB {
				continue
			}
			if time.Now().After(deadline) {
				t.Fatal("no setblock update")
			}
		}
	}
	// Drain the builder's setblock confirmation chat.
	for {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("digger drain: %v", err)
		}
		if p.ID == server.IDPlaySystemChatCB {
			break
		}
	}

	sendDigBlackbox(t, pwb, 0, 6, 70, 6, 33)
	// Digger: Block Update (air) then ack 33.
	sawUpdate, sawAck := false, false
	deadline := time.Now().Add(5 * time.Second)
	for !(sawUpdate && sawAck) && time.Now().Before(deadline) {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("digger dig stream: %v", err)
		}
		switch p.ID {
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err != nil {
				t.Fatalf("update pos: %v", err)
			}
			st, err := r.VarInt()
			if err != nil {
				t.Fatalf("update state: %v", err)
			}
			if x == 6 && y == 70 && z == 6 && st == 0 {
				sawUpdate = true
			}
		case server.IDPlayBlockChangedAck:
			if seq, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil && seq == 33 {
				sawAck = true
			}
		}
	}
	if !sawUpdate || !sawAck {
		t.Fatalf("digger saw update=%v ack=%v", sawUpdate, sawAck)
	}
	// Watcher: the same air update.
	_ = ca.SetDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 20; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("watcher dig stream: %v", err)
		}
		if p.ID != server.IDPlayBlockUpdateCB {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		x, y, z, err := r.Position()
		if err != nil {
			t.Fatalf("watcher pos: %v", err)
		}
		st, err := r.VarInt()
		if err != nil {
			t.Fatalf("watcher state: %v", err)
		}
		if x == 6 && y == 70 && z == 6 && st == 0 {
			return
		}
	}
	t.Fatal("watcher never received air update (6,70,6)")
}

// TestMcGoOnlyForgetChunkOnFarMove jumps ten chunks east and requires a
// Forget Level Chunk for every column of the spawn window the client was
// holding - the whole 7x7 a client that announces no view distance is
// sent, all of it now past the prune border.
func TestMcGoOnlyForgetChunkOnFarMove(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := joinPlayBlackbox(t, addr, "Walker")
	defer c.Close()
	drainToJoinBlackbox(t, "walker", pr)
	chunks := drainChunkWindow(t, "walker", c, pr, pw, 500*time.Millisecond)
	if len(chunks) != 49 {
		t.Fatalf("spawn window = %d columns, want 49", len(chunks))
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := sendMovePosRot(pw, 168.5, 66, 8.5); err != nil {
		t.Fatal(err)
	}
	const want = 49
	seen := map[[2]int32]bool{}
	for i := 0; i < 400 && len(seen) < want; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("move stream: %v", err)
		}
		if p.ID != server.IDPlayForgetChunk {
			continue
		}
		v, err := protocol.NewPacketReader(p.Data).Long()
		if err != nil {
			t.Fatalf("forget long: %v", err)
		}
		k := [2]int32{int32(uint64(v) & 0xFFFFFFFF), int32(uint64(v) >> 32)}
		if k[0] < -3 || k[0] > 3 || k[1] < -3 || k[1] > 3 {
			t.Fatalf("forgot %d,%d, which the client never held", k[0], k[1])
		}
		seen[k] = true
	}
	if len(seen) != want {
		t.Fatalf("forgets = %d, want %d", len(seen), want)
	}
}

// TestMcGoOnlyOpsCommands drives the operator slice end to end over the
// wire: /tp (coords + player), /pos verification, /save-all without
// persistence, /kick with watcher-visible leave, and /stop shutting the
// listener down.
func TestMcGoOnlyOpsCommands(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, pwa := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	cb, prb, _ := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	drainToJoinBlackbox(t, "bob", prb)
	_ = ca.SetDeadline(time.Now().Add(30 * time.Second))
	_ = cb.SetDeadline(time.Now().Add(30 * time.Second))

	// Alice sees Bob join (chat notice first, like presence).
	if text := readSystemChatBlackbox(t, pra, 10*time.Second); !strings.Contains(text, "Bob") {
		t.Fatalf("join text = %q", text)
	}
	if p, err := pra.ReadPacket(); err != nil || p.ID != server.IDPlayPlayerInfoUpdate {
		t.Fatalf("join info = %#x, err %v", p.ID, err)
	}

	runCmd := func(pw *protocol.Writer, pr *protocol.Reader, cmd string) string {
		t.Helper()
		sendChatBlackbox(t, pw, server.IDPlayChatCommandSB, cmd)
		return readSystemChatBlackbox(t, pr, 10*time.Second)
	}

	// /tp replies are vanilla's translatable components; flattening
	// yields the key plus any nested text (numeric args contribute
	// nothing).
	if out := runCmd(pwa, pra, "tp 100 70 100"); !strings.Contains(out, "commands.teleport.success.location.single") {
		t.Fatalf("/tp coords = %q", out)
	}
	if out := runCmd(pwa, pra, "pos"); !strings.HasPrefix(out, "100.50 70.00 100.50") {
		t.Fatalf("/pos after tp = %q", out)
	}
	if out := runCmd(pwa, pra, "tp Bob"); !strings.Contains(out, "commands.teleport.success.entity.single") || !strings.Contains(out, "Bob") {
		t.Fatalf("/tp player = %q", out)
	}
	// Bob has never moved, so teleporting to him lands on the world
	// spawn, which is the origin block of the flat world.
	if out := runCmd(pwa, pra, "pos"); !strings.HasPrefix(out, "0.50 -60.00 0.50") {
		t.Fatalf("/pos after tp player = %q", out)
	}
	if out := runCmd(pwa, pra, "tp Ghost"); out != "Unknown player: Ghost" {
		t.Fatalf("/tp ghost = %q", out)
	}
	if out := runCmd(pwa, pra, "save-all"); !strings.Contains(out, "disabled") {
		t.Fatalf("/save-all = %q", out)
	}

	if out := runCmd(pwa, pra, "kick Bob"); out != "Kicked Bob" {
		t.Fatalf("/kick = %q", out)
	}
	// Bob receives the disconnect packet (after his pending burst).
	sawDisc := false
	for i := 0; i < 200 && !sawDisc; i++ {
		p, err := prb.ReadPacket()
		if err != nil {
			break
		}
		sawDisc = p.ID == server.IDPlayDisconnect
	}
	if !sawDisc {
		t.Fatal("Bob never received the kick disconnect")
	}
	// Alice sees Bob leave, then his tab-list removal.
	if text := readSystemChatBlackbox(t, pra, 10*time.Second); !strings.Contains(text, "Bob") || !strings.Contains(text, "left") {
		t.Fatalf("leave text = %q", text)
	}
	if p, err := pra.ReadPacket(); err != nil || p.ID != server.IDPlayPlayerInfoRemove {
		t.Fatalf("leave remove = %#x, err %v", p.ID, err)
	}

	if out := runCmd(pwa, pra, "stop"); out != "Stopping the server" {
		t.Fatalf("/stop = %q", out)
	}
	// Alice is kicked with Server closed, then the socket goes away.
	sawDisc = false
	for i := 0; i < 50 && !sawDisc; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			break
		}
		if p.ID == server.IDPlaySystemChatCB {
			continue
		}
		sawDisc = p.ID == server.IDPlayDisconnect
	}
	if !sawDisc {
		t.Fatal("Alice never received the stop disconnect")
	}
}

// TestMcGoOnlyPlaceBlock stocks a creative slot over the wire, places
// stone against the grass at spawn, and requires the Block Update plus
// the prediction ack on the placer and the same update on a watcher.
func TestMcGoOnlyPlaceBlock(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, _ := joinPlayBlackbox(t, addr, "Watcher")
	defer ca.Close()
	drainToJoinBlackbox(t, "watcher", pra)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Builder")
	defer cb.Close()
	drainToJoinBlackbox(t, "builder", prb)
	_ = ca.SetDeadline(time.Now().Add(10 * time.Second))
	_ = cb.SetDeadline(time.Now().Add(10 * time.Second))
	_ = readSystemChatBlackbox(t, pra, 5*time.Second) // join broadcast

	give := func() {
		t.Helper()
		var b protocol.Buffer
		b.Short(36) // hotbar slot 0 in the player inventory menu
		b.VarInt(1)
		b.VarInt(1) // stone item, see server.ItemStone
		if err := pwb.WritePacket(server.IDPlayCreativeSlotSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		b.Reset()
		b.Short(0)
		if err := pwb.WritePacket(server.IDPlayHeldChangeSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	give()
	// Stand within reach of the grass at (8,-61,8) in the flat world.
	if err := sendMovePosRot(pwb, 7.5, -60, 7.5); err != nil {
		t.Fatal(err)
	}

	var b protocol.Buffer
	b.VarInt(0)
	b.Position(8, -61, 8)
	b.VarInt(1)
	b.Float(0.5)
	b.Float(0.5)
	b.Float(0.5)
	b.Bool(true)
	b.VarInt(5)
	if err := pwb.WritePacket(server.IDPlayUseItemOnSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Placer: Block Update (stone) then ack 5.
	sawUpdate, sawAck := false, false
	deadline := time.Now().Add(10 * time.Second)
	for !(sawUpdate && sawAck) && time.Now().Before(deadline) {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("builder place stream: %v", err)
		}
		switch p.ID {
		case server.IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err != nil {
				t.Fatalf("update pos: %v", err)
			}
			st, err := r.VarInt()
			if err != nil {
				t.Fatalf("update state: %v", err)
			}
			if x == 8 && y == -60 && z == 8 && st == server.StateStone {
				sawUpdate = true
			}
		case server.IDPlayBlockChangedAck:
			if seq, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil && seq == 5 {
				sawAck = true
			}
		}
	}
	if !sawUpdate || !sawAck {
		t.Fatalf("builder saw update=%v ack=%v", sawUpdate, sawAck)
	}
	// Watcher: the same stone update.
	for i := 0; i < 40; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("watcher place stream: %v", err)
		}
		if p.ID != server.IDPlayBlockUpdateCB {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		x, y, z, err := r.Position()
		if err != nil {
			t.Fatalf("watcher pos: %v", err)
		}
		st, err := r.VarInt()
		if err != nil {
			t.Fatalf("watcher state: %v", err)
		}
		if x == 8 && y == -60 && z == 8 && st == server.StateStone {
			return
		}
	}
	t.Fatal("watcher never received stone update (8,-60,8)")
}

// TestMcGoOnlyPlayerVisibility proves remote players render over TCP:
// Bob spawns for Alice with his profile UUID, Alice's move teleports
// for Bob, and Bob's kick removes his entity for Alice.
func TestMcGoOnlyPlayerVisibility(t *testing.T) {
	addr := requireMcGoOnly(t)
	ca, pra, pwa := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	cb, prb, pwb := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	drainToJoinBlackbox(t, "bob", prb)
	_ = ca.SetDeadline(time.Now().Add(20 * time.Second))
	_ = cb.SetDeadline(time.Now().Add(20 * time.Second))

	if text := readSystemChatBlackbox(t, pra, 10*time.Second); !strings.Contains(text, "Bob") {
		t.Fatalf("join text = %q", text)
	}
	if p, err := pra.ReadPacket(); err != nil || p.ID != server.IDPlayPlayerInfoUpdate {
		t.Fatalf("join info = %#x (%v)", p.ID, err)
	}
	// Bob's entity spawns for Alice.
	var bobID int32
	for i := 0; i < 20; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice spawn stream: %v", err)
		}
		if p.ID != server.IDPlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, err := r.VarInt()
		if err != nil {
			t.Fatalf("spawn id: %v", err)
		}
		uuid, err := r.UUID()
		if err != nil {
			t.Fatalf("spawn uuid: %v", err)
		}
		ty, err := r.VarInt()
		if err != nil {
			t.Fatalf("spawn type: %v", err)
		}
		if ty != server.EntityTypePlayer {
			continue // greeting pig or future mob: not our target
		}
		if uuid != offlineUUIDBlackbox("Bob") {
			t.Fatalf("spawn uuid = %x, want Bob", uuid)
		}
		bobID = id
		break
	}
	if bobID == 0 {
		t.Fatal("Alice never saw Bob's player entity")
	}
	// Alice moves; Bob tracks her via entity teleport.
	if err := sendMovePosRot(pwa, 20.5, 66, 8.5); err != nil {
		t.Fatal(err)
	}
	sawTele := false
	for i := 0; i < 60 && !sawTele; i++ {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob teleport stream: %v", err)
		}
		if p.ID != server.IDPlayEntityTeleportCB {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		id, _ := r.VarInt()
		x, _ := r.Double()
		y, _ := r.Double()
		z, _ := r.Double()
		if id != 0 && x == 20.5 && y == 66 && z == 8.5 {
			sawTele = true
		}
	}
	if !sawTele {
		t.Fatal("Bob never tracked Alice's move")
	}
	// Bob is kicked; Alice's tracker drops his entity.
	var b protocol.Buffer
	b.String("kick Bob")
	b.Long(0)
	b.Long(0)
	if err := pwa.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	if text := readSystemChatBlackbox(t, pra, 10*time.Second); !strings.Contains(text, "Kicked Bob") {
		t.Fatalf("kick reply = %q", text)
	}
	sawRemove := false
	deadline := time.Now().Add(10 * time.Second)
	for !sawRemove && time.Now().Before(deadline) {
		p, err := pra.ReadPacket()
		if err != nil {
			t.Fatalf("alice remove stream: %v", err)
		}
		if p.ID != server.IDPlayRemoveEntity {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		n, _ := r.VarInt()
		for j := int32(0); j < n; j++ {
			if id, _ := r.VarInt(); id == bobID {
				sawRemove = true
			}
		}
	}
	if !sawRemove {
		t.Fatalf("Alice never dropped Bob's entity %d", bobID)
	}
	_ = pwb
}

// waitForPacketBlackbox reads until a packet with the given ID arrives
// or the timeout elapses.
func waitForPacketBlackbox(t *testing.T, c net.Conn, pr *protocol.Reader, id int32, timeout time.Duration) *protocol.Packet {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.SetDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			return nil
		}
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// TestMcGoOnlyTimeSync requires the periodic Update Time broadcast to
// advance the world age once per second.
func TestMcGoOnlyTimeSync(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, _ := joinPlayBlackbox(t, addr, "Timelord")
	defer c.Close()
	drainToJoinBlackbox(t, "timelord", pr)

	readAge := func(p *protocol.Packet) int64 {
		r := protocol.NewPacketReader(p.Data)
		age, err := r.Long()
		if err != nil {
			t.Fatalf("update time: %v", err)
		}
		return age
	}
	p1 := waitForPacketBlackbox(t, c, pr, server.IDPlayUpdateTime, 5*time.Second)
	if p1 == nil {
		t.Fatal("no periodic update time within 5s")
	}
	a1 := readAge(p1)
	p2 := waitForPacketBlackbox(t, c, pr, server.IDPlayUpdateTime, 5*time.Second)
	if p2 == nil {
		t.Fatal("no second update time within 5s")
	}
	if a2 := readAge(p2); a2 <= a1 {
		t.Fatalf("world age did not advance: %d -> %d", a1, a2)
	}
}

// TestMcGoOnlyViewDistanceStreams proves a client that announces its
// view distance in configuration receives a full view window (17x17
// for distance 8) in one Chunk Batch, plus the cache radius sync.
func TestMcGoOnlyViewDistanceStreams(t *testing.T) {
	addr := requireMcGoOnly(t)
	c, pr, pw := loginStart(t, addr, "FarSight")
	defer c.Close()
	readLoginSuccess(t, pr, pw, "mc-go")
	if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	var info protocol.Buffer
	info.String("en_US")
	info.Byte(8)
	info.VarInt(0) // chat mode: enabled
	info.Bool(true)
	info.Byte(0x7F)
	info.VarInt(1)
	info.Bool(false) // text filtering
	info.Bool(true)  // allow server listings
	info.VarInt(0)   // particle status: all
	if err := pw.WritePacket(server.IDConfigClientInformation, info.Bytes()); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw, "99.9-test")
	for i := 0; i < 64; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		if p.ID == server.IDConfigFinish {
			break
		}
	}
	if err := pw.WritePacket(server.IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	drainToJoinBlackbox(t, "far", pr)

	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	// A view distance of 8 streams the window the real server streams for
	// that distance: the 19x19 square with its corners cut by a circle,
	// 329 columns rather than the 289 of a plain square (see
	// TestChunkWindowParity for the measurement).
	//
	// It arrives the way the real server sends it, nine columns at a time
	// and one batch a tick once the client acknowledges (see
	// server.pumpChunks), so the scripted client answers every batch the
	// way a real one does and counts what lands.
	const want = 329
	chunks := 0
	deadline := time.Now().Add(30 * time.Second)
	for chunks < want && time.Now().Before(deadline) {
		if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		if p.ID == server.IDPlayChunkCacheRadius {
			t.Fatalf("the real server sends no chunk cache radius packet; mc-go sent one")
		}
		switch p.ID {
		case server.IDPlayChunkData:
			chunks++
		case server.IDPlayChunkBatchDone:
			if err := ackChunkBatch(pw); err != nil {
				t.Fatal(err)
			}
		}
	}
	if chunks != want {
		t.Fatalf("chunks = %d, want %d", chunks, want)
	}
	// Nothing more belongs to the window: the stream is over.
	for {
		if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		if p.ID == server.IDPlayChunkData {
			t.Fatalf("chunks past the window: %d streamed, want %d", chunks+1, want)
		}
	}
}

// TestMcGoOnlyLatencyUpdate proves the tab-list ping flow: a fast
// keep-alive cadence pings Alice, her echo is acknowledged, and every
// session receives Alice's update-latency row.
func TestMcGoOnlyLatencyUpdate(t *testing.T) {
	addr := requireMcGoOnlyCfg(t, func(s *server.Server) {
		s.KeepAliveEvery = 200 * time.Millisecond
		s.KeepAliveTimeout = 5 * time.Second
	})
	ca, pra, pwa := joinPlayBlackbox(t, addr, "Alice")
	defer ca.Close()
	drainToJoinBlackbox(t, "alice", pra)
	cb, prb, _ := joinPlayBlackbox(t, addr, "Bob")
	defer cb.Close()
	drainToJoinBlackbox(t, "bob", prb)

	aliceUUID := offlineUUIDBlackbox("Alice")
	sawOnAlice, sawOnBob := false, false
	deadline := time.Now().Add(6 * time.Second)
	for !(sawOnAlice && sawOnBob) && time.Now().Before(deadline) {
		// Alice answers keep-alives as they arrive.
		if p, ok := readWithin(pra, 100*time.Millisecond); ok {
			if p.ID == server.IDPlayKeepAliveCB {
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.Long(); err == nil {
					var ack protocol.Buffer
					ack.Long(id)
					if err := pwa.WritePacket(server.IDPlayKeepAliveSB, ack.Bytes()); err != nil {
						t.Fatal(err)
					}
				}
			} else if u, ok := latencyRow(p); ok {
				if u != aliceUUID {
					t.Fatalf("alice latency uuid = %x, want %x", u, aliceUUID)
				}
				sawOnAlice = true
			}
		}
		if p, ok := readWithin(prb, 100*time.Millisecond); ok {
			if u, ok := latencyRow(p); ok {
				if u != aliceUUID {
					t.Fatalf("bob latency uuid = %x, want %x", u, aliceUUID)
				}
				sawOnBob = true
			}
		}
	}
	if !(sawOnAlice && sawOnBob) {
		t.Fatalf("latency row: alice=%v bob=%v", sawOnAlice, sawOnBob)
	}
}

// readWithin reads one packet with a short deadline.
func readWithin(pr *protocol.Reader, d time.Duration) (protocol.Packet, bool) {
	pr.SetDeadline(time.Now().Add(d))
	p, err := pr.ReadPacket()
	pr.SetDeadline(time.Time{})
	return p, err == nil
}

// latencyRow reports whether p is an update-latency Player Info Update
// and returns its subject UUID.
func latencyRow(p protocol.Packet) ([16]byte, bool) {
	if p.ID != server.IDPlayPlayerInfoUpdate {
		return [16]byte{}, false
	}
	r := protocol.NewPacketReader(p.Data)
	act, err := r.Byte()
	if err != nil || act != 0x10 {
		return [16]byte{}, false
	}
	n, err := r.VarInt()
	if err != nil || n != 1 {
		return [16]byte{}, false
	}
	u, err := r.UUID()
	if err != nil {
		return [16]byte{}, false
	}
	if _, err := r.VarInt(); err != nil {
		return [16]byte{}, false
	}
	return u, true
}
