# Hero-Task Protocol (SW-122 / EVAL-01)

> **Status:** ACTIVE — suite versioned, smoke-gated; execution against the pinned
> real repos happens in CI (SW-123 / EVAL-02).
> **Suite:** `corpus/hero/` (20 tasks) · **Gate:** `cmd/eval/hero_test.go`
> **Budgets:** `docs/eval/hero-budgets.json` (schema now, numbers from EVAL-02)

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
| `empty`     | hero-03, hero-04 | honest empty answers instead of near-miss noise |
| `not_found` | hero-05 | unknown symbols yield the typed not-found outcome |
| negative anchors | hero-02, -07, -10, -12, -14, -15, -20 | evidence never cites symbols that cannot appear |

hero-04 deliberately pins the **shipped** `definition` semantics (outbound
`defines` edges — "a symbol points at what it defines", so a leaf function is
`empty`). If that contract is ever redefined, the task must change in the same
reviewed diff.

### Fixtures

- `tier1-fixture-go` (`corpus/fixtures/go`) — the frozen SW-110 byte-parity
  oracle: single file, rich shapes (ambiguity, call chains, interfaces). It
  must **not** grow — golden snapshots pin its bytes.
- `tier1-fixture-hero-go` (`corpus/fixtures/hero-go`) — added by SW-122 for
  cross-file behaviors (cross-file callers, `related_files` ranking,
  type-usage references) that a single-file fixture cannot express.

## Runner class and budgets (ADR 0003 U5)

Reference runner: **`ubuntu-latest`** (GitHub-hosted, linux, `CGO_ENABLED=0`)
— the same class every existing gate workflow uses. Local runs are smoke, CI
runs are evidence.

Absolute latency/rows budgets are **not invented**: `corpus/hero` tasks carry
no `max_latency_ms` (enforced by `TestHeroSuite_FailureClassesRepresented`),
and every budget field in `docs/eval/hero-budgets.json` is `null` until the
first reproducible EVAL-02 run on the reference runner freezes it as a
ratchet, citing commit, workflow run, and report artifact.

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

## EVAL-02 handoff (SW-123)

1. CI workflow (extending the `corpus.yml` / `eval-correctness.yml` pattern)
   runs, per pinned repo: full index (wallclock, peak RSS, DB size), warm p95
   per op class, then the 20 hero tasks; raw `internal/evalreport` JSON is
   published as an artifact **and** committed under `docs/eval/`.
2. First green run freezes the `null` budgets in `hero-budgets.json` as
   ratchets (separate reviewed commit citing the run).
3. The same measurements close ADR 0003 U2 (brief aggregate strategy) and U4
   (whole-graph cache disposition).
