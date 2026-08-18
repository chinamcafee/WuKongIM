# internal/usecase/user Flow

## Responsibility

`internal/usecase/user` coordinates legacy-compatible user token, device quit,
online-status, and system UID operations without depending on HTTP, gateway
frames, or concrete cluster types.

## Flow

```text
/user/token
  -> validate UID/token/device fields
  -> create UID metadata when missing
  -> upsert per-device token metadata
  -> for master-device updates, schedule owner-local same-device session close

/user/device_quit
  -> read selected device metadata
  -> clear stored token
  -> schedule owner-local matching-device session close

/user/onlinestatus
  -> prefer authoritative presence route lookup when configured
  -> return one legacy online item for each active route

/user/systemuids*
  -> persist reserved system UIDs through the configured system UID store
  -> maintain the process-local cache used by callers that need fast checks

restore resume
  -> use the optional restore-only system UID read port
  -> replace the complete process-local cache before foreground admission
```

The usecase treats a single node as a single-node cluster. Durable metadata
access happens through injected ports supplied by `internal/app`. The
restore-only read is selected only by `ReloadSystemUIDCache`; ordinary user
operations continue through the foreground-fenced store methods.

Internal device credential apply/revoke is item-wise and version fenced. The
usecase fixes APP/PC to MASTER, allow-lists operation/cause pairs, derives an
operation digest without logging plaintext tokens, persists ACTIVE or REVOKED
tombstones through Slot CAS, and always advances/reconciles Presence for both
APPLIED and IDEMPOTENT results. Durable credential outcome and route outcome
are independent response axes.
