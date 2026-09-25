package server

import (
	"bytes"
	"strings"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// textComponentContent encodes a text component for chat packets. The
// 776 protocol carries components as NBT; a plain string is a
// TAG_String root, the compact form for text without styling.
func textComponentContent(text string) []byte {
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, text); err != nil {
		// Cannot happen for a string; fall back to an empty string tag.
		buf.Reset()
		buf.WriteByte(nbt.TagString)
		buf.Write([]byte{0, 0})
	}
	return buf.Bytes()
}

// ChatMessage is published on TopicChat for plugins.
type ChatMessage struct {
	Name    string
	Text    string
	Command bool
}

// handleChat processes a chat or chat-command packet. Only the leading
// message string is parsed; trailing signature/timestamp fields vary by
// version and are ignored (framing is length-prefixed, so skipping the
// tail is safe). Slash messages run plugin commands; anything else is
// broadcast. Vanilla relays as signed Player Chat; we use System Chat
// until the signing milestone.
func (s *Server) handleChat(sess *Session, p protocol.Packet, command bool) {
	r := protocol.NewPacketReader(p.Data)
	text, err := r.String()
	if err != nil || text == "" {
		return
	}
	if len(text) > 256 {
		text = text[:256]
	}
	name := sess.getName()
	if s.bus != nil {
		s.bus.Publish(event.TopicChat, ChatMessage{Name: name, Text: text, Command: command})
	}
	if command {
		text = "/" + text
	}
	if strings.HasPrefix(text, "/") {
		fields := splitCommandArgs(text[1:])
		if len(fields) == 0 {
			return
		}
		// The real server echoes the input it was given in the context
		// message that follows a failed parse, and marks the cursor by
		// position within it — including any trailing or doubled
		// whitespace.
		input := text[1:]
		r, ok := s.runCommand(name, fields[0], fields[1:])
		if !ok {
			// The command name itself is unknown, so the parse never got
			// past the start of the input.
			r = reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
		}
		// send_command_feedback gates every message a command emits —
		// replies and parse-error context alike.
		if s.gameRuleBool("minecraft:send_command_feedback") {
			s.sendReply(sess, r)
			if r.fail != nil {
				s.sendSystemChatComponent(sess, errorContext(input, r.fail.cursor(input)))
			}
		}
		return
	}
	s.broadcastPlayerChat(sess, text)
}

// sendPlayerChat relays one player's chat message with the vanilla
// Player Chat Message packet: unsigned content, no signature, the chat
// type from the synced registry, and per-sender/global ordering
// indices. Fallback is System Chat when the chat_type registry is not
// available.
func (s *Server) broadcastPlayerChat(sender *Session, text string) {
	chatType := registryEntryID("minecraft:chat_type", "minecraft:chat")
	if chatType < 0 {
		s.broadcastSystemChat("<" + sender.getName() + "> " + text)
		return
	}
	index := sender.chatIndex.Add(1) - 1
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		global := sess.chatGlobal.Add(1) - 1
		var b protocol.Buffer
		b.VarInt(global)
		b.UUID(sender.getUUID())
		b.VarInt(index)
		b.Bool(false) // no message signature
		b.String(text)
		b.Long(time.Now().UnixMilli())
		b.Long(0)    // salt
		b.VarInt(0)  // previous messages
		b.Bool(true) // unsigned content present
		b.Raw(textComponentContent(text))
		b.VarInt(0) // filter type: pass through
		b.VarInt(chatType + 1)
		b.Raw(textComponentContent(sender.getName()))
		b.Bool(false) // no target name
		_ = sess.write(sess.ids().PlayPlayerChat, b.Bytes())
		return true
	})
}

// splitCommandArgs tokenizes a command line the way brigadier's
// argument readers consume it: whitespace separates arguments, but
// text inside quotes and inside balanced {} / [] stays whole — an
// item token like dirt[custom_name='Cool Dirt'] is one argument.
func splitCommandArgs(text string) []string {
	var fields []string
	start := -1
	var quote byte
	depth := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if start < 0 {
			if c == ' ' || c == '\t' {
				continue
			}
			start = i
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '{', '[':
			depth++
		case '}', ']':
			if depth > 0 {
				depth--
			}
		case ' ', '\t':
			if depth == 0 {
				fields = append(fields, text[start:i])
				start = -1
			}
		}
	}
	if start >= 0 {
		fields = append(fields, text[start:])
	}
	return fields
}

// playerChatName is the decorated display name vanilla's
// Entity.getDisplayName produces for a player — the literal name plus
// a /tell suggestion click event, shift-click insertion, and a
// show_entity hover. The chat commands (/say, /me, /msg) bind it as
// their sender name.
func playerChatName(sess *Session) component {
	name := sess.getName()
	u := sess.getUUID()
	return component{
		text:      name,
		click:     "/tell " + name + " ",
		insertion: name,
		hover: map[string]any{
			"action": "show_entity",
			"id":     "minecraft:player",
			"name":   name,
			"uuid": []int32{
				int32(u[0])<<24 | int32(u[1])<<16 | int32(u[2])<<8 | int32(u[3]),
				int32(u[4])<<24 | int32(u[5])<<16 | int32(u[6])<<8 | int32(u[7]),
				int32(u[8])<<24 | int32(u[9])<<16 | int32(u[10])<<8 | int32(u[11]),
				int32(u[12])<<24 | int32(u[13])<<16 | int32(u[14])<<8 | int32(u[15]),
			},
		},
	}
}

// sendDisguisedChat emits one Disguised Chat packet to a session —
// vanilla's wire path for the unsigned system-sender messages /say,
// /me and /msg produce. The content travels as a component and the
// chat_type binding (registry ref + sender name + optional target
// name) decorates it on the client.
func sendDisguisedChat(sess *Session, chatType int32, name component, content component, target *component) {
	var b protocol.Buffer
	b.Raw(content.content())
	b.VarInt(chatType + 1) // holder ref: registry id + 1
	b.Raw(name.content())
	if target != nil {
		b.Bool(true)
		b.Raw(target.content())
	} else {
		b.Bool(false)
	}
	_ = sess.write(sess.ids().PlayDisguisedChat, b.Bytes())
}

// broadcastDisguisedChat delivers a decorated message to every
// play-state session — the /say and /me shape.
func (s *Server) broadcastDisguisedChat(msgType string, name component, content component) {
	chatType := registryEntryID("minecraft:chat_type", "minecraft:"+msgType)
	if chatType < 0 {
		s.broadcastSystemChat(name.text + " " + content.text)
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		sendDisguisedChat(sess, chatType, name, content, nil)
		return true
	})
}

// sendSystemChatComponent delivers a component to one session, which
// is how the built-in commands answer: vanilla words their replies as
// translatable components.
func (s *Server) sendSystemChatComponent(sess *Session, c component) {
	_ = sess.write(sess.ids().PlaySystemChatCB, systemChatBody(c))
}

// systemChatBody encodes one non-overlay System Chat packet body for a
// component, shared by the single-session reply and the admin
// broadcast.
func systemChatBody(c component) []byte {
	var b protocol.Buffer
	b.Raw(c.content())
	b.Bool(false) // overlay
	return b.Bytes()
}

// sendActionBar delivers a component on the action bar: a System Chat
// packet with overlay set. Vanilla 26.2 answers an in-reach use that
// arrives before the client confirmed its teleports with the
// build.tooHigh action bar (see sendBuildTooHigh).
func (s *Server) sendActionBar(sess *Session, c component) {
	var b protocol.Buffer
	b.Raw(c.content())
	b.Bool(true) // overlay: action bar
	_ = sess.write(sess.ids().PlaySystemChatCB, b.Bytes())
}

// sendReply delivers whatever a command produced to its caller.
func (s *Server) sendReply(sess *Session, r reply) {
	if r.styled {
		s.sendSystemChatComponent(sess, r.comp)
		for _, c := range r.extra {
			s.sendSystemChatComponent(sess, c)
		}
		if r.opsEcho {
			s.sendOpsEcho(sess, r.comp)
			for _, c := range r.extra {
				s.sendOpsEcho(sess, c)
			}
		}
		return
	}
	if r.text != "" {
		s.sendSystemChat(sess, r.text)
	}
}

// sendSystemChat delivers a plain-text server message to one session.
func (s *Server) sendSystemChat(sess *Session, text string) {
	var b protocol.Buffer
	b.Raw(textComponentContent(text))
	b.Bool(false) // overlay
	_ = sess.write(sess.ids().PlaySystemChatCB, b.Bytes())
}

// sendOpsEcho mirrors CommandSourceStack.broadcastToAdmins: a command
// success flagged broadcastToOps reaches every other op as the gray
// italic admin line — chat.type.admin, [<name>: <message>] — while
// send_command_feedback is on. from is the session whose command
// produced the reply; nil is the server's own source, which is what
// the sprint report broadcasts through.
func (s *Server) sendOpsEcho(from *Session, c component) {
	if !s.gameRuleBool("minecraft:send_command_feedback") {
		return
	}
	name := component{text: "Server"}
	if from != nil {
		name = component{text: from.getName()}
	}
	echo := component{
		translate: "chat.type.admin",
		with:      []any{name, c},
		color:     "gray",
		italic:    true,
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess == from || !s.isOp(sess.getName()) {
			return true
		}
		s.sendSystemChatComponent(sess, echo)
		return true
	})
}

// isOp reports whether the name holds an ops.json entry — the list
// broadcastToAdmins sends to, regardless of OpsRequired.
func (s *Server) isOp(name string) bool {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	_, ok := s.ops[name]
	return ok
}

// broadcastSystemChatComponent delivers a component to every play-stage
// session — vanilla's death-message and advancement-announce fan-outs.
func (s *Server) broadcastSystemChatComponent(c component) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		s.sendSystemChatComponent(sess, c)
		return true
	})
}

// broadcastSystemChat delivers a plain-text message to every play-stage session.
func (s *Server) broadcastSystemChat(text string) {
	var b protocol.Buffer
	b.Raw(textComponentContent(text))
	b.Bool(false)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(sess.ids().PlaySystemChatCB, body)
		return true
	})
}

// broadcastSystemChatComponentExcept delivers a component to every
// play-stage session except one.
func (s *Server) broadcastSystemChatComponentExcept(except *Session, c component) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay {
			return true
		}
		s.sendSystemChatComponent(sess, c)
		return true
	})
}

// broadcastSystemChatExcept delivers to every play-stage session except one.
// Join messages use this: vanilla notifies existing players, not the joiner.
func (s *Server) broadcastSystemChatExcept(except *Session, text string) {
	var b protocol.Buffer
	b.Raw(textComponentContent(text))
	b.Bool(false)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(sess.ids().PlaySystemChatCB, body)
		return true
	})
}
