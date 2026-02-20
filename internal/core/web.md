# Web Frontend

Embedded single-page dashboard served at `/`. No build tools, no npm — everything is vendored and compiled into the binary via `go:embed`.

## Stack

| Layer | Technology | Version |
|-------|------------|---------|
| Templates | [templ](https://templ.guide) | 0.3.977 |
| CSS/Components | [Franken UI](https://franken-ui.dev) (UIkit 3 + shadcn/ui style) | 2.1.2 |
| Interactivity | htmx + ws extension | 2.0.4 |
| WebSocket | coder/websocket | — |
| Embedding | `go:embed all:web` | stdlib |

## File layout

```
internal/core/
  web/
    index.html                          Main page
    vendor/
      frankenui/core.min.css            Component styles + dark mode
      frankenui/utilities.min.css       Pre-built Tailwind utility classes
      frankenui/js/core.iife.js         UIkit component behaviors
      frankenui/js/icon.iife.js         UIkit icon system
      htmx/htmx.min.js                 htmx core
      htmx/ext-ws.js                   htmx WebSocket extension
  templates.templ                       templ source (EventsTable, PipelineRuns, etc.)
  templates_templ.go                    Generated — do not edit
  hub.go                                WebSocket hub (broadcast to clients)
  server.go                             HTTP routes, go:embed, static file serving
```

## How it works

### Static files (go:embed)

`server.go` embeds the entire `web/` directory:

```go
//go:embed all:web
var webFS embed.FS
```

`fs.Sub(webFS, "web")` strips the prefix so `/index.html` maps to `web/index.html`. The resulting `http.FileServer` serves everything at `/`. The binary is fully self-contained.

### Franken UI theming

Franken UI provides a shadcn/ui-style design system built on UIkit 3. It uses CSS custom properties (`--background`, `--primary`, `--destructive`, etc.) with HSL values. Dark mode is activated by `<html class="dark">`.

Component classes follow UIkit conventions:
- Cards: `uk-card`, `uk-card-header`, `uk-card-body`, `uk-card-title`
- Tables: `uk-table`, `uk-table-divider`, `uk-table-hover`
- Labels: `uk-label`, `uk-label-primary`, `uk-label-secondary`, `uk-label-destructive`
- Layout: `uk-container`, `uk-container-xl`

The `utilities.min.css` file provides pre-extracted Tailwind CSS utility classes (display, flex, spacing, typography, etc.) so no Tailwind build step is needed.

### templ templates

HTML fragments are generated server-side by templ. The key components:

- `EventsTable(events)` — renders the events `<table>` with label-decorated columns and expandable payload rows
- `PipelineRuns(runs)` — renders pipeline run summaries with status labels and step details
- `EventsWrapper(events)` — wraps `EventsTable` in `<div id="events-table">` for WebSocket swapping

Run `templ generate` to compile `.templ` → `_templ.go`. The `just build` target does this automatically.

### Live updates (htmx + WebSocket)

Two update mechanisms:

**Health polling** — htmx fetches `/api/health` every 10 seconds and swaps into `#health`:
```html
<div id="health" hx-get="/api/health" hx-trigger="load, every 10s" hx-swap="innerHTML">
```

**Events table** — htmx's WebSocket extension connects to `/ws`:
```html
<div hx-ext="ws" ws-connect="/ws">
  <div id="events-table">loading...</div>
</div>
```

The Hub (`hub.go`) manages WebSocket clients. On connect, it sends the current state immediately. When new events arrive or pipelines complete, it re-queries the DB, renders `EventsWrapper` via templ, and broadcasts the raw HTML to all clients. htmx matches the `id="events-table"` attribute and swaps the DOM element automatically.

The notify channel has capacity 1, so rapid events get coalesced into a single broadcast.

### Row expand/collapse

Clicking an event row toggles `.open` on the row and its sibling payload row. CSS controls visibility (`display: none` / `display: table-row`). No library needed — vanilla JS event delegation on `document`.

## Adding a vendored dependency

1. Download the file into `web/vendor/<name>/`
2. Reference it in `index.html` with a `<script>` or `<link>` tag
3. It gets embedded automatically via `go:embed all:web`

## Fallback HTML endpoint

`GET /api/events/html` renders the same `EventsTable` as a standalone HTML fragment. Not currently used by the frontend, but available as an htmx polling alternative to WebSocket.
