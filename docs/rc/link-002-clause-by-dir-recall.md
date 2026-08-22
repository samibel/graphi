# LINK-002 — the `clauseByDir` last-write-wins recall defect

**Status: OPEN.** Filed, reproduced and disclosed 2026-08-19. **Not fixed here** —
see §7.

| | |
|---|---|
| Id | **LINK-002** |
| Class | **Recall *and* soundness.** It drops true `calls` edges (§3.1) **and can substitute a wrong one** (§3.2). Hiding a clause manufactures *false uniqueness* and thereby defeats `receiverMethod`'s own skip-on-ambiguity rule, converting a mandated abstention into a confident wrong edge. Both halves reproduced through the CLI. |
| Source | [`engine/link/index.go:223`](../../engine/link/index.go) (`idx.clauseByDir[dir] = clause`) |
| Affected operation | Go `recv.Method` call resolution → `callers`, `callees`, `impact`, `neighborhood`, `agent_brief` and every degree-ranked output built on `calls` edges |
| Affected languages | **Go only** (see §3) |
| Affected profiles | **all three** — `fast`, `balanced`, `deep`; measured, §4 |
| Stop-ship? | **OPEN — an owner question, not a builder's ruling. See §9.** This row read "No" in the first draft of this record, on the premise that only true edges are dropped. **That premise is false** (§3.2), and D5 ("a wrong edge is stop-ship") is stated unqualified. The corrected facts and the honest mitigation are in §9; the ruling itself is the owner's. |
| Sibling defect | **LINK-003** — a second, larger last-write-wins in the same function (`byClause[clause][dir][bare]`), same false-uniqueness mechanism, ~5× the surface. Filed separately, §10. |
| Verified at | `a1a8a9a` on `claude/kotlin-java-canonical-ga-t3b8km` |
| Pin | [`engine/link/clausebydir_test.go`](../../engine/link/clausebydir_test.go) |

This record follows the shape of the PARITY-00x records in
[`parity-matrix-real-repo.md`](parity-matrix-real-repo.md): mechanism, blast
radius, a reproduction with its real output, what was measured versus what was
not, a workaround executed against the real CLI, and the fix direction.

---

## 1. Mechanism

`SymbolIndex.Add` writes the package clause of every dotted symbol it sees:

```go
// engine/link/index.go:222-224
if clause != "" {
    idx.clauseByDir[dir] = clause
}
```

The assignment is **unconditional**. A directory whose symbols declare more than
one package clause therefore retains only the **last clause the index build
happened to see**, and the losing clause's entry is overwritten with no record
that it existed.

Both consumers read through that single value:

- `Build` seeds the `receiverMethod` reverse index from it — `engine/link/index.go:260`,
  `for dir, clause := range idx.clauseByDir { tbl := idx.byClause[clause][dir] … }`.
  A losing clause's methods never enter `methodDirs` at all.
- `uniqueMethodInDir` reads it directly — `engine/link/index.go:418`,
  `clause := idx.clauseByDir[dir]`, then `idx.byClause[clause][dir]`.

`uniqueMethodInDir` is the **only** gate of `receiverMethod`, and
`receiverMethod` has exactly **one** consumer in the tree:
`engine/link/resolve_go.go:159`, the Go recv.Method call heuristic. A caller asks
`callers` on a method and gets a well-formed, confident, **incomplete** answer —
`outcome: found`, no skip, no diagnostic, no hint that anything was dropped.

### It contradicts a promise the code makes about itself

`BuildIndex`'s own doc comment (`engine/link/index.go:272-274`) reads:

> It is pure and deterministic: identical input **(in any order)** yields an
> index that resolves identically.

It does not. `TestLink002_BuildIndexOrderInvariantBroken` feeds the identical node
set in two orders and gets two disjoint resolved edge sets.

### Why it is nevertheless NOT a parity defect

In production the streaming order is `graphstore.ForEachNode`'s canonical NodeId
order, and a NodeId is a content hash. For a given tree the order — and therefore
the winning clause — is **fixed**. The full and incremental passes agree, so no
parity dispatch can ever surface this, which is exactly why it needed a hermetic
pin rather than a matrix row. Determinism is asserted, not assumed: measured over
five consecutive full `graphi rebuild` runs (§4) and pinned by
`TestLink002_Deterministic`.

---

## 2. Blast radius

**What is affected.** Go `recv.Method` calls whose receiver base is not an import
alias — the heuristic-tier `calls` edges carrying
`reason: "receiver-method call resolved heuristically by unique receiver-name match"`.
Every graph operation that reads `calls` inherits the loss.

**Two distinct effects, not one.** Which of the two a given call site gets depends
on whether the *winning* clause happens to declare a method with the same bare
name as the one being called:

1. **Drop** (§3.1) — the winning clause has no such method, so
   `uniqueMethodInDir` misses and the edge is never emitted. This is the recall
   half, and it is what the 136 / 6.9 % figure in §4 counts.
2. **Substitute** (§3.2) — the winning clause *does* declare that bare name, on
   an unrelated type. `uniqueMethodInDir` then returns **that** node, and the
   edge is emitted pointing at the **wrong** declaration. This is a soundness
   consequence and it is **not** counted by the 136.

The second effect is the more serious one and it was **missed in this record's
first draft**, which asserted "never soundness — no wrong edge is emitted" on all
five disclosure surfaces. That assertion was wrong; §3.2 is its refutation, and
§9 re-examines the stop-ship ruling that rested on it.

**What is NOT affected**, pinned by `TestLink002_ConfinedToReceiverMethod`:
`clauseByDir` has exactly two readers, and every other directory→package table
is keyed on `byClause`, `byDir` or `fileNodesByDir` instead. So cross-package
selector resolution (`crossPackage`), same-package resolution (`sameDir`),
`imports` targeting (`packageFileNodes`), package presence (`hasPackage`) and the
reverse-dependency translation (`DirsForImport`) are all untouched by the
collision. `references` and `imports` edges are unaffected; so is `search`.

**Which directories collide.** Any directory whose indexed symbols declare two
package clauses. The two shapes that actually occur:

1. **A Go external test package.** `foo/foo.go` declaring `package foo` beside
   `foo/foo_test.go` declaring `package foo_test`. Legal, idiomatic, and the
   dominant cause — it is what the fixture and every measured collision below use.
2. **A mixed-language directory.** Non-Go extractors derive their clause from
   `langPackage` (`core/parse/parser_tswalk.go:240`) — the directory basename, or
   the file stem at the repository root — so a Python, Ruby, JavaScript or Lua
   file sitting beside Go sources can write a different clause into the same
   directory key. This overlaps the mixed-directory work already scheduled
   separately (ADR 0008 ruling D9).

**A single symbol is enough to destroy the directory.** `clauseByDir` is written
by *any* dotted symbol, not only by methods. In the measured run below,
`engine/embed/ollama` declares **no** methods under `ollama_test`, yet
`ollama_test` won the last write and all **3** real methods in that directory
became unreachable — pure loss, zero gain.

---

## 3. Reproduction

Hermetic reproduction: `engine/link/clausebydir_test.go`, **ten** tests. Which of
them a fix is expected to turn red is stated here rather than left to be inferred,
and it was verified by running a simulated fix (§7):

- **Six pin LINK-002's wrong behaviour** and turn **red with instructions**
  rather than passing silently. Re-verified after the substitution pin was added:
  under the simulated fix all six fail, the redirect pin among them.
- **`TestLink003_BareNameShadowing`** pins the *sibling* defect (§10) and
  correctly stays **green** under a LINK-002-only fix — measured, not assumed.
  That is the executable form of §7's warning: a fix that closes LINK-002 alone
  leaves this pin green and the larger defect standing.
- **`TestLink002_ConfinedToReceiverMethod`** is a *blast-radius* pin: it asserts
  what the collision does **not** reach, so a correct fix leaves it **green**.
- **`TestLink002_RedirectIsCausedByTheCollision`** is the substitution's
  counterfactual — it asserts correct resolution when no collision exists, and
  stays **green**.
- **`TestLink002_ResolverAbstainsWhenItCanSeeBoth`** pins the *correct* behaviour
  LINK-002 defeats (the frozen skip-on-ambiguity rule) and must stay **green**
  through the fix. It is the mechanism argument in §3.2, made executable.

### 3.1 The drop — end-to-end, through the real CLI

Fixture — the commonest Go shape there is:

```
go.mod                  module example.com/repro
shop/cart.go            package shop       -> method Cart.Add, method Cart.Total
shop/cart_test.go       package shop_test  -> method Fixture.Reset
app/main.go             package main       -> run() calls c.Add(), c.Total(), f.Reset()
```

```console
$ graphi rebuild .
$ graphi callees d7195f0ee248e41d      # main.run
{"operation":"callees","symbol":"d7195f0ee248e41d","outcome":"found",
 "nodes":[{"id":"567849a4995318d1","kind":"method",
           "qualified_name":"shop_test.Fixture.Reset",
           "source_path":"shop/cart_test.go","line":7,"column":19}],
 "edges":[{"id":"edc31dd2ead00d54","from":"d7195f0ee248e41d","to":"567849a4995318d1",
           "kind":"calls","confidence_tier":"heuristic","confidence":0.6,
           "reason":"receiver-method call resolved heuristically by unique receiver-name match",
           "evidence":["app/main.go:11"]}]}
```

**Three recv.Method calls; one edge.** `clauseByDir["shop"]` settled on
`shop_test`, so the two calls into the **production** methods `shop.Cart.Add` and
`shop.Cart.Total` were dropped, and the only surviving edge is the one into the
**test** fixture. The outcome reads `found`. Nothing tells the user two thirds of
the answer is missing.

### Counterfactual — the losing half, isolated

Delete `shop/cart_test.go` and change nothing else:

```console
$ graphi callees d7195f0ee248e41d
… "qualified_name":"shop.Cart.Add"   … "reason":"receiver-method call resolved heuristically…"
… "qualified_name":"shop.Cart.Total" … "reason":"receiver-method call resolved heuristically…"
```

Both production edges return. **Adding one `_test.go` file with an external test
package silently deletes two true `calls` edges from an unrelated package.**

### 3.2 The substitution — LINK-002 redirects an edge to the wrong declaration

**This is the half the first draft of this record denied.** It is reproduced here
against the built CLI, and the ground truth is unambiguous by construction.

Fixture — the same directory shape, but the two clauses now declare the **same
bare method name**, and the caller's receiver is explicitly typed:

```
go.mod              module example.com/redirect
shop/cart.go        package shop       -> type Cart, method Cart.Reset
shop/cart_test.go   package shop_test  -> type Fixture, method Fixture.Reset
app/main.go         package main       -> func run(c *shop.Cart) { c.Reset() }
```

`c` **is** a `*shop.Cart`. The only correct target is `shop.Cart.Reset`.
`-profile fast` is used to isolate the heuristic, because it skips the
type-resolution pass and so leaves the heuristic edge as the *only* edge:

```console
--- WITH the collision, -profile fast ---
$ graphi query callees -symbol d7195f0ee248e41d      # main.run
{"operation":"callees","symbol":"d7195f0ee248e41d","outcome":"found",
 "nodes":[{"id":"567849a4995318d1","kind":"method",
           "qualified_name":"shop_test.Fixture.Reset",
           "source_path":"shop/cart_test.go","line":5,"column":19}],
 "edges":[{"id":"edc31dd2ead00d54","from":"d7195f0ee248e41d","to":"567849a4995318d1",
           "kind":"calls","confidence_tier":"heuristic","confidence":0.6,
           "reason":"receiver-method call resolved heuristically by unique receiver-name match",
           "evidence":["app/main.go:5"]}]}

--- WITHOUT the collision (cart_test.go removed), -profile fast ---
 "nodes":[{"id":"5095b88cc75941e3","kind":"method",
           "qualified_name":"shop.Cart.Reset",
           "source_path":"shop/cart.go","line":5,"column":16}],
 "edges":[{… "to":"5095b88cc75941e3","kind":"calls","confidence_tier":"heuristic" …}]
```

**Same call site, same edge slot, different target.** The collision did not drop
the edge — it **redirected** it, to a different method, on a different type, in a
different package, in a test file. `outcome: found`. No skip, no diagnostic.

Under `-profile balanced` and `-profile deep` the go/types pass additionally
emits the *correct* `confirmed`-tier edge, so the user sees **both**: a correct
`confirmed` edge to `shop.Cart.Reset` **and** a wrong `heuristic` edge to
`shop_test.Fixture.Reset`. The wrong edge is emitted on every profile; on `fast`
it is the only edge there is.

#### Why this is LINK-002 and not the heuristic merely being loose

`receiverMethod`'s frozen rule (`engine/link/index.go:415-417`) is: *resolve ONLY
on a unique receiver-name match; skip deterministically on ambiguity (>1 distinct
NodeId) or a miss.* With both `Reset` declarations visible the resolver is
**required to abstain**. That rule is live, and was verified rather than assumed
— the same two methods placed in two *different* directories, where LINK-002
hides neither, produce exactly the mandated abstention:

```console
# pkg/a.go: package pkg,   func (a *A) String()
# other/b.go: package other, func (b *B) String()
# app/main.go: func run(a *pkg.A) string { return a.String() }
$ graphi query callees -symbol d7195f0ee248e41d      # -profile fast
{"operation":"callees","symbol":"d7195f0ee248e41d","outcome":"empty","nodes":[],"edges":[]}
```

So the resolver abstains when it can see both candidates, and emits a confident
wrong edge when LINK-002 hides one of them. **LINK-002 manufactures false
uniqueness and converts a mandated abstention into a wrong edge.** That is a
soundness consequence, not a recall one.

---

## 4. What was measured, and what was not

### Measured — determinism

Five consecutive `graphi rebuild` runs over the fixture produced **byte-identical**
`callees` JSON, including the edge id. Deterministic, as claimed.

### Measured — profiles

The loss reproduces identically under `-profile fast`, `-profile balanced` and
`-profile deep` (the complete set the CLI accepts; `graphi rebuild -profile bogus`
answers `invalid profile "bogus": must be one of fast|balanced|deep`).

### Measured — recall loss on a real repository

On **this repository** (graphi at `a1a8a9a`), read from the **real committed
graphstore** and streamed through `graphstore.ForEachNode` into
`link.NewIndexBuilder()` — byte-for-byte the code path production uses at
`engine/ingest/linkfiles.go:72-79`. **13 620 committed nodes scanned**, and the
observed scan order was asserted to be ascending NodeId (it is), so this is
production's actual streaming order and not a re-parse approximation.

| | |
|---|---:|
| directories declaring methods | 105 |
| method declarations | 1 979 |
| **unreachable through `uniqueMethodInDir`** | **136 (6.9 %)** |

**Three different directory counts, each stated with the words that describe
it.** The first draft of this record published a single number — "10 of 105
directories … holding more than one package clause" — which is **mislabeled**:
10 is not the count of directories holding more than one package clause, and the
record's own per-directory table below lists **11** lossy directories, so the
page contradicted itself. All three quantities are distinct, all three are real,
and all three are given here:

| what is counted (all restricted to the 105 method-declaring directories) | count |
|---|---:|
| (a) directories holding **more than one package clause**, written by *any* dotted symbol | **21** |
| (b) directories where **more than one clause declares methods** | **10** |
| (c) directories that **actually lose at least one method** today | **11** |

**(a) is the blast-radius number and it is the largest.** It is the right notion
for the words "holding more than one package clause", because `clauseByDir` is
written by *any* dotted symbol, not only by methods — a single non-method symbol
under a second clause is enough to take the last write and destroy the
directory. (a) > (c) because in the other 10 directories the clause that won
happened to be the one declaring the methods; those directories are **one commit
away** from losing methods, with no code change to the methods themselves.

**(b) is what the first draft measured** and it is the narrowest of the three;
publishing it under (a)'s label **understated** the exposure by more than half.

**(c) = 11 is why (b) = 10 was self-contradictory on its own page.**
`engine/embed/ollama` is exactly the gap: it declares methods under **one** clause
only (`ollama`), so it is not in (b) — yet a non-method `ollama_test` symbol took
the last write and all **3** of its methods are lost, so it is in (a) and in (c).

Per directory, worst first — `winner` is the clause that took the last write.
This table is the (c) = **11** list:

| directory | methods lost | winner | clauses present |
|---|---:|---|---|
| `engine/ingest` | **108** | `ingest_test` | ingest, ingest_test |
| `engine/edit` | 7 | `edit` | edit, edit_test |
| `engine/analysis` | 5 | `analysis` | analysis, analysis_test |
| `engine/embed/ollama` | 3 | `ollama_test` | ollama, ollama_test (the latter declares **no** methods) |
| `engine/search` | 3 | `search_test` | search, search_test |
| `internal/canary` | 3 | `canary` | canary, canary_test |
| `core/parse` | 2 | `parse` | parse, parse_test |
| `engine/query` | 2 | `query` | query, query_test |
| `engine/embed` | 1 | `embed` | embed, embed_test |
| `engine/trust` | 1 | `trust` | trust, trust_test |
| `engine/watch` | 1 | `watch` | watch, watch_test |

`engine/ingest` alone accounts for 108 of the 136: `ingest_test` won, so **every
method declared in `engine/ingest`'s production sources** is invisible to the
recv.Method heuristic.

**The measurement method, so it can be re-run and challenged.** The probe opens
the committed store read-only, streams every node through
`graphstore.ForEachNode` into `link.NewIndexBuilder().Add` (the production call
site), asserts the observed order is ascending NodeId, calls `Build`, and then
tests the exact product predicate `uniqueMethodInDir(dir, recv, bareName)` for
every `method` node — recording separately whether it **misses** (LINK-002, the
136) and whether it **returns a different node than the one asked about**
(LINK-003, §10). The probe is deliberately **not checked in** — it is a
measurement tool, not a gate, and the gate is the hermetic pin.

### NOT measured — stated so no reader infers it

- **No figure on the pinned corpus clones.** cobra, gin and grpc-go were not
  re-indexed for this record; they are not present on this machine and cloning
  them was out of scope. The 6.9 % above is a figure for **one** repository and
  must not be read as a rate for Go repositories in general.
- **No edge-level count on a real repository.** The 136 is a count of
  **unreachable method declarations**, not of lost `calls` edges. How many edges
  each unreachable method would have carried was not counted, and the two numbers
  are not interchangeable.
- **No count of the SUBSTITUTION case (§3.2) on a real repository.** The 136
  counts only the **misses**. How many call sites on this tree receive a
  *redirected* heuristic edge rather than none was **not** measured — that needs
  a call-site-level count, not a declaration-level one, and it was not done.
  The substitution is therefore demonstrated (§3.2, through the CLI) but
  **unquantified**, and nothing here should be read as a bound on it. Note the
  direction of the ignorance: the 136 is not an upper bound on the harm.
- **No count of directories affected by the mixed-language shape** (§2 case 2).
  Every collision in the table above is the Go `_test` shape. The mixed-language
  shape is demonstrated by mechanism (`langPackage`), not by a measurement.
- **No claim about which clause wins on any other tree.** The winner is a content
  hash ordering; it is stable per tree and unpredictable across trees.

---

## 5. Interaction with LINK-001 / ADR 0011 (SW-166)

Both defects live in `engine/link/index.go` and both concern directory→package
resolution, so the interaction was checked rather than assumed.

**They do not share a read path.** ADR 0011 fixed LINK-001 by giving
`packageFileNodes` a read-time `packageFileFilter`, which narrows
**`fileNodesByDir`**. LINK-002 corrupts **`clauseByDir`**. The two maps have
disjoint consumers, pinned by `TestLink002_ConfinedToReceiverMethod`. The ADR 0011
fix therefore neither causes, worsens nor mitigates LINK-002 — and LINK-002 was
not introduced by it; it predates both.

**But they are the same principle, half-applied.** ADR 0011 ruled explicitly that
a `_test.go` file "is a package member but is **not importable**", and excluded it
from `imports` targets (`engine/link/resolve_go.go:19-42`,
`docs/adr/0011-…md:145-167`). LINK-002 is that same ruling's unfinished other
half: on the `receiverMethod` path a `_test.go` file's symbols are still allowed
to define what package a directory *is*, and in the §3 fixture they win. The fix
in §7 should be written knowing ADR 0011 already decided the underlying question.

**One shared consequence worth naming.** ADR 0011's filter is applied at *read*
time precisely because narrowing the index at `Add` time would under-approximate
and freeze edges (the ADR 0009 defect class). LINK-002 is the mirror image: a
value that *is* narrowed at `Add` time — to one clause — and under-approximates
exactly as that doctrine predicts.

---

## 6. Workaround — executed against the real CLI first

AC-6 exists because of the `-profile full` incident, where a published workaround
named a profile `profile.Parse` rejects. Both statements below were run.

**Give the receiver a syntactically known, import-qualified type.** Where a
call's receiver is typed `*shop.Cart` rather than an interface or an inferred
local, the Go **type-checker** resolves it and the heuristic is never consulted:

```console
# same repository, shop/cart_test.go still present, collision still active
$ graphi rebuild -profile balanced .
$ graphi callees d7195f0ee248e41d
… "qualified_name":"shop.Cart.Add",   … "confidence_tier":"confirmed","confidence":1,
  "reason":"call target resolved by the go/types type-checker"
… "qualified_name":"shop.Cart.Total", … "confidence_tier":"confirmed","confidence":1,
  "reason":"call target resolved by the go/types type-checker"
```

**Its limit, measured, not reasoned.** This holds under `-profile balanced` (the
default) and `-profile deep`. It does **not** hold under `-profile fast`, which
skips the typeresolve pass: the same query there answers
`{"operation":"callees",…,"outcome":"empty","nodes":[],"edges":[]}`. A user on
`fast` has no workaround on this path.

**What does not work, so nobody tries it.** Reading `confidence_tier` does not
help: the defect removes edges, and a removed edge has no tier to inspect.
Re-indexing does not help: the outcome is deterministic (§4).

---

## 7. Fix direction — and why it is not applied here

**Direction.** The honest rule already exists in this codebase:
`engine/typeresolve/pkggraph.go:132-144` **degrades** when a directory is
ambiguous instead of picking a winner. `clauseByDir` should do the same in
spirit — stop pretending a directory has one clause.

**One thing the fix must get right, learned by running it.** A literal transcription
of the `pkggraph` rule — drop the clause once the directory is ambiguous — was
applied experimentally against the pin and makes recall **worse**, not better:
the §3.1 fixture then resolves **0** of 3 calls instead of 1. `pkggraph` degrades to
avoid a *wrong* answer; here degrading to nothing forfeits a *right* one. The fix
therefore has to make `clauseByDir` hold the directory's **set** of clauses and
have `uniqueMethodInDir` consult all of them, degrading only on a genuine
bare-name collision — which is what `sameDir`/`dirAmbiguous` already do one table
over. All 3 calls in the §3.1 fixture should then resolve, and the §3.2 fixture
should **abstain** (two distinct `Reset` nodes become visible in one directory,
which is exactly the ambiguity `receiverMethod`'s frozen rule already mandates
skipping on) instead of emitting the wrong edge it emits today.

**The fix must be scoped against LINK-003 as well (§10).** A fix that only makes
`clauseByDir` hold a set still leaves `byClause[clause][dir][bare]` a
last-write-wins map with no `dirAmbiguous` companion, so `x.String()` would still
resolve to an arbitrary `String()`. The two defects share one mechanism —
manufactured false uniqueness — and a fix story that closes one and not the other
will read as a fix while the larger half survives.

**Why it is not applied in this change.** It is a **product-byte change**. Under
D7 it therefore carries its own ADR, a candidate move
(`parityreport.CandidateSHA`, the provenance sentence, the forbidden-phrasing
test, a move record under `docs/decisions/`), a re-measurement of two dispatches
with both `-verdict-diff` and `-counts-diff` at exit 0, matrix publication and an
evidence-index note. That ceremony is a scheduled item of its own, and the
disclosure contract (D8) says an open defect is disclosed at the point it is
reproduced, not at the point it is fixed. Hence this record.

**Expected direction of the fix's effect on the matrix:** `calls` edges **up**,
node counts unchanged. Unlike ADR 0011, which was a loss by design, this is a
gain by design — which also means the current 19/19 PASS is no evidence against
it: parity compares two passes of the same rule and cannot see a rule that under-
resolves consistently.

---

## 8. Disclosure

Per D8, live from this change until the change that closes the defect:

- `readme.md`, "Known limits" — the user-facing bullet.
- `internal/doctor/checks.go`, the `known-defects` check at **info** severity,
  registered in `cmd/graphi/doctor.go` and asserted by
  `internal/doctor/checks_test.go`.
- `docs/language-support.md`, beside the Go GA row.
- `docs/plan/2026-08-wave0-handoff-v1.md` carried this as an **UNVERIFIED
  EXPECTATION** (§7 item 3) and recorded it as undisclosed (§4). That published
  record is corrected by a **dated amendment**, per D6 — the original text stays.

Every one of those surfaces asserted, in this record's first draft, that LINK-002
"drops true edges only and never emits a wrong one". **All four have been
corrected** to carry §3.2's finding instead. LINK-003 (§10) is disclosed on the
same surfaces.

---

## 9. The stop-ship ruling, re-examined on the corrected facts

**Escalated. This is an owner decision and it is deliberately left open here.**

**What changed.** The header row of this record originally read
`Stop-ship? **No.** The zero-tolerance rule binds *wrong* edges; this drops
*true* ones.` That ruling had exactly one premise, and §3.2 falsifies it:
LINK-002 **does** emit a wrong edge. A ruling whose sole premise is false cannot
stand on its own reasoning, whatever the right answer turns out to be. The
builder is not entitled to re-derive a `No` and is not asserting a `Yes`; the
corrected facts are laid out and the ruling is handed back.

**The rule as written.** D6/D5 in `docs/plan/2026-08-wave0-handoff-v1.md:342`:
> **D5 — Zero-tolerance soundness.** A wrong edge is stop-ship.

Stated unqualified. Read literally against §3.2, LINK-002 is stop-ship.

**The facts that argue for a narrower reading — the honest mitigation.** These
are stated because withholding them would be as dishonest as the original
over-claim, not because they settle anything:

1. **The wrong edge is confined to the `heuristic` tier**, confidence **0.6**,
   carrying `reason: "receiver-method call resolved heuristically by unique
   receiver-name match"`. The tier exists precisely to mark an edge as
   unproven, and it is machine-readable on every operation that emits it.
2. **No `confirmed`-tier edge is affected.** Under `-profile balanced` (the
   default) and `-profile deep`, go/types independently emits the **correct**
   `confirmed` edge alongside the wrong heuristic one, so a consumer that filters
   on tier sees only the truth. Verified in §3.2.
3. **`-profile fast` has no such cover.** There the wrong edge is the *only* edge
   the user gets, and it is indistinguishable from a correct heuristic edge. This
   is the sharp case, and it is the one that makes mitigation 2 incomplete rather
   than sufficient.
4. **The defect predates this branch** and is not a regression introduced by any
   recent change; it has been live for as long as `clauseByDir` has existed.
5. **The substitution's frequency is unmeasured** (§4, NOT-measured list). That
   cuts both ways and must not be read as "rare".

**The question the owner is actually being asked.** Does D5's "a wrong edge is
stop-ship" bind a **`heuristic`-tier** edge, or only edges the product presents
as proven? That question is not answered anywhere in the repo, it is not a
builder's to answer, and the answer governs PARITY/LINK defects beyond this one.
If D5 binds every tier, LINK-002 is stop-ship today and the fix cannot wait for
its scheduled slot. If D5 binds only `confirmed`-tier edges, the ruling can stay
`No` — but it then needs to be **re-derived on that basis and written down**,
because the reasoning currently in the repo is the false one.

**Recorded, not resolved.** Per this record's own rule and D6, the original
wording is not being quietly replaced with a better-argued `No`. The row now
reads OPEN, and this section is what the owner rules on.

---

## 10. LINK-003 — a second, larger last-write-wins in the same function

**Filed separately; not fixed here and not in SW-168's scope.** Full entry:
`projects/graphi/backlog.md`, dated 2026-08-19.

`Add` writes **two** maps unconditionally, not one. Alongside
`idx.clauseByDir[dir] = clause` (LINK-002) it also writes, at
`engine/link/index.go:272`:

```go
idx.byClause[clause][dir][bare] = n.ID()
```

Two methods sharing a **bare name** in one package — `func (a *A) String()` and
`func (b *B) String()` — therefore overwrite each other. Unlike `byDir`, which
has a `dirAmbiguous` companion that `sameDir` consults, `byClause` has **no**
ambiguity companion at all, so `uniqueMethodInDir` cannot see the collision and
returns the survivor with full confidence. It is the **same false-uniqueness
mechanism** as §3.2, one table over, and it needs no second package clause.

Reproduced through the CLI, `-profile fast`, single package, single directory:

```console
# pkg/a.go: package pkg, func (a *A) String() string
# pkg/b.go: package pkg, func (b *B) String() string
# app/main.go: func run(a *pkg.A) string { return a.String() }
$ graphi query callees -symbol d7195f0ee248e41d
… "qualified_name":"pkg.B.String","source_path":"pkg/b.go" …
  "kind":"calls","confidence_tier":"heuristic","confidence":0.6
```

`a` is a `*pkg.A`. The edge points at `pkg.B.String`. **A wrong edge, with no
package-clause collision involved.**

**Measured on this repository**, same probe and same run as §4:

| | |
|---|---:|
| method declarations | 1 979 |
| unreachable — LINK-002 alone (`uniqueMethodInDir` misses) | 136 (6.9 %) |
| shadowed only — LINK-003 alone (resolves, but to a **different** node) | 527 (26.6 %) |
| **unreachable OR shadowed — both defects** | **663 (33.5 %)** |

Worst directories by shadowing: `core/parse` **188**, `engine/analysis` 60,
`core/graphstore` 47, `surfaces/client` 47, `engine/link` 26. A real instance:
`core/parse` declares `func (p *forestParser) Language()` (`broad.go:79`) and
`func (e *forestExtractor) Language()` (`broad.go:190`) in one package; one of
them is unreachable through this path and every `x.Language()` call resolves to
the survivor. `core/parse` also declares **25** distinct `Parse` methods and
**21** distinct `Extract` methods, all competing for one map slot.

**Roughly 5× LINK-002's surface, and it was not disclosed anywhere before this
record.** It is filed rather than fixed for the same reason LINK-002 is: the fix
is a product-byte change carrying the full D7 ceremony. §7 states why the eventual
fix story must be scoped against both.
