package server

import (
	"crypto/rand"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
)

// bossBar is one /bossbar bar: id, display component, fill and
// visibility state, and the sessions currently viewing it.
type bossBar struct {
	id      string
	name    any
	color   string
	style   string
	value   int32
	max     int32
	visible bool
	uuid    [16]byte
	players map[*Session]bool
}

// bossBarColors is vanilla's BossBarColor order — the packet index and
// the chat formatting each maps to in a bar's hoverable name.
var bossBarColors = map[string]struct {
	idx       int32
	textColor string
}{
	"pink":   {0, "light_purple"},
	"blue":   {1, "blue"},
	"red":    {2, "dark_red"},
	"green":  {3, "green"},
	"yellow": {4, "yellow"},
	"purple": {5, "dark_blue"},
	"white":  {6, "white"},
}

// bossBarStyles is vanilla's BossEvent.BossBarOverlay order.
var bossBarStyles = map[string]int32{
	"progress":   0,
	"notched_6":  1,
	"notched_10": 2,
	"notched_12": 3,
	"notched_20": 4,
}

// registerBossbar wires /bossbar.
func (s *Server) registerBossbar() {
	s.registerComponentCommand("bossbar", func(player string, args []string) reply {
		return s.runBossbar(player, args)
	})
}

// bossBarComp is the hoverable [name] label every bossbar reply
// carries: the stored name inside chat.square_brackets, colored by the
// bar's color, with show_text hover and insertion for the id.
func (s *Server) bossBarComp(b *bossBar) component {
	var nameArg any
	switch v := b.name.(type) {
	case string:
		nameArg = v
	case map[string]any:
		nameArg = componentFromValue(v)
	default:
		nameArg = v
	}
	return component{
		color:     bossBarColors[b.color].textColor,
		translate: "chat.square_brackets",
		with:      []any{nameArg},
		hover:     map[string]any{"action": "show_text", "value": b.id},
		insertion: b.id,
	}
}

// componentFromValue wraps a normalized component map back into a
// component for with-list rendering. It only needs the shapes a
// parsed SNBT component produces: text/translate/color/extra.
func componentFromValue(v map[string]any) component {
	c := component{}
	if t, ok := v["text"].(string); ok {
		c.text = t
	}
	if t, ok := v["translate"].(string); ok {
		c.translate = t
	}
	if t, ok := v["color"].(string); ok {
		c.color = t
	}
	if ex, ok := v["extra"].([]any); ok {
		for _, it := range ex {
			c.extra = append(c.extra, componentFromAny(it))
		}
	}
	return c
}

func componentFromAny(v any) component {
	switch t := v.(type) {
	case string:
		return component{text: t}
	case map[string]any:
		return componentFromValue(t)
	}
	return component{}
}

func (s *Server) runBossbar(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	sender := s.findSessionByName(player)
	switch args[0] {
	case "add":
		return s.runBossbarAdd(sender, args)
	case "remove":
		return s.runBossbarRemove(args)
	case "list":
		return s.runBossbarList(args)
	case "get":
		return s.runBossbarGet(args)
	case "set":
		return s.runBossbarSet(sender, args)
	default:
		return errToken("command.unknown.argument", 1)
	}
}

// bossbarID normalizes a bossbar id argument — bare names take the
// minecraft: namespace.
func bossbarID(arg string) string {
	if !strings.Contains(arg, ":") {
		return "minecraft:" + arg
	}
	return arg
}

func (s *Server) findBar(arg string) *bossBar {
	s.bossbarMu.Lock()
	defer s.bossbarMu.Unlock()
	return s.bossbars[bossbarID(arg)]
}

func (s *Server) runBossbarAdd(sender *Session, args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	name, _, serr := parseComponentArg(strings.Join(args[2:], " "))
	if serr != nil {
		return errToken("command.unknown.argument", 2)
	}
	id := bossbarID(args[1])
	s.bossbarMu.Lock()
	if _, exists := s.bossbars[id]; exists {
		s.bossbarMu.Unlock()
		return componentReply(commandError("commands.bossbar.create.failed", id))
	}
	b := &bossBar{
		id:      id,
		name:    compNormalize(name),
		color:   "white",
		style:   "progress",
		max:     100,
		visible: true,
		players: map[*Session]bool{},
	}
	_, _ = rand.Read(b.uuid[:])
	b.uuid[6] = (b.uuid[6] & 0x0f) | 0x40
	b.uuid[8] = (b.uuid[8] & 0x3f) | 0x80
	s.bossbars[id] = b
	s.bossbarOrder = append(s.bossbarOrder, id)
	s.bossbarMu.Unlock()
	return opsEchoReply(translatable("commands.bossbar.create.success",
		s.bossBarComp(b)))
}

func (s *Server) runBossbarRemove(args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", 2)
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	s.bossbarMu.Lock()
	b, ok := s.bossbars[bossbarID(args[1])]
	if ok {
		delete(s.bossbars, b.id)
		for i, id := range s.bossbarOrder {
			if id == b.id {
				s.bossbarOrder = append(s.bossbarOrder[:i], s.bossbarOrder[i+1:]...)
				break
			}
		}
	}
	s.bossbarMu.Unlock()
	if !ok {
		return componentReply(commandError("commands.bossbar.unknown", bossbarID(args[1])))
	}
	s.bossbarSend(b, 1, nil)
	return opsEchoReply(translatable("commands.bossbar.remove.success",
		s.bossBarComp(b)))
}

func (s *Server) runBossbarList(args []string) reply {
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	s.bossbarMu.Lock()
	ids := append([]string(nil), s.bossbarOrder...)
	s.bossbarMu.Unlock()
	if len(ids) == 0 {
		return componentReply(translatable("commands.bossbar.list.bars.none"))
	}
	join := component{text: ""}
	for i, id := range ids {
		if i > 0 {
			join.extra = append(join.extra, component{color: "gray", text: ", "})
		}
		join.extra = append(join.extra, s.bossBarComp(s.bossbars[id]))
	}
	return componentReply(translatable("commands.bossbar.list.bars.some",
		nestedCompound{key: "", value: int32(len(ids))}, join))
}

func (s *Server) runBossbarGet(args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	b := s.findBar(args[1])
	if b == nil {
		return componentReply(commandError("commands.bossbar.unknown", bossbarID(args[1])))
	}
	bc := s.bossBarComp(b)
	switch args[2] {
	case "max":
		return componentReply(translatable("commands.bossbar.get.max",
			bc, nestedCompound{key: "", value: b.max}))
	case "value":
		return componentReply(translatable("commands.bossbar.get.value",
			bc, nestedCompound{key: "", value: b.value}))
	case "players":
		if len(b.players) == 0 {
			return componentReply(translatable("commands.bossbar.get.players.none", bc))
		}
		return componentReply(translatable("commands.bossbar.get.players.some",
			bc, nestedCompound{key: "", value: int32(len(b.players))},
			bossbarPlayerNames(b)))
	case "visible":
		key := "commands.bossbar.get.visible.visible"
		if !b.visible {
			key = "commands.bossbar.get.visible.hidden"
		}
		return opsEchoReply(translatable(key, bc))
	default:
		return errToken("command.unknown.argument", 3)
	}
}

func (s *Server) runBossbarSet(sender *Session, args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) < 4 && args[2] != "players" {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	b := s.findBar(args[1])
	if b == nil {
		return componentReply(commandError("commands.bossbar.unknown", bossbarID(args[1])))
	}
	bc := func() component { return s.bossBarComp(b) }
	switch args[2] {
	case "max", "value":
		n, err := parseIntArg(args[3])
		if err != nil {
			return errToken("parsing.int.expected", 4)
		}
		if n < 0 {
			return errToken("argument.integer.low", 4, int32(0), n)
		}
		if len(args) > 4 {
			return errToken("command.unknown.argument", 5)
		}
		if args[2] == "max" {
			b.max = n
			s.bossbarUpdateProgress(b)
			return opsEchoReply(translatable("commands.bossbar.set.max.success",
				bc(), nestedCompound{key: "", value: n}))
		}
		b.value = n
		s.bossbarUpdateProgress(b)
		return opsEchoReply(translatable("commands.bossbar.set.value.success",
			bc(), nestedCompound{key: "", value: n}))
	case "color":
		if _, ok := bossBarColors[args[3]]; !ok {
			return errToken("command.unknown.argument", 4)
		}
		b.color = args[3]
		s.bossbarUpdateStyle(b)
		return opsEchoReply(translatable("commands.bossbar.set.color.success", bc()))
	case "style":
		if _, ok := bossBarStyles[args[3]]; !ok {
			return errToken("command.unknown.argument", 4)
		}
		b.style = args[3]
		s.bossbarUpdateStyle(b)
		return opsEchoReply(translatable("commands.bossbar.set.style.success", bc()))
	case "visible":
		if args[3] != "true" && args[3] != "false" {
			return errToken("parsing.bool.invalid", 4, args[3])
		}
		v := args[3] == "true"
		if v == b.visible {
			key := "commands.bossbar.set.visibility.unchanged.visible"
			if !v {
				key = "commands.bossbar.set.visibility.unchanged.hidden"
			}
			return componentReply(commandError(key))
		}
		b.visible = v
		if v {
			// Re-showing pushes the full ADD again.
			s.bossbarSend(b, 0, s.bossbarAddPayload(b))
			return opsEchoReply(translatable("commands.bossbar.set.visible.success.visible", bc()))
		}
		s.bossbarSend(b, 1, nil)
		return opsEchoReply(translatable("commands.bossbar.set.visible.success.hidden", bc()))
	case "name":
		v, _, serr := parseComponentArg(strings.Join(args[3:], " "))
		if serr != nil {
			return errToken("command.unknown.argument", 4)
		}
		b.name = compNormalize(v)
		var p protocol.Buffer
		p.Raw(componentBytes(b.name))
		s.bossbarSend(b, 3, p.Bytes())
		return opsEchoReply(translatable("commands.bossbar.set.name.success", bc()))
	case "players":
		if len(args) > 4 {
			return errToken("command.unknown.argument", 5)
		}
		want := map[*Session]bool{}
		if len(args) == 4 {
			if args[3][0] == '@' {
				targets, rep := s.selectDataEntities(sender, args[3], 4, false, nil)
				if rep != nil {
					return *rep
				}
				for _, d := range targets {
					if d.sess != nil {
						want[d.sess] = true
					}
				}
			} else if sess := s.findSessionByName(args[3]); sess != nil {
				want[sess] = true
			}
		}
		if samePlayerSet(b.players, want) {
			return componentReply(commandError("commands.bossbar.set.players.unchanged"))
		}
		s.bossbarSetPlayers(b, want)
		if len(b.players) == 0 {
			return opsEchoReply(translatable("commands.bossbar.set.players.success.none", bc()))
		}
		return opsEchoReply(translatable("commands.bossbar.set.players.success.some",
			bc(), nestedCompound{key: "", value: int32(len(b.players))},
			bossbarPlayerNames(b)))
	default:
		return errToken("command.unknown.argument", 3)
	}
}

// bossbarPlayerNames renders the member list as a join of decorated
// player names with ", " separators.
func bossbarPlayerNames(b *bossBar) component {
	sess := make([]*Session, 0, len(b.players))
	for p := range b.players {
		sess = append(sess, p)
	}
	// insertion order is not tracked; single-member bars dominate, so
	// sort by name for determinism.
	sort.Slice(sess, func(i, j int) bool { return sess[i].getName() < sess[j].getName() })
	if len(sess) == 1 {
		return playerChatComponent(sess[0])
	}
	join := component{text: ""}
	for i, p := range sess {
		if i > 0 {
			join.extra = append(join.extra, literal(", "))
		}
		join.extra = append(join.extra, playerChatComponent(p))
	}
	return join
}

func samePlayerSet(a, b map[*Session]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for p := range a {
		if !b[p] {
			return false
		}
	}
	return true
}

// bossbarSetPlayers swaps the member set: removed players get REMOVE,
// added players get the full ADD.
func (s *Server) bossbarSetPlayers(b *bossBar, want map[*Session]bool) {
	for p := range b.players {
		if !want[p] {
			_ = p.write(p.ids().PlayBossEvent, bossbarEvent(b, 1, nil))
		}
	}
	for p := range want {
		if !b.players[p] && p.state.Load() == StatePlay {
			_ = p.write(p.ids().PlayBossEvent, bossbarEvent(b, 0, s.bossbarAddPayload(b)))
		}
	}
	b.players = want
}

// bossbarSend emits a boss_event with the given op to every member.
func (s *Server) bossbarSend(b *bossBar, op int32, payload []byte) {
	for p := range b.players {
		if p.state.Load() != StatePlay {
			continue
		}
		_ = p.write(p.ids().PlayBossEvent, bossbarEvent(b, op, payload))
	}
}

// bossbarEvent writes [uuid][op][payload].
func bossbarEvent(b *bossBar, op int32, payload []byte) []byte {
	var p protocol.Buffer
	p.UUID(b.uuid)
	p.VarInt(op)
	p.Raw(payload)
	return p.Bytes()
}

// bossbarAddPayload is the op-0 body: name, progress, color, style,
// property flags.
func (s *Server) bossbarAddPayload(b *bossBar) []byte {
	var p protocol.Buffer
	p.Raw(componentBytes(b.name))
	var prog float32
	if b.max > 0 {
		prog = float32(b.value) / float32(b.max)
		if prog > 1 {
			prog = 1
		}
	}
	p.Float(prog)
	p.VarInt(bossBarColors[b.color].idx)
	p.VarInt(bossBarStyles[b.style])
	p.Byte(0)
	return p.Bytes()
}

func (s *Server) bossbarUpdateProgress(b *bossBar) {
	var p protocol.Buffer
	var prog float32
	if b.max > 0 {
		prog = float32(b.value) / float32(b.max)
		if prog > 1 {
			prog = 1
		}
	}
	p.Float(prog)
	s.bossbarSend(b, 2, p.Bytes())
}

func (s *Server) bossbarUpdateStyle(b *bossBar) {
	var p protocol.Buffer
	p.VarInt(bossBarColors[b.color].idx)
	p.VarInt(bossBarStyles[b.style])
	s.bossbarSend(b, 4, p.Bytes())
}

// parseIntArg is the plain int32 read shared by the int-valued
// subcommands.
func parseIntArg(tok string) (int32, error) {
	n, err := strconv.ParseInt(tok, 10, 32)
	return int32(n), err
}
