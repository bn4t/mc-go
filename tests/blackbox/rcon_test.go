package blackbox

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// rconPkt reads one RCON packet: length, request id, type, payload.
func rconPkt(t *testing.T, c net.Conn) (int32, int32, string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var head [4]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		t.Fatalf("rcon head: %v", err)
	}
	n := int32(binary.LittleEndian.Uint32(head[:]))
	body := make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatalf("rcon body: %v", err)
	}
	id := int32(binary.LittleEndian.Uint32(body[:4]))
	typ := int32(binary.LittleEndian.Uint32(body[4:8]))
	payload := string(body[8 : len(body)-2])
	return id, typ, payload
}

func rconSend(t *testing.T, c net.Conn, id, typ int32, payload string) {
	t.Helper()
	b := make([]byte, 0, len(payload)+14)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(payload)+10))
	b = binary.LittleEndian.AppendUint32(b, uint32(id))
	b = binary.LittleEndian.AppendUint32(b, uint32(typ))
	b = append(b, payload...)
	b = append(b, 0, 0)
	if _, err := c.Write(b); err != nil {
		t.Fatalf("rcon write: %v", err)
	}
}

// rconDial connects; the in-process listener may race the bind, so it
// retries briefly.
func rconDial(t *testing.T, addr, password string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("rcon dial %s: %v", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// normGameruleHelp sorts the rule list inside the /help gamerule line —
// vanilla iterates a HashMap so the order varies between runs.
var rconGameruleRe = regexp.MustCompile(`/gamerule \(([^)]*)\)`)

func normRconText(s string) string {
	return rconGameruleRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[len("/gamerule (") : len(m)-1]
		parts := strings.Split(inner, "|")
		sort.Strings(parts)
		return "/gamerule (" + strings.Join(parts, "|") + ")"
	})
}

// TestRconParity exercises the RCON console protocol on both servers:
// auth echo, -1 on failure and on unauthenticated packets, plain-text
// command replies, the 4096-byte response split, and the hex-formatted
// unknown-request answer.
func TestRconParity(t *testing.T) {
	const password = "hunter2"
	vRcon, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked")
	}
	mRcon, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked")
	}
	targets := requireBlackboxFiles(t,
		map[string]string{
			"enable-rcon":   "true",
			"rcon.port":     fmt.Sprint(vRcon),
			"rcon.password": password,
		},
		nil,
		func(s *server.Server) {
			s.RconAddr = fmt.Sprintf("127.0.0.1:%d", mRcon)
			s.RconPassword = password
		})
	defer targets.cleanup()

	run := func(addr string) [][3]string {
		var results [][3]string
		// connection 1: auth, commands, unknown types
		c := rconDial(t, addr, password)
		defer c.Close()
		rconSend(t, c, 1, 3, password)
		id, typ, pay := rconPkt(t, c)
		results = append(results, [3]string{fmt.Sprint(id), fmt.Sprint(typ), pay})

		for _, cmd := range []string{"list", "help", "notacommand", "say hello", "kick Q"} {
			rconSend(t, c, 2, 2, cmd)
			var text string
			var rt int32
			for {
				var rid int32
				var pay string
				rid, rt, pay = rconPkt(t, c)
				_ = rid
				text += pay
				if len(pay) < 4096 {
					break
				}
			}
			results = append(results, [3]string{"cmd:" + cmd, fmt.Sprint(rt), normRconText(text)})
		}
		// unknown request types
		for _, typ2 := range []int32{0, 99} {
			rconSend(t, c, 3, typ2, "x")
			id, typ, pay = rconPkt(t, c)
			results = append(results, [3]string{fmt.Sprintf("type%d", typ2), fmt.Sprint(id), pay})
		}
		c.Close()

		// connection 2: bad password then command (still open)
		c = rconDial(t, addr, password)
		rconSend(t, c, 7, 3, "wrong")
		id, typ, pay = rconPkt(t, c)
		results = append(results, [3]string{"badauth", fmt.Sprint(id), pay})
		rconSend(t, c, 8, 2, "list")
		id, typ, pay = rconPkt(t, c)
		results = append(results, [3]string{"postbadauth", fmt.Sprint(id), pay})
		c.Close()

		// connection 3: command before auth → -1, then auth works
		c = rconDial(t, addr, password)
		rconSend(t, c, 5, 2, "list")
		id, typ, pay = rconPkt(t, c)
		results = append(results, [3]string{"noauth", fmt.Sprint(id), pay})
		rconSend(t, c, 6, 3, password)
		id, typ, pay = rconPkt(t, c)
		results = append(results, [3]string{"lateauth", fmt.Sprint(id), pay})
		c.Close()
		return results
	}

	want := run(fmt.Sprintf("127.0.0.1:%d", vRcon))
	got := run(fmt.Sprintf("127.0.0.1:%d", mRcon))
	if len(want) != len(got) {
		t.Fatalf("vanilla %d results, mc-go %d", len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("step %d:\n  vanilla %q\n  mc-go   %q", i, want[i], got[i])
		}
	}
}
