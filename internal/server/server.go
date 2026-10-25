package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mc-go/api/frontend"
	"mc-go/internal/event"
	"mc-go/internal/plugin"
	"mc-go/internal/world"
)

// Server owns one gameplay runtime shared by attached protocol listeners.
// Connection goroutines decode input; simulationAction serializes gameplay
// changes, snapshots and persistence. World shards own block storage. Bounded
// session queues isolate socket writers from gameplay actions. Plugins use
// synchronous decision hooks and asynchronous observation events.
//
// Configure fields before Start. The bundled game codec speaks Java protocol 776.
type Server struct {
	Addr       string
	MOTD       string
	MaxPlayers int

	// KeepAliveEvery/Timeout govern play-stage pings; non-positive
	// values select vanilla-like 10s/30s defaults.
	KeepAliveEvery   time.Duration
	KeepAliveTimeout time.Duration

	// OutboundPacketLimit and OutboundByteLimit bound each connection's
	// retained outbound work, including its active socket write. Exhaustion
	// disconnects the client rather than dropping ordered state or blocking
	// simulation. Non-positive values select 256 packets and 8 MiB.
	OutboundPacketLimit int
	OutboundByteLimit   int64
	// WriteTimeout bounds each packet's socket write; non-positive uses 5s.
	WriteTimeout time.Duration

	// WorldPath is the save file for block overrides (see world.Save).
	// Empty disables persistence: SaveWorld reports an error and boot
	// skips loading, so tests and ephemeral servers stay file-free.
	WorldPath string

	// WorldDir is a vanilla-format world directory: level.dat, the
	// SavedData files beside it, and dimensions/<namespace>/<path>/
	// region chunks. When set it takes precedence over WorldPath and
	// the world is saved in the format the real server reads, which is
	// what makes a world survive a handoff between the two.
	WorldDir string

	// OnlineMode is server.properties' online-mode: when true the
	// login stage runs the vanilla encryption handshake — a 1024-bit
	// RSA keypair, the encryption_request hello (empty server id,
	// X.509 public key, 4-byte challenge, shouldAuthenticate), the
	// AES/CFB8 stream, and the session-server hasJoined lookup whose
	// verified profile replaces the offline UUID.
	OnlineMode bool

	// HasJoinedURL is the session-server endpoint online-mode logins
	// verify against; empty selects Mojang's. Tests point it at a
	// stub instead of the real service.
	HasJoinedURL string

	// loadMu guards worldLoaded, which makes LoadWorld idempotent so
	// the world a server was configured with is loaded exactly once
	// however many times loading is asked for.
	loadMu      sync.Mutex
	worldLoaded bool

	// WorldSeed is written into world_gen_settings.dat. It does not
	// change the flat generator's terrain, but the real server keeps it
	// and a mismatched seed is a world the two disagree about.
	WorldSeed int64

	// LevelName is what level.dat calls the world; empty uses the
	// world directory's base name, which is how vanilla names it.
	LevelName string

	// whitelistOn tracks the /whitelist flag; enforcement is not yet
	// implemented. idleTimeoutMinutes is the /setidletimeout value.
	whitelistOn        atomic.Bool
	idleTimeoutMinutes atomic.Int32
	// autosaveOff holds the /save-off flag; debugRun and perfRun track
	// /debug and /perf profiling windows; jfrRunning is /jfr's.
	autosaveOff atomic.Bool
	debugRun    profileRun
	perfRun     profileRun
	jfrRunning  atomic.Bool

	// inGameTick is set for the duration of each GameTick — a dirty
	// pack queued from inside the tick flushes on this tick's
	// boundary like vanilla's sendChanges, while one queued off-thread
	// (a command dispatch) waits for the next.
	inGameTick atomic.Bool

	// simulation owns gameplay mutations; network I/O remains on its connections.
	simulation simulationExecutor

	// GenerateStructures is recorded in the world's generator settings.
	// The zero value is false, so New sets the vanilla default (true);
	// it does not change the flat terrain, which has no structures to
	// generate either way.
	GenerateStructures bool

	// DataPath is the save file for player state and block entities.
	// Empty derives it from WorldPath (".data.json" suffix); an empty
	// WorldPath with an explicit DataPath still persists data.
	DataPath string

	// TimeSyncEvery controls the world-age broadcast cadence; vanilla
	// syncs once per second. Non-positive selects the 1s default, and
	// negative disables periodic sync entirely.
	TimeSyncEvery time.Duration

	// ViewDistance is the server's chunk view distance in columns.
	// The effective radius also honors the client's requested view
	// distance; scripted clients that never send Client Information
	// keep the minimal window. Zero selects the vanilla default (10).
	ViewDistance int

	// SimulationDistance is advertised in Join Game; entity ticking
	// will use it once entity simulation lands. Zero selects 10.
	SimulationDistance int

	// GameMode is the default game mode for joining players. The
	// zero value is creative to keep library embeddings predictable;
	// the mc-go binary defaults to survival (vanilla's default).
	GameMode byte

	// RconAddr/RconPassword configure the RCON listener: an empty
	// password disables it like vanilla's unset rcon.password.
	RconAddr     string
	RconPassword string
	// EnableQuery/QueryAddr configure the GameSpy4 UDP listener;
	// an empty QueryAddr binds the main port like vanilla's
	// query.port default.
	EnableQuery bool
	QueryAddr   string
	// StartFrozen starts the game clock frozen, as though /tick freeze
	// had been issued before the first tick. It exists for tests that
	// need a loaded world handed over untouched - a furnace's saved
	// cook and burn counters are exactly the number of ticks since the
	// world was loaded, so a test comparing them against the real
	// server has to be able to say "no ticks yet" and cannot, since
	// the first thing a joining player does is tick the world while
	// the freeze command is still in flight.
	StartFrozen bool

	// runtimeMu serializes startup, attachment and shutdown. Listener lifetime
	// never owns these resources: multiple frontends share one runtime.
	runtimeMu      sync.Mutex
	runtimeStarted bool
	connections    sync.WaitGroup
	runtimeDone    chan struct{}
	runtimeStop    func()
	runtimeCtx     context.Context
	runtimeCancel  context.CancelFunc

	listenersMu  sync.Mutex
	listeners    map[net.Listener]struct{}
	shuttingDown atomic.Bool

	bus           *event.Bus
	world         *world.World // overworld; also dims["minecraft:overworld"].w
	dims          map[string]*dimension
	commands      *plugin.Commands
	hooks         *plugin.Hooks
	pluginMu      sync.Mutex
	pluginManager *plugin.Manager
	// compCommands are the built-in commands that answer with a chat
	// component rather than plain text (see timecmd.go).
	compMu       sync.RWMutex
	compCommands map[string]func(string, []string) reply
	entities     *Manager
	board        scoreboard
	nextWindow   atomic.Int32
	// World rules, guarded by rulesMu. Difficulty and the game-rule
	// table are server-wide in vanilla (every player sees the same
	// values); lockDifficulty mirrors the /difficulty lock flag.
	rulesMu          sync.RWMutex
	difficulty       byte
	difficultyLocked bool
	gameRules        map[string]string
	// The world spawn, which a loaded level.dat can move; see
	// WorldSpawn. Guarded by spawnMu.
	spawnMu              sync.Mutex
	spawnPos             [3]int
	spawnYaw, spawnPitch float32
	haveSpawn            bool
	// ServerDir holds the vanilla-format admin files (ops.json,
	// whitelist.json, banned-players.json, banned-ips.json); empty
	// disables their load/save.
	ServerDir string

	// ops maps player name to permission level (1-4), from ops.json.
	// OpsRequired turns on vanilla's gating: without it every player is
	// level 4, which is what library embeddings and the scripted test
	// clients rely on. The mc-go binary enables it.
	ops     map[string]int
	opsList []adminOpEntry

	// banMu guards bannedNames, bannedIPs and whitelisted — the /ban
	// /pardon /ban-ip /pardon-ip and /whitelist add/remove tables —
	// plus the list order the json files keep them in.
	banMu         sync.Mutex
	bannedNames   map[string]string
	bannedIPs     map[string]string
	whitelisted   map[string]bool
	whitelistList []adminWLEntry
	bansList      []adminBanEntry
	ipBansList    []adminIPBanEntry

	// borders holds one world-border state per dimension, keyed by the
	// dimension's minecraft: name.
	borderMu sync.Mutex
	borders  map[string]*border

	// forcedChunks is /forceload's per-dimension forced-chunk set.
	forcedChunks forcedChunkSet
	// datapacks is the enabled set /datapack mutates (seeded with the
	// vanilla pack); customPacks are packs /datapack create registered.
	datapacks   map[string]bool
	customPacks map[string]bool
	// functions is the datapack function table loadFunctions fills from
	// WorldDir/datapacks: resource id -> command lines.
	functions map[string][]string
	// patrolNext is PatrolSpawner.nextTick — the countdown to the next
	// natural-patrol roll; starts at 0 so the first decrement rearms it
	// to 12000+rand(1200) exactly like vanilla.
	patrolNext int64

	// stopwatches is the /stopwatch set: resource id -> start time.
	stopwatchMu sync.Mutex
	stopwatches map[string]time.Time

	// bossbars holds the /bossbar bars in creation order.
	bossbarMu    sync.Mutex
	bossbars     map[string]*bossBar
	bossbarOrder []string
	OpsRequired  bool
	// savedPlayers holds login-time snapshots restored from DataPath.
	// untrack writes it from each connection goroutine and join reads
	// it, so every access runs under savedPlayersMu.
	savedPlayersMu sync.Mutex
	savedPlayers   map[string]playerPersist
	dataSaveErr    error
	// seenJoin records the names that reached the play stage this boot,
	// so a reconnect draws the returning-player sync (attribute reset
	// stub) even when nothing was persisted to disk. Guarded by seenMu.
	seenMu   sync.Mutex
	seenJoin map[string]bool

	sessions sync.Map // int64 -> *Session
	nextID   atomic.Int64
	online   atomic.Int32
	sem      chan struct{} // caps concurrent sessions
	started  time.Time     // server boot, for world age sync

	// clock is the game clock: 20 TPS, freezable and steppable.
	clock tickClock
	// ticks is the scheduled block/fluid tick queue; see scheduler.go
	// and fluid.go.
	ticks *tickScheduler
	// rng drives random block ticks and any other simulated chance;
	// see blocktick.go.
	rng *randState
	// randSections caches, per 16^3 section, whether it holds any block
	// with a random-tick rule. randMu guards it.
	randMu       sync.Mutex
	randSections map[randSectionKey]uint8

	// dirtyBlocks accumulates changed block positions per chunk
	// section across a tick; blockChangesFlushStep emits them the way
	// vanilla's ChunkHolder.broadcastChanges does — a lone change rides
	// block_update, several ride section_blocks_update, with the state
	// read at flush time. dirtyMu guards it.
	dirtyMu     sync.Mutex
	dirtyBlocks map[blockSectionKey]map[uint16]bool
	// sleepSkipNotified latches per dimension once the "skipping
	// through the night" action bar has been sent for the current
	// all-asleep window, so it is announced once rather than per tick.
	sleepSkipNotified map[string]bool
	// blockEvents is Level.blockEvent's queue — piston moves fire at
	// the tail of the tick (ServerLevel.tickBlockEvents).
	blockEvents []rsBlockEvent
	// movingPistons are the live moving-piston block entities — each
	// cell a piston move covered sits as a moving_piston for vanilla's
	// 2-tick interlude (progress += 0.5/tick, finalTick at >=1.0)
	// before its carried state lands.
	movingPistons []movingPiston
	// dirtyBEs holds block entities /data dirtied without a block
	// change — the sign re-render vanilla sends out of the same
	// tick-end broadcastChanges pass. dirtyBEOrder keeps it a queue.
	dirtyBEs     map[dirtyBEKey]bool
	dirtyBEOrder []dirtyBEKey
	// ticking is set while runGameLoop is alive — block changes batch
	// for the end-of-tick flush only then; without a ticker (unit tests
	// driving pipes directly) broadcastBlockState writes immediately.
	ticking atomic.Bool

	// weatherRaining/weatherThunder are the server-wide weather flags
	// (vanilla keeps one WeatherData on the server); weatherTicks is
	// the remaining duration counted down per tick — see weather.go.
	// rainLevel/thunderLevel are the lerped intensities vanilla's
	// isRaining/isThundering actually read (>0.2 / >0.9 thresholds,
	// ramping 0.01/tick — a thunder command doesn't channel for ~90
	// ticks).
	weatherRaining atomic.Bool
	weatherThunder atomic.Bool
	weatherTicks   atomic.Int64
	weatherRain    atomic.Int32 // rainLevel * 1000, lerped
	weatherThundLv atomic.Int32 // thunderLevel * 1000, lerped

	// storage is the /data storage scope — vanilla's CommandStorage:
	// a global map of resource-location id to NBT compound, empty for
	// ids nobody wrote. storageMu guards it.
	storageMu sync.Mutex
	storage   map[string]map[string]any
}

// New builds a server with its world, bus, and command registry.
// Call InitPlugins (or rely on main.go) before Serve.
func New(addr string) *Server {
	bus := event.New()
	s := &Server{
		Addr:             addr,
		MOTD:             MOTD,
		MaxPlayers:       MaxPlayers,
		KeepAliveEvery:   10 * time.Second,
		KeepAliveTimeout: 30 * time.Second,
		bus:              bus,
		world:            world.New(bus, 8),
		commands:         plugin.NewCommands(),
		hooks:            plugin.NewHooks(),
		entities:         NewManager(),
		storage:          map[string]map[string]any{},
		sem:              make(chan struct{}, 256),
		started:          time.Now(),
		// Creative keeps existing scripted sessions deterministic;
		// the mc-go binary sets survival from -gamemode.
		GameMode:           GameModeCreative,
		GenerateStructures: true,
		difficulty:         DifficultyNormal,
		gameRules:          defaultGameRules(),
		ops:                map[string]int{},
		borders:            map[string]*border{},
		bossbars:           map[string]*bossBar{},
		ticks:              newTickScheduler(),
		rng:                newRandState(0x5DEECE66D),
		randSections:       map[randSectionKey]uint8{},
		dirtyBlocks:        map[blockSectionKey]map[uint16]bool{},
		dirtyBEs:           map[dirtyBEKey]bool{},
		sleepSkipNotified:  map[string]bool{},
	}
	if s.ViewDistance == 0 {
		s.ViewDistance = 10
	}
	if s.SimulationDistance == 0 {
		s.SimulationDistance = 10
	}
	s.runtimeCtx, s.runtimeCancel = context.WithCancel(context.Background())
	s.runtimeDone = make(chan struct{})
	s.initDimensions()
	s.registerBuiltins()
	s.registerWorldCommands()
	return s
}

// worldAgeTicks reports the world age vanilla syncs: the number of
// completed game ticks. It is the game clock rather than the wall
// clock, so a frozen or stepped game reports exactly the ticks it has
// simulated - the same thing /time query gametime answers with.
func (s *Server) worldAgeTicks() int64 {
	return s.gameTime()
}

// Bus exposes the event hub to plugins and tests.
func (s *Server) Bus() *event.Bus { return s.bus }

// World exposes the sharded world.
func (s *Server) World() *world.World { return s.world }

// Entities exposes the entity manager.
func (s *Server) Entities() *Manager { return s.entities }

// OnlineCount is the number of logged-in players.
// Status probes and handshaking connections are tracked but never
// counted, matching vanilla's players.online.
func (s *Server) OnlineCount() int { return int(s.online.Load()) }

// Online keeps the old status-hook shape working.
func (s *Server) Online() int { return s.OnlineCount() }

// api adapts Server to plugin.API.
type serverAPI struct{ s *Server }

func (a serverAPI) Context() context.Context      { return a.s.runtimeCtx }
func (a serverAPI) Hooks() *plugin.Hooks          { return a.s.hooks }
func (a serverAPI) Bus() *event.Bus               { return a.s.bus }
func (a serverAPI) Commands() *plugin.Commands    { return a.s.commands }
func (a serverAPI) Kick(name, reason string) bool { return a.KickContext(a.s.runtimeCtx, name, reason) }
func (a serverAPI) KickContext(ctx context.Context, name, reason string) bool {
	if ctx.Err() != nil {
		return false
	}
	if sess := a.s.findSessionByName(name); sess == nil || sess.state.Load() != StatePlay || a.s.ShuttingDown() {
		return false
	}
	return a.s.queueSimulationAction(func() {
		if ctx.Err() == nil {
			a.s.kickPlayerOwned(name, reason)
		}
	})
}
func (a serverAPI) Teleport(name string, x, y, z float64) bool {
	return a.TeleportContext(a.s.runtimeCtx, name, x, y, z)
}
func (a serverAPI) TeleportContext(ctx context.Context, name string, x, y, z float64) bool {
	if ctx.Err() != nil {
		return false
	}
	if badFloat(x) || badFloat(y) || badFloat(z) {
		return false
	}
	if sess := a.s.findSessionByName(name); sess == nil || sess.state.Load() != StatePlay || a.s.ShuttingDown() {
		return false
	}
	return a.s.queueSimulationAction(func() {
		if ctx.Err() != nil {
			return
		}
		sess := a.s.findSessionByName(name)
		if sess == nil {
			return
		}
		_, _, _, yaw, pitch, _ := sess.Pos()
		a.s.teleportPlayerRot(name, x, y, z, yaw, pitch)
	})
}
func (a serverAPI) PlayerState(name string) (plugin.PlayerState, bool) {
	return a.s.PlayerStateOf(name)
}
func (a serverAPI) ReportViolation(v plugin.Violation) { a.s.ReportViolation(v) }
func (a serverAPI) ServerInfo() plugin.Info {
	return plugin.Info{
		VersionName:     VersionName,
		ProtocolVersion: ProtocolVersion,
		MOTD:            a.s.MOTD,
		MaxPlayers:      a.s.MaxPlayers,
	}
}

// InitPlugins runs every compiled-in registered plugin.
func (s *Server) InitPlugins() error {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if s.ShuttingDown() {
		return fmt.Errorf("server is shutting down")
	}
	if s.pluginManager != nil {
		return nil
	}
	manager, err := plugin.NewManager(s.runtimeCtx, serverAPI{s}, plugin.All())
	if err != nil {
		return err
	}
	s.pluginManager = manager
	return nil
}

// stopPlugins stops lifecycle resources outside the initialization lock.
func (s *Server) stopPlugins() {
	s.pluginMu.Lock()
	manager := s.pluginManager
	s.pluginManager = nil
	s.pluginMu.Unlock()
	if manager != nil {
		if err := manager.Close(); err != nil {
			log.Printf("plugins: %v", err)
		}
	}
	if s.hooks != nil {
		s.hooks.Close()
	}
	if s.bus != nil {
		s.bus.Close()
	}
}

// Serve accepts Java Edition connections on ln until it closes. It
// delegates to the registered "java" frontend so every protocol enters
// through the same Frontend abstraction. This method blocks.
func (s *Server) Serve(ln net.Listener) error {
	return s.ServeFrontend("java", ln)
}

// ServeFrontend serves a registered entry point over the shared runtime.
// Closing ln detaches this frontend. Stop releases the runtime itself.
func (s *Server) ServeFrontend(name string, ln net.Listener) error {
	if fe, ok := NewFrontend(name, s); ok {
		return fe.Serve(ln)
	}
	if fe, ok := frontend.New(name); ok {
		return s.Attach(fe, ln)
	}
	return fmt.Errorf("%s frontend not registered", name)
}

// ListenAndServe binds Addr and serves.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	return s.Serve(ln)
}

// sessionByEntityID returns the play session owning an entity id.
func (s *Server) sessionByEntityID(eid int32) *Session {
	if eid == 0 {
		return nil
	}
	var out *Session
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.entityID.Load() == eid {
			out = other
			return false
		}
		return true
	})
	return out
}

// entityByID returns the managed entity with the id, if any.
func (s *Server) entityByID(eid int32) *Entity {
	if eid == 0 {
		return nil
	}
	if e, ok := s.entities.Get(eid); ok {
		return &e
	}
	return nil
}

func (s *Server) track(sess *Session) {
	s.sessions.Store(sess.id, sess)
	// OnlineCount only covers logged-in players; status probes stay
	// uncounted. The increment happens in handleLogin once the name
	// is assigned.
}

func (s *Server) untrack(sess *Session) {
	s.sessions.Delete(sess.id)
	// ServerWaypointManager.removePlayer: the leaver's receiver row is
	// dropped; its transmitter conns unwind via the dead-source path
	// next tick (UNTRACK to every remaining receiver).
	s.wpDropAll(sess)
	// The open menu is removed on disconnect as it is on a client
	// close — only its contents drop rather than going back into the
	// inventory (AbstractContainerMenu.removed).
	sess.windowMu.Lock()
	w := sess.window
	sess.window = nil
	sess.windowMu.Unlock()
	if w != nil {
		s.menuRemoved(sess, w, true)
	}
	// A disconnecting rider dismounts: remaining viewers get the
	// vehicle's emptied passenger list (PlayerList.remove -> stopRiding).
	s.dismount(sess)
	// A player who leaves is no longer editing anything: measured on 26.2,
	// the sign its editor was open on is free for the next player at once.
	s.releaseSignEdits(sess)
	name := sess.getName()
	if name == "" {
		return
	}
	s.online.Add(-1)
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerLeave, map[string]string{
			"name": name,
		})
	}
	log.Printf("%s left the game", name)
	s.broadcastSystemChatComponent(component{
		color:     "yellow",
		translate: "multiplayer.player.left",
		with:      []any{playerChatComponent(sess)},
	})
	s.broadcastPlayerInfoRemove([][16]byte{sess.getUUID()})
	// Retire the player's entity everywhere it was spawned.
	if sess.spawned.Load() {
		if eid := sess.entityID.Load(); eid != 0 {
			s.entities.Remove(eid)
			s.broadcastEntityRemoveIn(sess.dimName(), nil, []int32{eid})
		}
	}
	// If they were the last one standing in that dimension, its chunks
	// unload: everything still live there parks dormant until someone
	// returns (vanilla's chunk unload on the entity side).
	s.reparkDim(sess.dimName())
	// Persist the leaving player's state so a reconnect resumes it —
	// in memory always, to disk when a data path is configured. A
	// vanilla-layout world writes the player's .dat here, the same file
	// vanilla's PlayerList.save touches on disconnect.
	if sess.state.Load() == StatePlay {
		sess.addCustomStat("leave_game", 1)
		s.savedPlayersMu.Lock()
		if s.savedPlayers == nil {
			s.savedPlayers = make(map[string]playerPersist)
		}
		p := snapshotPlayer(sess)
		s.savedPlayers[name] = p
		s.savedPlayersMu.Unlock()
		if s.dataPath() != "" {
			if err := s.saveServerDataOwned(); err != nil && s.dataSaveErr == nil {
				s.dataSaveErr = err
			}
		}
		if err := s.savePlayerFile(p); err != nil && s.dataSaveErr == nil {
			s.dataSaveErr = err
		}
		if err := s.savePlayerMetaFile(p); err != nil && s.dataSaveErr == nil {
			s.dataSaveErr = err
		}
	}
}
