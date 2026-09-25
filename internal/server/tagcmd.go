package server

import "mc-go/internal/nbt"

// registerTagCommand wires /tag — entity tags feeding [tag=...]
// selectors.
func (s *Server) registerTagCommand() {
	s.registerComponentCommand("tag", func(player string, args []string) reply {
		return s.runTag(player, args)
	})
}

// runTag implements /tag <targets> add|remove|list [name].
func (s *Server) runTag(player string, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	sender := s.findSessionByName(player)
	var targets []*dataEntity
	if len(args[0]) > 0 && args[0][0] == '@' {
		var rep *reply
		targets, rep = s.selectDataEntities(sender, args[0], 1, false)
		if rep != nil {
			return *rep
		}
	} else if sess := s.findSessionByName(args[0]); sess != nil {
		targets = []*dataEntity{{sess: sess}}
	}
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	sub := args[1]
	if sub != "add" && sub != "remove" && sub != "list" {
		return errToken("command.unknown.argument", 2)
	}
	if sub == "list" {
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		return tagListReply(targets)
	}
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 3)
	}
	tag := args[2]
	if sub == "add" {
		var changed []*dataEntity
		for _, d := range targets {
			if addEntityTag(d, tag) {
				changed = append(changed, d)
			}
		}
		if len(changed) == 0 {
			return componentReply(commandError("commands.tag.add.failed"))
		}
		tagArg := nestedCompound{key: "", value: tag}
		if len(targets) == 1 {
			return opsEchoReply(translatable("commands.tag.add.success.single",
				tagArg, targets[0].nameComp()))
		}
		return opsEchoReply(translatable("commands.tag.add.success.multiple",
			tagArg, int32(len(changed))))
	}
	var changed []*dataEntity
	for _, d := range targets {
		if removeEntityTag(d, tag) {
			changed = append(changed, d)
		}
	}
	if len(changed) == 0 {
		return componentReply(commandError("commands.tag.remove.failed"))
	}
	tagArg := nestedCompound{key: "", value: tag}
	if len(targets) == 1 {
		return opsEchoReply(translatable("commands.tag.remove.success.single",
			tagArg, targets[0].nameComp()))
	}
	return opsEchoReply(translatable("commands.tag.remove.success.multiple",
		tagArg, int32(len(changed))))
}

// tagListReply renders /tag <target> list: the name, the count as a
// nested int, and the tags joined by gray ", " separators.
func tagListReply(targets []*dataEntity) reply {
	if len(targets) != 1 {
		return componentReply(translatable("commands.tag.list.multiple.success",
			int32(len(targets))))
	}
	d := targets[0]
	tags := tagList(d)
	list := component{text: ""}
	if len(tags) == 1 {
		list = component{color: "green", text: tags[0]}
	} else if len(tags) > 1 {
		list.text = ""
		for i, t := range tags {
			if i > 0 {
				list.extra = append(list.extra, component{color: "gray", text: ", "})
			}
			list.extra = append(list.extra, component{color: "green", text: t})
		}
	}
	return componentReply(translatable("commands.tag.list.single.success",
		d.nameComp(), nestedCompound{key: "", value: int32(len(tags))}, list))
}

// entityTags reads the tag list off a dataEntity — the mob's ExtraNBT
// Tags for entities, the session's tag set for players.
func tagList(d *dataEntity) []string {
	if d.sess != nil {
		d.sess.tagMu.Lock()
		defer d.sess.tagMu.Unlock()
		out := make([]string, 0, len(d.sess.tags))
		for _, t := range d.sess.tags {
			out = append(out, t)
		}
		return out
	}
	return mobTagList(d.mob)
}

func addEntityTag(d *dataEntity, tag string) bool {
	if d.sess != nil {
		d.sess.tagMu.Lock()
		defer d.sess.tagMu.Unlock()
		for _, t := range d.sess.tags {
			if t == tag {
				return false
			}
		}
		d.sess.tags = append(d.sess.tags, tag)
		return true
	}
	tags := mobTagList(d.mob)
	for _, t := range tags {
		if t == tag {
			return false
		}
	}
	setMobTagList(d.mob, append(tags, tag))
	return true
}

func removeEntityTag(d *dataEntity, tag string) bool {
	if d.sess != nil {
		d.sess.tagMu.Lock()
		defer d.sess.tagMu.Unlock()
		for i, t := range d.sess.tags {
			if t == tag {
				d.sess.tags = append(d.sess.tags[:i:i], d.sess.tags[i+1:]...)
				return true
			}
		}
		return false
	}
	tags := mobTagList(d.mob)
	for i, t := range tags {
		if t == tag {
			setMobTagList(d.mob, append(tags[:i:i], tags[i+1:]...))
			return true
		}
	}
	return false
}

// mobTags pulls the string Tags list out of an entity's ExtraNBT —
// /summon stores it as an nbt.List, /tag writes it as plain []any.
func mobTagList(e *Entity) []string {
	if e == nil {
		return nil
	}
	var items []any
	switch tv := e.ExtraNBT["Tags"].(type) {
	case []any:
		items = tv
	case nbt.List:
		items = tv.Items
	default:
		return nil
	}
	out := make([]string, 0, len(items))
	for _, v := range items {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func setMobTagList(e *Entity, tags []string) {
	if e.ExtraNBT == nil {
		e.ExtraNBT = map[string]any{}
	}
	items := make([]any, 0, len(tags))
	for _, t := range tags {
		items = append(items, t)
	}
	e.ExtraNBT["Tags"] = items
}
