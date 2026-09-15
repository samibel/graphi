# Fresh unseen compact-17 holdout method

Status: **curator-sealed, pre-capture** for product source commit
`26a36f0958cb16d925c4dd871cda76d0e117b15a`, source tree
`6ab1d7075c787b7a8884114d5d732be812cb325f`, and
`task_context/2-compact/17`.

This run adopts the existing qrel-blind smoke evaluation contract version 2
and measurement contract version 2 without amendment. Version 1 remains
incorporated by reference exactly as stated in `methodology-v2.md`. The
candidate response budget remains 1,200 tokens, the version-2 follow-up read
cap remains 120 source lines, and the blind pass rule remains `k=56` of
`N=64`. No query can be dropped, substituted, retried, or waived.

## Frozen population

The independently curated dataset contains 64 answerable English holdout
queries against `spf13/cobra@a0a6ae020bb3899ff0276067863e50523f897370`.
Every query has one unique family and one independently reviewed exact grade-3
source span. The preregistered strata are:

- `exact_identifier`: 11
- `exact_path`: 11
- `nl_behaviour`: 11
- `architecture_flow`: 11
- `config_docs`: 10
- `ambiguous`: 10

All 64 reviewed spans fit beneath the frozen 1,200-token answer-span ceiling
after the fixed response overhead. There are no `no_hit` rows.

## Independence and visibility

The curator used only the clean pinned Cobra checkout, public methodology,
public budgets and targets, the candidate-neutral public rubric, and dataset
schema/validation code. The curator did not inspect any development dataset,
consumed holdout, candidate capture, answer, bundle, rating, grade, or result.
The previous compact-17 holdout was treated as consumed and remained unopened.

The root orchestration agent may receive only content-free paths, digests,
counts, and audit results. Questions, judgements, paths, line ranges, anchors,
answers, bundles, and per-query results remain sealed from it.

## Ordering

The dataset, this method, the rubric, curator preregistration, curator
attestation, and operator handoff must be committed before freeze or capture.
The operator then freezes inputs under contract 2, commits the precondition,
captures all 64 candidate bundles once, and commits the harness-generated
pre-registration and capture artifacts before any primary rater is executed.
Capture, rating, grading, adjudication, and the release decision are outside
the curator role.

Every mutating harness phase is preceded by the read-only argument and state
checks in `OPERATOR-HANDOFF.md`. A failed preflight stops the run; it is not
repaired by overwriting, retrying, or substituting any sealed input.
