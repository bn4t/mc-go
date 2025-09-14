package config

import "time"

// Config is the runtime configuration for the server process.
// It is shared across subsystems; keep it declarative.
type Config struct {
	ListenAddr        string        `yaml:"listen_addr"`
	MOTD              string        `yaml:"motd"`
	MaxPlayers        int           `yaml:"max_players"`
	Debug             bool          `yaml:"debug"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	Protocol          string        `yaml:"protocol"`
	KeepAliveInterval time.Duration `yaml:"keepalive_interval"`
}
