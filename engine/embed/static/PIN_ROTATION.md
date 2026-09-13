# Static model pin rotation governance

Current governed revision: `e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.

This file is the review record beside `PinnedRevision` in `pins.go`. The
`TestStatic_PinRotationGovernance` gate requires the current revision above to
match the Go pin and requires every recorded production-static retrieval run to
be named below. Changing only `PinnedRevision` therefore fails the ordinary Go
test gate.

## Approval

A repository maintainer responsible for semantic-search evaluation approves a
rotation in the pull request after reviewing its evidence. The appended
rotation entry must name that approver and the pull request; a pin author saying
only that the upstream revision is newer is not approval evidence.

## Required rotation record and re-measurement

Before a new revision may replace the current one, append a dated entry to this
file containing all of the following:

1. The old and new model selectors, the upstream reason for rotating, the exact
   SHA-256 of `config.json`, `tokenizer.json`, `model.safetensors`, and
   `modules.json`, and the approving maintainer and pull request.
2. A CGo-free execution of the production static embedder over the checked-in
   cross-architecture inputs on two `GOARCH` values using one verified artifact
   handoff. Record both commands, environments, output SHA-256 values, and the
   byte-exact comparison. If any vector component differs, stop the rotation
   and record the input, component, and two IEEE-754 bit patterns; do not round,
   sort, or add a tolerance.
3. Regenerated Model2Vec oracle/conformance evidence under
   `engine/embed/static/testdata/oracle/`, including exact token IDs, embedding
   rows, and produced-vector checks against the new pinned bytes.
4. A fresh full static retrieval evaluation equivalent to
   `docs/eval/retrieval/runs/2026-09-01-static-local/`, with the new selector,
   model fingerprint, repository SHA, dataset SHA, raw rankings, and aggregate
   round-trip recorded.
5. A fresh SW-264 `task_context/2` AC-9 measurement equivalent to
   `docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local/`.
   Record the grade-3 coverage and full provenance before a release gate
   calibrates a threshold from it. Any threshold or baseline derived from an
   invalidated run must be recalibrated from the replacement.
6. A fresh derivation of `docs/eval/retrieval-targets.json`. This instruction
   used to read "`docs/eval/retrieval-targets.json` stays untouched until
   SW-266"; SW-266 is spent and SW-282 rewrote the file. Its bars are now
   DERIVED from this pinned embedder's `semantic_name_only` numbers on the
   development split (see the SW-282 runs enumerated below), so a rotation
   invalidates them: re-measure the comparator-only development slice, re-derive
   the file, re-run the gate, and record the new per-target verdict in
   `docs/eval/retrieval/targets-gate-expectations.json`. Re-measuring without
   re-deriving leaves a bar the rotated model was never compared against.

## Records made stale by the next rotation

- `docs/eval/retrieval/runs/2026-09-06-qwen-dev/` — opt-in alternative-model
  experiment; its Potion reference captures depend on the current static pin.
  Qwen attempts are separately labeled and do not rotate this pin or its gates.

The following checked-in artifacts were produced by the current pinned model
and become stale when `PinnedRevision` changes:

- `docs/eval/retrieval/runs/2026-09-01-static-local/` — the production-static
  seven-baseline retrieval evaluation and AC-9 comparison.
- `docs/eval/retrieval/runs/2026-09-02-capsule-local/` — the first v3-capsule
  retrieval run. Its report names candidate `313db48…+dirty` (the production
  static AC-9 commit), and its static semantic raw series is carried unchanged
  by the two SW-263 runs below.
- `docs/eval/retrieval/runs/2026-09-02-sw263-local/` — the v3 shared-document
  source evaluation over the current static semantic series.
- `docs/eval/retrieval/runs/2026-09-02-sw263-v3-restoration-local/` — the
  semantic-first restoration evaluation over that same static semantic series.
- `docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local/` —
  SW-264's 6/6 grade-3 `task_context/2` measurement that SW-266 uses as
  threshold-calibration evidence.
- `docs/eval/retrieval/runs/2026-09-03-sw272-field-parity/` — SW-272's exact
  grade-3 2x3 operator control. Its `semantic_name_only` and `semantic_first`
  cells are produced by this pinned embedder, so a rotation invalidates the
  semantic half of the comparison and with it the +0.2159 field-parity gap.
  Added when the governance gate caught the run's absence on rebase — the
  first new production-static run to appear after the gate was written, and
  the evidence that the gate is not decorative.
- `docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/` — SW-270's
  before/after measurement of the bare-filename exact-path rule on the SW-258
  dev split (`before/` at `9bf9326`, `after/` at `8ef5635` — the `.go`-only
  rule from review round 1). Its `semantic_name_only` and `semantic_first`
  series on both sides are produced by this pinned embedder; the `exact_path`
  0.6667 → 1.0000 result and the "every other stratum identical" finding are
  only valid for this revision.
- `docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/` — SW-280's
  qrel-blind smoke evaluation. Its 64 preserved `task_context/2` bundles were
  produced by this pinned embedder over the sealed cobra-v2 holdout, and the
  31/64 pass count against `k=56` is a statement about those bundles. A rotation
  changes the bundles and therefore invalidates the pass count, the responses
  graded from it and the interval; it also invalidates the run's preserved
  real-tokenizer counts, which `internal/eval/tokenizer/PIN_ROTATION.md` governs
  separately. Added when this governance gate caught the run's absence — the
  second time the gate has bitten on a genuinely new production-static run.
- `docs/eval/retrieval/runs/2026-09-06-sw282-recalibration-local/` — SW-282's
  comparator-only development report. `docs/eval/retrieval-targets.json` is
  DERIVED from it: on both conceptual strata the best single baseline is now
  `semantic_name_only`, produced by this pinned embedder, so a rotation moves
  `architecture_flow`'s bar (0.4578575262772977) and `nl_behaviour`'s
  (0.544970253069991) and invalidates the `exact_identifier` Top-1 floor of 1.
  Rotating the pin therefore requires re-deriving the targets file, not only
  re-measuring.
- `docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/` — SW-282's gating
  report over the same development slice with the full default baseline set.
  The recorded per-target verdict in
  `docs/eval/retrieval/targets-gate-expectations.json` (and the release-line
  `retrieval-targets` gate) is a statement about these numbers, so a rotation
  invalidates the recorded MISS on `architecture_flow` and on the
  `exact_identifier` floor as well as the PASS on `nl_behaviour`.
- `docs/eval/retrieval/runs/2026-09-06-sw282-coverage-local/` — SW-282's 6/6
  grade-3 `task_context/2` coverage re-measurement over `cobra-v2`'s dev
  `nl_behaviour` queries, which the `bundle_coverage` target is enforced
  against. A rotation changes the bundles and therefore the coverage count.
- `docs/eval/static-embedder-cross-arch/2026-09-03-sw271/` — the byte-exact
  `darwin/arm64` versus `darwin/amd64` vector record for this revision.
- `docs/eval/retrieval/runs/2026-09-06-recovery-dev/` — development-only
  before/after retrieval and MCP source-retention diagnostics, including a
  rejected implementation-priority experiment. All three ranking runs and
  the preserved MCP payloads become stale on model rotation. This records
  their dependency on the unchanged pin; it authorizes no release claim.
- `docs/eval/retrieval/runs/2026-09-06-architecture-dev/` — development-only
  candidate-ranking experiment and independent MCP captures.
- `docs/eval/retrieval/runs/2026-09-06-bundle-selection-dev/` — development-only
  source-selection ablations, final ranking and exact MCP payload captures.
- `docs/eval/retrieval/runs/2026-09-06-candidate-admission-dev/` — development-only
  term-balanced candidate admission and non-displacing grouped-declaration
  context measurement, including exact MCP payload captures from two indexes.
- `docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/` — development-only
  lifecycle-focus and exact referenced-definition recovery measurement. Its
  ranking report and two-index MCP payload capture use this pinned model.
- `docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency/` and
  `docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency-v2/` — the first
  two registered compact development sufficiency runs. Their frozen inputs and
  blind answers are bound to bundles produced by this pinned model.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-dev/` — the first
  development-only double capture through the production compact MCP path.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v2-dev/` — the reserved
  candidate-bound development recapture after the fail-closed audit fixes.
  Both product runs use the pinned production model and become stale if its
  revision changes.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v3-dev/` — the final-audit
  candidate-bound development recapture after source discovery and reference
  hydration were placed under one shared bounded snapshot.
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v4-dev/` — the sealed-source
  snapshot candidate recapture after empty-snapshot, Markdown, cancellation and
  partial-read accounting boundaries were closed.

The three SW-263-era JSON reports above predate selector stamping in that report
shape. They are explicit legacy entries because their candidate provenance and
identical `raw/hits-semantic_name_only.json` bytes bind them to the static AC-9
series. The other five run directories currently under
`docs/eval/retrieval/runs/` record an absent embedder or Ollama and are not
invalidated by rotating this pin.

## Current-pin adoption record

SW-271 places the already-shipped SW-262 pin
`potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`
under this policy. This adoption is not a model rotation. Its four SHA-256 values
remain the ones in `PinnedSHA256`; its dependent retrieval records and initial
cross-architecture evidence are enumerated above.
