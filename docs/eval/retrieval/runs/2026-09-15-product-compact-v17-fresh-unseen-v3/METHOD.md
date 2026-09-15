# Fresh unseen Contract-2 holdout v3 method

This directory pre-registers an independently curated, sealed 64-query
holdout for `task_context/2-compact/17`. It is bound to release candidate
`26a36f0958cb16d925c4dd871cda76d0e117b15a` (tree
`6ab1d7075c787b7a8884114d5d732be812cb325f`) and Cobra
`a0a6ae020bb3899ff0276067863e50523f897370`.

The frozen contracts are `sw266-measurement-contract/2` and
`sw280-qrel-blind-smoke-evaluation/2`. Version 1 applies where version 2 does
not restate it. Candidate response budget is 1,200 real cl100k tokens. The
single designated follow-up read, if present, is independently preserved and
capped at 120 lines. It is not part of the 1,200-token response ceiling.

The decision population is all 64 holdout questions and all 64 unique
families. The immutable threshold is `k=56/64`, derived by the implementation's
two-sided exact 95% Clopper-Pearson lower-bound rule at the 3/4 floor. No query,
miss, refusal or malformed response may be removed from N. No retry, overwrite,
threshold adjustment, or best-available result is allowed.

The predeclared balance is:

| Stratum | N |
|---|---:|
| `exact_identifier` | 11 |
| `exact_path` | 11 |
| `nl_behaviour` | 11 |
| `architecture_flow` | 11 |
| `config_docs` | 10 |
| `ambiguous` | 10 |

Each query has one independently reviewed exact grade-3 answer span. All spans
and anchors resolve in the clean pinned Cobra checkout; all fit below the
1,200-token answer-span ceiling. Dataset construction used only the clean
pinned corpus and public method/schema inputs. It did not inspect candidate
outputs, development examples, consumed holdouts, captures, answers, grades,
or adjudications.

The dataset must remain unavailable to the candidate operators and all raters.
Capture of all 64 candidate bundles must complete and be committed before any
rater executes. Capture, rating, grading, adjudication and decision are outside
the curator's role and were not run while creating this directory.

The evidence is designed to detect error, accident and drift. It is not
designed to detect deliberate falsification by someone with write access to
this repository and should not be read as establishing that none occurred.
