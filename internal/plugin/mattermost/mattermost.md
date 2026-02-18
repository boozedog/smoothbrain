# mattermost

Bidirectional plugin that posts messages to and receives messages from a [Mattermost](https://mattermost.com) server.

## Type

Source + Sink

## Config

| Field | Required | Description |
|-------|----------|-------------|
| `url` | Yes | Base URL of the Mattermost server (e.g. `https://mattermost.example.com`) |
| `token` | No | Mattermost personal access token or bot token (inline) |
| `token_file` | No | Path to a file containing the token |
| `listen` | No | Enable WebSocket listener to receive messages (default `false`) |

## Setup

1. In Mattermost, create a bot account or generate a personal access token under **Profile > Security > Personal Access Tokens**.
2. Provide the token via `token` or write it to a file and reference it with `token_file`.
3. Make sure the bot/user is a member of any channels you want to post to or listen in.
4. Set `"listen": true` to enable the source (incoming messages).

## Source behavior

When `listen` is true, the plugin connects to the Mattermost WebSocket API and emits events for:

- **Direct messages** (DMs) sent to the bot
- **@mentions** of the bot in any channel it belongs to

Regular channel messages without an @mention are ignored. The bot's own messages are filtered out to prevent loops.

Emitted events have `source: "mattermost"`, `type: "message"` and payload:

| Field | Description |
|-------|-------------|
| `channel` | Channel ID (used directly by the sink for replies) |
| `channel_id` | Channel ID |
| `post_id` | Mattermost post ID |
| `root_id` | Thread root ID (empty if not in a thread) |
| `message` | Message text |
| `user_id` | Sender's user ID |
| `sender_name` | Sender's display name |
| `channel_type` | `"D"` for DM, `"O"` for open channel, etc. |

## Sink behavior

When an event is routed to this plugin, it posts a message to the Mattermost channel specified in the event payload's `channel` field (this should be a channel ID, set via the route's sink params or carried through from a source event).

If the payload contains `post_id`, the reply is threaded: it uses `root_id` if present (continuing an existing thread) or `post_id` (starting a new thread under the triggering message).

If the payload contains a `summary` key (e.g. from the xai transform), that's used as the message body. Otherwise the full payload is formatted as a JSON code block.

## Example config

```json
{
  "plugins": {
    "mattermost": {
      "url": "https://mattermost.example.com",
      "token_file": "/run/secrets/mattermost-token",
      "listen": true
    }
  }
}
```

## Example routes

Sink-only (post alerts to a channel):

```json
{
  "name": "alerts-to-chat",
  "source": "uptime-kuma",
  "event": "alert",
  "pipeline": [],
  "sink": {
    "plugin": "mattermost",
    "params": {"channel": "abc123channelid"}
  }
}
```

Bidirectional (reply to DMs/mentions via xai):

```json
{
  "name": "mm-reply",
  "source": "mattermost",
  "event": "message",
  "pipeline": [
    {"plugin": "xai", "action": "summarize", "params": {"prompt": "Respond helpfully."}}
  ],
  "sink": {"plugin": "mattermost", "params": {}}
}
```

No sink params needed for the reply route — `channel`, `post_id`, and `root_id` flow through from the source event payload.
