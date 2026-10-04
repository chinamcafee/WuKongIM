---
scope: package
summary: Performs durable critical webhook delivery with stable IDs, retry, dead letters and optional batching; presence status is bounded best effort.
---

# Webhook Runtime Flow

## Responsibility

Notify and offline callbacks enter a synchronous WAL outbox with bounded bytes and entries. Backpressure preserves critical events. HTTP completion never changes SENDACK or message durability. Presence status uses a bounded best-effort batch pool.

## Boundaries

The runtime receives already-decided events; it does not own subscriber scans, personal send policy, presence or Channel ordering. Endpoint and JSON compatibility are runtime responsibilities.

## Main Flows

1. Notify/offline admission durably stores each original event ID and body; restart recovers pending records.
2. Workers claim due records, send with bounded timeouts and mark each delivered or failed. Exponential retry ends in retained dead letters; explicit replay keeps IDs.
3. Optional notify batching groups claimed records after a bounded coalescing wait. The default is one item, preserving existing HTTP ID/body contracts. Batch items add stable `event_id`; the request ID binds that exact group. Regrouping can change the group ID, so consumers deduplicate by item ID when enabling batching.
4. Online status stays best effort and retains its legacy JSON shape. Offline notifications preserve the device push policy and existing schema.

## Invariants and Failure Semantics

- Critical delivery is at least once. A successful HTTP response followed by a crash before its marker can be retried; consumers must deduplicate stable source IDs.
- Admission, outbox size, claimed groups and concurrent requests remain bounded.
- Shutdown releases interrupted claims and keeps undelivered work for restart.

## Read First

- [Runtime](runtime.go), [outbox](outbox.go), [mapper](mapper.go), [sender](sender.go)

## Update Triggers

Update when durability, event IDs, batching, retry, replay, offline policy or shutdown semantics change.
