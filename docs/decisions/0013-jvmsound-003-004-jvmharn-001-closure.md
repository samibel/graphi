# ADR 0013 — Closure of JVMSOUND-003, JVMSOUND-004, JVMHARN-001 (SW-188)

- Status: **Accepted** — D1…D9 ratified together with the SW-188 commit
- Date: 2026-08-20
- Program: [`../plan/2026-08-graphi-p2-language-ga-program-v1.md`](../plan/2026-08-graphi-p2-language-ga-program-v1.md) §5.1 (design), §5.2 WP-J2…WP-J4
- Depends on: ADR 0008 (declared-type resolution) Accepted; ADR 0012 (capability
  levels) Accepted
- Closes: `JVMSOUND-003` (arity miscount from CST comments),
  `JVMSOUND-004` (array-erased signature key), `JVMHARN-001` (Kotlin
  value-class name mangling) — three reproduced wrong-confirmed-edge defects
  found by SW-172 / SW-173 and carried in `projects/graphi/backlog.md` as the
  load-bearing findings that kept the WP-J11 flip gated

> ## SW-188 (this commit) — D1…D9 ruled together, **Accepted**
>
> Per ADR 0008 D6's discipline, a closure that fixes more than one defect is
> not three rulings but **one** — the rulings are not independent: a binder
> whose arity is right but whose signature is array-erased, or whose signature
> is right but whose mangled name is invisible, still emits the wrong edge
> for the same code on either side of the JVMSOUND-004/HARN-001 line. This
> ADR records the rulings as a single decision, with the defect that
> motivates each one stated, the patch that satisfies it named, and the
> positive regression test that pins it. No individual ruling is renamed or
> removed; this block is the whole ratification.
>
> | ID | Status | Recorded state |
> |---|---|---|
> | **D1** | **Accepted** | A comment node inside an `argument_list` is **not an argument**. JVMSOUND-003 was the binder counting the `line_comment` and `block_comment` children of `argument_list` (the `/* the scale */` and `// …` siblings of the real call) as if they were expressions; the arity the binder then handed to `(name, arity)` was one too high, and `LookupCallable` returned a wrong-most-derived match in the closure's neighbourhood. The fix is in `engine/jvmresolve/body_java.go:666-693` `countArgs` — a switch that no-ops over `(`, `)`, `,`, `line_comment`, `block_comment`, `multiline_comment` and counts the rest. Pinned by `engine/jvmresolve/body_java_test.go::TestCountArgs_SkipsComments` (positive regression: a `r.apply(1 /* the scale */)` and `r.apply(/*a*/1, /*b*/2)` bind arity 1 and 2 respectively) and `TestCountArgs_CrossFileBindsBase` (the cross-file closure, scalar binds scalar, array binds array). The old pin in `internal/jvmgroundtruth/signature_test.go` (`TestArity_CatchesOverloadMisbind_JVMSOUND003` and `TestJVMSOUND003_CrossFileWrongEdge`) is replaced by a `t.Skip` stub pointing reviewers at the positive regression, per the convention that a pin which is "RED with instructions" must never sit on main past the commit that closes the defect it pinned. |
> | **D2** | **Accepted** | Arity is the count of **argument expressions** in the call, not the count of CST children of `argument_list`. The two differ exactly when a comment, an annotation, or a misplaced token occupies a slot. With D1 implemented, the binder's notion of arity is the arity javac would compute over the same source. KNOWN RESIDUAL (named, not left implicit): a `//` between arguments is a comment in this implementation; in the Kotlin grammar an `EOL` token of certain shapes can occupy a slot the way a comment does in Java — `engine/jvmresolve/body_kotlin.go::countArgs` is the parallel site and is checked but not the rule this ADR decides; a Kotlin-specific EOL rule is a follow-up, not part of this closure. |
> | **D3** | **Accepted** | A `countArgs` change is **never a silent one**. The three pin tests that held JVMSOUND-003 open are replaced with `t.Skip` stubs that name the positive regression test, so a reviewer who runs the groundtruth suite and sees a skipped test knows both that the closure is intentional and where to look. Same rule will apply to JVMSOUND-004 (D6) and JVMHARN-001 (D9) below. The discipline is a follow-up on top of the pin/positive split ADR 0012 already established for capability levels; it is a binder-wide convention. |
> | **D4** | **Accepted** | The callable signature key carries the **array dimensionality** of every parameter, not just its resolved FQN. JVMSOUND-004 was the binder keying `m(T)` and `m(T[])` onto the same signature — `callableSig` used `p.Type.Base` only, and `Base` is the array-erased form by construction in `table.go`'s `fieldType`/`paramType` walkers. The fix is in `engine/jvmresolve/hierarchy.go:140-149, 157-177` (`callableSig` consults `arrayDims(p.Type.Raw)` and concatenates the dimensionality string into the key) plus the new helper `arrayDims` at lines 193-217. Same rule for a primitive parameter: a method declared `m(int[])` and one declared `m(int)` no longer share a signature, and the WIDENED blast radius (one-dim vs two-dim at the same arity) closes as a side effect — both pairs produce DISTINCT keys. Pinned by `engine/jvmresolve/hierarchy_test.go::TestCallableSig_ArrayDim` (two positive shapes: scalar-vs-one-dim across a BASE/DERIVED chain, and one-dim-vs-two-dim on the same class) and `TestCallableSig_CrossFileBindsBase` (the cross-file closure: `a.Base.apply(Thing)` from the Base receiver binds cleanly, the same pair read from the Derived receiver is `AmbiguousMember`, never the most-derived wrong binding). The two old pin tests (`TestSignature_CatchesEqualArityMisbind_JVMSOUND004`, `TestJVMSOUND004_CrossFileWrongEdge`) and the array-dim-followup pin (`TestJVMSOUND004_ArrayDimensionality`) are replaced with `t.Skip` stubs pointing at the positive regressions. |
> | **D5** | **Accepted** | `arrayDims` counts **trailing `[]` GROUPS** in the written type text, not the present/absent bit. The previous mental model was "is the parameter an array?"; the right one is "how many dimensions, in the order the source writes them?" — `int xs[]` and `int[] xs` both count one group, `int[][] xs` and `int[] xs[]` both count two. The loop in `arrayDims` requires a `]` then walks back to its matching `[`, strips the pair, and continues, which is **undercount-only** (a `m(T)` rendered with the bracket on the parameter rather than the type would undercount, never overcount, so an overload pair can only ever see DISTINCT keys, never a false collapse). The C-style declarator's brackets sit on the parameter and not the type, but a binder that reads the source through tree-sitter sees the bracket on the type text for the language's normal form (`int[] x`); the helper's job is to make the two shapes agree. |
> | **D6** | **Accepted** | The signature key uses the **full TypeRef** (`Raw` + `Base`), not `Base` alone. `Base` is the array-erased form because the type table's job is to resolve the **type identity**, not the **rendered signature**; a `fieldType` walker that preserved brackets would have to reconstruct the C-style declarator's brackets onto the parameter name, which mixes the two concerns. The split — table keeps `Base` array-erased, signature key reconstructs the dimensionality from `Raw` — is the clean separation, and `callableSig` is the place that does the joining. KNOWN RESIDUAL (stated so it is not overread): two DIFFERENT external types that share a simple name (e.g. `a.Foo` and `b.Foo`, neither declared in the repo) both key on `?Foo` and would collapse. This is the same simple-name collision class the heuristic linker already carries; it can only affect the override/overload distinction of methods whose parameters are both external and same-named, which is much narrower than the intra-repo case this fixes. The fix is the WIDENED blast radius SW-172 round 1 asked for, the rule is one-count-of-trailing-`[]`-groups, and the order of the groups is the order the source writes them. |
> | **D7** | **Accepted** | Kotlin's value-class name mangling is recognized as a binding shape. JVMHARN-001 was the binder answering `NotFoundMember` (or `AmbiguousMember`) for a call site that source-declared `foo` but javac compiled to `foo-<hash>` — the compiler emits a BRIDGE function under the plain name (taking the boxed inline-class parameter) AND a REAL function under the mangled name (taking the unwrapped underlying type), and the source resolves to the mangled form. 22 of kotlinx.serialization's 27 by-name counterexamples (SW-173 capture digest `e6d39791…`) were the oracle accusing correct code. The recognition rule is in `engine/jvmresolve/hierarchy.go:219-271` `valueClassBridgeName`; the lookup is `LookupCallableValueClassAware` at lines 289-316. Pinned by `engine/jvmresolve/hierarchy_test.go::TestValueClassBridgeName` (8 cases — four real mangled names, an ordinary name, a too-short suffix, a non-base-64 character, a digit-leading prefix, an empty prefix, and a hash-only shape) and `TestLookupCallableValueClassAware` (the lookup itself: a mangled-name call binds the same `Holder.foo(V)` the plain lookup binds). |
> | **D8** | **Accepted** | The bridge-name rule is **split on the FIRST `-`**, not the last. kotlinc's hash alphabet includes `-` itself (and `+`, `_`, plus the base-64 letters and digits), so the bridge name is everything BEFORE the first dash and the hash is everything AFTER. Ordinary user-declared names with dashes (legal in backticked Kotlin) do not match because the part after the first `-` must be at least 4 base-64 characters AND the prefix must be a valid Kotlin identifier. KNOWN RESIDUAL: the hash is computed over the parameter types' mangled form, so any two functions that take inline-class parameters of the same boxed types at the same arity collide on the SAME mangled suffix; the binder must look both up. The lookup iterates the closed chain in BFS order, so the first-found rule resolves any such collision the same way the bytecode would; frequency is bounded by inline-class parameter shapes and is named rather than measured here. |
> | **D9** | **Accepted** | A value-class-aware lookup is a **fallback** under the ORIGINAL name. `LookupCallableValueClassAware` first tries the plain `LookupCallable(receiver, name, arity)`; if and only if the plain lookup does not bind, the bridge name is tried; if the bridge-name lookup binds, the result is reported under the ORIGINAL name (the call site's text) so graphi's emitted edge matches what the source declares. The mangled form itself is NEVER written to `TypedSite`. The four call sites in `engine/jvmresolve/body_kotlin.go` (lines 337, 494, 518, 593) are updated in lockstep — the call sites are where the binder hands its result to the typed-site emitter, and a partial update would be a re-instatement of the defect for one of the four sites. The old pin test (`TestKotlinValueClassMangledName_JVMHARN001`) is replaced with a `t.Skip` stub pointing at the two positive regressions. |
>
> **Where this ratification stops.** It records the rulings as they stand on
> 2026-08-20, and it does **not** perform the WP-J11 flip (D4 inherits ADR
> 0007's `GRAPHI_JVM_TYPEROLVE` opt-in shape; lifting it to default-on is
> W1.f / SW-179, owner-gated on the WP-J11 flip gate being green
> end-to-end). The two-dispatch parity measurement over the fixed product is
> the evidence the flip gate reads; it is recorded in the candidate-move
> record [`2026-08-parity-candidate-move-adr0013.md`](2026-08-parity-candidate-move-adr0013.md),
> not in this ADR, on the same split the project has used for ADR 0009, 0010
> and 0011.

## Problem

Three reproduced wrong-confirmed-edge defects sat in `engine/jvmresolve` as
the load-bearing findings that kept `GRAPHI_JVM_TYPERESOLVE` default-off:

| Defect | What it was | Wrong edge |
|---|---|---|
| **JVMSOUND-003** | `countArgs` counted comment nodes inside `argument_list` as arguments | `r.apply(1 /* the scale */)` bound `apply(2)`; the most-derived two-arg overload was emitted where javac emits the one-arg. Pinned red-without-fix at `internal/jvmgroundtruth/signature_test.go::TestArity_CatchesOverloadMisbind_JVMSOUND003`. |
| **JVMSOUND-004** | `callableSig` keyed on `p.Type.Base` (array-erased by construction in `table.go`), so `m(T)` and `m(T[])` produced the SAME signature | An overload set was misread as an override chain, and the most-derived member won where `AmbiguousMember` was required. WIDENED blast radius (one-dim vs two-dim at the same arity) discovered in SW-172 round 1. Pinned at `TestSignature_CatchesEqualArityMisbind_JVMSOUND004` and `TestJVMSOUND004_ArrayDimensionality`. |
| **JVMHARN-001** | The binder did not recognize Kotlin's value-class name mangling; `foo-<hash>` calls were unbindable from a `Holder.foo(V)` declaration | 22 of 27 by-name counterexamples on kotlinx.serialization; the oracle accused correct code. Pinned at `internal/jvmgroundtruth/signature_test.go::TestKotlinValueClassMangledName_JVMHARN001`. |

All three are soundness defects — they emit edges that javac does not. None
is a recall gap.

## Decision (this commit)

- `engine/jvmresolve/body_java.go:666-693` — `countArgs` skips comment nodes.
- `engine/jvmresolve/hierarchy.go:140-217, 219-316` — `callableSig` carries
  array dimensionality; `arrayDims`, `valueClassBridgeName`, and
  `LookupCallableValueClassAware` are the new primitives.
- `engine/jvmresolve/body_kotlin.go:337, 494, 518, 593` — the four call sites
  that hand the binder's result to the typed-site emitter use
  `LookupCallableValueClassAware`.
- `internal/jvmgroundtruth/signature_test.go` — the three pin tests are
  replaced with `t.Skip` stubs pointing at the positive regressions
  (`TestCountArgs_SkipsComments` and `TestCountArgs_CrossFileBindsBase` in
  `engine/jvmresolve/body_java_test.go`,
  `TestCallableSig_ArrayDim` and `TestCallableSig_CrossFileBindsBase` in
  `engine/jvmresolve/hierarchy_test.go`, and
  `TestLookupCallableValueClassAware` in the same file).
- `engine/jvmresolve/hierarchy_test.go` — `TestValueClassBridgeName` covers
  the recognition rule.
- `engine/jvmresolve/body_java_test.go` — `TestCountArgs_SkipsComments` covers
  the arity rule, `TestCountArgs_CrossFileBindsBase` covers the cross-file
  closure.

## Decision points (D1…D9)

| ID | Question | Recommendation |
|---|---|---|
| D1 | Are comment nodes inside an `argument_list` arguments? | accept — they are not. `countArgs` no-ops over `line_comment`, `block_comment`, `multiline_comment`, `(`, `)`, `,`. |
| D2 | What is "arity" in the binder's vocabulary? | accept — the count of argument expressions, not the count of CST children of `argument_list`. |
| D3 | What happens to the JVMSOUND-003 pin tests when the closure lands? | accept — replace with `t.Skip` stubs pointing at the positive regressions. |
| D4 | Is array dimensionality part of the signature key? | accept — every parameter's trailing `[]` groups, counted and concatenated, are part of the key. |
| D5 | How is array dimensionality counted? | accept — `arrayDims` walks `Raw` backward, requires a `]`-then-`[` pair, and is undercount-only. |
| D6 | What shape of TypeRef does the key use? | accept — the full `Raw + Base` (the table keeps `Base` array-erased; the signature key reconstructs dimensionality from `Raw`). |
| D7 | Is Kotlin's value-class name mangling a binding shape? | accept — `valueClassBridgeName` and `LookupCallableValueClassAware` are the primitives. |
| D8 | Where does the bridge-name rule split? | accept — at the FIRST `-`, not the last; suffix is `≥4` base-64 characters, prefix is a valid Kotlin identifier. |
| D9 | Where is the value-class-aware lookup called? | accept — at the four call sites in `body_kotlin.go` that hand the binder's result to the typed-site emitter, in lockstep; the mangled form is never written to `TypedSite`. |

## Rejected alternatives (recorded, not silently omitted)

- **Strip comments at parse time** — would force a grammar-aware filter on
  every walker, and a comment that is a C-comment-style `/* … */` is not
  the same shape as a Kotlin `// …` end-of-line token; the per-language
  switch in `countArgs` is the smaller surface and is local to the binder.
- **Key `callableSig` on `Raw` alone** — would lose the intra-repo FQN
  identity (D2's "two params both spelled `Foo`" case), and the
  `Raw + arrayDims` shape is the only one that gets both. (D6.)
- **Use `LastIndex("-")` for the bridge split** — kotlinc's hash alphabet
  includes `-`; the first `-` is the split, the last is unsafe. (D8.)
- **Write the mangled name to `TypedSite`** — would be honest about what
  the bytecode calls but would not match what the source declares, and
  every typed-site consumer (callers, references, the binder itself on
  re-bind) would have to recognize the mangled form; the lookup-time
  rejoining at D9 is the smaller surface.

## Consequences

- The binder's notion of arity matches javac's notion of arity (D1, D2).
- The binder's notion of signature matches javac's notion of signature, for
  the overload set the override-vs-overload distinction can read (D4–D6).
- Kotlin value-class call sites bind (D7–D9).
- The three pin tests are no longer red-without-fix; the corresponding
  positive regressions are the proof of record (D3, parallel for D4 and D9).
- The WP-J11 flip's "any demonstrated false confirmed edge" condition is
  discharged for these three defects; the rest of the gate (the capability
  matrix, the index-migration story, the per-defect applicability test) is
  unchanged and is read in
  [`2026-08-language-ga-wpj11-flip-gate.md`](2026-08-language-ga-wpj11-flip-gate.md).
