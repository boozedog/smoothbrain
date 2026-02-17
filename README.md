# smoothbrain

Personal infrastructure orchestrator. Routes events between services, runs automations, and has an LLM intelligence layer. Built as a single Go binary with compiled-in plugins.

**MVP pipeline:** Uptime Kuma alert → xAI summarize → Mattermost notification

## Architecture

```
webhook POST /hooks/uptime-kuma
    → event bus
    → router (matches source + event type to routes)
    → transform pipeline (xAI summarize)
    → sink (Mattermost post)
    → logged to SQLite
```

- **Event bus** — in-process pub/sub
- **Plugins** — sources (emit events), transforms (enrich), sinks (deliver)
- **Routes** — configurable pipelines: source → transforms → sink
- **Web UI** — embedded HTML + htmx at `/`

## Local development

Requires Go 1.25+.

### Build and run

```sh
go run ./cmd/smoothbrain -config examples/dev.json
```

Or build a binary:

```sh
go build -o smoothbrain ./cmd/smoothbrain
./smoothbrain -config examples/dev.json
```

### Minimal dev config

Create `examples/dev.json` for local testing without external services:

```json
{
  "http": {"address": "127.0.0.1:8080"},
  "database": "smoothbrain.db",
  "plugins": {
    "uptime-kuma": {},
    "xai": {"model": "grok-3"},
    "mattermost": {"url": "http://localhost"}
  },
  "routes": [],
  "supervisor": {"tasks": []}
}
```

This starts the server with all plugins registered but no routes wired, so you can test the webhook ingestion and web UI without needing API keys.

### Test it

With the server running:

```sh
# Health check
curl http://127.0.0.1:8080/api/health

# Send a fake Uptime Kuma alert
curl -X POST http://127.0.0.1:8080/hooks/uptime-kuma \
  -H "Content-Type: application/json" \
  -d '{"monitor":{"name":"My Service","url":"https://example.com"},"heartbeat":{"status":0,"msg":"Connection refused"}}'

# View events (JSON)
curl http://127.0.0.1:8080/api/events

# Web UI
open http://127.0.0.1:8080
```

Or use the test script:

```sh
./examples/test-webhook.sh           # defaults to 127.0.0.1:8080
./examples/test-webhook.sh localhost:9090  # custom address
```

### Full pipeline (with real services)

To test the full uptime-kuma → xai → mattermost route, create secret files and use the example config:

```sh
echo "xai-sk-your-key-here" > /tmp/xai-key
echo "your-mattermost-token" > /tmp/mm-token
```

Edit `examples/config.json` to point `api_key_file` and `token_file` at those paths, set the Mattermost URL and channel ID, then:

```sh
go run ./cmd/smoothbrain -config examples/config.json
```

## API

| Endpoint | Method | Description |
|---|---|---|
| `/api/health` | GET | Health check |
| `/api/events` | GET | Recent events (JSON) |
| `/api/events/html` | GET | Recent events (HTML fragment, used by htmx) |
| `/hooks/uptime-kuma` | POST | Uptime Kuma webhook receiver |

## Deployment (NixOS)

```nix
{
  inputs.smoothbrain.url = "github:dmarx/smoothbrain";

  # In your NixOS config:
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
    routes = [{
      name = "uptime-kuma-alerts";
      source = "uptime-kuma";
      event = "alert";
      pipeline = [{
        plugin = "xai";
        action = "summarize";
        params.prompt = "Summarize this alert concisely.";
      }];
      sink = {
        plugin = "mattermost";
        params.channel = "your-channel-id";
      };
    }];
  };
}
```

## Project structure

```
cmd/smoothbrain/main.go          Entry point
internal/
  config/config.go               Config structs + JSON loader
  core/
    bus.go                        Event bus (in-process pub/sub)
    router.go                     Route matching + pipeline execution
    server.go                     HTTP server + embedded web UI
  store/store.go                  SQLite (WAL mode, schema migration)
  plugin/
    plugin.go                     Plugin/Sink/Transform interfaces
    registry.go                   Plugin lifecycle management
    uptimekuma/uptimekuma.go      Webhook source
    xai/xai.go                    LLM transform
    mattermost/mattermost.go      Chat sink
nix/module.nix                   NixOS module
examples/
  config.json                    Full example config
  test-webhook.sh                Test script
```
