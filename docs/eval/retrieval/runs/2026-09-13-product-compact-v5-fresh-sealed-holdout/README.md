# Product compact V6 fresh sealed holdout

Status: **CURATED AND SEALED — NOT FROZEN, OPENED OR RUN**.

This directory reserves one future, independently operated qrel-blind release
evaluation. It now contains the curator-delivered opaque sealed dataset and
public attestation, but the candidate team and root orchestration agent have not
opened its content. No prompt, captured bundle, response, grade or adjudication
exists yet.

The frozen product candidate is
`d8d6a2c1d8da2de0bd90d94350a98e32a138e26c`. Its externally committed,
candidate-bound V6 development evidence is
`37bdb43efee6c73a5739f58a609f6605aa1fc8ef`, with capture SHA-256
`5abdcabdca489cd3431f8b9db4b126ea6c937671de87f3a17e3b40c63f803acb`.
That evidence commit is referenced, not imported into this branch. The final
freeze-base commit remains to be recorded after the public pre-freeze inputs
are committed. The reserved directory name retains `v5` for path stability; it
does not identify the product candidate or development evidence.

## 2026-09-13 PRE-FREEZE AMENDMENT — participant execution identity

This amendment was made before `freeze`, `capture` or any participant response.
The execution service exposes a concrete OpenAI model ID (`gpt-6-astra` or
`gpt-5.6-sol`) but no immutable backend snapshot or build digest. These IDs are
not aliases such as `latest`. Each participant identity is therefore the stable
logical role/configuration ID in `participants.json`, bound to its concrete
model ID, `codex-cli 0.153.4`, reasoning effort `high`, and the stateless
per-item execution controls in [METHOD.md](METHOD.md).

Model behavior is not byte-reproducible at the model layer because the provider
does not expose a backend snapshot/build digest. This limitation is recorded
rather than replaced with an invented identity. Four earlier session UUIDs are
evidence that the participants acknowledged their roles before dataset
completion; they are retained only in `operator-attestation.json` and will not
be resumed for evaluation.

This identity amendment changes no population size `N`, threshold `k`, dataset,
bundle, grading rubric or scoring rule.

The production embedder is preregistered as
`static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
Changing that identity creates a different run.

## Curator handoff

The independent curator delivered one newly curated, stratified and sealed
dataset at the reserved run-relative path. Its SHA-256 is
`9f2289c71bbc8515bd58b0210ddcf528eb5be427896918b3e5a3d390ad10d5aa`;
the public curator-attestation SHA-256 is
`7925c77e11e4ddb2c825209ed0795c36b7aa4ce9df8ba4ff36ab0150c731fe66`.
The operator treats the dataset as opaque until the fail-closed harness opens
it during `freeze`.

The candidate team and root orchestration agent must not read the dataset,
questions, judgements or answer keys. The independent operator may pass the
sealed dataset path to the fail-closed harness after the public inputs and
candidate are frozen; that does not authorize manual inspection or disclosure
of its contents.

## Preconditions for `freeze`

- A final product candidate is committed, clean and unchanged by this run.
- Candidate-bound V6 development evidence is committed and independently
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
5. Each primary receives only one prompt in a fresh stateless Codex process.
   Raw responses and confidential JSONL execution logs are written once. Any
   non-`agent_message` item event is a refusal without retry. Responses are
   sealed before grading; execution logs are hashed and committed but never
   disclosed to the root orchestration agent.
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
