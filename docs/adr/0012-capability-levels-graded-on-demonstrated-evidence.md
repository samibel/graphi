# ADR 0012 — Capability levels are graded on demonstrated evidence, and SQL's `cross-file-heuristic` is earned

- **Status:** Accepted
- **Date:** 2026-08-19
- **Story:** SW-183 — audit every shipped language's derived capability level against what is proven
- **Supersedes:** nothing. **Withdraws a premise**, which is not the same thing —
  see §2.
- **Related:** [ADR 0007](0007-semantic-resolver-registry.md) (the derivation's
  home), [ADR 0011](0011-imports-edge-targets-package-source-files.md),
  [the programme plan](../plan/2026-08-graphi-p2-language-ga-program-v1.md) §4/§7
  (G1, "ADR-W1"), [the per-language GA template](../plan/2026-08-per-language-ga-template-v1.md).

## Context

A language's capability level is **derived at read time** from the live
registries (`engine/trust/capability.go`), never from a maintained table. That is
the right design: a copied table is correct exactly until the next grammar lands.

But the derivation grades `cross-file-heuristic` on one predicate — *is a
resolver registered for this language?* (`capability.go:113-116`). **A resolver
that resolves nothing is still a registered resolver.** So the derivation can
over-claim, and no existing test can see it: `surfaces/client/capability_test.go`
binds the derivation to the registries faithfully, which means it is downstream
of exactly the fact in doubt.

The programme booked this as a known honesty gap under the label **ADR-W1**, with
**SQL** as its worked instance. Three places state it, all in the same words:

- the programme plan §4 finding 3 — *"SQL's capability label is
  registration-derived, not outcome-derived"*;
- the programme plan G1 — *"today SQL reads `cross-file-heuristic` while its
  resolver deliberately proves nothing"*;
- ADR 0007, "Still open" — *"the derivation still grades resolver
  REGISTRATION"*.

And two more places assert the consequence as a fact about SQL:

- `docs/language-support.md`, the SQL row — *"— (no provable cross-file refs at
  this tier; resolver skips+counts)"* — and its footnote, *"that resolver
  currently proves no cross-file references and counts skips instead"*;
- `engine/link/resolve_bash.go`, `sqlResolver`'s doc comment — *"the resolver is
  an honest no-op: **it emits no edge** and fabricates nothing"*.

SW-183 measured all 22 shipped languages against fixtures rather than against
registrations. **The general concern is sound. The SQL instance is false.**

## 1. What was measured

Full method, per-row evidence and every negative result:
[`../rc/capability-audit-2026-08-19.md`](../rc/capability-audit-2026-08-19.md).
In short: 22 isolated two-file repositories, one per shipped language, each
carrying a real cross-file relationship written in that language's own syntax,
ingested under the profile the CLI resolves (`balanced`), the committed graph
then asked whether an edge spans the two files.

**Result: 16 of 16 languages with a registered resolver produce a genuine
cross-file edge; 6 of 6 without one produce none. No language's derived level
over-claims.**

SQL is one of the 16. `CREATE VIEW active_users AS SELECT id FROM users;` in
`query.sql`, against `CREATE TABLE users (id INT);` in `schema.sql`, yields:

```
references  query.active_users → schema.users   (query.sql → schema.sql, tier=derived)
```

Two independent counterfactuals establish that the **registration** is what
produces it — neither is inference:

| counterfactual | result |
|---|---|
| remove `l.Register(sqlResolver{})` from `engine/link.New`, rebuild the CLI, re-index the same fixture | the `references` edge **disappears**; `related-files query.sql` returns `outcome: empty`, `method: no_edges` |
| keep the registration, replace `sqlResolver.Resolve`'s body with `return nil` | the audit test reports **`OVER-CLAIM: sql is derived "cross-file-heuristic" but its fixture produces NO cross-file edge`** |

The mechanism, read back from source once the measurement pointed at it:
`sqlResolver.Resolve` calls `resolveRefs(in, idx, st, binder{})`
(`engine/link/resolve_bash.go`). An **empty binder disables the import-keyed
mechanisms only**. `resolveRefs`'s same-directory resolution
(`engine/link/resolve_common.go`) still runs, and a `derived`-tier
same-directory hit across two files is a cross-file edge. The doc comment
generalised "no import binding" into "no edge", and every downstream record
inherited it.

## 2. Decisions

**D1 — SQL keeps `cross-file-heuristic`. The level is earned, not over-claimed.**
`docs/rc/capability-audit-2026-08-19.md` carries the row and the counterfactuals.
This satisfies SW-183's AC-3 by its **second** branch ("or its cross-file
capability proven"), not by a re-grade.

**D2 — SQL's capability is bounded, and the bound is published with it.** SQL
resolves cross-file **within one directory** and never across directories: the
same fixture with `schema.sql` in `db/` and `query.sql` in `app/` produces
nothing. That is not a defect — SQL has no import construct to bind — and it is
inside `cross-file-heuristic`'s own definition, which reads *"a resolver links
references across files and packages … (or `derived` within a package)"*
(`capability.go:36-38`). It is published because a level without its bound is the
next over-claim.

**D3 — ADR-W1's premise is WITHDRAWN; ADR-W1's concern is NOT.** The sentence
"SQL's resolver deliberately proves nothing" is false and is retired wherever it
appears, by dated amendment (D6), never by rewrite. The *general* proposition —
that a registration-keyed derivation **can** over-claim — survives intact and is
now demonstrated executably rather than argued: mutation M4 above manufactures
exactly that state and the audit catches it. **ADR-W1 as a scheduled re-grade of
SQL is closed with no re-grade required.**

**D4 — the derivation's predicate stays REGISTRATION-based. The outcome binding
becomes a standing test, not a stronger predicate.** This is SW-183's AC-9
answer, and the reasoning matters more than the verdict:

- A *"registered **and** demonstrated"* predicate cannot be evaluated where the
  derivation runs. `trust.Capabilities` is a pure function over three registry
  observations, called on every trust-report read. "Demonstrated" is a property
  of a **fixture ingest**, which is a build-time measurement. Computing it at
  read time is impossible; caching it into the derivation reintroduces the
  hand-maintained table the design exists to avoid, and it would go stale
  silently — the exact failure mode ADR 0007 and PRD v1.0 §8 Phase 10 forbid.
- The honest place for the outcome check is therefore **CI**, and it now exists:
  `surfaces/client/capabilityaudit_test.go` reads the level from the same
  `languageCapabilities()` assembly the trust report serves and asserts it
  against fixture-measured evidence, in **both** directions. Registration and
  demonstration are no longer *assumed* co-extensive; they are *asserted* so, and
  the day they diverge the build says which language and in which direction.
- **Per-language registration stays the place to fix an over-claim.** When the
  audit goes red, the fix is to remove (or repair) that language's `Register`
  call — a local, reviewable, one-line change whose effect on the derivation is
  immediate and total. Nothing about the grading machinery needs to change for
  it, which is the property that makes a future re-grade cheap.

**D5 — LINK-004 is filed, disclosed, and NOT fixed here.** The audit found one
genuine defect, in Python and not in any of the languages the ticket suspected:
`from pkg.util import helper` and `import pkg.util` — dotted module paths naming
a module **inside** a package — resolve to nothing at all, neither a `calls` edge
nor an `imports` edge. `pyClause` (`engine/link/resolve_python.go:86-91`) keys a
module path on its last dotted segment, while a symbol's clause is its **parent
directory** base (`core/parse/parser_tswalk.go:240-251`); for `pkg.util` those
are `"util"` and `"pkg"`, and they can only coincide when the module path has one
segment — which is the shape every existing test uses. Single-segment forms
(`import util`, `from util import helper`, `from pkg import util`,
`from pkg import helper`) all resolve correctly. Fixing it is a linker change
carrying the full D7 ceremony and is not an audit's work. Disclosed per D8 on
`readme.md`, the doctor `known-defects` check and `docs/language-support.md`;
pinned by `TestLink004_PythonDottedModuleImportResolvesNothing`, which fails
**with instructions** the moment the defect closes.

**Python's LEVEL is untouched by this ADR.** LINK-004 is a recall defect *within*
`cross-file-heuristic`, not a challenge to it — four of five canonical import
forms resolve. Proving Python's level remains **SW-181's** job, which SW-183 is
explicitly scoped out of.

## 3. Consequences

- Two user-facing surfaces stated something false about SQL and are corrected:
  `docs/language-support.md` (row, footnote and the `imports`-target table) and
  `sqlResolver`'s doc comment. The comment is corrected **at source**, because
  the source comment is where the false claim was manufactured and every
  downstream record copied it.
- `docs/rc/capability-audit-2026-08-19.md` becomes the reference the Wave 3
  grading stories (SW-184, SW-185) consume instead of re-deriving levels.
- **A product-byte change is incurred and its ceremony is OWED, not performed.**
  Correcting a comment in a compiled file and adding a doctor disclosure both
  move product bytes — the Wave 0 handoff's own amendment measured that a *pure
  comment* reaches the binary through debug metadata. `parityreport.CandidateSHA`
  is **deliberately not moved** here: product bytes had already diverged from
  candidate `3b8d43f` before this story, a candidate move is already owed, and
  that divergence is a known **escalated owner decision**. This story joins that
  owed move; it does not create a second one and it does not resolve the first.
  No parity dispatch run at this commit is publishable, which is the provenance
  gate working as designed.
- **Graph bytes do NOT change.** Nothing in this story touches a parser, a
  resolver body, a binder or the ingest path. Measured rather than asserted: the
  audit fixtures' committed graphs are identical before and after.

## 4. What this ADR does not decide

- Whether LINK-004 is fixed, and at what tier its edges would land.
- Whether `related_files`' `no_edges` summary is honest — that is LANGHONEST-001,
  and §5 of the audit record narrows its scope but does not close it.
- Any language's GA status. This ADR grades capability levels; GA is SW-184 and
  SW-185, gated on evidence rows that do not yet exist.
- The stop-ship ruling on `heuristic`-tier wrong edges (LINK-002 §9). Untouched.
