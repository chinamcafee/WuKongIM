#!/usr/bin/env bash
set -euo pipefail

# Exercise the shipped binary, without a host executable, network access, or
# binary_path override. The same probe gates local tests and image publication.
[[ $# -eq 2 ]] || { echo 'usage: verify-docker-prometheus.sh IMAGE linux/amd64|linux/arm64' >&2; exit 2; }
image="$1"
platform="$2"
case "$platform" in linux/amd64|linux/arm64) ;; *) echo "unsupported platform: $platform" >&2; exit 2 ;; esac
for tool in docker jq mktemp; do
  command -v "$tool" >/dev/null || { echo "required tool missing: $tool" >&2; exit 1; }
done

temporary="$(mktemp -d)"
container=''
volume=''
cleanup() {
  status=$?
  if [[ -n "$container" ]]; then
    if [[ "$status" -ne 0 ]]; then docker logs --tail 60 "$container" >&2 || true; fi
    docker rm --force "$container" >/dev/null || true
  fi
  if [[ -n "$volume" ]]; then docker volume rm "$volume" >/dev/null || true; fi
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 0755 "$temporary"
cat >"$temporary/wukongim.toml" <<'TOML'
[node]
id = 1
data_dir = "/var/lib/wukongim"
[cluster]
listen_addr = "127.0.0.1:7001"
[api]
listen_addr = "0.0.0.0:5001"
[log]
dir = "/var/lib/wukongim/logs"
[prometheus]
enable = true
[observability]
metrics_enable = true
TOML
# This fixture has no credentials and must be readable by the image's UID 10001.
chmod 0644 "$temporary/wukongim.toml"
volume="$(docker volume create)"

start_node() {
  container="$(docker run --detach --platform "$platform" --network none --restart no \
    --mount "type=bind,src=$temporary/wukongim.toml,dst=/etc/wukongim/wukongim.toml,readonly" \
    --mount "type=volume,src=$volume,dst=/var/lib/wukongim" "$image")"
}

get() {
  docker exec "$container" wget -q -O - -T 3 "$1"
}

wait_ready() {
  local deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    [[ "$(docker inspect --format '{{.State.Running}}' "$container")" == true ]] || {
      echo "WuKongIM exited before embedded Prometheus became ready ($platform)" >&2
      return 1
    }
    if get http://127.0.0.1:5001/readyz >/dev/null 2>&1 &&
       get http://127.0.0.1:9099/-/ready >/dev/null 2>&1; then
      get http://127.0.0.1:5001/metrics >"$temporary/metrics"
      [[ -s "$temporary/metrics" ]]
      [[ "$(docker exec "$container" id -u)" == 10001 ]]
      [[ "$(docker inspect --format '{{.RestartCount}}' "$container")" == 0 ]]
      return
    fi
    sleep 1
  done
  echo "timed out waiting for embedded Prometheus ($platform)" >&2
  return 1
}

wait_query() {
  local url="$1" deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if get "$url" >"$temporary/query.json" 2>/dev/null &&
       jq -e '.status == "success" and .data.resultType == "vector" and
         (.data.result | length) == 1 and .data.result[0].metric.job == "wukongim" and
         .data.result[0].metric.instance == "127.0.0.1:5001" and
         .data.result[0].value[1] == "1"' "$temporary/query.json" >/dev/null; then
      return
    fi
    sleep 1
  done
  echo "Prometheus did not return a successful WuKongIM scrape ($platform)" >&2
  return 1
}

stop_node() {
  docker stop --time 30 "$container" >/dev/null
  [[ "$(docker inspect --format '{{.State.ExitCode}}' "$container")" == 0 ]] || {
    echo "WuKongIM did not stop cleanly ($platform)" >&2
    return 1
  }
}

start_node
wait_ready
query='http://127.0.0.1:9099/api/v1/query?query=up%7Bjob%3D%22wukongim%22%7D'
wait_query "$query"
# Retain only the extracted executable when the caller requests dependency
# scanning/SBOM input. Runtime data and configuration never leave the probe.
if [[ -n "${WK_DOCKER_PROMETHEUS_ARTIFACT_DIR:-}" ]]; then
  artifact_dir="$WK_DOCKER_PROMETHEUS_ARTIFACT_DIR/${platform#linux/}"
  mkdir -p "$artifact_dir"
  docker cp "$container:/var/lib/wukongim/prometheus/bin/prometheus-linux-${platform#linux/}" \
    "$artifact_dir/prometheus"
fi
# Freeze the evaluation time so a new scrape cannot masquerade as persisted data.
sample_time="$(jq -er '.data.result[0].value[0]' "$temporary/query.json")"
stop_node
docker rm "$container" >/dev/null
container=''
start_node
wait_ready
wait_query "$query&time=$sample_time"
stop_node
echo "embedded Prometheus verified: $platform; non-root, offline startup, scrape, historical query after recreation, graceful stop"
