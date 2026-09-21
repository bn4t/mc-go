package server

import "math"

// runKill implements /kill [target]: the target is an entity selector —
// bare names match players only — and every entity it resolves dies.
// Players go through the death flow, living mobs through damageMob's
// dying path, and non-living entities (drops, orbs) are discarded
// outright, matching vanilla's Entity.kill(): living entities hurt with
// Float.MAX_VALUE through the generic_kill source, everything else
// discarded.
func (s *Server) runKill(player string, args []string) reply {
	sender := s.findSessionByName(player)
	tok := "@s"
	if len(args) > 0 {
		tok = args[0]
	}
	var cands []*dataEntity
	if len(tok) == 0 || tok[0] != '@' {
		if sess := s.findSessionByName(tok); sess != nil {
			cands = []*dataEntity{{sess: sess}}
		}
	} else {
		var rep *reply
		cands, rep = s.selectDataEntities(sender, tok, 1, false)
		if rep != nil {
			return *rep
		}
	}
	if len(cands) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	for _, d := range cands {
		if d.sess != nil {
			s.broadcastDamageEvent(d.sess.entityID.Load(), damageInfo{typeName: "generic_kill"})
			s.killPlayer(d.sess, translatable("death.attack.genericKill", playerChatComponent(d.sess)))
			continue
		}
		e := d.mob
		if e.Dying {
			continue
		}
		if e.Living {
			s.damageMob(e, math.MaxFloat32)
		} else {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		}
	}
	if len(cands) == 1 {
		return componentReply(translatable("commands.kill.success.single", cands[0].nameComp()))
	}
	return componentReply(translatable("commands.kill.success.multiple",
		int32(len(cands))))
}
