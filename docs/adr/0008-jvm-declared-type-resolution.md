# ADR 0008 — JVM Declared-Type Semantic Resolution (`jvmresolve`, confirmed tier for Java/Kotlin)

- Status: **Proposed** (decision points D1–D9 below are open and the owner
  rules; **D9 is RULED and implemented as of 2026-08-19 (W0.h/SW-170) but, like
  the rest of this ADR, is not ratified until WP-J11**. The binder is now REGISTERED behind the ADR 0007 seam but
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

**Ground truth (CI only, never product):** LANDED (WP-J9,
`internal/jvmgroundtruth` + `.github/workflows/jvm-groundtruth.yml`). A
nightly/dispatch workflow compiles the sources with a runner-local
JDK/kotlinc, `javap -c -p` disassembles them, and the parser projects the
constant-pool method refs into the same call-fact space as graphi's confirmed
edges. **Soundness** is zero-tolerance (every confirmed edge must be a
bytecode fact; a counterexample is a `JVMSOUND-0xx` stop-ship) and **recall**
is measured, not gated. The comparator + parser are unit-tested against a
checked-in REAL javap fixture, and the live e2e test compiles a fixture and
proves graphi ⊆ bytecode end to end — green in development (2/2 confirmed
calls backed by bytecode, the ambiguous overload conservatively dropped for a
recall gap, the honest sound outcome). The gate earned its keep immediately:
it caught a harness parser bug (javap omits the owner prefix on same-class
calls) that had produced a false soundness violation — exactly the failure a
weaker self-check would have missed.

WP-J6 (G6) adds the hero-jvm suite: a hermetic Java+Kotlin fixture
(`corpus/fixtures/hero-jvm`) and 16 scenarios (`corpus/hero-jvm`) exercising
all 12 stable ops with the binder live — cross-file and cross-language
confirmed callers/callees/references, interface implementations, and the
ambiguous/partial/empty/not_found failure classes plus a negative anchor — all
passing (`cmd/eval` hero_jvm_test.go, `GRAPHI_JVM_TYPERESOLVE` set so the
fixture indexes at the typed-confirmed capability).

## Decision points (owner rules; recommendations recorded)

| ID | Question | Recommendation |
|---|---|---|
| D1 | Confirmed semantics = static binding? | accept — go/types precedent + bytecode correspondence |
| D2 | Is Kotlin `typed-confirmed`-eligible given inference? | measure the confirmed-edge share on the pinned corpus first; owner sets the threshold; honest fallback: Kotlin GA at `cross-file-heuristic`, typed deferred |
| D3 | Extractor deepening: field/property nodes + declared-type metadata? | **Node half LANDED 2026-08-14** (WP-J2 slice): Java field/constant declarators and Kotlin properties become variable/constant nodes, kind from the DECLARED form only (`static final` / `constant_declaration` / `const val` → constant), pinned by `TestExtractJava_FieldNodes` / `TestExtractKotlin_PropertyNodes`; the frozen golden fixtures are field-free, so their bytes are unchanged, and the full suite stayed green. Declared-TYPE metadata on nodes is DEFERRED — the binder re-parses sources itself (see above), so node-level types are not load-bearing for WP-J3; revisit only if the trust surface wants them. Known honest cost: a field sharing a bare name with a same-package symbol now marks that name dir-ambiguous in the heuristic linker (drop+count, never a wrong edge) |
| D4 | Kill-switch shape | inherit ADR 0007 (`GRAPHI_NO_TYPERESOLVE` + per-language) |
| D6 | Overload binding rule | **CORRECTED 2026-08-16 — the original "(name, arity) uniqueness" was UNSOUND as implemented and emitted WRONG edges (JVMSOUND-001/002, both reproduced).** The rule is now: a callable binds at (name, arity) uniqueness **over a candidate set that contains no VARIADIC or DEFAULTED member** (either can satisfy a call at other arities, so its presence forfeits the whole name — `variadic-forfeit`), **with signature identity keyed on RESOLVED type (intra-repo FQN), not written text** (so `q.Foo` and `r.Foo` do not collapse; primitives/externals key on marked text so genuine `m(int)`/`m(int)` overrides still bind). Any ambiguity drops+counts. Pinned by `TestAnalyzeJavaBodies_VariadicForfeit`/`_ResolvedSignature`, the Kotlin `_ElasticForfeit`, and the `change_overload` change class; each proven red-without-fix. NOTE on the oracle: WP-J9's differential check keys calls at method-name granularity, so it is STRUCTURALLY BLIND to same-name overload mis-binding — the unit tests are the proof of record for this class, and giving the oracle signature-aware keys is a follow-up on top of R3's corpus-scale run |
| D8 | Entry criterion for JVM real-repo parity | **RE-SCOPED 2026-08-16 (independent review R8).** Was: "both PARITY-001/002 fixes land first." Now a PER-DEFECT applicability test: a Go-level ingest defect blocks the JVM real-repo run only where the JVM code path can exhibit it. PARITY-001 (deleted-path purge ordering) was language-independent and did block — it is now FIXED. PARITY-002 (Go/Python clause-keyed `imports` fan-out) CANNOT arise for Java/Kotlin, whose imports emit a single file→package edge (`engine/link/resolve_common.go`, `packageNodeByPath` lookup, no fan-out), so it does not block WP-J7. jvm_delete_file is no longer deferred — it is a required, proven row |
| D9 | What is the unit of the stale-confirmed sweep in a directory holding more than one language? | **RULED 2026-08-19 (W0.h, SW-170). The sweep key is `(directory, language)`, and the mixed-directory EXEMPTION is removed rather than narrowed.** See the ruling in full below |

### D9 in full — the sweep key is `(directory, language)`

**Ruled 2026-08-19 by W0.h (SW-170).** Raised as an open decision point by the
independent architect + product-owner review, §5 item 4
([`../decisions/2026-08-language-ga-independent-review.md`](../decisions/2026-08-language-ga-independent-review.md)),
which observed that the behaviour existed only as a code comment and that it is
a **soundness decision**, not an implementation detail.

**What the code did before.** `engine/ingest`'s reconciliation deletes a stored
confirmed edge when this pass no longer emits it *and* the edge's from-node sits
in a unit the pass CHECKED. That question was keyed on the **directory alone**,
which is unanswerable when a directory holds two languages: the java registrant,
told only "this directory checked clean", would delete the kotlin registrant's
confirmed edges out of the same directory. `engine/jvmresolve` avoided that by
emitting a mixed directory's unit rows **DEGRADED** under a named reason, which
exempted the whole directory from the sweep.

**Why the exemption is the wrong shape — the ruling's reasoning.**

1. **An exemption is unobservable; a partition is countable.** Nothing counted
   the skipped directories, nothing named them in a diagnostic, and the unit
   rows claimed a *degradation* that had not happened — a parse failure and "we
   declined to decide" reported through the same slot. Under a partition every
   directory is a checked unit of some language and the sweep simply runs.
2. **It was not a small hole; it was the normal shape.** `.java` beside `.kt` is
   what Kotlin-in-Java adoption produces (the okio corpus pin carries 284 `.kt`
   beside 29 `.java`). The exemption's cost is paid exactly where the binder is
   supposed to earn its keep.
3. **The consequence is a WRONG SURVIVING EDGE, not merely a missing one.** A
   superseded confirmed edge in a mixed directory survives every incremental
   `sync`, forever, while the full pass never has it. Measured, not argued: on a
   hermetic fixture the incremental graph kept
   `mix.run --calls[confirmed]--> tax.rate` after the receiver's declared type
   had moved to `alt.Alt`, and after the file supplying that type was deleted.
   Both are recorded as parity rows (`jvm_mixed_dir_change_receiver_type`,
   `jvm_mixed_dir_delete_callee`), red before this ruling on both stores and both
   profile axes.
4. **The hazard the exemption guarded against is gone by construction, not by
   care.** A registrant can no longer reach the sibling language's edges at all,
   because the sibling's files are not in its `Owns` set. So a kill-switched or
   degraded sibling cannot have its edges swept by an enabled neighbour — the
   property the exemption was buying, now obtained without the cost.

**The mechanism.** `typeresolve.Resolver` gains `Owns(relPath) bool`, the
language half of the key: which source paths' nodes belong to this registrant.
Its contract is `Owns ⊇ Subject` per registrant and **pairwise disjoint** across
registrants (pinned by `engine/semantic`'s
`TestRegistry_OwnsIsDisjointAndCoversSubject`). The ingest sweep restricts its
candidate set to edges whose from-node is owned; the directory check is then
applied to that set. `jvmresolve` no longer tracks sibling directories at all,
and `reasonMixedDir` is deleted.

**Scope, and what this deliberately does NOT decide.**

- The `DEGRADED` slot keeps its original meaning — a parse skip still exempts
  its unit, and degradation still never deletes knowledge. Being *mixed* is
  simply no longer one of its reasons.
- **This ruling changes what the go/types registrant's sweep can reach, and that
  is stated rather than buried.** `goResolver.Owns` is `*.go` (test files
  included), so the Go sweep no longer reaches a confirmed
  calls/references/implements edge whose from-node is sourced outside a `.go`
  file. Under the shipped default that set is provably empty — those three kinds
  reach the confirmed tier only from a registered semantic resolver
  (`engine/link` never returns `TierConfirmed`), and with
  `GRAPHI_JVM_TYPERESOLVE` unset go/types is the only registrant. Measured:
  this repository's canonical graph snapshot is byte-identical across the change
  (`9c5eb1f9c1db1ca469b2a8fb3bbcaa1105a6d5c9fceabb35898300c7c2fd6539`,
  21 895 857 bytes), and the whole hermetic Go change-class table
  is unchanged. The **Go sweep key itself is not re-decided here** — that is a
  separate decision with its own measurement, and this ruling only removes a
  cross-language over-reach that could not fire under the shipped default.

  *The instrument, so the digest is reproducible rather than merely asserted*
  (corrected 2026-08-19, SW-170 review round 1 finding 2 — the first draft of
  this bullet published `c9f8de76…` / 21 891 266 B, which no clean checkout
  reproduces): build `./cmd/graphi` with
  `CGO_ENABLED=0 go build -trimpath -buildvcs=false`, run `index --full` from a
  **pristine checkout of the commit that carries this ruling** with an isolated
  `XDG_STATE_HOME`, then serialize the resulting store through
  `core/graphstore`'s `Snapshot` and digest those bytes. The value above is
  identical for the before-binary, the after-binary and a repeat run. The
  superseded figure was taken over an intermediate working tree rather than the
  committed one: the same instrument returns 21 885 083 B over the parent commit
  `98ba4a6` and 21 895 857 B over this one, and 21 891 266 B falls between them.
  **A digest published in an ADR names the tree it was taken over.**
- **THE RESIDUAL RISK THIS RULING CREATES, named rather than left implicit —
  `Owns` is a PARTITION, not a COVER (raised by the SW-170 review, finding 3).**
  The registrants own exactly `*.go`, `*.java` and `*.kt`, so *every other path
  is owned by nobody*, and a stored confirmed `calls`/`references`/`implements`
  edge whose from-node sits in such a file could never be swept by any
  registrant — it would be **immortal**, surviving every incremental `sync`
  while the full pass never has it. That is the same wrong-surviving-edge class
  D9 exists to close, and before D9 the Go pass reached those edges incidentally
  because the key was the directory alone. **It is unreachable today**, and the
  argument has exactly one load-bearing step — *no parser mints a confirmed-tier
  edge of a sweepable kind*: `engine/link/link.go` never returns
  `TierConfirmed`; every tree-sitter walker hardcodes `TierDerived`
  (`core/parse/parser_tswalk.go`, `core/parse/parser_ts.go`), as does
  `extract_go.go` for calls/references; the only confirmed producers are
  `defines`, `notebook_cell` and the semantic resolvers themselves, and neither
  `defines` nor `notebook_cell` is in `engine/ingest`'s `typeresolveKind` set.
  That step was **unenforced** — `core/parse/mapping.go` honours a
  parser-supplied `spec.Tier` verbatim — so it is now pinned by
  `core/parse/confirmed_tier_guard_test.go::TestNoConfirmedTierOutsideDefines`,
  a test-only allow-list guard that fails closed on any new confirmed-tier site
  in the parser layer. The guard holds the line; it does not close the gap. The
  durable fixes are a **residual-owner rule** (an edge whose from-node is owned
  by nobody is swept by whichever registrant checked its directory, which
  restores the pre-D9 reach without breaking disjointness) or a product-wide
  assertion that no confirmed `typeresolveKind` edge has an unowned from-node.
  Choosing between them is **deferred to SW-178's D9 ratification**, on the
  strength of a measured delta of zero.
- D9 is a JVM ruling and is **Proposed** like the rest of this ADR until the
  WP-J11 ratification (SW-178) rules on D1–D9 together.

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
