# Holdout-shaped development split — reviewed

Status: **independently reviewed on 2026-09-14** by a separate model with
no context from the author's session (`codex-cli 0.153.4`, run
non-interactively in a workspace-write sandbox with the six-step procedure
below as its only instruction). Its report is `review-report.md`; the
reviewed file is `dataset.json`, id `cobra-v3-dev-reviewed`, SHA-256
`33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`.
Outcome, from the report: 30 rows confirmed, 1 span adjusted, 3 questions
reworded, 30 rows replaced for collision with a sealed key (29 by span
overlap, 14 by paraphrase; the sets overlap). The reviewer authored the
replacements from the pinned checkout. After review, the author's own
count-only script finds 0 span overlaps with either sealed key, and a loose
substring paraphrase heuristic still flags 3 and 1 question texts, which
the reviewer's stricter judgement did not; those counts are recorded, not
resolved, because resolving them would mean the author reading the keys.

What the review does not make this: human-reviewed evidence. The
evidence class says `agent-drafted; agent-reviewed (independent)`. It is a
development split — fit to steer selection decisions and to forecast, not
to be cited as a holdout result. Three reviewer-authored `exact_path`
rows target non-Go files (`go.mod`, `LICENSE.txt`, `Makefile`); whether
retrieval indexes those at all is a property of the candidate, not of the
split, and they were kept as the reviewer judged them.

The reviewer also answered the `exact_path` rubric question from the
sealed holdouts' `grading-rubric.md`: the rubric has no special case for
path queries and allows paraphrase and reorganisation, so a judged span
delivered in adjacent pieces with only a blank line omitted **is
sufficient**. The nine draft `exact_path` rows counted "incomplete" on the
span measure are therefore not losses under the rubric.

The sections below are the original draft record and the procedure the
reviewer followed.

---

# Holdout-shaped development split — original draft record

Status at drafting: draft for independent review; not a dataset.

## Why this exists

Both sealed holdouts have one small grade-3 answer span per question and a
stratum mix of 11/11/11/11/10/10 (`exact_identifier`, `exact_path`,
`nl_behaviour`, `architecture_flow`, `config_docs`, `ambiguous`). The
committed development split has 63 spans over 40 questions, multi-span
`config_docs` keys and three whole-file `exact_path` targets that cannot fit
the frozen 1,200-token ceiling. Tuning on it optimises a shape the holdout
does not grade (`runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`).
This draft has the holdout's shape so that a development measurement can
forecast a holdout pass count instead of merely catching regressions.

## What was authored, by whom, from what

- 64 questions, `cd-01`…`cd-64`, all `split: dev`, exactly one grade-3
  judgement each, no `no_hit` rows, stratum counts 11/11/11/11/10/10.
- Authored on 2026-09-14 by the candidate's author (the same author as the
  compact selector), reading only the pinned Cobra checkout
  `a0a6ae020bb3899ff0276067863e50523f897370` and the committed development
  split. **No holdout question, key, bundle, response or grade was seen.**
- Every judgement carries `annotator: claude-fable-5.1 (draft author;
  unreviewed)` and `reviewer: PENDING …`. The dataset's `evidence_class` says
  the same. Both must change under review or the file must not be used.
- `family_id` is `cobra-family-` + the first 16 hex of
  `sha256("cobra-v3-draft:" + topic key)`; it is unique per question here and
  deliberately does not reuse any committed family id.
- All 64 spans resolve in the pinned checkout and all 64 fit the frozen
  ceiling alone (`answer-span-ceiling.json`: `any_complete_feasible` 64/64).

The author's own selector was used for nothing here: the questions were
written from source, not from what the projector answers well. That is a
statement, not a proof; the review is what makes it one.

## The author's known biases, for the reviewer

1. The author knows which query shapes the compact selector special-cases.
   Several questions land near those shapes because Cobra's interesting code
   is where the dev split already looked (hooks, completion, flags). The
   reviewer should feel free to drop or reword any question that reads as
   written *for* the selector rather than *about* Cobra.
2. `exact_path` spans here are "the file's defining declaration", chosen so
   they fit; the sealed holdouts' `exact_path` rubric is unknown to the
   author. The reviewer who knows that rubric should re-judge all eleven.
3. `ambiguous` questions are bare terms with one canonical definition chosen
   by the author; a reviewer may reasonably pick a different canonical span.
4. Three questions reuse a source region that a committed dev question also
   judges at grade 3 in a different span
   (`Traverse` 788-829 vs `cb-12`/`Find`; `Suggest` vs `cb-13`; `Root`,
   `Parent`, `Name` are near `ci-*` config_docs spans). They are in the same
   split, so no leak; the reviewer may still prefer fresh ground.

## Mechanical checks already run by the author (2026-09-14)

- Every one of the 64 anchors occurs inside its span in the pinned
  checkout (0 missing).
- Collision **counts** against the two sealed keys, computed by a script
  that printed only totals — the author does not know which rows collide:

  | Sealed holdout | Draft rows whose span overlaps a holdout grade-3 span | Question-text collisions | Family-id collisions |
  |---|---:|---:|---:|
  | first fresh | 16/64 | 3/64 | 0 |
  | second fresh | 13/64 | 2/64 | 0 |

  Both sealed holdouts are spent, so these overlaps cannot contaminate a
  *rerun* of them; they mean that tuning on this draft partly tunes on
  answer regions those holdouts also judged, exactly as the committed
  development split already does in a repository this small. The reviewer
  with key access must still remove the colliding rows (step 5) before the
  file is cited by any run, and should replace them so the stratum counts
  stay 11/11/11/11/10/10.

## Review procedure

For each of the 64 rows:

1. Open the span in the pinned checkout. Confirm it is the single best
   grade-3 answer to the question as a reader would want it, and that the
   `anchor` string occurs inside the span.
2. Adjust `start_line`/`end_line` if the span is cut or padded; keep it as
   small as the answer allows. Keep it under the ceiling (re-run the ceiling
   tool).
3. Reword the question if it is ambiguous, leading, or reads like a
   selector probe.
4. Replace `reviewer` with your identity and `annotator` if you changed the
   judgement; set `evidence_class` to the committed convention once every
   row is reviewed.

Then, once, with access to the sealed keys:

5. Diff every question and every span here against both sealed holdout keys.
   Drop any row whose question paraphrases a holdout question or whose span
   overlaps a holdout span, and record the count dropped (not the rows).
6. Recompute `answer-span-ceiling.json` and the dataset SHA-256, and record
   both in this file.

Only after step 6 may the file move out of `drafts/`, receive an id without
`-draft`, and be cited by a run directory.

## Verify the draft as committed

```sh
export CGO_ENABLED=0
go run ./cmd/retrieval-eval -answer-span-ceiling \
  -dataset docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
  -checkout /absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
  -out /tmp/draft-ceiling.json
```

This loads and validates the schema, checks every span against the checkout,
and prices it. The committed `answer-span-ceiling.json` must reproduce.
