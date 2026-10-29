package server

import "mc-go/internal/world"

// worldCollisionShapeAt supplies block-entity-dependent shapes when that
// block has live animation state, then falls back to the static report.
func (s *Server) worldCollisionShapeAt(d *dimension, state world.Block, pos world.Pos) []cellBox {
	if isShulkerBoxBlock(state) {
		return shulkerWorldCollisionShapeAt(d, state, pos)
	}
	if isBlockNamed(state, "moving_piston") {
		for _, mp := range s.movingPistons {
			if mp.dim == d.name && mp.pos == pos {
				return movingPistonCollisionShape(mp, movingPistonProgress(mp))
			}
		}
		return nil // An orphan moving block has no block-entity collision.
	}
	return measuredCollisionShapeAt(state, pos)
}

// A shulker lid adds half its current progress along its facing direction.
// Progress is a float in the block entity, widened before scaling by 1/2.
func shulkerCollisionShape(state world.Block, progress float32) []cellBox {
	box := cellBox{0, 0, 0, 1, 1, 1}
	amount := float64(progress) / 2
	facing, _ := world.StateProperty(state, "facing")
	switch facing {
	case "down":
		box[1] -= amount
	case "up":
		box[4] += amount
	case "north":
		box[2] -= amount
	case "south":
		box[5] += amount
	case "west":
		box[0] -= amount
	case "east":
		box[3] += amount
	}
	return []cellBox{box}
}

func (s *Server) shulkerViewerChange(d *dimension, c *containerData, change int) {
	d.containers.mu.Lock()
	c.shulkerOpeners += change
	if c.shulkerOpeners < 0 {
		c.shulkerOpeners = 0
	}
	d.containers.mu.Unlock()
}

func advanceShulkerAnimation(c *containerData) {
	if c.shulkerOpeners > 0 {
		c.shulkerProgress += float32(.1)
		if c.shulkerProgress > 1 {
			c.shulkerProgress = 1
		}
	} else {
		c.shulkerProgress -= float32(.1)
		if c.shulkerProgress < 0 {
			c.shulkerProgress = 0
		}
	}
}

func shulkerWorldCollisionShapeAt(d *dimension, state world.Block, pos world.Pos) []cellBox {
	d.containers.mu.Lock()
	var progress float32
	if c := d.containers.containers[pos]; c != nil && c.spec.shulker {
		progress = c.shulkerProgress
	}
	d.containers.mu.Unlock()
	return shulkerCollisionShape(state, progress)
}
