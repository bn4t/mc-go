package server

import (
	"math"
)

// track.go — per-player entity pairing, mc-go's equivalent of vanilla's
// ChunkMap.TrackedEntity.
//
// A player is paired with an entity while all of (updatePlayer):
//   - squared distance <= (min(playerViewDistance, effectiveRange))²
//     in blocks — Euclidean, not axis-aligned; each range measured in
//     chunks is multiplied by 16
//   - entity.broadcastToPlayer(player) — true for everything mc-go
//     models except the pairing player itself
//   - isChunkTracked(player, entityChunk): the column is inside the
//     player's tracking view AND its chunk packet has actually gone
//     out (mc-go: sess.sent holds delivered columns; queued alone is
//     still streaming, which is why a just-tp'd entity can unpair)
//
// effectiveRange widens the entity's clientTrackingRange*16 to the
// largest indirect passenger's (getEffectiveRange), scaled by
// entity-broadcast-range-percentage — default 100, i.e. unchanged.
//
// Pairing sends the spawn bundle (add_entity + pairing data —
// ClientboundBundlePacket wrapping is its own gap); unpairing sends
// remove_entities. Vanilla evaluates on section moves: an entity's
// 16-block section changed, a player's section changed (every entity
// vs that player), a player moved anywhere (entity map vs player,
// from updatePlayerPos), an entity was added, or tracking stopped.
//
// trackSpec is {clientTrackingRange chunks, updateInterval ticks}
// per entity type, extracted from the 26.2 jar — see
// tools/trackprobe (trackranges.txt). The interval gates vanilla's
// ServerEntity.sendChanges cadence; mc-go still syncs every tick.
var trackSpec = map[string][2]int{
	"acacia_boat":            {10, 3},
	"acacia_chest_boat":      {10, 3},
	"allay":                  {8, 2},
	"area_effect_cloud":      {10, 2147483647},
	"armadillo":              {10, 3},
	"armor_stand":            {10, 3},
	"arrow":                  {4, 20},
	"axolotl":                {10, 3},
	"bamboo_chest_raft":      {10, 3},
	"bamboo_raft":            {10, 3},
	"bat":                    {5, 3},
	"bee":                    {8, 3},
	"birch_boat":             {10, 3},
	"birch_chest_boat":       {10, 3},
	"blaze":                  {8, 3},
	"block_display":          {10, 1},
	"bogged":                 {8, 3},
	"breeze":                 {10, 3},
	"breeze_wind_charge":     {4, 10},
	"camel":                  {10, 3},
	"camel_husk":             {10, 3},
	"cat":                    {8, 3},
	"cave_spider":            {8, 3},
	"cherry_boat":            {10, 3},
	"cherry_chest_boat":      {10, 3},
	"chest_minecart":         {8, 3},
	"chicken":                {10, 3},
	"cod":                    {4, 3},
	"copper_golem":           {10, 3},
	"command_block_minecart": {8, 3},
	"cow":                    {10, 3},
	"creaking":               {8, 3},
	"creeper":                {8, 3},
	"dark_oak_boat":          {10, 3},
	"dark_oak_chest_boat":    {10, 3},
	"dolphin":                {5, 3},
	"donkey":                 {10, 3},
	"dragon_fireball":        {4, 10},
	"drowned":                {8, 3},
	"egg":                    {4, 10},
	"elder_guardian":         {10, 3},
	"enderman":               {8, 3},
	"endermite":              {8, 3},
	"ender_dragon":           {10, 3},
	"ender_pearl":            {4, 10},
	"end_crystal":            {16, 2147483647},
	"evoker":                 {8, 3},
	"evoker_fangs":           {6, 2},
	"experience_bottle":      {4, 10},
	"experience_orb":         {6, 20},
	"eye_of_ender":           {4, 4},
	"falling_block":          {10, 20},
	"fireball":               {4, 10},
	"firework_rocket":        {4, 10},
	"fox":                    {8, 3},
	"frog":                   {10, 3},
	"furnace_minecart":       {8, 3},
	"ghast":                  {10, 3},
	"happy_ghast":            {10, 3},
	"giant":                  {10, 3},
	"glow_item_frame":        {10, 2147483647},
	"glow_squid":             {10, 3},
	"goat":                   {10, 3},
	"guardian":               {8, 3},
	"hoglin":                 {8, 3},
	"hopper_minecart":        {8, 3},
	"horse":                  {10, 3},
	"husk":                   {8, 3},
	"illusioner":             {8, 3},
	"interaction":            {10, 3},
	"iron_golem":             {10, 3},
	"item":                   {6, 20},
	"item_display":           {10, 1},
	"item_frame":             {10, 2147483647},
	"jungle_boat":            {10, 3},
	"jungle_chest_boat":      {10, 3},
	"leash_knot":             {10, 2147483647},
	"lightning_bolt":         {16, 2147483647},
	"llama":                  {10, 3},
	"llama_spit":             {4, 10},
	"magma_cube":             {8, 3},
	"mangrove_boat":          {10, 3},
	"mangrove_chest_boat":    {10, 3},
	"mannequin":              {32, 2},
	"marker":                 {0, 3},
	"minecart":               {8, 3},
	"mooshroom":              {10, 3},
	"mule":                   {8, 3},
	"nautilus":               {10, 3},
	"oak_boat":               {10, 3},
	"oak_chest_boat":         {10, 3},
	"ocelot":                 {10, 3},
	"ominous_item_spawner":   {8, 3},
	"painting":               {10, 2147483647},
	"pale_oak_boat":          {10, 3},
	"pale_oak_chest_boat":    {10, 3},
	"panda":                  {10, 3},
	"parched":                {8, 3},
	"parrot":                 {8, 3},
	"phantom":                {8, 3},
	"pig":                    {10, 3},
	"piglin":                 {8, 3},
	"piglin_brute":           {8, 3},
	"pillager":               {8, 3},
	"polar_bear":             {10, 3},
	"splash_potion":          {4, 10},
	"lingering_potion":       {4, 10},
	"pufferfish":             {4, 3},
	"rabbit":                 {8, 3},
	"ravager":                {10, 3},
	"salmon":                 {4, 3},
	"sheep":                  {10, 3},
	"shulker":                {10, 3},
	"shulker_bullet":         {8, 3},
	"silverfish":             {8, 3},
	"skeleton":               {8, 3},
	"skeleton_horse":         {10, 3},
	"slime":                  {10, 3},
	"small_fireball":         {4, 10},
	"sniffer":                {10, 3},
	"snowball":               {4, 10},
	"snow_golem":             {8, 3},
	"spawner_minecart":       {8, 3},
	"spectral_arrow":         {4, 20},
	"spider":                 {8, 3},
	"spruce_boat":            {10, 3},
	"spruce_chest_boat":      {10, 3},
	"squid":                  {8, 3},
	"stray":                  {8, 3},
	"strider":                {10, 3},
	"sulfur_cube":            {10, 3},
	"tadpole":                {10, 3},
	"text_display":           {10, 1},
	"tnt":                    {10, 10},
	"tnt_minecart":           {8, 3},
	"trader_llama":           {10, 3},
	"trident":                {4, 20},
	"tropical_fish":          {4, 3},
	"turtle":                 {10, 3},
	"vex":                    {8, 3},
	"villager":               {10, 3},
	"vindicator":             {8, 3},
	"wandering_trader":       {10, 3},
	"warden":                 {16, 3},
	"wind_charge":            {4, 10},
	"witch":                  {8, 3},
	"wither":                 {10, 3},
	"wither_skeleton":        {8, 3},
	"wither_skull":           {4, 10},
	"wolf":                   {10, 3},
	"zoglin":                 {8, 3},
	"zombie":                 {8, 3},
	"zombie_horse":           {10, 3},
	"zombie_nautilus":        {10, 3},
	"zombie_villager":        {8, 3},
	"zombified_piglin":       {8, 3},
	"player":                 {32, 2},
	"fishing_bobber":         {4, 5},
}

// sectOf maps a position to its 16³ section grid cell — the unit
// vanilla's TrackedEntity.lastSectionPos compares.
func sectOf(x, y, z float64) [3]int {
	return [3]int{
		int(math.Floor(x / 16)),
		int(math.Floor(y / 16)),
		int(math.Floor(z / 16)),
	}
}

// trackRangeBlocks returns the entity's effective tracking span in
// blocks: clientTrackingRange*16 widened across indirect passengers
// (ChunkMap.TrackedEntity.getEffectiveRange). Unknown types fall back
// to the builder default of 10 chunks.
func (s *Server) trackRangeBlocks(e *Entity) float64 {
	name, _ := EntityTypeName(e.Type)
	r := 160.0 // EntityType.Builder default: clientTrackingRange(10)
	if spec, ok := trackSpec[name]; ok {
		r = float64(spec[0] * 16)
	}
	seen := map[int32]bool{e.ID: true}
	stack := append([]int32(nil), e.Passengers...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		pe, ok := s.entities.Get(id)
		if !ok {
			continue
		}
		if pn, ok := EntityTypeName(pe.Type); ok {
			if spec, ok := trackSpec[pn]; ok && float64(spec[0]*16) > r {
				r = float64(spec[0] * 16)
			}
		}
		stack = append(stack, pe.Passengers...)
	}
	return r
}

// trackDeltaTypes is EntityType.trackDeltas' hardcoded set — the ten
// types whose ServerEntity always runs the motion-emit branch inside
// sendChanges (needsSync||trackDelta||fallFlying). Every other type
// emits set_entity_motion only when needsSync (PosDirty) is flagged.
var trackDeltaTypes = map[string]bool{
	"player": true, "llama_spit": true, "wither": true, "bat": true,
	"item_frame": true, "glow_item_frame": true, "leash_knot": true,
	"painting": true, "end_crystal": true, "evoker_fangs": true,
}

// entitySyncInterval is trackSpec's second column — vanilla's
// ServerEntity.updateInterval, the sendChanges cadence per entity
// type. Displays tick every tick, mobs every 3, projectiles every 10,
// items/falling blocks/orbs/arrows every 20, and item frames, glow
// item frames, end crystals, and area effect clouds use MAX_INT so
// their move block never runs off the cadence (position still goes
// out through needsSync/isDirty).
func (s *Server) entitySyncInterval(e *Entity) int64 {
	name, _ := EntityTypeName(e.Type)
	if spec, ok := trackSpec[name]; ok {
		return int64(spec[1])
	}
	return 3
}

// viewBlocks is the player's view span in blocks —
// min(server view distance, client-requested view distance) * 16,
// matching getPlayerViewDistance. A client that never sent Client
// Information rides on the server's view distance.
func (sess *Session) viewBlocks() float64 {
	sess.infoMu.Lock()
	v := int(sess.info.ViewDistance)
	sess.infoMu.Unlock()
	if v == 0 || v > sess.server.ViewDistance {
		v = sess.server.ViewDistance
	}
	return float64(v * 16)
}

// chunkSeen is mc-go's isChunkTracked: the column is inside the
// session's CURRENT tracking view around the player's chunk and its
// chunk packet has gone out (sent) — vanilla's contains(x,z) && !isPending.
// The view is isWithinDistance with the two-chunk shrink: for view
// distance 2 that is the 7x7 square centred on the player — the
// hysteresis ring queued/sent keep past the edge does not count.
func (sess *Session) chunkSeen(cx, cz int) bool {
	x, _, z, _, _, _ := sess.Pos()
	pcx, pcz := int(x)>>4, int(z)>>4
	dx, dz := cx-pcx, cz-pcz
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	dx -= 2
	if dx < 0 {
		dx = 0
	}
	dz -= 2
	if dz < 0 {
		dz = 0
	}
	dist := sess.viewRadius(sess.server.ViewDistance)
	if dx*dx+dz*dz >= dist*dist {
		return false
	}
	sess.sentMu.Lock()
	defer sess.sentMu.Unlock()
	return sess.sent[[2]int{cx, cz}]
}

// tracksEntity reports whether the session's seenBy set holds id.
func (sess *Session) tracksEntity(id int32) bool {
	sess.trackMu.Lock()
	defer sess.trackMu.Unlock()
	_, ok := sess.trackedEnts[id]
	return ok
}

// setTracked writes one entity id's pairing state.
func (sess *Session) setTracked(id int32, on bool) {
	sess.trackMu.Lock()
	defer sess.trackMu.Unlock()
	if sess.trackedEnts == nil {
		sess.trackedEnts = make(map[int32]struct{})
	}
	if on {
		sess.trackedEnts[id] = struct{}{}
	} else {
		delete(sess.trackedEnts, id)
	}
}

// trackedPairs reports the session's paired ids — for the remove pass
// that untracks everything (dimension change, disconnect).
func (sess *Session) trackedPairs() []int32 {
	sess.trackMu.Lock()
	defer sess.trackMu.Unlock()
	ids := make([]int32, 0, len(sess.trackedEnts))
	for id := range sess.trackedEnts {
		ids = append(ids, id)
	}
	return ids
}

// pairable is the vanilla pairing predicate for one session/entity.
func (s *Server) pairable(sess *Session, e *Entity) bool {
	if sess.state.Load() != StatePlay {
		return false
	}
	if sess.entityID.Load() == e.ID {
		return false // updatePlayer early-outs on player == entity
	}
	// A dormant entity is parked in an unticketed chunk — vanilla
	// keeps it out of the tracker until its chunk loads.
	if e.Dying || e.Dormant || e.Dim != sess.dimName() {
		return false
	}
	px, _, pz, _, _, _ := sess.Pos()
	dx, dz := e.X-px, e.Z-pz
	span := sess.viewBlocks()
	if tr := s.trackRangeBlocks(e); tr < span {
		span = tr
	}
	if dx*dx+dz*dz > span*span {
		return false
	}
	return sess.chunkSeen(int(math.Floor(e.X/16)), int(math.Floor(e.Z/16)))
}

// pairEnt pairs the session with the entity: the spawn packet plus
// the pairing data vanilla's sendPairingData bundles — entity data,
// passengers. (Vanilla also ships syncable attributes, velocity, leash
// and equipment in the same bundle.)
func (s *Server) pairEnt(sess *Session, e *Entity) {
	sess.setTracked(e.ID, true)
	_ = s.sendEntityTo(sess, e)
	// The pairing add_entity carries the current position, so the
	// shared send baseline re-latches here like vanilla's
	// VecDeltaCodec on re-add — otherwise the next flush emits a
	// residual move/sync to the fresh tracker.
	e.syncInit = true
	e.syncPX, e.syncPY, e.syncPZ = mobFixed(e.X), mobFixed(e.Y), mobFixed(e.Z)
	e.lastYRotB = angleByte(e.Yaw)
	e.lastXRotB = angleByte(e.Pitch)
	e.lastYHeadB = angleByte(e.HeadYaw)
	e.wasOnGround = e.Grounded
	e.PosDirty = false
	e.teleportDelay = 0
}

// unpairEnt drops the pairing — remove_entities, like removePairing.
func (s *Server) unpairEnt(sess *Session, id int32) {
	if !sess.tracksEntity(id) {
		return
	}
	sess.setTracked(id, false)
	_ = sess.write(sess.ids().PlayRemoveEntity, encodeRemove(id))
}

// evalPair applies updatePlayer to one session/entity pair: pair when
// now in range, unpair when now out of it.
func (s *Server) evalPair(sess *Session, e *Entity) {
	tracked := sess.tracksEntity(e.ID)
	ok := s.pairable(sess, e)
	switch {
	case !tracked && ok:
		s.pairEnt(sess, e)
	case tracked && !ok:
		s.unpairEnt(sess, e.ID)
	}
}

// untrackAll drops every pairing the session holds, emitting the
// remove_entities vanilla sends on removePlayer — used by dimension
// changes and disconnects.
func (s *Server) untrackAll(sess *Session) {
	for _, id := range sess.trackedPairs() {
		sess.setTracked(id, false)
		_ = sess.write(sess.ids().PlayRemoveEntity, encodeRemove(id))
	}
}

// entityTrackStep runs the pairing evaluation once per game tick,
// mirroring ChunkMap.updateEntitySections: entities whose 16-block
// section changed re-evaluate against every session, then sessions
// whose own section changed re-evaluate against every entity.
func (s *Server) entityTrackStep() {
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	var sessions []*Session
	s.sessions.Range(func(_, v any) bool {
		sessions = append(sessions, v.(*Session))
		return true
	})
	// Live player positions: a player entity's stored coordinates
	// trail the session's, so eval them with the session's own Pos.
	livePos := make(map[int32]*Session, len(sessions))
	for _, sess := range sessions {
		livePos[sess.entityID.Load()] = sess
	}
	for _, e := range ents {
		// Player entities' stored coordinates trail the live session —
		// eval them against a copy holding the session's own Pos.
		ec := e
		if psess, ok := livePos[e.ID]; ok {
			x, y, z, _, _, _ := psess.Pos()
			ec = &Entity{}
			*ec = *e
			ec.X, ec.Y, ec.Z = x, y, z
		}
		sec := sectOf(ec.X, ec.Y, ec.Z)
		if sec == e.lastSect {
			continue
		}
		e.lastSect = sec
		for _, sess := range sessions {
			s.evalPair(sess, ec)
		}
	}
	for _, sess := range sessions {
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			continue
		}
		x, y, z, _, _, _ := sess.Pos()
		sec := sectOf(x, y, z)
		if sec == sess.lastTrackSect {
			continue
		}
		sess.lastTrackSect = sec
		for _, e := range ents {
			ec := e
			if psess, ok := livePos[e.ID]; ok {
				ex, ey, ez, _, _, _ := psess.Pos()
				ec = &Entity{}
				*ec = *e
				ec.X, ec.Y, ec.Z = ex, ey, ez
			}
			s.evalPair(sess, ec)
		}
	}
}

// retrackSession re-evaluates every entity against one session — the
// updatePlayerPos path vanilla runs on each client position packet.
func (s *Server) retrackSession(sess *Session) {
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		ec := e
		s.sessions.Range(func(_, v any) bool {
			os := v.(*Session)
			if os.entityID.Load() == e.ID {
				x, y, z, _, _, _ := os.Pos()
				ec = &Entity{}
				*ec = *e
				ec.X, ec.Y, ec.Z = x, y, z
			}
			return true
		})
		s.evalPair(sess, ec)
	}
}
