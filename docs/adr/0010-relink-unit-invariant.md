# ADR 0010 — An edge's value may not depend on more files than the re-link unit (the PARITY-003 fix)

- Status: **Accepted** (language-GA Wave 0, W0.f-4, 2026-08-16)
- Story: language-GA execution plan Wave 0, W0.f-4
  ([`../plan/2026-08-graphi-language-ga-execution-plan-v1.md`](../plan/2026-08-graphi-language-ga-execution-plan-v1.md))
- Spec-Gate: the conformance change-class table under its NEW profile axis
  (`parityProfiles()`, both stores × `default` + `balanced`), plus
  `TestProfile_BalancedKeepsEveryImporterEdge`
- Depends: ADR 0009 (module-aware import resolution) — its re-measurement is
  what isolated PARITY-003 from the defect it had been conflated with
- Feeds: WP-J7 real-repo parity (G4), the Wave-0 gate

## Problem

PARITY-003, measured on pinned real repositories
([`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md)): under
the **default** index profile (`balanced`, what every `graphi
index/sync/rebuild` resolves to), `graphi sync` settled a deterministic
SUPERSET of the file→file `imports` edges `graphi rebuild` produced over the
identical tree — gin +5 edges, grpc-go +120.

The mechanism was the Balanced profile's per-target import aggregation
(`engine/ingest/linkfiles.go`): "external" `imports` edges were diverted and
collapsed to ONE edge per target node, from a REPRESENTATIVE source. That
collapse is a function of the file set of ONE pass. A re-link carries only a
subset of the tree, so the incremental pass re-aggregated the subset while the
previous pass's aggregated edges survived (the stale-edge sweep removes the
from-OWNED edges of reprocessed nodes, and the representative's file was not
reprocessed).

Closing the hermetic gate gap exposed a **second, worse** wrong the real-repo
counts had hidden: with two importers of one target, only the representative
kept an edge, so a file that really does import a package had **no `imports`
edge at all**, and the surviving edge carried the OTHER importers' `file:line`
evidence. That is a dropped true edge in a GA operation (`related_files`,
`imports`) — a recall defect, not a size optimization.

**Why every hermetic gate was blind.** `ingest.New` leaves the profile at its
zero value, which matches neither the Fast nor the Balanced branch, while the
CLI resolves Balanced for every user-facing pass
(`core/profile.ResolveProfile`). A 19-row, two-store parity table therefore
never executed the configuration the product ships.

## Decision

**The invariant, stated once for the whole engine:**

> The value of an edge may never depend on a set of files LARGER than the
> re-link unit. Anything that aggregates, collapses, ranks or de-duplicates
> across files is unsafe in an incrementally-updated store unless the
> incremental pass is guaranteed to see the identical file set — which, by
> definition of "incremental", it is not.

PARITY-002 violated it through the package-CLAUSE relation (an importer's edge
set depended on every directory in the repo declaring a clause); PARITY-003
violated it through profile aggregation. One invariant, two instances, and it
is the rule future work is measured against — not a description of two fixed
bugs.

**Applied here:** the Balanced import aggregation is REMOVED, not repaired.
Balanced now behaves exactly like Deep for `imports` edges; Fast still drops
them. The removal is not a trade-off against index size, because the
aggregation had **no legitimate prey in any language**: a true external import
mints no `imports` edge at all (Go `packageFileNodes`, `clausePackageFileNodes`
and `packageNodeByPath` each return nothing for a package the repository does
not declare), so every edge it could see was intra-repo. `isExternalImport`
split the path on `/` only, so it classified the repository's OWN dotted module
path — `github.com/…`, `google.golang.org/…`, and every Java/Python/C# package
path — as external.

**Consequence for the profile ladder, stated rather than hidden:** `i.profile`
is read in exactly three places (`typeresolve.go` Fast skips typeresolve;
`linkfiles.go` Fast drops imports; the aggregation, now gone), so **Balanced
and Deep are behaviourally identical today**. Both names stay — they are CLI
values and persisted `index.profile` metadata — but Balanced's "bounded
linking" claim is currently **unbacked**, and this ADR records that instead of
letting the name imply a reduction that no longer happens.

**The gate gap is closed structurally:** the conformance change-class table now
runs over a PROFILE axis (`parityProfiles()`: `default` + `balanced`) crossed
with both stores. A defect that lives in the shipped profile can no longer pass
a green hermetic table.

## Rejected alternatives (recorded, not silently omitted)

- **Make the aggregation deterministic across passes** — requires the FULL
  importer set of every affected target on every re-link, i.e. O(repo) re-links
  on hot targets. Exactly the performance cliff ADR 0009 rejected for
  PARITY-002, and it would preserve the dropped-edge and merged-evidence
  wrongs.
- **Keep aggregation but decide externality structurally** (module map /
  "did this resolve to a committed repo node") — the correct predicate makes the
  aggregation set EMPTY, so this is the same as removing it, with dead code left
  behind. It also cannot help the no-`go.mod` case, where the module map is
  empty by construction.
- **Aggregate at READ time** (`engine/query` / `surfaces`) — preserves a
  "fewer visible edges" UX with an exact store, and stays parity-safe. Not
  taken here: it changes `query.Result`, a frozen Stable-12 contract with a
  golden guard, and would bundle a surface decision into an ingest fix. Left
  open as a real option if a size claim is ever wanted back.
- **A deterministic per-FILE cap** (parity-safe, since a file's edges are always
  re-emitted together) — it would still have to pick one target among several
  and thus misattribute the edge, so it needs its own design and its own
  measurement.

## Consequences

- **Product-byte change on the shipped default profile.** Balanced graphs gain
  the imports edges the aggregation used to swallow (and lose the synthetic
  `aggregated N imports of …` edges). The parity-matrix candidate moves again;
  previously measured rows stay STALE until re-measured.
- Users of the default profile GAIN edges they should always have had — the
  fix is a recall improvement, not only a parity fix.
- PARITY-003's disclosure (readme Known limits, `graphi sync -h`, the doctor
  `known-defects` check) is retired in the same change that closes the defect,
  per the disclosure's own contract.
- The profile axis roughly doubles the change-class table's subtests. It is
  deliberately NOT gated behind `-short`: hiding the shipped default profile
  behind a flag is what produced this defect's two-release lifetime.

## Measured (2026-08-16, same day)

The real-repository re-measurement ran on the moved candidate and **PARITY-003
is closed by measurement, not only hermetically**: 19 of 19 rows PASS over two
publishable dispatches that agree on every verdict AND every per-row count and
snapshot digest (`-verdict-diff` and `-counts-diff` both exit 0). The three
rows that isolated the defect flipped: gin `remove_implementation`
6604/6599 → 6791/6791, gin `change_build_tag` 6607/6602 → 6794/6794, grpc-go
`replace_generated_file` 69733/69613 → 92518/92518. Record and artifacts:
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md),
`parity-matrix-adr0010-run-{c,d}.json`.

**The measurement also sized the recall loss the FAIL rows understated.** The
collapse was firing on every Go repository with a dotted module path, including
rows that already PASSED (there both passes aggregated consistently, so parity
held while edges were lost): the shipped default kept roughly 40 of 340
`imports` edges on cobra, 99 of 291 on gin, and **670 of 23 575 on grpc-go** —
up to ~97% of intra-repo `imports` edges dropped. Removing the aggregation is
therefore a recall FIX of that magnitude, which is why this ADR frames it as
correctness rather than as giving up an optimization. The fan-out budget the
Real-World Report Card publishes (< 8 imports edges per node) is met with room
to spare on the fixed product: cobra 0.36, gin 0.15, grpc-go 1.58.
