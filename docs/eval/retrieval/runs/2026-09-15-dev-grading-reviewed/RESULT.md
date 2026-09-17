# Development grading of the reviewed split with the holdout's rater and grader pipeline

Status: **development instrument and result; not a release result.** It
exists because the third fresh holdout
(`../2026-09-15-product-compact-v14-third-fresh-sealed-holdout/`, 38 of
64, RELEASE: NO) was predicted at 59 of 64 by the development overlap
measure. This run grades the reviewed development split
(`cobra-v3-dev-reviewed`, SHA-256 `33760861d5c7…`) the way the holdout
grades — same capture code and two-slice rater prompt
(`TestDevGradingCapture`), same grader packet builder and version-2
transcript rubric (`TestDevGradingPackets`), same two rater and one grader
configurations, same per-item discipline — so that the author can read
the answers and grades and see where the overlap measure lies.

Candidate: compact/14 at `76752a6e` (the holdout's frozen candidate).
Bundles, prompts, raw responses, grades and per-item event logs are
committed beside this file. The grader packets are not: they embed the
rubric path as given to the packet test, which here was an absolute
path outside the repository, and the repository's pre-commit guard
refuses that string; `TestDevGradingPackets` rebuilds them from the
committed bundles, responses and rubric, differing only in that one line. 128 primary items, 128 grader items, 0
mechanical refusals; no adjudication stage was run (the holdout's decision
rule is applied here as "both primaries pass").

## Calibration: what the pipeline sees versus what the overlap measure said

| Stratum | n | overlapped | complete | both primaries PASS | any PASS | INSUFFICIENT responses | holdout 3 passed |
|---|---:|---:|---:|---:|---:|---:|---:|
| ambiguous | 10 | 10 | 9 | **8** | 10 | 0 | 1/10 |
| architecture_flow | 11 | 8 | 6 | **5** | 6 | 6 | 2/11 |
| config_docs | 10 | 10 | 9 | **9** | 10 | 0 | 7/10 |
| exact_identifier | 11 | 11 | 11 | **10** | 11 | 0 | 11/11 |
| exact_path | 11 | 11 | 5 | **7** | 9 | 1 | 6/11 |
| nl_behaviour | 11 | 9 | 9 | **9** | 9 | 4 | 11/11 |
| **all** | 64 | 59 | 49 | **48** | 55 | 11 | 38/64 |

Three readings, each checked against the grader's own rationales:

1. **Overlap is the wrong proxy; completeness is close to the right one.**
   Every question whose reviewed span reached the rater whole passed
   unless a rater answered off-target (49 complete → 48 both-pass). Every
   question that overlapped without being complete failed when the missing
   lines carried the reviewed behaviour: the grader writes "omits `X`
   identified as essential by the reviewed spans" (cd-14, cd-73, cd-44,
   cd-35). A partial window is graded as absent.
2. **Eleven INSUFFICIENT answers are real transcript gaps**, all in flows
   and behaviour questions: the callee that carries the answer
   (`persistentFlag`, the `TraverseChildren` branch, `findFlag`) is not in
   the transcript. These are the candidate-pool and cap misses the
   compact/14 result already named; the rater cannot repair them.
3. **`ambiguous` does not transfer.** 8 of 10 here, 1 of 10 on the third
   holdout, 6 and 9 of 10 on the first two. The development terms are
   identifier-shaped words with one Go declaration; the graders accept the
   declaration. Whatever the third curator's ten terms are, the same rules
   reach one of them. That stratum is where the author's questions least
   resemble a fresh curator's, and it cannot be diagnosed without reading
   the sealed key — which this author will not do.

## What follows for development

- Replace "overlapped" by "complete" as the primary selection measure on
  the reviewed split; report the grader pipeline's both-pass count as the
  calibrated forecast (48 of 64 here; the holdout came in at 38).
- The candidate's remaining losses on this split are span completeness
  under the budget (exact_path files whose declarations do not fit whole;
  flow answers split across a function and its callee) and the five
  retrieval misses. Neither is a projection rule; the second needs the
  retrieval slice with its own gates.
- No holdout is worth spending until the calibrated forecast, not the
  overlap count, clears the bar — and the `ambiguous` gap says a fresh
  curator's questions will still land below it.

## Reproduce

```sh
export CGO_ENABLED=0 GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02
GRAPHI_DEV_GRADING_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
GRAPHI_DEV_GRADING_OUT=$PWD/docs/eval/retrieval/runs/2026-09-15-dev-grading-reviewed \
  go test ./internal/eval/retrieval -run '^TestDevGradingCapture$' -count=1 -v
# raters: one codex exec per prompt and configuration, exactly as METHOD.md of the third holdout
GRAPHI_DEV_GRADING_DIR=$PWD/docs/eval/retrieval/runs/2026-09-15-dev-grading-reviewed \
GRAPHI_DEV_GRADING_RUBRIC=$PWD/docs/eval/retrieval/runs/2026-09-15-dev-grading-reviewed/grading-rubric.md \
  go test ./cmd/retrieval-eval -run '^TestDevGradingPackets$' -count=1 -v
# grader: one codex exec per packet; then the analysis over questions.json, bundles/, responses-raw/, grades-raw/
```
