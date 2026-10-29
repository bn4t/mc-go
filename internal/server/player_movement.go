package server

import (
	"context"
	"errors"
	"log"
	"time"

	"mc-go/api/frontend"
	"mc-go/api/plugin"
	"mc-go/internal/event"
	"mc-go/internal/world"
)

type playerMovementDecision struct {
	move    frontend.Movement
	allowed bool
	correct bool
}

// validatePlayerMovementOwned is the shared avatar movement rule. Hooks observe
// the same proposal and all frontends revalidate their result before any pose,
// entity record or observer is changed. Passenger handling is Java-only today.
func (s *Server) validatePlayerMovementOwned(ctx context.Context, p *playerRecord, from, move frontend.Movement, w *world.World, passenger bool) playerMovementDecision {
	proposal := plugin.Move{
		ID: p.ID(), Player: p.profile().Name,
		From: plugin.Position{X: from.X, Y: from.Y, Z: from.Z, Yaw: from.Yaw, Pitch: from.Pitch, OnGround: from.OnGround},
		To:   plugin.Position{X: move.X, Y: move.Y, Z: move.Z, Yaw: move.Yaw, Pitch: move.Pitch, OnGround: move.OnGround},
	}
	original := proposal.To
	proposal, allowed, hookErr := s.hooks.BeforeMoveE(ctx, proposal)
	var callbackErr *plugin.CallbackError
	if errors.As(hookErr, &callbackErr) {
		log.Printf("plugin movement hook: %v", callbackErr)
	}
	if !allowed {
		return playerMovementDecision{move: from, correct: true}
	}
	target := proposal.To
	modified := target != original
	move = frontend.Movement{X: target.X, Y: target.Y, Z: target.Z, Yaw: target.Yaw, Pitch: target.Pitch, OnGround: target.OnGround}
	if badFloat(target.X) || badFloat(target.Y) || badFloat(target.Z) || badFloat(float64(target.Yaw)) || badFloat(float64(target.Pitch)) {
		return playerMovementDecision{move: from, correct: modified}
	}
	const border = 29999984.0
	if target.X < -border || target.X > border || target.Z < -border || target.Z > border || target.Y < -2048 || target.Y > 2048 {
		return playerMovementDecision{move: from, correct: modified}
	}
	if passenger {
		move.X, move.Y, move.Z = from.X, from.Y, from.Z
		return playerMovementDecision{move: move, allowed: true}
	}
	half, height := s.playerCollisionDims(p)
	if p.mode() != GameModeSpectator && (target.X != from.X || target.Y != from.Y || target.Z != from.Z) && s.playerBoxCollidesInDimension(p.dimension(), from.X, from.Y, from.Z, target.X, target.Y, target.Z, half, height) {
		return playerMovementDecision{move: from, correct: true}
	}
	return playerMovementDecision{move: move, allowed: true, correct: modified}
}

// applyPlayerRecordMoveOwned is shared by real Java players and neutral avatars.
// The record owns identity and pose; its optional Java adapter supplies only the
// additional, already-existing Java gameplay/replication behavior.
func (s *Server) applyPlayerRecordMoveOwned(p *playerRecord, move frontend.Movement) bool {
	return s.applyPlayerRecordMoveContextOwned(s.runtimeCtx, p, move)
}

func (s *Server) applyPlayerRecordMoveContextOwned(ctx context.Context, p *playerRecord, move frontend.Movement) bool {
	if actionContextError(ctx) != nil || s.ShuttingDown() || !p.active.Load() || (p.avatar != nil && p.avatar.closed.Load()) {
		return false
	}
	from, at := p.position(), time.Now()
	passenger := p.java != nil && p.java.riding.Load() != 0
	decision := s.validatePlayerMovementOwned(ctx, p, from, move, p.dimension().w, passenger)
	// A synchronous hook may block while its request or attachment is canceled,
	// or close its own connection. Neither a pose nor a correction may be
	// published after that terminal condition becomes observable.
	if actionContextError(ctx) != nil || s.ShuttingDown() || !p.active.Load() || (p.avatar != nil && p.avatar.closed.Load()) {
		return false
	}
	if !decision.allowed {
		if decision.correct {
			s.correctPlayerOwned(p, from)
		}
		return false
	}
	move = decision.move
	if passenger {
		s.commitPlayerPoseOwned(p, move)
		return true
	}
	s.commitPlayerPoseWithEffectsOwned(p, move, func() {
		if decision.correct {
			s.correctPlayerOwned(p, move)
		}
		if p.java != nil {
			s.finishJavaPlayerMoveOwned(p.java, from, move, at)
		}
	})
	if s.bus != nil {
		latency := time.Duration(0)
		if p.java != nil {
			latency = p.java.latency()
		}
		s.bus.Publish(event.TopicPlayerMove, PlayerMove{
			ID: p.ID(), Name: p.profile().Name, X: move.X, Y: move.Y, Z: move.Z,
			FromX: from.X, FromY: from.Y, FromZ: from.Z,
			Yaw: move.Yaw, Pitch: move.Pitch, OnGround: move.OnGround,
			At: at, Latency: latency,
		})
	}
	if p.java != nil {
		_ = s.sendWindow(p.java, int(move.X)>>4, int(move.Z)>>4)
		s.sendCenterChunk(p.java, int(move.X)>>4, int(move.Z)>>4)
	}
	return true
}

// commitPlayerPoseOwned is also the teleport commit seam. It requires ownership
// and a validated finite pose; movement validation is intentionally separate
// because privileged teleports may enter blocks.
func (s *Server) commitPlayerPoseOwned(p *playerRecord, pose frontend.Movement) {
	s.commitPlayerPoseWithEffectsOwned(p, pose, nil)
}

func (s *Server) commitPlayerPoseWithEffectsOwned(p *playerRecord, pose frontend.Movement, afterPosition func()) {
	p.setPosition(pose)
	if afterPosition != nil {
		afterPosition()
	}
	if eid := p.entityID.Load(); eid != 0 {
		s.entities.updatePlayerPose(eid, pose.X, pose.Y, pose.Z, pose.Yaw, pose.Pitch, pose.OnGround)
		if p.java == nil {
			// A neutral avatar has no client-side chunk window. Reevaluate
			// each real viewer so range crossings pair/unpair immediately.
			if e, ok := s.entities.GetPtr(eid); ok {
				s.sessions.Range(func(_, v any) bool { s.evalPair(v.(*Session), e); return true })
			}
		}
		s.broadcastEntityTeleport(p.java, eid, pose.X, pose.Y, pose.Z, pose.Yaw, pose.Pitch, pose.OnGround)
	}
	s.publishPlayerMovedOwned(p)
}
