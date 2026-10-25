package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"mc-go/api/event"
	"mc-go/api/plugin"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func copyBlockEdit(edit worldview.Edit) worldview.Edit {
	if edit.Expected != nil {
		expected := *edit.Expected
		edit.Expected = &expected
	}
	return edit
}

// editBlockOwned is the typed privileged entry point only. Its policy boundary
// precedes the existing command-style write pipeline, whose neighbor effects
// can change additional cells, block entities and entities. It is not a rollback
// transaction or a player dig/place operation.
func (s *Server) editBlockOwned(ctx context.Context, edit worldview.Edit, origin string) (result worldview.EditResult, err error) {
	if err = actionContextError(ctx); err != nil {
		return
	}
	if s.ShuttingDown() {
		return result, worldview.ErrClosed
	}
	d, err := s.worldviewDimension(edit.Dimension)
	if err != nil {
		return result, err
	}
	if err = validateWorldviewPosition(d, edit.Position); err != nil {
		return
	}
	catalog := s.BlockCatalog()
	if _, valid := catalog.Lookup(edit.State); !valid {
		return result, fmt.Errorf("%w: unknown canonical target state", worldview.ErrRejected)
	}
	pos := world.Pos{X: int(edit.Position.X), Y: int(edit.Position.Y), Z: int(edit.Position.Z)}
	before := d.w.GetBlock(pos)
	result.Before, result.After = before, before
	beforeKey, valid := catalog.State(before)
	if !valid {
		return result, fmt.Errorf("%w: unknown existing state", worldview.ErrRejected)
	}
	if edit.Expected != nil {
		expected, valid := catalog.Lookup(*edit.Expected)
		if !valid || expected != before {
			return result, fmt.Errorf("%w: expected state did not match", worldview.ErrRejected)
		}
	}
	proposal := plugin.BlockEdit{Origin: origin, Dimension: edit.Dimension, Position: edit.Position, Before: beforeKey, Target: edit.State}
	proposal, accepted, hookErr := s.hooks.BeforeBlockEdit(ctx, proposal)
	if hookErr != nil {
		var fault *plugin.CallbackError
		if errors.As(hookErr, &fault) {
			log.Printf("block edit policy failed: %v", hookErr)
			return result, fmt.Errorf("%w: block edit policy failed", worldview.ErrRejected)
		}
		return result, hookErr
	}
	if !accepted {
		return result, fmt.Errorf("%w: block edit policy veto", worldview.ErrRejected)
	}
	// Revalidate immediately before the first core mutation. Neither a modified
	// target nor an expired attachment/plugin lifetime may bypass this boundary.
	if err = actionContextError(ctx); err != nil {
		return
	}
	if s.ShuttingDown() {
		return result, worldview.ErrClosed
	}
	if err = validateWorldviewPosition(d, edit.Position); err != nil {
		return
	}
	target, valid := catalog.Lookup(proposal.Target)
	if !valid {
		return result, fmt.Errorf("%w: unknown modified target state", worldview.ErrRejected)
	}
	current := d.w.GetBlock(pos)
	if current != before {
		return result, fmt.Errorf("%w: state changed during policy", worldview.ErrRejected)
	}
	if edit.Expected != nil {
		expected, valid := catalog.Lookup(*edit.Expected)
		if !valid || current != expected {
			return result, fmt.Errorf("%w: expected state did not match", worldview.ErrRejected)
		}
	}
	if err = actionContextError(ctx); err != nil {
		return
	}
	if current == target {
		return result, nil
	}
	s.setBlockAndBroadcastInOwned(d, "", pos, target, true)
	s.emitLightUpdate(d, pos)
	result.Applied = true
	result.After = d.w.GetBlock(pos)
	if s.bus != nil {
		s.bus.Publish(event.TopicBlockEdited, event.BlockEdited{Origin: origin, Dimension: edit.Dimension, Position: edit.Position, Before: before, Target: target, After: result.After})
	}
	return result, nil
}

func (c attachmentCore) EditBlock(ctx context.Context, request worldview.Edit) (result worldview.EditResult, err error) {
	edit := copyBlockEdit(request)
	bound, release, err := c.beginBlockAction(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	c.s.simulationAction(func() {
		result, err = c.s.editBlockOwned(bound, edit, "frontend:"+c.attachment.label)
	})
	return
}
