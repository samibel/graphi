# The JVM call-site binding rate, with its denominator (W1.b / SW-175)

**Measured 2026-08-19** on branch `claude/kotlin-java-canonical-ga-t3b8km`, from
the tree at `c855d04` plus this story's CI-only harness. Harness:
`internal/jvmbindrate`. Run:

```bash
GRAPHI_JVM_CORPUS_PINS=<pins-dir> go test ./internal/jvmbindrate/ -run TestPins -v -count=1
```

> **This document sets no threshold.** ADR 0008's decision D2 is the owner's,
> and the deliverable here is the measurement with both of its numbers. Nothing
> below recommends a bar, and no acceptance criterion was rewritten to make a
> figure look better.

---

## 0. The one-paragraph summary, before any table

The published Java figure is **21.39 %** — 6 065 bound call sites out of 28 354
CST call sites, on guava's JRE module, over a parse with **zero** recovery
errors. The published Kotlin figures are **19.16 %** (kotlinx.serialization) and
**3.47 %** (okio), and both carry a caveat Java's do not: **the Kotlin grammar
fails to parse 13.1 % and 9.9 % of their files cleanly** (§4). That caveat now
has a **measured direction**: dirty parses *depress* the Kotlin rates, and over
cleanly-parsed files only, kotlinx binds at **21.15 %** against guava/src's
**21.39 %** — so **the headline Java/Kotlin gap is almost entirely a parse
artefact** (§8.0). What survives is the **5.9× spread between the two Kotlin
corpora** (§8.1), which is real. Every rate here is a *Phase B* rate; fewer than
half of guava's bound Java sites survive into the graph as confirmed edges, and
the product-visible Java figure is **9.80 %** (§9). The **skip histogram does not
account for the unbound remainder** and cannot — §6 measures why. The largest
single effect is in §5: **pointing graphi at the guava repository as checked out
yields 0.13 %**, and the named skip vocabulary reports 2.6 % of the loss.

**This document is a third draft, and its error history is part of its
evidence.** Two independent adversarial reviews have now attacked the
denominator. The first found six errors — one silently dropped every Kotlin call
written inside a string template. The second found **three more**, two of them
(F8, F9) the *same defect class as the first round's largest*, recurring in a
sibling counter and in an entire uncounted family. §3 records all nine. The
response to the second round was not to patch the two node types that were named:
§1.5 replaces inspection with an **enumeration of both grammars' full symbol
tables**, tested on every run, because a defect class that recurs after being
fixed once is a missing method rather than a missing line.

**No headline rate has moved across either round.** All four reproduce to the
digit. What moved is the *widest* denominator (okio 3.28 % → **2.74 %**, kotlinx
17.24 % → **15.28 %**), two published exclusion sizes that were understated by
~45 %, and a false claim that the widest variant was "the most damning" — now
retracted and replaced by a stated line (§1.3).

---

## 1. What is counted, on each side of the fraction

### 1.1 The denominator — from the parse tree, never from the binder

If the denominator were "bound sites plus the sites the binder skipped", the
binder would define its own denominator: every site it cannot even see would
vanish from both sides, and the rate would rise towards 100 % as its blind spots
grew. So the denominator is an **independent walk of the tree-sitter CST**
(`internal/jvmbindrate.countInvocations`) which knows nothing about tables,
types, members or scopes.

It uses the **same grammar** the binder parses with, deliberately. A second
parser would measure the disagreement between two parsers rather than the
coverage of one binder. Independence here means independent *of the binder*, not
of the grammar.

The counted node types were read off the **real embedded grammar**
(`gotreesitter` v0.20.2) with a dump probe, not from upstream documentation:

| language | node type in the denominator | example |
|---|---|---|
| java | `method_invocation` | `helper()`, `a.f()`, `A.stat()` |
| java | `object_creation_expression` | `new A(1)`, `new A(1){…}` |
| kotlin | **`call_suffix`** | `helper()`, `a.f()`, `A(1)`, `l.map{…}`, **and the calls inside `"${f()}"`** |

Nesting is counted, not collapsed: `l.get(0).length()` is **two** Java call
sites and `A(2).m(…)` is two Kotlin ones. That matches the binder, which recurses
into receivers and arguments and reports the inner site separately — so both
sides of the fraction agree about what a call site *is*, and disagree only about
whether it bound. `TestDenominator_MatchesTheHandCount` pins both languages
against an exhaustively hand-enumerated fixture, and refuses a fixture that does
not parse cleanly.

**Kotlin counts `call_suffix`, not `call_expression`, and that is a correction
rather than a preference.** See §3, F1.

### 1.2 The numerator — the shipped binder's own output

`jvmresolve.AnalyzeJavaBodies` / `AnalyzeKotlinBodies` return `[]TypedSite`; the
numerator is the count with `Kind == SiteCall`. It is not a reimplementation:
`TestHarnessMeasuresTheProductNotACopy` pins the harness's in-module import set
to `engine/jvmresolve` alone, so it cannot grow a private table builder or a
private body walker and start measuring a copy of the product. §8.1 closes the
loop by showing the harness's histogram is the **product's own**, reason for
reason, to the digit.

### 1.3 Every exclusion, named with its size

An exclusion whose size is not published is a place to hide a bad rate. Every one
is tallied on every run and printed beside the rate. They split into **three**
groups, and the split is where the second review found two defects (§3, F8/F9).

**Excluded but genuinely a call** — `WidestDenominator()` adds all of these back,
so the widest variant can only ever make a rate look **worse**:

| construct | language | why excluded |
|---|---|---|
| `explicit_constructor_invocation` — `super(…)`, `this(…)` | java | neither binder emits a site for it |
| `constructor_delegation_call` — `constructor(x) : this(x, 0)` | kotlin | **the true twin of the Java row above** (§3, F3) |
| `constructor_invocation` under `delegation_specifier` — `class A : B(1)` | kotlin | the *primary* constructor's superclass call; its Java counterpart `extends B` produces **no node at all**, so this row has no symmetric partner |
| `enum_constant` — `enum E { A(1) }` | java | invokes `E(int)`, emits no call node |
| `enum_entry` — `enum class E { A(1) }` | kotlin | same |
| `infix_expression` — `a shl b` | kotlin | a call **iff** `shl` is a user-declared `infix fun`, which the CST cannot tell; Java's `a + b` is not a call node either |
| `indexing_suffix` — `a[i]`, `a[i] = v`, `"${a[i]}"` | kotlin | a call **iff** `get`/`set` is an `operator fun`; Java's `a[i]` is `array_access`. **The marker is the SUFFIX, not `indexing_expression`** — §3, F8 |
| `additive_expression` — `a + b` | kotlin | → `plus` / `minus` |
| `multiplicative_expression` — `a * b` | kotlin | → `times` / `div` / `rem` |
| `prefix_expression` — `-a`, `!a` | kotlin | → `unaryMinus` / `not` / `inc` / `dec` |
| `postfix_expression` — `a++` | kotlin | → `inc` / `dec`. **`a!!` is the same node type and is not a call** |
| `equality_expression` — `a == b` | kotlin | → `equals`. **`a === b` is the same node type and is not a call** |
| `comparison_expression` — `a < b` | kotlin | → `compareTo` |
| `range_expression` — `a..b` | kotlin | → `rangeTo` |
| `check_expression` / `range_test` — `a in b` | kotlin | → `contains`. **`a is B` is the same node type and is not a call** |
| augmented `assignment` — `a += b` | kotlin | → `plusAssign`. **Plain `a = b` is the same node type and is not a call** |

The nine rows below `indexing_suffix` are the **operator-convention family**, and
they were counted in **no bucket at all** until the second review (§3, F9) —
neither in the denominator nor among the published exclusions, while a quarter of
the same family (`infix` + indexing) *was* published, which implied the published
figure was the whole Kotlin-only operator residual. It is roughly four times
larger. They are excluded on the same Java-symmetry rule and now counted.

Each is discriminated **by its operator token, never by its node type**, because
three of these node types carry a non-call form under the same name. Counting the
node type would overcount okio's postfix family by more than half (157 `!!`
against 103 real `inc`/`dec`).

**Excluded and not a call at all** — never added back:

| construct | language | why |
|---|---|---|
| `constructor_invocation` under `annotation` — `@Anno(v = 1)` | kotlin | not a call |
| `object_literal` — `object : X { … }` | kotlin | a type declaration; its constructor call, where it has one, is already counted in the row above |
| `method_reference` — `A::stat` | java | creates a function value; the call happens elsewhere |
| `callable_reference` — `::stat` | kotlin | same — **and a lower bound**, see §3, F7 |
| `array_creation_expression` — `new int[3]` | java | no callee |
| `a!!` (`postfix_expression`) | kotlin | a null assertion. **Not an operator convention** — no `operator fun` can be declared for `!!` and the source names nothing. It is not "no callee": on Kotlin/JVM it lowers to `Intrinsics.checkNotNull`, a callee the **compiler invents**, which by the line stated below makes it a *synthesized-protocol* construct rather than "not a call at all". Its size is published on its own row here — **okio 157, kotlinx 43** — and is in **no** denominator either way, so nothing moves. |
| `a === b` (`equality_expression`) | kotlin | referential identity; never `equals`. **okio 29, kotlinx 59** |
| `a = b` (plain `assignment`) | kotlin | not a call. **okio 704, kotlinx 672** |
| `a is B` (`check_expression` / `type_test`) | kotlin | a type test. **okio 50, kotlinx 167** |

Those four are the forms the operator-token discrimination **rejects**. They are
published for the same reason the exclusions are: a rejection that is not counted
is a place to hide a denominator choice — and in this case, the choice that makes
this document's operator-family figures *smaller* than the review's (§3, F9).

**Excluded because the source names no callable at all** — the
**synthesized-protocol** group, measured in **both** languages and added to
**neither** denominator:

| construct | language | synthesized calls |
|---|---|---|
| `for (x in xs)` / `for (X x : xs)` | kotlin / **java** | `iterator()`, `hasNext()`, `next()` — ≥3 per loop |
| `val (a, b) = p` | kotlin | `component1()`, `component2()` — one per variable |
| `val p by lazy {}` | kotlin | `getValue()` / `setValue()` |
| `try (X x = …)` | java | `close()` — one per resource |

**These sizes are a LOWER BOUND, in two ways** — stated here, beside the numbers,
rather than only in the code. First, each multiplier is a minimum (`≥3` per
loop). Second, and more importantly, **whole construct families are not counted
at all**: Java string concatenation (`StringConcatFactory`), autoboxing
(`Integer.valueOf`) and the implicit `super()`; Kotlin string-template
`toString()` and `collection_literal` → `arrayOf`. The omissions are systematic
in **both** languages, so they do not tilt the Java/Kotlin comparison, and since
this group is in **no** denominator **no rate in this document depends on the
gap**. Read the figure as "at least this many" — it exists to *bound* the
exclusion, not to enumerate it.

This is the line `WidestDenominator()` stops at, and it is stated rather than
claimed: **a construct whose source names a callable is in; one where the
compiler invents the call is out.** `for (x in xs)` writes no identifier a binder
could resolve, yet implies three calls. Crucially the line is drawn **identically
in both languages** — Java's `for (X x : xs)` is the same construct and is
excluded the same way — so it cannot flatter one language against the other. The
sizes are published (guava **6 851** for-in loops and 43 resources; okio 100 / 28
/ 2; kotlinx 114 / 36 / 11), so this is a bounded residual, not one taken on
trust.

An earlier draft instead claimed the widest denominator was "deliberately the
most damning variant: nothing here can make the published rate look better."
**That claim was false and is retracted** — it was missing a third of the indexing
suffixes and the entire operator family. It has not been replaced with another
superlative; it has been replaced with a stated line and an enumeration that
tests it (§1.5).

**The annotation row is the one an adversarial reader should check first**, and
it is why Kotlin cannot simply count `constructor_invocation`: **the Kotlin
grammar gives an annotation's argument list and a superclass delegation the same
node type**, and only the parent discriminates. Counting them together would put
1 059 annotations into kotlinx.serialization's call-site denominator and 560 into
okio's. Java is not exposed to this — `@Anno(v = 1)` parses as `annotation` →
`annotation_argument_list` → `element_value_pair`, with no `method_invocation`
anywhere. Both facts are pinned against the real grammar by
`TestGrammarShapes_AreWhatWeCount`.

### 1.4 What the denominator does NOT depend on: a compiler

This measurement reads source and asks the binder; it never touches bytecode. So
unlike SW-173's oracle run — which scored guava over the **623 of 3 204** sources
it could stage and compile — **the binding rate covers 100 % of the source files
at each pin**: 3 204/3 204 for guava, 313/313 for okio, 615/615 for
kotlinx.serialization. AC-8's partial-coverage clause therefore has no partial
coverage to declare *on the compile axis*, and the figures here must **not** be
read as covering the same population as the oracle's. The coverage limit that
does apply is a different one, and it is §4.

---

### 1.5 The enumeration — why the node types are no longer chosen by inspection

The denominator has now been wrong **nine** times across two adversarial rounds.
The two the second review found (F8, F9) were the **same defect class** as the
first (F1): a call-bearing node type nobody thought to look for. A defect class
that recurs after being fixed once is not a defect — it is a missing method.

So the node types are no longer chosen by inspection. They are chosen against an
enumeration of the grammars themselves
(`internal/jvmbindrate/grammar_enumeration.go`):

| | java | kotlin |
|---|---:|---:|
| named symbols in the embedded grammar | 166 | 198 |
| …that **occur** on the three pins | **125** | **141** |
| **D** — in the primary denominator | 2 | 1 |
| **W** — names a callable; in the widest denominator | 2 | 15 |
| **P** — synthesized protocol; in **no** denominator | 2 | 3 |
| **N** — not a call | 119 | 122 |

The **N bucket is an explicit list, never a default.** With a default, a grammar
upgrade that adds a call-bearing node type is classified as harmless by silence —
which is exactly how the nine got in.
`TestGrammarEnumeration_ClassifiesEveryOccurringNodeType` walks the same symbol
table at test time and fails on any occurring node type in no bucket;
`…_CallBearingBucketsAreReachable` closes the other half by asserting that every
node type classified as a call actually moves a counter, so the enumeration
cannot document a fix the walk does not perform.

**It has already caught one.** `record_declaration` reached the list by failing
the test rather than by being thought of: guava v33 predates Java records, so no
pin produces the node, and the hermetic fixture does. It is not a call (a record
*declares* its accessors, it does not call them) — but that is now a recorded
decision instead of an accident of corpus coverage.

**It caught a second one, and that one was the census itself (F10).** The java
column above read **120 / N 114** when this section was first published, and
**120 is guava's number**: the intersection had been taken against guava alone
and published as "the three pins". Measured per corpus:

| corpus | java types occurring | in no bucket |
|---|---:|---:|
| guava alone | 120 | 0 |
| okio `.java` | 86 | 0 |
| kotlinx.serialization `.java` | 8 | **5** |
| **all three pins — the published population** | **125** | **5** |
| kotlin, both pins | 141 | 0 |

The D/W/P/N row summed to 120 only because the five unclassified types were
dropped rather than counted. They are `module_declaration`, `module_body`,
`requires_module_directive`, `requires_modifier` and `exports_module_directive`,
from kotlinx.serialization's six `module-info.java` — the files
`corpus/manifest.json` singles out. All five are JPMS module-descriptor syntax:
they declare a module graph, contain no expression, and **cannot be a call**, so
**no rate, denominator, exclusion size or arm in this document moves**; each was
checked individually. They are now classified `bucketNotACall`.

**What actually failed here was the guard, not the arithmetic.**
`TestGrammarEnumeration_ClassifiesEveryOccurringNodeType` was **red** against the
pins — and no configuration ran it there. The PR gate runs it hermetically, where
it passes; the corpus workflow ran its step as `-run TestPins`, which excludes
it. So the method guard had never once executed against the corpus its numbers
come from. That step is now `-run 'TestPins|TestGrammarEnumeration'`. §10 of this
document states that *a census that under-counts is the same defect class as a
denominator that under-counts*; this is that rule applied to the census that
states it.

**What the enumeration does not cover, stated because it is the real residual:**
it classifies **node types, not types**. `a + b` is counted as an
operator-convention call whether `a` is an `Int` (a JVM intrinsic — no callee
exists) or a `BigDecimal` (a genuine `plus` call). No CST can tell those apart.
That is exactly why the family is excluded from the primary denominator and
included in the widest one with both sizes published: the split is unmeasurable
here, so it is **bounded rather than invented**. The 41 java and 57 kotlin
symbols that never occur on the pins are deliberately unclassified — recording a
decision about a node type no corpus produces would be a guess dressed as an
enumeration.

**One operator token on okio is unclassified, and it is published rather than
absorbed.** `UnclassifiedOperator` is the grammar-drift guard: an operator token
the walk does not recognise lands there instead of being guessed into either
bucket. It reads **1** on okio and **0** everywhere else, and the single hit is
fully explained — a `prefix_expression` whose operator token was consumed by
error recovery, in
`okio/src/commonTest/kotlin/okio/CommonRealBufferedSourceTest.kt`, a file that
carries `ERROR` nodes (§4). It is a parse-degradation artefact, not a grammar
gap.

---

## 2. Reproducibility (AC-5), and what the measurement is pinned to

Every pin is measured **twice from scratch** in the same run and the two results
are compared on their **entire rendered report** — files, types, collisions,
numerator, denominator, widest denominator, every exclusion and rejection, the parse
degradation counters, every histogram row and the residual — not on the headline
rate. A difference fails the test with "publish this as a finding, never retry it
away".

**Result: identical on all three pins, at every scope, for both languages.** No
variance was observed and therefore none is averaged away.

The hermetic half (`TestMeasure_IsDeterministic`) repeats the comparison five
times over a fixture.

### 2.1 The measurement pins the TREE, not only the commit

An earlier draft asserted the pin's commit sha from `.git/HEAD` and stopped
there, then enumerated sources by **walking the filesystem**. That leaves a hole,
and a reviewer demonstrated it rather than hypothesising it: an okio checkout
carrying **16 gitignored gradle-generated `.java` files** under
`.gradle/**/dependencies-accessors/**` was swallowed whole. okio's Java whole-pin
figures moved **16.07 % → 12.72 %** (29 files / 139 / 865 → 45 / 149 / 1 171)
while the sha assertion stayed green and the two-run reproducibility check stayed
identical — because both runs read the same wrong file set. `git clean -xdff`
restored exactly 29 `.java` and 284 `.kt`, so **the published numbers were
right**; the guarantee that they stay right did not exist.

The hazard was live, not theoretical: `.github/workflows/jvm-corpus.yml` runs
SW-173's compile step over `/tmp/pins` and then this measurement over **the same
clones**, and this document invites any reader to recompute with
`GRAPHI_JVM_CORPUS_PINS` pointed at a directory whose cleanliness nothing
checked.

**Three assertions now run before a single byte is measured:**

| # | assertion | closes |
|---|---|---|
| 1 | `git rev-parse HEAD` == the pinned **commit** | the checkout is the revision claimed |
| 2 | `git rev-parse HEAD^{tree}` == the pinned **tree** | the figures name the tree they came from |
| 3 | `git status --porcelain --untracked-files=no` is **empty** | no tracked file's bytes differ from that tree |

and the file set itself now comes from **`git ls-tree -r HEAD`**, not from
`filepath.Walk`. That is not a filter — it is the definition of what the pin
contains, so it closes the growth hazard without reintroducing the shrinkage
hazard the old comment feared. A file is in the measurement **iff the pinned
commit contains it**.

| pin | commit | tree | `.java` | `.kt` |
|---|---|---|---:|---:|
| guava | `2214c636…` | `375ac95a…` | 3 204 | 0 |
| okio | `8b870e8e…` | `3b3cd2ef…` | 29 | 284 |
| kotlinx.serialization | `3efe324b…` | `bf86bc14…` | 6 | 609 |

Those counts are `git ls-tree`'s, and they are the counts every figure in this
document rests on.

---

## 3. Two adversarial rounds — nine denominator errors, found and fixed

The first draft of this document published figures computed by a denominator
with six defects. An adversarial review attacked the denominator specifically —
*does it count anything that is not a call site, or miss anything that is* — by
probing the real grammar rather than reasoning about it. Every finding was
**reproduced first-hand before any code changed**, and each now has a fixture.
This section exists because a denominator's error history is part of its
evidence, and because five of the six moved the rate in the **flattering**
direction.

**A second, independent review then found three more (F8, F9 and the §10 census
miss), and that is the more important fact about this table.** F8 and F9 are the
*same defect class as F1* — the grammar's marker is not the node you would guess
— recurring in a sibling counter and in an entire uncounted family, **after F1
had been found and fixed**. Patching what a reviewer names does not close a
defect class. §1.5 is the method that replaced the patching.

| # | defect | direction | fix | measured effect on the pins |
|---|---|---|---|---|
| **F1** | Kotlin string-template calls have **no `call_expression`** — `"${f()}"` parses as `interpolated_expression → simple_identifier + call_suffix`. The whole construct was missing from the denominator. | flattering | count **`call_suffix`**, the grammar's exact invocation marker | +42 sites on kotlinx (15 346 → 15 388), +20 on okio. **Small — 0.3 %** — and stated as measured rather than estimated |
| **F2** | Kotlin `infix_expression` (`a shl b`) and `indexing_expression` (`a[i]`) were counted **nowhere** | flattering | excluded for symmetry with Java, but now **counted**, and in the widest denominator | okio **675 + 269**, kotlinx **797 + 274** — and F2's *fix* was itself defective in two ways, F8 and F9 below |
| **F3** | The doc's symmetry claim was **wrong**: Java's `super(…)` was paired with Kotlin's `class A : B(1)`. The real twin is `constructor_delegation_call`; `: B(1)` is the *primary* constructor's superclass call, whose Java counterpart `extends B` has no node. | flattering (Kotlin's widest only) | both counted, separately, and the asymmetry published | kotlinx 15 delegation calls vs 453 superclass delegations — they are not the same population |
| **F4** | Java `enum_constant` and Kotlin `enum_entry` argument lists counted nowhere | flattering, symmetric | counted; in the widest denominator | guava whole pin **4 103**, guava/src 166, kotlinx 182, okio 90 |
| **F5/F6** | **Parse degradation was silent.** tree-sitter recovers instead of failing, so a file contributes a partial count with a nil error. The doc claimed "a parse error yields zero counts"; measured false. Worse, this suite's **own** grammar-pinning fixture was a one-line Kotlin class body that yields 2 ERROR nodes — it passed by luck. | unknown sign, unbounded | `ParseErrorNodes` / `FilesWithParseErrors` counted and published; `countNodes` now **fails** on any fixture with an ERROR node | §4 — and it is the largest finding of the round |
| **F7** | Kotlin's `callable_reference` count is a **lower bound**: `::f` parses as `callable_reference` but `A::g` and `a::h` parse as `navigation_expression`. Java's `method_reference` catches all forms. | none on the rate | disclosed in code and here | the two languages' reference-exclusion columns are **not comparable** |

**The second round — F8 and F9, both in F2's own fix:**

| # | defect | direction | fix | measured effect on the pins |
|---|---|---|---|---|
| **F8** | **`indexing_expression` is the wrong marker** — exactly F1, in the sibling counter. `a[i] = v` and `a[i] += v` (`operator fun set`) parse as `assignment → directly_assignable_expression → indexing_suffix` with **no `indexing_expression` node at all**, and `"${a[i]}"` yields a bare suffix too. Every indexed **write** and every interpolated read was missing. | **flattering** — `WidestDenominator()` includes the counter, so undercounting it *raised* the widest rate | count **`indexing_suffix`**, exactly as `call_suffix` replaced `call_expression` | okio counted 269, **missed 133 (+49 %)** → **402**; kotlinx counted 274, missed 113 (+41 %) → **387** |
| **F9** | **The operator-convention family was counted in no bucket at all** — `additive`, `multiplicative`, `prefix`, `postfix`, `equality`, `comparison`, `range`, `contains`, augmented assignment. Neither in the denominator nor among the published exclusions, while `infix` + indexing *were* published, implying the published figure was the whole residual. | flattering, and ~4× larger than what was published | counted, discriminated **by operator token**, and added to the widest denominator | okio **3 886**, kotlinx **2 073** against the 944 / 1 071 that were published |

**The adjacent error to F8 was checked and is NOT present**, which matters
because over-correcting would have been its own defect: a multi-dimensional read
`a[i][j]` emits one suffix per dimension, and on both pins every
`indexing_expression` carries exactly one suffix. Nothing is undercounted there,
and the fix does not pretend otherwise. It is pinned by
`TestIndexingSuffix_CountsWritesAndInterpolations`.

**This document's F9 figures are deliberately *smaller* than the review's, and
the difference is the point.** The review counted node types; this counts
operator tokens. Three of those node types carry a non-call form under the same
name, so the raw node counts include constructs that call nothing:

| over-count | okio | kotlinx |
|---|---:|---:|
| `a!!` counted as `inc`/`dec` (`postfix_expression`) | **157** | 43 |
| `a === b` counted as `equals` (`equality_expression`) | 29 | 59 |
| `@Ann a` counted as an operator (`prefix_expression`) | 0 | 2 |
| indexed set counted twice (already in `indexing_suffix`) | 132 | 100 |

and one construct the review's list **missed** in the other direction:

| under-count | okio | kotlinx |
|---|---:|---:|
| `a in b` / `when { in 1..5 -> }` → `contains` | **69** | 60 |

Net: this document's widest denominators are okio **24 809** and kotlinx
**19 295**, against the review's projected 24 926 and 19 336 — a difference of
117 and 41. The resulting rates are **2.74 %** and **15.28 %** against the
review's 2.73 % and 15.25 %. The correction here is very slightly *less* damning
than the review's, and it is stated plainly for that reason: the difference is
non-calls removed, not calls hidden, and every one of those rejections is
published with its size in §1.3.

One further asymmetry was found and is **not** fixed, because it has no correct
answer: Java's `new X(){…}` is an `object_creation_expression` and **is** in the
denominator, while Kotlin's `object : X { … }` is an `object_literal` and is not.
It is counted (`object_literal`: kotlinx 22, okio 90) so its size is visible.

**None of the nine moved a headline rate.** All four reproduce to the digit
across both rounds — Java `guava/src` 21.39 %, guava whole-pin 0.13 %, kotlinx
19.16 %, okio 3.47 % — because every defect was in the *widest* variant or in a
published exclusion size. That is a fact about where the errors landed, not a
defence: F8 falsified a written claim about the widest denominator, and F9 made a
published exclusion size read as four times more complete than it was.

---

## 4. FINDING B-0 — the Kotlin grammar does not parse 10–13 % of these pins cleanly

This is the finding the first draft could not have made, because it had no
counter for it.

`gts.Parser.Parse` returns a **nil error** on malformed input and hands back a
tree containing `ERROR` nodes with arbitrary subtrees missing. Both sides of the
fraction read that tree — the binder's walkers and this harness's counter — so a
recovered region loses sites from the numerator *and* the denominator, in a ratio
nothing here measures.

| pin / language | files | files with `ERROR` nodes | `ERROR` nodes |
|---|---:|---:|---:|
| **kotlinx.serialization — kotlin** | 609 | **80 (13.1 %)** | 6 207 |
| **okio — kotlin** | 284 | **28 (9.9 %)** | 1 274 |
| kotlinx.serialization — kotlin, `core/` | 103 | 14 (13.6 %) | 435 |
| okio — kotlin, `okio/` | 232 | 26 (11.2 %) | 1 264 |
| **guava — java, `guava/src`** | 621 | **0** | **0** |
| guava — java, `android/guava/src` | 609 | 1 (0.2 %) | 50 |
| guava — java, whole pin | 3 204 | 9 (0.3 %) | 462 |
| okio — java | 29 | 0 | 0 |

### 4.1 The direction of the bias — determined, and it is anti-flattering

An earlier draft of this section said the direction was **"not determined — both
sides of the fraction shrink"** and listed it under what the measurement does not
establish. **That was wrong in method, and the correction does not favour the
draft's caution.** Determining the *direction* never required knowing what the
grammar dropped; it only required comparing the two subpopulations, which costs
one partition.

**Method, and the confound it avoids.** The binder's table is built **once over
all files**, so both arms bind against an **identical index** — a clean file that
references a type declared in a dirty file still resolves it. Rebuilding a table
per arm would have measured "how well does a corpus half bind itself", a
different and much easier question that would have starved the clean arm of
types. The numerator is then partitioned by each site's source file and the
denominator by that same file's own `ERROR`-node count, so the two arms
**partition** the corpus and reconstitute the published totals exactly.
`TestParseArms_PartitionTheCorpus` asserts the reconstitution rather than
trusting it — two arms that quietly failed to sum would be a second measurement
wearing a split's clothes.

| pin / scope | arm | files | bound | CST sites | rate |
|---|---|---:|---:|---:|---:|
| **kotlinx — whole** | **clean** | 529 | 2 908 | 13 748 | **21.15 %** |
| | **dirty** | 80 | 41 | 1 640 | **2.50 %** |
| | *all* | *609* | *2 949* | *15 388* | *19.16 %* ✓ |
| **okio — whole** | **clean** | 256 | 681 | 19 128 | **3.56 %** |
| | **dirty** | 28 | **0** | 498 | **0.00 %** |
| | *all* | *284* | *681* | *19 626* | *3.47 %* ✓ |
| kotlinx — `core/` | clean | 89 | 535 | 2 287 | 23.39 % |
| | dirty | 14 | 10 | 372 | 2.69 % |
| okio — `okio/` | clean | 206 | 565 | 15 997 | 3.53 % |
| | dirty | 26 | 0 | 493 | 0.00 % |
| **guava — whole (java)** | clean | 3 195 | 349 | 269 996 | 0.13 % |
| | dirty | 9 | **0** | 1 896 | **0.00 %** |
| **guava — `guava/src`** | clean | **621** | 6 065 | 28 354 | **21.39 %** |
| | dirty | 0 | — | — | n/a |

**The direction is unambiguous: a recovered tree destroys the NUMERATOR far
faster than the denominator.** The binder binds 41 of 1 640 sites in kotlinx's
dirty files and **0 of 498** in okio's, while the CST still contributes a full
denominator from those same files — because tree-sitter's recovery leaves the
invocation nodes standing even where the binder can no longer walk the body. So
the published Kotlin rates are **depressed** by dirty parses, not inflated.

**And the effect is not a Kotlin phenomenon.** Java's dirty arm binds **0 of
1 896** on guava's whole pin, exactly as okio's does. The mechanism is parse
quality, not language — Kotlin merely suffers it 40× more often (§8.2).

"Undetermined" therefore erred **against** the product, which is the right
direction to err. But it was an *incomplete* measurement presented as an
*unavailable* one, and the two are not the same thing. The split costs ~40 lines
and is now printed on every run.

**Consequences.**

1. **The headline Java figure is over a completely clean parse.** 0 ERROR nodes
   in 621 files. Nothing below qualifies it.
2. **Both Kotlin headline figures are understated**, by the mechanism above. No
   correction is applied and none should be: the clean-arm rate is not a better
   estimate of "the rate", it is the rate over a different population. Both are
   published; neither is presented as the headline.
3. **A per-file magnitude illustrates the mechanism**, measured on a fixture: a
   Kotlin class body whose members are not newline-separated yields 3 ERROR nodes
   and **loses two of its three calls**. Whitespace alone changes that
   denominator threefold. (`TestParseDegradation_IsCountedNotAssumedAway`)
4. **This is a measurement-quality asymmetry between the languages**, on top of
   the evidence asymmetry in §7.2 — and §8 now reads very differently because of
   it.

Not filed as a defect and not fixed — it is a property of the vendored grammar,
and changing grammars is not this story's business. It is now *visible*, which is
the whole point.

---

## 5. FINDING B-1 — pointing graphi at guava as checked out yields 0.13 %

`guava` ships every `com.google.common.*` class **twice**: once under
`guava/src` (the JRE flavour) and once under `android/guava/src` (the Android
flavour), with the same package clause and the same class name. The binder's
`tabledType` (`engine/jvmresolve/body_java.go:686-692`) binds a FQN only when it
has **exactly one** candidate:

```go
func (w *javaBodyWalk) tabledType(fqn string) *Type {
	cands := w.ix.byFQN[fqn]
	if len(cands) != 1 {
		return nil
	}
	return cands[0]
}
```

`nil` means the type's **entire body walk is abandoned**, at the cost of one
`java_body_unmatched` increment.

| scope | types in a collided FQN | `java_body_unmatched` | rate |
|---|---:|---:|---:|
| whole pin | **6 765 of 7 084 (95.5 %)**, across 3 341 colliding FQNs | 3 095 | **0.13 %** |
| `guava/src` | 0 of 1 483 | 0 | **21.39 %** |
| `android/guava/src` | 0 of 1 438 | 0 | **22.63 %** |

**Why this matters more than the number does.** 271 543 call sites are unbound
on the whole pin and the named vocabulary accounts for 7 114 of them — **2.6 %**.
The remaining 264 429 are invisible to the counters, because one
`java_body_unmatched` increment stands for *every* call site in an abandoned
body, however long that body is. `TestHistogram_IsNotCallSiteKeyed` demonstrates
exactly that on a hermetic fixture: two files declaring the same FQN, five calls
each — 10 unbound sites, 2 counter increments — and then the same fixture with an
8× longer body, where the denominator grows to 80 and **the counter stays at 2**.

**Not filed as a defect and nothing here was fixed.** It is a measured property
of the binder's abstention rule interacting with a monorepo layout, and whether
it should change is a design question this story does not open. What it is,
unambiguously, is the reason a binding rate must never be published without its
denominator *and* its skip histogram: the 0.13 % row and the 21.39 % row describe
the same binder on the same code.

okio carries a smaller instance of the same shape — **95 of 337 Kotlin types in
32 colliding FQNs**, from the Kotlin-multiplatform `expect`/`actual` source-set
layout, which is the same layout SW-173 recorded as the reason okio could not be
compiled. kotlinx.serialization: 64 of 1 768 in 24.

---

## 6. FINDING B-2 — the histogram does not sum to the remainder, and cannot (AC-4)

AC-4 asks for the named counters to sum to the unbound remainder and, **if they
do not**, for the discrepancy to be published as a finding. They do not. The
residual (`denominator − bound − histogram`) takes **both signs** on real data:

| corpus / scope | unbound sites | histogram total | residual |
|---|---:|---:|---:|
| guava — whole pin | 271 543 | 7 114 | **+264 429** |
| guava — `guava/src` | 22 289 | 22 966 | **−677** |
| guava — `android/guava/src` | 20 786 | 21 207 | −421 |
| okio — java, whole pin | 726 | 784 | −58 |
| okio — kotlin, whole pin | 18 945 | 19 534 | **−589** |
| okio — kotlin, `okio/` | 15 925 | 16 206 | −281 |
| kotlinx — kotlin, whole pin | 12 439 | 11 917 | **+522** |
| kotlinx — kotlin, `core/` | 2 114 | 2 019 | +95 |

**The discrepancy is not "sites lost outside the named vocabulary".** It is the
arithmetic consequence of differencing two quantities with **different keys**:
the named counters are not call-site-keyed. Two mechanisms, each isolated on its
own hermetic fixture with a hand count, and a control that bounds the
explanation:

1. **A value site increments the call-site vocabulary.** `t.field` on an external
   receiver increments `java_receiver_external` while contributing **nothing** to
   a call-site denominator. → drives the residual **negative**.
2. **`java_body_unmatched` fires once per body**, so one increment stands for an
   unbounded number of unbound call sites (§5). → drives it **positive**.
3. **CONTROL — a chained call does not double-count.** `t.alpha().gamma()` is two
   sites and produces exactly two increments, so *over*-counting is excluded as a
   mechanism and (1) and (2) are the whole story for call sites rather than two
   examples of many.

**The consequence for anyone reading the histogram.** It answers *"for what named
reasons did the binder abstain, and how often"* — which is what SW-171 built it
for and what it is honest at. It does **not** answer *"how many call sites are
unbound and why"*, and subtracting it from a call-site denominator is not a valid
operation. That is published here rather than left for a later reader to
discover by getting a negative number.

---

## 7. The published rates, with the skip histogram beside every one

Never instead of it. A high rate with a large unexplained skip bucket is exactly
the claim this document exists to make uncheckable.

### 7.1 Java — guava `guava/src` (the headline Java scope)

**21.39 % = 6 065 bound call sites / 28 354 CST call sites.** Widest denominator
28 906 → **20.98 %**. Parse: **0 files with ERROR nodes.** 621 source files, all
tabled. 1 483 tabled types, **0** in a collided FQN. Bound *value* sites (not in
the numerator): 1 237.

| named reason | count | share of the histogram |
|---|---:|---:|
| `java_lookup_open_chain` | 9 787 | 42.6 % |
| `java_receiver_external` | 5 747 | 25.0 % |
| `java_receiver_untyped` | 4 396 | 19.1 % |
| `java_lookup_not_found` | 2 238 | 9.7 % |
| `java_lookup_ambiguous` | 694 | 3.0 % |
| `java_super_external` | 104 | 0.5 % |
| **total** | **22 966** | |

Exclusions at this scope: `explicit_constructor_invocation` 386, `enum_constant`
166, `method_reference` 158, `array_creation_expression` 235; all Kotlin-only
buckets 0.

The two largest buckets are the honest kind, in ADR 0008's own terms:
`java_lookup_open_chain` is a supertype chain leaving the repository and
`java_receiver_external` is a receiver typed outside it — both are boundaries of
the graph, not recall gaps a binder change would close. Together they are
**67.6 %**. `java_lookup_ambiguous` — the D6 overload-set drop, the bucket that
*is* a closable recall gap — is **3.0 %**.

### 7.2 Java — the other scopes

| scope | rate | numerator | denominator | widest | histogram | parse errors |
|---|---:|---:|---:|---:|---:|---:|
| guava `android/guava/src` | 22.63 % | 6 078 | 26 864 | 22.19 % | 21 207 | 1 file / 50 |
| guava whole pin | **0.13 %** | 349 | 271 892 | 0.13 % | 7 114 | 9 files / 462 |
| okio (29 `.java`) | 16.07 % | 139 | 865 | 16.01 % | 784 | 0 |
| okio `okio/` (11 `.java`) | 9.68 % | 18 | 186 | 9.68 % | 142 | 0 |
| kotlinx.serialization (6 `.java`) | **n/a** | 0 | **0** | n/a | 0 | 0 |

guava whole pin: `java_body_unmatched` 3 095 · `java_lookup_open_chain` 1 584 ·
`java_receiver_untyped` 1 117 · `java_receiver_external` 491 ·
`java_receiver_ambiguous` 431 · `java_lookup_not_found` 254 ·
`java_lookup_ambiguous` 135 · `java_super_external` 7. Read only with §5 and §6
beside it: it accounts for 2.6 % of the unbound sites at that scope. Exclusions
there include `enum_constant` **4 103** and `array_creation_expression` 5 690.

okio's Java: `java_receiver_ambiguous` 302 · `java_receiver_untyped` 244 ·
`java_lookup_not_found` 115 · `java_receiver_external` 91 ·
`java_lookup_open_chain` 27 · `java_super_external` 5.

kotlinx.serialization's six `.java` files are `module-info.java` JPMS
declarations. They carry no methods and produce **zero** call sites — which the
manifest already predicted and this run confirms independently (0 tabled types).
**No rate is published for them: 0 / 0 is not 0 %.**

### 7.3 Kotlin — kotlinx.serialization (the headline Kotlin scope)

**19.16 % = 2 949 bound call sites / 15 388 CST call sites.** Widest denominator
**19 295 → 15.28 %**. Parse: **80 of 609 files carry ERROR nodes (§4)** — on the
529 that parse cleanly the rate is **21.15 %** (§4.1). 1 768 tabled types, 64 in
24 colliding FQNs. Bound value sites: 484.

| named reason | count | share |
|---|---:|---:|
| `kotlin_lookup_not_found` | 5 033 | 42.2 % |
| `kotlin_receiver_untyped` | 1 960 | 16.4 % |
| `kotlin_val_inferred` | 1 628 | 13.7 % |
| `kotlin_lambda_rebound` | 1 019 | 8.6 % |
| `kotlin_trailing_lambda` | 946 | 7.9 % |
| `kotlin_receiver_external` | 936 | 7.9 % |
| `kotlin_lookup_ambiguous` | 212 | 1.8 % |
| `kotlin_receiver_ambiguous` | 79 | 0.7 % |
| `kotlin_body_unmatched` | 58 | 0.5 % |
| `kotlin_lookup_open_chain` | 46 | 0.4 % |
| **total** | **11 917** | |

Excluded-but-a-call: superclass delegation 453 · `infix_expression` 797 ·
**`indexing_suffix` 387** · `enum_entry` 182 · `constructor_delegation_call` 15 ·
**operator convention 2 073** (additive 372 · multiplicative 168 · prefix 307 ·
postfix 113 · equality 615 · comparison 344 · range 55 · contains 60 ·
augmented-assign 39).
Excluded-not-a-call: annotation `constructor_invocation` **1 059** ·
`callable_reference` 21 · `object_literal` 22 · `a!!` 43 · `a === b` 59 ·
plain `a = b` 672 · `a is B` 167.
Synthesized protocol (in no denominator): 114 for-in loops · 36 destructuring
components · 11 delegated properties.

### 7.4 Kotlin — okio

**3.47 % = 681 / 19 626.** Widest **24 809 → 2.74 %**. Parse: **28 of 284 files
carry ERROR nodes** — on the 256 that parse cleanly the rate is **3.56 %** (§4.1).
337 tabled types, **95 in 32 colliding FQNs**. Value sites 88.

| named reason | count | share |
|---|---:|---:|
| `kotlin_val_inferred` | 4 838 | 24.8 % |
| `kotlin_lookup_not_found` | 4 519 | 23.1 % |
| `kotlin_receiver_untyped` | 4 504 | 23.1 % |
| `kotlin_receiver_ambiguous` | 2 813 | 14.4 % |
| `kotlin_lambda_rebound` | 951 | 4.9 % |
| `kotlin_trailing_lambda` | 799 | 4.1 % |
| `kotlin_lookup_open_chain` | 513 | 2.6 % |
| `kotlin_lookup_ambiguous` | 289 | 1.5 % |
| `kotlin_receiver_external` | 233 | 1.2 % |
| `kotlin_body_unmatched` | 75 | 0.4 % |
| **total** | **19 534** | |

Excluded-but-a-call: `infix_expression` **675** · **`indexing_suffix` 402** ·
superclass delegation 114 · `enum_entry` 90 · `constructor_delegation_call` 16 ·
**operator convention 3 886** (additive 957 · multiplicative 996 · prefix 712 ·
postfix 103 · equality 519 · comparison 312 · range 20 · contains 69 ·
augmented-assign 198).
Excluded-not-a-call: annotation `constructor_invocation` 560 · `object_literal`
90 · `callable_reference` 1 · `a!!` **157** · `a === b` 29 · plain `a = b` 704 ·
`a is B` 50.
Synthesized protocol (in no denominator): 100 for-in loops · 28 destructuring
components · 2 delegated properties.
Unclassified operator tokens: **1** — the single parse-recovery artefact
explained in §1.5.

**okio is the pin where the operator-convention family matters most.** Its
3 886 operator calls plus 675 infix and 402 indexing suffixes total 4 963 —
**25 % of the size of its entire primary denominator**. That is the residual F9
left unpublished, and it is why okio's widest rate (2.74 %) now sits 0.73 pp
*below* its primary rather than the 0.19 pp the first draft reported.

`kotlin_val_inferred` at 24.8 % is the "idiomatic-Kotlin recall cost" ADR 0008's
D2 predicted, and okio is the pin where it is largest. It is a *closable* gap in
principle — inferring `val` types is what a real type checker does — and is
therefore not in the same class as `receiver_external`.

### 7.5 Kotlin — the module scopes

| scope | rate | numerator | denominator | widest | histogram | parse errors | clean-arm rate |
|---|---:|---:|---:|---:|---:|---:|---:|
| kotlinx `core/` | 20.50 % | 545 | 2 659 | **15.82 %** (3 446) | 2 019 | 14 files / 435 | **23.39 %** |
| okio `okio/` | 3.43 % | 565 | 16 490 | **2.73 %** (20 667) | 16 206 | 26 files / 1 264 | **3.53 %** |

For completeness, the widest rates at the Java scopes — unchanged by F8/F9,
because Java has no operator-convention family at all and no indexing suffix:
guava `guava/src` **20.98 %** (28 906), `android/guava/src` **22.19 %** (27 395),
guava whole-pin **0.13 %** (277 188), okio's Java **16.01 %** (868).

---

## 8. FINDING B-3 — the Java/Kotlin asymmetry, published rather than averaged

Four asymmetries. None is averaged away and none is presented as a defect.

> **This section was rewritten after §4.1 determined the parse-bias direction,
> and the rewrite changes its conclusion.** The earlier draft presented parse
> dirtiness as one asymmetry among four. It is not: on the headline Kotlin pin it
> is doing **nearly all the work** of the Java/Kotlin gap. What survives is the
> *intra-Kotlin* spread, which is real and is not a parse artefact.

### 8.0 The headline Java/Kotlin gap on kotlinx is almost entirely a parse artefact

Comparing like with like — clean parses against clean parses (§4.1):

| scope | rate as published | **rate over cleanly-parsed files only** |
|---|---:|---:|
| **java — guava `guava/src`** | 21.39 % | **21.39 %** (621 of 621 files are clean) |
| **kotlin — kotlinx.serialization** | 19.16 % | **21.15 %** |
| kotlin — okio | 3.47 % | **3.56 %** |

**On cleanly-parsed files kotlinx.serialization binds at 21.15 % against
guava/src's 21.39 % — a gap of 0.24 pp.** The published 2.23 pp gap between those
two headline figures is therefore **almost entirely a measurement artefact of the
vendored Kotlin grammar**, not a statement about the binder's capability on
Kotlin. Anyone reading "Java 21.39 %, Kotlin 19.16 %" as evidence that the Kotlin
binder is weaker than the Java one is reading a parse-quality difference.

Three things that must not be read into this:

1. **It does not make the published 19.16 % wrong.** That is the rate over the
   pin as it exists, which is what a user pointing graphi at kotlinx.serialization
   gets. The clean-arm rate is the rate over a *different population*, and it is
   published beside it rather than instead of it.
2. **It does not transfer to okio** — see §8.1, which now carries the finding.
3. **It says nothing about whether the bindings are correct.** §8.3 is untouched
   by this and remains the sharpest asymmetry in the document.

### 8.1 The rate spread within Kotlin is larger than the gap between the languages — and it survives the parse correction

Java's two clean scopes agree closely (21.39 % / 22.63 %). Kotlin's two corpora
do not: **19.16 %** on kotlinx.serialization against **3.47 %** on okio — a 5.5×
spread. Whatever a Kotlin binding rate is, it is not one number, and a single
published Kotlin figure would be an average across a spread wider than the
Java/Kotlin difference itself. **No average is published.**

**This is the finding that survives §8.0, and it is now the load-bearing one.**
On cleanly-parsed files the spread is **21.15 % against 3.56 % — 5.9×**, slightly
*wider* than the 5.5× published. So the intra-Kotlin spread is **not** a parse
artefact: both arms of it are measured over clean parses and they still differ by
a factor of six. Whatever explains okio's 3.56 % is a real property of the binder
meeting that codebase, and §8.1's composition evidence below is the only
measured lead this document has on it.

The measured composition differs correspondingly: okio's largest bucket is
`kotlin_val_inferred` (24.8 %) and its `kotlin_receiver_ambiguous` is 14.4 %
against kotlinx's 0.7 % — consistent with okio's `expect`/`actual` multiplatform
layout, which is also the source of its 32 colliding FQNs (§5). Stated as a
correspondence between two measurements, **not** as a proven cause.

### 8.2 Kotlin's parses are 40× dirtier — and that is now the *largest* explained effect

0 ERROR-node files in 621 for the headline Java scope; 80 in 609 for the headline
Kotlin one. §4. This is a property of the vendored grammar, not of the binder,
and it means the two languages' figures are not equally reliable *as
measurements*, before any question about the binder arises.

§4.1 now quantifies what that costs, and it is not a rounding effect: the dirty
arm binds **41 of 1 640** on kotlinx and **0 of 498** on okio. The mechanism is
not Kotlin-specific — guava's own dirty arm binds **0 of 1 896** — but Kotlin
meets it 40× more often, so it lands almost entirely on the Kotlin figures. This
is the asymmetry §8.0 is about, and it is the one a grammar upgrade could remove
without touching the binder at all.

### 8.3 Kotlin's bound sites are not verified at the precisions Java's are

This is the asymmetry that matters most for reading the rates, and it comes from
SW-172/SW-173 rather than from this measurement. **A binding rate says nothing
about whether the bindings are right**; the oracle answers that, and it can say
much less about Kotlin than about Java:

| | java (guava) | kotlin (kotlinx.serialization) | kotlin (okio) |
|---|---|---|---|
| pin compiles reproducibly | yes — 623 sources, 0 errors, 1 966 classes | yes — 52 sources, 0 errors, 255 classes | **no** — `not-compiled` |
| oracle by-name | **SOUND**, 0 counterexamples | 27 counterexamples, **all classified as the oracle accusing correct code** | not run |
| oracle by-arity | **SOUND** | **vacuously** sound — 0 of 1 009 judged | not run |
| oracle by-signature | **SOUND** | **vacuously** sound — 0 of 670 judged | not run |
| why Kotlin declines | — | all 351 abstain under `kotlin_bytecode_shape_unproven` | — |

So: **Java's 21.39 % is a rate of bindings an independent bytecode oracle found
sound at all three precisions over a large compiled subset. Kotlin's 19.16 % is a
rate of bindings no oracle has judged at any precision finer than by-name, and
okio's 3.47 % is a rate of bindings no oracle has judged at all.** The two
numbers look comparable and are not equally supported.

### 8.4 The oracle's coverage and this measurement's coverage are different populations

SW-173's guava figures are over the **623 staged sources** it could compile;
these are over **all 3 204**. Neither is wrong; they are not interchangeable, and
no row here is combined with a row from `jvm-corpus-compile-strategy.md`.

---

## 9. FINDING B-4 — Phase B is not the graph: the product-visible rate is lower

The rates above are **Phase B**: the site was typed and its member bound. Phase C
(`engine/jvmresolve/emit.go`) then maps both endpoints onto committed graph nodes
and drops what has none. That step needs a full ingest, so the harness does not
run it — and §7 must not be read as a count of confirmed edges.

It was measured separately, through the **built CLI**:

```bash
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi ./cmd/graphi   # 4f0e1a20…
cp -r <pin>/guava/src /tmp/guava-jre && cd /tmp/guava-jre && git init -q .
GRAPHI_JVM_TYPERESOLVE=1 /tmp/graphi rebuild
GRAPHI_JVM_TYPERESOLVE=1 /tmp/graphi trust-report --json     # the abstention block
GRAPHI_JVM_TYPERESOLVE=1 /tmp/graphi status --json           # for db_path
sqlite3 <db_path> "select kind, confidence_tier, count(*) from edges \
  where kind in ('calls','references') group by kind, confidence_tier;"
```

### 9.1 The harness's histogram IS the product's, reason for reason

`trust-report --json` on guava `guava/src`, Java, reports total **26 708**:

| reason | trust-report | harness (§7.1) |
|---|---:|---:|
| `java_lookup_open_chain` | 9 787 | 9 787 |
| `java_receiver_external` | 5 747 | 5 747 |
| `java_receiver_untyped` | 4 396 | 4 396 |
| `java_lookup_not_found` | 2 238 | 2 238 |
| `java_lookup_ambiguous` | 694 | 694 |
| `java_super_external` | 104 | 104 |
| `emit_from_no_node` | **2 716** | — Phase C, not run by the harness |
| `emit_to_no_node` | **1 026** | — Phase C, not run by the harness |
| **total** | **26 708** | 22 966 + 2 716 + 1 026 = **26 708** |

kotlinx.serialization `core/`, Kotlin, closes the same way: product total
**2 040** = harness 2 019 + `emit_from_no_node` 13 + `emit_to_no_node` 8, with all
nine Phase B reasons identical to the digit. **The numerator and histogram in
this document are the shipped binder's, not a harness's approximation of them.**

### 9.2 The full accounting, guava `guava/src`

| stage | count |
|---|---:|
| CST call sites (denominator) | 28 354 |
| Phase B bound **call** sites | 6 065 → **21.39 %** |
| Phase B bound **value** sites | 1 237 |
| Phase B bound sites, both kinds | 7 302 |
| dropped in Phase C (`emit_from_no_node` 2 716 + `emit_to_no_node` 1 026) | −3 742 |
| sites surviving Phase C's named drops | 3 560 |
| **`confirmed` edges in the store** (`calls` 2 778 + `references` 137) | **2 915** |
| difference, edge-identity dedup | 645 |

The 645 are not lost sites: `qn.go:192` collapses overloads onto one node, so two
call sites sharing a `(from, to, kind)` triple collapse onto one edge id. The
confirmed-edge count is therefore a **lower bound** on surviving sites and is not
itself a site rate.

**The product-visible Java figure: 2 778 confirmed `calls` edges over 28 354 CST
call sites = 9.80 %.** For kotlinx `core/`: 467 over 2 659 = **17.56 %**.

**Kotlin loses far less at Phase C than Java does** — `emit_from_no_node` is 13
on kotlinx `core/` against 2 716 on guava `guava/src`. Recorded as a measured
fact and deliberately **not** diagnosed; diagnosing it is not this story's job
and a guess would be worth nothing.

For context, the same store also holds 1 923 `derived` and 618 `heuristic`
`calls` edges from the generic linker — the tier a user gets today, since the JVM
registrants are default-off until SW-179.

---

## 10. AC-6 — what replaces the bare "3517 sites", and what it was

> **The old figure was a numerator without a denominator.** It is not corrected
> here, and it is not deleted. It is stated, and then answered.

`3517 typed sites` appears at **four** sites in this repository and was quoted in
support of ADR 0008 D2. It says how many sites the binder bound on
kotlinx.serialization; it never said out of how many exist. The independent
review's R6 named exactly this — *"`3517 typed sites` is not that answer"* — and
dropped Kotlin's ≥50 % recall threshold on the strength of it.

**The replacement, on the same pin at the same sha (`3efe324b`):**

> **19.16 % — 2 949 bound call sites of 15 388 CST call sites**, plus 484 bound
> value sites, published with the ten-row skip histogram in §7.3, every
> exclusion sizes, the parse-degradation counters in §4 and the asymmetry table
> in §8.3.

**A second, unexpected finding about the old number.** Today's binder produces
**3 433** typed sites on that pin (2 949 call + 484 value) — **84 fewer than
3 517**. What the old figure counted could not be determined from the tree, and
the binder has changed since it was taken (ADR 0008's D6 overload rule was
corrected on 2026-08-16 after JVMSOUND-001/002; SW-170 re-keyed the sweep). So
the published numerator is **not reproducible from this tree**, in addition to
never having had a denominator. Recorded rather than smoothed over, and no
attempt is made to reconcile the 84.

Amended in place, each with the old figure kept and labelled:

| site | what it is | status |
|---|---|---|
| `corpus/manifest.json` (kotlinx pin `notes`) | the machine-readable pin record | **amended** |
| `corpus/README.md:30` (kotlinx pin row) | the human-readable twin of the above | **amended** |
| `docs/plan/2026-08-…-execution-plan-v1.md` (M1.2) | the plan's supporting figure | **amended** |
| `docs/decisions/2026-08-language-ga-independent-review.md:104` | a **dated** independent-review record | **deliberately not amended** |

D6 says a published record is never rewritten, and R6's whole point was that the
figure is inadequate — which this document agrees with, so amending it would
falsify the record of the objection rather than answer it.

**This census was itself wrong in the first draft, and the correction is the
point.** It said "three sites" and amended two, missing `corpus/README.md:30` —
the human-readable twin of the manifest entry that *was* amended: same claim,
same pin, in the file a reader is far more likely to open. It is a live
descriptive README, not a dated record, so the D6 exemption never applied to it.
An independent review found it. A census that under-counts is the same defect
class as a denominator that under-counts, and it is recorded here for the same
reason.

---

## 11. What this measurement does NOT establish

1. **Whether the bindings are correct.** That is the oracle's job (SW-172 /
   SW-173) and §8.3 records how unevenly it covers the two languages. A high rate
   of wrong bindings would be worse than a low rate of right ones.
2. **A D2 threshold.** Not proposed, not implied, not hinted at. AC-7.
3. **The SIZE of the bias from Kotlin's 10–13 % dirty parses.** Its *direction*
   was previously listed here as undetermined; **§4.1 determines it** — dirty
   parses depress the rate — and the clean/dirty split is published. What remains
   unmeasurable is how many sites the recovered trees dropped from the
   denominator, so the clean-arm rate is a rate over a different population
   rather than a corrected estimate of the same one. No correction is applied.
4. **Why guava loses 2 716 sites to `emit_from_no_node` and kotlinx `core/` loses
   13.** Measured, undiagnosed (§9.2).
5. **Whether the FQN-collision behaviour in §5 should change.** Measured, not
   filed, not fixed.
6. **Whether Kotlin's operator-convention exclusions hide real calls, and how
   many.** The whole family is now counted (okio 4 963 including infix and
   indexing, kotlinx 3 257) and in the widest denominator, but the CST classifies
   **node types, not types**: `a + b` is a real `plus` call on a `BigDecimal` and
   a JVM intrinsic with no callee on an `Int`, and nothing here can tell them
   apart. The true split is unknown and is bounded rather than invented (§1.5).
6b. **What the 41 java and 57 kotlin grammar symbols that never occur on the pins
   would be.** Deliberately unclassified — see §1.5.
7. **Anything about Go**, which is `typed-confirmed` through `go/types` and out
   of scope by the ticket, or about the other 19 shipped languages.
8. **A rate for kotlinx.serialization's Java.** 0 of 0 is not 0 %.
9. **Per-file, per-package or per-symbol rates.** The numerator has site
   granularity, but SW-171's histogram is repository-global per language with no
   attribution, so no rate below repository granularity is publishable *with a
   histogram beside it* — and this document does not publish one without.

---

## 12. Provenance

| | |
|---|---|
| branch | `claude/kotlin-java-canonical-ga-t3b8km` |
| harness | `internal/jvmbindrate` (CI-only; `TestHarnessIsAbsentFromTheShippedBinary`) |
| CLI used for §9 | `./cmd/graphi`, `-trimpath -buildvcs=false`, sha256 `4f0e1a20689e3410ec9226511cb8e03b43fa139980427fa90ea265cf1dfa88b6` |
| grammars | `github.com/odvcencio/gotreesitter` v0.20.2, embedded Java and Kotlin |
| guava | commit `2214c63670fc161da170ac6e1a2d6d07e1531a55` (v33.0.0), **tree `375ac95aadc97ff4989a4a6fa60fd606d8122050`** |
| okio | commit `8b870e8eaacecb1c1ceffbbb47246112604a1f92` (3.9.1), **tree `3b3cd2ef8f11b7fb43191bd2115220c2410d6f3e`** |
| kotlinx.serialization | commit `3efe324be422ead21ca44f2f6318e1791c166556` (v1.6.3), **tree `bf86bc149a72ddef6c5bcf9dea9537d5d2506c0e`** |
| pin assertions | commit **and** tree asserted at run time, working tree asserted clean, sources enumerated from `git ls-tree -r HEAD` (§2.1) |
| compiler used | **none** — this measurement never touches bytecode (§1.4) |
| `parityreport.CandidateSHA` | **unmoved** at `3b8d43f6…`; no parity row, no published matrix and no `parity-classes*.yaml` touched by this story |

**Product bytes.** This story adds a CI-only package, a workflow step and
documentation, and changes nothing under `engine/`, `core/`, `surfaces/` or
`cmd/`. `./cmd/graphi` builds to `4f0e1a20…` before and after, identical to the
digest SW-174 measured at `1e440cf`. The pre-existing divergence from the
candidate (`036be635…`) predates this story and is a known escalated owner
decision; nothing here attempts to resolve it.
