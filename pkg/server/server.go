package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mc-go/server/pkg/config"
	"github.com/mc-go/server/pkg/network"
	"github.com/mc-go/server/pkg/player"
	"github.com/mc-go/server/pkg/plugin"
	"github.com/mc-go/server/pkg/world"
	"go.uber.org/zap"
)

type Server struct {
	config      *config.ServerConfig
	listener    net.Listener
	logger      *zap.Logger
	running     bool
	mu          sync.RWMutex
	players     *player.Manager
	worldMgr    *world.Manager
	networkMgr  *network.Manager
	pluginMgr   *plugin.Manager
	ctx         context.Context
	cancel      context.CancelFunc
	tickRate    time.Duration
}

func New(cfg *config.ServerConfig) (*Server, error) {
	var logger *zap.Logger
	var err error
	
	if cfg.Debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	s := &Server{
		config:   cfg,
		logger:   logger,
		tickRate: time.Second / 20,
		ctx:      ctx,
		cancel:   cancel,
	}
	
	s.players = player.NewManager(cfg.MaxPlayers)
	s.worldMgr = world.NewManager(cfg.WorldSettings)
	s.networkMgr = network.NewManager(s.logger)
	s.pluginMgr = plugin.NewManager(s.logger)
	
	return s, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.running {
		return fmt.Errorf("server already running")
	}
	
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.config.Host, s.config.Port))
	if err != nil {
		return fmt.Errorf("failed to bind to %s:%d: %w", s.config.Host, s.config.Port, err)
	}
	
	s.listener = listener
	s.running = true
	
	// Register compiled-in plugins here
	plugins := []plugin.Plugin{
		plugin.NewExamplePlugin(),
	}
	
	if err := s.pluginMgr.LoadPlugins(plugins); err != nil {
		s.logger.Warn("Failed to load plugins", zap.Error(err))
	}
	
	s.pluginMgr.TriggerEvent(plugin.EventServerStart, nil)
	
	if err := s.worldMgr.LoadWorlds(); err != nil {
		return fmt.Errorf("failed to load worlds: %w", err)
	}
	
	go s.acceptConnections()
	go s.gameTick()
	
	s.logger.Info("Server started", zap.String("host", s.config.Host), zap.Int("port", s.config.Port))
	s.logger.Info("Running Minecraft 1.8.9 protocol", zap.Int("version", 47))
	
	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if !s.running {
		return nil
	}
	
	s.logger.Info("Shutting down server")
	
	s.pluginMgr.TriggerEvent(plugin.EventServerStop, nil)
	
	s.running = false
	s.cancel()
	
	if s.listener != nil {
		s.listener.Close()
	}
	
	s.players.DisconnectAll("Server shutting down")
	
	if err := s.worldMgr.SaveAll(); err != nil {
		s.logger.Error("Failed to save worlds", zap.Error(err))
	}
	
	s.pluginMgr.UnloadAll()
	
	return nil
}

func (s *Server) acceptConnections() {
	for s.running {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running {
				s.logger.Error("Failed to accept connection", zap.Error(err))
			}
			continue
		}
		
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	s.logger.Debug("New connection", zap.String("address", conn.RemoteAddr().String()))
	
	client := network.NewClient(conn, s.networkMgr)
	
	if err := client.HandleHandshake(); err != nil {
		s.logger.Error("Handshake failed", zap.String("address", conn.RemoteAddr().String()), zap.Error(err))
		client.Disconnect()
		return
	}
	
	s.logger.Debug("Handshake complete", 
		zap.String("address", conn.RemoteAddr().String()),
		zap.Int("state", int(client.State())))
	
	switch client.State() {
	case network.StateStatus:
		s.logger.Debug("Handling status request", zap.String("address", conn.RemoteAddr().String()))
		if err := s.handleStatus(client); err != nil {
			s.logger.Error("Status handling failed", zap.Error(err))
		}
		client.Disconnect()
	case network.StateLogin:
		s.logger.Debug("Handling login", zap.String("address", conn.RemoteAddr().String()))
		s.handleLogin(client)
		// Connection is now owned by the player - don't close it here
	default:
		s.logger.Warn("Unknown connection state", zap.Int("state", int(client.State())))
		client.Disconnect()
	}
}

func (s *Server) handleStatus(client *network.Client) error {
	status := map[string]interface{}{
		"version": map[string]interface{}{
			"name":     "1.8.9",
			"protocol": 47,
		},
		"players": map[string]interface{}{
			"max":    s.config.MaxPlayers,
			"online": s.players.Count(),
			"sample": s.players.GetSample(),
		},
		"description": map[string]interface{}{
			"text": s.config.MOTD,
		},
	}
	
	return client.SendStatus(status)
}

func (s *Server) handleLogin(client *network.Client) {
	s.logger.Debug("Starting login process")
	
	username, uuid, err := client.HandleLogin(s.config.OnlineMode)
	if err != nil {
		s.logger.Error("Login failed", zap.Error(err))
		client.SendDisconnect("Login failed")
		return
	}
	
	s.logger.Debug("Login successful", zap.String("username", username), zap.String("uuid", uuid.String()))
	
	p := s.players.CreatePlayer(username, uuid, client)
	if p == nil {
		s.logger.Warn("Failed to create player", zap.String("username", username))
		client.SendDisconnect("Server is full")
		return
	}
	
	// Set the plugin manager as the command handler
	p.SetCommandHandler(s.pluginMgr)
	
	s.pluginMgr.TriggerEvent(plugin.EventPlayerJoin, p)
	
	if err := p.JoinWorld(s.worldMgr.GetDefaultWorld()); err != nil {
		s.logger.Error("Failed to join world", zap.String("username", username), zap.Error(err))
		client.SendDisconnect("Failed to join world")
		return
	}
	
	s.logger.Info("Player joined the game", zap.String("username", username), zap.String("uuid", uuid.String()))
	
	// Start handling packets in background
	go func() {
		defer func() {
			s.logger.Debug("Player disconnected", zap.String("username", username))
			client.Disconnect()
			s.players.RemovePlayer(uuid)
		}()
		
		s.logger.Debug("Starting packet handler", zap.String("username", username))
		p.HandlePackets()
		s.logger.Debug("Packet handler ended", zap.String("username", username))
	}()
}

func (s *Server) gameTick() {
	ticker := time.NewTicker(s.tickRate)
	defer ticker.Stop()
	
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Server) tick() {
	s.worldMgr.Tick()
	s.players.Tick()
	s.pluginMgr.Tick()
}

func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *Server) GetPlayer(name string) *player.Player {
	return s.players.GetByName(name)
}

func (s *Server) BroadcastMessage(message string) {
	s.players.BroadcastMessage(message)
}

func (s *Server) ExecuteCommand(sender *player.Player, command string) {
	_ = s.pluginMgr.ExecuteCommand(sender, command)
}