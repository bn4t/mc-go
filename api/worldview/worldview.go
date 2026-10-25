// Package worldview defines fixed, finite block-state views of a shared world.
// Views contain canonical blocks only: no lighting, block entities, NBT, entity
// replication, inventory, chunk generation protocol, or moving view window.
package worldview

import (
	"context"
	"errors"

	"mc-go/api/registry"
)

const (
	MaxCells         = 65536
	MaxPerAttachment = 4
)

type Pos struct{ X, Y, Z int32 }

// Box includes Min and excludes Max on every axis. A valid view is nonempty,
// contains at most MaxCells cells, and lies within its dimension's build bounds.
type Box struct{ Min, Max Pos }

// Limits bound queued detached payloads, including the initial snapshot.
// Next transfers ownership and releases the queue charge. These limits exclude
// consumer-retained values, adapter copies, capture temporaries and total heap.
// Nonpositive values select bounded host defaults; global quotas also apply.
type Limits struct {
	Messages int
	Bytes    int64
}

type Request struct {
	Dimension string
	Bounds    Box
	Limits    Limits
}

type UpdateKind uint8

const (
	Snapshot UpdateKind = iota + 1
	Delta
)

type Change struct {
	Position Pos
	State    registry.BlockStateID
}

// Update starts with Snapshot, followed by nonempty Delta batches. Snapshot
// Blocks are x-fastest: ((y-Min.Y)*sizeZ+(z-Min.Z))*sizeX+(x-Min.X).
// Catalog is immutable and resolves these runtime-local IDs to complete state
// keys; IDs are not Java wire values or persistent identities. Revisions are
// ordered observation watermarks within a runtime dimension, with permitted
// gaps. Writes outside all active interests need not advance them; they are not
// complete world versions or compare-and-set tokens. One owner commit coalesces repeated
// writes to a position into its final state and suppresses unchanged values.
// Every update repeats the fixed Dimension, Bounds and Catalog.
type Update struct {
	Revision  uint64
	Kind      UpdateKind
	Dimension string
	Bounds    Box
	Catalog   *registry.BlockCatalog
	Blocks    []registry.BlockStateID
	Changes   []Change
}

// Subscription owns one fixed view. Its request context and frontend attachment
// own its lifetime. Close signals immediately and schedules owned cleanup;
// cancellation of Next only cancels that read. Only one Next may consume a
// subscription at a time. Overflow terminates this view, requiring a new watch;
// it does not retire an avatar or stop another view or attachment.
type Subscription interface {
	Next(context.Context) (Update, error)
	Close() error
}

// Edit is a privileged single-cell world operation, not a simulated player
// placement or break. State is a complete canonical identity. Expected, when
// present, is a compare-and-set precondition copied before waiting or enqueueing.
type Edit struct {
	Dimension string
	Position  Pos
	State     registry.StateKey
	Expected  *registry.StateKey
}

// EditResult describes this edit's synchronous side-effect pipeline. Applied
// means that pipeline ran, not that Before and After necessarily differ. After
// includes immediate side effects; cancellation after the first mutation does
// not roll them back. Neighbor effects are not an atomic multi-cell transaction.
// A later
// deferred edit in the same owner batch can supersede After before stream
// publication; the receipt does not claim a matching replication revision.
type EditResult struct {
	Applied bool
	Before  registry.BlockStateID
	After   registry.BlockStateID
}

type EditOutcome struct {
	Result EditResult
	Err    error
}

// EditReceipt completes once through one buffered outcome, then closes. One
// consumer receives that outcome. It is
// used by callback-safe deferred plugin editing rather than waiting on ownership.
type EditReceipt <-chan EditOutcome

var (
	ErrClosed           = errors.New("worldview: subscription closed")
	ErrOverflow         = errors.New("worldview: replication limits exceeded")
	ErrBusy             = errors.New("worldview: block action already in progress")
	ErrInvalidBounds    = errors.New("worldview: invalid block bounds")
	ErrUnknownDimension = errors.New("worldview: unknown dimension")
	ErrRejected         = errors.New("worldview: operation rejected")
	ErrLimit            = errors.New("worldview: resource limit exceeded")
)
