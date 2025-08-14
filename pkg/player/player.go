package player

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mc-go/server/pkg/network"
	"github.com/mc-go/server/pkg/protocol"
)

type Player struct {
	username      string
	uuid          uuid.UUID
	client        *network.Client
	entityID      int32
	x, y, z       float64
	yaw, pitch    float32
	onGround      bool
	health        float32
	food          int32
	gameMode      GameMode
	permissions   []string
	world         World
	mu            sync.RWMutex
	lastKeepAlive time.Time
	commandHandler interface {
		ExecuteCommand(sender *Player, command string) error
	}
}

type GameMode int

const (
	Survival GameMode = iota
	Creative
	Adventure
	Spectator
)

type World interface {
	GetName() string
	GetChunk(x, z int32) interface{}
	SpawnPlayer(p *Player) error
}

func NewPlayer(username string, uuid uuid.UUID, client *network.Client, entityID int32) *Player {
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
	}
}

func (p *Player) SetCommandHandler(handler interface {
	ExecuteCommand(sender *Player, command string) error
}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commandHandler = handler
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

func (p *Player) GetGameMode() GameMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.gameMode
}

func (p *Player) SetGameMode(mode int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gameMode = GameMode(mode)
	_ = p.sendGameModeChange()
}

func (p *Player) JoinWorld(world World) error {
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
		p.broadcastChatMessage(message)
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
	
	p.mu.Lock()
	p.x = x
	p.y = y
	p.z = z
	p.onGround = onGround
	p.mu.Unlock()
	
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
	
	p.mu.Lock()
	p.x = x
	p.y = y
	p.z = z
	p.yaw = yaw
	p.pitch = pitch
	p.onGround = onGround
	p.mu.Unlock()
	
	return nil
}

func (p *Player) handlePlayerDigging(packet *protocol.Packet) error {
	return nil
}

func (p *Player) handlePlayerBlockPlacement(packet *protocol.Packet) error {
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
	p.mu.RUnlock()
	
	if handler == nil {
		p.SendMessage("§cNo command handler available")
		return
	}
	
	if err := handler.ExecuteCommand(p, command); err != nil {
		p.SendMessage(fmt.Sprintf("§cError: %s", err.Error()))
	}
}

func (p *Player) broadcastChatMessage(message string) {
	formatted := fmt.Sprintf("<%s> %s", p.username, message)
	packet := protocol.NewPacket(0x02)
	packet.WriteString(fmt.Sprintf(`{"text":"%s"}`, formatted))
	_ = packet.WriteByte(0)
	
	p.client.WritePacket(packet)
}

func (p *Player) sendJoinGame() error {
	packet := protocol.NewPacket(0x01) // Join Game packet
	packet.WriteInt(p.entityID)        // Entity ID
	_ = packet.WriteByte(byte(p.gameMode)) // Gamemode
	_ = packet.WriteByte(0)                // Dimension (0 = overworld)
	_ = packet.WriteByte(0)                // Difficulty
	_ = packet.WriteByte(60)               // Max players (ignored by client)
	packet.WriteString("default")      // Level type
	packet.WriteBoolean(false)         // Reduced debug info
	
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
	
	_ = packet.WriteByte(flags)   // Flags
	packet.WriteFloat(0.05)        // Flying speed
	packet.WriteFloat(0.1)         // Walking speed
	
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
	p.SetPosition(x, y, z)
	_ = p.sendPlayerPositionAndLook()
}

func (p *Player) sendInitialChunks() error {
	// Send chunks in a 3x3 area around spawn (view distance of 1)
	playerChunkX := int32(0) // Spawn at 0,0
	playerChunkZ := int32(0)
	
	for x := playerChunkX - 1; x <= playerChunkX + 1; x++ {
		for z := playerChunkZ - 1; z <= playerChunkZ + 1; z++ {
			if err := p.sendChunk(x, z); err != nil {
				return fmt.Errorf("failed to send chunk %d,%d: %w", x, z, err)
			}
		}
	}
	
	return nil
}

func (p *Player) sendChunk(chunkX, chunkZ int32) error {
	// Create a simple flat chunk
	primaryBitMask, chunkData := protocol.CreateEmptyChunkData()
	
	fmt.Printf("DEBUG: Sending chunk %d,%d (data size: %d bytes)\n", chunkX, chunkZ, len(chunkData))
	
	// Create the chunk packet
	packet := protocol.CreateChunkDataPacket(chunkX, chunkZ, true, primaryBitMask, chunkData)
	
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