# xai

Transform plugin that calls the [xAI (Grok)](https://x.ai) chat completions API to process events.

## Type

Transform

## Config

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `model` | No | `"grok-3"` | Model ID to use |
| `api_key_file` | No | — | Path to a file containing the xAI API key |

## Setup

1. Get an API key from [console.x.ai](https://console.x.ai).
2. Write it to a file and reference it with `api_key_file`.

## Actions

### `summarize`

Sends the event payload to the model with a system prompt and adds the response as `summary` in the payload.

| Param | Required | Default | Description |
|-------|----------|---------|-------------|
| `prompt` | No | `"Summarize this alert concisely for a chat notification:"` | Custom system prompt |

## Example config

```json
{
  "plugins": {
    "xai": {
      "model": "grok-3",
      "api_key_file": "/run/secrets/xai-key"
    }
  }
}
```

## Example route

```json
{
  "name": "summarize-alerts",
  "source": "uptime-kuma",
  "event": "alert",
  "pipeline": [{
    "plugin": "xai",
    "action": "summarize",
    "params": {"prompt": "Summarize this monitoring alert for Slack."}
  }],
  "sink": {"plugin": "mattermost", "params": {"channel": "alerts"}}
}
```
