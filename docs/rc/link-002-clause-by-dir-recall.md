# LINK-002 — the `clauseByDir` last-write-wins recall defect

**Status: OPEN.** Filed, reproduced and disclosed 2026-08-19. **Not fixed here** —
see §7.

| | |
|---|---|
| Id | **LINK-002** |
| Class | **Recall** (edges that should resolve do not). Never soundness — no wrong edge is emitted. |
| Source | [`engine/link/index.go:223`](../../engine/link/index.go) (`idx.clauseByDir[dir] = clause`) |
| Affected operation | Go `recv.Method` call resolution → `callers`, `callees`, `impact`, `neighborhood`, `agent_brief` and every degree-ranked output built on `calls` edges |
| Affected languages | **Go only** (see §3) |
| Affected profiles | **all three** — `fast`, `balanced`, `deep`; measured, §4 |
| Stop-ship? | **No.** The zero-tolerance rule binds *wrong* edges; this drops *true* ones. |
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

Hermetic reproduction: `engine/link/clausebydir_test.go`, six tests, all pinning
the current wrong behaviour. Verified to turn **red with instructions** under a
simulated fix (§7) rather than pass silently.

### End-to-end, through the real CLI

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

On **this repository** (graphi at `a1a8a9a`, 1 576 files parsed, 13 097 nodes),
driving the real `parse.NewDefaultRegistry()` and the real `BuildIndex`, with the
node set sorted by NodeId to reproduce `graphstore.ForEachNode`'s production
streaming order:

| | |
|---|---|
| directories declaring methods | 105 |
| …of those holding **more than one package clause** | **10** |
| method declarations | 1 979 |
| **unreachable through `uniqueMethodInDir`** | **136 (6.9 %)** |

Per directory, worst first — `winner` is the clause that took the last write:

| directory | methods lost | winner | clauses present |
|---|---:|---|---|
| `engine/ingest` | **108** | `ingest_test` | ingest, ingest_test |
| `engine/edit` | 7 | `edit` | edit, edit_test |
| `engine/analysis` | 5 | `analysis` | analysis, analysis_test |
| `engine/search` | 3 | `search_test` | search, search_test |
| `internal/canary` | 3 | `canary` | canary, canary_test |
| `engine/embed/ollama` | 3 | `ollama_test` | ollama (+ a non-method `ollama_test` symbol) |
| `core/parse` | 2 | `parse` | parse, parse_test |
| `engine/query` | 2 | `query` | query, query_test |
| `engine/watch`, `engine/embed`, `engine/trust` | 1 each | — | …, …_test |

`engine/ingest` alone accounts for 108 of the 136: `ingest_test` won, so **every
method declared in `engine/ingest`'s production sources** is invisible to the
recv.Method heuristic.

**The measurement method, so it can be re-run and challenged.** The probe parses
the working tree with the shipped default registry (skipping `.git`, `.graphi`,
`node_modules`, `vendor`), sorts the resulting nodes by `NodeId`, calls
`BuildIndex`, and then tests the exact product predicate
`uniqueMethodInDir(dir, "r", bareName)` for every `method` node. It was
**validated against the CLI first**: run over the §3 fixture it reports
`winner="shop_test", lost=2 of 3`, which is precisely what the CLI produced. The
probe itself is deliberately **not checked in** — it is a measurement tool, not a
gate, and the gate is the hermetic pin.

### NOT measured — stated so no reader infers it

- **No figure on the pinned corpus clones.** cobra, gin and grpc-go were not
  re-indexed for this record; they are not present on this machine and cloning
  them was out of scope. The 6.9 % above is a figure for **one** repository and
  must not be read as a rate for Go repositories in general.
- **No edge-level count on a real repository.** The 136 is a count of
  **unreachable method declarations**, not of lost `calls` edges. How many edges
  each unreachable method would have carried was not counted, and the two numbers
  are not interchangeable.
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
the §3 fixture then resolves **0** of 3 calls instead of 1. `pkggraph` degrades to
avoid a *wrong* answer; here degrading to nothing forfeits a *right* one. The fix
therefore has to make `clauseByDir` hold the directory's **set** of clauses and
have `uniqueMethodInDir` consult all of them, degrading only on a genuine
bare-name collision — which is what `sameDir`/`dirAmbiguous` already do one table
over. All 3 calls in the §3 fixture should then resolve.

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
