# Migration E2E

Migration scenarios run the real `cmd/wkcli` CLI and, after successful
verification, real `cmd/wukongim` processes. Unsupported input must fail before
target creation; do not claim a refusal scenario proves successful migration.
Use stopped original-release fixtures and public HTTP/WKProto observations.
Never import app, use cases, or database internals.
