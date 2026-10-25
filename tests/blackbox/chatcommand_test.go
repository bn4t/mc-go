package blackbox

import (
	"bytes"
	"testing"
	"time"
)

// TestSayCommandParity pins /say's wire shape: vanilla answers with a
// Disguised Chat packet carrying the literal message, the say_command
// chat-type binding, and the sender's decorated display name
// (suggest-command click, insertion, show_entity hover). Both servers
// derive the same offline UUID for a name, so the packet bodies are
// compared byte for byte.
func TestSayCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Sayer"),
	}, nil)
	defer targets.cleanup()

	bodyFor := func(tag, addr string) []byte {
		c, pr, pw := joinPlayBlackbox(t, addr, "Sayer")
		defer c.Close()
		drainToJoinBlackbox(t, tag, pr)
		_ = c.SetDeadline(time.Now().Add(20 * time.Second))
		sendCommand(t, pw, "say hello there")
		end := time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			// Disguised chat is 0x21 on the vanilla wire; mc-go's own
			// table uses PlayDisguisedChat.
			if p.ID == 0x21 {
				return p.Data
			}
		}
		return nil
	}

	v := bodyFor("vanilla", targets.vanilla)
	g := bodyFor("mc-go", targets.mcgo)
	if v == nil {
		t.Fatal("vanilla emitted no disguised_chat for /say")
	}
	if g == nil {
		t.Fatal("mc-go emitted no disguised_chat for /say")
	}
	if !bytes.Equal(v, g) {
		t.Fatalf("say packet mismatch\nvanilla: % x\nmc-go:   % x", v, g)
	}
}

// TestMsgCommandParity pins /msg's whisper pair: the target gets
// msg_command_incoming and the sender msg_command_outgoing with the
// target's decorated name.
func TestMsgCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Sender"),
	}, nil)
	defer targets.cleanup()

	type pair struct{ incoming, outgoing []byte }
	run := func(tag, addr string) pair {
		recv, rpr, _ := joinPlayBlackbox(t, addr, "Receiver")
		defer recv.Close()
		drainToJoinBlackbox(t, tag, rpr)
		snd, spr, spw := joinPlayBlackbox(t, addr, "Sender")
		defer snd.Close()
		drainToJoinBlackbox(t, tag+"2", spr)
		_ = recv.SetDeadline(time.Now().Add(20 * time.Second))
		_ = snd.SetDeadline(time.Now().Add(20 * time.Second))
		sendCommand(t, spw, "msg Receiver hi there")
		var out pair
		end := time.Now().Add(5 * time.Second)
		for out.incoming == nil && time.Now().Before(end) {
			p, err := rpr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == 0x21 {
				out.incoming = p.Data
			}
		}
		for out.outgoing == nil && time.Now().Before(end) {
			p, err := spr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == 0x21 {
				out.outgoing = p.Data
			}
		}
		return out
	}

	v := run("vanilla", targets.vanilla)
	g := run("mc-go", targets.mcgo)
	if v.incoming == nil || v.outgoing == nil {
		t.Fatalf("vanilla whisper pair missing: %+v", v)
	}
	if !bytes.Equal(v.incoming, g.incoming) {
		t.Fatalf("incoming whisper mismatch\nvanilla: % x\nmc-go:   % x", v.incoming, g.incoming)
	}
	if !bytes.Equal(v.outgoing, g.outgoing) {
		t.Fatalf("outgoing whisper mismatch\nvanilla: % x\nmc-go:   % x", v.outgoing, g.outgoing)
	}
}
