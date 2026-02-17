package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	HTTP       HTTPConfig                 `json:"http"`
	Database   string                     `json:"database"`
	Plugins    map[string]json.RawMessage `json:"plugins"`
	Routes     []RouteConfig              `json:"routes"`
	Supervisor SupervisorConfig           `json:"supervisor"`
}

type HTTPConfig struct {
	Address string `json:"address"`
}

type RouteConfig struct {
	Name     string       `json:"name"`
	Source   string       `json:"source"`
	Event    string       `json:"event"`
	Pipeline []StepConfig `json:"pipeline"`
	Sink     SinkConfig   `json:"sink"`
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

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg := &Config{
		HTTP:     HTTPConfig{Address: "127.0.0.1:8080"},
		Database: "smoothbrain.db",
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
