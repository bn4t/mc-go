package server

import (
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// /item replace moves item stacks into slots by name: the slot registry
// (container.N, inventory.N, hotbar.N, enderchest.N, armor.*, weapon.*,
// player.cursor, player.crafting.N, horse.N) measured off the real server
// into internal/server/testdata/vanilla-26.2/item_slots.tsv, and the
// replies around it pinned by TestItemCommandParity.
//
// The registry maps every name to one global index, and each target
// decides which indices it has. The numbered families alias: hotbar.N is
// container.N (both index N), and inventory.N is container.9+N. A block
// takes an index below its slot count, so a chest takes hotbar.0 (index
// 0) and inventory.0 (index 9) but not inventory.26 (index 35), and a
// furnace takes hotbar.2 (index 2) but not hotbar.3 (index 3) or
// inventory.0 (index 9). A player takes 0-35 (hotbar and main), the hands
// and armor, its ender chest, the cursor, and the 2x2 grid - the horse
// names alias the grid (horse.0 is player.crafting.0, both index 500).
// container.36-53 and horse.4-14 are known names the player does not
// have: writing one fails with no_changed naming the item, reading one
// as a from-source fails with source.no_such_slot, and modifying one
// fails with no_changes naming only the slot.

// Global slot indices, the numbers the real server reports for slots a
// target does not have.
const (
	itemSlotMainhand = 98
	itemSlotOffhand  = 99
	itemSlotFeet     = 100
	itemSlotLegs     = 101
	itemSlotChest    = 102
	itemSlotHead     = 103
	itemSlotBody     = 105
	itemSlotCursor   = 499
)

// parseItemSlot maps a slot name to its global index. Names outside the
// registry - unknown families, missing or extra index parts, or an index
// past the family's range - fail, which the command reports as
// slot.unknown with the cursor past the name.
func parseItemSlot(tok string) (int32, bool) {
	numbered := func(prefix string, lo, hi, base int32) (int32, bool) {
		s, ok := strings.CutPrefix(tok, prefix)
		if !ok {
			return 0, false
		}
		if s == "" {
			return 0, false
		}
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < int(lo) || n > int(hi) {
			return 0, false
		}
		return base + int32(n), true
	}
	if id, ok := numbered("container.", 0, 53, 0); ok {
		return id, true
	}
	if id, ok := numbered("inventory.", 0, 26, 9); ok {
		return id, true
	}
	if id, ok := numbered("hotbar.", 0, 8, 0); ok {
		return id, true
	}
	if id, ok := numbered("enderchest.", 0, 26, 200); ok {
		return id, true
	}
	if id, ok := numbered("player.crafting.", 0, 3, 500); ok {
		return id, true
	}
	if id, ok := numbered("horse.", 0, 14, 500); ok {
		return id, true
	}
	switch tok {
	case "weapon.mainhand":
		return itemSlotMainhand, true
	case "weapon.offhand":
		return itemSlotOffhand, true
	case "armor.feet":
		return itemSlotFeet, true
	case "armor.legs":
		return itemSlotLegs, true
	case "armor.chest":
		return itemSlotChest, true
	case "armor.head":
		return itemSlotHead, true
	case "armor.body":
		return itemSlotBody, true
	case "player.cursor":
		return itemSlotCursor, true
	}
	return 0, false
}

// normalizeItemName applies vanilla's default namespace: a bare name is
// minecraft:'s, anything with a colon is taken as written.
func normalizeItemName(tok string) string {
	if strings.Contains(tok, ":") {
		return tok
	}
	return "minecraft:" + tok
}

// isNSChar reports whether c continues an id namespace.
func isNSChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '-'
}

// isPathChar reports whether c continues an id path.
func isPathChar(c byte) bool {
	return isNSChar(c) || c == '/'
}

// idPrefix reads what vanilla's id readers take before they stop: a
// namespace, one colon, and a path. Anything from the first other
// character on is not part of the id, so STONE reads as nothing and
// stONE as st.
func idPrefix(tok string) string {
	i := 0
	for i < len(tok) && isNSChar(tok[i]) {
		i++
	}
	if i < len(tok) && tok[i] == ':' {
		j := i + 1
		for j < len(tok) && isPathChar(tok[j]) {
			j++
		}
		return tok[:j]
	}
	return tok[:i]
}

// echoItemID renders the id a failed lookup reports: the default
// namespace when none was read, and only what was read. foo:BAR fails
// as foo:, foO:bar as minecraft:fo, and a bare STONE as minecraft:.
func echoItemID(prefix string) string {
	if ns, path, ok := strings.Cut(prefix, ":"); ok {
		if ns == "" {
			ns = "minecraft"
		}
		return ns + ":" + path
	}
	if prefix == "" {
		return "minecraft:"
	}
	return "minecraft:" + prefix
}

// shortItemName strips the minecraft: prefix the rest of the server
// stores items under.
func shortItemName(full string) string {
	return strings.TrimPrefix(full, "minecraft:")
}

// parseItemCount reads the count after an item: 1-99, defaulting to one
// when omitted. Vanilla's integer reader takes digits with an optional
// leading minus and nothing else.
func parseItemCount(tok string) (int32, bool) {
	s := strings.TrimPrefix(tok, "-")
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}

// armorSlotAccepts is the equipment filter the real server applies to
// /item writes on a player: the head takes anything, the other armor
// slots only their own piece (and the chest an elytra), and the body
// only wolf armor. The elytra is vanilla's own exception, measured with
// the rest of the filter table.
func armorSlotAccepts(id int32, name string) bool {
	switch id {
	case itemSlotHead:
		return true
	case itemSlotChest:
		return name == "elytra" || strings.HasSuffix(name, "_chestplate")
	case itemSlotLegs:
		return strings.HasSuffix(name, "_leggings")
	case itemSlotFeet:
		return strings.HasSuffix(name, "_boots")
	case itemSlotBody:
		return name == "wolf_armor"
	}
	return true
}

// playerSlotValid reports whether a global index names a slot the player
// has: 0-35 (hotbar and main), hands and armor, the ender chest, the
// cursor, and the 2x2 grid aliased as horse.0-3.
func playerSlotValid(id int32) bool {
	switch {
	case id >= 0 && id <= 35:
		return true
	case id >= itemSlotMainhand && id <= itemSlotHead:
		return true
	case id == itemSlotBody:
		return true
	case id >= 200 && id <= 226:
		return true
	case id == itemSlotCursor:
		return true
	case id >= 500 && id <= 503:
		return true
	}
	return false
}

// entityMainSlot maps a global main-storage index to the session's
// inventory: 0-8 are the hotbar, which lives at the window's hotbar
// slots, and 9-35 are the main storage, which lines up directly.
func entityMainSlot(id int32) int16 {
	if id < 9 {
		return int16(slotHotbarStart + id)
	}
	return int16(id)
}

// readEntitySlot reads one of a player's global slots.
func readEntitySlot(sess *Session, id int32) (ItemStack, bool) {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	switch {
	case id >= 0 && id <= 35:
		return sess.inv[entityMainSlot(id)], true
	case id == itemSlotMainhand:
		return sess.inv[invSlotForHotbar(sess.held.Load())], true
	case id == itemSlotOffhand:
		return sess.inv[slotOffhand], true
	case id == itemSlotHead:
		return sess.inv[slotArmorStart], true
	case id == itemSlotChest:
		return sess.inv[slotArmorStart+1], true
	case id == itemSlotLegs:
		return sess.inv[slotArmorStart+2], true
	case id == itemSlotFeet:
		return sess.inv[slotArmorStart+3], true
	case id == itemSlotBody:
		return sess.bodyArmor, true
	case id >= 200 && id <= 226:
		return sess.ender[id-200], true
	case id == itemSlotCursor:
		return sess.carried, true
	case id >= 500 && id <= 503:
		return sess.inv[slotCraftStart+(id-500)], true
	}
	return ItemStack{}, false
}

// writeEntitySlot writes one of a player's global slots and reports
// whether equipment changed, so the caller can broadcast it the way a
// menu click would.
func (s *Server) writeEntitySlot(sess *Session, id int32, st ItemStack) bool {
	equip := false
	sess.invMu.Lock()
	switch {
	case id >= 0 && id <= 35:
		sess.inv[entityMainSlot(id)] = st
	case id == itemSlotMainhand:
		sess.inv[invSlotForHotbar(sess.held.Load())] = st
		equip = true
	case id == itemSlotOffhand:
		sess.inv[slotOffhand] = st
		equip = true
	case id == itemSlotHead:
		sess.inv[slotArmorStart] = st
		equip = true
	case id == itemSlotChest:
		sess.inv[slotArmorStart+1] = st
		equip = true
	case id == itemSlotLegs:
		sess.inv[slotArmorStart+2] = st
		equip = true
	case id == itemSlotFeet:
		sess.inv[slotArmorStart+3] = st
		equip = true
	case id == itemSlotBody:
		sess.bodyArmor = st
	case id >= 200 && id <= 226:
		sess.ender[id-200] = st
	case id == itemSlotCursor:
		sess.carried = st
	case id >= 500 && id <= 503:
		sess.inv[slotCraftStart+(id-500)] = st
	}
	sess.invMu.Unlock()
	if id >= 500 && id <= 503 {
		// The grid is the menu's grid, so its result follows the write.
		s.refreshCraftResult(sess)
	}
	if equip {
		s.broadcastEquipment(sess)
	}
	s.syncItemSlot(sess, id)
	return equip
}

// syncItemSlot pushes an /item write on a player slot to that player's
// client: a window-0 slot update, or a cursor update for the cursor.
func (s *Server) syncItemSlot(sess *Session, id int32) {
	state := sess.invState.Add(1)
	switch {
	case id == itemSlotCursor:
		sess.invMu.RLock()
		it := sess.carried
		sess.invMu.RUnlock()
		_ = s.sendCarriedItem(sess, it)
		return
	case id == itemSlotMainhand:
		sess.invMu.RLock()
		it := sess.inv[invSlotForHotbar(sess.held.Load())]
		sess.invMu.RUnlock()
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(state)
		b.Short(invSlotForHotbar(sess.held.Load()))
		writeSlot(&b, it)
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
		return
	}
	var slot int16 = -1
	switch {
	case id >= 0 && id <= 35:
		slot = entityMainSlot(id)
	case id == itemSlotOffhand:
		slot = slotOffhand
	case id == itemSlotHead:
		slot = slotArmorStart
	case id == itemSlotChest:
		slot = slotArmorStart + 1
	case id == itemSlotLegs:
		slot = slotArmorStart + 2
	case id == itemSlotFeet:
		slot = slotArmorStart + 3
	case id >= 200 && id <= 226:
		// The ender chest has no open menu to sync to.
		return
	case id >= 500 && id <= 503:
		slot = slotCraftStart + int16(id-500)
	}
	if slot < 0 {
		return
	}
	sess.invMu.RLock()
	it := sess.inv[slot]
	sess.invMu.RUnlock()
	var b protocol.Buffer
	b.VarInt(0)
	b.VarInt(state)
	b.Short(slot)
	writeSlot(&b, it)
	_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
}

// itemDisplayKey is the translation key an item's brackets name it by:
// a block item takes its block's key, anything else its item key.
func itemDisplayKey(full string) string {
	name := shortItemName(full)
	if _, ok := world.BlockStateOf(name, nil); ok {
		return "block.minecraft." + name
	}
	return "item.minecraft." + name
}

// itemBrackets renders a stack as vanilla's chat.square_brackets item:
// the display name in brackets, white with a show_item hover, the count
// on the hover when it is not one. Air has neither color nor hover.
func itemBrackets(st ItemStack) component {
	name := component{translate: itemDisplayKey("minecraft:air")}
	inner := component{extra: []component{name}, text: ""}
	if st.Empty() {
		return component{translate: "chat.square_brackets", with: []any{inner}}
	}
	full := "minecraft:" + shortItemName(mustItemName(st.ID))
	name = component{translate: itemDisplayKey(full)}
	inner = component{extra: []component{name}, text: ""}
	hover := map[string]any{"action": "show_item", "id": full}
	if st.Count != 1 {
		hover["count"] = st.Count
	}
	return component{
		color:     "white",
		hover:     hover,
		translate: "chat.square_brackets",
		with:      []any{inner},
	}
}

// mustItemName maps an item id back to its short name; id 0 is air.
func mustItemName(id int32) string {
	if name, ok := world.ItemName(id); ok {
		return name
	}
	return "air"
}

// entityTargetComponent renders an /item entity target the way the real
// server's replies name it: the player's name, clickable to message
// them, hovering their entity id.
func entityTargetComponent(sess *Session) component {
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

// noChangedItem is the failure the real server answers a slot write the
// target refuses with: the item it would have held, and the slot's
// global index.
func noChangedItem(st ItemStack, id int32) component {
	return commandError("commands.item.target.no_changed.known_item",
		itemBrackets(st), nestedCompound{key: "", value: id})
}

// noUnchangedSlot is the failure a modify the target refuses gets: it
// names only the slot, a bare number where replace wraps an item.
func noUnchangedSlot(id int32) component {
	return commandError("commands.item.target.no_changes", id)
}

// runItem implements vanilla's /item surface: replace ... with,
// replace ... from, and the modify form, whose only modifier in a world
// without data packs is an unknown one. Token indexes count the command
// name as token 0, like every other command's fail marks.
func (s *Server) runItem(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "replace", "modify":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 1)
		}
		if args[1] != "block" && args[1] != "entity" {
			return errToken("command.unknown.argument", 2)
		}
		if args[0] == "modify" {
			return s.runItemModify(player, args)
		}
		return s.runItemReplace(player, args)
	}
	return errToken("command.unknown.argument", 1)
}

// itemBlockPos parses the three coordinates of a block target, resolved
// against the sender like every other block command.
func (s *Server) itemBlockPos(player string, args []string) (bx, by, bz int, tok int, ok bool) {
	px, py, pz := s.baseBlockPos(player)
	vals := []int{px, py, pz}
	for i := 0; i < 3; i++ {
		v, err := parseCoord(args[2+i], vals[i])
		if err != nil {
			return 0, 0, 0, 3 + i, false
		}
		vals[i] = v
	}
	return vals[0], vals[1], vals[2], 0, true
}

// runItemModify parses an `item modify` target and slot, then the
// modifier: without data packs no modifier id exists, and inline NBT
// parses as a loot function to apply. Block form tokens: modifier is
// args[6] (token 7); entity form: args[4] (token 5).
func (s *Server) runItemModify(player string, args []string) reply {
	slotArg, modArg, modTok := 5, 6, 7
	if args[1] == "entity" {
		slotArg, modArg, modTok = 3, 4, 5
	}
	if args[1] == "block" {
		if len(args) < 6 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if _, _, _, tok, ok := s.itemBlockPos(player, args); !ok {
			return errToken("parsing.int.expected", tok)
		}
	} else {
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if s.resolveTarget(player, args[2]) == nil {
			return componentReply(commandError("argument.entity.notfound.entity"))
		}
	}
	if _, ok := parseItemSlot(args[slotArg]); !ok {
		return errTokenEnd("slot.unknown", slotArg+1, args[slotArg])
	}
	if len(args) <= modArg {
		return errTokenEnd("command.unknown.command", len(args))
	}
	if r, ok := s.parseItemModifier(args[modArg], modTok); !ok {
		return r
	}
	if len(args) > modArg+1 {
		return errToken("command.unknown.argument", modTok+1)
	}
	// The modifier applied no functions, so the stack is unchanged and
	// the reply names what the slot holds. The one exception is a
	// bare set_damage compound, which wears the stack the way the
	// real server does (see applySetDamage): without it no
	// damage-varying setup survives a round trip.
	d := s.dimOf(player)
	if args[1] == "block" {
		x, y, z, _, _ := s.itemBlockPos(player, args)
		pos := world.Pos{X: x, Y: y, Z: z}
		st, r, ok := s.readItemBlock(d, pos, mustParseItemSlot(args[slotArg]))
		if !ok {
			return r
		}
		if worn, ok := applySetDamage(args[modArg], st); ok {
			s.writeItemBlock(d, pos, mustParseItemSlot(args[slotArg]), worn)
			st = worn
		}
		return componentReply(translatable("commands.item.block.set.success",
			nestedCompound{key: "", value: int32(x)},
			nestedCompound{key: "", value: int32(y)},
			nestedCompound{key: "", value: int32(z)},
			itemBrackets(st)))
	}
	tgt := s.resolveTarget(player, args[2])
	id := mustParseItemSlot(args[slotArg])
	// A modify writes nothing, so a slot the target refuses fails with
	// no_changes naming only the slot: container.40 ends there.
	if !playerSlotValid(id) {
		return componentReply(noUnchangedSlot(id))
	}
	st, _ := readEntitySlot(tgt, id)
	if worn, ok := applySetDamage(args[modArg], st); ok {
		s.writeEntitySlot(tgt, id, worn)
		st = worn
	}
	return componentReply(translatable("commands.item.entity.set.success.single",
		entityTargetComponent(tgt), itemBrackets(st)))
}

// applySetDamage wears st as a bare `{"function":"set_damage",
// "damage":f}` compound does on the real server, reporting whether it
// applied. The fraction is damage-relative: 0.5 on a 250-max pick
// reads back 125, 0.0 wears it to nothing (damage == max), 1.0
// repairs it flat. Six fractions measured on vanilla 26.2 (0.0,
// 0.1, 0.2, 0.5, 0.8, 0.9 -> 250, 225, 200, 125, 49, 25) pin the
// arithmetic exactly: damage truncates (1-f)*max in 32-bit floats,
// which is the only rounding that fits 0.8 -> 49 and 0.1 -> 225 at
// once. Fractions outside 0..1 clamp (unmeasured), a missing or
// unparsable damage stays a no-op success, and so does a compound
// carrying anything besides function and damage (conditions the
// engine cannot evaluate), a list modifier, an empty stack, or an
// item with no durability.
func applySetDamage(tok string, st ItemStack) (ItemStack, bool) {
	if !strings.HasPrefix(tok, "{") || st.Empty() {
		return st, false
	}
	c, _, lerr := parseSNBTCompound(tok, 0)
	if lerr != nil {
		return st, false
	}
	fn, ok := c.vals["function"]
	if !ok || fn.kind != snbtString ||
		strings.TrimPrefix(fn.str, "minecraft:") != "set_damage" {
		return st, false
	}
	if len(c.vals) != 2 {
		return st, false
	}
	dmg, ok := c.vals["damage"]
	if !ok || dmg.kind != snbtNumber {
		return st, false
	}
	f, err := strconv.ParseFloat(strings.TrimRight(dmg.num,
		"bBsSiIlLfFdD"), 64)
	if err != nil {
		return st, false
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	maxDmg, ok := damageableItems[mustItemName(st.ID)]
	if !ok {
		return st, false
	}
	st.Damage = int32((1 - float32(f)) * float32(maxDmg))
	return st, true
}

// mustParseItemSlot maps a slot token already parsed successfully.
func mustParseItemSlot(tok string) int32 {
	id, _ := parseItemSlot(tok)
	return id
}

// parseItemModifier parses the modifier of `item modify` or the token
// trailing a from-source. An id the item_modifier registry does not
// hold fails naming it; anything else parses as NBT for a loot
// function to apply. ok reports the modifier applied no functions -
// the only appliable modifier in a world without data packs is the
// empty list - so the target stack is unchanged.
func (s *Server) parseItemModifier(tok string, tokNum int) (reply, bool) {
	if prefix := idPrefix(tok); prefix != "" {
		echo := echoItemID(prefix)
		if prefix == tok {
			return errTokenEnd("argument.resource_or_id.no_such_element", tokNum,
				echo, "minecraft:item_modifier"), false
		}
		return errTokenOff("argument.resource_or_id.no_such_element", tokNum,
			len(prefix), echo, "minecraft:item_modifier"), false
	}
	if strings.HasPrefix(tok, "{") {
		return s.parseCompoundModifier(tok, tokNum)
	}
	if strings.HasPrefix(tok, "[") {
		return s.parseListModifier(tok, tokNum)
	}
	// Neither an id nor a tag: the reader reports both failures at the
	// end, as EXTRA shows.
	return errTokenEnd("argument.resource_or_id.failed_to_parse", tokNum,
		`Failed to parse either. First: Not a map: "`+tok+`"; Second: Not a list: "`+tok+`"`), false
}

// modEither wraps a bare compound's modifier failure the way the real
// server's resource-or-nbt reader does: it tried the token as a
// modifier and as a list, and names both failures.
func modEither(first, canon string) string {
	return "Failed to parse either. First: " + first + "; Second: Not a list: " + canon
}

// modFunctionID resolves a modifier's function value against the loot
// function registry. It returns the failure's bare message: the caller
// wraps it for a bare compound and reports it bare for a list element.
func modFunctionID(fn *snbtTag) string {
	if fn.kind != snbtString {
		return "Not a string"
	}
	name := strings.TrimPrefix(fn.str, "minecraft:")
	if !lootFunctionTypes[name] {
		return "Unknown registry key in ResourceKey[minecraft:root / minecraft:loot_function_type]: " + fn.str
	}
	return ""
}

// modResolveID reports a modifier compound's unknown string function
// id at the value's end, which is where the real server points when the
// id read fine but the registry does not hold it. Deeper levels stay
// data: only the modifier's own compound resolves.
func modResolveID(c *snbtTag) (msg string, pos int, has bool) {
	fn, ok := c.vals["function"]
	if !ok || fn.kind != snbtString {
		return "", 0, false
	}
	if msg := modFunctionID(fn); msg != "" {
		return msg, fn.end, true
	}
	return "", 0, false
}

// modApplyError decodes one parsed compound as a modifier: the missing
// function key, a function value that is not a string, or set_count
// without its count. An unknown string id is resolved separately, at
// the value's end. Anything else would apply, which needs a
// loot-function engine mc-go does not have, so the caller treats it as
// a no-op success. It returns "" when the modifier would apply.
func modApplyError(c *snbtTag) string {
	fn, ok := c.vals["function"]
	if !ok {
		return "No key function in MapLike[" + snbtCanon(c) + "]"
	}
	if fn.kind != snbtString {
		return "Not a string"
	}
	name := strings.TrimPrefix(fn.str, "minecraft:")
	if name == "set_count" {
		if _, ok := c.vals["count"]; !ok {
			return "No key count in MapLike[" + snbtCanon(c) + "]"
		}
	}
	return ""
}

// modElementError decodes one list-element compound: an unknown string
// id points at the value's end, everything else at the token's end.
func modElementError(el *snbtTag) (msg string, mid bool, pos int) {
	if m, p, has := modResolveID(el); has {
		return m, true, p
	}
	if m := modApplyError(el); m != "" {
		return m, false, 0
	}
	return "", false, 0
}

// parseCompoundModifier parses a bare-compound modifier: grammar
// failures point into the token, semantic ones come wrapped.
func (s *Server) parseCompoundModifier(tok string, tokNum int) (reply, bool) {
	c, next, lerr := parseSNBTCompound(tok, 0)
	if lerr != nil {
		return snbtReply(tokNum, tok, lerr), false
	}
	// Junk past a closed compound is ignored: {}y decodes the {} part
	// and fails on it, pointing past what it read.
	if msg, pos, has := modResolveID(c); has {
		return errTokenOffReply(tokNum, tok, pos,
			modEither(msg, snbtCanon(c))), false
	}
	if msg := modApplyError(c); msg != "" {
		return errTokenOffReply(tokNum, tok, next,
			modEither(msg, snbtCanon(c))), false
	}
	return reply{}, true
}

// parseListModifier parses a list modifier: the empty list applies
// zero functions, and each element decodes like a bare compound but
// reports its failure bare. Elements apply last to first, which is the
// order the real server joins their failures in.
func (s *Server) parseListModifier(tok string, tokNum int) (reply, bool) {
	l, next, lerr := parseSNBTList(tok, 0)
	if lerr != nil {
		return snbtReply(tokNum, tok, lerr), false
	}
	if next != len(tok) {
		// Junk past a closed list wants the end of the argument: []x
		// ends wanting command.expected.separator past the bracket.
		return errTokenOff("command.expected.separator", tokNum, next), false
	}
	if len(l.items) == 0 {
		return reply{}, true
	}
	type modFail struct {
		msg string
		mid bool
		pos int
	}
	var fails []modFail
	for i := len(l.items) - 1; i >= 0; i-- {
		el := l.items[i]
		if el.kind != snbtCompound {
			fails = append(fails, modFail{msg: "Not a map: " + snbtCanon(el)})
			continue
		}
		if msg, mid, pos := modElementError(el); msg != "" {
			fails = append(fails, modFail{msg: msg, mid: mid, pos: pos})
		}
	}
	if len(fails) == 1 && fails[0].mid {
		return errTokenOffReply(tokNum, tok, fails[0].pos, fails[0].msg), false
	}
	if len(fails) > 0 {
		msgs := make([]string, 0, len(fails))
		for _, f := range fails {
			msgs = append(msgs, f.msg)
		}
		return errTokenEnd("argument.resource_or_id.failed_to_parse", tokNum,
			strings.Join(msgs, "; ")), false
	}
	return reply{}, true
}

// snbtReply renders a grammar failure at its token offset: the end of
// the token when the input ran out there, else past the prefix read.
func snbtReply(tokNum int, tok string, lerr *snbtError) reply {
	if lerr.pos >= len(tok) {
		return errTokenEnd(lerr.key, tokNum, lerr.with...)
	}
	return errTokenOff(lerr.key, tokNum, lerr.pos, lerr.with...)
}

// errTokenOffReply reports a semantic failure at an absolute token
// offset: the end of the token there, else past the prefix read.
func errTokenOffReply(tokNum int, tok string, pos int, with ...any) reply {
	if pos >= len(tok) {
		return errTokenEnd("argument.resource_or_id.failed_to_parse", tokNum, with...)
	}
	return errTokenOff("argument.resource_or_id.failed_to_parse", tokNum, pos, with...)
}

// itemSource is one end of a from-variant: a block position or an entity
// target plus a parsed slot index.
type itemSource struct {
	block   bool
	x, y, z int
	target  *Session
	slot    int32
}

// parseItemSource parses the source after a `from`: args[at] is the
// kind token (token at+1). It returns the token after the source.
func (s *Server) parseItemSource(player string, args []string, at int) (itemSource, int, reply, bool) {
	var src itemSource
	if at >= len(args) {
		return src, 0, errTokenEnd("command.unknown.command", len(args)), false
	}
	switch args[at] {
	case "block":
		if len(args) < at+5 {
			return src, 0, errTokenEnd("command.unknown.command", len(args)), false
		}
		px, py, pz := s.baseBlockPos(player)
		vals := []int{px, py, pz}
		for i := 0; i < 3; i++ {
			v, err := parseCoord(args[at+1+i], vals[i])
			if err != nil {
				return src, 0, errToken("parsing.int.expected", at+2+i), false
			}
			vals[i] = v
		}
		id, ok := parseItemSlot(args[at+4])
		if !ok {
			return src, 0, errTokenEnd("slot.unknown", at+5, args[at+4]), false
		}
		src = itemSource{block: true, x: vals[0], y: vals[1], z: vals[2], slot: id}
		return src, at + 5, reply{}, true
	case "entity":
		if len(args) < at+3 {
			return src, 0, errTokenEnd("command.unknown.command", len(args)), false
		}
		tgt := s.resolveTarget(player, args[at+1])
		if tgt == nil {
			return src, 0, componentReply(commandError("argument.entity.notfound.entity")), false
		}
		id, ok := parseItemSlot(args[at+2])
		if !ok {
			return src, 0, errTokenEnd("slot.unknown", at+3, args[at+2]), false
		}
		src = itemSource{target: tgt, slot: id}
		return src, at + 3, reply{}, true
	}
	return src, 0, errToken("command.unknown.argument", at+1), false
}

// runItemReplace parses an `item replace` target and a with/from source,
// then runs it.
func (s *Server) runItemReplace(player string, args []string) reply {
	if args[1] == "block" {
		if len(args) < 6 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		x, y, z, tok, ok := s.itemBlockPos(player, args)
		if !ok {
			return errToken("parsing.int.expected", tok)
		}
		id, ok := parseItemSlot(args[5])
		if !ok {
			return errTokenEnd("slot.unknown", 6, args[5])
		}
		if len(args) < 7 {
			return errTokenEnd("command.unknown.command", 6)
		}
		d := s.dimOf(player)
		pos := world.Pos{X: x, Y: y, Z: z}
		switch args[6] {
		case "with":
			st, r, ok := s.parseItemStack(args, 8)
			if !ok {
				return r
			}
			return s.writeItemBlock(d, pos, id, st)
		case "from":
			src, next, r, ok := s.parseItemSource(player, args, 7)
			if !ok {
				return r
			}
			if len(args) > next {
				// A token after the source parses as a modifier, the
				// same one item modify takes.
				if r, ok := s.parseItemModifier(args[next], next+1); !ok {
					return r
				}
				if len(args) > next+1 {
					return errToken("command.unknown.argument", next+2)
				}
			}
			st, r, ok := s.readItemSource(d, src)
			if !ok {
				return r
			}
			return s.writeItemBlock(d, pos, id, st)
		}
		return errToken("command.unknown.argument", 7)
	}
	if len(args) < 4 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	tgt := s.resolveTarget(player, args[2])
	if tgt == nil {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	id, ok := parseItemSlot(args[3])
	if !ok {
		return errTokenEnd("slot.unknown", 4, args[3])
	}
	if len(args) < 5 {
		return errTokenEnd("command.unknown.command", 4)
	}
	switch args[4] {
	case "with":
		st, r, ok := s.parseItemStack(args, 6)
		if !ok {
			return r
		}
		return s.writeItemEntity(tgt, id, st)
	case "from":
		src, next, r, ok := s.parseItemSource(player, args, 5)
		if !ok {
			return r
		}
		if len(args) > next {
			// A token after the source parses as a modifier, the
			// same one item modify takes.
			if r, ok := s.parseItemModifier(args[next], next+1); !ok {
				return r
			}
			if len(args) > next+1 {
				return errToken("command.unknown.argument", next+2)
			}
		}
		d := s.dimOf(player)
		st, r, ok := s.readItemSource(d, src)
		if !ok {
			return r
		}
		return s.writeItemEntity(tgt, id, st)
	}
	return errToken("command.unknown.argument", 5)
}

// parseItemStack parses the item and optional count after a `with` whose
// item token sits at token itemTok. The stack's own cap is checked here,
// before any target is touched: an oversized stack fails naming the cap
// even where the target has no such slot. Air takes the same path with
// a cap of one, so `air 5` is refused while `air 1` clears the slot.
func (s *Server) parseItemStack(args []string, itemTok int) (ItemStack, reply, bool) {
	ai := itemTok - 1 // args index of the item token
	if len(args) <= ai {
		return ItemStack{}, errTokenEnd("command.unknown.command", itemTok-1), false
	}
	tok := args[ai]
	var comps string
	if i := strings.IndexByte(tok, '['); i >= 0 {
		if !strings.HasSuffix(tok, "]") {
			return ItemStack{}, errToken("command.unknown.argument", itemTok), false
		}
		comps = tok[i+1 : len(tok)-1]
		tok = tok[:i]
	}
	full := normalizeItemName(tok)
	id, ok := world.ItemID(shortItemName(full))
	if !ok {
		return ItemStack{}, errToken("argument.item.id.invalid", itemTok, echoItemID(idPrefix(tok))), false
	}
	count := int32(1)
	if len(args) > ai+1 {
		n, ok := parseItemCount(args[ai+1])
		if !ok {
			return ItemStack{}, errToken("parsing.int.expected", itemTok+1), false
		}
		if n < 1 {
			return ItemStack{}, errToken("argument.integer.low", itemTok+1, int32(1), n), false
		}
		if n > 99 {
			return ItemStack{}, errToken("argument.integer.big", itemTok+1, int32(99), n), false
		}
		count = n
		if len(args) > ai+2 {
			return ItemStack{}, errToken("command.unknown.argument", itemTok+2), false
		}
	}
	if count > maxStackOfID(id) {
		return ItemStack{}, componentReply(overstackedItem(ItemStack{ID: id, Count: count})), false
	}
	if id == 0 {
		return ItemStack{}, reply{}, true
	}
	st := ItemStack{ID: id, Count: count}
	if comps != "" {
		if r, ok := applyItemComponents(&st, comps, itemTok); !ok {
			return ItemStack{}, r, false
		}
	}
	return st, reply{}, true
}

// applyItemComponents applies the bracket grammar after an item spec:
// `name=value` sets a component the item model carries, `!name` clears
// one. Only the modeled set (damage, repair_cost, custom_name) is
// interpreted; anything else is ignored rather than rejected.
func applyItemComponents(st *ItemStack, comps string, tok int) (reply, bool) {
	for _, e := range splitComponents(comps) {
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "!") {
			name := shortItemName(normalizeItemName(strings.TrimPrefix(e, "!")))
			switch name {
			case "damage":
				st.Damage = 0
			case "repair_cost":
				st.RepairCost = 0
			}
			continue
		}
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return errToken("command.unknown.argument", tok), false
		}
		switch shortItemName(normalizeItemName(k)) {
		case "damage":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return errToken("parsing.int.expected", tok), false
			}
			st.Damage = int32(n)
		case "repair_cost":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return errToken("parsing.int.expected", tok), false
			}
			st.RepairCost = int32(n)
		}
	}
	return reply{}, true
}

// splitComponents splits a bracket list on top-level commas, so values
// holding commas inside {} or [] stay whole.
func splitComponents(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// readItemSource reads the stack a from-variant copies.
func (s *Server) readItemSource(d *dimension, src itemSource) (ItemStack, reply, bool) {
	if src.block {
		pos := world.Pos{X: src.x, Y: src.y, Z: src.z}
		st, r, ok := s.readItemBlock(d, pos, src.slot)
		if !ok {
			r.comp = sourceError(r.comp)
			return ItemStack{}, r, false
		}
		return st, reply{}, true
	}
	if !playerSlotValid(src.slot) {
		return ItemStack{}, componentReply(commandError("commands.item.source.no_such_slot", src.slot)), false
	}
	st, ok := readEntitySlot(src.target, src.slot)
	if !ok {
		return ItemStack{}, componentReply(commandError("commands.item.source.no_such_slot", src.slot)), false
	}
	return st, reply{}, true
}

// sourceError rewrites a block-target error into its source form: the
// real server reports which end of the copy failed.
func sourceError(c component) component {
	if len(c.extra) == 0 {
		return c
	}
	key := c.extra[0].translate
	switch key {
	case "commands.item.target.not_a_container":
		c.extra[0].translate = "commands.item.source.not_a_container"
	case "commands.item.target.no_such_slot":
		c.extra[0].translate = "commands.item.source.no_such_slot"
	}
	return c
}

// readItemBlock reads one slot of the container at pos.
func (s *Server) readItemBlock(d *dimension, pos world.Pos, id int32) (ItemStack, reply, bool) {
	blk := d.w.GetBlock(pos)
	switch {
	case isChestBlock(blk):
		if id >= chestSlots {
			return ItemStack{}, componentReply(commandError("commands.item.target.no_such_slot", id)), false
		}
		c := s.chestAt(d, pos)
		d.chests.mu.Lock()
		st := c.slots[id]
		d.chests.mu.Unlock()
		return st, reply{}, true
	case isFurnaceBlock(blk):
		if id >= furnaceSlots {
			return ItemStack{}, componentReply(commandError("commands.item.target.no_such_slot", id)), false
		}
		f := s.furnaceAt(d, pos)
		d.furnaces.mu.Lock()
		st := f.slots[id]
		d.furnaces.mu.Unlock()
		return st, reply{}, true
	case isHopperBlock(blk):
		if id >= hopperSlots {
			return ItemStack{}, componentReply(commandError("commands.item.target.no_such_slot", id)), false
		}
		h := s.hopperAt(d, pos)
		d.hoppers.mu.Lock()
		st := h.slots[id]
		d.hoppers.mu.Unlock()
		return st, reply{}, true
	}
	if spec, ok := containerSpecFor(blk); ok {
		if id >= int32(spec.slots) {
			return ItemStack{}, componentReply(commandError("commands.item.target.no_such_slot", id)), false
		}
		c := s.containerAt(d, pos)
		d.containers.mu.Lock()
		st := c.slots[id]
		d.containers.mu.Unlock()
		return st, reply{}, true
	}
	x, y, z := int32(pos.X), int32(pos.Y), int32(pos.Z)
	return ItemStack{}, componentReply(commandError("commands.item.target.not_a_container", x, y, z)), false
}

// writeItemBlock writes one slot of the container at pos and answers
// with the block success. A write has no slot filter - the filters
// belong to the menu, which this command does not open - and writing
// the furnace's input recomputes its cook total, which is what makes
// that field follow the slot.
func (s *Server) writeItemBlock(d *dimension, pos world.Pos, id int32, st ItemStack) reply {
	blk := d.w.GetBlock(pos)
	x, y, z := int32(pos.X), int32(pos.Y), int32(pos.Z)
	size := -1
	switch {
	case isChestBlock(blk):
		size = chestSlots
	case isFurnaceBlock(blk):
		size = furnaceSlots
	case isHopperBlock(blk):
		size = hopperSlots
	default:
		if spec, ok := containerSpecFor(blk); ok {
			size = spec.slots
		}
	}
	if size < 0 {
		return componentReply(commandError("commands.item.target.not_a_container", x, y, z))
	}
	if id >= int32(size) {
		return componentReply(commandError("commands.item.target.no_such_slot", id))
	}
	// The payload is validated before the target is touched, so the cap
	// check lives in the item parse; this stays as a safety net for
	// stacks that never went through one.
	if !st.Empty() && st.Count > maxStackOfID(st.ID) {
		return componentReply(overstackedItem(st))
	}
	switch {
	case isChestBlock(blk):
		c := s.chestAt(d, pos)
		d.chests.mu.Lock()
		c.slots[id] = st
		d.chests.mu.Unlock()
	case isFurnaceBlock(blk):
		f := s.furnaceAt(d, pos)
		d.furnaces.mu.Lock()
		f.slots[id] = st
		if id == furnaceInput {
			f.refreshCookTotal()
		}
		d.furnaces.mu.Unlock()
	case isHopperBlock(blk):
		h := s.hopperAt(d, pos)
		d.hoppers.mu.Lock()
		h.slots[id] = st
		d.hoppers.mu.Unlock()
	default:
		c := s.containerAt(d, pos)
		d.containers.mu.Lock()
		c.slots[id] = st
		d.containers.mu.Unlock()
	}
	return componentReply(translatable("commands.item.block.set.success",
		nestedCompound{key: "", value: x},
		nestedCompound{key: "", value: y},
		nestedCompound{key: "", value: z},
		itemBrackets(st)))
}

// overstackedItem is the refusal a stack larger than the item takes
// gets, naming the cap.
func overstackedItem(st ItemStack) component {
	name := mustItemName(st.ID)
	return commandError("arguments.item.overstacked",
		nestedCompound{key: "", value: "minecraft:" + name},
		nestedCompound{key: "", value: maxStackOfID(st.ID)})
}

// writeItemEntity writes one of a player's slots: out-of-range indices
// and armor writes the slot refuses fail with the item the slot would
// have held, and a stack larger than the item takes is refused with the
// cap named.
func (s *Server) writeItemEntity(sess *Session, id int32, st ItemStack) reply {
	if !playerSlotValid(id) {
		return componentReply(noChangedItem(st, id))
	}
	if !st.Empty() {
		name := mustItemName(st.ID)
		if (id >= itemSlotFeet && id <= itemSlotHead || id == itemSlotBody) && !armorSlotAccepts(id, name) {
			return componentReply(noChangedItem(st, id))
		}
		if st.Count > maxStackOfID(st.ID) {
			return componentReply(overstackedItem(st))
		}
	}
	s.writeEntitySlot(sess, id, st)
	return componentReply(translatable("commands.item.entity.set.success.single",
		entityTargetComponent(sess), itemBrackets(st)))
}
