# ADR 0007 — The Semantic-Resolver Registry (Language GA program, WP-J0/WP-J1)

- Status: **Accepted** (implemented 2026-08-14: `engine/typeresolve/registry.go`,
  the registry-driven pass in `engine/ingest/typeresolve.go`, and
  `internal/coverage.CheckGALanguages` — the behavior-preserving gate ran green:
  full test suite, conformance byte-parity suite, hero gate, coverage checks)
- Date: 2026-08-14
- Program: [`docs/plan/2026-08-graphi-p2-language-ga-program-v1.md`](../plan/2026-08-graphi-p2-language-ga-program-v1.md)
  §5 WP-J0 (seam) and §6/WP-J1 (GA-language machine check)
- Depends on: nothing in-flight — deliberately behavior-preserving groundwork
- Feeds: ADR 0008 (JVM binder registers behind this seam); every later wave's
  semantic resolver

## Problem

The `confirmed`-tier third ingest phase is hard-coded to Go twice over:

1. `engine/typeresolve/check.go:44` — `Languages()` returns the constant
   `["go"]`; the trust surface (`surfaces/client/trust_report.go:333`) consumes
   this single package directly.
2. `engine/ingest/typeresolve.go` — the dispatch gates on the `.go` filename
   suffix (lines 70, 89, 192) and excludes `_test.go`. Generalizing
   `Languages()` alone would not route a single non-Go file into a semantic
   pass.

Separately, the GA **language axis** is prose-only: `docs/stability-tiers.md`
states "language = Go ← NOT encoded in the matrix". A language can today be
flipped GA by editing prose, which the honesty culture of this repository is
otherwise built to prevent.

## Decision (proposed)

1. **Registry seam.** A semantic-resolver registry mirroring the open/closed
   shape of `engine/link` (`Register` in a constructor, never editing an
   existing resolver) and `core/parse.Registry`. Interface (sketch):
   `Languages() []string` plus a snapshot-resolve entry point taking the walked
   file units and the committed node set, returning confirmed-tier edge intents
   — the contract `engine/typeresolve.Resolve` already implements.
   `engine/typeresolve` becomes the first registrant, unchanged in behavior.
2. **Registry-driven ingest dispatch.** `engine/ingest`'s third phase asks the
   registry which languages have a semantic resolver and routes each file unit
   by its parser language, replacing the `.go`-suffix checks.
3. **Kill switches.** `GRAPHI_NO_TYPERESOLVE` keeps its current global meaning
   (disables the whole third phase). New: `GRAPHI_NO_TYPERESOLVE_<LANG>`
   disables one registrant.
4. **Trust surface consumes the registry union** instead of
   `typeresolve.Languages()` directly; `engine/trust.CapabilityInputs.TypeChecked`
   is already a `[]string` and needs no change.
5. **GA-language machine check (WP-J1).** New matrix rows
   `category: ga-language` in `docs/coverage-matrix.yaml` (flat scalars, one
   new struct field for `capability:`), and
   `internal/coverage.CheckGALanguages` beside `CheckStableTier`, enforcing:
   (i) the row's declared capability equals the registry-derived level
   (outcome-based, not registration-based); (ii) every `GA-LANG-<lang>-*`
   evidence-index row reads PASS with URI + sha; (iii) adding a row requires
   (i)+(ii); removing one is legal but loud. Day one: exactly one row
   (`go` / `typed-confirmed`).

## Gate

Behavior-preserving **to the byte**. The before/after evidence is the tree's
pre-seam pinned-byte expectations passing unchanged: the conformance
change-class table (full-vs-incremental snapshot bytes on both stores, with
witnesses), `engine/ingest`'s typeresolve parity and golden graph-dump tests
(exact node/edge sets including tier/confidence/reason), the hero gate, and
the full test suite — every one of them authored against the pre-seam code and
green after it. The nil-vs-empty return distinction and the three path
predicates are additionally pinned by dedicated tests. Any diff against these
expectations is a defect in this ADR's implementation, not a baseline move.

## Open points — resolved at implementation

- **Registry location:** inside `engine/typeresolve` (`registry.go`), the
  smallest diff — the Result currency and the trust-facts folding already live
  there, and the package-level `Languages()` now delegates to the registry so
  the trust surface needed no change. A future `engine/jvmresolve` imports
  typeresolve for the seam types and is registered in `NewRegistry`.
- **Resolver interface shape:** three path predicates instead of one, because
  the pre-seam Go pass genuinely used three distinct path sets that byte-parity
  forbids collapsing: `Subject` (gates the run — non-test `.go`; go.mod alone
  runs nothing), `Input` (the file map — all `.go` incl. `_test.go` plus
  go.mod), `Triggers` (the incremental re-run gate — non-test `.go` or go.mod).
  Pinned by `TestGoResolver_PathPredicates`.
- **Kill switch:** env-var-per-language (`GRAPHI_NO_TYPERESOLVE_<LANG>`),
  pinned as a wire identifier by `TestTyperesolve_PerLanguageKillSwitch`; the
  global switch is unchanged.
- **GA-language check location:** `internal/coverage/galang.go`, consuming the
  derivation through the exported `surfaces/client.LanguageCapabilities()` (the
  SAME assembly the trust report serves) plus `internal/evidence` gate facts —
  wired into `cmd/coverage -check` behind `category: ga-language` matrix rows.

## Still open (deferred, tracked in the program plan)

- Outcome-based capability derivation for heuristic resolvers (the SQL wrinkle,
  program plan §4 finding 3 / ADR-W1): the derivation still grades resolver
  REGISTRATION. This blocks no current row — Go's binding is outcome-true — and
  is wave-5 scope.
- The trust-facts fields (`lastTypeResolution`, `lastPackageEvidence`) are
  single-slot and hold ONE resolver's facts — exact with one registrant, and
  the pass carries a load-bearing comment requiring per-language widening
  BEFORE a second registrant lands (WP-J3 entry criterion).

## Consequences

- A new language's semantic resolver is a new `Register` call plus its own
  package — never an edit to `engine/typeresolve`.
- The GA language set becomes machine-checked; a prose edit can no longer flip
  a language GA.
- Until a second registrant exists, the shipped behavior is byte-identical to
  today's.
