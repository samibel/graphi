# Retrieval evaluation harness (SW-258)

> **Package:** `internal/eval/retrieval` · **Entry point:** `cmd/retrieval-eval` ·
> **PR gate:** `go test ./internal/eval/retrieval` (hermetic, over `testdata/fixture-repo`) ·
> **Targets:** `docs/eval/retrieval-targets.json` (recalibrated by SW-282, immutable until **SW-283**) ·
> **Budgets:** `docs/eval/retrieval-budgets.json` (immutable until **SW-266**, left byte-identical by SW-282) ·
> **Enforced by:** `go run ./cmd/retrieval-eval -check-targets <report>` and the release-gate `retrieval-targets` runner.

`internal/eval` is the static token-parity harness over prebuilt context strings; it never runs a
retrieval pipeline and cannot produce Recall/MRR/NDCG. This harness is its sibling: it runs real
queries against a pinned repository through the **real engine seams**, scores every ranking against
**graded source spans**, and writes a versioned JSON report whose every published number is
recomputable from exported raw samples. The same discipline as `cmd/eval -aggregate`: a published
number that does not follow from its samples is an error, not a rounding note.

## What is measured

Seven baselines, executed by name and in this order:

| Baseline | Seam | Notes |
|---|---|---|
| `lexical` | `engine/search.Service.Search` | the store's FTS5 bm25 ranking over qualified names (SQLite, the shipped backend) |
| `hybrid_v1` | `engine/agenttools/hybridsearch.Search` (`search_hybrid/1`) | lexical retrieval + identifier/path/degree signals, no vectors; the weights hash is stamped in `method` |
| `semantic_name_only` | `engine/search.Service.SemanticSearch` | v1 `NodeText` over exactly the production-v3-eligible node IDs; on the default build (no embedder) it is reported `unavailable` with the engine's typed reason — never zeros |
| `chunk_only` | `engine/retrieval.Retrieve` (`ModeLexicalOnly`) | the SW-263 lexical-only pipeline; no semantic candidates consulted |
| `fusion` | `engine/retrieval.Retrieve` (`ModeFusionNoGraph`) | the SW-263 fused pipeline; integer RRF over lexical + semantic, no graph rerank |
| `semantic_first` | `engine/retrieval.Retrieve` (`ModeAuto`) | the shipped semantic-first pipeline: semantic prefix with lexical backfill |
| `oracle_upper_bound` | the judged spans themselves, grade ≥ 1 ranked by grade | the ceiling the scorer can reach; proves the metric code, not a retriever |

Four evaluator-only baselines are selectable by explicit name and are absent from
`AllBaselines`: `fusion+graph`, the historical bounded graph-rerank experiment;
`lexical_full_document`, SQLite FTS5/BM25 over the exact admitted `SemanticDocument` v3
`Text` bytes; and the SW-272 operator controls `fts5_or_control` and
`fts5_or_control_full_document`. Each OR control uses the corresponding FTS5 table,
tokenizer, document bytes, SQLite driver, and parameterless `bm25()` call; its sole delta is
explicit `OR` between the same quoted prefix terms used by the all-terms query. These are
controls, not reference implementations and not CoIR-compatible references. They live only in
`internal/eval/retrieval`; production's graphstore continues to index qualified names, so the
SW-272 control changes no shipped database or default report bytes. When a configured embedder
is present, the runner also constructs a separate `semantic_name_only` index; this prevents the
name-only label from accidentally querying the production v3 vector index after the v3 migration.

Per baseline, per query: the top-10 hits, then **Top-1, Recall@5, Recall@10, MRR@10, NDCG@10,
first-relevant-rank, and Recall under 600 / 1200 / 2000 context tokens**. Per baseline: index time,
query p50/p95, peak RSS and vector-sidecar size, each with a status (`measured`, `UNKNOWN` with a
reason, or `not_applicable` with a reason). A measure that could not be taken renders `UNKNOWN`,
never zero.

## Matching rule (defined once, tested once)

`SpanMatches` in `internal/eval/retrieval/metrics.go`, stamped into every report as `matching_rule`:

> A hit matches a judged span when `hit.path == span.path` (exact, repo-relative POSIX path) and
> `span.start_line <= hit.line <= span.end_line`. `hit.line` is the node's declaration line as the
> engine reports it (the oracle uses the span's `start_line`). Each judged span is credited once per
> ranking for recall and DCG, so ten hits inside one function do not inflate either.

Derived definitions:

- **Relevant** = judgement grade ≥ `relevant_min_grade` (default 2; a dataset may override).
- **Top-1** = 1 when the first hit is relevant. **Recall@k** = relevant spans covered by the top-k /
  relevant spans. **MRR@10** = 1/rank of the first relevant hit within the top 10, else 0.
  **NDCG@10** uses gain 2^grade − 1 and log2(rank + 1); the ideal ordering is *every* judged span
  by grade (marginal grade-1 spans included), and a hit credits the highest-grade uncredited span it
  matches. **first_relevant_rank** is over the whole ranking and `null` when no hit is relevant.
- **Recall under B tokens** = recall over the longest prefix of the ranking whose cumulative token
  cost is ≤ B. A hit is charged the whitespace tokens (`tokenizer_id: whitespace-fields-v1`,
  `internal/eval`'s `strings.Fields` counter) of its **read window**: `hit_context_window_lines`
  (40) lines from the hit's line, clipped at end of file. The rule is fixed and
  judgement-independent, so a baseline cannot be charged less for being right.
- **no_hit** queries have no relevant span and are not scored on recall; they report
  `negative_hit_at_5` — whether any top-5 hit matched one of the query's grade-0 negative-example
  spans — aggregated as `negative_hit_rate@5` (lower is better).

Aggregates are plain means over scored queries, per baseline: `overall`, per `stratum`, per
`split`. A group with nothing scored reads `status: UNKNOWN` and carries no numbers.

## Dataset schema (`schema_version: 1`)

```json
{
  "schema_version": 1, "id": "cobra-v1", "repo": "cobra", "repo_sha": "<pinned sha>",
  "language": "go", "evidence_class": "agent-annotated, human-reviewed",
  "queries": [{
    "id": "cb-01", "stratum": "exact_identifier", "language": "en", "split": "dev",
    "query": "ExecuteC",
    "judgements": [{
      "path": "command.go", "start_line": 1051, "end_line": 1137,
      "anchor": "func (c *Command) ExecuteC(", "grade": 3,
      "reason": "The declaration of ExecuteC itself.",
      "annotator": "claude-delegate (SW-258 build)", "reviewer": "orchestrator"
    }]
  }]
}
```

- Strata: `exact_identifier`, `exact_path`, `nl_behaviour`, `architecture_flow`, `config_docs`,
  `ambiguous`, `no_hit`. Splits: `dev` | `holdout` — **no ranking weight may be tuned on holdout**;
  the targets file is derived from `dev` only.
- Grades: 3 exact answer span · 2 directly relevant · 1 marginal · 0 irrelevant / negative example.
  Every judgement carries a one-sentence `reason`, the `annotator` and the `reviewer`.
- `anchor` is a substring that must occur inside `[start_line, end_line]` at the pinned commit. It is
  what makes the coverage test catch a *moved* span, not only a truncated file.
- Validation (`Dataset.Validate`) fails closed on any rule; a `no_hit` query may carry no relevant
  span, every other query must carry at least one.

Datasets live under `internal/eval/retrieval/testdata/datasets/`:

| File | Repo | Queries | Runs on PR |
|---|---|---|---|
| `fixture-v1.json` | `testdata/fixture-repo` (in-tree, buildable Go module) | 7 dev + 3 holdout, every stratum | always |
| `cobra-v1.json` | `cobra` @ `a0a6ae02…` (v1.8.0, `corpus/manifest.json`) | 30 dev (≥3 per stratum) + 10 holdout | shape always; span coverage only when a clone at the pin is present, else `SKIP` |
| `grpc-go-perf-v1.json` | `grpc-go` @ `dbbcf599…` (v1.60.1, `corpus/manifest.json`) | 5 dev + 3 holdout, **performance-only** (exact_identifier / exact_path / no_hit) | shape always; span coverage only when a clone at the pin is present, else `SKIP` |

`grpc-go-perf-v1.json` exists to measure the **large** size class for the budgets file (AC-8). It
is not a quality dataset: eight cheap-to-judge queries, no target is derived from it, and
`TestDatasets_GrpcGoDatasetShape` keeps it that way.

Judged span paths are validated to fit the artifact bound (`trust.MaxPathLength`, 240 bytes) and
never to carry the truncation marker, so a bounded hit path (below) can only match the spans its
canonical value matches.

## Span coverage is a test (AC-9)

`TestDatasets_FixtureSpansResolve` always runs. `TestDatasets_CobraSpansResolveAtPinnedSHA` and
`TestDatasets_GrpcGoSpansResolveAtPinnedSHA` look for a read-only clone at `$GRAPHI_CORPUS_COBRA` /
`$GRAPHI_CORPUS_GRPC_GO` or `$HOME/.cache/graphi/corpus/<repo>`, verify `git rev-parse HEAD` equals
the pin, and then check every judgement: regular file, line range inside the file, anchor inside
the range. A stale judgement fails `go test ./internal/eval/retrieval`. The PR path never clones.

## Running

```bash
# Hermetic PR-time run (the fixture repo, all seven default baselines, determinism, aggregate round-trip)
go test ./internal/eval/retrieval

# Dispatch: one pinned repo, one dataset, report + raw samples
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out retrieval-cobra.json -export-raw docs/eval/retrieval/runs/<date>-<runner-class>-cobra \
  -runner-class local            # -checkout <dir> overrides $HOME/.cache/graphi/corpus/cobra

# Reproduce every published number from the raw samples (exit 0 reproduced / 1 discrepancy /
# 2 unreadable / 3 incomplete)
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/<date>-<runner-class>-cobra

# Large size class for the budgets file: the performance-only grpc-go dataset (same dispatch)
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo grpc-go \
  -dataset internal/eval/retrieval/testdata/datasets/grpc-go-perf-v1.json \
  -out retrieval-grpc-go.json -export-raw <dir>

# SW-272 field-parity diagnostic: fixed 2x3 cells, exact grade-3 scoring, and every/only
# dev nl_behaviour query. -field-parity cannot be combined with -baseline or select holdout.
GRAPHI_STATIC_MODEL_DIR=<pinned-model-dir> CGO_ENABLED=0 \
go run ./cmd/retrieval-eval -field-parity -manifest corpus/manifest.json -repo cobra \
  -checkout <pinned-cobra-checkout> \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -out /tmp/sw272-field-parity-report.json \
  -export-raw docs/eval/retrieval/runs/<date>-sw272-field-parity

# Regenerate the frozen files (only from reports checked in beside them; see immutable_until in each file).
# The targets derivation REFUSES a report carrying the candidate pipeline (chunk_only / fusion /
# semantic_first) or any holdout query result, so -targets-report names a comparator-only run over a
# development-only dataset slice; -budget-* still read full runs.
go run ./cmd/retrieval-eval -derive -targets-report <cobra-report.json> \
  -budget-small <fixture-report.json> -budget-medium <cobra-report.json> -budget-large <grpc-go-report.json> \
  -targets-out docs/eval/retrieval-targets.json -budgets-out docs/eval/retrieval-budgets.json
```

`-repo fixture` selects the in-tree fixture without a manifest entry. A URL-pinned entry is never
cloned: the checkout must exist locally at the pinned sha or the run fails closed.

## Report layout (`format_version: 2`, `harness_version: retrieval-eval/1`)

> **Why 2.** Version 2 added `dataset.query_ids` to the report citation, and `-aggregate` compares
> that citation against the dataset rebuilt from its own bytes — the check that catches a query
> removed coherently from dataset, report and raw. A version-1 report carries no such field, so it
> is **refused** rather than read under a gate written for the stronger shape
> (`TestAggregate_DetectsDrift/a report from the previous format version is refused…`).
> `harness_version` did not move: what a hit is, how it is charged and which seams run are unchanged.

- `reproducible` — a pure function of candidate SHA, repository at its SHA, dataset bytes and
  harness/scorer version: header (`candidate_sha`, `runner_class`, `repo` with node/edge/file
  counts, `dataset` with `sha256` and sorted `query_ids`, `tokenizer_id`, `top_k`, `token_budgets`,
  `hit_context_window_lines`, `relevant_min_grade`, `matching_rule`) and the per-baseline results
  (`status`, `reason`, `method`, per-query `hits` + `metrics`, `overall`, `strata`, `splits`).
  **Byte-identical across two runs over the same inputs** (`TestRun_ReproducibleSectionIsByteIdenticalAcrossRuns`).
- `performance` — per baseline `index_ms`, `query_p50_us`, `query_p95_us`, `latency_samples`,
  `peak_rss_mb`, `vector_sidecar_bytes`; the block is `PerformanceFromRaw` over
  `raw/latency-<baseline>.json` and is recomputed exactly — status, value, unit and reason — so an
  `UNKNOWN` or `not_applicable` figure is checked against the raw record, not against itself.
- `environment` — `generated_at`, `os`, `arch`, `go_version`, `cpu_count`; checked for presence only.

Hit fields under repository control (`path`, `node_id`, `qualified_name`) are bounded at
`trust.MaxPathLength` (240 bytes) with a visible `…[truncated]` marker before they enter the report
or the raw files (`context/standards.md`); scoring runs over the canonical value.

Run directory (`-export-raw`): `run.json` (index with per-file sha256),
`<dataset-id>-report.json` (the single published report), `dataset.json` (the exact judged bytes),
`raw/hits-<baseline>.json` (every ranking, nothing
derived), `raw/latency-<baseline>.json` (every timed execution + the single-sample measures
`index_ms` / `peak_rss_mb` / `vector_sidecar_bytes` with their status and reason). An unavailable
baseline's raw records say `collected: false` and carry the typed `reason` — the only thing that can
justify `unavailable` in the report.

`run.json.report` is authoritative. New exports use the dataset-qualified name so the AC gate and
`-aggregate` read the same bytes without retaining a byte-identical `report.json` alias. The reader
still accepts historical directories whose index names `report.json`.

Every raw file is read **twice-identified**: `run.json` says which series and baseline a file is,
and the file says the same about itself (`format_version`, `harness_version`, `series`, `baseline`,
`collected`, `samples`). `ReadRunDir` requires the two to agree. The per-file sha256 proves the
bytes were not edited after the run; it does *not* prove the index and the payload mean the same
thing by them — swapping two raw files and re-stamping the index leaves both digests valid, and
only this check refuses it.

`-aggregate` is closed-world first: the report's `dataset` citation (`id`, `sha256`,
`evidence_class`, counts, sorted `query_ids`) must equal the same citation rebuilt from
`dataset.json` — whose sha256 is recomputed from its bytes, never read from `run.json` — and the
baseline universe on every side (report results, performance blocks, raw hits series, raw latency
series listed in `run.json`) must equal the harness's exact default, legacy, or field-parity
closed-world baseline set. A query removed
coherently from the dataset copy, the report and every raw series is therefore caught by the
citation the report still carries; a tamperer who also rewrites that citation has produced a
different report, and the `derived_from.sha256` in the targets/budgets files no longer matches it —
that provenance layer, not the aggregate, binds a checked-in artifact to its report.

It then checks, per baseline, **exact query-id set equality** between the report, the dataset
copy and each raw series (an omitted or extra query on any side is a discrepancy, whether or not
the aggregates were re-averaged); recomputes every per-query metric through the same `Evaluate`,
every aggregate through the same `Aggregate` over the *raw* hit set in dataset order, every
performance block through the same `PerformanceFromRaw`; compares the report's hit lists to the raw
hit lists; and checks the complete shape of an `unavailable` baseline against **both** raw records
(status only when both say `collected: false`; report reason == hits reason == latency reason;
zero samples and an empty query set in both records; zero queries, `UNKNOWN` aggregates and
`UNKNOWN` measures in the report). Every comparison is `reflect.DeepEqual` — no tolerances.

## Targets and budgets

`docs/eval/retrieval-targets.json`: per stratum, the best single-baseline value of every
metric over the dev split, the oracle ceiling, a `fusion_target` on `nl_behaviour` and
`architecture_flow` (best `ndcg@10` + 0.10, capped at the ceiling) and the Top-1 `no_regression`
floor on `exact_identifier`. `docs/eval/retrieval-budgets.json` (AC-8): per fixture size class —
small = the in-tree fixture, medium = cobra, large = grpc-go (performance-only dataset) — index
time, worst indexed-baseline query p95 and peak RSS with the measurement each came from and
budget = measured × 2.0; a class with no measurement reads `UNKNOWN`. Both carry `date`,
`derived_from` (report path + sha256) and their own `immutable_until`. The reports they were derived
from are checked in under `docs/eval/retrieval/runs/`.

### SW-282: the targets became executable, and they are missed

Until SW-282 the targets file was written by `-derive` and read back by exactly one Go test. No
release command evaluated it, so a missed target was invisible outside that test. It is now enforced
two ways, both reading one implementation (`retrieval.CheckTargets`):

```bash
go run ./cmd/retrieval-eval -check-targets docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/cobra-v2-dev-report.json
```

and the `retrieval-targets` runner in `cmd/release-gate` (`DefaultGates`, and `requiredGates` so an
absent gate is detected as absent rather than read as a pass). Comparisons use the stored values or
exact integer counts, never rounded display decimals.

The file was recalibrated on 2026-09-06 from
`runs/2026-09-06-sw282-recalibration-local/` — a comparator-only
(`lexical`, `hybrid_v1`, `semantic_name_only`, plus `oracle_upper_bound`) run over the
development-only slice of the frozen release dataset `cobra-v2`, with the production static embedder
configured so `semantic_name_only` is no longer `unavailable`. `DeriveTargets` **refuses** a report
carrying a candidate-pipeline baseline (a bar set to the candidate plus a delta is not a bar) and
**refuses** a report carrying any holdout row rather than filtering it out (a dropped holdout row
cannot be told apart from one that was never executed).

The shipped pipeline is graded against a **separately committed** report,
`runs/2026-09-06-sw282-gate-local/`, and `CheckTargets` refuses to grade the report the targets were
derived from. Today's verdict, recorded in `targets-gate-expectations.json` and re-derived on every
PR:

| target | required | observed | verdict |
|---|---|---|---|
| `nl_behaviour` fusion_target | ≥ 0.54497025306999103 | 0.63677401077084517 | PASS |
| `architecture_flow` fusion_target | ≥ 0.4578575262772977 | 0.32777888533499866 | **MISS** |
| `exact_identifier` no_regression | top1 ≥ 1 | 0.75 | **MISS** |
| `bundle_coverage` | 6 of 6, 0 misses, budget 1200 | 6 of 6 | PASS |
| `qrel_blind_smoke` | `RELEASE: YES` | `RELEASE: NO` (31/64 vs k=56) | **MISS** |

The misses are **recorded, not excepted**. SW-263's `0.00085` numeric shortfall exception was
deleted rather than restated: a tolerance four orders of magnitude below what a five-query stratum
can resolve asserts nothing. SW-263's recorded −0.00084 MISS against the old bar stands as
history. Fixing these misses is the recovery story's job; the release line is red until then, which
is the gate working.

### `bundle_coverage`: the SW-264 gate, and what it does NOT say

`bundle_coverage` gates the six dev `nl_behaviour` queries `SelectTaskContextDevNLBehaviour` selects
(`cb-11`…`cb-16`), at token budget 1200, under `retrieval.TaskContextMatchingRule`. Its threshold
is written in **whole queries** — `covered_queries_required: 6`, `max_misses: 0` — with the
population's resolution (`1/6`, 16.7 percentage points) beside it. There is no decimal share,
percentage or fractional threshold field, and a test enumerates the object's keys and fails if any
number in it is not an integer. The bar is zero mechanical misses because that is what the
aggregate's own no-miss rule implies, not because it is what SW-264 happened to measure; if a
re-measurement misses, the miss is recorded and the threshold is not lowered to it.

**It supplements and does not replace the qrel-blind bundle-sufficiency smoke gate**
(`runs/2026-09-05-sw280-qrel-blind-smoke/`). Coverage asserts that a reviewed grade-3 span is
*present* in the bundle bytes; the smoke gate asserts that a reader could *answer* from those bytes.
SW-280 is the direct evidence that the two can disagree — 31 of 64 against a pre-registered
`k=56`, with graders repeatedly locating the span outside the retrieved bytes. The
`retrieval-targets` gate requires **both** and fails when either is unmet **or absent**.

### The `cobra-v2` holdout has been opened and is spent

SW-280 opened the sealed holdout: all 64 answerable holdout queries were bundled, answered and
graded. It **can no longer validate a target, a threshold or a changed candidate** — any such
prospective validation from here requires a *fresh* holdout, stratified before it is sealed. No
target in the targets file is set from or gated on a holdout observation, and `DeriveTargets`
refuses a report that carries one.

### Answerable population and stratum composition

`answerable-population.json` records the corrected counts, recomputed from `cobra-v2.json` by a test
that fails on drift. "Answerable" is SW-266 AC-2's definition: not `no_hit` **and** at least one
grade-3 answer span. Under it the development split holds **40** queries, the holdout **64**, and
the full savings population **104** — not the 41/105 that a looser `stratum != no_hit` count
produced and that SW-279's approval recorded. The one query the two readings disagree about,
`cb-31` (`ambiguous`, five grade-2 judgements, no grade-3 span), is named with its reason in the
exclusion record.

`retrieval.AnswerableQueries` resolves that disagreement in favour of the contract;
`retrieval.AnswerableHoldout` still **refuses** it, and the asymmetry is deliberate: the holdout's
`N` fixes a pre-registered `k` before any response is opened, so an ambiguous holdout population must
stop the run, whereas the development split pre-registers nothing.

The composition binds what may be claimed. The answerable holdout is **57 of 64 `config_docs`
(89%)** and the full 104-query answerable savings population is **76 of 104 (73%)**. Any future
claim resting on this holdout is either narrowed to `config_docs` or requires a holdout stratified
before sealing — a mostly-`config_docs` sample may not be presented as generic developer
questions. That constraint is recorded rather than executed: expressing it inside the claim grammar
would change `allowedClaimShape` and therefore the frozen contract version.

### Superseded frozen-input digest (SW-280's precondition record)

`runs/2026-09-05-sw280-qrel-blind-smoke/end-of-run-hash-comparison.json` froze
`docs/eval/retrieval-targets.json` at sha256
`26a5ea05657d18b687f42f53fbebde2876e016de90b0d22b21f0441f236a0592`. **SW-282** rewrote that file,
which now hashes to `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d`. The sealed
run is evidence and has not been edited: its frozen targets digest is **historical**, and the drift
is by design rather than a defect. `docs/eval/retrieval-budgets.json`, the dataset, the grading
rubric and the methodology are all still byte-identical to what that run froze, and
`TestQrelBlindSmoke_CommittedRunFrozenInputsStillHashTheSameAtRest` still fails for any of them.

## SW-263 AC-9 evaluation runs

The `chunk_only`, `fusion` and `fusion+graph` baselines in this harness
are the SW-263 retrieval ablations (`engine/retrieval` in
`ModeLexicalOnly`, `ModeFusionNoGraph`, and `ModeAuto` respectively).
The gate in `internal/eval/retrieval/targets_test.go` is now
`TestTargets_GateVerdictMatchesCheckedInExpectations`. It evaluates the
targets file through `retrieval.CheckTargets` against the EXPLICIT
named report `retrieval.GateReportPath` (bound to
`retrieval.GateCandidateSHA`, never picked by filesystem mtime) and
compares the per-target verdict against
`docs/eval/retrieval/targets-gate-expectations.json`, failing on drift
in EITHER direction. `TestReport_MeetsAC9GateAgainstTargetsFile` and
its numeric shortfall exception were removed by SW-282.

**Embedder caveat (read this before citing fusion numbers):** the fusion
ablations require a configured embedder; the SW-258 targets were derived
without one (so `semantic_name_only` was `unavailable` there). The AC-9
runs that exercise fusion therefore ran with the `-embedder` selector
`ollama` (loopback default), whose construction lands the production
embedder with the model `nomic-embed-text`. The **embedder ID the
runner stamps in the report** for those runs is `ollama:nomic-embed-text`
— note that this is the embedder's identity (`scheme:model`), **not** a
selector form (`scheme:host:port` for `ollama`). The selector that
produced the running embedder was the bare `-embedder ollama`, never
`-embedder ollama:nomic-embed-text`: that selector is invalid because the
segment after the colon is the endpoint, not the model name, and the
loopback guard would refuse the non-IP host `nomic-embed-text`. The
historical run directories under `docs/eval/retrieval/runs/<date>*/`
that name "ollama:nomic-embed-text" are recording the embedder ID, not
the selector — and the spec's eventual production embedder is the SW-262
static path (`-embedder static:potion-code-16M-v2@<revision>`), not the
loopback Ollama. The fusion numbers are model-dependent and will move
when the static embedder lands. Do not present AC-9 numbers as the
spec's final numbers.

The 2026-08-31 cobra run (`docs/eval/retrieval/runs/2026-08-31-local/`)
is the SW-263 AC-9 evaluation: the `README.md` in that directory records
the actual numbers and the embedder id, and the verdict against the
targets of the day. Since SW-282 the gated run is
`docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/` and the targets
are the recalibrated ones.

## What this harness does not do

- It does not enable an embedder by default. With no `-embedder`, every semantic baseline remains
  explicitly `unavailable`; an opt-in run builds production v3 vectors and, when selected, the
  separate name-only control vectors.
- It does not tune anything. The holdout split exists so that later stories cannot.
- It does not compute the `grep+read` token baseline (SW-266).
