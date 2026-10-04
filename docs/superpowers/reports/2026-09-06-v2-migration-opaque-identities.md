# v2 migration opaque identity validation

The migration tool now preserves original `Conversation` UID/channel bytes and
`ChannelClusterConfig` channel bytes in versioned internal state. Previously,
ordinary JSON encoding would replace invalid UTF-8 and merge distinct identities;
the reader rejected these records to avoid that loss. Source columns remain raw
bytes in the archive. Catalog joins, selected references, conversion records and
independent field comparisons now use exact-byte encoding. Fresh workspaces and
archives are required after updating the tool.

Validated locally:

- Related migration package tests passed.
- Two distinct invalid-byte channel IDs and an invalid-byte conversation UID
  survived prepare, portable export/import, independent verification, native
  database reopen, Slot snapshot recovery and another reopen.
- Independent verification rejected another invalid byte returned at the same
  key, even though ordinary JSON would render both values identically.
- Native single-node cluster restart and three-node channel replica recovery
  integration tests passed.
- Stream-only exclusion still rejects invalid identity hashes/indexes, empty
  IDs, zero channel types and zero source configuration versions.

A bounded read-only source diagnostic found four opaque records per replica with
identical raw fingerprints across three replicas. All four identities round-trip
exactly; both conversation business records also round-trip exactly. Two channel
configurations still fail the existing authority check because `ConfVersion=0`.
Three separate empty-ID/type-0 records per replica remain rejected. No records
were newly excluded, and plugin methods/configuration/bindings still require a
verified compatibility mapping.

The diagnostic completed in 18.127 seconds. Metadata of 601 source files remained
unchanged, and the existing three target containers stayed healthy. No real-data
import or cutover occurred. This report does not certify full source preflight,
public API/SDK equivalence or the deferred 100 GiB / four-hour performance target.

## Subsequent original-code validation

The later original public DB/Raft harness showed that zero ConfVersion is valid
at initialization. The previous two authority errors were caused by an overly
strict migration check, not proof that those records were uninitialized. The
reader now preserves zero versions and nonempty type-zero source control IDs.
Three-source/three-target preparation, import and independent verification passed;
a version mismatch on one source replica still fails. Empty IDs and zero-type
business rows remain rejected. See the [channel-semantics follow-up](2026-09-06-v2-migration-channel-semantics.md).
