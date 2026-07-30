# The FR-7 parity class matrix — what it must contain, and what is already proven

**Story:** SW-156 · **Spec:** `projects/graphi/specs/p0-d-reliability-parity-matrix.md` ·
**Written:** 2026-07-30 · **Read at:** `main` @ `d9dadf035a00cd3be4ad7cdb93e524a2728386c1`

**Machine-readable companion:** [`parity-classes.yaml`](parity-classes.yaml). That file is
what code binds to; this file carries the reasoning. When the two disagree, the YAML is the
source of truth for `id`, `kind`, `verdict` and citation, and this file is the bug.

> **Amended 2026-07-30 after independent review, round 1.** All 17 verdicts were independently
> re-derived and **none changed** — no false PROVEN, no false ABSENT. Seven corrections landed,
> all of them to *reasoning, precision or field values*, none to a verdict: **M1** a false
> claim about `gopkg.in/yaml.v3` (*Finding 14*); **m2** a store field naming a store the test
> never opens (*Finding 11*); **m3** an over-general claim that no generated-file detection
> exists (*Finding 12*); **m4** a drift-guard contract that did not bind `verdict`
> (*Finding 13*); **m5** an `owner`/`harness_row` contradiction (*Finding 13*); **m6** two
> crash rows claiming `store: both` against their own cited primaries (*Finding 8*); **m7** a
> miscount of uninventoried proofs (*Finding 5*). Each correction is recorded in place with
> what the earlier version said and why it was wrong, rather than silently overwritten — the
> same standard this document applies to the documents it audits.

## Provenance — stated honestly, because the distinction matters

> **Product source is byte-identical to v0.7.1, the candidate of record at
> `80d67ed586723ab22704cf7aada316138cb1360e`.** This inventory was *read* at `main` @
> `d9dadf035a00cd3be4ad7cdb93e524a2728386c1`, which differs from the candidate only in
> project data, docs and test-gate wording. Both SHAs are recorded because neither alone is
> the honest answer.

This document **asserts nothing and runs nothing new.** It is an inventory of assertions that
already exist. It is **not** the PRD §12.3 reliability gate and may not be reported as one —
that gate needs SW-144 and SW-158 together. What it establishes is what the matrix must
contain and which of its rows are already paid for.

## How to read the row count mechanically

`grep -c '^| ' docs/rc/parity-class-matrix.md` returns **exactly 17**.

That is arranged, not accidental, and the device is declared here rather than left as a
trap: the 17 matrix data rows are the only lines in this file that begin with a pipe
followed by a space. Every table header, every separator and every non-matrix table uses the
compact `|cell|cell|` form, which renders identically in Markdown and does not match. If you
add a row to a findings table below, keep it compact or the count breaks.

## The 15-vs-16 resolution

Three documents describe this matrix and they did not agree. The disagreement is itself a
finding, and **16 is a miscount with no document behind it.**

|Source|Says|Verdict|
|---|---|---|
|`docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md:813-827`|**15** bullets under `#### Änderungsklassen` (heading `:809`), prefixed `Mindestens:`|**Authoritative.** FR-7 is the named authority for this matrix|
|same, `:807`|prose: *"die vollständige **15-Klassen-Matrix** ist noch offen"*|**Independently confirms 15.** The PRD names its own figure, so this is not a bullet-counting inference|
|`docs/plan/2026-07-graphi-p0-completion-delta-prd.md:1053-1069`|**17** bullets under `### Required change classes` (heading `:1051`)|**Correct, and mis-headed.** The 17 are FR-7's same 15 **plus** `interrupted full pass` (`:1068`) and `restart and recovery` (`:1069`) — which are **crash conditions, not change classes.** The Delta's heading is what invites the conflation|
|`projects/graphi/backlog.md:32`|"the 15-class full-vs-incremental change matrix"|**Correct**|
|`projects/graphi/backlog.md:55`|"16 change classes"|**Wrong. A miscount, and not a defensible reading.** 16 is neither FR-7's 15 nor the Delta's 17. It is what you get by treating the Delta's 17-bullet list as one homogeneous set of change classes and then losing one. Corrected in place under spec decision 6|

**The matrix is 17 rows: 15 change classes (PRD FR-7) + 2 crash conditions (Delta §9).**

The two kinds stay **visibly distinct** in two independent ways — separate tables below, and a
`kind` field on every YAML row that the SW-157 drift guard checks. Counting crash conditions
among the change classes is exactly how 15 became 16, so the shape now makes that
unrepresentable rather than merely discouraged.

## Verdict discipline — the load-bearing rule

- **PROVEN** only where the cited primary test asserts **snapshot-bytes** or
  **envelope-bytes** equality.
- **PARTIAL** where the class-covering proof is a **spot query** or a presence/absence check.
  The substitution is named in the row. *A substituted assertion is never recorded as the
  original.*
- **ABSENT** where nothing proves the class.

Two refinements, stated up front because they decided several rows:

1. **A test that proves a *different property* is not a weak proof of this one.** It is
   recorded as an adjacent citation and the row reads ABSENT. `branch_switch` is the case:
   `cmd/graphi/sync_test.go:33` proves an *announcement*, not parity, so the row is ABSENT
   rather than PARTIAL. Marking it PARTIAL would have implied partial parity evidence, and
   there is none.
2. **Scenario incompleteness inside a byte-proven class does not downgrade the verdict; it is
   recorded as a residual.** `change_interface` byte-proves adding an interface embed and
   says so; removing one is a named residual, not a reason to call the byte proof PARTIAL.
   The verdict answers *"is the property proven by bytes?"* — the residual answers *"is it
   proven exhaustively?"*, which is SW-157's brief.

Where a class has genuinely separate *mechanisms* rather than merely separate scenarios and
only one is byte-proven, the row is **PARTIAL** — `add_implementation` is that case.

## Table A — the 15 change classes (PRD FR-7)

|Class|Kind|FR-7 · Delta|Verdict|Proof — test name @ `file:line`|Fixture|Store|Assertion|Why this verdict|
|---|---|---|---|---|---|---|---|---|
| `add_file` | change class | `:813` · `:1053` | **PROVEN** | `TestFullVsIncremental_ByteParity` @ `engine/conformance/conformance_test.go:134` | production Go parser | MemStore | snapshot bytes | Steps 0–2 add `a/a.go`, `b/b.go`, `c/c.go`, `nb.ipynb`; watcher-driven incremental vs full, snapshot bytes. Second independent proof: `TestLink_GoldenIncrementalVsFull_RealGo` @ `engine/ingest/link_e2e_test.go:43` adds a *linked* file |
| `modify_file` | change class | `:814` · `:1054` | **PROVEN** | `TestFullVsIncremental_ByteParity` @ `engine/conformance/conformance_test.go:134` | production Go parser | MemStore | snapshot bytes | Step 1 rewrites `a/a.go` (helper body + a new `Extra`). Also byte-proven by `link_e2e_test.go:43`; supported at field-dump strength by `TestTyperesolve_FullVsIncrementalByteParity` @ `engine/ingest/typeresolve_test.go:167` |
| `delete_file` | change class | `:815` · `:1055` | **PROVEN** | `TestFullVsIncremental_ByteParity` @ `engine/conformance/conformance_test.go:134` | production Go parser | MemStore | snapshot bytes | Step 3 removes `b/b.go`. Harder sibling proofs: `link_e2e_test.go:43` deletes `util/util.go`; `TestLink_GoExternalNode_InterningLifecycle` @ `link_external_lifecycle_e2e_test.go:29` deletes the *sole referencer* of an interned external node |
| `rename_symbol` | change class | `:816` · `:1056` | **PROVEN** | `TestLink_CascadeOnly_CrossPackageCalleeRename` @ `engine/ingest/link_cascade_test.go:30` | production Go parser | MemStore | snapshot bytes | The strongest available form: a cross-package callee rename where **only the callee file is reported**, so `dependentsOf` must cascade the importer in. This was a real shipped defect (BLOCK-1). Same-package cross-file rename at `link_e2e_test.go:43`; five non-Go languages at `link_fu5_e2e_test.go:32`. `TestLink_RenameOfTarget_OldEdgeAbsent` @ `link_e2e_test.go:218` is a **spot-query** substitute and is *not* the basis of this verdict |
| `move_symbol` | change class | `:817` · `:1057` | **ABSENT** | — | — | — | — | **Genuinely zero coverage.** Nothing relocates a declaration between files or packages. See *Finding 6* for the specific hazard this leaves untested |
| `rename_package` | change class | `:818` · `:1058` | **ABSENT** | — | — | — | — | **Genuinely zero coverage.** Nothing changes a `package` clause. The two nearest neighbours are *not* this class: `TestTyperesolve_GoModChangeParity` @ `typeresolve_test.go:220` renames the **module path**, and `TestLink_SameClauseDifferentDir` @ `link_e2e_test.go:286` has two same-clause directories but performs no rename. See *Finding 7* |
| `add_call` | change class | `:819` · `:1059` | **PROVEN** | `TestLink_GoldenIncrementalVsFull_RealGo` @ `engine/ingest/link_e2e_test.go:43` | production Go parser | MemStore | snapshot bytes | `shop/extra.go` arrives carrying `extra() → cost()`, a new `calls` edge, and byte parity holds **including tier, confidence, reason and evidence**. `typeresolve_test.go:167` adds calls at the *confirmed* tier but compares a graph field dump (*Finding 9*), so it supports rather than establishes |
| `remove_call` | change class | `:820` · `:1060` | **PROVEN** | `TestLink_CascadeOnly_IdentityPreservingCallerDrop` @ `engine/ingest/link_cascade_test.go:118` | production Go parser | MemStore | snapshot bytes | The hard form: the caller drops the call but **keeps its identity**, so `DeleteNode` never cascades the edge and only the stale-edge sweep can converge. Doubles as the FR-7 *"no stale linker edges"* proof. Was a real shipped defect (BLOCK-2) |
| `change_interface` | change class | `:821` · `:1061` | **PROVEN** | `TestLink_HierarchyGoldenIncrementalVsFull` @ `engine/ingest/hierarchy_e2e_test.go:139` | production Go parser | MemStore | snapshot bytes | **Corrects the spec, which predicted ABSENT.** The test declares a new interface `Added`, adds it to `Collector`'s embed set, and asserts incremental-vs-full snapshot bytes including `implements`/`inherits` provenance, on the production parser. Residual (not a downgrade): the body only **adds** an embed — removal and method-**signature** change are untested, and the test's own comment at `:135` says *"adds/removes"*, which overstates the body |
| `add_implementation` | change class | `:822` · `:1062` | **PARTIAL** | `TestResolve_ImplementsProven` @ `engine/typeresolve/check_test.go:207` | production Go parser | **none** | **spot query** | Two *mechanisms* mint `implements` and only one is byte-proven. **Syntactic embedding** (`core/parse/extract_go.go:33`) is covered incrementally at snapshot-byte strength by `hierarchy_e2e_test.go:139`, on MemStore. **Method-set satisfaction proven by `go/types`** (`engine/typeresolve/check.go:498`) is covered *only* by the cited test, which runs **one full check** and asserts an exact *set* of derived edge strings — no incremental comparison, no store snapshot. `store: none` is literal: `engine/typeresolve` contains **zero** graphstore usage, so the cited test opens no store at all (*Finding 11*). **Substitution named:** an exact-set presence assertion standing in for inc-vs-full byte parity |
| `remove_implementation` | change class | `:823` · `:1063` | **ABSENT** | — | — | — | — | Nothing removes an implementation by **either** mechanism: no test drops an interface embed, and none removes or breaks a method so a concrete type *stops* satisfying an interface. Removal is the direction that can leave a stale edge, which makes this the more valuable half of the pair |
| `branch_switch` | change class | `:824` · `:1067` | **ABSENT** | — | — | — | — | ABSENT, not PARTIAL, deliberately. `TestRunSync_LifecycleAndBranchSwitch` @ `cmd/graphi/sync_test.go:33` rewrites `.git/HEAD` and asserts **one stdout line** — `Branch switch detected: main → feature/login` (`cmd/graphi/sync.go:169` `printBranchSwitch`). No file *content* changes with the switch, so no graph delta exists and no full-vs-incremental comparison is attempted. Needs a real git repository → **SW-158** |
| `change_build_tag` | change class | `:825` · `:1064` | **ABSENT** | — | — | — | — | ABSENT **and degenerate** — see *Finding 3*. No test edits a `//go:build` line, and there is no build-constraint evaluation to test. The class **stays** in the matrix (spec decision 5) |
| `replace_generated_file` | change class | `:826` · `:1065` | **ABSENT** | — | — | — | — | No test replaces a generated file. Generated-file detection **does** exist in the tree, but not on any path that can affect parity — see *Finding 12*, which corrects an over-general claim in this row's first version. Within the ingest/graph path the class has no special-casing, so unlike `change_build_tag` it is **not** degenerate: it is a genuine large-diff, high-symbol-count stress on the ordinary modify path. Hermetic row → **SW-157**; the real-source instance → **SW-144** (grpc-go, 49 `DO NOT EDIT` files per `corpus/manifest.json`) |
| `change_external_import` | change class | `:827` · `:1066` | **PROVEN** | `TestLink_GoExternalNode_InterningLifecycle` @ `engine/ingest/link_external_lifecycle_e2e_test.go:29` | production Go parser | MemStore | snapshot bytes | Three subtests, each snapshot-byte inc-vs-full: a shared interned external node **survives** a sibling delete; an orphan is **swept** when its sole referencer is deleted (removing the import line outright); and swept when the sole referencer merely **stops** referencing it (`os.ReadFile` → `os.Getenv`). Doubles as the FR-7 *"no orphaned external nodes"* proof. Residual: no subtest **adds or swaps** an import path to a different external package, and the nearest proof that import *resolution* changes converge — `typeresolve_test.go:220` — compares a field dump (*Finding 9*) |

## Table B — the 2 crash conditions (Delta §9 only)

These are **not** change classes. Neither appears among FR-7's 15 bullets; the underlying
property is FR-7's *acceptance criterion* at `:834` (*"Crash Recovery konvergiert zum
gleichen finalen Zustand"*). They are listed here because the Delta PRD's SW-144 bullet list
requires them, and they are kept in a separate table because merging them into Table A is
precisely the arithmetic that produced "16".

|Condition|Kind|FR-7 · Delta|Verdict|Proof — test name @ `file:line`|Fixture|Store|Assertion|Why this verdict|
|---|---|---|---|---|---|---|---|---|
| `interrupted_full_pass` | crash condition | none · `:1068` | **PROVEN** | `TestFaultMatrix_FullPass_KillAtEveryBatchBoundary` @ `engine/ingest/faultmatrix_test.go:160` | synthetic stub parser | MemStore | snapshot bytes | Kills `IngestAll` at **all three** graph-batch boundaries, **mutates the tree between crash and retry**, and asserts snapshot-byte convergence to a fresh index; a guard subtest fails loudly if `IngestAll` ever grows a fourth batch. The cited test wraps a passthrough **MemStore**; real-SQLite coverage of the same condition is a *sibling* test, `TestFaultMatrix_FullPass_SQLiteCloseReopen` @ `:231` — see *Finding 8*. Dispositioned K1–K3 in `docs/adr/0004-ingest-recovery-disposition.md:34-36`. Residual: in-process fault injection, never a real process kill → **SW-158**, which that ADR reserves it to at `:92-94` (*"ING-REWRITE stays untriggered unless the EVAL-02 real-repo gates surface resource/recovery failures the synthetic matrix cannot"*); its own residual scope is at `:72-82` |
| `restart_and_recovery` | crash condition | none · `:1069` | **PROVEN** | `TestFaultMatrix_Incremental_KillAfterDurableGraphWrite` @ `engine/ingest/faultmatrix_test.go:490` | synthetic stub parser | MemStore | snapshot bytes | K6: a durable graph write lands, the meta transaction rolls back, and replaying the durable dirty rows must converge byte-identically. Reinforced by `TestFullPassGeneration_GraphFileRevert` @ `:357` on SQLite, and by `TestWarmOrFullIngest_ReplaysDirtyUnitsBeforeTrustingTheStore` @ `cmd/graphi/zeroconfig_recovery_test.go:52`, which constructs the **K7 divergence the drift pass cannot see** (crash between a durable delete and the re-put, then revert the source so disk == cache) and compares snapshot bytes against an uninterrupted reference. K1–K8 in `docs/adr/0004-ingest-recovery-disposition.md:34-41`. `TestIngest_CrashRecovery` @ `engine/ingest/ingest_test.go:137` is a **spot-query** substitute — it asserts a re-parse *count* of 1 — and is *not* the basis of this verdict. Residual: real process boundary → **SW-158** |

## Tally

|Kind|PROVEN|PARTIAL|ABSENT|Total|
|---|---|---|---|---|
|change classes (FR-7)|8|1|6|**15**|
|crash conditions (Delta §9)|2|0|0|**2**|
|**matrix**|**10**|**1**|**6**|**17**|

Nine of the fifteen change classes are PROVEN-or-PARTIAL, and the two with genuinely zero
coverage are `move_symbol` and `rename_package` — the shape the spec predicted. The
**membership** of the ABSENT set is not the shape the spec predicted; see *Finding 5*.

## Findings

### Finding 1 — Snapshot parity already subsumes FR-7's enumerated comparison scope

FR-7 `:832` asks for comparison over *"Nodes, Edges, Evidence, Confidence, IDs und relevante
Metadaten"*, and Delta `:1075-1083` adds source anchors, external node cleanup and stale
linker edge cleanup. Byte parity over `model.Graph.Marshal` (`core/model/serialize.go:122`)
is **strictly stronger** than that enumeration:

- `Marshal` sorts nodes by `NodeId` and edges by `EdgeId` and emits indentation-free
  canonical JSON, so two snapshots of the same logical state are byte-identical.
- `edgeWire` (`core/model/serialize.go:64-73`) carries `id`, `from`, `to`, `kind`,
  `confidence_tier`, `confidence`, `reason`, `evidence` — the whole Evidence/Confidence/IDs
  conjunct, per edge.
- `nodeWire` (`:50`, populated by `Node.toWire` at `:82`) carries `id`, `kind`,
  `qualified_name`, `source_path`, `line`, `column`, `meta` — the IDs *and* the source
  anchors Delta `:1080` adds.

**Consequence, and this is the point of recording it:** the enumerated fields are **not
separate work.** "Compare evidence and confidence" must not be re-implemented as a
field-by-field walk in SW-157 or SW-144. One `bytes.Equal` over the snapshot covers the
entire enumeration and cannot be made to pass by a walk that forgot a field.

### Finding 2 — The snapshot's blind spot, already dispositioned

Snapshot parity is not total. The FTS index is deliberately not stored
(`core/graphstore/snapshot.go:51-52`: *"The FTS index is intentionally NOT stored: it is
re-derived on load"*), and **anything persisted outside `model.Graph` is invisible to
snapshot parity** — intra-process taint findings, embeddings/vectors, `index.profile`
metadata, and the ingest-meta sidecar.

`docs/adr/0004-ingest-recovery-disposition.md:37` already dispositions exactly that state.
Kill point **K4** — crash after the meta commit, before taint persist / profile metadata /
WAL checkpoint — is recorded as **DOCUMENTED HARMLESS (Labs tier)**: the graph the 12 stable
ops read is complete and stamped; stale taint findings affect only the Labs `analyze taint`
readout and self-heal on the next full pass; `index.profile` is cosmetic; a skipped WAL
TRUNCATE is reclaimed by SQLite normally.

**This document cites that disposition and does not reopen it.** Extending the snapshot
envelope to cover taint, vectors or profile metadata would bump `SnapshotFormatVersion`
(`core/graphstore/snapshot.go:20`) — a product-byte change, out of slice in every story here.

**Citation correction.** The spec and SW-156's AC-8 both place the "FTS index is not stored"
claim at `core/graphstore/snapshot.go:49-51`. It is at **`:51-52`**, inside the
`snapshotEnvelope` doc comment that runs `:47-52`. The claim is correct; the line numbers
were two off, and this file carries the checked ones.

### Finding 3 — The build-tag class is degenerate, and every row carrying it must say so

**Nothing in graphi evaluates a build constraint.** Checked, not assumed:

```
grep -rn "go:build\|BuildConstraint\|build.Context\|MatchFile" --include="*.go" \
  engine/ingest engine/typeresolve core/parse | grep -v "_test.go"
→ core/parse/broad.go:1://go:build graphi_broad
```

One hit, and it is graphi's **own** build tag on graphi's **own** file — not evaluation of
ingested source. Corroborating:

- `engine/typeresolve/pkggraph.go:85` `GroupPackages` groups by *(directory, package clause)*
  from already-read bytes with `parser.ImportsOnly`, skips `_test.go`, and degrades a
  directory carrying multiple non-test clauses. It never looks at `//go:build`.
- `engine/typeresolve/doc.go:24` and `check.go:20` state the constraint that forecloses it:
  *"stdlib `go/types` ONLY — no `golang.org/x/tools`, no `go/packages`, no exec, no
  network"*. `go/packages` is where constraint evaluation would come from.

To graphi, a build-tag change is a **comment-line content change**, and parity over it holds
trivially. The class **stays** in the matrix (spec decision 5): FR-7 lists it, and dropping a
listed class to make the matrix look more meaningful is exactly the substitution this
programme forbids. But **every row that carries it must state what it does and does not
prove** — it proves parity over a comment-line edit and **nothing** about build-tag
semantics. Making graphi honour `//go:build` is a real product feature with a real design
cost; this document records the degeneracy and does not schedule the feature.

### Finding 4 — `internal/evalreport` is forbidden, and the mechanism is specific

`internal/evalreport/freshness.go:59` defines:

```go
var RequiredChangeClasses = []string{
	ChangeClassAdd, ChangeClassModify, ChangeClassDelete, ChangeClassCrossPackage,
}
```

and `cmd/eval/changeseq.go:39` derives from it:

```go
var changeSequenceCycle = len(evalreport.RequiredChangeClasses)
```

The comment above that line says it plainly: the cycle length is *"derived from the
required-class list rather than written as 4, so adding a class to AC-2's set cannot leave
the cycle length behind."* That coupling is a feature for `cmd/eval` and a trap for this
slice. **Adding a parity class to `RequiredChangeClasses` reshapes the change sequence
`cmd/eval` generates** for the freshness and update distributions — an *instrument* change,
not a product change, and precisely the situation the WP4 evidence row's own text names as
*"identical product bytes measured by a DIFFERENT instrument"* not being the same numbers. It
would invalidate the baseline SW-143 is waiting to run.

**Therefore: `internal/evalreport` non-test source is off-limits to every story in this
slice, and the parity harness keeps its own class list in its own file** — this matrix's
`docs/rc/parity-classes.yaml`, read by `internal/parity`, never by `internal/evalreport`.
The two lists have different jobs and must be allowed to diverge.

### Finding 5 — The seeded inventory's ABSENT set had the right size and the wrong membership

The spec seeded this story with an expected floor of six uncovered items:
`move symbol`, `rename package`, `change interface`, `add implementation`,
`remove implementation`, and idempotency. Checking rather than trusting it changed the
membership while leaving the count intact — which is why the floor was written as *"a floor
to check, not a conclusion to encode."*

|Item|Spec expected|Found|Basis|
|---|---|---|---|
|`move symbol`|ABSENT|**ABSENT** ✓|confirmed by exhaustive search of every test that calls `IngestChanged`|
|`rename package`|ABSENT|**ABSENT** ✓|confirmed; the two nearest tests prove different things|
|`change interface`|ABSENT|**PROVEN** ✗|`engine/ingest/hierarchy_e2e_test.go:139` — an inc-vs-full **snapshot-bytes** proof over an interface change on the production parser. The spec's inventory never visited `hierarchy_e2e_test.go`|
|`add implementation`|ABSENT|**PARTIAL** ✗|the embedding mechanism is byte-proven at `hierarchy_e2e_test.go:139`; only the `go/types` method-set mechanism is uncovered|
|`remove implementation`|ABSENT|**ABSENT** ✓|confirmed, and `hierarchy_e2e_test.go:135`'s *"adds/removes"* comment is why it looked covered|
|idempotency|not proven|**not proven** ✓|*Finding 10*|
|`branch_switch`|—|**ABSENT**|not in the spec's floor; the announcement test is not a parity test|
|`change_build_tag`|—|**ABSENT**|not in the spec's floor|
|`replace_generated_file`|—|**ABSENT**|not in the spec's floor|

Six ABSENT change classes either way. The practical consequence for SW-157 is a **different
work list** than the spec anticipated: `change_interface` needs a *residual* (embed removal,
method-signature change) rather than a from-scratch row, and three classes the spec did not
list at all — `branch_switch`, `change_build_tag`, `replace_generated_file` — need rows.

**Correction, review round 1 (m7).** The first version of this paragraph said *"two further
test files … both carrying real snapshot-byte parity proofs"* and named
`hierarchy_e2e_test.go` and `typeresolve_test.go`. The count was wrong and one of the two was
mis-classified — `typeresolve_test.go` compares a **field dump**, not snapshot bytes, which
this document's own *Finding 9* says. The full, enumerated list of inc-vs-full proofs the
seeded inventory did not visit:

|Test|Assertion|Effect on a verdict|
|---|---|---|
|`TestLink_HierarchyGoldenIncrementalVsFull` @ `engine/ingest/hierarchy_e2e_test.go:139`|snapshot bytes|**changed two** — `change_interface` and `add_implementation`|
|`TestNotebook_IncrementalVsFull` @ `engine/ingest/notebook_test.go:399`|snapshot bytes|none — modifies a sibling `.py` while a notebook stays in the corpus; a mixed-language `modify_file` proof|
|`TestService_ByteIdenticalThroughService` @ `engine/watch/service_test.go:121`|snapshot bytes|none — add + modify through the **real watch Service** with `Start` and a burst of changes, rather than a direct `Reconcile` call|
|`TestTyperesolve_FullVsIncrementalByteParity` @ `engine/ingest/typeresolve_test.go:167`|**field dump** (*Finding 9*)|none — supporting only|
|`TestTyperesolve_GoModChangeParity` @ `engine/ingest/typeresolve_test.go:220`|**field dump** (*Finding 9*)|none — supporting only|

So **three** uninventoried snapshot-byte proofs, not two, plus two field-dump proofs. Only the
first changed a verdict.

The remaining `engine/ingest` files that call `IngestChanged` were read and carry **no**
inc-vs-full parity assertion of any kind — `parseerror_test.go`, `readonly_test.go`,
`storecache_e2e_test.go`, `symlink_test.go`, `taint_config_test.go`. They exercise error
handling, read-only stores, cache behaviour, symlinks and taint config. `surfaces/parity_test.go`
also calls `IngestChanged` but proves a different property entirely — **surface** parity, CLI
vs MCP vs HTTP envelopes for one operation — and is not evidence for any row here.

Also uninventoried: `link_fu5_e2e_test.go:32`, `link_python_e2e_test.go:106`,
`link_typescript_e2e_test.go:105` and `link_java_pkgnode_e2e_test.go:21` carry
snapshot-byte inc-vs-full rename proofs on **non-Go** languages. They are recorded as
supporting evidence, not as Go rows.

### Finding 6 — What `move_symbol` actually leaves untested

The ABSENT rows are this story's real output, so the two zero-coverage classes get their
suspected hazard stated. **A suspicion is a test to write, never a fix to apply.**

Go node qualified names are **package-qualified, not file-qualified** — `shop.price`,
`shop.checkout` (see the lookups in `link_e2e_test.go:218` and the `fromQN`/`toQN` pairs in
`link_fu5_e2e_test.go:40`). So a **same-package move** of a declaration from `a.go` to `b.go`:

- **preserves the `NodeId`** (identity is over the qualified name, which did not change), yet
- **changes `source_path` and `line`**, both of which `nodeWire` carries
  (`core/model/serialize.go:82`) and both of which therefore *must* change in the snapshot.

Two files thus claim one `NodeId` inside a single change set, and the source file's per-file
stale-node purge runs against a node the destination file now owns. That is structurally the
**BLOCK-2 hazard class** described at `engine/ingest/link_cascade_test.go:109-117` — where
the stale-edge sweep skipped edges whose `To` was also owned by the reprocessed set — and
BLOCK-2 was a *real shipped defect* found by exactly this kind of cascade-only test. A
cross-package move is the easier case (the QN changes, so it degenerates into rename plus
delete); the same-package move is the one to write first.

**If SW-157 finds a real mismatch here, it publishes the FAIL and files it. It does not fix
it** — a fix is a product-byte change, moves the candidate, and violates the owner's ruling
that one v0.7.2 batches every correction (Delta PRD §6.2).

### Finding 7 — What `rename_package` actually leaves untested

`GroupPackages` (`engine/typeresolve/pkggraph.go:85`) keys type-check units by *(directory,
package clause)* and, per its own doc comment at `:82-84`, *"a directory with MULTIPLE
non-test package clauses yields one unit per clause, each Degraded — go/types cannot check
either mixture soundly, and picking a winner would silently drop the loser's symbols."*

A package rename applied file-by-file therefore passes **through** that degraded state: while
some files in the directory say `package old` and others say `package new`, the directory has
two clauses and every unit in it is Degraded, which withdraws confirmed-tier edges. The
incremental path must converge with a full index on both sides of that transition **and** at
any intermediate point a change set can land on. `TestTyperesolve_GoModChangeParity`
(`typeresolve_test.go:220`) proves the analogous *module*-rename degradation converges — the
confirmed edge degrades to the re-linked heuristic edge rather than disappearing — which is
encouraging and is not this class.

### Finding 8 — "every parity proof is MemStore-only" is true of the change classes and false of the crash conditions

The spec and `backlog.md:32` both state that every parity proof in the tree is MemStore-only.
That is **correct for all 15 change classes** — every citation in Table A runs
`graphstore.NewMemStore()`, while the shipped store is SQLite — and it is the gap SW-157
closes by running the table on both backends.

It is **wrong for the crash conditions.** `engine/ingest/faultmatrix_test.go` opens real
SQLite stores: `graphstore.OpenSQLite` at `:246` (`TestFaultMatrix_FullPass_SQLiteCloseReopen`),
`:295`, `:368`, `:399`, `:440` (`TestFullPassGeneration_GraphFileRevert`). Better still, the
reference store in those tests is a `MemStore` (`:338`, `:472`), so the assertion compares
**SQLite snapshot bytes against MemStore snapshot bytes** — which additionally proves the
snapshot envelope is store-independent, exactly as `core/graphstore/snapshot.go:47-52` claims.

**Correction, review round 1 (m6).** The first version of this document turned that finding
into `store: both` on **both crash-condition rows**, and that was a step too far. The SQLite
coverage is real but it lives in *sibling* tests, not in either row's **cited primary**:
`:160` wraps `batchFaultStore{graphstore.NewMemStore()}` and `:490` wraps
`writeFaultStore{graphstore.NewMemStore()}`. Both rows now read `store: MemStore`, which is
what their own `test_name` does, and each names its SQLite sibling in `note`. The rule the
error violated is now written into the YAML's field documentation: **every field describes the
row's primary cited proof, not the best proof that exists anywhere for the class.** The
finding itself is unchanged — the crash conditions *do* have real SQLite coverage, and the
change classes have none.

`core/graphstore/contract_test.go` reinforces the store-independence claim directly:
`TestContract_SnapshotLoadRoundTrip` (`:371`), `TestContract_DeleteThenReindexByteIdentical`
(`:222`) and `TestContract_CanonicalOrdering` (`:252`) each run `mem` and `sqlite` subtests
against one shared contract suite.

### Finding 9 — a fourth assertion form exists in the tree, and it is not snapshot bytes

Several parity tests do not compare snapshot bytes. They compare `dumpGraph`
(`engine/ingest/typeresolve_test.go:64`), a rendered whole-graph string: one sorted line per
node (`id kind qualified_name path:line:column`) and per edge
(`id from->to kind tier confidence reason evidence`).

This matters because it is easy to read as equivalent and it is not:

- It is **much stronger than a spot query** — it is whole-graph, and it includes tier,
  confidence, reason and evidence.
- It is **weaker than snapshot bytes** — it omits node `meta`, which `nodeWire` carries
  (`core/model/serialize.go:61`), and it is not the versioned envelope
  (`core/graphstore/snapshot.go:47`) that `graphi snapshot` actually writes.

AC-3's assertion-strength vocabulary is a closed set of three — snapshot bytes / envelope
bytes / spot query — and did not anticipate this form. Rather than silently widening the
vocabulary or dishonestly filing it under one of the three, **no row's verdict rests on a
field-dump assertion.** The three tests that use it —
`TestWarmDeltaMatchesFullReindex` (`engine/ingest/warmstart_test.go:142`),
`TestTyperesolve_FullVsIncrementalByteParity` (`typeresolve_test.go:167`) and
`TestTyperesolve_GoModChangeParity` (`typeresolve_test.go:220`) — are cited as **supporting**
evidence, and every verdict-bearing citation in both tables is a snapshot-bytes assertion or
an explicitly named spot-query substitution. Recorded so SW-157 does not reach for
`dumpGraph` believing it is the snapshot.

### Finding 10 — FR-7's idempotency is not currently proven

FR-7 `:833` requires *"Wiederholte Ausführung ist idempotent"* and Delta `:1088` requires
*"Repeated incremental application is idempotent"*. **Neither is proven today**, and the two
tests that look like they prove it do not:

- `TestRepeatRun_Determinism` @ `engine/conformance/conformance_test.go:191` repeats
  **dispatch, not application.** It builds one store, then loops eight times over
  `envelope()` → `analysis.Service.Dispatch` → `analysis.Marshal`, comparing envelope bytes.
  It never re-applies a change set. Checked mechanically:

  ```
  grep -n "Reconcile\|IngestChanged\|Dispatch" engine/conformance/conformance_test.go
  → 87, 122   (both inside helpers; :122 Reconcile runs once per step, and
              TestRepeatRun_Determinism declares exactly one step)
  ```

  The test's own doc comment at `:187-190` is accurate — it claims *"repeated dispatch"* and
  proves exactly that (no map-iteration, goroutine-order or wall-clock dependence in the
  read path). The gap is that nothing else claims the other half.
- `TestLink_Idempotent` @ `engine/link/link_test.go:158` calls `Link()` twice on identical
  inputs and deep-compares the returned edge slices. That is **pure-function repeatability
  of one stage**, in memory, with no store, no ingest pass and no byte comparison.

What FR-7 asks for and nothing supplies: **apply the same change set twice through ingest and
assert the graph state is byte-identical after the second application.** SW-157 adds it. Note
that it is a *distinct* property from full-vs-incremental parity — a system can converge
inc→full and still drift on a redundant re-apply, e.g. by appending rather than replacing
evidence, or by double-counting a reverse-dependency row.

### Finding 11 — one row's proof touches no store at all, so the store vocabulary needed a fourth value

*Added in review round 1 (m2).* AC-3 asks every PROVEN/PARTIAL row to record a store backend
from `MemStore | SQLite | both`. `add_implementation`'s primary proof fits none of them:
`engine/typeresolve` contains **zero** graphstore usage — `grep -rn graphstore engine/typeresolve`
returns nothing — so `TestResolve_ImplementsProven` runs a `go/types` check and inspects
derived edge strings without ever opening a store.

The first version recorded `MemStore` there, which contradicted the row's own note conceding
"no store snapshot" one line later. Rather than write a store the test never opens, the
vocabulary widened by one honest value: **`none`**. This is the same treatment *Finding 9*
gives the field-dump assertion form — where a closed vocabulary meets a case it did not
anticipate, the document records the gap rather than forcing the value. The YAML's validator
now also rejects `store: none` on any row claiming a byte assertion, since bytes can only come
from a store.

### Finding 12 — generated-file detection *does* exist; it just cannot reach the parity path

*Added in review round 1 (m3).* The `replace_generated_file` row's first version inferred that
no generated-file special-casing exists anywhere, from a grep scoped to `core/parse` and
`engine/ingest`. **The inference over-reached.** Detection exists:

- `surfaces/client/direct.go:596` `GeneratedMarkerDetector(root)` returns a predicate that
  scans a file's head for `Code generated ... DO NOT EDIT` and `@generated`.
- It is injected into the engine's suppression config and consumed at
  `engine/diagnostic/suppress.go:101`, which classifies a matching file `SuppressionGenerated`.

**The verdict is unaffected, and the reason is precise rather than convenient.** That detector
lives in the **diagnostic suppression** path — dead-code triage, deciding whether to report a
finding about a symbol — is injected surface-side, and is reached by neither the parse/ingest
pipeline nor `model.Graph`. It cannot influence snapshot bytes, so it cannot influence parity
in either direction.

What survives of the original point: **within the ingest and graph path** the class genuinely
has no special-casing, which is why it is a large-diff, high-symbol-count stress on the
ordinary modify path rather than a degenerate row like `change_build_tag`. Recorded this way
because "no such code exists" and "such code exists but cannot reach this path" are different
claims, and only the second one is true.

### Finding 13 — this inventory goes stale by design, and the drift guard now says so

*Added in review round 1 (m4).* Every verdict here is an observation dated 2026-07-30 at
`main` @ `d9dadf0`, taken **before any harness existed**. SW-157's entire job is to falsify six
of the ABSENT rows. The drift-guard contract as first written had **three** directions —
MISSING, PHANTOM, KIND — and bound `id` and `kind` but **not `verdict`**. A row could
therefore keep reading `ABSENT` forever while a passing harness row sat beside it: the file
would be lying and nothing would fail.

That hole is closed. Two directions were added to the contract in
`docs/rc/parity-classes.yaml`:

- **VERDICT** — a row with `harness_row: "required"` whose harness row exists and passes must
  not read `verdict: "ABSENT"`. The guard fails if it does.
- **OWNER** — `harness_row: "required"` implies `owner: "SW-157"`; `harness_row: "deferred"`
  implies `owner == deferred_to`. Any other pairing fails, because it means the file names one
  story as responsible and asks a different one to deliver. (This one also fixed a live
  inconsistency: `replace_generated_file` carried `required` with `owner: SW-144`.)

**The rule for SW-157**, stated in the YAML so it cannot be missed: the commit that adds a
harness row for class *X* must, in the same commit, update *X*'s `verdict` and its
`test_file`/`test_line`/`test_name`/`fixture`/`store`/`assertion` to point at the harness row
that now proves it. Legacy citations move into `note` as pre-harness provenance rather than
being deleted, so this inventory stays auditable after it is superseded. `matrix_version` does
not bump for a verdict change — it tracks shape, not evidence.

### Finding 14 — `gopkg.in/yaml.v3` *is* a first-party dependency; the flat-subset shape is a choice, not a constraint

*Added in review round 1 (M1), correcting a false claim of mine.* The first version of
`docs/rc/parity-classes.yaml` justified its flat, quoted, single-nesting-level shape by
asserting that graphi does not depend on a general YAML parser and that `gopkg.in/yaml.v3` is
"not imported by any first-party package". **Both halves were false**, and the error came from
reading `go.mod` instead of grepping the imports:

- `engine/scenario/scenario.go:26` imports `gopkg.in/yaml.v3` **unconditionally**, no build
  tag, in the **product tree**.
- `engine/scenario/scenario.go:259` calls `yaml.Unmarshal` on checked-in source-of-truth YAML —
  the 20 `corpus/hero/*.yaml` scenarios driving the standing hero gate. That is this file's own
  job, already precedented in the product.
- `go list -deps ./engine/scenario | grep yaml` returns it.
- The `// indirect` marker at `go.mod:47` is **stale**, and a stale marker is not evidence.

The consequence is not cosmetic: the original text instructed SW-157 that a hand-rolled parser
was **mandatory**. It is not. The subset restriction is kept, but as a **deliberate choice**
with its real justification — it costs nothing (a matrix has no need for nesting), it keeps the
file readable by `cmd/coverage`-style parsers *as well as* by `yaml.Unmarshal` so a future
guard can live in either place, and a flat block list diffs one row per change.

**Recommendation to SW-157, stated plainly rather than left to inference: use
`yaml.Unmarshal` into a `[]struct`.** It is already in the product tree, it is the smaller
diff, and it keeps a hand-rolled parser off the review surface. Keep the file inside the flat
subset regardless, so the choice stays reversible.

## Scope — what this document deliberately does not do

- **It fixes nothing.** No parity defect and no parity gap is repaired here. An ABSENT row
  with a stated suspicion is the output; a test is SW-157's output; a fix is a product-byte
  change that moves the candidate and belongs to the batched v0.7.2 (Delta PRD §6.2).
  **In slice: find it, publish the FAIL, file it. Out of slice: fix it.**
- **It writes no assertion and no product file.** `internal/evalreport` and everything under
  `engine/`, `core/`, `surfaces/` and `cmd/eval/` are unchanged, proven by diff rather than
  asserted.
- **It does not re-litigate ADR 0004's K4 disposition** (*Finding 2*), and does not extend
  the snapshot envelope.
- **It changes no evidence-index row.** An inventory fills nothing. WP4 remains STALE with
  its parity conjunct unmeasured; WP6's 90-day clock has not started.
- **It is not the §12.3 gate**, and SW-157's green `go test` over synthetic fixtures will not
  be either. Only SW-144 + SW-158 together produce the FR-7 evidence, and spec decision 4
  says neither may be reported alone.

## The work list this inventory produces

**SW-157** — hermetic declarative harness, both store backends, production Go parser:

1. `move_symbol` — new row, same-package move first (*Finding 6*).
2. `rename_package` — new row, including the intermediate multi-clause degraded state
   (*Finding 7*).
3. `remove_implementation` — new row, both mechanisms.
4. `add_implementation` — promote PARTIAL → PROVEN by covering the `go/types` method-set
   mechanism incrementally.
5. `change_interface` — residual only: embed **removal** and interface method-**signature**
   change.
6. `change_external_import` — residual only: add/swap an import path to a different external
   package.
7. `change_build_tag` — new row, explicitly labelled with its degeneracy (*Finding 3*).
8. `replace_generated_file` — hermetic reproducer; the real instance is SW-144's
   (*Finding 12* for why the existing detector is irrelevant to it).
9. **Idempotency** — the assertion FR-7 demands and nothing supplies (*Finding 10*).
10. **Both backends** for all 15 change classes — today every one is MemStore-only
    (*Finding 8*).
11. Fix the overstated doc comment at `engine/ingest/hierarchy_e2e_test.go:135`
    (*"adds/removes"* → adds), a `_test.go`-only change.
12. **Update this file's verdicts in the same commit as each harness row** — the VERDICT and
    OWNER guard directions now make forgetting a build failure (*Finding 13*).

**How to read `parity-classes.yaml`, recommended:** `yaml.Unmarshal` into a `[]struct`. It is
already in the product tree (`engine/scenario/scenario.go:26`, `:259`), it is the smaller diff,
and it keeps a hand-rolled parser off the review surface. The file is *also* inside the
`internal/coverage` flat subset so a `cmd/coverage`-style parser remains an option — that is a
deliberate compatibility choice, not a constraint (*Finding 14*).

**SW-144** — real pinned repositories, built binary as a subprocess. These are **residual**
owners: the `owner` field on every `harness_row: "required"` row reads `SW-157`, and SW-144's
share is named in each row's `note` (*Finding 13*, OWNER direction).
`replace_generated_file` on grpc-go, `change_build_tag` on gin (the manifest's declared
repo, 16 files with `//go:build`), the everyday classes on cobra.

**SW-158** — real process boundary: `branch_switch` over a real git repository, and the
process-level complements of both crash conditions that
`docs/adr/0004-ingest-recovery-disposition.md:92-94` reserves to the real-repo gates.

**Citation correction.** The spec places that reservation at `:72-84`. `:72-82` is the ADR's
*"Residual scope (assigned, not forgotten)"* section — relevant, but not the reservation. The
sentence that actually reserves real-repo recovery failures to the real-repo gates is at
`:92-94`, under *"Disposition for the `ING-REWRITE` trigger"* (`:84`).
