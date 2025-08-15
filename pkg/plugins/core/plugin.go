package core

import (
    "fmt"
    "strconv"

    "github.com/mc-go/server/pkg/event"
    "github.com/mc-go/server/pkg/player"
    "github.com/mc-go/server/pkg/plugin"
    "github.com/mc-go/server/pkg/protocol"
    "github.com/mc-go/server/pkg/types"
)

// Plugin handles essential gameplay functionality
type Plugin struct {
	*plugin.BasePlugin
	eventBus *event.Bus
}

// New creates a new core plugin
func New(eventBus *event.Bus) plugin.Plugin {
	return &Plugin{
		BasePlugin: plugin.NewBasePlugin(
			"CorePlugin",
			"1.0.0",
			"MC-GO Team",
			"Core gameplay functionality",
		),
		eventBus: eventBus,
	}
}

func (p *Plugin) OnEnable() error {
	fmt.Println("[CorePlugin] Registering event handlers...")

	// Register strongly-typed event handlers
	name := p.Name()

	// Server events
	p.eventBus.RegisterServerStart(name, event.PriorityNormal, p.onServerStart)
	p.eventBus.RegisterServerTick(name, event.PriorityNormal, p.onServerTick)

	// Player events
	p.eventBus.RegisterPlayerJoin(name, event.PriorityNormal, p.onPlayerJoin)
	p.eventBus.RegisterPlayerQuit(name, event.PriorityNormal, p.onPlayerQuit)
	p.eventBus.RegisterPlayerMove(name, event.PriorityNormal, p.onPlayerMove)
	p.eventBus.RegisterPlayerChat(name, event.PriorityNormal, p.onPlayerChat)
	p.eventBus.RegisterPlayerCommand(name, event.PriorityHigh, p.onPlayerCommand)
	p.eventBus.RegisterPlayerTeleport(name, event.PriorityNormal, p.onPlayerTeleport)
	p.eventBus.RegisterPlayerGameModeChange(name, event.PriorityNormal, p.onPlayerGameModeChange)

	// Block events
	p.eventBus.RegisterBlockBreak(name, event.PriorityNormal, p.onBlockBreak)
	p.eventBus.RegisterBlockPlace(name, event.PriorityNormal, p.onBlockPlace)

	// World events
	p.eventBus.RegisterChunkGenerate(name, event.PriorityNormal, p.onChunkGenerate)
	p.eventBus.RegisterChunkLoad(name, event.PriorityNormal, p.onChunkLoad)

	// Packet events (monitor priority to observe but not modify)
	p.eventBus.RegisterPacketReceive(name, event.PriorityMonitor, p.onPacketReceive)

	fmt.Println("[CorePlugin] Core gameplay plugin enabled")
	return nil
}

func (p *Plugin) OnDisable() error {
    p.eventBus.Unregister(p.Name())
    fmt.Println("[CorePlugin] Core gameplay plugin disabled")
    return nil
}

// RegisterCommands provides basic built-in commands
func (p *Plugin) RegisterCommands() map[string]plugin.CommandHandler {
    return map[string]plugin.CommandHandler{
        "help": func(sender *player.Player, args []string) error {
            sender.SendMessage("§eCommands: §7/help, /gm <0-3>, /tp <x> <y> <z>, /pos")
            return nil
        },
        "pos": func(sender *player.Player, args []string) error {
            x, y, z := sender.GetPosition()
            sender.SendMessage(fmt.Sprintf("§7You are at §e(%.2f, %.2f, %.2f)", x, y, z))
            return nil
        },
        "gm": func(sender *player.Player, args []string) error {
            if len(args) < 1 {
                sender.SendMessage("§cUsage: /gm <0-3>")
                return nil
            }
            v, err := strconv.Atoi(args[0])
            if err != nil || v < 0 || v > 3 {
                sender.SendMessage("§cGame mode must be 0,1,2,3")
                return nil
            }
            sender.SetGameMode(v)
            sender.SendMessage(fmt.Sprintf("§aGame mode set to %d", v))
            return nil
        },
        "tp": func(sender *player.Player, args []string) error {
            if len(args) < 3 {
                sender.SendMessage("§cUsage: /tp <x> <y> <z>")
                return nil
            }
            xf, err1 := strconv.ParseFloat(args[0], 64)
            yf, err2 := strconv.ParseFloat(args[1], 64)
            zf, err3 := strconv.ParseFloat(args[2], 64)
            if err1 != nil || err2 != nil || err3 != nil {
                sender.SendMessage("§cInvalid coordinates")
                return nil
            }
            sender.Teleport(xf, yf, zf)
            sender.SendMessage(fmt.Sprintf("§aTeleported to (%.2f, %.2f, %.2f)", xf, yf, zf))
            return nil
        },
    }
}

// Server event handlers

func (p *Plugin) onServerStart(e *event.ServerStartEvent) {
	fmt.Println("[CorePlugin] Server started")
}

func (p *Plugin) onServerTick(e *event.ServerTickEvent) {
	// Update world time every second (20 ticks)
	if e.Tick%20 == 0 {
		// This would update world time in a full implementation
	}
}

// Player event handlers

func (p *Plugin) onPlayerJoin(e *event.PlayerJoinEvent) {
	// Send welcome message
	e.Player.SendMessage("§e[Server] §aWelcome to the server, §b" + e.Player.GetUsername() + "§a!")
	e.Player.SendMessage("§7Type §e/help §7for a list of commands")

	// Set join message
	e.JoinMessage = "§e" + e.Player.GetUsername() + " joined the game"

	// Send initial chunks around spawn
	//p.sendSpawnChunks(e.Player)
}

func (p *Plugin) onPlayerQuit(e *event.PlayerQuitEvent) {
	e.QuitMessage = "§e" + e.Player.GetUsername() + " left the game"
}

func (p *Plugin) onPlayerMove(e *event.PlayerMoveEvent) {
	// Check if player moved to a new chunk
	oldChunkX := int32(e.From.X) >> 4
	oldChunkZ := int32(e.From.Z) >> 4
	newChunkX := int32(e.To.X) >> 4
	newChunkZ := int32(e.To.Z) >> 4

	if oldChunkX != newChunkX || oldChunkZ != newChunkZ {
		// Player moved to a new chunk, load new chunks if needed
		fmt.Printf("[CorePlugin] %s moved to chunk %d,%d\n",
			e.Player.GetUsername(), newChunkX, newChunkZ)

		// In a full implementation, this would trigger chunk loading
		p.loadChunksAroundPlayer(e.Player, newChunkX, newChunkZ)
	}

	// Check for invalid movements (anti-cheat)
	dx := e.To.X - e.From.X
	dy := e.To.Y - e.From.Y
	dz := e.To.Z - e.From.Z
	distance := dx*dx + dy*dy + dz*dz

	// If player moved too far in one tick (possible teleport hack)
	if distance > 100 { // 10 blocks squared
		fmt.Printf("[CorePlugin] Suspicious movement from %s: %.2f blocks\n",
			e.Player.GetUsername(), distance)
		// Could cancel the event here: e.SetCancelled(true)
	}
}

func (p *Plugin) onPlayerChat(e *event.PlayerChatEvent) {
	// Format chat message
	e.Format = "<%s> %s"

	// Could add chat filtering, permissions, etc.
	fmt.Printf("[Chat] %s: %s\n", e.Player.GetUsername(), e.Message)
}

func (p *Plugin) onPlayerCommand(e *event.PlayerCommandEvent) {
	// Log commands (but don't cancel - let command system handle it)
	fmt.Printf("[Command] %s: /%s %v\n",
		e.Player.GetUsername(), e.Command, e.Args)
}

func (p *Plugin) onPlayerTeleport(e *event.PlayerTeleportEvent) {
	fmt.Printf("[CorePlugin] %s teleported from (%.1f, %.1f, %.1f) to (%.1f, %.1f, %.1f) [%v]\n",
		e.Player.GetUsername(),
		e.From.X, e.From.Y, e.From.Z,
		e.To.X, e.To.Y, e.To.Z,
		e.Cause)

	// Load chunks at destination
	chunkX := int32(e.To.X) >> 4
	chunkZ := int32(e.To.Z) >> 4
	p.loadChunksAroundPlayer(e.Player, chunkX, chunkZ)
}

func (p *Plugin) onPlayerGameModeChange(e *event.PlayerGameModeChangeEvent) {
	modes := []string{"Survival", "Creative", "Adventure", "Spectator"}
	oldMode := "Unknown"
	newMode := "Unknown"

	if int(e.OldGameMode) < len(modes) {
		oldMode = modes[e.OldGameMode]
	}
	if int(e.NewGameMode) < len(modes) {
		newMode = modes[e.NewGameMode]
	}

	fmt.Printf("[CorePlugin] %s changed game mode from %s to %s\n",
		e.Player.GetUsername(), oldMode, newMode)

	// Apply game mode specific settings
	switch e.NewGameMode {
	case 1: // Creative
		e.Player.SendMessage("§aYou are now in Creative mode")
	case 0: // Survival
		e.Player.SendMessage("§aYou are now in Survival mode")
	case 2: // Adventure
		e.Player.SendMessage("§aYou are now in Adventure mode")
	case 3: // Spectator
		e.Player.SendMessage("§aYou are now in Spectator mode")
	}
}

// Block event handlers

func (p *Plugin) onBlockBreak(e *event.BlockBreakEvent) {
	// Check if player can break blocks
	gameMode := e.Player.GetGameMode()
	if gameMode == 2 { // Adventure
		e.SetCancelled(true)
		e.Player.SendMessage("§cYou cannot break blocks in Adventure mode")
		return
	}

	// Check if block is breakable (e.g., bedrock)
	if e.BlockType == types.BlockTypeBedrock { // Bedrock
		if gameMode != 1 { // Creative
			e.SetCancelled(true)
			e.Player.SendMessage("§cYou cannot break bedrock")
			return
		}
	}

	fmt.Printf("[CorePlugin] %s broke block at %d,%d,%d\n",
		e.Player.GetUsername(), e.Block.X, e.Block.Y, e.Block.Z)
}

func (p *Plugin) onBlockPlace(e *event.BlockPlaceEvent) {
	// Check if player can place blocks
	gameMode := e.Player.GetGameMode()
	if gameMode == 2 || gameMode == 3 { // Adventure or Spectator
		e.SetCancelled(true)
		e.Player.SendMessage("§cYou cannot place blocks in this game mode")
		return
	}

	fmt.Printf("[CorePlugin] %s placed block at %d,%d,%d\n",
		e.Player.GetUsername(), e.Block.X, e.Block.Y, e.Block.Z)
}

// World event handlers

func (p *Plugin) onChunkGenerate(e *event.ChunkGenerateEvent) {
	// Generate chunk data
	primaryBitMask, chunkData := protocol.CreateEmptyChunkData()
	packet := protocol.CreateChunkDataPacket(
		e.ChunkX,
		e.ChunkZ,
		true,
		primaryBitMask,
		chunkData,
	)
	e.Data = packet.Data.Bytes()

	fmt.Printf("[CorePlugin] Generated chunk %d,%d for world %s\n",
		e.ChunkX, e.ChunkZ, e.World)
}

func (p *Plugin) onChunkLoad(e *event.ChunkLoadEvent) {
	if e.IsNew {
		fmt.Printf("[CorePlugin] Loading new chunk %d,%d in world %s\n",
			e.ChunkX, e.ChunkZ, e.World)
	}
}

// Packet event handlers

func (p *Plugin) onPacketReceive(e *event.PacketReceiveEvent) {
	// Monitor incoming packets (for debugging/logging)
	// This is read-only due to PriorityMonitor
}

// Helper methods

func (p *Plugin) sendSpawnChunks(player types.Player) {
	// Send 3x3 chunks around spawn
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			p.sendChunk(player, x, z)
		}
	}
}

func (p *Plugin) loadChunksAroundPlayer(player types.Player, centerX, centerZ int32) {
	// Load chunks in view distance
	viewDistance := int32(3) // Should come from config

	for x := centerX - viewDistance; x <= centerX+viewDistance; x++ {
		for z := centerZ - viewDistance; z <= centerZ+viewDistance; z++ {
			// In a full implementation, check if chunk is already loaded
			p.sendChunk(player, x, z)
		}
	}
}

func (p *Plugin) sendChunk(player types.Player, chunkX, chunkZ int32) {
	// Fire chunk generate event
	chunkEvent := &event.ChunkGenerateEvent{
		ChunkX: chunkX,
		ChunkZ: chunkZ,
		World:  "world", // Should get from player's world
	}

	p.eventBus.Fire(chunkEvent)

	// Send the generated chunk data to the player
	if len(chunkEvent.Data) > 0 {
		// This would send the chunk packet to the player
		fmt.Printf("[CorePlugin] Sending chunk %d,%d to %s\n",
			chunkX, chunkZ, player.GetUsername())
	}
}
