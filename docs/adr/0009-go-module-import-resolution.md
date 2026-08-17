# ADR 0009 — Go Import Resolution by Module-Relative Directory (the PARITY-002 fix)

- Status: **Accepted** (owner-directed completion of language-GA Wave 0 item
  W0.f, 2026-08-16; the owner's standing instruction for this item was to
  finish and review it — see the ratification header in
  [`../decisions/2026-08-language-ga-independent-review.md`](../decisions/2026-08-language-ga-independent-review.md))
- Date: 2026-08-16
- Story: language-GA execution plan Wave 0, W0.f step 2
  ([`../plan/2026-08-graphi-language-ga-execution-plan-v1.md`](../plan/2026-08-graphi-language-ga-execution-plan-v1.md))
- Spec-Gate: the conformance classes `change_colliding_package_dir` (now a real
  parity assertion) and `add_nested_gomod` (new), both stores, plus the
  `internal/parity` real-repo planners for both ids
- Depends: the PARITY-001 fix (purge-before-link ordering) — module-aware
  resolution consults the committed node population per directory, so it needs
  deleted paths purged before `linkFiles`, or a re-moduled subtree would still
  count purged nodes as resolution targets. (An earlier version of this bullet
  claimed the dependency was about reading cached paths in-tx; that mechanism
  never existed — the map is built from the walk, before the purge — and the
  claim was corrected in review round 1, finding 6.)
- Feeds: WP-J7 real-repo parity (G4), the Wave-0 gate (`internal/parity` two
  dispatches with identical COUNTS), Wave 2's Python fan-out decision (F5)

## Problem

PARITY-002: `graphi sync` could settle a different — and on large repositories
a non-deterministic — file→file `imports` edge set than `graphi rebuild` over
the identical tree. Reproduced hermetically (conformance class
`change_colliding_package_dir`) and measured on pinned clones
([`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md)).

The mechanism was a CLOSURE defect. Go import resolution keyed on the package
CLAUSE — `path.Base(importPath)` — and unioned every directory in the repo
declaring that clause (`engine/link/index.go` `byClause`), emitting one
`imports` edge per file node in the union. Two directories may legitimately
declare `package json`, so an importer of `x/json` also fanned out over
`y/json`: a semantically WRONG edge on the full pass, and — because the
incremental cascade (`dependentsOf`) is directory-local and nothing imports
`y/json` — an edge the incremental pass never re-emitted. Neither side was
right; `rebuild` is the reference by definition, so `sync` "diverged". The
divergence was FROZEN: the affected importers were never in any re-link set, so
no amount of re-syncing healed it.

## Decision

Go import paths resolve to the ONE repository directory their module path
declares, through a per-pass **module map** (`engine/link/modulemap.go`) built
from every `go.mod` in the tree:

- **Longest-matching module path wins** — multi-module repositories are the
  pinned corpus's normal case (grpc-go: 11 `go.mod`; kubernetes: 34, with
  `staging/` publishing 5899 files as separate modules).
- **Segment-boundary matching**, never raw prefixes: module `example.com/m`
  matches `example.com/m/x` and never `example.com/mtools`.
- **Nested modules EXCLUDE their subtree** from the enclosing module
  (`ModuleMap.ownedBy`): once `sub/` roots its own module, the root module's
  path arithmetic may not resolve into it. This rule was not in the original
  design sketch — the `add_nested_gomod` conformance class caught its absence
  during development (both passes kept a stale edge), and the ownership check
  exists because that row demanded it.
- **Fail-closed**: an import matching no module in the tree resolves to
  nothing (stdlib / third-party — today's behaviour). A tree with NO `go.mod`
  keeps the historical clause behaviour unchanged; a `go.mod` that cannot be
  read or parsed contributes no resolution, never a guess.

Wiring: `engine/ingest` builds the map once per pass from the walked units'
`go.mod` census (contents read from disk; `fileUnit` carries only hashes by
design) — the SAME builder for both passes, because `IngestAll` and
`ingestChanged` each begin with a full walk of the tree, so the units are the
complete census either way (review round 1, finding 6: an earlier
incremental-only variant unioned in cached paths; the union was provably
redundant and was removed). The map attaches at all three index build sites
(`linkFiles`, and both reverse-deps translations). `packageFileNodes`,
`crossPackage` and `hasPackage` consult it for Go emission.

`DirsForImport` — the reverse-dep translation's resolver — returns the UNION
of the module basis and the clause basis, NOT the module directory alone.
This is a review-round-2 correction of a CONFIRMED blocker (independent
reviewer, finding 1, reproduced hermetically): the translation serves ALL
languages and its targets carry no language, while non-Go emission stays
clause-based — a module-only answer swallowed every non-Go target in any tree
containing a go.mod, so a Python caller's dependency record was stored
verbatim ("shop") instead of as its target directory ("src/shop"), the
cascade never re-linked it, and the full pass's cross-module edge went
permanently missing from the incremental graph. A NEW frozen divergence of
exactly the class this ADR closes, introduced by its own first version. The
invariant the translation must uphold is records ⊇ emission dependencies, per
language, all languages at once; the union satisfies it (the module dir is
not always inside the clause dirs — a directory's declared clause can differ
from its import path's last segment), and over-approximation is safe by
idempotence: an unnecessary re-link re-emits identical bytes. The earlier
"records agree with the emission by construction" wording claimed equality
where only coverage is needed — and its module-only implementation delivered
neither. Pinned by `TestLink_Python_MixedTreeWithGoMod_IncrementalParity`
(mixed tree, clause ≠ directory, change set naming only the target file).

**Cache invalidation widens with the dependency**: a change to a `go.mod` at
ANY depth (`link.GoModPath`) triggers the full re-link, where the historical
predicate matched only the literal root path. Without this, a nested `go.mod`
would shift resolution for its subtree with no re-link — a PARITY-002-shaped
divergence introduced by the fix itself. Pinned red-without/green-with by
`add_nested_gomod`.

**Widened in review round 1 — `crossPackage` and `hasPackage` are module-aware
too.** The first version of this ADR left them clause-based on the argument
that they are fail-closed (ambiguity → skip) and "not PARITY-002's mechanism".
The independent review REFUTED that argument (finding 1, confirmed by repro):
on a full pass a clause collision on a shared symbol name made the
`crossPackage` lookup ambiguous, which dropped the intra-repo `calls` edge and
minted an interned external node (`example.com/m/x/json.A`) with a heuristic
edge instead — while the incremental pass, whose directory-local cascade never
re-linked the importer, kept the old intra edge. The same frozen
full-vs-incremental divergence, through `calls` instead of `imports`:
fail-closed resolution is only parity-safe if both passes fail closed on the
same inputs, and the clause union made the inputs differ. Both lookups now
resolve through the module map when one exists (clause behaviour unchanged
when the tree has no `go.mod`), and the conformance class pins the shape: the
colliding directory also declares a same-name symbol, and the witness asserts
the intra edge survives and the external node never exists.

**Deliberately NOT changed:**

- `receiverMethod` stays bare-name-based. It never consults the import path at
  all (it resolves `recv.Method` across directories by receiver/method name),
  so the module map has nothing to key on, and its ambiguity rule is
  symmetric between the passes.
- Non-Go languages: Python/Ruby/JS package imports keep their clause fan-out
  (`clausePackageFileNodes`) and Java/Kotlin keep the single interned
  file→package edge — non-Go EMISSION never consults the module map, so a
  single pass's non-Go bytes are unchanged. (The shared reverse-dep
  TRANSLATION is a different story — see the `DirsForImport` union above,
  which review round 2 forced.) Python's structural exposure to the same
  fan-out shape is recorded (review finding F5) and is a Wave-2 entry
  question, measured separately — never bundled into this change.
- `engine/typeresolve`'s `Input`/`Triggers` stay root-only: its Resolve reads
  only the root module path today, so a nested `go.mod` cannot change its
  result; widening them would re-run the pass for nothing.

The module directive is parsed in `engine/link` rather than importing
`typeresolve.ParseModulePath` — the same deliberate-duplication reasoning the
kind strings record (`check.go:43`) — and the two parsers are pinned to agree
by `TestParseModuleDirective_AgreesWithTyperesolve` (test-only dependency).

## Rejected alternatives (recorded, not silently omitted)

- **Widening `dependentsOf` over the clause relation** — closes the cascade
  instead of fixing the resolution, making every re-link O(repo) on
  clause-heavy repositories (any edit to any `package json` file would re-link
  every importer of every `*/json` path), and it would KEEP emitting the
  semantically wrong cross-directory edges. Trades a correctness bug for a
  performance cliff and half the fix.
- **Importing `engine/typeresolve` for the module parser** — couples the
  heuristic layer to the type-checked one for eight lines of parsing.
- **Reading `go.mod` contents from `fileUnit.src`** — the walk deliberately
  does not retain source bytes; disk is the one truth both passes share, and
  reading it keeps the full/incremental maps identical over a stable tree.

## Consequences

- Product-byte change: `imports` edge sets change on clause-colliding
  repositories (they lose semantically wrong edges). Additionally, an import
  that names a module path NOT present in the tree — the mid-refactor state
  after a module rename, when files still import the old path — now resolves
  as EXTERNAL (interned node, heuristic edge) instead of being clause-guessed
  back onto an intra-repo directory. `TestTyperesolve_GoModChangeParity` pins
  the new shape: the call edge retargets to the external node rather than
  disappearing, and is never confirmed. The candidate moves;
  previously measured perf/evidence rows stay STALE until re-measured, and the
  real-repo matrix rows for gin/grpc-go stand as published (with a superseded
  header) until `internal/parity` re-runs.
- PARITY-002's user-facing disclosure (readme Known limits, `sync -h`, the
  doctor known-defects check) is retired in the same change that closes the
  defect, per the disclosure's own contract.
- The Wave-0 gate's determinism proof (`internal/parity`, two dispatches with
  identical COUNTS on grpc-go) becomes runnable; until it runs, PARITY-002's
  non-deterministic half is closed by argument (the fan-out no longer exists
  for Go), not by measurement — the distinction the evidence discipline
  requires stating.

## Review round 2 (independent adversarial reviewer, 2026-08-16)

An independent reviewer was set on the whole diff with instructions to refute
it. Outcomes, so none is silently absorbed:

- **Finding 1 (BLOCKER, confirmed, fixed)**: the `DirsForImport` union above.
- **Finding 3 (fixed)**: a nested `go.mod` that is unparseable — or that lost
  the duplicate-module-path tie-break — used to contribute nothing at all, so
  the ENCLOSING module's arithmetic resolved into its subtree: intra-repo
  edges for a tree the Go toolchain refuses to build. `moduleDirs` is now
  built from every input `go.mod` path, parseable or not: a broken module
  still marks a BOUNDARY (shielded and unresolvable), pinned by
  `TestModuleMap_UnparseableNestedGoModStillShieldsSubtree`.

Known limits, recorded rather than fixed (all deterministic and identical
across passes — none is a parity defect):

- **Torn `go.mod` read** (finding 2): a non-atomic writer racing a pass can
  yield truncated-but-parseable content and thus a wrong resolution FOR THAT
  PASS. Drift-healed: the cache's go.mod hash mismatches the settled bytes,
  the next sync re-links everything. The unhealed shape needs a delete +
  byte-identical restore with BOTH watcher events lost — inherent to reading
  disk mid-pass (the rejected `fileUnit.src` alternative would trade it for a
  worse one), accepted with eyes open.
- **Ambiguous import** (finding 3's second half): when a nested module's
  declared path collides with the enclosing module's arithmetic for an
  existing directory — Go's "ambiguous import" build error — `Dir` picks the
  module-path match deterministically instead of failing closed. Malformed
  tree; both passes agree.
- **Hard-fail wedge widened** (finding 4): any go.mod change pulls every
  linkable file into the re-link set, and a pre-existing `SkipParseError`
  elevation makes the incremental pass hard-fail if ANY file in that set
  cannot be parsed — pre-existing policy for the root go.mod, now reachable
  from any depth. Changing the elevation policy is its own semantic decision,
  not smuggled in here.
- **Pre-purge translation index** (finding 6, pre-existing): the incremental
  reverse-deps translation runs before the deleted-path purge, the full
  pass's after — meta-only (`reverse_deps` rows, graph bytes agree), and
  owned by the PARITY-001 follow-up (W0.i), where the batch-ordering work
  already lives.
