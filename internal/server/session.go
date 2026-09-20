package server

import (
	"crypto/md5"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Session is one connection's state. Each session runs on its own
// read goroutine; one writer goroutine drains the outbound queue.
// World shards and the tick coordinator run on theirs. The queue
// decouples slow readers from tick/broadcast paths: sess.write
// enqueues and never blocks on the socket, so a stalled client cannot
// stall the world or other sessions. Per-session FIFO preserves packet
// order; drops only happen after disconnect.
type Session struct {
	id     int64
	server *Server
	conn   net.Conn
	pr     *protocol.Reader
	pw     *protocol.Writer
	wmu    sync.Mutex
	state  atomic.Int32
	nameMu sync.RWMutex
	name   string
	uuid   [16]byte
	// Negotiated client version: set to LatestVersion at accept, then
	// to the handshake's protocol at login. Handlers dispatch packet
	// IDs through ids() so one core serves multiple versions.
	ver Version
	// clientProto is the raw handshake protocol number.
	clientProto atomic.Int32
	// Keep-alive bookkeeping (play stage only).
	lastAck atomic.Int64 // unix nanos of last client response
	kaNext  atomic.Int64 // next keep-alive id to send
	kaTime  atomic.Int64 // unix nanos the last keep-alive was sent
	rtt     atomic.Int64 // last measured round-trip, nanos
	// busy counts packets currently being handled by the read loop. A
	// slow handler (a big /tick step runs hundreds of ticks inline, as
	// vanilla's does) leaves the client's queued replies unread, so the
	// keep-alive watchdog must not read that silence as a dead client.
	busy atomic.Int32
	// teleportNext issues Synchronize Player Position ids; the spawn
	// teleport is 1, server-initiated teleports count up from there.
	teleportNext atomic.Int32
	// Player position (play stage). Guarded by posMu; the read loop
	// writes, tick/broadcast paths read.
	posMu       sync.RWMutex
	px, py, pz  float64
	yaw, pitch  float32
	onGround    bool
	teleportAck atomic.Int32 // last confirmed teleport id, -1 none
	// unlockedRecipes holds the recipe-book keys the session has been
	// given, keyed by data.Recipes index; guarded by recipesMu.
	recipesMu       sync.Mutex
	unlockedRecipes map[int]bool
	// advSent/advCrits/advDirty track advancement sync state: defs sent,
	// criteria earned (adv id -> criterion -> millis), and advancements
	// awaiting the next UpdateAdvancements flush. Guarded by advMu.
	advMu         sync.Mutex
	advSent       map[string]bool
	advCrits      map[string]map[string]int64
	advDirty      map[string]bool
	advDirtyOrder []string
	advDone       map[string]bool
	// pendingAdv carries a returning player's saved advancement state
	// from applySavedPlayer to initAdvancements at join.
	pendingAdv map[string]savedAdv
	// invSnap is the last-tick inventory snapshot that decides whether
	// the inventory_changed advancement trigger needs evaluating.
	invSnap   [slotCount]ItemStack
	invSnapOK bool
	// Player inventory window (46 slots), guarded by invMu. Slots are Slots are
	// populated from Set Creative Mode Slot and container clicks; the
	// selected hotbar slot (0-8) picks what Use Item On places.
	invMu   sync.RWMutex
	inv     [slotCount]ItemStack
	carried ItemStack
	// ender is the player's ender chest (27 slots), which /item addresses
	// as enderchest.N and which persists with the player.
	ender [27]ItemStack
	// bodyArmor is the body equipment slot /item addresses as
	// armor.body. A player never equips it through a menu, but the
	// command writes it like the real server's does.
	bodyArmor ItemStack
	// effects is the active mob-effect map (totem pops apply the
	// death_protection component's death effects).
	effMu    sync.Mutex
	effects  map[int32]mobEffectInst
	invState atomic.Int32
	held     atomic.Int32
	// chatIndex counts this player's relayed chat messages; chatGlobal
	// counts messages delivered to this client (Player Chat Message
	// ordering counters).
	chatIndex  atomic.Int32
	chatGlobal atomic.Int32
	// Client settings from Client Information.
	infoMu sync.Mutex
	info   clientInfo
	// Survival digging state (see digTickStep): the block being dug,
	// how many ticks of progress it has, and whether the client has
	// confirmed the dig with a finish (stop) action.
	digMu        sync.Mutex
	digPos       world.Pos
	digBlock     world.Block
	digTicks     int
	digNeeded    int
	digConfirmed bool
	digActive    bool
	// Survival player state (touched only from the session read loop).
	health       float32
	food         int32
	saturation   float32
	dead         bool
	fallDistance float64
	// fallStartY is where the current drop began — vanilla's
	// fall_from_height start_position reads this point.
	fallStartY float64
	fallStart  bool
	sneaking   bool
	flying       bool
	// Experience tracked from orbs (vanilla /xp levels ride later).
	xpTotal    int32
	xpLevel    int32
	xpProgress float32
	// Hunger simulation: exhaustion accumulates toward food loss.
	exhaustion    float32
	foodTickTimer int
	starveTimer   int
	sprinting     bool
	jumping       bool
	// combatTicks counts down the combat timer (300 ticks in vanilla).
	combatTicks int32
	// eatingTicks counts down an in-progress food use.
	eatingTicks int32
	eatingItem  int32
	// bowStartTick is the server tick a bow draw began on; zero means
	// not drawing.
	bowStartTick int64
	// crossbowStartTick is the server tick a crossbow charge began;
	// crossbowCharged is set once 25 ticks pass — the second use after
	// that fires instead of drawing again. Early release cancels.
	crossbowStartTick int64
	crossbowCharged   bool
	// usingItemID is the item id currently in continuous use
	// (spyglass scope); 0 when nothing is in use. Vanilla fires
	// using_item every tick while set.
	usingItemID int32
	// levitationStart / levitationStartTick anchor the levitation
	// trigger — vanilla stamps them when the effect lands and clears
	// on removal.
	levitationStart     []float64
	levitationStartTick int64
	// portalEntryX/Z is where the player left the overworld for the
	// nether; nether_travel measures the return against it.
	portalEntryX   float64
	portalEntryZ   float64
	portalEntrySet bool
	// invulnTicks is the damage grace vanilla grants after a hit: twenty
	// ticks counted down on the player tick, during which only damage
	// exceeding lastHurt lands, and only for the excess.
	invulnTicks int32
	// attackTicker is vanilla's attackStrengthTicker: the ticks since
	// the last attack() call; it paces the damage cooldown scale and
	// resets on every attack that reaches the damage code.
	attackTicker int32
	// locTicks paces the location advancement trigger, which vanilla
	// fires for a player every twenty ticks (tickCount % 20).
	locTicks int32
	// lastHorizSq is the squared horizontal delta of the last move
	// packet — vanilla's knownMovement, which gates the sweep attack
	// (it must stay under (movement speed * 2.5)²).
	lastHorizSq float64
	lastHurt    float32
	// lastHurtBy is the entity id of the most recent damage source's
	// attacker, for killed_by/kill-score stat attribution.
	lastHurtBy int32
	// healthDirty marks health/food/saturation changes to sync. Vanilla
	// sends at most one Set Health per player per tick, carrying the
	// end-of-tick values, so tick-side writers set this instead of
	// sending immediately; the player tick drains it.
	healthDirty atomic.Bool
	// absorb is the absorption heart pool (half-hearts). Vanilla
	// sets it to 4*(amplifier+1) when the Absorption effect lands and
	// clears it on removal; damage soaks it before touching health.
	absorb float32
	// Open container window (chest) and its state id.
	windowMu    sync.Mutex
	window      *openWindow
	windowState int32
	// dim is the player's current dimension; set on play entry and by
	// dimension changes. nil means the overworld.
	dim atomic.Pointer[dimension]
	// Portal state: consecutive ticks standing in a nether portal and
	// the post-travel re-entry cooldown.
	portalTicks         int
	portalCooldownTicks int
	// entityID is this player's entity id in the shared Manager,
	// allocated on entering play so remote clients can track them.
	// spawned is set once the join burst (including spawn fan-out)
	// completes; leave fan-out only fires for spawned sessions.
	entityID atomic.Int32
	spawned  atomic.Bool
	// Chunk streaming, guarded by sentMu. queued holds every column the
	// session has taken on - already streamed or still waiting - sent
	// only the ones the client has actually been given, which is what
	// hasChunk and the forget pass work on, and pending is the queue of
	// columns in the order they will go out. rate is how many columns a
	// batch holds under the client's last acknowledgement, zero until the
	// first acknowledgement; opened records whether the opening batch has
	// gone out; burst is how many of the ten batches that acknowledgement
	// bought are still owed; interval is the ticks between those batches
	// and cool the ticks still to wait before the next one.
	//
	// The queue is what the streaming throttle works on: the real server
	// sends one batch of columns per interval and nothing at all until the
	// client acknowledges a batch (see pumpChunks).
	sentMu   sync.Mutex
	queued   map[[2]int]bool
	sent     map[[2]int]bool
	pending  [][2]int
	rate     int
	opened   bool
	burst    int
	interval int
	cool     int
	// chunkPumpMu serializes whole batches: a window change on the
	// connection goroutine and the tick loop can pump at the same time.
	chunkPumpMu sync.Mutex
	// Chunk-cache center last sent to the client, guarded by sentMu.
	centerSet        bool
	centerX, centerZ int
	// Per-player game mode. Vanilla keeps the mode on the player, not
	// the server, so /gamemode and the Change Game Mode packet only
	// affect one session. Seeded from Server.GameMode at play entry;
	// guarded by modeMu because the tick and fan-out paths read it.
	modeMu   sync.RWMutex
	gameMode byte
	// Recipe book UI toggles (crafting/furnace book open, filtering)
	// and the set of recipes the player has seen, from the recipe book
	// packets.
	bookMu      sync.Mutex
	bookOpen    [2]bool // 0: crafting book, 1: furnace book
	bookFilter  [2]bool
	seenRecipes map[int32]bool
	// Pages and title written into book-and-quill stacks, keyed by
	// inventory slot. The item model carries no components yet, so the
	// book's contents live here until components land.
	bookPages map[int16][]string
	bookTitle map[int16]string
	// loaded is set by the Player Loaded packet: the client reports its
	// terrain loaded. The real server acts on a player's world
	// interactions only after that packet arrives, or after
	// loadedGraceTicks have gone by - measured on 26.2, a use or a dig
	// sent in the first moments of a session does nothing at all and
	// works the instant the packet lands. joinTick is the game tick the
	// session entered play, where that grace is counted from.
	loaded   atomic.Bool
	joinTick atomic.Int64
	// sleeping/sleepTicks are the bed state: sleeping is set by a bed
	// click inside the sleep window; GameTick increments sleepTicks and
	// skips the night once everyone has been under 100 ticks.
	sleeping   atomic.Bool
	sleepTicks atomic.Int32
	// blocking is the raised-shield state: set by a use-item start on a
	// shield (either hand), cleared by the release_use_item action.
	blocking atomic.Bool
	// Player statistics counters and the last-sent baseline.
	statMu sync.Mutex
	stats  *statStore
	// seenAdvTabs holds advancement tabs the client reported as seen.
	seenAdvTabs map[string]bool
	// chatSession is the client's signed-chat session key, from the
	// Player Session packet; nil until the client publishes one.
	chatSession *chatSessionKey
	// lastChatAck is the highest acknowledged chat message count.
	lastChatAck atomic.Int32
	// customBrand is the client's reported brand, from the
	// minecraft:brand plugin channel.
	brandMu     sync.Mutex
	customBrand string
	// Outbound queue: write() enqueues, writeLoop drains to the socket.
	// Buffered so broadcasts never block the tick/IO path on a slow
	// reader; FIFO preserves per-session packet order.
	sendCh    chan sendPkt
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	// inflight counts socket writes in progress; flush() waits for
	// both an empty queue and no in-flight write, so a queued packet
	// is actually on the wire (not just dequeued) before short-lived
	// flows like status close the connection.
	inflight atomic.Int32
}

// sendPkt is one queued outbound action: either a packet or a
// compression-threshold switch. Threshold switches ride the same FIFO
// so the Set Compression packet itself goes out raw and everything
// after it is framed, exactly like the synchronous vanilla handshake.
type sendPkt struct {
	id   int32
	data []byte
	// setThreshold, when non-nil, switches the writer's compression
	// threshold instead of sending a packet.
	setThreshold *int32
}

// loadedGraceTicks is how long the real server waits for a client's
// Player Loaded packet before it stops gating that player's world
// interactions. Measured on 26.2 by sending a use every 100ms from the
// moment of joining and noting when the first one was acted on: 3.0
// seconds, which is 60 ticks. A real client reports its terrain loaded
// well inside that, which is why the gate is invisible in normal play.
const loadedGraceTicks = 60

// interactionsOpen reports whether this session's world interactions -
// a use, a dig - are acted on yet. Until the client reports its terrain
// loaded, or the grace runs out, the real server drops them: no editor,
// no damage, no acknowledgement.
func (sess *Session) interactionsOpen(now int64) bool {
	if sess.loaded.Load() {
		return true
	}
	return now-sess.joinTick.Load() >= loadedGraceTicks
}

// Pos returns the last known player position.
func (sess *Session) Pos() (x, y, z float64, yaw, pitch float32, onGround bool) {
	sess.posMu.RLock()
	defer sess.posMu.RUnlock()
	return sess.px, sess.py, sess.pz, sess.yaw, sess.pitch, sess.onGround
}

// getName returns the login name ("" before login), safe for concurrent use.
func (sess *Session) getName() string {
	sess.nameMu.RLock()
	defer sess.nameMu.RUnlock()
	return sess.name
}

func (sess *Session) setName(n string) {
	sess.nameMu.Lock()
	defer sess.nameMu.Unlock()
	sess.name = n
}

// setIdentity publishes name+uuid together so concurrent tab-list
// readers never observe a name with a zero UUID.
func (sess *Session) setIdentity(n string, u [16]byte) {
	sess.nameMu.Lock()
	defer sess.nameMu.Unlock()
	sess.name, sess.uuid = n, u
}

// getUUID returns the profile UUID, safe for concurrent use (the
// tab-list fan-out reads other sessions' rows).
func (sess *Session) getUUID() [16]byte {
	sess.nameMu.RLock()
	defer sess.nameMu.RUnlock()
	return sess.uuid
}

// ids returns this session's negotiated packet-ID table.
func (sess *Session) ids() ProtoIDs { return sess.ver.IDs }

// latency reports the last keep-alive round-trip, 0 if unmeasured.
// Anticheat plugins use it to excuse laggy clients.
func (sess *Session) latency() time.Duration {
	return time.Duration(sess.rtt.Load())
}

// hasChunk reports whether the session is tracking column (cx,cz), the
// question the block, light, dig and sign fan-outs ask before sending an
// update. A column counts as tracked from the moment the session takes
// it on, not from the moment its chunk packet goes out - the same as the
// real server, which tracks a player whose chunk is still queued behind
// the streaming throttle.
func (sess *Session) hasChunk(cx, cz int) bool {
	sess.sentMu.Lock()
	defer sess.sentMu.Unlock()
	return sess.queued[[2]int{cx, cz}]
}

func (sess *Session) setPos(x, y, z float64, yaw, pitch float32, onGround bool) {
	sess.posMu.Lock()
	defer sess.posMu.Unlock()
	sess.px, sess.py, sess.pz = x, y, z
	sess.yaw, sess.pitch = yaw, pitch
	sess.onGround = onGround
}

// markAlive records a client response for the timeout watchdog.
func (sess *Session) markAlive() {
	sess.lastAck.Store(time.Now().UnixNano())
}

func (sess *Session) lastAlive() time.Time {
	return time.Unix(0, sess.lastAck.Load())
}

// handleConn runs handshake dispatch, then the phase state machine.
func (s *Server) handleConn(c net.Conn) {
	sess := &Session{
		id:     s.nextID.Add(1),
		server: s,
		conn:   c,
		sendCh: make(chan sendPkt, 256),
		done:   make(chan struct{}),
	}
	sess.pr = protocol.NewReader(c)
	sess.pw = protocol.NewWriter(c)
	sess.ver = LatestVersion()
	sess.teleportNext.Store(1)
	sess.state.Store(StateHandshaking)
	sess.wg.Add(1)
	go sess.writeLoop()
	defer func() {
		sess.closeQueue()
		_ = c.Close()
		sess.wg.Wait()
	}()
	s.track(sess)
	defer s.untrack(sess)

	sess.readLoop()
}

// closeQueue signals the writer to exit (idempotent).
func (sess *Session) closeQueue() {
	sess.closeOnce.Do(func() { close(sess.done) })
}

// write enqueues one packet, safe from any goroutine. It never blocks
// on the socket; after disconnect it drops and reports closed.
func (sess *Session) write(id int32, data []byte) error {
	select {
	case <-sess.done:
		return net.ErrClosed
	default:
	}
	select {
	case sess.sendCh <- sendPkt{id: id, data: data}:
		return nil
	case <-sess.done:
		return net.ErrClosed
	}
}

// writeLoop drains the queue in FIFO order. A socket error terminates
// the loop and signals done so future writes drop fast. On shutdown it
// best-effort flushes what is already queued (e.g. a disconnect kick)
// before exiting.
func (sess *Session) writeLoop() {
	defer sess.wg.Done()
	for {
		select {
		case <-sess.done:
			for {
				select {
				case pkt := <-sess.sendCh:
					if !sess.applyOne(pkt) {
						return
					}
				default:
					return
				}
			}
		case pkt := <-sess.sendCh:
			if !sess.applyOne(pkt) {
				sess.closeQueue()
				return
			}
		}
	}
}

// applyOne executes one queued action; false means the socket broke.
func (sess *Session) applyOne(pkt sendPkt) bool {
	if pkt.setThreshold != nil {
		sess.wmu.Lock()
		sess.pw.SetThreshold(*pkt.setThreshold)
		sess.wmu.Unlock()
		return true
	}
	sess.inflight.Add(1)
	defer sess.inflight.Add(-1)
	sess.wmu.Lock()
	err := sess.pw.WritePacket(pkt.id, pkt.data)
	sess.wmu.Unlock()
	return err == nil
}

// setWriteThreshold queues a compression-threshold switch behind
// already-queued packets, preserving handshake framing order.
func (sess *Session) setWriteThreshold(n int32) {
	v := n
	select {
	case sess.sendCh <- sendPkt{setThreshold: &v}:
	case <-sess.done:
	}
}

// flush waits until the queue drains and the in-flight socket write
// completes, or timeout elapses. Short-lived flows (status pong, login
// deny) call this before returning so the client actually receives the
// last packet before we close.
func (sess *Session) flush(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(sess.sendCh) == 0 && sess.inflight.Load() == 0 {
			// One more beat: a packet may have been enqueued between
			// the two checks; re-verify before declaring drained.
			time.Sleep(5 * time.Millisecond)
			if len(sess.sendCh) == 0 && sess.inflight.Load() == 0 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (sess *Session) readLoop() {
	// pr/pw are initialized in handleConn so the writer can start
	// before any broadcast can reach this session.
	hs, err := sess.pr.ReadPacket()
	if err != nil || hs.ID != IDHandshake {
		return
	}
	r := protocol.NewPacketReader(hs.Data)
	proto, err := r.VarInt() // client protocol version
	if err != nil {
		return
	}
	sess.clientProto.Store(proto)
	if _, err := r.String(); err != nil { // server address
		return
	}
	if _, err := r.UShort(); err != nil { // port
		return
	}
	next, err := r.VarInt()
	if err != nil {
		return
	}

	switch next {
	case NextStatus:
		sess.state.Store(StateStatus)
		sess.server.handleStatus(sess)
	case NextLogin:
		sess.state.Store(StateLogin)
		sess.server.handleLogin(sess)
	default:
		return
	}
}

// OfflineUUID is the profile UUID vanilla assigns in offline mode.
// Exported so blackbox tests can build ops.json entries that name the
// same player the server will see.
func OfflineUUID(name string) [16]byte { return offlineUUID(name) }

// offlineUUID derives the vanilla offline-mode UUID:
// MD5("OfflinePlayer:"+name) with version/variant bits set.
func offlineUUID(name string) [16]byte {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	sum[6] = (sum[6] & 0x0F) | 0x30
	sum[8] = (sum[8] & 0x3F) | 0x80
	return sum
}
