# Original v2 fixture

The fixture uses the unmodified `pkg/wkdb` at source commit
`a888f89533d0e7d1b2030e06504ca97f1ad891d4` (`v2.2.5-20260422`).
`generate.go.txt` is a separate caller of its public API, not a server patch.
Run the harness as a `.go` file with that checkout as the working directory.
It creates two Pebble v1 shards and `expected.json`, obtained through original
v2 public reads before closing the database normally.

All identities, tokens and payloads are synthetic. The binary fixture exercises
original keys, column values, indexes, WAL replay, non-ASCII identities and an ID
above JavaScript's safe integer range. It does not establish cluster authority
or replace the process-level topology and public API migration tests.

## Original server process fixtures

`original-v2-server.tar.gz` contains the complete normally stopped data directory
from the same unchanged source binary, serving a single-node cluster. The source
was built with `go build -o /tmp/wukongim-v2-original-a888f895 .` at the pinned
checkout. `original-v2-server-config.yaml` records the synthetic test configuration;
its temporary paths must be changed before reproducing it. `populate_server.py.txt`
and `finish_server.py.txt` use the public HTTP API, then the supervisor sends
SIGINT and waits for exit before packaging the data. The server was not patched.

`original-v2-server-api.json` records actual requests and responses, including
credentials, members, normal/CMD messages, read/delete state and a closed event
projection. `original-v2-server-slot-api.json` records public Slot progress after
an ordinary restart; all 64 Slot positions match the archived stopped data.
The original constructor ignores `db.slotShardNum`: two business shards coexist
with eight actual Slot log DBs. Inventory the actual directories.

The three `original-v2-unconverged-*.tar.gz` files are **negative fixtures** from
unchanged original processes with node IDs 11, 22 and 33. Initialization and later
ordinary restarts did not produce a healthy writable test cluster: a public token
write timed out and Slot 13 retained an unapplied tail. All processes were stopped
before packaging. The migration must reject these sources; they are not evidence
of successful multi-node source migration.

Source quirks remain evidence, not silently repaired input. The original message
iterator can skip the first header column; the raw durable header is preserved.
The old ChannelInfo member counters may be zero even when actual members exist;
original public count methods scan member rows instead.

Fixture SHA-256:

- `original-v2-server.tar.gz`: `03b6171798c9b9d582c265012d6d9f085285785f345b16768fe869c3074ce22a`

- `original-v2-unconverged-11.tar.gz`: `12f4cb650d78299abd2797d3be98f5b71727a774d2627adcf710e23911fdf631`

- `original-v2-unconverged-22.tar.gz`: `88953ba79e0cc6d771b00e57c4d530745c621de40d809420d1a92b2fa8bfd088`

- `original-v2-unconverged-33.tar.gz`: `b66363b30358179314944d3fd2cda3141e11ac120aaf5e9bd7d53b3c9b9f9959`

- `original-v2.tar.gz`: `b6842f60ea19804246fcad0798f498c17fca0794811009f21965130f60426b96`

## Healthy original three-node cluster

`original-v2-three-{1,2,3}.tar.gz` were produced by the same unmodified
`a888f89533d0e7d1b2030e06504ca97f1ad891d4` product binary. The separate
`generate_three_node.py.txt` uses three nodes, four source Slots, three Slot and
Channel replicas, and two business shards. It captures API observations,
checks every Slot replica's applied/committed/log positions, and normally stops
all three original processes before packaging. These are separate from the
older unconverged 64-Slot negative fixtures, which remain rejection cases.

SHA-256:

- Node 1: `59316086a16f383ac46204a51ef2ab816568c30334c7094e766564ac7f2363d2`
- Node 2: `f218fdde300770cea1ae96d09cd4dd63b48205d2e55fcbaab311a47bf6e9ee36`
- Node 3: `cd0d4b9a6fb0cb145cb7ba5a65e2589513a44549767921ca1f1aa422d12b1604`

All tokens and payloads in these fixtures are synthetic test data.

`original-v2-three-reopened-read.json` captures the original node 1 public
forward-history read after an unmodified restart. It confirms all three original
message IDs/sequences and payloads. A later request through original node 2
returned HTTP 400, so it is not described as a successful multi-ingress restart
check. The fixture tarballs above remain from the first fully converged stop;
they were not regenerated or patched after that separate read experiment.

## Original empty group

`original-v2-empty.tar.gz` comes from the same unchanged original product binary,
a single-node cluster with four Slots and two business shards. The external
`generate_empty.py.txt` creates `emptyalice` and an `emptygroup` subscription
through HTTP, sends no messages, captures `/conversation/sync` returning `[]`,
and normally stops the process before packaging. All credentials are synthetic.

Original v2 stores a conversation row with an update timestamp for this group,
but that timestamp does not mean the empty group is explicitly activated. The
migration must preserve membership and keep the group invisible until its first
message. `original-v2-empty-api.json` records the original public observations.

SHA-256: `fdf6886c79d84fef0a17e35d57d08b5a6ab4a688a985f10d036c7fba3dea3d62`.

## Original global plugin records

`original-v2-plugin-kv.json` contains exact Plugin columns generated by the
unmodified original public `AddOrUpdatePlugin` and checked by `GetPlugin`.
`generate_plugins.go.txt` is an external caller; it creates temporary databases
only and exports the original key/value pairs after normal close. Tests replay
these pairs into private copies of the stopped single-node cluster fixture.
No source server code or shipped fixture tarball is changed.

Cases cover registration text alone, global Send/PersistAfter methods without
user bindings, a persisted disabled status, and configuration without methods.
Only registration text may be archived; methods/config require a verified
business compatibility mapping. Persisted status is not proof of inactivity: the
original plugin runtime determines availability from its live RPC connection.
All values are synthetic; errors must not expose configuration contents.

SHA-256: `97a91d64d827d6fb2816efc4adc8709a05e8892760ca03ae91d621b5ac5b37f9`.

## Original channel edge semantics

`channel_semantics.go.txt` is an external caller of the same unmodified source;
run its `.go` copy with a new output-directory argument. It uses original public
Save/Get/Exists/Search methods, closes and reopens the DB, and drives the original
Raft ConfChange entrypoint. `original-v2-channel-semantics.json` records observations.
The 377 original pkg Go and module files were byte-checked against SourceCommit
before the harness ran. All data is synthetic; no deployed source was opened.

Version zero persists and can initialize a leader. A later downgrade leaves the
newer configuration unchanged; Step itself returns nil despite logging rejection.
A nonempty type-zero source configuration also survives reopen. Empty channel
rows are considered absent by Exists, but remain visible in management search;
empty control rows return not-found on point reads yet appear in config listing.
These observations do not prove any historical production commit or authorize
discarding the empty rows. Migration retains strict replica comparison and rejects empty identities. The
original observations do not justify a special record or weaker validation in
the existing v3 storage model.

## Original runtime plugin bindings

`plugin_bindings.go.txt` creates one synthetic binding through the unmodified
original public writer, closes/reopens it, and calls the original existence,
highest-priority and plugin-user directory readers. The 377 original pkg Go and
module files were byte-compared with SourceCommit before execution.
`original-v2-plugin-bindings.json` records the six original primary/index pairs
and nanosecond timestamp: User.PluginNo is empty while the runtime binding exists.
Migration now validates the composite primary hash and both live indexes, and
decodes binding timestamps without truncation. This does not bypass the remaining
plugin process/config compatibility gate.

SHA-256: `47c2453834b52a3aa8bf2ffaa745a142debc2e6e544cf05efcdf775d6e6cd4c2`.

## Existing v3 contract tests

Original archives remain unchanged. `compatibleMessageFixture` makes explicitly
synthetic private copies with optional message headers/settings, expiration,
StreamNo and topic zeroed so metadata/import/retry tests can isolate their own
contracts. This is test construction, never a migration conversion rule or proof
of original full-message compatibility. Unmodified source archives separately
prove decoding fidelity and CLI refusal for unrepresentable messages. The old
stream-table exclusion still preserves every primary Message in source selection
and archival; unsupported parent fields block conversion rather than being erased.

## Historical configuration authority probes

`historical_authority_replica_test.go.txt` and
`historical_authority_store_test.go.txt` are synthetic probes for the unmodified
original tree at `9b699e31a58fccb09a00fd9d326a78f40bbfcc7e`. In a separate checkout,
copy them to `pkg/cluster/replica/migration_history_test.go` and
`pkg/cluster/clusterstore/migration_history_test.go`, respectively, then run:

```sh
GOWORK=off go test -mod=mod ./pkg/cluster/replica ./pkg/cluster/clusterstore -run TestMigrationHistorical -count=1 -v
```

These probes exercise old encoded-version persistence at Slot index 305 and
compare self-Leader markers with a real follower-transfer positive control.
They use synthetic data and temporary storage only; they never open production
backups. `-mod=mod` fills the old tree's missing dependency sums; no original
runtime source is changed. Evidence and exact source/dependency hashes are in
`tmp/server-rehearsal-20260907/authority-23/history/` for the recorded run.

## Original metadata lookup probe

`original_metadata_lookup_test.go.txt` is a synthetic test for the unmodified
fixed SourceCommit. Copy it to `pkg/wkdb/migration_original_metadata_test.go` in
an extracted original source tree and run:

```sh
GOWORK=off go test -mod=mod ./pkg/wkdb -run '^TestMigrationOriginalMetadataLookups$' -count=1 -v
```

The probe exercises hot versus cold device lookup, conversation unique-index
versus recent-list lookup, indexed delete-boundary updates and deletion of every
duplicate primary. It uses temporary synthetic databases only. Production
backups are never opened by the original runtime. The migration fixture tests
separately exercise explicit cold-device selection, damaged-index refusal,
replica disagreement, archive reconstruction with source paths unmounted and
native offline target verification for 1→1, 3→1 and 3→3 clusters.

## Ordinary history without unsupported extensions

`original-v2-ordinary.tar.gz` is a successful single-node cluster source generated
by `generate_ordinary.py.txt` through the unchanged original HTTP API. The source
was rebuilt from `git archive a888f89533d0e7d1b2030e06504ca97f1ad891d4`; all 976
archived source files were byte-checked after the build. Build/fixture hashes are
recorded in `original-v2-ordinary-provenance.json`.

The source has four Slots, two business shards, two synthetic users and three
ordinary RedDot messages with no expiration, topic or stream extensions. Original
send requests/acknowledgements, the complete forward history, conversations and
converged Slot API positions are in `original-v2-ordinary-api.json`. The process
was stopped normally before packaging. No existing fixture was rewritten.

The first generation attempts exposed the original startup banner preceding Slot
creation and the different forward-history starting position. The retained
harness waits for source configuration and requests forward history from zero;
failed attempts were not published as fixtures. The existing expiration-bearing
single-node fixture remains a refusal case. Positive multi-node source coverage
uses the existing unchanged healthy `original-v2-three-{1,2,3}.tar.gz` archives.
