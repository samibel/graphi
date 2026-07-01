# First Curated Pure-Go Language: TypeScript over the SymbolExtractor Seam

This document covers TypeScript support: the first real language wired over the
`SymbolExtractor` seam. It records the state before and after this slice, explains
why the change was made, and establishes the repeatable recipe every other tier-1
grammar then follows. It is intended for contributors adding the next tier-1
language.

See also: [`symbol-extractor-seam.md`](symbol-extractor-seam.md) (the seam's
foundation) and [`parse-registry.md`](parse-registry.md).

## Before

The language-neutral extraction seam existed and was proven, but its only real
consumer was Go, which uses `go/ast` rather than tree-sitter. The tree-sitter→graph
mapping helper (`MapTreeSitter`) and the `PendingRef` contract were written
test-first against fixture captures, with no real grammar driving them yet. JSON
shipped as a structural-only parser, with no symbol extraction.

```text
GoParser.Parse  ──▶ goSymbolExtractor.Extract (go/ast)   ──▶ nodes / edges / PendingRefs
JSONParser.Parse ─▶ (structural tree only, no extraction)
MapTreeSitter   ──▶ (proven, but no grammar consumer yet)
```

The frozen tier-1 list in [`bench/lang-budget.md`](../bench/lang-budget.md) named
TypeScript as the first curated grammar (row 3), but no third-party grammar dependency
existed in `go.mod`.

## After

TypeScript is wired over the **same** seam as Go — a parser plus a `SymbolExtractor`,
registered one line in `RegisterDefaults` — but its CST comes from a **pure-Go,
CGo-free** tree-sitter runtime instead of `go/ast`:

```text
TSParser.Parse ──▶ tsSymbolExtractor.Extract (tree-sitter CST)
                       │  walk CST: defs (pass 1) + uses (pass 2)
                       │  split intra-file edges vs cross-file/selector PendingRefs
                       ▼
                   MapTreeSitter("typescript", nodeSpecs, edgeSpecs) ──▶ nodes / edges
                                                                          + PendingRefs
```

New/changed files:

- `core/parse/parser_ts.go` — `TSParser` (mirrors `GoParser`: `Language()="typescript"`,
  `.ts`, ctx-check + panic-recover, returns `ParseResult`) and `tsSymbolExtractor`
  (walks the CST, builds position-sorted node specs + an intra-file/deferred edge
  split, feeds `MapTreeSitter`). All source-text slicing stays inside `Extract`, so the
  mapping helper remains a pure leaf (`purity_test`).
- `core/parse/defaults.go` — one line: `r.Register(NewTSParser())`.
- `core/parse/parser_ts_test.go` — committed frozen fixture + golden node/edge snapshot;
  exact closed node set/kinds; intra-file `defines`/`calls`/`references` edges; use-site
  `file:line` provenance; selector `PendingRef`; byte-identical determinism across
  **repeated AND concurrent** parses (32 goroutines, `-race`).
- `go.mod` / `go.sum` — pins `github.com/odvcencio/gotreesitter` (pure-Go runtime +
  grammar registry).

### Pure-Go grammar (the one real risk, resolved)

The maintained CGO go-sitter-forest grammars cannot enter the default tier: they
use `import "C"` and an 8.8 MB `parser.c`, so under `CGO_ENABLED=0` all their Go
files are build-constraint-excluded. Instead, this slice uses
**`github.com/odvcencio/gotreesitter`**, which re-implements the tree-sitter
parser, lexer, and query engine in Go (no `import "C"`, no C toolchain) and ships
grammar parse tables as Go-embedded blobs. This keeps:

- `CGO_ENABLED=0 go build ./...` green, with the `internal/cgoconformance`
  import-graph scan passing and naming no offender — the real definition of
  "CGo-free" here;
- zero outbound network at runtime, since the grammar is module-pinned and
  embedded at build time, so nothing is fetched at parse time;
- byte-identical determinism, since `Extract` is a pure transform over the parsed
  CST.

### TypeScript kind mapping

TypeScript has MORE kinds than the frozen vocabulary `{file, function, method, type,
variable, constant}`. The extractor collapses them (documented in the test header):

| TS construct                                   | Emitted kind |
|------------------------------------------------|--------------|
| `function_declaration`                         | `function`   |
| class `method_definition`                      | `method`     |
| `interface` / `type` alias / `enum` / `class`  | `type`       |
| `let` / `var` binding                          | `variable`   |
| `const` binding                                | `constant`   |

Absent **by design** at this tier (so the closed node-set assertion is unambiguous):
namespaces/modules, decorators, ambient declarations.

### Imports & PendingRefs

Following the Go reference, imports are recorded as `ImportSpec`s (alias + path) and
surfaced in `References` for the reverse-dependency cascade; **no `EdgeImports` graph
edge** is emitted this slice. Cross-module / selector uses (`util.log(...)`) are
recorded as inert `PendingRef`s (no fabricated `NodeId`), exactly as Go records
`fmt.Println`. The cross-file linker resolves them later.

## Why

The goal is to establish a repeatable recipe on one real pure-Go grammar before
fanning out to the rest of tier-1. This slice proves end-to-end that a tree-sitter
grammar can drive the seam deterministically and CGo-free, producing the same
node/edge/provenance contract as the Go reference. Later languages then replicate
this recipe — one query/walk plus one golden fixture per language — over disjoint
files, in parallel, without re-solving graph plumbing, determinism, or the
cross-file deferral rules.

### Subset-tag default build

The `gotreesitter` `grammars` package embeds **all 206** grammar blobs by default
(`//go:embed grammar_blobs/*.bin`, gated `!grammar_subset`, +~24.5 MiB). This slice
wires the upstream **subset build-tag** mechanism so the shipped default build
embeds only the registered language's blob — the all-206 default embed is
prohibited in the shipped default:

- `internal/release.DefaultGrammarSubsetTags` is the single source of truth:
  `{grammar_subset, grammar_subset_typescript}`. The umbrella `grammar_subset` tag
  switches OFF the all-grammars embed; each `grammar_subset_<lang>` opts exactly one
  blob back in via the upstream generated `z_subset_blob_embed_<lang>.go`.
- The canonical `cmd/release` build (`go run ./cmd/release`) applies these tags, so
  the shipped binary is subset-tagged by construction. Later languages extend this
  list one entry per new language, paired with its `RegisterDefaults` line.
- The `release` CI job asserts the shipped binary embeds only the expected
  `grammar_blobs/*.bin` set (verified: **1** blob, `typescript.bin`, not 207).

The corrected size model — one ~3.13 MB one-time pure-Go runtime (paid once, for
all languages) plus a ~119 KiB per-language blob, governed by the whole-binary
**< 50 MB** ceiling — and both re-recorded size numbers (subset ≈ +3.10 MiB;
all-206 ≈ +24.5 MiB, cautionary only) live in
[`bench/lang-budget.md`](../bench/lang-budget.md) ("Measured deltas (SW-053 —
TypeScript, first curated grammar)"). The old ≤ 1.0 MB per-language envelope is
superseded; a later story re-pins `bench-budget.yml` against the subset-tagged
total.

```mermaid
flowchart LR
  subgraph before["Before (SW-052)"]
    GO1["GoParser → go/ast"] --> EX1["goSymbolExtractor"]
    MAP1["MapTreeSitter (no grammar consumer)"]
  end
  subgraph after["After (SW-053)"]
    GO2["GoParser → go/ast"] --> EX2["goSymbolExtractor"]
    TS["TSParser → pure-Go tree-sitter CST"] --> EXT["tsSymbolExtractor"]
    EXT --> MAP2["MapTreeSitter(\"typescript\")"]
    MAP2 --> OUT["nodes / edges + PendingRefs"]
  end
  before --> after
```
