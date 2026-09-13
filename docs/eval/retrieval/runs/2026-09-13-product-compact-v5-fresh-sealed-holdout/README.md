# Product compact V5 fresh sealed holdout

Status: **SCAFFOLD ONLY — HOLDOUT NOT CURATED, FROZEN, OPENED OR RUN**.

This directory reserves one future, independently operated qrel-blind release
evaluation. It contains public procedure files only. It contains no dataset,
question, judgement, prompt, captured bundle, response, grade or adjudication.

No candidate SHA is asserted here. The final product candidate, its bound V5
development evidence and the final freeze-base commit must be supplied and
verified before this scaffold can be frozen.

The production embedder is preregistered as
`static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
Changing that identity creates a different run.

## Current blocker

The run is not eligible to start because an independent curator has not yet
delivered a newly curated, stratified and sealed dataset. The curator must be
identified before curation, must not have implemented the candidate, and must
not use development captures, prior holdout questions, prior holdout answers or
prior holdout results. The curator may use only the pinned corpus, the public
methodology and the preregistered stratification rules.

The candidate team and root orchestration agent must not read the dataset,
questions, judgements or answer keys. The independent operator may pass the
sealed dataset path to the fail-closed harness after the public inputs and
candidate are frozen; that does not authorize manual inspection or disclosure
of its contents.

## Preconditions for `freeze`

- A final product candidate is committed, clean and unchanged by this run.
- Candidate-bound V5 development evidence is committed and independently
  verifies the real MCP `task_context/2` path, actor-visible compact version,
  exact 1,200-cl100k ceiling and byte reproducibility.
- The independent curator and operator attestations are complete.
- `participants.json` contains resolved, distinct participant identities; its
  deliberately empty template values have been replaced.
- The fresh dataset exists at its final repository-relative path, is sealed by
  content digest, and was transferred without disclosure to the candidate team.
- The pinned corpus checkout, embedder and tokenizer identities are resolved.
- The worktree is clean and all public frozen inputs are committed.

## Write-once order

1. Commit this completed public scaffold and the curator-delivered sealed
   dataset without opening its contents in the candidate-team context.
2. The independent operator runs `-blind-eval freeze`. That invocation is the
   first authorized harness access to the sealed dataset.
3. Commit `precondition-record.json` by itself.
4. The independent operator runs `-blind-eval capture` and commits
   `pre-registration.json`, `capture-provenance.json`, `prompts/` and `bundles/`
   before any rater receives a prompt.
5. Each primary receives only one prompt at a time. Raw responses are written
   once and sealed before grading.
6. The grader receives only the run-local frozen rubric and the generated
   per-response grader packet. Grades are written once and sealed.
7. If primaries disagree, the adjudicator answers from the original question
   and exact bundle before seeing either primary response or grade. The answer
   is sealed before the disagreement is disclosed.
8. Commit every sealed record and sidecar, run `-blind-eval decide`, and commit
   the resulting comparison, outcome and report. There is no retry or waiver.

See [METHOD.md](METHOD.md), [grading-rubric.md](grading-rubric.md) and the
repository's public retrieval methodology and threat model. Any published
claim must retain the threat-model disclosure about deliberate falsification
by a repository writer.
