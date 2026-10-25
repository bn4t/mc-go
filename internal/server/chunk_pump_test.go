package server

import (
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func chunkPumpFixture(t *testing.T, s *Server, count int, writer bool) (*Session, net.Conn) {
	t.Helper()
	sess, client := outboundPipe(t, s, writer)
	sess.setIdentity("SnapshotViewer", offlineUUID("SnapshotViewer"))
	sess.dim.Store(s.dims[dimOverworld])
	sess.spawned.Store(true)
	sess.opened, sess.rate, sess.burst, sess.interval = true, maxChunkBatch, burstBatches, 1
	sess.queued = make(map[[2]int]bool)
	for i := 0; i < count; i++ {
		key := [2]int{i, 0}
		sess.pending = append(sess.pending, key)
		sess.queued[key] = true
	}
	t.Cleanup(func() {
		if !writer {
			for len(sess.sendCh) != 0 {
				sess.retireOutbound(<-sess.sendCh)
			}
		}
	})
	return sess, client
}

func readPumpQueue(t *testing.T, sess *Session) []protocol.Packet {
	t.Helper()
	var packets []protocol.Packet
	for len(sess.sendCh) != 0 {
		packet := <-sess.sendCh
		data := packet.data
		if packet.future != nil {
			select {
			case <-packet.future.done:
			case <-time.After(3 * time.Second):
				t.Fatal("encoder future did not complete")
			}
			var err error
			data, err = packet.future.wait(nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		packets = append(packets, protocol.Packet{ID: packet.id, Data: data})
		sess.retireOutbound(packet)
	}
	return packets
}

func assertPumpBatch(t *testing.T, packets []protocol.Packet, count int) {
	t.Helper()
	if len(packets) < count+2 || packets[0].ID != IDPlayChunkBatchStart {
		t.Fatalf("invalid batch packets: count=%d", len(packets))
	}
	for i := 0; i < count; i++ {
		if packets[i+1].ID != IDPlayChunkData {
			t.Fatalf("packet%d = %#x", i+1, packets[i+1].ID)
		}
	}
	done := packets[count+1]
	n, err := protocol.NewPacketReader(done.Data).VarInt()
	if done.ID != IDPlayChunkBatchDone || err != nil || int(n) != count {
		t.Fatalf("batch done id=%#x count=%d error=%v", done.ID, n, err)
	}
}

func TestChunkPumpSaturationPreservesPendingAndPacing(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	s.ChunkEncodingJobs = 2
	s.ChunkEncodingBytes = 2 * chunkSnapshotUpperBound(s.dims[dimOverworld], nil)
	sess, _ := chunkPumpFixture(t, s, 3, false)
	reservation, ok := s.tryReserveChunkEncoding(2, s.ChunkEncodingBytes)
	if !ok {
		t.Fatal("could not fill encoder quota")
	}
	defer reservation.Close()
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
	})
	if len(sess.pending) != 3 || len(sess.sent) != 0 || sess.burst != burstBatches || sess.cool != 0 || len(sess.sendCh) != 0 {
		t.Fatalf("saturation changed batch state: pending=%d sent=%d burst=%d cool=%d packets=%d", len(sess.pending), len(sess.sent), sess.burst, sess.cool, len(sess.sendCh))
	}
	reservation.Close()
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
	})
	assertPumpBatch(t, readPumpQueue(t, sess), 2)
	if len(sess.pending) != 1 || len(sess.sent) != 2 || sess.burst != burstBatches-1 || sess.cool != 1 {
		t.Fatal("retry consumed incorrect chunk or pacing credit")
	}
}

func TestChunkPumpHardByteLimitBoundsBatch(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	s.ChunkEncodingBytes = 2 * chunkSnapshotUpperBound(s.dims[dimOverworld], nil)
	sess, _ := chunkPumpFixture(t, s, 4, false)
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
	})
	assertPumpBatch(t, readPumpQueue(t, sess), 2)
	if len(sess.pending) != 2 {
		t.Fatal("hard byte limit did not retain remaining columns")
	}
}

func TestChunkPumpRejectsOneSnapshotOverHardLimit(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	s.ChunkEncodingBytes = chunkSnapshotUpperBound(s.dims[dimOverworld], nil) - 1
	sess, _ := chunkPumpFixture(t, s, 1, false)
	var err error
	s.simulationAction(func() { err = s.pumpChunks(sess) })
	if err == nil || !sess.gone() || len(sess.pending) != 1 || len(sess.sent) != 0 || len(sess.sendCh) != 0 {
		t.Fatalf("impossible snapshot not rejected before batch admission: %v", err)
	}
}

func TestChunkPumpInitialSaturatedFallbackPreservesJoinOrder(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	s.ChunkEncodingJobs = initialChunkBatch
	s.ChunkEncodingBytes = int64(initialChunkBatch) * chunkSnapshotUpperBound(s.dims[dimOverworld], nil)
	sess, _ := chunkPumpFixture(t, s, initialChunkBatch, false)
	sess.opened, sess.rate, sess.burst = false, 0, 0
	sess.spawned.Store(false)
	reservation, ok := s.tryReserveChunkEncoding(initialChunkBatch, s.ChunkEncodingBytes)
	if !ok {
		t.Fatal("could not fill encoder quota")
	}
	defer reservation.Close()
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
		if err := sess.write(IDPlayUpdateAttributes, nil); err != nil {
			t.Fatal(err)
		}
	})
	packets := readPumpQueue(t, sess)
	assertPumpBatch(t, packets, initialChunkBatch)
	if len(packets) != initialChunkBatch+3 || packets[len(packets)-1].ID != IDPlayUpdateAttributes {
		t.Fatal("join continuation overtook initial chunk batch")
	}
	if jobs, bytes := chunkPoolUsage(s.chunkEncoder); jobs != initialChunkBatch || bytes != s.ChunkEncodingBytes {
		t.Fatal("synchronous fallback consumed worker snapshot quota")
	}
}

func blockedChunkEncoder(t *testing.T, s *Server) func() {
	t.Helper()
	s.ChunkEncodingWorkers, s.ChunkEncodingJobs, s.ChunkEncodingBytes = 1, 2, 2<<20
	r, ok := s.tryReserveChunkEncoding(1, 1)
	if !ok {
		t.Fatal("cannot reserve blocker")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	f := r.Submit(1, func() ([]byte, error) { close(started); <-release; return nil, nil })
	r.Close()
	<-started
	t.Cleanup(f.Cancel)
	return func() { once.Do(func() { close(release) }) }
}

func TestChunkPumpCapturedChunkPrecedesLaterBlockDeltaOnWire(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	release := blockedChunkEncoder(t, s)
	defer release()
	sess, client := chunkPumpFixture(t, s, 1, true)
	s.track(sess)
	t.Cleanup(func() { s.sessions.Delete(sess.id) })
	position := world.Pos{X: 0, Y: 0, Z: 0}
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
		s.world.SetBlock(position, world.Stone)
		s.broadcastBlockState(s.dims[dimOverworld], position, world.Stone)
	})
	// The encoder is still blocked, yet the mutation has completed on the owner.
	if s.world.GetBlock(position) != world.Stone {
		t.Fatal("owner did not progress while encoder blocked")
	}
	release()
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := protocol.NewReader(client)
	var packets []protocol.Packet
	for i := 0; i < 4; i++ {
		packet, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, packet)
	}
	assertPumpBatch(t, packets, 1)
	if old := chunkSnapshotStateAt(t, packets[1].Data, 0, 0, 0, MinY); old != blockState(world.Air) {
		t.Fatalf("captured chunk included later write: %d", old)
	}
	if packets[3].ID != IDPlayBlockUpdateCB {
		t.Fatalf("later delta packet = %#x", packets[3].ID)
	}
	delta := protocol.NewPacketReader(packets[3].Data)
	_, _, _, _ = delta.Position()
	if state, err := delta.VarInt(); err != nil || state != blockState(world.Stone) {
		t.Fatalf("later delta state=%d error=%v", state, err)
	}
}

func chunkSnapshotStateAt(t *testing.T, body []byte, x, y, z, minY int) int32 {
	t.Helper()
	r := protocol.NewPacketReader(body)
	_, _ = r.Int()
	_, _ = r.Int()
	maps, _ := r.VarInt()
	for i := int32(0); i < maps; i++ {
		_, _ = r.VarInt()
		n, _ := r.VarInt()
		if _, err := r.Read(int(n) * 8); err != nil {
			t.Fatal(err)
		}
	}
	length, _ := r.VarInt()
	data, err := r.Read(int(length))
	if err != nil {
		t.Fatal(err)
	}
	sections := protocol.NewPacketReader(data)
	for section := 0; section <= (y-minY)/16; section++ {
		_, _ = sections.Short()
		_, _ = sections.Short()
		states := decodeContainer(t, sections, 4096, 4)
		_ = decodeContainer(t, sections, 64, 1)
		if section == (y-minY)/16 {
			return states[((y-minY)&15)*256+(z&15)*16+(x&15)]
		}
	}
	t.Fatal("target section missing")
	return -1
}

func TestChunkPumpDimensionHopKeepsOldBatchBeforeRespawn(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	release := blockedChunkEncoder(t, s)
	defer release()
	sess, client := chunkPumpFixture(t, s, 1, true)
	s.track(sess)
	t.Cleanup(func() { s.sessions.Delete(sess.id) })
	destination := s.dims[dimNether]
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
		s.changeDimension(sess, destination, .5, 33, .5, 10)
		var marker protocol.Buffer
		marker.Long(123)
		if err := sess.write(IDPlayKeepAliveCB, marker.Bytes()); err != nil {
			t.Fatal(err)
		}
	})
	release()
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := protocol.NewReader(client)
	var packets []protocol.Packet
	respawn := -1
	for {
		packet, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, packet)
		if packet.ID == IDPlayRespawn {
			respawn = len(packets) - 1
		}
		if packet.ID == IDPlayChunkData && respawn >= 0 {
			t.Fatal("old dimension chunk completed after Respawn")
		}
		if packet.ID == IDPlayKeepAliveCB {
			break
		}
	}
	assertPumpBatch(t, packets, 1)
	if respawn < 3 {
		t.Fatalf("Respawn index=%d, old batch was not complete before dimension switch", respawn)
	}
	if state := chunkSnapshotStateAt(t, packets[1].Data, 0, 1, 0, MinY); state != blockState(world.Air) {
		t.Fatal("old snapshot read the player's new dimension")
	}
	awaitChunkPoolEmpty(t, s.chunkEncoder)
	s.simulationAction(s.chunkTickStep)
	packets = nil
	for i := 0; i < 4; i++ {
		packet, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, packet)
	}
	assertPumpBatch(t, packets, 2)
	if state := chunkSnapshotStateAt(t, packets[1].Data, 0, 1, 0, destination.minY); state != blockState(world.BlockStateMust("netherrack")) {
		t.Fatal("new dimension batch used old snapshot state")
	}
}

func TestChunkPumpDefaultLimitsAdmitMaximumVanillaBatch(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.stopChunkEncoder)
	sess, _ := chunkPumpFixture(t, s, maxChunkBatch, false)
	s.simulationAction(func() {
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
	})
	assertPumpBatch(t, readPumpQueue(t, sess), maxChunkBatch)
	if sess.gone() || len(sess.pending) != 0 || len(sess.sent) != maxChunkBatch {
		t.Fatal("default budgets rejected the maximum vanilla batch")
	}
	if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
		t.Fatalf("completed batch retains %d packets/%d output bytes", packets, bytes)
	}
}
