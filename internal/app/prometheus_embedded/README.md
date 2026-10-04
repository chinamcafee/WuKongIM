This directory stages Prometheus binaries before building cmd/wukongim.
The Dockerfile verifies the fixed upstream source archive selected by
docker/prometheus/toolchain.env, applies only its explicitly pinned dependency
security updates, and cross-compiles for Linux amd64 or arm64 with the pinned
Go builder. The custom version suffix identifies the dependency-patched build.
The final image also carries the upstream license and notice. This managed
process serves the metrics/query API used by Manager; the separate upstream
Prometheus web UI assets are not built. Local development scripts may build
assets from source instead.

Generated files are named prometheus-<goos>-<goarch> and are embedded into the
final wukongim binary through go:embed. They are intentionally ignored by git.
Docker also ignores developer-generated assets, so a local file cannot replace
the verified build input. Enabling prometheus with an empty binary_path extracts
the matching asset under the configured Prometheus data directory and starts it
as an app-managed child process.
