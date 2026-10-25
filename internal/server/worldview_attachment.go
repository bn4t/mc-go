package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"mc-go/api/registry"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

// One optional block action can wait for this attachment's simulation owner.
// The wait itself is not interruptible; all contexts are rechecked at commit.
func (c attachmentCore) beginBlockAction(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if c.attachment.ctx.Err() != nil || c.s.ShuttingDown() {
		return nil, nil, worldview.ErrClosed
	}
	if !c.attachment.blockAction.CompareAndSwap(false, true) {
		return nil, nil, worldview.ErrBusy
	}
	bound, stop := bindActionLifetime(ctx, c.attachment.ctx)
	return bound, func() { stop(); c.attachment.blockAction.Store(false) }, nil
}

func (s *Server) worldviewDimension(name string) (*dimension, error) {
	d := s.dims[name]
	if d == nil {
		return nil, fmt.Errorf("%w: %q", worldview.ErrUnknownDimension, name)
	}
	return d, nil
}

func validateWorldviewPosition(d *dimension, p worldview.Pos) error {
	const border = 29999984
	if p.X < -border || p.X > border || p.Z < -border || p.Z > border || int64(p.Y) < int64(d.minY) || int64(p.Y) >= int64(d.minY)+int64(d.height) {
		return worldview.ErrInvalidBounds
	}
	return nil
}

func validateWorldviewBox(d *dimension, box worldview.Box) (int64, error) {
	dx, dy, dz := int64(box.Max.X)-int64(box.Min.X), int64(box.Max.Y)-int64(box.Min.Y), int64(box.Max.Z)-int64(box.Min.Z)
	if dx <= 0 || dy <= 0 || dz <= 0 || dx > worldview.MaxCells || dy > worldview.MaxCells || dz > worldview.MaxCells || dx*dy*dz > worldview.MaxCells {
		return 0, worldview.ErrInvalidBounds
	}
	if err := validateWorldviewPosition(d, box.Min); err != nil {
		return 0, err
	}
	last := worldview.Pos{X: box.Max.X - 1, Y: box.Max.Y - 1, Z: box.Max.Z - 1}
	if err := validateWorldviewPosition(d, last); err != nil {
		return 0, err
	}
	return dx * dy * dz, nil
}

type worldviewSubscription struct {
	server     *Server
	attachment *frontendAttachment
	dimension  *dimension
	interest   uint64
	mailbox    *worldviewMailbox
	closed     atomic.Bool
	closeOnce  sync.Once
	removed    bool // simulation-owned; only this registration can remove its token
	stopsMu    sync.Mutex
	stops      []func() bool
}

func (v *worldviewSubscription) Next(ctx context.Context) (worldview.Update, error) {
	return v.mailbox.Next(ctx)
}

func (v *worldviewSubscription) finish(err error) {
	v.closed.Store(true)
	v.mailbox.Close(err)
	v.stopsMu.Lock()
	for _, stop := range v.stops {
		stop()
	}
	v.stops = nil
	v.stopsMu.Unlock()
}

func (v *worldviewSubscription) Close() error {
	if v.closed.Load() {
		return nil
	}
	v.closeOnce.Do(func() {
		if v.closed.Load() {
			return
		}
		v.finish(worldview.ErrClosed)
		// Closing a live watch schedules at most one owned cleanup. Handles
		// already retired by overflow/detach are allocation-free no-ops.
		// Count/cell quota stays reserved until owned cleanup removes interest.
		go v.server.simulationAction(func() { v.server.removeWorldviewOwned(v, worldview.ErrClosed) })
	})
	return nil
}

func (v *worldviewSubscription) bindLifetime(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() { _ = v.Close() })
	v.stopsMu.Lock()
	if v.closed.Load() {
		stop()
	} else {
		v.stops = append(v.stops, stop)
	}
	v.stopsMu.Unlock()
}

func (c attachmentCore) WatchBlocks(ctx context.Context, request worldview.Request) (subscription worldview.Subscription, err error) {
	bound, release, err := c.beginBlockAction(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	c.s.simulationAction(func() {
		if err = actionContextError(bound); err != nil {
			return
		}
		if c.s.ShuttingDown() {
			err = worldview.ErrClosed
			return
		}
		d, dimensionErr := c.s.worldviewDimension(request.Dimension)
		if dimensionErr != nil {
			err = dimensionErr
			return
		}
		cells, boundsErr := validateWorldviewBox(d, request.Bounds)
		if boundsErr != nil {
			err = boundsErr
			return
		}
		mailbox, quotaErr := newWorldviewMailbox(&c.s.worldviewBudget, &c.attachment.worldviewOwner, cells, request.Dimension, request.Limits)
		if quotaErr != nil {
			err = quotaErr
			return
		}
		// Flush previous committed dirt before establishing this baseline. No
		// world writes can interleave capture and registration under ownership.
		c.s.flushWorldUpdatesOwned()
		blocks := make([]registry.BlockStateID, int(cells))
		min := world.Pos{X: int(request.Bounds.Min.X), Y: int(request.Bounds.Min.Y), Z: int(request.Bounds.Min.Z)}
		max := world.Pos{X: int(request.Bounds.Max.X) - 1, Y: int(request.Bounds.Max.Y) - 1, Z: int(request.Bounds.Max.Z) - 1}
		d.w.FillBox(min, max, blocks)
		if err = actionContextError(bound); err != nil || c.s.ShuttingDown() {
			if err == nil {
				err = worldview.ErrClosed
			}
			mailbox.Close(err)
			mailbox.releaseWatch()
			return
		}
		if err = mailbox.initialize(worldview.Update{Revision: c.s.worldviewRevisionOwned(d), Kind: worldview.Snapshot,
			Dimension: request.Dimension, Bounds: request.Bounds, Catalog: world.BlockCatalog(), Blocks: blocks}); err != nil {
			mailbox.Close(err)
			mailbox.releaseWatch()
			return
		}
		// initialize adopts blocks as its private baseline. Never retain or
		// mutate that array after handing it over.
		interest, interestErr := d.w.AddBlockInterest(min, max)
		if interestErr != nil {
			err = interestErr
			mailbox.Close(err)
			mailbox.releaseWatch()
			return
		}
		if err = actionContextError(bound); err != nil || c.s.ShuttingDown() {
			if err == nil {
				err = worldview.ErrClosed
			}
			d.w.RemoveBlockInterest(interest)
			mailbox.Close(err)
			mailbox.releaseWatch()
			return
		}
		view := &worldviewSubscription{server: c.s, attachment: c.attachment, dimension: d, interest: interest, mailbox: mailbox}
		if c.s.worldviews == nil {
			c.s.worldviews = make(map[*worldviewSubscription]struct{})
		}
		c.s.worldviews[view] = struct{}{}
		subscription = view
	})
	if subscription != nil {
		view := subscription.(*worldviewSubscription)
		view.bindLifetime(ctx)
		view.bindLifetime(c.attachment.ctx)
	}
	return
}

func (s *Server) removeWorldviewOwned(view *worldviewSubscription, err error) {
	if view.removed {
		return
	}
	view.removed = true
	view.finish(err)
	delete(s.worldviews, view)
	view.dimension.w.RemoveBlockInterest(view.interest)
	// Removal precedes releasing baseline/index cell and watch-count quota.
	view.mailbox.releaseWatch()
}

func (s *Server) closeAttachmentWorldviewsOwned(attachment *frontendAttachment) {
	for view := range s.worldviews {
		if view.attachment == attachment {
			s.removeWorldviewOwned(view, worldview.ErrClosed)
		}
	}
}

func (s *Server) closeAllWorldviewsOwned() {
	for view := range s.worldviews {
		s.removeWorldviewOwned(view, worldview.ErrClosed)
	}
}

func (s *Server) publishWorldChangesOwned(d *dimension, revision uint64, changes []world.BlockChange) {
	// The dimension journal is already bounded by reserved watched cells.
	// Build one shared canonical batch, not a delta array per subscriber.
	canonical := make([]worldview.Change, len(changes))
	for i, change := range changes {
		canonical[i] = worldview.Change{Position: worldview.Pos{X: int32(change.Position.X), Y: int32(change.Position.Y), Z: int32(change.Position.Z)}, State: change.State}
	}
	var failed []*worldviewSubscription
	for view := range s.worldviews {
		if view.dimension == d && !view.mailbox.tryPublishDelta(revision, canonical) {
			failed = append(failed, view)
		}
	}
	for _, view := range failed {
		s.removeWorldviewOwned(view, view.mailbox.Err())
	}
}
