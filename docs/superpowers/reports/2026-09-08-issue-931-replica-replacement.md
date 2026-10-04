# Issue 931: Channel follower replacement

## Reproduction and diagnosis

Source: `93633f2db907f36065f976ba1576de073429d923` (merged PR #930).
The original four-process scenario reproduced the reported failure in 23.63 seconds:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover \
  -run '^TestChannelFollowerReplicaRepairAfterNodeKill$' -count=1 -timeout=3m -p=1 -v
```

The task became `blocked` at `final_target_catch_up/target_lagging`.
Allowing another scan alone did not repair it: the unchanged 45-second scenario
wait expired while the task remained running. Bounded diagnostic probes showed
target node 4 staying at LEO/HW/checkpoint 0 against a cutover LEO/HW of 3.
Those temporary probes were removed after diagnosis.

Three interacting causes were confirmed:

1. A lagging target was permanently blocked, and the executor excludes blocked tasks.
2. Durable-quorum authority contained only ISR voters. A replacement learner
   was absent from durable replication, while the legacy follower pull path is
   disabled when the durable quorum log is configured.
3. Quorum exchanges update storage independently of a loaded follower reactor;
   migration probes could continue reading its old activation frontier.

## Repair and safety boundaries

- Project non-ISR replicas as learners separately from ISR voters. After an
  authority install proves recovery and its barrier on the voter quorum,
  schedule learner copies through the existing fixed repair workers. The
  integrated main implementation also schedules copies after every commit.
- Copy immutable proposal pages with the proven committed frontier. Retain
  exact page progress between bounded worker turns, fenced to the task entry
  and gap version. Pure tail growth preserves completed pages; new gap evidence
  invalidates them, and full-version checks prevent premature completion.
  Membership changes cancel superseded work. The ledger allows
  the configured voter count plus one temporary replacement replica per Channel.
- Learners never participate in recovery voting or write quorum counting.
  Metadata still promotes the learner only after the existing cutover and
  membership guards pass; `min_isr=2` is unchanged.
- Refresh migration follower probes from consistent exact storage and recheck
  metadata and runtime identity, epochs, role, status and write fence after
  the read. Reject a durable authority ahead of the observed metadata, including
  its fence version. Use the shared ReplicaStore adapter and handle lifecycle.
- Keep transient final catch-up lag runnable without writing the same task
  back to Slot Raft each scan. Invalid runtime evidence still blocks.

## Validation

The original four-node scenario passed after repair. The complete Channel
failover package also passed, including leader loss, fail-closed new placement,
and follower replacement followed by another original follower loss. Its final
run completed in 82.946 seconds; the four-node case took 28.46 seconds.
The E2E assertions and timeouts were not weakened.

Focused race-enabled regression coverage passed for:

- 257 historical entries plus a committed authority barrier copied over
  multiple pages without requiring a subsequent business SEND;
- learner promotion followed by successful quorum commit with the old
  followers unavailable;
- rejection of a leader-plus-learner topology without a voter quorum;
- exact work-generation fencing of retained repair cursors;
- loaded follower progress refresh and rejection of mixed runtime/durable authority;
- repeated lagging probes retaining their cutover proof until committed catch-up.

The related Channel/Cluster unit and race suites passed. Final repository-wide
unit and hosted check receipts are recorded in the associated PR so later
receipt updates do not change the tested code revision. FLOW contracts passed
with only the pre-existing advisory line-count warning in `internal/access/api`.

## Scope

This is a local process-level recovery qualification, not a release or a
production capacity/SLO qualification. Already persisted blocked tasks are not
automatically reclassified by this repair; operator-controlled abort/recreate
remains separate from ordinary runnable-task execution.

## Integration review on current main

Main `c36a77c89cee10f1b87fa5f83d35d4cd888b62ef` independently added learner
replication and authoritative repair activation in PR #933. Integration retains
those owners, per-commit learner scheduling, native recovery safety, and the
expanded process-pause/history scenarios. It removes the duplicate Node probe
path from the original candidate.

Two deterministic regressions failed before the integration fixes: concurrent
commits discarded completed page cursors, and native repair probes accepted
mixed runtime/durable authority. The cursor now distinguishes tail growth from
new gap evidence; probe checks share the existing Channels service owner.
Final integrated-head validation and review receipts are recorded in PR #932.
