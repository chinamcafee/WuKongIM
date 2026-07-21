# gateway_token_auth AGENTS

This scenario owns black-box Gateway token authentication coverage.

## Contract

- Start real `cmd/wukongim` processes through `test/e2e/suite`.
- Enable `WK_GATEWAY_TOKEN_AUTH_ENABLED` explicitly on every scenario node.
- Register and rotate credentials only through public `/user/token` HTTP calls.
- Authenticate only through public WKProto CONNECT traffic.
- Cover both a single-node cluster and a routed static three-node cluster.
- Assert fail-closed rejection without inspecting internal storage or token logs.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/gateway_token_auth -count=1 -timeout 3m -p=1
```
