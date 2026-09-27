package server

import (
	"bytes"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// World-level state that several serverbound handlers share: the
// permission table, the game-rule defaults, block-entity NBT for the
// tag-query responses, and the command-completion engine.

// defaultGameRules is the vanilla 26.2 game-rule table with its default
// values, captured verbatim from the real server (see
// TestCaptureVanillaGameRules). TestGameRulesMatchVanilla pins the
// whole table over the wire, so a default can never drift silently.
func defaultGameRules() map[string]string {
	// Captured verbatim from the real server's Game Rule Values reply
	// (TestCaptureVanillaGameRules); TestGameRulesMatchVanilla keeps it
	// pinned.
	return map[string]string{
		"minecraft:advance_time":                         "true",
		"minecraft:advance_weather":                      "true",
		"minecraft:allow_entering_nether_using_portals":  "true",
		"minecraft:block_drops":                          "true",
		"minecraft:block_explosion_drop_decay":           "true",
		"minecraft:command_block_output":                 "true",
		"minecraft:command_blocks_work":                  "true",
		"minecraft:drowning_damage":                      "true",
		"minecraft:elytra_movement_check":                "true",
		"minecraft:ender_pearls_vanish_on_death":         "true",
		"minecraft:entity_drops":                         "true",
		"minecraft:fall_damage":                          "true",
		"minecraft:fire_damage":                          "true",
		"minecraft:fire_spread_radius_around_player":     "128",
		"minecraft:forgive_dead_players":                 "true",
		"minecraft:freeze_damage":                        "true",
		"minecraft:global_sound_events":                  "true",
		"minecraft:immediate_respawn":                    "false",
		"minecraft:keep_inventory":                       "false",
		"minecraft:lava_source_conversion":               "false",
		"minecraft:limited_crafting":                     "false",
		"minecraft:locator_bar":                          "true",
		"minecraft:log_admin_commands":                   "true",
		"minecraft:max_block_modifications":              "32768",
		"minecraft:max_command_forks":                    "65536",
		"minecraft:max_command_sequence_length":          "65536",
		"minecraft:max_entity_cramming":                  "24",
		"minecraft:max_snow_accumulation_height":         "1",
		"minecraft:mob_drops":                            "true",
		"minecraft:mob_explosion_drop_decay":             "true",
		"minecraft:mob_griefing":                         "true",
		"minecraft:natural_health_regeneration":          "true",
		"minecraft:player_movement_check":                "true",
		"minecraft:players_nether_portal_creative_delay": "0",
		"minecraft:players_nether_portal_default_delay":  "80",
		"minecraft:players_sleeping_percentage":          "100",
		"minecraft:projectiles_can_break_blocks":         "true",
		"minecraft:pvp":                                  "true",
		"minecraft:raids":                                "true",
		"minecraft:random_tick_speed":                    "3",
		"minecraft:reduced_debug_info":                   "false",
		"minecraft:respawn_radius":                       "10",
		"minecraft:send_command_feedback":                "true",
		"minecraft:show_advancement_messages":            "true",
		"minecraft:show_death_messages":                  "true",
		"minecraft:spawn_mobs":                           "true",
		"minecraft:spawn_monsters":                       "true",
		"minecraft:spawn_patrols":                        "true",
		"minecraft:spawn_phantoms":                       "true",
		"minecraft:spawn_wandering_traders":              "true",
		"minecraft:spawn_wardens":                        "true",
		"minecraft:spawner_blocks_work":                  "true",
		"minecraft:spectators_generate_chunks":           "true",
		"minecraft:spread_vines":                         "true",
		"minecraft:tnt_explodes":                         "true",
		"minecraft:tnt_explosion_drop_decay":             "false",
		"minecraft:universal_anger":                      "false",
		"minecraft:water_source_conversion":              "true",
	}
}

// blockEntityTag renders the NBT the Tag Query Response carries for the
// block entity at pos in dimension d, or nil when the block has none
// (vanilla answers with a bare TAG_End in that case). The block at pos
// decides which block entity applies, so a query for a plain block
// never conjures storage.
func (s *Server) blockEntityTag(d *dimension, pos world.Pos) []byte {
	blk := d.w.GetBlock(pos)
	if _, ok := signSpecOf(blk); ok {
		if st, ok := s.signAt(d, pos); ok {
			return signNBT(st)
		}
		return nil
	}
	name, _ := world.StateOwner(blk)
	base := strings.TrimPrefix(name, "minecraft:")
	switch base {
	case "chest", "trapped_chest", "barrel":
		if c := s.peekChest(d, pos); c != nil {
			return containerNBT(name, c.slots[:], d.beNames[pos], c.lootTable, c.lootSeed)
		}
	case "furnace", "blast_furnace", "smoker":
		if f := s.peekFurnace(d, pos); f != nil {
			return containerNBT(name, f.slots[:], d.beNames[pos], "", 0)
		}
	case "hopper":
		if h := s.peekHopper(d, pos); h != nil {
			return containerNBT(name, h.slots[:], d.beNames[pos], "", 0)
		}
	case "beacon":
		return beaconNBT(name, d, pos)
	}
	// The generic container store covers shulker boxes, dispensers,
	// brewing stands, crafters and the rest — everything that routes
	// through containerSpecFor.
	if _, ok := containerSpecFor(blk); ok {
		d.containers.mu.Lock()
		c := d.containers.containers[pos]
		d.containers.mu.Unlock()
		if c != nil {
			return containerNBT(name, c.slots, d.beNames[pos], c.lootTable, c.lootSeed)
		}
		// A container record exists only after the menu or a tag
		// touched it; a virgin container still answers its empty NBT.
		return containerNBT(name, nil, d.beNames[pos], "", 0)
	}
	return nil
}

// peekChest returns existing chest storage without creating any.
func (s *Server) peekChest(d *dimension, pos world.Pos) *chestData {
	d.chests.mu.Lock()
	defer d.chests.mu.Unlock()
	return d.chests.chests[pos]
}

// peekFurnace returns existing furnace storage without creating any.
func (s *Server) peekFurnace(d *dimension, pos world.Pos) *furnaceData {
	d.furnaces.mu.Lock()
	defer d.furnaces.mu.Unlock()
	return d.furnaces.furnaces[pos]
}

// peekHopper returns existing hopper storage without creating any.
func (s *Server) peekHopper(d *dimension, pos world.Pos) *hopperData {
	d.hoppers.mu.Lock()
	defer d.hoppers.mu.Unlock()
	return d.hoppers.hoppers[pos]
}

// containerNBT builds a container block entity's NBT: the vanilla
// "Items" list of {Slot, id, count} compounds plus the identifying id,
// with the block entity's CustomName (raw component JSON) and
// LootTable/LootTableSeed fields when present.
func containerNBT(id string, slots []ItemStack, customName string, lootTable string, lootSeed int64) []byte {
	items := nbt.List{Elem: nbt.TagCompound}
	for i, it := range slots {
		if it.Empty() {
			continue
		}
		name, ok := itemNameOf(it.ID)
		if !ok {
			continue
		}
		items.Items = append(items.Items, map[string]any{
			"Slot":  int8(i),
			"id":    name,
			"count": int32(it.Count),
		})
	}
	m := map[string]any{
		"id": id,
	}
	// trySaveLootTable: an armed table replaces Items in the saved tag.
	if lootTable != "" {
		m["LootTable"] = lootTable
		if lootSeed != 0 {
			m["LootTableSeed"] = lootSeed
		}
	} else {
		m["Items"] = items
	}
	if customName != "" {
		m["CustomName"] = customName
	}
	var buf bytes.Buffer
	_ = nbt.WriteUnnamed(&buf, m)
	return buf.Bytes()
}

// beaconNBT renders a beacon block entity's tag the way the real
// server saves it: Levels plus the chosen effect ids. The payment slot
// lives on the menu container, so the block entity never carries Items.
func beaconNBT(id string, d *dimension, pos world.Pos) []byte {
	m := map[string]any{"id": id}
	levels := int32(0)
	if be := d.beacons[pos]; be != nil {
		levels = int32(be.levels)
		if be.primary != "" {
			m["primary_effect"] = "minecraft:" + be.primary
		}
		if be.secondary != "" {
			m["secondary_effect"] = "minecraft:" + be.secondary
		}
	}
	m["Levels"] = levels
	if n := d.beNames[pos]; n != "" {
		m["CustomName"] = n
	}
	var buf bytes.Buffer
	_ = nbt.WriteUnnamed(&buf, m)
	return buf.Bytes()
}

// selectHotbar switches the held hotbar slot and re-syncs the client's
// selected-slot display plus the equipment broadcast.
func (sess *Session) selectHotbar(slot int32) {
	if slot < 0 || slot > 8 {
		return
	}
	sess.held.Store(slot)
	_ = sess.server.sendSetHeldSlot(sess, slot)
	sess.server.broadcastEquipment(sess)
}

// dispatchCommandText runs a slash command's text through the command
// registry, the shared path for Chat Command, signed Chat Command, and
// commands run from other packets. text may include or omit the leading
// slash.
func (s *Server) dispatchCommandText(sess *Session, text string) {
	body := strings.TrimPrefix(text, "/")
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return
	}
	name := sess.getName()
	input := body
	r, ok := s.runCommand(name, fields[0], fields[1:])
	if !ok {
		r = reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if s.gameRuleBool("minecraft:send_command_feedback") {
		s.sendReply(sess, r)
		if r.fail != nil {
			s.sendSystemChatComponent(sess, errorContext(input, r.fail.cursor(input)))
		}
	}
}

// handleInteract processes a right-click use on an entity: Interact
// (0x1A). The 26.2 record is (entityId, hand, location LpVec3,
// usingSecondaryAction) — there is no action discriminator: attacks
// arrive as their own Attack packet (0x01). The LpVec3 starts with a
// byte that is 0 for Vec3.ZERO, otherwise three packed fields over six
// bytes (plus a trailing VarInt when bit 2 of the first byte is set).
// mc-go ignores the click position; vanilla uses it only for range.
func (s *Server) handleInteract(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.VarInt()
	if err != nil {
		return
	}
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	var clickY float64
	_, clickY, _, _ = r.ReadLpVec3()
	sneaking, err := r.Bool()
	if err != nil {
		return
	}
	// Vanilla drops the interact entirely when the eye-to-entity-box
	// distance exceeds entity_interaction_range (base 3.0) + 3.0.
	if !s.withinInteractRange(sess, id) {
		return
	}
	// Vanilla's Player.interactOn: entity.interact first — sneak
	// leash-transfer, leash-shears, equipment-shears, then the
	// unleash/lead-attach pair — each returning SUCCESS to end the
	// call; then mobInteract's handlers; the item's
	// interactLivingEntity (wool shears) runs last.
	if s.leashShiftTransfer(sess, id, sneaking) {
		return
	}
	if s.shearOffLeashConnections(sess, id, hand) {
		return
	}
	if s.shearAnimal(sess, id, hand, sneaking) {
		return
	}
	if s.leashUnleashOrAttach(sess, id, hand) {
		return
	}
	s.bucketFish(sess, id, hand)
	s.feedAnimal(sess, id, hand)
	s.tameAnimal(sess, id, hand)
	if s.equipMountChest(sess, id, hand, sneaking) {
		return
	}
	s.openMountInventory(sess, id, sneaking)
	s.rideVehicle(sess, id, sneaking)
	s.tradeVillager(sess, id, hand)
	s.giveAllay(sess, id, hand)
	s.cureZombieVillager(sess, id, hand)
	s.armorStandInteract(sess, id, hand, clickY)
	s.shearMobItemPhase(sess, id, hand, sneaking)
	s.interactEntityAdv(sess, id, hand)
}

// withinInteractRange is ServerGamePacketListenerImpl.handleInteract's
// isWithinEntityInteractionRange gate: the eye position's distance to
// the entity's bounding box must be under attribute
// entity_interaction_range (base 3.0, unmodified here) plus a fixed
// 3.0 of slack — 6.0 blocks to the nearest box face.
func (s *Server) withinInteractRange(sess *Session, eid int32) bool {
	px, py, pz, _, _, _ := sess.Pos()
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	var t Entity
	if ok {
		t = *e
	}
	s.entities.mu.RUnlock()
	if !ok {
		return false
	}
	name, _ := EntityTypeName(t.Type)
	bw, bh := entityBox(name)
	ex, ey, ez := px-t.X, py+1.62-(t.Y+bh/2), pz-t.Z
	// Nearest point on the box to the eye: clamp each axis.
	hw := bw / 2
	if ex > hw {
		ex = hw
	} else if ex < -hw {
		ex = -hw
	}
	hh := bh / 2
	if ey > hh {
		ey = hh
	} else if ey < -hh {
		ey = -hh
	}
	if ez > hw {
		ez = hw
	} else if ez < -hw {
		ez = -hw
	}
	dx := px - (t.X + ex)
	dy := py + 1.62 - (t.Y + bh/2 + ey)
	dz := pz - (t.Z + ez)
	return dx*dx+dy*dy+dz*dz < 36
}

// handlePlayerCommand processes Player Command (0x2A): sneak, sprint,
// leave bed, mount, and elytra transitions. The action enum is fixed by
// the protocol; unrecognised ids are ignored, matching vanilla.
func (s *Server) handlePlayerCommand(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // entity id
		return
	}
	action, err := r.VarInt()
	if err != nil {
		return
	}
	if _, err := r.VarInt(); err != nil { // jump boost
		return
	}
	// Vanilla 26.2's PlayerCommand actions; sneaking left the enum for
	// the player_input packet.
	switch action {
	case 0: // stop sleeping
		// Sleeping is not modelled; vanilla clears the sleeping flag.
	case 1: // start sprinting
		sess.sprinting = true
		s.broadcastSharedFlags(sess)
	case 2: // stop sprinting
		sess.sprinting = false
		s.broadcastSharedFlags(sess)
	case 3, 4: // start/stop riding jump
		// Requires a ridden horse; vanilla ignores it without one.
	case 5: // open vehicle inventory
		// Requires a ridden entity; vanilla ignores it without one.
	case 6: // start flying with elytra
		// Vanilla runs tryToStartFallFlying regardless of game mode —
		// canGlide's airborne/passenger/levitation/glider checks do
		// the gating; a second packet while gliding toggles it off.
		s.tryStartFallFlying(sess)
	}
}

// commandArgSuggesters supplies argument completions per command. Each
// entry answers for the argument position (0 is the first argument
// after the command name) and may inspect the earlier arguments.
var commandArgSuggesters = map[string]func(*Server, *Session, []string) []string{
	"give": func(s *Server, sess *Session, prior []string) []string {
		if len(prior) == 0 {
			return s.onlinePlayerNames()
		}
		return itemNames()
	},
	"tp": func(s *Server, sess *Session, prior []string) []string {
		return s.onlinePlayerNames()
	},
	"teleport": func(s *Server, sess *Session, prior []string) []string {
		return s.onlinePlayerNames()
	},
	"kick": func(s *Server, sess *Session, prior []string) []string {
		return s.onlinePlayerNames()
	},
	"summon": func(s *Server, sess *Session, prior []string) []string {
		return allEntityTypeNames()
	},
	"execute": func(s *Server, sess *Session, prior []string) []string {
		if len(prior) == 0 {
			return []string{"in"}
		}
		if prior[0] == "in" && len(prior) == 1 {
			return []string{"minecraft:overworld", "minecraft:the_end", "minecraft:the_nether"}
		}
		return nil
	},
	"setblock": func(s *Server, sess *Session, prior []string) []string {
		if len(prior) < 3 {
			return nil
		}
		return blockNames()
	},
	"gamemode": func(s *Server, sess *Session, prior []string) []string {
		return []string{"survival", "creative", "adventure", "spectator"}
	},
	"difficulty": func(s *Server, sess *Session, prior []string) []string {
		return []string{"peaceful", "easy", "normal", "hard"}
	},
	"gamerule": func(s *Server, sess *Session, prior []string) []string {
		if len(prior) == 0 {
			return s.gameRuleNames()
		}
		return []string{"true", "false"}
	},
	"kill": func(s *Server, sess *Session, prior []string) []string {
		return s.onlinePlayerNames()
	},
	"effect": func(s *Server, sess *Session, prior []string) []string {
		if len(prior) == 0 {
			return s.onlinePlayerNames()
		}
		return nil
	},
}

// handleCommandSuggestion answers a tab-completion request with the
// Command Suggestions Response. The completion covers the last word of
// the request text, exactly like vanilla's ask_server suggestions.
func (s *Server) handleCommandSuggestion(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	tx, err := r.VarInt()
	if err != nil {
		return
	}
	text, err := r.String()
	if err != nil {
		return
	}
	start, length, matches := s.suggestCompletions(sess, text)
	var b protocol.Buffer
	b.VarInt(tx)
	b.VarInt(int32(start))
	b.VarInt(int32(length))
	b.VarInt(int32(len(matches)))
	for _, m := range matches {
		b.String(m)
		b.Bool(false) // no tooltip
	}
	_ = sess.write(sess.ids().PlayCommandSuggestionsCB, b.Bytes())
}

// suggestCompletions returns the replacement range and matching values
// for the word ending at the cursor. text is the request body, which
// includes the leading slash for commands.
func (s *Server) suggestCompletions(sess *Session, text string) (start, length int, matches []string) {
	body := text
	offset := 0
	if strings.HasPrefix(body, "/") {
		body = body[1:]
		offset = 1
	}
	// The word under the cursor starts after the last space.
	wordStart := strings.LastIndexByte(body, ' ') + 1
	word := body[wordStart:]
	start = offset + wordStart
	length = len(word)
	prior := strings.Fields(body[:wordStart])

	if len(prior) == 0 {
		// Completing the command name itself.
		for _, name := range s.commands.Names() {
			if strings.HasPrefix(name, word) {
				matches = append(matches, name)
			}
		}
		sort.Strings(matches)
		return start, length, matches
	}
	provider := commandArgSuggesters[prior[0]]
	if provider == nil {
		return start, length, nil
	}
	for _, cand := range provider(s, sess, prior[1:]) {
		if strings.HasPrefix(cand, word) {
			matches = append(matches, cand)
		}
	}
	sort.Strings(matches)
	if len(matches) > 64 {
		matches = matches[:64]
	}
	return start, length, matches
}

// onlinePlayerNames lists the names of players in the play stage.
func (s *Server) onlinePlayerNames() []string {
	var out []string
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay {
			return true
		}
		if name := sess.getName(); name != "" {
			out = append(out, name)
		}
		return true
	})
	sort.Strings(out)
	return out
}

// gameRuleNames lists the game rules with their minecraft: prefix
// stripped, which is how /gamerule names them.
func (s *Server) gameRuleNames() []string {
	s.rulesMu.RLock()
	out := make([]string, 0, len(s.gameRules))
	for name := range s.gameRules {
		out = append(out, strings.TrimPrefix(name, "minecraft:"))
	}
	s.rulesMu.RUnlock()
	sort.Strings(out)
	return out
}

// itemNames lists every item registry name with its minecraft: prefix
// stripped, the form the vanilla client completes.
func itemNames() []string {
	out := make([]string, 0, len(world.ItemIDs))
	for name := range world.ItemIDs {
		out = append(out, strings.TrimPrefix(name, "minecraft:"))
	}
	sort.Strings(out)
	return out
}

// blockNames lists every block registry name, prefix stripped.
func blockNames() []string {
	all := world.BlockNames()
	out := make([]string, 0, len(all))
	for _, name := range all {
		out = append(out, strings.TrimPrefix(name, "minecraft:"))
	}
	sort.Strings(out)
	return out
}

// allEntityTypeNames lists every entity type name, prefix stripped.
func allEntityTypeNames() []string {
	out := make([]string, 0, len(entityTypeNames))
	for _, name := range entityTypeNames {
		out = append(out, strings.TrimPrefix(name, "minecraft:"))
	}
	sort.Strings(out)
	return out
}
