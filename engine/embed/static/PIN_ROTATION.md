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
   Record the grade-3 coverage and full provenance before SW-266 (or a later
   release gate) calibrates a threshold from it. Any threshold or baseline
   derived from an invalidated run must be recalibrated from the replacement;
   `docs/eval/retrieval-targets.json` stays untouched until SW-266.

## Records made stale by the next rotation

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
  dev split (`before/` at `9bf9326`, `after/` at `71cba5e`). Its
  `semantic_name_only` and `semantic_first` series on both sides are produced
  by this pinned embedder; the `exact_path` 0.6667 → 1.0000 result and the
  "every other stratum identical" finding are only valid for this revision.
- `docs/eval/static-embedder-cross-arch/2026-09-03-sw271/` — the byte-exact
  `darwin/arm64` versus `darwin/amd64` vector record for this revision.

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
