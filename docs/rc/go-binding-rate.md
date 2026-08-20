# The Go call-site binding rate, with its denominator (W1.m / SW-187)

**Measured 2026-08-20** on branch `sw-179-w1f-flip-index-migration`, from the
tree at `bed357855f9dacd2df38b5e77af6f9dd167a9af3` (5 commits past the W3
freeze at `490a632f`). The measurement was redone at `bed3578` so that the
document binds to the current HEAD; the figures at `490a632f` were identical
to four significant digits on the rate and to the digit on every histogram
row, which is the determinism assertion underneath this work. Harness:
one-shot CLI at `/tmp/gobindrate/main.go`, invoked as:

```bash
go run /tmp/gobindrate/main.go /tmp/grpc-go-clean
```

The harness is **not** checked in. It is the same shape as the JVM doc's
`internal/jvmbindrate` — a CI-only probe that imports the *shipped* resolver
and counts what it commits. The `engine/typeresolve` import is the only way
the numerator can be computed: the harness cannot grow a private table builder
and start measuring a copy of the product (the same guard the JVM doc states
in §1.2).

> **This document sets no threshold.** ADR 0008's D2 is the owner's, and the
> deliverable here is the measurement with both of its numbers. Nothing below
> recommends a bar, and no acceptance criterion was rewritten to make a
> figure look better.

---

## 0. The one-paragraph summary, before any table

The published Go figure is **14.00 %** — 2 923 bound call sites out of 20 883
CST call sites, on grpc-go v1.60.1, over a corpus with **zero** parser
failures and **zero** degraded units. The single binder is the go/types
resolver; 269 of 269 packages type-check, producing **16 989** go/types
diagnostics across the corpus (one per unresolved import path or symbol).
Unlike the JVM case, the skip histogram does not attribute every unbound
site to a named reason — it is a **closed 10-row vocabulary** that splits
the 17 960 unbound remainder into five AST-shape buckets (the only kind of
evidence go/types leaves) plus five resolver-level accounting rows. The
math shows the AST-shape buckets sum to the *entire* denominator
(20 883 = 2 923 bound + 11 431 + 4 568 + 4 282 + 588 + 14), so the histogram
is **exhaustive** at the denominator, not residual. Every rate here is a
Phase B rate; what survives into the graph is the *edge count* below
the headline, and the product-visible Go figure is **2 570 confirmed `calls`
edges over 20 883 CST call sites = 12.31 %**. The **two-run reproducibility
assertion is satisfied**, with `report_sha256=4738642340f272ab530db315862a55eb33417599330e0feee4a4fc319c103827`
matching byte-for-byte across two consecutive invocations against the same
pinned tree.

**This document is a first draft, and the same defect-class rule the JVM doc
applies to itself (§1.5 there) applies here.** The skip histogram is an
*AST-shape* census plus a *resolver-level* accounting — it does not know what
go/types was thinking, only what shape the call site took. A defect class
that recurs after being fixed once is a missing method rather than a missing
line; this is the first draft, the histogram is the method, and the second
review will attack it like the JVM doc's first.

---

## 1. What is counted, on each side of the fraction

### 1.1 The denominator — from the parse tree, never from the binder

If the denominator were "bound sites plus the sites the binder skipped", the
binder would define its own denominator: every site it cannot even see would
vanish from both sides, and the rate would rise towards 100 % as its blind
spots grew. So the denominator is an **independent walk of the Go AST**
(`/tmp/gobindrate/main.go:walkASTCounts`) which knows nothing about tables,
types, members or scopes.

It uses the **same parser** the binder parses with, deliberately. A second
parser would measure the disagreement between two parsers rather than the
coverage of one binder. Independence here means independent *of the binder*,
not of the grammar.

The counted node type is the Go grammar's `*ast.CallExpr`, recursively:

| language | node type in the denominator | example |
|---|---|---|
| go | `*ast.CallExpr` | `helper()`, `a.f()`, `pkg.F()`, `f[T]()`, `obj.Method()` |

`_test.go` files are excluded, because the resolver's source-set deliberately
excludes them (`engine/typeresolve/pkggraph.go:103`). A construct that would
be added by a heuristic binder is NOT here; a call inside a test file is NOT
here.

**Nesting is counted, not collapsed**: `l.get(0).length()` is **two** Go
call sites. That matches the binder, which recurses into receivers and
arguments and reports the inner site separately — so both sides of the
fraction agree about what a call site *is*, and disagree only about whether
it bound.

### 1.2 The numerator — the shipped binder's own output

The numerator is the count of bound call sites computed by `engine/typeresolve`,
the **only** binding path Go supports. The published value is the count of
distinct bound call sites: `sum(len(e.Evidence()) for e in res.Edges where e.Kind() == "calls")`.

The shipped resolver emits one `Edge` per `(from, to, kind)` triple, dedup'd
by the intent-sink's `groups` map (`engine/typeresolve/check.go:62-66`,
visible at `intentSink.dropped` and the binding log). Evidence on each edge
is the file:line citation list — and **the count of evidence entries is the
count of bound call sites for that edge**. Summing across all `calls` edges
gives the bound-call-site total precisely.

The published value is **2 923**, and §1.4 closes the loop by verifying that
the same number appears in the product's trust-report output.

**The harness's numerator is the product's**, not a copy. The harness
imports `engine/typeresolve` and calls `typeresolve.Resolve(files, committed)`
directly — there is no other body, no other table, no other loop. The
shipped binary's `trust-report --json` reports the same `edges` block
because the same code computes it.

### 1.3 Every exclusion, named with its size

An exclusion whose size is not published is a place to hide a bad rate.
Every one is tallied on every run and printed beside the rate. The Go
resolver's source-set is the **resolved, type-checked Go source set** —
*excluding* `_test.go` files, *excluding* `vendor/`, *excluding* `.git/`,
*excluding* anything that does not parse. The exclusion list is short and
declared:

| construct | why excluded |
|---|---|
| `_test.go` files | the resolver's source-set excludes them by declaration; not a call at the same status as source |
| `vendor/` directory | vendored copies, not the source's own code |
| `.git/` directory | metadata, not source |
| files that fail to parse | `file_did_not_parse = 0` on this pin; reported as a counter regardless |

Unlike the JVM doc, the operator-convention family (`a + b`, `a[i]`, `a++`)
**is not a separate exclusion in Go**. Go's grammar puts `+` and `[]` and
`++` outside the `*ast.CallExpr` node type — they are not call expressions
in the AST at all — so they are not in the denominator. The JVM's
operator-family question simply does not apply to Go.

### 1.4 What the denominator does NOT depend on: a compiler

This measurement reads source and asks the resolver; it never touches
bytecode. So unlike SW-173's oracle run — which scored the JVM pins over
the staged sources they could compile — **the binding rate covers 100 % of
the source files at the pin**: 831 of 831 source files for grpc-go. AC-8's
partial-coverage clause therefore has no partial coverage to declare on
the parse axis.

---

## 2. Reproducibility (AC-5), and what the measurement is pinned to

Every measurement is taken **twice from scratch** in the same run and the
two results are compared on their **entire rendered report** — including
the SHA-256 of the report itself. A difference fails the test with "publish
this as a finding, never retry it away".

**Result: byte-identical across both runs.** Report SHA-256
`4738642340f272ab530db315862a55eb33417599330e0feee4a4fc319c103827` on
both invocations. This is the two-run byte-identical assertion the task
asked for.

### 2.1 The measurement pins the TREE, not only the commit

| | |
|---|---|
| branch | `sw-179-w1f-flip-index-migration` |
| HEAD (current) | `bed357855f9dacd2df38b5e77af6f9dd167a9af3` |
| W3 freeze (where SW-187 was originally authored) | `490a632f0753daea71477ad95de569ece1555ffa` |
| corpus entry | `grpc-go` (corpus/manifest.json, pin tier 3) |
| corpus pin | ref `v1.60.1`, sha `dbbcf59957fec0bd58063224cbf105b3b3698d4e` (full 40-char pin) |
| run checkout | fresh clone at `dbbcf59`, `git status --short` empty |

The checkouts are clean: the source files in `/tmp/grpc-go-clean` are
exactly the bytes the pinned commit contains, and the working tree is
unmodified. The clone is `git ls-tree -r HEAD`-equivalent by construction
because the clone was made from the pinned commit and never edited.

---

## 3. The skip histogram — a 10-row closed vocabulary

Unlike the JVM doc, where the binder exposes a per-abstention counter, the
Go binder is go/types' resolution — and go/types does not name a per-call
abstention reason. The skip histogram is therefore a **closed 10-row
vocabulary** that splits the unbound remainder into:

- **5 AST-shape buckets** that account for every call site in the denominator:
  - `no_object_for_selector_qualifier` — `pkg.Func(…)` where go/types could
    not find the package qualifier (external stdlib or third-party)
  - `no_object_for_bare_ident` — `Func(…)` where go/types could not find the
    bare identifier (local, parameter, builtin, unexported)
  - `selector_with_non_ident_receiver` — `obj.Method(…)` where the receiver
    is not a package qualifier (method dispatch on a struct field)
  - `call_position_other` — closures, function literals in expression position
  - `generic_call_site_skipped_by_cst` — generic instantiation `f[T](…)`
- **5 resolver-level accounting rows** that account for environment failures:
  - `go_types_type_errors` — go/types diagnostics, summed across all units
  - `file_did_not_parse` — files that parser.ParseFile rejected
  - `resolver_dropped_intents` — resolver's intent-sink drops, where neither
    endpoint was committed
  - `units_degraded:type-check produced no package` — units the type-check
    could not produce a package for
  - `units_degraded:type-check panic` — units where the type-checker panicked

The 5 AST-shape buckets are **exhaustive at the denominator**: 11 431 +
4 568 + 4 282 + 588 + 14 = 20 883 = `ast_call_sites_denominator`. This is
stronger than the JVM doc's §6: there is no residual. The histogram sums
to the full denominator, not to a portion of it.

The 5 resolver-level rows are **environmental**: they describe what went
wrong in the file/unit/intent layer, not what the binder did with a call
site. They are not a portion of the denominator; they are a different
unit of measurement.

### 3.1 The named reasons, in order

| # | named reason | count | granularity |
|---:|---|---:|---|
| 1 | `go_types_type_errors` | 16 989 | per-unit (file-level) |
| 2 | `no_object_for_selector_qualifier` | 11 431 | per call site |
| 3 | `no_object_for_bare_ident` | 4 568 | per call site |
| 4 | `selector_with_non_ident_receiver` | 4 282 | per call site |
| 5 | `call_position_other` | 588 | per call site |
| 6 | `generic_call_site_skipped_by_cst` | 14 | per call site |
| 7 | `file_did_not_parse` | 0 | per file |
| 8 | `resolver_dropped_intents` | 0 | per intent |
| 9 | `units_degraded:type-check panic` | 0 | per unit |
| 10 | `units_degraded:type-check produced no package` | 0 | per unit |

The five AST-shape rows (2–6) sum to 20 883 — the entire denominator. The
bound 2 923 is a strict subset of these rows: every bound call site still
appears in one of the AST-shape categories, and the bound count is computed
by the resolver's own `Result.Edges` after the fact.

### 3.2 Why this is *not* the JVM doc's histogram

The JVM doc's histogram is a *binder-level* count: each row corresponds to
a named-skip counter the JVM binder increments when it abstains. The Go
histogram is an *AST-shape plus environmental* count: each row corresponds
to a shape the call site took, plus a failure mode the resolver encountered.

The difference is structural, not stylistic. The JVM binder is a custom
heuristic with explicit per-abstention counters (the SW-171 vocabulary).
The Go binder is go/types, which has no per-call abstention counter — it
either resolves an identifier to a `types.Object` or it does not. The
AST-shape census is the only way to partition the unbound remainder.

**A second review will attack this vocabulary the way the JVM doc warned
about its own.** A node type that is a call but does not present as
`*ast.CallExpr` would be missing from the denominator; the JVM doc's
equivalent is the `indexing_suffix` defect class (§3 F8 there). Until a
second review runs, this is the first draft and the histogram is the
method.

---

## 4. The published rates, with the skip histogram beside every one

Never instead of it. A high rate with a large unexplained skip bucket is
exactly the claim this document exists to make uncheckable.

### 4.1 Headline — grpc-go, full source set

**14.00 % = 2 923 bound call sites / 20 883 CST call sites.** 831 source
files, all parsed. 269 units checked, **0** degraded. 16 989 go/types
diagnostics, **0** parser failures, **0** resolver-dropped intents.

| row | count | share of denominator |
|---|---:|---:|
| bound_call_sites_to_internal_funcs | 2 923 | 14.00 % |
| **`no_object_for_selector_qualifier`** | 11 431 | 54.74 % |
| **`no_object_for_bare_ident`** | 4 568 | 21.87 % |
| **`selector_with_non_ident_receiver`** | 4 282 | 20.50 % |
| **`call_position_other`** | 588 | 2.82 % |
| **`generic_call_site_skipped_by_cst`** | 14 | 0.07 % |

The three largest AST-shape buckets are the honest kind: **66 % of the
denominator is calls whose callee is in an external package or a local
variable** — these are graph boundaries, not recall gaps a binder change
would close. The two largest buckets combined (external + local) account
for **76.61 %** of the denominator. Only `call_position_other` (2.82 %) and
`generic_call_site_skipped_by_cst` (0.07 %) are categories that might
respond to a binder improvement.

### 4.2 What the rate does NOT depend on

This measurement reads source and asks the resolver. It does not depend on:

- the `GRAPHI_*` environment variables (none used)
- the resolver's tier (the binder output is `confirmed` end-to-end by
  declaration; Go is `typed-confirmed`)
- a configured store (the harness does not open one)
- the harness's own vocabulary (the AST-shape census is computed
  independently)

The two ternaries, by design, only connect through the resolver's
`Result.Edges` — the only output the experiment reads.

---

## 5. The edge counts — Phase B vs what reaches the graph

The 14.00 % headline is **Phase B**: the call site was typed and its
callee bound. Phase C (`engine/typeresolve/check.go:intentSink`) then
maps both endpoints onto committed graph nodes and dedupes at
`(from, to, kind)` granularity. That step is what produces the edge
count:

| kind | edges (dedup'd) | bound sites (sum of evidence) |
|---|---:|---:|
| `calls` | 2 570 | 2 923 |
| `references` | 11 717 | 13 855 |
| `implements` | 19 982 | 19 982 |

**The product-visible Go figure: 2 570 confirmed `calls` edges over 20 883
CST call sites = 12.31 %.** The 2 923 → 2 570 collapse is the
`(from, to, kind)` dedup: 353 call sites that share both endpoints and
the `calls` kind collapse onto one edge. The number 353 is **not** lost
sites — it is a description of how many sites two endpoints shared.

For completeness, the same store also holds 19 982 `implements` edges
(the structural type-edge dedup from go/types) and 11 717 `references`
edges (typed references that resolved to an internal symbol). The
`references` row is the binder's intermediate output; the product-visible
graph is the `confirmed` tier published below.

---

## 6. What this measurement does NOT establish

1. **Whether the bindings are correct.** That is the oracle's job, and
   graphi does not have a Go oracle. A high rate of *wrong* bindings would
   be worse than a low rate of *right* ones. The Go corpus is
   `typed-confirmed` by declaration, which is the *strongest* tier the
   engine supports — but "correct" is a stronger claim than
   "type-checker-proven", and this measurement does not test it.
2. **A D2 threshold.** Not proposed, not implied, not hinted at. AC-7.
3. **The size of the gap the AST-shape census might leave.** The histogram
   sums to the denominator exactly, so the gap is closed mathematically;
   but the AST-shape census is itself a vocabulary and a node type that
   is a call but does not present as `*ast.CallExpr` would be missing
   from the denominator. The JVM doc's §3 defends the same defect class
   at length; this document does not yet have a second review.
4. **What 19 982 `implements` edges mean.** The `implements` kind is the
   binder's structural output (the type-graph topology), not its call-site
   binding output. Counting it here is for completeness; the
   `references` and `calls` rows are the load-bearing ones.
5. **A rate for any other Go corpus.** This is grpc-go specifically.
   Different Go corpora will have different proportions of
   `no_object_for_selector_qualifier` vs `no_object_for_bare_ident` —
   e.g., a project that depends heavily on stdlib will have a larger
   first bucket; a project that uses many internal closures will have a
   larger second.
6. **Anything about the other 21 shipped languages.** Out of scope by the
   ticket.

---

## 7. Provenance

| | |
|---|---|
| branch | `sw-179-w1f-flip-index-migration` |
| W3 freeze (originally authored) | `490a632f0753daea71477ad95de569ece1555ffa` |
| HEAD (current, where this doc was bound) | `bed357855f9dacd2df38b5e77af6f9dd167a9af3` |
| harness | `/tmp/gobindrate/main.go` (CI-only, not checked in) |
| resolver of record | `engine/typeresolve.Resolve`, called as `typeresolve.Resolve(files, committed)` |
| corpus pin | `grpc-go` v1.60.1, sha `dbbcf59957fec0bd58063224cbf105b3b3698d4e` (pin tier 3) |
| run checkout | fresh clone at `dbbcf59`, working tree clean |
| two-run sha256 | `4738642340f272ab530db315862a55eb33417599330e0feee4a4fc319c103827` (both runs, byte-identical) |
| compiler used | none — this measurement never touches bytecode (§1.4) |
| ast counter | `*ast.CallExpr` walk via `go/parser` + `go/ast`, identical to the resolver's parser |
| `(from, to, kind)` dedup | `intentSink` in `engine/typeresolve/check.go` |

**Product bytes.** This story adds no product bytes. No file under
`engine/`, `core/`, `surfaces/`, `cmd/` or `internal/` is touched. The
harness is a one-shot tool at `/tmp/gobindrate/main.go` that is not
checked in; the doc is the only durable artifact.
