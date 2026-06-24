# Curated Tier-1 Pure-Go Language Coverage (SW-054)

This document satisfies the `[DOC]` acceptance criterion for SW-054. It records the
state **before** and **after** completing the curated pure-Go default language tier and
explains **why** the change was made. SW-054 is child 3/6 of EP-009; it **clones the
SW-053 reference recipe** over the remaining frozen tier-1 languages.

See also: [`typescript-extractor.md`](typescript-extractor.md) (the SW-053 reference
recipe), [`symbol-extractor-seam.md`](symbol-extractor-seam.md) (the SW-052 STEP-0
seam), and [`../bench/lang-budget.md`](../bench/lang-budget.md) (the frozen list +
measured blob-size deltas).

## Before

After SW-053, exactly one tree-sitter language was wired over the SW-052
`SymbolExtractor` seam: **TypeScript**, produced by the pure-Go, CGo-free
`github.com/odvcencio/gotreesitter` runtime and embedded as a single subset-tagged
blob (`typescript.bin`). Go (`go/ast`) and JSON (stdlib) shipped natively. The frozen
tier-1 list named ~20 more curated languages, but none were implemented — the seam had a
single real grammar consumer.

```text
GoParser     ──▶ goSymbolExtractor   (go/ast)        ──▶ nodes / edges / PendingRefs
JSONParser   ──▶ (structural only)
TSParser     ──▶ tsSymbolExtractor   (gotreesitter CST) ──▶ nodes / edges / PendingRefs
                 └─ subset tag: grammar_subset, grammar_subset_typescript (1 blob)
```

## After

The full frozen **curated pure-Go tier** ships through the **same** seam. Each language
is a disjoint `parser_<lang>.go` + `parser_<lang>_test.go` pair that clones the SW-053
recipe — a `Parser` (ctx-check + panic-recover → normalized `ParseResult`) plus a
`SymbolExtractor` that walks the gotreesitter CST in two passes (collect top-level
definitions, then resolve uses), splitting **intra-file edges** from cross-file/selector
**`PendingRef`s** (never fabricating a `NodeId`), recording imports as `ImportSpec`s
(never an `EdgeImports`), and emitting deterministic, file:line-provenanced graph
elements through `MapTreeSitter`. The shared two-pass machinery lives in
`parser_tswalk.go` (`cstWalk`) so each per-language file stays a thin, declarative
mapping of that grammar's node types onto the frozen `{file, function, method, type,
variable, constant}` vocabulary — collapsing or **omitting kinds the language lacks**
(per AC#1) rather than inventing new ones.

```text
<Lang>Parser ──▶ <lang>SymbolExtractor (gotreesitter CST) ──▶ nodes / edges / PendingRefs
                 │  pass 1: top-level defs → closed node set (kinds the language has)
                 │  pass 2: uses → intra-file edges  vs  selector/cross-file PendingRefs
                 │  imports → ImportSpec (+ surfaced in References); never EdgeImports
                 ▼  one RegisterDefaults line  +  one grammar_subset_<lang> tag
```

### Languages shipped (19, SW-054) + 1 reference (TypeScript, SW-053)

Mechanical-mirror (TS-adjacent), C-family/OO, heavier-divergence, and markup/config
families, grouped by extractor effort:

- **TS-adjacent mirror:** JavaScript, TSX.
- **C-family / OO:** Python, Java, C, Ruby, Rust, PHP, C#, Kotlin.
- **Heavier divergence:** C++ (namespaces), Bash (sparse type system), SQL
  (statement-oriented, no callables), Lua (tables-as-objects).
- **Markup / config (collapse to `file` + few def kinds, exercising the absent-by-design
  path):** CSS, YAML, TOML, Markdown, HCL/Terraform.

Each carries a committed golden fixture asserting the **exact closed node set + literal
kinds**, the intra-file `defines`/`calls`/`references` edges with full provenance and at
least one **use-site** `file:line` pin, a cross-file/selector negative case (or, for
languages without selector/import syntax — SQL, Bash — the **documented absence** of
those forms), and **byte-identical determinism** across repeated and 32-goroutine
concurrent parses (green under `-race`).

### Subset-tagged default build (AC#4)

The shipped default build is subset-tagged via the single source of truth
`internal/release.DefaultGrammarSubsetTags` (umbrella `grammar_subset` + one
`grammar_subset_<lang>` per registered language), applied by the canonical `cmd/release`
build. It embeds **exactly the 20 registered blobs** (TypeScript + 19 SW-054), **not the
all-206 default embed** and **no unregistered blob**, builds green under
`CGO_ENABLED=0`, and passes `internal/cgoconformance` with no offender named. Measured
size: **18,979,778 B (~18.1 MB)** — well under the **< 50 MB** whole-binary hard gate
(the marginal cost over the SW-053 TS-only subset is only ~3.3 MiB of parse tables; the
~3.13 MB pure-Go runtime was already paid by SW-053). See
[`../bench/lang-budget.md`](../bench/lang-budget.md) for the per-blob deltas.

### Reasoning

The goal of SW-054 is to **complete the user-visible language coverage** of the default,
CGo-free tier: a single static `CGO_ENABLED=0` binary that parses the curated tier-1
languages with zero outbound network and no telemetry, with all grammars module-pinned
and Go-embedded at build time. The recipe was fully proven in SW-053, so each language is
a mechanical clone over a disjoint file pair — breadth, not new architecture. Grammars
that cannot enter the pure-Go subset default tier are **deferred to `graphi-broad`
(SW-056)**.

### HTML deferred to `graphi-broad` (SW-056)

One frozen-list language — **HTML** — is **deferred to SW-056** despite having a pure-Go
gotreesitter grammar. In gotreesitter v0.20.2 the shared HTML external-scanner core
(`goLexerAdapter`, `htmlDeserializeTagsInto`, `htmlScan*`) is physically co-located in
`blade_scanner.go`, gated `grammar_subset_blade`. A subset build with only
`grammar_subset_html` therefore **fails to compile**, and enabling `grammar_subset_blade`
would embed an **unregistered `blade.bin` blob** — prohibited by AC#4. HTML's extractor
is intentionally not landed in SW-054 to keep the default subset build green; re-evaluate
when upstream splits the HTML scanner core into an html-gated file. This is a
**build-packaging** deferral (subset-isolation), distinct from the SW-056 CGO-only-grammar
deferral path. See [`../bench/lang-budget.md`](../bench/lang-budget.md) for the full note.
