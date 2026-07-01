# Curated Tier-1 Pure-Go Language Coverage

This document covers the completion of graphi's curated pure-Go default language
tier: the state before and after, and why the change was made. It is intended for
contributors working on tier-1 language support or the default build. This slice
clones the TypeScript reference recipe over the remaining frozen tier-1 languages.

See also: [`typescript-extractor.md`](typescript-extractor.md) (the reference
recipe), [`symbol-extractor-seam.md`](symbol-extractor-seam.md) (the foundation
seam), and [`../bench/lang-budget.md`](../bench/lang-budget.md) (the frozen
language list and measured blob-size deltas).

## Before

Exactly one tree-sitter language was wired over the `SymbolExtractor` seam:
**TypeScript**, produced by the pure-Go, CGo-free `github.com/odvcencio/gotreesitter`
runtime and embedded as a single subset-tagged blob (`typescript.bin`). Go
(`go/ast`) and JSON (stdlib) shipped natively. The frozen tier-1 list named ~20
more curated languages, but none were implemented yet — the seam had only one real
grammar consumer.

```text
GoParser     ──▶ goSymbolExtractor   (go/ast)        ──▶ nodes / edges / PendingRefs
JSONParser   ──▶ (structural only)
TSParser     ──▶ tsSymbolExtractor   (gotreesitter CST) ──▶ nodes / edges / PendingRefs
                 └─ subset tag: grammar_subset, grammar_subset_typescript (1 blob)
```

## After

The full frozen curated pure-Go tier ships through the same seam. Each language is
a disjoint `parser_<lang>.go` + `parser_<lang>_test.go` pair that clones the
TypeScript recipe:

- a `Parser` (ctx-check + panic-recover → normalized `ParseResult`) plus a
  `SymbolExtractor` that walks the gotreesitter CST in two passes — first
  collecting top-level definitions, then resolving uses;
- a split between **intra-file edges** and cross-file/selector **`PendingRef`s**,
  never fabricating a `NodeId`;
- imports recorded as `ImportSpec`s, never as an `EdgeImports`;
- deterministic, file:line-provenanced graph elements emitted through
  `MapTreeSitter`.

The shared two-pass machinery lives in `parser_tswalk.go` (`cstWalk`), so each
per-language file stays a thin, declarative mapping of that grammar's node types
onto the frozen `{file, function, method, type, variable, constant}` vocabulary —
collapsing or omitting kinds the language lacks rather than inventing new ones.

```text
<Lang>Parser ──▶ <lang>SymbolExtractor (gotreesitter CST) ──▶ nodes / edges / PendingRefs
                 │  pass 1: top-level defs → closed node set (kinds the language has)
                 │  pass 2: uses → intra-file edges  vs  selector/cross-file PendingRefs
                 │  imports → ImportSpec (+ surfaced in References); never EdgeImports
                 ▼  one RegisterDefaults line  +  one grammar_subset_<lang> tag
```

### Languages shipped (19, plus TypeScript as the reference)

The 19 new languages are grouped below by extractor effort, from mechanical
mirrors of TypeScript through heavier-divergence and markup/config families:

- **TS-adjacent mirror:** JavaScript, TSX.
- **C-family / OO:** Python, Java, C, Ruby, Rust, PHP, C#, Kotlin.
- **Heavier divergence:** C++ (namespaces), Bash (sparse type system), SQL
  (statement-oriented, no callables), Lua (tables-as-objects).
- **Markup / config** (collapse to `file` plus a few def kinds, exercising the
  absent-by-design path): CSS, YAML, TOML, Markdown, HCL/Terraform.

Each language carries a committed golden fixture asserting:

- the exact closed node set and literal kinds;
- intra-file `defines`/`calls`/`references` edges with full provenance and at
  least one use-site `file:line` pin;
- a cross-file/selector negative case (or, for languages without
  selector/import syntax — SQL, Bash — the documented absence of those forms);
- byte-identical determinism across repeated and 32-goroutine concurrent
  parses (green under `-race`).

### Subset-tagged default build

The shipped default build is subset-tagged via the single source of truth
`internal/release.DefaultGrammarSubsetTags` (umbrella `grammar_subset` plus one
`grammar_subset_<lang>` per registered language), applied by the canonical
`cmd/release` build. It embeds exactly the 20 registered blobs (TypeScript plus
the 19 new languages) — not the all-206 default embed, and no unregistered blob —
builds green under `CGO_ENABLED=0`, and passes `internal/cgoconformance` with no
offender named. Measured size: **18,979,778 B (~18.1 MB)**, well under the
**< 50 MB** whole-binary hard gate (the marginal cost over the TypeScript-only
subset is only ~3.3 MiB of parse tables; the ~3.13 MB pure-Go runtime was already
paid for). See [`../bench/lang-budget.md`](../bench/lang-budget.md) for the
per-blob deltas.

### Reasoning

The goal is to complete the user-visible language coverage of the default,
CGo-free tier: a single static `CGO_ENABLED=0` binary that parses the curated
tier-1 languages with zero outbound network and no telemetry, with all grammars
module-pinned and Go-embedded at build time. The recipe was fully proven with
TypeScript, so each additional language is a mechanical clone over a disjoint
file pair — breadth, not new architecture. Grammars that cannot enter the
pure-Go subset default tier are deferred to the opt-in `graphi-broad` flavor.

### HTML deferred to `graphi-broad`

One frozen-list language, **HTML**, is deferred despite having a pure-Go
gotreesitter grammar. In gotreesitter v0.20.2 the shared HTML external-scanner
core (`goLexerAdapter`, `htmlDeserializeTagsInto`, `htmlScan*`) is physically
co-located in `blade_scanner.go`, gated `grammar_subset_blade`. A subset build
with only `grammar_subset_html` therefore fails to compile, and enabling
`grammar_subset_blade` would embed an unregistered `blade.bin` blob — which the
subset-tagging rule above prohibits. HTML's extractor is intentionally not
landed in this tier to keep the default subset build green; re-evaluate when
upstream splits the HTML scanner core into an html-gated file. This is a
build-packaging deferral (subset-isolation), distinct from the `graphi-broad`
CGO-only-grammar deferral path. See
[`../bench/lang-budget.md`](../bench/lang-budget.md) for the full note.
