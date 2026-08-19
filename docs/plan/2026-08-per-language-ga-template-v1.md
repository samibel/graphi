# graphi — the per-language GA template v1 (2026-08-19)

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
(`internal/coverage/galang.go:88`) binds it three ways: the row's declared level
must equal the level the **live registries** derive
(`surfaces/client.LanguageCapabilities()`); every `GA-LANG-<lang>-*` row in the
evidence index must read PASS with URI **and** sha; and a language other than
`go` with **no** such rows is rejected outright
(`galang.go:133` — "a GA claim without evidence rows is vacuous").

**May abstain from.** Nothing. Every level is declarable; no level is exempt.

**Class.** E1 — the check is a test-covered binary gate
(`internal/coverage/galang_test.go`), and it fails closed.

> **A trap this slot hides.** The derivation is *registration*-derived, not
> *outcome*-derived. `engine/trust/capability.go:96-127` is a first-match-wins
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
`Owns(relPath string) bool` (`engine/typeresolve/registry.go:40-47`) — the
LANGUAGE half of the `(directory, language)` stale-confirmed sweep key. The
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
The Go guard (`paritymatrix_test.go:183`) has **six** directions; the JVM guard
(`jvmparity_matrix_test.go:43`) has **three**. The template requires all six:

| Direction | Rejects | JVM has it? |
|---|---|---|
| **MISSING** | a `required` row with no harness row of the same id | ✔ |
| **PHANTOM** | a harness row id not declared in the YAML | ✔ |
| **DEFERRED** | a `deferred` row that HAS a harness row, or names no `deferred_to` | ✔ |
| **KIND/OWNER** | a `kind` outside the vocabulary; a count other than the **pinned literal** (adding a class means editing the number and saying why); a `required` row with an invented `owner` | ✔ |
| **AXIS** | a narrowed axis — `parityProfiles()` dropping an entry while rows still publish `profile: "both"`, or `len(parityBackends()) != 2` while rows publish `store: "both"` | **✘** |
| **VOCABULARY** | a closed-set field outside its vocabulary; an `ABSENT` row still citing `fixture`/`store`/`profile`/`assertion`; a `required` row with `profile != "both"`; `known_defect` containing whitespace; `store: "none"` with a byte assertion | **✘** |

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

**May abstain from.** Nothing. This is the slot that turns S6 from E3 into E1.
A family table with no drift guard is a list of intentions.

**Class.** E1 with all six directions; **E3** for any claim carried only by a
field no guard reads.

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
   (`internal/corpus/corpus_test.go:530-534`).

A **separate scenario directory per family is mandatory, not stylistic**:
`corpus/hero/` is frozen at exactly 20 scenarios by `cmd/eval/hero_test.go`, so
adding a language's scenarios there breaks the Go gate. And the tier-1 entry
must carry **no `confirmed_edges`** where the corpus runner drives the *default*
binary with the family's binder off — asserting a confirmed edge there would
contradict the default-off contract (`corpus/manifest.json:554` says so in
words).

The gate's assertions are the template's real content here, and they are
stronger than "twenty scenarios exist". The JVM instance
(`cmd/eval/hero_jvm_test.go:54-97`) asserts: at least as many tasks as stable
ops; **every** stable operation has a task; **no** task exercises a non-frozen
operation; every declared failure class has a task; **at least one task declares
a negative (`absent`) anchor**; and **no** task declares `max_latency_ms`,
because budgets are frozen from a reproducible CI run (ADR 0003 U5), not
invented in a scenario file.

**May legitimately abstain from.** Scenarios whose semantics the level cannot
express — an `intra-file-only` language has no cross-file `callers` scenario to
write. The abstention is recorded by the scenario that replaces it: the op still
gets a task, and that task asserts the **honest-empty** behaviour of §5.

**Class.** E1.

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

**May abstain from.** Nothing.

**Class.** E1.

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

**Class.** E1 for the check; the row's own claim is whatever §2 rule 2 yields.

---

## 4. Substitution is labelled, never renamed (AC-3)

Where a gate is **substituted** rather than met, the substitution is reported
under its own name and never under the original gate's.

**The rule, made mechanical.** The evidence-row id suffix for a substituted G2 is
**`G2SUB`**, never `G2`:

```
GA-LANG-java-G2SUB      ← the heuristic-resolver contract proof
GA-LANG-go-G2           ← would be the real thing (go alone, and go is grandfathered)
```

Both match the prefix `GA-LANG-<lang>-` that `CheckGALanguages` requires, so the
distinction costs nothing mechanically and buys a property worth having:
`grep 'GA-LANG-.*-G2:'` over the index answers "which languages actually have a
type-checker-proven tier?" and cannot be fooled by a substitute. The row's
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

### 5.1 The predicate, stated precisely

`outcome: empty` is **not** the predicate. Structurally different answers all
serialize with `"outcome":"empty"`, and the field that separates them is
`confidence.method`:

| `confidence.method` | Where it is set | What it means | Honest when |
|---|---|---|---|
| `no_edges` | `engine/agenttools/related/related.go:236`, `changeimpact/changeimpact.go:506` | The anchor **resolved**; it has no edges of the requested kind. | always |
| `empty_graph`, `empty_history`, `no_git_provider`, `no_framework_annotations` | `engine/agenttools/*` | A **named** reason for the emptiness. | always — a named reason is the honest shape |
| `unavailable`, `ambiguous` | `engine/agenttools/*` | A typed refusal to answer. | always |
| **`unresolved`** | `engine/agenttools/*` | The operation **could not find the anchor at all**. | only if the anchor really is not in the graph **and the summary says why** |

Note what that table shows: the vocabulary already has the right *shape* — most
methods name a reason. `unresolved` is the one that names none, and it is the
one a `parse-only` file lands on. (`definition_only`,
`engine/agenttools/explain/explain.go:99`, accompanies `outcome: found`, not
`empty`; it is listed nowhere above for that reason.)

`unresolved` is where an operation lies. Its summary today reads

> `related_files: no symbol or file matched "X"; try `search` for discovery, a
> repo-relative path, a qualified name, or a 16-hex node id`

which is advice for a **typo**. Given an indexed, tracked, successfully parsed
file, that sentence is false in the only way that matters to an agent: it says
"you asked wrong" when the truth is "this file kind carries no graph nodes".

**So the template's honest-empty predicate is:**

> For every in-repo artefact of language L that a Stable-12 operation is asked
> about, the operation must resolve the anchor (`method != "unresolved"`), **or**
> its summary must state that the artefact's *file kind* is not represented in
> the graph. It must never answer as though the anchor were mistyped.

### 5.2 The enforcement gap — named, because it would otherwise propagate

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

**Therefore the template requires two things of every consuming story:**

1. **The scenario must anchor on an artefact that EXISTS.** Both shipped
   honest-empty scenarios query `zzz_no_such_symbol_zzz` — a term that genuinely
   does not exist, so `empty` is honest by construction and the scenario proves
   nothing about the hard case. The template's honest-empty scenario anchors on
   a **real, indexed, checked-in file of language L**.
2. **The harness must be able to see the difference.** Until
   `scenario.Result` carries the summary and `confidence.method`, requirement 1
   is asserted by review and not by a gate. That prerequisite is **TEMPL-P1**
   (§12). It is a product-byte change and is deliberately **not** made here.

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
`f054bb0`. `D-n` rows are divergences; each is classified as a **template
defect** (the template asked for the wrong thing) or a **JVM-instance gap** (the
template is right and the instance does not yet have it). **Closing the gaps is
not this story** — that is stated in the ticket's own out-of-scope list.

| Slot | Reproduced? | Divergence |
|---|---|---|
| S1 | **No** — `docs/coverage-matrix.yaml` has exactly one `ga-language` row (`go`). | **D-1**, instance gap: expected. Java/Kotlin are pre-flip; the row lands at WP-J11 (SW-179). Not a defect. |
| S2 | Yes — the declared-type binder, default-off behind `GRAPHI_JVM_TYPERESOLVE`. | — |
| S3 | Yes — `Owns` on the registrant, `Owns ⊇ Subject`, pairwise disjoint (`engine/typeresolve/registry.go:40-47`). | **D-2**, instance gap **carried forward**: the no-registrant path has immortal confirmed edges, unreachable only because walkers hardcode `TierDerived`, guarded test-only. Already recorded in SW-170; restated here because the template's S3 is where a new language would otherwise inherit it. |
| S4 | Yes — `internal/jvmgroundtruth`, three precisions (ByName/ByArity/BySignature). | **D-3**, instance gap: JVMSOUND-003/004, JVMHARN-001 and blind spots #15/#17 are open. The oracle is E0 for what it covers and silent on those. |
| S5 | Yes — `internal/jvmcorpus` + the `jvm_compile` blocks. | **D-4**: okio is pinned but **not compiled** — `corpus/manifest.json:411` records the negative result in full. The template calls this a correct abstention, not a gap. |
| S6 | Yes — `docs/rc/parity-classes-jvm.yaml`, `matrix_version: 3` (comment), 13 required rows, zero deferred. | **D-10**, instance gap: the JVM rows omit `test_line`, `prd_source` and `delta_source`, which the Go rows carry. They unmarshal empty into the shared `parityRow` struct, so nothing complains. |
| S7 | Partly — `jvmparity_test.go` + `jvmparity_matrix_test.go` carry MISSING, PHANTOM, DEFERRED, KIND/OWNER and RequiredRowsAreProven. | **D-11**, instance gap, **the most consequential one found**: the JVM guard has **no AXIS and no VOCABULARY direction**, and (because of D-10) no citation guard. The 13 JVM rows all publish `profile: "both"` and `store: "both"` — claims that on the Go side are checked and here are not. |
| S8 | Yes — Go classes mapped/adapted/`not_applicable` with reasons; six JVM classes added. | **D-5**, **template defect, corrected in this document**: the template's first draft said the disposition was machine-checked *exhaustively*. It is not — the guard compares a family table to its own twin only, and nothing enumerates the Go table. §3/S8 now states this and §12 carries it as TEMPL-P2. |
| S9 | Partly — guava and okio at the v3 measured standard. | **D-6**, instance gap: WP-J7 (SW-176) has not run; there is no published JVM real-repo matrix. Expected by sequencing. |
| S10 | Yes — 16 scenarios in `corpus/hero-jvm/`, `corpus/fixtures/hero-jvm/`, `cmd/eval/hero_jvm_test.go`. | **D-7**, instance gap: `hjvm-03-search-empty` anchors on `zzz_no_such_symbol_zzz` and asserts `outcome: empty` alone. Per §5 that is the easy case; the JVM instance has **no** scenario for the hard one. |
| S11 | Partly — `guava` is in `hero-budgets.json` `real_repos.selection` with real ceilings, and in the `eval-full.yml` matrix. | **D-8**, instance gap: no `docs/eval/runs/` directory for a JVM corpus, and `hero_suite.scenario_dir` names `corpus/hero` only, so `corpus/hero-jvm` has no budget entry. G7 is SW-177, post-candidate-move by design. |
| S12 | Partly — `docs/language-support.md` carries the Java/Kotlin row at `cross-file-heuristic`; the derivation is live. | — |
| S13 | Yes — `trust_language_skips` / `trust_skip_provenance`, `AbstentionFacts.Registrants`. | — |
| S14 | **No** — `docs/rc/evidence-index.yaml` contains **zero** `GA-LANG-*` rows. | **D-9**, instance gap: SW-174 creates them, born UNKNOWN. |
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
exercised.** The template's S12 wording is tightened accordingly.

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
profile artefact.

**It is not a zero-symbol rule.** `empty.py` (a comment only) and `notes.md`
(prose only) both resolve with `method: "no_edges"`. Only the two `.json` files
do not. The mechanism: `core/parse/mapping.go:106` mints the `file` node inside
`MapTreeSitter`, **before** the node-spec loop, so every symbol-extracting
parser yields a file node even for zero symbols; `core/parse/parser_json.go:53-62`
returns `Meta` and `Root` only and calls no extractor at all —
`ExtractsSymbols() == false` (`parser_json.go:29`), the sole `false` among 25
registered parsers. `engine/ingest` mints no file node of its own
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
filed as **`LANGHONEST-001`** so that SW-183 and SW-185 inherit an id rather
than rediscover a symptom.

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
2. **The template caught a real defect that the JVM instance could not have
   surfaced.** Java and Kotlin are `cross-file-heuristic`; nothing in the JVM
   asset set exercises `parse-only`. A template extracted from the JVM instance
   and validated only against it would have shipped an unsatisfiable AC-4 into
   SW-185.
3. **The anti-vacuity rule earns its place.** All three `.json` responses carry
   `"outcome":"empty"`. A hero scenario copied from `hero-03`/`hjvm-03` would be
   **green** on every one of them. §5's predicate is what separates them, and
   §5.2's TEMPL-P1 is what would let a gate see it.

---

## 11. Product-byte measurement (AC-8)

`./cmd/graphi` built with `-trimpath -buildvcs=false`, `CGO_ENABLED=0`, from the
working tree before and after this change:

| tree | sha256 |
|---|---|
| `f054bb0`, clean | `87762557ef3400c71ed7f0d275ca9a16ebbd0ba84aa2083462c6b83936647cb7` |
| + this document, + the three cross-references | `87762557ef3400c71ed7f0d275ca9a16ebbd0ba84aa2083462c6b83936647cb7` |

**Byte-identical.** The change touches four paths — this document, plus
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
(`internal/parityreport/report.go:87`) — HEAD builds to `87762557…`, the
candidate to `036be635…`. A parity dispatch run here is **not publishable**, a
candidate move is already owed, and that is a known escalated owner decision
predating this story. This document neither causes it nor resolves it.

---

## 12. Prerequisites and escalations this template raises

| id | What | Why it is not done here |
|---|---|---|
| **TEMPL-P1** | `scenario.Result` must carry the contract `summary` and `confidence.method` (or the schema must gain an `expect.confidence_method`), so §5's honest-empty predicate is gate-checkable rather than review-checked. | A product-byte change; AC-8 forbids it in this story. Owner: whichever of SW-181/SW-185 first needs a hard honest-empty gate. |
| **TEMPL-P2** | A check that enumerates `docs/rc/parity-classes.yaml` and asserts every class is dispositioned in each family table. Today the guard only compares a family table to its own twin (D-5). | A product change, and it must not run before the family-table set stabilises. |
| **TEMPL-P3** | `legalFixtures` gains a `"production <lang> parser"` member so a family table stops labelling its rows `"production Go parser"` (D-12). | A product change; harmless but not free — every existing JVM row's value changes with it. Should land before a third family table. |
| **TEMPL-P4** | The AXIS and VOCABULARY guard directions, and the `test_line` citation guard, are extended to every `parity-classes-<fam>.yaml` (D-10, D-11). | A product change. **This is the highest-value prerequisite in the table**: until it lands, every family table's `profile: "both"` / `store: "both"` claim is E4 wearing an E1 label, and each new family multiplies the exposure. Owner: whichever of SW-181/SW-182 lands the second family table. |
| **LANGHONEST-001** | Three Stable-12 operations answer about an indexed `parse-only` file as though the anchor were mistyped; `trust-report` reports `files_skipped: 0` and `registrants: []` for those files. §10.4. | Fixing it means minting file nodes for parse-only parsers, which changes `imports` targets (ADR 0011's `fileNodesByDir`) — a product-byte change with full D7 ceremony, and out of scope here. |

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

# §3/S14, §9/D-9 — zero GA-LANG-* rows exist today
grep -c 'GA-LANG-' docs/rc/evidence-index.yaml     # expect 0
go run ./cmd/evidence -check

# §3/S3 — the Owns contract
sed -n '27,60p' engine/typeresolve/registry.go

# §5.2 — the harness gap: Summary and Confidence.Method are not captured
sed -n '487,503p' engine/scenario/scenario.go
grep -n 'Summary' engine/scenario/scenario.go      # no capture into Result

# §9/D-7 — the shipped honest-empty scenarios anchor on a nonexistent term
cat corpus/hero/hero-03-search-empty.yaml corpus/hero-jvm/hjvm-03-search-empty.yaml

# §3/S7, §9/D-11 — the JVM guard is missing AXIS and VOCABULARY
grep -c 'func Test' engine/conformance/jvmparity_matrix_test.go     # 3
grep -n '"AXIS"\|"VOCABULARY"' engine/conformance/paritymatrix_test.go
grep -n '"AXIS"\|"VOCABULARY"' engine/conformance/jvmparity_matrix_test.go   # no hits

# §9/D-10 — the three fields the JVM rows omit
grep -c 'test_line\|prd_source\|delta_source' docs/rc/parity-classes.yaml      # >0
grep -c 'test_line\|prd_source\|delta_source' docs/rc/parity-classes-jvm.yaml  # 0

# §9/D-12 — every JVM row claims a Go parser
grep -c 'fixture: "production Go parser"' docs/rc/parity-classes-jvm.yaml      # 13

# §3/S9 — the legacy (non-v3) pins Wave 2 will inherit
grep -n '"name": "flask"' -A 8 corpus/manifest.json   # no tier, no measured block

# §10 — reproduce the dry run. Build from the branch; the installed binary is stale.
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-tmpl ./cmd/graphi
mkdir -p /tmp/tmplfix/pkg && cd /tmp/tmplfix && git init -q .
printf 'def helper(x):\n    return x + 1\n' > pkg/util.py
printf 'from pkg.util import helper\n\ndef main():\n    return helper(41)\n' > app.py
printf 'name: demo\nlisten: 8080\n' > settings.yaml
printf '{"name": "demo", "version": 1}\n' > config.json
printf '# only a comment\n' > empty.py
git add -A && git -c user.email=a@b -c user.name=c commit -qm init
/tmp/graphi-tmpl index >/dev/null
/tmp/graphi-tmpl related-files app.py         # method: no_edges
/tmp/graphi-tmpl related-files empty.py       # method: no_edges  (NOT a zero-symbol rule)
/tmp/graphi-tmpl related-files settings.yaml  # method: no_edges
/tmp/graphi-tmpl related-files config.json    # method: unresolved   <-- §10.4
/tmp/graphi-tmpl explain-symbol config.json   # same
/tmp/graphi-tmpl change-risk config.json      # same
/tmp/graphi-tmpl trust-report --json | jq '.coverage, .abstention'
# expect files_indexed == the tracked count, files_skipped: 0, registrants: []

# §10.3 mechanism
sed -n '25,32p;53,62p' core/parse/parser_json.go
sed -n '100,115p' core/parse/mapping.go

# §11 — the product-byte claim
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/head ./cmd/graphi
shasum -a 256 /tmp/head
git diff --stat --name-only HEAD~1 -- engine core surfaces cmd internal  # expect empty
```
