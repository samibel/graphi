# Follow-up designation (compact/13) — development result, second-response contract

Status: **development result under the gate policy approved on 2026-09-14;
not a release result.** No holdout was opened, rerun, or consulted. The
two-call numbers below are measured under the **draft** contract
`contract-v2-second-response.md`, which is not adopted; they authorise
nothing. This run supersedes `2026-09-14-compact12-dev` as the current
candidate.

The capture is bound to candidate `d83894fdb7899de225b86392297aa5d7eb6ce3a0`, the pinned Cobra
checkout `a0a6ae020bb3899ff0276067863e50523f897370`, and development
dataset `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
Capture SHA-256 `57c60b369463a666dbd8b1a93a49e19cc237bc2b55cf8fb27a7535aba0a769f9`; two independent index builds, distinct
freshness generations, 44/44 identical MCP bytes, digests and cl100k
counts. The reviewed holdout-shaped split (SHA-256
`33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`) is
measured through the production MCP path by `TestDraftDevForecast`.

## What changed since compact/12

The response designates one follow-up read. When its first emitted source
is a cut window into a larger unit — a Go declaration from doc comment to
closing brace, or a Markdown section — the structured content carries
`"followup": "path:start-end"` naming that unit from its start, capped at
120 lines. It is absent when the lead is whole. Only repository bytes and
the emitted lead decide it; `followup_test.go` pins lead-only, whole unit,
cap, absence, supplemental failure and the citation wire form. Selection
and hydration are unchanged; the single-response span counts are therefore
identical to compact/12. Wire identity: `task_context/2-compact/13`.

The object form of the hint (`{"path":…,"start_line":…,"end_line":…}`)
was measured first on a quick rebuild of the old split and cost 12 tokens
of paired median saving; the citation form cost 7.5 on the same rebuild
and 3.5 on the bound capture below. The citation form is what ships.

## Before / after

| Reviewed split, 64 questions, production path | compact/12 | compact/13, one response | **compact/13 + designated read** |
|---|---:|---:|---:|
| Span overlapped | 51 | 51 | **54** |
| Span complete in one source | 39 | 39 | **45** |
| config_docs overlapped / complete | 8 / 6 | 8 / 6 | **10 / 9** |
| architecture_flow overlapped / complete | 7 / 4 | 7 / 4 | **8 / 6** |
| nl_behaviour overlapped / complete | 9 / 8 | 9 / 8 | **9 / 9** |
| Follow-up reads issued | — | — | 12 of 64 |
| Median / max follow-up read tokens | — | — | 770 / 1,062 |
| Median / max response tokens | 957 / 1,187 | 959 / 1,187 | unchanged (response 1) |

Twelve responses carried a designation; six of them completed a span the
single response had cut (cd-24, cd-49, cd-52, cd-54, cd-83, cd-84), three
overlapped a span they had missed entirely. Two designations pointed at the
wrong unit (cd-63, cd-93: a documentation lead for a question whose answer
is in `command.go`) and bought nothing. Under the rubric's reading the
two-call overlap rate, 84.4 %, gives `P(≥ 56 of 64)` ≈ 0.31; the single
response stays at ≈ 0.08.

| Committed split, 40 questions, bound capture | compact/9 (start) | compact/12 | **compact/13** | Policy |
|---|---:|---:|---:|---|
| Responses ≤ 1,200 tokens | 40 | 40 | **40** | held |
| Cheaper than equal-recall GrepRead/2 | 24 | 27 | **27** | held (cost) |
| Paired median token saving | 76.0 (6.69 %) | 81.5 (7.27 %) | **78.0 (6.99 %)** | held vs. start; −3.5 vs. compact/12 |
| Median / max response tokens | 1,019.5 / 1,174 | — | 1,013 / 1,165 | held |
| Required grade-3 overlap reached | 40 | 35 | 35 | observed |
| At least one complete grade-3 span | 33 | 24 | 24 | observed |

**Cost, stated plainly.** The designation is present on 7 of the 40
committed-split responses and costs them 10–14 tokens each. On the bound
capture that is 0 cheaper responses and −3.5 tokens of paired median
saving against compact/12, and +3 responses / +2.0 tokens against the
slice start. The cost measures are held against the slice start, which is
what the approved policy names; the small loss against compact/12 is the
price of the hint and is recorded rather than argued away. Under the
current contract (one response) the hint buys nothing; under the draft
contract it is the whole mechanism.

Two-call observation on the committed split, charged by the draft's
earliest-prefix rule: reached 35/40 (unchanged — none of the five misses
is a cut lead), 7 reads designated, 26/40 cheaper than GrepRead/2,
paired median saving 63.5 tokens.

Ranking gates unchanged (the projection does not touch retrieval).

## What this does and does not establish

- The product half of option C exists, is deterministic, is pinned by
  tests, and reaches on the reviewed split exactly what the simulation
  predicted (54 / 45).
- The measurement half — two-slice transcript validation, the equal-recall
  scorer over both slices, the rubric packet — is **not built**. The draft
  contract's enforcement table says which rules are unenforced. No holdout
  may be pre-registered under contract 2 until they exist and are reviewed.
- `P ≈ 0.31` at `k = 56` is not a bar to spend a holdout on. The ten
  questions still not overlapped after the read are five with no covering
  evidence in the bundle (three non-Go `exact_path` files retrieval does
  not index), three below the 15-candidate cap, and two selection losses
  inside very long files; none of them is a projection problem.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02
# reviewed split, production path, one response and designated read
GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
  go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v
# committed split, cost, single-response and two-call lines, from the bound capture
GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=$PWD/docs/eval/retrieval/runs/2026-09-14-compact13-followup-dev/bundles.json \
  go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v
# bound two-index capture
GRAPHI_RECOVERY_OUT=$PWD/docs/eval/retrieval/runs/2026-09-14-compact13-followup-dev/bundles.json \
GRAPHI_RECOVERY_COBRA=$GRAPHI_PRODUCT_COMPACT_DEV_COBRA \
GRAPHI_RECOVERY_CANDIDATE_SHA=d83894fdb7899de225b86392297aa5d7eb6ce3a0 \
  go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v
```
