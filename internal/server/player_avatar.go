package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"mc-go/api/frontend"
	"mc-go/api/player"
)

type frontendAttachment struct {
	ctx       context.Context
	admitting atomic.Bool
}

type attachmentCore struct {
	frontendCore
	attachment *frontendAttachment
}

type actionLifetimeKey struct{}
type actionLifetime struct {
	ctx      context.Context
	previous *actionLifetime
}

// The host checks lifetimes directly at commit as well as canceling the hook
// context, so correctness does not depend on cancellation callback scheduling.
func actionContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lifetime, _ := ctx.Value(actionLifetimeKey{}).(*actionLifetime)
	for ; lifetime != nil; lifetime = lifetime.previous {
		if err := lifetime.ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func bindActionLifetime(ctx, lifetime context.Context) (context.Context, func()) {
	bound, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	previous, _ := ctx.Value(actionLifetimeKey{}).(*actionLifetime)
	bound = context.WithValue(bound, actionLifetimeKey{}, &actionLifetime{ctx: lifetime, previous: previous})
	return bound, func() { stop(); cancel() }
}

func (c attachmentCore) Snapshot(ctx context.Context) (frontend.Snapshot, error) {
	ctx, stop := bindActionLifetime(ctx, c.attachment.ctx)
	defer stop()
	return c.frontendCore.Snapshot(ctx)
}

func (c attachmentCore) Execute(ctx context.Context, action frontend.Action) (frontend.Result, error) {
	ctx, stop := bindActionLifetime(ctx, c.attachment.ctx)
	defer stop()
	return c.frontendCore.Execute(ctx, action)
}

type avatarConnection struct {
	server     *Server
	record     *playerRecord
	attachment *frontendAttachment
	lifetime   context.Context
	mailbox    *playerMailbox
	closed     atomic.Bool
	moving     atomic.Bool
	closeOnce  sync.Once
	stopsMu    sync.Mutex
	stops      []func() bool
}

func (c *avatarConnection) ID() player.ID { return c.record.ID() }
func (c *avatarConnection) Next(ctx context.Context) (player.Update, error) {
	return c.mailbox.Next(ctx)
}
func (c *avatarConnection) Move(ctx context.Context, movement player.Movement) (applied bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if c.lifetime.Err() != nil || c.attachment.ctx.Err() != nil || c.closed.Load() {
		return false, player.ErrClosed
	}
	if !c.moving.CompareAndSwap(false, true) {
		return false, player.ErrBusy
	}
	defer c.moving.Store(false)
	actionCtx, stopLifetime := bindActionLifetime(ctx, c.lifetime)
	actionCtx, stopAttachment := bindActionLifetime(actionCtx, c.attachment.ctx)
	defer func() { stopLifetime(); stopAttachment() }()
	c.server.simulationAction(func() {
		if err = actionCtx.Err(); err != nil {
			return
		}
		if c.closed.Load() || !c.record.active.Load() || c.server.ShuttingDown() || c.lifetime.Err() != nil || c.attachment.ctx.Err() != nil {
			err = player.ErrClosed
			return
		}
		applied = c.server.applyPlayerRecordMoveContextOwned(actionCtx, c.record, movement)
		if canceled := actionContextError(actionCtx); canceled != nil {
			err = canceled
		} else if c.closed.Load() || !c.record.active.Load() {
			err = player.ErrClosed
		}
	})
	return
}

func (c *avatarConnection) finish(err error) {
	c.closed.Store(true)
	c.mailbox.Close(closedPlayerError(err))
	c.stopsMu.Lock()
	for _, stop := range c.stops {
		stop()
	}
	c.stops = nil
	c.stopsMu.Unlock()
}

// Close signals immediately and schedules exactly one owned retirement. It is
// safe from a synchronous hook: it never waits for the owner running that hook.
func (c *avatarConnection) Close() error {
	c.closeOnce.Do(func() {
		c.finish(player.ErrClosed)
		go c.server.simulationAction(func() { c.server.retirePlayerOwned(c.record, "Frontend connection closed") })
	})
	return nil
}

func (c *avatarConnection) bindLifetime(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	c.stopsMu.Lock()
	if c.closed.Load() {
		stop()
	} else {
		c.stops = append(c.stops, stop)
	}
	c.stopsMu.Unlock()
}

func (c frontendCore) Admit(context.Context, player.Profile, player.Options) (player.Connection, error) {
	return nil, fmt.Errorf("%w: no frontend attachment", player.ErrAdmissionRejected)
}

func (c attachmentCore) Admit(ctx context.Context, profile player.Profile, options player.Options) (connection player.Connection, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if !c.attachment.admitting.CompareAndSwap(false, true) {
		return nil, player.ErrBusy
	}
	defer c.attachment.admitting.Store(false)
	admissionCtx, stopAttachment := bindActionLifetime(ctx, c.attachment.ctx)
	defer stopAttachment()
	if options.Capabilities != 0 && options.Capabilities != player.PresenceMovement {
		return nil, player.ErrUnsupportedCapabilities
	}
	c.s.simulationAction(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		if c.attachment.ctx.Err() != nil || c.s.ShuttingDown() {
			err = player.ErrClosed
			return
		}
		c.s.flushPlayerUpdatesOwned()
		// Reserve snapshot quota before allocating its detached state slice or
		// changing any player lifecycle. Replacement conflicts are excluded.
		count, text := 1, int64(len(profile.Name)+len(dimOverworld))
		c.s.playersMu.RLock()
		for _, p := range c.s.players {
			other := p.profile()
			if p.active.Load() && other.Name != profile.Name && other.UUID != profile.UUID {
				count++
				text += int64(len(other.Name) + len(p.physicalDimName()))
			}
		}
		c.s.playersMu.RUnlock()
		mailbox, quotaErr := newPlayerMailbox(options.Limits, count, text)
		if quotaErr != nil {
			err = quotaErr
			return
		}
		p := &playerRecord{core: c.s, name: profile.Name, uuid: profile.UUID, gameMode: GameModeCreative}
		if err = c.s.reservePlayerOwned(admissionCtx, p); err != nil {
			mailbox.Close(err)
			return
		}
		p.setDimension(c.s.dims[dimOverworld])
		x, y, z := c.s.WorldSpawn()
		p.setPosition(player.Pose{X: x, Y: y, Z: z})
		p.setEntityID(c.s.entities.Spawn(&Entity{UUID: profile.UUID, Type: mustEntityType("player"), Dim: dimOverworld, X: x, Y: y, Z: z, presenceOnly: true}).ID)
		avatar := &avatarConnection{server: c.s, record: p, attachment: c.attachment, lifetime: ctx, mailbox: mailbox}
		p.avatar = avatar
		if err = ctx.Err(); err != nil {
			c.s.retirePlayerOwned(p, "Admission canceled")
			return
		}
		if c.attachment.ctx.Err() != nil || c.s.ShuttingDown() {
			err = player.ErrClosed
			c.s.retirePlayerOwned(p, "Admission canceled")
			return
		}
		c.s.replacePlayersOwned(p)
		states := make([]player.State, 0, count)
		for _, other := range c.s.activePlayersOwned() {
			states = append(states, other.snapshot())
		}
		states = append(states, p.snapshot())
		c.s.playerRevision++
		if err = mailbox.initialize(player.Update{Revision: c.s.playerRevision, Kind: player.Snapshot, Players: states}); err != nil {
			c.s.retirePlayerOwned(p, "Snapshot initialization failed")
			return
		}
		c.s.commitPlayerAdmissionOwned(p)
		// Publishing Added can overflow another subscriber, whose removal
		// can in turn overflow this new stream. Never announce a retired
		// avatar to Java or return its closed handle as a successful admit.
		if !p.active.Load() || avatar.closed.Load() || mailbox.Err() != nil {
			err = closedPlayerError(mailbox.Err())
			return
		}
		c.s.announcePlayerPresenceOwned(p)
		connection = avatar
	})
	if connection != nil {
		avatar := connection.(*avatarConnection)
		avatar.bindLifetime(ctx)
		avatar.bindLifetime(c.attachment.ctx)
	}
	return
}

func (s *Server) closeAttachmentPlayers(attachment *frontendAttachment) {
	s.simulationAction(func() {
		for _, p := range s.activePlayersOwned() {
			if p.avatar != nil && p.avatar.attachment == attachment {
				s.retirePlayerOwned(p, "Frontend attachment closed")
			}
		}
	})
}
