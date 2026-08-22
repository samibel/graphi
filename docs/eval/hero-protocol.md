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

### Comparison-class budgets (SW-191 · EVALBUDGET-001 closure)

The historical block (`real_repos.selection`) is and remains the reference-class
ceiling. **SW-191** adds a sibling block, `historical_ceilings`, that records
comparison-class ceilings (`runner_class: local-sandbox`) derived from the
two-dispatch campaign over each pin — `guava`/`okio`/`kotlinx_serialization`
from SW-177 and `flask`/`ky`/`express` from the SW-191 perf run.

**The two tables are not interchangeable, and neither can reach the other.**
`evaluateFullRunBudgets` routes on the RUN's `-runner-class`: the reference
class reads `real_repos` and nothing else; the comparison class
(`local-sandbox`) reads `historical_ceilings` and nothing else, through
`fullBudgetManifest.comparisonCeilings()`. That routing is the whole of the
acceptance. The ceiling block is fail-closed on its OWN declaration rather than
on the manifest's: it must declare `runner_class: local-sandbox`,
`runner_role: comparison`, and `ratcheting: false` **written down** (the field
is a pointer, so an omitted declaration is refused rather than defaulted). Any
of those bent — including re-pointing the block at `ubuntu-latest` — is an
error, not a widened gate. Pinned by
`cmd/eval/budgets_test.go::TestEvaluateFullRunBudgets_CeilingBlockCannotBeRepointed`
and, on the checked-in file, by `TestHeroBudgets_HistoricalCeilingsSchema`.

A comparison-class ceiling applied through this branch is **not** comparable to
a reference-class run and must never be read as one. Concretely:
`-full-run ky -budgets docs/eval/hero-budgets.json -runner-class local-sandbox`
scores six checks and exits 0; the same invocation at
`-runner-class ubuntu-latest` still exits 1 with
`repo "ky" is not in budget selection`.

The `historical_ceilings.per_repo[<repo>].notes` field carries each pin's
freshness result. **EVALFRESH-001 is closed** (SW-191): the change sequence is
language-scoped (`cmd/eval/sourcefamily.go`) and `-incremental-changes` now
exits 0 on every pin. It does not follow that every pin MEETS FR-8's
100-change floor — the notes record the completed-change count per pin, and the
shortfalls that remain are parser-coverage gaps in the Kotlin, TypeScript and
Java extractors rather than harness gaps.

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

**Workflow:** `.github/workflows/eval-full.yml` — the historical-ceiling matrix
over cobra/flask/guava on `ubuntu-latest` (the reference runner class), the
hero-suite job, and (SW-124…SW-130) four measurement jobs over the five pinned
Go repositories; weekly schedule + manual dispatch; never a PR gate (the hero
suite's PR gate is `cmd/eval/hero_test.go` inside testgate).

The four measurement jobs differ from the matrix job in three ways that matter,
and all three exist so their numbers can be read at all (SW-130):

- they **check the frozen candidate out** and build the harness from it, because
  the harness forces every PRD §12.2 gate to UNKNOWN when the measured revision
  is not the cited candidate;
- they take the candidate **citation** from the dispatched ref, copied out of the
  tree before the tree moves — a commit cannot contain its own hash, so the
  freeze record naming a SHA never exists at that SHA;
- they write **every output outside the checkout**, because an untracked file
  sets `worktree_dirty` and triggers the same blanket UNKNOWN.

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

## Raw samples, environment capture, and reproducing the numbers (SW-128)

Everything above produces *aggregates*. FR-9 asks for the individual
measurements too, for the environment they were produced in, and for the
aggregates to be **reproducible from the raw data**. That is a directory
convention plus one command.

**Export.** Any `-full-run` (single or `-cold-runs N`) can write a run directory:

```sh
go run ./cmd/eval -manifest corpus/manifest.json -full-run grpc-go \
  -runner-class ubuntu-latest \
  -reference-scenario docs/eval/reference-scenario.json \
  -candidate docs/rc/evidence-index.yaml \
  -export-raw auto
```

`auto` applies the SW-128 path convention — `docs/eval/runs/<date>-<runner-class>/`,
the same shape the historical runs already use — and an explicit path is for CI.
The layout and its rules are documented in
[`docs/eval/runs/README.md`](runs/README.md).

**The separation that matters.** `raw/` holds four sample-only files, one per
harness (SW-124…SW-127): cold runs, timed query executions with their pool
membership, incremental changes, and progress-stall intervals. They carry **no
percentile, no aggregate and no verdict**. The published report keeps its own
shape unchanged. Reproducing one from the other is therefore a real check
rather than a comparison of a number with a file that already contains it.

**Reproduce.**

```sh
go run ./cmd/eval -aggregate docs/eval/runs/2026-07-28-ubuntu-latest
```

Every statistic the report publishes is recomputed from `raw/` through the same
exported derivations the harnesses used (`RecomputeColdAggregates`,
`RecomputeQueryLatency`, `RecomputeIncremental`, `RecomputeStalls`) and compared
**exactly** — every percentile in this tree is a nearest-rank *observed sample*,
never an interpolation, so two correct derivations agree bit for bit and a
tolerance would only be somewhere for drift to hide. Exit `0` publishable,
`1` discrepancy, `2` unreadable, `3` incomplete.

**Environment.** `environment.json` records CPU, RAM, OS, kernel, Go version,
filesystem and observed page-cache state, plus runner class, frozen candidate
SHA, and the harness and scorer versions. A probe that fails leaves the field
**absent** with the reason recorded; `aggregate.json` renders it `UNKNOWN`. An
empty `kernel` never reads as a documented kernel, and a run whose environment is
incomplete is not publishable however cleanly its arithmetic reproduces.

**Method versioning.** Raw files carry `format_version` (the file shape) and
`harness_version` (the measurement method). A directory whose raw files disagree
about the harness version is **refused**, not warned about: an old and a new
methodology are not one measurement, and averaging them is the silent drift the
P0 risk register names.

**Scope.** The aggregator checks that a report follows from its samples. It does
not decide whether the numbers are *good* — the PRD §12.2 gates already do that,
in the harness, and a reproduced FAIL is still a FAIL. Nor does it re-measure:
`-aggregate` reads a directory and never runs an index.

## Profiles, and the rule that a fix cites one (SW-129)

FR-8 carries two acceptance criteria that only work together — *a missed
performance gate produces profiles* and *no optimisation without a profile* —
and PRD §8.5 states the process rule: every production fix starts from a
gold-corpus error, a regression test, **a reproducible profile**, or a clear
security finding.

**The rule.** A change that responds to a §12.2 gate — an index made faster,
memory brought down, a stall removed — **cites the profile from the run whose
gate it responds to**: the run directory, the gate id, and the profile file. Not
a profile taken later on a developer laptop, and not a plausible explanation. If
the profile does not exist, the fix has no evidence yet, and producing one is the
first step of the work rather than the last.

That rule is affordable only because the profile is a by-product of the failure
rather than a follow-up task, which is what the automation below is for.

**What happens on a miss.** Any `-full-run` (single or `-cold-runs N`) reads its
gates, and if any of them FAILED it immediately re-runs the affected scenario
under four profilers and writes them into the same run directory as the raw
samples:

```
docs/eval/runs/2026-07-28-ubuntu-latest/
└── profiles/
    ├── profiles.json          which gate each set answers for, with digests
    └── cold_index/
        ├── cpu.pprof          CPU time
        ├── heap.pprof         live objects, after a forced GC
        ├── allocs.pprof       total allocations (runtime.MemProfileRate sampling)
        └── io.pprof           the block profile — see the caveat below
```

One directory per affected scenario (`cold_index`, `query_latency`,
`incremental`, `progress_stalls`). Read any of them with:

```sh
go tool pprof docs/eval/runs/2026-07-28-ubuntu-latest/profiles/cold_index/cpu.pprof
```

`report.json` and `run.json` both reference the sets, each naming the gate it
answers for and the threshold it missed, so "which profile explains this FAIL"
is answerable from the artifact alone.

**Off on the normal path.** A green run profiles **nothing**: the profiler is
not merely written to a discarded file, it is never started, no directory is
created and no runtime sampling rate is touched. A harness that profiled every
run would distort the numbers it exists to establish. The automation can be
turned off with `-profile-on-miss=false` — the cold series passes exactly that
to its child runs, because the series profiles a missed gate once, itself —
and a run that misses a gate with profiling off says so in the log.

**Two caveats, stated rather than assumed.**

1. **The profiles come from a diagnostic re-execution, not from the measured
   run.** AC-4 forbids profiling the measurement, so the profiler starts only
   after a gate has been read as missed and re-runs that scenario on the same
   machine, the same checkout and the same binary. It localises where the cost
   is; it is not a replay of the exact execution that missed the gate. The
   scenario's setup — cloning, and the index a query or freshness scenario needs
   first — happens outside the profile window, so a warm-latency profile never
   contains the ingest that made the queries possible.
2. **`io.pprof` is the runtime *block profile*.** Go has no file-I/O profile:
   the runtime does not attribute blocking syscalls to a pprof profile. The
   block profile shows goroutine blocking on channels and locks — where an
   ingest worker pool's waiting on I/O becomes observable — and the run's real
   block-device counters (`getrusage` `ru_inblock`/`ru_oublock`) are published
   beside it in `io_counters` rather than inferred from it.

**A failed capture is not a green run.** If the profiles cannot be produced —
an unwritable path, a scenario that cannot be re-executed — the set is recorded
as incomplete, the reason is printed, and the run exits non-zero. "No profile"
and "no problem" must not look alike in CI.

**Scope.** This is measurement infrastructure only: nothing here runs in the
shipped binary, and interpreting the profiles or acting on them is WP4 work with
its own stories.

## The published baseline, and what a complete run costs (SW-130)

Everything above is instruments. The first measurement they produced is
[`runs/2026-07-28-ubuntu-latest/`](runs/2026-07-28-ubuntu-latest/) — two
complete runs on the frozen candidate **v0.7.0 at `5815db5`**, over the five
pinned Go repositories, with the report in `p0-baseline.md` / `p0-baseline.json`
and every sample beside it.

**Method — how a run is made readable.** A §12.2 gate is only read when
`candidate_match` is true, i.e. when `git rev-parse HEAD` equals the SHA the
evidence index cites and the worktree is clean. Three things follow, and each
measurement job does all three:

1. **Check the candidate out**, and build the harness from that checkout. The
   built binary is what runs — never `go run` from a working tree.
2. **Take the candidate citation from the dispatched ref**, copied out of the
   tree *before* the tree moves onto the candidate. A commit cannot contain its
   own hash, so the freeze record naming a SHA never exists at that SHA; the
   citation is an external fact about the artifact, which is what `-candidate`
   is for.
3. **Write every output outside the checkout.** An untracked file — a report, a
   run directory, or the harness binary itself — sets `worktree_dirty`, and a
   dirty worktree forces the same blanket UNKNOWN as a wrong revision.

> **CORRECTION 2026-07-29 (SW-130 review, finding 2) — added, step 1 above is
> unchanged and still accurate.** "The built binary is what runs" means the
> **eval harness**, built from the candidate's checkout with the product packages
> linked into it. It is **not** the `graphi` release binary: the harness has no
> external-binary path, and the published release assets are built with 22 build
> tags and `-trimpath` while the harness build carries neither. So the *source*
> measured is the candidate's, byte for byte; the *build* is not the release
> build. SW-130's AC-1 asked for "the release binary of the frozen candidate" and
> this protocol does not deliver that reading — the external-binary path (option
> C) remains open work. Nothing above should be read as satisfying it.

**What a complete run costs.** This is the figure that constrains scheduling for
every later P0 band, and it is read from the harness's own accounting rather
than off the CI job clock: each measurement step stamps a timestamp immediately
before invoking the built binary and immediately after it returns, so checkout,
toolchain setup, the candidate checkout and the compile all sit outside the
window. The per-job figures are in `p0-baseline.json` under `run_cost`; the
headline is in the table below.

A complete run has **two** costs and they are not interchangeable:

- **Runner cost** — the sum over all twenty measurement jobs. This is what one
  complete run consumes in runner capacity.
- **Critical path** — the longest single job. This is what an operator waits,
  because the twenty jobs are independent and run in parallel.

| Run | Runner cost (sum of 20 jobs) | Critical path (longest job) | CI job clock, for contrast |
|---|---|---|---|
| run-a — dispatch [30377165970](https://github.com/samibel/graphi/actions/runs/30377165970) | **1070 s** (17 m 50 s) | **455 s** (7 m 35 s) — freshness/grpc-go | 511 s (8 m 31 s) |
| run-b — dispatch [30379259481](https://github.com/samibel/graphi/actions/runs/30379259481) | **1094 s** (18 m 14 s) | **457 s** (7 m 37 s) — freshness/grpc-go | 554 s (9 m 14 s) |
| both runs, as PRD §16 requires | **2164 s** (36 m 04 s) | — | 1065 s (17 m 45 s) |

The two costs answer different questions. **2164 harness-seconds** is what the
PRD's "two consecutive complete runs" consumes in runner capacity; **~7 m 37 s**
is what an operator actually waits, because the twenty jobs are independent and
GitHub runs them in parallel. The critical path is the same job in both runs —
freshness over grpc-go — which is also the only job holding a failing gate, so
the slowest thing to measure and the thing most in need of a fix are one and the
same.

The last column is the CI job clock and is shown only so the two are not
confused. It is larger than the runner cost per run for a different reason than
one might expect: it is the *elapsed* time of the whole dispatch, twenty jobs
wide, not a sum. Neither column may be substituted for the other.

The GitHub job clock is deliberately *not* the number quoted: it additionally
contains checkout, toolchain setup and the compile, and before SW-130 three of
the four measurement jobs ran `go run ./cmd/eval` inside the measured step, so
the compile sat inside the step being timed. It never fell inside a *measured
window* — those are in-process, around `ing.IngestAll` and around each timed
operation — so no published number was ever corrupted by it; it only inflated
the step clock, which is why the cost is taken from the harness instead.
