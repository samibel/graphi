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
| `replace_generated_file` | **FAIL** | grpc-go | full 14898/69772 (stable); inc 14898/**69939 (run-a)**, **69940 (run-b)** — **PARITY-002, and the incremental edge count is NON-DETERMINISTIC: see §5** |
| `change_external_import` | **PASS** | cobra | |
| `interrupted_full_pass` | *DEFERRED* | — | SW-158 (crash condition, not a change class) |
| `restart_and_recovery` | *DEFERRED* | — | SW-158 (crash condition, not a change class) |

**Four failing rows, two defects.** The three PARITY-002 rows are one defect surfacing three
times, not three defects — see §5.

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

**It is disclosed here because this record's own standard demands it.** §7 says a row that
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

Which representative the linker lands on depends on what the index contains when it runs, and a
re-link sees a different index than a cold pass. **Neither side is absolutely right** — but
`rebuild` is the reference by definition, so `sync` is what diverges.

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
