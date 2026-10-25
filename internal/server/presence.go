package server

import (
	"time"

	"mc-go/internal/protocol"
)

// Tab-list action bits, clean-room from the public 26.1.2 spec:
// enum order ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE,
// UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME,
// UPDATE_LIST_PRIORITY, UPDATE_HAT.
const (
	infoAdd      = 0x01
	infoChat     = 0x02
	infoGamemode = 0x04
	infoListed   = 0x08
	infoLatency  = 0x10
	infoName     = 0x20
	infoPriority = 0x40
	infoHat      = 0x80
)

// infoActions is the full presence set vanilla sends: identity, chat
// key state, gamemode, listed flag, ping, display name, priority, hat.
const infoActions = infoAdd | infoChat | infoGamemode | infoListed |
	infoLatency | infoName | infoPriority | infoHat

// playerEntry is one tab-list row.
type playerEntry struct {
	uuid     [16]byte
	name     string
	gamemode int32
	listed   bool
	latency  int32
}

// encodePlayerInfoGamemode builds a gamemode-only Player Info Update:
// just the UPDATE_GAME_MODE bit, then per entry UUID plus gamemode.
// Vanilla sends one of these to every player, target included, when
// someone's mode changes.
func encodePlayerInfoGamemode(uuid [16]byte, gamemode int32) []byte {
	var b protocol.Buffer
	b.Byte(infoGamemode)
	b.VarInt(1)
	b.UUID(uuid)
	b.VarInt(gamemode)
	return b.Bytes()
}

// broadcastPlayerInfoGamemode reports a mode change to every
// play-stage session, the changed player included: the differential
// test caught the target's own copy on both self and remote switches.
func (s *Server) broadcastPlayerInfoGamemode(sess *Session) {
	body := encodePlayerInfoGamemode(sess.getUUID(), int32(sess.mode()))
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay {
			return true
		}
		_ = other.write(other.ids().PlayPlayerInfoUpd, body)
		return true
	})
}

// encodePlayerInfoAdd builds a Player Info Update body: Byte actions,
// VarInt entry count, then per entry UUID + name + empty properties +
// gamemode + listed + latency, in action-enum order.
func encodePlayerInfoAdd(entries []playerEntry) []byte {
	var b protocol.Buffer
	b.Byte(infoActions)
	b.VarInt(int32(len(entries)))
	for _, e := range entries {
		b.UUID(e.uuid)
		b.String(e.name)
		b.VarInt(0)   // properties: none in offline mode
		b.Bool(false) // initialize chat: no signature data
		b.VarInt(e.gamemode)
		b.Bool(e.listed)
		b.VarInt(e.latency)
		b.Bool(false) // no display-name override
		b.VarInt(0)   // list priority
		b.Bool(false) // hat layer visible
	}
	return b.Bytes()
}

// encodePlayerInfoLatency builds a Player Info Update body carrying
// only the update-latency action (0x10).
func encodePlayerInfoLatency(entries []playerEntry) []byte {
	var b protocol.Buffer
	b.Byte(infoLatency)
	b.VarInt(int32(len(entries)))
	for _, e := range entries {
		b.UUID(e.uuid)
		b.VarInt(e.latency)
	}
	return b.Bytes()
}

// broadcastPlayerLatency announces one player's measured ping to every
// play-stage session (including the subject, like vanilla).
func (s *Server) broadcastPlayerLatency(sess *Session) {
	if sess.state.Load() != StatePlay {
		return
	}
	rtt := sess.latency()
	ms := int32(rtt / time.Millisecond)
	if ms < 0 {
		ms = 0
	}
	body := encodePlayerInfoLatency([]playerEntry{{
		uuid:    sess.getUUID(),
		latency: ms,
	}})
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay {
			return true
		}
		_ = other.write(other.ids().PlayPlayerInfoUpd, body)
		return true
	})
}

// encodePlayerInfoRemove builds a Player Info Remove body: VarInt count
// + UUIDs.
func encodePlayerInfoRemove(uuids [][16]byte) []byte {
	var b protocol.Buffer
	b.VarInt(int32(len(uuids)))
	for _, u := range uuids {
		b.UUID(u)
	}
	return b.Bytes()
}

// selfEntry snapshots the session's own tab-list row.
func selfEntry(sess *Session) playerEntry {
	return playerEntry{
		uuid:     sess.getUUID(),
		name:     sess.getName(),
		gamemode: 1, // creative, matching Join Game
		listed:   true,
		latency:  0,
	}
}

// othersEntries collects tab-list rows for every play-stage session
// except one (nil except collects all).
func (s *Server) othersEntries(except *Session) []playerEntry {
	var out []playerEntry
	for _, p := range s.presencePlayersOwned() {
		if except != nil && p.profile() == except.playerRecord.profile() {
			continue // an exact-profile replacement must not list its old self
		}
		if p.java != nil {
			if p.java == except || p.java.state.Load() != StatePlay || p.java.getName() == "" {
				continue
			}
			out = append(out, selfEntry(p.java))
		} else {
			out = append(out, recordEntry(p))
		}
	}
	return out
}

func recordEntry(p *playerRecord) playerEntry {
	profile := p.profile()
	return playerEntry{uuid: profile.UUID, name: profile.Name, gamemode: int32(p.mode()), listed: true}
}

// presencePlayersOwned exposes only committed authoritative incarnations.
// Configuring or rejected Java sessions never become visible through transport
// tracking alone.
func (s *Server) presencePlayersOwned() []*playerRecord {
	return s.activePlayersOwned()
}

// announcePlayerPresenceOwned preserves Java's profile-before-player-spawn
// dependency. Neutral admission calls this only after publishing its record.
func (s *Server) announcePlayerPresenceOwned(p *playerRecord) {
	s.broadcastPlayerInfoAdd(p.java, []playerEntry{recordEntry(p)})
	if e, ok := s.playerRecordEntityOwned(p); ok {
		s.broadcastEntitySpawn(p.java, e)
	}
}

func (s *Server) removePlayerPresenceOwned(p *playerRecord) {
	if !p.replacement {
		s.broadcastPlayerInfoRemove([][16]byte{p.profile().UUID})
	}
	s.broadcastEntityRemoveIn(p.dimName(), p.java, []int32{p.entityID.Load()})
}

// sendPlayerInfoTo sends rows for all existing players to one session.
func (s *Server) sendPlayerInfoTo(sess *Session) error {
	if others := s.othersEntries(sess); len(others) > 0 {
		return sess.write(sess.ids().PlayPlayerInfoUpd, encodePlayerInfoAdd(others))
	}
	return nil
}

// broadcastPlayerInfoAdd announces rows to every play-stage session
// except one.
func (s *Server) broadcastPlayerInfoAdd(except *Session, entries []playerEntry) {
	if len(entries) == 0 {
		return
	}
	body := encodePlayerInfoAdd(entries)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(sess.ids().PlayPlayerInfoUpd, body)
		return true
	})
}

// broadcastPlayerInfoRemove drops uuids from every play-stage session's
// tab list.
func (s *Server) broadcastPlayerInfoRemove(uuids [][16]byte) {
	if len(uuids) == 0 {
		return
	}
	body := encodePlayerInfoRemove(uuids)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(sess.ids().PlayPlayerInfoRem, body)
		return true
	})
}
