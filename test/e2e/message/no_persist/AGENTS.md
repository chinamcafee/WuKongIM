# Ordinary non-persistent delivery

Prove ordinary `no_persist=1, sync_once=0` HTTP sends reach online WKProto
subscribers in single-node and three-node clusters with 256 Hash Slots.
Use real processes and public APIs. Assert transient IDs, zero sequences,
receive flags, permission rejection, and unchanged committed history.

Run: `GOWORK=off go test -tags=e2e ./test/e2e/message/no_persist -count=1 -timeout 3m -p=1`
