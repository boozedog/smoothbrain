# twitter

Source plugin that polls an X (Twitter) List for new tweets using the v2 search API.

## Type

Source (polling-based)

## Config

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `bearer_token` | Yes* | — | X API v2 Bearer token |
| `bearer_token_file` | Yes* | — | Path to a file containing the Bearer token |
| `list_id` | Yes | — | The numeric ID of the X List to monitor |
| `query_filter` | No | `""` | Additional search operators appended to the query (e.g. `-is:retweet -is:reply`) |
| `poll_interval` | No | `"60s"` | How often to poll for new tweets (Go duration string) |

*Provide either `bearer_token` or `bearer_token_file`.

## Setup

1. Create a project and app at [developer.x.com](https://developer.x.com).
2. Generate a Bearer token (app-only auth) — the free tier works but has limited requests.
3. Create a List in the X web UI and note its numeric ID from the URL.
4. Write the Bearer token to a file and reference it with `bearer_token_file`.

The plugin uses `GET /2/tweets/search/recent` with the `list:<id>` operator. It tracks `since_id` to only fetch new tweets each poll cycle and follows pagination if there are more than 100 results.

If `bearer_token` and `list_id` are not both set, the plugin logs a warning and stays idle — the server still starts normally.

## Event

| Field | Value |
|-------|-------|
| Source | `twitter` |
| Type | `tweet` |

### Payload

| Key | Type | Description |
|-----|------|-------------|
| `tweet_id` | string | Tweet ID |
| `text` | string | Tweet text |
| `author_id` | string | Author's user ID |
| `author_username` | string | Author's @handle |
| `author_name` | string | Author's display name |
| `created_at` | string | ISO 8601 timestamp |
| `like_count` | int | Likes |
| `retweet_count` | int | Retweets |
| `reply_count` | int | Replies |
| `impression_count` | int | Impressions |
| `url` | string | Link to the tweet |

## Example config

```json
{
  "plugins": {
    "twitter": {
      "bearer_token_file": "/run/secrets/twitter-bearer",
      "list_id": "1234567890",
      "query_filter": "-is:retweet -is:reply",
      "poll_interval": "60s"
    }
  }
}
```
