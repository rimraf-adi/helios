package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration options
type Config struct {
	Network  NetworkConfig  `yaml:"network"`
	Transfer TransferConfig `yaml:"transfer"`
	TUI      TUIConfig      `yaml:"tui"`
}

// NetworkConfig holds network-related settings
type NetworkConfig struct {
	MaxStreams         int           `yaml:"max_streams"`
	ChunkSizeMB        int           `yaml:"chunk_size_mb"`
	TimeoutSeconds     int           `yaml:"timeout_seconds"`
	KeepAliveSeconds   int           `yaml:"keep_alive_seconds"`
	MaxIdleSeconds     int           `yaml:"max_idle_seconds"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	HandshakeTimeout   time.Duration `yaml:"-"`
}

// TransferConfig holds transfer-related settings
type TransferConfig struct {
	Compression      string `yaml:"compression"` // "auto", "always", "never"
	BandwidthLimitMB int    `yaml:"bandwidth_limit_mb"`
	MaxRetries       int    `yaml:"max_retries"`
	VerifyChunks     bool   `yaml:"verify_chunks"`
	OutputDir        string `yaml:"output_dir"`
}

// TUIConfig holds TUI-related settings
type TUIConfig struct {
	Enabled       bool `yaml:"enabled"`
	RefreshRateMs int  `yaml:"refresh_rate_ms"`
	ShowStreams   bool `yaml:"show_streams"`
}

// Default returns default configuration
func Default() *Config {
	return &Config{
		Network: NetworkConfig{
			MaxStreams:         16,
			ChunkSizeMB:        8,
			TimeoutSeconds:     30,
			KeepAliveSeconds:   30,
			MaxIdleSeconds:     300,
			InsecureSkipVerify: true,
			HandshakeTimeout:   30 * time.Second,
		},
		Transfer: TransferConfig{
			Compression:      "auto",
			BandwidthLimitMB: 0,
			MaxRetries:       3,
			VerifyChunks:     true,
			OutputDir:        "./received",
		},
		TUI: TUIConfig{
			Enabled:       true,
			RefreshRateMs: 100,
			ShowStreams:   true,
		},
	}
}

// ChunkSize returns the chunk size in bytes
func (c *Config) ChunkSize() int64 {
	return int64(c.Network.ChunkSizeMB) * 1024 * 1024
}

// Load reads configuration from a YAML file
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save writes configuration to a YAML file
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
