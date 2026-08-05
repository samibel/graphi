# P2 Architecture Phase 0 — Contract Freeze & Baseline

**Recorded:** 2026-08-05, against `2ef15d8c4bf2c01ec764b9d2d3087ff357a63845` (`main` at
recording time).
**Toolchain:** `go1.26.5 linux/amd64`, `CGO_ENABLED=0`, single module
`github.com/samibel/graphi`.
**Method:** measured, not asserted. Every number below was produced by a tool in this
repository and every artifact can be regenerated with the command that produced it. No
markdown document was used as evidence for any claim.
**Subject:** the PRD *Graphi P2 Phase 0: Architecture Foundation & Modularization*.
**What this document is:** the record of what must not change while the P2 modularization
moves use-case logic out of `surfaces/client` into an application layer. It is not an
architecture decision — that is Phase 1's ADR — and it is not an Architecture GO.

---

## Why this exists

The P2 refactor removes a 29-method central interface by relocating its responsibilities.
Its central promise is that **nothing observable changes**: same bytes, same MCP contract,
same persistence, same fail-closed behaviour, comparable performance. A promise like that
is only testable against a recorded "before", so Phase 0 records one before any production
code moves.

Phase 0 changes no production code. Everything added is documentation, snapshots, tests,
and CI tooling.

---

## 1. Test baseline (PRD task 3)

`CGO_ENABLED=0 go test ./...` over all 106 module packages: **exit 0, no failures**, at the
recorded commit with no source changes. This is the "existing tests stay green" reference
every later phase is measured against.

The gates that guard the contracts this refactor touches are listed below. Phase 0 adds no
new gate workflow: every check it introduces is an ordinary Go test, so `testgate.yml`
(`go run ./cmd/testgate`) picks them up automatically.

| Contract | Enforced by | Kept green by |
|---|---|---|
| Byte-stable output of the 12 stable ops | `surfaces/characterization_golden_test.go` (AC1) | unchanged |
| Mem vs SQLite byte-identity | `surfaces/characterization_golden_test.go` (AC2) | unchanged |
| CLI ↔ MCP ↔ HTTP parity | `surfaces/parity_test.go` + 8 per-feature parity suites | unchanged |
| MCP tool **names** | `surfaces/mcp/tools_snapshot_test.go` | unchanged |
| MCP tool **schemas** | `surfaces/mcp/descriptors_snapshot_test.go` | **added by Phase 0** — see §2 |
| HTTP routes and contract document | `surfaces/http/routes_snapshot_test.go`, `contract.schema.json` | unchanged |
| Stable client port surface | `surfaces/capability_ports_test.go` (CAP-01) | unchanged |
| Release tool-removal guard | `docs/mcp-tool-baseline.json` via `cmd/release-gate` | unchanged |
| Layer direction | `internal/layerguard` via `cmd/layerguard` | unchanged |
| Zero egress / CGo-free | `internal/canary`, `internal/audit`, `internal/cgoconformance` | unchanged |

## 2. Contract snapshots (PRD task 2)

**Gap closed.** The MCP surface froze tool *names* only. Schemas, descriptions, annotations,
and profile membership were unpinned, so a required argument could be added or a read-only
annotation dropped with no gate noticing — all of them wire-contract facts for every MCP
consumer, and all of them things this refactor must carry across untouched.

`surfaces/mcp/descriptors_snapshot_test.go` now pins the full descriptor documents for both
profiles into `docs/rc/mcp-tool-descriptors.baseline.json` (45 tools in the Labs/maximal
catalog, 11 in the default Stable profile). The snapshot is taken **before**
`filterSupportedToolDescriptors`, so it is a pure function of the code rather than of a
particular client's wiring, and determinism is asserted separately by re-serializing.

Regeneration is deliberately manual: on a mismatch the test writes the observed document to
`…baseline.json.actual` and fails. Review that diff; replace the baseline only when the
change is intended and approved.

## 3. Persistence and recovery references (PRD task 4)

Phase 0 builds nothing new here — the existing fixtures are the reference set, and this
section records that they are what a runtime or wiring change is judged against.

| Property | Reference |
|---|---|
| Full vs incremental byte parity (both backends) | `engine/conformance` + `docs/rc/parity-classes.yaml` (15 change classes + 2 crash conditions), `TestParityMatrix_DriftGuard` |
| Real-repo index parity, two-run discipline | `internal/parity`, `cmd/parity -verdict-diff`, `docs/rc/parity-matrix-run-{a,b}.json` |
| Crash recovery | `engine/ingest` (`TestIngest_CrashRecovery`, `TestProvenance_CrashRecoveryIsIdempotent`), `engine/edit` (`TestApply_CrashRecoveryViaDirtyFlag`), `cmd/graphi/zeroconfig_recovery_test.go` |
| Reopen / warm start | `engine/ingest/warmstart_test.go`, `engine/ingest/faultmatrix_test.go` (`SQLiteCloseReopen`), `engine/embed` reload tests |
| Backend contract | `core/graphstore/contract_test.go` and siblings |

Phase 8 (composition root and runtime lifecycle) is the phase that can break these. It must
run these fixtures unchanged, not adapt them.

## 4. The legacy client inventory (PRD tasks 6, 7, 9)

`surfaces/client.Client` has **29 methods** and the package declares **22 exported error
sentinels**. Both sets, and the migration target of every method, are recorded in
[`docs/migration-matrix.md`](../migration-matrix.md) (source:
[`docs/migration-matrix.yaml`](../migration-matrix.yaml)).

The matrix is machine-checked rather than hand-maintained, because an inventory that can
rot is worse than none — it would read as authoritative while being wrong. `cmd/archmatrix
-check` derives the live method set by reflection, the sentinel set and the compatibility
stubs from the source, and the surface-usage column from the call sites, then fails on any
disagreement in either direction.

| Bounded context | Target service | Phase | Methods |
|---|---|---|---|
| Graph Read | `app/graphread` | 4 | 12 |
| Code Change | `app/codechange` | 5 | 5 |
| Review & Forge | `app/review` | 6 | 7 |
| Knowledge | `app/knowledge` | 7 | 4 |
| Operations & Capability | `app/operations` | 7 | 1 |

**Compatibility-stub debt at the baseline.** `Direct` implements all 29 use cases. The HTTP
client declines **19** of them with a typed sentinel and the daemon client declines **21** —
those counts are the debt the PRD forbids growing, and the matrix now freezes them: adding a
stub fails `cmd/archmatrix -check` until it is recorded and justified.

**Open decision (needs maintainer sign-off): `Brief`.** The matrix places it in Knowledge,
following the PRD's Knowledge context. The counter-argument is that `agent_brief` is one of
the 12 frozen stable read operations and shares `agentDeps` with `ExplainSymbol`,
`RelatedFiles`, and `ChangeRisk`, all Graph Read. The placement decides which service owns a
stable-12 encoder, so it should be settled before Phase 4 starts, not during it.

## 5. Import graph (PRD task 8)

[`docs/rc/import-graph.json`](../rc/import-graph.json) records all 106 module packages and
their module-internal production imports at the baseline commit. It is a **one-shot
descriptive artifact, not a gate**: every pull request changes the import graph, so a
freshness check would be noise. `internal/layerguard` remains the enforcing guard.

The aggregate that matters to this refactor:

| Edge | Count | Meaning for the migration |
|---|---|---|
| `surfaces` → `engine` | **33** | The debt phases 4–7 must drive to zero: no surface handler may import an engine package once its use cases live in `app/*`. |
| `surfaces` → `core` | **5** | Same, one layer deeper. |
| `engine` → `internal` | 5 | Engine depends on internal packages; each must be classified in Phase 10. |
| `internal` → `surfaces` | 4 | Tooling reaching into surfaces (bench, canary, and now the Phase 0 harnesses). |
| `cmd` → `engine` | 31 | Command wiring that Phase 8's composition root absorbs. |

Layerguard today is rank-based and leaves **27 `internal/` packages plus 3 in
`corpus`/`extensions` entirely unconstrained**, and it permits sideways edges within a
layer. Phase 10 replaces that with an explicit zone matrix; this artifact is the input that
tells it what it will have to classify.

## 6. Performance baseline (PRD task 5)

[`docs/rc/perf-baseline.json`](../rc/perf-baseline.json), recorded with **15 samples per
metric** after 2 discarded warmups. Both median and p95 are recorded for every timing: the
PRD's budgets are p95 budgets, because a layer that adds a small constant to every call
shows in the tail long before it moves the median.

| Metric | Median | p95 |
|---|---|---|
| `full_index` | 10.453 ms | 11.598 ms |
| `warm_query/definition` | 0.078 ms | 0.126 ms |
| `warm_query/callers` | 0.085 ms | 0.150 ms |
| `warm_query/callees` | 0.082 ms | 0.136 ms |
| `warm_query/references` | 0.051 ms | 0.094 ms |
| `warm_query/neighborhood` | 0.582 ms | 1.452 ms |
| `warm_query/search` | 0.149 ms | 0.217 ms |
| `trust_report` | 2.224 ms | 2.625 ms |

Binary size: **33 158 532 bytes**, built through `internal/release.CanonicalBuildArgs`
(the shipped `CGO_ENABLED=0` subset-tagged contract), inside the 34 250 000-byte budget
in `bench/bench-budget.yml`.

Two things make these numbers trustworthy rather than decorative:

- The index and query workload is `bench/fixture`, and the harness independently computes
  its digest as `50f9966c…`, **matching the digest pinned in `bench/bench-budget.yml`**.
  A test asserts that equality, so the baseline cannot claim to describe the gated workload
  while measuring something else.
- **Gap closed:** the trust report had no benchmark anywhere in the repo, and the refactor
  moves it (its encoder lives at surface rank). It is now measured under policy `review-v1`
  against a purpose-built resolvable module, and the harness **fails hard** if the result is
  state `UNAVAILABLE` or carries no verdict — so the number cannot silently describe the
  fail-closed path. The recorded run produced verdict `PASS`, state `CURRENT`.

Timings are only comparable against another run on the same machine. `cmd/perfbaseline
-diff` warns when the fixture, toolchain, environment, or sample count makes two runs
incomparable, and applies the PRD budgets (full index 3 %, warm query 5 %, binary size 2 %).

**This is not a release gate.** `bench/bench-budget.yml` remains the gate for the metrics it
owns; this artifact exists so the architecture phases can answer "did the application layer
cost anything" with a measurement.

## 7. Differential harness (PRD task 10)

`internal/archdiff` drives use cases through the `surfaces/client.Client` seam — the one
boundary both the legacy client and a future application-backed client satisfy — so a later
phase supplies a second implementation and calls `Compare` with no change to the harness.

[`docs/rc/archdiff-baseline.json`](../rc/archdiff-baseline.json) records **51 use cases**
across all five bounded contexts: 30 successful results (digested over path-normalized
bytes), 18 typed refusals, and 3 intentional negative paths (an unimplemented refactor kind
failing closed, an unknown undo token, an unknown analyzer).

Coverage here is deliberately **broad rather than deep**. Depth already exists — the
characterization suite pins the exact bytes of the twelve stable ops. What no existing suite
covered is the long tail: edit previews, review analysis over an offline mock forge,
knowledge operations, and the fail-closed paths. That tail is what a migration silently
breaks.

Recorded refusals are contracts, not gaps. Two are worth naming:

- `knowledge/memory.export-path-rejected` → `ErrExportPathRejected`. A server-side export
  path must stay refused (SAFE-01). A refactor that "fixes" this is a regression.
- `graphread/semantic-search.unconfigured` → a **typed graceful-skip payload with no error**.
  Collapsing it into a sentinel would be a contract break, so it is recorded as a success
  with its own digest.

A separate table runs every optional capability against a client with nothing wired and
asserts each one refuses. This is the PRD's "no noop implementation that reports a missing
capability as a success" rule, made executable.

**One baseline assumption was wrong and is recorded as a finding:** `Diagnose` is *not* an
optional-service capability. `ErrDiagnosticUnavailable` is a **transport** gap — the
in-process client always has a diagnostic reader, and only the remote clients decline. It is
therefore excluded from the fail-closed table, because freezing a "fails closed" expectation
there would have frozen something untrue of this implementation.

**Reproducibility comes first.** `TestRecordAll_IsReproducibleAcrossEnvironments` builds two
independent environments — different temp dirs, different stores, a freshly ingested graph
each — and requires identical recorded outcomes. Re-recording the artifact twice produces
byte-identical files. A harness that could not reproduce its own output would be unable to
tell "the application layer changed behaviour" from "the recording is noisy", so this test
failing is a stop-the-line signal, never a flake to retry.

---

## Regenerating the artifacts

```sh
# Migration matrix (checked in CI via internal/archmatrix tests)
go run ./cmd/archmatrix -generate
go run ./cmd/archmatrix -check

# Import graph — one-shot; scan a pristine checkout of the baseline commit
go run ./cmd/importgraph -commit <sha> -root <checkout> -out docs/rc/import-graph.json

# Performance baseline (~2 min; builds the canonical release binary)
go run ./cmd/perfbaseline -record -commit <sha> -samples 15 -out docs/rc/perf-baseline.json
go run ./cmd/perfbaseline -diff docs/rc/perf-baseline.json candidate.json

# Differential baseline
go run ./cmd/archdiff -record -commit <sha>
go run ./cmd/archdiff -check

# MCP descriptor snapshot — no auto-update; the test writes …baseline.json.actual on a
# mismatch, and a human moves it after reviewing the diff.
go test ./surfaces/mcp -run TestCharacterization_ToolDescriptors
```

**The standing rule for every one of these:** a failing baseline is a finding, not a chore.
Do not regenerate an artifact to make a test pass. Explain the diff first; if it is
intended, record the intent in the pull request that re-pins it.

---

## What Phase 0 does *not* establish

- **No architecture decision.** Package names (`app/*`, `contracts/*`, `sdk/*`), the import
  allowlist, and the public-vs-internal boundary are Phase 1's ADR. The bounded contexts
  used here follow the PRD so the inventory has stable keys; they are not thereby ratified.
- **No Architecture GO.** That is Phase 11, and it depends on evidence that does not exist
  yet.
- **No claim about the remote clients' behaviour.** The differential harness drives the
  in-process seam. HTTP and daemon behaviour is covered by the existing parity suites, and
  the stub counts in the migration matrix are derived from source, not from execution.
- **No performance claim beyond this machine.** The recorded numbers are a reference point
  for a same-machine A/B, nothing more.

## Open decisions carried into Phase 1

1. **`Brief` placement** — Knowledge (current entry) or Graph Read. Settle before Phase 4.
2. **`CompareBranches` in the differential harness** — recorded as its fail-closed sentinel
   because no branch-state materializer is wired. If Phase 6 wants a behavioural baseline
   for it, a deterministic materializer fixture has to be built first.
3. **`internal/` classification** — 27 internal packages plus 3 in `corpus`/`extensions` are
   unranked today. Phase 10 must classify each as runtime or tooling; the import graph names
   them, but the decision is not made here.
