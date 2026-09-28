package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"mc-go/internal/protocol"
)

// TestOnlineModeLogin drives the whole encrypted login stage: the
// hello's shape (empty server id, X.509 key, 4-byte challenge,
// shouldAuthenticate), the key exchange, the AES/CFB8 stream, and a
// stubbed hasJoined whose profile UUID + properties land in Login
// Success — the same end-to-end path vanilla runs against Mojang.
func TestOnlineModeLogin(t *testing.T) {
	profileID := "069a79f444e94726a5befca90e38aaf5"
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("username") != "AuthPlayer" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		sid := r.URL.Query().Get("serverId")
		if sid == "" || sid == "0" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   profileID,
			"name": "AuthPlayer",
			"properties": []map[string]string{
				{"name": "textures", "value": "dGV4dHVyZQ==", "signature": "c2ln"},
			},
		})
	}))
	defer stub.Close()

	s := New("unused")
	s.OnlineMode = true
	s.HasJoinedURL = stub.URL
	c, pr, pw := pipePair(t, s)
	defer c.Close()

	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String("AuthPlayer")
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}

	hello, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if hello.ID != IDLoginEncryptionReq {
		t.Fatalf("login resp id = %#x, want encryption_request 0x01", hello.ID)
	}
	hr := protocol.NewPacketReader(hello.Data)
	serverID, err := hr.String()
	if err != nil || serverID != "" {
		t.Fatalf("hello serverId = %q, err %v; want empty", serverID, err)
	}
	pubDER, err := hr.ByteArray()
	if err != nil || len(pubDER) == 0 {
		t.Fatalf("hello pubkey len = %d, err %v", len(pubDER), err)
	}
	pubAny, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatalf("hello pubkey not PKIX DER: %v", err)
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("hello pubkey type = %T, want RSA", pubAny)
	}
	if pub.N.BitLen() != 1024 {
		t.Fatalf("hello pubkey bits = %d, want 1024", pub.N.BitLen())
	}
	challenge, err := hr.ByteArray()
	if err != nil || len(challenge) != 4 {
		t.Fatalf("hello challenge len = %d, err %v; want 4", len(challenge), err)
	}
	shouldAuth, err := hr.Bool()
	if err != nil || !shouldAuth {
		t.Fatalf("hello shouldAuthenticate = %v, err %v; want true", shouldAuth, err)
	}
	if hr.Remaining() != 0 {
		t.Fatalf("hello has %d trailing bytes", hr.Remaining())
	}

	// The client proves the channel: RSA-encrypt the shared secret and
	// the challenge echo, then compute the session id exactly like
	// vanilla's Crypt.digestData.
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	secretEnc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, secret)
	if err != nil {
		t.Fatal(err)
	}
	challengeEnc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, challenge)
	if err != nil {
		t.Fatal(err)
	}
	var kb protocol.Buffer
	kb.VarInt(int32(len(secretEnc)))
	kb.Raw(secretEnc)
	kb.VarInt(int32(len(challengeEnc)))
	kb.Raw(challengeEnc)
	if err := pw.WritePacket(IDLoginKey, kb.Bytes()); err != nil {
		t.Fatal(err)
	}
	h := sha1.New()
	h.Write([]byte(""))
	h.Write(secret)
	h.Write(pubDER)
	wantServerID := new(big.Int).SetBytes(h.Sum(nil)).Text(16)

	// Both sides switch to AES/CFB8 right after the key packet.
	cr, cw, err := protocol.CipherStreams(c, c, secret)
	if err != nil {
		t.Fatal(err)
	}
	pr = protocol.NewReader(cr)
	pw = protocol.NewWriter(cw)

	// Compression is negotiated inside the encrypted stream.
	setComp, err := pr.ReadPacket()
	if err != nil || setComp.ID != IDLoginSetCompression {
		t.Fatalf("want encrypted set compression 0x03, got %#x err %v", setComp.ID, err)
	}
	threshold, err := protocol.NewPacketReader(setComp.Data).VarInt()
	if err != nil || threshold != CompressionThreshold {
		t.Fatalf("threshold = %d, err %v", threshold, err)
	}
	pr.SetThreshold(threshold)
	pw.SetThreshold(threshold)

	success, err := pr.ReadPacket()
	if err != nil || success.ID != IDLoginSuccess {
		t.Fatalf("want encrypted login success 0x02, got %#x err %v", success.ID, err)
	}
	sr := protocol.NewPacketReader(success.Data)
	uuid, err := sr.UUID()
	if err != nil {
		t.Fatal(err)
	}
	var wantUUID [16]byte
	for i := 0; i < 16; i++ {
		var v uint64
		if _, err := fmt.Sscanf(profileID[i*2:i*2+2], "%02x", &v); err != nil {
			t.Fatal(err)
		}
		wantUUID[i] = byte(v)
	}
	if uuid != wantUUID {
		t.Fatalf("success uuid = %x, want profile %x", uuid, wantUUID)
	}
	name, err := sr.String()
	if err != nil || name != "AuthPlayer" {
		t.Fatalf("success name = %q, err %v", name, err)
	}
	propCount, err := sr.VarInt()
	if err != nil || propCount != 1 {
		t.Fatalf("property count = %d, err %v; want 1", propCount, err)
	}
	pname, _ := sr.String()
	pval, _ := sr.String()
	hasSig, _ := sr.Bool()
	psig, _ := sr.String()
	if pname != "textures" || pval != "dGV4dHVyZQ==" || !hasSig || psig != "c2ln" {
		t.Fatalf("property = {%q %q sig=%q}, want textures/dGV4dHVyZQ==/c2ln", pname, pval, psig)
	}
	if wantServerID == "0" {
		t.Fatal("client-side serverId collapsed to 0")
	}
}

// TestOnlineModeBadChallenge pins the "Protocol error" disconnect a
// client gets when the challenge echo doesn't decrypt back to the
// hello's token.
func TestOnlineModeBadChallenge(t *testing.T) {
	s := New("unused")
	s.OnlineMode = true
	c, pr, pw := pipePair(t, s)
	defer c.Close()

	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String("AuthPlayer")
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	hello, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	hr := protocol.NewPacketReader(hello.Data)
	_, _ = hr.String()
	pubDER, err := hr.ByteArray()
	if err != nil {
		t.Fatal(err)
	}
	pubAny2, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := pubAny2.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("pubkey type = %T, want RSA", pubAny2)
	}
	// Wrong shared secret AND wrong challenge echo — vanilla throws
	// IllegalStateException("Protocol error") and drops the session.
	secretEnc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	challengeEnc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte{9, 9, 9, 9})
	if err != nil {
		t.Fatal(err)
	}
	var kb protocol.Buffer
	kb.VarInt(int32(len(secretEnc)))
	kb.Raw(secretEnc)
	kb.VarInt(int32(len(challengeEnc)))
	kb.Raw(challengeEnc)
	if err := pw.WritePacket(IDLoginKey, kb.Bytes()); err != nil {
		t.Fatal(err)
	}
	disconnect, err := pr.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if disconnect.ID != IDLoginDisconnect {
		t.Fatalf("want login disconnect 0x00, got %#x", disconnect.ID)
	}
}
