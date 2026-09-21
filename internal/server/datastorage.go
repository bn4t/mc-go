package server

import "strconv"

// /data storage — get, merge, remove, modify — against the global
// CommandStorage map. Vanilla resolves a missing id to an empty
// compound rather than failing, so queries on fresh storage reply
// with the {} collapse. Replies measured on 26.2: a read answers
// commands.data.storage.query with the id as an {"":id} compound and
// the pretty tag; every write answers commands.data.storage.modified
// with the id as a bare string argument.
func (s *Server) runDataStorage(player string, args []string) reply {
	if len(args) < 3 {
		return componentReply(commandError("command.unknown.command"))
	}
	id := args[2]
	modified := func() reply {
		return componentReply(translatable("commands.data.storage.modified", id))
	}

	s.storageMu.Lock()
	m, ok := s.storage[id]
	if !ok {
		m = map[string]any{}
		s.storage[id] = m
	}
	s.storageMu.Unlock()

	switch args[0] {
	case "get":
		var target any = m
		if len(args) > 3 {
			steps, ok := parseNBTPath(args[3])
			if !ok {
				return componentReply(commandError("command.unknown.command"))
			}
			target, ok = nbtGet(m, steps)
			if !ok {
				return componentReply(commandError("arguments.nbtpath.nothing_found", args[3]))
			}
		}
		return componentReply(translatable("commands.data.storage.query",
			nestedCompound{key: "", value: id},
			component{extra: nbtPrettyElems(target), text: ""}))

	case "merge":
		if len(args) < 4 {
			return componentReply(commandError("command.unknown.command"))
		}
		tg, _, perr := parseSNBTCompound(args[3], 0)
		if perr != nil || tg == nil || tg.kind != snbtCompound {
			return componentReply(commandError("command.unknown.command"))
		}
		nbtMergeInto(m, snbtToValue(tg))
		return modified()

	case "remove":
		if len(args) < 4 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[3])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		if !nbtRemove(m, steps) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		return modified()

	case "modify":
		// data modify storage <id> <path> <op> <source> — the same
		// set/merge/append/prepend/insert ops and value|from|string
		// sources the entity and block scopes take.
		if len(args) < 7 {
			return componentReply(commandError("command.unknown.command"))
		}
		steps, ok := parseNBTPath(args[3])
		if !ok {
			return componentReply(commandError("command.unknown.command"))
		}
		op := args[4]
		rest := args[5:]
		var insertIdx int
		if op == "insert" {
			v, err := strconv.Atoi(rest[0])
			if err != nil || len(rest) < 2 {
				return componentReply(commandError("command.unknown.command"))
			}
			insertIdx = v
			rest = rest[1:]
		}
		val, serr := s.modifySource(player, rest)
		if serr != nil {
			return *serr
		}
		if !nbtModify(m, steps, op, insertIdx, val) {
			return componentReply(commandError("commands.data.merge.failed"))
		}
		return modified()
	}
	return componentReply(commandError("command.unknown.command"))
}

// storageCompound resolves a `from storage`/`string storage` source:
// the id's compound (empty when nobody wrote it) or a path inside it.
func (s *Server) storageCompound(id string) map[string]any {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	m, ok := s.storage[id]
	if !ok {
		m = map[string]any{}
		s.storage[id] = m
	}
	return m
}
