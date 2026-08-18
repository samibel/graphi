# Parity-matrix candidate move — the ADR 0010 candidate (2026-08-16)

- Status: **In effect** (language-GA Wave 0, W0.f-4)
- Moves: `parityreport.CandidateSHA`
  `c4209dd3be146c1d965acf4ea36a00aea5a3e70f` (the ADR 0009 merge) →
  **`7574a49379d3ede0a08bdb024e7a2e315bdc14a1`** (the ADR 0010 commit: the
  pass-scoped Balanced import aggregation removed)
- Supersedes: [`2026-08-parity-candidate-move-adr0009.md`](2026-08-parity-candidate-move-adr0009.md),
  which superseded the P0 v0.7.1 freeze record for the same reason

## Why the move is forced, not chosen

ADR 0010 removes the Balanced profile's per-target import aggregation. Balanced
is the profile every shipped `graphi index/sync/rebuild` resolves to, so
balanced graphs change: they gain the `imports` edges the aggregation used to
swallow, and lose the synthetic `aggregated N imports of …` edges. A parity run
over the fixed tree therefore measures a DIFFERENT product than `c4209dd`, and
under the old candidate every run refuses publication with `product tree
differs from the candidate` — the provenance gate doing its job.

## What moved, and what deliberately did NOT

Same split as the previous move: this is the **parity-matrix measurement
candidate** only. The P0 **release** candidate in
`docs/rc/evidence-index.yaml`'s `candidate:` block still names the published,
tagged, attested release v0.7.1 at `80d67ed`; no release is tagged at
`7574a49`, and tagging one is the owner's decision, not a side effect of a
measurement. Parity reports therefore cite a candidate the release block does
not — THIS record, and its predecessor, are where that divergence is explained.

## What the move costs (stated before it is paid)

The W0.f-3 measurement published hours earlier
(`docs/rc/parity-matrix-adr0009-run-{a,b}.json`, 16 PASS / 3 FAIL) becomes
historical: a true record of the product as it was between the two fixes.
Nothing is re-pointed. In particular the three PARITY-003 FAIL rows keep
standing as the published evidence that isolated the defect — they are what
made this move necessary.

## What the move enables

A publishable two-dispatch re-measure of the 19-row matrix on the fixed
product, to settle whether the three PARITY-003 rows (gin
`remove_implementation`, gin `change_build_tag`, grpc-go
`replace_generated_file`) flip FAIL → PASS, with `-counts-diff` proving the
result identical across dispatches. Until that publishes, ADR 0010's fix is
proven hermetically (the new profile axis, red-without/green-with on both
stores) and by argument on the pinned repositories — not by measurement.

## Change control

Follows the §9 discipline of every prior move: recorded BEFORE its first
published measurement, costs stated, superseded records kept, and no stale row
inherits the new candidate without being re-measured. Two moves in one day is
not a process failure — it is two product-byte fixes landing in one day, each
with its own measurement.
