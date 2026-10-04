# Plugin migration runtime acceptance and configuration decision

Date: 2026-09-07. Worktree: `codex/v2-v3-migration`.

The isolated three-node v3 test now joins original captured PluginUser bindings,
the audited original executable, native configuration files, real cluster
binding queries, normal plugin-origin message sends, automatic offline Receive,
and a complete stop/start cycle. Both tested configuration candidates pass the
bounded message checks. However, preserving three distinct node-local names does
not preserve a single reply name for the bound user. The production compatibility
gate remains closed pending complete acceptance. The user subsequently approved
uniform source-1001 configuration for `wk.plugin.ai-example`; implementation and
source-derived configuration checks are recorded in
[the config policy report](2026-09-07-v2-migration-plugin-config-policy.md).
The candidate run's private evidence retains its original pre-approval status.

## Inputs and isolation

- Source code remains pinned to
  `a888f89533d0e7d1b2030e06504ca97f1ad891d4`.
- Parent capture digest:
  `3780b1757dbba3b6e46bf2c750bcaec1c09d7b65d974f586d365e2f893d5a896`.
- The private component fixture contains exactly nine captured PluginUser rows:
  one primary and two indexes on each original node. All three primary field
  digests match
  `62ddf9a60db1d99a6c79844ef59b426e94a226295ff32e7cab3d9678d45d4967`.
- The component uses original source snapshots to check topology and Slot
  progress, validates original indexes, selects formal copies, converts one
  logical binding, installs three native replicas and independently verifies
  them before starting the target cluster. The component capture digest is
  deliberately distinct from the full-source capture and is not a full-source
  PREPARED or cutover certificate. No Plugin registration/configuration gate
  is skipped in the production preparation workflow.
- Original executable SHA-256:
  `671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224`.
  The test copies these unchanged bytes to `wk.plugin.ai-example.wkp`; v3's
  default scanner derives the identity from the filename. The old
  `-linux-amd64` suffix is not part of the registered plugin number.
- An ephemeral Docker container on the existing test host has no external
  network, a read-only root, read-only input mounts, no Linux capabilities,
  two CPU quota, 1536 MiB memory and a 512 MiB executable temporary filesystem.
  Three app nodes communicate only over its loopback interface. Program,
  configuration and binding files are private; no actual UID or config values
  are included here.

## Results

Test: `TestMigrationPluginBindingsInNativeThreeNodeCluster`, under the
`integration` build tag. Both rounds query the imported binding through all
three actual cluster nodes and start the plugin through normal application
wiring and the default executable scanner.

Each configuration candidate passes:

1. Six direct offline hook cases per round: personal and group messages invoked
   on each node, with real cluster binding reads and actual committed replies.
2. Six ordinary message-send cases per round: personal and group sends entering
   each node automatically trigger offline Receive; the plugin's reply returns
   through the normal message usecase and is durably readable.
3. Full cluster shutdown and restart, then the same binding/configuration checks
   and twelve additional cases on fresh channels.

That is 24 committed plugin replies per candidate, including twelve automatic
post-commit cases. This does not qualify recovery of the earlier channels'
complete history; the separately recorded native historical-recovery blocker
remains open. Gateway/SDK delivery, online-client states, failure failover,
arbitrary plugin methods and production traffic are outside this bounded test.

The original per-node configuration candidate passes in 27.70 seconds. Its twelve
automatic replies use all three configuration names: target configuration 1
appears three times, configuration 2 six times, and configuration 3 three times.
Different channels use different executor nodes even for the same bound UID.

The uniform source-1001 candidate passes in 27.39 seconds. All twelve automatic
replies match source 1001's configured name. The candidate changes only each
target's effective `config` value; each target's original desired-state identity,
enabled flag and timestamps are preserved. The approved original assignments
and original source data remain intact.

Focused app plugin/offline-observer unit tests also pass. Initial harness runs
identified two environment issues: Docker's default temporary mount prevented
executing staged files, and a zero-value app config omitted Delivery. Explicit
temporary execution permission and the normal product delivery setting resolve
these without changing server runtime or storage code.

## Why the configuration decision is material

Pinned original sources establish the following path:

- `internal/manager/manager_tag.go:calcUsersInNode` assigns users to their UID
  Slot leaders.
- `internal/channel/handler/event_distribute.go:distributeByTag` forwards to
  those nodes, then queues offline events for their recipients.
- `internal/pusher/handler/event_pushoffline.go:pushOffline` invokes the bound
  plugin through `processAIPush` on that node.

The captured binding user's UID hashes to source Slot 36, whose leader is 1001.
Thus source 1001 is the expected executor under that captured, stable topology.
This is a code-and-snapshot inference, not a replay of the stopped production
server or a claim that every historical reply always used the same node.
PluginUser storage itself belongs to Slot 0; that separate storage authority
must not be confused with Receive execution affinity.

The unchanged v3 delivery runtime emits offline effects from the process
executing the delivery plan. The observed mixed-name replies confirm the effect
of this difference for this exact plugin, which embeds `config.name` in its
reply payload. Earlier approval of 1001→1, 1002→2, 1003→3 preserved node-local
configuration, but did not settle this newly demonstrated reply-content change.

The user subsequently approved source 1001's effective config on all three
targets for this plugin. It changes no v3 storage or scheduling logic. The
`plugin_configs` implementation and new plan are documented in the config policy
report; the private candidate-run evidence here remains a historical record.

## Evidence and remaining work

Private evidence lives in `tmp/server-rehearsal-20260907/plugin-32/`: binding
fixture, test binaries and sources, assigned/uniform candidate inputs, complete
run logs, source-code excerpts/digests, checksums and final host state.

No source backup or live target was imported, started, stopped or reconfigured.
The test container was removed. Full-source plugin preparation, final executable
and configuration installation, remaining source business compatibility gaps,
historical recovery, SDK cache transition and 100 GiB performance acceptance are
not certified. `cutover_ready` remains false.

## 2026-09-08 follow-up

The single-node and three-node cluster tests now verify every pre-restart
message and plugin reply through every node before new sends, across two full
restarts. Both pass with the approved source-1001 config. See
[the recovery acceptance report](2026-09-08-v2-migration-plugin-recovery.md).
This supersedes only the earlier component historical-recovery limitation;
complete source preparation, executable packaging and SDK acceptance remain
separate gates. The original evidence above is unchanged.
