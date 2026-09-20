# Development grading of the compact/14 capture with the subagent panel

Status: **development instrument and result; not a release result.** This
run exists to separate the two changes mixed into
`../2026-09-20-dev-grading-compact17/`: the candidate change
(compact/14 → compact/17) and the panel change (the holdout's two codex
configurations → fresh-context kimi-k3 subagents on both arms). It
re-grades the committed compact/14 capture of
`../2026-09-15-dev-grading-reviewed/` — the same 64 bundles, the same
dataset (`cobra-v3-dev-reviewed`, SHA-256 `33760861d5c7…`), the same
version-2 transcript rubric (SHA-256 prefix `f132ee59f254`) — with
exactly the subagent panel and discipline the compact/17 run used: one
fresh agent per item, instructed to read exactly one prompt or packet
file and write exactly one answer file. 128 responses, 128 grades, 0
refusals. Bundles/, prompts/ and questions.json are byte-copies of the
committed compact/14 capture; responses-raw/ and grades-raw/ are new.

The grader packets are not committed: they embed the rubric path as
given to the packet test, an absolute path outside the repository, which
the pre-commit guard refuses; `TestDevGradingPackets` rebuilds them from
the committed material, differing only in that one line.

## Three-way comparison

| Run | Candidate | Panel | both-pass | any-pass | overlap | complete | INSUFF |
|---|---|---|---:|---:|---:|---:|---:|
| 2026-09-15-dev-grading-reviewed | compact/14 | codex a/b | **48**/64 | 55 | 59 | 49 | 11 |
| this run | compact/14 | kimi-k3 ×2 | **59**/64 | 60 | 59 | 49 | 7 |
| 2026-09-20-dev-grading-compact17 | compact/17 | kimi-k3 ×2 | **59**/64 | 62 | 61 | 60 | 2 |

Per-stratum both-pass, this run (kimi panel on compact/14):

| Stratum | n | overlapped | complete | both primaries PASS | any PASS | INSUFFICIENT responses |
|---|---:|---:|---:|---:|---:|---:|
| ambiguous | 10 | 10 | 9 | **10** | 10 | 0 |
| architecture_flow | 11 | 8 | 6 | **9** | 9 | 4 |
| config_docs | 10 | 10 | 9 | **10** | 10 | 0 |
| exact_identifier | 11 | 11 | 11 | **11** | 11 | 0 |
| exact_path | 11 | 11 | 5 | **10** | 11 | 0 |
| nl_behaviour | 11 | 9 | 9 | **9** | 9 | 3 |
| **all** | 64 | 59 | 49 | **59** | 60 | 7 |

Readings:

1. **The panel change, not the candidate change, moves the count.**
   compact/14 scores 48 under the codex panel and 59 under the subagent
   panel; compact/17 scores 59 under the same subagent panel. The
   candidate delta at constant panel is 59 → 59.
2. **The subagent panel is measurably more lenient on partial spans.**
   The compact/14 run with the codex panel established "a partial window
   is graded as absent" (exact_path: 5 complete of 11 → 7 both-pass;
   architecture_flow: 6 complete → 5 both-pass). Under the subagent
   panel the same capture's exact_path goes 10/11 both-pass on the same
   5 complete spans, and architecture_flow 9/11 on the same 6. The
   graders here accept partial windows when the answer's claim is still
   grounded in them. The codex panel remains the calibrated reference
   for holdout forecasts; the subagent panel over-reads partial spans.
3. **The candidate's structural delta is real but invisible to this
   panel.** compact/17 raises completeness 49 → 60 and cuts the
   no-overlap misses from four (cd-29, cd-41, cd-79, cd-87) to two
   (cd-29, cd-87). Those are properties of the captured transcripts, not
   of any panel; cd-41 and cd-79 are INSUFFICIENT-for-real misses under
   compact/14 that compact/17's flow-completeness and query-directed
   rules repair. Because the subagent panel already passed most partial
   windows, the repairs do not change the count.
4. **The surviving two misses are the same in both captures.** cd-29
   (`findFlag`, completions.go:841-858) and cd-87 (the traversal flow
   span) are absent from both candidates' transcripts — upstream-pool
   and ranking gaps, exactly the retrieval slice's business. One
   disagreement remains in each run (cd-14 here; cd-18, cd-44, cd-86 in
   the compact/17 run).

## What follows for development

- Holdout forecasts must use the codex panel or a panel calibrated
  against it; the subagent panel reads 59 on a capture the codex panel
  reads 48.
- The candidate-side evidence for compact/17 over compact/14 is the
  transcript structure (completeness 60 vs 49, misses 2 vs 4), which no
  panel votes on. Whether that structure converts into holdout passes is
  exactly what a holdout would answer; the release gate stays RED and
  nothing here is release evidence.
- cd-29 and cd-87 remain the two retrieval misses to work; they fail in
  every capture and under every panel tried so far.

## Reproduce

```sh
# capture: this run reuses ../2026-09-15-dev-grading-reviewed/bundles|prompts|questions.json verbatim
# raters and grader: one fresh subagent per prompt/packet, instructed to read
# exactly that one file and write exactly one answer file
GRAPHI_DEV_GRADING_DIR=$PWD/docs/eval/retrieval/runs/2026-09-20-dev-grading-compact14-kimi-panel \
GRAPHI_DEV_GRADING_RUBRIC=$PWD/docs/eval/retrieval/runs/2026-09-20-dev-grading-compact14-kimi-panel/grading-rubric.md \
  go test ./cmd/retrieval-eval -run '^TestDevGradingPackets$' -count=1 -v
python3 docs/eval/retrieval/runs/2026-09-20-dev-grading-compact14-kimi-panel/analyze.py \
  docs/eval/retrieval/runs/2026-09-20-dev-grading-compact14-kimi-panel
```
