# Approved plugin configuration policy

Date: 2026-09-07. Worktree: `codex/v2-v3-migration`.

The user approved using source node 1001's effective configuration for
`wk.plugin.ai-example` on all three target nodes. The tool now represents this
choice explicitly, preserves every original registration/configuration, imports
native desired-state files, and verifies those files against original captured
rows. This completes configuration handling for the approved choice; it is not
full-source preparation, plugin executable deployment, or production cutover.

## Plan and behavior

The existing `plugin_nodes` assignments remain 1001→1, 1002→2, 1003→3. Add:

```json
"plugin_configs": [
  {"plugin_no": "wk.plugin.ai-example", "source_node": 1001}
]
```

- Only this plugin's effective Config changes. Each target keeps its originally
  mapped node's enabled state and exact desired-state timestamps. Unlisted
  plugins keep their mapped source configuration.
- The selected source and the plugin must exist; the plugin must be registered
  on every mapped source node. Duplicate, unknown, unsafe or incomplete choices
  fail. The existing total node bijection remains required for plugin settings.
- Prepare and archive reconstruction derive assignments from original rows.
  Private records retain the original fields, source row key/digest, and the
  selected configuration's source key/digest. Reports contain no config values.
- The policy changes the plan digest. Old workspaces/archives cannot be reused
  as artifacts of the new plan.
- Import calls the unchanged native plugin Store under `data_dir/plugin-state`
  before publishing the generation fingerprint. The config report participates
  in generation identity. Exact retries preserve files; changed files are
  rejected, including an interrupted generation without a READY checkpoint.
- Offline verification decodes original Plugin rows again and applies the plan
  independently of converted settings. It checks JSON without float64 rounding,
  enabled flags, timestamps, missing/extra files and counts. Bounded directory
  enumeration rejects symlinks, corrupt files and unexpected entries. Evidence
  is ordered in the disk workspace so its digest is reproducible.
- The CLI wires both native import and source-derived verification. The
  `plugin_settings` verification report contains its own source-bound digest.
  Its success never sets `cutover_ready`.

## Validation

The complete migration package regression passed:

```sh
GOWORK=off go test ./internal/usecase/migration ./internal/infra/migrationv2 \
  ./internal/infra/migrationv3 ./internal/app/migration \
  ./internal/access/migratecli ./cmd/wkmigrate -count=1
```

Measured package times: usecase 6.449 s, original-v2 adapter 110.194 s, native-v3
adapter 11.194 s. The app, entry adapter and command compile successfully.
Subsequent focused tests passed for CLI composition/config policy (2.530 s) and
interrupted native config import (3.104 s). The Linux amd64 CLI also builds
successfully with `CGO_ENABLED=0`.
The regression includes:

- Three synthetic source-node configurations derived from original public-API
  columns, explicit reversed target mapping, another unaffected plugin, distinct
  enabled flags/timestamps and a JSON integer above float64's exact range.
- Original raw rows/configs and selected-source provenance remain intact.
  Missing choices/registrations, duplicate choices and invalid identities fail.
- Verification succeeds without reading converted settings. Changed config,
  large integer, enabled state, timestamp, missing state and extra state fail.
- Descriptor-only original fixtures complete prepare → export → source unmount
  → fresh archive rebuild → native import → retry → independent verification.
  The CLI also performs this path and rejects a subsequently missing config.
- Actual native file corruption, changed values, missing/extra files and symlinks
  fail. A changed configuration in an incomplete import is not overwritten;
  restoring the exact input permits recovery and verification.
- Original active methods/nonempty-config fixtures still fail the full business
  compatibility gate even when an explicit config source is supplied.

The configuration fixtures are component/descriptor tests. They do not establish
that an active real-source plugin now passes the complete preparation gate.

## Real captured configuration check

Private artifacts: `tmp/server-rehearsal-20260907/plugin-33/`.

The probe reused exactly three captured original Plugin primary rows, checking
all row SHA-256 values against the previous full capture's assignment evidence.
It created a separately identified component workspace, generated settings with
the approved policy, round-tripped them through the native Store, and verified
values independently from those raw rows. Its inspector reads private config
artifacts only; it does not attest a target database or running cluster.

- Parent full capture:
  `3780b1757dbba3b6e46bf2c750bcaec1c09d7b65d974f586d365e2f893d5a896`.
- Component identity:
  `32e93dc452f75db4cf6d5f1b40c495e21820a6c03b3c61794ea941a576e35438`.
- New plan digest:
  `9a793e3dbba8d5d2f958235d61fff15da10362c2981ce115db5316688818a344`.
- Configuration assignment digest:
  `f9c8eb6a4bfe9f77b9bbcc84c49e14bae535e470bae997c0c8d5e75dced9e771`.
- Independent configuration verification digest:
  `af083984007a9cce2761847da3e04a3c3894a96bce0ac3f0fa32de791c1cadca`.
- All three effective configurations have SHA-256
  `7cae637b161fc5057487e38b8bc39277b683cb1c3c6b8c366a2a21a97c47616d`.

Each generated native file is byte-identical to its respective source-1001
candidate file used by the prior successful three-node/restart runtime test.
That previous runtime evidence is reused; no new runtime pass is claimed here.
The three original row digests and configurations remain distinct and intact.

All 275 previously tracked native files are still byte-identical to baseline
`ab1ced72450658ba85399b03293ac37c20278721`. This change adds no v3 storage,
message recovery, plugin scheduling or runtime adaptation. No production source
or target directory was written, and no live import or cutover was performed.

## Remaining work

The complete active-plugin compatibility gate remains closed: audited executable
packaging, installation and runtime acceptance still need to be connected to the
production migration workflow. The full real-source run has not advanced beyond
that gate. Message format gaps (including RedDot and retained stream-main
fields), historical conversation visibility, the independently reproduced native
history recovery issue, SDK cache cutover, and the deferred 100 GiB / four-hour
acceptance remain separate unresolved items. Configuration success is not an
exception to any of those checks.
