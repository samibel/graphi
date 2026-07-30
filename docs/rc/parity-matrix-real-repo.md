# Full/incremental parity matrix over pinned real repositories — SW-144

**Status: PUBLISHED FAIL.** 10 of 14 executed change classes PASS; **4 FAIL**, accounted for
by **two product defects**, both filed and neither fixed.

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, `.github/workflows/parity.yml` |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`** |
| Matrix source | `docs/rc/parity-classes.yaml` (15 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-run-a.json`, `docs/rc/parity-matrix-run-b.json` |

---

## 1. What this is, and the two things it is not

It is the first reading of PRD §12.3's full-vs-incremental parity gate **on real Go**. Every
parity proof in the tree before it ran over a `t.TempDir()` fixture.

**It is not the whole of checklist row 13.** Row 13 is satisfied **only by SW-144 *and*
SW-158 together** (adopted decision 4). The recovery, crash-injection and branch-switch rows
are SW-158's; they are declared `harness_row: deferred` in the matrix source and appear here
as DEFERRED, not as passes. **Neither story alone may be recorded as "SW-144 done."**

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
| `branch_switch` | *DEFERRED* | — | SW-158 |
| `change_build_tag` | **FAIL** | gin | inc 1890/6605 vs full 1890/6602 — **PARITY-002** |
| `replace_generated_file` | **FAIL** | grpc-go | inc 14898/69939 vs full 14898/69772 — **PARITY-002** |
| `change_external_import` | **PASS** | cobra | |
| `interrupted_full_pass` | *DEFERRED* | — | SW-158 (crash condition, not a change class) |
| `restart_and_recovery` | *DEFERRED* | — | SW-158 (crash condition, not a change class) |

**Four failing rows, two defects.** The three PARITY-002 rows are one defect surfacing three
times, not three defects — see §5.

### The two §12.3 store-level counts

**Orphaned external nodes = 0** and **stale linker edges = 0** on every executed row, over the
real repository graph after that row's change sequence — counted from the same envelope the
assertion compared, not inferred from the fixture-level proofs at
`engine/ingest/link_external_lifecycle_e2e_test.go:29` and `link_cascade_test.go:118`.

**Read that with §5's scope limit.** A stale linker edge here means an edge whose endpoint is
**not a node in the graph**. PARITY-002's extra edges have valid endpoints, so they are
invisible to this counter. Zero means *no dangling endpoints*; it does not mean *no edges a
full pass would have recomputed away*.

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

**Modifying an existing file in a multi-package repository leaves `graphi sync` carrying
`imports` edges that `graphi rebuild` does not produce.** Filed in `projects/graphi/backlog.md`.

Node counts are identical on both sides in every instance; **every diverging edge is
`kind: "imports"`**, and no other edge kind and no node ever differs.

**It is one defect, not three, and this is the load-bearing evidence.** The three gin rows are
three different files and three different edit shapes — appending a function to `auth.go`,
deleting method `Context.Status` in `context.go`, editing a `//go:build` comment in
`context_appengine.go` — yet they produce a **byte-identical `BranchDiffReport`**, down to the
same five edge ids. One identical delta from three unrelated edits means the divergence belongs
to the repository plus an incremental re-link, not to any change class. The matrix reports it
as three rows only because the matrix is organised by class.

**The trigger is modification, not `sync` itself.** A control run pins the boundary: `add_file`
on gin **converges**, while `modify_file` on the same clone diverges. cobra converges on
`modify_file` too, so package depth matters as well — cobra has 2 packages, gin 7, grpc-go 277
directories containing Go source, where the divergence reaches **167 extra `imports` edges**.

**It is not PARITY-001:** opposite direction (incremental carries *more*), different edge kind,
different trigger.

**Where to look first, and this is not a root cause.** `imports` edges are package-level while
the incremental unit of work is the file, so a package's import edge set must be recomputed
from a set of files of which only some were re-parsed. SW-144's scope was to prove the
divergence, publish it and file it. PARITY-001's first stated cause was wrong in two ways and
had to be corrected in review; this record therefore stops at what was observed.

### One near-miss, recorded because false findings are expensive

The first cut of the `rename_package` planner rewrote only non-test files, leaving cobra's
`doc/man_examples_test.go` declaring `package doc_test` and importing the **old** path. That
incomplete rename diverged — 938/3912 against 941/3916, full minting three external nodes plus
four edges — with PARITY-001's exact signature. **It was not a product defect.** It was the
harness manufacturing dangling references and then measuring them. Completing the rename made
the row converge, and it now PASSES. A divergence caused by the harness's own edit is the most
expensive kind of false positive an evidence gate can publish.

## 6. What this does **not** compare

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

## 7. Provenance

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
never a flake to be retried away.**

## 8. Reproducing it

```bash
# The whole matrix (clones cobra, lo, uuid, gin, grpc-go).
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class ubuntu-latest -report parity-report.json

# Cheapest path: cobra only. Completes on a developer machine.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -report parity-local.json

# One row, for iteration.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 -classes delete_file

# Two dispatches must agree before anything is published.
go run ./cmd/parity -verdict-diff run-a.json,run-b.json
```

Tier 4 (kubernetes, ~3 min index at ~9 GB peak RSS) is **excluded by construction**, not by
configuration: `internal/parity.MaxSupportedTier` clamps the cap at 3, so no flag value,
environment variable or workflow input can pull it in. It is SW-145's subject and needs a named
machine.

The workflow (`.github/workflows/parity.yml`) runs on `workflow_dispatch` and a nightly
schedule, **never on a pull request**. The hermetic `internal/parity` tests carry the harness
logic into the PR gate via `go run ./cmd/testgate`; the real-repo matrix is the evidence. A
hermetic test that clones is not hermetic, and a matrix row that runs on a fixture is not
evidence.
