# Development result: answer-source recovery

This development-only run removes all remaining grade-3 source misses on the
40 answerable development queries without changing the dataset, scorer,
thresholds, 40-item cap, or 1,200-token snippet budget. It is not a release
result: the sealed holdout was neither opened nor run, and the historical blind
smoke remains immutable and red.

## Diagnosis and change

The three remaining misses had different immediate shapes but shared a narrow
handoff defect: relevant evidence was visible just outside the final source
candidate set.

- `ci-1222`: the bundle already contained a documentation example calling
  `RegisterFlagCompletionFunc`, but not the called definition. `task_context/2`
  now resolves at most one exact, query-relevant called definition for explicit
  function/method/definition questions.
- `ci-678`: a selected test declaration called `SetArgs` outside the earlier
  point window. For an explicit "How ... test/testing" procedure question, the
  same bounded resolver examines the selected declaration and lets its single
  best exact called definition compete for source budget.
- `ci-511`: a deterministic lifecycle-tail semantic view moved `OnInitialize`
  from outside the final retrieval set to rank 16. `task_context/2` widens its
  internal pool from 15 to 16 only for the same explicit
  initialization-function/hook/callback query shape. The primary band remains
  five and the snippet budget remains 1,200.

Approximate symbol names are filtered after the selective symbol search; the
definition bridge is bounded to 15 source candidates, 12 names, one admitted
definition, and eight lexical matches per name. The lifecycle view performs one
additional embedding only when both lifecycle and callable terms are present.
Its retrieval identity is `retrieval/7` and its width is included in the
weights hash.

Two broader experiments were rejected. Raising graph snippet neighbors from 3
to 12 left all three misses and reduced complete queries from 34 to 32. Applying
the semantic tail view to every long natural-language question dropped
architecture-flow nDCG@10 from 0.462467 to 0.381380. Neither rejected change is
present.

## Development measurements

All bundle figures use the 40 dev questions with at least one grade-3 span.

| Measure | Before | After |
|---|---:|---:|
| Any emitted source overlaps grade 3 | 37/40 | 40/40 |
| At least one complete grade-3 span | 34/40 | 37/40 |
| Complete grade-3 spans | 40/63 | 43/63 |
| Every grade-3 span complete | 21/40 | 22/40 |
| Mean snippet whitespace tokens | 1112.100 | 1112.175 |
| Mean complete MCP cl100k tokens | 8026.975 | 8027.050 |

Only `ci-511`, `ci-678`, and `ci-1222` change from zero to one complete
grade-3 span. No development query loses overlap or a complete span.

| Existing ranking measure | After | Frozen requirement |
|---|---:|---:|
| Architecture-flow nDCG@10 | 0.4624671523 | 0.4578575263 — PASS |
| NL-behaviour nDCG@10 | 0.7029047224 | 0.5449702531 — PASS |
| Exact-identifier Top-1 | 1.0 | 1.0 — PASS |
| Overall nDCG@10 (41 scored) | 0.5645491768 | — |

Two independent Cobra indexes produced identical MCP response bytes, SHA-256
digests and cl100k counts for all 44 dev records (`44/44`), while their internal
freshness generations differed. Aggregate reproduction checked 761 metrics
with zero discrepancies.

## Development payload-cost attribution

`payload-cost-attribution.json` decomposes the exact preserved MCP responses;
it neither changes production serialization nor enters a release gate. Because
BPE tokenization is not additive across independently tokenized fragments, the
token columns are exact ordered marginals over complete valid responses in the
recorded order: empty MCP text, JSON structure, metadata, summary, then source
snippets. The five marginals reconcile exactly to every complete response.

Across all 44 dev responses, provenance/metadata plus JSON structure consume
211,511 of 349,485 cl100k tokens (60.5%). Source snippets consume 129,065
(36.9%), the summary 7,677 (2.2%), and the outer envelope 1,232 (0.35%). The
source strings contain 93,216 tokens in isolation but cost 129,065 as the final
wire marginal; double JSON quoting, escaping, and BPE boundary interaction are
therefore material. The safe optimization order suggested by this diagnostic
is: remove or compact repeated non-source item/evidence metadata first, test a
single-serialization structured result in a new MCP contract version second,
and leave source depth intact until equal-recall proves it can shrink. Summary
or envelope trimming cannot materially solve the payload-size problem.

## Reproduce

Run from the repository root with the pinned model installed and a clean Cobra
checkout at `a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra

go run ./cmd/retrieval-eval -repo cobra \
  -checkout "$GRAPHI_RECOVERY_COBRA" \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out /tmp/answer-recovery-report.json \
  -export-raw /tmp/answer-recovery-run

GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_OUT=/tmp/answer-recovery-bundles.json \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

go run ./cmd/retrieval-eval -aggregate \
  docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/after

go run ./cmd/retrieval-eval -setup-tokenizer

CGO_ENABLED=0 go run ./cmd/payload-cost-dev \
  -input docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json \
  -out /tmp/payload-cost-attribution.json

cmp /tmp/payload-cost-attribution.json \
  docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/payload-cost-attribution.json
```

`-check-targets` reports PASS for architecture-flow, NL-behaviour,
exact-identifier, and the existing six-query bundle gate. It still exits 1 only
for the immutable historical blind smoke (`RELEASE: NO`); this dev run does not
claim to replace or retroactively change that held-out result.
