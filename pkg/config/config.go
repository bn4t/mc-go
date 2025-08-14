package config

import (
	"fmt"
	"io/ioutil"
	"os"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host       string         `yaml:"host"`
	Port       int            `yaml:"port"`
	MaxPlayers int            `yaml:"max_players"`
	MOTD       string         `yaml:"motd"`
	OnlineMode bool           `yaml:"online_mode"`
	Debug      bool           `yaml:"debug"`
	LogLevel   int            `yaml:"log_level"`
	WorldSettings *WorldSettings `yaml:"world"`
	NetworkSettings *NetworkSettings `yaml:"network"`
	GameSettings *GameSettings `yaml:"game"`
}

type WorldSettings struct {
	DefaultWorld   string `yaml:"default_world"`
	WorldsDir      string `yaml:"worlds_dir"`
	ViewDistance   int    `yaml:"view_distance"`
	GenerateStructures bool `yaml:"generate_structures"`
	AllowNether    bool   `yaml:"allow_nether"`
	AllowEnd       bool   `yaml:"allow_end"`
	MaxBuildHeight int    `yaml:"max_build_height"`
	Seed           int64  `yaml:"seed"`
}

type NetworkSettings struct {
	CompressionThreshold int  `yaml:"compression_threshold"`
	ConnectionTimeout    int  `yaml:"connection_timeout"`
	KeepAliveInterval    int  `yaml:"keepalive_interval"`
	EnableQuery          bool `yaml:"enable_query"`
	QueryPort            int  `yaml:"query_port"`
	EnableRcon           bool `yaml:"enable_rcon"`
	RconPort             int  `yaml:"rcon_port"`
	RconPassword         string `yaml:"rcon_password"`
}

type GameSettings struct {
	Difficulty        string `yaml:"difficulty"`
	Gamemode          string `yaml:"gamemode"`
	Hardcore          bool   `yaml:"hardcore"`
	PVP               bool   `yaml:"pvp"`
	SpawnAnimals      bool   `yaml:"spawn_animals"`
	SpawnMonsters     bool   `yaml:"spawn_monsters"`
	SpawnNPCs         bool   `yaml:"spawn_npcs"`
	SpawnProtection   int    `yaml:"spawn_protection"`
	MaxTickTime       int64  `yaml:"max_tick_time"`
	ForceGamemode     bool   `yaml:"force_gamemode"`
	AllowFlight       bool   `yaml:"allow_flight"`
	AnnounceAdvancements bool `yaml:"announce_advancements"`
}

func DefaultConfig() *ServerConfig {
	return &ServerConfig{
		Host:       "0.0.0.0",
		Port:       25565,
		MaxPlayers: 20,
		MOTD:       "A Minecraft Server",
		OnlineMode: false,
		Debug:      false,
		LogLevel:   1,
		WorldSettings: &WorldSettings{
			DefaultWorld:       "world",
			WorldsDir:          "worlds",
			ViewDistance:       10,
			GenerateStructures: true,
			AllowNether:        true,
			AllowEnd:           true,
			MaxBuildHeight:     256,
			Seed:               0,
		},
		NetworkSettings: &NetworkSettings{
			CompressionThreshold: 256,
			ConnectionTimeout:    30,
			KeepAliveInterval:    20,
			EnableQuery:          false,
			QueryPort:            25565,
			EnableRcon:           false,
			RconPort:             25575,
			RconPassword:         "",
		},
		GameSettings: &GameSettings{
			Difficulty:        "normal",
			Gamemode:          "survival",
			Hardcore:          false,
			PVP:               true,
			SpawnAnimals:      true,
			SpawnMonsters:     true,
			SpawnNPCs:         true,
			SpawnProtection:   16,
			MaxTickTime:       60000,
			ForceGamemode:     false,
			AllowFlight:       false,
			AnnounceAdvancements: true,
		},
	}
}

func Load(path string) (*ServerConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		config := DefaultConfig()
		if err := Save(path, config); err != nil {
			return nil, fmt.Errorf("failed to save default config: %w", err)
		}
		return config, nil
	}
	
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	config := DefaultConfig()
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	
	return config, nil
}

func Save(path string, config *ServerConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	
	return nil
}

func (c *ServerConfig) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	
	if c.MaxPlayers < 1 {
		return fmt.Errorf("max_players must be at least 1")
	}
	
	if c.WorldSettings.ViewDistance < 2 || c.WorldSettings.ViewDistance > 32 {
		return fmt.Errorf("view_distance must be between 2 and 32")
	}
	
	if c.NetworkSettings.CompressionThreshold < -1 {
		return fmt.Errorf("compression_threshold must be -1 or higher")
	}
	
	validDifficulties := map[string]bool{
		"peaceful": true,
		"easy":     true,
		"normal":   true,
		"hard":     true,
	}
	
	if !validDifficulties[c.GameSettings.Difficulty] {
		return fmt.Errorf("invalid difficulty: %s", c.GameSettings.Difficulty)
	}
	
	validGamemodes := map[string]bool{
		"survival":  true,
		"creative":  true,
		"adventure": true,
		"spectator": true,
	}
	
	if !validGamemodes[c.GameSettings.Gamemode] {
		return fmt.Errorf("invalid gamemode: %s", c.GameSettings.Gamemode)
	}
	
	return nil
}