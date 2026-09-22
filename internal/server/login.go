package server

import (
	"crypto/rand"
	"log"
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

	sess.setIdentity(name, offlineUUID(name))
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
	b.VarInt(0) // property count: none in offline mode
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
