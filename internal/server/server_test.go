package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// pipePair connects a scripted fake client to a live session without
// touching the network, so tests run even where binds are blocked.
func pipePair(t *testing.T, s *Server) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleConn(server)
	}()
	// A cold join under the race detector can take longer than the
	// protocol assertions; this watchdog only guards a hung test session.
	watchdog := time.AfterFunc(60*time.Second, func() { _ = client.Close() })
	t.Cleanup(func() {
		watchdog.Stop()
		_ = client.Close()
		<-done
	})
	return client, protocol.NewReader(client), protocol.NewWriter(client)
}

func sendHandshake(pw *protocol.Writer, proto int32, addr string, port uint16, next int32) error {
	var b protocol.Buffer
	b.VarInt(proto)
	b.String(addr)
	b.UShort(port)
	b.VarInt(next)
	return pw.WritePacket(IDHandshake, b.Bytes())
}

func TestStatusPing(t *testing.T) {
	s := New("unused")
	_, pr, pw := pipePair(t, s)

	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextStatus); err != nil {
		t.Fatal(err)
	}
	if err := pw.WritePacket(IDStatusRequest, nil); err != nil {
		t.Fatal(err)
	}
	resp, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != IDStatusResponse {
		t.Fatalf("status id = %#x", resp.ID)
	}
	r := protocol.NewPacketReader(resp.Data)
	js, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
	}
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatalf("bad json %q: %v", js, err)
	}
	if v.Version.Protocol != ProtocolVersion || v.Version.Name != VersionName {
		t.Fatalf("version = %+v", v.Version)
	}

	const payload int64 = 123456789
	var b protocol.Buffer
	b.Long(payload)
	if err := pw.WritePacket(IDPingRequest, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	pong, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if pong.ID != IDPongResponse {
		t.Fatalf("pong id = %#x", pong.ID)
	}
	got, err := protocol.NewPacketReader(pong.Data).Long()
	if err != nil || got != payload {
		t.Fatalf("pong payload = %d, err %v", got, err)
	}
}

func TestLoginJoinFlow(t *testing.T) {
	s := New("unused")
	c, pr, pw := pipePair(t, s)
	defer c.Close()

	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String("TestPlayer")
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}

	setComp, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if setComp.ID != IDLoginSetCompression {
		t.Fatalf("login resp id = %#x, want set compression 0x03", setComp.ID)
	}
	threshold, err := protocol.NewPacketReader(setComp.Data).VarInt()
	if err != nil || threshold != CompressionThreshold {
		t.Fatalf("threshold = %d, err %v", threshold, err)
	}
	pr.SetThreshold(threshold)
	pw.SetThreshold(threshold)

	success, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if success.ID != IDLoginSuccess {
		t.Fatalf("login resp id = %#x, want success 0x02", success.ID)
	}
	sr := protocol.NewPacketReader(success.Data)
	if _, err := sr.UUID(); err != nil {
		t.Fatalf("success uuid: %v", err)
	}
	name, err := sr.String()
	if err != nil || name != "TestPlayer" {
		t.Fatalf("success name = %q, err %v", name, err)
	}

	if err := pw.WritePacket(IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	var kp protocol.Buffer
	writeKnownPacks(&kp, []packEntry{{"minecraft", "core", "99.9-test"}})
	if err := pw.WritePacket(IDConfigKnownPacks, kp.Bytes()); err != nil {
		t.Fatal(err)
	}
	brand, err := pr.ReadPacket()
	if err != nil || brand.ID != IDConfigPluginMessageCB {
		t.Fatalf("config brand id = %#x, err %v", brand.ID, err)
	}
	feat, err := pr.ReadPacket()
	if err != nil || feat.ID != IDConfigFeatureFlags {
		t.Fatalf("feature flags id = %#x, err %v", feat.ID, err)
	}
	kbResp, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if kbResp.ID != IDConfigKnownPacksCB {
		t.Fatalf("known packs id = %#x, want %#x", kbResp.ID, IDConfigKnownPacksCB)
	}
	sel, err := parseKnownPacks(kbResp.Data)
	if err != nil || len(sel) != 1 || sel[0].id != "core" || sel[0].version != "26.2" {
		t.Fatalf("selected packs = %+v, err %v", sel, err)
	}
	// All captured registries arrive in vanilla order; both dimension
	// types and biomes must be present.
	seenReg := map[string]bool{}
	var afterRegs protocol.Packet
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != IDConfigRegistryData {
			afterRegs = p
			break
		}
		key, entries := parseRegistry(t, p.Data)
		if len(entries) == 0 {
			t.Fatalf("registry %q has no entries", key)
		}
		seenReg[key] = true
	}
	for _, need := range []string{"minecraft:dimension_type", "minecraft:worldgen/biome"} {
		if !seenReg[need] {
			t.Fatalf("registries lack %s: %v", need, seenReg)
		}
	}
	if afterRegs.ID != IDConfigUpdateTags {
		t.Fatalf("update tags id = %#x, want 0x%x", afterRegs.ID, IDConfigUpdateTags)
	}
	finish, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if finish.ID != IDConfigFinish {
		t.Fatalf("config id = %#x, want finish 0x03", finish.ID)
	}
	if err := pw.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	_ = finish

	join, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if join.ID != IDPlayJoinGame {
		t.Fatalf("play id = %#x, want join game", join.ID)
	}
	eid, err := protocol.NewPacketReader(join.Data).Int()
	if err != nil || eid < 1 {
		t.Fatalf("entity id = %d, err %v", eid, err)
	}

	// Vanilla-ordered stateless join burst, then the chunk window.
	wantIDs := []int32{
		IDPlayDifficulty, IDPlayAbilitiesCB, IDPlaySetHeldItemCB,
		IDPlayUpdateRecipes, IDPlayEntityEvent, IDPlayCommands,
		IDPlayRecipeBookSet, IDPlayRecipeBookAdd, IDPlaySyncPos,
		IDPlayServerData, IDPlayPlayerInfoUpdate, IDPlayPlayerInfoUpdate,
		IDPlayInitializeBorder, IDPlayUpdateTime, IDPlayDefaultSpawn,
		IDPlayGameEvent, IDPlayTickingState, IDPlayTickingStep,
		IDPlaySetCenterChunk,
		IDPlayContainerContent, IDPlayEntityMetadata,
		IDPlayRecipeBookAdd, IDPlayUpdateAdvance, IDPlaySetHealth,
		IDPlaySetExperience,
	}
	for i, want := range wantIDs {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("join packet %d: %v", i, err)
		}
		if p.ID != want {
			t.Fatalf("join packet %d = %#x, want %#x", i, p.ID, want)
		}
		if p.ID == IDPlaySyncPos {
			prr := protocol.NewPacketReader(p.Data)
			if tid, err := prr.VarInt(); err != nil || tid != 1 {
				t.Fatalf("teleport id = %d, err %v (want 1)", tid, err)
			}
			x, err := prr.Double()
			if err != nil {
				t.Fatalf("pos x: %v", err)
			}
			y, err := prr.Double()
			if err != nil {
				t.Fatalf("pos y: %v", err)
			}
			z, err := prr.Double()
			if err != nil {
				t.Fatalf("pos z: %v", err)
			}
			sx, sy, sz := s.World().Spawn()
			if x != sx || y != sy || z != sz {
				t.Fatalf("spawn = %v,%v,%v want %v,%v,%v", x, y, z, sx, sy, sz)
			}
		}
		if p.ID == IDPlayPlayerInfoUpdate {
			r := protocol.NewPacketReader(p.Data)
			act, _ := r.Byte()
			if act != infoActions {
				t.Fatalf("info actions = %#x, want %#x", act, infoActions)
			}
			n, _ := r.VarInt()
			if n == 1 {
				if _, err := r.UUID(); err != nil {
					t.Fatalf("self info uuid: %v", err)
				}
				name, err := r.String()
				if err != nil || name != "TestPlayer" {
					t.Fatalf("self info name = %q, err %v", name, err)
				}
			}
		}
	}
	// Spawn columns: the default window around chunk (0,0), which is the
	// 7x7 square the real server streams to a client that has announced
	// no view distance (see TestChunkWindowParity). Light rides inline.
	//
	// The window arrives in two halves, as it does from the real server:
	// the opening batch of nine columns, which is all a client is sent
	// before it acknowledges anything, and then the rest once it
	// answers with a rate (see pumpChunks for the measurement).
	seen := map[[2]int32]bool{}
	readBatch := func(want int) {
		if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchStart {
			t.Fatalf("batch start = %#x, err %v", p.ID, err)
		}
		for i := 0; i < want; i++ {
			ch, err := pr.ReadPacket()
			if err != nil {
				t.Fatal(err)
			}
			if ch.ID != IDPlayChunkData {
				t.Fatalf("chunk %d id = %#x, want chunk data", i, ch.ID)
			}
			cr := protocol.NewPacketReader(ch.Data)
			cx, err := cr.Int()
			if err != nil {
				t.Fatalf("chunk %d x: %v", i, err)
			}
			cz, err := cr.Int()
			if err != nil {
				t.Fatalf("chunk %d z: %v", i, err)
			}
			if cx < -3 || cx > 3 || cz < -3 || cz > 3 || seen[[2]int32{cx, cz}] {
				t.Fatalf("chunk %d coords out of range or dup: %d,%d", i, cx, cz)
			}
			seen[[2]int32{cx, cz}] = true
		}
		if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchDone {
			t.Fatalf("batch done = %#x, err %v", p.ID, err)
		}
	}
	readBatch(initialChunkBatch)
	// The attribute sync follows the opening batch, as on vanilla.
	if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayUpdateAttributes {
		t.Fatalf("attributes = %#x, err %v", p.ID, err)
	}
	var batchAck protocol.Buffer
	batchAck.Float(maxChunkBatch)
	if err := pw.WritePacket(IDPlayChunkBatchRecvSB, batchAck.Bytes()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	s.chunkTickStep()
	readBatch(49 - initialChunkBatch)
	if len(seen) != 49 {
		t.Fatalf("chunks = %d, want 49", len(seen))
	}
	if got := s.OnlineCount(); got != 1 {
		t.Fatalf("online = %d, want 1 during session", got)
	}
}

// joinPlay runs the scripted handshake through config finish-ack,
// returning a client positioned at the first play packet (Join Game).
func joinPlay(t *testing.T, s *Server, name string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, pr, pw := pipePair(t, s)
	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String(name)
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	setComp, err := pr.ReadPacket()
	if err != nil || setComp.ID != IDLoginSetCompression {
		t.Fatalf("want set compression, got %#x err %v", setComp.ID, err)
	}
	threshold, err := protocol.NewPacketReader(setComp.Data).VarInt()
	if err != nil {
		t.Fatal(err)
	}
	pr.SetThreshold(threshold)
	pw.SetThreshold(threshold)
	if success, err := pr.ReadPacket(); err != nil || success.ID != IDLoginSuccess {
		t.Fatalf("want login success, got %#x err %v", success.ID, err)
	}
	if err := pw.WritePacket(IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	var kp protocol.Buffer
	writeKnownPacks(&kp, []packEntry{{"minecraft", "core", "99.9-test"}})
	if err := pw.WritePacket(IDConfigKnownPacks, kp.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Brand + feature flags + known packs + registries + tags + finish.
	for i := 0; i < 64; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if p.ID == IDConfigFinish {
			break
		}
	}
	if err := pw.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	// A real client reports its terrain loaded a fraction of a second
	// after entering play, which is what opens its world interactions;
	// the scripted client does the same so tests exercise the same world
	// (see interactionsOpen for the gate the report releases).
	if err := pw.WritePacket(IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	return c, pr, pw
}

func TestKeepAliveAckKeepsSession(t *testing.T) {
	s := New("unused")
	s.KeepAliveEvery = 20 * time.Millisecond
	s.KeepAliveTimeout = 200 * time.Millisecond
	c, pr, pw := joinPlay(t, s, "Alive")
	defer c.Close()

	// Join Game + SyncPos + 9 chunks precede the first keep-alive.
	drainJoinBurst(t, pr)
	ka, err := waitPacket(t, pr, IDPlayKeepAliveCB)
	if err != nil {
		t.Fatalf("keepalive: %v", err)
	}
	id, err := protocol.NewPacketReader(ka.Data).Long()
	if err != nil || id < 1 {
		t.Fatalf("keepalive id = %d, err %v", id, err)
	}
	var ack protocol.Buffer
	ack.Long(id)
	if err := pw.WritePacket(IDPlayKeepAliveSB, ack.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := waitPacket(t, pr, IDPlayKeepAliveCB); err != nil {
		t.Fatalf("second keepalive: %v", err)
	}
	if got := s.OnlineCount(); got != 1 {
		t.Fatalf("online = %d, want 1", got)
	}
}

func TestKeepAliveTimeoutDropsSilent(t *testing.T) {
	s := New("unused")
	s.KeepAliveEvery = 20 * time.Millisecond
	s.KeepAliveTimeout = 60 * time.Millisecond
	c, pr, _ := joinPlay(t, s, "Silent")
	defer c.Close()

	// Drain everything but never ack: the watchdog must fire.
	go func() {
		for {
			if _, err := pr.ReadPacket(); err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for s.OnlineCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.OnlineCount(); got != 0 {
		t.Fatalf("online = %d, want 0 after timeout", got)
	}
}

func TestTeleportConfirmTracked(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Mover")
	defer c.Close()

	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	var b protocol.Buffer
	b.VarInt(1) // confirm the spawn teleport from SyncPos
	if err := pw.WritePacket(IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var found int32 = -2
		s.sessions.Range(func(_, v any) bool {
			found = v.(*Session).teleportAck.Load()
			return false
		})
		if found == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("teleportAck = %d, want 1", found)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sendPosRot(t *testing.T, pw *protocol.Writer, x, y, z float64, yaw, pitch float32, ground bool) {
	t.Helper()
	var b protocol.Buffer
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(yaw)
	b.Float(pitch)
	var flags byte
	if ground {
		flags |= 0x01
	}
	b.Byte(flags)
	if err := pw.WritePacket(IDPlayMovePosRotSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func sessionPos(t *testing.T, s *Server) (float64, float64, float64) {
	t.Helper()
	var x, y, z float64
	var found bool
	s.sessions.Range(func(_, v any) bool {
		x, y, z, _, _, _ = v.(*Session).Pos()
		found = true
		return false
	})
	if !found {
		t.Fatal("no session tracked")
	}
	return x, y, z
}

func TestMovementUpdatesPos(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Mover")
	defer c.Close()
	drainJoinBurst(t, pr)
	finishJoin(t, s, pr, pw)
	sendPosRot(t, pw, 10.5, 66, -3.25, 90, 0, true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if x, y, z := sessionPos(t, s); x == 10.5 && y == 66 && z == -3.25 {
			break
		}
		if time.Now().After(deadline) {
			x, y, z := sessionPos(t, s)
			t.Fatalf("pos = %v,%v,%v", x, y, z)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Moving into chunk (0,-1) takes on the columns that only the new
	// window holds: drain that batch so the pipe reader keeps up with
	// the session writer.
	fresh := freshColumns(2, 0, 0, 0, -1)
	if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchStart {
		t.Fatalf("batch start = %#x, err %v", p.ID, err)
	}
	for i := 0; i < len(fresh); i++ {
		ch, err := pr.ReadPacket()
		if err != nil || ch.ID != IDPlayChunkData {
			t.Fatalf("stream chunk %d = %#x, err %v", i, ch.ID, err)
		}
	}
	if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchDone {
		t.Fatalf("batch done = %#x, err %v", p.ID, err)
	}
	// Out-of-border and NaN moves must not touch state.
	sendPosRot(t, pw, 1e9, 66, 0, 0, 0, true)
	var nan protocol.Buffer
	nan.Double(10.5)
	nan.Double(math.NaN())
	nan.Double(-3.25)
	nan.Float(0)
	nan.Float(0)
	nan.Byte(0x01)
	if err := pw.WritePacket(IDPlayMovePosRotSB, nan.Bytes()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if x, y, z := sessionPos(t, s); x != 10.5 || y != 66 || z != -3.25 {
		t.Fatalf("bad move changed pos to %v,%v,%v", x, y, z)
	}
}

// waitPacket reads until a packet with the given ID arrives.
func waitPacket(t *testing.T, pr *protocol.Reader, id int32) (protocol.Packet, error) {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			return protocol.Packet{}, err
		}
		if p.ID == id {
			return p, nil
		}
	}
	return protocol.Packet{}, fmt.Errorf("packet %#x never arrived", id)
}

func sendChat(t *testing.T, pw *protocol.Writer, id int32, text string) {
	t.Helper()
	var b protocol.Buffer
	b.String(text)
	b.Long(0) // timestamp placeholder; server reads the head only
	b.Long(0) // salt placeholder
	if err := pw.WritePacket(id, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// readSystemChatComponent reads one System Chat packet and returns the
// translation key of its component, for replies that are not plain text.
func readSystemChatComponent(t *testing.T, pr *protocol.Reader) string {
	t.Helper()
	root := readSystemChatNBT(t, pr)
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("component root = %T, want compound", root)
	}
	if key, ok := m["translate"].(string); ok {
		return key
	}
	if l, ok := m["extra"].(nbt.List); ok && len(l.Items) > 0 {
		if sub, ok := l.Items[0].(map[string]any); ok {
			if key, ok := sub["translate"].(string); ok {
				return key
			}
		}
	}
	t.Fatalf("component has no translation key: %v", m)
	return ""
}

// readSystemChatNBT reads one System Chat packet and decodes its
// component.
func readSystemChatNBT(t *testing.T, pr *protocol.Reader) any {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("chat read: %v", err)
		}
		if p.ID != IDPlaySystemChatCB {
			continue
		}
		root, err := nbt.ReadUnnamed(bytes.NewReader(p.Data))
		if err != nil {
			t.Fatalf("chat nbt: %v", err)
		}
		return root
	}
	t.Fatal("no system chat arrived")
	return nil
}

func readSystemChat(t *testing.T, pr *protocol.Reader) string {
	t.Helper()
	for i := 0; i < 256; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("chat read: %v", err)
		}
		if p.ID != IDPlaySystemChatCB {
			continue
		}
		root, err := nbt.ReadUnnamed(bytes.NewReader(p.Data))
		if err != nil {
			t.Fatalf("chat nbt: %v", err)
		}
		text, ok := root.(string)
		if !ok {
			t.Fatalf("chat root = %T, want string", root)
		}
		return text
	}
	t.Fatal("no system chat arrived")
	return ""
}

func TestChatCommandRunsPlugin(t *testing.T) {
	s := New("unused")
	s.commands.Register("ping", func(player string, args []string) string {
		return "pong " + player
	})
	c, pr, pw := joinPlay(t, s, "Chatter")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sendChat(t, pw, IDPlayChatCommandSB, "ping")
	if got := readSystemChat(t, pr); got != "pong Chatter" {
		t.Fatalf("reply = %q", got)
	}
	sendChat(t, pw, IDPlayChatCommandSB, "nope")
	// An unknown command is answered with the real server's error
	// component, not a hand-written sentence.
	if got := readSystemChatComponent(t, pr); got != "command.unknown.command" {
		t.Fatalf("unknown-command reply = %q", got)
	}
}

func TestChatBroadcastsMessage(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Talker")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sendChat(t, pw, IDPlayChatMessageSB, "hello world")
	// Player chat relays as Player Chat Message (unsigned content).
	p, err := waitPacket(t, pr, IDPlayPlayerChatCB)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // global index
		t.Fatal(err)
	}
	if _, err := r.UUID(); err != nil { // sender
		t.Fatal(err)
	}
	if _, err := r.VarInt(); err != nil { // sender index
		t.Fatal(err)
	}
	if present, err := r.Bool(); err != nil || present {
		t.Fatalf("signature present = %v, err %v", present, err)
	}
	if msg, err := r.String(); err != nil || msg != "hello world" {
		t.Fatalf("message = %q, err %v", msg, err)
	}
}

func TestChunkStreamingOnMove(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Walker")
	defer c.Close()
	drainJoinBurst(t, pr)
	finishJoin(t, s, pr, pw)
	// Walk east into chunk (2,0): the window is centered there now, so
	// the columns the old window around (0,0) did not cover stream as
	// chunk+light pairs. Both windows come from the same rule, so the
	// expected set is derived from it rather than listed.
	sendPosRot(t, pw, 40.5, 66, 8.5, 0, 0, true)
	want := map[[2]int32]bool{}
	for _, k := range freshColumns(2, 0, 0, 2, 0) {
		want[k] = false
	}
	if len(want) == 0 {
		t.Fatal("the move streams nothing")
	}
	if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchStart {
		t.Fatalf("batch start = %#x, err %v", p.ID, err)
	}
	for i := 0; i < len(want); i++ {
		ch, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("stream chunk %d: %v", i, err)
		}
		if ch.ID != IDPlayChunkData {
			t.Fatalf("stream %d id = %#x, want chunk data", i, ch.ID)
		}
		cr := protocol.NewPacketReader(ch.Data)
		cx, _ := cr.Int()
		cz, _ := cr.Int()
		key := [2]int32{cx, cz}
		if _, ok := want[key]; !ok {
			t.Fatalf("unexpected streamed chunk %d,%d", cx, cz)
		}
		want[key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("column %v never streamed", k)
		}
	}
	if p, err := pr.ReadPacket(); err != nil || p.ID != IDPlayChunkBatchDone {
		t.Fatalf("batch done = %#x, err %v", p.ID, err)
	}
	// The cache center follows the player onto the new column.
	ctr, err := pr.ReadPacket()
	if err != nil || ctr.ID != IDPlaySetCenterChunk {
		t.Fatalf("center id = %#x, err %v", ctr.ID, err)
	}
	tr := protocol.NewPacketReader(ctr.Data)
	if cx, err := tr.VarInt(); err != nil || cx != 2 {
		t.Fatalf("center x = %d, err %v (want 2)", cx, err)
	}
	if cz, err := tr.VarInt(); err != nil || cz != 0 {
		t.Fatalf("center z = %d, err %v (want 0)", cz, err)
	}
	// Staying inside the same column must not re-send the center.
	sendPosRot(t, pw, 41.5, 66, 9.5, 0, 0, true)
	_ = c.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if p, err := pr.ReadPacket(); err == nil {
		t.Fatalf("repeat center: unexpected %#x", p.ID)
	}
	_ = c.SetDeadline(time.Time{})
}

// chatText decodes a system-chat or NBT component payload into its
// plain text form. The 776 protocol carries components as NBT; plain
// text is a TAG_String root.
func chatText(t *testing.T, data []byte) string {
	t.Helper()
	root, err := nbt.ReadUnnamed(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("component nbt: %v", err)
	}
	if text, ok := root.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", root)
}

func TestOfflineUUID(t *testing.T) {
	id := offlineUUID("Notch")
	if id[6]>>4 != 3 {
		t.Fatalf("version bits = %x", id[6]>>4)
	}
	if id[8]>>6 != 2 {
		t.Fatalf("variant bits = %x", id[8]>>6)
	}
	if again := offlineUUID("Notch"); again != id {
		t.Fatal("offline UUID not deterministic")
	}
}
