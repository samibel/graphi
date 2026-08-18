# Language support

Per-language coverage of the parser registry and the cross-file linker. The
tier vocabulary (GA / Preview / Labs / Source-only) is defined in
[`stability-tiers.md`](stability-tiers.md); the machine-checked inventory is
the [coverage matrix](coverage-matrix.md).

The parser registry is open/closed — languages plug in behind a stable seam
without touching existing code.

> **Go is the only GA language.** Every other language in the table below is
> **Preview**: it ships, it is usable, it runs the same 12 GA operations — but it
> is outside the GA promise and its accuracy is unproven. Preview languages resolve
> cross-file references at the `heuristic` tier only; **Go alone** additionally gets
> type-checker-`confirmed` edges (`engine/typeresolve`).

**Default tier (CGo-free, shipped binary).** Two stdlib parsers plus **20**
subset-tagged pure-Go `gotreesitter` grammars. The shipped default is built with
`-tags 'grammar_subset grammar_subset_<lang> …'`
([`internal/release.DefaultGrammarSubsetTags`](../internal/release/build.go)) so only
these languages' grammar blobs are embedded — never the all-206 default embed.

| Language | Tier | Capability level ³ | Symbol nodes | Intra-file edges | Cross-file/package edges |
|---|---|---|---|---|---|
| **Go** | **GA** | `typed-confirmed` | ✅ func / method / type / var / const / file | ✅ `defines`, `calls`, `references` | ✅ `calls` / `references` / `imports` (linker pass, heuristic tier) + `confirmed`-tier go/types edges ¹ |
| JSON | Preview | `parse-only` | structural (AST) | — | — |
| TypeScript · TSX/JSX · JavaScript | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **Python** | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| Ruby · PHP · Lua | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| Java · Kotlin · C# | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| C · C++ · Rust | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| Bash/Shell | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `imports` (per-language resolver, heuristic tier) ² |
| SQL | Preview | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | — (no provable cross-file refs at this tier; resolver skips+counts) ² |
| CSS · YAML · TOML · Markdown · HCL/Terraform | Preview | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| HTML | Source-only | — (not registered) | ✖ not shipped — grammar exists upstream but is not subset-buildable in isolation (see below) | — | — |

> ³ **Capability level** is the machine-readable grade the P1 trust surface reports per
> language (`graphi trust-report --json`, field `capabilities`; PRD v1.0 §3/§5). The four
> levels are `typed-confirmed` > `cross-file-heuristic` > `intra-file-only` > `parse-only`,
> and each names the STRONGEST evidence available for that language — not overall support
> quality, and not a score.
>
> **This column is not maintained by hand.** The surface derives it at read time from the
> live registries — `typeresolve.Languages()` for type-checking, `link.Linker.Languages()`
> for cross-file resolvers, and `core/parse`'s `SymbolCapable` declaration for symbol
> extraction — and `surfaces/client/capability_test.go` re-derives every expectation from
> those same registries, so the matrix cannot drift away from what the code actually does.
> If this table and `--json` ever disagree, `--json` is right and this table is stale.
> SQL sits at `cross-file-heuristic` because a resolver IS registered for it; that resolver
> currently proves no cross-file references and counts skips instead, which is a resolver
> outcome, not a missing capability.

## How cross-file resolution actually works, language by language

> ¹ The cross-file / cross-package **linker pass** ([`engine/link`](../engine/link)) is
> wired into ingest and resolves Go references against the fully-committed node set:
> same-package cross-file bare-ident calls/refs (`derived` tier) and cross-package
> selector calls (`pkg.Fn`, `recv.Method`) plus file→file `imports` (`heuristic` tier,
> with file:line evidence). It preserves the byte-identical full-vs-incremental invariant
> and the rename/move cascade. The linker is **never** `confirmed`: unresolved or ambiguous
> references are dropped deterministically, never fabricated. Since v0.2.0 a third
> ingest phase ([`engine/typeresolve`](../engine/typeresolve)) runs the stdlib go/types
> checker over the whole repository and upserts type-checker-**proven** Go
> `calls`/`references`/`implements` edges at the `confirmed` tier (confidence 1.0) on
> top of the linker's output — correct receiver-type method dispatch, shadowing, and
> import resolution. A package the checker cannot prove (parse error, import cycle)
> keeps its heuristic edges; kill switch: `GRAPHI_NO_TYPERESOLVE=1`.
>
> ² Intra-file extraction ships for every language above. One per-language
> cross-file resolver (`resolve_<lang>.go`) over the same `engine/link` registry seam
> (Open/Closed — a new language is a new `Register` call in `link.New()`, never an edit
> to an existing resolver). Ingest dispatches the linker per language. **Shipped:**
> Go; **TypeScript family** (relative ESM imports, named/namespace bindings; non-relative/
> aliased specifiers and `tsconfig` paths are external → skipped — no path-mapping);
> **Python · Rust · Java · Kotlin** (clause-keyed module/FQN resolution — Python dotted
> modules, Rust `::` paths, Java/Kotlin FQNs key on their package segment); **C#**
> (`using` namespaces as ambient clauses); **C · C++** (`#include` translation units —
> file→file imports + ambient include-dir calls; **no overload resolution** → ambiguous
> calls skip+count); **Ruby · PHP · Lua · Bash** (relative `require`/`source` →
> file→file imports + same-/ambient-dir calls). **SQL** has no provable cross-file
> references at this tier, so its resolver deliberately resolves nothing (skip+count).
> Every cross-file edge is `heuristic` tier with file:line evidence and is **never**
> `confirmed`; unresolved/ambiguous references are dropped and counted, never fabricated.

## What an `imports` edge targets

An `imports` edge targets the imported package's **source** files — never every
file that happens to sit in the resolved directory. Membership is decided on the
**file extension**, by the asking resolver, at read time
([ADR 0011](adr/0011-imports-edge-targets-package-source-files.md)). Three
families, because the three import mechanisms differ:

| Family | Languages | Target set |
|---|---|---|
| Directory fan-out | **Go** | file→file, `.go` **minus `_test.go`** |
| | **Python** | file→file, `.py` **and** `.ipynb`; `test_*.py` is included |
| | **C#** | file→file, `.cs` |
| | **Rust** | file→file, `.rs` |
| Interned package node | **Java · Kotlin** | ONE file→`package` edge; no directory fan-out exists to filter |
| Exact target path | **TypeScript family · C · C++ · Ruby · PHP · Lua · Bash** | the resolver already names the exact file (`./util.ts`, `util.h`, `require './x'`); only a committed node at that path emits an edge |
| No `imports` edge at all | **SQL** | its resolver deliberately resolves nothing (skip+count), so there is no target set |

Go excludes `_test.go` because a test file is a package member but is **not
importable** — the same ruling [`engine/typeresolve`](../engine/typeresolve)
already makes. Python does **not** exclude `test_*.py`, because a Python test
module is an ordinary importable module; the Go rule rests on non-importability,
not on the word "test". The filter binds the **target** side only: a `_test.go`
file remains a first-class importer.

Two consequences are accepted rather than hidden, and they are the ones a file
actually has to be a graph node to suffer. A **non-source build input in the
package directory** — a `.sql`, `.md`, `.yml`/`.yaml`, `.toml`, `.css` or `.sh`,
whether pulled in by `//go:embed` or merely co-located — and a **cgo package's
`.c`/`.h` sources** lose their only graph path, because graphi models no embed,
codegen or cgo relation. A third follows at the operation level: a `.md` or
`.yml` used as a `related_files` **anchor** now answers empty, because that
inbound `imports` edge was its only cross-file path.

A file kind with **no registered parser** (`.proto`, `.tmpl`, `.txt`) or with a
**parse-only** parser (`.json`) is never a committed `file` node in the first
place, so it was never an `imports` target and loses nothing here. Neither does
anything under `testdata/`, which is a different directory from the one the
import resolves to. An earlier version of this page named those as losses; see
the dated correction at the top of
[ADR 0011](adr/0011-imports-edge-targets-package-source-files.md).

## Deferred / not in the default tier

- **HTML** — has a pure-Go grammar but is **not subset-buildable in isolation** in
  gotreesitter v0.20.2 (its scanner core is co-located with `grammar_subset_blade`
  upstream), so it is **deferred** and **not shipped** in the default tier. Re-evaluate
  when upstream splits the HTML scanner out.
- **Dockerfile / Protobuf / GraphQL** — **not** in the committed tier-1 set (follow-up).
- **`zig` and the broad long tail** — available **only** in the opt-in `graphi-broad`
  CGO build ([`graphi-broad.md`](graphi-broad.md)), never in the CGo-free default.
  Read that document's residual-security warning before pointing the broad flavor
  at untrusted source.

The frozen tier-1 list and the corrected (one-time runtime + per-blob) binary-budget
model live in [`../bench/lang-budget.md`](../bench/lang-budget.md); the curated-tier
resolution and the full per-language blob deltas are recorded in that file.
