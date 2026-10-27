package server

import (
	"bytes"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"mc-go/internal/data"
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

// SetOpLevel records a permission level for a name the way an
// ops.json entry does — used by the binary's ops.json load and by
// tests seeding the table.
func (s *Server) SetOpLevel(name string, level int) {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	s.ops[name] = level
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
	if sess.gameMode != m {
		sess.prevGameMode = int32(sess.gameMode)
	}
	sess.gameMode = m
	sess.modeMu.Unlock()
	sess.playerRecord.markStateDirty()
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

// setDifficultyQuiet stores the difficulty without telling anyone,
// which is what loading a world needs: it happens at boot, before a
// player is connected to be told.
func (s *Server) setDifficultyQuiet(d byte) {
	s.rulesMu.Lock()
	s.difficulty = d
	s.rulesMu.Unlock()
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
// subscription set — vanilla's ServerPlayer.updateDebugSubscribers.
// Registry ids index vanilla's debug_subscription registry in bootstrap
// order; 0 (dedicated_server_tick_time) is the one mc-go serves, the
// rest are stored for the remaining debug-value streams.
func (s *Server) handleDebugSubscriptionRequest(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 4096 {
		return
	}
	subs := make(map[int32]struct{}, n)
	for i := int32(0); i < n; i++ {
		id, err := r.VarInt()
		if err != nil {
			return
		}
		subs[id] = struct{}{}
	}
	sess.debugSubs.Store(&subs)
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

// debugSampleTick flushes one debug_sample per wall-clock tick to every
// subscribed session — ServerDebugSubscribers.tick for
// dedicated_server_tick_time ships the tick's measured nanos as the
// RemoteDebugSampleType.TICK_TIME (0) sample. It is a wall tick, not a
// game tick: the stream keeps flowing while the game clock is frozen.
func (s *Server) debugSampleTick(d time.Duration) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		subs := sess.debugSubs.Load()
		if subs == nil {
			return true
		}
		if _, ok := (*subs)[0]; !ok {
			return true
		}
		var b protocol.Buffer
		b.VarInt(1)
		b.Long(d.Nanoseconds())
		b.VarInt(0)
		_ = sess.write(sess.ids().PlayDebugSample, b.Bytes())
		return true
	})
}

// Container button ids, from the menus that have button handlers: the
// lectern turns pages and takes its book back, the stonecutter picks a
// recipe, the loom picks a pattern, and the enchantment table picks an
// offer.
const (
	lecternButtonPrev = 1
	lecternButtonNext = 2
	lecternButtonTake = 3
)

// handleContainerButtonClick routes a container button click to the
// open menu. Vanilla drops a button click when the window does not
// match or the menu has no button handler; the only handler with an
// effect here is the lectern's take-book button.
func (s *Server) handleContainerButtonClick(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	button, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.container == nil || w.id != win {
		return
	}
	if w.container.spec.menu == menuEnchantment {
		s.enchantButton(sess, w, button)
		return
	}
	if w.container.spec.menu == menuStonecutter {
		s.stonecutterSelect(sess, w, button)
		return
	}
	if w.container.spec.menu == menuLoom {
		s.loomSelect(sess, w, button)
		return
	}
	if w.container.spec.menu != menuLectern {
		return
	}
	// LecternMenu.clickMenuButton: 1/2 step the page, 3 takes the
	// book, >= 100 jumps to page-100.
	switch button {
	case lecternButtonTake:
		s.lecternTakeBook(sess, w)
	case lecternButtonPrev, lecternButtonNext:
		delta := int32(-1)
		if button == lecternButtonNext {
			delta = 1
		}
		w.dim.containers.mu.Lock()
		cur := w.container.page
		w.dim.containers.mu.Unlock()
		s.lecternSetPage(w, w.pos, cur+delta)
	default:
		if button >= 100 {
			s.lecternSetPage(w, w.pos, button-100)
		}
	}
}

// handleContainerSlotStateChanged records a crafter slot toggle. The
// crafter is the only menu with per-slot states.
func (s *Server) handleContainerSlotStateChanged(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.VarInt()
	if err != nil {
		return
	}
	win, err := r.VarInt()
	if err != nil {
		return
	}
	enabled, err := r.Bool()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.container == nil || w.id != win || !w.container.spec.crafter {
		return
	}
	if slot < 0 || slot > 8 {
		return
	}
	c := w.container
	w.dim.containers.mu.Lock()
	if enabled {
		c.disabled &^= 1 << uint(slot)
	} else {
		c.disabled |= 1 << uint(slot)
	}
	w.dim.containers.mu.Unlock()
	// CrafterMenu.clickMenuButton → setSlotState: the slot-enable lands
	// in the menu's containerData and broadcasts to every viewer —
	// SLOT_DISABLED=1, SLOT_ENABLED=0 on the wire.
	val := int32(0)
	if !enabled {
		val = 1
	}
	s.broadcastContainerProp(c, int16(slot), val)
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

// handlePlayerLoaded records that the client finished loading terrain,
// which is what lifts the interaction gate: until this packet arrives,
// or loadedGraceTicks go by without it, the real server drops the
// player's uses and digs, and mc-go does the same.
//
// The packet carries no body at all, which 26.2 enforces: a scripted
// client that sends a byte with it is disconnected with "Packet
// play/serverbound/minecraft:player_loaded (ServerboundPlayerLoadedPacket)
// was larger than I expected". mc-go reads nothing from it, so both the
// real client's empty packet and anything else are accepted.
func (s *Server) handlePlayerLoaded(sess *Session, p protocol.Packet) {
	sess.loaded.Store(true)
}

// handleSeenAdvancements records which advancement tab the client
// opened and echoes select_advancements_tab back — vanilla's
// handleSeenAdvancements resolves the tab id to an AdvancementHolder
// and calls PlayerAdvancements.setSelectedTab, which emits the packet
// whenever the stored selection changes. The tab id field is only
// present for the OPENED_TAB action.
func (s *Server) handleSeenAdvancements(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	action, err := r.VarInt()
	if err != nil {
		return
	}
	sess.bookMu.Lock()
	if sess.seenAdvTabs == nil {
		sess.seenAdvTabs = map[string]bool{}
	}
	switch action {
	case 0: // opened tab
		tab, err := r.String()
		if err != nil {
			sess.bookMu.Unlock()
			return
		}
		sess.seenAdvTabs[tab] = true
		sess.bookMu.Unlock()
		s.advSetSelectedTab(sess, tab)
	case 1: // closed screen — vanilla keeps lastSelectedTab
		sess.bookMu.Unlock()
	default:
		sess.bookMu.Unlock()
	}
}

// advSetSelectedTab mirrors PlayerAdvancements.setSelectedTab: an
// unknown id leaves the selection alone, a root with a display selects
// it, anything else clears it — and only a change emits
// select_advancements_tab (nullable Identifier).
func (s *Server) advSetSelectedTab(sess *Session, tab string) {
	if data.AdvDefWire[tab] == nil {
		return
	}
	next := ""
	if advSelectableTab(tab) {
		next = tab
	}
	sess.bookMu.Lock()
	if sess.lastAdvTab == next {
		sess.bookMu.Unlock()
		return
	}
	sess.lastAdvTab = next
	sess.bookMu.Unlock()
	var b protocol.Buffer
	if next == "" {
		b.Bool(false)
	} else {
		b.Bool(true)
		b.String(next)
	}
	_ = sess.write(sess.ids().PlaySelectAdvancementsTab, b.Bytes())
}

// advSelectableTab reports whether tab is a selectable advancement
// tab root — vanilla's gate is value().isRoot() && display().isPresent()
// which on the update_advancements wire is hasParent=false followed by
// hasDisplay=true.
func advSelectableTab(tab string) bool {
	def := data.AdvDefWire[tab]
	if def == nil {
		return false
	}
	r := protocol.NewPacketReader(def)
	if _, err := r.String(); err != nil { // own id
		return false
	}
	hasParent, err := r.Bool()
	if err != nil || hasParent {
		return false
	}
	hasDisplay, err := r.Bool()
	return err == nil && hasDisplay
}

// handleRecipeBookChangeSettings stores the recipe-book UI toggles and
// echoes them back, which is what vanilla does when a client changes a
// filter.
func (s *Server) handleRecipeBookChangeSettings(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	book, err := r.VarInt()
	if err != nil || book < 0 || book > 3 {
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

// sendRecipeBookSettingsFor encodes the recipe-book toggles in the
// vanilla order: open/filtering per book type — crafting, furnace,
// blast furnace, smoker — so the packet carries eight booleans.
func (s *Server) sendRecipeBookSettingsFor(sess *Session) error {
	sess.bookMu.Lock()
	body := [8]bool{}
	for i := 0; i < 4; i++ {
		body[i*2], body[i*2+1] = sess.bookOpen[i], sess.bookFilter[i]
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
	profile, err := sess.javaProfile()
	if err != nil {
		return
	}
	index, ok := profile.recipeCanonicalIndex(id)
	if !ok {
		return
	}
	id = int32(index)
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

// handleRenameItem applies an anvil rename: the name becomes the open
// menu's pending field and the output recomputes. Vanilla ignores the
// packet when the open menu is not an anvil and drops a name past the
// field's 50-character cap, which is what the checks reproduce.
func (s *Server) handleRenameItem(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	name, err := r.String()
	if err != nil || utf8.RuneCountInString(name) > anvilMaxNameLength {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	if openWindowKindAnvil(w) {
		w.pendingName = name
	}
	sess.windowMu.Unlock()
	if !openWindowKindAnvil(w) {
		return
	}
	s.anvilSyncResult(sess, w, w.container)
}

// handleSelectTrade is implemented in trade.go (MerchantMenu's
// setSelectionHint + tryMoveItems).

// handleSetBeacon is BeaconMenu.updateEffects: the packet applies only
// while the player has a beacon menu open whose payment slot holds an
// item, the chosen effects must pass validateEffects against the
// pyramid's current level, and a success commits them onto the block
// entity and consumes one payment item.
func (s *Server) handleSetBeacon(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	primary, hasPrimary, err := r.OptionalVarInt()
	if err != nil {
		return
	}
	secondary, hasSecondary, err := r.OptionalVarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	// Vanilla ignores the packet unless the open menu is a beacon the
	// player is still in range of (AbstractContainerMenu.stillValid).
	if w == nil || w.container == nil || w.container.spec.menu != menuBeacon {
		return
	}
	if !containerStillValid(sess, w.pos) {
		return
	}
	c := w.container
	w.dim.containers.mu.Lock()
	hasPayment := !c.slots[beaconPayment].Empty()
	w.dim.containers.mu.Unlock()
	var primaryName, secondaryName string
	if hasPrimary {
		if n, ok := MobEffectName(primary); ok {
			primaryName = n
		}
	}
	if hasSecondary {
		if n, ok := MobEffectName(secondary); ok {
			secondaryName = n
		}
	}
	be := w.dim.beacons[w.pos]
	lv := 0
	if be != nil {
		lv = be.levels
	}
	// updateEffects returning false — no payment item, or a pair that
	// fails validateEffects — disconnects the client outright.
	if !hasPayment || !validateBeaconEffects(primaryName, secondaryName, lv) {
		s.disconnectWithComponent(sess, translatable("multiplayer.disconnect.generic"))
		return
	}
	if be != nil {
		be.primary, be.secondary = primaryName, secondaryName
	}
	// paymentSlot.remove(1): one item of the payment stack is consumed.
	w.dim.containers.mu.Lock()
	st := c.slots[beaconPayment]
	if st.Count > 1 {
		st.Count--
		c.slots[beaconPayment] = st
	} else {
		c.slots[beaconPayment] = ItemStack{}
	}
	w.dim.containers.mu.Unlock()
	s.syncContainerMenuSlots(sess, w, []int{beaconPayment})
	s.sendBeaconProperties(sess, w)
}

// handleMoveVehicle is vanilla's ServerGamePacketListenerImpl.
// handleMoveVehicle: the driver's reported vehicle position applies
// when it stays within 100 dist² (minus the vehicle's own motion) of
// the last accepted anchor — a bigger jump rubber-bands the driver
// back with a clientbound move_vehicle of the server's position.
// Only the first passenger on a client-controllable vehicle steers
// (boats; saddled steerables).
func (s *Server) handleMoveVehicle(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	x, err := r.Double()
	if err != nil {
		return
	}
	y, err := r.Double()
	if err != nil {
		return
	}
	z, err := r.Double()
	if err != nil {
		return
	}
	yaw, err := r.Float()
	if err != nil {
		return
	}
	pitch, err := r.Float()
	if err != nil {
		return
	}
	onGround, err := r.Bool()
	if err != nil {
		return
	}
	vid := sess.riding.Load()
	if vid == 0 {
		return
	}
	ep, ok := s.entities.GetPtr(vid)
	if !ok || ep.Dying {
		return
	}
	// getControllingPassenger == player: the first rider only.
	if len(ep.Passengers) == 0 || ep.Passengers[0] != sess.entityID.Load() {
		return
	}
	name, ok := EntityTypeName(ep.Type)
	if !ok || !vehicleClientDriven(name, ep) {
		return
	}
	// getControllingPassenger's steerable half: Pig/Strider also
	// require the rider's held food-on-a-stick (Player.isHolding
	// covers both hands) — without it the packet drops silently,
	// no correction packet.
	if stick, yes := steerableStick[name]; yes {
		if n, _ := itemNameOf(sess.heldStack().ID); n != stick {
			if n2, _ := itemNameOf(sess.stackAt(slotOffhand).ID); n2 != stick {
				return
			}
		}
	}
	// clampHorizontal (±3.0E7 xz, ±2.0E7 y) and wrapDegrees before the
	// delta check — the packet carries raw doubles/floats.
	x = clampTo(x, -3.0e7, 3.0e7)
	z = clampTo(z, -3.0e7, 3.0e7)
	y = clampTo(y, -2.0e7, 2.0e7)
	yaw = wrapDegrees32(yaw)
	pitch = wrapDegrees32(pitch)
	dx := x - ep.VehAnchor[0]
	dy := y - ep.VehAnchor[1]
	dz := z - ep.VehAnchor[2]
	motion2 := ep.MotionX*ep.MotionX + ep.MotionY*ep.MotionY + ep.MotionZ*ep.MotionZ
	if dx*dx+dy*dy+dz*dz-motion2 > 100 {
		// "{} (vehicle of {}) moved too quickly!" — reject the move
		// and rubber-band the driver with the server's position.
		s.sendMoveVehicle(sess, ep)
		return
	}
	ep.X, ep.Y, ep.Z = x, y, z
	ep.Yaw, ep.Pitch = yaw, pitch
	ep.Grounded = onGround
	ep.VehAnchor = [3]float64{x, y, z}
	ep.PosDirty = true
	ep.NeedsSync = true
	s.mobSyncFlush(ep)
}

// vehicleClientDriven is vanilla's isClientControllableVehicle: boats
// always answer client moves; steerables (pig/strider/happy_ghast) and
// saddleable equines only once saddled.
func vehicleClientDriven(name string, e *Entity) bool {
	if isBoatEntityName(name) {
		return true
	}
	switch name {
	case "pig", "strider", "happy_ghast",
		"horse", "zombie_horse", "skeleton_horse",
		"donkey", "mule", "llama", "trader_llama",
		"camel", "camel_husk":
		return e.Saddle
	}
	return false
}

// sendMoveVehicle emits the clientbound move_vehicle packet — the
// driver's rubber-band after a rejected move: x/y/z doubles then
// yaw/pitch floats, mirroring the serverbound shape.
func (s *Server) sendMoveVehicle(sess *Session, e *Entity) {
	var b protocol.Buffer
	b.Double(e.X)
	b.Double(e.Y)
	b.Double(e.Z)
	b.Float(e.Yaw)
	b.Float(e.Pitch)
	_ = sess.write(0x39, b.Bytes())
}

// clampTo is vanilla's clampHorizontal/clampVertical bound.
func clampTo(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// wrapDegrees32 is Mth.wrapDegrees for a float32 angle.
func wrapDegrees32(d float32) float32 {
	v := float64(d)
	v -= float64(math.Floor(v/360+0.5)) * 360
	return float32(v)
}

// handlePaddleBoat is vanilla's ServerGamePacketListenerImpl.
// handlePaddleBoat: the controlled vehicle's AbstractBoat gets
// setPaddleState(left, right) — metadata entries 11/12 (bool). Like
// move_vehicle, only the first passenger's input applies, and only
// onto boats. SynchedEntityData.set marks each entry dirty only when
// its value actually changed, so the flush emits just the entries
// whose paddle moved.
func (s *Server) handlePaddleBoat(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	left, err := r.Bool()
	if err != nil {
		return
	}
	right, err := r.Bool()
	if err != nil {
		return
	}
	vid := sess.riding.Load()
	if vid == 0 {
		return
	}
	ep, ok := s.entities.GetPtr(vid)
	if !ok || ep.Dying {
		return
	}
	if len(ep.Passengers) == 0 || ep.Passengers[0] != sess.entityID.Load() {
		return
	}
	name, ok := EntityTypeName(ep.Type)
	if !ok || !isBoatEntityName(name) {
		return
	}
	var b protocol.Buffer
	if left != ep.PaddleL {
		ep.PaddleL = left
		b.Byte(11)
		b.Byte(8) // boolean serializer
		b.Bool(left)
	}
	if right != ep.PaddleR {
		ep.PaddleR = right
		b.Byte(12)
		b.Byte(8)
		b.Bool(right)
	}
	if b.Len() > 0 {
		b.Byte(0xFF)
		s.broadcastEntityMetadata(ep.ID, b.Bytes())
	}
}

// handleBundleItemSelected is Select Bundle Item: the player's current
// menu (an open window, else the inventory) toggles the selection of
// the bundle in the named slot — AbstractContainerMenu's
// setSelectedBundleItemIndex. The selection decides which entry the
// next right-click or use takes out; it never reaches the wire, so
// nothing is sent back. Vanilla's decoder rejects an index below -1;
// such a packet, like a slot outside the menu, changes nothing here.
func (s *Server) handleBundleItemSelected(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.VarInt()
	if err != nil {
		return
	}
	idx, err := r.VarInt()
	if err != nil || idx < -1 || r.Remaining() != 0 {
		return
	}
	toggle := func(it ItemStack) (ItemStack, bool) {
		b, ok := bundleOf(it)
		if !ok {
			return it, false
		}
		m := newBundleMut(b)
		m.toggle(idx)
		m.store(&it)
		return it, true
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	switch {
	case w == nil:
		if slot < 0 || slot >= slotCount {
			return
		}
		sess.invMu.Lock()
		if it, ok := toggle(sess.inv[slot]); ok {
			sess.inv[slot] = it
		}
		sess.invMu.Unlock()
	case w.craft != nil:
		if slot < 0 || slot >= craftTableSlots {
			return
		}
		if it, ok := toggle(sess.craftMenuStack(w.craft, int(slot))); ok {
			sess.setCraftMenuStack(w.craft, int(slot), it)
		}
	case w.hopper != nil:
		if slot < 0 || slot >= hopperMenuSlots {
			return
		}
		if it, ok := toggle(sess.hopperMenuStack(w.hopper, int(slot))); ok {
			sess.setHopperMenuStack(w.hopper, int(slot), it)
		}
	default:
		if slot < 0 || int(slot) >= w.menuSize() {
			return
		}
		if it, ok := toggle(sess.menuStack(int(slot))); ok {
			sess.setMenuStack(int(slot), it)
		}
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
	_ = sess.write(sess.ids().PlayContainerSlot, sess.containerSlotBody(0, int16(slotHotbarStart+slot), sess.stackAt(int16(slotHotbarStart+slot))))
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
func (sess *Session) containerSlotBody(window int8, slot int16, it ItemStack) []byte {
	var b protocol.Buffer
	b.Byte(byte(window))
	b.VarInt(0) // state id
	b.Short(slot)
	sess.writeSlot(&b, it)
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
	sess.writeSlot(&b, it)
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

// handleSpectatorAction decodes this profile's selection at packet0x3e. The
// 775 raw entity ID and776 optional entity ID must not share a numeric decoder.
func (s *Server) handleSpectatorAction(sess *Session, p protocol.Packet) {
	profile, err := sess.javaProfile()
	if err != nil {
		return
	}
	selection, err := profile.decodeSpectatorSelection(p.Data)
	if err != nil {
		return
	}
	s.applySpectatorSelectionOwned(sess, selection)
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
	s.teleportPlayerOwned(sess.getName(), x, y, z)
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
	if r.Remaining() != 0 {
		return
	}
	profile, err := sess.javaProfile()
	if err != nil {
		return
	}
	canonical, ok := profile.recipeCanonicalIndex(recipeID)
	if !ok {
		return
	}
	s.placeRecipe(sess, windowID, int32(canonical), makeAll)
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
