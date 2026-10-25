package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
)

// handleLogin runs the offline-mode login stage (no encryption, no
// compression yet): Login Start -> Login Success -> Login Acknowledged.
// On success it advances the session into Configuration.
func (s *Server) handleLogin(sess *Session) {
	p, err := sess.pr.ReadPacket()
	if err != nil || p.ID != IDLoginStart {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	name, err := r.String()
	if err != nil || name == "" || len(name) > 16 {
		return
	}
	if _, err := r.UUID(); err != nil {
		return
	}
	// The handshake protocol selects the packet-ID table. Unknown
	// versions are rejected like vanilla rejects outdated clients.
	ver, ok := NegotiateVersion(sess.clientProto.Load())
	if !ok {
		s.sendLoginDisconnect(sess, `{"text":"Outdated client! This server speaks `+LatestVersion().Name+`"}`)
		return
	}
	sess.ver = ver
	if s.OnlineCount() >= s.MaxPlayers {
		s.sendLoginDisconnect(sess, `{"text":"Server is full"}`)
		return
	}

	// Online mode runs the vanilla encryption handshake first: the
	// hello packet (empty server id, X.509 key, 4-byte challenge,
	// shouldAuthenticate), the key response proving the client owns
	// the channel, the AES/CFB8 stream, then the session-server
	// lookup whose verified profile replaces the offline identity.
	if s.OnlineMode {
		uuid, props, ok := s.loginEncrypted(sess, name)
		if !ok {
			return
		}
		sess.setIdentity(name, uuid)
		sess.profileProps = props
	} else {
		sess.setIdentity(name, offlineUUID(name))
	}
	// Bans and ip-bans reject at login like vanilla: the disconnect is
	// the translated reason, the typed text when one was given.
	if reason, banned := s.bannedReason(name, sess); banned {
		s.sendLoginDisconnect(sess, reason)
		return
	}
	if s.whitelistOn.Load() && !s.onWhitelist(name) {
		s.sendLoginDisconnect(sess,
			`{"translate":"multiplayer.disconnect.not_whitelisted"}`)
		return
	}
	s.online.Add(1)
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerLogin, map[string]string{"name": name})
	}
	log.Printf("%s logged in from %s", name, sess.conn.RemoteAddr())

	// Negotiate compression like vanilla (default threshold 256):
	// Set Compression goes out raw, everything after is framed. The
	// writer threshold switch is queued behind the packet so ordering
	// holds with the async outbound queue; the reader switches now
	// because the client's next packet (Login Ack) is already framed.
	var sc protocol.Buffer
	sc.VarInt(CompressionThreshold)
	if err := sess.write(sess.ids().LoginSetCompression, sc.Bytes()); err != nil {
		return
	}
	sess.setWriteThreshold(CompressionThreshold)
	sess.pr.SetThreshold(CompressionThreshold)

	var b protocol.Buffer
	b.UUID(sess.uuid)
	b.String(name)
	if s.OnlineMode {
		b.VarInt(int32(len(sess.profileProps)))
		for _, pp := range sess.profileProps {
			b.String(pp.name)
			b.String(pp.value)
			if pp.signature != "" {
				b.Bool(true)
				b.String(pp.signature)
			} else {
				b.Bool(false)
			}
		}
	} else {
		b.VarInt(0) // property count: none in offline mode
	}
	// The session id trails the properties. The real 26.2 client
	// decodes a UUID here and kicks without it.
	var sid [16]byte
	_, _ = rand.Read(sid[:])
	sid[6] = (sid[6] & 0x0f) | 0x40
	sid[8] = (sid[8] & 0x3f) | 0x80
	b.UUID(sid)
	if err := sess.write(sess.ids().LoginSuccess, b.Bytes()); err != nil {
		return
	}

	ack, err := sess.pr.ReadPacket()
	if err != nil || ack.ID != IDLoginAcknowledged {
		return
	}
	sess.state.Store(StateConfiguration)
	s.handleConfig(sess)
}

func (s *Server) sendLoginDisconnect(sess *Session, reason string) {
	var b protocol.Buffer
	b.String(reason)
	_ = sess.write(sess.ids().LoginDisconnect, b.Bytes())
	sess.flush(2 * time.Second)
}

// profileProperty is one GameProfile properties entry — the Mojang
// texture blob is the live user.
type profileProperty struct {
	name, value, signature string
}

const mojangHasJoined = "https://sessionserver.mojang.com/session/minecraft/hasJoined"

var (
	loginKeyMu   sync.Mutex
	loginKeyPair *rsa.PrivateKey
	loginKeyDER  []byte
)

// loginKey lazily generates the server's RSA keypair the way vanilla's
// MinecraftServer.getKeyPair does — 1024-bit, X.509-encoded public key.
func (s *Server) loginKey() (*rsa.PrivateKey, []byte, error) {
	loginKeyMu.Lock()
	defer loginKeyMu.Unlock()
	if loginKeyPair == nil {
		k, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return nil, nil, err
		}
		loginKeyPair = k
		// PublicKey.getEncoded() is the X.509 SubjectPublicKeyInfo
		// (PKIX) encoding, not raw PKCS#1 — the differential hello
		// test caught a DER-format mismatch here.
		loginKeyDER, _ = x509.MarshalPKIXPublicKey(&k.PublicKey)
	}
	return loginKeyPair, loginKeyDER, nil
}

// loginEncrypted runs the online-mode handshake after Login Start:
// encryption_request hello, the ServerboundKey response's challenge
// check, the AES/CFB8 stream arm, and the hasJoined profile lookup.
// Returns the verified profile's UUID + properties; the session is
// already disconnected on false.
func (s *Server) loginEncrypted(sess *Session, name string) ([16]byte, []profileProperty, bool) {
	priv, pubDER, err := s.loginKey()
	if err != nil {
		return [16]byte{}, nil, false
	}
	// The challenge is Ints.toByteArray(random.nextInt()) — four
	// big-endian bytes the client echoes back RSA-encrypted.
	var challenge [4]byte
	_, _ = rand.Read(challenge[:])

	var hello protocol.Buffer
	hello.String("")
	hello.VarInt(int32(len(pubDER)))
	hello.Raw(pubDER)
	hello.VarInt(int32(len(challenge)))
	hello.Raw(challenge[:])
	hello.Bool(true) // shouldAuthenticate
	if err := sess.write(IDLoginEncryptionReq, hello.Bytes()); err != nil {
		return [16]byte{}, nil, false
	}

	p, err := sess.pr.ReadPacket()
	if err != nil || p.ID != IDLoginKey {
		return [16]byte{}, nil, false
	}
	r := protocol.NewPacketReader(p.Data)
	secretEnc, err := r.ByteArray()
	if err != nil {
		return [16]byte{}, nil, false
	}
	challengeEnc, err := r.ByteArray()
	if err != nil {
		return [16]byte{}, nil, false
	}
	// ServerboundKeyPacket.isChallengeValid: RSA-decrypt the echo and
	// compare; a mismatch is vanilla's "Protocol error" disconnect.
	gotChallenge, err := rsa.DecryptPKCS1v15(rand.Reader, priv, challengeEnc)
	if err != nil || string(gotChallenge) != string(challenge[:]) {
		s.sendLoginDisconnect(sess, `{"text":"Protocol error"}`)
		return [16]byte{}, nil, false
	}
	secret, err := rsa.DecryptPKCS1v15(rand.Reader, priv, secretEnc)
	if err != nil || len(secret) != 16 {
		s.sendLoginDisconnect(sess, `{"text":"Protocol error"}`)
		return [16]byte{}, nil, false
	}
	// Crypt.digestData(serverId, publicKey, secretKey): SHA-1 of the
	// three concatenated byte arrays, rendered BigInteger-signed hex.
	h := sha1.New()
	h.Write([]byte(""))
	h.Write(secret)
	h.Write(pubDER)
	serverID := new(big.Int).SetBytes(h.Sum(nil)).Text(16)

	// Connection.setEncryptionKey: both directions switch to AES/CFB8
	// immediately after the key packet — compression and login success
	// then travel inside the encrypted stream.
	cr, cw, err := protocol.CipherStreams(sess.conn, sess.conn, secret)
	if err != nil {
		return [16]byte{}, nil, false
	}
	sess.pr = protocol.NewReader(cr)
	sess.wmu.Lock()
	sess.pw = protocol.NewWriter(cw)
	sess.wmu.Unlock()

	uuid, props, ok := s.verifyProfile(sess, name, serverID)
	if !ok {
		return [16]byte{}, nil, false
	}
	return uuid, props, true
}

// verifyProfile performs the session-server hasJoined lookup and maps
// the returned profile onto the session's identity. A failed lookup
// disconnects like vanilla's unverified_username kick.
func (s *Server) verifyProfile(sess *Session, name, serverID string) ([16]byte, []profileProperty, bool) {
	base := s.HasJoinedURL
	if base == "" {
		base = mojangHasJoined
	}
	url := fmt.Sprintf("%s?username=%s&serverId=%s", base, name, serverID)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		s.sendLoginDisconnect(sess, `{"translate":"multiplayer.disconnect.unverified_username"}`)
		return [16]byte{}, nil, false
	}
	defer resp.Body.Close()
	var prof struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Properties []struct {
			Name      string `json:"name"`
			Value     string `json:"value"`
			Signature string `json:"signature"`
		} `json:"properties"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || json.Unmarshal(body, &prof) != nil || prof.ID == "" {
		s.sendLoginDisconnect(sess, `{"translate":"multiplayer.disconnect.unverified_username"}`)
		return [16]byte{}, nil, false
	}
	u, err := parseProfileUUID(prof.ID)
	if err != nil {
		s.sendLoginDisconnect(sess, `{"translate":"multiplayer.disconnect.unverified_username"}`)
		return [16]byte{}, nil, false
	}
	props := make([]profileProperty, 0, len(prof.Properties))
	for _, pp := range prof.Properties {
		props = append(props, profileProperty{name: pp.Name, value: pp.Value, signature: pp.Signature})
	}
	return u, props, true
}

// parseProfileUUID reads a hasJoined id — Mojang writes the UUID
// without dashes.
func parseProfileUUID(id string) ([16]byte, error) {
	var u [16]byte
	if len(id) != 32 {
		return u, fmt.Errorf("profile id %q", id)
	}
	for i := 0; i < 16; i++ {
		var v uint64
		if _, err := fmt.Sscanf(id[i*2:i*2+2], "%02x", &v); err != nil {
			return u, err
		}
		u[i] = byte(v)
	}
	return u, nil
}
