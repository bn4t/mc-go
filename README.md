# Minecraft 1.8.9 Server Implementation

A modular Minecraft 1.8.9 server implementation written in Go.

## Features

- **Modular Architecture**: Clean separation of concerns with distinct packages for server, network, protocol, world, player, plugin, and configuration management
- **Plugin System**: Dynamic plugin loading system supporting custom commands and event handlers
- **World Management**: Basic world and chunk generation with support for multiple worlds
- **Player Management**: Complete player session handling with packet processing
- **Network Layer**: Full implementation of Minecraft 1.8.9 protocol (version 47) with compression support
- **Configuration**: YAML-based configuration with sensible defaults
- **Logging**: Structured logging with Zap

## Project Structure

```
mc-go/
├── cmd/
│   └── server/          # Server entry point
├── pkg/
│   ├── config/          # Configuration management
│   ├── network/         # Network and client handling
│   ├── player/          # Player entities and management
│   ├── plugin/          # Plugin system
│   ├── protocol/        # Minecraft protocol implementation
│   ├── server/          # Core server logic
│   └── world/           # World and chunk management
└── internal/
    └── utils/           # Internal utilities
```

## Quick Start

1. Build the server:
```bash
go build -o mcserver cmd/server/main.go
```

2. Run the server:
```bash
./mcserver
```

The server will create a default `server.yaml` configuration file on first run.

## Configuration

The server uses a YAML configuration file with the following main sections:

- **Server Settings**: Host, port, max players, MOTD, online mode
- **World Settings**: Default world, view distance, world generation options
- **Network Settings**: Compression, timeouts, query/RCON support
- **Game Settings**: Difficulty, game mode, PvP, spawn protection

## Plugin Development

Plugins must implement the `Plugin` interface:

```go
type Plugin interface {
    Name() string
    Version() string
    Author() string
    Description() string
    OnEnable() error
    OnDisable() error
    OnTick()
    RegisterCommands() map[string]CommandHandler
    RegisterEventHandlers() map[EventType]EventHandler
}
```

Build plugins as shared objects (`.so` files) and place them in the `plugins/` directory.

## Architecture

### Core Components

- **Server**: Main server orchestration and lifecycle management
- **Network Manager**: Handles client connections and packet compression
- **Protocol**: Complete Minecraft 1.8.9 protocol implementation with packet serialization/deserialization
- **Player Manager**: Manages player sessions, authentication, and state
- **World Manager**: Handles world loading, chunk generation, and block management
- **Plugin Manager**: Dynamic plugin loading, event system, and command registration

### Packet Flow

1. Client connects → Network layer accepts connection
2. Handshake → Determine connection state (Status/Login)
3. Login → Authenticate player (online/offline mode)
4. Join Game → Initialize player in world
5. Play → Handle gameplay packets bidirectionally

## Supported Features

- Basic server status and ping
- Player authentication (online/offline mode)
- World join and spawn
- Basic movement and rotation
- Chat messages
- Keep-alive handling
- Plugin system with events
- Command system
- Multiple worlds
- Chunk generation

## Requirements

- Go 1.21 or higher
- Network port 25565 (default)

## License

This implementation is for educational purposes.