# graphi P2 — Language GA program v1 (every shipped language to canonical GA, starting with Java/Kotlin)

> ## AMENDMENT — 2026-08-19 (SW-183): ADR-W1's premise is withdrawn; its concern is not
>
> Per D6 this amendment is **added**. Nothing below is rewritten, re-pointed or
> deleted — the statements it supersedes are named here rather than edited in
> place, so a reader who arrives at them sees what they said and why they are
> wrong.
>
> **1. Three statements in this document are FALSE and are withdrawn.** All three
> say the same thing in different words:
>
> | where | statement | status |
> |---|---|---|
> | **§3, G1** | *"today SQL reads `cross-file-heuristic` while its resolver deliberately proves nothing"* | **FALSE** |
> | **§4, finding 3** | *"SQL's capability label is registration-derived, not outcome-derived"* — as a claim about SQL | **FALSE as applied to SQL**; true as a claim about the derivation in general |
> | **§7, wave 5** | *"SQL first requires the ADR-W1 re-grade: either the resolver learns something provable or the declared level drops"* | **DISCHARGED with no re-grade** — the resolver already proves something |
>
> SQL's resolver **does** resolve cross-file references. A view referencing a
> table declared in a sibling file yields
> `references query.active_users → schema.users` at the `derived` tier.
> Established by measurement plus **two counterfactuals**: removing
> `l.Register(sqlResolver{})` makes the edge disappear, and replacing
> `sqlResolver.Resolve`'s body with `return nil` makes the capability audit report
> an over-claim. The mechanism the original claim missed: an empty binder disables
> the **import-keyed** mechanisms only, while `resolveRefs`' same-directory
> resolution still runs. The false claim originated in `sqlResolver`'s own doc
> comment (*"it emits no edge"*) and was copied verbatim into all five downstream
> records, this document's three included.
>
> **The bound is published with the capability**, so the correction does not
> become the next over-claim: SQL resolves cross-file **within one directory
> only** and emits **no `imports` edge**. That is inside `cross-file-heuristic`'s
> own definition, not a defect.
>
> **2. G1's general requirement STANDS and is now met differently than G1
> imagined.** G1 says *"the derivation must reflect resolver **outcomes**, not
> bare registration"*. The concern is correct — a registered resolver that
> resolves nothing would still grade `cross-file-heuristic`, and mutation M4
> manufactures exactly that state. But the fix G1 implies (a stronger predicate
> inside `trust.Capabilities`) is **rejected**, because "demonstrated" is a
> property of a fixture ingest and cannot be evaluated where a pure read-time
> derivation runs; caching it back reintroduces the hand-maintained table the
> design exists to avoid. **The outcome binding lives in CI instead**:
> `surfaces/client/capabilityaudit_test.go` asserts every shipped language's
> derived level against a measured cross-file edge, in both directions, and fails
> the build for an unfixtured new language. Ruling:
> [ADR 0012](../adr/0012-capability-levels-graded-on-demonstrated-evidence.md) D4.
>
> **3. §7's Wave 3 sketch assumption about `bash` is withdrawn.** The sketch
> assumed bash was `intra-file-only`; the live derivation says
> `cross-file-heuristic` and **the live derivation is right** — `source ./util.sh`
> followed by a call to a function the sourced script defines yields both a
> `calls` edge (derived) and an `imports` edge (heuristic). Measured; row 2 of the
> audit.
>
> **4. All 22 shipped languages are now graded against evidence, and none
> over-claims.** 16 of 16 languages with a registered resolver produce a genuine
> cross-file edge; 6 of 6 without one produce none. Levels are unmoved
> (`typed-confirmed` 1 · `cross-file-heuristic` 15 · `intra-file-only` 5 ·
> `parse-only` 1), so **no re-grade was made** and the Wave 3 grading stories
> (SW-184, SW-185) may consume the published table instead of re-deriving levels:
> [`../rc/capability-audit-2026-08-19.md`](../rc/capability-audit-2026-08-19.md).
>
> **5. One genuine defect was found, in a language this document did not
> suspect.** **LINK-004** — Python's dotted module imports (`from pkg.util import
> helper`, `import pkg.util`) resolve to **nothing**. Filed, disclosed per D8, not
> fixed, and pinned by a test that fails with instructions when it closes.
> Python's **level** is untouched and remains SW-181's to prove. Audit §3.
>
> **What did NOT change.** `parityreport.CandidateSHA` is unmoved; the published
> parity matrix and every evidence row are untouched; no graph byte moves. The
> product-byte ceremony this story incurs is **owed, not performed** — product
> bytes had already diverged from candidate `3b8d43f` before it, and that
> divergence is a known escalated owner decision.

- **Status:** PROPOSED — no work package has started; every evidence row this
  program names is born UNKNOWN, and UNKNOWN counts as *not passed*.
- **Date:** 2026-08-14
- **Authority:** this document plans the **language axis only**. The 12 frozen
  operations, the two GA surfaces (CLI, MCP stdio) and the CGo-free build are
  untouched throughout; where this document and
  [`../stability-tiers.md`](../stability-tiers.md) or the machine-checked
  matrices disagree, the matrices win and this document changes.
- **Relationship to P1:** this program consumes P1's trust surface (the
  capability matrix, `engine/trust/capability.go`) and must not start WP-J3+
  before P1 Phase 10 closes. WP-J0 and WP-J1 are safe to start earlier — they
  are behavior-preserving groundwork.
- **Decision owner:** Sami. §9 lists the ADR-worthy decision points; two ADR
  skeletons ship with this plan
  ([ADR 0007](../adr/0007-semantic-resolver-registry.md),
  [ADR 0008](../adr/0008-jvm-declared-type-resolution.md), both *Proposed*).

## 1. What "canonical GA" is — the bar this program holds every language to

[`docs/stability-tiers.md`](../stability-tiers.md) defines GA as a conjunction:

```
GA  =  operation ∈ {the 12 tier:stable rows}     ← pinned by cmd/coverage -check
   AND language  = Go                            ← NOT encoded in the matrix (prose only)
   AND surface   ∈ {CLI, MCP stdio}
   AND build     = the CGo-free default binary
```

This program moves exactly one conjunct: **the language axis**. "Canonical GA"
for a language L means L clears the *same evidence bar Go clears today* — not a
lighter one, and not a prose promotion. Go's bar, named by artifact:

| Evidence Go has | Artifact |
|---|---|
| `confirmed`-tier semantic edges (type-checker-proven) | `engine/typeresolve` (go/types, third ingest phase; `Languages()` = `["go"]`, `engine/typeresolve/check.go:44`) |
| Hermetic change-class parity, byte-exact, both stores | `engine/conformance` (`TestFullVsIncremental_ByteParity`), bound to [`../rc/parity-classes.yaml`](../rc/parity-classes.yaml) by the six drift-guard directions |
| Real-repository parity with the built binary | `internal/parity` + `cmd/parity`, published at [`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md); defects filed (PARITY-001/002), pinned, never hidden |
| Pinned, measured, stratified corpus | `corpus/manifest.json` v3 (six Go repos, 10-property stratification, full-sha pins, `measured` blocks from real clones) |
| Hero gate: 20 scenarios over exactly the 12 ops | `corpus/hero/*.yaml` + `cmd/eval` hero gate on `corpus/fixtures/hero-go` |
| Perf evidence with checked-in raw runs | cold-index / query-latency / freshness / progress-stalls under `docs/eval/runs/…` |
| Honest, registry-derived capability surface | `engine/trust/capability.go` fed by `typeresolve.Languages()`, `link.Linker.Languages()`, `parse.SymbolCapable` (`surfaces/client/trust_report.go:333`); re-derived by `surfaces/client/capability_test.go` |
| Claims discipline | [`../rc/evidence-index.yaml`](../rc/evidence-index.yaml): PASS requires evidence URI + sha; UNKNOWN/STALE count as not passed |

## 2. The generalized per-language GA bar (G1–G9)

GA for a language L is the conjunction of nine checkable items. Each names the
Go artifact it generalizes. These are the gate IDs this program's evidence-index
rows will use (`GA-LANG-<lang>-G<n>`).

- **G1 — Declared capability contract.** L declares a target capability level
  from the closed vocabulary in `engine/trust/capability.go`
  (`typed-confirmed` > `cross-file-heuristic` > `intra-file-only` >
  `parse-only`), and the level *derived from the live registries* equals the
  declaration. New for this program: the derivation must reflect resolver
  **outcomes**, not bare registration — today SQL reads `cross-file-heuristic`
  while its resolver deliberately proves nothing
  ([`../language-support.md`](../language-support.md), SQL note). That wrinkle
  becomes a blocking gap under this bar (§9, ADR-W1).
- **G2 — Semantic (`confirmed`-tier) resolution where L admits it.** A
  third-phase resolver registered behind the generalized seam (WP-J0), under
  the identical hard constraints of `engine/typeresolve/doc.go`: pure Go, no
  network, no external toolchain, CGo-free, deterministic, kill switch. Two
  mandatory sub-artifacts: **(G2a)** a NodeId-identity map with a byte-exact
  golden cross-test against the real `core/parse` extractor (the
  `engine/typeresolve/qn.go` pattern — a confirmed edge may only attach to a
  node the extractor actually created); **(G2b)** the under-approximation
  invariant: partial information may only *drop* edges (skip+count), never mint
  false ones. Languages whose declared level is below `typed-confirmed` skip G2
  and instead prove their heuristic resolver's contract (never-confirmed,
  drop+count, deterministic) at the same test strength.
- **G3 — Hermetic change-class parity.** Rows for L in the declarative
  conformance table: full-vs-incremental snapshot **byte** parity on MemStore
  **and** SQLite, non-vacuous witnesses proven red-without-the-change, bound to
  a per-family `docs/rc/parity-classes-<family>.yaml` by the same six
  drift-guard directions. Every Go class gets an explicit per-language
  applicability disposition (`applicable` / `adapted` / `not_applicable` +
  reason), plus language-specific classes (§5, WP-J5).
- **G4 — Real-repository parity.** `internal/parity` drives the built binary
  over L's pinned clones; results published in the
  `parity-matrix-real-repo.md` pattern; divergences filed as `PARITY-0xx`.
- **G5 — Corpus stratification + pins.** L's `corpus/manifest.json` entries
  reach the v3 Go standard: release-tag ref + full 40-char sha, a `measured`
  block from a real clone at the pinned sha, tier assignment, and an L-specific
  stratification (~8–10 properties analogous to the Go ten).
- **G6 — Hero gate.** ~20 hero scenarios over exactly the 12 stable ops for L,
  on a checked-in `corpus/fixtures/hero-<lang>` fixture, including negative
  anchors and honest-empty/abstention scenarios.
- **G7 — Perf + budget evidence.** The four perf suites include L's corpus with
  raw run artifacts under `docs/eval/runs/…`; the grammar blob stays inside its
  [`../../bench/lang-budget.md`](../../bench/lang-budget.md) allocation and the
  whole-binary budget gate.
- **G8 — Honest capability surface.** Registries report L at the declared
  level; `capability_test.go` re-derives it; the
  [`../language-support.md`](../language-support.md) row flips; trust-report
  `--json` and prose agree.
- **G9 — Evidence-index rows + the machine GA-language check.** One
  `GA-LANG-<lang>-G2..G8` row per gate under the evidence-index discipline, and
  the language appears in the machine-encoded GA-language mechanism (§6), whose
  check requires all of the above.

**Cross-cutting rule (not a row):** defects found on the way get IDs and stay
published as executable data (the `known_defect`/pinned-behaviour pattern of
`delete_file`/PARITY-001). One sharpening unique to this program: **a
demonstrated false `confirmed` edge is a stop-ship for the GA flip** — unlike a
parity defect, it falsifies the tier's meaning, not merely a coverage claim.

## 3. GA-at-declared-capability — a first-class concept, not a footnote

Ruled by the owner at program start (2026-08-14): **every shipped language can
reach GA**, including the ones that can never reach `typed-confirmed`.

GA for a language means: *its declared capability level (G1) is fully proven at
the same evidence bar, and every surface says the level.* The bar's items scale
with the level:

| Declared level | The bar |
|---|---|
| `typed-confirmed` | all of G1–G9 |
| `cross-file-heuristic` | G2 replaced by a proof of the heuristic resolver's own contract (never-confirmed, drop+count, deterministic) at equal test strength; everything else unchanged |
| `intra-file-only` | change-class set shrunk via the applicability disposition; hero scenarios constrained to intra-file semantics |
| `parse-only` | parse determinism, parity, and honest behavior of all 12 ops over structural nodes |

The invariant at every level: **no operation lies.** `callers` on a YAML file
returns a well-formed honest empty (the `hero-03-search-empty` pattern), and
the trust report names the level. "GA" never appears beside a language on any
surface without its level.

> **Added 2026-08-19 (SW-180).** The per-slot generalization of §2's gates —
> which asset each gate needs, where it lives, what a language may legitimately
> abstain from and what its evidence class degrades to when it does — is
> [`2026-08-per-language-ga-template-v1.md`](2026-08-per-language-ga-template-v1.md).
> That document also records, from a measured non-JVM instantiation, that the
> honest-empty invariant as written above is **not satisfiable today at ANY
> capability level below `typed-confirmed`**, in two distinct shapes — and that
> `outcome: empty` alone — the `hero-03-search-empty` assertion — does **not**
> discriminate an honest empty from a lying one. Both are owner decisions
> raised there, not resolved here; this paragraph is added so a reader of §3
> does not take the invariant as already met.
>
> **Widened 2026-08-19 (SW-180, rebuild round 1), because the first version of
> this paragraph said "`parse-only`" and that is one level too narrow — it
> exempted the level SW-181 and SW-182 target.** Filed as **LANGHONEST-001**:
>
> - **`parse-only`** — a parse-only file has no graph node at all, so three
>   Stable-12 operations answer as though the anchor were **mistyped**.
> - **`intra-file-only` AND `cross-file-heuristic`** — `related_files` states
>   *"anchor … resolved (file) but has no both edges to other files"* **as
>   fact** over a file that carries the reference. Measured on `main.css`
>   (`@import`), `readme.md` (a link), and — decisively — on **`app.py`
>   (`from pkg.util import helper`), which is `cross-file-heuristic`, has a
>   registered resolver, and whose extractor demonstrably computes imports**.
>   All four return the byte-identical sentence. This shape is the more
>   dangerous, because `outcome` and `method` both read as a **successful
>   answer** rather than as a refusal.
>
> **The practical consequence for SW-181 and SW-182:** neither may treat
> `confidence.method == "no_edges"` on its own language as the honest-empty
> shape. It is the shape the defect takes at their level.

## 4. Where Java and Kotlin stand today (gap analysis, code-grounded)

Both are Preview at `cross-file-heuristic`:

- **Parsers:** `core/parse/parser_java.go` / `parser_kotlin.go` — pure-Go
  gotreesitter subset grammars, symbol nodes + intra-file edges, package nodes.
  Grammar blobs: Java 46,587 B; Kotlin 337,236 B — the largest in the default
  set (`bench/lang-budget.md`).
- **Cross-file:** `engine/link/resolve_java.go` — `javaResolver` and
  `kotlinResolver` share `fqnImportBinder` (FQN imports, package-clause keying,
  wildcard imports, interned external nodes for unresolvable FQNs). Known limit,
  stated in the code: **instance calls through a variable receiver are not
  resolvable** (receiver type unknown at CST level) → skip+count, never guessed.
- **No `confirmed` tier:** there is no typeresolve equivalent for the JVM.

Four findings that shape the work (verified against the tree, 2026-08-14):

1. **The JVM extractors are shallower than a binder needs.** The Java extractor
   deliberately collapses kinds onto `{file, method, type}` — no field/property
   nodes, no declared-type metadata anywhere in the extracted graph. Receiver
   typing needs information the extractor does not emit today (→ WP-J2,
   ADR 0008 decision D3).
2. **The ingest seam is hard-coded to Go, not just `typeresolve.Languages()`.**
   `engine/ingest/typeresolve.go` gates on the `.go` filename suffix (lines 70,
   89, 192). Generalizing `Languages()` alone is insufficient; the dispatch
   itself must become registry-driven (→ WP-J0, ADR 0007).
3. **SQL's capability label is registration-derived, not outcome-derived** —
   the honesty wrinkle named in G1 (→ ADR-W1, wave 5).
4. **Grammar-tag coupling around Java:** `grammar_subset_toml`/`grammar_subset_lua`
   reuse lexer symbols from `java_lexer.go` and fail to compile in isolation
   without `grammar_subset_java` (`bench/lang-budget.md`). Any Java grammar work
   must keep the accumulated-set build green, not just Java's.
- **Corpus:** guava v33.0.0 is pinned (tier 3) but — like every non-Go entry —
  carries **no `measured` block and no stratification properties**; the
  manifest-v3 discipline exists only for the six Go pins. No Kotlin repository
  is pinned. `corpus/fixtures/kotlin` exists with self-declared *Partial*
  parity. Hero scenarios and the conformance change-class harness are Go-only.

## 5. Phase JV — the Java/Kotlin work packages

### 5.1 The crux: `jvmresolve`, a declared-type binder (ADR 0008)

A new engine package (working name `engine/jvmresolve`) registered beside
`engine/typeresolve` behind the WP-J0 seam, raising JVM edges to `confirmed`
with **declared types only — no inference, ever**. Like `typeresolvePass`, it
recomputes over the entire walked snapshot (parity by construction) and
re-parses the `.java`/`.kt` sources itself with the same gotreesitter grammars
(mirroring how typeresolve re-parses with `go/parser`).

- **Phase A — class/package/member table.** Package declarations; imports
  (single-type, on-demand `*`, `import static`, Kotlin aliases); top-level and
  nested/inner types; per type: supertype names as written, fields/properties
  with declared types, methods/constructors with parameter and return types and
  modifiers; Kotlin companion objects, `object` declarations, top-level
  functions. Type-name resolution follows a JLS-scoping approximation with a
  strict ambiguity rule: own/enclosing types → single-type imports → same
  package → on-demand imports (any ambiguity ⇒ unresolved, drop+count) →
  otherwise external (`java.lang` included — external types never yield
  confirmed edges).
- **Phase B — declared-type propagation, no inference.** Typed: explicitly
  typed fields/properties, parameters, explicitly typed locals, `this`/`super`,
  constructor results, qualified statics, chains whose every link's declared
  return type is an intra-repo type. Untyped — skip+counted with **named
  counters**: Java `var` locals; every inferred Kotlin `val`/`var`;
  lambda/scope-function receivers (`apply`, `let`, `run`, implicit `it`);
  extension-function receivers; members inherited from *external* supertypes
  (no classpath ⇒ the lookup chain fails closed the moment it exits the repo).
- **Phase C — member lookup + emission.** `calls`: receiver's declared type T,
  member lookup by name walking the intra-repo supertype chain; **binding
  requires uniqueness at (name, arity)** — any overload ambiguity at that key
  drops+counts. `references`: type references in extends/implements/field/
  param/return positions. `implements`/`inherits`: nominal — the declared
  clause is the proof once both endpoint names resolve intra-repo.

**What `confirmed` honestly means here (ADR 0008 D1, recommended).** A
confirmed JVM `calls` edge asserts **static binding**: "this call site's
receiver has declared type T, and T's uniquely-named (name, arity) member M is
the statically bound target; runtime dispatch may select an override, reachable
via implements/overrides edges." This is the contract Go already GA'd —
`go/types` binds an interface-value call to the interface's method object, not
the dynamic implementation — and it is exactly what `javac` encodes in bytecode
(`invokevirtual` names the declared type's method ref), which makes the
differential ground-truth check (WP-J9) a direct check of the contract. What is
genuinely weaker than go/types — no inference, no argument-type overload
resolution, no classpath, no annotation-processor output (Lombok) — all fails
**closed** under G2b: the weakness costs recall, never soundness.

**Kotlin's narrower contract (ADR 0008 D2, measure first).** Idiomatic Kotlin
infers most locals, so Kotlin's confirmed *coverage* will be materially lower
than Java's under the identical regime. Kotlin still qualifies for
`typed-confirmed` — the level names "the STRONGEST evidence available"
([`../language-support.md`](../language-support.md)), not a share — and honesty
is preserved by publishing a **measured confirmed-edge share per language** as
a named evidence artifact, never a marketing number. The decision stays open:
if the measured Kotlin share on the pinned corpus comes back negligible
(threshold ruled by the owner), the honest fallback is Kotlin GA at
`cross-file-heuristic` in wave 1 with `typed-confirmed` deferred.

**Differential ground truth — CI only, never product (WP-J9).** A
nightly/dispatch workflow installs a JDK/kotlinc **on the runner only**,
compiles pinned corpus repos, extracts static call-binding facts from bytecode
(constant-pool method refs via `javap` — which *is* the D1 contract), and
checks two directions: **soundness** — every graphi confirmed edge appears in
ground truth; zero tolerance; any counterexample is a `JVMSOUND-0xx` defect and
blocks the flip. **Recall** — measured and published, informational, not gated.
The product never gains a toolchain dependency.

### 5.2 Work packages

| WP | Deliverable | Gate |
|---|---|---|
| **WP-J0** | Generalize the semantic-resolver seam (ADR 0007): registry mirroring `engine/link`'s open/closed `Register`; `engine/typeresolve` becomes the first registrant; the `.go`-suffix dispatch in `engine/ingest/typeresolve.go` becomes registry-driven; trust surface consumes the registry union; kill switches `GRAPHI_NO_TYPERESOLVE` (global, kept) + `GRAPHI_NO_TYPERESOLVE_<LANG>` | **Behavior-preserving to the byte:** full conformance suite, hero gate, and before/after snapshot-byte comparison on the Go corpus all unchanged |
| **WP-J1** | Machine-encode the GA language axis (§6) — deliberately early, while the set is `{go}` | `cmd/coverage -check` green with exactly one `ga-language` row (`go`); prose flip in `stability-tiers.md` |
| **WP-J2** | Extractor deepening: field nodes (Java) / property nodes (Kotlin) + declared-type metadata (ADR 0008 D3); then the `qn.go`-analog identity map with byte-exact golden cross-test (G2a) | Snapshot-byte change is versioned + re-baselined deliberately, conformance re-run in the same commit |
| **WP-J3** | Java binder (phases A–C), dark behind the kill switch; purity/determinism tests (`engine/link/purity_test.go` pattern); named skip counters for every untyped path | G2b invariant tests green; zero confirmed edges to external targets |
| **WP-J4** | Kotlin binder on the shared table infrastructure; inferred-val / scope-function / extension-receiver skip counters as first-class observability | same as WP-J3 + the D2 share measurement published |
| **WP-J5** | Hermetic change-class rows: new `docs/rc/parity-classes-jvm.yaml` (same constrained YAML subset, own `matrix_version`, same six guard directions) + harness rows, both stores, non-vacuous witnesses | Drift guard green; every Go class dispositioned (see below) |
| **WP-J6** | Corpus deepening: guava to the v3 standard; new pins (candidates below — **pins must be MEASURED at pin time**); JVM stratification (~10 axes) | Manifest v3 discipline holds for every JVM entry |
| **WP-J7** | Real-repo parity over the JVM pins via `internal/parity`; publish; file defects | Entry criterion (ADR 0008 D8): PARITY-001/002 fixes land first, else every JVM verdict starts PARTIAL for non-JVM reasons |
| **WP-J8** | Hero-JVM: `corpus/fixtures/hero-java` + `hero-kotlin`, ~20 scenarios each over the 12 ops, incl. confirmed-tier-pinning anchors (`tier == confirmed` witnesses) and honest-empty scenarios for the untyped gaps | Hero gate green in `cmd/eval` |
| **WP-J9** | Differential ground-truth CI job (`jvm-groundtruth.yml`, nightly/dispatch, runner-only JDK) | Soundness direction: zero counterexamples |
| **WP-J10** | Perf + budget: the four suites over the JVM corpus; runs published; Kotlin's 337 KB blob inside the budget gate; `GA-LANG-java-*` / `GA-LANG-kotlin-*` evidence rows created (born UNKNOWN) | Raw artifacts under `docs/eval/runs/…` |
| **WP-J11** | The flip: registries report java/kotlin at `typed-confirmed`; `capability_test.go` extended; `language-support.md` + `stability-tiers.md` updated; §6 check fed `java`, `kotlin` | **Stop-ship:** any open `JVMSOUND-0xx`; any evidence row UNKNOWN/STALE |

**Change-class mapping (WP-J5).** Map directly: `add_file`, `modify_file`,
`delete_file`, `rename_symbol`, `move_symbol`, `add_call`, `remove_call`,
`change_external_import`, `replace_generated_file`. Adapt: `rename_package`
(directory + package clause + importers), `change_interface` /
`add_implementation` / `remove_implementation` (nominal: add an interface
method; add/remove an `implements` clause; default-method variant).
Language-agnostic/deferred: `branch_switch` (SW-158 pattern). **not_applicable
with recorded reason:** `change_build_tag` (graphi evaluates no build
constraints; no honest JVM analog). New JVM-specific classes (~6, frozen in the
YAML): `change_overload` (a new overload makes a (name,arity)-unique binding
ambiguous — the confirmed edge must *drop*, witness pins exactly that; plus the
inverse), `move_nested_class`, `change_type_hierarchy`,
`change_import_shadowing`, `kotlin_infer_declared_flip`
(`val x: Foo = …` ↔ `val x = …` — confirmed edges must appear/vanish),
`move_top_level_function` (Kotlin file facades / companion moves).

**Corpus candidates (WP-J6 — candidates, not commitments; pins measured at pin
time).** Java: a small library (gson or commons-lang), a CLI (picocli), a
service (spring-petclinic), a generated-code carrier (grpc-java or
protobuf-java), a Lombok/annotation-processing carrier as a known-limits repo.
Kotlin: okio (brings multiplatform `expect`/`actual` as a declared hazard),
kotlinx.serialization (compiler-plugin stress), ktor (large service), moshi or
okhttp (mixed Java+Kotlin — a stratification property Go never needed).
Stratification axes (~10): small lib; build system (Maven vs Gradle);
multi-module; nested/inner-class density; generics; annotation-processing/
generated code; test density; mixed Java+Kotlin; Kotlin multiplatform;
wildcard/static-import density.

**Sequencing.** J0→J1 first (J1 parallelizable); J2→J3→J4 serially; J5/J6/J9 in
parallel after J3; J7/J8/J10 after J5/J6; J11 last. Java may flip before Kotlin
if the D2 measurement forces a re-decision — per-language evidence rows make
split flips clean.

## 6. Machine-encoding the GA language axis (WP-J1)

Today the language conjunct lives in prose only
(`stability-tiers.md`: "NOT encoded in the matrix"). The smallest honest
mechanism, reusing existing parser and check shapes:

1. **New matrix rows:** `category: ga-language` in
   [`../coverage-matrix.yaml`](../coverage-matrix.yaml), flat scalars only
   (fits `internal/coverage/matrix.go` `parseMatrixYAML` with one new struct
   field), each row carrying `capability: <declared level>`. Day one: exactly
   one row, `go` / `typed-confirmed`.
2. **New check** `internal/coverage.CheckGALanguages` beside `CheckStableTier`,
   wired into `cmd/coverage -check`, enforcing three bindings:
   **(i) registry binding** — the row's declared capability equals the level
   derived from the live registries (incorporating resolver outcomes, closing
   the SQL wrinkle); **(ii) evidence binding** — every `GA-LANG-<lang>-*` row
   in `evidence-index.yaml` reads PASS with URI + sha (UNKNOWN/STALE ⇒ build
   failure); **(iii) monotone honesty** — adding a row requires (i)+(ii) green;
   removing one (demotion) is legal but loud in the check's report.
3. **Prose follows machine:** the GA conjunction line becomes
   "language ∈ ga-language rows ← pinned by cmd/coverage -check", and Preview
   becomes "every shipped parser row not in ga-language rows".

Result: a language cannot be flipped GA by prose edit. It takes registries that
report the capability, green evidence rows, and a matrix row — three artifacts,
one check.

## 7. Rollout waves after JVM

Ordering rationale: resolver-family reuse (each wave amortizes a binder
family), corpus availability under permissive licenses, usage impact, grammar
risk.

- **Wave 1 (this phase): Java, Kotlin** — nominal declared-type family;
  establishes the seam, the per-family parity-classes pattern, and the
  differential-ground-truth pattern.
- **Wave 2: TypeScript, TSX/JSX, JavaScript** — largest usage payoff. TS
  targets `typed-confirmed` under a **declared-annotation-only** regime (the
  JVM precedent transfers: explicit annotations, class members, import graph;
  no inference — no attempt to re-implement `tsc`). Module resolution
  (tsconfig paths, package.json, index files) is the `pkggraph` analog; the
  current "non-relative ⇒ external" honesty stays until proven otherwise. JS
  targets `cross-file-heuristic` GA. Ground truth: `tsc`-derived facts, CI
  only.
- **Wave 3: C#, Rust, Python** — C# reuses the JVM family almost verbatim
  (nominal, declared types, `using` namespaces) → `typed-confirmed`. Rust:
  explicit `::` paths give strong free-/associated-function resolution, but
  idiomatic `let x = …` infers everywhere — run the measured-share spike
  *before* declaring the target level (the D2 pattern). Python: dynamic; GA at
  `cross-file-heuristic`; optional-annotation boosters are future capability
  work, not GA work.
- **Wave 4: C, C++, Ruby, PHP, Lua, Bash** — C is tractable for a narrow
  declared-type binder (no overloads); C++ (templates, overloads, ADL)
  recommends `cross-file-heuristic` GA honestly rather than a binder that
  would be unsound in practice; the scripting family GAs at
  `cross-file-heuristic` (relative require/source model already shipped).
- **Wave 5: SQL, JSON, YAML, TOML, CSS, Markdown, HCL** —
  GA-at-declared-capability proper. SQL first requires the ADR-W1 re-grade:
  either the resolver learns something provable or the declared level drops to
  what the code exhibits — then GA at that level. YAML/TOML/CSS/Markdown/HCL GA
  at `intra-file-only`; JSON at `parse-only`. (HCL cross-file module refs would
  be capability work, out of scope here.) **HTML stays Source-only** — nothing
  un-shipped can GA.

## 8. Risks and non-goals

**Risks.**

- *Soundness of a hand-built JVM binder* — the top risk. Mitigations: G2b
  under-approximation with named skip counters; the G2a identity golden test;
  purity/determinism tests; the CI differential soundness gate at zero
  tolerance; kill switches; dark rollout until the flip; the false-confirmed
  stop-ship rule as backstop.
- *Kotlin grammar size/complexity* — the largest blob, the richest CST;
  parse-failure and panic rates on the real corpus must be **measured and
  published**; an assumption is not evidence.
- *Annotation processing / Lombok / codegen* — source-absent symbols cost
  recall (drop+count); publish measured skip counts on a repo carrying the
  property.
- *Corpus licensing* — pin only permissive repos; record the license in the
  manifest entry; clones stay CI-transient.
- *Eval cost growth* — keep the tier discipline (PR = hermetic + hero only;
  real-repo and ground-truth jobs nightly/dispatch; JDK install confined to the
  nightly job); budget CI minutes per wave and treat overrun as a scope signal.
- *Diluting the GA promise* — countered by §6 (no prose flips) and §3 (the
  level is explicit on every surface).
- *Snapshot-byte changes from WP-J2* — invalidates recorded JVM artifacts and
  user indexes; version note + deliberate re-baselining, conformance re-run in
  the same commit.
- *Sequencing vs P1* — WP-J3+ waits for P1 Phase 10; the PARITY-001/002 entry
  criterion is the concrete coupling point.

**Non-goals.** No `javac`/`kotlinc`/toolchain at runtime, ever. No
classpath/jar/`.class` reading in the product (pure-Go classfile parsing is
feasible and network-free, but breaks the repo-snapshot determinism model —
recorded as a rejected-for-now alternative in ADR 0008, not silently omitted).
No build-system semantics (Maven/Gradle). No generics instantiation reasoning
beyond raw-name binding. No annotation-processor emulation. No Kotlin
multiplatform `expect`/`actual` resolution (drop+count). No coverage-percentage
promises on public surfaces beyond named artifacts. No new operations; no
surface-axis movement.

## 9. Open decision points (ADR-worthy)

| ID | Decision | Recommendation | Where |
|---|---|---|---|
| D1 | Confirmed-call semantics for JVM = static binding | accept (go/types precedent + bytecode correspondence) | ADR 0008 |
| D2 | Kotlin `typed-confirmed` eligibility threshold | measure first, owner rules on the share | ADR 0008 |
| D3 | Extractor deepening scope (field/property nodes) | yes — field references are a large share of the confirmed surface and Go's bar includes var/const | ADR 0008 |
| D4 | Registry + kill-switch shape | global switch kept, per-language switches added | ADR 0007 |
| D5 | GA-language mechanism: matrix category vs separate file | matrix `category: ga-language` | ADR 0007 / §6 |
| D6 | Overload binding rule | (name, arity) uniqueness, ambiguity drops | ADR 0008 |
| D8 | PARITY-001/002 as entry criterion for JVM real-repo parity | yes — else every verdict starts PARTIAL for non-JVM reasons | ADR 0008 |
| W1 | SQL capability re-grade + outcome-based derivation | decide in wave 5 planning; blocks SQL GA either way | future ADR |

## 10. Claims discipline

This document asserts **no measurements**. Every number it names (grammar blob
sizes, corpus counts) cites the checked-in artifact that produced it; every
gate this program defines starts UNKNOWN in
[`../rc/evidence-index.yaml`](../rc/evidence-index.yaml), and UNKNOWN counts as
not passed. Corpus candidates are candidates until pinned and measured. Nothing
here asserts accuracy, performance or coverage for any language beyond what a
checked-in artifact demonstrates.
