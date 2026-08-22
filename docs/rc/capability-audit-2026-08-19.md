# Capability audit — all 22 shipped languages, graded against evidence

- **Date:** 2026-08-19
- **Story:** SW-183
- **Decision record:** [ADR 0012](../adr/0012-capability-levels-graded-on-demonstrated-evidence.md)
- **Mechanism:** `surfaces/client/capabilityaudit_test.go`
- **Measured at:** branch `claude/kotlin-java-canonical-ga-t3b8km`, parent `083e8f3`
- **Binary used for the CLI half:** `./cmd/graphi` built `CGO_ENABLED=0 -trimpath -buildvcs=false` → `87762557ef3400c71ed7f0d275ca9a16ebbd0ba84aa2083462c6b83936647cb7`

> **This is a published record. Per D6 it is never rewritten.** A later
> measurement goes on top as a new dated section; the rows below stay as
> measured, including the ones that turned out to correct a previously published
> claim.

## 0. The headline, stated before the table so it cannot be skimmed past

**No shipped language's derived capability level over-claims.** All 16 languages
with a registered cross-file resolver produce a genuine cross-file edge; all 6
without one produce none. Zero languages were re-graded down.

**That is a suspicious result for an over-claim audit, and it is not the whole
result.** The over-claims this story found are in the *published claims about*
capability, not in the derivation:

| # | Over-claim found | Direction | Disposition |
|---|---|---|---|
| 1 | `docs/language-support.md` SQL row + footnote: *"no provable cross-file refs at this tier; resolver skips+counts"* | **UNDER**-claim about SQL | **Re-graded**: corrected on the surface (D8); the row now states what SQL does and its bound |
| 2 | `engine/link/resolve_bash.go`, `sqlResolver` doc comment: *"it emits no edge"* | **UNDER**-claim, and the SOURCE of #1 | **Re-graded**: corrected at source |
| 3 | Programme plan §4 finding 3, G1, §7 wave 5, and ADR 0007 "Still open": *"SQL's resolver deliberately proves nothing"* | premise of the whole ADR-W1 workstream | **Withdrawn** by dated amendment (D6); ADR 0012 D3 |
| 4 | The programme plan's Wave 3 sketch assuming `bash` is `intra-file-only` | **UNDER**-claim about bash | **Resolved**: the live derivation is right, bash is `cross-file-heuristic`, measured (AC-4) |
| 5 | SW-180's LANGHONEST-001 root measurement — *"the graph carries 9 edges and ZERO file→file edges of any kind"*, generalised to *"no level is exempt"* | **OVER**-claim about the scope of a real defect | **Narrowed** by dated amendment; see §5 |

**How an over-claim in the derivation would have been detected, had one
existed** — stated because "we found none" is worthless without it. Mutation
**M4** (§4) manufactures exactly that state: SQL stays registered, its
`Resolve` body becomes `return nil`, the derivation still reads
`cross-file-heuristic`, and the audit reports
`OVER-CLAIM: sql is derived "cross-file-heuristic" but its fixture produces NO
cross-file edge`. The instrument was shown to work on the very language the
programme suspected, before being trusted on the other 21.

## 1. Method

For each of the 22 languages the parser registry ships:

1. an **isolated two-file repository** — never a shared fixture — carrying one
   real cross-file relationship written in that language's **own** syntax
   (`source ./util.sh`, `#include "util.h"`, `require_relative 'util'`,
   `mod util;`, `@import "base.css"`, …);
2. ingested through the real `Ingester` under **`profile.Balanced`**, which is
   what `core/profile.ResolveProfile` returns for every user-facing CLI pass. A
   capability measured under a profile no user runs is not a claim about the
   product;
3. the **committed graph** then asked whether any edge spans the two files.

**Three rules make the count mean something**, and each of them changed a row:

- **An edge into an `external` node never counts.** Minting a stub for an
  unresolved reference is the product's honest-miss path; counting it would let
  *"we failed to resolve this"* pass as cross-file capability. SW-180's 12-file
  measurement contains exactly one such edge.
- **Direction is not constrained.** `related_files` itself queries
  `direction: both`, and an inbound `imports` edge is as much a cross-file
  relationship as an outbound `calls` edge. Constraining direction would have
  re-graded Ruby down.
- **Non-vacuity first.** Every row names a witness symbol that must be a
  committed node before the row is allowed to report anything. Without it,
  "no cross-file edge" is indistinguishable from "the fixture never parsed" —
  and for `json`, whose parser mints no node at all, the *absence* is asserted
  instead.

**The language set is enumerated from the live derivation, not hand-listed.**
`TestCapabilityAudit_EveryShippedLanguageIsFixtured` reads
`languageCapabilities()` — the same assembly `graphi trust-report --json`
serves — and compares it to the fixture table in **both** directions. A grammar
landing tomorrow fails the build until somebody writes it a fixture. This is
AC-1's class-level discipline and the SW-150 lesson: a sweep that returns exactly
its own input is not evidence of exhaustiveness.

### Fixtures live in the test, not under `corpus/` — a deliberate template divergence

The per-language GA template (§3/S10) puts fixtures under `corpus/hero-<fam>/`.
This audit diverges, on the strength of that same document's own §7 enforcement
audit: **every corpus-backed slot is review-only for existence**, because the
guards read a hardcoded const path and nothing globs `corpus/hero-*` — a family
that ships no fixture directory is *silent*, not red. An audit whose entire claim
is exhaustiveness must not rest on the one storage choice that cannot fail by
omission. Recorded here as a divergence rather than worked around silently, per
the template's AC-7 practice.

## 2. The 22 rows

Every row's evidence column is **regenerated**, not transcribed:

```
CGO_ENABLED=0 go test ./surfaces/client/ -run TestCapabilityAudit_DerivedLevel -count=1 -v | grep 'AUDIT '
```

| # | Language | Derived level | Cross-file edge producible? | Evidence (kind, tier, span) | Grade |
|---|---|---|---|---|---|
| 1 | `go` | `typed-confirmed` | **yes** | `calls main.Run→pkg.Helper` **tier=confirmed**; `imports` heuristic | **holds** |
| 2 | `bash` | `cross-file-heuristic` | **yes** | `calls main.run→util.helper` derived; `imports` heuristic | **holds** (AC-4) |
| 3 | `c` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic (`main.c→util.h`) | **holds** |
| 4 | `c_sharp` | `cross-file-heuristic` | **yes** | `calls Main.Run→Lib.Helper` heuristic; `imports` heuristic | **holds** |
| 5 | `cpp` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic (`main.cpp→util.hpp`) | **holds** |
| 6 | `java` | `cross-file-heuristic` | **yes** | `calls app.run→lib.helper` heuristic — **no `imports` edge** | **holds** |
| 7 | `javascript` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 8 | `kotlin` | `cross-file-heuristic` | **yes** | `calls app.run→lib.helper` heuristic — **no `imports` edge** | **holds** |
| 9 | `lua` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 10 | `php` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 11 | `python` | `cross-file-heuristic` | **yes** | `calls app.run→pkg.helper` heuristic; `imports` heuristic | **holds**, with **LINK-004** (§3) |
| 12 | `ruby` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 13 | `rust` | `cross-file-heuristic` | **yes** | `calls main.run→util.helper` heuristic — **no `imports` edge** | **holds** |
| 14 | `sql` | `cross-file-heuristic` | **yes** | `references query.active_users→schema.users` **derived** — **no `imports` edge**, no cross-directory resolution | **holds — the published claim that it does not was FALSE** (§4) |
| 15 | `tsx` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 16 | `typescript` | `cross-file-heuristic` | **yes** | `calls` derived; `imports` heuristic | **holds** |
| 17 | `css` | `intra-file-only` | **no** | `@import "base.css"` with both files indexed → zero spanning edges. Question **askable** (CSS Cascade, `@import`) and answered negative | **holds** |
| 18 | `markdown` | `intra-file-only` | **no** | `[base](./base.md)` with both files indexed → zero spanning edges. Question **askable** (CommonMark §6.3) | **holds** |
| 19 | `yaml` | `intra-file-only` | **no** | question **NOT askable**: YAML 1.2.2 defines no include; `include:` is an ordinary mapping key | **holds** (abstention, E3) |
| 20 | `toml` | `intra-file-only` | **no** | question **NOT askable**: TOML v1.0.0 defines no include | **holds** (abstention, E3) |
| 21 | `hcl` | `intra-file-only` | **no** | question **NOT askable**: `module { source }` is Terraform's schema layered on HCL | **holds** (abstention, E3) |
| 22 | `json` | `parse-only` | **no** | no committed node of any kind; question **NOT askable** (RFC 8259) | **holds** |

**Nothing in this table is a pass by silence** (AC-8): each of the 22 rows is
backed by an ingested fixture, and the six negatives are separated into *askable
and answered negative* (css, markdown — the honest `intra-file-only` result) and
*not askable, abstained with a language-spec citation* (yaml, toml, hcl, json),
per the GA template §5.5. **Four rows carry a bound that a bare "holds" would
hide** — java, kotlin, rust and sql produce **no `imports` edge**, so
`related_files` on those languages rests on symbol edges alone.

### Levels are unchanged, so `trust-report --json` is unchanged

`typed-confirmed` 1 · `cross-file-heuristic` 15 · `intra-file-only` 5 ·
`parse-only` 1 — identical to the reading the spec took at `23da507`. **No
re-grade of the derivation was warranted, so none was made** (AC-2 / AC-6 have no
antecedent to fire on; see §7).

## 3. LINK-004 — the one genuine defect the audit found

**Python's dotted module imports resolve to nothing.** `from pkg.util import
helper` and `import pkg.util` — module paths naming a module **inside** a
package, and the two commonest import forms in real Python — produce **no edge
at all**: no `calls`, no `imports`.

Measured across the five canonical import forms, isolated fixtures, CLI built
from this branch:

| form | resolves? |
|---|---|
| `import util` (same dir) | **yes** — `calls`+`imports`, heuristic |
| `from util import helper` (same dir) | **yes** — `calls`+`imports`, derived+heuristic |
| `from pkg import util` → `util.helper()` | **yes** — `calls`+`imports` to `pkg/util.py` **and** `pkg/__init__.py` |
| `from pkg import helper` (defined in `__init__.py`) | **yes** — `calls`+`imports` |
| **`from pkg.util import helper`** | **NO — zero edges** |
| **`import pkg.util` → `pkg.util.helper()`** | **NO — zero edges** |

**Mechanism**, read back from source after the measurement pointed at it:
`pyClause` (`engine/link/resolve_python.go:86-91`) keys a module path on its
**last dotted segment**, while a symbol's clause is its **parent directory** base
(`langPackage`, `core/parse/parser_tswalk.go:240-251`). For `pkg.util` those are
`"util"` and `"pkg"` — they coincide only when the module path has **one**
segment, which is the shape every existing test in
`engine/link/resolve_python_test.go` uses (`pkg.dup` in `a/pkg/x.py`). The
resolver's own doc comment states the keying correctly at `:13-14` and `:84-85`;
what neither it nor any test noticed is that the last segment of a dotted path
names a **file**, and clauses are keyed on **directories**.

**Not fixed here.** Fixing it is a linker change carrying the full D7 ceremony;
this story is an audit. **Disclosed** per D8 on `readme.md` "Known limits", the
doctor `known-defects` check and `docs/language-support.md`. **Pinned** by
`TestLink004_PythonDottedModuleImportResolvesNothing`, which asserts the current
broken behaviour and fails **with instructions** the moment the defect closes —
so the fix cannot land while the disclosure stays up.

**Python's LEVEL is not challenged by this.** Four of five forms resolve;
LINK-004 is a recall defect *within* `cross-file-heuristic`. Proving Python's
level is **SW-181's** job and this audit does not shortcut it.

**Not measured:** how much of a real Python repository's import graph this loses.
The forms are enumerated; their frequency in the wild is not.

## 4. SQL — the known over-claim that is not one (AC-3)

The programme carried SQL as its worked instance of a registration-derived
over-claim. **Measured, it is not one.**

```
query.sql : CREATE VIEW active_users AS SELECT id FROM users;
schema.sql: CREATE TABLE users (id INT);
→ references query.active_users → schema.users  (query.sql → schema.sql, tier=derived)
```

Two independent counterfactuals, both executed, neither inferred:

| # | Mutation | Result |
|---|---|---|
| **M1** | remove `l.Register(sqlResolver{})` from `engine/link.New`, rebuild the CLI (`2754aedf…`), re-index | edge **gone**; `related-files query.sql` → `outcome: empty`, `method: no_edges`. The audit test still passes, correctly: the derivation drops SQL to `intra-file-only` and the evidence agrees — a coherent re-grade, not a red |
| **M4** | keep the registration, `sqlResolver.Resolve` body → `return nil` | audit reports **`OVER-CLAIM: sql is derived "cross-file-heuristic" but its fixture produces NO cross-file edge`**, listing the two surviving `defines` edges |

M4 is the decisive one: it isolates the resolver **body** from the
**registration** and shows the edge is produced by the former.

**Why the false claim was believable.** `sqlResolver.Resolve` calls
`resolveRefs(in, idx, st, binder{})`. An empty binder disables the **import-keyed**
mechanisms — and SQL has no import construct, so that part of the comment is
true. But `resolveRefs`'s **same-directory** resolution still runs, and a
`derived`-tier same-directory hit across two files **is** a cross-file edge. The
comment generalised "no import binding" into "no edge", and five downstream
records copied it verbatim.

**The bound, published with the capability so the correction does not become the
next over-claim:** SQL resolves cross-file **within one directory only**. The
same fixture with `schema.sql` in `db/` and `query.sql` in `app/` produces
nothing. That is inside `cross-file-heuristic`'s own definition — *"…(or
`derived` within a package)"* (`engine/trust/capability.go:36-38`) — and it is
not a defect: SQL has no import construct to bind.

## 5. LANGHONEST-001 — the scope claim this audit narrows

SW-180 measured, on a mixed 12-file fixture, that the graph carried **9 edges and
zero file→file edges of any kind**, that `app.py`'s
`from pkg.util import helper` produced no edge, and concluded that
`related_files`' `no_edges` sentence is *"a lie at every level below
`typed-confirmed`"* — with the "zero file→file edges" framing offered as the
explanation of *why* no level is exempt. Its round-2 reviewer independently
reproduced the 9/0 figures and endorsed the framing.

**The figures are right. The generalisation is not, and this audit narrows it.**
SW-180's Python fixture used `from pkg.util import helper` — the **one** import
form that hits LINK-004. Four of the other five forms produce `calls` **and**
`imports` file→file edges, so "zero file→file edges of any kind" is a property of
**that fixture**, not of the product, and it is not the reason css and markdown
answer `no_edges`.

**What survives untouched:** LANGHONEST-001 itself. `main.css` (`@import`) and
`readme.md` (a markdown link) genuinely have the construct, genuinely produce no
edge, and genuinely receive the summary *"resolved (file) but has no both edges
to other files"* — which states an absence in the code that is not there. That
finding is confirmed by row 17 and row 18 above. What changes is only its
**stated cause and scope**: it is an `intra-file-only` honesty defect, not a
product-wide absence of file→file edges.

This is SW-180's own methodological rule — *"a fixture that cannot express the
relation under test cannot validate the rule about it"* — applied to SW-180. The
correction is filed as a **dated amendment** to
[`../plan/2026-08-per-language-ga-template-v1.md`](../plan/2026-08-per-language-ga-template-v1.md)
per D6; nothing there is rewritten or re-pointed.

## 6. Negative and inconclusive results, recorded rather than deleted (D5)

- **Three of this audit's own first-pass fixtures were wrong**, and each would
  have re-graded a language downward on a fixture defect. Recorded because the
  ticket's own review note names both failure directions ("too easy" and "too
  hard") and this audit hit the second one three times:
  - `ruby`: bare `helper` (no parens) parses as an identifier read and records no
    call ref. Ruby looked cross-file-incapable. With `helper()` it resolves.
  - `javascript`: `from "./util.js"` (explicit extension) yields the `calls` edge
    but **no** `imports` edge; `from "./util"` yields both. A measured recall
    quirk, published here, not a level change.
  - `python`: `from pkg.util import helper` yields nothing — the one case that
    was **not** a fixture defect. It is LINK-004.
- **The UNDER-claim branch of the audit is structurally unexercisable today, and
  is a guard rather than a check.** `Linker.Link` returns nil for any language
  with no registered resolver (`engine/link/link.go:224-228`), and a language
  with a semantic resolver would derive `typed-confirmed`, so no language can
  produce cross-file edges while deriving `intra-file-only`. Mutation M3
  (registering a resolver for css) therefore surfaces as an **over**-claim, not
  an under-claim. The assertion is kept because it costs nothing and it is the
  only thing standing between a future derivation change and a silent
  under-claim — but it is **not** demonstrated red, and saying it is covered
  would be exactly the vacuity this programme audits for.
- **`references` edges are unverified for ruby, php and lua.**
  `docs/language-support.md` promises them `calls` / `references` / `imports`;
  this audit measured `calls` and `imports` and did **not** construct a
  reference-only fixture. Not a proven over-claim; an unmeasured claim, named so
  it is not mistaken for a measured one.
- **Every measurement here is a two-file hermetic fixture.** None of it is a
  real-repository recall figure, and none of it says anything about *how much* of
  a real tree each language resolves. That is G6/binding-rate work (SW-175 and
  the per-language stories), deliberately not attempted here.
- **The `surfaces` MCP-journey flake did not fire** in any run of this story.
  Recording that it did not is the discipline; it is not a claim that it is
  fixed.

## 7. AC-9 — the recommendation, and why it is not a stronger predicate

**Recommendation: keep the derivation's predicate registration-based; bind the
outcome in CI instead. Per-language registration stays the place to fix an
over-claim.** Full reasoning in [ADR 0012](../adr/0012-capability-levels-graded-on-demonstrated-evidence.md)
D4; in short:

- *"registered **and** demonstrated"* cannot be evaluated where the derivation
  runs — `trust.Capabilities` is a pure function over three registry
  observations, called on every trust-report read, while "demonstrated" is a
  property of a fixture ingest. Caching it back into the derivation reintroduces
  the hand-maintained table the design exists to avoid, and it goes stale
  silently — the exact failure PRD v1.0 §8 Phase 10 forbids.
- The honest place is CI, and it now exists. `capabilityaudit_test.go` reads the
  level from `languageCapabilities()` and asserts it against measured evidence in
  both directions.
- **Honest limit on that recommendation:** on today's 22 languages, registration
  and demonstration coincide **exactly** (16/16 and 6/6). So the stronger
  predicate would change no row today, and this audit is therefore *not* evidence
  that it is unnecessary — only that it is currently unfalsifiable by the shipped
  set. What made the difference is not the predicate but the **instrument**: the
  concern was real, correct in general, and wrong in its one worked instance, and
  only a fixture could tell those apart.

## 8. How to re-verify this record

```bash
# The audit itself, all 22 rows, with per-row evidence regenerated.
CGO_ENABLED=0 go test ./surfaces/client/ -run TestCapabilityAudit -count=1 -v | grep 'AUDIT '

# The completeness half: enumerated from the live derivation, both directions.
CGO_ENABLED=0 go test ./surfaces/client/ -run TestCapabilityAudit_EveryShippedLanguageIsFixtured -count=1

# LINK-004's pin (asserts the DEFECT; fails with instructions when it is fixed).
CGO_ENABLED=0 go test ./surfaces/client/ -run TestLink004 -count=1 -v

# M4 — the mutation that proves the instrument detects an over-claim. In a
# throwaway worktree, replace sqlResolver.Resolve's body with `return nil`, then:
#   go test ./surfaces/client/ -run TestCapabilityAudit_DerivedLevel/sql
# expect: OVER-CLAIM: sql is derived "cross-file-heuristic" …

# The levels are unmoved: 1 / 15 / 5 / 1.
graphi trust-report --json | jq -r '.capabilities[] | .level' | sort | uniq -c
```
