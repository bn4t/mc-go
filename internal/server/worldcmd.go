package server

import (
	"fmt"

	"math"
	"mc-go/internal/nbt"
	"sort"
	"strconv"
	"strings"
)

// worldcmd.go covers the world-state command cluster: /forceload's
// per-dimension forced-chunk set, /fillbiome's quart-granularity biome
// writes, /datapack's builtin pack table, /help's smart-usage list,
// /version's identity block, /waypoint's locator-bar entries, and
// /swing's animation broadcast. Replies follow the measured 26.2 wire
// components field for field.

func (s *Server) registerWorldStateCommands() {
	s.registerComponentCommand("forceload", s.runForceLoad)
	s.registerComponentCommand("fillbiome", s.runFillBiome)
	s.registerComponentCommand("datapack", s.runDataPack)
	s.registerComponentCommand("help", s.runHelp)
	s.registerComponentCommand("version", s.runVersion)
	s.registerComponentCommand("waypoint", s.runWaypoint)
	s.registerComponentCommand("swing", s.runSwing)
}

// ---------------------------------------------------------------- /forceload

// forcedChunks is the per-dimension forced-chunk set /forceload mutates,
// keyed by dimension name then by ChunkPos.toLong (z in the high word).
type forcedChunkSet map[string]map[int64]bool

func (s *Server) forceLoadSet(dim string) map[int64]bool {
	if s.forcedChunks == nil {
		s.forcedChunks = forcedChunkSet{}
	}
	m := s.forcedChunks[dim]
	if m == nil {
		m = map[int64]bool{}
		s.forcedChunks[dim] = m
	}
	return m
}

// parseColumnPos parses a pos2d argument pair (ints or ~relative to the
// sender's feet) starting at args[i], where tokBase is the pair's input
// token index.
func parseColumnPos(sess *Session, args []string, i, tokBase int) (int, int, *reply) {
	if len(args) <= i {
		r := errTokenEnd("command.unknown.command", tokBase)
		return 0, 0, &r
	}
	if len(args) == i+1 {
		if _, err := strconv.ParseInt(strings.TrimPrefix(args[i], "~"), 10, 64); err == nil {
			r := errToken("argument.pos2d.incomplete", tokBase)
			return 0, 0, &r
		}
		// falls through: the bad first token reports parsing.int.* below
	}
	var bx, bz float64
	if sess != nil {
		px, _, pz, _, _, _ := sess.Pos()
		bx, bz = math.Floor(px), math.Floor(pz)
	}
	out := [2]int{}
	base := [2]float64{bx, bz}
	for j := 0; j < 2 && i+j < len(args); j++ {
		tok := args[i+j]
		v, rel := tok, false
		if strings.HasPrefix(v, "~") {
			rel, v = true, v[1:]
		}
		n := 0
		if v != "" {
			num, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				var r reply
				if _, perr := strconv.ParseInt(v, 10, 64); perr == nil {
					r = errToken("parsing.int.invalid", tokBase+j, tok)
				} else {
					r = errToken("parsing.int.expected", tokBase+j)
				}
				return 0, 0, &r
			}
			n = int(num)
		}
		if rel {
			n += int(base[j])
		}
		out[j] = n
	}
	return out[0], out[1], nil
}

// chunkLong is ChunkPos.toLong: z in the high word, x low. Sorting these
// keys reproduces vanilla's listing order.
func chunkLong(x, z int) int64 {
	return int64(uint32(z))<<32 | int64(uint32(x))
}

// chunkStr is ChunkPos's chat form, "[x, z]".
func chunkStr(x, z int) string { return fmt.Sprintf("[%d, %d]", x, z) }

func (s *Server) runForceLoad(player string, args []string) reply {
	sess := s.findSessionByName(player)
	dim := dimOverworld
	if sess != nil {
		dim = sess.dimName()
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "add", "remove":
		add := args[0] == "add"
		if !add && len(args) > 1 && args[1] == "all" {
			if len(args) > 2 {
				return errToken("command.unknown.argument", 3)
			}
			s.forceLoadSet(dim)
			for k := range s.forcedChunks[dim] {
				delete(s.forcedChunks[dim], k)
			}
			return componentReply(translatable("commands.forceload.removed.all", dim))
		}
		fx, fz, r := parseColumnPos(sess, args, 1, 2)
		if r != nil {
			return *r
		}
		tx, tz := fx, fz
		if len(args) > 3 {
			tx, tz, r = parseColumnPos(sess, args, 3, 4)
			if r != nil {
				return *r
			}
		} else if len(args) == 3 {
			// `add <x> <z>` with nothing after is fine: from == to.
		}
		if len(args) > 5 {
			return errToken("command.unknown.argument", 6)
		}
		clamp := func(v int) int {
			return max(-30000000, min(30000000, v))
		}
		x0, x1 := clamp(fx)>>4, clamp(tx)>>4
		z0, z1 := clamp(fz)>>4, clamp(tz)>>4
		if x0 > x1 {
			x0, x1 = x1, x0
		}
		if z0 > z1 {
			z0, z1 = z1, z0
		}
		total := int64(x1-x0+1) * int64(z1-z0+1)
		if total > 256 {
			return componentReply(commandError("commands.forceload.toobig", int32(256), total))
		}
		set := s.forceLoadSet(dim)
		changed := 0
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				k := chunkLong(x, z)
				if add && !set[k] {
					set[k] = true
					changed++
				} else if !add && set[k] {
					delete(set, k)
					changed++
				}
			}
		}
		verb := "added"
		if !add {
			verb = "removed"
		}
		switch {
		case changed == 0:
			return componentReply(commandError("commands.forceload." + verb + ".failure"))
		case changed == 1:
			return componentReply(translatable("commands.forceload."+verb+".single",
				chunkStr(x0, z0), dim))
		default:
			return componentReply(translatable("commands.forceload."+verb+".multiple",
				nestedCompound{key: "", value: int32(changed)},
				nestedCompound{key: "", value: dim},
				nestedCompound{key: "", value: chunkStr(x0, z0)},
				nestedCompound{key: "", value: chunkStr(x1, z1)}))
		}
	case "query":
		set := s.forceLoadSet(dim)
		if len(args) == 1 {
			if len(set) == 0 {
				return componentReply(commandError("commands.forceload.added.none", dim))
			}
			keys := make([]int64, 0, len(set))
			for k := range set {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
			strs := make([]string, len(keys))
			for i, k := range keys {
				strs[i] = chunkStr(int(int32(k&0xffffffff)), int(int32(k>>32)))
			}
			if len(keys) == 1 {
				return componentReply(translatable("commands.forceload.list.single",
					dim, strs[0]))
			}
			return componentReply(translatable("commands.forceload.list.multiple",
				nestedCompound{key: "", value: int32(len(keys))},
				nestedCompound{key: "", value: dim},
				nestedCompound{key: "", value: strings.Join(strs, ", ")}))
		}
		x, z, r := parseColumnPos(sess, args, 1, 2)
		if r != nil {
			return *r
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 4)
		}
		cl := func(v int) int { return max(-30000000, min(30000000, v)) >> 4 }
		cx, cz := cl(x), cl(z)
		if set[chunkLong(cx, cz)] {
			return componentReply(translatable("commands.forceload.query.success",
				chunkStr(cx, cz), dim))
		}
		return componentReply(commandError("commands.forceload.query.failure",
			chunkStr(cx, cz), dim))
	}
	return errToken("command.unknown.argument", 1)
}

// ---------------------------------------------------------------- /fillbiome

// runFillBiome implements "fillbiome <from> <to> <biome> [replace
// [<filter>]]": positions quantize to quart space, the volume bound is
// the max_block_modifications game rule applied to the quantized box,
// and an out-of-loaded-range position fails argument.pos.unloaded
// without a context echo.
func (s *Server) runFillBiome(player string, args []string) reply {
	sess := s.findSessionByName(player)
	pos := func(args []string, i, tokBase int) ([3]int, *reply) {
		if len(args) <= i {
			r := errTokenEnd("command.unknown.command", tokBase)
			return [3]int{}, &r
		}
		if len(args)-i < 3 {
			r := errToken("argument.pos3d.incomplete", tokBase)
			return [3]int{}, &r
		}
		return s.parseBlockPos(sess, args[i:], tokBase)
	}
	from, r := pos(args, 0, 1)
	if r != nil {
		return *r
	}
	to, r := pos(args, 3, 4)
	if r != nil {
		return *r
	}
	if len(args) <= 6 {
		return errTokenEnd("command.unknown.command", 7)
	}
	biome := strings.TrimPrefix(args[6], "minecraft:")
	if !biomeNames[biome] {
		return errTokenEnd("argument.resource.not_found", 7,
			args[6], "minecraft:worldgen/biome")
	}
	filter := ""
	if len(args) > 7 {
		if args[7] != "replace" {
			return errToken("command.unknown.argument", 8)
		}
		if len(args) == 8 {
			return errTokenEnd("command.unknown.command", 8)
		}
		if len(args) > 8 {
			filter = strings.TrimPrefix(args[8], "minecraft:")
			if !biomeNames[filter] {
				return errTokenEnd("argument.resource.not_found", 9,
					args[8], "minecraft:worldgen/biome")
			}
		}
	}
	if len(args) > 9 {
		return errToken("command.unknown.argument", 10)
	}
	d := s.dimOf(player)
	// Vanilla checks loaded status on the unquantized endpoints.
	for _, p := range [2][3]int{from, to} {
		if !s.blockPosLoaded(d, sess, p[0], p[1], p[2]) {
			return componentReply(commandError("argument.pos.unloaded"))
		}
	}
	q := func(v int) int { return (v >> 2) << 2 }
	q0, q1 := [3]int{q(from[0]), q(from[1]), q(from[2])}, [3]int{q(to[0]), q(to[1]), q(to[2])}
	span := func(a, b int) int {
		if a > b {
			a, b = b, a
		}
		return b/4 - a/4 + 1
	}
	limit := s.gameRuleInt("minecraft:max_block_modifications")
	vol := int64(span(q0[0], q1[0])) * int64(span(q0[1], q1[1])) * int64(span(q0[2], q1[2]))
	if vol > int64(limit) {
		return componentReply(commandError("commands.fillbiome.toobig",
			int32(limit), vol))
	}
	current := ""
	if d != nil {
		current = strings.TrimPrefix(d.w.Biome(), "minecraft:")
	}
	n := int64(0)
	if filter == "" || filter == current {
		n = vol
	}
	if d != nil {
		// Store the quart overrides gameplay reads through biomeAt;
		// the chunk palette still ships the base biome.
		d.biomesMu.Lock()
		if d.biomes == nil {
			d.biomes = map[[3]int]string{}
		}
		for qx := q0[0] / 4; qx <= q1[0]/4; qx++ {
			for qy := q0[1] / 4; qy <= q1[1]/4; qy++ {
				for qz := q0[2] / 4; qz <= q1[2]/4; qz++ {
					key := [3]int{qx, qy, qz}
					eff := current
					if prev, has := d.biomes[key]; has {
						eff = strings.TrimPrefix(prev, "minecraft:")
					}
					if filter == "" || eff == filter {
						d.biomes[key] = "minecraft:" + biome
					}
				}
			}
		}
		d.biomesMu.Unlock()
	}
	return componentReply(translatable("commands.fillbiome.success.count",
		int32(n), int32(from[0]), int32(from[1]), int32(from[2]),
		int32(to[0]), int32(to[1]), int32(to[2])))
}

// blockPosLoaded reports whether a block position lies in a loaded
// chunk of the dimension — inside the height range and inside a chunk
// some play-stage session of that dimension has been sent.
func (s *Server) blockPosLoaded(d *dimension, sess *Session, x, y, z int) bool {
	if d == nil {
		return true
	}
	if y < d.minY || y >= d.minY+d.height {
		return false
	}
	if sess != nil && sess.state.Load() == StatePlay && sess.dimName() == d.name {
		return sess.hasChunk(x>>4, z>>4)
	}
	loaded := false
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() == StatePlay && other.dimName() == d.name &&
			other.hasChunk(x>>4, z>>4) {
			loaded = true
			return false
		}
		return true
	})
	return loaded
}

// gameRuleInt reads an integer-valued game rule.
func (s *Server) gameRuleInt(name string) int {
	s.rulesMu.RLock()
	v := s.gameRules[name]
	s.rulesMu.RUnlock()
	n, _ := strconv.Atoi(v)
	return n
}

// ---------------------------------------------------------------- /datapack

// dataPackComponent is the decorated pack label vanilla puts in
// datapack replies: [nameAndSource] with the pack's name/description on
// hover and its id as the shift-click insertion.
func dataPackComponent(name string) component {
	return component{
		translate: "chat.square_brackets",
		color:     "green",
		insertion: name,
		hover: map[string]any{
			"action": "show_text",
			"value": map[string]any{
				"text": "",
				"extra": nbt.List{Elem: nbt.TagCompound, Items: []any{
					map[string]any{"translate": "dataPack." + name + ".name"},
					map[string]any{"": "\n"},
					map[string]any{"translate": "dataPack." + name + ".description"},
				}},
			},
		},
		with: []any{component{
			color:     "gray",
			translate: "pack.nameAndSource",
			with: []any{
				nestedCompound{key: "", value: name},
				component{translate: "pack.source.builtin"},
			},
		}},
	}
}

// joinPackComponents is vanilla's ComponentUtils.formatList over pack
// labels: a single pack passes through as its own component; several
// become one compound joined by ", " literals.
func joinPackComponents(names []string) any {
	if len(names) == 1 {
		return dataPackComponent(names[0])
	}
	var parts []component
	for i, n := range names {
		if i > 0 {
			parts = append(parts, component{emptyKey: ", "})
		}
		parts = append(parts, dataPackComponent(n))
	}
	return component{extra: parts, forceCompound: true}
}

// builtinDataPacks is the pack set a fresh vanilla world loads.
var builtinDataPacks = []string{"vanilla"}

func (s *Server) runDataPack(player string, args []string) reply {
	if s.datapacks == nil {
		s.datapacks = map[string]bool{"vanilla": true}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	known := func(name string) bool {
		for _, p := range builtinDataPacks {
			if p == name {
				return true
			}
		}
		_, ok := s.customPacks[name]
		return ok
	}
	switch args[0] {
	case "list":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		var first, second component
		if len(s.datapacks) == 0 {
			first = translatable("commands.datapack.list.enabled.none")
		} else {
			names := make([]string, 0, len(s.datapacks))
			for p := range s.datapacks {
				names = append(names, p)
			}
			sort.Strings(names)
			first = translatable("commands.datapack.list.enabled.success",
				nestedCompound{key: "", value: int32(len(names))},
				joinPackComponents(names))
		}
		var avail []string
		for name := range s.customPacks {
			if !s.datapacks[name] {
				avail = append(avail, name)
			}
		}
		sort.Strings(avail)
		if len(avail) == 0 {
			second = translatable("commands.datapack.list.available.none")
		} else {
			second = translatable("commands.datapack.list.available.success",
				nestedCompound{key: "", value: int32(len(avail))},
				joinPackComponents(avail))
		}
		return componentReplyWith(first, second)
	case "enable", "disable":
		if len(args) < 2 {
			r := errTokenEnd("command.unknown.command", len(args)+1)
			return r
		}
		name := args[1]
		if !known(name) {
			return componentReply(commandError("commands.datapack.unknown", name))
		}
		// Optional position literal (first|last|before|after + name) is
		// accepted but ordering is trivial with a single builtin pack.
		if args[0] == "enable" {
			if s.datapacks[name] {
				return componentReply(commandError("commands.datapack.enable.failed", name))
			}
			s.datapacks[name] = true
			return componentReply(translatable("commands.datapack.modify.enable", dataPackComponent(name)))
		}
		if !s.datapacks[name] {
			return componentReply(commandError("commands.datapack.disable.failed", name))
		}
		delete(s.datapacks, name)
		return componentReply(translatable("commands.datapack.modify.disable", dataPackComponent(name)))
	case "create":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		name := args[1]
		if !validPackName(name) {
			return componentReply(commandError("commands.datapack.create.invalid_name", name))
		}
		if known(name) {
			return componentReply(commandError("commands.datapack.create.already_exists", name))
		}
		if s.customPacks == nil {
			s.customPacks = map[string]bool{}
		}
		s.customPacks[name] = true
		return componentReply(translatable("commands.datapack.create.success", name))
	}
	return errToken("command.unknown.argument", 1)
}

// validPackName accepts the characters vanilla's datapack file filter
// allows: lowercase letters, digits, and -._ plus / for nested paths.
func validPackName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '/':
		default:
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- /help

// helpSubUsage overrides a root's usage line for "help <name>" when the
// node's smart usage runs deeper than the root list shows — measured
// entries only; other roots fall back to their top-level line.
var helpSubUsage = map[string]string{
	"clear":  "/clear [<targets>] [<item>]",
	"summon": "/summon <entity> [<pos>]",
	"help":   "/help [<command>]",
}

// helpLines returns the usage lines a player at level sees — vanilla
// emits the smart usage of each root the level unlocks, in the
// registration order captured by helpUsageLines. /random and /datapack
// keep runnable-at-level-0/3 subcommands out of their usage text.
func helpLines(level int) []string {
	var out []string
	for _, u := range helpUsageLines {
		rest := strings.TrimPrefix(u, "/")
		root := rest
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			root = rest[:i]
		}
		if commandLevel(root) > level {
			continue
		}
		switch {
		case root == "random" && level < 2:
			u = "/random (value|roll)"
		case root == "datapack" && level < 4:
			u = "/datapack (enable|disable|list)"
		}
		out = append(out, u)
	}
	return out
}

func (s *Server) runHelp(player string, args []string) reply {
	level := s.permissionLevel(player)
	if len(args) == 0 {
		lines := helpLines(level)
		cs := make([]component, len(lines)-1)
		for i, u := range lines[1:] {
			cs[i] = literal(u)
		}
		return componentReplyWith(literal(lines[0]), cs...)
	}
	name := strings.TrimPrefix(args[0], "/")
	if commandLevel(name) > level {
		return componentReply(commandError("commands.help.failed"))
	}
	if u, ok := helpSubUsage[name]; ok {
		return componentReply(literal(u))
	}
	for _, u := range helpUsageLines {
		rest := strings.TrimPrefix(u, "/")
		root := rest
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			root = rest[:i]
		}
		if root == name {
			if strings.Contains(u, " -> ") {
				return reply{} // vanilla emits nothing for redirect aliases
			}
			return componentReply(literal(u))
		}
	}
	return componentReply(commandError("commands.help.failed"))
}

// ---------------------------------------------------------------- /version

func (s *Server) runVersion(player string, args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 1)
	}
	return componentReplyWith(
		translatable("commands.version.header"),
		translatable("commands.version.id", "26.2"),
		translatable("commands.version.name", "26.2"),
		translatable("commands.version.data", int32(4903)),
		translatable("commands.version.series", "main"),
		translatable("commands.version.protocol",
			nestedCompound{key: "", value: int32(776)},
			nestedCompound{key: "", value: "0x308"}),
		translatable("commands.version.build_time", "Tue Jun 16 12:01:27 UTC 2026"),
		translatable("commands.version.pack.resource", "88.0"),
		translatable("commands.version.pack.data", "107.1"),
		translatable("commands.version.stable.yes"),
	)
}

// ---------------------------------------------------------------- /waypoint

// runWaypoint implements "waypoint list" and "waypoint modify <entity>
// (color <color>|color hex <hex>|color reset|style set <style>|style
// reset)". The locator-bar waypoint a player carries is what list
// renders; modify edits that entity's waypoint fields.
func (s *Server) runWaypoint(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	sender := s.findSessionByName(player)
	dim := dimOverworld
	if sender != nil {
		dim = sender.dimName()
	}
	switch args[0] {
	case "list":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		var comps []component
		s.sessions.Range(func(_, v any) bool {
			other := v.(*Session)
			if other.state.Load() != StatePlay || other.dimName() != dim {
				return true
			}
			comps = append(comps, waypointEntryComponent(other, dim))
			return true
		})
		for i := range s.entities.All() {
			e := &s.entities.All()[i]
			if e.Dim != dim || !e.Waypoint {
				continue
			}
			comps = append(comps, waypointMobComponent(e, dim))
		}
		if len(comps) == 0 {
			return componentReply(translatable("commands.waypoint.list.empty", dim))
		}
		var joined component
		if len(comps) == 1 {
			joined = comps[0]
		} else {
			var parts []component
			for i, c := range comps {
				if i > 0 {
					parts = append(parts, component{emptyKey: ", "})
				}
				parts = append(parts, c)
			}
			joined = component{extra: parts, forceCompound: true}
		}
		return componentReply(translatable("commands.waypoint.list.success",
			nestedCompound{key: "", value: int32(len(comps))},
			nestedCompound{key: "", value: dim},
			joined))
	case "modify":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		target, r := s.resolveSingleEntity(sender, args[1], 2)
		if r != nil {
			return *r
		}
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", 3)
		}
		switch args[2] {
		case "color":
			if len(args) < 4 {
				return errTokenEnd("command.unknown.command", 4)
			}
			switch args[3] {
			case "reset":
				setWaypointColor(target, "")
				return componentReply(translatable("commands.waypoint.modify.color.reset"))
			case "hex":
				if len(args) < 5 {
					return errTokenEnd("command.unknown.command", 5)
				}
				hex := args[4]
				col := "#" + strings.ToUpper(hex)
				setWaypointColor(target, col)
				return componentReply(translatable("commands.waypoint.modify.color",
					component{color: col, text: col}))
			default:
				c := args[3]
				if !chatColorNames[c] {
					return errTokenEnd("argument.color.invalid", 4, c)
				}
				setWaypointColor(target, c)
				return componentReply(translatable("commands.waypoint.modify.color",
					component{color: c, text: c}))
			}
		case "style":
			if len(args) < 4 {
				return errTokenEnd("command.unknown.command", 4)
			}
			switch args[3] {
			case "reset":
				setWaypointStyle(target, "")
				return componentReply(translatable("commands.waypoint.modify.style"))
			case "set":
				if len(args) < 5 {
					return errTokenEnd("command.unknown.command", 5)
				}
				setWaypointStyle(target, args[4])
				return componentReply(translatable("commands.waypoint.modify.style"))
			}
			return errToken("command.unknown.argument", 4)
		}
		return errToken("command.unknown.argument", 3)
	}
	return errToken("command.unknown.argument", 1)
}

// chatColorNames is the named-color argument set (the 16 chat colors).
var chatColorNames = map[string]bool{
	"black": true, "dark_blue": true, "dark_green": true, "dark_aqua": true,
	"dark_red": true, "dark_purple": true, "gold": true, "gray": true,
	"dark_gray": true, "blue": true, "green": true, "aqua": true,
	"red": true, "light_purple": true, "yellow": true, "white": true,
	"reset": true,
}

// setWaypointColor writes the entity's waypoint color field. A player's
// waypoint is its session entity; a mob's lives on the Entity.
func setWaypointColor(t *dataEntity, color string) {
	if t.sess != nil {
		if e, ok := t.sess.server.entities.GetPtr(t.sess.entityID.Load()); ok {
			e.WaypointColor = color
		}
		return
	}
	t.mob.WaypointColor = color
}

func setWaypointStyle(t *dataEntity, style string) {
	if t.sess != nil {
		if e, ok := t.sess.server.entities.GetPtr(t.sess.entityID.Load()); ok {
			e.WaypointStyle = style
		}
		return
	}
	t.mob.WaypointStyle = style
}

// waypointEntryComponent is the decorated waypoint label list carries:
// name text that suggests a tp on click, the waypoint's color, the
// coordinates tooltip on hover, and the name as insertion.
func waypointEntryComponent(sess *Session, dim string) component {
	name := sess.getName()
	x, y, z, _, _, _ := sess.Pos()
	return waypointComponent(name, name, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)), dim)
}

func waypointMobComponent(e *Entity, dim string) component {
	typeName, _ := EntityTypeName(e.Type)
	name := entityDisplayNameText[typeName]
	if name == "" {
		name = typeName
	}
	c := waypointComponent(name, name, int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z)), dim)
	if e.WaypointColor != "" {
		c.color = e.WaypointColor
	}
	return c
}

func waypointComponent(text, name string, x, y, z int, dim string) component {
	color := "#FFFFFF"
	return component{
		text:      text,
		color:     color,
		insertion: name,
		click:     fmt.Sprintf("/execute in %s run tp @s %d %d %d", dim, x, y, z),
		hover: map[string]any{
			"action": "show_text",
			"value":  map[string]any{"translate": "chat.coordinates.tooltip"},
		},
	}
}

// ---------------------------------------------------------------- /swing

// runSwing implements "swing [<targets>] [mainhand|offhand]" — an entity
// selector (defaulting to the sender) whose living members broadcast an
// Entity Animation.
func (s *Server) runSwing(player string, args []string) reply {
	sess := s.findSessionByName(player)
	var targets []*dataEntity
	if len(args) == 0 {
		if sess != nil {
			targets = []*dataEntity{{sess: sess}}
		}
	} else {
		ts, rep := s.selectDataEntities(sess, args[0], 1, false)
		if rep != nil {
			if rep.comp.extra != nil && len(rep.comp.extra) == 1 &&
				rep.comp.extra[0].translate == "argument.entity.notfound.player" {
				return componentReply(commandError("argument.entity.notfound.entity"))
			}
			return *rep
		}
		targets = ts
	}
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	anim := byte(animSwingMainArm)
	if len(args) > 1 {
		switch args[1] {
		case "mainhand":
		case "offhand":
			anim = animSwingOffhand
		default:
			return errToken("command.unknown.argument", 2)
		}
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	living := targets[:0]
	for _, t := range targets {
		if t.sess != nil || t.mob.Living {
			living = append(living, t)
		}
	}
	if len(living) == 0 {
		return componentReply(commandError("commands.swing.failed.notliving"))
	}
	for _, t := range living {
		var eid int32
		if t.sess != nil {
			eid = t.sess.entityID.Load()
		} else {
			eid = t.mob.ID
		}
		s.broadcastEntityAnimation(nil, eid, anim)
	}
	if len(living) == 1 {
		return componentReply(translatable("commands.swing.success.single", living[0].nameComp()))
	}
	return componentReply(translatable("commands.swing.success.multiple",
		nestedCompound{key: "", value: int32(len(living))}))
}
