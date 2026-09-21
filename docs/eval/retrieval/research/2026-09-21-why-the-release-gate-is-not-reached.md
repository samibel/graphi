# Why the release gate is not reached

Date: 2026-09-21. Status: a synthesis of already-committed evidence; it
measures nothing new and is not a release result. Every claim links the
artifact it is read from.

## The question

`go run ./cmd/retrieval-eval -check-targets <report>` evaluates five targets.
Four pass: architecture-flow nDCG@10 (0.4625 ≥ 0.4579), natural-language
nDCG@10 (0.7029 ≥ 0.5450), exact-identifier Top-1 (1.0 ≥ 1), bundle coverage
(6/6). The fifth — `qrel_blind_smoke`, which requires a sealed holdout to
record **RELEASE: YES** — misses. This document is the full causal account of
why, and what each layer of evidence does and does not rule out.

## The binding target is a statistical proof, not a point score

The smoke gate is decided on `k`, derived before any response is opened
(`runs/2026-09-15-product-compact-v17-fresh-unseen-v4/README.md`, "How `k`
was derived"): the smallest integer in [0, 64] whose two-sided exact
Clopper-Pearson 95% lower bound is at least 3/4. k = 56 (lower bound
0.7685); 55 already falls to 0.7498 and misses the floor.

Consequence: the gate does not ask for 75% of 64 questions. It asks for
**95% confidence that the true per-question rate is at least 75%**, which
requires a raw count of 56/64 = 87.5%. As the answer-span ceiling run
computed (`runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`): with a
fully feasible span supply, passing k = 56 with 95% probability needs a true
rate near 94%; passing with even odds needs 87.5%.

## The sealed evidence series

Four sealed, independently curated, pre-registered holdouts exist. All four
returned RELEASE: NO.

| Holdout | Candidate | Pass count | Raw rate | CP 95% lower bound |
|---|---|---:|---:|---:|
| First fresh sealed (`…-v5-fresh-sealed-holdout`) | compact/v5 | 42/64 | 65.6% | — |
| Second fresh sealed (`…-v5-second-fresh-sealed-holdout`) | compact/v5 | 32/64 | 50.0% | — |
| Third fresh sealed (`…-v14-third-fresh-sealed-holdout`) | compact/14 | 38/64 | 59.4% | — |
| Unseen v4 (`…-v17-fresh-unseen-v4`) | compact/17 | 47/64 | 73.4% | 0.609 |

The best raw rate observed is 73.4%. The gate needs 87.5%. The smoke gate
reads the latest recorded outcome, so today it reports 47 of 64 against
k = 56.

Per-stratum, third holdout (compact/14, 38): exact_identifier 11/11,
nl_behaviour 11/11, config_docs 7/10, exact_path 6/11, architecture_flow
2/11, ambiguous 1/10. Unseen v4 (compact/17, 47): exact_identifier 11/11,
exact_path 11/11, config_docs 10/10, nl_behaviour 7/11, architecture_flow
6/11, ambiguous 2/10.

## Measured non-causes

Each of these was tested and rejected with committed evidence:

1. **The 1,200-token ceiling.** The answer-span ceiling run (aggregate-only,
   sealed data, `runs/2026-09-14-holdout-answer-span-ceiling/`) shows 64/64
   of the second holdout's and 62/64 of the first holdout's reviewed answer
   spans fit the frozen budget. The ceiling is attainable in principle; it
   is not why the holdouts fail.
2. **The embedder.** The shipped static embedder (Potion-512) mechanically
   delivers the reviewed grade-3 span in 60/64 bundles (94%)
   (`runs/embedded-model-qualification/FINDING-coderank-does-not-beat-potion.md`).
   The alternative embedder (CodeRank, admitted within every operating
   budget: RSS 94%, artifacts 51%, query p95 6%) delivers 51/64 — paired
   0 won / 9 lost against the shipped one — and its losses concentrate
   exactly in the strata a semantic embedder should win (ambiguous −3,
   nl_behaviour −3). Model-side retrieval is not the binding constraint.
3. **The candidate pool.** The two persistent reviewed-split retrieval
   misses (cd-29 `findFlag`, cd-87 `writeCommands`) are inside the retrieval
   top-50 (evidence ranks 11 and 46) on the pinned corpus
   (`research/2026-09-21-cd29-cd87-miss-diagnosis.md`). They die in
   evidence ranking, and the one admission mechanism built for them was
   measured to break the frozen architecture-flow gate and reverted.
4. **Ranking-gate metrics.** architecture-flow, natural-language,
   exact-identifier and bundle-coverage targets all pass on the development
   population (`runs/2026-09-15-compact17-release-dev/RESULT.md`). Ranking
   as measured is not the failing component — although the
   architecture-flow margin (0.4625 vs 0.4579) is thin enough that one
   measured experiment broke it.

## The three measured causes

### 1. The development population over-predicts the holdout — structurally

The development split reads 48-59/64 on the grading pipeline
(`runs/2026-09-15-dev-grading-reviewed/`, 48/64 with the codex panel;
`runs/2026-09-20-dev-grading-compact17/` and
`runs/2026-09-20-dev-grading-compact14-kimi-panel/`, 59/64 with the
subagent panel on both candidates). The sealed holdouts return 32-47. The
gap is largest where the author's questions least resemble a fresh
curator's: `ambiguous` reads 8-10/10 in development and 1-2/10 in every
holdout. The ceiling run's third conclusion states the general form: the
development split has 63 spans over 40 questions with multi-span config
keys and whole-file targets, the holdouts have one small span per question;
"the development split can still catch regressions; it cannot forecast a
pass count." The compact selector's twelve query-shape predicates are named
there as the most likely carrier of the transfer failure.

### 2. Span presence is not answerability

The mechanical channel delivers the reviewed span into 94% of bundles; the
blind pipeline answers 73% (v4). The ~20-point difference is the rubric's
subject: a blind reader must be able to ANSWER from the transcript, and a
partial window is graded as absent — the codex-panel grading of the
reviewed split showed every question whose span arrived whole passed, and
every question that merely overlapped failed when the missing lines carried
the reviewed behaviour. compact/15/16/17 attacked exactly this
(completeness 49 → 60 on the reviewed split) and the transfer is visible in
the strata that moved between the third holdout and v4: architecture_flow
2/11 → 6/11, exact_path 6/11 → 11/11, config_docs 7/10 → 10/10. It is
equally visible that `nl_behaviour` fell 11/11 → 7/11 and `ambiguous` did
not move (1 → 2): the projection work transfers where questions are
document-shaped, and not where they are behaviour-shaped or genuinely
ambiguous.

### 3. The bar demands more than the candidate family's best day

The four sealed runs come from four different curators and therefore four
different question lots; 32 to 47 is partly draw. But the gate does not
grade on draws — it requires a candidate whose true rate the data can
certify at 75% with 95% confidence. The best observed lot produced a 73.4%
raw rate and a 0.609 lower bound. Between the best day so far and the bar
stand nine questions, on a pipeline whose best evidence series reads
32 → 42 → 38 → 47 and has never once approached 56.

## Where v4's 17 misses sit

ambiguous 8, architecture_flow 5, nl_behaviour 4. The first is the
population-transfer failure (cause 1) — the author cannot author those
questions, so they cannot be developed against; they can only be found by
spending holdouts. The second and third are answer-sufficiency failures
(cause 2) in the strata where the transcript's coherence, not span
presence, decides the grade.

## What would have to be true for RELEASE: YES

A fresh, independently curated, pre-registered 64-query holdout returns at
least 56 passes under the unchanged contract-2 rubric and decision rule.
Everything short of that — ranking gates, development gradings, ceiling
feasibility, embedder qualifications — is supporting evidence at best. The
frozen methodology deliberately provides no path to `k` relief, no waiver,
no retry and no re-grade (the `-blind-eval` flag text, the outcome records,
and `targets-gate-expectations.json` all say so).

The honest levers, in order of the evidence behind them:

1. **A holdout-shaped development set** (one reviewed small span per
   question, authored fresh, never a holdout question) so that dev numbers
   can measure what the holdout grades — the ceiling run's explicit
   recommendation, and the purpose of
   `drafts/2026-09-14-holdout-shaped-dev/`.
2. **Answer-sufficiency work in nl_behaviour and ambiguous shapes**, the
   strata that did not transfer — but gated on the holdout-shaped set, not
   on the current 40/64, which provably cannot see those failures.
3. **Nothing on the model side for now**: the shipped embedder delivers
   94% mechanically and the alternative loses to it.

## Bottom line

The release gate is not reached because its binding component is a proof
obligation — 95% confidence in a true 75% blind answer rate on questions
the candidate's author has never seen — and the measured answer rate of the
best candidate so far is 73% on its best day, 50-66% otherwise. The budget
is exonerated, the embedder is exonerated, the ranking gates pass. What
stands in the way is a 15-20-point answer-sufficiency gap concentrated in
ambiguous and natural-language questions, plus a development population
that structurally cannot see that gap — which is why every improvement
that transferred moved flows and paths, and why the strata that decide the
gate have not moved.
