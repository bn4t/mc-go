package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net"

	"mc-go/api/player"
	"mc-go/internal/event"
)

// reservePlayerOwned is the single capacity reservation for avatars and Java
// logins still configuring. Identity alone never implies a counted player.
func (s *Server) reservePlayerOwned(ctx context.Context, p *playerRecord) error {
	if p.counted {
		return nil
	}
	if err := actionContextError(ctx); err != nil {
		return err
	}
	if s.ShuttingDown() {
		return player.ErrClosed
	}
	profile := p.profile()
	if profile.Name == "" || len(profile.Name) > 16 || profile.UUID == [16]byte{} {
		return fmt.Errorf("%w: invalid identity", player.ErrAdmissionRejected)
	}
	s.banMu.Lock()
	_, banned := s.bannedNames[profile.Name]
	s.banMu.Unlock()
	if banned || (s.whitelistOn.Load() && !s.onWhitelist(profile.Name)) {
		return fmt.Errorf("%w: identity policy", player.ErrAdmissionRejected)
	}
	s.playersMu.RLock()
	conflict := false
	for _, other := range s.players {
		identity := other.profile()
		if other != p && !other.left && (identity.Name == profile.Name || identity.UUID == profile.UUID) {
			// Java retains exact-profile replacement at owned play entry.
			// Neutral avatars reject duplicates, including pending Java login.
			if p.java == nil || identity != profile {
				conflict = true
				break
			}
		}
	}
	s.playersMu.RUnlock()
	if conflict {
		return fmt.Errorf("%w: identity already reserved", player.ErrAdmissionRejected)
	}
	decision, err := s.hooks.BeforeAdmission(ctx, profile)
	if err != nil {
		return err
	}
	if decision.Denied {
		return fmt.Errorf("%w: %s", player.ErrAdmissionRejected, decision.Reason)
	}
	if err := actionContextError(ctx); err != nil {
		return err
	}
	if s.ShuttingDown() {
		return player.ErrClosed
	}
	if s.OnlineCount() >= s.MaxPlayers {
		return fmt.Errorf("%w: server is full", player.ErrAdmissionRejected)
	}
	if _, err := rand.Read(p.incarnation[:]); err != nil {
		return err
	}
	p.core = s
	p.counted = true
	s.playersMu.Lock()
	if s.players == nil {
		s.players = make(map[player.ID]*playerRecord)
	}
	s.players[p.ID()] = p
	s.playersMu.Unlock()
	s.online.Add(1)
	return nil
}

// Replacement is committed only after admission policy and resource preflight
// succeed. Names retain Java's case-sensitive policy; UUID conflicts also share
// the same namespace. Pending configuring logins reserve capacity, not presence.
func (s *Server) replacePlayersOwned(p *playerRecord) {
	profile := p.profile()
	for _, previous := range s.activePlayersOwned() {
		other := previous.profile()
		if p.java != nil && previous != p && other == profile {
			previous.replacement = true
			if previous.java != nil {
				s.disconnectWithComponent(previous.java, translatable("multiplayer.disconnect.duplicate_login"))
			}
			s.retirePlayerOwned(previous, "Replaced by a new admission")
		}
	}
}

func (s *Server) commitPlayerAdmissionOwned(p *playerRecord) {
	if p.left || p.admitted {
		return
	}
	s.supersedePlayerSavesOwned(p)
	p.admitted = true
	p.active.Store(true)
	state := p.snapshot()
	p.published.Store(&state)
	s.publishPlayerUpdateOwned(player.Update{Kind: player.Added, Player: state}, p)
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerJoined, event.PlayerJoined{Player: state})
		s.bus.Publish(event.TopicPlayerJoin, map[string]string{"name": state.Profile.Name})
	}
}

// supersedePlayerSavesOwned transfers remembered/native authority only after
// Java admission succeeds. Retired transports can remain tracked during kick
// grace or delayed cleanup, so the active-player index alone is insufficient.
// One owner-only flag per retained session avoids a persistent identity map;
// admission scans O(tracked sessions), retaining no additional sessions.
func (s *Server) supersedePlayerSavesOwned(p *playerRecord) {
	if p.java == nil {
		return
	}
	uuid := p.profile().UUID
	s.sessions.Range(func(_, value any) bool {
		previous := value.(*Session)
		if previous != p.java && previous.playerRecord.admitted && previous.playerRecord.profile().UUID == uuid {
			previous.persistenceSuperseded = true
		}
		return true
	})
}

func (s *Server) retirePlayerStateOwned(p *playerRecord, reason string) *player.Update {
	if p == nil || p.left {
		return nil
	}
	state := p.snapshot()
	if p.java != nil {
		p.java.cancelLivePreparationOwned()
	}
	p.left = true
	p.active.Store(false)
	s.playerDirtyMu.Lock()
	delete(s.playerDirty, p)
	s.playerDirtyMu.Unlock()
	s.playersMu.Lock()
	delete(s.players, p.ID())
	s.playersMu.Unlock()
	if p.counted {
		p.counted = false
		s.online.Add(-1)
	}
	if p.avatar != nil {
		p.avatar.finish(player.ErrClosed)
	}
	if !p.admitted {
		if id := p.entityID.Load(); id != 0 {
			s.entities.Remove(id)
		}
		return nil
	}
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerLeft, event.PlayerLeft{Player: state, Reason: reason})
		s.bus.Publish(event.TopicPlayerLeave, map[string]string{"name": state.Profile.Name})
	}
	log.Printf("%s left the game", state.Profile.Name)
	if p.java != nil {
		s.broadcastSystemChatComponent(component{color: "yellow", translate: "multiplayer.player.left", with: []any{playerChatComponent(p.java)}})
	}
	s.removePlayerPresenceOwned(p)
	if id := p.entityID.Load(); id != 0 {
		s.entities.Remove(id)
	}
	return &player.Update{Kind: player.Removed, Player: state, Reason: reason}
}

func (s *Server) retirePlayerOwned(p *playerRecord, reason string) {
	if update := s.retirePlayerStateOwned(p, reason); update != nil {
		s.publishPlayerUpdateOwned(*update, nil)
	}
}

// Finish the current fanout before retiring failed recipients. An iterative
// worklist preserves revision order even when removal overflows another stream.
func (s *Server) publishPlayerUpdateOwned(update player.Update, except *playerRecord) {
	type publication struct {
		update player.Update
		except *playerRecord
	}
	queue := []publication{{update, except}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.update.Revision == 0 {
			s.playerRevision++
			current.update.Revision = s.playerRevision
		}
		var failed []*playerRecord
		for _, p := range s.activePlayersOwned() {
			if p != current.except && p.avatar != nil && !p.avatar.mailbox.tryPublish(current.update) {
				failed = append(failed, p)
			}
		}
		for _, p := range failed {
			if removed := s.retirePlayerStateOwned(p, "Replication stream closed"); removed != nil {
				queue = append(queue, publication{update: *removed})
			}
		}
	}
}

func (s *Server) publishPlayerMovedOwned(p *playerRecord) {
	s.markPlayerDirty(p)
}

func (s *Server) correctPlayerOwned(p *playerRecord, pose player.Pose) {
	if p.java != nil {
		_ = s.sendSyncPos(p.java, pose.X, pose.Y, pose.Z)
		return
	}
	if !p.active.Load() || p.avatar == nil {
		return
	}
	s.playerRevision++
	state := p.snapshot()
	state.Pose = pose
	if !p.avatar.mailbox.tryPublish(player.Update{Revision: s.playerRevision, Kind: player.Correction, Player: state}) {
		s.retirePlayerOwned(p, "Replication stream closed")
	}
}

func (s *Server) retireAvatarsOwned(reason string) {
	for _, p := range s.activePlayersOwned() {
		if p.avatar != nil {
			s.retirePlayerOwned(p, reason)
		}
	}
}

func closedPlayerError(err error) error {
	if err == nil || err == net.ErrClosed {
		return player.ErrClosed
	}
	return err
}
