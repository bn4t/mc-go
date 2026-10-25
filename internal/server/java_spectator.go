package server

import (
	"errors"

	"mc-go/internal/protocol"
)

type javaSpectatorSelection struct {
	entityID int32
	present  bool
}

// Official 26.1/26.2 codec constants and independent live wire observations
// establish different bodies at the same packet ID:775 raw entity2 is02,
// 776 OptionalInt entity2 is03. Empty776 selection is not a reset request.
func (p *javaProfile) decodeSpectatorSelection(body []byte) (javaSpectatorSelection, error) {
	r := protocol.NewPacketReader(body)
	value, err := r.VarInt()
	if err != nil {
		return javaSpectatorSelection{}, err
	}
	if value < 0 || r.Remaining() != 0 {
		return javaSpectatorSelection{}, errors.New("invalid spectator selection body")
	}
	switch p.version.Protocol {
	case 775:
		return javaSpectatorSelection{entityID: value, present: true}, nil
	case 776:
		if value == 0 {
			return javaSpectatorSelection{}, nil
		}
		return javaSpectatorSelection{entityID: value - 1, present: true}, nil
	default:
		return javaSpectatorSelection{}, errors.New("unmeasured spectator selection codec")
	}
}

// startSpectatingOwned is shared by the command and both Java packet codecs.
// The measured initial response moves the spectator to the selected entity,
// then changes the camera. Continuous camera-follow remains a separate core
// parity gap; an absent selection does not synthesize an unobserved reset.
func (s *Server) startSpectatingOwned(sender *Session, target *dataEntity) bool {
	if sender == nil || target == nil || sender.mode() != GameModeSpectator {
		return false
	}
	targetID := int32(0)
	targetDimension := ""
	if target.sess != nil {
		if target.sess == sender || target.sess.state.Load() != StatePlay || target.sess.gone() {
			return false
		}
		targetID, targetDimension = target.sess.entityID.Load(), target.sess.dimName()
	} else if target.mob != nil {
		targetID, targetDimension = target.mob.ID, target.mob.Dim
		if targetDimension == "" {
			targetDimension = dimOverworld
		}
	}
	if targetID == 0 || targetID == sender.entityID.Load() || targetDimension != sender.dimName() {
		return false
	}
	x, y, z, pitch, yaw := target.posRot()
	if !s.teleportPlayerRot(sender.getName(), x, y, z, float32(yaw), float32(pitch)) {
		return false
	}
	var camera protocol.Buffer
	camera.VarInt(targetID)
	return sender.write(sender.ids().PlaySetCamera, camera.Bytes()) == nil
}

func (s *Server) applySpectatorSelectionOwned(sess *Session, selection javaSpectatorSelection) {
	if !selection.present || sess.mode() != GameModeSpectator {
		return
	}
	if player := s.sessionByEntityID(selection.entityID); player != nil {
		s.startSpectatingOwned(sess, &dataEntity{sess: player})
		return
	}
	if entity, ok := s.entities.GetPtr(selection.entityID); ok {
		s.startSpectatingOwned(sess, &dataEntity{mob: entity})
	}
}
