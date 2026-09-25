package server

import (
	"sort"
	"strings"
	"time"

	"encoding/json"

	"mc-go/internal/protocol"
)

// registerAdminCommands wires /op /deop /kick /ban /pardon /ban-ip
// /pardon-ip — the operator set that manages access rather than the
// world.
func (s *Server) registerAdminCommands() {
	s.registerComponentCommand("op", func(player string, args []string) reply {
		return s.runOp(player, args)
	})
	s.registerComponentCommand("deop", func(player string, args []string) reply {
		return s.runDeop(player, args)
	})
	s.registerComponentCommand("kick", func(player string, args []string) reply {
		return s.runKickCmd(player, args)
	})
	s.registerComponentCommand("ban", func(player string, args []string) reply {
		return s.runBan(player, args)
	})
	s.registerComponentCommand("pardon", func(player string, args []string) reply {
		return s.runPardon(player, args)
	})
	s.registerComponentCommand("ban-ip", func(player string, args []string) reply {
		return s.runBanIP(player, args)
	})
	s.registerComponentCommand("pardon-ip", func(player string, args []string) reply {
		return s.runPardonIP(player, args)
	})
}

// opName shows the name the way vanilla's reply does: the resolved
// profile name for online players, the typed name otherwise.
func (s *Server) opName(arg string) string {
	if sess := s.findSessionByName(arg); sess != nil {
		return sess.getName()
	}
	return arg
}

func (s *Server) runOp(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	name := s.opName(args[0])
	s.rulesMu.Lock()
	_, already := s.ops[name]
	if !already {
		s.ops[name] = 4
		s.opListAdd(name, 4)
	}
	s.rulesMu.Unlock()
	if already {
		return componentReply(commandError("commands.op.failed"))
	}
	if sess := s.findSessionByName(name); sess != nil {
		_ = s.sendEntityEvent(sess, sess.entityID.Load(), opStatus(4))
		_ = s.sendCommands(sess)
	}
	return opsEchoReply(translatable("commands.op.success", name))
}

func (s *Server) runDeop(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	name := s.opName(args[0])
	s.rulesMu.Lock()
	_, was := s.ops[name]
	if was {
		delete(s.ops, name)
		s.opListDel(name)
	}
	s.rulesMu.Unlock()
	if !was {
		return componentReply(commandError("commands.deop.failed"))
	}
	if sess := s.findSessionByName(name); sess != nil {
		_ = s.sendEntityEvent(sess, sess.entityID.Load(), opStatus(0))
		_ = s.sendCommands(sess)
	}
	return opsEchoReply(translatable("commands.deop.success", name))
}

// opStatus is the entity-event status byte vanilla sends a player when
// their permission level changes: 24 + level.
func opStatus(level int) byte { return byte(24 + level) }

// kickReason builds the disconnect component vanilla drops a kicked
// player with: the translated default for a bare /kick, the typed text
// for one with a reason.
func kickReason(args []string) component {
	if len(args) < 2 {
		return translatable("multiplayer.disconnect.kicked")
	}
	return literal(strings.Join(args[1:], " "))
}

func (s *Server) runKickCmd(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	target := s.findSessionByName(args[0])
	if target == nil || target.state.Load() != StatePlay {
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	reason := kickReason(args)
	s.disconnectWithComponent(target, reason)
	// The reply echoes the reason as an empty-key compound when typed.
	reasonArg := any(reason)
	if len(args) > 1 {
		reasonArg = nestedCompound{key: "", value: strings.Join(args[1:], " ")}
	}
	return opsEchoReply(translatable("commands.kick.success",
		playerChatComponent(target), reasonArg))
}

func (s *Server) runBan(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	name := args[0]
	// GameProfileArgument resolves to the profile's canonical name —
	// an online player's session name, the typed literal otherwise.
	target := s.findSessionByName(name)
	if target != nil {
		name = target.getName()
	}
	reason := translatable("multiplayer.disconnect.banned.reason.default")
	var reasonArg any = reason
	reasonText := "Banned by an operator."
	if len(args) > 1 {
		reasonText = strings.Join(args[1:], " ")
		reasonArg = reasonText
	}
	s.banMu.Lock()
	if s.bannedNames == nil {
		s.bannedNames = map[string]string{}
	}
	_, dup := s.bannedNames[name]
	if !dup {
		s.bannedNames[name] = reasonText
		s.banListAdd(name, reasonText, player)
	}
	s.banMu.Unlock()
	if dup {
		return componentReply(commandError("commands.ban.failed"))
	}
	success := translatable("commands.ban.success", name, reasonArg)
	issuer := s.findSessionByName(player)
	if issuer == nil {
		if target != nil && target.state.Load() == StatePlay {
			s.disconnectWithComponent(target,
				translatable("multiplayer.disconnect.banned.reason", reason))
		}
		return opsEchoReply(success)
	}
	// The issuer's reply and the operator broadcast land before the
	// kick — a ban on your own name still reports first.
	s.sendReply(issuer, opsEchoReply(success))
	if target != nil && target.state.Load() == StatePlay {
		s.disconnectWithComponent(target,
			translatable("multiplayer.disconnect.banned.reason", reason))
		s.waitUntracked(target)
	}
	return reply{}
}

// waitUntracked blocks until the session's read loop has untracked it
// — the deferred disconnect close and the leave broadcast ride that
// path — so follow-on work observes the player gone, the way vanilla's
// sequential disconnect handling publishes each removal in order.
func (s *Server) waitUntracked(sess *Session) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.sessions.Load(sess.id); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Server) runPardon(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	name := args[0]
	if sess := s.findSessionByName(name); sess != nil {
		name = sess.getName()
	}
	s.banMu.Lock()
	_, was := s.bannedNames[name]
	delete(s.bannedNames, name)
	s.banListDel(name)
	s.banMu.Unlock()
	if !was {
		return componentReply(commandError("commands.pardon.failed"))
	}
	return opsEchoReply(translatable("commands.pardon.success", name))
}

func (s *Server) runBanIP(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	ip := args[0]
	reason := translatable("multiplayer.disconnect.banned.reason.default")
	var reasonArg any = reason
	reasonText := "Banned by an operator."
	if len(args) > 1 {
		reasonText = strings.Join(args[1:], " ")
		reasonArg = reasonText
	}
	// The target is either a literal address or a player to resolve to
	// one; a name nobody has resolves nothing.
	if !isIPArg(ip) {
		sess := s.findSessionByName(ip)
		if sess == nil {
			return componentReply(commandError("commands.banip.invalid"))
		}
		ip = sessionIP(sess)
	}
	s.banMu.Lock()
	if s.bannedIPs == nil {
		s.bannedIPs = map[string]string{}
	}
	_, dup := s.bannedIPs[ip]
	if !dup {
		s.bannedIPs[ip] = reasonText
		s.ipBanListAdd(ip, reasonText, player)
	}
	s.banMu.Unlock()
	if dup {
		return componentReply(commandError("commands.banip.failed"))
	}
	// Connected players on the address drop with the ip-ban component.
	// The kicked list orders by join like vanilla's player list.
	var kicked []*Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sessionIP(sess) == ip {
			kicked = append(kicked, sess)
		}
		return true
	})
	sort.Slice(kicked, func(i, j int) bool { return kicked[i].id < kicked[j].id })
	success := translatable("commands.banip.success", ip, reasonArg)
	var info component
	if len(kicked) > 0 {
		var parts []component
		for i, sess := range kicked {
			if i > 0 {
				parts = append(parts, component{color: "gray", text: ", "})
			}
			parts = append(parts, s.entityTargetComponent(sess))
		}
		info = translatable("commands.banip.info",
			int32(len(kicked)), component{text: "", extra: parts})
	}
	issuer := s.findSessionByName(player)
	out := opsEchoReply(success)
	if len(kicked) > 0 {
		out = opsEchoReplyWith(success, info)
	}
	// A player issuer who banned their own address is kicked by this
	// command — the replies have to land first, so they flush inline
	// rather than through the post-dispatch reply path.
	if issuer == nil {
		return out
	}
	s.sendReply(issuer, out)
	for _, sess := range kicked {
		s.disconnectWithComponent(sess,
			translatable("multiplayer.disconnect.banned_ip.reason", reason))
		s.waitUntracked(sess)
	}
	return reply{}
}

func (s *Server) runPardonIP(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	ip := args[0]
	if !isIPArg(ip) {
		return componentReply(commandError("commands.pardonip.invalid"))
	}
	s.banMu.Lock()
	_, was := s.bannedIPs[ip]
	delete(s.bannedIPs, ip)
	s.ipBanListDel(ip)
	s.banMu.Unlock()
	if !was {
		return componentReply(commandError("commands.pardonip.failed"))
	}
	msg := translatable("commands.pardonip.success", ip)
	return opsEchoReply(msg)
}

// isIPArg recognizes a dotted-quad address the way vanilla's ban-ip
// literal does.
func isIPArg(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

// sessionIP is the numeric host part of the client's remote address.
func sessionIP(sess *Session) string {
	addr := sess.conn.RemoteAddr().String()
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// disconnectWithComponent kicks a play session with a component
// reason, the shape vanilla's disconnect packet carries.
func (s *Server) disconnectWithComponent(sess *Session, c component) {
	var b protocol.Buffer
	b.Raw(c.content())
	_ = sess.write(sess.ids().PlayDisconnect, b.Bytes())
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = sess.conn.Close()
	}()
}

// bannedReason reports the login-disconnect JSON for a banned name or
// banned source address, and whether either list matched.
func (s *Server) bannedReason(name string, sess *Session) (string, bool) {
	s.banMu.Lock()
	defer s.banMu.Unlock()
	if reason, ok := s.bannedNames[name]; ok {
		return `{"translate":"multiplayer.disconnect.banned.reason","with":[{"text":` +
			jsonString(reason) + `}]}`, true
	}
	if reason, ok := s.bannedIPs[sessionIP(sess)]; ok {
		return `{"translate":"multiplayer.disconnect.banned_ip.reason","with":[{"text":` +
			jsonString(reason) + `}]}`, true
	}
	return "", false
}

// jsonString renders one string as a JSON literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
