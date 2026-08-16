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
- Depends: the PARITY-001 fix (purge-before-link ordering) — its purge-first
  discipline is what lets the incremental module map read cached paths within
  the pass's tx and see deletions
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

Wiring: `engine/ingest` builds the map once per pass — from the walked units'
`go.mod` census on a full pass (contents read from disk; `fileUnit` carries
only hashes by design), and from units ∪ cached paths on an incremental pass —
and attaches it at all three index build sites (`linkFiles`, and both
reverse-deps translations). `packageFileNodes` and `DirsForImport` consult it;
because `DirsForImport` feeds `reverse_deps`, the cascade's dependency records
agree with the emission by construction.

**Cache invalidation widens with the dependency**: a change to a `go.mod` at
ANY depth (`link.GoModPath`) triggers the full re-link, where the historical
predicate matched only the literal root path. Without this, a nested `go.mod`
would shift resolution for its subtree with no re-link — a PARITY-002-shaped
divergence introduced by the fix itself. Pinned red-without/green-with by
`add_nested_gomod`.

**Deliberately NOT changed:**

- `crossPackage` / `hasPackage` / `receiverMethod` stay clause-based. They are
  fail-closed (ambiguity → skip, and `hasPackage` errs toward suppressing
  external nodes — the documented safe direction), they are not PARITY-002's
  mechanism, and switching them is a separate semantic change with its own
  blast radius.
- Non-Go languages: Python/Ruby/JS package imports keep their clause fan-out
  (`clausePackageFileNodes`) and Java/Kotlin keep the single interned
  file→package edge. Python's structural exposure to the same fan-out shape is
  recorded (review finding F5) and is a Wave-2 entry question, measured
  separately — never bundled into this change.
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
  repositories (they lose semantically wrong edges). The candidate moves;
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
