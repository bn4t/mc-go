package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the minimal runtime configuration used by this lean server.
// It intentionally focuses on network and identity knobs only.
type Config struct {
	ListenAddr   string        `yaml:"listen_addr"` // e.g. ":25565"
	MOTD         string        `yaml:"motd"`
	MaxPlayers   int           `yaml:"max_players"`
	Debug        bool          `yaml:"debug"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// Protocol version pin (e.g., 1.21.1). Currently informational.
	Protocol          string        `yaml:"protocol"`
	KeepAliveInterval time.Duration `yaml:"keepalive_interval"`
}

func defaultConfig() *Config {
	return &Config{
		ListenAddr:        ":25565",
		MOTD:              "mc-go experimental",
		MaxPlayers:        20,
		Debug:             false,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		Protocol:          "1.21.1",
		KeepAliveInterval: 15 * time.Second,
	}
}

// LoadConfig loads YAML from path. If the file is missing, a default config is
// written to that location and returned.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			cfg := defaultConfig()
			if err := writeDefault(path, cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func writeDefault(path string, cfg *Config) error {
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	return nil
}
