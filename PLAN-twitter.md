# Twitter/X List Monitor: Source Plugin

Epic: `td-4e941c`

## Context

Add a polling-based source plugin that monitors an X List via the v2 search API and emits new tweets as events on the bus. The user creates and manages the List in the X web UI; the plugin just needs the List ID and a Bearer token.

Uses `GET /2/tweets/search/recent` with the `list:<id>` operator combined with configurable filters (e.g. `-is:retweet -is:reply`) to minimize API read costs ($0.005/post under X's pay-per-use pricing). Tracks `since_id` to only fetch new posts. Deduplication within a UTC day is handled by X's billing, so frequent polling doesn't multiply cost.

## Tasks

| # | Task ID | Title | Points | Depends on |
|---|---------|-------|--------|------------|
| 1 | `td-19b2f2` | Twitter plugin: core polling loop and X API v2 client | 5 | — |
| 2 | `td-f942d0` | Wire Twitter plugin into main.go and example configs | 1 | td-19b2f2 |

```
td-19b2f2 core plugin
    └─> td-f942d0 wiring + config
```

## Files to create/modify

| File | Action |
|------|--------|
| `internal/plugin/twitter/twitter.go` | **Create** — the plugin |
| `cmd/smoothbrain/main.go` | **Edit** — register the plugin |
| `examples/dev.json` | **Edit** — add twitter config stub |

## Plugin design: `internal/plugin/twitter/twitter.go`

### Config

```go
type Config struct {
    BearerToken     string `json:"bearer_token"`
    BearerTokenFile string `json:"bearer_token_file"`
    ListID          string `json:"list_id"`
    QueryFilter     string `json:"query_filter"`     // e.g. "-is:retweet -is:reply"
    PollInterval    string `json:"poll_interval"`     // duration string, default "60s"
}
```

### Lifecycle

- `Name()` returns `"twitter"`
- `Init()` — unmarshal config, read bearer token from file (same pattern as xai), parse poll interval with `time.ParseDuration`, validate list_id is present
- `Start()` — store bus ref, launch `go p.poll(ctx)` goroutine
- `Stop()` — no-op (ctx cancellation from main handles goroutine shutdown)

### Polling loop

- Ticker at configured interval (default 60s)
- Build query string: `list:<list_id> <query_filter>`
- Call `GET https://api.x.com/2/tweets/search/recent` with params:
  - `query` — the constructed query
  - `since_id` — last seen tweet ID (omitted on first poll)
  - `tweet.fields` — `created_at,public_metrics,author_id`
  - `user.fields` — `username,name`
  - `expansions` — `author_id`
  - `max_results` — `100`
- Parse response, follow `next_token` pagination if present
- For each tweet, emit a `plugin.Event`
- Update `since_id` to `meta.newest_id` from the response
- Log errors but don't crash — retry on next tick

### Event shape

```
Source:    "twitter"
Type:      "tweet"
Payload: {
    "tweet_id":         "1234567890",
    "text":             "the tweet text...",
    "author_id":        "9876543210",
    "author_username":  "handle",
    "author_name":      "Display Name",
    "created_at":       "2026-02-17T12:00:00.000Z",
    "like_count":       42,
    "retweet_count":    5,
    "reply_count":      3,
    "impression_count": 1000,
    "url":              "https://x.com/handle/status/1234567890"
}
```

### X API v2 response structures (for reference)

Search response:
```json
{
  "data": [
    {
      "id": "1234567890",
      "text": "...",
      "author_id": "9876543210",
      "created_at": "2026-02-17T12:00:00.000Z",
      "public_metrics": {
        "like_count": 42,
        "retweet_count": 5,
        "reply_count": 3,
        "impression_count": 1000
      }
    }
  ],
  "includes": {
    "users": [
      {"id": "9876543210", "username": "handle", "name": "Display Name"}
    ]
  },
  "meta": {
    "newest_id": "1234567890",
    "oldest_id": "1234567880",
    "result_count": 10,
    "next_token": "..."
  }
}
```

## Wiring (`cmd/smoothbrain/main.go`)

Add import and register call following existing pattern:
```go
import "github.com/dmarx/smoothbrain/internal/plugin/twitter"
// ...
registry.Register(twitter.New(log))
```

## Example config

```json
{
  "plugins": {
    "twitter": {
      "bearer_token_file": "/tmp/twitter-bearer",
      "list_id": "1234567890",
      "query_filter": "-is:retweet -is:reply",
      "poll_interval": "60s"
    }
  }
}
```

## Example route (twitter → xai → mattermost)

```json
{
  "name": "twitter-digest",
  "source": "twitter",
  "event": "tweet",
  "pipeline": [{
    "plugin": "xai",
    "action": "summarize",
    "params": {"prompt": "Summarize this tweet for a chat notification."}
  }],
  "sink": {"plugin": "mattermost", "params": {"channel": "twitter-feed"}}
}
```

## Verification

1. `go build ./cmd/smoothbrain` compiles clean
2. Start with dev config (no bearer token) — plugin logs a warning but server starts fine
3. With a real bearer token + list ID: plugin polls, logs found tweets, events appear in `/api/events` and the web UI
