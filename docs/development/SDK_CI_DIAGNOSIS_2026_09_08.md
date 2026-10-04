# Rust registry acceptance and CI diagnosis — 2026-09-08

## Published package

The independently downloaded crates.io `wukong-easy-sdk = "=0.1.0"` archive
passed a 120-second macOS / Rust 1.86 real-server run: Rust/Rust roundtrip,
incorrect-Token rejection, 1,747 confirmed Rust/JS Unicode echoes over WSS,
three forced cuts and recoveries, no duplicates or event loss, and complete
owned-process cleanup. One interrupted send retained unknown-outcome semantics.
The JS peer confirmed 1,748 replies, including that interrupted operation.

- Package source: `5b4a59cdbb66a9e0c3878e73ba4656f08ee05c6b`.
- Archive SHA-256: `0029747f10b86f566e2d659535df0954114769a90962e562fb522a95e5508719`.
- Harness source: `cfa48a038c2cfd56948ace43afe3b2f5f91dace3`.
- Server source: `27a39f15bf163b433f417b78ab6bfc6e589585e5`, 256 Hash Slots,
  single-node cluster, Token authentication enabled; JS `easyjssdk@2.0.4`.
- [Public receipt](https://github.com/WuKongIM/WuKongEasySDK-Rust/releases/download/v0.1.0/registry-e2e-0.1.0-macos-120s.json).

Registry mode starts with an empty Cargo home and a separate consumer manifest,
checks the registry lock entry, downloaded archive hash and archived source
identity, and never uses a Git/path SDK dependency. Receipt identities separate
harness source from package source. This proves bounded messaging and recovery,
not capacity, physical-device behavior, offline recovery or multi-day stability.

The same clean harness also passed [Linux CI](https://github.com/WuKongIM/WuKongEasySDK-Rust/actions/runs/34194945967) with the public package: 3,021 confirmed echoes in 120 seconds, three recoveries, no interrupted/duplicate operations or event loss, and complete cleanup. Source and registry jobs retain separate receipts.

## Scheduler test: confirmed test timing defect, repaired

[CI attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/34192156949/attempts/1)
failed `TestRunScheduledMessagesByKeyDispatchesReadyKeysBehindLargeBusyPrefix`:
1,000 ready senders behind 100,000 same-key messages did not complete within a
75 ms wall-clock assertion under `-race`. Server baseline was
`50f3c2a621157183e6150079bd72588322b376f3`.

Two local runs of the unchanged test, each with `-race -count=100`, both failed
4 times. The replacement uses `synctest.Wait` to observe the point where all
other goroutines are durably blocked: all 1,000 ready messages must have started
while the busy sender remains blocked. It does not relax the behavior assertion.

The unchanged scheduler passed 100 race iterations with this synchronization,
and the complete workload unit and race suites passed. A temporary mutation
that blocked selection whenever the busy sender held its key failed immediately
(0 of 1,000 ready messages), proving the new test detects the original starvation
pattern. The mutation was removed; no scheduler runtime code changed.

Reproduce: `GOWORK=off go test -race ./internal/bench/workload -run '^TestRunScheduledMessagesByKeyDispatchesReadyKeysBehindLargeBusyPrefix$' -count=100 -timeout=90s`.

## Channel append latency: unresolved hosted-runner tail

[CI attempt 2](https://github.com/WuKongIM/WuKongIM/actions/runs/34192156949/attempts/2)
passed unit/race gates but failed `BenchmarkThreeNodeChannelAppend500QPS`:
4% of operations exceeded 200 ms (allowed at most 1%), append p99 240.128 ms.
The stage receipt puts 240.104 ms in store-append wait, versus microseconds in
admission, worker queue and post-store completion. Mean physical commit was
9.265 ms; averages do not establish the cause of the tail.

The Workflow additionally checks a 400 ms p99 bound, but the benchmark has an
independent 200 ms bound. Both already existed before this documentation task.
Neither threshold is changed here, and the CI failure remains a failure.

The exact 3,000-operation / 500-QPS seam on macOS Apple M4 passed three unprofiled
runs: p99 47.15, 39.20 and 166.3 ms, each with 0% above 200 ms. One separate CPU/
block-profile run passed at 39.41 ms. CPU samples were mostly runtime/syscall
work; aggregate block time includes idle worker channels and cannot be equated
to per-message critical-path latency. No reproducible CPU hot path was found.
The host had only roughly 3 GB free and concurrent disk activity, so these are
diagnostic observations, not a controlled performance or capacity baseline.

Reproduce: `GOWORK=off go test -tags=integration ./pkg/channel/replication -run '^$' -bench '^BenchmarkThreeNodeChannelAppend500QPS$' -benchtime=3000x -count=3 -timeout=2m`.
For a separate diagnostic run, add `-count=1 -cpuprofile=/tmp/channel-cpu.pprof -blockprofile=/tmp/channel-block.pprof`.

Evidence local to this task is retained under
`tmp/easy-sdk-rust-validation/registry-e2e/`: baseline/test/mutation logs,
unprofiled benchmark logs, CPU/block profiles and summaries, source context and
registry receipt. The hosted failure has no corresponding profile or per-commit
storage-tail/host-pressure trace. Those signals on the same failed runner are
needed to distinguish storage delay, host contention and a runtime regression.
No server performance fix or recovered CI performance verdict is claimed.
