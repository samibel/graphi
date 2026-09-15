# Preregistered compact/17 sealed-holdout method

Status: **new candidate-bound pre-capture run**, based on an unchanged eligible
seal. It is bound to candidate commit
`2d65df335544c2c66c0ff12e8f79db4f6c53e72b`, candidate tree
`9af24c26ba6b5b221e70f7e8998c351b640776f8`, and product identity
`task_context/2-compact/17`.

The earlier compact/16 preregistration is not amended, replaced, or
substituted. This is a distinct run directory and a distinct preregistration.
The sealed dataset is copied byte-for-byte from its original curator commit;
its SHA-256 is unchanged. Its embedded compact/16 note records curation
provenance only. Candidate binding is represented by this run's preregistration
and, later, the harness precondition record; the dataset schema itself has no
candidate-binding field.

The governing public method remains `docs/eval/retrieval/methodology.md`. The
run uses `sw280-qrel-blind-smoke-evaluation/2` and
`sw266-measurement-contract/2`, the frozen 1,200-token budget, exact grade-3
scoring, two-sided exact 95% Clopper–Pearson interval, lower-bound floor `3/4`,
and preregistered threshold `k=56` for `N=64`.

## Reuse eligibility

The unchanged seal is eligible because its source run contains only curation
and preregistration artifacts: no candidate capture, prompt disclosure,
primary response, rating, grade, adjudication, or result exists. The source
attestation records no disclosure of holdout content to the root orchestration
agent. The frozen method prohibits mutation or substitution within a run and
after capture begins; it does not mark an undisclosed, never-captured seal as
consumed merely because a candidate-bound preregistration was created.

`reuse-determination.json` records the non-content-bearing evidence for this
decision. If any asserted precondition is later shown false, this new run is
invalid and must not be captured.

## Mandatory stage order

1. Verify candidate commit/tree/product identity, corpus pin, and every frozen
   input hash in `curation-pre-registration.json`.
2. Freeze the new run using contract selection `2`.
3. Capture the complete real candidate transcript for every sealed item.
4. Hash and commit the complete capture, prompts, and preserved bundles.
5. Only after that commit may a primary, rater, grader, or adjudicator run.

This commit performs none of steps 2–5.
