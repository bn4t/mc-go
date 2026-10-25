// Package player defines transient, protocol-neutral avatar connections.
// The first supported capability is presence and movement. These avatars do not
// implement inventory, combat, survival simulation, or world/chunk replication.
package player

import (
	"context"
	"errors"
)

// ID identifies one host-issued admission, not an account or a reusable name.
// A reconnect receives a different ID. Its bytes have no public interpretation.
type ID [16]byte

// Profile is an authenticated identity supplied by a trusted frontend. Name
// must contain 1..16 bytes and UUID must be nonzero. Avatar admission rejects
// any reserved name or UUID conflict; names are compared case-sensitively.
type Profile struct {
	Name string
	UUID [16]byte
}

type Pose struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
}

type Movement = Pose

// State is a detached value. EntityID identifies the same authoritative entity
// observed by other frontends; it is not a protocol-specific entity type ID.
type State struct {
	ID        ID
	Profile   Profile
	EntityID  int32
	Dimension string
	Pose      Pose
	Gamemode  int32
}

type Capability uint64

const PresenceMovement Capability = 1

type Limits struct {
	// Limits cover queued detached values (estimated struct and copied string
	// storage), including Snapshot. Dequeued values belong to the adapter;
	// these limits do not bound its retained copies or total process memory.
	Messages int
	Bytes    int64
}

type Options struct {
	Capabilities Capability // zero selects PresenceMovement
	Limits       Limits     // nonpositive values select bounded host defaults
}

type UpdateKind uint8

const (
	Snapshot UpdateKind = iota + 1
	Added
	Moved
	Removed
	Correction
)

// Update is an ordered, reliable presence update. Snapshot contains all active
// players, including the newly admitted avatar. Other kinds contain Player.
// Revisions increase within a connection; gaps are permitted. Returned values
// belong to the consumer and contain no mutable server references.
// Moved reports a committed pose, dimension, or presentation-mode change.
type Update struct {
	Revision uint64
	Kind     UpdateKind
	Players  []State
	Player   State
	Reason   string
}

// Connection controls only its own admitted avatar. Admit's context and its
// frontend attachment own its lifetime. Canceling Next's context only cancels
// that read. Close is idempotent; a closed incarnation cannot be reused.
// Only one Next call may consume the stream at a time.
// One Move may wait for simulation ownership; concurrent calls return ErrBusy.
// Cancellation is checked before waiting and before commit, but does not
// interrupt the ownership mutex wait. Close signals immediately and schedules
// owned retirement without waiting for the running simulation action.
type Connection interface {
	ID() ID
	Move(context.Context, Movement) (bool, error)
	Next(context.Context) (Update, error)
	Close() error
}

var (
	ErrClosed                  = errors.New("player: connection closed")
	ErrOverflow                = errors.New("player: replication limits exceeded")
	ErrUnsupportedCapabilities = errors.New("player: unsupported avatar capabilities")
	ErrAdmissionRejected       = errors.New("player: admission rejected")
	ErrBusy                    = errors.New("player: action already in progress")
)
