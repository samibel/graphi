# Content-free operator handoff

This handoff applies only to the committed run directory containing this file.
Do not open or print `sealed-dataset.json`. Resolve all paths explicitly; do
not use globs for sealed or write-once artifacts.

Required identities:

- product source commit: `26a36f0958cb16d925c4dd871cda76d0e117b15a`
- product source tree: `6ab1d7075c787b7a8884114d5d732be812cb325f`
- Cobra commit: `a0a6ae020bb3899ff0276067863e50523f897370`
- contract: `2`
- candidate method: `task_context/2-compact/17`
- candidate budget: `1200`
- decision rule: `k=56/64`

Use explicit operator-local variables whose resolved values are recorded in
the operator log:

```sh
RUN=<absolute path to this run directory>
DATASET=docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v2/sealed-dataset.json
COBRA=<absolute path to the clean pinned Cobra checkout>
EMBEDDER=<the preregistered non-empty production selector>
TOKENIZER_DIR=<absolute path to the pinned tokenizer directory>
```

## Universal preflight before every mutating invocation

Run all of these read-only checks immediately before the invocation. A failure
stops the run. Do not fix it by retrying the same holdout, overwriting an
artifact, changing an argument, or substituting a query.

```sh
go run ./cmd/retrieval-eval -h >/dev/null
test -n "$RUN" && test -d "$RUN"
test -n "$DATASET" && test -f "$DATASET"
test -n "$COBRA" && test -d "$COBRA"
test "$(git -C "$COBRA" rev-parse HEAD)" = a0a6ae020bb3899ff0276067863e50523f897370
test -z "$(git -C "$COBRA" status --porcelain --untracked-files=normal)"
test -z "$(git status --porcelain --untracked-files=normal)"
test "$(git show -s --format=%T 26a36f0958cb16d925c4dd871cda76d0e117b15a)" = 6ab1d7075c787b7a8884114d5d732be812cb325f
test "$(shasum -a 256 "$DATASET" | awk '{print $1}')" = fb7737442cd251c30f6800d557e3b89c600dd0a66374daf53837aa0144d8936c
```

The clean-worktree check occurs after the preceding phase has been committed.
Before capture, also verify that every candidate-tree difference from the
named product source commit is confined to this run directory.

## Phase 1: freeze

Read-only argument/state validation:

```sh
test ! -e "$RUN/precondition-record.json"
test -f "$RUN/grading-rubric.md"
test -f docs/eval/retrieval/methodology-v2.md
```

Single mutating invocation:

```sh
CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval -blind-eval freeze -blind-eval-contract 2 \
  -blind-eval-dir "$RUN" -dataset "$DATASET"
```

Validate the produced record without exposing the dataset, then commit it.
Do not proceed until that commit is clean and independently recorded.

## Phase 2: capture

This phase must complete and be committed before any primary, grader, or
adjudicator process is started.

Read-only argument/state validation:

```sh
test -n "$EMBEDDER"
test -f "$RUN/precondition-record.json"
test -f "$RUN/participants.json"
test ! -e "$RUN/pre-registration.json"
test ! -e "$RUN/capture-provenance.json"
test ! -e "$RUN/prompts"
test ! -e "$RUN/bundles"
```

Single mutating invocation:

```sh
CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval -blind-eval capture -blind-eval-contract 2 \
  -blind-eval-dir "$RUN" -repo cobra -checkout "$COBRA" \
  -embedder "$EMBEDDER"
```

Verify N=64 and k=56 from the generated pre-registration without printing any
per-query entry. Commit the pre-registration, provenance, prompts, and bundles
as one capture commit. Confirm the worktree is clean. Only then may rating
start. There is no capture retry on this dataset.

## Phase 3: seal responses, packets, grades, and adjudications

Before **every** seal call, rerun the universal preflight and verify the exact
expected raw-input slots for that stage. Never overwrite a raw or sealed slot.
The pinned checkout argument is mandatory on every seal invocation:

```sh
CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval -blind-eval seal -blind-eval-contract 2 \
  -blind-eval-dir "$RUN" -checkout "$COBRA"
```

Commit each completed write-once stage before exposing its outputs to the next
role. A missing, malformed, unexpected, or conflicting artifact stops the run;
do not retry or overwrite it.

## Phase 4: decide

Only after all response, grade, adjudication, sidecar, and execution-log
requirements are committed, rerun the universal preflight and perform
read-only presence/digest validation. Confirm that decision outputs do not yet
exist except for the curator's public pre-capture `README.md`, which the
existing harness intentionally replaces with its final generated report.

```sh
test ! -e "$RUN/hash-comparison.json"
test ! -e "$RUN/outcome.json"
CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval -blind-eval decide -blind-eval-contract 2 \
  -blind-eval-dir "$RUN"
```

Commit the generated comparison, outcome, final report, and required evidence
manifest. Publish no `RELEASE:YES` unless every fail-closed gate passes and the
threat-model disclosure accompanies the claim.
