# SW-280 — method notes for the qrel-blind smoke evaluation

This is the method record for SW-266 slice 6. It does **not** amend
`docs/eval/retrieval/methodology.md`, which is SW-274's frozen measurement contract; this evaluation
inherits that contract and adds nothing to the token estimand.

## What this evaluation is

A **qrel-blind smoke evaluation**: can somebody who sees only the question and the exact serialized
`task_context/2` bundle answer the question? The raters see our bundle format and our question set.
It is not a system-blind evaluation, not a human panel, and not an estimate of general
answerability. Its pass count is a separate gate and does not enter the token estimand or its
interval (`docs/eval/retrieval/methodology.md`, "Estimand and claim boundary").

## The order of operations, and why it is the deliverable

1. **Freeze.** `precondition-record.json` names every input by content hash with its freeze commit
   and timestamp: the sealed dataset, the candidate sha and its 1200-token budget, the comparator
   version, the tokenizer pin and its vocabulary digest, the budgets and targets files, this
   directory's grading rubric, the frozen methodology, and the frozen claim wording. The evaluation
   refuses to start when any of these is absent, and fails the run when any hash differs at the end.
2. **Derive and pre-register.** `N` is read from the sealed dataset as the count of answerable
   holdout queries. `k` is derived from `N` by code — the smallest integer in `[0, N]` whose
   two-sided exact Clopper-Pearson 95% lower bound is at least `3/4`. Both, together with the
   content address of every query text and every captured bundle, are written to
   `pre-registration.json` and committed **before the first rater response exists**.
3. **Rate.** Each primary rater receives the question text and the preserved bundle bytes, and
   nothing else. Every response is content-addressed the moment it is recorded, and names the
   pre-registration's own hash — which a response produced earlier could not have done.
4. **Grade.** Each answered response is graded against the frozen rubric by a recorded grader. Each
   grade names the content address of the response it graded.
5. **Adjudicate, only on a disagreement.** The adjudicator answers from the same question and the
   same bundle. Its response is frozen and content-addressed **before** the disclosure record
   exists, and the disclosure record names that frozen hash. Majority of the three graded outcomes
   decides the query.
6. **Decide.** Every frozen input hash is recomputed and compared. A pass count below `k`, or any
   drifted input, records `RELEASE: NO`.

## Rules that have no exception

- A missing, empty or refused response is a failure for that rater **and** a failure for its query.
  It is not adjudicated, re-requested or replaced.
- Two primary failures are a query failure and are not adjudicated.
- `k` is never clamped to `N`. For `N <= 12` no `k` exists at the `3/4` floor, and the evaluation
  records `RELEASE: NO` with that reason rather than lowering the bar.
- There is no flag, environment variable, configuration key or report field that lowers `k`, waives
  a query, excludes a query from `N`, retries a graded response or forces a pass.

## How to reproduce

```text
go run ./cmd/retrieval-eval -blind-eval freeze  -blind-eval-dir <this directory> -dataset internal/eval/retrieval/testdata/datasets/cobra-v2.json
go run ./cmd/retrieval-eval -blind-eval capture -blind-eval-dir <this directory> -repo cobra -checkout <pinned cobra clone> -embedder static:potion-code-16M-v2@<pin>
go run ./cmd/retrieval-eval -blind-eval decide  -blind-eval-dir <this directory>
```

The rating and grading steps between `capture` and `decide` write `responses/`, `grades/` and
`adjudications/`; they are dispatches, not PR-time tests.
