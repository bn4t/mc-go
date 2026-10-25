// Vanilla source: net/minecraft/server/network/
// ServerLoginPacketListenerImpl (tick → ClientboundHelloPacket) and
// net/minecraft/network/protocol/login/ClientboundHelloPacket.
// Measurement (javap on server-26.2.jar): with online-mode=true the
// login stage answers Login Start with encryption_request 0x01 whose
// body is writeUtf("") + writeByteArray(PKIX pubkey) +
// writeByteArray(Ints.toByteArray(random.nextInt()) — four bytes) +
// writeBoolean(true). The keypair is RSA-1024
// (MinecraftServer.getKeyPair). mc-go's Server.OnlineMode reproduces
// the same hello, then the key/challenge check, the AES/CFB8 stream,
// and the hasJoined profile lookup the internal suite drives
// end-to-end against a stub session server.
package blackbox

import (
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"net"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// helloShape is the encryption_request body parsed into the parts a
// differential test can compare — the key bytes and challenge are
// random per connection, so parity is the shape, not the contents.
type helloShape struct {
	serverID    string
	pubDER      []byte
	challenge   []byte
	shouldAuth  bool
	trailing    int
	keyBitLen   int
	keyParseErr error
}

func readHelloShape(t *testing.T, tag string, pr *protocol.Reader) helloShape {
	t.Helper()
	p, err := pr.ReadPacket()
	if err != nil {
		t.Fatalf("%s: read encryption_request: %v", tag, err)
	}
	if p.ID != 0x01 {
		t.Fatalf("%s: login resp id = %#x, want encryption_request 0x01", tag, p.ID)
	}
	r := protocol.NewPacketReader(p.Data)
	var h helloShape
	if h.serverID, err = r.String(); err != nil {
		t.Fatalf("%s: hello serverId: %v", tag, err)
	}
	if h.pubDER, err = r.ByteArray(); err != nil {
		t.Fatalf("%s: hello pubkey: %v", tag, err)
	}
	if h.challenge, err = r.ByteArray(); err != nil {
		t.Fatalf("%s: hello challenge: %v", tag, err)
	}
	if h.shouldAuth, err = r.Bool(); err != nil {
		t.Fatalf("%s: hello shouldAuthenticate: %v", tag, err)
	}
	h.trailing = r.Remaining()
	pubAny, err := x509.ParsePKIXPublicKey(h.pubDER)
	h.keyParseErr = err
	if err == nil {
		if rsaPub, ok := pubAny.(*rsa.PublicKey); ok {
			h.keyBitLen = rsaPub.N.BitLen()
		} else {
			h.keyParseErr = fmt.Errorf("pubkey type %T, want RSA", pubAny)
		}
	}
	return h
}

// TestEncryptionHelloParity boots both servers with online-mode=true
// and compares the encryption_request that answers Login Start —
// field order, byte-array framing, the 4-byte challenge, the
// shouldAuthenticate flag, and the RSA-1024 key size. The Mojang
// lookup itself is out of scope here (the real session server needs a
// paid account); the stubbed end-to-end path is pinned in the
// internal suite.
func TestEncryptionHelloParity(t *testing.T) {
	tg := requireBlackboxCfg(t,
		map[string]string{"online-mode": "true"},
		func(s *server.Server) { s.OnlineMode = true })
	defer tg.cleanup()

	connect := func(tag, addr string) (net.Conn, helloShape) {
		c, pr, _ := loginStart(t, addr, "ParityProbe")
		return c, readHelloShape(t, tag, pr)
	}
	vc, vShape := connect("vanilla", tg.vanilla)
	defer vc.Close()
	mc, mShape := connect("mc-go", tg.mcgo)
	defer mc.Close()

	if vShape.serverID != "" || mShape.serverID != "" {
		t.Fatalf("serverId: vanilla=%q mc-go=%q, want empty", vShape.serverID, mShape.serverID)
	}
	if vShape.keyParseErr != nil {
		t.Fatalf("vanilla pubkey not RSA PKIX DER: %v", vShape.keyParseErr)
	}
	if mShape.keyParseErr != nil {
		t.Fatalf("mc-go pubkey not RSA PKIX DER: %v", mShape.keyParseErr)
	}
	if vShape.keyBitLen != 1024 || mShape.keyBitLen != vShape.keyBitLen {
		t.Fatalf("pubkey bits: vanilla=%d mc-go=%d, want 1024", vShape.keyBitLen, mShape.keyBitLen)
	}
	if len(vShape.challenge) != 4 || len(mShape.challenge) != len(vShape.challenge) {
		t.Fatalf("challenge len: vanilla=%d mc-go=%d, want 4", len(vShape.challenge), len(mShape.challenge))
	}
	if !vShape.shouldAuth || mShape.shouldAuth != vShape.shouldAuth {
		t.Fatalf("shouldAuthenticate: vanilla=%v mc-go=%v, want true", vShape.shouldAuth, mShape.shouldAuth)
	}
	if vShape.trailing != 0 || mShape.trailing != 0 {
		t.Fatalf("hello trailing bytes: vanilla=%d mc-go=%d", vShape.trailing, mShape.trailing)
	}
}
