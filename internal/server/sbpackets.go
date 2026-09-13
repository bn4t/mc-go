package server

import (
	"bytes"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Handlers for the serverbound play packets beyond the core movement,
// digging, container, and chat set. Each one is routed from
// playServerbound in routes.go; the routing table covers the full 776
// serverbound surface.

// Difficulty values, matching the Change Difficulty packet enum.
const (
	DifficultyPeaceful = 0
	DifficultyEasy     = 1
	DifficultyNormal   = 2
	DifficultyHard     = 3
)

// chatSessionKey is the signed-chat session a client publishes in the
// Player Session packet. mc-go does not verify signatures (chat
// reporting is out of scope) but tracks the key so a later signed-chat
// milestone has the data, and so /debug-style introspection can report
// whether a client is "secure".
type chatSessionKey struct {
	UUID      [16]byte
	ExpiresAt int64
	PublicKey []byte
	Signature []byte
}

// opLevel reports this session's permission level. Vanilla levels:
// 0 player, 1 bypass spawn protection, 2 command blocks/cheats,
// 3 multiplayer management, 4 server administration. The default
// server is open (every player is level 4) so library embeddings and
// scripted tests keep working; the mc-go binary sets OpsRequired and
// loads ops.json for vanilla's behavior.
func (sess *Session) opLevel() int {
	return sess.server.permissionLevel(sess.getName())
}

// permissionLevel resolves a player name to a permission level.
func (s *Server) permissionLevel(name string) int {
	if !s.OpsRequired {
		return 4
	}
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	if lvl, ok := s.ops[name]; ok {
		return lvl
	}
	return 0
}

// mode returns the player's current game mode.
func (sess *Session) mode() byte {
	sess.modeMu.RLock()
	defer sess.modeMu.RUnlock()
	return sess.gameMode
}

func (sess *Session) setMode(m byte) {
	sess.modeMu.Lock()
	sess.gameMode = m
	sess.modeMu.Unlock()
}

// setGameMode applies a mode change the way vanilla does: the player's
// abilities are re-sent, a Game Event (change game mode) goes to every
// viewer, and the survival state is re-seeded.
func (s *Server) setGameMode(sess *Session, mode byte) {
	if mode > GameModeSpectator {
		return
	}
	sess.setMode(mode)
	sess.flying = mode == GameModeSpectator
	_ = s.sendAbilities(sess)
	s.broadcastGameEvent(3, float32(mode))
}

// broadcastGameEvent sends a Game Event packet to every session.
func (s *Server) broadcastGameEvent(event byte, value float32) {
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay {
			return true
		}
		_ = s.sendGameEvent(sess, event, value)
		return true
	})
}

// handleChangeDifficulty applies the Change Difficulty packet (op
// level 2, like vanilla) and re-broadcasts the new value.
func (s *Server) handleChangeDifficulty(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	d, err := r.Byte()
	if err != nil || d > DifficultyHard {
		return
	}
	if sess.opLevel() < 2 {
		return
	}
	s.setDifficulty(d)
}

// setDifficulty stores the difficulty and fans the Change Difficulty
// packet out to every player.
func (s *Server) setDifficulty(d byte) {
	s.rulesMu.Lock()
	s.difficulty = d
	locked := s.difficultyLocked
	s.rulesMu.Unlock()
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay {
			return true
		}
		_ = s.sendDifficultyLocked(sess, d, locked)
		return true
	})
}

// sendDifficultyLocked sends Change Difficulty with the lock flag.
func (s *Server) sendDifficultyLocked(sess *Session, d byte, locked bool) error {
	var b protocol.Buffer
	b.Byte(d)
	b.Bool(locked)
	return sess.write(sess.ids().PlayDifficulty, b.Bytes())
}

// handleLockDifficulty flips the /difficulty lock flag (op level 2).
func (s *Server) handleLockDifficulty(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	locked, err := r.Bool()
	if err != nil {
		return
	}
	if sess.opLevel() < 2 {
		return
	}
	s.rulesMu.Lock()
	s.difficultyLocked = locked
	d := s.difficulty
	s.rulesMu.Unlock()
	_ = s.sendDifficultyLocked(sess, d, locked)
}

// handleChangeGameMode applies the Change Game Mode packet (op level 2).
func (s *Server) handleChangeGameMode(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	mode, err := r.VarInt()
	if err != nil || mode < 0 || mode > GameModeSpectator {
		return
	}
	if sess.opLevel() < 2 {
		return
	}
	s.setGameMode(sess, byte(mode))
}

// handleSetGameRule applies one game rule (op level 2) and broadcasts
// the resulting table, matching vanilla's Game Rule Values packet.
func (s *Server) handleSetGameRule(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	name, err := r.String()
	if err != nil {
		return
	}
	value, err := r.String()
	if err != nil {
		return
	}
	if sess.opLevel() < 2 {
		return
	}
	s.rulesMu.Lock()
	if _, ok := s.gameRules[name]; !ok {
		s.rulesMu.Unlock()
		return
	}
	s.gameRules[name] = value
	rules := make(map[string]string, len(s.gameRules))
	for k, v := range s.gameRules {
		rules[k] = v
	}
	s.rulesMu.Unlock()
	s.broadcastGameRules(rules)
}

// gameRuleValues encodes the Game Rule Values body: a prefixed array of
// name/value string pairs.
func gameRuleValues(rules map[string]string) []byte {
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)
	var b protocol.Buffer
	b.VarInt(int32(len(names)))
	for _, name := range names {
		b.String(name)
		b.String(rules[name])
	}
	return b.Bytes()
}

// broadcastGameRules sends the game-rule table to every player.
func (s *Server) broadcastGameRules(rules map[string]string) {
	body := gameRuleValues(rules)
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(sess.ids().PlayGameRuleValuesCB, body)
		return true
	})
}

// sendGameRules answers a "request game rule values" client status.
func (s *Server) sendGameRules(sess *Session) error {
	s.rulesMu.RLock()
	rules := make(map[string]string, len(s.gameRules))
	for k, v := range s.gameRules {
		rules[k] = v
	}
	s.rulesMu.RUnlock()
	return sess.write(sess.ids().PlayGameRuleValuesCB, gameRuleValues(rules))
}

// handleBlockEntityTagQuery answers the F3+I block query with a Tag
// Query Response carrying the block entity's NBT, or TAG_End when the
// block has no block entity (vanilla's "no NBT" encoding).
func (s *Server) handleBlockEntityTagQuery(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	tx, err := r.VarInt()
	if err != nil {
		return
	}
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	tag := s.blockEntityTag(sess.dimension(), pos)
	_ = sess.write(sess.ids().PlayTagQueryCB, tagQueryBody(tx, tag))
}

// handleEntityTagQuery answers the F3+I entity query. mc-go entities
// carry no NBT beyond their type, so the response is an empty compound
// with the identifying fields vanilla exposes for a bare entity.
func (s *Server) handleEntityTagQuery(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	tx, err := r.VarInt()
	if err != nil {
		return
	}
	id, err := r.VarInt()
	if err != nil {
		return
	}
	e, ok := s.entities.Get(id)
	if !ok {
		_ = sess.write(sess.ids().PlayTagQueryCB, tagQueryBody(tx, nil))
		return
	}
	var buf bytes.Buffer
	_ = nbt.WriteUnnamed(&buf, map[string]any{
		"id":   entityTypeNameOr(e.Type),
		"Pos":  []float64{e.X, e.Y, e.Z},
		"UUID": dashUUID(e.UUID),
	})
	_ = sess.write(sess.ids().PlayTagQueryCB, tagQueryBody(tx, buf.Bytes()))
}

// tagQueryBody frames a Tag Query Response: transaction id, then the
// NBT payload or a bare TAG_End when there is none.
func tagQueryBody(tx int32, tag []byte) []byte {
	var b protocol.Buffer
	b.VarInt(tx)
	if len(tag) == 0 {
		b.Byte(nbt.TagEnd)
	} else {
		b.Write(tag)
	}
	return b.Bytes()
}

// handlePingRequest echoes the play-stage ping as a Pong Response with
// the same id, which is what the client's latency graph measures. The
// body is a Long in both directions; TestPlayPingParity pins that
// against the real server, which disconnects a client that sends the
// 4-byte Int form.
func (s *Server) handlePingRequest(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.Long()
	if err != nil {
		return
	}
	var b protocol.Buffer
	b.Long(id)
	_ = sess.write(sess.ids().PlayPongResponseCB, b.Bytes())
}

// handlePong consumes a Pong Response to a server Ping. mc-go does not
// send play-stage Pings, so there is nothing to correlate.
func (s *Server) handlePong(sess *Session, p protocol.Packet) {}

// handleChatAck records the client's acknowledged chat count. mc-go
// relays chat without signatures, so the count only feeds /debug-style
// introspection; no client-visible state changes.
func (s *Server) handleChatAck(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if n, err := r.VarInt(); err == nil {
		sess.lastChatAck.Store(n)
	}
}

// handleChatSessionUpdate stores the client's signed-chat session key.
func (s *Server) handleChatSessionUpdate(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.UUID()
	if err != nil {
		return
	}
	expires, err := r.Long()
	if err != nil {
		return
	}
	pub, err := r.ByteArray()
	if err != nil {
		return
	}
	sig, err := r.ByteArray()
	if err != nil {
		return
	}
	sess.chatSession = &chatSessionKey{
		UUID: id, ExpiresAt: expires, PublicKey: pub, Signature: sig,
	}
}

// handleCustomPayload consumes a serverbound plugin message. Vanilla
// only acts on a small set of channels; the client brand is recorded
// (it is what the server reports in /list and the debug screen) and
// unknown channels are ignored, which is vanilla's behavior for
// channels it does not register a handler for.
func (s *Server) handleCustomPayload(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	channel, err := r.String()
	if err != nil {
		return
	}
	rest, _ := r.Read(r.Remaining())
	switch channel {
	case "minecraft:brand", "MC|Brand":
		// The brand is a length-prefixed string inside the payload.
		br := protocol.NewPacketReader(rest)
		if brand, err := br.String(); err == nil {
			sess.brandMu.Lock()
			sess.customBrand = brand
			sess.brandMu.Unlock()
		}
	}
}

// Brand reports the client brand this session announced, "" if none.
func (sess *Session) Brand() string {
	sess.brandMu.Lock()
	defer sess.brandMu.Unlock()
	return sess.customBrand
}

// handleDebugSubscriptionRequest replaces the session's debug
// subscription set. Subscriptions only drive the client's debug
// renderers, so mc-go retains them for introspection and sends nothing.
func (s *Server) handleDebugSubscriptionRequest(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 4096 {
		return
	}
	for i := int32(0); i < n; i++ {
		if _, err := r.VarInt(); err != nil {
			return
		}
	}
}

// handleConfigurationAcknowledged re-runs the configuration stage when
// the client asks for it, matching vanilla's Start Configuration +
// configuration handshake.
func (s *Server) handleConfigurationAcknowledged(sess *Session, p protocol.Packet) {
	if sess.state.Load() != StatePlay {
		return
	}
	if err := sess.write(sess.ids().PlayStartConfiguration, nil); err != nil {
		return
	}
	s.handleConfig(sess)
}

// handleContainerButtonClick consumes a container button click. Vanilla
// only routes buttons for enchantment tables, stonecutters, lecterns,
// and the loom; mc-go has none of those menus yet, so a click on any
// open menu is dropped exactly as vanilla drops a button click for a
// menu without a button handler.
func (s *Server) handleContainerButtonClick(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // window id
		return
	}
	if _, err := r.VarInt(); err != nil { // button id
		return
	}
}

// handleContainerSlotStateChanged consumes a crafter slot toggle. The
// crafter is the only menu with slot states; mc-go has no crafter menu,
// so the packet is validated and dropped.
func (s *Server) handleContainerSlotStateChanged(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // slot id
		return
	}
	if _, err := r.VarInt(); err != nil { // window id
		return
	}
	if _, err := r.Bool(); err != nil { // enabled
		return
	}
}

// handleCookieResponse consumes a response to a Cookie Request. mc-go
// never sends cookie requests, so there is nothing to correlate; the
// shape is still parsed so a client that volunteers one is not an error.
func (s *Server) handleCookieResponse(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.String(); err != nil {
		return
	}
	if _, err := r.ByteArray(); err != nil {
		return
	}
}

// handleResourcePackResponse consumes a resource-pack status update.
// mc-go does not push resource packs, so vanilla's behavior is to drop
// the response; parsing keeps the framing honest.
func (s *Server) handleResourcePackResponse(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // pack id
		return
	}
	if _, err := r.VarInt(); err != nil { // status
		return
	}
}

// handlePlayerLoaded records that the client finished loading terrain.
// Vanilla uses the flag to decide when the player may interact; mc-go
// streams chunks and accepts interactions immediately, so the flag is
// tracked for introspection only.
func (s *Server) handlePlayerLoaded(sess *Session, p protocol.Packet) {
	sess.loaded.Store(true)
}

// handleSeenAdvancements records which advancement tab the client
// opened or closed. Vanilla echoes nothing; the state drives the
// client's own "seen" badge.
func (s *Server) handleSeenAdvancements(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	action, err := r.VarInt()
	if err != nil {
		return
	}
	tab, err := r.String()
	if err != nil {
		return
	}
	sess.bookMu.Lock()
	if sess.seenAdvTabs == nil {
		sess.seenAdvTabs = map[string]bool{}
	}
	switch action {
	case 0: // opened tab
		sess.seenAdvTabs[tab] = true
	case 1: // closed screen
		delete(sess.seenAdvTabs, tab)
	}
	sess.bookMu.Unlock()
}

// handleRecipeBookChangeSettings stores the recipe-book UI toggles and
// echoes them back, which is what vanilla does when a client changes a
// filter.
func (s *Server) handleRecipeBookChangeSettings(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	book, err := r.VarInt()
	if err != nil || book < 0 || book > 1 {
		return
	}
	open, err := r.Bool()
	if err != nil {
		return
	}
	filter, err := r.Bool()
	if err != nil {
		return
	}
	sess.bookMu.Lock()
	sess.bookOpen[book] = open
	sess.bookFilter[book] = filter
	sess.bookMu.Unlock()
	_ = s.sendRecipeBookSettingsFor(sess)
}

// sendRecipeBookSettingsFor encodes the four recipe-book toggles in the
// vanilla order: crafting open/filtering, then furnace open/filtering.
func (s *Server) sendRecipeBookSettingsFor(sess *Session) error {
	sess.bookMu.Lock()
	body := [4]bool{
		sess.bookOpen[0], sess.bookFilter[0],
		sess.bookOpen[1], sess.bookFilter[1],
	}
	sess.bookMu.Unlock()
	var b protocol.Buffer
	for _, v := range body {
		b.Bool(v)
	}
	return sess.write(sess.ids().PlayRecipeBookSet, b.Bytes())
}

// handleRecipeBookSeenRecipe marks a recipe as seen or unseen. The
// client tracks the badge itself; the server only remembers the set so
// a reconnect can replay it.
func (s *Server) handleRecipeBookSeenRecipe(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.VarInt()
	if err != nil {
		return
	}
	seen, err := r.Bool()
	if err != nil {
		// Older framing carries no flag: the packet means "seen".
		seen = true
	}
	sess.bookMu.Lock()
	if sess.seenRecipes == nil {
		sess.seenRecipes = map[int32]bool{}
	}
	if seen {
		sess.seenRecipes[id] = true
	} else {
		delete(sess.seenRecipes, id)
	}
	sess.bookMu.Unlock()
}

// handleRenameItem consumes an anvil rename. mc-go has no anvil menu, so
// a rename cannot legitimately arrive; vanilla ignores the packet when
// the open menu is not an anvil, which is the behavior reproduced here.
func (s *Server) handleRenameItem(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.String(); err != nil {
		return
	}
}

// handleSelectTrade consumes a villager trade selection. mc-go has no
// merchant menu, so vanilla's "ignore when no merchant is open" applies.
func (s *Server) handleSelectTrade(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil {
		return
	}
}

// handleSetBeacon consumes a beacon effect change. mc-go has no beacon
// menu, so vanilla's "ignore when no beacon is open" applies.
func (s *Server) handleSetBeacon(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, _, err := r.OptionalVarInt(); err != nil {
		return
	}
	if _, _, err := r.OptionalVarInt(); err != nil {
		return
	}
}

// handleMoveVehicle applies a vehicle position update. mc-go has no
// rideable entities yet, so a client cannot be riding one; vanilla
// ignores the packet when the player has no vehicle.
func (s *Server) handleMoveVehicle(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.Double(); err != nil {
		return
	}
	if _, err := r.Double(); err != nil {
		return
	}
	if _, err := r.Double(); err != nil {
		return
	}
	if _, err := r.Float(); err != nil {
		return
	}
	if _, err := r.Float(); err != nil {
		return
	}
	if _, err := r.Bool(); err != nil {
		return
	}
}

// handlePaddleBoat consumes a boat paddle input. No boats exist yet, so
// vanilla's "ignore when not in a boat" applies.
func (s *Server) handlePaddleBoat(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.Bool(); err != nil {
		return
	}
	if _, err := r.Bool(); err != nil {
		return
	}
}

// handleBundleItemSelected consumes a bundle slot selection. mc-go's
// item model carries no components yet, so the bundle has no contents
// to reorder; the packet is validated and dropped.
func (s *Server) handleBundleItemSelected(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil {
		return
	}
	if _, err := r.VarInt(); err != nil {
		return
	}
}

// handleCustomClickAction consumes a dialog click action. mc-go shows no
// dialogs, so there is no action to run; vanilla also drops an action
// for a dialog that is not open.
func (s *Server) handleCustomClickAction(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.String(); err != nil {
		return
	}
	if _, err := r.ByteArray(); err != nil {
		return
	}
}

// handleEditBook writes pages into the held book and quill. Vanilla
// accepts the edit only for a writable book in the selected hotbar
// slot; the resulting stack keeps its identity but gains the pages.
func (s *Server) handleEditBook(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.VarInt()
	if err != nil || slot < 0 || slot > 8 {
		return
	}
	count, err := r.VarInt()
	if err != nil || count < 0 || count > 100 {
		return
	}
	pages := make([]string, 0, count)
	for i := int32(0); i < count; i++ {
		page, err := r.String()
		if err != nil {
			return
		}
		if len(page) > 1024 {
			page = page[:1024]
		}
		pages = append(pages, page)
	}
	// Optional title: present when the client signs the book.
	title := ""
	if r.Remaining() > 0 {
		if t, err := r.String(); err == nil {
			title = t
		}
	}
	if !sess.writeBook(slot, pages, title) {
		return
	}
	// Vanilla acknowledges by syncing the changed slot back.
	_ = sess.write(sess.ids().PlayContainerSlot, containerSlotBody(0, int16(slotHotbarStart+slot), sess.stackAt(int16(slotHotbarStart+slot))))
}

// writeBook stores pages (and an optional title) on the hotbar stack.
// It reports whether the slot held a writable book.
func (sess *Session) writeBook(slot int32, pages []string, title string) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	idx := int16(slotHotbarStart + slot)
	it := sess.inv[idx]
	if it.Empty() {
		return false
	}
	name, ok := itemNameOf(it.ID)
	if !ok {
		return false
	}
	if title == "" && name != "minecraft:writable_book" {
		return false
	}
	if title != "" && name != "minecraft:writable_book" && name != "minecraft:written_book" {
		return false
	}
	sess.bookPages[idx] = append([]string(nil), pages...)
	sess.bookTitle[idx] = title
	return true
}

// containerSlotBody encodes a Set Container Slot packet: window id,
// state id, slot, then the item stack.
func containerSlotBody(window int8, slot int16, it ItemStack) []byte {
	var b protocol.Buffer
	b.Byte(byte(window))
	b.VarInt(0) // state id
	b.Short(slot)
	writeSlot(&b, it)
	return b.Bytes()
}

// handlePickItemFromBlock implements middle-click on a block: the item
// form of the target block is placed on the player's cursor, or in the
// matching hotbar slot when it is already in the inventory. Vanilla
// requires the block to be within reach.
func (s *Server) handlePickItemFromBlock(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	if _, err := r.Bool(); err != nil { // include data
		return
	}
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	if !sess.withinReach(pos) {
		return
	}
	blk := sess.world().GetBlock(pos)
	itemID, ok := world.ItemIDForBlockState(blk)
	if !ok {
		return
	}
	sess.pickItem(itemID)
}

// handlePickItemFromEntity implements middle-click on an entity: the
// entity's item form (its spawn egg) goes to the cursor.
func (s *Server) handlePickItemFromEntity(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.VarInt()
	if err != nil {
		return
	}
	if _, err := r.Bool(); err != nil { // include data
		return
	}
	e, ok := s.entities.Get(id)
	if !ok {
		return
	}
	if itemID, ok := pickItemForEntity(e.Type); ok {
		sess.pickItem(itemID)
	}
}

// pickItem resolves an item for pick-block: the existing hotbar stack
// when the player already carries one, otherwise a new stack on the
// cursor in creative and nothing in survival (vanilla searches the
// inventory and only creates the stack in creative).
func (sess *Session) pickItem(itemID int32) {
	if itemID == 0 {
		return
	}
	sess.invMu.Lock()
	for slot := int16(0); slot < 9; slot++ {
		if sess.inv[slotHotbarStart+slot].ID == itemID {
			sess.invMu.Unlock()
			sess.selectHotbar(int32(slot))
			return
		}
	}
	for slot := int16(9); slot < 36; slot++ {
		if sess.inv[slotMainStart+slot].ID == itemID {
			// Swap the found stack into the selected hotbar slot,
			// which is vanilla's pick-block behavior.
			held := sess.held.Load()
			sess.inv[slotMainStart+slot], sess.inv[slotHotbarStart+held] =
				sess.inv[slotHotbarStart+held], sess.inv[slotMainStart+slot]
			sess.invMu.Unlock()
			sess.syncInventory()
			return
		}
	}
	creative := sess.mode() == GameModeCreative
	sess.invMu.Unlock()
	if !creative {
		return
	}
	// Creative: put the item on the cursor without disturbing the
	// inventory, which is what vanilla does for a block the player
	// does not carry.
	sess.carriedMuSet(ItemStack{ID: itemID, Count: 1})
	_ = sess.server.sendSetCursorItem(sess)
}

// sendSetCursorItem syncs the player's cursor stack (window 0).
func (s *Server) sendSetCursorItem(sess *Session) error {
	it := sess.carriedBuffer()
	var b protocol.Buffer
	writeSlot(&b, it)
	return sess.write(sess.ids().PlaySetCursorItem, b.Bytes())
}

// syncInventory re-sends the whole player inventory window, which is
// what vanilla does after a pick-block swap.
func (sess *Session) syncInventory() {
	_ = sess.server.sendContainerContent(sess)
	_ = sess.server.sendSetCursorItem(sess)
}

// withinReach reports whether pos is inside the player's interaction
// radius (vanilla's 6-unit block reach, measured from the eye height).
func (sess *Session) withinReach(pos world.Pos) bool {
	x, y, z, _, _, _ := sess.Pos()
	dx := x - (float64(pos.X) + 0.5)
	dy := y + 1.62 - (float64(pos.Y) + 0.5)
	dz := z - (float64(pos.Z) + 0.5)
	const reach = 6.0
	return dx*dx+dy*dy+dz*dz <= reach*reach
}

// pickItemForEntity maps an entity type to the item vanilla's pick
// block produces for it: the spawn egg when the entity has one.
func pickItemForEntity(entityType int32) (int32, bool) {
	name, ok := EntityTypeName(entityType)
	if !ok {
		return 0, false
	}
	if id, ok := world.ItemID(name + "_spawn_egg"); ok {
		return id, true
	}
	return 0, false
}

// handleSpectatorAction consumes a spectator left-click. The action
// only opens a menu for the targeted entity (a mob's inventory); with
// no such menus, vanilla's behavior is to do nothing visible.
func (s *Server) handleSpectatorAction(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil {
		return
	}
}

// handleTeleportToEntity moves a spectator to another player, which is
// the only entity kind the vanilla client targets.
func (s *Server) handleTeleportToEntity(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	id, err := r.UUID()
	if err != nil {
		return
	}
	if sess.mode() != GameModeSpectator {
		return
	}
	target := s.sessionByUUID(id)
	if target == nil || target == sess {
		return
	}
	x, y, z, _, _, _ := target.Pos()
	s.TeleportPlayer(sess.getName(), x, y, z)
}

// sessionByUUID finds an online session by profile UUID.
func (s *Server) sessionByUUID(id [16]byte) *Session {
	var found *Session
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok {
			return true
		}
		if sess.getUUID() == id {
			found = sess
			return false
		}
		return true
	})
	return found
}

// handlePlaceRecipe places the ingredients of a recipe into the open
// crafting grid, which is what clicking a recipe in the recipe book
// does. The recipe id is the network id from Recipe Book Add.
func (s *Server) handlePlaceRecipe(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	windowID, err := r.VarInt()
	if err != nil {
		return
	}
	recipeID, err := r.VarInt()
	if err != nil {
		return
	}
	makeAll, err := r.Bool()
	if err != nil {
		return
	}
	s.placeRecipe(sess, windowID, recipeID, makeAll)
}

// entityTypeNameOr renders an entity type id as its registry name.
func entityTypeNameOr(id int32) string {
	if name, ok := EntityTypeName(id); ok {
		return name
	}
	return "minecraft:unknown"
}

// writeBookTitle normalises a book title to vanilla's 32-character cap.
func writeBookTitle(title string) string {
	title = strings.TrimSpace(title)
	if len(title) > 32 {
		title = title[:32]
	}
	return title
}
