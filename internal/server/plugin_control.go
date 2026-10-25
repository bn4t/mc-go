package server

import (
	"context"

	"mc-go/api/player"
	"mc-go/api/plugin"
)

var _ plugin.PlayerControl = serverAPI{}
var _ plugin.LifecyclePlayerControl = serverAPI{}

func (a serverAPI) KickID(id player.ID, reason string) bool {
	return a.KickIDContext(a.s.runtimeCtx, id, reason)
}

func (a serverAPI) KickIDContext(ctx context.Context, id player.ID, reason string) bool {
	if ctx.Err() != nil || a.s.ShuttingDown() {
		return false
	}
	if _, found := a.s.playerStateByID(id); !found {
		return false
	}
	return a.s.queueSimulationAction(func() {
		if ctx.Err() == nil {
			a.s.kickPlayerRecordOwned(a.s.findPlayerByIDOwned(id), reason)
		}
	})
}

func (a serverAPI) TeleportID(id player.ID, x, y, z float64) bool {
	return a.TeleportIDContext(a.s.runtimeCtx, id, x, y, z)
}

func (a serverAPI) TeleportIDContext(ctx context.Context, id player.ID, x, y, z float64) bool {
	if ctx.Err() != nil || a.s.ShuttingDown() || badFloat(x) || badFloat(y) || badFloat(z) {
		return false
	}
	if _, found := a.s.playerStateByID(id); !found {
		return false
	}
	return a.s.queueSimulationAction(func() {
		if ctx.Err() != nil {
			return
		}
		p := a.s.findPlayerByIDOwned(id)
		if p == nil {
			return
		}
		pose := p.position()
		pose.X, pose.Y, pose.Z = x, y, z
		a.s.teleportPlayerRecordOwned(p, pose)
	})
}

func (a serverAPI) PlayerStateID(id player.ID) (plugin.PlayerState, bool) {
	return a.s.pluginPlayerStateID(id)
}
