package event

// EventBus is the interface for the event bus
type EventBus interface {
	// Fire triggers an event
	Fire(event Event)
	
	// Server Events
	RegisterServerStart(plugin string, priority Priority, handler func(*ServerStartEvent))
	RegisterServerStop(plugin string, priority Priority, handler func(*ServerStopEvent))
	RegisterServerTick(plugin string, priority Priority, handler func(*ServerTickEvent))
	
	// Player Events
	RegisterPlayerPreLogin(plugin string, priority Priority, handler func(*PlayerPreLoginEvent))
	RegisterPlayerLogin(plugin string, priority Priority, handler func(*PlayerLoginEvent))
	RegisterPlayerJoin(plugin string, priority Priority, handler func(*PlayerJoinEvent))
	RegisterPlayerQuit(plugin string, priority Priority, handler func(*PlayerQuitEvent))
	RegisterPlayerChat(plugin string, priority Priority, handler func(*PlayerChatEvent))
	RegisterPlayerCommand(plugin string, priority Priority, handler func(*PlayerCommandEvent))
	RegisterPlayerMove(plugin string, priority Priority, handler func(*PlayerMoveEvent))
	RegisterPlayerTeleport(plugin string, priority Priority, handler func(*PlayerTeleportEvent))
	RegisterPlayerGameModeChange(plugin string, priority Priority, handler func(*PlayerGameModeChangeEvent))
	
	// Block Events
	RegisterBlockBreak(plugin string, priority Priority, handler func(*BlockBreakEvent))
	RegisterBlockPlace(plugin string, priority Priority, handler func(*BlockPlaceEvent))
	
	// World Events
	RegisterChunkGenerate(plugin string, priority Priority, handler func(*ChunkGenerateEvent))
	RegisterChunkLoad(plugin string, priority Priority, handler func(*ChunkLoadEvent))
	RegisterChunkUnload(plugin string, priority Priority, handler func(*ChunkUnloadEvent))
	
	// Packet Events
	RegisterPacketReceive(plugin string, priority Priority, handler func(*PacketReceiveEvent))
	RegisterPacketSend(plugin string, priority Priority, handler func(*PacketSendEvent))
	
	// Unregister all handlers for a plugin
	Unregister(plugin string)
	
	// Clear all handlers
	Clear()
}