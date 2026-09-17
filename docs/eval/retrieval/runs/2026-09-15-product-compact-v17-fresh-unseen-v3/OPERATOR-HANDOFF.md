# Content-free operator handoff

This run is curated only. The operator must not open or print
`sealed-dataset.json`. Use the absolute run directory and the exact clean Cobra
checkout. Never improvise a missing parameter, retry a response/grade, or
overwrite a sealed artifact.

## Immutable inputs

```text
RUN=<absolute path to this run directory>
COBRA=<absolute path to the clean pinned Cobra checkout>
CANDIDATE=26a36f0958cb16d925c4dd871cda76d0e117b15a
CANDIDATE_TREE=6ab1d7075c787b7a8884114d5d732be812cb325f
CORPUS=a0a6ae020bb3899ff0276067863e50523f897370
CONTRACT=2
N=64
K=56
BUDGET=1200
FOLLOWUP_MAX_LINES=120
```

Before **every mutating invocation**, first run `go run
./cmd/retrieval-eval -h >/dev/null` and perform an equivalent read-only argument
and state validation. Confirm all required arguments, exact SHAs, clean
worktrees, absence of the phase's output path, and correct current phase. Do
not use a failed mutation as argument discovery. A failed or partial mutation
is a refusal requiring a new independent run, not a retry or overwrite.

## Phase 0: checkout and seal preconditions

- Check out the committed curation tip in a clean isolated worktree.
- Verify the committed curator pre-registration and dataset SHA-256 values.
- Verify `git -C "$COBRA" rev-parse HEAD` equals `$CORPUS` and
  `git -C "$COBRA" status --porcelain` is empty.
- Verify the product binding resolves exactly to `$CANDIDATE` and
  `$CANDIDATE_TREE`; do not substitute the curation commit for this declared
  release-candidate identity.
- Verify no capture, response, packet, grade, adjudication, decision, or raw
  phase directory exists.

## Phase 1: freeze

Read-only validate the full command first, including `-blind-eval-contract 2`,
`-blind-eval-dir`, and `-dataset`. Then invoke exactly one freeze. Commit the
new precondition record before proceeding. Verify it freezes 1,200 tokens,
Contract 2, 120 follow-up lines, the dataset digest, and the clean curation-tip
commit whose curator record binds the exact release candidate above.

## Phase 2: capture

Read-only validate `-blind-eval capture` with all mandatory parameters:
`-blind-eval-contract 2`, `-blind-eval-dir "$RUN"`, `-repo cobra`,
`-checkout "$COBRA"`, and the exact production `-embedder`. Recheck the clean
pinned checkout immediately before the call. Capture exactly once. Validate
64/64 bundles, their digest/tokenizer bindings, and any designated follow-up
payloads. Commit the complete capture and generated harness pre-registration
before any rater is started.

## Phase 3: primary rating

Only after the capture commit exists, dispatch both pre-registered primary
rater slots. Raters receive only their permitted candidate bundle and question
prompt, never qrels, answer spans, other responses, or repository access.
Persist raw outputs write-once.

## Phase 4: seal responses and prepare grading

Read-only validate the entire seal invocation first. The mandatory checklist
includes `-blind-eval seal`, `-blind-eval-contract 2`,
`-blind-eval-dir "$RUN"`, and **`-checkout "$COBRA"`**. Reverify the checkout
pin and cleanliness, then seal exactly once. No retry and no overwrite.

## Phase 5: grade, adjudicate, decide

The grader uses only generated grader packets and the frozen rubric. Seal raw
grades once with the same preflight discipline. Dispatch adjudication only for
valid disagreements and seal it once. Before decide, verify the full digest
chain, sidecar manifest, participant/model identities, N=64, k=56, corpus and
candidate bindings, and clean committed state. A missing or invalid member is
`RELEASE: NO`; it is never dropped from N.

The curator performed none of Phases 1 through 5.
