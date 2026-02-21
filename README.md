# smoothbrain

Personal infrastructure orchestrator. Routes events between services, runs automations, and has an LLM intelligence layer. Built as a single Go binary with compiled-in plugins.

## Architecture

```
webhook POST /hooks/{plugin}
    -> event bus
    -> router (matches source + event type to routes)
    -> transform pipeline (LLM summarize, URL fetch, etc.)
    -> sink (Mattermost post, Obsidian note, etc.)
    -> logged to SQLite
```

- **Event bus** — in-process pub/sub with SQLite persistence
- **Plugins** — sources (emit events), transforms (enrich), sinks (deliver)
- **Routes** — configurable pipelines: source -> transforms -> sink
- **Supervisor** — scheduled tasks on cron expressions
- **Web UI** — embedded HTML + htmx + franken-ui at `/`, live updates via WebSocket
- **Auth** — WebAuthn/passkey authentication

### Plugins

| Plugin | Type | Description |
|---|---|---|
| uptime-kuma | source | Webhook receiver for Uptime Kuma alerts |
| td | source | Webhook receiver for td task events |
| mattermost | source + sink | Chat commands via WebSocket, posts responses |
| xai | transform | xAI/Grok LLM summarization and processing |
| webmd | transform | Fetches URLs and converts to markdown |
| claudecode | transform | Runs Claude Code CLI queries |
| obsidian | transform + sink | Vault indexing, note/link/log writing |
| tailscale | health | Health check wrapper for embedded tsnet node |

## Local development

Requires Go 1.25+ and [templ](https://templ.guide/).

### Build and run

```sh
just build    # templ generate + go build
just dev      # hot reload via air
```

Or manually:

```sh
templ generate
go run ./cmd/smoothbrain -config examples/dev.json
```

### Environment

Copy `example.env` to `.env` and fill in values. mise loads `.env` automatically.

```sh
cp example.env .env
```

### Config

Config is JSON with `$VAR` environment variable expansion. See `examples/dev.json` for a working dev config.

Key sections:

```json
{
  "http": {"address": "127.0.0.1:8080"},
  "database": "smoothbrain.db",
  "auth": {
    "rp_display_name": "smoothbrain",
    "rp_id": "smoothbrain.tail9fdd65.ts.net",
    "rp_origins": ["https://smoothbrain.tail9fdd65.ts.net"]
  },
  "tailscale": {"enabled": true, "auth_key": "$TS_AUTHKEY"},
  "plugins": {
    "xai": {"model": "grok-4-1-fast-non-reasoning", "api_key": "$XAI_API_KEY"},
    "mattermost": {"url": "$MATTERMOST_URL", "token": "$MATTERMOST_TOKEN", "listen": true}
  },
  "routes": [],
  "supervisor": {"tasks": []}
}
```

### Tailscale / tsnet

smoothbrain embeds a Tailscale node via tsnet. When `"tailscale": {"enabled": true}`, both a local HTTP server and a tsnet HTTPS listener run simultaneously. Set `TS_AUTHKEY` or `"auth_key"` in config. On first run without an auth key, tsnet prints a login URL to stderr.

### Test it

```sh
# Health check
curl http://127.0.0.1:8080/api/health

# Send a fake Uptime Kuma alert
curl -X POST http://127.0.0.1:8080/hooks/uptime-kuma \
  -H "Content-Type: application/json" \
  -d '{"monitor":{"name":"My Service","url":"https://example.com"},"heartbeat":{"status":0,"msg":"Connection refused"}}'

# View events
curl http://127.0.0.1:8080/api/events

# Web UI
open http://127.0.0.1:8080
```

Or use the test script:

```sh
./examples/test-webhook.sh
```

## API

| Endpoint | Method | Description |
|---|---|---|
| `/` | GET | Web UI |
| `/api/health` | GET | Health check (all plugins) |
| `/api/health/html` | GET | Health status HTML fragment |
| `/api/events` | GET | Recent events (JSON) |
| `/api/events/html` | GET | Recent events (HTML fragment) |
| `/api/events/{id}/runs` | GET | Pipeline runs for an event |
| `/api/status/html` | GET | Status HTML fragment |
| `/api/log/html` | GET | Recent log entries (HTML fragment) |
| `/ws` | GET | WebSocket for live UI updates |
| `/hooks/uptime-kuma` | POST | Uptime Kuma webhook |
| `/hooks/td` | POST | td webhook |

## Deployment (NixOS)

> Note: The NixOS module exists but may not reflect the latest config changes.

```nix
{
  inputs.smoothbrain.url = "github:boozedog/smoothbrain";

  imports = [ smoothbrain.nixosModules.default ];

  services.smoothbrain = {
    enable = true;
    http.address = "127.0.0.1:8080";
    plugins = {
      uptime-kuma.enable = true;
      xai = {
        enable = true;
        apiKeyFile = config.sops.secrets.xai-api-key.path;
      };
      mattermost = {
        enable = true;
        url = "https://mattermost.example.com";
        tokenFile = config.sops.secrets.mattermost-token.path;
      };
    };
  };
}
```

## Project structure

```
cmd/smoothbrain/main.go          Entry point
internal/
  config/config.go               Config structs + JSON loader
  auth/                          WebAuthn/passkey authentication
  core/
    bus.go                       Event bus (in-process pub/sub)
    hub.go                       WebSocket hub (live UI updates)
    router.go                    Route matching + pipeline execution
    server.go                    HTTP server + embedded web UI
    web/                         Embedded web UI (franken-ui, htmx)
    supervisor.go                Scheduled task runner
    logbuf.go                    Log ring buffer
  store/store.go                 SQLite (WAL mode, schema migration)
  plugin/
    plugin.go                    Plugin interfaces
    registry.go                  Plugin lifecycle management
    claudecode/                  Claude Code CLI
    mattermost/                  Chat source + sink
    obsidian/                    Obsidian vault integration
    tailscale/                   tsnet health wrapper
    td/                          td webhook source
    uptimekuma/                  Uptime Kuma webhook source
    webmd/                       URL-to-markdown
    xai/                         xAI/Grok LLM
nix/module.nix                   NixOS module
examples/
  dev.json                       Dev config
  config.json                    Full example config
  test-webhook.sh                Test script
```
