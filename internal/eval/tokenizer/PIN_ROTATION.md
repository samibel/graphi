# Real-tokenizer pin rotation governance

Current governed tokenizer: `tiktoken:cl100k_base:ordinary`.
Current governed file: `cl100k_base.tiktoken`.
Current governed source: `https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken`.
Current governed vocabulary SHA-256: `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`.

This is the review record beside `PinnedVocabularySHA256` in `pins.go`. The
`TestTokenizer_PinRotationGovernance` gate requires the current digest above to
match the Go pin. Changing only the pin therefore fails the ordinary Go test
gate.

## Approval

A repository maintainer responsible for the SW-266 token-savings evaluation
approves a rotation in the pull request after reviewing its evidence. The
appended rotation entry must name that approver and pull request; merely saying
that an upstream vocabulary is newer is not approval evidence.

## Required rotation record and re-measurement

Before a tokenizer ID, counting policy, pre-tokenizer, or vocabulary digest may
replace the current pin, append a dated entry containing all of the following:

1. The old and new `tokenizer_id`, source URL, file name, exact SHA-256, upstream
   reason for rotating, approving maintainer, and pull request.
2. CGo-free conformance against the upstream tokenizer implementation for the
   complete golden token vectors: token IDs as well as counts, including the
   identifier, punctuation-dense code, long-path, and non-ASCII UTF-8 cases.
3. A fresh corruption, truncation, and missing-artifact run showing the loader
   fails closed and names the expected and actual SHA-256 values.
4. Regenerated raw payload token counts for every invalidated token-savings run
   directory. Counts, aggregates, confidence interval, and any claim sentence
   derived from an older tokenizer are stale; they may not be relabeled.
5. Review of the ordinary-text special-token policy and the frozen cl100k
   pre-tokenizer. A code-only behavior change invalidates measurements just as
   a vocabulary-file change does and requires a new `tokenizer_id`.

## Records made stale by the next rotation

No token-savings run directories exist at adoption time (SW-277).

SW-280 commits the first tokenizer-dependent run directory:

- `docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/` — the qrel-blind smoke evaluation.
  Its preserved `task_context/2` bundles carry real-tokenizer counts under the current pin, so a
  rotation must list this directory as stale and regenerate those counts before it can pass.
- `docs/eval/retrieval/runs/2026-09-06-recovery-dev/` — development-only MCP
  captures from independent indexes. The preserved payload token counts and
  their reproducibility comparison must be recomputed after a rotation.
- `docs/eval/retrieval/runs/2026-09-06-architecture-dev/` — development-only
  MCP captures for the architecture-ranking experiment.
- `docs/eval/retrieval/runs/2026-09-06-qwen-dev/` — opt-in alternative-model
  development experiment; any complete MCP payload counts use the pinned
  tokenizer. Failed attempts are not complete quality measurements.
- `docs/eval/retrieval/runs/2026-09-06-bundle-selection-dev/` — development-only
  exact MCP source-selection ablations and final independent-build captures.
- `docs/eval/retrieval/runs/2026-09-06-candidate-admission-dev/` — development-only
  candidate-admission and grouped-declaration captures; all complete MCP
  payload counts must be recomputed after a tokenizer rotation.
- `docs/eval/retrieval/runs/2026-09-06-release-preflight-dev/` — development-only
  frozen GrepRead comparator transcripts and exact per-response token counts;
  the counts must be recomputed after a tokenizer rotation. This diagnostic
  blocks a savings claim; it publishes no savings aggregate.
- `docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/` — development-only
  answer-source recovery capture; all complete MCP payloads are preserved and
  verified across two independent index builds.
- `docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/` — development-only
  comparator prototype; its exact response-prefix and transcript counts must
  be recomputed after a tokenizer rotation.
- `docs/eval/retrieval/runs/2026-09-07-compact-wire-dev/` — development-only
  compact task-context frontier; every paired row uses the pinned tokenizer.
- `docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency/` and
  `docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency-v2/` — registered
  compact development sufficiency runs whose preserved responses carry pinned
  real-token counts.
- `docs/eval/retrieval/runs/2026-09-07-compact-wire-v2-dev/` — second compact
  development frontier with paired real-token measurements.
- `docs/eval/retrieval/runs/2026-09-12-compact-dev-sufficiency-v3/` and
  `docs/eval/retrieval/runs/2026-09-12-compact-dev-sufficiency-v4/` — registered
  blind development runs over tokenizer-counted compact responses.
- `docs/eval/retrieval/runs/2026-09-12-compact-wire-v3-dev/` and
  `docs/eval/retrieval/runs/2026-09-12-compact-wire-v4-dev/` — compact frontier
  measurements under the current tokenizer pin.
- `docs/eval/retrieval/runs/2026-09-13-compact-wire-v5-dev/` and
  `docs/eval/retrieval/runs/2026-09-13-compact-wire-v5-blind-dev/` — v5 frontier
  and registered blind diagnostic.
- `docs/eval/retrieval/runs/2026-09-13-compact-wire-v6-dev/` and
  `docs/eval/retrieval/runs/2026-09-13-compact-wire-v6-blind-dev/` — v6 frontier
  and registered blind diagnostic.
- `docs/eval/retrieval/runs/2026-09-13-compact-wire-v7-dev/` and
  `docs/eval/retrieval/runs/2026-09-13-compact-wire-v7-blind-dev/` — v7 compact
  frontier and its registered blind diagnostic, including a pinned-tokenizer
  ceiling over every serialized bundle.
- `docs/eval/retrieval/runs/2026-09-13-compact-wire-v8-dev/` and
  `docs/eval/retrieval/runs/2026-09-13-compact-wire-v8-blind-dev/` — v8 compact
  frontier and registered blind diagnostic with declaration outlines,
  field-flow closure and evidence-derived summary roadmaps under the same
  pinned-tokenizer ceiling.
- `docs/eval/retrieval/runs/2026-09-13-compact-wire-v9-dev/` and
  `docs/eval/retrieval/runs/2026-09-13-compact-wire-v9-blind-dev/` — v9 compact
  frontier and its registered blind development diagnostic with exact-path
  breadth and source-call-chain role closure under the same pinned-tokenizer
  ceiling.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-dev/` — two independent
  production MCP captures of the promoted compact task-context candidate; all
  payload counts and the byte-reproducibility result depend on the current pin.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v2-dev/` — the reserved
  candidate-bound development recapture after the capture validator began
  enforcing the exact 1,200-token wire ceiling with this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v3-dev/` — the final-audit
  candidate-bound recapture; its serialized wire ceiling uses this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v4-dev/` — the sealed-source
  snapshot candidate recapture; its byte identity and exact serialized wire
  ceiling use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-dev/` — the
  public-version-bound candidate recapture; its byte identity and exact
  serialized wire ceiling use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v6-dev/` — the
  presealed-rubric successor recapture; its byte identity and exact serialized
  wire ceiling use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/` — the exact
  fresh-holdout capture-candidate development recapture; its byte identity and
  exact serialized wire ceiling use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-candidate-path-dev/` — the candidate-bound
  exact-basename retention development recapture; its 44/44 byte identity and
  exact serialized wire ceiling use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-14-holdout-answer-span-ceiling/` — the
  aggregate-only answer-span ceilings of the two sealed holdouts; every span
  cost and both feasibility counts use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-14-named-declaration-dev/` — the
  candidate-bound named-declaration completion development recapture; its 44/44
  byte identity, its exact serialized wire ceiling, and the answer-span
  feasibility ceiling recorded in its result all use this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-fresh-sealed-holdout/`
  — the completed fresh sealed-holdout capture and its 64 exact response-byte
  counts are bound to this tokenizer.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-second-fresh-sealed-holdout/`
  — the completed second fresh sealed-holdout capture and its exact
  response-byte counts are bound to this tokenizer.

The governance test scans every directory immediately below
`docs/eval/retrieval/runs/` for the current `tokenizer_id`. Once a run contains
real-token counts, its exact directory must be added here in backticks before
the gate will pass. This makes the inventory grow with evidence rather than
letting a later pin silently inherit old counts.

## Current-pin adoption record

SW-277 adopts OpenAI's canonical `cl100k_base.tiktoken` vocabulary at SHA-256
`223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`.
The implementation is a standard-library-only Go pre-tokenizer and byte-pair
encoder. It interprets payloads as ordinary text, not as a channel that may
inject tokenizer special tokens. This adoption publishes no measurement and
invalidates no prior run because no SW-266 token-savings run exists yet.
