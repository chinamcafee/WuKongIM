# Plugin binding import and independent verification

Date: 2026-09-07. Branch: `codex/v2-v3-migration`.

The migration now converts selected original `PluginUser` records, installs them
through the existing v3 `BindPluginUser` API, and independently verifies their
fields, ownership, replicas and both native lookup directions. No v3 storage or
runtime semantics were changed. This completes the binding data path, not the
full plugin deployment or migration acceptance.

## Mapping and verification

- Source authority remains Slot 0, including agreement between formal replicas
  and validation of both original UID/plugin indexes. Target ownership follows
  the native UID hash-slot layout, independent of source node ownership and of
  the explicit node-to-node plugin configuration assignment.
- UID and plugin number remain exact. Original physical IDs stay archived;
  v3 uses its native composite key. Native timestamps use Unix milliseconds;
  exact original nanoseconds stay archived. Negative instants use floor
  semantics. An update before creation is rejected at original precision.
- Before calling the native upsert, installation rejects any existing binding
  whose fields differ, preventing its timestamp-preserving behavior from hiding
  an unexpected target row. Exact retries remain supported.
- Verification reads expected fields directly from selected original rows, not
  converted rows or importer counters. It uses the native exact and UID-existence
  reads plus a 128-row reverse-index scan per hash-slot/plugin group. A disk ledger
  bounds memory; it detects missing, duplicate, unexpected and changed results.
  Counts cover all native primary tables and nodes, followed by bootstrap checks.
- The native index orders strings by byte length then content. Pagination tests
  deliberately cross different UID lengths whose textual order is reversed.

## Validation

The original public-API binding fixture provides exact PluginUser columns and
both index keys. Tests insert these into private copies of stopped server
fixtures; extra identities/timestamps and cleared optional message extensions
are synthetic derivatives, not whole-source API acceptance evidence.

- Archive export, source-directory removal, archive reconstruction, installation,
  exact retry and independent verification pass for single-node cluster to
  single-node cluster, single-node to three-node cluster, three-node to
  single-node cluster and three-node to three-node cluster. Tests include one
  replica across three target nodes and three replicas across three nodes.
- Native UID reads preserve positive and negative timestamp expectations after
  reopening. A 260-binding group crosses three reverse-index pages.
- Fault injection in the inspector rejects missing/changed primary results and
  missing, duplicate, changed-timestamp or wrong-plugin reverse results even
  with unchanged target files and counts.
- Direct mutations in private native stores test removal, changed timestamps,
  changed plugin/UID, an extra binding and a copy on a non-owner node. Independent
  verification rejects each mutation. Unaffected nodes are inspected read-only.
- Native key-size limits and sub-millisecond backwards timestamps are tested.
  Existing global plugin business/configuration rejection tests still pass.
- The six migration packages pass their regression suite. Final focused
  storage-mutation coverage is recorded separately after improving its fixture
  to avoid opening unaffected nodes for writes.
- A byte comparison of 275 existing native files against
  `ab1ced72450658ba85399b03293ac37c20278721`, including the native plugin binding
  table and key definitions, finds no differences.

## Captured real-source component check

A bounded probe reads only the PluginUser key range from the previously verified
local capture, without reading or changing the live source or target server.

- Capture digest:
  `3780b1757dbba3b6e46bf2c750bcaec1c09d7b65d974f586d365e2f893d5a896`.
- Source nodes 1001/1002/1003 each contain one binding and both original indexes.
  Captured Slot 0 agrees on leader 1001 and these three formal replicas.
- Every binding's complete original field digest equals the prior audit:
  `62ddf9a60db1d99a6c79844ef59b426e94a226295ff32e7cab3d9678d45d4967`.
- The one logical binding was written to a private native metadata probe store,
  closed and reopened. Exact, UID and plugin-to-user reads match the expected
  fields. Neither the UID nor configuration values are printed in the report.

Private evidence: `tmp/server-rehearsal-20260907/plugin-31/`, including
`binding-probe.json`, helper source, test logs and SHA-256 inventory. The probe
is a binding component check; it does not bypass the full prepare gate or create
a full-source verified report.

## Remaining scope

Active plugin methods/configuration still block full source preparation until
runtime behavior and deployment are connected to a verified compatibility
mapping. The old `User.PluginNo` field is not substituted for actual PluginUser
bindings and still blocks if populated. Earlier isolated executable tests and
node-local configuration assignments remain separate evidence.

No live target import, plugin deployment or cutover occurred. Message-format,
history recovery, client cache and large-scale performance acceptance remain
separate unresolved items; `cutover_ready` remains false.
