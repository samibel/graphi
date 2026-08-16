# Parity-matrix candidate move — the ADR 0009 candidate (2026-08-16)

- Status: **In effect** (language-GA Wave 0, W0.f-3; executes the owner-approved
  re-measurement plan)
- Moves: `parityreport.CandidateSHA`
  `80d67ed586723ab22704cf7aada316138cb1360e` (v0.7.1, the P0 release candidate)
  → **`c4209dd3be146c1d965acf4ea36a00aea5a3e70f`** (the merge that landed
  ADR 0009 on `main`, including both independent review rounds)

## What moved, and what deliberately did NOT

**Moved: the PARITY-MATRIX measurement candidate** — the SHA
`internal/parityreport.CandidateSHA` names, which `internal/parity`'s
provenance compares the product binary against and every parity report cites.

**Not moved: the P0 release candidate** in `docs/rc/evidence-index.yaml`'s
`candidate:` block. That block is defined as the PUBLISHED, TAGGED, ATTESTED
release (v0.7.1 at `80d67ed`, per SW-136's freeze record), and `c4209dd` has no
release: tagging one is a release-management decision the owner makes, not a
side effect of a measurement run. Until a release is tagged at or after
`c4209dd`, parity reports will cite a candidate the evidence index's release
block does not — THIS RECORD is the explanation of that divergence, and any
reader of either artifact is pointed here.

## Why the move is forced, not chosen

ADR 0009 (module-aware Go import resolution — the PARITY-002 fix) is a
product-byte change; its own Consequences section says the candidate moves.
Concretely: `imports`/`calls` edge sets change on clause-colliding
repositories, so a parity run over the fixed tree measures a DIFFERENT product
than v0.7.1. Under the old candidate every run refuses publication with
`product tree differs from the candidate` — correctly, because the refusal is
the provenance gate doing its job. The only honest ways forward were to move
the measurement candidate or to stop measuring; PARITY-002's closure requires
the measurement (`internal/parity`, two dispatches with identical COUNTS), so
the candidate moves.

## What the move costs (stated before it is paid)

Every parity measurement published against `80d67ed` — the SW-144 pair, the
SW-158 complete pair, and every row of `docs/rc/parity-matrix-real-repo.md` —
is historical the moment this lands: a true record of the OLD product, never a
statement about the new one. The published FAIL rows for PARITY-001/002 stay
preserved as the record of those defects. Nothing is re-pointed; the re-measure
produces NEW evidence or none.

## What the move enables

A publishable two-dispatch re-measure of the full 19-row matrix (17 change
classes + 2 crash conditions, now including `change_colliding_package_dir` and
`add_nested_gomod`) on the fixed product — the measurement that decides whether
PARITY-002's non-deterministic half is actually closed, via the new
`-counts-diff` gate (per-row node/edge counts + snapshot digests; the published
record demonstrated `-verdict-diff` is structurally blind to count flapping).

## Change control

Follows the §9 discipline of the three P0 moves
(`2026-07-p0-candidate-freeze-v071.md` and predecessors): the move is recorded
BEFORE its first published measurement, its costs are stated, superseded
records are kept, and no stale row inherits the new candidate without being
re-measured.
