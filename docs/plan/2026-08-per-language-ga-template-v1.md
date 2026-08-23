# graphi — the per-language GA template v1 (2026-08-19)

> ## AMENDMENT — 2026-08-23 (SW-188): the S4 divergence row's open-defect list is out of date
>
> Per D6 this is **added**, not merged in. Nothing below is rewritten,
> re-pointed or deleted, and no acceptance criterion or ruling in this document
> changes.
>
> **§3 / S4's D-3 row (`:1378`)** reads "JVMSOUND-003/004, JVMHARN-001 and
> blind spots #15/#17 are open". Three of those five are now closed:
>
> - **JVMSOUND-003** and **JVMSOUND-004** closed 2026-08-20 under ADR 0013
>   (D1–D2, D4–D6): `countArgs` no-ops over comment nodes
>   (`engine/jvmresolve/body_java.go:675`), and `callableSig` carries array
>   dimensionality via `arrayDims` (`engine/jvmresolve/hierarchy.go:193`).
> - **JVMHARN-001** closed 2026-08-23 **in the harness**, which is where it
>   lived: `demangleValueClass` in `internal/jvmgroundtruth/groundtruth.go`
>   demangles kotlinc's `<name>-<hash>` on both `ParseJavap` paths, guarded on
>   the plain-name bridge being declared in the same class in the capture.
>   Closed in code with positive regressions; **not** closed by re-measurement —
>   the kotlinx by-name differential re-run needs `kotlinc` and is a CI dispatch
>   that has not run.
>
> **Blind spots #15 and #17 stay open**, and so does the register itself: the
> D-3 row's point — that the oracle is E0 for what it covers and silent on what
> it does not — is unchanged.
>
> **One item the row did not name is now on the record and is open:**
> **JVMHARN-003** — the oracle judges Kotlin at no precision finer than by-name.
> `internal/jvmgroundtruth/binder.go:236` abstains for every Kotlin site before
> a parameter is inspected; 351 of 351 kotlinx comparisons abstain, and
> by-arity and by-signature are vacuously sound at 0 judged of 1 009 and 670
> truth facts. Java is judged at three precisions; Kotlin at one.

> ## AMENDMENT — 2026-08-19 (SW-183): LANGHONEST-001's *scope claim* is narrowed; the defect itself stands
>
> Per D6 this is **added**, not merged in. Nothing below is rewritten, re-pointed
> or deleted, and none of this document's acceptance criteria or rulings change.
> Read §5.2, §7, §10.3–§10.6 and §12 together with this block.
>
> **What stands, unqualified.** LANGHONEST-001 is real. `main.css` containing
> `@import "base.css"` and `readme.md` containing `[base](./base.md)`, with both
> targets tracked and indexed, receive `related_files` summary *"anchor … resolved
> (file) but has no both edges to other files"* at `method: "no_edges"` — a
> statement of absence that is not true of the code. SW-183 re-measured both on
> isolated single-language fixtures and **confirms them** (audit rows 17 and 18).
> §5.3's practice of recording earlier drafts' errors rather than swapping them
> out is what makes this amendment possible at all, and it is why this one is
> written the same way.
>
> **What is NARROWED.** This document's **root measurement** — *"the graph carries
> 9 edges and ZERO file→file edges of any kind"*, generalised into *"the 'both
> edges' relation is empty for every file in every language at every level, which
> is why the outputs are byte-identical and why no level is exempt"* (§5.2/F1),
> and into §7's *"true at THREE of the four levels"* — **does not generalise, and
> the reason is this document's own fixture rule.**
>
> §10.3's Python fixture used `from pkg.util import helper`. That is the **one**
> Python import form that resolves to nothing — not because Python is
> cross-file-incapable, but because of a specific dotted-module binding defect now
> filed as **LINK-004**. Measured across all five canonical forms on isolated
> fixtures through a CLI built from this branch:
>
> | form | file→file `imports` edge? | cross-file `calls` edge? |
> |---|---|---|
> | `import util` | **yes** | **yes** |
> | `from util import helper` | **yes** | **yes** |
> | `from pkg import util` → `util.helper()` | **yes** (two: `pkg/util.py` and `pkg/__init__.py`) | **yes** |
> | `from pkg import helper` | **yes** | **yes** |
> | `from pkg.util import helper` | no | no |
> | `import pkg.util` | no | no |
>
> So **file→file edges are produced in abundance** — 12 of the 16 languages with a
> registered resolver emit them, including Python — and "zero file→file edges of
> any kind" is a property of **that 12-file fixture**, not of the product. It is
> therefore **not** the explanation for why css and markdown answer `no_edges`.
>
> **Consequence for LANGHONEST-001's scope.** The defect is an
> **`intra-file-only` honesty defect**, not a product-wide absence of file→file
> edges. Whether it also binds `cross-file-heuristic` is **reopened, not
> re-decided here**: `app.py` under LINK-004 *does* receive the false sentence, so
> the instance F1 rested on is real — but its cause is a per-language recall
> defect with its own id and its own fix, not the structural absence F1 inferred.
> **The `parse-only` half (`json` → `method: "unresolved"`) is untouched**, and
> the AC-4 escalation in §12 stands exactly as written.
>
> **The reusable lesson is this document's own, turned on itself.** §5.3/§10.6:
> *"a fixture that cannot express the relation under test cannot validate the rule
> about it."* Draft 1 was caught by that rule on YAML. The same rule catches the
> Python fixture — one that hits a latent defect cannot validate a claim about the
> class the defect belongs to. **A single failing instance is a fixture defect
> until the form matrix says otherwise**; SW-183 hit this three times in its own
> first-pass fixtures (ruby's paren-less call, javascript's explicit `.js`
> extension, and this one) and only the third survived.
>
> **§5.5 is unaffected and was executed as written.** All 22 audit rows apply its
> askable/not-askable test, and `json` — which review round 2 noted was missing
> from §5.5's table — is disposed of by the stated test (RFC 8259 defines no
> reference construct; JSON Schema's `$ref` is a downstream vocabulary, the
> identical argument to YAML's `include:`).
>
> Record: [`../rc/capability-audit-2026-08-19.md`](../rc/capability-audit-2026-08-19.md)
> §3 and §5; ruling: [ADR 0012](../adr/0012-capability-levels-graded-on-demonstrated-evidence.md).

> **Status: a specification, not a measurement.** This document says what must
> exist for a language to be graded at the G1–G9 bar, where each asset lives,
> and what an honest abstention costs. It grades no language. Every fact it
> states about the current tree is stated with the path it was read from, and
> every measurement it reports names the command that produced it.

## 0. What this is, and what it is not

Bringing Java and Kotlin toward the GA bar produced an asset set. That set
exists as **one instance**, not as a template: a family parity-class YAML, a
conformance twin table, corpus pins, a hero suite, a ground-truth oracle, perf
wiring, evidence rows. Sixteen more languages need the same set, and copying it
by hand sixteen times produces sixteen subtly different bars — which is how a GA
claim becomes unverifiable.

This document extracts the template **before** Python (SW-181), the TypeScript
family (SW-182) and the Wave 3 languages (SW-184, SW-185) consume it, so the
first consumers validate it rather than the seventh discovering it was wrong.

**It is not:**

- a generator. It specifies what must exist and how each item is checked. A
  generator is a possible later convenience.
- a change to the JVM instance. §9 records divergences between the template and
  what Java/Kotlin actually have; **closing them is separate work with its own
  evidence.**
- a re-decision of the twelve stable operations or of the capability-level
  vocabulary. Both are taken as given.
- a product-byte change. Nothing under `engine/`, `core/`, `surfaces/`,
  `cmd/` or `internal/` is touched by the change that adds this file; the
  measurement is in §11.

**Authority.** The bar is
[`2026-08-graphi-p2-language-ga-program-v1.md`](2026-08-graphi-p2-language-ga-program-v1.md)
§2 (G1–G9) and §3 (the per-level scaling). The disciplines D1–D11 are
[`2026-08-wave0-handoff-v1.md`](2026-08-wave0-handoff-v1.md) §6. Where this
document and either of those disagree, they win and this one is the defect.

---

## 1. The template at a glance

Fourteen slots. `<lang>` is the canonical parser language id from the
`core/parse` vocabulary (`python`, `typescript`, `tsx`, `javascript`, …);
`<fam>` is a **family** id where several languages share one binder and one
table (`jvm` covers java + kotlin; `ts` would cover typescript + tsx +
javascript).

**A family shares S2's package, S4–S11.** Every language in the family still
gets its **own** S1 matrix row, its **own** S3 `Owns` set (one registrant per
language — see S2), and its **own** S12, S13 and S14. Getting that split wrong
is the first way a family goes vacuous: two languages sharing one evidence row
means one of them is graded on the other's evidence.

| Slot | Asset | Path convention | Gate |
|---|---|---|---|
| **S1** | Capability declaration | `docs/coverage-matrix.yaml` → `category: ga-language`, `id: <lang>`, `capability: <level>` | G1 |
| **S2** | Semantic resolver **or** its declared substitute | `engine/<fam>resolve/` (a `typeresolve.Resolver` registrant), or `engine/link/resolve_<lang>.go` | G2 / G2SUB |
| **S3** | Sweep-domain declaration | `Owns(relPath) bool` on the S2 registrant | G2 (ADR 0008 D9) |
| **S4** | Ground-truth oracle | `internal/<fam>groundtruth/` (+ `testdata/`) | G2b |
| **S5** | Oracle input strategy | `internal/<fam>corpus` + the `<fam>_compile` block in `corpus/manifest.json` | G2b |
| **S6** | Hermetic change-class table | `docs/rc/parity-classes-<fam>.yaml` | G3 |
| **S7** | Conformance twin + drift guard | `engine/conformance/<fam>parity_test.go` + `<fam>parity_matrix_test.go` | G3 |
| **S8** | Applicability disposition | the `harness_row` / `deferred_to` / `known_defect` fields of S6 | G3 |
| **S9** | Real-repository parity | `corpus/manifest.json` pins + `cmd/parity` + a published matrix under `docs/rc/` | G4 / G5 |
| **S10** | Hero suite | `corpus/hero-<fam>/*.yaml` + `corpus/fixtures/hero-<fam>/` + `cmd/eval/hero_<fam>_test.go` | G6 |
| **S11** | Perf + budget | `docs/eval/runs/<date>-<runner-class>/` + `bench/lang-budget.md` | G7 |
| **S12** | Honest capability surface | `docs/language-support.md` row + `surfaces/client/capability_test.go` | G8 |
| **S13** | Abstention legibility | `trust_language_skips` / `trust_skip_provenance`, `AbstentionFacts.Registrants` | G8 |
| **S14** | Evidence rows | `docs/rc/evidence-index.yaml` → `GA-LANG-<lang>-G<n>` | G9 |

Note the gate/slot mapping is not 1:1 in either direction. G2 alone needs four
slots (S2, S3, S4, S5); S9 carries both G4 and G5. **Grading by gate id alone
loses slots**, which is exactly the copying error this template exists to
prevent.

---

## 2. The evidence-class ladder

Every slot produces evidence of some class. The classes are ordered, and the
ordering is what makes an abstention costly rather than free.

| Class | Name | What it means |
|---|---|---|
| **E0** | ORACLE-PROVEN | An implementation of the language's own semantics, independent of graphi, adjudicates graphi's output, and the counterexample count is published **with its denominator**. |
| **E1** | HARNESS-PROVEN | A hermetic test asserts the behaviour and was demonstrated **red without the change** (D5). Byte-exact where the assertion is snapshot bytes. |
| **E2** | CORPUS-MEASURED | A rate published with its denominator, from a pinned clone at a pinned sha, reproducible by a recorded recipe. It measures **what the product did**, not whether it was right. |
| **E3** | DECLARED-AND-CHECKED | A claim carried as **data** in a checked-in table and bound to a harness by a bidirectional drift guard. It proves the claim is stated and cannot be silently dropped. It does **not** prove the claim is true. |
| **E4** | DECLARED-ONLY | Prose, or a data row with no guard behind it. **Not evidence.** |

Three rules bind the ladder:

1. **A `GA-LANG-*` row may never read PASS on E4.** `cmd/evidence -check`
   already refuses a PASS without a URI and a sha
   (`internal/evidence/check.go:78-86`); this rule is the semantic half of the
   same discipline, and it is enforced by review, not by a binary.
2. **A gate's class is the MINIMUM over the slots that feed it.** G2 is fed by
   S2, S3, S4 and S5; if S4 abstains, G2 is at S4's degraded class regardless
   of how strong S2 is.
3. **An abstention is legitimate only if it is itself carried at E3 or better.**
   An absence is not an abstention. This is the anti-vacuity rule: it takes the
   change-class applicability disposition (S8) and makes it the shape *every*
   slot's abstention must take.

> **Honesty about the ladder's own enforcement, so it is not read as stronger
> than it is.** E0/E1/E2 are distinguishable by artefact — an oracle run, a
> hermetic test, a pinned-clone measurement — and a reviewer can check which one
> exists. **E3 versus E4 is the only boundary with a binary behind it** (a drift
> guard either exists and binds or does not), and **rules 1–3 themselves are
> review-enforced, not machine-enforced.** Nothing today reads a class off a
> `GA-LANG-*` row and fails a build. That is a real limit, it is why D9's
> mandatory adversarial review is load-bearing here rather than decorative, and
> it is stated because a template that silently over-claims its own enforcement
> would be the same defect it exists to prevent.

---

## 3. The slots, one by one

Each entry states: what the language **must** supply, what it **may legitimately
abstain from**, and what the slot's evidence class **degrades to** when it does.

### S1 — Capability declaration (G1)

**Must supply.** One row in `docs/coverage-matrix.yaml` under
`category: ga-language`, `status: shipped`, carrying `capability:` from the
closed vocabulary in `engine/trust/capability.go`. `CheckGALanguages`
(`internal/coverage/galang.go:88`) binds it **four** ways — the source's own
comment claims two, because it folds the evidence rule into one and omits the
`status` check entirely: (1) the row's declared level must equal the level the
**live registries** derive (`surfaces/client.LanguageCapabilities()`); (2) every
`GA-LANG-<lang>-*` row in the evidence index must read PASS with URI **and** sha
(`:129-131`); (3) a language other than `go` with **no** such rows is rejected
outright (`:133` is the rejection; `:134` is the message it appends — "a GA claim
without evidence rows is vacuous"); and (4) the row must read `status: shipped`
(`:107-110`; `:106` is blank) — planned or partial cannot be GA. A fifth binding
the source comment also omits: **duplicate row ids** are rejected at `:100-103`.
Rule (2) is why the rows and the matrix row must land in a particular order; see
S14.

**May abstain from.** Nothing. Every level is declarable; no level is exempt.

**Class.** E1 — the check is a test-covered binary gate
(`internal/coverage/galang_test.go`), and it fails closed.

> **A trap this slot hides.** The derivation is *registration*-derived, not
> *outcome*-derived. `engine/trust/capability.go:96-128` is a first-match-wins
> ladder over `TypeChecked` / `CrossFileLinked` / `SymbolExtraction`. A resolver
> that registers and proves nothing still reads `cross-file-heuristic` — which
> is the SQL wrinkle named in the programme doc's G1, and which the live read
> shows is plausible for `bash`, `c`, `cpp`, `lua` and `php` too. **SW-183 audits
> this class before any Wave 3 language is graded.** A language must not be
> graded against a level it was auto-derived into.

### S2 — Semantic resolver, or its declared substitute (G2 / G2SUB)

**At `typed-confirmed`.** A third-phase resolver at `engine/<fam>resolve/`,
registered behind the `engine/typeresolve` seam (ADR 0007) from
`engine/semantic/semantic.go`, under the hard constraints of
`engine/typeresolve/doc.go`: pure Go, no network, no external toolchain,
CGo-free, deterministic, kill switch. Plus **G2a** — a NodeId-identity map with
a byte-exact golden cross-test against the real `core/parse` extractor (a
confirmed edge may only attach to a node the extractor actually created) — and
**G2b**, the under-approximation invariant: partial information may only *drop*
edges (skip + count), never mint false ones.

**Below `typed-confirmed`.** G2 is **substituted**, not waived. The language
proves its heuristic resolver's own contract at equal test strength: it is
**never** `confirmed`; it **drops and counts** rather than guessing; it is
deterministic.

**May abstain from.** The `confirmed` tier itself — but only by declaring a
level below `typed-confirmed` at S1. There is no path that keeps the level and
drops the tier.

**Class.** E1 both ways. The substitute is not weaker evidence; it is evidence
about a weaker claim. What must never happen is the substitute being *reported*
as G2 — see §4.

> **The family asymmetry, which a two-language family will get wrong.** In a
> family, `Input` spans **all** the family's languages — a Java file importing a
> Kotlin class must bind, so the input map cannot be per-language — while
> `Subject`, `Owns` and the emitted edges stay **strictly per-language**
> (`engine/jvmresolve/resolver.go:3-13` and `:94-105`). There is **one registrant
> per language, not one per family**, because the kill switch, the capability
> ladder and the trust facts are all keyed on a language. A family that registers
> one resolver for two languages breaks S1, S3 and S13 at once — and, per the
> package comment, "a registrant claiming the sibling language's directories
> would delete the sibling's fresh edges."

### S3 — Sweep-domain declaration (G2, ADR 0008 ruling D9)

**Must supply.** The S2 registrant implements
`Owns(relPath string) bool` — **declared at `engine/typeresolve/registry.go:66`**;
`:40-65` is its doc comment (`:38` is the tail of `Input`'s comment and `:39` is
`Input(relPath string) bool`), and citing that range instead of the declaration was
draft 1's error (§13) — the LANGUAGE half of the `(directory, language)`
stale-confirmed sweep key. The
contract is `Owns ⊇ Subject` per registrant, and **pairwise disjoint across
registrants**.

**May abstain from.** Nothing, and this is the slot most likely to be skipped by
a language that copies the template's *visible* artefacts. It has no YAML, no
published matrix and no evidence row of its own, so it is invisible to a
checklist keyed on files.

> **The gap it guards, stated so a downstream story cannot inherit it silently.**
> A path owned by **no** registrant has confirmed edges that the sweep can never
> mark stale — they become immortal. That state is unreachable today only
> because every tree-sitter walker hardcodes `TierDerived`, and the guard is
> **test-only**: `core/parse/confirmed_tier_guard_test.go`. A new language that
> registers an S2 resolver and forgets `Owns` re-opens it. **Every language
> instantiating this template MUST state its `Owns` set explicitly, or record
> that it registers no S2 resolver at all.**

**Class.** E1 where a resolver is registered; **not applicable** — and it must
say so in words — where none is.

### S4 — Ground-truth oracle (G2b)

**This is the slot that does not generalise.** See §6 for the decision
procedure; the short form is that `internal/jvmgroundtruth` works because
`javac`/`kotlinc` emit class files whose `SourceFile` attribute attributes each
fact back to a source path.

**Must supply — where an oracle is available.** An adjudicator, independent of
graphi, that consumes the same pinned sources and produces a fact set graphi's
output can be diffed against. Published: the counterexample count **with its
denominator**, and every forged stop-ship the oracle itself was found to
manufacture (the JVM instance closed nine such forges before the oracle could be
trusted; that is the expected shape, not an anomaly).

**May legitimately abstain.** A language with no deterministic, CI-installable,
source-path-attributing oracle. Python and the TypeScript family are both in
this class for different reasons (§6).

**Degradation.** E0 → **E2**, and the abstention must be recorded at E3: a named
row in the language's `parity-classes-<fam>.yaml` (or, where the language has no
family table, a named block in its GA record) stating *which oracle was
considered, what specifically defeated it, and what the E2 substitute measures
instead*. The okio entry in `corpus/manifest.json:411` is the model — it records
a **negative** result in full, naming the 18 dual-claimant paths and why keeping
either claimant would fabricate a fact.

> **The consequence for the GA-LANG row.** By rule 2 of §2, a language that
> abstains here publishes G2/G2SUB at **E2**, and its row's `current` field must
> say so. It must not read like Java's.

### S5 — Oracle input strategy (G2b)

**Must supply — where S4 is supplied.** A digest-pinned, reproducible way to
produce the oracle's input, with no resolver, no version ranges, no transitive
discovery. The JVM instance pins its four annotation JARs by URL + sha256 and
never invokes Maven (`corpus/manifest.json:345`), precisely because `mvn compile`
is not reproducible.

**May abstain.** Wherever S4 abstains. It has no independent existence.

**Degradation.** Follows S4.

### S6 — Hermetic change-class table (G3)

**Must supply.** `docs/rc/parity-classes-<fam>.yaml`, in the same constrained
YAML subset the Go table uses: one top-level key (`parity_classes:`), a block
sequence of **flat** maps, every value double-quoted, so both the
`cmd/coverage`-style parser and `yaml.Unmarshal` read it and it diffs one row per
change. Row fields, frozen by the JVM instance:

```
id            frozen wire identifier — snake_case, stable, NEVER renamed
kind          change_class | crash_condition
label         human label (may be renamed freely; the id may not)
verdict       PROVEN | PARTIAL | ABSENT
test_file     the conformance twin's path
test_name     the twin's test function
test_line     the LINE of that function                        <- see the gap below
prd_source    line-cited requirement provenance                <- see the gap below
delta_source  line-cited delta provenance                      <- see the gap below
fixture       "synthetic stub parser" | "production Go parser" | "real pinned repository"
store         MemStore | SQLite | both | none
profile       default | balanced | both
assertion     "snapshot bytes" | "envelope bytes" | "spot query"
harness_row   required | deferred
known_defect  a BARE defect id (no whitespace — prose belongs in `note`), or ""
deferred_to   a story id, or ""       (REQUIRED when harness_row: deferred)
owner         the work package, from a CLOSED set
note          why the row exists and what its witness pins, incl. the
              red-before-the-fix figures
```

Every value is double-quoted (so a `#` inside a scalar is not read as a comment
by the hand-rolled parser), maps are flat, no nesting, no anchors, no multi-line
scalars. The canonical Go struct is `parityRow`
(`engine/conformance/paritymatrix_test.go:50-69`); a family guard reuses it
verbatim, so fields a family omits simply unmarshal empty — **which is how a
family silently drops a guard.** See S7.

`matrix_version` is a **header comment, not a YAML key** — nothing parses it.
It is bumped for a SHAPE change (a row added, removed or re-kinded, or a field
added), never for a verdict change, and the bump carries a comment saying what
changed and why (`docs/rc/parity-classes-jvm.yaml:8-33`).

> **Template hazard in the closed `fixture` vocabulary.** `legalFixtures`
> (`paritymatrix_test.go:71-112`) is `{"synthetic stub parser", "production Go
> parser", "real pinned repository"}` — there is **no value for a non-Go family**.
> The JVM table therefore labels every row `"production Go parser"` while the
> subject under test is the JVM binder, and says so in the row's `note`
> (`parity-classes-jvm.yaml`, `jvm_add_file`). That is a knowing mislabel, and
> **each new family inherits it**: Python's table would claim a Go parser too.
> The template's position is that the vocabulary should gain a
> `"production <lang> parser"` member before a third family lands. That is a
> product change (**TEMPL-P3**, §12), not this story's.

**May abstain from.** Individual change classes — but only through S8, never by
omission.

**Class.** E3 for the table; the rows themselves reach E1 through S7.

### S7 — Conformance twin + drift guard (G3)

**Must supply.** A harness table in `engine/conformance/<fam>parity_test.go` —
`<fam>BaseTree()`, `<fam>ChangeClassTable()`, the driver
`Test<Fam>FullVsIncremental_ByteParity`, the row runner
`run<Fam>ChangeClassRow` — and a guard in `<fam>parity_matrix_test.go`.

The driver runs `backends × profiles × rows` and, per row, asserts in this
order: **(1) the witness against the incremental graph first, unconditionally**
(a failure here is a `VACUOUS ROW`, not a parity failure), then **(2)**
snapshot-byte equality between the incremental and the full pass. Ordering
matters: a row whose witness is vacuous would otherwise pass by comparing two
identically-empty snapshots.

**The guard set. This is the slot where the JVM instance is measurably weaker
than the Go instance, and copying the JVM instance propagates the weakness.**
The Go guard has **seven** `t.Run` directions, all inside one test function; the
JVM guard has **no `t.Run` at all** and spreads its checks over three test
functions — `TestJVMParityMatrix_DriftGuard` (`:43`, carrying MISSING / PHANTOM /
DEFERRED), `TestJVMParityMatrix_KindCountAndOwners` (`:148`, carrying **KIND**
and **OWNER** as two distinct properties in one function), and
`TestJVMParityMatrix_RequiredRowsAreProven` (`:184`). Against the Go set that
leaves **VERDICT, AXIS and VOCABULARY** absent — three, not two. The template
requires all seven:

| Direction | Go guard | Rejects | JVM has it? |
|---|---|---|---|
| **MISSING** | `:206` | a `required` row with no harness row of the same id | ✔ |
| **PHANTOM** | `:225` | a harness row id not declared in the YAML | ✔ |
| **KIND** | `:240` | a `kind` outside the vocabulary; a count other than the **pinned literal** (adding a class means editing the number and saying why) | ✔ |
| **VERDICT** | `:286` | a row whose `verdict` does not match what its harness row actually proves | **✘** |
| **OWNER** | `:408` | a `required` row with an owner outside the closed `requiredRowOwners` set | ✔ |
| **AXIS** | `:460` | a narrowed axis — `parityProfiles()` dropping an entry while rows still publish `profile: "both"`, or `len(parityBackends()) != 2` while rows publish `store: "both"` | **✘** |
| **VOCABULARY** | `:483` | a closed-set field outside its vocabulary; an `ABSENT` row still citing `fixture`/`store`/`profile`/`assertion`; a `required` row with `profile != "both"`; `known_defect` containing whitespace; `store: "none"` with a byte assertion | **✘** |

> **Round-1 correction, and a warning about where the number came from.** Draft 1
> of this document said **six** directions and folded KIND and OWNER into one
> row, which silently dropped **VERDICT**. A family implementing exactly draft
> 1's list would still have been missing a guard — the template reproducing the
> very defect (D-11) it was written to catch. Note also that
> `paritymatrix_test.go:167`'s **own comment says "SIX"** and is stale; the
> number here was counted from the `t.Run` calls, not read from the prose. **Do
> not take a guard count from a comment.**

Plus the Go-only **citation** guard (`changeclass_test.go:1681`): every row's
`test_line` must parse and **that exact line must read
`func <test_name>(t *testing.T) {`**. The JVM table carries no `test_line`, so
this guard cannot exist for it.

**Why AXIS is not optional.** It exists because review round 1 *demonstrated the
hole*: deleting the balanced entry from `parityProfiles()` left the package
**green** while sixteen rows kept publishing "proven under both profiles"
(`paritymatrix_test.go:452-458`). A family table that publishes `profile:
"both"` without an AXIS guard is publishing an unchecked claim — E4 wearing an
E1 label.

> **The discoverability hole, found in round 1 and worse than the missing
> directions.** Both guards read a **hardcoded const path** —
> `parityClassesPath = "../../docs/rc/parity-classes.yaml"`
> (`paritymatrix_test.go:17`) and
> `jvmParityClassesPath = "../../docs/rc/parity-classes-jvm.yaml"`
> (`jvmparity_matrix_test.go:14`). **Nothing in the tree globs
> `docs/rc/parity-classes-*.yaml`.** Therefore a family that ships **no** table
> and **no** guard produces **zero CI signal** — the absence is not a failing
> test, it is silence. S6 and S7 are not gates a language can fail by omission;
> they are gates a language can *skip*. Until TEMPL-P4 lands a glob, "the family
> table exists" is a **review obligation**, and §7's `✔` for S7 at every level is
> a statement about what the template requires, not about what CI checks.
>
> **And it is not only S6/S7 — S10 has the identical hole, which round 1 missed.**
> `cmd/eval/hero_jvm_test.go:35` hardcodes `filepath.Glob(…, "corpus",
> "hero-jvm", "*.yaml")` and **nothing in the tree globs `corpus/hero-*`**, so a
> family shipping neither `corpus/hero-<fam>/` nor `cmd/eval/hero_<fam>_test.go`
> is equally silent. Round 1 disclosed the hole for S6/S7 and left §3/S10 reading
> "Class. E1" — the same defect, undisclosed on the slot a consuming story is
> most likely to reach first. **Three slots, one bug: a hardcoded path where a
> glob belongs.** TEMPL-P4(b) is widened accordingly.

**May abstain from.** Nothing. This is the slot that turns S6 from E3 into E1.
A family table with no drift guard is a list of intentions.

**Class.** E1 with all seven directions **for a table the guard actually
reads**; **E3** for any claim carried only by a field no guard reads; **E4** for
the existence of the table itself, which nothing enumerates.

### S8 — Applicability disposition (G3, AC-6)

**Must supply.** For **every** class in the Go table
(`docs/rc/parity-classes.yaml`), the family table carries an explicit
disposition: `applicable` (a row of its own), `adapted` (a row of its own, whose
`note` states what was adapted and why), or `not_applicable` **with a recorded
reason**. Plus the language-specific classes the family adds.

The JVM instance's dispositions are the worked example: `change_build_tag` is
`not_applicable` because graphi evaluates no build constraints and there is no
honest JVM analog; `rename_package` is `adapted` (directory + package clause +
importers); six JVM-specific classes are added and frozen in the YAML.

**The mechanism, stated so a shrunk set is a reduction and not an absence:** the
disposition lives in the **data**, and the drift guard's PHANTOM/MISSING
directions make a row that exists-in-one-and-not-the-other a build failure. An
`intra-file-only` language's smaller class set is therefore *declared* and
*checked*. A class that is simply missing from both sides is invisible to the
guard — which is why the disposition must be **exhaustive over the Go table**,
not merely internally consistent.

> **Named gap.** No binary today enumerates the Go table and asserts that a
> family table dispositions every one of its classes; the guard only compares a
> family table against its own twin. Until one exists, exhaustiveness is a
> **review obligation** (D9), and this document is where that is written down
> rather than assumed. Building the cross-table check is a product change and is
> **not** this story's — see §12, TEMPL-P2.

**Class.** E3 for the disposition; E1 for every class dispositioned
`applicable`/`adapted` and carried by S7.

### S9 — Real-repository parity, corpus pins and stratification (G4, G5)

**Must supply.** Entries in `corpus/manifest.json` at the **v3 measured
standard**: a release-tag `ref` plus a full 40-char `sha`; a `measured` block
whose counts were taken from a **real clone at the pinned sha** on a recorded
date, never from repository metadata, with the counting `method` written out;
a `tier`; a recorded license; and a language-specific stratification (~8–10
properties analogous to the Go ten). Then `cmd/parity` over those pins,
published in the `parity-matrix-real-repo.md` pattern, with divergences filed as
`PARITY-0xx` and never hidden.

**Publication requires two dispatches** agreeing on both `-verdict-diff` and
`-counts-diff` at exit 0, a clean tree, a candidate byte-match and the runner
class. **Exit 1 is legitimate evidence** (a FAIL row); exit 2 is a harness error.

**May legitimately abstain from.** A *pin*, where no permissively-licensed
repository of the needed shape exists — recorded as a named gap in the
stratification, not as a silent hole. **Not** from the measured standard: a pin
without a `measured` block is not a pin.

> **The trap for Wave 2, stated because SW-181 will walk into it.** The
> manifest already contains `flask`, `sinatra`, `ky` and `express`. They are
> **legacy pins, not v3 pins**: they carry only `name/url/ref/sha/language/
> license/permitted_use/searches/notes` — **no `tier`, no `measured` block, no
> `confirmed_edges`**, and their `sha` is a 12-char prefix rather than the full
> 40. A story that reads "Python already has a pin" and moves on ships a G5 row
> backed by nothing. Bringing a legacy pin to the v3 standard is *the same
> amount of work* as adding a new one — the JVM instance did exactly that for
> guava (`corpus/manifest.json:386`, "the 12-char prefix expanded to the full
> 40-char sha and a measured census added"), and that is the model.

**Class.** E2 for the pins and the matrix.

> **Sequencing constraint, not a slot property.** Every real-repo number is
> bound to a measurement candidate. A number taken before a candidate move is
> wasted work. Any story instantiating S9 must check
> `internal/parityreport.CandidateSHA` against the state of the branch first.

### S10 — Hero suite (G6)

**Must supply.** Three artefacts that must be created together:

1. `corpus/fixtures/hero-<fam>/` — a checked-in, multi-file fixture.
2. `corpus/hero-<fam>/*.yaml` — the scenarios, one directory per family,
   **parallel to** `corpus/hero/` rather than mixed into it.
3. `cmd/eval/hero_<fam>_test.go` — the gate, plus a `tier1-fixture-hero-<fam>`
   entry in `corpus/manifest.json` carrying `path`, `tier: 1`, a **family**
   `language` id (the JVM entry reads `language: "jvm"`, not `java`), a
   `scenario_ref`, and an entry in the hermetic-fixture allowlist
   (`wantPRGate`, `internal/corpus/corpus_test.go:527-535`).

A **separate scenario directory per family is mandatory, not stylistic**:
`corpus/hero/` is frozen at exactly 20 scenarios by `cmd/eval/hero_test.go`, so
adding a language's scenarios there breaks the Go gate. And the tier-1 entry
must carry **no `confirmed_edges`** where the corpus runner drives the *default*
binary with the family's binder off — asserting a confirmed edge there would
contradict the default-off contract (`corpus/manifest.json:554` says so in
words).

The gate's assertions are the template's real content here, and they are
stronger than "twenty scenarios exist". The JVM instance
(`cmd/eval/hero_jvm_test.go:51-97`) asserts: at least twelve tasks — a
**hardcoded `len(heroes) < 12`** floor (`:53`) that is a *redundant belt* on top
of, **not a substitute for**, the derived checks that follow it; then, driving
`for _, op := range coverage.CanonicalStableOps()` (`:61`), **bidirectionally**
— **every** stable operation has a task (`:63-65`) and **no** task exercises a
non-frozen operation (`:67-71`), both derived from the frozen set itself;
every declared failure class has a task; **at least one task declares
a negative (`absent`) anchor**; and **no** task declares `max_latency_ms`,
because budgets are frozen from a reproducible CI run (ADR 0003 U5), not
invented in a scenario file.

**May legitimately abstain from.** Scenarios whose semantics the level cannot
express — an `intra-file-only` language has no cross-file `callers` scenario to
write. The abstention is recorded by the scenario that replaces it: the op still
gets a task, and that task asserts the **honest-empty** behaviour of §5 — or,
where L defines no cross-file construct at all, §5.5's abstention path.

**Class.** **E1 for the CONTENT of a hero suite that exists** — the gate's
assertions above are strong and derived from `CanonicalStableOps()`. **E4 for
the suite's EXISTENCE**, and this is a round-1-of-rebuild correction: the slot
previously read a bare "Class. E1". Nothing globs `corpus/hero-*` (see the
discoverability note under S7), so a family that ships no hero suite at all is
**silent, not red** — the identical hole as S6/S7. By §2 rule 3 an absence is
not an abstention, so **"the hero suite exists" is a review obligation**, and
§7's `✔` for S10 at all four levels states what the template requires, not what
CI checks.

### S11 — Perf + budget (G7)

**Must supply.** Three things, and the JVM instance has only the first:

1. The language's repo in `docs/eval/hero-budgets.json` → `real_repos.selection`
   with real ceilings for `index_wallclock_ms`, `peak_rss_mb`, `db_size_mb` and
   `warm_p95_us.{structural,search,agent_tools}` — each as `{baseline, budget}`.
   A numeric zero anywhere is rejected (`validateNoSilentZeroBudgets`).
2. A `hero_suite` entry naming the family's `scenario_dir`. Today
   `hero_suite.scenario_dir` names **`corpus/hero` only**, so
   `corpus/hero-jvm` has no budget entry at all.
3. Raw run artefacts under `docs/eval/runs/<date>-<runner-class>/`, reproducible
   exactly via `cmd/eval -export-raw` + `-aggregate` (exit 0 = every metric
   reproduced **and** environment documented; 3 = incomplete, deliberately not
   1; 1 = a discrepancy).

Plus the grammar blob inside its `bench/lang-budget.md` allocation and the
whole-binary budget gate. Note `hero-budgets.json` is `historical: true`,
`ratcheting: false` — the ceilings fail closed but may never ratchet, so a
language's numbers are a floor for honesty, not a performance target.

**May abstain from.** Nothing, but the *shape* scales: a language with no S9
pin runs the suites over its hero fixture and says so, which is a smaller claim
at the same class.

**Class.** E2.

### S12 — Honest capability surface (G8)

**Must supply.** The registries report the language at the declared level;
`surfaces/client/capability_test.go` re-derives it from those same registries;
the `docs/language-support.md` row flips; `trust-report --json` and the prose
agree. Where they disagree, **`--json` is right and the table is stale** — the
table says so about itself (`docs/language-support.md:43-48`).

> **The tightening §10.2 measured, applied here rather than only asserted there.**
> **"The trust report names the level" is NOT evidence that the language was
> exercised, and a story must not cite it as such.** The capability list is
> **registry-global, not repository-scoped**: `trust-report --json .capabilities`
> returns all **22** registered languages regardless of what the indexed
> repository contains — measured on a 12-file fixture holding files in five of
> them (§10.2). So this half of S12's obligation is satisfied **by construction**,
> for every language, including one with no file present in any repository. It
> costs a story nothing and proves a story nothing.
>
> What S12 therefore actually demands, and what a reviewer must check instead:
>
> 1. the **`capability_test.go` re-derivation** — this is the load-bearing half,
>    because it binds the printed level to the live registries;
> 2. the `docs/language-support.md` row, which is **explicitly permitted to be
>    stale** by its own text (`:43-48`) and so is E4 on its own; and
> 3. that the surface prints the **matrix's `capability:` value rather than a
>    literal** (§8).
>
> **Class consequence.** The trust-report clause is **E4 — DECLARED-ONLY** taken
> by itself, because nothing about it is specific to the language being graded.
> S12 reaches E1 through (1) alone. A GA-LANG row citing the trust report as its
> G8 evidence is citing a fact that was true before the story started.

**May abstain from.** Nothing.

**Class.** E1 **via the `capability_test.go` re-derivation only** — see the
callout; the trust-report and `language-support.md` halves do not carry it.

### S13 — Abstention legibility (G8)

**Must supply.** The language's skips are **named and counted**, not merely
absent: `trust_language_skips` + `trust_skip_provenance` (ingest schema 6), with
`AbstentionFacts.Registrants` sourced from the recorded pass and surfaced on
`strict_query` and the trust report.

**The subtlety that will bite a downstream story:** availability is a property
of the **generation's provenance**, not of the schema version. A store written
at schema 6 by a pass that recorded no provenance reports abstention
`available: false`. A language instantiating this slot must verify availability
against a *freshly written* generation, not against a schema check.

**May legitimately abstain from.** A language whose S2 resolver skips nothing
because it resolves nothing — but it must then publish `registrants: []` with
the reason, not an empty section.

**Class.** E1 for the mechanism; E2 for any published skip rate.

### S14 — Evidence rows (G9)

**Must supply.** One row per gate in `docs/rc/evidence-index.yaml`, id
`GA-LANG-<lang>-G<n>` — the prefix `GA-LANG-<lang>-` is what
`CheckGALanguages` matches (`internal/coverage/galang.go:122`), so the suffix is
free and the template fixes it. Rows are **born UNKNOWN**, and UNKNOWN counts as
not passed. Required fields per `internal/evidence/evidence.go:73-85`: `id`,
`gate`, **`section`** (a row that cites no plan section is rejected),
`threshold`, `current`, `status`, `evidence_uri`, `sha`, `owner`,
`next_action`, `due`. A PASS without both `evidence_uri` and `sha` is rejected
(`internal/evidence/check.go:78-86`). A STALE row must name what superseded it
in `current`.

**Template addition:** the row's `current` field carries the gate's **evidence
class** from §2, spelled out (`E2 CORPUS-MEASURED — no oracle, see …`). Without
it, two rows that both read PASS assert claims of very different strength and
nothing on the surface distinguishes them.

**May abstain from.** No gate. A gate a language cannot meet gets a row that
reads UNKNOWN or FAIL with the reason, never no row at all — `galang.go:133`
treats "no rows" as vacuous and fails the build, which is the mechanism working.

> **Round-1 correction — draft 1's advice here would have BROKEN THE BUILD, and
> the ordering constraint it missed is the real content of this slot.**
> `internal/coverage/galang.go:129-131` raises a violation for **every**
> `GA-LANG-<lang>-*` row that is not PASS, not merely for a missing set. So the
> two obligations only compose in one order:
>
> 1. **Create the rows first, born UNKNOWN, while the language has NO
>    `ga-language` matrix row.** `CheckGALanguages` iterates matrix rows, so a
>    language absent from the matrix is never inspected and its UNKNOWN rows are
>    invisible to the check. This is where SW-174 lives.
> 2. **Add the matrix row LAST, only once every one of its `GA-LANG-*` rows
>    reads PASS with URI and sha.** This is the flip (WP-J11 / SW-179).
>
> Reversing that order fails `go run ./cmd/coverage -check` immediately. The
> mechanism is right — a matrix row is a GA *claim*, and a claim with an UNKNOWN
> gate under it should fail — but draft 1 stated the obligation without the
> ordering and would have sent a story into a red build.
>
> **The vacuity this slot still admits, stated because the check does not close
> it:** `galang.go:122-135` only counts rows matching the prefix and rejects
> `found == 0`. **One** row named `GA-LANG-<lang>-G1` reading PASS satisfies the
> entire evidence binding; G2–G9 need not exist. "One row per gate" is a
> **template requirement with no binary behind it** — E3 at best, and the cheapest
> conforming artefact is also the least honest one. A reviewer must count rows.

**Class.** E1 for the check's own two bindings; **E3** for "one row per gate",
which nothing enforces. The row's own claim is whatever §2 rule 2 yields.

---

## 4. Substitution is labelled, never renamed (AC-3)

Where a gate is **substituted** rather than met, the substitution is reported
under its own name and never under the original gate's.

**The rule, made GREPPABLE — not mechanical.** Draft 1 titled this "made
mechanical", which over-claimed: `CheckGALanguages` matches on
`strings.HasPrefix(g.ID, "GA-LANG-<lang>-")` and constrains the suffix **not at
all**, so `GA-LANG-python-G2` for a heuristic resolver passes exactly as
`GA-LANG-python-G2SUB` does. The convention buys a reliable `grep`, and a
reviewer who runs it; it buys **no** build failure. Stated plainly so nobody
schedules work on the belief that a binary is watching.

The evidence-row id suffix for a substituted G2 is **`G2SUB`**, never `G2`:

```
GA-LANG-java-G2SUB      ← the heuristic-resolver contract proof
GA-LANG-go-G2           ← would be the real thing (go alone, and go is grandfathered)
```

Both match the prefix `GA-LANG-<lang>-` that `CheckGALanguages` requires, so the
distinction costs nothing mechanically and buys a property worth having:
`grep -c '^  - id: GA-LANG-.*-G2$'` over the index answers "which languages
actually have a type-checker-proven tier?" — today **0** — and cannot be fooled
by a substitute.

> **CORRECTED 2026-08-19 (SW-174). This grep ended in `-G2:` until now, and in
> that form it was DEAD: it returned 0 whether or not a `G2` row existed.**
> Rows in `docs/rc/evidence-index.yaml` are sequence items with no trailing
> colon (`  - id: WP0`), so nothing could ever match `-G2:`. It read 0 for the
> wrong reason, and the sentence above it — *"cannot be fooled by a
> substitute"* — was true only because it could not be satisfied by anything.
> Found by this document's round-3 review (F1) against a copy with a row
> appended. **Re-proven by SW-174 against the real index with real rows in it**,
> which is the state that matters because it is the only state that tests the
> claim actually being made:
>
> ```
> # with a real GA-LANG-go-G2 row temporarily present, alongside the two real
> # GA-LANG-{java,kotlin}-G2SUB rows this story landed:
> grep -c '^  - id: GA-LANG-.*-G2$'  docs/rc/evidence-index.yaml   # -> 1
> grep -c '^  - id: GA-LANG-.*-G2:'  docs/rc/evidence-index.yaml   # -> 0   DEAD
>
> # with the go row removed (the checked-in state):
> grep -c '^  - id: GA-LANG-.*-G2$'  docs/rc/evidence-index.yaml   # -> 0
> grep -c '^  - id: GA-LANG-.*-G2:'  docs/rc/evidence-index.yaml   # -> 0
> ```
>
> `-G2$` separates the two states; `-G2:` cannot. And `-G2$` does **not** match
> `GA-LANG-java-G2SUB` or `GA-LANG-kotlin-G2SUB`, which is the substitute-proof
> property this section claims — now demonstrated against actual `G2SUB` rows
> rather than argued. **The reusable rule, which is round 3's own closing
> lesson: run every published command against the case it is supposed to
> detect, not only against the case where it has nothing to find.**

**Note the `^  - id:` anchor.** The first version of this
paragraph published the *bare* `grep 'GA-LANG-.*-G2:'` and called it a grep that
"cannot be fooled": the bare form matches this very paragraph, so it can never
read 0 while the paragraph exists, and its value **rises with every edit to the
paragraph** — 1 when the defect was found, 2 once the correction was written.
No number is pinned for the bare form here, for the reason §13 gives: any count
derived from prose drifts when the prose is edited, so state the property and
pin only the row-anchored count.
That is the same defect §13 and `evidence-index.yaml:65-72` warn against —
*count ROWS, never mentions* — reproduced, twice, by the commits that fixed the
earlier instances of it, and reproduced here in the paragraph arguing the
convention is worth having. A claim about a grep is only as honest as the
anchoring of the grep. The row's
`gate` field reads `G2SUB — heuristic resolver contract (substitutes G2)`, and
its `threshold` states the substitute's own three obligations (never-confirmed,
drop + count, deterministic) rather than G2's.

**The same rule generalises past G2.** Any slot met by a substitute — an E2
corpus measurement standing in for an E0 oracle at S4, a hero-fixture perf run
standing in for a corpus run at S11 — names the substitute in the row and
carries the degraded class from §2. **A substitution reported under the original
gate's name is a false evidence row, and a false evidence row is the failure this
whole programme exists to prevent.**

---

## 5. The honest-empty invariant, and what actually checks it (AC-4)

The invariant at every level is **no operation lies**: each of the twelve stable
operations returns a well-formed honest result for a language that cannot
express the relationship, and the trust report names the level.

> **ROUND 1 — this section was rewritten after an adversarial probe broke its
> first version, and the first version's error is stated rather than quietly
> replaced.** Draft 1 claimed a single predicate over `confidence.method` could
> police all twelve operations, and blessed `method: "no_edges"` as honest
> *always*. **Both halves were wrong.** The predicate reaches **four** of the
> twelve ops, and `no_edges` is the exact shape the lie takes.
> §5.1-§5.2 below are the corrected version; §5.3 records what was wrong,
> because a template whose own honesty rule was over-claimed is worth a reader's
> suspicion.
>
> **ROUND 1 OF THE REBUILD — round 1's own correction was scoped one level too
> narrow.** It called `no_edges` "the `intra-file-only` lie". It is not confined
> to that level: the byte-identical output is measured on `app.py` at
> **`cross-file-heuristic`**, a level with a registered resolver and an extractor
> that demonstrably computes imports — **the level SW-181 and SW-182 target**.
> §5.2's table now grades each instance separately, §5.3 items 5–6 record round
> 1's errors on the same terms as draft 1's, and §5.5 adds the abstention path
> for a language with no cross-file construct at all.

### 5.1 There is no single predicate, because there is no single result shape

**The twelve stable operations return four structurally different answers.**
Measured through the CLI built from this branch (§10.1's method), against a CSS
symbol in an indexed fixture:

| Shape | Ops | Carries `summary`? | Carries `confidence.method`? |
|---|---|---|---|
| **A — contract envelope** | `explain_symbol`, `change_risk`, `related_files`, `agent_brief` | **yes** | **yes** |
| **B — legacy graph envelope** | `callers`, `callees`, `references`, `definition`, `neighborhood` | no | no |
| **C — analyzer envelope** | `impact` | no | no |
| **D — bespoke** | `search` | no (no `outcome` field at all) | no |

```
$ gr callers 8dcaa2dd711c2526
{"operation":"callers","symbol":"8dcaa2dd711c2526","outcome":"empty","nodes":[],"edges":[]}
$ gr impact 8dcaa2dd711c2526
{"analyzer":"impact","outcome":"empty","symbol":"8dcaa2dd711c2526"}
$ gr search card
{"query":"card","matches":[{"node_id":"8dcaa2dd711c2526",…}]}
```

**Consequence, and it is the most important sentence in this document:** any
honest-empty rule phrased over `confidence.method` is **vacuously satisfied** on
shapes B and C — the field is absent, so `method != "unresolved"` is trivially
true — and **inexpressible** on shape D. Excluding `index` (lifecycle-only),
that is **4 of 11 answering operations covered and 7 not**. A consuming story
that checks the field on the four ops that have it and certifies the other seven
by silence has done nothing.

### 5.2 On shape A, `method` discriminates — but not the way draft 1 said

The real vocabulary, read from the `Confidence(fallbackLabel, fallbackMethod)`
call sites in `engine/` plus the default in
`engine/agenttools/shape/shape.go:102-110`:

`edge_tiers` (the **default**, used whenever the tier distribution is non-empty)
· `no_edges` · `no_inbound_edges` · `no_links` · `seeds_only` · `definition_only`
· `aggregate` · `community_graph` · `unresolved` · `ambiguous` · `unavailable` ·
`empty_graph` · `empty_history` · `no_git_provider` · `no_framework_annotations`
· `recorded_annotations` · `hybrid_ranking` · `integer_signal_model` ·
`brief_sections` · `churn_x_centrality`.

Two of these are where an operation lies, and **only one of them was in draft 1's
table**:

- **`unresolved`** — the anchor could not be found. Its summary is typo advice
  (§10.4). Honest only if the anchor genuinely is not in the graph *and the
  summary says why*.
- **`no_edges` / `no_inbound_edges` / `no_links`** — **the lie, and it is NOT
  confined to `intra-file-only`.** The anchor resolved, and the operation states
  *as fact* that it has no edges. Measured on a single 12-file fixture whose
  ground truth is unambiguous, with the strength of each instance graded rather
  than asserted:

```
app.py        contains  from pkg.util import helper   pkg/util.py tracked+indexed   [python,   cross-file-heuristic]
main.css      contains  @import "base.css";           base.css    tracked+indexed   [css,      intra-file-only]
readme.md     contains  [base](./base.md)             base.md     tracked+indexed   [markdown, intra-file-only]
settings.yaml contains  include: ./other.yaml         other.yaml  tracked+indexed   [yaml,     intra-file-only]

$ graphi related-files app.py
{"outcome":"empty","summary":"anchor \"app.py\" resolved (file) but has no both
 edges to other files", … "method":"no_edges"}
```

**Byte-identical for `main.css`, `readme.md` and `settings.yaml`** — the same
sentence, the same `method`, at two different capability levels.

**The instances are not equally strong, and the difference is what scopes the
defect.** The test is whether **language L's extractor actually computes the
relation** — that is this section's own predicate, stated below — and by it:

| Fixture | Language / level | Does L's extractor compute it? | Verdict |
|---|---|---|---|
| `app.py` | python, `cross-file-heuristic` | **YES** — `pyImports` records `import x` and `from m import n` as `ImportSpec`s (declared at `core/parse/parser_python.go:233`; `:232` is its doc comment), called at `:75` and surfaced as `Imports:` at `:85` | **A lie, decisively.** The strongest instance in the set. |
| `main.css` | css, `intra-file-only` | No — `parser_css.go:94`, "no import system (Imports empty)" — but **CSS the language has `@import`**, so this is a capability graphi declined, not a relation that does not exist | **A lie about a real construct.** Stands. |
| `readme.md` | markdown, `intra-file-only` | No — `parser_markdown.go:94`, same declaration; **Markdown the language has link syntax** | **A lie about a real construct.** Stands. |
| `settings.yaml` | yaml, `intra-file-only` | No — `parser_yaml.go:94` — and **YAML the language defines no `include` directive at all**: `include:` is an ordinary mapping key with a string value, and file inclusion is a *downstream convention* (Ansible, Compose), not YAML semantics | **CONTESTABLE — do not rely on it.** Here "has no edges to other files" is arguably **true**. Kept, relabelled, and analysed in §5.5 rather than swapped out. |

**`app.py` is the instance that settles the scope**, and it is the one draft 1
*and* round 1 both missed: Python is `cross-file-heuristic`, it has a registered
resolver, its extractor demonstrably computes imports — and it returns the same
sentence. **"has no both edges to other files" is false** wherever the extractor
computes the relation. Stating a capability limit as *absence of a relation* is
exactly the "presents an abstention as an answer" failure the invariant forbids.
Draft 1 marked this method honest **always**; round 1 corrected that but scoped
the correction to `intra-file-only`, which is one level too narrow.

> **The measurement that makes the scope unambiguous.** On the 12-file fixture
> the graph carries **9 edges and ZERO file→file edges of any kind** — every
> edge is either `defines` (file→symbol, intra-file) or a single
> `calls: function:app.py → external` stub whose target node has no source path.
> `app.py`'s `from pkg.util import helper` produced **no `imports` edge to
> `pkg/util.py`**. So `related_files`' "both edges" relation is empty for
> **every file, in every language, at every level tested** — which is why the
> output is byte-identical across levels, and why no level is exempt.
> *(Queried directly against the store: `select count(*) from edges e join nodes
> nf on nf.id=e.from_id join nodes nt on nt.id=e.to_id where nf.kind='file' and
> nt.kind='file'` → 0. Recipe in §13.)*

And `edge_tiers`, the default, is not an emptiness marker at all:

```
$ gr change-risk 8dcaa2dd711c2526      # a CSS selector
{"outcome":"found","summary":"risk: low — fan-in 1 (0 calls) from 0 file(s),
 fan-out 0; …","method":"edge_tiers"}
$ gr explain-symbol 8dcaa2dd711c2526
{"outcome":"found","summary":"type main..card defined at main.css:3 — 0 callers,
 0 callees, 0 references …","method":"definition_only"}
```

A **risk verdict** and **three counts**, stated as fact, over relations graphi
never computes for CSS. Both carry `outcome: found`, so no emptiness rule of any
shape sees them.

**The corrected predicate, stated per shape and honestly bounded:**

> **Shape A.** For an in-repo artefact of language L, an operation must either
> (i) resolve the anchor and report a *named, true* reason for emptiness, or
> (ii) state that the relation is **not extracted for L** — never that it is
> absent, and never as typo advice. `no_edges`, `no_inbound_edges` and `no_links`
> satisfy this **only** where L's extractor actually computes that relation;
> where it does not, they are a lie and the summary must name the capability
> limit instead.
>
> **Shapes B, C, D.** The predicate **cannot be expressed today.** These seven
> operations carry no summary and no confidence object, so they can report
> neither a reason nor a capability limit. **For them the invariant is an open
> product question, not a checklist item**, and a consuming story must record it
> as unmet rather than as satisfied.

### 5.3 What the earlier drafts got wrong, and why it is recorded rather than replaced

**Draft 1:**

1. It asserted one predicate over all twelve ops. There are four result shapes;
   the predicate reaches one of them.
2. It listed `no_edges` as honest "always". It is a lie wherever L's extractor
   computes the relation.
3. It omitted `edge_tiers` — the *default* method — and every `found`-outcome
   path, so the `change_risk` and `explain_symbol` cases above were outside its
   scope entirely.
4. Its validation fixture (§10.1) used `settings.yaml` with **no cross-file
   reference**, which is structurally incapable of exposing (2). §10.5's claim
   that every slot "had a determinate answer for Python, YAML and JSON" was an
   artefact of that fixture, and is corrected in §10.6.

**Round 1 — which fixed (1)–(4) and then made two errors of its own, recorded
here on the same terms:**

5. **It scoped the `no_edges` finding to `intra-file-only`, one level too
   narrow**, calling it "the `intra-file-only` lie". `app.py` — Python,
   `cross-file-heuristic`, registered resolver, extractor that demonstrably
   computes imports — returns the byte-identical sentence (§5.2). The cause is
   the same one as (4): round 1 *extended* draft 1's fixture with CSS and
   Markdown but never re-asked the question of the language it had already
   tested, so its new fixture could express the relation and its old one was
   never re-run against the corrected rule.
6. **It kept the YAML instance as equal evidence.** It is not (§5.2's table);
   YAML defines no `include` directive, so that one instance is contestable.
   The finding never depended on it — Python, CSS and Markdown carry it — but
   presenting three instances as equivalent overstated the evidence by one.

The general lesson, which binds the consuming stories: **a fixture that cannot
express the relation under test cannot validate the rule about it** — and its
round-1 corollary, **extending a fixture is not the same as re-running the
corrected rule over the whole of it.** Draft 1 found the `parse-only` defect only
because JSON differs structurally from Python; it missed the `no_edges` defect
because its YAML did not differ from its Python in the one way that mattered;
round 1 then under-scoped the fix because it never pointed the corrected rule
back at Python.

### 5.4 The enforcement gap — named, because it would otherwise propagate

**The hero harness cannot express this predicate today.** In
`engine/scenario/scenario.go:487-503`, a contract result is reduced to
`res.Evidence` = its **items** and its **evidence citations** only. `cr.Summary`
is never captured; `cr.Confidence.Method` is never captured (`Confidence.Top`
is, and it reads `unknown` for both the honest and the dishonest empty). Anchors
match against `res.Evidence` (`:568-576`), which is `[]` in both cases, and
`has_evidence: true` fails in both.

Consequence: the honest empty and the lying empty produce a **byte-identical**
`scenario.Result`. A scenario asserting `expect: {outcome: empty}` — which is
exactly what `corpus/hero/hero-03-search-empty.yaml` and
`corpus/hero-jvm/hjvm-03-search-empty.yaml` assert, and nothing more — passes on
both.

Two further harness facts, both verified, that make the gap worse than the
capture window alone suggests: `finish()` (`scenario.go:528-564`) fails only on
**declared** expectations, so a scenario with an empty `expect:` block always
passes; and `cmd/eval/hero_jvm_test.go:51-97` asserts only *coverage* — that
every stable op has a task, four failure classes appear, one `absent` anchor
exists, no `max_latency_ms` — and **nothing about the content of any individual
task's expectations**. Twelve scenario files asserting `outcome: empty` and
nothing else are green whether the answers are honest or lies.

**Therefore the template requires three things of every consuming story:**

1. **The scenario must anchor on an artefact that EXISTS, and — new in round 1 —
   on one that CARRIES THE RELATION UNDER TEST.** Both shipped honest-empty
   scenarios query `zzz_no_such_symbol_zzz`, so `empty` is honest by
   construction. And a fixture file with no cross-file reference cannot expose
   the `no_edges` lie (§5.3). The honest-empty scenario anchors on a **real,
   indexed, checked-in file of language L that actually contains the reference
   the operation is being asked about** — **unless L has no such construct to
   contain, in which case §5.5's abstention path applies instead.** The rule is
   satisfiable only for languages that *have* a cross-file construct, and two
   that SW-185 grades do not.
2. **The harness must be able to see the difference — on the right shape.**
   **TEMPL-P1 as first written is insufficient and would be actively dangerous.**
   Carrying `summary` and `confidence.method` into `scenario.Result` helps only
   shape A. Landing it as specified would produce a gate that *looks* like it
   covers all twelve operations and in fact covers four — converting an
   acknowledged review obligation into a false green, which is worse than the
   gap. TEMPL-P1 is restated in §12 accordingly.
3. **The seven shape-B/C/D operations are recorded as UNMET, not certified by
   silence.** A consuming story's G6 row must say which operations its
   honest-empty evidence actually covers.

### 5.5 When language L has NO cross-file construct at all — the abstention path

§5.4's rule 1 demands a fixture carrying "the reference the operation is being
asked about". **For some languages there is no such reference to write**, and
the rule as stated is not merely hard for them, it is **unsatisfiable**. This is
not hypothetical: it is the situation of **three of the five** `intra-file-only`
languages — hcl, toml and yaml, per the table below — and **SW-185 grades
exactly those**. *(Corrected 2026-08-19, rebuild round 2: this sentence said
"two", written before hcl was moved to the "no" side, and was not updated with
the table.)* **`json` is not in the table because it is `parse-only`, not
`intra-file-only`** — SW-185 grades it too, and it lands on the **"no"** side by
the same test: JSON Schema's `$ref` is a convention of one consumer layered on
JSON, structurally identical to `include:` being Ansible's layered on YAML. At
`parse-only` the abstention path is necessary but **not sufficient** — §10.4's
anchor-resolution failure bites first, and item 4 below is the assertion it
breaks.

**The test — and it is about the LANGUAGE, not about graphi's parser.** All
five `intra-file-only` parsers declare "no import system (Imports empty) —
absent by design" (`parser_css.go:94`, `parser_hcl.go:94`,
`parser_markdown.go:94`, `parser_toml.go:94`, `parser_yaml.go:94`), plus
`parser_sql.go:77` at `cross-file-heuristic`. That declaration says only what
*graphi* extracts. The question S10 needs answered is different: **does L's own
specification define a construct that names another file?**

| L | Construct | Fixture rule satisfiable? |
|---|---|---|
| **css** | `@import "base.css";` — in the CSS specification | **Yes.** Use it. |
| **markdown** | `[text](./other.md)` — link syntax, in the specification | **Yes.** Use it. |
| **hcl** | none in HCL *the syntax*. `module { source = … }` is **Terraform's schema** layered on HCL, exactly as `include:` is Ansible's layered on YAML | **No** — see the note below |
| **toml** | none. TOML is a value-serialisation format; a path in a TOML value is a string the *reading application* interprets | **No** |
| **yaml** | none. `include:` is an ordinary mapping key; `%YAML`/`%TAG` are directives but name no file; anchors/aliases are intra-*document* | **No** |

> **Why HCL is on the "no" side, and why the distinction is worth the pedantry.**
> The tempting move is to accept `module { source = "./mod" }` as HCL's import.
> It is not — it is a *schema convention of one consumer*, and accepting it is
> precisely the reasoning that made the `settings.yaml` instance contestable
> (§5.2). **The consistent test is: would a conforming implementation of L, with
> no application on top, resolve this to another file?** For css and markdown,
> yes. For hcl, toml and yaml, no. A story that grades HCL against a Terraform
> fixture is grading Terraform, and must say so in the row rather than claim HCL.

**The abstention path, which is what such a language does instead.** It does not
get to skip the honest-empty obligation — that would be the absence-masquerading-
as-abstention failure §2 rule 3 forbids. It **substitutes a different question**,
and labels the substitution per §4:

1. **The claim changes from "the answer is honest" to "the question is not
   askable of L."** The scenario anchors on a real, indexed, checked-in file of
   L that carries the language's *strongest available* intra-file structure, and
   the row states that L defines no cross-file construct, **with a citation to
   the language specification, not to graphi's parser** — because the parser
   comment `"no import system"` is a statement about graphi and would make the
   abstention circular.
2. **The abstention is recorded at E3**, per §2 rule 3: a named row in the
   language's `parity-classes-<fam>.yaml` (or its GA record) stating *which
   construct was looked for, why L has none, and what the substitute asserts*.
   The `<fam>_oracle_abstention` block of §6.3 is the shape to copy.
3. **The G6 row's class degrades E1 → E3** for the cross-file half, and says so
   in `current`. It may **not** read like css's.
4. **What it must still assert at full strength**, because these remain askable:
   that the anchor **resolves** (a file node exists — non-trivial, and exactly
   what `parse-only` fails, §10.4); that the summary does **not** state absence
   of a relation as a fact about the code **in a way that is false of L** — the
   qualifier matters and is not pedantry: for a language that genuinely defines
   no cross-file construct, "has no edges to other files" is *arguably true*
   (§5.2 concedes exactly this for `settings.yaml`), so the unqualified form of
   this assertion would fail yaml on a true statement. What must be asserted is
   that the summary is not false, and that it does not present a **product
   limitation** as a **property of the code** — which is what makes the same
   sentence a lie for css, markdown and python and merely uninformative for
   yaml; and the intra-file operations, which are the whole of what the level
   claims.

> **The trap this closes, stated for SW-185.** Without this path, a story
> grading yaml or toml has two bad options: fabricate a fixture with a
> convention-level `include:` and grade a lie as though it were the language
> (the contestable instance in §5.2), or write no honest-empty scenario at all
> and let the omission read as compliance. **Both produce a green G6 row that
> means nothing.** The abstention path is the third option, and it is cheaper
> than either — but only if the story knows before it builds its fixture that
> its language is on the "no" side of the table above.

---

## 6. What is per-language, and what is JVM-specific

### 6.1 The decision procedure

A slot is **JVM-specific** iff it depends on any of:

- **(a)** an external compiler that is deterministic, CI-installable, and
  pinnable by version **and** digest;
- **(b)** an emitted artefact that attributes a derived fact back to a **source
  path** — for the JVM, the `SourceFile` attribute that `javap` surfaces;
- **(c)** **nominal, declared-type** semantics: the receiver's type is written in
  the source, so a binder can read it without inferring.

Applying it:

| Slot | Depends on | Verdict |
|---|---|---|
| S1, S3, S12, S13, S14 | none of (a)(b)(c) | **per-language** — every language, every level |
| S6, S7, S8 | none | **per-language**; only the row *set* differs |
| S9, S10, S11 | none | **per-language**; only the pins/fixtures differ |
| S2 at `typed-confirmed` | **(c)** | **JVM-specific** in its confirmed form; the substitute is per-language |
| **S4, S5** | **(a) and (b)** | **JVM-specific.** These do not transfer. |

### 6.2 Why S4/S5 do not transfer, per language family

- **Python** fails (a) and (c). There is no compiler emitting a checkable fact
  set; `.pyc` records no resolved call targets. A `mypy`-derived oracle fails
  determinism-by-pinning in practice (its answers move with stub-package
  versions) and covers only annotated code, so its denominator is a biased
  subset — reporting a rate over it as though it were the call-site population
  would be the exact over-claim this programme prevents. **Python abstains from
  S4; G2SUB lands at E2.**
- **TypeScript / TSX / JavaScript** pass (a) — `tsc` is pinnable — and pass (c)
  only under a **declared-annotation-only** regime. They fail (b) in the shape
  that matters: source maps attribute *emitted* positions, not the resolved
  target of a call, so an oracle would have to consume the TypeScript compiler
  API rather than an emitted artefact. That is a real option, and it is a
  bigger one than "copy `internal/jvmgroundtruth`". **The template's demand on
  SW-182 is that it decide and record this explicitly, not that it inherit a
  JVM answer.**
- **`intra-file-only` and `parse-only` languages** have nothing for an oracle to
  adjudicate — no cross-file claim is made. **S4/S5 are `not_applicable` with a
  recorded reason**, which is a legitimate abstention at full strength, not a
  degradation.

### 6.3 The JVM-specific asset that looks generic and is not

`corpus/manifest.json`'s `jvm_compile` block is the trap. It reads like a
generic "how to build this pin" field, and a downstream story could reasonably
copy its shape. It is not generic: its `reason` for guava
(`corpus/manifest.json:345`) is an argument about *Maven's* non-reproducibility
and about a classpath of exactly four annotation JARs, and its negative result
for okio (`:411`) is an argument about the *`SourceFile` attribute's* inability
to disambiguate Kotlin-multiplatform `expect`/`actual` paths. **Both arguments
are (a)- and (b)-shaped and neither has a Python or TypeScript analogue.**
A language whose oracle abstains carries a `<fam>_oracle_abstention` block in
the same position instead, with the same discipline: what was considered, what
defeated it, what the substitute measures.

---

## 7. The per-level bar (AC-2)

`✔` applies as written · `SUB` substituted, labelled per §4 · `↓` reduced,
with the reduction declared and checked · `n/a` not applicable, with a recorded
reason.

| Slot | `typed-confirmed` | `cross-file-heuristic` | `intra-file-only` | `parse-only` |
|---|---|---|---|---|
| S1 declaration | ✔ | ✔ | ✔ | ✔ |
| S2 resolver | ✔ (G2 + G2a + G2b) | **SUB** (G2SUB) | n/a — no cross-file claim | n/a |
| S3 `Owns` | ✔ | ✔ where a resolver registers | n/a (stated) | n/a (stated) |
| S4 oracle | ✔ where (a)+(b) hold, else **SUB→E2** | **SUB→E2** unless (a)+(b) hold | n/a | n/a |
| S5 oracle input | follows S4 | follows S4 | n/a | n/a |
| S6 class table | ✔ | ✔ | **↓** via S8 | **↓** via S8 — parse/ingest determinism classes only |
| S7 twin + guard | ✔ | ✔ | ✔ | ✔ |
| S8 disposition | ✔ | ✔ | ✔ (**this is where the shrink is declared**) | ✔ |
| S9 real-repo | ✔ | ✔ | **↓** pins may be shared with a host repo | **↓** |
| S10 hero | ✔ 12 ops + failure classes | ✔ | **↓** scenarios constrained to intra-file semantics; every op still gets a task | **↓** every op gets a task, all over structural nodes |
| S11 perf | ✔ | ✔ | ✔ | ✔ |
| S12 surface | ✔ | ✔ | ✔ | ✔ |
| S13 abstention | ✔ | ✔ | ✔ (`registrants: []` + reason) | ✔ |
| S14 evidence rows | ✔ G1–G9 | ✔ with G2→G2SUB | ✔ | ✔ |

**Read the `↓` cells with §2 rule 3.** A reduction is only a reduction if it is
declared in data and checked. Nothing in this table permits an absence.

**S7, S12 and S14 never scale.** A `parse-only` language's table is smaller, but
its drift guard, its capability derivation and its evidence rows are exactly as
strong as `typed-confirmed`'s. That is deliberate: those three slots are what
make the smaller claim *checkable*, so weakening them would make the reduced
levels the vacuous ones.

> **A `✔` means "the template requires this". It does NOT mean "the product
> satisfies this."** The distinction matters most at **S10, S12 and S13**, whose
> `✔`s the shipped product does **not** currently earn — §5.2 measures Stable-12
> operations stating a false fact about files that each carry a cross-file
> reference the walker does not extract.
>
> **This is true at THREE of the four levels, not one.** Round 1 annotated only
> the `intra-file-only` column; the identical output is measured on `app.py` at
> **`cross-file-heuristic`** (§5.2), and `parse-only` fails differently and
> worse (§10.4). **LANGHONEST-001 covers `cross-file-heuristic`,
> `intra-file-only` and `parse-only`** — every level below `typed-confirmed`,
> and `typed-confirmed` is untested only because `go` is the sole language
> there. Read the S10/S12/S13 rows of those three columns as *"required, and
> known not to be satisfied today"*.

### 7.1 What actually fails a build — the `Enforced by` column

**Read this before reading a single `✔` above.** The marks in §7 state **what the
template requires**. They state nothing about what CI checks, and the two differ
sharply.

> **Round 1 disclosed this as a four-item exception list, and that structure was
> itself the defect.** An enumerated list of exceptions invites the reader to
> take every *unlisted* `✔` as the enforced remainder. It is not: **nine slots
> are review-only for their own existence and round 1 named four of them.** The
> list is replaced by a column, so the reader cannot make the inference by
> omission. The audit below is per slot, and the question it asks is the only one
> that matters: **for an ARBITRARY NEW LANGUAGE — not for the JVM instance, which
> has hand-written per-family files — what fails a CI job when this property is
> violated?**

| Slot | §7 mark | **Enforced by** | What is NOT caught |
|---|---|---|---|
| **S1** declaration | ✔×4 | **PARTIAL CI** — `go run ./cmd/coverage -check` (`.github/workflows/coverage-matrix.yml:40`) + `go test ./internal/coverage/...` bind level, `status: shipped`, evidence PASS+URI+sha, and duplicate ids | **existence.** `CheckGALanguages` iterates *matrix rows* and `continue`s past every non-`ga-language` row (loop opens `galang.go:96`, `continue` at `:98`), so a shipped language with **no row** is never inspected. §3/S1's "fails closed" is true only for a row that exists. |
| **S2** resolver | ✔ / SUB | **PARTIAL CI** — the declared level is bound to the live derivation | **G2a and G2b.** The NodeId golden and the under-approximation invariant live in `engine/jvmresolve/*_test.go` — a JVM-specific package. **Nothing generic requires either of a new language.** |
| **S3** `Owns` | ✔ | **REAL CI** — `engine/semantic/semantic_test.go:52 TestRegistry_OwnsIsDisjointAndCoversSubject` holds *every registrant at once* and pins SUPERSET + pairwise disjointness. **The one slot whose `✔` is fully earned**, and it is also fail-loud on arrival: the hardcoded `len(resolvers) != 3` means a fourth registrant **breaks the test until someone edits it**. | the **path corpus is "representative rather than exhaustive"** (its own comment). A new language's extensions are not in `paths`, so its `Owns`/`Subject` behaviour over its *own* file types is unchecked until the story adds them. Registration is caught; coverage of the new extensions is not. |
| **S4** oracle | ✔ / SUB | **REVIEW ONLY** | **everything.** Nothing requires an oracle to exist, or requires the abstention to be recorded. |
| **S5** oracle input | follows S4 | **REVIEW ONLY** | everything |
| **S6** class table | ✔ / ↓ | **REVIEW ONLY for existence**; CI for content *once a guard exists* | **existence** — nothing globs `parity-classes-*.yaml` (§3/S7) |
| **S7** twin + guard | ✔×4 | **REVIEW ONLY for existence**; CI for content | **existence**, same hardcoded-const cause |
| **S8** disposition | ✔×4 | **REVIEW ONLY for exhaustiveness** | the guard compares a family table to **its own twin only**; nothing enumerates the Go table (D-5, TEMPL-P2) |
| **S9** real-repo | ✔ / ↓ | **REVIEW ONLY** | nothing requires a pin at all |
| **S10** hero | ✔ / ↓ | **REVIEW ONLY for existence** — **the identical silence hole as S6/S7**: `cmd/eval/hero_jvm_test.go:35` hardcodes `filepath.Glob(…, "corpus", "hero-jvm", "*.yaml")` and **nothing in the tree globs `corpus/hero-*`** | a family shipping **neither** `corpus/hero-<fam>/` **nor** `cmd/eval/hero_<fam>_test.go` produces **zero CI signal — silence, not a red build.** Content is well guarded *once the file exists* (§3/S10). |
| **S11** perf | ✔×4 | **REVIEW ONLY for existence** — `validateNoSilentZeroBudgets` (`cmd/eval/refscenario.go:550`) only checks values **already in the file** | a language with **no budget entry** is not caught. `hero_suite.scenario_dir` names `corpus/hero` alone (`docs/eval/hero-budgets.json:18`), so even `corpus/hero-jvm` has no entry — the JVM instance meets 1 of the 3 requirements (D-8). |
| **S12** surface | ✔×4 | **PARTIAL CI** — `surfaces/client/capability_test.go` re-derives from the registries, and that is the whole of S12's real enforcement | the trust-report clause is **satisfied by construction** (registry-global, §3/S12 + §10.2), and `docs/language-support.md` is **explicitly permitted to be stale** (`:43-48`) |
| **S13** abstention | ✔×4 | **REVIEW ONLY** for a new language | measured: `files_skipped: 0`, `registrants: []` while files carry no node (§10.4) |
| **S14** evidence rows | ✔×4 | **PARTIAL CI** — the URI+sha rule **is** genuinely enforced, by `internal/evidence/evidence_test.go:227 TestCheckedInIndexIsFreshAndHonest`, which loads the **real** index and runs `Check` inside the full `go test` suite | **"one row per gate"** — `galang.go:133` rejects only `found == 0`, so one PASS row satisfies the whole binding (§3/S14) |

**Result, counted: 1 slot fully CI-enforced (S3), 4 partial (S1, S2, S12, S14),
and 9 review-only for their own existence (S4, S5, S6, S7, S8, S9, S10, S11,
S13).** Seven marks read as mechanical and are not: **S1-existence, S2's
G2a/G2b, S4, S5, S9, S10-omission, S11-existence.**

> **A correction about S14's enforcement route, because citing the wrong one
> would send a story to configure a job that does not exist.** `cmd/evidence
> -check` appears in **no workflow** — the only evidence/coverage binary wired
> into CI is `cmd/coverage -check`. What actually enforces the evidence index is
> the **Go test** named above, via the standing full-suite run. Both reach the
> same `Check`; only one of them runs in CI.

> **The three silence holes are one bug in three places.** S6, S7 and S10 all
> fail the same way — a hardcoded path where a glob belongs — and round 1
> disclosed it for S6/S7 while §3/S10 assigned S10 "Class. E1". **S10's `✔` at
> all four levels, and its E1, are claims about a file that CI never looks for.**
> TEMPL-P4(b) is written for `parity-classes-*.yaml`; it must be widened to
> `corpus/hero-*` and `cmd/eval/hero_*_test.go` or S10 keeps the hole after
> S6/S7 lose it.

> **The honest summary of §7.** It is a specification of the bar. **One slot of
> fourteen is fully enforced by CI for a new language**; four more are partial;
> the rest are review obligations wearing a `✔`. That is not a reason to weaken
> the table — it is the reason D9's adversarial review is mandatory rather than
> decorative, and it is written here so a consuming story budgets **review**
> time instead of trusting a green build. **A green CI run is not evidence that
> a language met this bar.**

---

## 8. The naming rule (AC-5)

**No surface says "GA" beside a language without its capability level.**
`Java — GA (cross-file-heuristic)`, never bare "GA". This binds
`readme.md`, `docs/language-support.md`, `docs/stability-tiers.md`,
`docs/coverage-matrix.md`, CLI help text, MCP tool descriptions, the trust
report's prose and release notes. It is D2, ratified, and it is the adopted
mitigation for the objection that GA at differing levels confuses users.

The mechanical half already exists and should be leaned on rather than
duplicated: the level in `docs/coverage-matrix.yaml`'s `capability:` field is
bound by `CheckGALanguages` to the live derivation, so the level a surface
prints cannot drift from the level the code exhibits — **provided the surface
prints the matrix's value rather than a literal.** A story instantiating S12
should check that its surface reads the field.

---

## 9. The template applied to the JVM instance (AC-7)

Applying the template to Java and Kotlin, slot by slot, against the tree at
`f054bb0` (this story's parent; the story's own commits are docs-only, so every
code fact below holds unchanged at HEAD). `D-n` rows are divergences; each is classified as a **template
defect** (the template asked for the wrong thing) or a **JVM-instance gap** (the
template is right and the instance does not yet have it). **Closing the gaps is
not this story** — that is stated in the ticket's own out-of-scope list.

| Slot | Reproduced? | Divergence |
|---|---|---|
| S1 | **No** — `docs/coverage-matrix.yaml` has exactly one `ga-language` row (`go`). | **D-1**, instance gap: expected. Java/Kotlin are pre-flip; the row lands at WP-J11 (SW-179). Not a defect. |
| S2 | Yes — the declared-type binder, default-off behind `GRAPHI_JVM_TYPERESOLVE`. | — |
| S3 | Yes — `Owns` on the registrant, `Owns ⊇ Subject`, pairwise disjoint (declared at `engine/typeresolve/registry.go:66`). | **D-2**, instance gap **carried forward**: the no-registrant path has immortal confirmed edges, unreachable only because walkers hardcode `TierDerived`, guarded test-only. Already recorded in SW-170; restated here because the template's S3 is where a new language would otherwise inherit it. |
| S4 | Yes — `internal/jvmgroundtruth`, three precisions (ByName/ByArity/BySignature). | **D-3**, instance gap: JVMSOUND-003/004, JVMHARN-001 and blind spots #15/#17 are open. The oracle is E0 for what it covers and silent on those. |
| S5 | Yes — `internal/jvmcorpus` + the `jvm_compile` blocks. | **D-4**: okio is pinned but **not compiled** — `corpus/manifest.json:411` records the negative result in full. The template calls this a correct abstention, not a gap. |
| S6 | Yes — `docs/rc/parity-classes-jvm.yaml`, `matrix_version: 3` (comment), 13 required rows, zero deferred. | **D-10**, instance gap: the JVM rows omit `test_line`, `prd_source` and `delta_source`, which the Go rows carry. They unmarshal empty into the shared `parityRow` struct, so nothing complains. |
| S7 | Partly — `jvmparity_test.go` + `jvmparity_matrix_test.go` carry MISSING, PHANTOM, DEFERRED, **KIND**, **OWNER** (two distinct properties, not one) and RequiredRowsAreProven. | **D-11**, instance gap, **the most consequential one found**: the JVM guard is missing **three** of the Go guard's seven directions — **VERDICT, AXIS and VOCABULARY** — and (because of D-10) the citation guard. **Round-1 correction, propagated here in round 1 of the rebuild:** the first version of this row folded KIND and OWNER into one item and named only AXIS and VOCABULARY, so it under-counted the gap by one and reproduced in §9 the exact defect §3/S7 had already corrected. The 13 JVM rows all publish `profile: "both"` and `store: "both"` — claims that on the Go side are checked and here are not. |
| S8 | Yes — Go classes mapped/adapted/`not_applicable` with reasons; six JVM classes added. | **D-5**, **template defect, corrected in this document**: the template's first draft said the disposition was machine-checked *exhaustively*. It is not — the guard compares a family table to its own twin only, and nothing enumerates the Go table. §3/S8 now states this and §12 carries it as TEMPL-P2. |
| S9 | Partly — guava and okio at the v3 measured standard. | **D-6**, instance gap: WP-J7 (SW-176) has not run; there is no published JVM real-repo matrix. Expected by sequencing. |
| S10 | Yes — 16 scenarios in `corpus/hero-jvm/`, `corpus/fixtures/hero-jvm/`, `cmd/eval/hero_jvm_test.go`. | **D-7**, instance gap: `hjvm-03-search-empty` anchors on `zzz_no_such_symbol_zzz` and asserts `outcome: empty` alone. Per §5 that is the easy case; the JVM instance has **no** scenario for the hard one. |
| S11 | Partly — `guava` is in `hero-budgets.json` `real_repos.selection` with real ceilings, and in the `eval-full.yml` matrix. | **D-8**, instance gap: no `docs/eval/runs/` directory for a JVM corpus, and `hero_suite.scenario_dir` names `corpus/hero` only, so `corpus/hero-jvm` has no budget entry. G7 is SW-177, post-candidate-move by design. |
| S12 | Partly — `docs/language-support.md` carries the Java/Kotlin row at `cross-file-heuristic`; the derivation is live. | — |
| S13 | Yes — `trust_language_skips` / `trust_skip_provenance`, `AbstentionFacts.Registrants`. | — |
| S14 | **Partly, since 2026-08-19** — `docs/rc/evidence-index.yaml` carried **zero** `GA-LANG-*` rows when this register was written; SW-174 landed **18** (java and kotlin, G1–G9 with G2→G2SUB), all UNKNOWN. **Go still has none.** | **D-9**, instance gap, half closed: SW-174 creates the rows, born UNKNOWN — **and SW-174 is step 1 of the ordering constraint in §3/S14, not an independent task.** Rows first, *while the language still has no `ga-language` matrix row*; the matrix row last (SW-179), only once all of them read PASS. `galang.go:129-131` violates on every non-PASS row, so the reverse order is a red build. Stating "born UNKNOWN" without the ordering is the advice §3/S14 records as build-breaking. **New — D-9b, found by SW-174: the ordering constraint has no answer for `go`,** which already has a `ga-language` matrix row and therefore cannot do step 1 at all. Measured: nine `GA-LANG-go-*` rows added → `cmd/coverage -check` → `ga-language check FAILED — 9 violation(s)`, exit 1. Removed again, and the resolution (produce the evidence, or withdraw go's matrix row until it exists) escalated to the owner as SW-174 AC-6. See `evidence-index.yaml`'s rule 4. |
| S6 (fixture label) | Yes, but under a knowingly wrong value. | **D-12**, instance gap: all 13 JVM rows read `fixture: "production Go parser"` because `legalFixtures` has no non-Go member (§3/S6). Documented in the rows' own `note`; it becomes wrong at the third family. TEMPL-P3. |

**Reading of AC-7.** The template reproduces the JVM asset set for every slot
the instance has reached, and twelve divergences fell out. They split cleanly:

- **One template defect, corrected in this document — D-5.** The first draft
  claimed the applicability disposition was exhaustively machine-checked. It is
  not; §3/S8 now says so and §12 carries TEMPL-P2.
- **Four sequencing gaps the programme's own order predicts** — D-1, D-6, D-8,
  D-9. Not defects; the stories that close them are scheduled.
- **Three open instance gaps carried forward with their existing ids** — D-2
  (the `Owns` no-registrant gap), D-3 (JVMSOUND-003/004, JVMHARN-001, blind
  spots #15/#17), D-7 (the honest-empty scenario anchors on a nonexistent term).
- **One correct abstention** — D-4 (okio not compiled, negative result published
  in full).
- **Three newly found instance gaps in the guard layer** — D-10, D-11, D-12.

**D-11 is the finding that most justifies extracting this template at all.** The
JVM family table publishes `profile: "both"` on all thirteen rows, and no guard
checks it — the AXIS direction that exists on the Go side, and that was written
*because a demonstrated hole let sixteen rows publish an unchecked axis claim*,
was not carried over. Four downstream stories were about to copy the JVM
instance. **Copying it would have propagated a missing guard into four more
family tables**, each publishing the same unchecked claim, and each doing so
under a `GA-LANG-<lang>-G3` row reading PASS. This is precisely the "sixteen
subtly different bars" failure the ticket names, caught before the first copy.

No divergence was found that the template's slot structure could not name —
which is the property AC-7 is testing.

---

## 10. Instantiating the template on a non-JVM language (validation dry run)

A template validated only against the instance it was extracted from is
unproven. This section instantiates it on languages that share **none** of the
JVM's (a)/(b)/(c) properties.

**This is a validation dry run, not a grading.** It creates no evidence row, no
matrix row and no GA claim, and it does not do SW-181's, SW-183's or SW-185's
work. It exists to find out whether the template's slots are answerable off the
JVM — and it found something.

### 10.1 Method

Built from the branch (the installed `graphi` is stale):

```
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o <bin> ./cmd/graphi
```

Fixture — four tracked files in a fresh git repo, later extended by three more:

```
app.py         from pkg.util import helper / def main(): return helper(41)
pkg/util.py    def helper(x) / def unused_helper(y)
settings.yaml  name: demo / listen: 8080 / nested: {key: value}
config.json    {"name":"demo","version":1,"deps":{"a":"1.0"}}
empty.py       a single comment line, no symbols
notes.md       one line of prose, no headings
other.json     {"k":1}
```

`graphi index`, then `graphi rebuild` twice. All outputs below are verbatim.

### 10.2 S1 — the capability declaration is answerable, and global

`graphi trust-report --json | jq .capabilities` returns **22 languages**, matching
the spec's measured inventory: `go` at `typed-confirmed`; 15 at
`cross-file-heuristic` (including `python`, `typescript`, `tsx`, `javascript`,
`java`, `kotlin`, `sql`); 5 at `intra-file-only` (`css`, `hcl`, `markdown`,
`toml`, `yaml`); `json` at `parse-only`.

**Template finding.** The list is **registry-global**, not repository-scoped: it
names all 22 regardless of what the indexed repo contains. S12's obligation
("the trust report names the level") is therefore satisfied by construction for
every language — including one with no files present. **A story must not cite
"the trust report names the level" as evidence that its language was
exercised.**

**The tightening is applied in `§3/S12`, not merely claimed here** — see the
callout there, which downgrades the trust-report clause to **E4 taken alone** and
re-points S12's E1 onto the `capability_test.go` re-derivation. *(Round 1 of the
rebuild: the previous version of this paragraph ended "The template's S12 wording
is tightened accordingly" and §3/S12 was in fact unchanged — a claimed-but-unapplied
edit, and an instance of the same propagation failure this section is about. A
story working slot-by-slot through §3, as this document instructs, would never
have reached this paragraph.)*

### 10.3 S10 — the honest-empty invariant, at three levels

```
$ graphi related-files app.py
{"outcome":"empty","summary":"anchor \"app.py\" resolved (file) but has no both
 edges to other files", … "confidence":{"top":"unknown","method":"no_edges"} …}

$ graphi related-files settings.yaml            # intra-file-only
{"outcome":"empty","summary":"anchor \"settings.yaml\" resolved (file) but has
 no both edges to other files", … "method":"no_edges" …}

$ graphi related-files config.json              # parse-only
{"outcome":"empty","summary":"related_files: no symbol or file matched
 \"config.json\"; try `search` for discovery, a repo-relative path, a qualified
 name, or a 16-hex node id", … "method":"unresolved" …}
```

`explain-symbol config.json` and `change-risk config.json` — both Stable-12 —
return the same `unresolved` shape. Byte-identical across two consecutive
`graphi rebuild` runs **and under all three index profiles** (`-profile fast`,
`balanced`, `deep`), so this is a property of the product, not a flake and not a
profile artefact. *(That byte-identity claim is scoped to these `related-files`
outputs. It does **not** extend to `trust-report`, whose `graph_generation.id`
is fresh per pass; every substantive field of `trust-report` is stable, the id
is not.)*

> **RECONCILIATION WITH §5.2 — read this before drawing any conclusion from the
> block above.** An earlier version of this section presented the first two
> outputs as **"the honest shape"**, contrasted against `config.json`'s
> `unresolved`. **That labelling was wrong and is withdrawn.** §5.2 classifies
> the byte-identical output as a **lie** wherever L's extractor computes the
> relation, and §5.2's predicate is the one that governs — the two sections
> cannot both stand, and §5.2 settles it:
>
> - **`app.py` is the clearest case against the old label.** Python is
>   `cross-file-heuristic`, it has a registered resolver, and `pyImports`
>   (`core/parse/parser_python.go:233`) **does** compute imports. The file
>   contains `from pkg.util import helper` and `pkg/util.py` is tracked and
>   indexed. By §5.2's own predicate the sentence *"has no both edges to other
>   files"* is **false** here — this is a lie, not an honest shape.
> - **`settings.yaml` is the one instance where the old label may survive**, and
>   only because YAML defines no `include` directive (§5.2's table, §5.5). Even
>   there, "honest" is an accident of the language, not a property of the
>   product: the identical sentence is returned either way, so the output
>   carries no information about which case it is in.
>
> **What the contrast in the block above actually shows** is therefore *two
> different failures*, not one failure and one success: at `parse-only` the
> operation says **the anchor is unknown**; at `intra-file-only` **and at
> `cross-file-heuristic`** it says **the relation is absent**. The second is the
> more dangerous because `outcome` and `method` both read as a successful
> answer. Both are LANGHONEST-001.

**It is not a zero-symbol rule.** `empty.py` (a comment only) and `notes.md`
(prose only) both resolve with `method: "no_edges"`. Only the two `.json` files
do not. The mechanism: `core/parse/mapping.go:106` mints the `file` node inside
`MapTreeSitter`, **before** the node-spec loop, so every symbol-extracting
parser yields a file node even for zero symbols; `core/parse/parser_json.go:53-62`
returns `Meta` and `Root` only and calls no extractor at all —
`ExtractsSymbols() == false` (`parser_json.go:29`), the sole `false` among the **22**
parsers `RegisterDefaults` installs (23 with `zig`, CGO-only under `graphi-broad`). `engine/ingest` mints no file node of its own
(`engine/ingest/parsefile.go:227-277`), so a `parse-only` file has **no node of
any kind**.

**This is documented as intended design, and it is not a new defect.**
`docs/language-support.md:149-151` states it exactly: a file with a parse-only
parser "is never a committed `file` node in the first place, so it was never an
`imports` target and loses nothing here";
`docs/adr/0011-imports-edge-targets-package-source-files.md:35` repeats it,
citing `JSONParser.ExtractsSymbols() == false`. It carries no defect id and is
disclosed in the language docs — correctly, for the scope those documents
address, which is *`imports`-edge targeting*.

### 10.4 The finding

**What is new is not the behaviour; it is that the GA bar's own invariant
contradicts it.** Programme doc §3 and this ticket's AC-4 require that *each of
the twelve stable operations returns a well-formed honest result for a language
that cannot express the relationship*. At `parse-only` the shipped product
cannot meet that as written: three Stable-12 operations answer about a tracked,
indexed, successfully parsed file with a sentence that says the **anchor** was
wrong. `graphi parse config.json` succeeds ("parsed config.json as json, 53
bytes"), so the file is not unparseable — it is unrepresented.

Two corroborating readings, both from the same run:

- `repo-overview` reports **"5 files"** for the 7-file fixture and lists
  languages `Python, YAML, Markdown` — JSON is absent. A count, stated as fact,
  that omits two tracked files. (Labs, not Stable-12 — recorded for completeness.)
- `trust-report --json` reports `coverage.files_discovered: 7`,
  `files_indexed: 7`, `files_skipped: 0`, and `abstention.registrants: []`. The
  **abstention surface itself** — S13, the slot whose whole job is legibility —
  reports nothing skipped, while two of the seven files carry no node.

**Classified.** This is not a soundness defect: no wrong edge is emitted, so D5
does not fire. It is an **honesty defect in the abstention surface**, and it is
filed as **`LANGHONEST-001`** so that **SW-181, SW-182**, SW-183 and SW-185
inherit an id rather than rediscover a symptom. *(SW-181/SW-182 were added to
that list in rebuild round 1, when the scope was widened to their level — see
the table below.)*

**Scope of LANGHONEST-001 — widened twice, and the second widening is the one
that reaches the nearest consuming stories.** The id covers **two distinct
failure modes at every capability level below `typed-confirmed`**:

| Level | Failure mode | Measured on |
|---|---|---|
| `cross-file-heuristic` | says **the relation is absent** (`no_edges`) about a file whose extractor *does* compute that relation | `app.py` (§5.2) — **added in round 1 of the rebuild** |
| `intra-file-only` | says **the relation is absent** (`no_edges`) about a file carrying an unextracted reference | `main.css`, `readme.md` (§5.2) — added in round 1 |
| `parse-only` | says **the anchor is unknown** (`unresolved`) about a tracked, indexed, successfully parsed file | `config.json` (this section) — the original finding |

**`cross-file-heuristic` is the level SW-181 (Python) and SW-182 (TypeScript
family) target**, which is why the under-scoping mattered: a story reading a
version of this document that exempted its own level would write the
honest-empty G6 row on the strength of `no_edges`, and produce exactly the
false green the invariant exists to prevent — one level up from where anyone was
looking for it.

**Disclosure (D8), analysed rather than assumed.** D8 binds "an open defect in a
GA operation". `related_files` *is* a GA operation, but GA today is `go` alone,
and this defect is unreachable on Go. The disclosure obligation is therefore
**not triggered today** — and it *is* triggered the moment a `parse-only`
language gets a `ga-language` row. Recording it now, before that row exists, is
the point. Note the standing tension the Wave 0 handoff already recorded: the
doctor `known-defects` check is compiled in, so disclosing there is a
product-byte change — which AC-8 forbids this story from making.

### 10.5 What the dry run proves about the template

1. **The slots are answerable off the JVM.** Every one of S1, S6–S14 had a
   determinate answer for Python, YAML and JSON. Only S4/S5 needed the
   abstention path — which is the split §6 predicts.
   > **This point is too strong as written — see §10.6, which corrects it.** It
   > is left standing rather than rewritten because the *reason* it was too
   > strong is the reusable finding. Do not read point 1 without §10.6.
2. **The template caught a real defect that the JVM instance could not have
   surfaced.** Java and Kotlin are `cross-file-heuristic`; nothing in the JVM
   asset set exercises `parse-only`. A template extracted from the JVM instance
   and validated only against it would have shipped an unsatisfiable AC-4 into
   SW-185.
3. **The anti-vacuity rule earns its place.** All three `.json` responses carry
   `"outcome":"empty"`. A hero scenario copied from `hero-03`/`hjvm-03` would be
   **green** on every one of them.

### 10.6 What the dry run MISSED, found by the round-1 adversarial probe

Point 1 above is **too strong, and its own fixture is why.** §10.1's
`settings.yaml` reads `name: demo / listen: 8080 / nested: {key: value}` — an
`intra-file-only` file with **no cross-file reference in it**. A fixture that
cannot express the relation under test cannot validate the rule about it, so the
dry run concluded YAML was fine when it had never been asked the question that
matters.

Re-run on a fixture that does ask it — `main.css` containing
`@import "base.css";`, `readme.md` containing `[base](./base.md)`,
`settings.yaml` containing `include: ./other.yaml`, every target tracked and
indexed — `related_files` answers `"anchor \"main.css\" resolved (file) but has
no both edges to other files"` with `method: "no_edges"` for all three. **That
statement is false**, and draft 1's §5.1 marked `no_edges` honest *always*.

Three consequences, all folded into the sections above rather than left here:

- **§5 was rewritten** (§5.1–§5.4). The predicate reaches 4 of 12 operations,
  not all 12, because there are four result shapes and seven ops carry neither a
  summary nor a `confidence.method`.
- **LANGHONEST-001 is widened** — in round 1 from `parse-only` to `parse-only` +
  `intra-file-only`, and **in round 1 of the rebuild to `cross-file-heuristic`
  as well**, i.e. to **every level below `typed-confirmed`**. At `parse-only` an
  operation says the anchor is unknown; at the other two it says the relation is
  absent. The second is the more dangerous, because it reads as a confident
  answer. **Round 1's scoping was the defect** — it stopped at `intra-file-only`
  because it never re-ran the corrected rule against the Python file that was in
  its fixture the whole time (§5.3 item 5). `typed-confirmed` is unannotated only
  because `go` is the sole language at that level, not because it was cleared.
- **§7's `✔` marks for S10/S12/S13 are annotated** as "required, not currently
  satisfied" — across **all three** affected columns, not `intra-file-only` alone.

**Recorded as a finding about method, not just about YAML:** draft 1 found the
`parse-only` defect only because JSON differs *structurally* from Python. It
missed the `intra-file-only` defect because its YAML did not differ from its
Python in the one dimension under test. Both consuming waves should read that as
a fixture-design rule, not as a YAML anecdote.

---

## 11. Product-byte measurement (AC-8)

`./cmd/graphi` built with `-trimpath -buildvcs=false`, `CGO_ENABLED=0`, from the
working tree before and after this change:

| tree | sha256 |
|---|---|
| `f054bb0`, clean | `87762557ef3400c71ed7f0d275ca9a16ebbd0ba84aa2083462c6b83936647cb7` |
| + this document, + the three cross-references (`083e8f3`) | `87762557ef3400c71ed7f0d275ca9a16ebbd0ba84aa2083462c6b83936647cb7` |
| `b19a3f7` (SW-183 landed on top), clean | `4f0e1a20689e3410ec9226511cb8e03b43fa139980427fa90ea265cf1dfa88b6` |
| + this story's rebuild-round-2 docs edits | `4f0e1a20689e3410ec9226511cb8e03b43fa139980427fa90ea265cf1dfa88b6` |

**Byte-identical — measured once per round, against that round's own parent, and
NOT carried forward as a constant.** The digest changed between rounds because
**SW-183 landed product changes** (`engine/link/resolve_bash.go`,
`internal/doctor/checks.go`) between this story's round 1 and round 2. AC-8 is a
statement that *this story* moves no product byte, which the pairing of each row
with its own parent shows; it was never a claim that the repository's binary is
frozen at `87762557…`. Re-measured 2026-08-19: quoting a digest measured against
a stale parent is how a byte-identity claim silently becomes false. The change touches four paths — this document, plus
cross-reference comments in `docs/plan/2026-08-graphi-p2-language-ga-program-v1.md`,
`docs/rc/parity-classes-jvm.yaml` and `docs/rc/evidence-index.yaml` — and
nothing under `engine/`, `core/`, `surfaces/`, `cmd/` or `internal/`. The two
YAML files are read from disk by test-time and gate-time code, never `go:embed`ed
(verified: no `//go:embed` in the tree names `docs/`), so a comment in them
reaches no binary.

**Why that is worth measuring rather than asserting.** The Wave 0 handoff
records that a *pure comment in a compiled source file* is a product byte under
this gate, because line positions reach the binary through debug metadata and
`-trimpath` does not neutralise them. The rule "docs-only means no product
bytes" is therefore not a rule about intent — it is a rule about which files
the compiler reads, and it has to be checked each time.

**Stated so it is not mistaken for a publishability claim:** the product tree at
`f054bb0` already differs from the measurement candidate
`3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`
(`internal/parityreport/report.go:87`) — the tree at `f054bb0` builds to
`87762557…`, the candidate to `036be635…`. A parity dispatch run here is **not publishable**, a
candidate move is already owed, and that is a known escalated owner decision
predating this story. This document neither causes it nor resolves it.

---

## 12. Prerequisites and escalations this template raises

| id | What | Why it is not done here |
|---|---|---|
| **TEMPL-P1** *(restated in round 1 — the first version was dangerous)* | **Two parts, and part (b) is the load-bearing one.** **(a)** `scenario.Result` carries the contract `summary` and `confidence.method` (or the schema gains `expect.confidence_method`). **(b)** The seven shape-B/C/D operations — `callers`, `callees`, `references`, `definition`, `neighborhood`, `impact`, `search` — gain *some* way to express "this relation is not extracted for language L", because today they carry neither a summary nor a confidence object and can express nothing. | A product-byte change; AC-8 forbids it here. **Do NOT land (a) alone:** it produces a gate that appears to cover twelve operations and covers four, converting a known review obligation into a false green — strictly worse than the present gap. **Owner: SW-181**, as the first consuming story in programme order and therefore the first to need this — pinned to a ticket in rebuild round 1 because "whichever of SW-181/SW-185 first needs it" named no story and so scheduled nothing. If SW-181 lands without it, the obligation moves to SW-182, then SW-185, where it becomes blocking. |
| **TEMPL-P2** | A check that enumerates `docs/rc/parity-classes.yaml` and asserts every class is dispositioned in each family table. Today the guard only compares a family table to its own twin (D-5). | A product change, and it must not run before the family-table set stabilises. |
| **TEMPL-P3** | `legalFixtures` gains a `"production <lang> parser"` member so a family table stops labelling its rows `"production Go parser"` (D-12). | A product change; harmless but not free — every existing JVM row's value changes with it. Should land before a third family table. |
| **TEMPL-P4** *(widened in round 1)* | Three parts. **(a)** The **AXIS**, **VOCABULARY** and **VERDICT** directions plus the `test_line` citation guard are extended to every `parity-classes-<fam>.yaml` (D-10, D-11) — note draft 1 said "six directions" and omitted VERDICT, so it under-specified its own fix. **(b)** *(widened in round 1 of the rebuild — it was scoped to S6/S7 and the same hole exists at S10)* The guards **glob** rather than reading hardcoded consts, in **three** places: `docs/rc/parity-classes-*.yaml` (S6/S7), **`corpus/hero-*/`** and **`cmd/eval/hero_*_test.go`** (S10). All three are the same bug — a hardcoded path where a glob belongs — and each lets a family ship nothing and be *silent* rather than red. **(c)** `legalFixtures` gains a non-Go member (that is TEMPL-P3). | A product change. **Highest-value prerequisite in this table.** Until (a) lands, every family table's `profile: "both"` / `store: "both"` claim is E4 wearing an E1 label; until (b) lands, **S6, S7 and S10** cannot be failed by omission at all. Each new family multiplies all three exposures. Owner: whichever of SW-181/SW-182 lands the second family table. |
| **LANGHONEST-001** *(scope widened twice — now every level below `typed-confirmed`)* | **Two failure modes.** **(i)** At `parse-only`, three Stable-12 operations answer about an indexed file as though the anchor were mistyped, and `trust-report` reports `files_skipped: 0` / `registrants: []` for those files (§10.4). **(ii)** At **`cross-file-heuristic`** *and* `intra-file-only`, `related_files` states *"has no both edges to other files"* as fact over a file that carries the reference — including `app.py`, whose extractor demonstrably computes imports (§5.2). Mode (ii) is the more dangerous: `outcome` and `method` both read as a successful answer. | Mode (i) means minting file nodes for parse-only parsers, which changes `imports` targets (ADR 0011's `fileNodesByDir`) — a product-byte change with full D7 ceremony. Mode (ii) needs the summary to name the capability limit instead of asserting absence — also product bytes. Both out of scope under AC-8. **Mode (ii) lands on SW-181 and SW-182 directly**, which is why the scope is stated here rather than discovered there. |

**Escalated to the owner — AC-4 presupposes a uniformity that is measurably
false.** AC-4 requires the template to demand the honest-empty invariant *at
every level*. This document does demand it. But at `parse-only` the demand is
**unsatisfiable by the shipped product** (§10.4), so a `parse-only` language
cannot pass a template that is enforced literally. There are two owner options
and the builder took neither:

- **(A)** Close LANGHONEST-001 — mint a `file` node for parse-only parsers —
  and the invariant holds as written at all four levels. Cost: a product-byte
  change that also changes `imports` targeting.
- **(B)** Restate the invariant at `parse-only` as *"an operation must not
  answer as though the anchor were mistyped; where a file kind is not
  represented in the graph, the summary must say so"* — a weaker and honest
  form that the product could meet with a summary-string change alone.

**No acceptance criterion has been rewritten on the strength of this.** The
template as written demands the invariant literally; §10.4 records that
`parse-only` cannot meet it today; the choice between (A) and (B) is the
owner's, and SW-185 is where it becomes blocking.

---

## 13. How to re-verify this document

```bash
# §1 slot paths — every named convention resolves against the JVM instance
ls docs/rc/parity-classes-jvm.yaml engine/conformance/jvmparity_test.go \
   engine/conformance/jvmparity_matrix_test.go corpus/hero-jvm \
   corpus/fixtures/hero-jvm cmd/eval/hero_jvm_test.go \
   internal/jvmgroundtruth internal/jvmcorpus

# §3/S1, §9/D-1 — the ga-language set really is {go}
grep -n 'category: ga-language' -B3 docs/coverage-matrix.yaml
go run ./cmd/coverage -check

# §3/S14, §9/D-9 — GA-LANG-* ROWS. CORRECTED 2026-08-19 (SW-174): this line
# read "zero GA-LANG-* ROWS exist today" and the command below was annotated
# "expect 0 — STABLE". Both were true when written and are FALSE from SW-174 on:
# eighteen rows now exist (GA-LANG-java-G1..G9 and GA-LANG-kotlin-G1..G9, with
# G2 substituted by G2SUB at both), all UNKNOWN. The count is NOT stable — it
# rises with every language that lands its scaffold (SW-181/182/184/185/186).
# What is stable is the PROPERTY: the command counts rows and never counts a
# mention. Pinning "0 — STABLE" was the same class of defect the note below
# warns about, one level up: a number that is only correct while nothing exists.
#
# NOTE the `- id:` anchor. A bare `grep -c 'GA-LANG-'` on that file does NOT
# return the row count: it counts the mentions in the comment block explaining
# the row shape as well. Draft 1 published the bare grep with "# expect 0"
# beside it — a command self-falsified by its own commit. Count rows, never
# mentions.
#
# AND DO NOT PIN THE BARE COUNT EITHER. Round 1 fixed the command but wrote
# "a bare grep returns 6" beside it; the rebuild round edited that comment block
# and the bare count became 10, so the ANNOTATION had acquired the same defect
# as the command it was warning about. Any number derived from prose drifts when
# the prose is edited. State the property, pin only the row-anchored count.
grep -c '^  - id: GA-LANG-' docs/rc/evidence-index.yaml   # 18 as of SW-174; rises per language
go run ./cmd/evidence -check

# §3/S3 — the Owns contract. The DECLARATION is at :66; :40-65 is its doc
# comment. Draft 1 cited ":40-47" and printed a range that stopped before the
# declaration it claimed to show.
sed -n '27,70p' engine/typeresolve/registry.go
grep -n 'Owns(relPath string) bool' engine/typeresolve/registry.go
  # expect TWO lines: :66 (the INTERFACE method — the one S3 cites) and :116
  # (goResolver's implementation). Round 2 corrected an annotation that said
  # "expect :66" and so read as though the grep returned one line.

# §5.4 — the harness gap: Summary and Confidence.Method are not captured into
# scenario.Result. Round 2 replaced `grep -n 'Summary' scenario.go`, which was
# published here with the annotation "no capture into Result" and returns
# ZERO OUTPUT — an empty result demonstrates nothing, since it is what a
# misspelled token also returns. Show what IS captured, then show the absence
# as a COUNT, which is a reading rather than a silence:
sed -n '487,503p' engine/scenario/scenario.go
grep -c 'res\.\(OpOutcome\|EvidenceCount\|ConfidenceTop\) = ' engine/scenario/scenario.go
  # 9 — Outcome, evidence count and Confidence.TOP are captured, in several
  # branches; the contract-envelope branch is the block printed above (:488-490).
  # (This annotation said ":488 :489 :490" when first written in round 2 and was
  # corrected by running it: three of the nine, quoted as though they were all.)
grep -c 'Summary' engine/scenario/scenario.go        # 0 — the token never appears
grep -n 'Confidence\.' engine/scenario/scenario.go   # :490 ONLY, and it is .Top
  # So no scenario assertion can reach summary text or confidence.METHOD, which
  # is exactly the gap TEMPL-P1(a) addresses and why (a) alone is insufficient.

# §9/D-7 — the shipped honest-empty scenarios anchor on a nonexistent term
cat corpus/hero/hero-03-search-empty.yaml corpus/hero-jvm/hjvm-03-search-empty.yaml

# §3/S7, §9/D-11 — the Go guard has SEVEN t.Run directions; the JVM guard is
# missing VERDICT, AXIS and VOCABULARY. Count the t.Run calls, NOT the source's
# own comment at paritymatrix_test.go:167, which says "SIX" and is stale.
grep -n 't.Run("' engine/conformance/paritymatrix_test.go
  # expect MISSING PHANTOM KIND VERDICT OWNER AXIS VOCABULARY
grep -c '"AXIS"\|"VOCABULARY"\|"VERDICT"' engine/conformance/jvmparity_matrix_test.go  # 0

# §3/S7 — the discoverability hole: both guards use a HARDCODED const path and
# nothing globs the family tables, so a missing family table is silent, not red.
grep -in 'parityClassesPath = ' engine/conformance/*_test.go   # both are consts
grep -rn 'parity-classes-\*' --include='*.go' .      # expect NO hits

# §7.1 — THE ENFORCEMENT AUDIT. Each command backs one "Enforced by" cell.
# S10 has the SAME silence hole as S6/S7 -- the hero glob is hardcoded per
# family and nothing globs corpus/hero-*:
grep -n 'corpus", "hero' cmd/eval/hero_jvm_test.go
  # expect TWO lines, :35 and :109, BOTH hardcoding "hero-jvm" — which is the
  # point. Round 2 corrected an annotation reading "# :35" as though it were one.
grep -rn 'filepath.Glob.*hero-' --include='*.go' . | grep -v '"hero-jvm"\|"hero"'
  # expect NO hits: every hero glob names its family dir literally.
  # DO NOT use a bare `grep -rn 'hero-\*'` here. Round 2 measured it at ONE hit
  # — surfaces/client/capabilityaudit_test.go:45, a COMMENT added by SW-183
  # describing this very hole. Same defect as the GA-LANG greps above: a
  # "returns 0" claim falsified by prose that quotes it. Match the CODE.
# S3 is the one fully-enforced slot -- it holds every registrant at once:
grep -n 'func TestRegistry_OwnsIsDisjointAndCoversSubject' engine/semantic/semantic_test.go  # :52
grep -n 'len(resolvers) != 3\|representative rather than exhaustive' engine/semantic/semantic_test.go
# S1 is never inspected for a language with no matrix row -- it `continue`s:
sed -n '88,96p' internal/coverage/galang.go
# S14's URI+sha rule is enforced by the GO TEST on the real index, NOT by
# cmd/evidence -check, which appears in no workflow:
grep -n 'func TestCheckedInIndexIsFreshAndHonest' internal/evidence/evidence_test.go  # :227
grep -rn 'cmd/evidence' .github/                          # expect NO hits
grep -rn 'cmd/coverage' .github/                          # coverage-matrix.yml:40 only
# S11 only validates budgets ALREADY PRESENT, and hero-jvm has no entry:
grep -n 'func validateNoSilentZeroBudgets' cmd/eval/refscenario.go   # :550
grep -n 'scenario_dir' docs/eval/hero-budgets.json        # "corpus/hero" only

# §3/S12, §10.2 — the capability list is REGISTRY-GLOBAL, so "the trust report
# names the level" is satisfied by construction and proves nothing. 22 languages
# are reported for a fixture holding files in five of them.
cd /tmp/fx12 && /tmp/graphi-tmpl trust-report --json | jq '.capabilities | length'   # 22

# §9/D-10 — the three fields the JVM ROWS omit. Anchor on the row indentation:
# a bare grep also matches this change's own D-10 header commentary. Same rule
# as above — the row-anchored counts are the ones that hold.
grep -c '^    test_line:\|^    prd_source:\|^    delta_source:' docs/rc/parity-classes.yaml      # 57
grep -c '^    test_line:\|^    prd_source:\|^    delta_source:' docs/rc/parity-classes-jvm.yaml  # 0

# §9/D-12 — every JVM ROW claims a Go parser. Row-anchored, so header
# commentary mentioning the value does not inflate it.
grep -c '^    fixture: "production Go parser"' docs/rc/parity-classes-jvm.yaml   # 13

# §3/S9 — the legacy (non-v3) pins Wave 2 will inherit
grep -n '"name": "flask"' -A 17 corpus/manifest.json
  # -A 17 spans the WHOLE entry (to the next "name"); piping it through
  # `grep -c 'tier\|measured'` returns 0. Round 2 widened this from -A 8, which
  # stopped inside the entry and so showed an absence it had not covered.

# §5.2, §10 — reproduce the whole measurement on ONE 12-file fixture.
# Build from the branch; the installed binary is stale.
# The fixture MUST carry the reference under test: a YAML with no `include:`
# cannot expose the no_edges defect, which is how draft 1 missed it — and a
# fixture that omits app.py cannot expose that the defect reaches
# cross-file-heuristic, which is how ROUND 1 under-scoped it.
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-tmpl ./cmd/graphi
mkdir -p /tmp/fx12/pkg && cd /tmp/fx12 && git init -q .
printf 'from pkg.util import helper\n\ndef main():\n    return helper(41)\n' > app.py
printf 'def helper(x):\n    return x + 1\n\ndef unused_helper(y):\n    return y\n' > pkg/util.py
printf '# only a comment\n'                             > empty.py
printf 'just prose, no headings\n'                      > notes.md
printf 'include: ./other.yaml\n'                        > settings.yaml
printf 'k: v\n'                                         > other.yaml
printf '{"name": "demo", "version": 1}\n'               > config.json
printf '{"k":1}\n'                                      > other.json
printf '@import "base.css";\n\n.card { color: red; }\n' > main.css
printf '.base { color: blue; }\n'                       > base.css
printf '[base](./base.md)\n'                            > readme.md
printf '# Base\n'                                       > base.md
git add -A && git -c user.email=a@b -c user.name=c commit -qm init
/tmp/graphi-tmpl index >/dev/null

# The four no_edges instances. ALL FOUR RETURN THE SAME SENTENCE.
/tmp/graphi-tmpl related-files app.py         # cross-file-heuristic -- A LIE (§5.2)
/tmp/graphi-tmpl related-files main.css       # intra-file-only      -- A LIE
/tmp/graphi-tmpl related-files readme.md      # intra-file-only      -- A LIE
/tmp/graphi-tmpl related-files settings.yaml  # intra-file-only      -- CONTESTABLE (§5.5)
/tmp/graphi-tmpl related-files empty.py       # no_edges (NOT a zero-symbol rule)
/tmp/graphi-tmpl related-files config.json    # method: unresolved   <-- §10.4
/tmp/graphi-tmpl explain-symbol config.json   # same
/tmp/graphi-tmpl change-risk config.json      # same
/tmp/graphi-tmpl trust-report --json | jq '.coverage, .abstention'
# expect files_indexed == 12, files_skipped: 0, registrants: []
# NOTE: trust-report is NOT byte-stable across runs (graph_generation.id is
# fresh per pass). Every substantive field is; the id is not.

# §5.2 — WHY the outputs are byte-identical across levels: the graph holds NO
# file->file edge at all. Every edge is `defines` (file->symbol, intra-file)
# plus one `calls` to an `external` stub. app.py's import produced NO edge to
# pkg/util.py. Read db_path from `graphi status --json`.
DB=$(/tmp/graphi-tmpl status --json | jq -r .db_path)
sqlite3 "$DB" "select count(*) from edges;"                       # 9 (balanced profile)
sqlite3 "$DB" "select count(*) from edges e
   join nodes nf on nf.id=e.from_id join nodes nt on nt.id=e.to_id
   where nf.kind='file' and nt.kind='file';"                      # 0  <-- the point
sqlite3 "$DB" "select count(*) from nodes where kind='file';"     # 10 of 12 (no .json)

# §5.2 — and the predicate that makes app.py the decisive instance: python's
# extractor DOES compute imports, so `no_edges` cannot be honest for it.
grep -n 'func pyImports' core/parse/parser_python.go              # :233 (:232 is the comment)
grep -n 'no import system' core/parse/parser_yaml.go core/parse/parser_css.go \
                           core/parse/parser_markdown.go core/parse/parser_toml.go \
                           core/parse/parser_hcl.go
  # ALL FIVE intra-file-only parsers, each at :94. Round 2 added parser_hcl.go,
  # whose omission made this command contradict §5.5's "all five" claim.
# §5.1 — the four result shapes. Only the first carries summary + method.
ID=$(/tmp/graphi-tmpl search card | sed 's/.*"node_id":"\([^"]*\)".*/\1/')
/tmp/graphi-tmpl explain-symbol "$ID"   # shape A: summary + confidence.method
/tmp/graphi-tmpl change-risk    "$ID"   # shape A: outcome=found, method=edge_tiers
/tmp/graphi-tmpl callers        "$ID"   # shape B: no summary, no confidence
/tmp/graphi-tmpl impact         "$ID"   # shape C: {analyzer,outcome,symbol}
/tmp/graphi-tmpl search card            # shape D: {query,matches} -- no outcome

# §10.3 mechanism
sed -n '25,32p;53,62p' core/parse/parser_json.go
sed -n '100,115p' core/parse/mapping.go

# §11 — the product-byte claim
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/head ./cmd/graphi
shasum -a 256 /tmp/head
git diff --stat --name-only HEAD~1 -- engine core surfaces cmd internal  # expect empty
```
