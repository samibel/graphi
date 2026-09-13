# Second fresh sealed qrel-blind holdout

Status: **curated and sealed before capture** for product candidate
`1430daf27e795c8c3a7dcc230d858935044bc33f`.

This directory currently contains only the independently curated sealed
dataset and public, non-content-bearing pre-registration metadata. It contains
no capture, prompt, bundle, response, grade, adjudication, or decision.

The run reuses the frozen public qrel-blind methodology, strata, statistical
rule, and grading rubric of the valid first fresh holdout. The complete
answerable population is fixed at `N=64`; the exact two-sided 95% Clopper–Pearson
lower-bound rule with floor `3/4` therefore fixes the pass threshold at `k=56`.

The later operator must bind freeze and capture to the candidate commit above.
Capture must be completed, content-addressed, and committed before any prompt
is delivered to a primary or any rater process is started. Any access to a
rater before the capture commit invalidates this run.

The dataset is confidential. Public reporting may disclose only hashes,
aggregate population/stratum/family counts, validation status, and eventual
aggregate outcomes allowed by the governing threat model.
