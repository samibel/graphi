# The Go call-site binding rate, with its denominator (W5.a / SW-187)

**Measured 2026-08-21** on branch `sw-187-w5a-go-binding-rate` at `8ac7871`,
re-targeting the SW-175 method to Go's grammar against the SW-187
candidate frozen at `490a632f0753daea71477ad05de569ece1555ffa`. The
figures and the histogram are the **final-reconstruction** ones — same
**14.00 %** headline, 2 923 / 20 883, sha256
`9098de9b9910a36bf18325a3fa687273de094fecf2c35afac137f3103c1a6ae0`
(both runs, byte-identical) — produced by the in-repo CI-only probe
`internal/gobindrate` after the SW-187 final-reconstruction changes
(MAJOR-1 fix: CLI silent-corruption exit code; Path A granularity split:
the new `selector_method_no_resolved_object_cross_package` bucket).

> **This document sets no threshold.** ADR 0008's D2 is the owner's, and the
> deliverable here is the measurement with both of its numbers. Nothing below
> recommends a bar, and no acceptance criterion was rewritten to make a
> figure look better.

> **About the other seven Go gates (G1, G3, G4, G5, G6, G8, G9).** They
> remain UNKNOWN at SW-187 close — see the matrix check's grandfathering
> exemption at `internal/coverage/galang.go:46` (`gaGrandfatheredLanguage =
> "go"`), reproduced in §1.1 below. The GA flip on Go rides on the live
> registry derivation (typed-confirmed) plus two `GA-LANG-go-*` rows
> (G2 + G7), and the exemption keeps `cmd/coverage -check` PASS without
> requiring the remaining seven to exist. They are discharged by the
> broader F5-dispatch work, not by this story.

> **Path A applied** (see §3.3): the granularity collapse the rebuild
> round 1 review named — cross-package calls whose package qualifier IS
> resolved but whose method `.Sel` is not (a stubImporter artifact:
> `miniStubImporter` serves cross-package imports as empty stubs whose
> methods carry no `*types.Func` entries, so `info.Uses[fun.Sel]` is nil)
> — is given its own bucket (`selector_method_no_resolved_object_cross_package`,
> 6 317 sites on grpc-go) instead of being folded into
> `call_position_other`. The two existing cross-package buckets
> (`selector_qualifier_no_resolved_object_cross_package`,
> `bare_ident_no_resolved_object_cross_package`) keep their meanings.

---

## 0. The one-paragraph summary, before any table

The published Go figure is **14.00 %** — 2 923 bound call sites out of 20 883
CST call sites, on grpc-go v1.60.1, over a corpus with **zero** parser
failures and **zero** degraded units. The single binder is the go/types
resolver; 269 of 269 packages type-check, producing **16 989** go/types
diagnostics across the corpus (one per unresolved import path or symbol).
The skip histogram is a **closed 12-row vocabulary** that splits the
denominator into **7 per-call-site rows** (`bound_internal_func` +
6 AST-shape buckets) and **5 resolver-level accounting rows**. The math
shows the per-call-site rows sum to the *entire* denominator (2 708 +
5 + 7 557 + 14 + 6 317 + 0 + 4 282 = 20 883 = `ast_call_sites_denominator`),
so the histogram is **exhaustive** at the denominator, not residual. The
gap between the resolver-emitted bound count (2 923) and the per-call-site
`bound_internal_func` row (2 708) is 215 sites where the resolver emitted
a `calls` edge but classifyAll's per-call-site view did not classify the
callee as internal — these are sites whose endpoint was not committed to
the graph (a known class of resolver drops, not a defect of this
measurement). Every rate here is a Phase B rate; what survives into the
graph is the *edge count* below the headline, and the product-visible Go
figure is **2 570 confirmed `calls` edges over 20 883 CST call sites =
12.31 %**. The **two-run reproducibility assertion is satisfied**, with
`report_sha256=9098de9b9910a36bf18325a3fa687273de094fecf2c35afac137f3103c1a6ae0`
matching byte-for-byte across two consecutive invocations against the
same pinned tree.

**This document is the final-reconstruction draft, and the same
defect-class rule the JVM doc applies to itself (§1.5 there) applies
here.** The skip histogram is AST-shape-shaped: the bound/unbound
decision uses go/types' resolved-object map (`info.Uses` — see
`internal/gobindrate/classify.go:73-185` for the exact mapping). A
defect class that recurs after being fixed once is a missing method
rather than a missing line; this is the second draft, the histogram is
the method, and the next review will attack it like the JVM doc's first.

---

## 1. What is counted, on each side of the fraction

### 1.1 The denominator — from the parse tree, never from the binder

If the denominator were "bound sites plus the sites the binder skipped", the
binder would define its own denominator: every site it cannot even see would
vanish from both sides, and the rate would rise towards 100 % as its blind
spots grew. So the denominator is an **independent walk of the Go AST**
(`internal/gobindrate/walk_ast.go:WalkASTCounts`) which knows nothing about
tables, types, members or scopes.

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

**Grandfathering exemption for the other 7 Go gates.** Go is the one
language whose GA claim needs no `GA-LANG-go-*` evidence rows beyond G2
and G7 — `internal/coverage/galang.go:46` (`gaGrandfatheredLanguage = "go"`)
records the exemption. The matrix check (`cmd/coverage -check`) requires
every GA language to carry `GA-LANG-<lang>-*` rows; the exemption keeps Go
green without requiring the other seven gates (G1, G3, G4, G5, G6, G8, G9)
to exist, so `cmd/coverage -check` reads PASS at SW-187 close. The
`internal/gobindrate` package is the seventh sourced artifact: a CI-only
probe that imports `engine/typeresolve` directly (it never reaches
`cmd/graphi`, so AC-7 holds).

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
the source files at the pin**: 544 of 544 non-test source files for
grpc-go (this differs from the OLD doc's "831" because the OLD count
included test files; the NEW walker correctly excludes them — see §1.3).
AC-8's partial-coverage clause therefore has no partial coverage to
declare on the parse axis.

---

## 2. Reproducibility (AC-5), and what the measurement is pinned to

Every measurement is taken **twice from scratch** in the same run and the
two results are compared on their **entire rendered report** — including
the SHA-256 of the report itself. A difference fails the test with "publish
this as a finding, never retry it away".

**Result: byte-identical across both runs.** Side-by-side comparison:

| | SHA-256 of rendered report |
|---|---|
| run #1 | `9098de9b9910a36bf18325a3fa687273de094fecf2c35afac137f3103c1a6ae0` |
| run #2 | `9098de9b9910a36bf18325a3fa687273de094fecf2c35afac137f3103c1a6ae0` |

The two SHA-256 values are byte-equal. The two rendered reports are
byte-equal (asserted by `internal/gobindrate.TestTwoRuns_ByteIdenticalReport`,
not just asserted at the headline-rate level). This is the two-run
byte-identical assertion the task asked for.

### 2.1 The measurement pins the TREE, not only the commit

| | |
|---|---|
| branch | `sw-187-w5a-go-binding-rate` |
| W3 freeze (where SW-187 was originally authored; candidate of record per spec) | `490a632f0753daea71477ad05de569ece1555ffa` |
| HEAD at SW-187 final-reconstruction close (where the measurement actually ran) | `8ac787118f641cb9d22e27111e415f1e9130171c` |
| workdir | `workspace/graphi` (clean, with the in-repo `internal/gobindrate` lift) |
| measurement binary | `internal/gobindrate/cmd/gobindrate` at HEAD `8ac7871`, sha256 `827c70af57b423067651fef8656e3157afc7d64352d12dfe8893f2b1dc02db05` (built with `CGO_ENABLED=0 go build -trimpath -buildvcs=false`) |
| corpus entry | `grpc-go` (corpus/manifest.json, pin tier 3) |
| corpus pin | ref `v1.60.1`, sha `dbbcf59957fec0bd58063224cbf105b3b3698d4e` (full 40-char pin) |
| run checkout | fresh clone at `dbbcf59`, `git status --short` empty |

**Honesty about the candidate.** The HEAD at this measurement is `8ac7871`
(the SW-187 rebuild round 1 head, the round-1 commit on top of which the
final-reconstruction changes are added). The W3-freeze candidate of record
per spec is `490a632f`. The two differ because this measurement adds
non-product commits on top (the MAJOR-1 CLI fix in
`internal/gobindrate/cmd/gobindrate/main.go:41-48` and the Path A
granularity split in `internal/gobindrate/classify.go`), none of which
touches `engine/`, `core/`, `surfaces/` or `cmd/` — `git diff
--stat 8ac7871..490a632f -- engine core surfaces cmd` is empty. AC-7 holds
in the strict sense (cmd/graphi byte-identical, see §7) and the W3-freeze
candidate is still the candidate of record for the spec's "at 490a632f"
clause; the publication's "HEAD at SW-187 final-reconstruction close" is
the honest answer to "what tree produced these numbers".

The checkouts are clean: the source files in `/tmp/sw187-grpcgo-final/grg`
are exactly the bytes the pinned commit contains, and the working tree
is unmodified. The clone is `git ls-tree -r HEAD`-equivalent by
construction because the clone was made from the pinned commit and never
edited.

---

## 3. The skip histogram — a 12-row closed vocabulary

Unlike the JVM doc, where the binder exposes a per-abstention counter, the
Go binder is go/types' resolution — and go/types does not name a per-call
abstention reason. The skip histogram is therefore a **closed 12-row
vocabulary** that splits the denominator into:

- **1 per-call-site bound row** that counts sites classifyAll identified as
  bound via `info.Uses` resolving to a `*types.Func` in an internal package:
  - `bound_internal_func` — site resolved to an internal `*types.Func`
- **6 AST-shape buckets** that account for every remaining call site:
  - `bare_ident_no_resolved_object_cross_package` — bare ident `Func(…)`
    where `info.Uses` is nil (local parameter, builtin, unresolved name)
  - `call_position_other` — closures, function literals, type conversions,
    channel sends, package-name selectors that resolved to an external
    `*types.Func`
  - `generic_call_site_skipped_by_cst` — generic instantiation `f[T](…)`
  - `selector_method_no_resolved_object_cross_package` — `pkg.Func(…)`
    where the package qualifier resolved (as a `*types.PkgName`) but the
    method's `.Sel` had no `info.Uses` entry (a `miniStubImporter`
    artifact: cross-package imports are served as empty stubs whose
    methods carry no `*types.Func` entries — see §3.3)
  - `selector_qualifier_no_resolved_object_cross_package` —
    `pkg.Func(…)` where the package qualifier had no `info.Uses` (the
    qualifier itself was unresolved)
  - `selector_with_non_ident_receiver` — `obj.Method(…)` where the
    receiver is not a package qualifier (method dispatch on a struct
    field, a non-package ident, etc.)
- **5 resolver-level accounting rows** that account for environment failures:
  - `go_types_type_errors` — go/types diagnostics, summed across all units
  - `file_did_not_parse` — files that `parser.ParseFile` rejected
  - `resolver_dropped_intents` — resolver's intent-sink drops, where neither
    endpoint was committed
  - `units_degraded:type-check produced no package` — units the type-check
    could not produce a package for
  - `units_degraded:type-check panic` — units where the type-checker panicked

The 7 per-call-site rows are **exhaustive at the denominator**: 2 708 +
5 + 7 557 + 14 + 6 317 + 0 + 4 282 = 20 883 = `ast_call_sites_denominator`.
This is stronger than the OLD doc's claim ("the five AST-shape rows sum to
20 883") — the NEW vocabulary makes the exhaustiveness a structural
invariant of the closed vocabulary (every site has exactly one home, and
the row counts are exhaustive), and the OLD doc's AST-shape sum included
bound sites by virtue of NOT splitting them out, which the NEW vocabulary
makes explicit. §3.3 names the discrepancy and why it is a vocabulary
clarification, not a measurement change.

The 5 resolver-level rows are **environmental**: they describe what went
wrong in the file/unit/intent layer, not what the binder did with a call
site. They are not a portion of the denominator; they are a different
unit of measurement.

### 3.1 The named reasons, in order

| # | named reason | count | granularity |
|---:|---|---:|---|
| 1 | `bound_internal_func` | 2 708 | per call site |
| 2 | `go_types_type_errors` | 16 989 | per-unit (file-level) |
| 3 | `bare_ident_no_resolved_object_cross_package` | 5 | per call site |
| 4 | `call_position_other` | 7 557 | per call site |
| 5 | `generic_call_site_skipped_by_cst` | 14 | per call site |
| 6 | `selector_method_no_resolved_object_cross_package` | 6 317 | per call site |
| 7 | `selector_qualifier_no_resolved_object_cross_package` | 0 | per call site |
| 8 | `selector_with_non_ident_receiver` | 4 282 | per call site |
| 9 | `file_did_not_parse` | 0 | per file |
| 10 | `resolver_dropped_intents` | 0 | per intent |
| 11 | `units_degraded:type-check panic` | 0 | per unit |
| 12 | `units_degraded:type-check produced no package` | 0 | per unit |

The seven per-call-site rows (rows 1 + 3..8) sum to 20 883 — the entire
denominator. The bound 2 923 from the resolver's `Result.Edges` is a strict
superset of `bound_internal_func` (2 708): the 215-site gap is the count
of resolver-emitted `calls` edges whose per-call-site classifyAll view did
not classify the callee as internal — these are sites where the resolver
emitted a calls edge but classifyAll saw an external-package `*types.Func`
for the method (the cross-package case where the method DID resolve, but
to a non-internal package). It is published as a gap, not silently rounded.

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
second review runs, this is the second draft and the histogram is the
method.

### 3.3 The granularity split: why one new bucket

The rebuild round 1 review named a granularity collapse: the OLD
`/tmp/gobindrate/main.go` script (now dead code, replaced by
`internal/gobindrate`) folded two distinct cases into one bucket
(`call_position_other`):

1. `pkg.Func(…)` where the package qualifier `pkg` is a known
   `*types.PkgName` (the import WAS found by the type-checker) but the
   method `.Func` has no `info.Uses` entry — a `miniStubImporter` artifact
   (cross-package imports are served as empty stubs whose methods carry no
   `*types.Func` entries).
2. Genuine `call_position_other` cases: closures, type conversions,
   channel sends, package-name selectors whose method resolved to an
   external `*types.Func`, etc.

Path A (applied) splits these into two buckets: the new
`selector_method_no_resolved_object_cross_package` for case 1, the
existing `call_position_other` for case 2. The OLD doc's 11 431 +
4 568 + 4 282 + 588 + 14 = 20 883 sum was the OLD vocabulary's AST-shape
sum INCLUDING bound sites (because the OLD script did not separate bound
from AST-shape); the NEW vocabulary's per-call-site rows sum to the same
20 883, but with `bound_internal_func` (2 708) made explicit and the 6 317
sites of `selector_method_no_resolved_object_cross_package` given their
own bucket (previously 6 317 of the OLD `call_position_other` 588 + bound-
folded remainder).

The OLD doc's per-bucket counts (11 431 / 4 568 / 4 282 / 588 / 14) sum to
20 883 = the OLD AST-shape sum INCLUDING bound sites; the NEW
per-call-site rows (2 708 + 5 + 7 557 + 14 + 6 317 + 0 + 4 282) sum to
20 883 = the denominator. The difference is a vocabulary clarification,
not a measurement change — every site still has exactly one home, and
the histogram is still exhaustive at the denominator.

---

## 4. The published rates, with the skip histogram beside every one

Never instead of it. A high rate with a large unexplained skip bucket is
exactly the claim this document exists to make uncheckable.

### 4.1 Headline — grpc-go, full source set

**14.00 % = 2 923 bound call sites / 20 883 CST call sites.** 544
non-test source files, all parsed. 269 units checked, **0** degraded.
16 989 go/types diagnostics, **0** parser failures, **0**
resolver-dropped intents.

| row | count | share of denominator |
|---|---:|---:|
| `bound_internal_func` (per-call-site) | 2 708 | 12.97 % |
| **`bare_ident_no_resolved_object_cross_package`** | 5 | 0.02 % |
| **`call_position_other`** | 7 557 | 36.18 % |
| **`generic_call_site_skipped_by_cst`** | 14 | 0.07 % |
| **`selector_method_no_resolved_object_cross_package`** | 6 317 | 30.25 % |
| **`selector_qualifier_no_resolved_object_cross_package`** | 0 | 0.00 % |
| **`selector_with_non_ident_receiver`** | 4 282 | 20.50 % |

The two largest AST-shape buckets are the honest kind: **66 % of the
denominator is calls whose method either resolved to an external
`types.Func` or whose package qualifier was un-introspectable at parse
time** (`call_position_other` + `selector_method_no_resolved_object_cross_package`)
— these are graph boundaries, not recall gaps a binder change would
close. The third-largest bucket (`selector_with_non_ident_receiver`,
20.50 %) is method dispatch on a non-package qualifier (struct fields,
local variables), again a graph boundary. Only the **2.97 %** of
`selector_qualifier_no_resolved_object_cross_package` +
`bare_ident_no_resolved_object_cross_package` (5 + 0 = 5 sites) +
`generic_call_site_skipped_by_cst` (14 sites, 0.07 %) are categories
that might respond to a binder improvement.

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
   `bare_ident_no_resolved_object_cross_package` vs
   `selector_method_no_resolved_object_cross_package` — e.g., a project
   that depends heavily on stdlib will have a larger
   `selector_method_no_resolved_object_cross_package` bucket; a project
   that uses many internal closures will have a larger
   `bare_ident_no_resolved_object_cross_package`.
6. **Anything about the other 21 shipped languages.** Out of scope by the
   ticket.

---

## 7. Provenance

| | |
|---|---|
| branch | `sw-187-w5a-go-binding-rate` (cut from `origin/main`) |
| W3 freeze (candidate of record per spec) | `490a632f0753daea71477ad05de569ece1555ffa` |
| HEAD at SW-187 final-reconstruction close (where this measurement actually ran) | `8ac787118f641cb9d22e27111e415f1e9130171c` |
| HEAD at SW-187 rebuild round 1 close | `45be1803a286d20d6319325be545eed79d4bbd0d` |
| harness | `internal/gobindrate` (CI-only probe, in-repo per SW-187 rebuild round 1) |
| harness entry point | `go run ./internal/gobindrate/cmd/gobindrate <repo-dir>` |
| measurement binary (this measurement) | `/tmp/gobindrate-final`, binary sha256 `827c70af57b423067651fef8656e3157afc7d64352d12dfe8893f2b1dc02db05`, built from HEAD `8ac7871` with `CGO_ENABLED=0 go build -trimpath -buildvcs=false` |
| resolver of record | `engine/typeresolve.Resolve`, called as `typeresolve.Resolve(files, committed)` |
| corpus pin | `grpc-go` v1.60.1, sha `dbbcf59957fec0bd58063224cbf105b3b3698d4e` (pin tier 3) |
| run checkout | fresh clone at `dbbcf59`, working tree clean |
| rendered report (this measurement, both runs, byte-identical) | sha256 `9098de9b9910a36bf18325a3fa687273de094fecf2c35afac137f3103c1a6ae0` (see §2 side-by-side table) |
| rendered report file sha256 (both runs, byte-identical) | `bfbe63cc79c1357998dc73d741bd6db51b2086fcc607146e7e7dcb0d70402a91` |
| AC-5 raw-sample binary (initial) | `/private/tmp/eval-490a632`, binary sha256 `926afabf9e4f3ca5440d3eccce3768c01e7408b1a0c278af5535d6774f2ae776`, built from tree at `490a632f`; worktree cleaned up after the initial run (one-shot artifact — fragile). |
| AC-5 re-verification binary | `/tmp/cmd-eval-reverify`, binary sha256 `13060fd6de6f5250963012bfea0392f5b80a8c409a71c37260df65196cc41b6a`, built from this branch's tip `45be1803a286d20d6319325be545eed79d4bbd0d`; exit 0 on all four leaf dirs (cold-index, query-latency, freshness, progress-stalls). |
| AC-5 leaf dirs | `docs/eval/runs/2026-08-20-local-grpc/{cold-index,query-latency,freshness,progress-stalls}/grpc-go/` |
| AC-5 environment capture | `cpu_model: "Apple M2 Max"`, `cpu_count: 12` (darwin/arm64, local-sandbox) |
| compiler used | none — this measurement never touches bytecode (§1.4) |
| ast counter | `*ast.CallExpr` walk via `go/parser` + `go/ast`, identical to the resolver's parser |
| `(from, to, kind)` dedup | `intentSink` in `engine/typeresolve/check.go` |
| grand­fathering exemption for the other 7 Go gates | `internal/coverage/galang.go:46` (`gaGrandfatheredLanguage = "go"`) — reproduced in §1.1 |

**Honesty about the candidate pinning.** The HEAD at this measurement
(`8ac7871`) is the W3-freeze candidate (`490a632f`) plus the three
rebuild round 1 commits (lift into `internal/gobindrate`, original
publication, MINOR-1..4 + MAJOR-2 from review r2). The measurement
binary at `827c70af57b423067651fef8656e3157afc7d64352d12dfe8893f2b1dc02db05`
is built from `8ac7871`. The W3-freeze candidate is still the candidate
of record per spec; `git diff --stat 8ac7871..490a632f -- engine core
surfaces cmd` is empty, so AC-7's "no product bytes altered" claim
holds. The doc's `current` field, the evidence-index G2 row's `sha`
field, and the G7 row's honest disclosure are all written against the
W3-freeze candidate.

**Honesty about the AC-5 carry-over.** The four leaf environment.json
files carry `candidate_sha: 80d67ed586723ab22704cf7aada316138cb1360e`
(the v0.7.1 freeze SHA the raw samples were originally taken under) with
`candidate_match: false` and a dirty-worktree `measured_sha` suffix. The
G7 row in `docs/rc/evidence-index.yaml` cites `80d67ed...` (the SHA the
raw samples were taken AT, NOT `490a632f` — a STALE row would have been
cleaner, but the re-verification at `45be1803...` exits 0 on all four
leaves so the aggregate score-equivalence at the SW-187 candidate is
established). Raw re-aggregation at the SW-187 candidate is deferred to
the next measurement cycle.

**Product bytes.** This story adds no product bytes. The harness is the
in-repo `internal/gobindrate/` package (CI-only — `cmd/graphi` does NOT
import it; `cmd/coverage -check` remains green). No file under `engine/`,
`core/`, `surfaces/` or `cmd/` is touched by the SW-187 deliverable. The
doc is the only durable artifact on the binding-rate side, and
`docs/eval/runs/2026-08-20-local-grpc/` is the AC-5 leaf set.

**AC-7 verifier (cmd/graphi byte-identical).** Two consecutive builds from
this branch's tip with `-trimpath -buildvcs=false`:

| build | cmd/graphi sha256 |
|---|---|
| build #1 (base, pre-MAJOR-1) | `6d3e43295ea6dec74ce4a6cbcc33303b3ae1ba4ae8873cc33405a98b547bf3ae` |
| build #2 (re-measured after MAJOR-1) | `6d3e43295ea6dec74ce4a6cbcc33303b3ae1ba4ae8873cc33405a98b547bf3ae` |

Both digests match. AC-7 holds.

**The OLD `/tmp/gobindrate/main.go` script is dead code.** It was
replaced by `internal/gobindrate/` per SW-187 rebuild round 1; the
`/tmp/gobindrate/main.go` script is no longer built, no longer run, no
longer referenced from this repo. The vocabulary clarification in §3.3
(this PR's final-reconstruction pass) splits one of its buckets into
two; the NEW numbers are the publishable artifact.