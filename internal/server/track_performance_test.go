package server

import (
	"fmt"
	"testing"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// trackingFixture has no tick or network goroutines. All entity and
// session state is owned by the caller, as in a single simulation pass.
func trackingFixture(entities, players int) (*Server, *Session) {
	s := &Server{
		entities: NewManager(), ViewDistance: 10,
		dims: map[string]*dimension{dimOverworld: {name: dimOverworld}},
	}
	var observer *Session
	for i := 0; i < players; i++ {
		e := s.entities.Spawn(&Entity{Type: EntityTypePlayer, X: float64(i%16) * 32, Y: 65, Z: float64(i/16) * 32})
		sess := &Session{server: s, ver: LatestVersion(), done: make(chan struct{}), sendCh: make(chan sendPkt, 64)}
		sess.entityID.Store(e.ID)
		sess.state.Store(StatePlay)
		sess.setPos(e.X, e.Y, e.Z, 0, 0, true)
		s.sessions.Store(i, sess)
		if observer == nil {
			observer = sess
		}
	}
	for i := players; i < entities; i++ {
		s.entities.Spawn(&Entity{Type: EntityTypePig, X: float64(i%128)*16 + 1, Y: 65, Z: float64(i/128)*16 + 1})
	}
	observer.sent = make(map[[2]int]bool)
	for x := -12; x <= 12; x++ {
		for z := -12; z <= 12; z++ {
			observer.sent[[2]int{x, z}] = true
		}
	}
	// Start with stable pairings, so benchmarks measure reevaluation
	// including distance/chunk predicates, without packet/socket costs.
	for _, e := range s.entities.byID {
		if s.pairable(observer, e) {
			observer.setTracked(e.ID, true)
		}
	}
	return s, observer
}

func TestRetrackSessionLivePositions(t *testing.T) {
	s, observer := trackingFixture(3, 2)
	player, _ := s.entities.GetPtr(2)
	mob, _ := s.entities.GetPtr(3)
	var other *Session
	s.sessions.Range(func(_, v any) bool {
		if p := v.(*Session); p != observer {
			other = p
		}
		return true
	})
	// Stored player coordinates may lag far behind the live session.
	player.X = 4096
	s.retrackSession(observer)
	if !observer.tracksEntity(player.ID) || !observer.tracksEntity(mob.ID) {
		t.Fatal("live nearby player and ordinary entity must remain paired")
	}
	if observer.tracksEntity(observer.entityID.Load()) {
		t.Fatal("observer paired with itself")
	}
	if player.X != 4096 {
		t.Fatal("tracking pass mutated stored player coordinates")
	}
	other.setPos(4096, 65, 0, 0, 0, true)
	player.X = 32 // stale stored position says nearby; live session says far
	s.retrackSession(observer)
	if observer.tracksEntity(player.ID) {
		t.Fatal("player outside live range remains paired")
	}
	var pkt sendPkt
	select {
	case pkt = <-observer.sendCh:
	default:
		t.Fatal("unpair did not emit remove packet")
	}
	if pkt.id != observer.ids().PlayRemoveEntity {
		t.Fatalf("unpair packet id = %d", pkt.id)
	}
	other.setPos(32, 65, 0, 0, 0, true)
	player.X = 4096
	s.retrackSession(observer)
	if !observer.tracksEntity(player.ID) {
		t.Fatal("player returning to live range was not paired")
	}
	for len(observer.sendCh) > 0 {
		pkt = <-observer.sendCh
		if pkt.id != observer.ids().PlaySpawnEntity {
			continue
		}
		r := protocol.NewPacketReader(pkt.data)
		id, _ := r.VarInt()
		_, _ = r.UUID()
		_, _ = r.VarInt()
		x, err := r.Double()
		if err != nil || id != player.ID || x != 32 {
			t.Fatalf("spawn player id=%d x=%v err=%v", id, x, err)
		}
		return
	}
	t.Fatal("returning player did not emit spawn packet")
}

// retrackSessionScanBaseline freezes the previous per-entity session
// scan for an apples-to-apples microbenchmark with identical predicates.
func retrackSessionScanBaseline(s *Server, sess *Session) {
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		ec := e
		s.sessions.Range(func(_, v any) bool {
			os := v.(*Session)
			if os.entityID.Load() == e.ID {
				x, y, z, _, _, _ := os.Pos()
				ec = &Entity{}
				*ec = *e
				ec.X, ec.Y, ec.Z = x, y, z
			}
			return true
		})
		s.evalPair(sess, ec)
	}
}

func BenchmarkRetrackSession(b *testing.B) {
	for _, scale := range [][2]int{{128, 8}, {1024, 64}, {8192, 256}} {
		b.Run(fmt.Sprintf("entities=%d/players=%d", scale[0], scale[1]), func(b *testing.B) {
			for _, impl := range []struct {
				name string
				run  func(*Server, *Session)
			}{
				{"scan-baseline", retrackSessionScanBaseline},
				{"lookup", (*Server).retrackSession},
			} {
				b.Run(impl.name, func(b *testing.B) {
					s, observer := trackingFixture(scale[0], scale[1])
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						impl.run(s, observer)
					}
					b.StopTimer()
					if len(observer.sendCh) != 0 {
						b.Fatal("steady-state benchmark unexpectedly emitted packets")
					}
				})
			}
		})
	}
}

var benchmarkChunkBytes []byte
var benchmarkWorldBlock world.Block

func BenchmarkTrackingWorldRead(b *testing.B) {
	w := world.New(event.New(), 8)
	b.Cleanup(w.Stop)
	for i := 0; i < 4096; i++ {
		w.SetBlock(world.Pos{X: i % 64, Y: 65, Z: i / 64}, world.Stone)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkWorldBlock = w.GetBlock(world.Pos{X: i % 64, Y: 65, Z: (i / 64) % 64})
	}
}

func BenchmarkTrackingChunkEncoding(b *testing.B) {
	w := world.New(event.New(), 8)
	b.Cleanup(w.Stop)
	for y := 0; y < 32; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if (x+y+z)%3 == 0 {
					w.SetBlock(world.Pos{X: x, Y: y, Z: z}, world.Stone)
				}
			}
		}
	}
	d := testOverworld(w)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkChunkBytes = encodeChunk(d, 0, 0, nil)
	}
	b.StopTimer()
	b.ReportMetric(float64(len(benchmarkChunkBytes)), "packet-bytes")
	if len(benchmarkChunkBytes) == 0 {
		b.Fatal("empty chunk packet")
	}
}
