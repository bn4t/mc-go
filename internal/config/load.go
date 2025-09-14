package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Default returns a Config populated with sensible defaults.
func Default() *Config {
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

// Load reads YAML from path and overlays onto defaults. If the file does not
// exist, a default config is written to that location and returned.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			cfg := Default()
			if err := writeDefault(path, cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
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
