# Preregistered second fresh-holdout method

Status: **public pre-capture freeze** for product candidate
`1430daf27e795c8c3a7dcc230d858935044bc33f`. This file contains no holdout
content and authorizes no evaluation response.

The governing method is `docs/eval/retrieval/methodology.md`, using the same
qrel-blind bundle-sufficiency procedure, six answerable strata, exact pinned
token accounting, participant separation, and fail-closed validation as the
valid first fresh holdout. The run-local `grading-rubric.md` is byte-identical
to that run's frozen public rubric.

The independently curated sealed population is fixed before candidate capture:

- `N=64` answerable holdout queries;
- `k=56` minimum passing responses;
- two-sided exact Clopper–Pearson 95% confidence interval;
- lower confidence bound floor `3/4`;
- no `no_hit` items in the answerable population; and
- all items carry reviewed exact grade-3 source spans at the pinned corpus SHA.

The dataset, `N`, `k`, strata, families, qrels, and rubric may not be changed in
response to candidate behavior. There are no retries or substitutions.

## Mandatory sequence

1. Verify the base, candidate, corpus, dataset, method, budget, target, and
   rubric identities recorded in `curation-pre-registration.json`.
2. Freeze the harness preconditions and candidate binding.
3. Capture all real `task_context/2` responses for the sealed population.
4. Hash and commit the complete capture, prompts, and preserved bundles.
5. Only after that capture commit may any primary, grader, or adjudicator run.

Starting a rater, disclosing a prompt, or opening a candidate response before
step 4 completes invalidates the run. This curation commit performs none of
those stages.
