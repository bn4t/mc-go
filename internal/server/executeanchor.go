package server

// commandSourceEyeHeight reads the current executor's modeled eye offset.
// Player dimensions use their cached effective scale, not live dirty attributes.
func (s *Server) commandSourceEyeHeight(entity *dataEntity) float64 {
	if entity == nil {
		return 0
	}
	if player := entity.sess; player != nil {
		pose := int32(0)
		if e, ok := s.entities.Get(player.entityID.Load()); ok {
			pose = e.Pose
		}
		scale := player.collisionScale
		if scale == 0 {
			scale = 1
		}
		if pose == 2 || pose == 7 {
			scale = 1
		}
		return float64(float32(playerPoseEyeHeight(pose)) * scale)
	}
	e := entity.mob
	name, _ := EntityTypeName(e.Type)
	if name == "armor_stand" {
		if e.ArmorStandMarker {
			return 0
		}
		if e.Small {
			return float64(float32(.9875))
		}
	}
	// Interaction runtime dimensions and unmodeled pose/size overrides remain
	// outside the registry baseline projected here.
	eye := commandTypeEyes[name]
	if e.Baby {
		if value, ok := commandBabyEyes[name]; ok {
			eye = value
		} else {
			eye *= .5
		}
	}
	if name == "slime" || name == "magma_cube" {
		size := e.CubeSize
		if size < 1 {
			size = 1
		}
		eye *= float32(size)
	}
	return float64(eye)
}
