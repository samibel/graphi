# Answer-span ceiling protocol for a sealed holdout

Status: protocol, `answer-span-ceiling/1`. It authorises no release claim and
opens no sealed data. It exists so that a pre-registered pass count can be
read against what the frozen 1,200-token candidate budget permits at all —
before another independent holdout is spent finding out.

## What is measured and why it is safe to share

A reviewed grade-3 answer span whose exact `cl100k_base` cost, as one source
entry in an otherwise empty `task_context/2` response, exceeds the frozen
1,200-token candidate budget can never be delivered complete — by the current
candidate or by any future one. Counting those spans bounds from above what
any projector can reach on that dataset.

The instrument consults judgements and is therefore run **only by the
curator of the sealed split**, never by anyone who has produced or will tune
a candidate. Its aggregate report is safe to hand back because, by
construction and by test (`TestAnswerSpanCeiling_AggregatesPerSplitAndNamesNothing`,
`TestAnswerSpanCeiling_AggregateNamesNothingDetailIsOptIn`), it contains only:

- the dataset id and SHA-256, the pinned repository SHA, the ceiling, the
  overhead constant, the grade, the tokenizer id and vocabulary SHA-256; and
- per split and overall, six integer counts: answerable queries, grade-3
  spans, spans over the ceiling, queries with at least one feasible span,
  queries with every span feasible, and queries with no feasible span.

It names no query, no question text, no path and no line. The per-query
detail file is written only when `-answer-span-detail` names a separate file
and is for the curator's eyes only; it must not be committed for a sealed
split and must not be sent to the candidate's author.

## Command

```sh
export CGO_ENABLED=0
go run ./cmd/retrieval-eval -answer-span-ceiling \
  -dataset <sealed-dataset.json> \
  -checkout /absolute/path/to/cobra-at-<pinned-sha> \
  -out <run-dir>/answer-span-ceiling.json
```

The command refuses a checkout that is not at the SHA the dataset cites and
refuses a dataset with a span that does not resolve in that checkout; it never
silently excludes a span, because a missing span would make the bound look
tighter than it is. It downloads nothing; the tokenizer is the embedded,
SHA-verified artifact already governed by
`internal/eval/tokenizer/PIN_ROTATION.md`.

## What the curator returns

The file written to `-out`, unedited, plus its SHA-256, committed into the
holdout run directory beside `curation-pre-registration.json`. Nothing else.

## How it is read

Let `F` be `overall.any_complete_feasible` and `N` be
`overall.answerable_queries`. Then for any candidate:

- the count of holdout questions with a complete grade-3 span is at most `F`;
- a pre-registered `k > F` cannot be met by delivering complete answer spans,
  whatever the candidate does; and
- a `k` close to `F` requires the candidate to answer nearly every feasible
  question, and the observed count is binomial around the true rate, so the
  pass probability is far below one even for a candidate that never misses a
  feasible question.

The holdout is graded for bundle sufficiency by a rubric, not by span
completeness, so `F` is an indicator for the rubric's ceiling, not that
ceiling itself: a truthful outline of a whole-file target may be judged
sufficient where the whole file could not fit. That direction of error is the
only one available — the rubric cannot be *more* demanding than a complete
span — so `F/N` should be read as a rough upper bound on the achievable pass
rate, and any `k` set above `F` should be read as unattainable.

## Reference value on the development split

Computed from `docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json`
over the pinned Cobra checkout and recorded in
`docs/eval/retrieval/runs/2026-09-14-named-declaration-dev/answer-span-ceiling.json`:

| | Development |
|---|---:|
| Answerable queries | 40 |
| Grade-3 spans | 63 |
| Spans over the ceiling | 7 |
| Queries with ≥1 feasible span (`F`) | 35 |
| Queries with every span feasible | 33 |
| Queries with no feasible span | 5 |

`F/N = 35/40 = 87.5 %` on development. The two sealed holdouts, measured
aggregate-only under this protocol (with a custody disclosure, see
`runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`), have `F = 62/64`
and `F = 64/64`: one small grade-3 span per question, none or two over the
ceiling. The development split's whole-file targets are not what the
holdout curator judged, so the development `F` must not be read as a
forecast of a holdout's; it bounds development measurements only. See
`contract-v2-options.md` for what follows.

## Pre-registration wording

A holdout curation that adopts this protocol adds to
`curation-pre-registration.json`, before capture:

```json
"answer_span_ceiling": {
  "protocol": "answer-span-ceiling/1",
  "report": "answer-span-ceiling.json",
  "report_sha256": "<sha256 of the unedited report>",
  "any_complete_feasible": <F>,
  "answerable_queries": <N>
}
```

and states in `METHOD.md` whether `k` was chosen with knowledge of `F` and,
if so, by which rule. A `k` chosen without that knowledge is not invalid; it
is simply a bar whose attainability nobody had measured.
