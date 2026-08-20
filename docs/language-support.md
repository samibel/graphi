# Language support

Per-language coverage of the parser registry and the cross-file linker. The
tier vocabulary (GA / Preview / Labs / Source-only) is defined in
[`stability-tiers.md`](stability-tiers.md); the machine-checked inventory is
the [coverage matrix](coverage-matrix.md).

The parser registry is open/closed — languages plug in behind a stable seam
without touching existing code.

> **Languages at GA at their declared capability level.** Go is the only language
> at `GA (typed-confirmed)` and additionally gets type-checker-`confirmed` edges
> (`engine/typeresolve`). A language at `GA (cross-file-heuristic)` ships at that
> level with the level printed beside the word GA — `Python — GA (cross-file-heuristic)`,
> never GA alone — and its evidence rows are bound to the G1–G9 gate discipline
> (see `docs/rc/evidence-index.yaml`). Every other language in the table below is
> **Preview**: it ships, it is usable, it runs the same 12 GA operations — but it
> is outside the GA promise and its accuracy is unproven. Preview languages resolve
> cross-file references at the `heuristic` tier only.

**Default tier (CGo-free, shipped binary).** Two stdlib parsers plus **20**
subset-tagged pure-Go `gotreesitter` grammars. The shipped default is built with
`-tags 'grammar_subset grammar_subset_<lang> …'`
([`internal/release.DefaultGrammarSubsetTags`](../internal/release/build.go)) so only
these languages' grammar blobs are embedded — never the all-206 default embed.

| Language | Tier | Capability level ³ | Symbol nodes | Intra-file edges | Cross-file/package edges |
|---|---|---|---|---|---|
| **Go** | **GA (typed-confirmed)** | `typed-confirmed` | ✅ func / method / type / var / const / file | ✅ `defines`, `calls`, `references` | ✅ `calls` / `references` / `imports` (linker pass, heuristic tier) + `confirmed`-tier go/types edges ¹ |
| **Python** | **GA (cross-file-heuristic)** | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **but see open defect LINK-004** ⁵ |
| **TypeScript** | **GA (cross-file-heuristic)** | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **family share one resolver impl, immune to LINK-001 by exact-path resolution** ⁶ |
| **TSX** | **GA (cross-file-heuristic)** | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **family share one resolver impl, immune to LINK-001 by exact-path resolution** ⁶ |
| **JavaScript** | **GA (cross-file-heuristic)** | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **family share one resolver impl, immune to LINK-001 by exact-path resolution** ⁶ |
| **Bash/Shell** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `imports` (per-language resolver, heuristic tier) ² |
| **C** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **C++** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **Java** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **no `imports` edge** |
| **Kotlin** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **no `imports` edge** |
| **C#** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **Ruby** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **PHP** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **Lua** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² |
| **Rust** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `calls` / `references` / `imports` (per-language resolver, heuristic tier) ² — **no `imports` edge** |
| **SQL** | **GA (cross-file-heuristic)** ⁷ | `cross-file-heuristic` | ✅ symbol nodes | ✅ intra-file | ✅ `references` **same-directory only** (`derived` tier) — no `imports` edge, no cross-directory resolution ⁴ |
| **CSS** | **GA (intra-file-only)** ⁷ | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| **YAML** | **GA (intra-file-only)** ⁷ | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| **TOML** | **GA (intra-file-only)** ⁷ | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| **Markdown** | **GA (intra-file-only)** ⁷ | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| **HCL/Terraform** | **GA (intra-file-only)** ⁷ | `intra-file-only` | ✅ symbol nodes | ✅ intra-file | ⏳ per-language resolver (roadmap) ² — no `resolve_<lang>.go` registered in `engine/link`; intra-file nodes only |
| **JSON** | **GA (parse-only)** ⁷ | `parse-only` | structural (AST) | — | — |
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
>
> ⁴ **CORRECTION, 2026-08-19 (SW-183 / [ADR 0012](adr/0012-capability-levels-graded-on-demonstrated-evidence.md)).**
> This footnote previously read: *"SQL sits at `cross-file-heuristic` because a resolver IS
> registered for it; that resolver currently proves no cross-file references and counts skips
> instead, which is a resolver outcome, not a missing capability."* **The claim after the
> semicolon was false**, and the SQL row above said the same thing. SQL's resolver **does**
> prove cross-file references: a view referencing a table declared in a sibling file resolves
> at the `derived` tier. Established by measurement plus two counterfactuals — removing the
> registration makes the edge disappear, and emptying the resolver body makes the capability
> audit report an over-claim. The **bound** is published with the capability: resolution is
> **same-directory only** (ISO/IEC 9075 defines no file-inclusion construct, so there is
> nothing to carry a reference across a directory boundary) and SQL emits **no `imports`
> edge**. Record: [`rc/capability-audit-2026-08-19.md`](rc/capability-audit-2026-08-19.md) §4.
>
> **Levels are graded on registration, and CI now checks that against evidence.** The
> derivation asks "is a resolver registered for L?", which a resolver that resolves nothing
> would answer yes to. `surfaces/client/capabilityaudit_test.go` closes that gap: it ingests a
> two-file fixture per shipped language and asserts the derived level against a measured
> cross-file edge, in both directions. All 22 rows are published in the audit record above;
> **no language's derived level over-claims**, and an unfixtured new language fails the build.

> ⁵ **OPEN defect LINK-004 — Python dotted module imports resolve to nothing.**
> `from pkg.util import helper` and `import pkg.util` — module paths naming a module *inside*
> a package — produce **no edge at all**, neither `calls` nor `imports`. Single-segment forms
> (`import util`, `from util import helper`, `from pkg import util`, `from pkg import helper`)
> all resolve. The clause key is the module path's last dotted segment while a symbol's clause
> is its parent **directory** base, and for `pkg.util` those differ. Affects `related_files`,
> `callers`, `callees`, `impact` and `neighborhood` on Python. **Workaround:** import the
> package rather than the module — `from pkg import util` then `util.helper()` resolves, and
> additionally emits the `imports` edge. Not fixed; disclosed per the contract in
> `readme.md` and the doctor `known-defects` check. Record:
> [`rc/capability-audit-2026-08-19.md`](rc/capability-audit-2026-08-19.md) §3.
>
> ⁶ **TypeScript family — exact-path resolution; immune to LINK-001 by construction.**
> The TS family (`typescript`, `tsx`, `javascript`) is registered as **one** resolver impl
> because the three parsers emit the same ESM/CJS import surface and the same
> `cstWalk`-extracted binding shapes. Path resolution is **relative-only** (D1): `./x` /
> `../x` resolve against the importing file's directory; non-relative / aliased specifiers
> (`react`, `@app/x`, `tsconfig` `paths`) are external and skip+counted — no tsconfig
> path-mapping. **The target set is the specifier's resolved FILE, never the directory.**
> `import { g } from "../lib/util"` lands exactly one `imports` edge at `lib/util.ts` and
> no edge at `lib/extra.ts` / `lib/README.md` / `lib/util.test.ts` siblings, because there is
> no directory fan-out to filter — the specifier already names the file. This is the
> principled reason the TS family is **structurally immune** to the LINK-001 target-set
> class (Go's directory-fan-out filter, which would let a `import "./util"` land on
> every committed `.go` source sibling in the package instead of on the single
> import-named file). The control test pinning this is
> `engine/link/resolve_typescript_test.go::TestTSLink_NoDirectoryFanOut` (SW-182 AC-4).
>
> ⁷ **Capability-audit row attestation (F5-surface, this commit).** Every GA row in the
> table above binds to a numbered row in
> [`rc/capability-audit-2026-08-19.md`](rc/capability-audit-2026-08-19.md) (pinned at
> sha `deecee9e3a51707aa2d198abf91dc0b0a01573e6`, the audit record's own commit):
>
> | Row | Language | Audit row |
> |---|---|---|
> | 2 | Bash/Shell | row 2 |
> | 3 | C | row 3 |
> | 4 | Java | row 4 |
> | 5 | C++ | row 5 |
> | 6 | Kotlin | row 6 |
> | 8 | C# | row 8 |
> | 9 | Ruby | row 9 |
> | 10 | PHP | row 10 |
> | 12 | Lua | row 12 |
> | 13 | Rust | row 13 |
> | 14 | SQL | row 14 |
> | 17 | CSS | row 17 |
> | 18 | YAML | row 18 |
> | 19 | TOML | row 19 |
> | 20 | Markdown | row 20 |
> | 21 | HCL/Terraform | row 21 |
> | 22 | JSON | row 22 |
>
> The Go row (1) is the canonical typed-confirmed attestation that predates the
> 2026-08-19 audit and is not in the table; the Python row (11) is row 11 with the
> LINK-004 annotation already in footnote ⁵; the TypeScript / TSX / JavaScript rows
> (15 / 16 / 7) are bound to footnote ⁶. **The level printed beside the word GA is the
> same word the audit row asserts — never GA alone — and the audit's sha is the
> cite.**

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
> **OPEN defect LINK-002 on the Go `recv.Method` heuristic.** The heuristic half of the
> sentence above under-resolves in one directory shape: when a directory declares **two
> package clauses** — most often a package beside its **external `_test` package** — only
> one clause survives in the index, and methods under the losing clause are never offered
> to the receiver-method heuristic at all. It is **both a recall and a soundness
> defect**: where the surviving clause declares a method of the same name the call is not
> dropped but **redirected** to that unrelated method, because hiding a clause
> manufactures false uniqueness and defeats the resolver's own skip-on-ambiguity rule. It
> is deterministic per tree and present under `fast`, `balanced` and `deep`. **The
> `confirmed` layer is unaffected** — the wrong edge is always `heuristic` tier (0.6), and
> where the receiver's type is import-qualified go/types resolves the call correctly at
> `confirmed` tier alongside it, which is also the workaround (it needs `balanced` or
> `deep`; under `fast` the wrong edge is the only one there is). Measured on graphi's own
> tree: 136 of 1 979 method declarations (6.9 %) unreachable; 21 of 105 method-declaring
> directories hold more than one package clause and 11 lose methods today. How often the
> redirection happens is **not** measured, and the stop-ship ruling is **open**. Read the
> Go **GA** row with this limit attached until it closes:
> [`docs/rc/link-002-clause-by-dir-recall.md`](rc/link-002-clause-by-dir-recall.md).
>
> **OPEN defect LINK-003, on the same heuristic — filed 2026-08-19.** The same resolver
> keeps only **one entry per (package, method-name) pair**, with no ambiguity companion,
> so a package declaring both `func (a *A) String()` and `func (b *B) String()` resolves
> every unqualified `x.String()` to whichever won the last write — a **wrong**
> `heuristic`-tier edge needing no package-clause collision at all. Same affected
> operations, same tier confinement and same workaround as LINK-002. Measured on graphi's
> own tree: **663 of 1 979 (33.5 %)** method declarations unreachable *or* shadowed once
> both defects are counted, against 136 (6.9 %) for LINK-002 alone. **Not fixed**, and the
> eventual fix must close both together: §10 of
> [`docs/rc/link-002-clause-by-dir-recall.md`](rc/link-002-clause-by-dir-recall.md).
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
| No `imports` edge at all | **SQL** · **Java** · **Kotlin** · **Rust** | SQL: ISO/IEC 9075 defines no file-inclusion construct, so there is no target set — its cross-file `references` edges come from same-directory resolution instead (corrected 2026-08-19, SW-183; the earlier wording "its resolver deliberately resolves nothing" was false — see footnote ⁴). Java/Kotlin resolve through the interned `package` node and Rust through `mod`, neither of which yields a file→file edge; measured in [`rc/capability-audit-2026-08-19.md`](rc/capability-audit-2026-08-19.md) §2 |

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
