# Hero-Task Protocol (SW-122 / EVAL-01 · SW-123 / EVAL-02)

> **Status:** correctness suite COMPLETE; current-harness performance
> re-baseline PENDING. Historical reference evidence is committed under
> `docs/eval/runs/2026-07-15-ubuntu-latest/` (workflow run 29418826616), but it
> was produced by the previous harness and is not directly comparable to a
> current run.
> **Suite:** `corpus/hero/` (20 tasks) · **Gates:** `cmd/eval/hero_test.go`,
> `cmd/eval/fullrun_test.go`
> **Budgets:** `docs/eval/hero-budgets.json` — schema v3, declared
> `historical: true` / `ratcheting: false`. Still enforced fail-closed; never a
> comparable post-change ratchet.
> **Measurement contract:** `docs/eval/reference-scenario.json` (SW-123) —
> the reference runner class, the reference scenario, and every PRD §12.2 gate
> mapped by name to a pinned repository. Check it with
> `go run ./cmd/eval -check-reference-scenario`.

## What the hero suite is

The hero suite is the versioned, executable correctness contract for the **12
frozen stable operations** (SCOPE-01): `index`, `search`, `definition`,
`callers`, `callees`, `references`, `neighborhood`, `impact`, `explain_symbol`,
`related_files`, `change_risk`, `agent_brief`. Each task is an
`engine/scenario` file binding one operation to a deterministic tier-1 fixture
with **source anchors** (expected evidence citations), and — where the task
pins a failure mode — the **expected failure-class outcome**. The suite is data:
adding or tightening a task is a reviewed YAML change, never a code change.

### Failure classes (master plan: at least one task per class)

| Class       | Hero task(s) | What it pins |
|-------------|--------------|--------------|
| `ambiguous` | hero-08, hero-18 | several candidates are presented, never silently picked |
| `partial`   | hero-17 | truncation by item cap is reported, never silent |
| `empty`     | hero-03 | honest empty answers instead of near-miss noise |
| `not_found` | hero-05 | unknown symbols yield the typed not-found outcome |
| negative anchors | hero-02, -07, -10, -12, -14, -15, -20 | evidence never cites symbols that cannot appear |

hero-04 pins the graph's canonical `definition` semantics: ingest emits
`defines` as **definer/container → defined symbol**, so the query follows an
inbound edge from a symbol to its defining file or container. A known top-level
function returning `empty` is an accuracy failure.

### Fixtures

- `tier1-fixture-go` (`corpus/fixtures/go`) — the frozen SW-110 byte-parity
  oracle: single file, rich shapes (ambiguity, call chains, interfaces). It
  must **not** grow — golden snapshots pin its bytes.
- `tier1-fixture-hero-go` (`corpus/fixtures/hero-go`) — added by SW-122 for
  cross-file behaviors (cross-file callers, `related_files` ranking,
  type-usage references) that a single-file fixture cannot express.

## Runner class and budgets (ADR 0003 U5 · SW-123)

Reference runner: **`ubuntu-latest`** (GitHub-hosted, linux, `CGO_ENABLED=0`)
— the same class every existing gate workflow uses. Local runs are smoke, CI
runs are evidence.

Since **SW-123** that is no longer only prose. `docs/eval/reference-scenario.json`
is the machine-readable measurement contract, and it is validated by
`cmd/eval/refscenario_test.go` on every PR:

- **exactly one** runner class carries `role: reference` — `ubuntu-latest`,
  documented with CPU, RAM, OS, kernel, Go version, filesystem and the *cold*
  cache protocol (FR-8's acceptance criterion);
- the second class, `local-sandbox` (Apple M2 Max / macOS), is declared
  `role: comparison`. Its numbers are never reported as reference values and
  never freeze a budget;
- every **PRD §12.2** gate is mapped by name to a repository from
  `corpus/manifest.json` — a mapping pointing at a repository that is not
  pinned is a test failure;
- the reference scenario itself is **`grpc-go` v1.60.1**, the largest
  non-stress repository in the corpus. `kubernetes` is the FR-2 *stress
  target*, deliberately not the reference — it is bound by the program-wide
  4 GB peak-RSS stop rule, not by the §12.2 gates;
- the 8 GB-host OOM gate has a method (cgroup v2 `MemoryMax=8G`,
  `MemorySwapMax=0`, limit read back and recorded, `oom_kill`/137/kernel-log as
  the failure signal), not a statement of intent;
- FR-8's scope limitation travels inside the artifact, so a consumer reading
  only the JSON cannot publish the gates as universal guarantees.

`cmd/eval -full-run -reference-scenario …` validates the contract before it
measures anything and stamps the run with the class's declared **role**; a
runner class the contract does not declare fails the run closed.

Absolute latency/rows budgets are **not invented**: `corpus/hero` tasks carry
no `max_latency_ms` (enforced by `TestHeroSuite_FailureClassesRepresented`).
The first reproducible reference run supplied the numeric limits now stored in
`docs/eval/hero-budgets.json`. `eval-full.yml` passes that file explicitly; the
CLI validates runner class, repo selection, metric presence, and every threshold
fail-closed, recording checks inside each JSON report.

Those numbers are **historical compatibility ceilings**, not comparable
baseline+ratchet pairs, and since SW-123 the file *says so in machine-readable
form* (`schema_version: 3`, `historical: true`, `ratcheting: false`, plus the
`historical_reason`). `cmd/eval` rejects a budget artifact that claims both, or
that omits the reason. The historical harness did not measure the same workload:
it omitted `impact` from the structural pool, did not require semantic checks for
all 12 Stable operations, used the earlier symbol sample, and sampled MAXRSS only
immediately after `IngestAll`. The current harness adds degree-stratified sampling,
all-12 semantic coverage, and a second MAXRSS sample after the Stable suite. A new
run on the re-frozen candidate (SW-121) measured by SW-124 is required to
establish ratchets under that method.

SW-123 also **removed** the file's `measured_max_latency_ms_per_op` map. It held
`0` for all twelve Stable operations — the historical run measured every hero
task below the millisecond floor — and nothing read it. A zero budget that
silently counts as met is worse than no budget, because it renders green;
`hero_suite.latency_signal: "none"` now states the absence instead of implying a
limit, and `cmd/eval` rejects **any** numeric zero anywhere in the budget
artifact.

## Pinned real repositories (EVAL-02 selection)

Three of the SHA-pinned `corpus/manifest.json` repos, per the master plan (one
JVM monorepo + two other languages, Go retained as the typeresolve acceptance
target):

| Repo | Ref (SHA pin) | Why |
|------|---------------|-----|
| `cobra` | `v1.8.0` (`a0a6ae020bb3`) | Go; carries the confirmed-tier `callers` acceptance gate |
| `flask` | `3.0.0` (`735a4701d6d5`) | Python; docs/JSON/mixed-asset bug class |
| `guava` | `v33.0.0` (`2214c63670fc`) | Java/JVM Maven **monorepo** (master-plan requirement); tier 3 |

`guava` is **tier 3**: it runs in the nightly/manual corpus job and the
EVAL-02 workflow, never in the per-PR corpus smoke (`corpus.yml` passes
`-max-tier 2` on push/PR). Its SHA was recorded from
`git ls-remote https://github.com/google/guava refs/tags/v33.0.0`
(2026-07-14); the tag is lightweight, so the tag object is the checkout HEAD,
and the first green tier-3 CI run re-verifies the pin fail-closed.

## How to run

```sh
# Local smoke (also the standing PR gate, in test form):
go test ./cmd/eval -run TestHeroSuite

# Full CLI run with a report artifact:
go run ./cmd/eval -manifest corpus/manifest.json -scenarios corpus/hero \
  -out hero-report.json -format markdown
```

The CLI gates alternative suites on their own outcomes (every tier-1 scenario
must pass); `docs/eval-baseline.json` belongs to the default suite only and is
neither read nor writable from a `-scenarios` run.

## EVAL-02 execution (SW-123)

**Harness:**

```sh
go run ./cmd/eval -manifest corpus/manifest.json -full-run <repo> \
  -runner-class ubuntu-latest -budgets docs/eval/hero-budgets.json \
  -reference-scenario docs/eval/reference-scenario.json
```
measures ONE repo per process (peak RSS stays attributable): shallow-clone at
the pinned ref with fail-closed SHA verification → cold full index (wallclock,
`getrusage` peak RSS, on-disk DB size) → degree-stratified warm coverage of all
12 stable operations → a second full-session MAXRSS sample and per-op-class p95 (microseconds;
per-op resolution in `warm_p95_us_per_op`) over the same in-process session,
driven through the same `engine/scenario.FixtureEngine` the hero suite uses.
Raw evidence: `internal/evalreport.FullRunReport` JSON. Hermetic gate:
`cmd/eval/fullrun_test.go` (local fixture, no network).

**Workflow:** `.github/workflows/eval-full.yml` — matrix over cobra/flask/guava
on `ubuntu-latest` (the reference runner class) + the hero-suite job; weekly
schedule + manual dispatch; never a PR gate (the hero suite's PR gate is
`cmd/eval/hero_test.go` inside testgate).

**Evidence and required re-baseline:**

1. The committed `ubuntu-latest` reports are immutable historical evidence for
   commit `71353f90720e079b84b7a0549bd51fc632bcfe37`. Their guava 11,821 MB
   MAXRSS value was sampled immediately after ingest, before `agent_brief` or
   the other warm operations. Its cause is **UNKNOWN**; it cannot be attributed
   to Stable reads or whole-cache materialization from those reports.
2. Preliminary sandbox reports under
   `docs/eval/runs/2026-07-15-local-sandbox/` freeze nothing. Runner class and
   old-harness measurements make them smoke evidence only.
3. Selective hydration, aggregate brief reads, and bounded impact work are implemented.
   Impact uses indexed bounded incident reads, a `16× MaxNodes` returned-edge budget,
   and a `min(2× MaxNodes, 16)` distinct-kind probe cap; exhausting any cap marks the
   result `truncated`. Semantic checks and the extended harness are also implemented.
   Those code facts do not prove a production performance improvement.
4. Run the current workflow matrix on the current commit. Commit the new raw
   reports, verify all 12 semantic checks and the post-suite RSS metric, then
   replace the provisional limits with reviewed comparable ratchets. Historical
   JSON remains unchanged.
