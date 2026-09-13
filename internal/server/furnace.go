package server

import (
	"sync"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Furnace menu layout: 0 input, 1 fuel, 2 output, then the player rows.
const (
	furnaceSlots     = 3
	furnaceInput     = 0
	furnaceFuel      = 1
	furnaceOutput    = 2
	menuFurnace      = 14 // minecraft:furnace in the menu registry
	furnaceCookTicks = 200
)

// furnaceData is one furnace block entity's state.
type furnaceData struct {
	slots     [furnaceSlots]ItemStack
	burn      int32 // remaining burn ticks
	burnTotal int32
	cook      int32
	cookTotal int32
}

// furnaceStore keeps furnace state by position.
type furnaceStore struct {
	mu       sync.Mutex
	furnaces map[world.Pos]*furnaceData
}

// isFurnaceBlock reports whether a block state is a furnace.
func isFurnaceBlock(b world.Block) bool {
	name, ok := world.BlockName(b)
	if !ok {
		return false
	}
	return name == "furnace" || name == "blast_furnace" || name == "smoker"
}

// smeltResult maps a smeltable input item to its output.
var smeltResult = map[string]string{
	"sand":         "glass",
	"cobblestone":  "stone",
	"raw_iron":     "iron_ingot",
	"raw_gold":     "gold_ingot",
	"raw_copper":   "copper_ingot",
	"raw_porkchop": "cooked_porkchop",
	"raw_beef":     "steak",
	"raw_chicken":  "cooked_chicken",
	"potato":       "baked_potato",
	"kelp":         "dried_kelp",
	"clay_ball":    "brick",
	"netherrack":   "nether_brick",
	"oak_log":      "charcoal",
}

// fuelTicks is the burn duration of the common fuels.
var fuelTicks = map[string]int32{
	"coal":        1600,
	"charcoal":    1600,
	"oak_planks":  300,
	"stick":       100,
	"oak_log":     300,
	"lava_bucket": 20000,
}

func (s *Server) ensureFurnace(d *dimension, pos world.Pos) {
	d.furnaces.mu.Lock()
	if d.furnaces.furnaces == nil {
		d.furnaces.furnaces = make(map[world.Pos]*furnaceData)
	}
	if _, ok := d.furnaces.furnaces[pos]; !ok {
		d.furnaces.furnaces[pos] = &furnaceData{}
	}
	d.furnaces.mu.Unlock()
}

// furnaceAt returns the live furnace state, creating it if needed.
func (s *Server) furnaceAt(d *dimension, pos world.Pos) *furnaceData {
	s.ensureFurnace(d, pos)
	d.furnaces.mu.Lock()
	defer d.furnaces.mu.Unlock()
	return d.furnaces.furnaces[pos]
}

// removeFurnace drops a furnace's contents when its block goes away.
func (s *Server) removeFurnace(d *dimension, pos world.Pos) {
	d.furnaces.mu.Lock()
	data, ok := d.furnaces.furnaces[pos]
	delete(d.furnaces.furnaces, pos)
	d.furnaces.mu.Unlock()
	if !ok {
		return
	}
	for i := 0; i < furnaceSlots; i++ {
		if data.slots[i].Empty() {
			continue
		}
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         d.name,
			X:           float64(pos.X) + 0.5,
			Y:           float64(pos.Y) + 0.5,
			Z:           float64(pos.Z) + 0.5,
			Item:        data.slots[i],
			PickupDelay: 10,
		})
		data.slots[i] = ItemStack{}
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
}

// openFurnace shows the furnace window, contents, and properties.
func (s *Server) openFurnace(sess *Session, pos world.Pos) {
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), furnace: s.furnaceAt(sess.dimension(), pos), kind: menuFurnace}
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuFurnace)
	b.Raw(textComponentContent("Furnace"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendFurnaceContent(sess)
	s.sendFurnaceProperties(sess)
}

// sendFurnaceContent syncs the furnace menu.
func (s *Server) sendFurnaceContent(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	state := sess.windowState
	sess.windowMu.Unlock()
	if w == nil || w.furnace == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(furnaceSlots + 36)
	for i := 0; i < furnaceSlots+36; i++ {
		writeSlot(&b, sess.menuStack(i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// sendFurnaceProperties syncs the burn/cook indicators.
func (s *Server) sendFurnaceProperties(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.furnace == nil {
		return
	}
	w.dim.furnaces.mu.Lock()
	f := w.furnace
	props := [4]int32{f.burn, f.burnTotal, f.cook, f.cookTotal}
	w.dim.furnaces.mu.Unlock()
	for i, v := range props {
		var b protocol.Buffer
		b.VarInt(w.id)
		b.Short(int16(i))
		b.Short(int16(v))
		_ = sess.write(sess.ids().PlayContainerProperty, b.Bytes())
	}
}

// smeltable reports whether the furnace's input can smelt and the
// output has room.
func smeltable(f *furnaceData) (input ItemStack, result int32, ok bool) {
	input = f.slots[furnaceInput]
	if input.Empty() {
		return ItemStack{}, 0, false
	}
	itemName, ok := itemNameOf(input.ID)
	if !ok {
		return ItemStack{}, 0, false
	}
	outName, ok := smeltResult[itemName]
	if !ok {
		return ItemStack{}, 0, false
	}
	outID, ok := world.ItemID(outName)
	if !ok {
		return ItemStack{}, 0, false
	}
	out := f.slots[furnaceOutput]
	if !out.Empty() && (out.ID != outID || out.Count >= maxStack) {
		return ItemStack{}, 0, false
	}
	return input, outID, true
}

// itemNameOf resolves an item id back to its registry name.
func itemNameOf(id int32) (string, bool) {
	for name, v := range world.ItemIDs {
		if v == id {
			return name, true
		}
	}
	return "", false
}

// furnaceTickStep advances every furnace one tick, in every dimension.
func (s *Server) furnaceTickStep() {
	for _, d := range s.dims {
		d.furnaces.mu.Lock()
		type change struct {
			pos world.Pos
			f   *furnaceData
		}
		var current []change
		for pos, f := range d.furnaces.furnaces {
			current = append(current, change{pos, f})
		}
		d.furnaces.mu.Unlock()
		for _, c := range current {
			s.furnaceTick(d, c.pos, c.f)
		}
	}
}

// furnaceTick advances one furnace and syncs viewers when values
// change.
func (s *Server) furnaceTick(d *dimension, pos world.Pos, f *furnaceData) {
	d.furnaces.mu.Lock()
	_, result, canSmelt := smeltable(f)
	changed := false
	if f.burn > 0 {
		f.burn--
		changed = true
	}
	if f.burn == 0 && canSmelt {
		if fuelID := f.slots[furnaceFuel]; !fuelID.Empty() {
			if name, ok := itemNameOf(fuelID.ID); ok {
				if ticks, ok := fuelTicks[name]; ok {
					f.slots[furnaceFuel].Count--
					if f.slots[furnaceFuel].Count <= 0 {
						f.slots[furnaceFuel] = ItemStack{}
					}
					f.burn, f.burnTotal = ticks, ticks
					changed = true
				}
			}
		}
	}
	if canSmelt && f.burn > 0 {
		f.cook++
		if f.cook >= furnaceCookTicks {
			f.cook = 0
			f.slots[furnaceInput].Count--
			if f.slots[furnaceInput].Count <= 0 {
				f.slots[furnaceInput] = ItemStack{}
			}
			if f.slots[furnaceOutput].Empty() {
				f.slots[furnaceOutput] = ItemStack{ID: result, Count: 1}
			} else {
				f.slots[furnaceOutput].Count++
			}
		}
		changed = true
	} else if f.cook != 0 {
		f.cook = 0
		changed = true
	}
	props := [4]int32{f.burn, f.burnTotal, f.cook, f.cookTotal}
	slots := f.slots
	d.furnaces.mu.Unlock()
	if !changed {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w == nil || w.furnace != f {
			return true
		}
		for i, val := range props {
			var b protocol.Buffer
			b.VarInt(w.id)
			b.Short(int16(i))
			b.Short(int16(val))
			_ = sess.write(sess.ids().PlayContainerProperty, b.Bytes())
		}
		for i := 0; i < furnaceSlots; i++ {
			var b protocol.Buffer
			b.VarInt(w.id)
			b.VarInt(sess.windowState)
			b.VarInt(int32(i))
			writeSlot(&b, slots[i])
			_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
		}
		return true
	})
}
