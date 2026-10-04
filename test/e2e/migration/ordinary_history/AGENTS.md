# Original ordinary-history migration

Use only the unchanged original v2 server fixtures and their recorded HTTP
requests/acknowledgements. Do not rewrite source storage or import conversion
internals. Cover successful migration across source/target node counts, original
credentials and history, sequential appends, idempotency, full restart and
Channel Leader failover through the real CLI, HTTP and WKProto interfaces.
