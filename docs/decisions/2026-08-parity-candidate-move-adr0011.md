# Parity-matrix candidate move — the ADR 0011 candidate (2026-08-19)

- Status: **In effect** (language-GA Wave 0, W0.f-5)
- Moves: `parityreport.CandidateSHA`
  `7574a49379d3ede0a08bdb024e7a2e315bdc14a1` (the ADR 0010 commit) →
  **`3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`** (the commit at which the
  ADR 0011 LINK-001 fix's product bytes settled)
- Supersedes: [`2026-08-parity-candidate-move-adr0010.md`](2026-08-parity-candidate-move-adr0010.md),
  which superseded [`2026-08-parity-candidate-move-adr0009.md`](2026-08-parity-candidate-move-adr0009.md),
  which superseded the P0 v0.7.1 freeze record for the same reason
- ADR: [`../adr/0011-imports-edge-targets-package-source-files.md`](../adr/0011-imports-edge-targets-package-source-files.md)

## Why the move is forced, not chosen

ADR 0011 changes what an `imports` edge points at. Before it, the edge targeted
**every file in the imported package's directory**; after it, only the package's
**source** files, decided on the file extension, with in-package `_test.go`
files excluded (they are package members but are not importable — the ADR makes
that ruling explicitly). Balanced is the profile every shipped `graphi
index/sync/rebuild` resolves to, so balanced graphs change: they lose the
`imports` edges that pointed at `README.md`, `.golangci.yml`, `.sh` and similar
non-source files.

A parity run over the fixed tree therefore measures a DIFFERENT product than
`7574a49`, and under the old candidate every run refuses publication with
`product tree differs from the candidate` — the provenance gate doing its job.
That refusal was observed, not assumed, before this move was made: at
`7e4291f` a dispatch reported `ProductDiffEmpty=false`, HEAD building to
`036be635…` against the candidate's `882881…`.

## Which commit the candidate is, and why it is not the obvious one

The LINK-001 fix landed over **three** commits, and the candidate is **not** the
first of them:

| Commit | Subject | `./cmd/graphi` sha256 |
|---|---|---|
| `01f95cf` | link: fix LINK-001 — an `imports` edge targets the package's SOURCE files (ADR 0011) | `fcd26b6e…` |
| **`3b8d43f`** | link,docs: apply the LINK-001 adversarial review — one undisclosed break, a third honest loss, four stale claims | **`036be635…`** |
| `7e4291f` | docs: correct the LINK-001 honest losses — two of the three published ones cannot occur | `036be635…` |

`3b8d43f` touched `engine/link/index.go` again to close an undisclosed break
found in adversarial review, so the product bytes did not settle until it
landed. `7e4291f` touched only documentation and one `_test.go` file and
therefore builds to the same bytes.

This was decided **by building, not by reading commit subjects**: each tree was
materialized with `git worktree add --detach` and built with
`CGO_ENABLED=0 go build -trimpath -buildvcs=false ./cmd/graphi`, exactly as
`internal/parity/provenance.go:64-97` does it. Naming `01f95cf` — the commit
whose message contains the word "fix" — would have pinned the candidate to a
product that never shipped and made every subsequent run unpublishable for a
reason that reads like a harness bug.

## What moved, and what deliberately did NOT

Same split as all three previous moves: this is the **parity-matrix measurement
candidate** only. The P0 **release** candidate in
`docs/rc/evidence-index.yaml`'s `candidate:` block still names the published,
tagged, attested release **v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`**.
No release is tagged at `3b8d43f`, and tagging one is the owner's decision, not
a side effect of a measurement. Parity reports therefore cite a candidate the
release block does not — THIS record, and its three predecessors, are where that
divergence is explained.

The move also changes no product byte itself: `cmd/graphi` does not link
`internal/parityreport` (`go list -deps ./cmd/graphi` does not contain it), so
editing `CandidateSHA` cannot invalidate the very comparison it configures.

## What the move costs (stated before it is paid)

The W0.f-4 measurement (`docs/rc/parity-matrix-adr0010-run-{c,d}.json`, 19/19
PASS) becomes **historical**: a true record of the product as it was between the
ADR 0010 and ADR 0011 fixes. Nothing is re-pointed and nothing is deleted. In
particular:

- The 19/19 PASS result is **not** inherited by the new candidate. It has to be
  re-earned by measurement, and this move is what makes that measurement
  possible at all.
- The per-kind recall figures published beside it (cobra ~40 → 340, gin ~99 →
  291, grpc-go ~670 → 23 575 `imports` edges) were measured across the
  `c4209dd` → `7574a49` boundary. They stay true of that boundary and say
  nothing about this one.
- The LINK-001 figures in the same file (cobra **44 of 340** `imports` edges
  pointing at `.md`/`.yml`; grpc-go **2 120** at `.md`/`.sh`) are pre-fix
  measurements. They are exactly the edges ADR 0011 removes, which is why the
  new measurement is expected to read **lower** edge counts, not higher.

## What the move enables

A publishable two-dispatch re-measure of the 19-row matrix on the fixed product,
which is what converts LINK-001 from "closed in code and by hermetic proof" to
"closed by measurement" — the stronger of the two claims this project
distinguishes. Until that publishes, the weaker claim is the one on the record,
and the 2026-08-19 amendment at the top of
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md) says so in
those words.

## Change control

Follows the §9 discipline of every prior move: recorded BEFORE its first
published measurement, costs stated, superseded records kept, and no stale row
inherits the new candidate without being re-measured. The retired candidate's
wording (`"adr 0010 candidate"`) joins the forbidden-phrasing list in
`internal/parity/parity_test.go` at the same time, so a provenance sentence that
still names the previous candidate fails closed rather than reading as a correct
claim about the wrong product.
