# bench_churn AGENTS

This scenario proves that wkbench identity-swap churn keeps group membership
aligned with the replacement connection in an authenticated three-node cluster.
It also proves each Bench Token creation or rotation is visible on the first
CONNECT through every node, including a non-replica. Use two Slot replicas and
256 hash slots, require exact AuthFail for incorrect or replaced Tokens, keep
Gateway Token authentication enabled, and use all-node HTTP readiness.

Run it with:

```sh
GOWORK=off go test -tags=e2e ./test/e2e/message/bench_churn -count=1 -timeout 2m
```

The test must use a real `cmd/wukongim` subprocess and real WKProto sessions.
Keep the identity pool and group small; this is a correctness regression, not a
load test.
