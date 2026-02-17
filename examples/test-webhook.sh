#!/usr/bin/env bash
# Send a test Uptime Kuma webhook payload to verify routing.
# Usage: ./examples/test-webhook.sh [host:port]
set -euo pipefail

ADDR="${1:-127.0.0.1:8080}"

echo "==> Sending test webhook to $ADDR"
curl -s -X POST "http://$ADDR/hooks/uptime-kuma" \
  -H "Content-Type: application/json" \
  -d '{
    "heartbeat": {
      "status": 0,
      "msg": "Connection refused",
      "time": "2026-02-17T12:00:00Z",
      "ping": null
    },
    "monitor": {
      "name": "Production API",
      "url": "https://api.example.com/health",
      "type": "http"
    }
  }' | jq .

echo ""
echo "==> Checking events API"
curl -s "http://$ADDR/api/events" | jq .

echo ""
echo "==> Health check"
curl -s "http://$ADDR/api/health" | jq .
