# Smoothbrain - Architecture & Implementation Plan

## Context

Smoothbrain is a personal infrastructure orchestrator: it routes events between services, runs automations, provides a unified API, and has an LLM intelligence layer. Built as a Go binary with compiled-in plugins, deployed natively on NixOS via a flake + systemd module.

## Core Concepts

- **Event bus** - in-process pub/sub. Plugins emit events, core routes them.
- **Plugins** - compiled-in Go packages. Can be sources (emit), sinks (receive), or transforms (enrich).
- **Routes** - user-defined pipelines: source event -> optional transforms -> sink. Configured in Nix.
- **Supervisor** - periodic LLM tasks (configurable cron-like schedules).

## Project Structure

```
smoothbrain/
├── flake.nix                    # Nix flake (derivation + NixOS module)
├── flake.lock
├── go.mod
├── go.sum
├── cmd/
│   └── smoothbrain/
│       └── main.go              # Entry point: load config, init plugins, start bus
├── internal/
│   ├── config/
│   │   └── config.go            # Config structs (deserialized from JSON)
│   ├── core/
│   │   ├── bus.go               # Event bus implementation
│   │   ├── router.go            # Route matching & pipeline execution
│   │   ├── supervisor.go        # Periodic LLM supervisor tasks
│   │   └── server.go            # HTTP server (webhooks + API + web UI)
│   ├── store/
│   │   └── store.go             # SQLite wrapper (events, plugin state)
│   └── plugin/
│       ├── plugin.go            # Plugin interface definitions
│       ├── registry.go          # Plugin registry
│       ├── mattermost/
│       │   └── mattermost.go
│       ├── uptimekuma/
│       │   └── uptimekuma.go
│       ├── xai/
│       │   └── xai.go
│       ├── markdown/
│       │   └── markdown.go
│       ├── observability/
│       │   └── observability.go
│       └── twitter/
│           └── twitter.go
├── web/
│   └── ...                      # Embedded static assets for web UI
└── nix/
    └── module.nix               # NixOS module
```

## Plugin Interface

```go
type Event struct {
    ID        string
    Source    string            // plugin that emitted it
    Type      string            // e.g. "alert", "message", "tweet"
    Payload   map[string]any    // arbitrary data
    Timestamp time.Time
}

type Plugin interface {
    Name() string
    Init(cfg json.RawMessage) error
    Start(ctx context.Context, bus EventBus) error
    Stop() error
}

// Optional interfaces plugins can implement:

type Sink interface {
    Plugin
    HandleEvent(ctx context.Context, event Event) error
}

type Transform interface {
    Plugin
    Transform(ctx context.Context, event Event, action string, params map[string]any) (Event, error)
}

type EventBus interface {
    Emit(event Event)
}
```

Sources just call `bus.Emit()` from within `Start()`. Sinks implement `HandleEvent()`. Transforms implement `Transform()`. A plugin can implement multiple roles.

## Route Configuration

```go
type Config struct {
    HTTP       HTTPConfig                 `json:"http"`
    Database   string                     `json:"database"`
    Plugins    map[string]json.RawMessage `json:"plugins"`
    Routes     []RouteConfig              `json:"routes"`
    Supervisor SupervisorConfig           `json:"supervisor"`
}

type RouteConfig struct {
    Name     string       `json:"name"`
    Source   string       `json:"source"`    // plugin name
    Event    string       `json:"event"`     // event type filter
    Pipeline []StepConfig `json:"pipeline"`  // transform steps
    Sink     SinkConfig   `json:"sink"`
}

type StepConfig struct {
    Plugin string         `json:"plugin"`
    Action string         `json:"action"`
    Params map[string]any `json:"params"`
}

type SinkConfig struct {
    Plugin string         `json:"plugin"`
    Params map[string]any `json:"params"`  // e.g. {"channel": "alerts"}
}
```

## Router Flow

```
1. Plugin (source) calls bus.Emit(event)
2. Router matches event against routes (source + event type)
3. For each matching route:
   a. Run pipeline steps sequentially (each transform enriches the event)
   b. Deliver final event to sink via HandleEvent()
   c. Log to SQLite
```

## Nix Integration

**flake.nix** exposes:
- `packages.default` - the Go binary
- `nixosModules.default` - the NixOS service module

**NixOS module** (`nix/module.nix`):
```nix
services.smoothbrain = {
  enable = mkEnableOption "smoothbrain orchestrator";
  http.address = mkOption { type = str; default = "127.0.0.1:8080"; };
  database = mkOption { type = str; default = "/var/lib/smoothbrain/state.db"; };

  plugins = {
    uptime-kuma.enable = mkEnableOption "Uptime Kuma plugin";
    xai = {
      enable = mkEnableOption "xAI plugin";
      model = mkOption { type = str; default = "grok-3"; };
      apiKeyFile = mkOption { type = str; };
    };
    mattermost = {
      enable = mkEnableOption "Mattermost plugin";
      url = mkOption { type = str; };
      tokenFile = mkOption { type = str; };
    };
  };

  routes = mkOption {
    type = listOf (submodule { ... });
    default = [];
  };

  supervisor.tasks = mkOption {
    type = listOf (submodule { ... });
    default = [];
  };
};
```

Generates `/etc/smoothbrain/config.json` and runs as a systemd service. Secrets use `*File` options (compatible with sops-nix/agenix) - Go reads from file paths at startup.

## SQLite Schema

```sql
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    type TEXT NOT NULL,
    payload TEXT NOT NULL,  -- JSON
    timestamp DATETIME NOT NULL,
    route TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE plugin_state (
    plugin TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,  -- JSON
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin, key)
);

CREATE TABLE supervisor_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task TEXT NOT NULL,
    result TEXT,  -- JSON
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Web UI

Embedded via `embed.FS`. Go stdlib `net/http` serves static files + JSON API. Frontend: plain HTML + htmx for interactivity. Pages: event log, plugin status, route health, supervisor log.

## Dependencies (minimal)

- `modernc.org/sqlite` - pure Go SQLite (no CGo, easier Nix builds)
- Everything else: stdlib (`net/http`, `encoding/json`, `context`, `log/slog`)
- Mattermost/xAI clients: hand-rolled with `net/http`

## MVP Implementation Steps

Epic: `td-5f3dcd` — Smoothbrain MVP: Core Framework + Uptime Kuma / xAI / Mattermost

| # | Task ID | Title | Points | Depends on |
|---|---------|-------|--------|------------|
| 1 | `td-ff9a26` | Project scaffolding: go mod, flake.nix, main.go, config, SQLite | 3 | — |
| 2 | `td-c82184` | Core framework: event bus, plugin registry, router | 5 | td-ff9a26 |
| 3 | `td-5a3472` | Uptime Kuma plugin: webhook source | 2 | td-c82184 |
| 4 | `td-8918ee` | xAI plugin: LLM transform layer | 3 | td-c82184 |
| 5 | `td-9b1954` | Mattermost plugin: chat sink | 2 | td-c82184 |
| 6 | `td-27a21a` | Wire MVP route: uptime-kuma -> xai -> mattermost | 2 | td-5a3472, td-8918ee, td-9b1954 |
| 7 | `td-d6eb84` | NixOS module: flake module, systemd service, config generation | 5 | td-27a21a |
| 8 | `td-96e6b1` | Web UI: embedded event log and plugin status | 3 | td-27a21a |

**Total: 25 points**

```
td-ff9a26 scaffolding
    └─> td-c82184 core framework
            ├─> td-5a3472 uptime kuma
            ├─> td-8918ee xai
            └─> td-9b1954 mattermost
                    └─> td-27a21a wire MVP route
                            ├─> td-d6eb84 nix module
                            └─> td-96e6b1 web ui
```

## Verification

- `go build ./cmd/smoothbrain` compiles
- `nix build` produces the binary
- Send test webhook to `/hooks/uptime-kuma` -> flows through xAI -> appears in Mattermost
- Web UI shows event in the log
- `nixos-rebuild switch` deploys and starts the service
