package network

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mc-go/server/pkg/event"
	"github.com/mc-go/server/pkg/protocol"
	"github.com/mc-go/server/pkg/types"
)

type ConnectionState int

const (
	StateHandshaking ConnectionState = iota
	StateStatus
	StateLogin
	StatePlay
)

type Client struct {
	conn     net.Conn
	state    ConnectionState
	manager  *Manager
	reader   *protocol.PacketReader
	writer   *protocol.PacketWriter
	mu       sync.RWMutex
	username string
	uuid     uuid.UUID
	protocol int32
	// Event context (set after player is created)
	eventBus event.EventBus
	player   types.Player
}

func NewClient(conn net.Conn, manager *Manager) *Client {
	return &Client{
		conn:    conn,
		state:   StateHandshaking,
		manager: manager,
		reader:  protocol.NewPacketReader(conn),
		writer:  protocol.NewPacketWriter(conn),
	}
}

func (c *Client) HandleHandshake() error {
	packet, err := c.reader.ReadPacket()
	if err != nil {
		return fmt.Errorf("failed to read handshake packet: %w", err)
	}
	
	if packet.ID != 0x00 {
		return fmt.Errorf("expected handshake packet (0x00), got 0x%02X", packet.ID)
	}
	
	protocolVersion, err := packet.ReadVarInt()
	if err != nil {
		return fmt.Errorf("failed to read protocol version: %w", err)
	}
	
	serverAddress, err := packet.ReadString()
	if err != nil {
		return fmt.Errorf("failed to read server address: %w", err)
	}
	
	serverPort, err := packet.ReadUShort()
	if err != nil {
		return fmt.Errorf("failed to read server port: %w", err)
	}
	
	nextState, err := packet.ReadVarInt()
	if err != nil {
		return fmt.Errorf("failed to read next state: %w", err)
	}
	
	c.mu.Lock()
	c.protocol = protocolVersion
	
	switch nextState {
	case 1:
		c.state = StateStatus
	case 2:
		c.state = StateLogin
	default:
		c.mu.Unlock()
		return fmt.Errorf("invalid next state: %d", nextState)
	}
	c.mu.Unlock()
	
	_ = serverAddress
	_ = serverPort
	
	return nil
}

func (c *Client) HandleLogin(onlineMode bool) (string, uuid.UUID, error) {
	// Set timeout for login packet
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	
	packet, err := c.reader.ReadPacket()
	if err != nil {
		return "", uuid.UUID{}, fmt.Errorf("failed to read login start: %w", err)
	}
	
	if packet.ID != 0x00 {
		return "", uuid.UUID{}, fmt.Errorf("expected login start packet (0x00), got 0x%02X", packet.ID)
	}
	
	username, err := packet.ReadString()
	if err != nil {
		return "", uuid.UUID{}, err
	}
	
	c.mu.Lock()
	c.username = username
	c.mu.Unlock()
	
	var playerUUID uuid.UUID
	if onlineMode {
		if err := c.enableEncryption(); err != nil {
			return "", uuid.UUID{}, err
		}
		playerUUID, err = c.authenticatePlayer()
		if err != nil {
			return "", uuid.UUID{}, err
		}
	} else {
		playerUUID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("OfflinePlayer:"+username))
	}
	
	c.mu.Lock()
	c.uuid = playerUUID
	c.mu.Unlock()
	
	if err := c.sendLoginSuccess(username, playerUUID); err != nil {
		return "", uuid.UUID{}, err
	}
	
	c.mu.Lock()
	c.state = StatePlay
	c.mu.Unlock()
	
	return username, playerUUID, nil
}

func (c *Client) enableEncryption() error {
	return nil
}

func (c *Client) authenticatePlayer() (uuid.UUID, error) {
	return uuid.New(), nil
}

func (c *Client) sendLoginSuccess(username string, playerUUID uuid.UUID) error {
	// Send set compression packet first if compression is enabled
	threshold := c.manager.GetCompressionThreshold()
	if threshold >= 0 {
		compressionPacket := protocol.NewPacket(0x03) // Set Compression packet
		compressionPacket.WriteVarInt(int32(threshold))
		if err := c.writer.WritePacket(compressionPacket); err != nil {
			return fmt.Errorf("failed to send compression packet: %w", err)
		}
		
		// Enable compression after sending the packet
		c.writer.SetCompression(true, threshold)
		c.reader.SetCompression(true)
	}
	
	// Send login success
	packet := protocol.NewPacket(0x02)
	packet.WriteString(playerUUID.String())
	packet.WriteString(username)
	
	if err := c.writer.WritePacket(packet); err != nil {
		return fmt.Errorf("failed to send login success: %w", err)
	}
	
	return nil
}

func (c *Client) SendStatus(status map[string]interface{}) error {
	// Set a timeout for reading the status request
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	packet, err := c.reader.ReadPacket()
	if err != nil {
		return fmt.Errorf("failed to read status request: %w", err)
	}
	
	if packet.ID != 0x00 {
		return fmt.Errorf("expected status request packet (0x00), got 0x%02X", packet.ID)
	}
	
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}
	
	response := protocol.NewPacket(0x00)
	response.WriteString(string(statusJSON))
	
	if err := c.writer.WritePacket(response); err != nil {
		return err
	}
	
	// Set timeout for ping packet
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	pingPacket, err := c.reader.ReadPacket()
	if err != nil {
		return fmt.Errorf("failed to read ping packet: %w", err)
	}
	
	if pingPacket.ID != 0x01 {
		return fmt.Errorf("expected ping packet (0x01), got 0x%02X", pingPacket.ID)
	}
	
	payload, err := pingPacket.ReadLong()
	if err != nil {
		return err
	}
	
	pongPacket := protocol.NewPacket(0x01)
	pongPacket.WriteLong(payload)
	
	return c.writer.WritePacket(pongPacket)
}

func (c *Client) SendDisconnect(reason string) {
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	
	var packetID byte
	switch state {
	case StateLogin:
		packetID = 0x00
	case StatePlay:
		packetID = 0x40
	default:
		return
	}
	
	packet := protocol.NewPacket(packetID)
	packet.WriteString(fmt.Sprintf(`{"text":"%s"}`, reason))
	
	_ = c.writer.WritePacket(packet)
}

func (c *Client) Disconnect() {
	c.conn.Close()
}

func (c *Client) State() ConnectionState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *Client) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *Client) ReadPacket() (*protocol.Packet, error) {
	return c.reader.ReadPacket()
}

func (c *Client) WritePacket(packet *protocol.Packet) error {
	// Fire PacketSendEvent if we have context; allow cancellation
	if c.eventBus != nil && c.player != nil {
		e := &event.PacketSendEvent{Player: c.player, Packet: packet}
		c.eventBus.Fire(e)
		if e.IsCancelled() {
			return nil
		}
	}
	return c.writer.WritePacket(packet)
}

// SetEventContext attaches event bus and player for packet send/receive events
func (c *Client) SetEventContext(bus event.EventBus, p types.Player) {
	c.mu.Lock()
	c.eventBus = bus
	c.player = p
	c.mu.Unlock()
}
