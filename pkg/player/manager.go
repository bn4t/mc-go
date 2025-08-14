package player

import (
	"sync"

	"github.com/google/uuid"
	"github.com/mc-go/server/pkg/network"
)

type Manager struct {
	players    map[uuid.UUID]*Player
	maxPlayers int
	entityID   int32
	mu         sync.RWMutex
}

func NewManager(maxPlayers int) *Manager {
	return &Manager{
		players:    make(map[uuid.UUID]*Player),
		maxPlayers: maxPlayers,
		entityID:   1,
	}
}

func (m *Manager) CreatePlayer(username string, playerUUID uuid.UUID, client *network.Client) *Player {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if len(m.players) >= m.maxPlayers {
		return nil
	}
	
	if _, exists := m.players[playerUUID]; exists {
		return nil
	}
	
	player := NewPlayer(username, playerUUID, client, m.entityID)
	m.entityID++
	
	m.players[playerUUID] = player
	
	return player
}

func (m *Manager) RemovePlayer(playerUUID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	delete(m.players, playerUUID)
}

func (m *Manager) GetByUUID(playerUUID uuid.UUID) *Player {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return m.players[playerUUID]
}

func (m *Manager) GetByName(name string) *Player {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	for _, player := range m.players {
		if player.GetUsername() == name {
			return player
		}
	}
	
	return nil
}

func (m *Manager) GetAll() []*Player {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	players := make([]*Player, 0, len(m.players))
	for _, player := range m.players {
		players = append(players, player)
	}
	
	return players
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return len(m.players)
}

func (m *Manager) GetSample() []map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	sample := make([]map[string]string, 0, 5)
	count := 0
	
	for _, player := range m.players {
		if count >= 5 {
			break
		}
		
		sample = append(sample, map[string]string{
			"name": player.GetUsername(),
			"id":   player.GetUUID().String(),
		})
		count++
	}
	
	return sample
}

func (m *Manager) BroadcastMessage(message string) {
	m.mu.RLock()
	players := make([]*Player, 0, len(m.players))
	for _, player := range m.players {
		players = append(players, player)
	}
	m.mu.RUnlock()
	
	for _, player := range players {
		player.SendMessage(message)
	}
}

func (m *Manager) DisconnectAll(reason string) {
	m.mu.RLock()
	players := make([]*Player, 0, len(m.players))
	for _, player := range m.players {
		players = append(players, player)
	}
	m.mu.RUnlock()
	
	for _, player := range players {
		player.Disconnect(reason)
	}
}

func (m *Manager) Tick() {
	
}