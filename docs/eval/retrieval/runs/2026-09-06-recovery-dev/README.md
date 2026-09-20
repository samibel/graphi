# Development recovery: useful fixes, release still blocked

The exact-identifier regression and the MCP fingerprint instability are fixed.
Declaration-aware source selection substantially improves retention of complete
judged spans. This is **not a complete recovery**: architecture-flow ranking
still misses its frozen target, and eight of the 40 grade-3-answerable development
queries still receive no source overlapping a grade-3 answer. No token-savings
or bundle-sufficiency claim is authorized by this run.

The measurement contract, dataset, thresholds, tokenizer, 1,200-token argument,
and scoring implementation are unchanged. No held-out query was retrieved,
captured, rated, or used to choose a change.

## Population and execution identity

- Corpus: Cobra at `a0a6ae020bb3899ff0276067863e50523f897370`, clean local checkout.
- Input: the existing development-only
  `../2026-09-06-sw282-gate-local/dataset.json`, SHA-256
  `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
- This slice contains 44 English development records: 40 with grade-3 answers,
  `cb-31` with only lower-grade judgements, and three `no_hit` records. The
  existing ranking harness scores 41/44; the additional exact-grade-3 source
  diagnostics report 40/44. All 44 records are preserved on both sides.
- Embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
- Before: production code at `5d0ef8729be141e901e996d8660ec5bfe6dd182d`,
  `retrieval/2`. A fresh execution reproduces the previously committed scores.
- After: the working-tree changes accompanying this directory, `retrieval/3`.
  The report explicitly records `5d0ef8729be141e901e996d8660ec5bfe6dd182d+dirty`.
  These are development diagnostics, **not bound release captures**. The
  release capture's clean-tree/commit-binding checks have not been relaxed.

## Existing ranking harness: before / after

| Metric | Before | After | Frozen requirement |
|---|---:|---:|---:|
| Exact-identifier Top-1 | 3/4 | 4/4 | 4/4 |
| Architecture-flow nDCG@10, 5 queries | 0.32777888533499866 | 0.32777888533499866 | 0.4578575262772977 |
| NL-behaviour nDCG@10, 6 queries | 0.6367740107708452 | 0.6367740107708452 | 0.544970253069991 |
| Overall Top-1, 41 scored queries | 21/41 | 22/41 | — |
| Overall nDCG@10 | 0.5096786853013381 | 0.5271350338145441 | — |

Use the JSON reports for full-precision values. All six non-candidate baseline
rankings are unchanged. The lexical-only fallback and exact-path behavior are
unchanged. Natural-language ranking is unchanged by the accepted fix.

Each of `before/`, `after/`, and `rejected-source-prior/` contains the full
default baseline set, raw hits, latency observations, dataset copy, and run
index. The existing `-aggregate` command reproduces **761/761 metrics**, with
zero discrepancies and zero unknowns, in each directory. Reproduction success
means the reported observations match their raw data; it does not mean a
quality target was reached.

## Where the answers were lost

1. **Exact names were allowed to lose to approximate semantic matches.** The
   shipped ready path made the quantized semantic list an immutable prefix.
   `GenMarkdownTree` returned `GenMarkdown` first, despite having the exact
   declaration available. `ExecuteC` appeared at rank 9; the old Top-1 metric
   still credited its grade-2 wrapper at rank 1. The fix promotes actual
   qualified-name or qualified-suffix equality before truncation. It does not
   restore the entire hybrid lexical ranking, whose degree and partial-name
   scores can also put approximate names first. Unknown identifiers retain
   semantic-first behavior. Promoted rows carry `region=exact_identifier`.

2. **Hydration destroyed retrieval order.** `NodesByID` returns canonical-ID
   order by contract. `resolveSeedsV2` iterated that result and assigned new
   seed ranks, despite its comment promising retrieval order. The fix indexes
   hydrated nodes and iterates the retrieval rows, preserving each row's span.
   A regression deliberately reverses canonical IDs and applies an item cap
   of one, proving the highest-ranked seed survives.

3. **Declarations were reduced to a point plus six lines on either side.**
   `StartLine` and `EndLine` were both `n.Line()`. The 1,200-token argument
   could not make that 13-line window reach the rest of a long function.
   The new source assembler reuses parser-provided declaration boundaries.
   It retains complete declarations when they fit, reserves space for later
   candidates, and selects a contiguous query-focused window within larger
   declarations. Window scoring credits distinct query terms, with stable
   earlier-window tie breaking. Unsupported or unparseable sources use the
   existing point-window fallback. Parser input is bounded; window selection
   uses a sliding window rather than repeated full scans. Every emitted
   snippet is verified against its citation in the clean pinned checkout.

4. **Capped items left unused citations in the payload.** The shared item cap
   truncates `Items`, while retaining the complete `Evidence` array. `/2` now
   discards non-snippet citations referenced only by discarded items. It keeps
   all emitted source snippets, which have their own budget, and all references
   needed by retained items. Neither the 40-item default nor the five-seed
   admission limit was raised.

5. **An operational nonce was charged as answer content.** The index fingerprint
   includes `index.commit_generation`, minted randomly on graph mutations.
   It remains in retrieval/status diagnostics and in the freshness checks that
   prevent stale vectors from being served. `/2` no longer interpolates it into
   the actor-visible summary. Model identity, method/version, weights, source
   provenance, and readiness remain visible. This change does not pretend the
   operational fingerprint is a content digest.

Focused regression commands (each bug-specific test was observed failing
before its corresponding fix):

```sh
CGO_ENABLED=0 go test ./engine/retrieval -run TestExactNamePrecedes -count=1
CGO_ENABLED=0 go test ./engine/agenttools/taskctx -run 'TestTaskContextV2Preserves|TestTaskContextV2IncludesBodyBeyond' -count=1
CGO_ENABLED=0 go test ./engine/context -run 'TestDefinitionWindow|TestAssembleDefinitions' -count=1
```

## Actual MCP bytes and source retention

`bundles-before.json` and `bundles-after.json` preserve two independent index
builds each. The capture calls the existing `captureOneCandidateBundle` helper:
the bytes come from `surfaces/mcp.Server.Serve`, including the JSON-RPC envelope,
escaping and final newline. No result re-marshalling supplies a token count.
Both real-tokenizer and whitespace counts are recomputed through the existing
payload ledger. The engine receives no judgements.

The following are **additional diagnostics**, not replacements for the frozen
ranking scorer, equal-recall savings estimator, or blind sufficiency gate:

| Diagnostic | Before | After |
|---|---:|---:|
| Queries with any emitted source overlapping grade 3 | 32/40 | 32/40 |
| Queries containing at least one complete grade-3 span | 8/40 | 29/40 |
| Queries containing every grade-3 span | 3/40 | 17/40 |
| Complete grade-3 spans retained | 8/63 | 32/63 |
| Byte-identical MCP responses across independent builds | 0/44 | 44/44 |
| Mean emitted snippet whitespace tokens, first build | 479.1 | 1038.0 |
| Mean complete-response cl100k tokens, first build | 6591.3 | 7085.6 |

Overlap uses the existing `SpanMatches` at exact grade 3, applied to lines
actually emitted as source. Containment additionally requires every line of
the judged interval to be present in emitted snippets; multiple snippets may
cover an interval together. Neither diagnostic is a sufficiency rating.
Before captures total 263,650 real tokens over these 40 responses; after
captures total 283,424. Old-build token counts vary with the nonce, so the
preserved bytes are the reproducible authority for that historical observation.

The frozen 1,200 argument bounds **snippet whitespace tokens** in the existing
engine. The measurement contract separately charges the entire response with
cl100k. The accepted changes retain substantially more source at an increased
mean wire cost of 494.4 real tokens. There is no demonstrated token saving here,
and misses prevent a valid equal-recall savings aggregate. This experiment
does not establish that 1,200 is too small; no budget frontier was claimed or
used to change the budget.

## Remaining structural retrieval failure

The after diagnostic also records the actual first 50 retrieval rows. The
grade-3 definition ranks below are measured without passing qrels to retrieval:

| Development query | Grade-3 declaration ranks | Bundle admits |
|---|---|---|
| `cb-19`, execution lifecycle | `ExecuteC`: 10; `execute`: absent from first 50 | first 5 seeds, then bounded graph neighbors |
| `cb-20`, persistent flag propagation | `mergePersistentFlags`: 7; `updateParentsPflags`: 8 | first 5 seeds |
| `cb-21`, adding commands and setting parent | `AddCommand`: 19 | first 5 seeds |
| `cb-22`, documentation tree traversal | `GenMarkdownTreeCustom`: 41 | first 5 seeds |
| `cb-24`, completion flag-group enforcement | `enforceFlagGroupsForCompletion`: 1 | first 5 seeds |

This rules out span placement alone as a complete fix. In `cb-20`, the old
13-line window around `Parent` happened to overlap the nearby merge helper;
the complete `Parent` declaration correctly stops before that helper. An
overlap score can therefore fall while source selection becomes more faithful.
The actual propagation implementation needs to become a candidate admitted to
the bundle. Increasing source depth cannot admit a definition ranked 19 or 41.

The eight after misses are `cb-20`, `cb-21`, `cb-22`, `ci-467`, `ci-511`,
`ci-678`, `ci-771`, and `ci-1222`.

A tested alternative stably placed non-test function/method candidates before
other candidates in the first semantic candidate pool, keeping relative order
inside each group. This improved architecture nDCG from 0.3278 to **0.4107**, still
below 0.4579, while config/docs nDCG fell from **0.4114 to 0.3155**. Overall nDCG
fell to approximately 0.497. The experiment is preserved in
`rejected-source-prior/` and was removed from production. It shows why simply
preferring implementation files is insufficient.

Further recovery requires a change to candidate admission/ranking: for example,
retrieving within long function bodies or following relevant wrapper-to-callee
relations before selecting the five sources. This is a structural limitation
of the current immutable semantic prefix, not a proof that the product goal
or the 1,200 budget is impossible. This change deliberately leaves the
unresolved architecture target red.

## Reproduce

Run from the graphi repository root. The pinned model and tokenizer must already
be installed; measurement never downloads. Set the two local artifact paths
below for your machine. `GOCACHE` may point to a writable temporary directory
when running in a restricted workspace.

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/clean/pinned/cobra

go run ./cmd/retrieval-eval -repo cobra \
  -checkout "$GRAPHI_RECOVERY_COBRA" \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out /tmp/recovery-report.json -export-raw /tmp/recovery-ranking

GRAPHI_RECOVERY_OUT=/tmp/recovery-bundles.json \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

# Offline re-count of the committed MCP payloads and source diagnostics:
go test ./internal/eval/retrieval -run '^TestRecoveryDevArtifactPayloads$' -count=1 -v

# Recompute all ranking statistics from committed raw hits:
for run in before after rejected-source-prior; do
  go run ./cmd/retrieval-eval -aggregate "docs/eval/retrieval/runs/2026-09-06-recovery-dev/$run"
done

# Expected exit 1: architecture_flow and historical qrel_blind_smoke still miss.
go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-06-recovery-dev/after/cobra-v2-dev-report.json
```

For the before ranking execution, run the same evaluation command in a checkout
of `5d0ef8729be141e901e996d8660ec5bfe6dd182d`. To repeat the before MCP diagnostic,
copy only `internal/eval/retrieval/recovery_dev_test.go` into that checkout and
run `TestRecoveryDevCapture` with `GRAPHI_RECOVERY_REQUIRE_IDENTICAL=0`. Its
production code remains the before implementation; independent before payloads
will differ because this is the defect being measured.

The target check reports **2/5 misses**, down from 3/5. Its `bundle_coverage`
line still refers to the committed SW-282 six-query artifact, and its smoke
line still refers to the historical held-out evaluation. Neither line is a
fresh evaluation of this candidate. The new captures supply development
diagnostics only. The frozen target file and historical expectations remain
unchanged; no release pass is claimed.

## Validation

`CGO_ENABLED=0 go build ./...` passed. The full `CGO_ENABLED=0 go test ./...`
suite passed: 136 test-bearing packages, zero failures. Local HTTP fixture
tests required socket access outside the restricted sandbox; the first
restricted run's bind failures were not counted as passes. Formatting and
the model/tokenizer run inventories were corrected before the successful run.

`go run ./cmd/layerguard` passed. The offline payload verification passed for
both before and after artifacts, and the fresh after capture required all
44 responses, digests, and tokenizer counts to match across two independent
indexes with distinct freshness generations. No model or tokenizer pin changed.
