# Retrieval-ordered projection (compact/10) — development result

Status: **development result under the gate policy approved on 2026-09-14;
not a release result.** No holdout was opened, rerun, or consulted.

The capture is bound to candidate
`85706895694d2f991acc041ef5188e47da2a158a`, the pinned Cobra checkout
`a0a6ae020bb3899ff0276067863e50523f897370`, and development dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c` (the
committed development split, 44 rows). Capture SHA-256
`d96ae6c8fa26d076faa0e04d55aa14ccfe8d5654b49dbdd839cd9f9323ce0561`; two
independent index builds, 768 input documents each, distinct freshness
generations, 44/44 identical MCP bytes, digests and cl100k counts. The
reviewed holdout-shaped split (`drafts/2026-09-14-holdout-shaped-dev/`,
SHA-256 `33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`)
is measured through the same production MCP path by
`TestDraftDevForecast`; it is agent-reviewed development data, not
evidence.

## Gate policy this result is read under

Approved on 2026-09-14 after the sealed holdouts were shown to have one
small span per question and the committed development split was shown to
have a different shape (`runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`):

- **Primary selection measure:** the independently reviewed holdout-shaped
  split — spans overlapped and spans complete, 64 questions.
- **Held:** the four registered ranking gates (unchanged by construction —
  the change is inside the compact projection), the frozen 1,200-token
  ceiling, and the old split's cost measures (cheaper-than-GrepRead/2 count,
  paired median saving).
- **Observed, not gated:** the old split's overlap and completeness counts.

## What changed

Natural-language questions are projected by a new selector
(`compact/v9/select_nl.go`) that keeps retrieval's order for the primary
candidates instead of re-scoring every candidate by name matches, a dozen
query-shape literals and fixed bonuses. Depth follows rank: a declaration
the question spells as declared leads; what the lead's own text calls is
finished whole first; then every small seed; then the lead — reclaiming
trailing one-line citations when that is all that keeps it from finishing —
then bounded windows for the rest. Growth inside a long declaration moves
towards the lines that carry the question's words and never leaves the
declaration. Tests without test intent, duplicate windows, and raw
discovery hits already hydrated to a declaration no longer take slots.
Exact-identifier, exact-path and ambiguous queries keep the previous path.
No query identifier, judgement, target span or repository-specific path is
read. Wire identity: `task_context/2-compact/10`.

## Before / after

Before is the previous candidate `40133db5` (its old-split row is the
committed `2026-09-14-named-declaration-dev` capture; its reviewed-split row
was measured with `TestDraftDevForecast` at that commit).

### Reviewed holdout-shaped split (primary), 64 questions, production path

| Measure | Before | After |
|---|---:|---:|
| Span overlapped | 45 | **46** |
| Span cited | 34 | **41** |
| Span complete in one source | 28 | **33** |
| Mean span share delivered | 56.6 % | **62.7 %** |
| Median / max cl100k tokens | 1,036 / 1,190 | **949** / 1,187 |
| nl_behaviour overlapped / complete | 7 / 3 | **9 / 8** |
| architecture_flow overlapped / complete | 7 / 3 | 7 / **4** |
| config_docs overlapped / complete | 5 / 3 | 5 / 2 |
| exact_identifier complete | 11 | 11 |
| exact_path overlapped / complete | 8 / 2 | 8 / 2 |
| ambiguous overlapped / complete | 7 / 6 | 6 / 6 |

Misses by first losing stage: 5 absent from the 50-row window, 3 below the
15-candidate cap, 23 retrieved and lost in selection (nine of them
`exact_path` pieces the rubric accepts).

### Committed development split, 40 answerable questions, bound captures

| Measure | Before | After | Policy |
|---|---:|---:|---|
| Responses ≤ 1,200 cl100k tokens | 40 | 40 | held |
| Individually cheaper than equal-recall GrepRead/2 | 24 | **27** | held (cost) |
| Paired median token saving | 76.0 (6.69 %) | **96.0 (8.41 %)** | held (cost) |
| Median / max response tokens | 1,019.5 / 1,174 | **993** / 1,167 | held (cost) |
| Strictly contained duplicate sources | 0 | 0 | held |
| Required grade-3 overlap reached | 40 | **35** | observed |
| At least one complete grade-3 span | 33 | **24** | observed |
| Every grade-3 span complete | 18 | 17 | observed |
| Grade-3 lines delivered | 0.3993 | 0.3131 | observed |

The five lost required overlaps are `cb-21`, `cb-22`, `ci-1222`, `ci-678`,
`ci-771` — every one a question the previous selector reached only through
a literal it carried for that question's shape (`.parent =`, the recursive
walk reservation, `ShellCompRequestCmd`, `.setargs`, the shell-completion
declaration bridge). The nine lost complete spans are concentrated in
`config_docs` (19 → 12), whose keys hold two to four spans each and reward
the breadth the old selector spread its budget across; the new selector
spends that budget on depth for the retrieval's first rows, which is what
one-span questions reward. This is the trade the gate policy was approved
to make, and it is stated here as a regression on the old split, not hidden
in the aggregate.

Ranking gates, re-verified: architecture-flow nDCG@10 `0.46246715228468249`
(required `0.4578575262772977`), NL-behaviour nDCG@10 `0.70290472235070689`
(required `0.54497025306999103`), exact-identifier Top-1 `1.0`, bundle
coverage 6/6. The target checker exits 1 only on the immutable historical
qrel-blind `RELEASE: NO`.

## What was tried and rejected on the way (same instruments)

| Variant | Reviewed (overlapped / complete) | Old-split cost (cheaper; saving) | Decision |
|---|---|---|---|
| first cut: order + depth only | 43 / 30 | 18/40; −13 | too many regions, cost |
| + tests demoted, alternating growth, 10 seeds | 46 / 30 | 16/40; −48.5 | cost |
| + 8 seeds | 45 / 30 | 18/40; −6 | cost |
| 6 seeds, 2 others, 1 discovery region | 45 / 31 | 26/40; +79 | cost held; base of the rest |
| + small units first | 45 / 32 | 26/40; +76 | kept |
| + lead-first ordering (reverted) | 45 / 31 | 26/40; +79 | starved small seeds |
| + named lead, lead reclamation, lead reserve, dependencies first | **46 / 33** | 26/40; +79 (capture: 27/40; +96) | **accepted** |

Every constant above was chosen on the reviewed split and checked against
the old split's cost; none was chosen against a holdout.

## Release status

**No release YES.** The sealed holdouts stand at 32/64 and 42/64, both
`RELEASE: NO` against `k = 56`. This candidate's forecast on the reviewed
split is 46/64 overlapped and 33/64 complete; under the rubric's reading
(pieces count, paraphrase allowed) the overlap rate, 72 %, is the closer
proxy, and `P(≥ 56 of 64)` at that rate is 0.002. The candidate is a
measured step, not a passing one. The next levers, in the order the misses
point: the candidate pool and Markdown hydration windows upstream of the
projector (8 of the 31 misses have no covering evidence in the bundle), and
`config_docs`, where the reviewed split's documentation sections arrive as
heading fragments.

The candidate is frozen and clean at `85706895`; the commit recording this
evidence adds only this directory and the two pin-rotation inventories.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370
export GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA"

GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-14-compact10-dev/bundles.json" \
GRAPHI_RECOVERY_CANDIDATE_SHA=85706895694d2f991acc041ef5188e47da2a158a \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=docs/eval/retrieval/runs/2026-09-14-compact10-dev/bundles.json \
go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v

GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v

go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

go test ./...
```
