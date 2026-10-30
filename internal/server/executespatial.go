package server

func entityCommandRotation(entity *dataEntity) [2]float32 {
	_, _, _, pitch, yaw := entity.posRot()
	return [2]float32{float32(yaw), float32(pitch)}
}

func (s *Server) executeRotatedAs(ctx *executeContext, player string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	selected, err := s.selectDataEntities(s.findSessionByName(player), args[0], tokBase, false, ctx.dim)
	if err != nil {
		return *err
	}
	var merged reply
	if !ctx.alive {
		return merged
	}
	for _, entity := range selected {
		next := *ctx
		next.rotation = entityCommandRotation(entity)
		next.forked = true
		r := s.execTail(&next, player, args[1:], tokBase+1)
		if r.fail != nil {
			return r
		}
		mergeReplies(&merged, r)
	}
	return merged
}
