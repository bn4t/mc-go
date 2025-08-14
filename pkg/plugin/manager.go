package plugin

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mc-go/server/pkg/player"
	"go.uber.org/zap"
)

type Manager struct {
	logger        *zap.Logger
	plugins       map[string]*LoadedPlugin
	commands      map[string]CommandHandler
	eventHandlers map[EventType][]EventHandler
	mu            sync.RWMutex
}

func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		logger:        logger,
		plugins:       make(map[string]*LoadedPlugin),
		commands:      make(map[string]CommandHandler),
		eventHandlers: make(map[EventType][]EventHandler),
	}
}

func (m *Manager) RegisterPlugin(p Plugin) error {
	if p == nil {
		return fmt.Errorf("plugin is nil")
	}
	
	loadedPlugin := NewLoadedPlugin(p)
	if loadedPlugin == nil {
		return fmt.Errorf("failed to create loaded plugin")
	}
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	name := p.Name()
	
	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("plugin %s is already registered", name)
	}
	
	m.plugins[name] = loadedPlugin
	
	if err := m.enablePlugin(loadedPlugin); err != nil {
		delete(m.plugins, name)
		return fmt.Errorf("failed to enable plugin %s: %w", name, err)
	}
	
	m.logger.Info("Registered plugin", 
		zap.String("name", name),
		zap.String("version", p.Version()),
		zap.String("author", p.Author()))
	
	return nil
}

func (m *Manager) LoadPlugins(plugins []Plugin) error {
	for _, p := range plugins {
		if err := m.RegisterPlugin(p); err != nil {
			m.logger.Error("Failed to register plugin", zap.Error(err))
		}
	}
	return nil
}

func (m *Manager) enablePlugin(loadedPlugin *LoadedPlugin) error {
	if err := loadedPlugin.Plugin.OnEnable(); err != nil {
		return err
	}
	
	for cmdName, handler := range loadedPlugin.Commands {
		if _, exists := m.commands[cmdName]; exists {
			m.logger.Warn("Command already registered", zap.String("command", cmdName))
			continue
		}
		m.commands[cmdName] = handler
	}
	
	for eventType, handler := range loadedPlugin.EventHandlers {
		m.eventHandlers[eventType] = append(m.eventHandlers[eventType], handler)
	}
	
	loadedPlugin.Enabled = true
	
	return nil
}

func (m *Manager) UnloadPlugin(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	loadedPlugin, exists := m.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s is not loaded", name)
	}
	
	if loadedPlugin.Enabled {
		if err := m.disablePlugin(loadedPlugin); err != nil {
			return fmt.Errorf("failed to disable plugin %s: %w", name, err)
		}
	}
	
	delete(m.plugins, name)
	
	m.logger.Info("Unloaded plugin", zap.String("name", name))
	
	return nil
}

func (m *Manager) disablePlugin(loadedPlugin *LoadedPlugin) error {
	if err := loadedPlugin.Plugin.OnDisable(); err != nil {
		return err
	}
	
	for cmdName := range loadedPlugin.Commands {
		delete(m.commands, cmdName)
	}
	
	for eventType, handler := range loadedPlugin.EventHandlers {
		handlers := m.eventHandlers[eventType]
		for i, h := range handlers {
			if fmt.Sprintf("%p", h) == fmt.Sprintf("%p", handler) {
				m.eventHandlers[eventType] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}
	
	loadedPlugin.Enabled = false
	
	return nil
}

func (m *Manager) UnloadAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	for name, loadedPlugin := range m.plugins {
		if loadedPlugin.Enabled {
			if err := m.disablePlugin(loadedPlugin); err != nil {
				m.logger.Error("Failed to disable plugin", zap.String("name", name), zap.Error(err))
			}
		}
	}
	
	m.plugins = make(map[string]*LoadedPlugin)
	m.commands = make(map[string]CommandHandler)
	m.eventHandlers = make(map[EventType][]EventHandler)
}

func (m *Manager) GetPlugin(name string) *LoadedPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return m.plugins[name]
}

func (m *Manager) GetAllPlugins() []*LoadedPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	plugins := make([]*LoadedPlugin, 0, len(m.plugins))
	for _, plugin := range m.plugins {
		plugins = append(plugins, plugin)
	}
	
	return plugins
}

func (m *Manager) ExecuteCommand(sender *player.Player, command string) error {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}
	
	cmdName := strings.ToLower(parts[0])
	args := parts[1:]
	
	m.mu.RLock()
	handler, exists := m.commands[cmdName]
	m.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("Unknown command: /%s", cmdName)
	}
	
	return handler(sender, args)
}

func (m *Manager) TriggerEvent(eventType EventType, data interface{}) {
	event := NewEvent(eventType, data)
	
	m.mu.RLock()
	handlers := make([]EventHandler, len(m.eventHandlers[eventType]))
	copy(handlers, m.eventHandlers[eventType])
	m.mu.RUnlock()
	
	for _, handler := range handlers {
		if event.IsCancelled() {
			break
		}
		
		if err := handler(event); err != nil {
			m.logger.Error("Event handler error", 
				zap.Int("eventType", int(eventType)),
				zap.Error(err))
		}
	}
}

func (m *Manager) Tick() {
	m.mu.RLock()
	plugins := make([]*LoadedPlugin, 0, len(m.plugins))
	for _, plugin := range m.plugins {
		if plugin.Enabled {
			plugins = append(plugins, plugin)
		}
	}
	m.mu.RUnlock()
	
	for _, plugin := range plugins {
		plugin.Plugin.OnTick()
	}
}

func (m *Manager) ReloadPlugin(name string, newPlugin Plugin) error {
	if err := m.UnloadPlugin(name); err != nil {
		return fmt.Errorf("failed to unload plugin: %w", err)
	}
	
	if err := m.RegisterPlugin(newPlugin); err != nil {
		return fmt.Errorf("failed to reload plugin: %w", err)
	}
	
	return nil
}