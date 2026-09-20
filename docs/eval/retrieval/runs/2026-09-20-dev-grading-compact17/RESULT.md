# Development grading of the reviewed split at compact/17 with the holdout's rater and grader pipeline

Status: **development instrument and result; not a release result.** It
repeats the reviewed-split grading of
`../2026-09-15-dev-grading-reviewed/` (compact/14, 48 of 64 both-pass) at
the current candidate so the author can see what compact/15, compact/16
and compact/17 changed before another holdout is considered.

Candidate: compact/17 (`task_context/2-compact/17`,
`engine/agenttools/taskctx/compact/v9/compact.go`) at `698e2261`, the
merge head this capture was taken from. Dataset and split are unchanged:
`cobra-v3-dev-reviewed`, SHA-256 `33760861d5c7…`, the same 64 questions.
Capture code, rater prompt, grader packet builder and the version-2
transcript rubric are the ones the third holdout used
(`TestDevGradingCapture`, `TestDevGradingPackets`; rubric SHA-256 prefix
`f132ee59f254`). The capture designates 9 follow-up reads (cd-24, cd-41,
cd-52, cd-58, cd-61, cd-63, cd-78, cd-88, cd-93); all 9 were taken and
charged by the transcript scorer.

**The panel is not the holdout's panel.** The plan was the holdout's two
codex configurations again; the codex attempt stopped at the usage limit
after 38 of 128 primary responses (kept, unused, in
`responses-raw-codex-aborted/` and `execution-logs-codex-aborted/`). The
run was completed with fresh-context kimi-k3 subagents as *both* rater
configurations and as the grader: each item was a separate agent
instructed to read exactly one prompt or packet file and to write exactly
one answer file. That is instruction-enforced blindness, not the codex
run's process isolation, and the same model sat on both sides of every
pair. 128 responses and 128 grades, 0 refusals. Per consequence, the
48 → 59 difference against the compact/14 run mixes candidate change and
panel change; this run does not separate them. What is panel-independent
is named below.

Bundles, prompts, raw responses, grades, the aborted codex attempt and
the analysis are committed beside this file. The grader packets are not:
they embed the rubric path as given to the packet test, an absolute path
outside the repository, which the pre-commit guard refuses;
`TestDevGradingPackets` rebuilds them from the committed bundles,
responses and rubric, differing only in that one line.

## Calibration at compact/17

| Stratum | n | overlapped | complete | both primaries PASS | any PASS | INSUFFICIENT responses | holdout 3 passed |
|---|---:|---:|---:|---:|---:|---:|---:|
| ambiguous | 10 | 10 | 10 | **10** | 10 | 0 | 1/10 |
| architecture_flow | 11 | 9 | 8 | **8** | 10 | 2 | 2/11 |
| config_docs | 10 | 10 | 10 | **10** | 10 | 0 | 7/10 |
| exact_identifier | 11 | 11 | 11 | **11** | 11 | 0 | 11/11 |
| exact_path | 11 | 11 | 11 | **10** | 11 | 0 | 6/11 |
| nl_behaviour | 11 | 10 | 10 | **10** | 10 | 0 | 11/11 |
| **all** | 64 | 61 | 60 | **59** | 62 | 2 | 38/64 |

Readings, checked against the graders' rationales:

1. **Completeness still tracks the grade.** 60 questions reached the
   raters with the reviewed span whole; 58 of them passed both primaries,
   and the two that did not (cd-18, cd-86) are grader disagreements about
   the answer, not transcript gaps. Three of the four incomplete
   questions failed at least one primary (cd-29, cd-44, cd-87); the
   fourth (cd-40) both-passed because the answer did not actually need
   the missing span. The "a partial window is graded as absent" rule from
   the compact/14 run still holds.
2. **Two retrieval misses remain, and they are panel-independent.**
   cd-29 (`nl_behaviour`): the `findFlag` span (`completions.go:841-858`)
   is not in the transcript; both raters answered without it and both
   graders failed them. cd-87 (`architecture_flow`): the reviewed span is
   not in the transcript; both raters declared INSUFFICIENT and both
   graders failed them. These are upstream-pool and ranking gaps, not
   projection rules, and any panel would have seen the same transcripts.
3. **Disagreement concentrates where it did before.** 3 of 64 pairs
   split (cd-18 exact_path, cd-44 architecture_flow on an incomplete
   span, cd-86 architecture_flow where grader A reads the answer as
   contradicting the transcript on the traversal and grader B accepts
   it). Two of three are flow questions, matching the compact/14 run's
   finding that architecture_flow is where the rubric's completeness
   demand is hardest to apply.
4. **The `ambiguous` stratum passed 10 of 10 here and 1 of 10 on the
   third holdout.** The compact/14 run already showed this stratum does
   not transfer to a fresh curator; nothing about this run changes that.

## What follows for development

- The calibrated forecast at compact/17 is 59 of 64 with this panel. That
  number is not comparable to the compact/14 run's 48 of 64 (different
  panel), and neither number is a release result. To isolate the
  candidate delta, re-grade the committed compact/14 capture
  (`../2026-09-15-dev-grading-reviewed/bundles/`) with this same
  subagent panel; that comparison is cheap and both sides would share
  the panel. **Done:** `../2026-09-20-dev-grading-compact14-kimi-panel/`
  — compact/14 scores 59 of 64 under this panel too; the 48 → 59
  difference was the panel, and the candidate delta at constant panel is
  the transcript structure (completeness 49 → 60, misses 4 → 2), which
  no panel votes on.
- The two real misses (cd-29's `findFlag` window, cd-87's flow span) are
  the retrieval slice's business: candidate pool and ranking, with the
  ranking gates as their own evidence. The projection is not implicated.
- The release gate stays RED. Nothing in this run speaks to a fresh
  curator's questions; per the standing rule, no release claim comes
  from development data.

## Reproduce

```sh
export CGO_ENABLED=0 GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02
GRAPHI_DEV_GRADING_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
GRAPHI_DEV_GRADING_OUT=$PWD/docs/eval/retrieval/runs/2026-09-20-dev-grading-compact17 \
  go test ./internal/eval/retrieval -run '^TestDevGradingCapture$' -count=1 -v
# raters and grader: one fresh subagent per prompt/packet, instructed to read
# exactly that one file and write exactly one answer file (no codex; see above)
GRAPHI_DEV_GRADING_DIR=$PWD/docs/eval/retrieval/runs/2026-09-20-dev-grading-compact17 \
GRAPHI_DEV_GRADING_RUBRIC=$PWD/docs/eval/retrieval/runs/2026-09-20-dev-grading-compact17/grading-rubric.md \
  go test ./cmd/retrieval-eval -run '^TestDevGradingPackets$' -count=1 -v
python3 docs/eval/retrieval/runs/2026-09-20-dev-grading-compact17/analyze.py \
  docs/eval/retrieval/runs/2026-09-20-dev-grading-compact17
```
