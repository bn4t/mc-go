package server

import (
	"bytes"
	"reflect"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// commandMinecartData is SynchedEntityData's two command-cart entries.
// BaseCommandBlock's runtime fields are separate: disabled edits and quiet
// execution need not change the cached command/output sent to clients.
type commandMinecartData struct {
	command       string
	output        any
	commandDirty  bool
	outputDirty   bool
	lastActivated int32
}

var commandMinecartKeys = [...]string{"Command", "SuccessCount", "TrackOutput", "UpdateLastExecution", "LastExecution", "LastOutput"}

func commandMinecartFields(e *Entity) map[string]any {
	if e.ExtraNBT == nil {
		e.ExtraNBT = map[string]any{}
	}
	for key, value := range entitySaveExtras["command_block_minecart"] {
		if _, ok := e.ExtraNBT[key]; !ok {
			e.ExtraNBT[key] = cloneNBTValue(value)
		}
	}
	if e.commandCart == nil {
		e.commandCart = &commandMinecartData{output: ""}
	}
	return e.ExtraNBT
}

func commandMinecartOutput(value any) any {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if len(v) == 1 {
			for _, key := range []string{"text", ""} {
				if text, ok := v[key].(string); ok {
					return text
				}
			}
		}
		return cloneNBTValue(v)
	case nbt.List:
		return cloneNBTValue(v)
	}
	return ""
}

func commandMinecartComponentContent(value any) []byte {
	var b bytes.Buffer
	if err := nbt.WriteUnnamed(&b, commandMinecartOutput(value)); err != nil {
		return literal("").content()
	}
	return b.Bytes()
}

// Setting a changed synced entry invokes the minecart's callback on both
// sides. In particular a changed command resets BaseCommandBlock's count,
// and a changed output stores Component.empty even while tracking is off.
func commandMinecartUpdated(e *Entity) {
	fields := commandMinecartFields(e)
	data := e.commandCart
	command, _ := fields["Command"].(string)
	if data.command != command {
		data.command = command
		data.commandDirty = true
		fields["SuccessCount"] = int32(0)
	}
	output := any("")
	if value, ok := fields["LastOutput"]; ok && value != nil {
		output = commandMinecartOutput(value)
	}
	if !reflect.DeepEqual(data.output, output) {
		data.output = cloneNBTValue(output)
		data.outputDirty = true
		fields["LastOutput"] = cloneNBTValue(output)
	}
	if data.commandDirty || data.outputDirty {
		e.MetaDirty = true
	}
}

func commandMinecartLoad(e *Entity, save map[string]any) {
	fields := commandMinecartFields(e)
	for _, key := range commandMinecartKeys {
		delete(fields, key)
	}
	for key, value := range entitySaveExtras["command_block_minecart"] {
		fields[key] = cloneNBTValue(value)
	}
	for _, key := range commandMinecartKeys {
		if value, ok := save[key]; ok {
			fields[key] = cloneNBTValue(value)
		}
	}
	commandMinecartFilterSaved(fields)
	commandMinecartUpdated(e)
}

func commandMinecartSavedFields(e *Entity, fields map[string]any) {
	commandMinecartFilterSaved(fields)
	if e.CustomName != "" {
		fields["CustomName"] = commandMinecartOutput(storedComponentArg(e.CustomName, e.CustomNameComp))
	}
}

func commandMinecartFilterSaved(fields map[string]any) {
	if !nbtTruth(fields["TrackOutput"]) || fields["LastOutput"] == nil {
		delete(fields, "LastOutput")
	}
	if !nbtTruth(fields["UpdateLastExecution"]) || commandLong(fields["LastExecution"], -1) == -1 {
		delete(fields, "LastExecution")
	}
}

func (s *Server) commandMinecartLoadTag(e *Entity, tag *snbtTag) {
	save := entitySaveMap(e, s.gameTime(), s.entities).m
	if tag != nil {
		if values, ok := snbtToValue(tag).(map[string]any); ok {
			for key, value := range values {
				save[key] = cloneNBTValue(value)
			}
		}
	}
	s.applyEntitySave(e, save)
}

func (s *Server) handleSetCommandMinecart(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.VarInt()
	if err != nil {
		return
	}
	command, err := r.String()
	if err != nil {
		return
	}
	tracking, err := r.Bool()
	if err != nil {
		return
	}
	if sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		s.sendSystemChatComponent(sess, translatable("advMode.notAllowed"))
		return
	}
	e, ok := s.entities.GetPtr(id)
	if !ok || e.Dim != sess.dimName() || e.Type != mustEntityType("command_block_minecart") {
		return
	}
	fields := commandMinecartFields(e)
	fields["Command"] = command
	fields["SuccessCount"] = int32(0)
	fields["TrackOutput"] = nbtBool(tracking)
	if !tracking {
		delete(fields, "LastOutput")
	}
	enabled := s.gameRuleBool("minecraft:command_blocks_work")
	if enabled {
		commandMinecartUpdated(e)
	}
	if command != "" {
		key := "advMode.setCommand.disabled"
		if enabled {
			key = "advMode.setCommand.success"
		}
		s.sendSystemChatComponent(sess, translatable(key, command))
	}
}

func encodeCommandMinecartMetadata(e *Entity) []byte {
	commandMinecartFields(e)
	var b protocol.Buffer
	if e.CustomName != "" {
		b.Byte(2)
		b.Byte(6)
		b.Bool(true)
		b.Raw(commandMinecartComponentContent(storedComponentArg(e.CustomName, e.CustomNameComp)))
	}
	if e.CustomNameVisible {
		b.Byte(3)
		b.Byte(8)
		b.Bool(true)
	}
	if e.Silent {
		b.Byte(4)
		b.Byte(8)
		b.Bool(true)
	}
	if e.NoGravity {
		b.Byte(5)
		b.Byte(8)
		b.Bool(true)
	}
	if e.commandCart.command != "" {
		b.Byte(13)
		b.Byte(4)
		b.String(e.commandCart.command)
	}
	if !reflect.DeepEqual(e.commandCart.output, "") {
		b.Byte(14)
		b.Byte(5)
		b.Raw(commandMinecartComponentContent(e.commandCart.output))
	}
	if b.Len() == 0 {
		return nil
	}
	b.Byte(0xff)
	return b.Bytes()
}

// Prepare the dirty pack at sendChanges, combining pending generic entity
// fields with command-cart entries at the same tracker boundary.
func prepareCommandMinecartMetadata(e *Entity) {
	if e.commandCart == nil {
		return
	}
	data := e.commandCart
	if !data.commandDirty && !data.outputDirty {
		return
	}
	var b protocol.Buffer
	if len(e.dataSync) > 0 {
		b.Raw(e.dataSync[:len(e.dataSync)-1])
	}
	if data.commandDirty {
		b.Byte(13)
		b.Byte(4)
		b.String(data.command)
		data.commandDirty = false
	}
	if data.outputDirty {
		b.Byte(14)
		b.Byte(5)
		b.Raw(commandMinecartComponentContent(data.output))
		data.outputDirty = false
	}
	b.Byte(0xff)
	e.dataSync = b.Bytes()
	e.MetaDirty = false
}

func (s *Server) restoreCommandMinecart(d *dimension, save map[string]any, pos [3]float64, uuid [16]byte) *Entity {
	e := s.entities.Spawn(&Entity{Type: mustEntityType("command_block_minecart"), Dim: d.name, X: pos[0], Y: pos[1], Z: pos[2]})
	if uuid != [16]byte{} {
		e.UUID = uuid
	}
	s.applyEntitySave(e, save)
	e.CartYawO = e.Yaw
	// Pairing sends the complete cached command/output. There is no prior
	// client state to update after native loading.
	e.dataSync = nil
	e.MetaDirty = false
	e.commandCart.commandDirty = false
	e.commandCart.outputDirty = false
	return e
}

func (s *Server) commandMinecartActivate(e *Entity) {
	commandMinecartFields(e)
	if e.TickCount-e.commandCart.lastActivated < 4 {
		return
	}
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	frame := commandSourceFrame{dim: d, pos: [3]float64{e.X, e.Y, e.Z}, rotation: [2]float32{e.Yaw, e.Pitch}, executor: &dataEntity{mob: e}, display: entityDisplayName(e), level: 2}
	s.performCommandBase(func() map[string]any { return commandMinecartFields(e) }, frame, func() { commandMinecartUpdated(e) })
	e.commandCart.lastActivated = e.TickCount
}
