# Full/incremental parity matrix over pinned real repositories

# Current measurement — the ADR 0010 candidate (2026-08-16, W0.f-4)

**Status: PUBLISHED PASS — 19 of 19 rows, and the first fully green matrix this
project has measured.** Two dispatches, `outcome PASS` and `publishable: true`
in both, agreeing on **every verdict** (`-verdict-diff` exit 0) AND on **every
per-row node/edge count and snapshot digest** (`-counts-diff` exit 0). The
two §12.3 store-level counts read **orphaned external nodes = 0** and **stale
linker edges = 0** on all 38 sides (19 rows × full + incremental).

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, two full local dispatches |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to the ADR 0010 candidate at `7574a49379d3ede0a08bdb024e7a2e315bdc14a1`** (candidate move: [`../decisions/2026-08-parity-candidate-move-adr0010.md`](../decisions/2026-08-parity-candidate-move-adr0010.md)); run SHA `3398d3b6c0f0`, runner class `Linux-X64/ccr-container`, go1.26.6 linux/amd64, clean worktree, both dispatches publishable |
| Matrix source | `docs/rc/parity-classes.yaml` (17 change classes + 2 crash conditions; every row with a proof records its `profile:` axis — the one ABSENT row correctly records none) |
| Report artifact | `docs/rc/parity-matrix-adr0010-run-c.json`, `…-adr0010-run-d.json` |
| Historical artifacts | the ADR 0009 pair and the v0.7.1 pairs, preserved not deleted — see the superseded records below |

## Results (identical in both dispatches, to the byte)

| Class | Verdict | Repository | inc = full (nodes/edges) |
|---|---|---|---|
| `add_file` | **PASS** | cobra | 940/4218 |
| `modify_file` | **PASS** | cobra | 939/4207 |
| `delete_file` | **PASS** | cobra | 897/4069 |
| `rename_symbol` | **PASS** | cobra | 938/4199 |
| `move_symbol` | **PASS** | cobra | 939/4217 |
| `rename_package` | **PASS** | cobra | 938/4206 |
| `add_call` | **PASS** | cobra | 939/4208 |
| `remove_call` | **PASS** | cobra | 938/4206 |
| `change_interface` | **PASS** | lo | 523/704 |
| `add_implementation` | **PASS** | lo | 526/707 |
| `remove_implementation` | **PASS** | gin | 1903/6791 |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical (branch switch a→b) |
| `change_build_tag` | **PASS** | gin | 1904/6794 |
| `replace_generated_file` | **PASS** | grpc-go | 14922/92518 |
| `change_external_import` | **PASS** | cobra | 940/4208 |
| `interrupted_full_pass` | **PASS** | cobra | 6/6 repetitions identical (K1, K3) |
| `restart_and_recovery` | **PASS** | cobra | 6/6 repetitions identical (K5 -> K7, K6 -> K7) |
| `change_colliding_package_dir` | **PASS** | cobra | 940/4207 |
| `add_nested_gomod` | **PASS** | cobra | 941/4196 |

## What this measurement closes

**PARITY-003 is closed by measurement.** All three rows that failed on the ADR
0009 candidate now pass, and the fix is ADR 0010 (the pass-scoped Balanced
import aggregation is removed):

| Row | Repo | ADR 0009 candidate (inc/full) | ADR 0010 candidate |
|---|---|---|---|
| `remove_implementation` | gin | 6604 / 6599 — **FAIL** | 6791 / 6791 — **PASS** |
| `change_build_tag` | gin | 6607 / 6602 — **FAIL** | 6794 / 6794 — **PASS** |
| `replace_generated_file` | grpc-go | 69733 / 69613 — **FAIL** | 92518 / 92518 — **PASS** |

**With PARITY-001 and PARITY-002 (closed by the previous measurement), the
matrix now carries no open parity defect** — and for the first time the
two-green-runs discipline holds at COUNT granularity, which is the property
`-verdict-diff` alone was demonstrated unable to see.

## The finding the FAIL rows understated: a large recall loss under the shipped default

The fix removed a collapse that was firing on **every** Go repository with a
dotted module path — including the rows that were already PASSING, because
there both passes aggregated consistently and parity was preserved while edges
were lost. Measured on the fixed candidate against the previous one:

| Repo | `imports` edges kept before | after | dropped by the default profile |
|---|---|---|---|
| cobra | ~40 | 340 | ~88% |
| gin | ~99 | 291 | ~66% |
| grpc-go | ~670 | 23 575 | **~97%** |

(Totals: cobra 3918 → 4218 edges, gin 6599 → 6791, grpc-go 69613 → 92518, i.e.
**+22 905 edges** on grpc-go. `lo` is unchanged at 704 — it has no intra-repo
imports to collapse.)

**PROVENANCE OF THIS TABLE, because it is not in the report artifacts**
(review round 1, finding 6): `parity-matrix-adr0010-run-{c,d}.json` carry only
per-row TOTAL node/edge counts and digests, so they cannot reproduce the
per-kind figures above. Those come from counting `imports` edges in the
snapshots the run kept, and from re-indexing the same pinned clones with a
binary built at the previous candidate. Reproduce with:
`go build -o /tmp/pre ./cmd/graphi` at `c4209dd` and at `7574a49`, index a
pinned clone with each, then count by kind. The review re-derived all six
figures independently and they matched exactly, including the per-kind claim
that node counts and every non-`imports` kind (`calls`, `defines`,
`references`, `implements`, `inherits`) are IDENTICAL before and after — which
is what makes "the delta is entirely `imports`" a measurement rather than an
inference. The cobra "before" figure is the `add_file` row (Δ300); other cobra
rows carry Δ280–290, so recomputing from a different row gives a slightly
different total.

So under the profile the product actually ships, a file that really did import
a package frequently had **no `imports` edge at all** — only one representative
importer per target kept one, carrying the other importers' `file:line`
evidence. That is a recall defect in a GA operation (`related_files`,
`imports`), and no parity gate could see it, because parity compares two passes
of the same broken rule.

**One published claim cross-checked, because it could have been a product of
the defect:** the Real-World Report Card's metric 2 ("**0.96**, budget < 8") is
measured by `TestLinkFanout_EdgeExplosionBudget`, which runs the library's
ZERO-value profile and therefore always measured the un-aggregated world — the
figure never included the aggregation and is unaffected. Two precisions found
while checking it (review round 1, finding 13): that metric is **total** edges
per node, not imports-only (its label said otherwise and is corrected in
`../real-world-report.md`), so it is not the same ratio as the imports-per-node
figures here; and on its own fixture the shipped Balanced profile went 0.67 →
**0.96** with this fix, i.e. the product now lands exactly on the published
number instead of below it. The new real-repo imports-per-node figures sit far
inside the gate's bound either way: cobra **0.36**, gin **0.15**, grpc-go
**1.58**.

**Storage consequence, stated rather than left to be discovered:** ~33% more
edges on grpc-go means a larger index for repositories of that shape (measured:
grpc-go's store grows 25.2 MB → 30.7 MB, +22%). The §12.2 `db_size` gate
(≤ 300 MB) is UNKNOWN on this candidate — like every performance gate — so this
measurement neither satisfies nor breaks it; it is named here so the next
baseline run knows to look.

### CORRECTION (same day, from this change's independent review): the recall half was published without its precision half

The sentence this record and ADR 0010 first shipped — "users of the default
profile GAIN edges they should always have had" — is **not supportable as
written**, and the review that found it is the reason it is corrected here
rather than left standing. The restored edges are per-importer and correctly
attributed, but `imports` targets are **every file node in the target
directory**, not the imported package's source files
(`engine/link/index.go:150` fills `fileNodesByDir` from every `file` node;
`packageFileNodes` returns the whole list). So the aggregation had been masking
a pre-existing WRONG-edge class, and removing it multiplies what reaches the
user. Measured on the same pinned clones, `imports` edges by target:

| repo | targets that are not `.go` at all | share of all `imports` | `_test.go` targets |
|---|---|---|---|
| cobra | 4 → **44** (33 `.md`, 11 `.yml`) | 12.9% | 126 (37.1%) |
| gin | 8 → 8 | 2.7% | ~38% |
| grpc-go | 15 → **2 120** (1 417 `.md`, 703 `.sh`) | 9.0% | ~31.7% |

A `README.md` is not part of a Go package, so an `imports` edge to it is wrong
in either profile — `-profile deep` and every hermetic gate have always emitted
these. What changed is dominance under the SHIPPED default. Reproduced
end-to-end on pinned cobra: `graphi related-files -max-files 12
doc/man_docs.go` returned 5 genuinely-related items before and 12 after, the
extra 7 including `.golangci.yml`, `CONDUCT.md`, `CONTRIBUTING.md` and
`README.md`. So on that GA operation **recall improved and precision
regressed**, and degree-ranked surfaces (`agent-brief`'s "start here" files,
`search-hybrid`'s inbound-degree score) shift with it, unmeasured.

Filed as **LINK-001** (open, disclosed on the user surfaces, fix scheduled as
its own change with its own red/green and its own re-measurement — it is a
product-byte change and moves the candidate again). Fix direction:
`packageFileNodes` must return the target package's SOURCE files, which is a
language/extension question the index can answer from the node's path; whether
in-package `_test.go` files belong (they are part of the package but are not
importable) is a separate ruling that change must make explicitly. This
correction is published in the same change that received the review, per the
disclosure contract — the honest sequence is that the measurement was right,
the framing around it was not.

## Reproducing this measurement

```bash
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-c -report run-c.json
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-d -report run-d.json
go run ./cmd/parity -verdict-diff run-c.json,run-d.json   # verdicts agree
go run ./cmd/parity -counts-diff  run-c.json,run-d.json   # counts + digests agree
```

---

# Superseded measurement — the ADR 0009 candidate (2026-08-16, W0.f-3)

> **SUPERSEDED the same day by the ADR 0010 measurement above.** It stands as
> published: it is the record of the product BETWEEN the two fixes, and its
> three PARITY-003 FAIL rows are what isolated that defect and forced the
> second candidate move. Nothing here is re-pointed.

**Status: PUBLISHED FAIL, COMPLETE, and — for the first time — DETERMINISTIC.**
All **19** declared rows execute — 17 change classes (15 FR-7 + the two ADR
0009 rows) and 2 crash conditions. **16 PASS, 3 FAIL**, the three accounted for
by **ONE newly isolated defect (PARITY-003, filed below)**. Two dispatches
agree on **every verdict AND every per-row node/edge count and snapshot
digest** (`-verdict-diff` exit 0, `-counts-diff` exit 0) — the two-green-runs
discipline now holds at COUNT granularity, which the historical record below
could not claim.

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, two full local dispatches |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to the ADR 0009 candidate at `c4209dd3be146c1d965acf4ea36a00aea5a3e70f`** (candidate move: [`../decisions/2026-08-parity-candidate-move-adr0009.md`](../decisions/2026-08-parity-candidate-move-adr0009.md)); run SHA `4d032fe5acac3c978ca15eda1c97235aba4e2abc`, runner class `Linux-X64/ccr-container`, both dispatches publishable |
| Matrix source | `docs/rc/parity-classes.yaml` (17 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-adr0009-run-a.json`, `…-adr0009-run-b.json` |
| Historical artifacts | the v0.7.1-candidate pairs, preserved not deleted — see the historical record below |

## Results (identical in both dispatches, to the byte)

| Class | Verdict | Repository | inc nodes/edges | full nodes/edges |
|---|---|---|---|---|
| `add_file` | **PASS** | cobra | 940/3918 | = |
| `modify_file` | **PASS** | cobra | 939/3917 | = |
| `delete_file` | **PASS** | cobra | 897/3789 | = — **PARITY-001 CLOSED BY MEASUREMENT** (was FAIL) |
| `rename_symbol` | **PASS** | cobra | 938/3909 | = |
| `move_symbol` | **PASS** | cobra | 939/3917 | = |
| `rename_package` | **PASS** | cobra | 938/3916 | = |
| `add_call` | **PASS** | cobra | 939/3918 | = |
| `remove_call` | **PASS** | cobra | 938/3916 | = |
| `change_interface` | **PASS** | lo | 523/704 | = |
| `add_implementation` | **PASS** | lo | 526/707 | = |
| `remove_implementation` | **FAIL** | gin | 1903/**6604** | 1903/**6599** — **PARITY-003** |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical | |
| `change_build_tag` | **FAIL** | gin | 1904/**6607** | 1904/**6602** — **PARITY-003** |
| `replace_generated_file` | **FAIL** | grpc-go | 14922/**69733** | 14922/**69613** — **PARITY-003, now DETERMINISTIC** (see below) |
| `change_external_import` | **PASS** | cobra | 940/3918 | = |
| `interrupted_full_pass` | **PASS** | cobra | 6/6 repetitions identical (K1, K3) | |
| `restart_and_recovery` | **PASS** | cobra | 6/6 repetitions identical (K5→K7, K6→K7) | |
| `change_colliding_package_dir` | **PASS** | cobra | 940/3917 | = — new row, the PARITY-002 reproduction, real parity on real source |
| `add_nested_gomod` | **PASS** | cobra | 941/3906 | = — new row, the ADR 0009 invalidation pin |

The two §12.3 store-level counts read **orphaned external nodes = 0** and
**stale linker edges = 0** on every executed row, on both sides (same scope
limit as ever: a "stale linker edge" is one whose endpoint is not a node, so
PARITY-003's extra edges — valid endpoints — are invisible to it by design).

## What this measurement CLOSES

- **PARITY-001 is closed by measurement.** `delete_file` on real cobra source
  flips FAIL → PASS; the purge-before-link fix holds outside the fixture.
- **PARITY-002 is closed by measurement — both halves.** The deterministic
  half: the published fan-out signature (an importer of `x/json` carrying an
  `imports` edge into an unrelated directory that merely shares the clause) no
  longer appears anywhere in either dispatch, and the two rows built from the
  defect (`change_colliding_package_dir`, `add_nested_gomod`) PASS on real
  source. The NON-DETERMINISTIC half: the historical record's grpc-go row
  produced **three distinct incremental snapshots over six executions**
  (69902/69939/69940); this measurement produces **byte-identical incremental
  snapshots across both dispatches** (sha256 `86e7d02f…` in both), and
  `-counts-diff` — added because `-verdict-diff` is structurally blind to count
  flapping — exits 0 over the full pair. What ADR 0009 could previously close
  only by argument is now closed by measurement.

## PARITY-003 — filed by this measurement, NOT fixed

**One defect, three rows, a DIFFERENT mechanism than PARITY-002 — the
historical record's "PARITY-002" FAILs on gin/grpc-go were two defects
overlapping.** ADR 0009 removed the resolution-layer half; what remains is a
profile-layer closure defect:

- **Shape:** the incremental graph is a strict SUPERSET of the full graph
  (only-in-full = 0 in every failing row), only `imports` edges, deterministic
  — byte-identical across both dispatches. Class-independent: the two gin rows
  (unrelated mutations) diverge by the IDENTICAL five edge IDs.
- **Mechanism** (`engine/ingest/linkfiles.go`, Balanced profile): the profile
  aggregates "external" imports by TARGET file — one edge per target, from a
  REPRESENTATIVE source (`aggregated N imports of …`), computed over the files
  of ONE pass. `isExternalImport` classifies any dotted first segment as
  external, which catches the repository's OWN module path (`github.com/…`,
  `google.golang.org/…`) — the only imports that ever HAVE intra-repo edges to
  aggregate (true externals resolve to no target at all). A full pass
  aggregates over every file (gin: one edge per `internal/json` target from
  `binding/form_mapping.go`, "aggregated 6 imports"); an incremental pass
  re-aggregates over only the RE-LINKED subset (new representative edges,
  "aggregated 2 imports" from `errors.go`), while the baseline's aggregated
  edges survive — the stale-edge sweep removes from-OWNED edges of reprocessed
  nodes, and the representative's file was not reprocessed. gin: 5 + 5 = 10
  edges incremental vs 5 full. grpc-go: 120 extra incremental edges (99
  aggregated-reason, 21 lone-importer, 8 representative from-files).
- **A second wrong beyond parity:** the aggregated edge misattributes the
  import — an edge `from errors.go` carrying `errors_test.go`'s evidence, and
  on the full side a single representative standing for six importers. Readers
  of `related_files`/`imports` see one importer where there are six.
- **Why every hermetic gate missed it:** the engine's zero-value profile does
  not aggregate, and `ingest.New` defaults to it — the conformance and ingest
  suites drive the library. The CLI resolves the profile and DEFAULTS TO
  BALANCED, so every real `graphi rebuild`/`sync` runs the aggregation path the
  fixtures never exercise. This is a gate gap in its own right: the parity
  fixtures must also run under the shipped default profile.
- **Disclosed** (same contract as PARITY-002's disclosure, restored in the
  same change that files this): readme Known limits, `graphi sync -h`, and the
  doctor `known-defects` check. Workaround: `graphi rebuild`, or
  `-profile full`.
- **Fix direction, recorded not executed** (it is a product-byte change and
  moves the candidate again — own change, own red/green, own re-measure): an
  import path OWNED by the tree's module map is not external, which reduces
  the Balanced aggregation to its actual prey — and true externals mint no
  file→file imports edges, so the correct aggregation set is empty. Plus a
  Balanced-profile conformance run to close the gate gap.

## Reproducing this measurement

```bash
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-a -report run-a.json
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-b -report run-b.json
go run ./cmd/parity -verdict-diff run-a.json,run-b.json   # verdicts agree
go run ./cmd/parity -counts-diff  run-a.json,run-b.json   # counts + digests agree
```

---

# Historical record — the v0.7.1 candidate (SW-144 + SW-158), preserved as published

> Everything below measured **the OLD candidate** (v0.7.1 at `80d67ed…`),
> BEFORE the PARITY-001 and ADR 0009 fixes. It stands as published: its FAIL
> rows describe that tree, its "PARITY-002" rows conflate what are now known
> to be two defects (the fan-out fixed by ADR 0009, and PARITY-003 above), and
> its grpc-go non-determinism is the phenomenon the current measurement
> closes. Nothing below was rewritten.

**Status: PUBLISHED FAIL, and now COMPLETE.** All **17** declared rows execute — 15 FR-7 change
classes and 2 Delta §9 crash conditions. **13 PASS, 4 FAIL**, the four accounted for by **two
product defects**, both filed and neither fixed. Nothing is deferred any more.

| | |
|---|---|
| Produced by | `internal/parity` (+ `lifecycle.go`) + `cmd/parity`, `.github/workflows/parity.yml` |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`** |
| Matrix source | `docs/rc/parity-classes.yaml` (15 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-complete-run-a.json`, `…-complete-run-b.json` (the complete 17-row matrix, SW-144 + SW-158) |
| Superseded artifact | `docs/rc/parity-matrix-run-a.json`, `…-run-b.json` — SW-144's own pair, **preserved not deleted**: 14 executed rows with the three lifecycle rows DEFERRED. Every change-class verdict and digest in it reproduces in the complete pair. |

---

## 1. What this is, and the three things it is not

It is the first reading of PRD §12.3's full-vs-incremental parity gate **on real Go**. Every
parity proof in the tree before it ran over a `t.TempDir()` fixture.

**Checklist row 13 is satisfied ONLY NOW, and only by both stories.** Row 13 is satisfied
**by SW-144 *and* SW-158 together** (adopted decision 4) — SW-144 built the harness and the 15
change-class rows; SW-158 added the branch-switch, interrupted-full-pass and
restart-and-recovery rows in `internal/parity/lifecycle.go`. **Neither story alone was, or may
be recorded as, "SW-144 done."** Between the two, this record read four executed classes short
and three rows DEFERRED, and that state was never the §12.3 recovery gate.

**It is not WP6.** WP6's threshold contains a *"recovery/crash-fault suite 100% green"* conjunct
(`docs/rc/evidence-index.yaml:125-135`). The three lifecycle rows are an **input** to it. WP6's
90-day clock has not started, every other conjunct of that threshold is unmeasured, and the row
is **not moved** by this record.

**It is not a performance measurement.** No latency, no percentile, no RSS figure is produced
or implied. Parity is a reliability property (PRD `:802-805`); §12.2 is SW-143's.

## 2. How a row is decided

For each change class, against a pinned clone:

1. `graphi rebuild` at the pinned tree — the state the incremental pass updates **from**.
2. The change class is applied as a **real edit to real source**.
3. `graphi sync` — the incremental pass.
4. `graphi rebuild` into a **fresh** store that has never seen the pre-edit state.
5. **The assertion:** `bytes.Equal` over the two portable snapshot envelopes.

The graphi **binary is driven as a subprocess** throughout. The harness never calls ingest
in-process, so it cannot perturb the instrument even in principle; `TestNoIngestInProcess`
asserts that over both the normal **and** the `-test` dependency sets.

**Snapshot bytes assert. `graphi compare` only diagnoses.** The `BranchDiffReport` is captured
on a FAIL to explain it and never decides a row — it is a Labs surface, and a §12.3 gate must
not depend on a Labs analyzer's `BranchDiffSchemaVersion`. A diff showing no deltas while
snapshot bytes differ would itself be a finding; that combination did not occur in this run.

**Three rows are lifecycle events, not content edits, and are decided differently** — see §6.
The assertion is the same (snapshot bytes against a fresh full index); what varies is the
journey that produces the incremental side, and each of those rows publishes **every repetition
of its journey** rather than one execution.

Byte parity over the envelope is **strictly stronger** than FR-7 `:832`'s enumerated field
comparison: `model.Graph.Marshal` emits ids, kinds, qualified names, source anchors, meta,
confidence tiers, confidence, reasons and evidence, canonically sorted. The field-by-field walk
is therefore deliberately not re-implemented.

## 3. Results

| Class | Verdict | Repository | Signature |
|---|---|---|---|
| `add_file` | **PASS** | cobra | |
| `modify_file` | **PASS** | cobra | |
| `delete_file` | **FAIL** | cobra | inc 895/3784 vs full 897/3789 — **PARITY-001** |
| `rename_symbol` | **PASS** | cobra | |
| `move_symbol` | **PASS** | cobra | |
| `rename_package` | **PASS** | cobra | |
| `add_call` | **PASS** | cobra | |
| `remove_call` | **PASS** | cobra | |
| `change_interface` | **PASS** | lo | |
| `add_implementation` | **PASS** | lo | |
| `remove_implementation` | **FAIL** | gin | inc 1889/6602 vs full 1889/6599 — **PARITY-002** |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical — §6 |
| `change_build_tag` | **FAIL** | gin | inc 1890/6605 vs full 1890/6602 — **PARITY-002** |
| `replace_generated_file` | **FAIL** | grpc-go | full 14898/69772 (stable); inc 14898/**69939 (run-a)**, **69940 (run-b)** — **PARITY-002, and the incremental edge count is NON-DETERMINISTIC: see §5** |
| `change_external_import` | **PASS** | cobra | |
| `interrupted_full_pass` | **PASS** | cobra | crash condition, not a change class; 6/6 repetitions identical across ADR **K1** and **K3** — §6 |
| `restart_and_recovery` | **PASS** | cobra | crash condition, not a change class; 6/6 repetitions identical across ADR **K5→K7** and **K6→K7** — §6 |

**Four failing rows, two defects — and every one of them is a change class.** The three
PARITY-002 rows are one defect surfacing three times, not three defects — see §5. **All three
lifecycle rows PASS**, reproducibly, and §6 states exactly what that does and does not prove.

### The two §12.3 store-level counts

**Orphaned external nodes = 0** and **stale linker edges = 0** on every executed row, **on both
the rebuild side and the incremental side** — counted from the same envelopes the assertion
compared, not inferred from the fixture-level proofs at
`engine/ingest/link_external_lifecycle_e2e_test.go:29` and `link_cascade_test.go:118`.

**Both sides are counted, and each figure is labelled with the side it describes.** That is a
correction: the first cut of this harness passed only the rebuild graph to the counter and
decoded the incremental graph without using it, so these counts were undisclosed-ly a statement
about one of the two graphs a row compares — and the incremental side is the one a parity
defect actually lands on. Every side of every executed row reads 0/0, before and after. The gap
was coverage and disclosure, not a wrong count.

**Republishing did, however, move one figure — `replace_generated_file`'s `inc_edges` — and
that was not the correction's doing.** It exposed that the number varies between runs. See §5.

**Read that with §5's scope limit.** A stale linker edge here means an edge whose endpoint is
**not a node in the graph**. PARITY-002's extra edges have valid endpoints on both sides, so
they are invisible to this counter. Zero means *no dangling endpoints*; it does not mean *no
edges a full pass would have recomputed away*.

## 4. Repository selection — which class ran where, and why

AC-6 exists so no reader assumes a class was exercised on a repository it never touched.

- **Manifest-pinned.** `change_build_tag` → **gin** and `replace_generated_file` → **grpc-go**,
  taken from `corpus/manifest.json`'s stratification block. These two never roam: if the pinned
  repository is out of tier the class SKIPS rather than being re-pointed, because a substituted
  repository would make the row a claim about a property it was never selected for. grpc-go is
  also the manifest's declared multiple-go.mod repository, so that row exercises a multi-module
  tree as well (sub-modules are excluded from the edit model — only the root module is touched).
- **Smallest exhibiting.** Every other class walks the Go repositories in **(tier, measured
  go-file count)** order and takes the first whose **real source** a planner can find a target
  in. Tier leads so cobra — the only tier-1 real repository — is preferred, which is also what
  keeps the local `-max-tier 1` run meaningful.

**A finding from that walk, recorded because it contradicts a reasonable assumption:**
**cobra v1.8.0 declares no named interface types in non-test source at all** — only anonymous
`interface{}`. The three interface-shaped classes therefore cannot run on it and land on
**lo** (17 Go files, the smallest tier-3 Go repository) and **gin**. Selection is decided
against the clone, not against a repository's reputation, which is why this surfaced as a
different row assignment rather than as a vacuous pass.

## 5. The two defects — published, filed, **not fixed**

Fixing a parity defect is a product-byte change: it moves the candidate and violates the
owner's ruling that one v0.7.2 batches every correction after the F4 residual and the freshness
diagnosis are measured (Delta PRD §6.2). **In slice: find it, publish the FAIL, file it.**

### PARITY-001 — known, now confirmed on real source

Deleting a file that declares a symbol another package calls through an intra-module import
leaves `graphi sync` **permanently** diverged from `graphi rebuild`.

Found hermetically by SW-157 and already scheduled as **v0.7.2 batch item 3**. This run
confirms it on real source: deleting cobra's `cobra.go` (which declares `WriteStringAndCheck`,
called by the in-module `doc` package) gives **incremental 895/3784 against full 897/3789**.
Full mints two interned external nodes and the five edges pointing at them; one incremental
apply mints neither — exactly what the recorded phase-ordering cause predicts
(`engine/ingest/ingest.go:709` runs `linkFiles` before the deleted-path purge at `:721-736`,
and `engine/ingest/linkfiles.go:64-71` indexes the **live** store).

The blast radius is confirmed as an ordinary refactor shape on a real 36-file library, not a
fixture artifact. **Note for whoever fixes it:** SW-157's review established that the naive fix
— purging before `linkFiles` — **breaks SQLite** with `edge references unknown node`.

### PARITY-002 — new, found by this matrix

**Whenever `graphi sync` re-links a file, the file→file `imports` edge set can settle
differently from what `graphi rebuild` produces over the identical tree.** Filed in
`projects/graphi/backlog.md`.

Node counts are identical on both sides in every instance; **every diverging edge is
`kind: "imports"`**, and no other edge kind and no node ever differs.

**It is bidirectional, not purely additive.** On gin the incremental graph carries **4** edges
the rebuild does not **and the rebuild carries 1 the incremental does not** — net +3, but a
one-way "incremental adds edges" reading is wrong and would send a fixer looking only for a
missing sweep. On grpc-go the net is **+167 in run-a and +168 in run-b** — and that difference
is not rounding, it is the defect's second and worse property.

### PARITY-002's divergence is NON-DETERMINISTIC — observed, not explained

The gin rows reproduce to the byte. **The grpc-go row does not.** Over the identical pinned
tree and the identical binary:

| Execution | `full_edges` | `inc_edges` |
|---|---|---|
| pre-correction run-a | 69772 | 69940 |
| pre-correction run-b | 69772 | 69940 |
| published **run-a** | 69772 | **69939** |
| published **run-b** | 69772 | **69940** |
| review re-run 1 | 69772 | **69902** |
| review re-run 2 | 69772 | 69939 |

**Six executions, three distinct incremental snapshots, a spread of at least 38 edges. The full
side is 69772 every single time.** So `graphi rebuild` is reproducible here and `graphi sync`
is not, which makes "+168" one sample of a varying quantity rather than the magnitude of the
defect. **Every figure for this row is therefore attributed to the run that produced it**, and
no single number should be quoted as *the* size of the divergence.

**This is an observation with its sample, not a characterised mechanism.** Nothing here
establishes *why* the incremental count varies; that belongs to whoever fixes PARITY-002, and
it is recorded in `projects/graphi/backlog.md` as evidence rather than repaired here. It is
consistent with the under-determined representative-file target described above — if the
target set is not unique, which representative is chosen may depend on ordering that is not
pinned — but consistency is not proof and this record does not claim it.

**It is disclosed here because this record's own standard demands it.** §8 says a row that
differs between two otherwise-identical dispatches is an environment finding **to be
explained**, never a flake to be retried away. This row differs, and `-verdict-diff` cannot see
it: that gate compares **verdicts**, and all six executions agree the row FAILs. Deliberately
so — but it means verdict agreement is not evidence of a reproducible measurement, and the
counts beneath a verdict need reading per run.

**It is one defect, not three, and this is the load-bearing evidence.** The three gin rows are
three different files and three different edit shapes — appending a function to `auth.go`,
deleting method `Context.Status` in `context.go`, editing a `//go:build` comment in
`context_appengine.go` — yet they produce a **byte-identical `BranchDiffReport`**, down to the
same five edge ids. One identical delta from three unrelated edits means the divergence belongs
to the repository plus an incremental re-link, not to any change class. The matrix reports it
as three rows only because the matrix is organised by class.

**The trigger is bounded tightly by two controls, and it is narrower than "modification".**

- A **comment-only** edit triggers it. So it is independent of edit *content*, not merely of
  change class — nothing about the symbol graph needs to change.
- A **no-op `sync`** converges. So it is not "running `sync`" either.

The trigger is therefore precisely **`sync` re-linking any file at all**. `add_file` on gin
converges, and cobra converges throughout.

**"Package depth" is a correlate, not the discriminator** — an earlier version of this record
said otherwise and was wrong. What distinguishes the affected repositories is that they contain
package directories whose **import target is under-determined**:

- `imports` edges are **file→file**, emitted at `engine/link/resolve_go.go:193` from
  `idx.packageFileNodes(imp.Path)` — the set of file nodes in the imported directory — at
  `classSelector`, i.e. the **`heuristic`** tier (`engine/link/link.go` `tierFor`). The code's
  own comment concedes the shape: it links "to the directory's file node **when uniquely
  determinable**".
- gin's `internal/json` holds **four mutually-exclusive build-tag variants all declaring
  `package json`**, so "the" target file is not unique. graphi evaluates no build constraints,
  so all four are indexed.
- gin's `internal/bytesconv` puts a **`_test.go` file up as an import target on both sides**.

**The linker does not pick a representative — it fans out over the whole set.**
`resolve_go.go:188-193` loops `for _, targetFile := range idx.packageFileNodes(imp.Path)` and
emits **one `imports` edge per file node in the imported directory** (skipping only the
importing file itself). So what varies between a cold pass and a re-link is not *which* file was
chosen but *which file nodes were in the index when the loop ran* — and therefore **how many
edges the loop emitted**. That is why the divergence is counted in edges rather than in
retargeted endpoints, and why it is bidirectional. (The set semantics are stated correctly at
the first bullet above; this paragraph previously said "which representative the linker lands
on", which understated the precision of the mechanism rather than overclaiming it. Corrected in
SW-158, from SW-144 review round 1, re-raised round 3.) **Neither side is absolutely right** —
but `rebuild` is the reference by definition, so `sync` is what diverges.

**It is not PARITY-001:** different edge kind, different trigger (re-link vs deletion), and
bidirectional rather than one-way.

**This is a characterisation, not a fix, and not a completed root-cause analysis.** SW-144's
scope was to prove the divergence, publish it and file it. PARITY-001's first stated cause was
wrong in two ways and had to be corrected in review, which is why this record states what was
measured and stops there.

**Independently reproduced without this harness.** SW-144's review cloned gin v1.9.1, drove
only the built binary and edited by hand — no `internal/parity` in the loop — and reproduced
the same five edge ids. The review also ruled out the harness's false-positive mode
structurally rather than by inspection: `internal/parity/run.go:248-270` applies the mutation
**once** and indexes **the same on-disk tree** on both sides, so a malformed planner would yield
a malformed tree that both passes see identically and would not, by itself, produce a
divergence.

### One near-miss, recorded because false findings are expensive

The first cut of the `rename_package` planner rewrote only non-test files, leaving cobra's
`doc/man_examples_test.go` declaring `package doc_test` and importing the **old** path. That
incomplete rename diverged — 938/3912 against 941/3916, full minting three external nodes plus
four edges — with PARITY-001's exact signature. **It was not a product defect.** It was the
harness manufacturing dangling references and then measuring them. Completing the rename made
the row converge, and it now PASSES. A divergence caused by the harness's own edit is the most
expensive kind of false positive an evidence gate can publish.

## 6. The three lifecycle rows — SW-158

Three FR-7 / Delta §9 requirements are **lifecycle events rather than content edits**, so none
of them is decidable by the change-class machinery: FR-7 `:824`'s `Branch-Wechsel`, and Delta
§9's `interrupted full pass` and `restart and recovery` (`:1068-1069`). They are driven by
`internal/parity/lifecycle.go`.

**What they are the complement of, and what they deliberately do not redo.**
`engine/ingest/faultmatrix_test.go` (SW-118) already kills the pipeline at every cross-DB
boundary and proves convergence to a never-crashed store's snapshot bytes, and
`docs/adr/0004-ingest-recovery-disposition.md:32-41` dispositions kill points K1–K8 on that
evidence. **That layer is settled and is not re-implemented here.** What the ADR does *not*
claim is real-process, real-repository coverage — it reserves it, at `:92-94`: *"ING-REWRITE
stays untriggered unless the EVAL-02 real-repo gates surface resource/recovery failures the
synthetic matrix cannot."* These rows are that reserved complement. **The crash is a real
`SIGKILL` to a real subprocess**, never an injected in-process fault, and each row cites the
ADR's own kill points rather than inventing parallel vocabulary.

### How the signal is aimed, and how we know where it landed

The harness may not add a product hook to make a kill easier — that would be a product-byte
change. The lever is the one the project's standards already require to exist (*"a slow index
must stay observable and interruptible"*): the binary emits `ingest.ProgressEvent` on its own
stream, and the harness kills the moment the pass announces the phase it is waiting for.

The mapping is read off `engine/ingest/ingest.go`'s emission order, so it is exact rather than
approximate — and it is **corroborated independently** by reading the crashed store *before
anything recovers it*:

| Kill point | Marker | ADR | What the crashed store held (cobra) |
|---|---|---|---|
| `parse` (full pass) | first parse milestone; the parse loop completes before the first `BeginBatch` at `:144` | **K1** — before any graph batch | **0 nodes / 0 edges** — nothing committed |
| `resolve` (full pass) | `PhaseResolve` at `:246`, after the WRITE commit `:200` and the LINK commit `:240` | **K3** — after the LINK batch commit, before TYPERESOLVE | **938 / 3555** — WRITE+LINK durable, the 361 typeresolve edges absent |
| `parse` (incremental) | `PhaseParse` at `:563`; the phase-1 dirty-mark tx committed at `:531-546`, `BeginBatch` still ahead at `:581` | **K5 → K7** | **938 / 3916** — the baseline, no graph write yet |
| `link` (incremental) | `PhaseLink` at `:699`, after the durable graph commit at `:665`, inside the still-open meta tx | **K6 → K7** | **939 / 3917** — the graph already **ahead** of the rolled-back meta state |

Those four store shapes are the evidence that the signal landed where the row says it did. The
progress stream says where the kill was *aimed*; the crashed store says what had actually
committed when it arrived, and they agree in every repetition of both dispatches.

**K2 is NOT claimed by these rows, and that is stated rather than glossed.** K2 is the window
between the WRITE batch commit (`:200`) and the LINK batch commit (`:240`). `IngestAll`
announces `PhaseLink` at `:186` — *before* the WRITE batch commits — and emits nothing further
until `PhaseResolve` at `:246`, well past the LINK commit. **No observable marker separates
those two commits from outside the process.** A signal aimed at `PhaseLink` would land somewhere
in the K1–K2 window, so publishing it as "K2" would be a probabilistic claim dressed as a
precise one. K2's coverage remains the synthetic `kill-before-batch-2` subtest, and this record
says so instead of quietly counting it.

### The restart row crosses a real process boundary, and the ingest lock proves it

`restart_and_recovery` is the **K7** seam — *"any crashed incremental followed by a session
open"*, the kill point that had **zero production callers** before SW-118 wired
`RecoverWithRoot` into `warmOrFullIngest`. `graphi sync` goes through `SyncRepo` →
`warmOrFullIngest`, which recovers *before* it trusts the store.

Every repetition probes the **real cross-process ingest lock** (`internal/ingestlock`,
`meta/ingest.lock.db` — the same package `internal/doctor/indexcheck.go:44` and
`cmd/graphi/status.go:167` probe with) from outside, twice: **`held` while the subprocess is
mid-pass, `free` after `SIGKILL` destroys it.** That is what makes this one journey across a
process boundary rather than two sequential invocations sharing a directory: the lock is OS
file-locking state that dies with the process, while the durable dirty rows survive it on
purpose. `held → free` reads in **12 of 12** killed repetitions per dispatch.

### The branch-switch row asserts the graph, not the announcement

`cmd/graphi/sync_test.go:33 TestRunSync_LifecycleAndBranchSwitch` rewrites `.git/HEAD` and
asserts **one stdout line** — `printBranchSwitch`'s announcement at `cmd/graphi/sync.go:165-169`.
No file content changes with that switch, so no graph delta exists and no full-vs-incremental
comparison is attempted. **This row changes the working tree for real**: it indexes at ref A,
runs `git checkout` to ref B, drives the **shipped verb** `graphi sync`, and asserts snapshot
bytes against a fresh full index at B. The announcement is still captured — as a *diagnostic* —
so the two claims are visibly not the same one.

**Both refs are recorded, and neither is invented.** Ref B is the manifest pin
`v1.8.0 @ a0a6ae020bb3899ff0276067863e50523f897370` (*"Improve API to get flag completion
function (#2063)"*); ref A is `890302a35f578311404a462b3cdd404f34db3720` (*"Support usage as
plugin for tools like kubectl (#2018)"*), selected by a deterministic rule — **the nearest
ancestor of the pin whose diff to the pin touches at least one `.go` file**. Local branch names
are created *at* those two existing upstream commits; **nothing is committed into the clone**,
because inventing history would make the row unreproducible.

### Results, as the whole sample

Every lifecycle row publishes **each repetition**, not a summary. That is not ceremony: §5 of
this record already established that a stable *verdict* can sit on top of a *varying
measurement*, so one green execution is not evidence of convergence.

| Row | Kill points × repetitions | Verdict | Distinct incremental snapshots | Distinct full snapshots |
|---|---|---|---|---|
| `branch_switch` | — × 3 | **PASS** | 1 | 1 |
| `interrupted_full_pass` | K1, K3 × 3 each = 6 | **PASS** | 1 | 1 |
| `restart_and_recovery` | K5→K7, K6→K7 × 3 each = 6 | **PASS** | 1 | 1 |

**30 lifecycle journeys across the two dispatches, and every one converged to the byte.** Both
dispatches produced identical digests for both sides of all three rows. **A row FAILs if any
single repetition diverges** — there is no majority rule and no retry.

### A control separates recovery from PARITY-002

`restart_and_recovery` must drive an incremental pass, and §5 filed PARITY-002: `graphi sync`
re-linking any file can settle a different `imports` edge set from `rebuild`. A divergence here
would therefore have been ambiguous between a recovery defect and that already-filed one. So
every repetition also runs **the identical journey with no kill** — baseline, same edit, one
uninterrupted `sync` — and compares. The crashed-and-recovered graph is **byte-identical to the
uninterrupted control** (`f8ffcf0dd1cb0932…`) in every repetition, so recovery is transparent on
this row. The control **diagnoses and never decides**; the verdict is always the snapshot bytes
against the fresh full index.

### Coverage limits — stated, because a limit that is not published is not a limit

1. **K2 has no real-process coverage here** (above). It remains synthetically covered.
2. **The lifecycle rows run on cobra, at every tier cap.** The selection rule is the smallest
   in-cap repository, because a lifecycle row tests the *process* and has no source structure to
   go looking for — which is also what keeps `-max-tier 1` meaningful for AC-11. **The cost is
   that these rows do not exercise PARITY-002's re-link divergence**, which §5 observed on gin
   and grpc-go and explicitly *not* on cobra. Their PASS is a statement about the lifecycle
   journeys, not a counter-example to PARITY-002.
3. **On a platform with no faithful `SIGKILL`, the two signal rows do not run.** They are then
   recorded as `SKIPPED` **with the platform and reason in `coverage_limits`**, and the run is
   `INCOMPLETE` and **refuses to publish** — disclosure costs what it should, so "record the
   limit" cannot become the cheap way past the gate. This dispatch ran on `darwin/arm64`, where
   both rows executed; `coverage_limits` is empty in both reports.
4. **The rows prove the observable property, not a named internal mechanism.** They prove that a
   real crash followed by a real restart converges to a fresh full index's bytes. They do not
   isolate *which* internal path did the healing — `cmd/graphi/zeroconfig_recovery_test.go:52`
   is the test that constructs the specific K7 divergence the drift pass cannot see.

### What this means for ADR 0004's `ING-REWRITE` trigger

ADR 0004's stopping rule says `ING-REWRITE` *"stays untriggered unless the EVAL-02 real-repo
gates surface resource/recovery failures the synthetic matrix cannot."* **These rows surfaced
none.** That is recorded here as evidence bearing on the trigger, and **nothing is acted on**:
extending or amending ADR 0004 is a separate, deliberate act, not a side effect of a green row.

## 7. What this does **not** compare

A "100% parity" line with no stated scope is an overclaim. The comparison unit is the portable
snapshot envelope, so anything persisted **outside** `model.Graph` is invisible to it:

- intra-process taint findings
- embeddings / vectors
- `index.profile` metadata
- the ingest-meta sidecar
- the FTS index — deliberately not stored, re-derived on load
  (`core/graphstore/snapshot.go:49-51`)

That state is **already dispositioned DOCUMENTED HARMLESS (Labs tier)** at kill point K4,
`docs/adr/0004-ingest-recovery-disposition.md:37`. This record cites that disposition and does
not reopen it: extending the envelope would bump `SnapshotFormatVersion`, a product-byte change.

Two further scope limits, stated so they are not discovered later:

- **`change_build_tag` is degenerate.** No build-constraint evaluation exists anywhere in
  graphi, so a `//go:build` edit is a comment-line content change to it. The row proves parity
  over the change and **nothing** about build-tag semantics.
- **`stale linker edges = 0` counts dangling endpoints only** — see §3.

## 8. Provenance

**Product source byte-identical to v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`.**

The run did **not** happen *at* the candidate, and no sentence here says it did: the harness
does not exist at `80d67ed`. Both SHAs are recorded in every report.

The claim is verified **mechanically, by the built binary**, not by a path diff:
`go build -trimpath -buildvcs=false ./cmd/graphi` is built at the run SHA and at the candidate
(materialized with `git worktree`) and the two sha256 digests compared. `-trimpath` is what
makes that comparison meaningful across two build directories — without it the absolute source
path lands in DWARF `comp_dir` and two identical trees hash differently. Both reports carry
`product_binary_head` and `product_binary_candidate`.

This matters on this branch specifically: the path diff against `80d67ed` is **not** empty,
because the SW-157 parent commit edited `engine/conformance/doc.go`. `engine/conformance` is a
test-only package that `cmd/graphi` does not link, so the **product binary is unchanged** — and
the binary comparison says so, where the path diff alone would have refused publication for a
doc comment.

**Publication fails closed** on a dirty worktree, a differing product binary (or an
unverifiable one), a missing runner class, a manifest pin mismatch, or an incomplete run. Each
refusal is recorded in `not_publishable_because` with its reason.

**Two dispatches with identical verdict sets** are required, applying §12.4's two-green-runs
discipline to a reliability gate. Compare with
`go run ./cmd/parity -verdict-diff run-a.json,run-b.json`. The comparison is over **verdicts**,
not report bytes: two dispatches legitimately differ in timestamps and durations. **A row that
differs between two otherwise-identical dispatches is an environment finding to be explained,
never a flake to be retried away** — and **§5 is where that obligation is discharged**:
`replace_generated_file`'s incremental edge count differs between run-a and run-b, and §5
records the whole sample rather than reconciling it. The cross-reference runs both ways so
neither half can be read without the other.

## 9. Reproducing it

```bash
# The whole matrix (clones cobra, lo, uuid, gin, grpc-go).
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class ubuntu-latest -report parity-report.json

# Cheapest path: cobra only. Completes on a developer machine.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -report parity-local.json

# One row, for iteration.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 -classes delete_file

# The three lifecycle rows, each locally runnable on the cheapest tier (cobra).
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes branch_switch
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes interrupted_full_pass
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes restart_and_recovery

# How many times each lifecycle journey runs per kill point (default 3). This can
# only ever ADD executions to the sample: a row FAILs if ANY repetition diverged,
# so it can never retry a row into green.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 \
  -classes restart_and_recovery -lifecycle-repeat 5

# Two dispatches must agree before anything is published.
go run ./cmd/parity -verdict-diff run-a.json,run-b.json
```

The workflow needed **no change** for the lifecycle rows: they are declared rows of the same
matrix, so a plain dispatch runs them.

Tier 4 (kubernetes, ~3 min index at ~9 GB peak RSS) is **excluded by construction**, not by
configuration: `internal/parity.MaxSupportedTier` clamps the cap at 3, so no flag value,
environment variable or workflow input can pull it in. It is SW-145's subject and needs a named
machine.

The workflow (`.github/workflows/parity.yml`) runs on `workflow_dispatch` and a nightly
schedule, **never on a pull request**. The hermetic `internal/parity` tests carry the harness
logic into the PR gate via `go run ./cmd/testgate`; the real-repo matrix is the evidence. A
hermetic test that clones is not hermetic, and a matrix row that runs on a fixture is not
evidence.
