# Fresh unseen Contract-2 holdout v4

This directory pre-registers 64 independently curated holdout questions for
`task_context/2-compact/17`, release candidate
`26a36f0958cb16d925c4dd871cda76d0e117b15a` (tree
`6ab1d7075c787b7a8884114d5d732be812cb325f`), and Cobra
`a0a6ae020bb3899ff0276067863e50523f897370`.

The frozen contracts are `sw266-measurement-contract/2` and
`sw280-qrel-blind-smoke-evaluation/2`; version 1 remains incorporated where
version 2 does not restate it. The candidate response budget is 1,200 cl100k
tokens. One response-designated follow-up read may be preserved separately at
the fixed 120-line cap. The immutable decision threshold is `k=56/64`, derived
by the two-sided exact 95% Clopper-Pearson lower-bound rule at the 3/4 floor.
No population member may be removed, retried, waived, or overwritten.

The population contains 64 unique families: 11 `exact_identifier`, 11
`exact_path`, 11 `nl_behaviour`, 11 `architecture_flow`, 10 `config_docs`, and
10 `ambiguous`. Each question has one independently reviewed exact grade-3
source span. All 64 spans and anchors resolve in the clean pinned corpus and
fit the answer-span ceiling.

Construction used the clean pinned source plus public method/schema inputs.
No candidate output, development material, consumed holdout file, capture,
answer, grade, or adjudication was opened. New v4 identifiers and source-only
selection were used; prior refused sets were not reopened.

The dataset stays sealed from operators and raters. All capture must finish and
be committed before any rater runs. This curation performs no capture, rating,
grading, adjudication, or decision.
