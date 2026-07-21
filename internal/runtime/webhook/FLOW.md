# internal/runtime/webhook Flow

`internal/runtime/webhook` owns node-local HTTP webhook delivery. Critical
post-commit events (`msg.notify` and `msg.offline`) use a sync-WAL Pebble outbox;
the presence-only `user.onlinestatus` event remains a bounded best-effort memory
queue because Link-U does not derive durable business state from it.

## Critical Event Flow

```text
durable message commit / bounded offline-recipient chunk
  -> map to the v3 webhook body
  -> derive stable X-WK-Webhook-ID from event + message identity + recipients
  -> sync the active outbox record before admission returns
  -> bounded workers claim due records
  -> POST with stable ID and one-based attempt headers
  -> any HTTP 2xx: atomically replace the active record with a retained dedupe marker
  -> timeout / transport / non-2xx: persist exponential retry using the same ID
  -> max attempts: atomically move the record to dead-letter
```

Duplicate source admission is a successful no-op while the identity is pending,
dead, or retained as delivered. Process restart reopens Pebble and dispatches due
active records. Capacity is bounded by record count and logical bytes; saturation
backpressures critical admission instead of dropping it. Successful ID markers are
pruned only after `outbox_delivered_retention`.

Operators inspect node-local state with `GET /manager/webhooks/outbox` and may
requeue an explicit set of 1–100 dead-letter IDs with
`POST /manager/webhooks/outbox/replay`. Replay requires `cluster.webhook:w` when
Manager auth is enabled and never accepts an unbounded “replay all” request.

## Best-Effort Presence Flow

`user.onlinestatus` is mapped to the legacy-compatible status string, admitted to
a bounded batch pool, retried a finite number of times, and then discarded with a
bounded observation. It never enters the critical outbox.

## Observability and Security

Prometheus exposes delivery totals and latency plus outbox backlog, oldest age,
logical bytes, live retry attempts, and dead-letter count. Runtime observations
use bounded event/result labels. `HTTPAddr` may contain Basic Auth userinfo; sender
errors never include the configured URL, credentials, response body, payload, or
chat content.

Webhook delivery remains post-commit and cannot rewrite an already successful
SENDACK. Large offline fanout must continue to enter as bounded recipient chunks.
