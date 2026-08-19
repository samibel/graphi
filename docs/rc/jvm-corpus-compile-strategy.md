# The JVM corpus compile strategy, and what it actually covers

**SW-173 (W0.e stage 3), measured 2026-08-19 on branch
`claude/kotlin-java-canonical-ga-t3b8km`.**

Per discipline D6 this is a new record; it rewrites nothing and re-points
nothing. It publishes what was measured, states the denominators beside every
figure, and records the negative results rather than omitting them.

---

## 1. The decision, per pin, with its reason

The strategies live as **data** in `corpus/manifest.json` under each entry's
`jvm_compile` block, not in a script — so the choice travels with the pin and
`internal/jvmcorpus` can validate it. Each carries its reason in full; this is
the summary.

| pin | strategy | compiles? | scorable? | backs a corpus-scale figure? |
|---|---|---|---|---|
| guava v33.0.0 `2214c636` | `full-dependency-resolution` | **yes** | **yes** | **yes** |
| kotlinx.serialization v1.6.3 `3efe324b` | `full-dependency-resolution` | **yes** | **not yet** | **no** |
| okio 3.9.1 `8b870e8e` | `not-compiled` | not in the required layout | no | **no** |

**Why full resolution rather than accept-errors, for guava.** This was measured,
not preferred. With no classpath, `javac` over guava's 623 staged sources reports
**6 940 errors and emits ZERO class files** — so the accept-errors denominator
for this pin is 0 and the pin would contribute nothing. (Round 1 corrected both
numbers: this paragraph said "621 sources" and "6 960 errors" while §4.1 said
623. The re-measurement is 623 / 6 940 / **0 class files**. In a document whose
thesis is that a figure without its denominator is unreadable, its own two
source counts have to agree.) Every one of those
errors is a missing annotation package, and the pin's own `pom.xml` names all
four with exact versions. Pinning those four by URL + sha256 gives a **complete**
classpath with no resolver, no version ranges and no transitive discovery, and
the compile then reports **0 errors**.

**The digest-pinned lockfile is the reproducibility mechanism.** A coordinate
plus a repository base is a *resolution*; a URL plus a sha256 is a *pin*. Only
the second survives an upstream re-publish. `jvmcorpus.VerifyArtifacts` re-checks
every digest before an artifact reaches a classpath and **aborts** on a mismatch:
a compile against unexpected bytes is not the compile the strategy describes.

This also answers the review question about **transitively fetched compiler
plugins**. kotlinx.serialization needs one, and it is carried as an artifact with
`"role": "compiler-plugin"` — pinned and verified by exactly the same rule as any
jar, before it can influence a single emitted class.

---

## 2. The pinned toolchain (AC-1, AC-2)

| tool | version | how it is pinned |
|---|---|---|
| `javac` / `javap` | **21.0.6** (measurement host) | `setup-java` Temurin 21; the workflow **prints** `javac -version` so the exact build is recorded beside the figures |
| `kotlinc` | **1.9.24** | exact release URL **plus** `sha256 eb7b68e01029fa67bc8d060ee54c12018f2c60ddc438cf21db14517229aa693b`, verified with `sha256sum -c -` **before** unpacking |
| `kotlin-serialization-compiler-plugin` | **1.9.24** | Maven Central URL + `sha256 05615e38…`, verified in Go before use |

**What this replaced.** `jvm-groundtruth.yml` installed kotlinc from
`releases/latest/download/kotlin-compiler.zip`. The same commit could therefore
compile differently on two days, and evidence from an unpinned compiler is not
evidence. `TestWorkflows_KotlincIsPinned` now fails on a moving reference, on a
missing digest, and on a digest that is recorded but never checked.

**A standing claim that is now false.** The programme has carried "kotlinc has
never compiled in this environment" as a hard constraint. With this pin it
compiles here, and `TestGroundTruth_Kotlin_LiveKotlinc` — written in SW-172 to be
proven "for the FIRST time in CI" — **passes locally**:
`SOUND [by-name], recall 2/2 = 100.0%`.

**One skew, stated rather than hidden.** kotlinx.serialization builds with Kotlin
1.9.22 (`gradle.properties`); this strategy pins 1.9.24, because 1.9.24 is the
last 1.9.x and a compiler plugin must match its compiler exactly. Both are exact
versions, so the compile is reproducible — it is simply not byte-identical to
what the project's own CI produced, and nothing here claims otherwise.

---

## 3. Reproducibility (AC-5) — two independent runs, byte-identical

Each pin is staged, compiled and disassembled **twice from scratch**, into
separate directories. The compared artifact is the sha256 of the **exact bytes
the oracle consumes** — the merged javap capture — not a proxy for them.

| pin | runs | classes | javap shards | capture bytes | capture digest | verdict |
|---|---|---|---|---|---|---|
| guava | 2 | 1 966 | 4 | 12 307 568 | `7f1aa8d6484afe35df81de28e8d30eb45a974711e8d2f4d405443c674f6b5f19` | **identical** |
| kotlinx.serialization | 2 | 255 | 1 | 1 452 140 | `e6d39791efb4203d35110f9952f3feb2cf40abf01a89680ba35df55eef23d599` | **identical** |

Three deliberate choices make this hold, each of which is a reproducibility
defect if omitted:

1. **Sorted inputs.** The staged source list and the javap class list are both
   sorted, so argument order is a function of the sources rather than of a
   directory walk.
2. **A pinned locale for `javac`.** `-J-Duser.language=en -J-Duser.country=US`.
   This is not cosmetic: the sandbox this was developed in emits javac
   diagnostics in **German** ("6940 Fehler"), so anything that counts or
   classifies compiler output is environment-dependent until the locale is
   pinned — a difference that would otherwise have surfaced only as a mysterious
   disagreement between two runners.
3. **Digest-verified inputs**, so "same pin, same toolchain" is a checked claim.

**What could NOT be made reproducible** is in §6.

---

## 4. The denominators (AC-3, AC-4)

No figure below appears without the denominator it was computed against.

### 4.1 Source-file coverage

| pin | JVM sources at the pin | offered by the strategy | staged and compiled | compile errors |
|---|---:|---:|---:|---:|
| guava | 3 204 | 623 | **623** | **0** |
| kotlinx.serialization | 615 | 52 | **52** | **0** |
| okio | 313 | 89 | **0** (see §6) | n/a |

**Read the first column, not the third, when asking what fraction of a pin this
covers.** guava's 623 is `guava/src` plus `futures/failureaccess/src` — the main
module and the in-repo module it depends on. The other 2 581 files are
`guava-testlib`, `guava-tests`, `guava-gwt` and the `android/` flavour, which are
separate Maven modules with their own dependency sets and are **not** covered.
kotlinx.serialization's 52 is `core/` only; the `formats/` modules (json, cbor,
protobuf, hocon) are each a separate resolution question and are **not** covered.

### 4.2 The oracle, signature-aware (AC-6) — guava

Counterexamples **and** denominator, never a bare "0 counterexamples". Binder
confirmed **5 121** calls over the 623 staged files.

| precision | verdict | counterexamples | judged | truth facts | recall | abstained |
|---|---|---:|---:|---:|---:|---:|
| by-name | **SOUND** | **0** | 4 721 | 14 521 | 32.5 % | 315 |
| by-arity | **SOUND** | **0** | 4 082 | 14 565 | 28.0 % | 1 035 |
| by-signature | **SOUND** | **0** | 2 023 | 8 066 | 25.1 % | 3 098 |

**What moved in round 1, and what did not.** The round-1 reviewer reproduced this
table exactly at the first commit, so every difference from that run is stated
here rather than left for a reader to spot. **Only the truth-fact column moved**
(14 505 → 14 521, 14 535 → 14 565, 8 047 → 8 066, taking by-arity recall from
28.1 % to 28.0 %); counterexamples, judged, abstained and every reason below are
identical to the digit, and so are both capture digests. The cause is isolated,
not inferred: re-running the pin with each round-1 code change applied alone
gives **14 505 / 14 535 / 8 047 for the demangling fix (§5, fix 5) — bit-identical
to the first publication — and the new numbers for the anonymous-constructor
narrowing alone** (§5, fix 2's minor-1 correction). javac's local classes
(`Outer$1L`) were being redirected onto their superclass like true anonymous
ones, which COLLAPSED several distinct constructions onto one fact; declining to
fabricate them un-collapses 16 by-name facts. A slightly larger denominator with
the same numerator is a slightly lower recall and a strictly more honest one.

Abstention reasons, so the green is legible rather than assumed:

| reason | by-name | by-arity | by-signature |
|---|---:|---:|---:|
| `binder_param_type_unresolved` | — | — | 2 181 |
| `binder_nested_ctor_synthetic_params` | — | 698 | 698 |
| `bytecode_owner_unresolved` | 271 | 199 | 121 |
| `bytecode_anonymous_ctor_synthetic_params` | — | 92 | 65 |
| `bytecode_caller_not_alignable` | 44 | 41 | 27 |
| `binder_elastic_member` | — | 5 | 5 |
| `binder_signature_unverified` | — | — | 1 |

**This is not a vacuous green.** 4 721 confirmed calls were actually judged at
by-name and 2 023 at by-signature, against a truth set of 14 521 bytecode facts.
Abstention runs at 6.2 % / 20.2 % / 60.5 % of confirmed calls — the by-signature
figure dominated, as SW-172's reviewer predicted, by `binder_param_type_unresolved`
(external parameter types), not by anything this story changed.

---

## 5. Four forges found by corpus scale, and closed

SW-172's reviewer named the incomplete-capture forge as "the one I would put at
the top of SW-173's entry notes". It was closed **first**, before any pin ran
(§7). Running the pins then found four more — all of them the same shape: the
oracle accusing **correct** code. Each was a real forge, each is reproduced by a
test, and closing them took guava from 7 counterexamples to SOUND at all three
precisions.

| # | shape | mechanism | seen as |
|---|---|---|---|
| 1 | **synthetic bridge method** | `javap -c -p -s` does not print `ACC_SYNTHETIC`, so javac's bridge in `AbstractGraph` is indistinguishable from a real override; the owner walk stopped there and named `AbstractGraph.java` where the source declaration is in `AbstractBaseGraph.java` | 3 stop-ships at all 3 precisions |
| 2 | **anonymous-subclass constructor** | `new TypeTable(){…}` constructs `TypeResolver$2`; normalising that owner yields the callee name `"2"` where graphi correctly reports `TypeTable` | 4 stop-ships at by-name |
| 3 | **anonymous-constructor descriptor** | after redirecting the owner to the type the source named, the descriptor still carries the enclosing instance and every captured local — arity 2 where the source wrote 0 | 29 at by-arity, 12 at by-signature |
| 4 | **sourceless multifile facade** | Kotlin's `@JvmMultifileClass` facade has no `SourceFile` attribute; the parser **fabricated** `SerializersKt.java`, a path in no repository | 50 → 27 at by-name (with #5) |
| 5 | **multifile private-function mangling** | kotlinc renames private top-level functions to `name$PartClass` on the declaration **and** every call site, on both the caller and callee side | (counted with #4) |

Fixes 1, 2 and 4 convert the forge into a **named abstention** through the
existing `truthDeclined` rescue rather than into a guess. Fix 3 keeps the fact
decidable at by-name and declines at the two finer levels.

**The whole pre-existing suite — including the 25-case adversarial corpus and all
five JVMSOUND pins — stays green through every one of these changes.**

*Round 1, minor-3 — respectfully declined, with the measurement.* The reviewer
read this count as an off-by-one ("`go test -v` prints 26 subtests"). It prints
26 `=== RUN` lines, of which **one is the parent test**: `--- PASS` at subtest
indentation is **25**, and the `name:` entries in the table are 25. The published
figure was right. Round 1 adds a second corpus of 8 more in
`internal/jvmgroundtruth/demangle_test.go::TestDemangle_LegalDollarNameIsNotRewritten`,
so the do-not-accuse corpora now total 33 cases.

### 5.1 Fix 5 was itself a forge — found in round 1, narrowed, and the residual disclosed

**The first version of this section claimed a safety property that does not
hold**, and it is corrected here rather than quietly rewritten. It said fix 5
"demangles only when the suffix is exactly `$` + the owner class's simple name,
so a legally `$`-containing name is left alone and declines instead". `$` is a
legal Java identifier character (JLS 3.8), so a legal name can BE that suffix:

```java
package a; public class Bar { public int foo$Bar() { return 1; } }
package a; public class App { public int run(Bar b) { return b.foo$Bar(); } }
```

graphi answers `foo$Bar` — what the source wrote — and the rule rewrote the
bytecode fact to `foo` and accused it at by-name. The caller side forged
independently (`run$App` declared in class `App`). Both were **regressions**: the
parent commit scored the same fixtures `matched=1`. Correct Java, correct
binding, fabricated stop-ship — the one thing an oracle may never do.

**The narrowed rule.** Demangling now requires the owner to be a class kotlinc
could have minted the mangling for: its simple name contains `__` (kotlinc names
a multifile part class `<Facade>__<PartFile>`) **and** it was compiled from a
`.kt` file in this capture. Java is now immune whatever it is named — including
a Java class literally called `Foo__Bar` with a method `m$Foo__Bar`, which the
`__` test alone would still have rewritten.

**The residual, measured rather than claimed closed.** Writing this, the tempting
next sentence was "and Kotlin cannot spell such a name". That is false, and it
was killed by trying it: kotlinc 1.9.24 accepts `$` inside a backquoted
identifier, so

```kotlin
package k
class Foo__Bar { fun `x$Foo__Bar`(): Int = 1 }
```

compiles to exactly the shape the rule matches
(`TestDemangle_KotlinCanSpellTheResidual` measures it). It is recorded as
**blind spot #17**, not as a closed case. The exact discriminator exists and is named: a genuine multifile
part class carries `kotlin.Metadata(k=5)` (kind 5 = multi-file class part) where
an ordinary Kotlin class carries `k=1` — printed by `javap -v`, **not** by the
`javap -c -p -s` this capture takes. That is the same capture upgrade the
`ACC_SYNTHETIC` bridge discriminator needs; both are filed as JVMCAP-001.

**The cost of declining, stated.** A legal `$`-containing callee name now
abstains under `bytecode_owner_unresolved` rather than matching, which is one
fewer judged call than the parent produced for that fixture. That is the trade
this programme takes every time: an oracle that declines is worse than one that
answers, and far better than one that accuses.

### 5.2 The bridge guard's price (fix 1), measured

Fix 1 declines whenever the callee's `(name, descriptor)` is declared both on the
receiver's static type and somewhere above it. **The first version of this
document did not publish what that costs, and its code comment understated what
it does** — it said the cost was "recall on genuine overrides called from within
the overriding class". It is not scoped to self-calls: it fires on **any** call
whose static receiver type declares the same `(name, descriptor)` as a supertype,
which is the ordinary way an overridden method is called.

Measured on guava at this commit, the single condition neutered and nothing else
changed (same pin, same capture digest `7f1aa8d6…`):

| by-name | guard ON (shipped) | guard OFF |
|---|---:|---:|
| counterexamples | **0** | **3** |
| judged (matched) | 4 721 | 4 932 |
| truth facts | 14 521 | 15 539 |
| recall | 32.5 % | 31.7 % |
| abstained | 315 | 101 |
| — `bytecode_owner_unresolved` | 271 | 65 |

So the SOUND verdict at by-name is bought with **211 confirmed calls moved from
judged to abstained — 4.1 % of guava's 5 121 confirmed — and 206 extra
`bytecode_owner_unresolved` abstentions, 76 % of that bucket and 65 % of the
whole by-name abstention budget, to close 3 forged stop-ships.** Roughly 70
declines per forge. 1 034 truth facts also leave the recall denominator, which is
why the **ratio moves the wrong way round**: recall *rises* from 31.7 % to 32.5 %
while absolute coverage falls. That is exactly why the absolute counts are
published beside it — a guard that improves the headline percentage by covering
less is the shape of figure this programme exists to refuse.

The trade is still the right one under the never-accuse rule, and it is not
reversed here. But it widens the `truthDeclined` channel that SW-172's round-2
MAJOR-9 showed can swallow a real defect, by the same 206, and that exposure is
now written down instead of being rediscovered.

---

## 6. Negative results (AC-7) — recorded, not omitted

### 6.1 okio — `not-compiled` in the layout the oracle requires

okio's JVM source set **does** compile: 89 sources, 0 errors, 114 classes. It is
still excluded, and the reason is structural rather than incidental.

The oracle keys a call on the callee's **source path**, and javap supplies that
path from the `SourceFile` attribute, which carries only the file *name* plus the
package. okio's Kotlin-multiplatform expect/actual layout gives **18
package-relative paths two claimants each** — `okio/Buffer.kt` exists in both
`commonMain` and `jvmMain`, likewise `BufferedSink`, `BufferedSource`,
`ByteString`, `DeflaterSink`, `FileSystem`, `FileSystem.System`,
`ForwardingSource`, `HashingSink`, `HashingSource`, `InflaterSource`, `Path`,
`RealBufferedSink`, `RealBufferedSource`, `SegmentPool`, `SegmentedByteString`,
`Sink`, `Timeout` — covering **36 of the 89** files.

Keeping one claimant of a pair would attribute the other's calls to the survivor:
a fabricated fact, stop-ship under D5. Excluding all 36 was **measured, not
assumed**: the remaining 53 files fail with **1 158 errors and 0 classes**,
because the expect and actual halves are each other's dependencies.

So okio is compilable-but-unscorable or scorable-but-uncompilable. Neither is
evidence, so it backs no figure.

**What was tried**, in order, all recorded in the manifest:

1. kotlinc over `commonMain`+`jvmMain` in the natural layout — **failed**, 15
   `actual … has no corresponding expected declaration` errors.
2. kotlinc over the full source-set closure read from `okio/build.gradle.kts`
   (`jvmMain` → `zlibMain`, `systemFileSystemMain` → `commonMain`) — **succeeded**,
   89 sources, 0 errors, 114 classes.
3. The same closure staged into the package-relative tree — **failed**, 1 158
   errors, 0 classes.
4. Gradle, the project's own build — **failed at configuration** with
   `Unknown Kotlin JVM target: 21`; okio 3.9.1's Kotlin Gradle plugin predates
   JDK 21. Independently of that, a Gradle route resolves a transitive plugin set
   that cannot be digest-pinned, which is the drift AC-1 forbids.
5. **Not tried**, named so the next attempt does not restart from zero:
   `javap -v`'s `SourceDebugExtension` (carries the full source path and would
   dissolve the collision), and per-source-set output trees keyed separately.
   Both are capture-strategy changes and belong to their own story.

### 6.2 kotlinx.serialization — compiles and is reproducible; **scoring is not yet established**

This pin publishes **no coverage figure and no counterexample count**. What is
established is in §3 and §4.1: the compile and its reproducibility.

The by-name differential reports **27 of 351** confirmed calls as unbacked. The
finer precisions are published too (round 1, minor-6), because "no figure" was
never meant to mean "no result":

| precision | verdict | counterexamples | judged | truth facts | recall | abstained |
|---|---|---:|---:|---:|---:|---:|
| by-name | **SOUNDNESS FAILURE** | **27** | 310 | 990 | 31.3 % | 14 (`bytecode_owner_unresolved`) |
| by-arity | SOUND | 0 | 0 | 1 009 | 0.0 % | 351 (`kotlin_bytecode_shape_unproven`) |
| by-signature | SOUND | 0 | 0 | 670 | 0.0 % | 351 (`kotlin_bytecode_shape_unproven`) |

The two SOUND rows are **vacuously** sound — every confirmed call abstains, so
nothing is judged — and are printed exactly so that cannot be read as a pass. The
by-name row accounts for every confirmed call: 310 matched + 27 unbacked + 14
abstained = 351.

**The classification, done (round 1, MAJOR-2).** The first version of this
section said the 27 were "measured to concentrate in two Kotlin lowerings the
harness does not yet model" and that classifying them was real work. The
reviewer classified them in fifteen minutes and showed the dominant mechanism was
neither of the two named. All 27 were then re-diagnosed here, against the pin's
own source, and the account below is that measurement:

| cluster | count | mechanism | verdict |
|---|---:|---|---|
| **A — value-class (inline-class) name mangling** | **22** | kotlinc mangles any function taking an inline-class parameter to `name-<hash>` and emits a bridge under the plain name. The real body's calls are attributed to the MANGLED caller: `serialize-2TYgG_w`, `deserialize-BwKQO78`, `toBuilder-GBYM_sE`, `writeContent-Coi6ktg`, `computeIfAbsent-gIAlu-s`. graphi answers `serialize` — what `ValueClasses.kt:16` declares | oracle accusing correct code |
| **B — interface member lowered onto the implementing class** | **4** | `AbstractEncoder.kt:80` calls `encodeSerializableValue`, declared on interface `Encoder` at `Encoding.kt:278`. graphi answers `Encoding.kt` — correct. kotlinc emits a real member on `AbstractEncoder` delegating to `Encoder$DefaultImpls`, so the owner walk stops there. The bridge guard (§5.2) cannot catch it: `parseClassHeader` records `extends` only, never `implements` | oracle accusing correct code |
| **C — `internal` visibility mangling, compounded** | **1** | `PrimitiveArraysSerializers.kt:55` declares `internal fun append`; kotlinc appends the module name and the value-class hash, giving `append$main` and `append-7apg3OU$main`. The fact declines, but the decline is keyed on the MANGLED callee name so it never reaches the rescue | oracle accusing correct code |

**Every one of the 27 is the oracle accusing correct code. None is a product
defect.** That is a more serious statement than "shapes the harness does not yet
model", and it means the Kotlin leg of this oracle is 27 forged accusations wide
at corpus scale — which has to be read together with §2's "kotlinc compiles here
and the Kotlin gate passes locally, recall 2/2".

Cluster A is a **second Kotlin name-mangling scheme**, directly analogous to the
`$PartClass` one §5 closed in this same change, and it was named nowhere in the
first version of this record. It is registered as **blind spot #18** and filed on
`projects/graphi/backlog.md` as JVMHARN-001 so it exists as work rather than as
prose inside a JSON string.

**It is still not fixed here, and the pin still publishes no coverage figure.**
Fixing cluster A means demangling a second scheme on the caller side, which would
move this pin's counterexample count — the one number a reviewer has already
reproduced independently — inside a round whose job is to correct the record, not
to change what it measures. The count stays 27, the classification is now
correct, and the fix has a ticket.

### 6.3 What could not be made reproducible

- **Nothing in the two compiling pins.** Both produced byte-identical captures
  across two independent runs.
- **The `javac` patch version is host-supplied.** `setup-java` pins Temurin 21,
  not `21.0.6`. The workflow records the exact build in its log, so a figure can
  always be traced to one, but two runs months apart may use different patch
  builds. Closing this needs a digest-pinned JDK, the same treatment kotlinc got.
- **okio, at all** — §6.1.
- **Kotlin scoring** — §6.2.

### 6.4 The two blind spots this round ADDED to the register (AC-7)

Both are appended to SW-172's AC-7 list, which is the programme's designated
place for them, and both are on `backlog.md` so they exist as work:

- **#17 — the multifile demangle residual** (§5.1). A Kotlin-compiled class whose
  simple name contains `__` and which declares a member named `<x>$<that name>`
  is still rewritten. Reachable — kotlinc accepts `$` in a backquoted identifier,
  and it was measured, not argued. Exact closure: `kotlin.Metadata(k=5)` via
  `javap -v` (**JVMCAP-001**).
- **#18 — Kotlin value-class name mangling** (§6.2, cluster A). `name-<hash>` is
  a second mangling scheme, unmodelled, and it is the largest source of forged
  accusations on the Kotlin half of the corpus: **22 of kotlinx.serialization's
  27** by-name counterexamples (**JVMHARN-001**). Unlike #17 this one is not rare
  — it fires on every function taking an inline-class parameter.

---

## 7. The incomplete-capture forge, closed before the pins ran

SW-172's reviewer demonstrated that an omitted class makes `resolveOwner` read an
intra-repo owner as external, so the truth fact loses its source path and
graphi's correct call is accused **at all three precisions with no abstention and
no counter**. It could not fire in SW-172 because one `javap` exec covered every
class. guava breaks that: **1 966 classes, ~100 KB of class names on one command
line**.

`jvmgroundtruth.NewCapture` is now the only door. It merges shards and **refuses**
unless every class in a required set appears in the merged capture, naming the
missing ones. The required set is enumerated from the **compiler's own output
directory**, never from the capture — a set derived from the capture is satisfied
exactly when it must fail, and `TestCapture_MissingSetIsDerivedFromDisk`
demonstrates that self-satisfying variant passing vacuously so the distinction is
proven rather than asserted.

`TestIncompleteCapture_ForgesWithoutTheGate_RefusedWithIt` is structured
red-without-fix: the whole capture scores sound; the same capture with one class
omitted **forges a violation at every precision**; the gate refuses it; and a
properly sharded capture yields facts identical to the whole one.

**Sharding is always on** (`DefaultShardBytes` = 32 KB, far below any real
`ARG_MAX`). A safety mechanism that engages only on the largest input is one that
is first exercised by the run that discovers the forge in production. guava's
capture took **4 shards**; every run therefore exercises merge-and-verify.

---

## 8. How to re-run this

```bash
# Dispatch-only; it clones three repositories and fetches a pinned classpath.
gh workflow run jvm-corpus.yml

# Or locally, with the pins checked out at their manifest shas under /tmp/pins
# and the digest-pinned artifacts under /tmp/jvmlib:
GRAPHI_JVM_CORPUS_PINS=/tmp/pins \
GRAPHI_JVM_CORPUS_LIB=/tmp/jvmlib \
GRAPHI_JVM_TYPERESOLVE=1 \
  go test ./internal/jvmcorpus/ -run TestPins -count=1 -v -timeout 45m

# The hermetic halves need neither clones nor a toolchain:
go test ./internal/jvmcorpus/ -run 'TestCheckedInManifest|TestValidate|TestWorkflows' -count=1
go test ./internal/jvmgroundtruth/ -run 'TestIncompleteCapture|TestCapture_|TestShardClasses' -count=1
```

## 9. What this change does not touch

`parityreport.CandidateSHA`, the published 19/19 real-repo matrix and every
evidence row are untouched. Nothing here is a product byte: `go list -deps
./cmd/graphi` reaches neither `internal/jvmcorpus` nor `internal/jvmgroundtruth`,
so the shipped binary is structurally incapable of changing — and it was measured
unchanged as well (`128486624372a838…`, identical before and after).
