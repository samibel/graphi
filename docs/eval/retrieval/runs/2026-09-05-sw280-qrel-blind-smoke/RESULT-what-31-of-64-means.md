# What 31/64 means, and what it does not

This note interprets the SW-280 qrel-blind smoke evaluation's result. It changes no number and
adjusts no threshold; it exists so that the pass count is read for what it is.

## The number

- `N` = 64 answerable holdout questions. `k` = 56, derived from `N` before the first response was
  opened. Observed pass count as reviewed: **31**. **One of those counted passes is disclosed as
  unsupported by the bundle-only rule, so the corrected count is 30 of 64** — see "The one counted
  pass that is not supported" below. Exact Clopper-Pearson 95% interval for 31/64 [0.357501996719,
  0.612741191779]; the lower bound does not reach the 3/4 floor. **RELEASE: NO on either count**,
  and the release is decided on the smaller of the two.
- 128 primary responses, all answered — no missing, empty or refused response in this run.
- 8 primary disagreements out of 64 (1/64 resolution). All 8 were adjudicated; 5 resolved to pass,
  3 to fail.

## The single largest driver: raters declining on the bundle they were given

**38 of the 128 primary responses (and several adjudicator responses) open with `INSUFFICIENT`** —
the frozen instructions' word for "the response does not contain enough to answer". The rubric
counts that as an honest answer and a failed one. So a large part of the failing count is not raters
answering wrongly; it is raters saying the bundle did not carry the answer.

Grader rationales, written independently per response, repeatedly locate the same shape: the
reviewed grade-3 span was not in the retrieved bytes. Several name it explicitly — for `ci-1923` the
answer key is in `completions.go`, which does not appear in the bundle at all; for `ci-2150` the key
is `command.go:569-575` and the bundle's nearest `command.go` snippet spans 625-637 — about 50 lines
past the key, with the four other `command.go` snippets further away still; for
`ci-2177` the bundle truncated at its 40-item cap with the target `MarkFlagsRequiredTogether` just
outside it. Those are retrieval-side observations recorded by graders who could see both the bundle
and the key.

The opposite case also occurs and is recorded: for `ci-725` the grader found the correct material
present in the bundle (`cobra.ArbitraryArgs` at `args.go:69`) and the rater still missed it, and for
`ci-2252` the adjudicator declined on a span its own citation had quoted.

## The one counted pass that is not supported

`ci-1086` is counted as a pass and the bundle-only rule does not support it. The reviewed grade-3
operation is pflag's `Changed`, and the captured `task_context/2` bundle for `ci-1086` does not
contain it. Primary rater 2 says so in its own response — the bundle does not show `.Changed` or
`Flags().Changed` — and answers around the absence; primary rater 1 supplies `Changed` by analogy,
and its grade's own rationale acknowledges that the operation was reached by inference. Both grades
are nevertheless `pass`.

**It is disclosed, not re-graded.** Quietly correcting a sealed grade is exactly the retry loop this
evaluation exists to exclude, and after the round-1 fixes a sealed grade cannot be re-sealed at all.
So the concern is recorded in `disclosed-grading-concerns.json`, published in `README.md`, and
carried in `outcome.json` as a disclosed concern that subtracts one from the corrected count. The
reviewed count stands at 31 with one unsupported pass; the corrected reading is 30. `RELEASE: NO`
is unchanged either way, and a disclosed concern can only ever subtract — there is no concern shape
that turns a counted failure into a pass.

## The population is dominated by one stratum

57 of the 64 answerable holdout questions are `config_docs`; the remaining seven are one each of
`exact_identifier`, `exact_path`, `architecture_flow`, `ambiguous` and three `nl_behaviour`. The
overall count is therefore very close to the `config_docs` count (24/57), and the six other strata
carry `1/n` resolutions of 1/1 or 1/3 — they can move the total by at most seven queries and cannot
support a per-stratum reading. This is a property of the sealed SW-279 dataset, not a choice made
here, and it is stated rather than averaged over.

## What this result is evidence for

That, on this pinned Cobra tree, for these 64 sealed questions, at a 1,200-token budget, one
`task_context/2` call frequently does not carry the reviewed answer span. That is exactly the check
the token-savings arithmetic cannot make for itself: a bundle can be cheaper because it contains
less of the answer.

## What it is not evidence for

It is not an estimate of general answerability, it is not a system-blind evaluation, and it is not a
human panel. The raters saw our bundle format and our question set. The pass count does not enter
the token estimand or its interval (`docs/eval/retrieval/methodology.md`, "Estimand and claim
boundary"), and it is not a measurement of tokens.

It is also not, by itself, a diagnosis. Distinguishing "the retrieval did not find the span" from
"the span was found but dropped by the item cap or the token budget" needs the per-query retrieval
records, which this slice preserves but does not analyse. That is a separate story.

## What this run's own records get wrong

Two defects in the run's own bookkeeping were found in review and cannot be corrected in place.

- `pre-registration.json`'s `precondition_record_commit` names `e48a1ba3…`. The precondition record
  does not exist in that commit; it first appears in `e2f104ed`. The field was populated from the
  record's `freeze_commit`, which is the candidate commit at freeze time, not the commit containing
  the record. It cannot be edited: the pre-registration is content-addressed and all 136 responses
  name that address, so rewriting it would destroy the ordering evidence it exists to provide. The
  capture instrument now resolves the containing commit from git and refuses an uncommitted record.
- `capture-provenance.json` carries no candidate binding, because the binding did not exist when the
  capture ran. Nothing in this run therefore ties the rated bytes to the candidate implementation or
  to the indexed checkout: a worktree modified without committing would have left the report saying
  the frozen candidate produced these bytes. `decide` now records that absence and refuses the
  release on it, on top of the pass count. The capture now refuses a dirty worktree on either side
  and a candidate tree that differs from the frozen candidate outside the run directory.

Neither defect can raise the pass count, and neither is offered as an excuse for it: 31 of 64 (30
corrected) against k=56 is the result, and both defects push the release further towards NO.

## Out of scope here, deliberately

Changing anything to make this gate pass is precisely what the precondition record and its
end-of-run hash comparison exist to detect. Every frozen input hash was unchanged at the end of this
run. If the gate is to be met, it is met by a new story that changes retrieval and re-runs this
evaluation from a new freeze — not by editing this one.
