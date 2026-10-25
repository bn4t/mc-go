package server

import (
	"errors"

	"mc-go/internal/protocol"
)

var errBadPacks = errors.New("bad known packs")

// packEntry is one Known Packs entry: namespace + id + version.
// Wire shape, clean-room from the public protocol docs:
// VarInt count, then String namespace, String id, String version.
type packEntry struct {
	namespace string
	id        string
	version   string
}

func parseKnownPacks(data []byte) ([]packEntry, error) {
	r := protocol.NewPacketReader(data)
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > 64 {
		return nil, errBadPacks
	}
	out := make([]packEntry, 0, n)
	for i := int32(0); i < n; i++ {
		ns, err := r.String()
		if err != nil {
			return nil, err
		}
		id, err := r.String()
		if err != nil {
			return nil, err
		}
		ver, err := r.String()
		if err != nil {
			return nil, err
		}
		out = append(out, packEntry{ns, id, ver})
	}
	return out, nil
}

func writeKnownPacks(b *protocol.Buffer, packs []packEntry) {
	b.VarInt(int32(len(packs)))
	for _, p := range packs {
		b.String(p.namespace)
		b.String(p.id)
		b.String(p.version)
	}
}

// knownPacksSelection is the pack set this server ships, matching the
// vanilla selection observed over the wire: minecraft:core at the data
// version. The server announces its own selection; it never mirrors the
// client's (a real client offering a different version must receive
// full registry payloads, which we always send).
var knownPacksSelection = []packEntry{
	{namespace: "minecraft", id: "core", version: "26.2"},
}

// sendConfigBrand sends the vanilla brand plugin message: channel
// minecraft:brand, payload a String with the server brand.
func (s *Server) sendConfigBrand(sess *Session) error {
	var b protocol.Buffer
	b.String("minecraft:brand")
	var payload protocol.Buffer
	payload.String("mc-go")
	pb := payload.Bytes()
	b.VarInt(int32(len(pb)))
	b.Raw(pb)
	return sess.write(sess.ids().ConfigPluginMessageCB, b.Bytes())
}

// sendFeatureFlags sends the enabled feature flags. Vanilla enables
// minecraft:vanilla on a normal server.
func (s *Server) sendFeatureFlags(sess *Session) error {
	var b protocol.Buffer
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	b.VarInt(int32(len(profile.config.featureFlags)))
	for _, flag := range profile.config.featureFlags {
		b.String(flag)
	}
	return sess.write(sess.ids().ConfigFeatureFlags, b.Bytes())
}

func (s *Server) sendKnownPacks(sess *Session) error {
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	var b protocol.Buffer
	writeKnownPacks(&b, profile.config.knownPacks)
	return sess.write(sess.ids().ConfigKnownPacksCB, b.Bytes())
}

// handleConfig runs the Configuration stage in vanilla order:
//  1. brand plugin message, feature flags, known packs selection
//  2. read the client's Known Packs (and ignore other packets)
//  3. send Registry Data (dimension_type + biome + ...)
//  4. update tags, then Finish Configuration; wait for the ack -> Play
func (s *Server) handleConfig(sess *Session) {
	if err := s.sendConfigBrand(sess); err != nil {
		return
	}
	if err := s.sendFeatureFlags(sess); err != nil {
		return
	}
	if err := s.sendKnownPacks(sess); err != nil {
		return
	}
	for {
		p, err := sess.pr.ReadPacket()
		if err != nil {
			return
		}
		switch p.ID {
		case sess.ids().ConfigKnownPacks:
			if _, err := parseKnownPacks(p.Data); err != nil {
				return
			}
			goto registries
		case sess.ids().ConfigClientInfo:
			if info, ok := parseClientInfo(p.Data); ok {
				sess.infoMu.Lock()
				sess.info = info
				sess.infoMu.Unlock()
			}
		default:
			// Ignore keep-alives, pongs, etc.
		}
	}
registries:
	if err := s.sendRegistries(sess); err != nil {
		return
	}
	if err := s.sendUpdateTags(sess); err != nil {
		return
	}
	var b protocol.Buffer // Finish Configuration has an empty body
	if err := sess.write(sess.ids().ConfigFinish, b.Bytes()); err != nil {
		return
	}
	for {
		p, err := sess.pr.ReadPacket()
		if err != nil {
			return
		}
		if p.ID == sess.ids().ConfigFinishAck {
			break
		}
	}
	s.handlePlay(sess)
}
