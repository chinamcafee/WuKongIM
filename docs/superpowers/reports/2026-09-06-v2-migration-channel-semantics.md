# Original v2 channel semantics follow-up

The migration reader previously treated `ConfVersion=0` as uninitialized and
rejected every zero channel type. Original-code observations disproved those
blanket rules for source control configuration. The fix preserves the exact
source values; it does not replace zero with a fabricated version or type.

## Evidence from unmodified v2

The external [caller](../../../internal/infra/migrationv2/testdata/channel_semantics.go.txt)
ran against source `a888f89533d0e7d1b2030e06504ca97f1ad891d4`. All 377 `pkg` Go
and module files were byte-checked against that revision before execution. The
caller created only synthetic data in a fresh temporary directory and reopened
it before collecting [observations](../../../internal/infra/migrationv2/testdata/original-v2-channel-semantics.json).

- `SaveChannelClusterConfig` / `GetChannelClusterConfig` retain version zero.
- Original Raft ConfChange accepts version zero as its initial configuration.
  A later attempted downgrade from version five leaves version five unchanged;
  Step logs the rejection but returns nil, so the state must be checked.
- A nonempty channel ID with control `ChannelType=0` survives reopen.
- Empty ChannelInfo is considered empty by `IsEmptyChannelInfo` and absent by
  `ExistChannel`, but management SearchChannels still returns its physical row.
- Empty ChannelClusterConfig gives not-found on a reopened point read, but
  GetChannelClusterConfigs lists its physical row. It cannot simply be dropped.

These observations establish format/read semantics, not the history of a real
production write or a substitute for authoritative replica comparison.

## Change and validation

The reader accepts zero configuration counters and nonempty type-zero source
control identities. Owner Slot authority and full replica comparisons still
apply, including the exact version bytes. Missing version bytes, invalid Leader,
zero term, unfinished transitions and conflicting replicas continue to fail.
Messages, policies and conversations still reject zero channel types. Empty IDs
remain unsupported; no additional business exclusion was added.

Validation passed:

- Three original source replicas with matching zero configuration versions:
  prepare, native three-node target import and independent verification.
- Only one source replica changed to zero: rejected as a replica conflict, no
  successful preparation marker or target directories.
- Nonempty type-zero source configuration: original identity retained through
  selection, native import and independent verification of all business rows.
- Missing scalar/Leader/replicas and unfinished-election negative cases.
- All related migration packages, native single-node restart and three-node
  channel replica recovery integration tests.

## Read-only server diagnostic 08

The diagnostic finished in 18.276 seconds. Of the seven previously identified
special rows on each of the three replicas, five now pass identity and source
control decoding and exact-byte state round trips. Their raw fingerprints match
across replicas. The remaining two are the empty-ID ChannelInfo and empty-ID
ChannelClusterConfig; both remain refused.

All 601 source files retained their recorded metadata, and the existing three v3
containers stayed healthy. No real import, cutover or new full prepare occurred.
The diagnostic is not proof that the whole source passes migration selection.
Plugin methods/configuration/bindings still need a verified compatibility mapping.
The only approved business exclusion remains historical Stream/StreamMeta; their
15 parent messages are still included. The 100 GiB / four-hour acceptance remains
pending a supplied test environment.
