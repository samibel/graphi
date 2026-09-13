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

## 2026-09-13 PRE-FREEZE OPERATOR AMENDMENT

Before freeze, capture, or any participant execution, the operator fixes four
stable logical role/configuration identities in `participants.json`. The two
primaries use concrete non-alias OpenAI Codex model IDs `gpt-6-astra` and
`gpt-5.6-sol`; the grader uses `gpt-6-astra`; the adjudicator uses
`gpt-5.6-sol`. Every item uses `codex-cli 0.153.4`, reasoning effort `high`, and
a new stateless ephemeral read-only process. The provider exposes no immutable
backend snapshot/build digest, so model-level behavior is attributable but not
byte-reproducible; no missing identity is invented.

Before any participant runs, two independent fresh index builds must produce
64/64 identical MCP response bytes, response digests, and cl100k token counts,
with every response at or below 1,200 tokens and actor-visible version
`task_context/2-compact/5`. This operator amendment changes no dataset, `N`,
`k`, bundle construction, rubric, or scoring rule.
