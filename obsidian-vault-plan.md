# Obsidian Vault Plugin for Smoothbrain

## Context

The smoothbrain event bus currently has no way to interact with local files. You have an Obsidian vault at `~/obsidian/smoothbrain` containing markdown notes, vehicle records, and daily notes. This plugin will let smoothbrain read from, write to, and search across the vault -- all driven by events on the bus. It also brings two infrastructure pieces that are defined but unimplemented: plugin store access (`StoreAware`) and the supervisor (scheduled tasks).

## Structured Data Format

Vehicle files use **Dataview inline fields** (`Key:: Value`) for metadata and **markdown tables** for time-series data. Daily notes use **sections** (`## TODO`, `## Completed`, `## Links`, `## Diary`). Everything stays human-readable and editable in Obsidian.

**Vehicle example** (`vehicles/tacoma.md`):
```markdown
# 2020 Toyota Tacoma

Make:: Toyota
Model:: Tacoma
Year:: 2020
VIN:: 1234567890
Registration Expires:: 2026-08-15
Inspection Expires:: 2026-03-01

## Maintenance Log

| Date | Miles | Description | Cost | Location |
|------|-------|-------------|------|----------|
| 2025-03-15 | 45000 | Oil change | $80 | Jiffy Lube |
| 2025-06-20 | 48000 | Tire rotation | $40 | Discount Tire |

## Mileage Log

| Date | Miles |
|------|-------|
| 2025-01-01 | 42000 |
| 2025-06-01 | 48000 |
```

**Daily note example** (`daily/2026/2026-02-19.md`):
```markdown
## TODO

- [ ] Review PR #42

## Completed

- [x] Fixed CI pipeline
- [x] Approved td task: deploy staging

## Links

- [[links/2026/2026-02-19-interesting-article|Interesting Article]]

## Diary

**14:32** - Fixed the CI pipeline, was a flaky test in the integration suite.
**16:15** - Merged the new auth feature after code review.
```

## Key Design Decision: Write-as-Transform

The smoothbrain architecture sends pipeline output to a single sink. To write to the vault AND get a confirmation reply in Mattermost, obsidian write operations are implemented as **transform actions** (not sink-only). A `write_note` transform writes to disk, then sets `payload["summary"]` to a confirmation string. The mattermost sink at the end of the pipeline delivers the confirmation. This keeps the architecture clean -- no multi-sink hacking needed.

## Search: SQLite FTS5

The existing `modernc.org/sqlite` dependency is compiled with `SQLITE_ENABLE_FTS5`. Zero new dependencies. BM25 ranking, snippet extraction, phrase queries, and field-scoped queries all work out of the box. The FTS5 index lives in the same SQLite database as everything else.

---

## Phase 1: StoreAware Infrastructure

Add an optional `StoreAware` interface so plugins can access the SQLite database. Follows the existing `CommandAware` pattern.

### Files to modify

**`internal/plugin/plugin.go`** -- Add interface:
```go
type StoreAware interface {
    SetStore(db *sql.DB)
}
```

**`internal/plugin/registry.go`** -- Add `db *sql.DB` field, modify `NewRegistry(log, db)`, inject store in `InitAll` before calling `Init`:
```go
if sa, ok := p.(StoreAware); ok {
    sa.SetStore(r.db)
}
```

**`cmd/smoothbrain/main.go`** -- Pass `db.DB()` to `NewRegistry`.

### Verify
`go build ./cmd/smoothbrain` passes. Existing plugins unchanged (none implement StoreAware).

---

## Phase 2: Plugin Skeleton

Create `internal/plugin/obsidian/obsidian.go`. Implements `Plugin`, `Transform`, and `StoreAware`.

```go
type Config struct {
    VaultPath string `json:"vault_path"` // default ~/obsidian/smoothbrain
}

type Plugin struct {
    cfg     Config
    db      *sql.DB
    bus     plugin.EventBus
    log     *slog.Logger
    watcher *Watcher // added in phase 5
}
```

Lifecycle: `SetStore(db)` → `Init(cfg)` (expand `~`, create FTS schema) → `Start(ctx, bus)` (index vault, start watcher) → `Stop()`.

Transform actions:
- `search` -- FTS5 full-text search, returns ranked results in `payload["summary"]`
- `read` -- read a specific file by path/name, returns content in `payload["summary"]`
- `query` -- query structured fields (e.g., vehicles with inspection expiring within N days)
- `write_note` -- append timestamped entry to daily diary, return confirmation
- `write_link` -- add link to daily note `## Links` section
- `write_log` -- append row to a vehicle maintenance/mileage table

Register in `main.go`:
```go
registry.Register(obsidian.New(log))
```

### Verify
App starts with `"obsidian": {"vault_path": "~/obsidian/smoothbrain"}` in config.

---

## Phase 3: Markdown Parser + FTS5 Index

### `internal/plugin/obsidian/markdown.go`

Line-by-line parser. No external library needed.

```go
type NoteFile struct {
    Path     string
    Title    string            // first H1 or filename
    Fields   map[string]string // Dataview fields (Key:: Value)
    Sections []Section
    Raw      string
}

type Section struct {
    Heading string
    Level   int
    Content string
    Tables  []Table
}

type Table struct {
    Headers []string
    Rows    [][]string
}
```

Key functions:
- `ParseNote(path, content string) NoteFile`
- `ParseInlineFields(content string) map[string]string` -- regex: `^([A-Za-z][A-Za-z0-9 _-]*)::(.+)$`
- `ParseTable(lines []string) Table`
- `(n NoteFile) FindSection(heading string) (Section, bool)`

### `internal/plugin/obsidian/index.go`

FTS5 schema:
```sql
CREATE TABLE IF NOT EXISTS obsidian_notes (
    path TEXT PRIMARY KEY,
    title TEXT,
    fields TEXT,        -- JSON of inline fields
    content TEXT,       -- full markdown body
    modified_at INTEGER -- unix timestamp
);

CREATE VIRTUAL TABLE IF NOT EXISTS obsidian_fts USING fts5(
    title, fields, content,
    content=obsidian_notes,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- insert/delete/update triggers to keep FTS in sync
```

Key functions:
- `(p *Plugin) initSchema() error`
- `(p *Plugin) IndexFile(relPath string) error` -- parse file, upsert into `obsidian_notes` (triggers update FTS)
- `(p *Plugin) IndexVault() error` -- walk vault, skip unchanged files (compare mtime)
- `(p *Plugin) Search(query string, limit int) ([]SearchResult, error)` -- BM25-ranked, with snippets

Search query:
```sql
SELECT n.path, n.title,
       snippet(obsidian_fts, 2, '**', '**', '...', 32) AS excerpt,
       bm25(obsidian_fts, 5.0, 3.0, 1.0) AS score
FROM obsidian_fts f
JOIN obsidian_notes n ON f.rowid = n.rowid
WHERE obsidian_fts MATCH ?
ORDER BY score
LIMIT ?
```
(bm25 weights: title=5x, fields=3x, content=1x; returns negative values, lower=better, so ascending order gives best first)

### Verify
Create test vault files, run `IndexVault()`, verify `Search("oil change", 5)` returns ranked results with snippets.

---

## Phase 4: File Writers

### `internal/plugin/obsidian/writers.go`

Atomic writes: write to temp file, `os.Rename` over original. Prevents corruption from concurrent Obsidian edits.

**Daily note operations:**
- `dailyNotePath(t time.Time) string` -- returns `daily/2026/2026-02-19.md`
- `ensureDailyNote(t time.Time) string` -- create from template if missing (with all 4 section headers)
- `writeNote(ctx, event, params)` -- append `**HH:MM** - {message}` to `## Diary`
- `writeLink(ctx, event, params)` -- append wiki-link to `## Links`

**Vehicle operations:**
- `writeLog(ctx, event, params)` -- append table row to specified section in a vehicle file
- Reads `payload["message"]` for the raw command text, expects payload fields: `vehicle`, `description`, `miles`, `cost`, `location` (set by upstream LLM transform or manually)

**Section manipulation helpers:**
- `appendToSection(content, sectionName, line string) string`
- `appendTableRow(content, sectionName string, values []string) string`

All writers call `IndexFile()` after mutation to keep FTS in sync.

### Verify
Configure routes, send `!note fixed CI` via Mattermost, check daily note file is created/updated. Send `!log oil change on tacoma at 50000 miles` through an LLM parse step, check vehicle file updated.

---

## Phase 5: File Watcher

**New dependency:** `github.com/fsnotify/fsnotify`

### `internal/plugin/obsidian/watcher.go`

```go
type Watcher struct {
    plugin  *Plugin
    watcher *fsnotify.Watcher
    mu      sync.Mutex
    pending map[string]time.Time // debounce buffer
}
```

- Recursive directory watching (manually add subdirs via `filepath.WalkDir`)
- Debounce: 500ms after last change before re-indexing a file
- Ignores dotfiles/directories (`.git`, `.obsidian`)
- On CREATE of a directory, add it to the watcher
- On CREATE/WRITE/RENAME of `.md` files, schedule re-index

Integrated in `Start()` after initial `IndexVault()`.

### Verify
Start app, edit a file in Obsidian, check debug logs show re-indexing.

---

## Phase 6: Supervisor

The supervisor config structs exist (`config.SupervisorConfig`, `config.SupervisorTask`) and the `supervisor_log` table exists. The runtime code does not.

### `internal/core/supervisor.go`

```go
type Supervisor struct {
    tasks    []config.SupervisorTask
    bus      *Bus
    store    *store.Store
    log      *slog.Logger
    cancel   context.CancelFunc
}
```

**Schedule format:** Support two forms:
- `"daily@09:00"` -- run at 09:00 local time each day
- `"1h"`, `"30m"` -- Go duration, run on interval

Each task emits a synthetic event: `Source: "supervisor"`, `Type: task.Name`, `Payload["message"]: task.Prompt`. The router picks it up and runs it through the configured pipeline just like any other event.

Logs each execution to `supervisor_log`.

### Wire up in `main.go`
```go
supervisor := core.NewSupervisor(cfg.Supervisor.Tasks, bus, db, log)
supervisor.Start(ctx)
defer supervisor.Stop()
```

### Example: vehicle inspection alerts

Config:
```json
{
  "supervisor": {
    "tasks": [{
      "name": "vehicle-check",
      "schedule": "daily@09:00",
      "prompt": "Check vehicle inspection and registration expiry dates"
    }]
  }
}
```

Route:
```json
{
  "name": "vehicle-inspection-alerts",
  "source": "supervisor",
  "event": "vehicle-check",
  "pipeline": [
    {"plugin": "obsidian", "action": "query", "params": {"dir": "vehicles", "field": "Inspection Expires", "within_days": 30}},
    {"plugin": "xai", "action": "summarize", "params": {"prompt": "Format these upcoming vehicle deadlines as a friendly reminder."}}
  ],
  "sink": {"plugin": "mattermost", "params": {"channel": "CHANNEL_ID"}}
}
```

### Verify
Set schedule to `"1m"` for testing, verify event emitted, pipeline runs, Mattermost receives alert.

---

## Example Route Configurations

### `!note <text>` -- Daily diary entry
```json
{
  "name": "mattermost-note",
  "description": "Add to daily diary",
  "source": "mattermost",
  "event": "note",
  "pipeline": [
    {"plugin": "obsidian", "action": "write_note", "params": {}}
  ],
  "sink": {"plugin": "mattermost", "params": {}}
}
```

### `!vault <question>` -- Search vault + LLM answer
```json
{
  "name": "mattermost-vault",
  "description": "Search and query the vault",
  "source": "mattermost",
  "event": "vault",
  "timeout": "60s",
  "pipeline": [
    {"plugin": "obsidian", "action": "search", "params": {}},
    {"plugin": "xai", "action": "summarize", "params": {"prompt": "Answer the user's question using the vault search results provided. Be concise and specific."}}
  ],
  "sink": {"plugin": "mattermost", "params": {}}
}
```

### `!log <description>` -- Vehicle maintenance via LLM parsing
```json
{
  "name": "mattermost-vehicle-log",
  "description": "Log vehicle maintenance",
  "source": "mattermost",
  "event": "log",
  "timeout": "60s",
  "pipeline": [
    {"plugin": "xai", "action": "summarize", "params": {"prompt": "Parse this vehicle maintenance command. Extract JSON: {\"vehicle\": \"\", \"description\": \"\", \"miles\": \"\", \"cost\": \"\", \"location\": \"\"}. Only return JSON."}},
    {"plugin": "obsidian", "action": "write_log", "params": {}}
  ],
  "sink": {"plugin": "mattermost", "params": {}}
}
```

### td approve → diary entry (automatic)
```json
{
  "name": "td-approve-diary",
  "source": "td",
  "event": "approve",
  "pipeline": [
    {"plugin": "obsidian", "action": "write_note", "params": {}}
  ],
  "sink": {"plugin": "mattermost", "params": {"channel": "CHANNEL_ID"}}
}
```

---

## Files Summary

### New files (6)
| File | Purpose |
|------|---------|
| `internal/plugin/obsidian/obsidian.go` | Plugin struct, lifecycle, transform dispatch |
| `internal/plugin/obsidian/markdown.go` | Markdown parser: inline fields, sections, tables |
| `internal/plugin/obsidian/index.go` | FTS5 schema, indexing, search |
| `internal/plugin/obsidian/writers.go` | File write operations (daily notes, vehicle logs) |
| `internal/plugin/obsidian/watcher.go` | fsnotify file watcher with debounce |
| `internal/core/supervisor.go` | Scheduled task execution |

### Modified files (3)
| File | Change |
|------|--------|
| `internal/plugin/plugin.go` | Add `StoreAware` interface (~4 lines) |
| `internal/plugin/registry.go` | Add `db` field, modify `NewRegistry` sig, inject store in `InitAll` |
| `cmd/smoothbrain/main.go` | Register obsidian plugin, pass db to registry, start supervisor |

### New dependency (1)
`github.com/fsnotify/fsnotify` -- file system notifications for the vault watcher
