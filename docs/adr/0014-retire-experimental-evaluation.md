# ADR 0014 — Retire the experimental model-evaluation track from `main`

- **Status:** Accepted
- **Date:** 2026-09-30
- **Scope:** Current `main` tree only; Git history is not rewritten
- **Supersedes:** the CodeRank development-qualification and qrel-blind
  release-recovery plans under `docs/superpowers/`

## Context

GrapHi's release line is blocked by a model-quality experiment that is not a
property of the released product. The current qrel-blind result is 47 passing
queries out of 64 against a preregistered threshold of 56. A draft estimate
mentioned 59/64, but that is not the current gate result. The attempted
CodeRank candidate also failed to improve on the shipped Potion profile: its
captured bundle contained the complete answer span for 51/64 queries versus
60/64 for Potion/512.

CodeRank is development-only. The latest published release is v0.12.1, and no
published tag exposes a CodeRank selector or depends on the CodeRank package.
Removing it therefore does not remove a released user capability.

The experiment has also made the repository disproportionately large. At this
decision's start, tracked documentation consists of 13,696 files and about
812 MB, while source-like files total 1,628. Two generated evidence trees
account for almost all of that size:

| tree | tracked files | tracked bytes |
|---|---:|---:|
| `docs/eval/runs/` | 1,424 | 594,595,293 |
| `docs/eval/retrieval/runs/` | 11,969 | 199,430,680 |

The repository currently treats raw run outputs as both documentation and test
fixtures. Tests, development commands, release policy, and prose therefore
depend directly on large historical artifacts. Deleting files alone would
break CI without removing the coupling that caused the growth.

GrapHi's architectural goal is fast product development. An optional,
unreleased model experiment must not block unrelated product releases or make
the main checkout primarily an evidence archive.

## Decision

### 1. Retire the entire experimental CodeRank and blind-evaluation track

Remove the development-only CodeRank adapter, embedded-model qualification
command, blind reader/grader orchestration, qrel-blind smoke gate, associated
scripts, and tests whose only purpose is to validate those experiments.

This includes the implementation under `engine/embed/coderank`,
`cmd/embedded-model-qualification`, the model-qualification and blind-eval
implementation in `internal/eval/retrieval`, and the corresponding command
surface in `cmd/retrieval-eval`.

Remove every remaining import, allowlist entry, selector, manifest field,
constant, and user-facing reference for CodeRank. The retirement must not leave
a silent fallback, accepted-but-ignored configuration, or dead protocol type.

### 2. Preserve shipped embedding and retrieval behavior

Potion, Ollama, the unconfigured lexical path, and GrapHi's normal semantic
search remain supported. Their public CLI, MCP, HTTP, and daemon behavior must
not change as a side effect of this retirement.

The default binary remains CGo-free and performs no automatic non-loopback
model access. Existing privacy, reproducibility, binary-size, and benchmark
gates remain blocking.

### 3. Remove model qualification from the general release decision

Remove `retrieval-targets` from `cmd/release-gate`'s required and default gates.
The general release decision must no longer depend on qrel-blind outcomes,
model promotion thresholds, committed development reports, or a particular
experimental embedding model.

The following release gates remain blocking:

- `testgate`;
- coverage;
- privacy;
- environment-independent benchmark budgets; and
- the existing static/reproducible release build workflows.

Retrieval behavior continues to be protected by deterministic product tests
that run through `testgate`. These tests check behavior GrapHi controls, not a
probabilistic quality threshold for an optional model.

If a future model is investigated, it begins outside the release interface.
It may enter `main` only through a new, separately reviewed decision after it
has already demonstrated value. An experiment is not allowed to make the
ordinary release line red merely to force continuation of the experiment.

### 4. Stop storing generated evaluation runs in Git

Delete the tracked contents of:

- `docs/eval/runs/`;
- `docs/eval/retrieval/runs/`;
- `docs/eval/retrieval/harvests/`; and
- `docs/eval/retrieval/drafts/`.

Delete completed Superpowers specifications and plans under
`docs/superpowers/`, including the superseded embedded-model recovery and
CodeRank integration designs.

Delete obsolete CodeRank, blind-evaluation, and model-qualification prose from
the remaining documentation. Update current user and contributor documents so
they describe only retained product behavior.

Add ignore rules for generated evaluation-run directories. Workflows may
produce full reports, databases, bundles, grades, and traces in temporary
directories and may upload them as CI artifacts. They must not require those
outputs to be committed to `main`.

The Git history is deliberately not rewritten. Historical commits and forks
remain valid, and the removed evidence can still be inspected at the commits
that introduced it. The current checkout becomes small; existing clones do not
receive a destructive history migration.

### 5. Separate product fixtures from documentation

A test that protects retained product behavior must not read a historical run
directory. Replace such dependencies with the smallest deterministic fixture
that exercises the product seam, stored beside the owning package under
`testdata/`.

Do not copy complete historical datasets or reports into `testdata`. A fixture
is retained only when all of the following are true:

1. it exercises currently shipped behavior;
2. a test reads it through the product interface;
3. it is minimal enough to review; and
4. its expected result is deterministic without an external model or network.

Tests that only prove historical evidence immutability, grading protocol
integrity, candidate binding, run sealing, preregistration, or model promotion
are deleted with the retired experiment.

### 6. Keep a small documentation surface

Retain documentation that a user or current maintainer needs:

- the root README, feature and CLI references;
- tutorials and installation documentation;
- current CI and release instructions;
- ADRs that describe the shipped architecture;
- small machine-readable configuration files used by active gates; and
- concise contributor guidance for producing CI artifacts.

An artifact is not documentation merely because it is stored below `docs/`.
Raw databases, request/response captures, generated bundles, grader packets,
and repeated run reports are build artifacts.

## Module and seam consequences

The product retrieval module keeps its existing public interface. The
experiment had introduced a second interface made of manifest paths, sealed
run directories, preregistration records, model identities, and release
thresholds. That interface offered no leverage to product callers and spread
knowledge across `engine`, `internal`, `cmd`, workflows, tests, and
documentation.

Retiring it restores locality:

- embedding implementations own shipped embedding behavior;
- retrieval tests own small product fixtures;
- the release gate owns deterministic release policy; and
- CI artifact storage owns generated measurements.

No adapter remains for a model that the product does not offer.

## Error handling and migration behavior

Because CodeRank was never released, there is no compatibility shim. A stale
development invocation fails because the command or selector no longer exists;
it must not be accepted and redirected to Potion.

Missing historical run files are not treated as runtime errors. Retained
commands and tests must no longer reference them. A CI workflow that generates
an evaluation artifact must fail within that workflow if generation fails, but
absence of a historical artifact in the Git tree is the desired state.

## Verification

Implementation is complete only when all of the following hold:

1. `go build ./...` passes.
2. `go test ./...` passes.
3. Web tests pass.
4. Release-gate tests pass and prove the remaining required-gate set.
5. The reproducible static release build passes.
6. The CodeRank package, qualification command, blind-eval implementation,
   qrel-blind gate, and their imports are absent.
7. No retained code or workflow references a removed run path.
8. No tracked file exists below either generated run directory.
9. Documentation links among retained documents are valid.
10. Public CLI, MCP, and HTTP golden/contract tests remain green.
11. The final change records tracked documentation file count and byte size,
    demonstrating the reduction from the baseline above.

## Implementation status (2026-09-30)

The cleanup reduced tracked documentation from 13,696 files / 811,745,116 bytes
to 159 files / 6,185,621 bytes at the Task 7 checkpoint. That is 13,537 fewer
tracked documentation files and 805,559,495 fewer bytes in the maintained tree.
The one remaining `docs/superpowers` file at this checkpoint is this execution's
temporary implementation plan; it is removed after final verification.

## Consequences

### Positive

- Unrelated GrapHi releases are no longer held hostage by an optional model
  experiment.
- Existing open-source users keep the embedding and retrieval behavior present
  in published releases.
- The current checkout loses roughly 800 MB and more than 13,000 generated
  evidence files.
- Tests become smaller, faster to navigate, and owned by the product modules
  whose behavior they protect.
- Future experiments must earn promotion before they can enlarge the product
  and release interfaces.

### Negative

- Reproducing the retired experiments requires checking out an historical
  commit.
- Full historical evidence is no longer visible in the current tree.
- Some product tests must be rewritten around minimal fixtures before the
  generated directories can be deleted safely.

These costs are accepted. Git history is the archive; `main` is the maintained
product.

## Non-goals

- Rewriting Git history or reducing existing clone object databases.
- Changing Potion, Ollama, lexical retrieval, or semantic-search defaults.
- Lowering the 56/64 threshold and presenting the failed experiment as a pass.
- Designing a replacement embedding model.
- Removing active privacy, build, coverage, or performance gates.
- Treating CI artifacts as permanent release claims.

## Rollback

The retirement can be reverted with an ordinary Git revert because history is
unchanged. Reintroduction must restore the complete retired module and its
tests; selectively restoring only the release block or only the raw evidence
is not a valid rollback.
