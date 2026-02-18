# uptime-kuma

Source plugin that receives webhook notifications from [Uptime Kuma](https://github.com/louislam/uptime-kuma).

## Type

Source (webhook-based)

## Config

| Field | Required | Description |
|-------|----------|-------------|
| `webhook_token` | No | Shared secret for authenticating incoming webhooks |
| `webhook_token_file` | No | Path to a file containing the webhook token |

If neither token field is set, the webhook endpoint accepts all requests (not recommended in production).

## Setup

1. In Uptime Kuma, go to a monitor's notification settings and add a webhook notification.
2. Set the URL to `http://<smoothbrain-host>:<port>/hooks/uptime-kuma`.
3. If you configured a `webhook_token`, add it as a custom header: `X-Webhook-Token: <your-token>`.

## Event

| Field | Value |
|-------|-------|
| Source | `uptime-kuma` |
| Type | `alert` |
| Payload | The raw JSON body from the Uptime Kuma webhook |

## Example config

```json
{
  "plugins": {
    "uptime-kuma": {
      "webhook_token_file": "/run/secrets/uptimekuma-token"
    }
  }
}
```
