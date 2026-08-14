# ADR 0007 — The Semantic-Resolver Registry (Language GA program, WP-J0/WP-J1)

- Status: **Proposed** (skeleton — no implementation exists; accepted only when
  WP-J0 lands with its behavior-preserving gate green)
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

Behavior-preserving **to the byte**: the full conformance suite, the hero gate,
and a before/after snapshot-byte comparison on the Go corpus must all be
unchanged by the seam extraction. Any diff is a defect in this ADR's
implementation, not a baseline move.

## Open points (to resolve before Accepted)

- Exact interface shape and package location of the registry (inside
  `engine/typeresolve` vs a new `engine/semantic` umbrella) — smallest-diff
  option preferred.
- Whether the per-language kill switch is env-var-per-language (as proposed) or
  one comma-separated variable.
- How the outcome-based capability derivation (needed to close the SQL wrinkle
  named in the program plan §4) is expressed without hand-maintained tables.

## Consequences

- A new language's semantic resolver is a new `Register` call plus its own
  package — never an edit to `engine/typeresolve`.
- The GA language set becomes machine-checked; a prose edit can no longer flip
  a language GA.
- Until a second registrant exists, the shipped behavior is byte-identical to
  today's.
