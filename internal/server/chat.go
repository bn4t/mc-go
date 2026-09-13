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
		fields := strings.Fields(text[1:])
		if len(fields) == 0 {
			return
		}
		// The real server echoes the input it was given in the context
		// message that follows a failed parse, and marks the cursor by
		// position within it. Fields were split on whitespace, so the
		// input is rebuilt the same way: a client that double-spaced its
		// command would see its own spacing in the real server's reply
		// and single spacing here.
		input := strings.Join(fields, " ")
		r, ok := s.runCommand(name, fields[0], fields[1:])
		if !ok {
			// The command name itself is unknown, so the parse never got
			// past the start of the input.
			r = reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
		}
		s.sendReply(sess, r)
		if r.fail != nil {
			s.sendSystemChatComponent(sess, errorContext(input, r.fail.cursor(input)))
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

// sendSystemChatComponent delivers a component to one session, which
// is how the built-in commands answer: vanilla words their replies as
// translatable components.
func (s *Server) sendSystemChatComponent(sess *Session, c component) {
	var b protocol.Buffer
	b.Raw(c.content())
	b.Bool(false) // overlay
	_ = sess.write(sess.ids().PlaySystemChatCB, b.Bytes())
}

// sendReply delivers whatever a command produced to its caller.
func (s *Server) sendReply(sess *Session, r reply) {
	if r.styled {
		s.sendSystemChatComponent(sess, r.comp)
		for _, c := range r.extra {
			s.sendSystemChatComponent(sess, c)
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
