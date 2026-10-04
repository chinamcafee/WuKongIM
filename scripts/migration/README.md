# Rehearse a delivered migration package

`rehearse-offline.py` runs the delivered Linux amd64 `wkcli migrate`, without a
source checkout or rebuild, through `prepare → export → import → retry → verify`.
It requires Python 3.9+, Docker, an already available Linux amd64 runtime image,
and an operator-approved immutable plan. Docker Desktop on arm64 can emulate
amd64 for functional checks; those timings are not a performance acceptance.

The script creates a **new output directory**. Sources and approved plugin
artifacts are read-only mounts in preparation/export. Import and verification
receive only the plan, binaries, archive and target directories, with independent
scratch directories. They have no network, source mount or producer workspace.
The repeat import runs before any server starts. The script never starts v2 or
v3, changes an existing deployment, removes old evidence, or chooses a policy to
resolve a compatibility failure.

## Inputs

1. Verify the delivered tarball against its separately recorded SHA-256, extract
   it, then run `sha256sum -c SHA256SUMS` inside the extracted directory. Take both
   binary hashes from the approved delivery record, not from an untrusted file.
2. Freeze the stopped source. If using a relocated copy, compare the complete
   relative-path/file-size/SHA-256 inventory with the original first. Keep the
   original stopped throughout the rehearsal; record the same inventory again
   afterward. Read-only mounts alone do not establish that a copy is current.
3. Copy [plan.example.json](plan.example.json) and fill in the actual topology.
   This minimal example describes a single-node cluster and has no exclusions or
   recovery overrides. It is not the approval for this repository's private test
   dataset. Multi-node clusters list every source and target node; use the
   configured source shard counts and target replica counts. Hash slots default
   to 256. Target addresses and IDs must match the eventual runtime config.
4. Translate source locations to `/source/<relative-directory>` and target
   locations to `/targets/<node-directory>`. Targets must be distinct immediate
   children. If there are approved plugin executables, use
   `/source-programs/<relative-file>` and supply `--artifact-root`. Keep all
   approved business policies and capture-bound proofs unchanged. A changed
   capture or proof is a blocking finding, not a request to regenerate approval.

Create the output directory's parent beforehand, but leave the output itself
absent. The script mounts scratch **parents**;
`wkcli migrate` creates `/scratch/workspace` itself. An existing empty workspace has
no identity seal and is correctly refused. It similarly leaves every target
data directory absent until import creates it.

```sh
python3 scripts/migration/rehearse-offline.py \
  --plan /srv/private/plan.json \
  --bundle /srv/delivery/extracted-package \
  --source-root /srv/frozen-v2 \
  --output /srv/rehearsals/new-run \
  --image sha256:REPLACE_WITH_EXISTING_RUNTIME_IMAGE_ID \
  --wkcli-sha256 REPLACE_WITH_APPROVED_BINARY_SHA256 \
  --wukongim-sha256 REPLACE_WITH_APPROVED_BINARY_SHA256 \
  --dry-run
```

The dry run checks paths and binary hashes, prints each exact container-creation
command, and creates nothing. During execution, the wrapper records the created
container ID and verifies its ownership before attaching to `docker start`. Remove `--dry-run` to run. Add `--expected-retained-messages N`
when an approved authoritative retained count is known; verification must then
report `N × channel_replicas` message replicas. Do not confuse physical source
rows or pre-exclusion counts with retained business messages.

Default guards reserve 2 GiB host disk, cap each phase at 90 minutes and each
container at 1.5 GiB memory/4 CPUs. These limits are functional rehearsal bounds,
not a sizing recommendation for production data. Scratch storage, the archive
and target replicas all consume disk. Provision their combined requirements
plus reserve. A guard stops only the exact container owned by that phase and
preserves directories/logs. Cleanup settles the attached start client and checks
container state again before removal, covering a delayed start. Creation is
bounded to 30 seconds and cannot execute migration work. If Docker becomes
unreachable during creation or cleanup, the execution record keeps
`cleanup: not_confirmed`; inspect the exact recorded owner before retrying.
An uncertain creation may leave a stopped container, never an automatically
started migration workload. Inspect a stopped run before creating another fresh
run; the wrapper deliberately does not resume or delete an interrupted output.
Use the documented CLI recovery procedure when a resume is appropriate. Before
retrying a stopped verification, preserve its execution record and logs, confirm
that no target has started, and keep the plan, archive and binaries unchanged.
The same independent verification workspace can be reused; `verify` still
rebuilds expectations from the archive and checks every target. Record the new
execution separately rather than overwriting the failed attempt.

## Evidence and runtime gate

Keep the output private: the plan, archive, mapping, scratch data and logs can
contain credentials and payloads. The script uses mode 0700 for its output and
restrictive file permissions. Retain `inputs.json`, `status.json`, every phase's
JSON result, execution record and container inspection. The checksummed archive
and sequence mapping under `prepare-work/workspace` are required migration
artifacts. Retain the tool's full results, including explicit omissions and
administrative archival counts.

`offline_verified` is not a traffic-switch approval. Before starting the new
target, retain the verified stopped generation or the complete reproducible
archive and evidence. Using the **same delivered `wukongim`**, start every target
with matching cluster ID, node IDs, addresses, data paths, hash slots and replica
counts. Keep ports and data directories separate from every existing deployment.

Validate readiness on every node, exact historical fields for representative
channels (including a renumbered channel), recovered conversation/read positions,
and new peer messages becoming unread. Check that every new `message_seq` is
strictly greater than the previous channel tail. For a person channel, send to
the receiving peer UID; a receiver-side history fixture names the other peer and
must not be reused as the sender-side destination. Execute API probes inside
the isolated network if the container runtime cannot publish internal-network
ports to the host. Restart the full cluster and
repeat historical/new-message checks, preserving the same target data. Keep
retries at the same ClientMsgNo and reconcile uncertain send results instead of
silently issuing another message. Client cutover additionally requires clearing
old message caches and sequence cursors while retaining login credentials when
sequences were compacted. API checks do not certify SDK login or rendering.

See the [migration runbook](../../docs/superpowers/runbooks/v2-to-v3-migration.md)
for operational recovery, policy details and the separate client cutover gates.
