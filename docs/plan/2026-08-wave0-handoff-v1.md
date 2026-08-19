# graphi — Wave 0 handoff v1 (2026-08-18)

> ## AMENDMENT — 2026-08-19 (SW-168): the `clauseByDir` defect is now LINK-002, reproduced and disclosed
>
> Per D6 this amendment is **added**; nothing below is rewritten, re-pointed or
> deleted. Items 1–2 supersede statements in the original text and are named here
> rather than edited in place. Items 3–4 were added in **review round 1** of
> SW-168 and supersede statements made **by this amendment's own first draft**,
> which is stated plainly rather than quietly corrected — the first draft asserted
> that LINK-002 "drops true edges only and never emits a wrong one" and published
> one directory count under a label describing a different one. Both were wrong,
> both were caught in review, and the corrections are items 3 and the figure table
> below:
>
> 1. **§4, "The `clauseByDir` last-write-wins recall defect — NOT disclosed".**
>    It now carries the id **LINK-002** and IS disclosed, on all three surfaces
>    the disclosure contract (D8) names: `readme.md` "Known limits", the doctor
>    `known-defects` check at info severity (`internal/doctor/checks.go`,
>    restored for it, registered in `cmd/graphi/doctor.go`, asserted by
>    `internal/doctor/checks_test.go`), and `docs/language-support.md` beside the
>    Go GA row. Record:
>    [`../rc/link-002-clause-by-dir-recall.md`](../rc/link-002-clause-by-dir-recall.md).
>
> 2. **§7 item 3, "The `clauseByDir` user-visible recall loss — mechanism read
>    from source, consequence not observed".** That UNVERIFIED EXPECTATION is
>    **discharged**: the consequence is now observed. On a hermetic Go fixture
>    (`package shop` beside an external `package shop_test`) a caller making
>    three `recv.Method` calls gets **one** `calls` edge from the built CLI — and
>    the survivor is the one into the **test** package, while both production
>    calls are dropped, with `outcome: found` and no diagnostic. Deleting the one
>    `_test.go` file restores both. Byte-identical over five consecutive
>    `graphi rebuild` runs, and identical under `fast`, `balanced` and `deep`.
>    Pinned by `engine/link/clausebydir_test.go`, which fails **with
>    instructions** the moment the defect is fixed.
>
> 3. **LINK-002 is a SOUNDNESS defect as well as a recall one, and the first
>    draft of this amendment said otherwise.** Added in review round 1 of SW-168.
>    Where the surviving clause declares a method of the same bare name, the call
>    is not dropped but **REDIRECTED** to that unrelated method: reproduced
>    through the CLI on a fixture whose ground truth is unambiguous —
>    `func run(c *shop.Cart) { c.Reset() }` resolves to `shop_test.Fixture.Reset`
>    with the collision present and to `shop.Cart.Reset` without it, same call
>    site, same edge slot. The mechanism is that hiding a clause manufactures
>    **false uniqueness** and thereby defeats `receiverMethod`'s own frozen
>    skip-on-ambiguity rule (`engine/link/index.go:415-417`), which is required
>    to abstain when both declarations are visible — verified by showing that it
>    *does* abstain when neither is hidden. The wrong edge is always `heuristic`
>    tier (0.6); `balanced`/`deep` emit the correct `confirmed` edge alongside it,
>    but under `-profile fast` the wrong edge is the only one. **Consequence for
>    this document:** the record's `Stop-ship? No` ruling rested entirely on the
>    false premise and is now **REOPENED as an owner question** — D5 is stated
>    unqualified ("a wrong edge is stop-ship") and whether it binds
>    `heuristic`-tier edges has never been decided. §9 of the record is what the
>    owner rules on. The frequency of the substitution on a real tree is **not
>    measured**.
>
> 4. **LINK-003 filed — a second, larger last-write-wins in the same function.**
>    Also from review round 1. `idx.byClause[clause][dir][bare] = n.ID()`
>    (`engine/link/index.go:272`) is written unconditionally too and, unlike
>    `byDir`, has **no `dirAmbiguous` companion**, so two methods sharing a bare
>    name in one package shadow each other invisibly — no package-clause collision
>    required. Reproduced through the CLI (`func (a *A) String()` beside
>    `func (b *B) String()`; a call on an `*A` resolves to `B`'s method).
>    Measured on the same run: **663 of 1 979 (33.5 %)** method declarations
>    unreachable **or** shadowed once both defects are counted, against 136
>    (6.9 %) for LINK-002 alone — roughly **5×** the surface. Same false-uniqueness
>    mechanism. Filed on `projects/graphi/backlog.md`, disclosed on the same three
>    user surfaces, pinned by `TestLink003_BareNameShadowing`, and **not fixed**.
>    It is recorded here because it changes the *scope* of the eventual fix story:
>    a fix that only makes `clauseByDir` hold a set leaves LINK-003 standing, and
>    that was verified executably — under the simulated LINK-002 fix the LINK-003
>    pin stays green.
>
> **Also now measured, where §4 had no figure.** On this repository at `a1a8a9a`,
> read from the real committed graphstore and streamed through
> `graphstore.ForEachNode` in production's own canonical NodeId order (13 620
> nodes; the order was asserted ascending, not assumed): **136 of 1 979** method
> declarations (6.9 %) are unreachable through `uniqueMethodInDir` — **108** of
> them in `engine/ingest`, where `ingest_test` took the last write.
>
> **Three distinct directory counts, because one number under the wrong label is
> how this measurement was first published.** Of the **105** directories that
> declare methods:
>
> | what is counted | count |
> |---|---:|
> | (a) directories **holding more than one package clause** (written by *any* dotted symbol) | **21** |
> | (b) directories where **more than one clause declares methods** | **10** |
> | (c) directories that **actually lose at least one method** today | **11** |
>
> (a) is the blast-radius number, and it is the one the words "holding more than
> one package clause" describe. (b) is the narrowest reading. (c) is what the
> record's per-directory table lists. The three differ because `clauseByDir` is
> written by any dotted symbol, so a directory can hold two clauses, or lose
> methods, without a second clause declaring any — `engine/embed/ollama` is
> exactly that case and is why (b) ≠ (c). Stated with its scope: this is one
> repository, it counts unreachable *declarations* rather than lost *edges*, the
> **substitution** case below is not counted at all, and the pinned corpus clones
> were **not** re-indexed. See §4 of the record for the full not-measured list.
>
> **Corrections to §4's own text, for the record.** §4 cites the assignment at
> `engine/link/index.go:158`; at `a1a8a9a` it is at **:223** (ADR 0011 moved it).
> §4 says the consumers are "the `receiverMethod` reverse index
> (`engine/link/index.go:195`) and `uniqueMethodInDir` (`engine/link/index.go:346`)";
> those are now `:260` and `:418`. The mechanism §4 describes is correct as
> described.
>
> **What did NOT change.** LINK-002 is **not fixed** — that is a product-byte
> change carrying the full D7 ceremony and is scheduled separately.
> `parityreport.CandidateSHA`, the published 19/19 real-repo matrix and every
> evidence row are untouched. §5's package order is unchanged; this amendment
> records the completion of its item 3 only.
>
> ### A finding about the provenance gate itself, surfaced by this change
>
> §8 measured that adding a **docs-only** file moves no product byte, and that is
> still true. It does **not** generalise the way a reader might assume, and the
> difference was measured rather than reasoned. Building `./cmd/graphi` with
> `-trimpath -buildvcs=false` from this working tree:
>
> | tree | sha256 |
> |---|---|
> | candidate `3b8d43f` (= HEAD `a1a8a9a`, verified) | `036be635…` |
> | + **one added comment line** in `engine/link/index.go`, nothing else | `5a6198aa…` |
> | + this story's `engine/link/index.go` comments (no statement changed) | `b714d731…` |
> | + the doctor `known-defects` disclosure | `ea3e335e…` |
>
> **A pure comment in a compiled source file is a product byte under this gate.**
> Line positions reach the binary through debug and runtime metadata, so
> `-trimpath` does not neutralise them. Two consequences worth carrying forward:
> (1) *any* change touching a compiled file — including one whose ticket says
> "comment/doc only" — forfeits `ProductDiffEmpty` and therefore publishability,
> so the candidate must move for it; (2) D8's disclosure obligation and a
> "no product bytes" constraint are **not jointly satisfiable** whenever the
> disclosure surface is compiled in, which the doctor check is. That tension is a
> real one for whoever schedules the next candidate move, and is recorded here
> rather than resolved: resolving it is an owner decision, not a builder's.
>
> **A PROPOSED reframing, carried as a proposal and nothing more.** SW-168's
> independent reviewer, having measured the same digests, suggested that the rule
> the measurement implies is *"no product **behaviour** change; publishability is
> expected to lapse until the next scheduled candidate move"* — i.e. that the
> constraint should bind behaviour rather than bytes, since as written it is
> unsatisfiable for **any** ticket touching a compiled file and will therefore
> recur on every future disclosure. **This is recorded as a proposal, not as a
> decision, and no acceptance criterion has been rewritten on the strength of
> it.** Adopting or rejecting it is the owner's call; it is written down so the
> option is on the table when the owner rules, and so that the next builder to hit
> the same contradiction finds it already stated.

## Why this file exists

The plan that governs the language-GA programme ("GA für ALLE Sprachen — die
Owner-Route v2") lives **outside this repository**, as prompt text in a session
that has ended. Its own step 0 says so. Everything it recorded — the exact repo
state, the proof artefacts behind PARITY-001/-002/-003, the open defects, the
remaining package order and the owner-ratified disciplines — was reconstructible
only from a chat log. This file lands that content in the repo so it survives a
session boundary.

## What this file is, and is not

- It is a **record**, not a re-plan. It states what was measured, what is open,
  and what the disciplines are. It re-scopes nothing.
- It **does not supersede**
  [`2026-08-graphi-p2-language-ga-program-v1.md`](2026-08-graphi-p2-language-ga-program-v1.md)
  (the G1–G9 bar) or
  [`2026-08-graphi-language-ga-execution-plan-v1.md`](2026-08-graphi-language-ga-execution-plan-v1.md)
  (the status ledger). It is a third document that cites both.
- It **corrects no cited artefact**. If an artefact it cites is found to be
  wrong, that is a finding to file, not to fix here.

## The evidence rule this document holds itself to

- Every **repo-state** fact below is stated with the commit it was verified at,
  and every such commit is reachable from the branch this document lands on.
- Every **evidence** claim cites the artefact path that carries it, and every
  cited path was resolved with a file-existence check.
- Anything from the source plan that could **not** be verified against this
  repository is either omitted or carried in §7 under an explicit
  **UNVERIFIED EXPECTATION** marker. Nothing here is asserted as measured that
  was not measured.

---

## 1. Repo state — verified at `23da507`

Verified 2026-08-18 at
`23da50774bc624dbf47efa3462388de53da602c8` (`23da507`).

| | |
|---|---|
| Branch | `claude/kotlin-java-canonical-ga-t3b8km` |
| HEAD | `23da50774bc624dbf47efa3462388de53da602c8` |
| Base | `origin/main` at `5285b1df7ddeff7133a94b83e034e89532465a6b` (`5285b1d`) |
| Ahead of `origin/main` | **5 commits** (`git rev-list --count origin/main..HEAD`) |

**Read `main` as `origin/main` throughout this document.** The local `main` ref
at `23da507` points at `12b90983…`, which is *behind* `origin/main`; counting
against it gives 86, not 5. The number that means anything is the one against
the remote.

The five commits, newest first:

| SHA | Subject |
|---|---|
| `23da507` | review: apply ADR 0010 review round 1 — JVM profile axis, AXIS guard, LINK-001 filed and disclosed |
| `8e7c949` | docs/rc: publish the ADR 0010 re-measurement — 19/19 PASS, PARITY-003 closed by measurement |
| `3398d3b` | parity: move the measurement candidate to the ADR 0010 fix commit |
| `7574a49` | ingest,conformance: fix PARITY-003 — remove the pass-scoped Balanced import aggregation (ADR 0010) |
| `6ea0b5d` | docs,doctor: the disclosed PARITY-003 workaround named a profile that does not exist |

`6ea0b5d` is worth reading before the other four: it is the correction of a
**published workaround that named a profile the CLI rejects** (`-profile full`).
It is the standing lesson behind discipline D8 in §6.

---

## 2. The measurement-candidate chain

The parity-matrix measurement candidate moved **twice on 2026-08-16**, each move
recorded before its first published measurement:

```
80d67ed  v0.7.1 — the P0 release candidate (published, tagged, attested)
   ↓     docs/decisions/2026-08-parity-candidate-move-adr0009.md
c4209dd  the merge that landed ADR 0009 (PARITY-002 fix)
   ↓     docs/decisions/2026-08-parity-candidate-move-adr0010.md
7574a49  the ADR 0010 fix (PARITY-003) — the candidate in effect at 23da507
```

All three SHAs are reachable from this branch. At `23da507` the constant
`parityreport.CandidateSHA` (`internal/parityreport/report.go:74`) reads
`7574a49379d3ede0a08bdb024e7a2e315bdc14a1`.

**What deliberately did not move:** the **P0 release candidate** in
`docs/rc/evidence-index.yaml`'s `candidate:` block still names the published,
tagged, attested release **v0.7.1 at `80d67ed`**. No release is tagged at
`7574a49`, and tagging one is the owner's decision, not a side effect of a
measurement. Parity reports therefore cite a candidate the release block does
not; the two move records are where that divergence is explained.

The provenance sentence is pinned by tests at `internal/parity/parity_test.go:641`
(the statement must say the product *source* is byte-identical to the current
candidate) and `:644` (a forbidden-phrasing list that includes the retired
candidate names). The authoritative check behind the sentence is a built-binary
comparison, not a path diff: `go build -trimpath -buildvcs=false ./cmd/graphi`
at HEAD and at the candidate must produce identical sha256 digests
(`internal/parity/provenance.go:64-97`).

---

## 3. Closed defects, and the artefacts that close them

| Defect | Status | Fix | Proof artefact |
|---|---|---|---|
| PARITY-001 | Closed **by measurement** | `d8f1fbb` (commit the deleted-path purge before `linkFiles`) | [`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md); ledger line in [`2026-08-graphi-language-ga-execution-plan-v1.md`](2026-08-graphi-language-ga-execution-plan-v1.md) |
| PARITY-002 | Closed **by measurement** | ADR 0009, [`../adr/0009-go-module-import-resolution.md`](../adr/0009-go-module-import-resolution.md); `6848746`, merged as `c4209dd` | [`../rc/parity-matrix-adr0009-run-a.json`](../rc/parity-matrix-adr0009-run-a.json) + [`…-run-b.json`](../rc/parity-matrix-adr0009-run-b.json) (W0.f-3: 16 PASS / 3 FAIL) |
| PARITY-003 | Fixed **and measured** | ADR 0010, [`../adr/0010-relink-unit-invariant.md`](../adr/0010-relink-unit-invariant.md); `7574a49` | [`../rc/parity-matrix-adr0010-run-c.json`](../rc/parity-matrix-adr0010-run-c.json) + [`…-run-d.json`](../rc/parity-matrix-adr0010-run-d.json) (W0.f-4: 19/19 PASS) |
| JVMSOUND-001/002 | Fixed | `9a9a9a2` (the binder no longer emits WRONG confirmed edges, W0.d) | ADR 0008 D6, [`../adr/0008-jvm-declared-type-resolution.md`](../adr/0008-jvm-declared-type-resolution.md); reproductions in [`2026-08-graphi-language-ga-execution-plan-v1.md`](2026-08-graphi-language-ga-execution-plan-v1.md) §1.1 |

### The 19/19 statement, stated precisely

Per [`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md)
("Current measurement — the ADR 0010 candidate"), and carried by
`parity-matrix-adr0010-run-{c,d}.json`:

- **19 of 19 rows PASS** — 17 change classes plus 2 crash conditions.
- **Two dispatches**, both `outcome PASS` and `publishable: true`, agreeing on
  every verdict (`-verdict-diff` exit 0) **and** on every per-row node/edge
  count and snapshot digest (`-counts-diff` exit 0).
- The two §12.3 store-level counts read **orphaned external nodes = 0** and
  **stale linker edges = 0** on all 38 sides (19 rows × full + incremental).
- Provenance: product source byte-identical to the ADR 0010 candidate at
  `7574a49`; run SHA `3398d3b`; runner class `Linux-X64/ccr-container`; clean
  worktree.

**Two things this does not say.** First, PARITY-001 is closed *as a defect*; its
**hardening is not** — the purge/batch ordering guard and the delete-shaped kill
test booked against it (review §4, R11) are still open and are the next item's
job, not this measurement's. Second, `-counts-diff` is what makes the
two-green-runs discipline hold at count granularity; `-verdict-diff` alone was
demonstrated structurally blind to the class it catches.

### The recall figures the fix exposed

The ADR 0010 fix removed a collapse firing on every Go repository with a dotted
module path. Cited from
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md), which
also carries the provenance paragraph explaining that these per-kind figures are
**not** in the JSON report artefacts and how to reproduce them (build
`./cmd/graphi` at `c4209dd` and at `7574a49`, re-index the same pinned clones,
count by kind):

| Repo | `imports` edges kept before | after | dropped by the shipped default |
|---|---|---|---|
| cobra | ~40 | 340 | ~88% |
| gin | ~99 | 291 | ~66% |
| grpc-go | ~670 | 23 575 | ~97% |

---

## 4. Open defects, and whether each is disclosed today

### LINK-001 — **DISCLOSED** on the user surfaces

**What it is.** An `imports` edge targets **every file in the imported package's
directory**, not the package's source files. Verified at `23da507`:
`engine/link/index.go:150` fills `fileNodesByDir` from every `file` node, and
`packageFileNodes` returns the whole list. A `README.md` is not part of a Go
package, so the edge is wrong in every profile; what ADR 0010 changed is its
*dominance under the shipped default*.

**Measured** — cited from
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md), on the
pinned clones: on cobra **44 of 340** `imports` edges point at `.md`/`.yml`;
on grpc-go **2 120** point at `.md`/`.sh`. Reproduced end-to-end on pinned
cobra: `graphi related-files -max-files 12 doc/man_docs.go` returned 5
genuinely-related items before and 12 after, the extra 7 including
`.golangci.yml`, `CONDUCT.md`, `CONTRIBUTING.md` and `README.md`. So on that GA
operation **recall improved and precision regressed**.

**Disclosure today** — verified at `23da507`:

- `readme.md:161`, under "Known limits", naming the defect, the measured
  figures, the affected operations, and a workaround.
- The doctor `known-defects` check, `internal/doctor/checks.go:387-400`, at
  **info** severity — never silently green. Pinned by
  `internal/doctor/checks_test.go:248-256`, which asserts the string mentions
  `LINK-001`, `imports`, `related_files` and `Workaround`.
- [`../adr/0010-relink-unit-invariant.md`](../adr/0010-relink-unit-invariant.md)`:154`
  records it as disclosed with its fix scheduled as its own change.

**Shape of the fix.** `packageFileNodes` must return the target package's
**source** files, decided on the file extension. Whether in-package `_test.go`
files belong is a separate ruling that change must make explicitly. This is a
**product-byte change**, so it moves the candidate again — which is why it is
sequenced first in §5.

### The `clauseByDir` last-write-wins recall defect — **NOT disclosed**

**What it is**, as a code fact verified at `23da507`:
`engine/link/index.go:158` assigns `idx.clauseByDir[dir] = clause`
**unconditionally**, so a directory whose symbols declare more than one package
clause retains only the **last clause the index build happened to see**. Both
consumers read through that single value: the `receiverMethod` reverse index
(`engine/link/index.go:195`) and `uniqueMethodInDir`
(`engine/link/index.go:346`) both look up `byClause[clauseByDir[dir]][dir]`.

**Shape.** This is a **recall** defect (edges that should resolve do not), not a
soundness defect (no wrong edge is emitted). It is therefore *not* stop-ship
under the zero-tolerance rule, which binds wrong edges.

**Disclosure today: none.** Verified mechanically at `23da507` — the identifier
`clauseByDir` occurs nowhere in the tree except `engine/link/index.go`, and
neither `readme.md`'s "Known limits" nor the doctor `known-defects` check
(`internal/doctor/checks.go:387`) names it. It also carries **no defect ID**
yet; it is referred to by its mechanism.

> **UNVERIFIED EXPECTATION.** The *user-visible* recall loss has not been
> reproduced or measured. The mechanism above is read off the source; the
> consequence is inferred from it, not observed. Filing it, reproducing it and
> disclosing it is its own item in §5 — and the disclosure contract (D8) means it
> should be disclosed at the point it is reproduced, not at the point it is fixed.

---

## 5. The remaining package order

Wave 0's order, with the reason it is this order and not another. The story IDs
are the portfolio tracker's; they are not carried in this repository.

| # | Package | Item | What it is |
|---|---|---|---|
| 1 | W0.f-5 | SW-166 | Fix LINK-001 — an `imports` edge targets the package's **source** files (its own ADR) |
| 2 | W0.f-5 | SW-167 | The measurement: move the candidate to the LINK-001 fix, re-publish the real-repo matrix |
| 3 | — | SW-168 | File, reproduce and **disclose** the `clauseByDir` recall defect |
| 4 | W0.i | SW-169 | PARITY-001 rework: purge/batch ordering guard + a delete-shaped kill test |
| 5 | W0.h | SW-170 | Mixed-directory sweep keyed on (directory, language) — ADR 0008 ruling D9 |
| 6 | W0.g | SW-171 | Legible abstention: JVM named skips in `PackageEvidence`, surfaced in `strict_query` and the trust report |
| 7 | W0.e (1+2) | SW-172 | Make the JVM oracle signature-aware |
| 8 | W0.e (3) | SW-173 | A reproducible compile strategy for the JVM corpus pins |

**Why W0.f-5 first, and why it is not parallelizable.** It moves the measurement
candidate again. Every perf baseline and every real-repo parity number taken
before that move is wasted work, so nothing that produces such a number may run
ahead of it. Wave 0 as a whole is a prerequisite for *every* language wave —
including Go's, whose grandfathering ends in the same programme.

**Honesty note on the package letters.** Grepping the tree at `23da507` finds
only `W0.d`, `W0.e`, `W0.f`, `W0.f-3`, `W0.f-4` and `W0.i` anywhere in the
repository. `W0.f-5`, `W0.g` and `W0.h` are labels from the owner plan, which
exists as no file. Recording them here is precisely why this handoff exists.

**Next free ADR number: 0011.** Verified at `23da507` — `docs/adr/` runs
`0001`–`0010` with no gaps.

---

## 6. The owner-ratified disciplines

The independent architect + product-owner review is
[`../decisions/2026-08-language-ga-independent-review.md`](../decisions/2026-08-language-ga-independent-review.md).
Its ratification header is the authority for D1–D4 below; the rest are the
standing rules the programme runs under. The last column says honestly whether
the rule is enforced or demonstrated by something *in this repository*, or
whether this document is its only record.

| | Discipline | In-repo corroboration |
|---|---|---|
| D1 | **The goal is unchanged: GA for every shipped language**, each at its honestly achievable level. §7's recommendation to *pause* the programme is **OVERRULED**; §5.2's proposal to retire per-language "GA" vocabulary is **declined**. | Review doc, ratification header |
| D2 | **The naming rule.** Every user-facing GA mention of a language carries its capability level beside it — `Java — GA (cross-file-heuristic)`, never bare "GA". | Review doc, ratification header (the reviewers' own mitigation, adopted as a standing rule) |
| D3 | **Every verified fact in review §1, and every soundness/integrity ruling, stands and is adopted unchanged.** The blockers are not a reason to stop; they are Wave 0. Review §3's priority order is Wave 0's order. | Review doc §1, §3 |
| D4 | **Go's grandfathering ends** — Go gets real `GA-LANG-go-*` evidence rows like every other language (review finding S2). | Review doc, ratification header |
| D5 | **Zero-tolerance soundness.** A wrong edge is stop-ship. Green without a demonstrated red-without-fix does not count. Negative results are recorded, never deleted. | Demonstrated: the ADR 0009 measurement's three FAIL rows stand published in [`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md) as the evidence that isolated PARITY-003 |
| D6 | **Published records are never rewritten.** A new dated section goes on top; the old one stays, and nothing is re-pointed. STALE rows are re-marked, never re-pointed. | Demonstrated: [`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md) carries the ADR 0010 measurement above a *superseded* ADR 0009 section; each move record names what it supersedes |
| D7 | **Every product-byte change carries the full ceremony:** its own ADR; a candidate move (`parityreport.CandidateSHA`, the provenance sentence, the tests at `internal/parity/parity_test.go:641,644` with the retired candidate name added to the forbidden list, plus a move record under `docs/decisions/`); a re-measurement of two dispatches with **both** `-verdict-diff` and `-counts-diff` at exit 0; matrix publication; an evidence-index note. | Enforced in part: the forbidden-phrasing test and the built-binary provenance gate (`internal/parity/provenance.go:64-97`) fail closed |
| D8 | **The disclosure contract.** An open defect in a GA operation is disclosed in readme "Known limits" **and** the doctor `known-defects` check (info severity, never silently green), and the disclosure is retracted **only** in the same change that closes the defect. Any published workaround is verified against the real CLI first. | Enforced: `internal/doctor/checks.go:373-400` documents the check's own history as the contract working; `6ea0b5d` is the `-profile full` incident and the standing lesson |
| D9 | **Per-package review is mandatory:** `/code-review` at high effort **plus** an independent adversarial reviewer with package-specific critical questions. Findings are reproduced first-hand, then fixed or documented as a known limit with a stated reason. | Recorded here only — no repo artefact enforces it |
| D10 | **PRs are created only on explicit owner instruction.** When the owner reports a merge, the branch is re-cut from `origin/main`. | Recorded here only |
| D11 | **Language.** Commits, code and repo docs in English; the owner is addressed in German. Model names never appear in commits, PRs or code. | Recorded here only — the repo carries no `CLAUDE.md`/`AGENTS.md`, and `CONTRIBUTING.md` does not state it |

---

## 7. Statements carried as UNVERIFIED EXPECTATIONS

These come from the source plan or from the programme spec. Each is recorded
because losing it would lose real information — and each is marked because this
document did **not** re-verify it. None of them may be read as a measurement.

1. **Dispatch cost.** The real-repo parity recipe is recorded as running at
   roughly three minutes per dispatch. Not re-timed for this document, and it is
   a property of a machine, not of the product.
2. **The standing `surfaces` flake.** Two MCP journey tests are recorded as
   racing a 2 s bind under full parallelism and green in isolation. Not
   reproduced here. Recording it is the discipline; reclassifying it as green
   is not.
3. **The `clauseByDir` user-visible recall loss** (§4) — mechanism read from
   source, consequence not observed.
4. **ADR 0011 as the LINK-001 ADR.** Verified only that `0011` is the next free
   number at `23da507`. That the LINK-001 fix will take it is an intent.
5. **Wave gate definitions.** "Wave 0 closes when the oracle is corpus-scale
   *and* signature-aware green, abstention is visible on CLI and MCP, and two
   dispatches agree at count level" is a statement about a future state, not a
   finding about the current one.
6. **The shipped-language inventory and its capability levels** (22 languages
   across four levels) were measured live from the candidate via
   `graphi trust-report --json`. That measurement belongs to the programme spec
   and was not re-run for this document; it is deliberately not restated here,
   so that no reader can mistake a copy for a re-measurement.

---

## 8. What this document does not touch

Per D6, this document **rewrites no existing published record**. In particular
it modifies no section of
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md) — neither
the current ADR 0010 measurement nor the superseded ADR 0009 one. The change
that adds this file adds only this file, under `docs/`, and touches nothing
under `engine/`, `core/`, `surfaces/` or `cmd/`.

**Measured, so that it is not merely asserted.** `./cmd/graphi` built with
`-trimpath -buildvcs=false` from a pristine worktree at `23da507` and from this
working tree with the new file present produce the **same** sha256,
`2668659c…`. Adding this document therefore moves no product byte, which is the
property that matters.

### A pre-existing finding this measurement surfaced

The same comparison shows something that is **not** caused by this change and
must not be lost:

> **At `23da507` the product tree does NOT byte-match the measurement candidate.**
> `./cmd/graphi` builds to `2668659c…` at `23da507` and to `882881…` at the
> candidate `7574a49`. The path tripwire agrees:
> `git diff --stat 7574a49..23da507 -- engine core surfaces cmd/graphi …
> ':!*_test.go'` reports `cmd/graphi/doctor.go` (+1) and `core/profile/profile.go`
> (+19/−3) — the LINK-001 doctor disclosure and the JVM profile axis, both landed
> by `23da507` *after* the candidate move at `3398d3b`.

Consequence, stated plainly: `CollectProvenance` would set
`ProductDiffEmpty = false` and **a parity dispatch run at `23da507` is not
publishable** (`internal/parity/provenance.go:93-96`). That is the provenance
gate doing its job, not a defect in it. It is also not a new obligation — the
candidate has to move for the LINK-001 fix anyway, and item 2 of §5 (SW-167) is
where it moves. Nothing here re-points a published row: the 19/19 matrix in
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md) was
measured against `7574a49` and stays exactly as published.

## 9. How to re-verify this document

```bash
# §1 — repo state. Count from the VERIFICATION POINT, not from HEAD: §1 is a
# statement about 23da507, and this document's own commit sits on top of it, so
# `origin/main..HEAD` grows by one the moment this file exists — and by more as
# Wave 0 proceeds. That is why the verification point is named rather than implied.
git rev-parse --abbrev-ref HEAD
git rev-list --count origin/main..23da507       # expect 5
git log --oneline origin/main..23da507          # expect the five subjects in §1

# §1–§3 — every commit cited above is reachable from this branch
for s in 23da507 8e7c949 3398d3b 7574a49 6ea0b5d 5285b1d \
         c4209dd 4d032fe 80d67ed d8f1fbb 6848746 9a9a9a2; do
  git merge-base --is-ancestor "$s" HEAD && echo "$s reachable"
done

# §2–§4 — the candidate and the disclosure surfaces
grep -n 'CandidateSHA = ' internal/parityreport/report.go     # :74
grep -n 'LINK-001' readme.md internal/doctor/checks.go
grep -rn 'clauseByDir' .                                       # engine/link/index.go only

# §8 — the change that ADDED this file is confined to docs/.
# NOTE the scope: this is a claim about ONE commit, not about the branch. The
# five commits in §1 are product-byte changes by design and do touch engine/,
# core/, surfaces/ and cmd/ — diffing the whole branch would say nothing.
c=$(git log --format=%H -1 -- docs/plan/2026-08-wave0-handoff-v1.md)
git show --name-only --format= "$c"           # expect: only this file

# §8 — the product-binary comparison parity actually makes
# (internal/parity/provenance.go:64-97). Build ./cmd/graphi at HEAD and at the
# candidate, both with -trimpath -buildvcs=false, and compare sha256.
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/head ./cmd/graphi
git worktree add --detach -f /tmp/cand-wt 7574a49379d3ede0a08bdb024e7a2e315bdc14a1
(cd /tmp/cand-wt && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/cand ./cmd/graphi)
shasum -a 256 /tmp/head /tmp/cand
git worktree remove --force /tmp/cand-wt
# At 23da507 these DIFFER (2668659c… vs 882881…) — see the finding in §8. Adding
# this document does not change /tmp/head; that is the part this story owns.
```
