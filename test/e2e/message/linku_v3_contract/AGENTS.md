# Link-U v3 contract AGENTS

This scenario owns the process-level Link-U compatibility contract for the
WuKongIM v3 server.

## Contract

- Run only against real `cmd/wukongim` child processes through `test/e2e/suite`.
- Require WKProto v6 and Gateway token authentication on every node.
- Cover the exact Link-U HTTP response families, lossless message sequence
  fields, public liveness/readiness, and durable webhook delivery metadata.
- Keep both single-node-cluster and static three-node-cluster gates in this
  package.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/linku_v3_contract -count=1 -timeout 4m -p=1
```
