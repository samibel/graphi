# Development result: fair candidate admission and non-displacing context

This development-only run fixes two measured bundle failures without changing
the dataset, scorer, thresholds, 40-item cap, or 1,200-token snippet budget.
The architecture, natural-language and exact-identifier gates remain green.
It is not a release result: the sealed holdout was neither opened nor run, and
the historical blind-smoke result remains red.

## Diagnosis

`search_hybrid/1` admitted the full query and then token channels sequentially
into a 60-row cap. Four early terms could exhaust the cap before a late subject
term contributed any candidate. On `ci-771`, the six-token tokenizer also spent
two positions on conversational words (`would`, `but`) and dropped `shell`.
The grade-3 `ShellCompRequestCmd` source therefore ranked 21 and sat outside the
15-candidate task-context source pool.

After admission, a second defect narrowed a grouped Go constant to the parser's
single value-spec span. The bundle emitted `completions.go:27-29`, while the
answer-bearing protocol declaration is `completions.go:26-33`. Blindly keeping
context around every constant recovered that span but displaced a complete
function for `cb-14`; that rejected experiment moved completeness from one
query to another and left the total at 33/40. The accepted rule selects core
definitions first and spends only otherwise-unused budget on grouped
const/variable context, so optional context cannot displace selected source.

`search_hybrid/1` is a byte-frozen public surface. The fair admission algorithm
therefore has a separate `search_hybrid-candidates/2` identity and is selected
only for a ready semantic retrieval generation. Explicit lexical-only and
degraded paths still call the unchanged `/1` implementation and pass its golden
byte tests. The actor-visible methods are `retrieval/6`,
`context-definitions/3`, and `task_context/2`.

## Development measurements

All bundle figures below use the 40 dev questions with at least one grade-3
span. The complete MCP count includes the exact serialized JSON-RPC response;
it is not the 1,200-token snippet budget.

| Measure | Before | After |
|---|---:|---:|
| Any emitted source overlaps grade 3 | 36/40 | 37/40 |
| At least one complete grade-3 span | 33/40 | 34/40 |
| Complete grade-3 spans | 39/63 | 40/63 |
| Every grade-3 span complete | 21/40 | 21/40 |
| Mean snippet whitespace tokens | 1107.600 | 1112.100 |
| Mean complete MCP cl100k tokens | 8018.550 | 8026.975 |

The admission-only capture is preserved as `bundles-admission-only.json`:
it moves overlap from 36/40 to 37/40 and raises `ShellCompRequestCmd` from
candidate rank 21 to 15. The final context rule makes that span complete and
moves complete-query coverage from 33/40 to 34/40. Three dev questions still
have no grade-3 source overlap: `ci-511`, `ci-678`, and `ci-1222`. This is a
positive incremental result, not evidence that the product sufficiency claim
has been earned.

| Existing ranking measure | After | Frozen requirement |
|---|---:|---:|
| Architecture-flow nDCG@10 | 0.4624671523 | 0.4578575263 — PASS |
| NL-behaviour nDCG@10 | 0.7029047224 | 0.5449702531 — PASS |
| Exact-identifier Top-1 | 1.0 | 1.0 — PASS |
| Overall nDCG@10 (41 scored) | 0.5645491768 | — |

Two independent Cobra indexes produced identical MCP response bytes, SHA-256
digests, and cl100k counts for all 44 dev records (`44/44`). Their internal
freshness generations were distinct, proving the fingerprint no longer leaks
into the serialized payload.

A broader CamelCase-segment FTS experiment was rejected: it recovered no
additional answer source, left architecture unchanged, and reduced overall
nDCG@10 from 0.5645491768 to 0.5552680145. It is not present in the code.

Machine-readable values and artifact hashes are in `measurement.json`. Ranking
raw samples, latency samples, report and aggregate reproduction are under
`after/`; aggregation reproduced 761/761 metrics with zero discrepancies.

## Reproduce

Run from the repository root with the already-installed pinned model and a
clean pinned Cobra checkout:

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370

go run ./cmd/retrieval-eval -repo cobra \
  -checkout "$GRAPHI_RECOVERY_COBRA" \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out /tmp/candidate-admission-report.json \
  -export-raw /tmp/candidate-admission-run

go run ./cmd/retrieval-eval -check-targets /tmp/candidate-admission-report.json

GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_OUT=/tmp/candidate-admission-bundles.json \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

go run ./cmd/retrieval-eval -aggregate \
  docs/eval/retrieval/runs/2026-09-06-candidate-admission-dev/after
```

The target check exits 1 only because it also checks the immutable historical
held-out blind smoke (`RELEASE: NO`). It prints PASS for architecture-flow,
NL-behaviour, exact identifier, and the pre-existing six-query bundle gate.
