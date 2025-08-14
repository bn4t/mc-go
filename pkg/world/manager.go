package world

import (
	"fmt"
	"sync"

	"github.com/mc-go/server/pkg/config"
)

type Manager struct {
	worlds       map[string]*World
	defaultWorld string
	settings     *config.WorldSettings
	mu           sync.RWMutex
}

func NewManager(settings *config.WorldSettings) *Manager {
	return &Manager{
		worlds:       make(map[string]*World),
		defaultWorld: "world",
		settings:     settings,
	}
}

func (m *Manager) LoadWorlds() error {
	defaultWorld := NewWorld(m.defaultWorld)
	
	m.mu.Lock()
	m.worlds[m.defaultWorld] = defaultWorld
	m.mu.Unlock()
	
	return nil
}

func (m *Manager) CreateWorld(name string) (*World, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if _, exists := m.worlds[name]; exists {
		return nil, fmt.Errorf("world %s already exists", name)
	}
	
	world := NewWorld(name)
	m.worlds[name] = world
	
	return world, nil
}

func (m *Manager) GetWorld(name string) *World {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return m.worlds[name]
}

func (m *Manager) GetDefaultWorld() *World {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return m.worlds[m.defaultWorld]
}

func (m *Manager) SetDefaultWorld(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if _, exists := m.worlds[name]; !exists {
		return fmt.Errorf("world %s does not exist", name)
	}
	
	m.defaultWorld = name
	return nil
}

func (m *Manager) GetAllWorlds() []*World {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	worlds := make([]*World, 0, len(m.worlds))
	for _, world := range m.worlds {
		worlds = append(worlds, world)
	}
	
	return worlds
}

func (m *Manager) SaveAll() error {
	m.mu.RLock()
	worlds := make([]*World, 0, len(m.worlds))
	for _, world := range m.worlds {
		worlds = append(worlds, world)
	}
	m.mu.RUnlock()
	
	for _, world := range worlds {
		if err := world.Save(); err != nil {
			return fmt.Errorf("failed to save world %s: %w", world.GetName(), err)
		}
	}
	
	return nil
}

func (m *Manager) Tick() {
	m.mu.RLock()
	worlds := make([]*World, 0, len(m.worlds))
	for _, world := range m.worlds {
		worlds = append(worlds, world)
	}
	m.mu.RUnlock()
	
	for _, world := range worlds {
		world.Tick()
	}
}

func (m *Manager) UnloadWorld(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if name == m.defaultWorld {
		return fmt.Errorf("cannot unload default world")
	}
	
	world, exists := m.worlds[name]
	if !exists {
		return fmt.Errorf("world %s does not exist", name)
	}
	
	if err := world.Save(); err != nil {
		return fmt.Errorf("failed to save world before unloading: %w", err)
	}
	
	delete(m.worlds, name)
	
	return nil
}