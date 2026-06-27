# SymbolExtractor Seam + Tree-Sitter Mapping Helper + PendingRef Contract (SW-052)

This document satisfies the `[DOC]` acceptance criterion for SW-052: it records the
state **before** and **after** the STEP-0 foundation slice and explains **why** the
change was made. It is also the **declared-owner contract document** for `PendingRef`
(AC `[ARCH]`): SW-052 owns `PendingRef`; the FU-1 cross-file linker (`engine/link`)
consumes it.

SW-052 is the **EP-009 STEP-0 hard gate** — no language worker (SW-053..056) may
start until this merges green.

## Before

Symbol extraction was **Go-specific**. The Go parser (`core/parse/parser_go.go`)
called a free function `extractGo(filename, pkg, fset, file)` in
`core/parse/extract_go.go` that walked `go/ast` and emitted `model.Node` /
`model.Edge` plus the deferred `PendingRef` records. The `Parser`/`ParseResult`
boundary already existed and already carried `PendingRefs` and `Imports`, but the
AST→graph mapping was hard-wired to the Go path:

```text
GoParser.Parse ──▶ extractGo(go/ast) ──▶ nodes / edges / PendingRefs
```

There was no language-neutral extraction contract. A new language would have had to
either reimplement the graph plumbing or bolt onto the Go-specific free function —
both of which couple grammar work to graph-derivation internals and would force
every worker to re-solve provenance, determinism, and the cross-file/selector
deferral rules.

## After

A new, **narrower** seam — `SymbolExtractor` — is layered **over** the existing
`Parser`/`ParseResult` boundary (it does **not** rename or replace `Parser`):

```go
type SymbolExtractor interface {
    Language() string
    Extract(filename string, root any) (nodes []model.Node, edges []model.Edge, pending []PendingRef, err error)
}
```

- `core/parse/extractor.go` — the interface, the **frozen canonical vocabulary**
  (`Kind{File,Function,Method,Type,Variable,Constant}` /
  `Edge{Defines,Calls,References,Imports}`), and the **Go reference implementation**
  `goSymbolExtractor`, which threads the existing `*goAST` handle (it already carries
  the `FileSet`) through `root any` and delegates to the unchanged `extractGo`.
- `core/parse/parser_go.go` — `GoParser` now produces the `*goAST` and runs symbol
  extraction through its injected `SymbolExtractor` (the reference impl). Parsing
  (text→AST) and extraction (AST→graph) are now separated concerns.
- `core/parse/mapping.go` — `MapTreeSitter`, the **tree-sitter→graph mapping
  helper**: a pure, I/O-free, CGo-free transform that turns a grammar query's
  captures/specs into canonical nodes/edges with full provenance and `file:line`
  evidence. This is the one genuinely-new code path; Go uses `go/ast`, so it has no
  live grammar consumer yet and is written **test-first** (`mapping_test.go`) so
  SW-053's first grammar worker inherits a proven, deterministic primitive.
- `core/parse/defaults.go` — registration is **one line per language**; each
  constructor wires its parser to its `SymbolExtractor`, so adding a tier-1 language
  is a single `r.Register(NewXxxParser())` line.

```text
                      ┌────────────────────────── pure leaf (core/parse) ───────────────────────────┐
  source ──▶ Parser.Parse ──▶ ParseResult{Root}                                                       │
                                   │                                                                   │
                                   ▼                                                                   │
                         SymbolExtractor.Extract(filename, root)                                       │
                                   │                                                                   │
              ┌────────────────────┼─────────────────────────────┐                                    │
              ▼                    ▼                              ▼                                    │
   Go: goSymbolExtractor   future grammar worker          MapTreeSitter (shared helper)               │
   (go/ast, reference)     (tree-sitter query)  ───────▶  captures/specs → nodes/edges + file:line    │
              │                    │                              │                                    │
              └────────────────────┴──────────────┬───────────────┘                                    │
                                                   ▼                                                   │
                              nodes / edges (intra-file)  +  PendingRefs (inert, deferred) ────────────┘
                                                                      │
                                                                      ▼  (consumed by FU-1)
                                                          engine/link cross-file linker
```

## Why

- **Decouple grammars from graph plumbing.** Each later language worker writes a
  grammar query and an `Extract` mapping — never graph-derivation internals,
  provenance rules, or determinism guarantees. The mapping helper centralizes those
  once.
- **Gate fan-out behind a frozen foundation.** EP-009 fans out across parallel
  workers. Landing the seam, the mapping helper, the `PendingRef` contract, and the
  frozen tier-1 list **first** (as one self-contained merge) means workers build
  against a fixed contract instead of a moving target.
- **Lock in the CGo-free firewall.** The default tier stays pure-Go
  (`CGO_ENABLED=0 go build ./...` green; `internal/cgoconformance` enforces it); the
  mapping helper is a pure leaf (`purity_test.go`); provenance is limited to
  `file:line` spans (no raw-source dumping). Later grammar-bearing slices inherit a
  provable default.
- **One owned contract, not two speculative interfaces.** SW-052 owns `PendingRef`;
  FU-1 consumes it. No FU-1 code is required to merge.

## The `PendingRef` contract (declared owner: SW-052)

`PendingRef` (`core/parse/parse.go`) is the **inert** record of a reference the parse
leaf saw but deliberately did **not** resolve. The parse boundary fabricates no
endpoint; the `engine/link` linker resolves each `PendingRef` against the
fully-committed node set.

| Field          | Meaning                                                                 |
|----------------|-------------------------------------------------------------------------|
| `FromQN`       | qualified name of the enclosing symbol that owns the reference (edge "from") |
| `Name`         | referenced bare name; for a selector, the trailing identifier (`Fn` in `pkg.Fn`) |
| `SelectorBase` | leading qualifier of a selector (`pkg`, or receiver `x` in `x.Method`); empty for a bare ident |
| `Kind`         | edge kind to emit on resolution (`calls` or `references`)               |
| `Line`         | 1-based source line, for `file:line` evidence                           |
| `Selector`     | whether the reference was a selector expression vs a bare identifier    |

**Contract invariants (binding on every `SymbolExtractor`):**

1. **Inert / no fabricated endpoint.** A `PendingRef` carries **no `NodeId`**. The
   parse leaf never mints or guesses an endpoint and emits **no resolved edge** for a
   deferred reference. NodeIds are minted/looked up by the linker against committed
   nodes.
2. **What gets deferred.** Same-package cross-file bare-ident calls/refs (no
   `SelectorBase`, `Selector=false`) and cross-package/cross-file selector calls/refs
   (`SelectorBase` set, `Selector=true`, e.g. `pkg.Fn`, `recv.Method`).
3. **Evidence.** `Line` records the reference site (not the definition site) for
   `file:line` provenance.
4. **Deterministic + deduplicated.** Deduplicated by logical identity
   (`FromQN, SelectorBase, Name, Kind, Selector`); the first line wins. Identical
   input yields the identical `PendingRef` set.

**Consumer (FU-1).** The `engine/link` linker maps a selector's `SelectorBase`
through the file's `ImportSpec`s to a package, looks up the target in the committed
node set, and only then mints a provenanced cross-file/cross-package edge. An
unresolvable reference is skipped and counted — never turned into a fabricated edge.

The cross-file/selector emission is covered by `core/parse/pendingref_test.go`
(`TestPendingRef_CrossFileSelectorEmission`): a `pkg.Fn` call and a `pkg.Val`
reference each become an inert selector `PendingRef` with correct `file:line` and
**no** fabricated `NodeId` / **no** resolved edge.

## Frozen tier-1 list & binary budget

The curated tier-1 language list (the pure-Go **`gotreesitter`** runtime + its
embedded grammar blobs, selected via subset build tags) and the per-worker
binary-budget model are frozen in [`bench/lang-budget.md`](../bench/lang-budget.md)
(the single source of truth — see also [`epics/index.md`](../epics/index.md) for
the registry). Languages whose only grammar is CGO-only (`go-sitter-forest`) or
whose extractor is out of tier-1 scope are deferred to the opt-in `graphi-broad`
CGO build. `bench/bench-budget.yml` is the data backing for the budget table.

> **RE-PLANNED 2026-06-24 (EP-009-REPLAN-001):** the original "pure-Go subset of
> `go-sitter-forest`" framing was false — `go-sitter-forest` is entirely CGO. The default
> tier uses the genuinely pure-Go `github.com/odvcencio/gotreesitter` runtime instead.

## Tests proving STEP-0

- `core/parse/extract_go_test.go` — all `TestExtractGo_*` (incl. `_Deterministic`,
  `_PendingRefs`) pass **unchanged** after the refactor (regression guard).
- `core/parse/mapping_test.go` — mapping helper: provenance + `file:line`,
  no-fabricated-endpoint, reserved-kind rejection, repeated + concurrent determinism.
- `core/parse/pendingref_test.go` — `PendingRef` cross-file/selector emission
  (declared-owner contract test).
- `core/parse/purity_test.go` — leaf purity (no `engine/`/`surfaces/` imports).
