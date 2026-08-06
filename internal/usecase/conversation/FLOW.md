# internal/usecase/conversation Flow

## Responsibility

`internal/usecase/conversation` owns entry-agnostic conversation list reads,
legacy-compatible conversation sync selection, legacy-compatible read/delete
mutations, and the lightweight UID-authority active patch contract. It does not
depend on gateway frames, HTTP DTOs, cluster, or channel log runtimes.
Storage is supplied through small ports for UID-owned conversation active
pages, durable UID-owned state rows, durable UID-owned state/delete writes, and
channel-owned message reads.

## List Flow

```text
List(uid, cursor, limit)
  -> scan UID-owned normal-kind conversation active index using (active_at, channel_id, channel_type) cursor
  -> keep the returned page order exactly as storage emits it
  -> build current-page ordinary last-message requests with visible_after_seq = deleted_to_seq
  -> batch-read newest non-CMD visible messages from each channel-owned message log
  -> keep conversation rows whose channel has no visible last message
  -> scan the committed interval (max(read_seq, deleted_to_seq), last_message_seq]
  -> count only ordinary red-dot messages whose sender differs from uid
  -> clone payloads before returning
```

The usecase no longer scans `user_channel_membership` or reads
`channel_latest` for the active list path. `conversation.active_at` is the
authoritative ordering anchor; last-message time is display data only and does
not reorder sparse or dense rows. `SparseActive=true` rows therefore stay in
the active page position chosen by their UID-owned row even when the channel log
contains a newer message.

Rows without a visible last message are returned with `LastMessage=nil` and
`Unread=0`. List reads do not delete, hide, or repair conversation rows.

## Sync Flow

```text
Sync(uid, last_msg_seqs, msg_count, only_unread, excluded_types, limit)
  -> scan the bounded UID-owned normal-kind active view from the beginning
  -> merge client-known last_msg_seqs as overlay candidates
  -> read durable UID-owned normal-kind rows for overlay candidates when present
  -> skip excluded channel types
  -> batch-read newest non-CMD channel messages for all candidate keys
  -> hide rows whose newest message is at or below deleted_to_seq
  -> scan each committed interval above max(read_seq, deleted_to_seq)
  -> count only ordinary red-dot messages whose sender differs from uid
  -> apply only_unread to that exact count
  -> sort by newest message time, channel type, then channel id
  -> trim to the final limit
  -> load recent messages only for the final returned window when msg_count > 0
```

Sync returns canonical channel IDs; access adapters convert person-channel IDs
back to peer IDs for legacy HTTP clients. `version` is accepted for compatibility
but does not trigger a historical directory scan. `last_msg_seqs` discovers
client-known conversations that are outside the active scan window; overlay
candidates without durable user state are returned only when the channel latest
ordinary message is newer than the client-supplied sequence. Recent messages
are filtered by the row delete floor, exclude CMD/SyncOnce messages, and are
cloned before returning.

Unread is not a sequence gap: silent `RedDot=false` entries, SyncOnce/CMD
entries, sender-owned messages, delete-covered rows, and sequence gaps do not
contribute. The cluster adapter pages committed channel logs and bounds parallel
channel scans with the same conversation-read concurrency option. This exact
scan is the correctness baseline; a future rebuildable red-dot ordinal can
replace it if profiling shows large unread intervals need constant-time reads.

## Mutation Flow

```text
ClearUnread(uid, channel, optional message_seq)
  -> read newest channel-owned visible message
  -> when message_seq > 0, clamp it to the newest visible sequence
  -> when message_seq = 0, retain legacy clear-through-latest behavior
  -> monotonically upsert UID-owned ReadSeq to that exact target

SetUnread(uid, channel, unread)
  -> read newest channel-owned visible message
  -> derive ReadSeq so at most unread messages remain unread
  -> upsert UID-owned ReadSeq

DeleteConversation(uid, channel, optional message_seq)
  -> use message_seq or read the newest channel-owned visible message
  -> write a UID-owned normal-kind delete barrier through HideConversations
  -> durable metadata clears active_at while preserving delete visibility floor
```

Mutation APIs keep personal-channel normalization in access adapters and accept
only normalized `ChannelID` values here. They do not scan active lists or repair
rows. Missing latest messages make clear/set unread no-ops, while delete
requires a concrete delete barrier and returns an error when neither the request
nor the message store can provide one.

## Authority Active Patch Contract

```text
recipient authority processor
  -> build one ActivePatch per effective recipient
  -> group patches by UID hash-slot authority
  -> target authority admits patches into its bounded active cache
  -> cache flushes ConversationActivePatch rows through cluster Slot ownership
```

`ActivePatch` carries the UID-owned row key, membership visibility floors,
message sequence fence, active timestamp, and explicit `SparseActive` mode.
The conversation package does not classify channel membership or decide sender
vs. recipient fanout. Those decisions belong to the recipient-authority
processor and dispatcher, so list reads observe only authoritative UID-owned
rows.

## Cursor Contract

The cursor is based on the active index row emitted by storage:

```text
(ActiveAt, ChannelID, ChannelType)
```

This matches the UID-owned active index order:

```text
active_at desc
channel_id asc
channel_type asc
```

Last message sequence is intentionally absent from the cursor because message
log tails do not participate in pagination order.
