package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func commandBlockName(name string) bool {
	return name == "command_block" || name == "repeating_command_block" || name == "chain_command_block"
}

// A newly placed CommandBlockEntity saves these BaseCommandBlock and
// block-specific defaults. Chain blocks alone start automatic.
func commandBlockDefaults(name string) map[string]any {
	return map[string]any{
		"Command": "", "SuccessCount": int32(0),
		"TrackOutput": int8(1), "UpdateLastExecution": int8(1),
		"powered": int8(0), "conditionMet": int8(0),
		"auto": nbtBool(name == "chain_command_block"),
	}
}

func commandBlockSavedFields(fields map[string]any) map[string]any {
	out := commandBlockDefaults("command_block")
	for k, v := range fields {
		out[k] = cloneNBTValue(v)
	}
	if !nbtTruth(out["TrackOutput"]) {
		delete(out, "LastOutput")
	}
	if !nbtTruth(out["UpdateLastExecution"]) || out["LastExecution"] == int64(-1) {
		delete(out, "LastExecution")
	}
	return out
}

func (s *Server) commandBlockAt(d *dimension, pos world.Pos) *genericBlockEntity {
	name, _ := world.StateOwner(d.w.GetBlock(pos))
	if !commandBlockName(name) {
		return nil
	}
	if g, _, ok := genericBlockEntityAt(d, pos); ok {
		return g
	}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	g := &genericBlockEntity{id: "minecraft:command_block", fields: commandBlockDefaults(name)}
	d.genericBE[pos] = g
	return g
}

func (s *Server) commandBlockOnPlace(d *dimension, pos world.Pos, blk world.Block) {
	name, _ := world.StateOwner(blk)
	if commandBlockName(name) {
		s.commandBlockAt(d, pos)
	}
}

func (s *Server) commandBlockLoadTag(d *dimension, pos world.Pos, tag *snbtTag) {
	if tag == nil {
		return
	}
	g := s.commandBlockAt(d, pos)
	if g == nil {
		return
	}
	wasAuto := nbtTruth(g.fields["auto"])
	fields := commandBlockDefaults("command_block")
	if in, ok := snbtToValue(tag).(map[string]any); ok {
		for k, v := range in {
			switch k {
			case "id", "x", "y", "z", "keepPacked":
				continue
			}
			fields[k] = cloneNBTValue(v)
		}
	}
	fields = commandBlockSavedFields(fields)
	d.genericBE[pos].fields = fields
	name, _ := world.StateOwner(d.w.GetBlock(pos))
	if !wasAuto && nbtTruth(fields["auto"]) && !nbtTruth(fields["powered"]) && name != "chain_command_block" {
		s.commandBlockSchedule(d, pos, g)
	}
}

func (s *Server) commandBlockSchedule(d *dimension, pos world.Pos, g *genericBlockEntity) {
	s.commandBlockMarkCondition(d, pos, g)
	name, _ := world.StateOwner(d.w.GetBlock(pos))
	s.queueCommandBlockTick(d, pos, name)
}

func (s *Server) commandBlockMarkCondition(d *dimension, pos world.Pos, g *genericBlockEntity) bool {
	state := d.w.GetBlock(pos)
	met := true
	if conditional, _ := world.StateProperty(state, "conditional"); conditional == "true" {
		facing, _ := world.StateProperty(state, "facing")
		off := rsOff[rsOpposite(rsDirOfName[facing])]
		previous := s.commandBlockAt(d, world.Pos{X: pos.X + off.X, Y: pos.Y + off.Y, Z: pos.Z + off.Z})
		met = previous != nil && spawnerShort(previous.fields, "SuccessCount", 0) > 0
	}
	g.fields["conditionMet"] = nbtBool(met)
	return met
}

// ServerGamePacketListenerImpl accepts edits even when command_blocks_work
// is false. The creative/gamemaster gate precedes the target lookup.
func (s *Server) handleSetCommandBlock(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	command, err := r.String()
	if err != nil {
		return
	}
	mode, err := r.VarInt()
	if err != nil || mode < 0 || mode > 2 {
		return
	}
	flags, err := r.Byte()
	if err != nil {
		return
	}
	if sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		s.sendSystemChatComponent(sess, translatable("advMode.notAllowed"))
		return
	}
	d := sess.dimension()
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	g := s.commandBlockAt(d, pos)
	if g == nil {
		return
	}
	old := d.w.GetBlock(pos)
	oldName, _ := world.StateOwner(old)
	wasAuto := nbtTruth(g.fields["auto"])
	facing, _ := world.StateProperty(old, "facing")
	name := [...]string{"chain_command_block", "repeating_command_block", "command_block"}[mode]
	conditional := "false"
	if flags&2 != 0 {
		conditional = "true"
	}
	state, ok := world.BlockStateOf(name, map[string]string{"facing": facing, "conditional": conditional})
	if !ok {
		return
	}
	if state != old {
		s.setSilentBlock(d, pos, state)
		// Mode conversion retains the old entity; ordinary replacement
		// still uses the normal generic-store removal rule.
		d.genericBE[pos] = g
	}
	g.fields["Command"] = command
	g.fields["SuccessCount"] = int32(0)
	g.fields["TrackOutput"] = nbtBool(flags&1 != 0)
	g.fields["auto"] = nbtBool(flags&4 != 0)
	auto, powered := flags&4 != 0, nbtTruth(g.fields["powered"])
	if !wasAuto && auto && !powered && name != "chain_command_block" || oldName != name && name == "repeating_command_block" && (powered || auto) {
		s.commandBlockSchedule(d, pos, g)
	}
	if flags&1 == 0 {
		delete(g.fields, "LastOutput")
	}
	if s.gameRuleBool("minecraft:command_blocks_work") {
		s.commandBlockUpdated(d, pos)
	}
	if command != "" {
		key := "advMode.setCommand.success"
		if !s.gameRuleBool("minecraft:command_blocks_work") {
			key = "advMode.setCommand.disabled"
		}
		s.sendSystemChatComponent(sess, translatable(key, command))
	}
}
