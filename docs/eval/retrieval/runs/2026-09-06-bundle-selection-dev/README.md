# Development recovery: ranking green, more answer source, savings unproven

The architecture ranking target passes without changing the model, dataset,
scorer, thresholds or 1,200-token argument. The subsequent bundle-selection
change makes the ranking improvement visible as actual source: all five
architecture development questions now receive at least one complete grade-3
span, versus two before this bundle change. Across the 40 grade-3-answerable
development questions, that diagnostic improves from 29/40 to 33/40.

This is not a release pass or a blind sufficiency result. Four questions still
have no emitted source overlapping a grade-3 answer. Mean complete-response
cost increases from 7,173.20 to 8,018.55 cl100k tokens. The product claim of
answering in fewer tokens than grep-plus-read remains unearned by this run.

## Scope and identity

- Corpus: clean Cobra at `a0a6ae020bb3899ff0276067863e50523f897370`.
- Input: the existing dev-only `../2026-09-06-sw282-gate-local/dataset.json`,
  SHA-256 `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
  All 44 records are retained: 40 grade-3-answerable, one grade-2-only
  (`cb-31`), and three `no_hit`. The unchanged ranking harness scores 41;
  grade-3 source diagnostics explicitly use 40. No held-out query was run,
  inspected for tuning, or rated during this work.
- Model throughout: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
  No generative answer model participates in these retrieval/bundle runs.
  A different embedding model might improve remaining misses, but was not
  tested and is not necessary to explain the observed implementation defects.
- Current methods: `retrieval/5`, `context-definitions/2`, `task_context/2`.
  The historical evaluation slot/dispatch label `semantic_first` remains for
  compatibility; natural-language rows identify their region as
  `evidence_ranked`. It does not mean the old immutable-prefix ranking is used.
- `after/` contains a fresh seven-baseline run, raw hits, latency observations,
  dataset copy and aggregate verification. Candidate binding is explicitly
  `5d0ef8729be141e901e996d8660ec5bfe6dd182d+dirty`. These are working-tree
  development artifacts, not clean-commit-bound release captures.
- Reference before the whole repair: `../2026-09-06-recovery-dev/before/`.
  Reference after exact-name/source-boundary repairs but before architecture
  rescoring: `../2026-09-06-recovery-dev/after/`.
  Immediate before for this bundle task: `../2026-09-06-architecture-dev/`.
  The separately captured `bundles-inherited.json` already contains an
  unfinished broader-pool experiment; it is not substituted for that baseline.

## Diagnosis and implemented changes

The diagnosis followed the `diagnosing-bugs` loop: isolate a failure, show it
in a focused test or preserved development capture, change one mechanism,
and compare the complete development population. Rejected experiments below
are retained as negative results, not silently replaced by the winning run.

1. **Exact names lost to semantic neighbours.** The original semantic prefix
   put `GenMarkdown` ahead of the exact `GenMarkdownTree` declaration. Actual
   qualified-name/suffix equality is now promoted before truncation; partial
   name scores do not get that privilege. Exact-identifier Top-1 is 4/4,
   up from 3/4. Unknown names keep the previous fallback.
2. **Architecture needs candidate admission as well as rescoring.** The
   immutable semantic prefix prevented complementary lexical evidence from
   competing. Natural-language ranking now scores the lexical/semantic union
   with a bounded name-term bonus and admits direct callees before the Top-K
   cut (at most 12 wrappers, four call edges per wrapper). Callees inherit the
   wrapper's base, not its name bonus, add their own term bonus, and are capped
   below the admitting wrapper. Details and prior experiments are preserved
   in `../2026-09-06-architecture-dev/README.md`.
3. **A rank-10 answer was still outside a five-source bundle.** For multi-word
   questions, `taskctx` now hydrates up to 15 ranked candidates through one
   retrieval call. Only five remain primary items and drive the existing
   bounded graph-neighbour reads. Extra candidates compete for source budget;
   an extra `candidate:` item is retained only when its source was emitted.
   The 40-item default and the 1,200 snippet budget are unchanged.
4. **Hydration and point windows lost the intended source.** The earlier
   repair preserves retrieval order across canonical-ID-ordered hydration,
   reuses parser declaration boundaries, and uses contiguous query-focused
   windows for long declarations. Bounded parser input and point-window
   fallback remain for unsupported/malformed sources. All emitted bytes are
   checked against their cited source lines; no synthetic source is produced.
5. **The same declaration could be paid for twice.** Source selection now
   deduplicates identical citation ranges, retaining the earliest rank. The
   focused duplicate-declaration test failed with 18 tokens instead of 12
   before the repair. This is exact-range deduplication, not a claim that all
   possible overlapping windows are eliminated.
6. **Equal reservations fragmented relevant late candidates.** Multi-word
   selection now scores the actual proposed source window by distinct query
   terms, a modest upstream-rank prior, and sublinear source cost. A candidate
   competes as a complete declaration if it fits, otherwise as a window of
   at most half the total budget when there are multiple sources. The priority
   is `(1+term_matches)*1000*(n+1)/(n+1+rank_index)`, divided by
   `max(1, floor(sqrt(snippet_tokens+16)))`. Stable greedy selection spends
   no more than the original budget; original ranks remain provenance.
   A synthetic relevant sixth source failed before this allocation change
   and passes afterwards.
7. **Breadth was wrong for a single-word topic.** Applying the wider pool and
   cost preference to `cb-35` ("completion") truncated the previously complete
   `InitDefaultCompletionCmd` span. Single-word names, paths and topics now
   retain the five-candidate pool and earlier fair-reservation/depth policy.
   This is a query-shape rule, not an identifier or dataset-ID exception.
   It restores that span and avoids unnecessary expansion for exact lookup.
8. **The cap left metadata without surviving items.** Unreferenced non-source
   evidence is retired after the item cap; emitted snippets and references
   needed by retained items survive. No increase to the cap was used. This
   improves integrity but does not by itself make a missed answer a candidate.
9. **An index-build nonce was charged as content.** The random freshness
   fingerprint is no longer interpolated into the actor-visible summary.
   Internal stale-index checks still use it; model, method and source identity
   remain visible. Two independent builds have different freshness generations
   but identical bytes, payload digests and tokenizer counts for all 44 inputs.

The final pass also corrects retrieval auditability: explicit `Base` scores
make `Final = Base + RRF + Graph + Classification` checkable, and the weights
hash includes the actual flow constants. Bounded graph-read failures propagate
instead of silently becoming successful retrieval; the existing task-context
lexical fallback remains explicitly marked as degraded. Production and eval
adapters expose the same added score metadata. Ranking scores were not changed
by these audit/error-handling corrections.

## Existing ranking harness

| Metric | Original before repair | Final | Frozen requirement |
|---|---:|---:|---:|
| Architecture nDCG@10, 5 queries | 0.3277788853 | 0.4624671523 | 0.4578575263 — PASS |
| NL behaviour nDCG@10, 6 queries | 0.6367740108 | 0.7029047224 | 0.5449702531 — PASS |
| Exact identifier Top-1, 4 queries | 3/4 | 4/4 | 4/4 — PASS |
| Exact path nDCG@10, 3 queries | 1.0 | 1.0 | — |
| Config/docs nDCG@10, 19 queries | 0.4114267544 | 0.4358348732 | — |
| Ambiguous nDCG@10, 4 queries | 0.4216634707 | 0.4216634707 | — |
| Overall nDCG@10, 41 scored | 0.5096786853 | 0.5645491768 | — |
| Overall Top-1 | 21/41 | 22/41 | — |

This does not mean every metric improved: config/docs Top-1 falls from 8/19
to 7/19 relative to the pre-rescoring baseline, while its nDCG rises. The
architecture margin is only 0.00461 on five development queries. Neither
generalization nor robustness to other models/corpora/languages is established.

All seven final `raw/hits-*.json` files are byte-identical to the immediate
`architecture-dev/after/raw/` reference. Thus this bundle change does not trade
away that ranking improvement. Report method stamps and weights hashes differ
because audit metadata changed. The existing `-aggregate` reproduces 761/761
metrics with zero discrepancies or unknowns; reproduction is not a quality pass.

The target check still exits 1: the historical `qrel_blind_smoke` is red. Its
`bundle_coverage` PASS references the old six-query SW-282 artifact, not a fresh
evaluation of this candidate. Neither historical line is presented here as
current-candidate validation. Holdout, blind sufficiency and release remain
unmeasured for the final candidate.

The original pre-rescoring values above come from raw JSON, correcting two
transcription errors in the earlier architecture README: config/docs was
0.4114267544, and overall Top-1 after the exact-name repair was 22/41, not 21/41.

## Actual serialized bundles, not citation-only credit

The MCP capture uses `surfaces/mcp.Server.Serve`, including JSON-RPC envelope,
escaped tool-result JSON and final newline. Token counts are calculated from
the preserved bytes, not a reconstructed result. A non-source relation citation
does not count as answer-bearing source. Overlap uses existing `SpanMatches`
at exact grade 3; containment additionally requires every judged line to occur
in emitted snippets, possibly across multiple snippets. Neither is a rater's
judgement of answer sufficiency.

| Diagnostic, 40 grade-3-answerable questions | Immediate before | Final |
|---|---:|---:|
| Any emitted source overlapping grade 3 | 33/40 | 36/40 |
| At least one complete grade-3 span | 29/40 | 33/40 |
| Every grade-3 span complete | 17/40 | 21/40 |
| Complete grade-3 spans | 31/63 | 39/63 |
| Mean snippet whitespace tokens | 1071.75 | 1107.60 |
| Mean complete-response cl100k tokens | 7173.20 | 8018.55 |
| Independent-build byte/digest/token equality | 44/44 | 44/44 |

No individual question loses overlap or complete-span count relative to the
immediate architecture-dev reference. Per-query changes are reproducible with
`changes.jq`. The remaining zero-overlap questions are `ci-511`, `ci-678`,
`ci-771`, and `ci-1222`; a higher aggregate does not erase these misses.

For context, before any repair the emitted-source complete-span diagnostic was
8/40 questions and 8/63 spans, with 0/44 byte-identical builds. Those observations
are in `../2026-09-06-recovery-dev/bundles-before.json`; they are not a blind
answerability score and are not a fresh measure of the 64-question holdout.

| Architecture question | Target declaration ranks in first 50 | Complete spans before → final |
|---|---|---:|
| `cb-19`, execution lifecycle | `ExecuteC` 3; `execute` 6 | 1/2 → 1/2 |
| `cb-20`, persistent flags | `mergePersistentFlags` 6; `updateParentsPflags` 21 | 0/2 → 1/2 |
| `cb-21`, command parent wiring | `AddCommand` 10 | 0/1 → 1/1 |
| `cb-22`, documentation traversal | `GenMarkdownTreeCustom` 10 | 0/1 → 1/1 |
| `cb-24`, flag groups | `enforceFlagGroupsForCompletion` 1 | 1/1 → 1/1 |

`execute` now overlaps emitted source but is not fully contained.
`updateParentsPflags` remains outside the 15-candidate source pool. Therefore
"all five have a complete span" must not be read as "all five have all answers."

## Ablations and rejected experiment

These are successive development snapshots, not independent trials. Every row
has preserved real MCP bytes for all 44 inputs and two independent indexes.
The table is derived by `metrics.jq`; it excludes the same four non-grade-3
records throughout. Intermediate working-tree code was not committed, so the
preserved payloads are the authority for recounting intermediate observations;
only the final implementation is directly rerunnable from this delivery.

| Capture | Overlap queries | Complete queries | All-complete queries | Complete spans | Mean cl100k |
|---|---:|---:|---:|---:|---:|
| `architecture-dev/bundles-after.json` | 33 | 29 | 17 | 31 | 7173.20 |
| `bundles-inherited.json`: wider pool, old allocation | 36 | 29 | 17 | 34 | 8197.575 |
| `bundles-dedup.json`: identical source ranges once | 36 | 29 | 19 | 36 | 8259.125 |
| `bundles-value.json`: query/rank/cost allocation | 36 | 32 | 20 | 38 | 8243.60 |
| `bundles-selected-only.json`: source-backed extras, method stamp | 36 | 32 | 20 | 38 | 8245.25 |
| `bundles-compact.json`: REJECTED metadata coalescing | 36 | 32 | 20 | 38 | 8300.95 |
| `bundles-before-short-query-guard.json`: audit v5, no coalescing | 36 | 32 | 20 | 38 | 8246.25 |
| `bundles-after.json`: single-word depth guard | 36 | 33 | 21 | 39 | 8018.55 |

The rejected coalescing variant merged duplicate item references/roles before
the 40-item cap. That freed positions for additional metadata and increased
wire cost by 55.70 tokens without any source gain. Its implementation was
removed; the negative capture remains. Likewise, the single-word regression
is visible in the intermediate captures, not hidden by reporting only gains.

## What this says about the budget and the model

The frozen 1,200 parameter limits snippet whitespace tokens in the existing
implementation. The measurement contract separately charges the entire MCP
response with cl100k. No budget, tokenizer, threshold or scorer was altered.
The final bundle costs an additional 845.35 real tokens on average (+11.8%)
relative to architecture-dev, while retaining more answer source. Increasing
source utility under the snippet cap is not the same as reducing actor cost.

This run neither proves that 1,200 is too small nor measures a sufficiency/token
frontier. Equal-recall savings cannot be claimed while answers are missing.
A model change is a separate candidate comparison, not an explanation for
canonical-ID reordering, duplicate snippets, a five-candidate admission cut,
or a random build nonce. The current result supports fixing those mechanisms
first. Further work should address the four source misses and metadata cost;
any broader quality claim needs a new authorized, independently judged
population without tuning on the existing held-out questions.

## Reproduce from the repository root

The pinned model/tokenizer must already be installed; these commands do not
download artifacts. Set model and corpus paths for the local machine. The
tokenizer directory below is the committed SHA-verified vocabulary fixture.
Use a new output directory to avoid overwriting preserved evidence.

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/clean/pinned/cobra
export GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact"
bundle_run=$(mktemp -d)

go run ./cmd/retrieval-eval -repo cobra \
  -checkout "$GRAPHI_RECOVERY_COBRA" \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out "$bundle_run/report.json" -export-raw "$bundle_run/ranking"

GRAPHI_RECOVERY_OUT="$bundle_run/bundles.json" \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

# Recount preserved exact bytes/digests/tokens and source diagnostics offline.
go test ./internal/eval/retrieval -run '^TestRecoveryDevArtifactPayloads$' -count=1 -v

report_dir=docs/eval/retrieval/runs/2026-09-06-bundle-selection-dev
prior_dir=docs/eval/retrieval/runs/2026-09-06-architecture-dev
go run ./cmd/retrieval-eval -aggregate "$report_dir/after"

jq -n -f "$report_dir/metrics.jq" \
  "$prior_dir/bundles-after.json" "$report_dir"/bundles-*.json
jq -s -f "$report_dir/changes.jq" \
  "$prior_dir/bundles-after.json" "$report_dir/bundles-after.json"

# Expected exit 1: ranking passes; the historical blind smoke remains red.
go run ./cmd/retrieval-eval -check-targets "$report_dir/after/cobra-v2-dev-report.json"

go test ./engine/context ./engine/retrieval ./engine/agenttools/taskctx \
  ./cmd/internal/runtime -count=1
go test ./...
go build ./...
go run ./cmd/layerguard
git diff --check
```

For the original before-ranking execution, use the same dev-only command in a
checkout of `5d0ef8729be141e901e996d8660ec5bfe6dd182d`; before MCP capture
instructions are in `../2026-09-06-recovery-dev/README.md`. Do not overwrite
the before captures: their unstable nonce is the defect being demonstrated.

## Validation

Focused source-selection, score-accounting, graph-error, production-composition
and candidate-pool tests pass with `CGO_ENABLED=0`. The offline artifact check
validates all preserved stages, and the fresh final capture passes source-byte
roundtrips and the 1,200 snippet limit for all 44 inputs in both builds.
`go build ./...`, layerguard and `git diff --check` pass. The full CGO-free test
suite passed for 136 test-bearing packages; a final-tree rerun is recorded in
`STATUS.md`. No model/tokenizer pin changed; their run inventories include both
new measurement directories.

Unchanged SHA-256 checks:

- `methodology.md`: `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d`.
- `retrieval-targets.json`: `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d`.
- Full sealed dataset (hash check only): `7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc`.

No commit, push, release approval, or held-out evaluation was performed.
