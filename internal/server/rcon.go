package server

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// RCON (Source-style remote console), matching the vanilla 26.2
// listener:
//   - wire: little-endian int32 length (not counting itself), int32
//     request id, int32 type, payload, two trailing NULs
//   - type 3 authenticates (payload = password); success echoes the id
//     in a type-2 packet, failure answers id -1 — and every later
//     packet on an unauthenticated connection also answers -1; the
//     connection stays open either way
//   - type 2 runs the payload as a command at level 4; the reply is
//     the flat text of every feedback message, concatenated, sent in
//     type-0 packets of at most 4096 payload bytes
//   - any other type answers "Unknown request %x" (hex digits)

const (
	rconTypeCommand = 2
	rconTypeAuth    = 3
	rconMaxPayload  = 4096
)

// startRcon binds the RCON listener when a password is configured. It
// returns a stop func closing the listener and every open connection.
func (s *Server) startRcon() (func(), error) {
	if s.RconPassword == "" {
		return func() {}, nil
	}
	addr := s.RconAddr
	if addr == "" {
		addr = ":25575"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("rcon listen: %w", err)
	}
	r := &rconListener{srv: s, ln: ln, stop: make(chan struct{})}
	go r.accept()
	return r.close, nil
}

type rconListener struct {
	srv   *Server
	ln    net.Listener
	stop  chan struct{}
	conns sync.Map
}

func (r *rconListener) accept() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.conns.Store(c, struct{}{})
		go r.serve(c)
	}
}

func (r *rconListener) close() {
	close(r.stop)
	_ = r.ln.Close()
	r.conns.Range(func(k, _ any) bool {
		_ = k.(net.Conn).Close()
		return true
	})
}

func (r *rconListener) serve(c net.Conn) {
	defer func() {
		r.conns.Delete(c)
		c.Close()
	}()
	authed := false
	head := make([]byte, 4)
	for {
		if _, err := io.ReadFull(c, head); err != nil {
			return
		}
		n := int(int32(binary.LittleEndian.Uint32(head)))
		if n < 10 || n > 4096+64 {
			return // vanilla drops malformed lengths silently
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c, body); err != nil {
			return
		}
		id := int32(binary.LittleEndian.Uint32(body[:4]))
		typ := int32(binary.LittleEndian.Uint32(body[4:8]))
		payload := body[8:]
		if len(payload) >= 2 {
			payload = payload[:len(payload)-2]
		}
		switch typ {
		case rconTypeAuth:
			if string(payload) == r.srv.RconPassword {
				authed = true
				r.write(c, id, rconTypeCommand, "")
			} else {
				r.write(c, -1, rconTypeCommand, "")
			}
		case rconTypeCommand:
			if !authed {
				r.write(c, -1, rconTypeCommand, "")
				continue
			}
			r.write(c, id, 0, r.srv.rconRun(string(payload)))
		default:
			r.write(c, id, 0, fmt.Sprintf("Unknown request %x", typ))
		}
	}
}

func (r *rconListener) write(c net.Conn, id, typ int32, payload string) {
	// Vanilla splits long answers into 4096-byte chunks.
	for {
		chunk := payload
		if len(chunk) > rconMaxPayload {
			chunk = payload[:rconMaxPayload]
		}
		var pkt [4 + 4 + 4]byte
		binary.LittleEndian.PutUint32(pkt[:4], uint32(len(chunk)+10))
		binary.LittleEndian.PutUint32(pkt[4:8], uint32(id))
		binary.LittleEndian.PutUint32(pkt[8:12], uint32(typ))
		if _, err := c.Write(append(pkt[:], []byte(chunk)...)); err != nil {
			return
		}
		if _, err := c.Write([]byte{0, 0}); err != nil {
			return
		}
		if len(chunk) == len(payload) {
			return
		}
		payload = payload[rconMaxPayload:]
	}
}

// rconRun executes a command line as the RCON source and returns the
// flat text of the reply — every feedback message concatenated with no
// separator, parse-error context appended like a second message.
func (s *Server) rconRun(line string) string {
	fields := splitCommandArgs(line)
	if len(fields) == 0 {
		return ""
	}
	r, ok := s.runCommandAt("RCON", 4, fields[0], fields[1:])
	if !ok {
		r = reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	var b strings.Builder
	if r.styled {
		b.WriteString(r.comp.plain())
		for _, c := range r.extra {
			b.WriteString(c.plain())
		}
	} else {
		b.WriteString(r.text)
	}
	// RconConsoleSource is still a CommandSourceStack: a reply flagged
	// broadcastToOps echoes to every op online as "[RCON: ...]".
	if r.opsEcho {
		s.sendOpsEchoNamed(nil, component{text: "RCON"}, r.comp)
	}
	for i, c := range r.extra {
		if i < len(r.extraEcho) && r.extraEcho[i] {
			s.sendOpsEchoNamed(nil, component{text: "RCON"}, c)
		}
	}
	if r.fail != nil {
		b.WriteString(errorContext(line, r.fail.cursor(line)).plain())
	}
	return b.String()
}
