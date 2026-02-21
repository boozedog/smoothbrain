package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func DefaultStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "smoothbrain"), nil
}

type Config struct {
	HTTP       HTTPConfig                 `json:"http"`
	Database   string                     `json:"database"`
	LogLevel   string                     `json:"log_level"`
	Auth       AuthConfig                 `json:"auth"`
	Plugins    map[string]json.RawMessage `json:"plugins"`
	Routes     []RouteConfig              `json:"routes"`
	Supervisor SupervisorConfig           `json:"supervisor"`
	Tailscale  TailscaleConfig            `json:"tailscale"`
}

type AuthConfig struct {
	RPDisplayName   string        `json:"rp_display_name"`
	RPID            string        `json:"rp_id"`
	RPOrigins       []string      `json:"rp_origins"`
	SessionDuration time.Duration `json:"session_duration"`
}

type HTTPConfig struct {
	Address string `json:"address"`
}

type RouteConfig struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Source      string       `json:"source"`
	Event       string       `json:"event"`
	Timeout     string       `json:"timeout,omitempty"` // Go duration string, default "30s"
	Pipeline    []StepConfig `json:"pipeline"`
	Sink        SinkConfig   `json:"sink"`
}

type StepConfig struct {
	Plugin string         `json:"plugin"`
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

type SinkConfig struct {
	Plugin string         `json:"plugin"`
	Params map[string]any `json:"params"`
}

type SupervisorConfig struct {
	Tasks []SupervisorTask `json:"tasks"`
}

type SupervisorTask struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Plugin   string `json:"plugin"`
}

type TailscaleConfig struct {
	Enabled     bool   `json:"enabled"`
	Hostname    string `json:"hostname"`
	ServiceName string `json:"service_name"`
	AuthKey     string `json:"auth_key"`
	StateDir    string `json:"state_dir"`
	Ephemeral   bool   `json:"ephemeral"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	// Expand $VAR and ${VAR} references with environment variables.
	data = []byte(os.ExpandEnv(string(data)))

	tsnetStateDir := "data/tsnet"
	if dir, err := DefaultStateDir(); err == nil {
		tsnetStateDir = filepath.Join(dir, "tsnet")
	}

	cfg := &Config{
		HTTP:     HTTPConfig{Address: "127.0.0.1:8080"},
		Database: "smoothbrain.db",
		Tailscale: TailscaleConfig{
			Hostname:    "smoothbrain",
			ServiceName: "svc:smoothbrain",
			StateDir:    tsnetStateDir,
		},
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.HTTP.Address == "" {
		return fmt.Errorf("config: http.address must not be empty")
	}
	if c.Database == "" {
		return fmt.Errorf("config: database path must not be empty")
	}
	if c.Tailscale.Enabled && c.Tailscale.ServiceName == "" {
		return fmt.Errorf("config: tailscale.service_name must not be empty when enabled")
	}
	seen := make(map[string]bool)
	for i, r := range c.Routes {
		if r.Name == "" {
			return fmt.Errorf("config: routes[%d].name must not be empty", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("config: duplicate route name %q", r.Name)
		}
		seen[r.Name] = true
		if r.Source == "" {
			return fmt.Errorf("config: route %q: source must not be empty", r.Name)
		}
		if r.Sink.Plugin == "" {
			return fmt.Errorf("config: route %q: sink.plugin must not be empty", r.Name)
		}
	}
	return nil
}
