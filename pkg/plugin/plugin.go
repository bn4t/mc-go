package plugin

import "github.com/mc-go/server/pkg/player"

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

type CommandHandler func(sender *player.Player, args []string) error

type EventHandler func(event Event) error

type EventType int

const (
	EventServerStart EventType = iota
	EventServerStop
	EventPlayerJoin
	EventPlayerQuit
	EventPlayerChat
	EventPlayerMove
	EventBlockBreak
	EventBlockPlace
	EventPlayerDeath
	EventPlayerRespawn
	EventWorldLoad
	EventWorldUnload
	EventWorldSave
	EventChunkLoad
	EventChunkUnload
	EventEntitySpawn
	EventEntityDespawn
	EventPlayerCommand
	EventWeatherChange
	EventTimeChange
)

type Event interface {
	Type() EventType
	Data() interface{}
	IsCancelled() bool
	SetCancelled(bool)
}

type BaseEvent struct {
	eventType EventType
	data      interface{}
	cancelled bool
}

func NewEvent(eventType EventType, data interface{}) Event {
	return &BaseEvent{
		eventType: eventType,
		data:      data,
		cancelled: false,
	}
}

func (e *BaseEvent) Type() EventType {
	return e.eventType
}

func (e *BaseEvent) Data() interface{} {
	return e.data
}

func (e *BaseEvent) IsCancelled() bool {
	return e.cancelled
}

func (e *BaseEvent) SetCancelled(cancelled bool) {
	e.cancelled = cancelled
}

type LoadedPlugin struct {
	Plugin        Plugin
	Name          string
	Enabled       bool
	Commands      map[string]CommandHandler
	EventHandlers map[EventType]EventHandler
}

func NewLoadedPlugin(p Plugin) *LoadedPlugin {
	if p == nil {
		return nil
	}

	return &LoadedPlugin{
		Plugin:        p,
		Name:          p.Name(),
		Enabled:       false,
		Commands:      p.RegisterCommands(),
		EventHandlers: p.RegisterEventHandlers(),
	}
}

type BasePlugin struct {
	name        string
	version     string
	author      string
	description string
}

func NewBasePlugin(name, version, author, description string) *BasePlugin {
	return &BasePlugin{
		name:        name,
		version:     version,
		author:      author,
		description: description,
	}
}

func (p *BasePlugin) Name() string {
	return p.name
}

func (p *BasePlugin) Version() string {
	return p.version
}

func (p *BasePlugin) Author() string {
	return p.author
}

func (p *BasePlugin) Description() string {
	return p.description
}

func (p *BasePlugin) OnEnable() error {
	return nil
}

func (p *BasePlugin) OnDisable() error {
	return nil
}

func (p *BasePlugin) OnTick() {
}

func (p *BasePlugin) RegisterCommands() map[string]CommandHandler {
	return make(map[string]CommandHandler)
}

func (p *BasePlugin) RegisterEventHandlers() map[EventType]EventHandler {
	return make(map[EventType]EventHandler)
}
