# smoothbrain

Personal infrastructure orchestrator. Single Go binary with compiled-in plugins.

## Project structure

```
cmd/smoothbrain/main.go          Entry point, wires everything together
internal/
  config/config.go               Config structs, JSON loader, env expansion
  auth/                          WebAuthn (passkey) authentication
  core/
    bus.go                       Event bus (in-process pub/sub)
    hub.go                       WebSocket hub for live UI updates
    router.go                    Route matching + pipeline execution
    server.go                    HTTP server, API routes, embedded web UI
    supervisor.go                Scheduled task runner
    logbuf.go                    Ring buffer for recent log entries
    templates.templ              Templ templates (generates templates_templ.go)
    views.go                     View helpers
  store/store.go                 SQLite (WAL mode, auto-migration)
  plugin/
    plugin.go                    Plugin/Sink/Transform/HealthChecker interfaces
    registry.go                  Plugin lifecycle (init, start, stop, health)
    claudecode/                  Claude Code CLI integration
    mattermost/                  Chat source + sink (WebSocket listener)
    obsidian/                    Vault indexing, note/link/log writing
    tailscale/                   Health check wrapper for tsnet
    td/                          td webhook source
    twitter/                     Twitter/X plugin (not registered in main.go)
    uptimekuma/                  Uptime Kuma webhook source
    webmd/                       URL-to-markdown fetcher
    xai/                         xAI/Grok LLM transform
nix/                             NixOS module (aspirational)
examples/
  dev.json                       Development config
  config.json                    Full example config
  test-webhook.sh                Manual test script
```

## Build and dev

Requires Go 1.25+ and [templ](https://templ.guide/).

```sh
just build          # templ generate + go build
just dev            # air (hot reload)
```

- **air** watches `.go` and `.templ` files, runs `templ generate` before build
- **templ** generates `*_templ.go` from `.templ` files — do not edit generated files
- **vendored**: dependencies are in `vendor/`. Run `go mod vendor` after changing `go.mod`

Additional justfile targets: `fmt` (gofumpt), `lint` (golangci-lint), `vuln` (govulncheck), `release` (goreleaser), `vendor-web` (downloads franken-ui + htmx).

## Toolchain

Tool versions are managed by [mise](https://mise.jdx.dev/) via `.mise.toml`:

- **gofumpt** — formatter (stricter than gofmt)
- **golangci-lint** — linter
- **govulncheck** — vulnerability scanner
- **goreleaser** — release builds
- **just** — task runner

## Config

JSON config with `$VAR` / `${VAR}` environment variable expansion (via `os.ExpandEnv`).

- `examples/dev.json` is the working dev config
- `example.env` lists required env vars; `.env` is loaded by mise
- Tailscale config is top-level (`"tailscale": {}`), not inside `"plugins"`
- Plugin configs live under `"plugins": {"name": {...}}`

### Tailscale / tsnet

The tailscale integration uses an embedded tsnet node, not the CLI. Config:

```json
"tailscale": {
  "enabled": true,
  "auth_key": "$TS_AUTHKEY",
  "hostname": "smoothbrain",
  "service_name": "svc:smoothbrain"
}
```

- Both local HTTP and tsnet HTTPS listeners run simultaneously
- State defaults to `~/.local/state/smoothbrain/tsnet/`
- Tags are set via the auth key (create a tagged key in Tailscale admin)
- The tailscale plugin is a thin health wrapper; `SetServer()` injects the `*tsnet.Server`

## Plugin architecture

Every plugin implements `plugin.Plugin` (Name, Init, Start, Stop). Optional interfaces:

- `Transform` — enriches events in a pipeline
- `Sink` — delivers events (e.g. Mattermost post)
- `HealthChecker` — reports health via `/api/health`
- `WebhookSource` — registers POST handlers at `/hooks/{name}`
- `CommandAware` — receives routable command list from config
- `StoreAware` — receives `*sql.DB` for direct database access

Plugins are registered in `main.go`, initialized from the `"plugins"` config map.

## Auth

WebAuthn/passkey auth. Configured via `"auth"` config block:

- `rp_id`: relying party ID (your FQDN)
- `rp_origins`: allowed origins for WebAuthn
- When `rp_id` is empty, auth is disabled

## Hooks

`hk.pkl` defines git hooks:

- **pre-commit**: `gofumpt` (formatting check) + `golangci-lint`
- **pre-push**: trivy scans (vuln, secret, misconfig) + `golangci-lint` + `govulncheck`

## Key conventions

- Tests exist for most packages — run `go test ./...` or `just lint`
- slog for all logging
- Plugin names are lowercase, hyphenated in config, directory names in code
