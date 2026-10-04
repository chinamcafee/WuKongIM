# channel_failover AGENTS

This file is for agents working inside
`test/e2e/message/channel_failover`.

## Scenario Purpose

This scenario proves a static three-Controller-voter `cmd/wukongim` cluster can
keep Channel quorum-acknowledged messages after one Channel leader node stops,
automatically fail over affected channels through durable migration tasks, and
fail closed for new Channel placement while the configured replica count
cannot be satisfied. Its follower-repair path adds one data-only spare, proves
the repaired spare enters the public Channel replicas and ISR while preserving
`min_isr=2`, restores the replaced source as a Controller voter, and then proves
the repaired spare can carry Channel quorum after another original replica
stops. The process-pause case retains TCP connections in a ten-Slot cluster,
limits scans to one page per tick, and distinguishes writes before process recovery from history safety after
recovery. The strict availability gate sends immediately after ACK and leader
pause while the trailing replica may be behind, then checks multiple successful
writes and an exact cross-ingress ClientMsgNo retry before resuming the process.
After resume, send through the former leader itself to exercise stale route recovery.
The strict case also pages all acknowledged history with a two-message limit,
using only More and the oldest visible sequence to cross hidden recovery barriers.
Do not weaken this gate or treat the safety case as proof of availability.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 6m -p=1
```

## Maintenance Rules

- Keep the scenario black-box: use real `wukongim` child processes and public
  HTTP/manager APIs.
- Keep health and migration intervals short through per-node config overrides.
  The pause case waits one bounded health TTL before sending because remote
  node inventory may itself wait on the paused peer. Resume the exact owned
  process in cleanup before cluster teardown.
- HTTP-only scenarios use `WaitHTTPReady`; they do not use an unauthenticated
  WKProto readiness handshake or disable client token authentication.
- Do not inspect internal stores in this scenario; use manager message and
  Slot list surfaces for recovery assertions.
- Before stopping a second original Channel replica, restart the replaced
  source and assert through public manager state that all three original
  Controller voters are healthy and schedulable. In the same node-inventory
  snapshot, node 4 must remain data-only, fresh, alive, runtime-ready, and
  schedulable; promoting it does not preserve a majority after two original
  voters stop.

- The Channel failover fixture uses 256 Hash Slots and explicitly disables Token authentication for its existing tokenless readiness client. SDK acceptance keeps Token authentication enabled.
