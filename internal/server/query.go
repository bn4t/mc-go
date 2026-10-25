package server

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// GameSpy4 query protocol ("enable-query"), measured against the
// vanilla 26.2 listener:
//   - magic FE FD, then a type byte and 4-byte ident
//   - type 9 hands back the ident plus a decimal challenge token
//   - type 0 with a valid token answers a basic stat; a request of
//     exactly 15 bytes (four trailing NULs) answers the full stat,
//     which vanilla caches and rewrites idents on for five seconds
//   - anything else is silently dropped

type queryChallenge struct {
	ident  [4]byte
	token  int32
	issued time.Time
}

// startQuery binds the UDP query listener when EnableQuery is set.
func (s *Server) startQuery() (func(), error) {
	if !s.EnableQuery {
		return func() {}, nil
	}
	addr := s.QueryAddr
	if addr == "" {
		// vanilla defaults query.port to the server port — the same
		// socket space, so reuse the main listen address.
		addr = s.Addr
	}
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("query listen: %w", err)
	}
	q := &queryListener{srv: s, conn: conn, challenges: map[string]*queryChallenge{}}
	done := make(chan struct{})
	go func() { defer close(done); q.serve() }()
	var once sync.Once
	return func() { once.Do(func() { _ = conn.Close() }); <-done }, nil
}

type queryListener struct {
	srv        *Server
	conn       net.PacketConn
	mu         sync.Mutex
	challenges map[string]*queryChallenge
}

func (q *queryListener) serve() {
	buf := make([]byte, 1460)
	for {
		n, addr, err := q.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		// Query responses are small metadata reads. Handle them inline so
		// packet bursts cannot create unbounded goroutines, and closing the
		// socket joins every outstanding response before runtime teardown.
		q.handle(pkt, addr)
	}
}

func (q *queryListener) handle(pkt []byte, addr net.Addr) {
	if len(pkt) <= 3 || pkt[0] != 0xfe || pkt[1] != 0xfd {
		return
	}
	switch pkt[2] {
	case 9:
		if len(pkt) < 7 {
			return
		}
		ch := &queryChallenge{token: rand.Int32N(16777216), issued: time.Now()}
		copy(ch.ident[:], pkt[3:7])
		key := addr.String()
		q.mu.Lock()
		q.challenges[key] = ch
		q.mu.Unlock()
		out := []byte{9}
		out = append(out, ch.ident[:]...)
		out = append(out, []byte(fmt.Sprint(ch.token))...)
		out = append(out, 0)
		_, _ = q.conn.WriteTo(out, addr)
	case 0:
		if len(pkt) < 11 {
			return
		}
		key := addr.String()
		q.mu.Lock()
		ch, ok := q.challenges[key]
		q.mu.Unlock()
		if !ok || binary.BigEndian.Uint32(pkt[7:11]) != uint32(ch.token) ||
			time.Since(ch.issued) > 30*time.Second {
			return
		}
		var out []byte
		if len(pkt) == 15 {
			out = q.srv.queryFullStat(ch.ident[:])
		} else {
			out = q.srv.queryBasicStat(ch.ident[:])
		}
		_, _ = q.conn.WriteTo(out, addr)
	}
}

func qstr(b []byte, s string) []byte {
	return append(append(b, s...), 0)
}

func (s *Server) queryBasicStat(ident []byte) []byte {
	b := []byte{0}
	b = append(b, ident...)
	b = qstr(b, s.MOTD)
	b = qstr(b, "SMP")
	b = qstr(b, s.queryWorldName())
	b = qstr(b, fmt.Sprint(s.OnlineCount()))
	b = qstr(b, fmt.Sprint(s.MaxPlayers))
	b = binary.BigEndian.AppendUint16(b, uint16(s.queryPort()))
	return qstr(b, s.queryHostIP())
}

func (s *Server) queryFullStat(ident []byte) []byte {
	b := []byte{0}
	b = append(b, ident...)
	b = qstr(b, "splitnum")
	b = append(b, 128, 0)
	for _, kv := range [][2]string{
		{"hostname", s.MOTD},
		{"gametype", "SMP"},
		{"game_id", "MINECRAFT"},
		{"version", "26.2"},
		{"plugins", ""},
		{"map", s.queryWorldName()},
		{"numplayers", fmt.Sprint(s.OnlineCount())},
		{"maxplayers", fmt.Sprint(s.MaxPlayers)},
		{"hostport", fmt.Sprint(s.queryPort())},
		{"hostip", s.queryHostIP()},
	} {
		b = qstr(qstr(b, kv[0]), kv[1])
	}
	b = append(b, 0, 1)
	b = qstr(b, "player_")
	b = append(b, 0)
	s.sessions.Range(func(_, v any) bool {
		b = qstr(b, v.(*Session).getName())
		return true
	})
	return append(b, 0)
}

func (s *Server) queryWorldName() string {
	if s.LevelName != "" {
		return s.LevelName
	}
	return "world"
}

func (s *Server) queryPort() int {
	_, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return 0
	}
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}

func (s *Server) queryHostIP() string {
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil || host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
}
