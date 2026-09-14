# Contract v2 options: what the 1,200-token evidence permits

Status: decision **material**, not a decision. It changes no frozen input.
`sw266-measurement-contract/1` and `sw280-qrel-blind-smoke-evaluation/1`
remain in force until a successor version is written, reviewed and adopted
in its own slice; nothing here may be applied to a running or sealed
evaluation.

## The situation, in numbers

| Fact | Value | Source |
|---|---:|---|
| Pre-registered holdout bar | 56 of 64 (87.5 %) | `curation-pre-registration.json`, both sealed holdouts |
| Rule that produced it | smallest `k` whose exact Clopper-Pearson 95 % lower bound is ≥ 3/4 | same |
| Last two independent holdout outcomes | 32/64, then 42/64 | sealed run directories |
| Development questions with any answer span that fits 1,200 tokens | 35 of 40 (87.5 %) | `answer-span-ceiling.json` |
| Development questions with every answer span fitting | 33 of 40 | same |
| Development `≥1 complete span`, current candidate | 33 of 40 (82.5 %) | `2026-09-14-named-declaration-dev/RESULT.md` |
| Gain in `≥1 complete span` from raising the source frontier 325 → 750 | 0 | frontier sweep, same RESULT |
| Paired median saving at 325 / 425 fields | +5.9 % / −5.1 % | same |
| Query-shape predicates in the compact selector fitted on the 40 dev questions | 12 | `compact/v9/compact.go` |

Two arithmetic consequences:

1. **The bar equals the development ceiling.** A candidate that delivers a
   complete span for every development question that can have one scores
   exactly 35/40 = 87.5 %. If the holdout is composed like the development
   split, a candidate with a *true* per-question rate of 87.5 % observes
   56 or more of 64 with probability ≈ 0.59; at the current 82.5 % it is
   ≈ 0.19; at 80 % it is ≈ 0.08. `k = 56` is a coin toss for a perfect
   candidate and a long shot for a good one.
2. **Response budget is exhausted as a lever.** Between 325 and 750 source
   fields the complete-span counts do not move; the token-savings claim
   inverts. The remaining losses are ranking (11 of 63 dev spans), retrieval
   recall (7) and the candidate cap (3), and 7 spans cannot fit at all.

**Measured on the sealed holdouts (2026-09-14, aggregate only; see
`runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`):** the first sealed
holdout has `F = 62/64`, the second `F = 64/64`. The paragraph this replaces
predicted at most 53 by extrapolating the development split's whole-file
`exact_path` targets to the holdout; the curator did not judge whole files,
and every holdout question has exactly one small grade-3 span. The
development split is therefore a poor proxy for the holdout's shape, and
consequence 1 above — the bar equals the ceiling — holds for the development
split only. On the holdouts, `k = 56` is attainable and the entire gap is
candidate quality.

## Options

Each option is a new contract version. None may be applied retroactively.

### A. Keep the estimand; derive `k` from the measured ceiling

Keep 1,200 tokens, one response, `GrepRead/2`, the paired-median estimand and
the rubric. Change only how `k` is set: the curator computes `F` on the
sealed key before capture and pre-registers `k` as the smallest count whose
Clopper-Pearson lower bound is at or above `floor × F/N` — the same 3/4
floor, applied to the attainable population rather than to all of `N`.

- Cost: none in product; a paragraph in `sw280` v2 and one field in the
  pre-registration. The infeasible questions stay in `N` and are still
  graded; they simply stop being assumed answerable.
- Risk: it reads as moving the bar. It is defensible only because `F` is
  computed by the curator from the sealed key, by a committed, tested tool,
  before capture, and recorded with its SHA-256. A `k` derived after capture
  is not this option.
- What it does not fix: the 42/64 → `k` gap is not only ceiling. At the
  development rate the candidate would still be short of a ceiling-derived
  `k`; the ranking and recall work remains.

### B. Raise the ceiling

A larger candidate budget (for example 2,000 tokens) is a new estimand under
`sw266` v2 with a new `GrepRead` comparison at the same recall target.

- What it buys on development: the 7 over-ceiling spans include four that
  would fit under 2,000 tokens (`cb-09`, `cb-14`'s first span, `cb-19`'s
  second, `cb-35`); the three whole-file `exact_path` targets would still
  not fit. `F` would rise from 35 to at most 38 of 40.
- What it costs: the frontier sweep says the paired median saving is already
  negative at 425 fields. At 2,000 tokens the token-savings claim against
  `GrepRead` is very unlikely to survive on this dataset; the candidate
  would be evaluated on answerability alone.
- Risk: every committed capture, count, aggregate and claim text is bound to
  the 1,200 budget and is invalidated together; the tokenizer and static-pin
  rotation inventories already enumerate what.

### C. Allow a bounded second call

Keep 1,200 tokens per response but let the measured object be one
`task_context/2` response plus at most one follow-up read of a span the first
response cited, with both responses charged. `GrepRead` already gets eight
reads; the candidate gets two.

- What it buys: whole-file and long-function targets become reachable
  through a truthful outline followed by one exact read. This is the only
  option under which `exact_path` questions can be answered at their own
  ceiling.
- What it costs: a new capture instrument (two responses, one transcript),
  a new equal-recall scorer step, and a rubric revision so the grader sees
  both responses. The paired comparison against `GrepRead` stays
  well-defined because both arms are then multi-response transcripts
  charged per response.
- Risk: the largest change of the three; it is a different product
  behaviour, not a different measurement of the same one.

**Measured ceiling (2026-09-14, reviewed holdout-shaped split, candidate
compact/12).** `TestOneSpanCompactDev` with `GRAPHI_ONE_SPAN_FOLLOWUP`
simulates the contract with one *deterministic* follow-up: after the
compact response, read the whole declaration or section that the first
emitted source lies in, capped at 120 lines, charged by its own cl100k
count. No question-specific choice is made; a real second call chosen by
the reader can only do better.

| | one response (compact/12) | + one follow-up read |
|---|---:|---:|
| Spans overlapped | 51/64 | **54/64** |
| Spans complete | 39/64 | **45/64** |
| config_docs overlapped / complete | 8 / 6 | **10 / 9** |
| architecture_flow overlapped / complete | 7 / 4 | 8 / 6 |
| nl_behaviour overlapped / complete | 9 / 8 | 9 / 9 |
| Follow-up reads issued | — | 11 of 64 |
| Median / max follow-up tokens | — | 626 / 1,062 |

Reading the *first truncated* cited declaration instead of the lead's
issues 58 reads for the same 54/45, so the lead policy is the right
default. The remaining ten non-overlapped questions are three non-Go
`exact_path` files retrieval does not index, five candidate-pool or cap
misses, and two struct-field targets inside a very long type.

At 84 % overlap the chance of 56 of 64 is about 0.3 — a coin toss made
fairer, not a pass. The follow-up buys what a single response cannot
(whole declarations, whole sections) and leaves the pool misses where they
are; a release-grade result at `k = 56` needs both this contract and the
upstream pool work.

### D. Do nothing to the contract; fix ranking and recall

Spend the next slice on the 21 development spans lost upstream of the
projector (7 retrieval recall, 3 candidate cap, 11 ranking), re-gated on the
existing nDCG targets, and then spend a holdout.

- What it buys: real answer quality; it is also the only option that can
  raise a candidate's true rate rather than the bar's attainability.
- What it does not buy: `k = 56` on a holdout whose ceiling is below 56. If
  the curator's `F` for the holdout is under 56, this option alone cannot
  produce a release YES no matter how good the ranking becomes.
- Risk: the development split has 40 questions and one-question resolution;
  each of the twelve existing query-shape predicates was justified by a
  development gain of that size, and the two sealed holdouts have shown how
  those gains transfer. Any ranking change should be gated on removing
  predicates, not adding them.

## Recommendation

The protocol has been run on both sealed keys: `F = 62/64` and `F = 64/64`.
That settles it in favour of the second branch that this section originally
left open:

- **A is withdrawn.** The bar is attainable in principle on both holdouts;
  deriving `k` from the ceiling would change nothing and would look like
  what it is not.
- **D is the slice.** The 42 → 56 gap is candidate quality on questions
  whose answers fit. Before spending it, build a development set shaped like
  the holdout — one reviewed small span per question, authored fresh, never
  a holdout question — because the current 40 development questions cannot
  measure what the holdout grades. Gate the ranking work on *removing*
  query-shape predicates from the compact selector, not adding to them.
- **C** remains the structural change to plan for if whole-file or
  long-function questions ever enter a holdout; on these two they did not.
- **B** is not recommended: it trades the token-savings claim for at most
  three development questions and zero holdout questions.

## What must not happen

- No `k` is lowered on a sealed or running evaluation.
- No holdout is rerun, reopened, or consulted for tuning; the 42/64 result
  stands as the last independent evidence.
- No option here is adopted by editing this file; each is a versioned
  contract change with its own tests and its own review.
