# Preparation command scenario

This black-box scenario verifies opaque Link-U preparation events through a real
single-node cluster. Use synthetic users and the standard binary only; never
connect to the developer's active business instance or use real credentials.

Cover authenticated device flags 0 and 2, independent v3 sync/ACK cursors,
restart before ACK, stable message deduplication, and silent conversation isolation.
Messages expose source channel IDs; ACK receipts retain the command suffix.
Keep signing helpers scenario-local and import no internal product packages.

Run: `GOWORK=off go test -tags=e2e ./test/e2e/message/preparation_cmd -count=1 -timeout 2m -p=1`
Set `WK_E2E_BINARY` to the standard prebuilt binary when testing its exact behavior.
