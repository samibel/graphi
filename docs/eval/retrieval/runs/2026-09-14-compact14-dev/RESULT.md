# File paths, struct fields and separated tokens (compact/14) — development result

Status: **development result under the gate policy approved on 2026-09-14;
not a release result.** No holdout was opened, rerun, or consulted. The
two-call numbers are measured under the **draft** second-response contract
(`contract-v2-second-response.md`), which is not adopted; they authorise
nothing. This run supersedes `2026-09-14-compact13-followup-dev` as the
current candidate.

The capture is bound to candidate `b149af455b2abcef80c640b87788726b261cca13`, the pinned Cobra
checkout `a0a6ae020bb3899ff0276067863e50523f897370`, and development
dataset `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
Capture SHA-256 `28f6048d73910ccffe484769f2dc7fc11156acaeb4c38c6370a49ad5418dbe33`; two independent index builds, distinct
freshness generations, 44/44 identical MCP bytes, digests and cl100k
counts. The reviewed holdout-shaped split (SHA-256
`33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`) is
measured through the production MCP path by `TestDraftDevForecast`.

## What changed since compact/13

This slice worked the ten reviewed-split questions that were not
overlapped even with the designated read. Diagnosis by what the projector
was given: three were paths of files the discovery snapshot never scans
(`go.mod`, `LICENSE.txt`, `Makefile`), two were bare terms whose answer is
a struct field (`Aliases`, `post-run` → `PostRun`), one was a candidate at
retrieval rank 11 (past the six seeds), four were absent from the
retrieval window or below the 15-candidate cap. Three general rules
address the first two groups; each is decided by query text and
repository bytes only and is pinned by tests:

1. **Exact file.** A one-token question that is the path of a regular,
   text, non-`.go` repository file outside excluded directories is
   answered by that file: its first 120 lines as one region ranked ahead
   of every retrieval row (`exact_file.go`, `exact_file_test.go`).
2. **Field declarations and separated tokens.** In exact-identifier
   discovery a `name type` line (struct field, parameter) declares the
   name — before, only `func`/`type`/`var`/`const` did — and a token
   written in prose (`post-run`, `pre_run`) is searched as the identifier
   its parts spell, whole and case-insensitively (`grepread.go`,
   `identifier_discovery_test.go`).
3. **Bare terms.** A documentation heading that spells the term does not
   "name" it (only source declares an identifier), so the term takes the
   retrieval-ordered path; there, the line that declares the term earns a
   decisive anchor bonus. A mention alone earns nothing: a first version
   that rewarded whole-word mentions moved `directive` onto a comment that
   used the word and lost the type it names (`bare_term_test.go`).

Wire identity: `task_context/2-compact/14`.

## Before / after

| Reviewed split, 64 questions, production path | compact/13 | **compact/14** |
|---|---:|---:|
| One response: spans overlapped / complete | 51 / 39 | **56 / 43** |
| + designated read: spans overlapped / complete | 54 / 45 | **59 / 49** |
| exact_path overlapped / complete (two-call) | 8 / 2 | **11 / 5** |
| ambiguous overlapped / complete (two-call) | 8 / 8 | **10 / 9** |
| Follow-up reads / median / max tokens | 12 / 770 / 1,062 | 12 / 770 / 1,062 |
| Median / max response tokens | 959 / 1,187 | 959 / 1,187 |
| `P(≥ 56 of 64)` at the one-response overlap rate | ≈ 0.08 | ≈ 0.59 |
| `P(≥ 56 of 64)` at the two-call overlap rate | ≈ 0.31 | **≈ 0.94** |

| Committed split, 40 questions, bound capture | compact/9 (start) | compact/13 | **compact/14** | Policy |
|---|---:|---:|---:|---|
| Responses ≤ 1,200 tokens | 40 | 40 | **40** | held |
| Cheaper than equal-recall GrepRead/2 | 24 | 27 | **27** | held (cost) |
| Paired median token saving | 76.0 (6.69 %) | 78.0 (6.99 %) | **78.0 (6.99 %)** | held (cost) |
| Required grade-3 overlap reached | 40 | 35 | 35 | observed |
| At least one complete grade-3 span | 33 | 24 | 24 | observed |

Two-call observation on the committed split (draft contract, earliest
prefix): reached 35/40, 7 reads designated, 26/40
cheaper, paired median saving 63.5.

Ranking gates unchanged (the projection does not touch retrieval).

## What remains

Five reviewed-split questions are still not overlapped after the read.
One is a candidate at retrieval rank 11: the retrieval-ordered selector
takes six seeds, and raising that number was measured in slice A as a
cost regression. Four never reach the projector: two are absent from the
50-row retrieval window and two sit at ranks 17–46, below the
15-candidate cap. Nothing in the projection can reach those; they are
retrieval ranking and pool width, gated by the registered nDCG and
bundle-coverage targets, and belong to a separate slice with their own
capture. The curator's pre-registration draft asked to hold until the
two-call overlap on the reviewed split reached 56 of 64; it is 59. What
still blocks a holdout is not the candidate but the contract: the four
adoption items in that draft (version constants, slice-2 binding, a
two-slice release aggregate, `methodology.md`) are unbuilt by design, and
under the adopted contract 1 — one response — the same split stands at
56 of 64 overlapped, `P ≈ 0.59`, a coin toss.

## Rejected on the same instruments: candidate pool 15 → 20

Measured 2026-09-15 by codex-cli 0.153.4 from a bounded brief (constant
`candidatePoolLimit` only; projector and ranking untouched; nothing
committed). Widening the pool was the one upstream lever the projection
cannot supply, aimed at the reviewed-split question at retrieval rank 17.

| Reviewed split, production path | pool 15 (this result) | pool 20 |
|---|---:|---:|
| One response: overlapped / complete | 56 / 43 | 55 / 42 |
| + designated read: overlapped / complete | 59 / 49 | **58 / 47** |
| Follow-up reads | 12 | 10 |

| Committed split, quick rebuild | pool 15 | pool 20 |
|---|---:|---:|
| Cheaper than GrepRead/2 / paired median saving | 26 / +74.0 | 28 / +76.5 |
| Reached / any complete | 35 / 24 | 35 / 24 |

The wider pool reached none of the targeted questions and displaced the
leads of two others (`cd-23`, `cd-78`: complete → not overlapped), because
the retrieval-ordered selector still takes six seeds and five more rows
shift which six those are. The small cost gain on the committed split does
not buy back two reviewed-split answers. Rejected; the constant stays 15.
Ranking gates on a fresh report over the committed development split:
architecture-flow nDCG@10 `0.46246715228468249`, NL-behaviour nDCG@10
`0.70290472235070689`, exact-identifier Top-1 `1.0`, bundle coverage 6/6 —
unchanged from every RESULT since compact/10 (the 2026-09-06 report in the
gate-local run directory predates the ranking work and is not the
comparison point).

## Independent evidence after this result

A third fresh sealed holdout, curated independently and evaluated under
contract version 2 with the designated read
(`runs/2026-09-15-product-compact-v14-third-fresh-sealed-holdout/`),
returned **38 of 64 against k = 56 — RELEASE: NO**. Per stratum:
exact_identifier 11/11, nl_behaviour 11/11, config_docs 7/10, exact_path
6/11, architecture_flow 2/11, ambiguous 1/10. The reviewed development
split's two-call overlap (59/64) predicted a pass; the blind
transcript-sufficiency grade did not follow it. Overlap with a reviewed
span is not the quantity the rubric grades — a rater must answer the
question from the transcript and a grader must find the reviewed
behaviour identified — and on bare terms and multi-step flows the two
diverge most. The next development instrument must grade the way the
holdout grades, before another holdout is spent.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02
GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
  go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v
GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=$PWD/docs/eval/retrieval/runs/2026-09-14-compact14-dev/bundles.json \
  go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v
GRAPHI_RECOVERY_OUT=$PWD/docs/eval/retrieval/runs/2026-09-14-compact14-dev/bundles.json \
GRAPHI_RECOVERY_COBRA=$GRAPHI_PRODUCT_COMPACT_DEV_COBRA \
GRAPHI_RECOVERY_CANDIDATE_SHA=b149af455b2abcef80c640b87788726b261cca13 \
  go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v
```
