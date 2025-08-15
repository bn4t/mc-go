package player

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mc-go/server/pkg/event"
	"github.com/mc-go/server/pkg/network"
	"github.com/mc-go/server/pkg/protocol"
	"github.com/mc-go/server/pkg/types"
)

type Player struct {
	username       string
	uuid           uuid.UUID
	client         *network.Client
	entityID       int32
	x, y, z        float64
	yaw, pitch     float32
	onGround       bool
	health         float32
	food           int32
	gameMode       GameMode
	permissions    []string
	world          types.World
	mu             sync.RWMutex
	lastKeepAlive  time.Time
	commandHandler interface {
		ExecuteCommand(sender *Player, command string) error
	}
	eventBus event.EventBus
}

type GameMode int

const (
	Survival GameMode = iota
	Creative
	Adventure
	Spectator
)

func NewPlayer(username string, uuid uuid.UUID, client *network.Client, entityID int32, eventBus event.EventBus) *Player {
	return &Player{
		username:      username,
		uuid:          uuid,
		client:        client,
		entityID:      entityID,
		health:        20.0,
		food:          20,
		gameMode:      Survival,
		permissions:   []string{},
		lastKeepAlive: time.Now(),
		eventBus:      eventBus,
	}
}

func (p *Player) SetCommandHandler(handler interface {
	ExecuteCommand(sender *Player, command string) error
}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commandHandler = handler
}

func (p *Player) SetEventBus(bus event.EventBus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eventBus = bus
}

func (p *Player) GetUsername() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.username
}

func (p *Player) GetUUID() uuid.UUID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.uuid
}

func (p *Player) GetEntityID() int32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.entityID
}

func (p *Player) GetPosition() (float64, float64, float64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.x, p.y, p.z
}

func (p *Player) SetPosition(x, y, z float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.x = x
	p.y = y
	p.z = z
}

func (p *Player) GetRotation() (float32, float32) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.yaw, p.pitch
}

func (p *Player) SetRotation(yaw, pitch float32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.yaw = yaw
	p.pitch = pitch
}

func (p *Player) GetGameMode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return int(p.gameMode)
}

func (p *Player) SetGameMode(mode int) {
    // Fire cancellable event before applying
    p.mu.RLock()
    old := p.gameMode
    bus := p.eventBus
    p.mu.RUnlock()

    if bus != nil {
        e := &event.PlayerGameModeChangeEvent{Player: p, OldGameMode: int(old), NewGameMode: mode}
        bus.Fire(e)
        if e.IsCancelled() {
            return
        }
    }

    p.mu.Lock()
    p.gameMode = GameMode(mode)
    p.mu.Unlock()
    _ = p.sendGameModeChange()
}

func (p *Player) JoinWorld(world types.World) error {
	p.mu.Lock()
	p.world = world
	p.mu.Unlock()

	fmt.Printf("DEBUG: Sending Join Game packet\n")
	if err := p.sendJoinGame(); err != nil {
		return fmt.Errorf("failed to send join game: %w", err)
	}

	fmt.Printf("DEBUG: Sending Spawn Position packet\n")
	if err := p.sendSpawnPosition(); err != nil {
		return fmt.Errorf("failed to send spawn position: %w", err)
	}

	fmt.Printf("DEBUG: Sending Player Abilities packet\n")
	if err := p.sendPlayerAbilities(); err != nil {
		return fmt.Errorf("failed to send player abilities: %w", err)
	}

	fmt.Printf("DEBUG: Sending Player Position and Look packet\n")
	if err := p.sendPlayerPositionAndLook(); err != nil {
		return fmt.Errorf("failed to send player position and look: %w", err)
	}

	if err := world.SpawnPlayer(p); err != nil {
		return fmt.Errorf("failed to spawn player in world: %w", err)
	}

	// Send chunks after a small delay to ensure client is ready
	go func() {
		time.Sleep(50 * time.Millisecond)
		fmt.Printf("DEBUG: Sending initial chunks\n")
		if err := p.sendInitialChunks(); err != nil {
			fmt.Printf("ERROR: Failed to send initial chunks: %v\n", err)
		}
	}()

	fmt.Printf("DEBUG: JoinWorld completed successfully\n")
	return nil
}

func (p *Player) HandlePackets() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	done := make(chan bool)

	// Goroutine for sending keep-alive packets
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := p.sendKeepAlive(); err != nil {
					done <- true
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Main packet reading loop
	for {
		// Set read deadline
		if err := p.client.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			fmt.Printf("ERROR: Failed to set deadline: %v\n", err)
			break
		}

		packet, err := p.client.ReadPacket()
		if err != nil {
			fmt.Printf("ERROR: Failed to read packet from %s: %v\n", p.username, err)
			// Check if it's a timeout or actual error
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				p.Disconnect("Connection timeout")
			} else {
				p.Disconnect("Connection lost")
			}
			close(done)
			return
		}

        fmt.Printf("DEBUG: Received packet 0x%02X from %s\n", packet.ID, p.username)

        // Fire PacketReceiveEvent; allow cancellation
        p.mu.RLock()
        bus := p.eventBus
        p.mu.RUnlock()
        if bus != nil {
            e := &event.PacketReceiveEvent{Player: p, Packet: packet}
            bus.Fire(e)
            if e.IsCancelled() {
                continue
            }
        }

		if err := p.handlePacket(packet); err != nil {
			fmt.Printf("ERROR: Failed to handle packet 0x%02X: %v\n", packet.ID, err)
			// Log the error but don't disconnect for minor issues
			continue
		}
	}

	close(done)
}

func (p *Player) handlePacket(packet *protocol.Packet) error {
	switch packet.ID {
	case 0x00:
		return p.handleKeepAlive(packet)
	case 0x01:
		return p.handleChatMessage(packet)
	case 0x03:
		return p.handlePlayerOnGround(packet)
	case 0x04:
		return p.handlePlayerPosition(packet)
	case 0x05:
		return p.handlePlayerLook(packet)
	case 0x06:
		return p.handlePlayerPositionAndLook(packet)
	case 0x07:
		return p.handlePlayerDigging(packet)
	case 0x08:
		return p.handlePlayerBlockPlacement(packet)
	case 0x09:
		return p.handleHeldItemChange(packet)
	case 0x0A:
		return p.handleAnimation(packet)
	case 0x0B:
		return p.handleEntityAction(packet)
	case 0x0D:
		return p.handleCloseWindow(packet)
	case 0x0E:
		return p.handleClickWindow(packet)
	case 0x10:
		return p.handleCreativeInventoryAction(packet)
	case 0x13:
		return p.handlePlayerAbilities(packet)
	case 0x15:
		return p.handleClientSettings(packet)
	case 0x16:
		return p.handleClientStatus(packet)
	case 0x17:
		return p.handlePluginMessage(packet)
	}

	return nil
}

func (p *Player) handleKeepAlive(packet *protocol.Packet) error {
	p.mu.Lock()
	p.lastKeepAlive = time.Now()
	p.mu.Unlock()
	return nil
}

func (p *Player) handleChatMessage(packet *protocol.Packet) error {
	message, err := packet.ReadString()
	if err != nil {
		return err
	}

	if len(message) > 0 && message[0] == '/' {
		p.handleCommand(message[1:])
	} else {
		// Fire typed PlayerChatEvent allowing plugins to modify/deny
		p.mu.RLock()
		bus := p.eventBus
		p.mu.RUnlock()

		if bus != nil {
			e := &event.PlayerChatEvent{
				Message: message,
				Format:  "<%s> %s",
				Player:  p,
			}
			bus.Fire(e)
			if e.IsCancelled() {
				return nil
			}
			// Send formatted chat line (currently to this player only)
			formatted := fmt.Sprintf(e.Format, p.username, e.Message)
			p.broadcastChatMessage(formatted)
		} else {
			// Fallback format if no bus
			formatted := fmt.Sprintf("<%s> %s", p.username, message)
			p.broadcastChatMessage(formatted)
		}
	}

	return nil
}

func (p *Player) handlePlayerOnGround(packet *protocol.Packet) error {
	onGround, err := packet.ReadBoolean()
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.onGround = onGround
	p.mu.Unlock()

	return nil
}

func (p *Player) handlePlayerPosition(packet *protocol.Packet) error {
	x, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	y, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	z, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	onGround, err := packet.ReadBoolean()
	if err != nil {
		return err
	}

	// Store old position for event
	p.mu.Lock()
	oldX, oldY, oldZ := p.x, p.y, p.z
	oldYaw, oldPitch := p.yaw, p.pitch
	p.x = x
	p.y = y
	p.z = z
	p.onGround = onGround
	p.mu.Unlock()

	// Trigger move event
	p.triggerMoveEvent(oldX, oldY, oldZ, x, y, z, oldYaw, oldPitch, oldYaw, oldPitch)

	return nil
}

func (p *Player) handlePlayerLook(packet *protocol.Packet) error {
	yaw, err := packet.ReadFloat()
	if err != nil {
		return err
	}

	pitch, err := packet.ReadFloat()
	if err != nil {
		return err
	}

	onGround, err := packet.ReadBoolean()
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.yaw = yaw
	p.pitch = pitch
	p.onGround = onGround
	p.mu.Unlock()

	return nil
}

func (p *Player) handlePlayerPositionAndLook(packet *protocol.Packet) error {
	x, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	y, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	z, err := packet.ReadDouble()
	if err != nil {
		return err
	}

	yaw, err := packet.ReadFloat()
	if err != nil {
		return err
	}

	pitch, err := packet.ReadFloat()
	if err != nil {
		return err
	}

	onGround, err := packet.ReadBoolean()
	if err != nil {
		return err
	}

	// Store old position for event
	p.mu.Lock()
	oldX, oldY, oldZ := p.x, p.y, p.z
	oldYaw, oldPitch := p.yaw, p.pitch
	p.x = x
	p.y = y
	p.z = z
	p.yaw = yaw
	p.pitch = pitch
	p.onGround = onGround
	p.mu.Unlock()

	// Trigger move event
	p.triggerMoveEvent(oldX, oldY, oldZ, x, y, z, oldYaw, oldPitch, yaw, pitch)

	return nil
}

func (p *Player) handlePlayerDigging(packet *protocol.Packet) error {
    // 1.8.9: Status (VarInt), Position (Long), Face (Byte)
    status, err := packet.ReadVarInt()
    if err != nil {
        return err
    }
    pos, err := packet.ReadLong()
    if err != nil {
        return err
    }
    // face, _ := packet.ReadByte() // not used
    _ , _ = packet.ReadByte()

    x, y, z := decodeBlockPosition(pos)

    // Fire BlockBreakEvent on finish destroy (status 2)
    if status == 2 {
        p.mu.RLock()
        bus := p.eventBus
        p.mu.RUnlock()
        if bus != nil {
            e := &event.BlockBreakEvent{
                Player: p,
                Block:  event.Block{X: x, Y: y, Z: z, World: "world"},
            }
            bus.Fire(e)
            if e.IsCancelled() {
                return nil
            }
        }
    }

    return nil
}

func (p *Player) handlePlayerBlockPlacement(packet *protocol.Packet) error {
    // 1.8.9: Position (Long), Face (Byte), HeldItem, CursorX/Y/Z
    pos, err := packet.ReadLong()
    if err != nil {
        return err
    }
    // face, _ := packet.ReadByte() // ignore remainder for now
    _, _ = packet.ReadByte()

    x, y, z := decodeBlockPosition(pos)

    p.mu.RLock()
    bus := p.eventBus
    p.mu.RUnlock()
    if bus != nil {
        e := &event.BlockPlaceEvent{
            Player: p,
            Block:  event.Block{X: x, Y: y, Z: z, World: "world"},
        }
        bus.Fire(e)
        if e.IsCancelled() {
            return nil
        }
    }

    return nil
}

func (p *Player) handleHeldItemChange(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleAnimation(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleEntityAction(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleCloseWindow(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleClickWindow(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleCreativeInventoryAction(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handlePlayerAbilities(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleClientSettings(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleClientStatus(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handlePluginMessage(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handleCommand(command string) {
	p.mu.RLock()
	handler := p.commandHandler
	bus := p.eventBus
	p.mu.RUnlock()

	if handler == nil {
		p.SendMessage("§cNo command handler available")
		return
	}

	// Fire typed PlayerCommandEvent first; allow cancellation
	if bus != nil {
		// Best-effort parse into name + args for the event
		parts := strings.Fields(command)
		cmdName := ""
		var args []string
		if len(parts) > 0 {
			cmdName = parts[0]
			if len(parts) > 1 {
				args = parts[1:]
			}
		}
		e := &event.PlayerCommandEvent{Player: p, Command: cmdName, Args: args}
		bus.Fire(e)
		if e.IsCancelled() {
			return
		}
	}

	if err := handler.ExecuteCommand(p, command); err != nil {
		p.SendMessage(fmt.Sprintf("§cError: %s", err.Error()))
	}
}

func (p *Player) broadcastChatMessage(formatted string) {
	packet := protocol.NewPacket(0x02)
	packet.WriteString(fmt.Sprintf(`{"text":"%s"}`, formatted))
	_ = packet.WriteByte(0)

	p.client.WritePacket(packet)
}

func (p *Player) sendJoinGame() error {
	packet := protocol.NewPacket(0x01)     // Join Game packet
	packet.WriteInt(p.entityID)            // Entity ID
	_ = packet.WriteByte(byte(p.gameMode)) // Gamemode
	_ = packet.WriteByte(0)                // Dimension (0 = overworld)
	_ = packet.WriteByte(0)                // Difficulty
	_ = packet.WriteByte(60)               // Max players (ignored by client)
	packet.WriteString("default")          // Level type
	packet.WriteBoolean(false)             // Reduced debug info

	if err := p.client.WritePacket(packet); err != nil {
		return fmt.Errorf("WritePacket failed: %w", err)
	}
	return nil
}

func (p *Player) sendSpawnPosition() error {
	packet := protocol.NewPacket(0x05) // Spawn Position packet
	// Position is encoded as (x & 0x3FFFFFF) << 38 | (y & 0xFFF) << 26 | (z & 0x3FFFFFF)
	// For position 0,64,0:
	position := int64(0)<<38 | int64(64)<<26 | int64(0)
	packet.WriteLong(position)

	if err := p.client.WritePacket(packet); err != nil {
		return fmt.Errorf("WritePacket failed: %w", err)
	}
	return nil
}

func (p *Player) sendPlayerAbilities() error {
	packet := protocol.NewPacket(0x39) // Player Abilities packet

	flags := byte(0)
	if p.gameMode == Creative {
		flags |= 0x01 // Invulnerable
		flags |= 0x02 // Flying
		flags |= 0x04 // Allow Flying
		flags |= 0x08 // Creative Mode
	}

	_ = packet.WriteByte(flags) // Flags
	packet.WriteFloat(0.05)     // Flying speed
	packet.WriteFloat(0.1)      // Walking speed

	if err := p.client.WritePacket(packet); err != nil {
		return fmt.Errorf("WritePacket failed: %w", err)
	}
	return nil
}

func (p *Player) sendPlayerPositionAndLook() error {
	packet := protocol.NewPacket(0x08) // Player Position and Look packet
	packet.WriteDouble(0.5)            // X (center of block)
	packet.WriteDouble(65.0)           // Y (feet position, 1 block above ground at y=64)
	packet.WriteDouble(0.5)            // Z (center of block)
	packet.WriteFloat(0.0)             // Yaw
	packet.WriteFloat(0.0)             // Pitch
	_ = packet.WriteByte(0)            // Flags (all absolute, no relative)

	if err := p.client.WritePacket(packet); err != nil {
		return fmt.Errorf("WritePacket failed: %w", err)
	}

	// Update player position
	p.mu.Lock()
	p.x = 0.5
	p.y = 65.0
	p.z = 0.5
	p.yaw = 0.0
	p.pitch = 0.0
	p.mu.Unlock()

	return nil
}

func (p *Player) sendGameModeChange() error {
	packet := protocol.NewPacket(0x2B)
	_ = packet.WriteByte(3)
	packet.WriteFloat(float32(p.gameMode))

	return p.client.WritePacket(packet)
}

func (p *Player) sendKeepAlive() error {
	packet := protocol.NewPacket(0x00)
	packet.WriteVarInt(int32(time.Now().Unix()))

	return p.client.WritePacket(packet)
}

func (p *Player) SendMessage(message string) {
	packet := protocol.NewPacket(0x02)
	packet.WriteString(fmt.Sprintf(`{"text":"%s"}`, message))
	_ = packet.WriteByte(0)

	p.client.WritePacket(packet)
}

func (p *Player) Disconnect(reason string) {
	p.client.SendDisconnect(reason)
	p.client.Disconnect()
}

func (p *Player) Teleport(x, y, z float64) {
    // Fire cancellable teleport event
    p.mu.RLock()
    oldX, oldY, oldZ := p.x, p.y, p.z
    oldYaw, oldPitch := p.yaw, p.pitch
    bus := p.eventBus
    p.mu.RUnlock()

    if bus != nil {
        e := &event.PlayerTeleportEvent{
            Player: p,
            From:   event.Location{World: "world", X: oldX, Y: oldY, Z: oldZ, Yaw: oldYaw, Pitch: oldPitch},
            To:     event.Location{World: "world", X: x, Y: y, Z: z, Yaw: oldYaw, Pitch: oldPitch},
            Cause:  event.TeleportCausePlugin,
        }
        bus.Fire(e)
        if e.IsCancelled() {
            return
        }
    }

    p.SetPosition(x, y, z)
    _ = p.sendPlayerPositionAndLook()
}

func (p *Player) sendInitialChunks() error {
    // Send chunks in a 3x3 area around spawn (view distance of 1)
    playerChunkX := int32(0) // Spawn at 0,0
    playerChunkZ := int32(0)

	for x := playerChunkX - 1; x <= playerChunkX+1; x++ {
		for z := playerChunkZ - 1; z <= playerChunkZ+1; z++ {
            if err := p.sendChunk(x, z); err != nil {
                return fmt.Errorf("failed to send chunk %d,%d: %w", x, z, err)
            }
        }
    }

	return nil
}

func (p *Player) sendChunk(chunkX, chunkZ int32) error {
    // Ask plugins to generate chunk data first
    var data []byte
    var primaryBitMask int32
    p.mu.RLock()
    bus := p.eventBus
    p.mu.RUnlock()

    if bus != nil {
        e := &event.ChunkGenerateEvent{ChunkX: chunkX, ChunkZ: chunkZ, World: "world"}
        bus.Fire(e)
        if len(e.Data) > 0 {
            data = e.Data
            // Primary bitmask is not transported in event; assume full (0xFFFF) when provided
            primaryBitMask = 0xFFFF
        }
        // Also notify chunk load
        bus.Fire(&event.ChunkLoadEvent{ChunkX: chunkX, ChunkZ: chunkZ, World: "world", IsNew: true})
    }

    if data == nil {
        // Fallback to empty chunk
        primaryBitMask, data = protocol.CreateEmptyChunkData()
    }

    fmt.Printf("DEBUG: Sending chunk %d,%d (data size: %d bytes)\n", chunkX, chunkZ, len(data))

    // Create the chunk packet
    packet := protocol.CreateChunkDataPacket(chunkX, chunkZ, true, primaryBitMask, data)

    if err := p.client.WritePacket(packet); err != nil {
        return fmt.Errorf("failed to send chunk packet: %w", err)
    }

    fmt.Printf("DEBUG: Successfully sent chunk %d,%d\n", chunkX, chunkZ)
    return nil
}

func (p *Player) HasPermission(permission string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, perm := range p.permissions {
		if perm == permission || perm == "*" {
			return true
		}
	}

	return false
}

func (p *Player) AddPermission(permission string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.permissions = append(p.permissions, permission)
}

func (p *Player) GetHealth() float32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.health
}

func (p *Player) SetHealth(health float32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.health = health
}

func (p *Player) GetFood() int32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.food
}

func (p *Player) SetFood(food int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.food = food
}

func (p *Player) IsOnGround() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.onGround
}

func (p *Player) SendTitle(title, subtitle string, fadeIn, stay, fadeOut int) {
	// TODO: Implement title sending
}

func (p *Player) SendActionBar(message string) {
	// TODO: Implement action bar
}

func (p *Player) RemovePermission(permission string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	filtered := p.permissions[:0]
	for _, perm := range p.permissions {
		if perm != permission {
			filtered = append(filtered, perm)
		}
	}
	p.permissions = filtered
}

func (p *Player) GetWorld() types.World {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.world
}

func (p *Player) SetWorld(world types.World) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.world = world
}

func (p *Player) SendChunk(chunkX, chunkZ int32) {
	_ = p.sendChunk(chunkX, chunkZ)
}

func (p *Player) GetInventory() types.Inventory {
	// TODO: Implement inventory
	return nil
}

func (p *Player) Kick(reason string) {
	p.Disconnect(reason)
}

func (p *Player) GetPing() int64 {
	// TODO: Calculate actual ping
	return 0
}

func (p *Player) IsOnline() bool {
	return p.client != nil
}

func (p *Player) triggerMoveEvent(oldX, oldY, oldZ, newX, newY, newZ float64, oldYaw, oldPitch, newYaw, newPitch float32) {
	p.mu.RLock()
	bus := p.eventBus
	p.mu.RUnlock()

	if bus != nil {
		moveEvent := &event.PlayerMoveEvent{
			Player: p,
			From: event.Location{
				World: "world", // TODO: Get from player's world
				X:     oldX,
				Y:     oldY,
				Z:     oldZ,
				Yaw:   oldYaw,
				Pitch: oldPitch,
			},
			To: event.Location{
				World: "world", // TODO: Get from player's world
				X:     newX,
				Y:     newY,
				Z:     newZ,
				Yaw:   newYaw,
				Pitch: newPitch,
			},
		}
        bus.Fire(moveEvent)
        if moveEvent.IsCancelled() {
            // Revert to old position and inform client
            p.mu.Lock()
            p.x, p.y, p.z = oldX, oldY, oldZ
            p.yaw, p.pitch = oldYaw, oldPitch
            p.mu.Unlock()
            _ = p.sendPlayerPositionAndLook()
        }
    }
}

// decodeBlockPosition decodes a 1.8.9 packed block position (X:26, Y:12, Z:26)
func decodeBlockPosition(val int64) (int32, int32, int32) {
    x := int32(val >> 38)
    y := int32((val >> 26) & 0xFFF)
    z := int32(val << 38 >> 38)
    // Sign correction for 26-bit x and z
    if x >= 1<<25 { x -= 1 << 26 }
    if z >= 1<<25 { z -= 1 << 26 }
    if y >= 1<<11 { y -= 1 << 12 }
    return x, y, z
}
