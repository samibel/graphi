# ADR 0008 — JVM Declared-Type Semantic Resolution (`jvmresolve`, confirmed tier for Java/Kotlin)

- Status: **Proposed** (decision points D1–D8 below are open and the owner
  rules. The binder is now REGISTERED behind the ADR 0007 seam but
  DEFAULT-OFF: `engine/semantic` adds the java/kotlin registrants only under
  the experimental `GRAPHI_JVM_TYPERESOLVE` opt-in, so the shipped default —
  graph bytes, trust report, capability matrix — is unchanged until the
  rulings and the GA-LANG evidence land; flipping the default is WP-J11
  scope. The live path is proven under the flag: a real IngestAll produces
  the confirmed cross-language edge, full-vs-incremental byte parity holds,
  and the per-language kill switches reach the registrants
  (engine/ingest/jvmresolve_e2e_test.go). WP-J5 proves hermetic
  full-vs-incremental snapshot-byte parity for a wave-1 change-class subset on
  BOTH MemStore and SQLite with the binder live — including the two signature
  behaviours, the D6 overload drop (jvm_change_overload) and the D2
  declared/inferred flip (kotlin_infer_declared_flip) — bound to
  docs/rc/parity-classes-jvm.yaml by a drift guard. Groundwork: the D3 node half; `engine/jvmresolve`
  slice 1 — the declaration→node identity map with its golden cross-test
  (gate G2a), which PINNED three collector facts (Java enum members, Kotlin
  enum-class members, Kotlin companions + their members mint NO nodes);
  slice 2 — Phase A: the declaration table (`table.go`, both CST walkers) and
  the JLS-approximation type-name resolution with the strict ambiguity rule
  (`resolve.go`); slice 3 — supertype chains + member lookup
  (`hierarchy.go`): the D6 rule implemented as identical-signature override
  chains binding most-derived, differing same-arity signatures ambiguous, and
  ANY open chain (external/ambiguous supertype) forfeiting every binding —
  even a receiver-declared member, since an external overload could be the
  more applicable javac target; slices 4/5 — Phase B for BOTH languages
  (`body_java.go`, `body_kotlin.go`): declared-type receiver propagation with
  block-scoped environments and the named-gap counters, including Kotlin's
  lambda-rebinding forfeit (`this`/bare calls inside any lambda) and the
  trailing-lambda arity forfeit; and slice 6 — Phase C emission (`emit.go`):
  confirmed@1.0 calls/references edges with the D1 reason string, nominal
  implements from interface clauses only (class-extends stays heuristic
  `inherits`, which ingest's sweep excludes from the confirmed set),
  constructor calls targeting the TYPE node (the heuristic FQN binder's shape,
  so confirmed upserts replace rather than duplicate), and the committed-set
  gate as the structural never-fabricate — proven end-to-end in tests whose
  committed sets come from the REAL core/parse extractors)
- Date: 2026-08-14
- Program: [`docs/plan/2026-08-graphi-p2-language-ga-program-v1.md`](../plan/2026-08-graphi-p2-language-ga-program-v1.md)
  §5.1 (design), §5.2 WP-J2…WP-J4, WP-J9 (ground truth)
- Depends on: ADR 0007 (registry seam) accepted and landed
- Feeds: WP-J11 (the Java/Kotlin GA flip); the wave-2/3 binders (TS, C#) reuse
  this ADR's declared-type regime as precedent

## Problem

Java and Kotlin are capped at `cross-file-heuristic`: the shared FQN binder
(`engine/link/resolve_java.go`) resolves imported qualified/static calls, but —
as its own comment states — an instance call through a variable receiver is not
resolvable, because the receiver type is unknown at CST level. Go's GA bar
includes `confirmed`-tier edges proven by a type checker
(`engine/typeresolve`). There is no stdlib type checker for the JVM, and the
product may never depend on `javac`/`kotlinc`, the network, or CGo.

Additionally, the JVM extractors are shallower than any binder needs: the Java
extractor deliberately collapses kinds onto `{file, method, type}` — no
field/property nodes and no declared-type metadata exist in the extracted
graph.

## Decision (proposed)

A new engine package (working name `engine/jvmresolve`) registered behind the
ADR 0007 seam, raising JVM edges to `confirmed` with **declared types only —
no inference, ever**. It recomputes over the entire walked snapshot (parity by
construction) and re-parses `.java`/`.kt` with the same gotreesitter grammars,
mirroring how `engine/typeresolve` re-parses with `go/parser`.

- **Phase A — class/package/member table:** packages, imports (single-type,
  on-demand, static, Kotlin aliases), nested/inner types, members with declared
  types and modifiers, Kotlin objects/companions/top-level functions.
  Type-name resolution by JLS-scoping approximation with a strict ambiguity
  rule; anything ambiguous or external (incl. `java.lang`) never yields a
  confirmed edge.
- **Phase B — declared-type propagation, no inference:** explicitly typed
  fields/properties/params/locals, `this`/`super`, constructor results,
  qualified statics, declared-return-type chains. Java `var`, inferred Kotlin
  `val`/`var`, lambda/scope-function receivers, extension receivers, and
  members inherited from external supertypes are skip+counted with **named
  counters** — fail closed, never guess.
- **Phase C — member lookup + emission:** `calls` bound at
  (name, arity)-uniqueness on the declared receiver type's intra-repo supertype
  chain; `references` from type positions; `implements`/`inherits` nominal from
  the declared clause.

**The contract (D1):** a confirmed JVM `calls` edge asserts **static binding**
— the same contract Go GA'd (go/types binds an interface-value call to the
interface's method object, not the dynamic implementation), and exactly what
`javac` encodes in bytecode method refs. Runtime dispatch may select an
override, reachable via implements/overrides edges; the edge's reason string
says so.

**Ground truth (CI only, never product):** a nightly workflow compiles pinned
corpus repos with a runner-local JDK and checks **soundness** (every graphi
confirmed edge appears in the bytecode-derived facts; zero tolerance; a
counterexample is a `JVMSOUND-0xx` defect and blocks the GA flip) and measures
**recall** (published, not gated).

## Decision points (owner rules; recommendations recorded)

| ID | Question | Recommendation |
|---|---|---|
| D1 | Confirmed semantics = static binding? | accept — go/types precedent + bytecode correspondence |
| D2 | Is Kotlin `typed-confirmed`-eligible given inference? | measure the confirmed-edge share on the pinned corpus first; owner sets the threshold; honest fallback: Kotlin GA at `cross-file-heuristic`, typed deferred |
| D3 | Extractor deepening: field/property nodes + declared-type metadata? | **Node half LANDED 2026-08-14** (WP-J2 slice): Java field/constant declarators and Kotlin properties become variable/constant nodes, kind from the DECLARED form only (`static final` / `constant_declaration` / `const val` → constant), pinned by `TestExtractJava_FieldNodes` / `TestExtractKotlin_PropertyNodes`; the frozen golden fixtures are field-free, so their bytes are unchanged, and the full suite stayed green. Declared-TYPE metadata on nodes is DEFERRED — the binder re-parses sources itself (see above), so node-level types are not load-bearing for WP-J3; revisit only if the trust surface wants them. Known honest cost: a field sharing a bare name with a same-package symbol now marks that name dir-ambiguous in the heuristic linker (drop+count, never a wrong edge) |
| D4 | Kill-switch shape | inherit ADR 0007 (`GRAPHI_NO_TYPERESOLVE` + per-language) |
| D6 | Overload binding rule | (name, arity) uniqueness; any ambiguity drops+counts; the `change_overload` change class pins the drop |
| D8 | Entry criterion for JVM real-repo parity | PARITY-001/002 fixes land first — they are ingest-level and language-independent, and would make every JVM verdict start PARTIAL for non-JVM reasons. WP-J5's hermetic parity gate honors this: jvm_delete_file is DEFERRED (docs/rc/parity-classes-jvm.yaml), not pinned as a JVM defect it is not |

## Rejected alternatives (recorded, not silently omitted)

- **Shelling out to `javac`/`kotlinc`** — violates the no-toolchain,
  zero-egress, determinism constraints of `engine/typeresolve/doc.go`; also
  unavailable on user machines by assumption.
- **Reading the classpath / jars / `.class` files in the product** — pure-Go
  classfile parsing is feasible and network-free, but it breaks the
  repo-snapshot determinism model (the graph would depend on build state
  outside the walked source). Rejected for now; revisit only with its own ADR.
- **Type inference (even "just" local `val`/`var`)** — every inference step is
  a soundness liability a hand-built binder cannot discharge; the regime is
  declared-types-only, and inference gaps are named skip counters instead.

## Consequences

- Java gains confirmed static-binding `calls`/`references`/`implements` edges
  for declared-type receivers; Kotlin gains a narrower measured subset.
- Everything unprovable stays exactly as honest as today: heuristic tier or
  skip+count.
- The differential CI job hardens the binder during development and is the
  standing soundness gate after the flip.
