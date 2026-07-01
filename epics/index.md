# graphi — Epic Registry & Status

> Traceability map for PB-001 → EP-001..EP-008. It's the in-repo answer to
> "has every point in the concept been worked on?" Status reflects code present
> on the default branch and verified by tests/CI — not intent.
>
> **Single source of truth for capabilities:** the machine-checked
> [capability coverage matrix](../docs/coverage-matrix.md) (CI-enforced —
> docs-vs-code drift breaks the build). Design context lives in the
> [Architecture Plan](../docs/architecture-plan.md). The epic statuses below are
> reconciled to that matrix.

## Legend

- ✅ **shipped** — implemented, tested, and (where applicable) CI-gated.
- 🟡 **partial** — core implemented; named follow-ups remain.
- ⏳ **planned** — scoped, not yet started.

## Epics

| Epic | Title | Status | Key packages | Notes |
|---|---|---|---|---|
| EP-001 | Core code graph & structural queries | ✅ shipped | `core/parse`, `core/graphstore`, `engine/{query,search,ingest,link}` | Graph, queries, search, incremental ingest, and the daemon are all shipped. **23 CGo-free parsers** are registered (FU-2 ✅; 22 shipped + 1 html ⏳ planned), **optional semantic search** is wired (FU-3 ✅, OFF by default), and the **cross-file/cross-package linker** (FU-1 ✅, `engine/link`) runs post-ingest in both full and incremental paths while preserving the byte-identical invariant. |
| EP-002 | Local-first trust, DevOps & eval | ✅ shipped | `internal/{canary,cgoconformance,audit}`, `.github/workflows/*` | Egress canary, CGo-free gate, zero-telemetry, ledger audit, eval, reproducible release, layer-direction guard. |
| EP-003 | Token-savings ledger & token-efficient context | ✅ shipped | `engine/{meter,price,ledger,cap,context}` | Per-call metering, embedded USD price table, durable JSONL ledger, anti-gaming cap, `graphi savings`. |
| EP-004 | Impact analysis & semantic queries | ✅ shipped | `engine/analysis/{impact,callchain,concept,metrics,batched}` | Operates on the graph; now exercised by real extracted Go nodes/edges. |
| EP-005 | Deep analysis (taint, PDG, interproc, contracts, git-history) | ✅ shipped | `engine/analysis/{taint,pdg,interproc,contracts,githistory}` | Analyzers plus MCP/CLI wiring. Depth and recall scale with extraction coverage (see FU-1/FU-2). Taint is held to PB-001 §10's "100% recall on labeled set" requirement by a committed labeled corpus and a recall gate (`engine/analysis/taint/{corpus,recall}_test.go`) that fails CI on recall **or** precision drift. |
| EP-006 | Graph-aware edits & refactoring | ✅ shipped | `engine/edit` | Atomic edit saga, rename/extract/move/signature refactor, provenance + recovery, MCP/CLI surface. |
| EP-007 | GitHub PR review vertical | ✅ shipped | `engine/review`, `extensions/github-action` | Risk/signals/questions/gate + sticky comment; GitHub host asserted as sole network user. |
| EP-008 | Multi-surface expansion | ✅ shipped | `surfaces/{http,daemon,cli,mcp,tui}`, `web/`, `extensions/vscode` | HTTP/SSE, React+Sigma web client, TUI, VS Code ext, `graphi setup`/`privacy-audit`. Parity-tested across surfaces. |
| EP-009 | Curated language tier (consolidation) | ✅ shipped | `core/parse`, `core/community` (later), `internal/release` | Frozen tier-1 list (`bench/lang-budget.md`): 21 CGo-free gotreesitter grammars plus 2 stdlib (Go, JSON), with an opt-in `graphi-broad` CGO flavor and `zig` wired in. Covers SW-052…SW-057. |
| EP-011 | Compound + hierarchy queries | ✅ shipped | `engine/query/{compound,dispatch}`, `surfaces/{mcp,client,cli}` | `compound` (Cypher-style) + `implementers`/`implements`/`overrides`/`subtypes`/`supertypes` queries over the new inherits/implements/overrides edge vocabulary. |
| EP-012 | Agent memory & skills | ✅ shipped | `engine/{memory,distill,skillgen}` | `graphi memory`, `graphi distill`, `graphi skillgen`; CLI + MCP + HTTP. Deterministic, local, no LLM. |
| EP-013 | Pattern queries (AST + clones) | ✅ shipped | `engine/query/{searchast,findclones}` | `search_ast` + `find_clones` pattern queries; CLI + MCP + HTTP. |
| EP-015 | Diagnostics & code actions | ✅ shipped | `engine/diagnostic/`, `engine/edit/inline.go`, `engine/edit/safe_delete.go`, `engine/edit/serialize.go`, `surfaces/ep015_parity_test.go` | Covers SW-091…SW-094: `graphi diagnose` (graph-derived severity + suggested code-action), `graphi inline` (reference-correct inline refactor with fail-safe block list), and `graphi safe-delete` (reference-safety-gated) — sharing a marshaller and a byte-parity harness. |
| EP-016 | Live IDE transport | ✅ shipped | `engine/overlay/`, `surfaces/daemon/control.go`, `surfaces/daemon/service.go`, `surfaces/mcp/http.go`, `engine/observe/class.go`, `surfaces/guard/`, `internal/canary/gate.go` | Covers SW-095…SW-099: in-memory editor overlay; daemon control-plane RPCs plus stop and OS service templates; MCP streamable-HTTP transport with stdio envelope parity; granular per-class SSE subscriptions; and a **zero-egress enforcement guard** with a central loopback/egress chokepoint. |
| EP-017 | Notebooks, watcher, interproc taint, communities | ✅ shipped | `engine/ingest/notebook.go`, `engine/watch/`, `engine/analysis/interproctaint/`, `core/community/louvain.go`, `engine/community/detector.go`, `engine/analysis/communities.go`, `engine/analysis/notebookingest.go`, `engine/analysis/watcherstatus.go`, `engine/conformance/` | Covers SW-100…SW-104: `.ipynb` cell-provenance ingestion; an `fsnotify` watcher with a bounded worker-pool and deterministic canonical-ordered apply; interprocedural taint fixpoint over Sharir–Pnueli summaries; deterministic Louvain community detection behind the grouping seam; and a single-dispatch surfacing plus full-vs-incremental byte-parity conformance gate. |
| EP-018 | PR-tool suite | ✅ shipped | `surfaces/forge/forge.go`, `engine/analysis/triage.go`, `engine/analysis/conflicts.go`, `engine/analysis/suggest_reviewers.go`, `engine/analysis/compare_branches.go`, `engine/analysis/critique_review.go` | Covers SW-105…SW-108: `list_prs` (forge enumeration), `triage_prs` (graph-derived ranking), `conflicts_prs` (textual + graph-semantic + asymmetric contract-dependency), `suggest_reviewers` (ownership/churn + affected-subgraph proximity), `compare_branches` (graph-level NodeId-keyed diff), and `critique_review` (deterministic graph-evidence critique of an existing review). Engine egress stays at zero; the only egress is at the surface boundary. |

## Resolved concept decisions (PB-001 frontmatter)

| # | Topic | Decision | In-repo state |
|---|---|---|---|
| OQ1 | Parsing | Hybrid: CGo-free default + opt-in `graphi-broad` | ✅ Curated CGo-free tier (22 parsers shipped + html ⏳ planned) + opt-in `graphi-broad` CGO flavor shipped (FU-2). |
| OQ2 | License | Apache-2.0 | ✅ Full Apache-2.0 text in [`LICENSE`](../LICENSE); third-party attributions in [`NOTICE`](../NOTICE). |
| OQ3 | Name | graphi | ✅ |
| OQ4 | Headline metric | ~50× fewer tokens, eval-gated | ✅ `internal/eval` claim gate on committed dataset. |
| OQ5 | Launch hero | Claude Code (`graphi setup`) | ✅ |
| OQ6 | Default embedder | Graceful-skip until configured | ✅ Resolved — optional `engine/embed` + semantic search, OFF by default, graceful-skip until configured (FU-3 / SW-059). |

## Follow-up stories (the remaining "points")

These are the concrete gaps surfaced by the PB-001 coverage audit, sequenced by
leverage. Status is reconciled to the [coverage matrix](../docs/coverage-matrix.md).

- **FU-1 — Cross-file / cross-package linker pass.** ✅ **shipped**
  `engine/link` is a pure, store-free linker that runs *after* all symbols are
  committed. It resolves selector calls (`pkg.Fn`, `recv.Method`) and imports
  against the fully-committed symbol index, then emits cross-file
  `calls`/`references`/`imports` edges with provenance (name-resolved calls are
  `derived`; cross-package/selector/`recv.Method` carry the heuristic tier). It's
  wired into both the full and incremental ingest paths
  (`engine/ingest/ingest.go`) and preserves the full-vs-incremental byte-identical
  invariant (deterministic edge ordering, idempotent across repeated `Link`).
  This unlocks whole-repo callers/callees, impact, and taint analysis on real code.
  **Scope:** FU-1 shipped the Go resolver; the per-language resolvers for the other
  tier-1 grammars (`resolve_<lang>.go`) shipped under **FU-5**, over the same
  `engine/link` seam.

- **FU-2 — Curated pure-Go language tier + `graphi-broad`.** ✅ **shipped**
  22 CGo-free parsers are registered behind the `RegisterDefaults` seam (2 stdlib
  plus 20 subset-tagged pure-Go `gotreesitter` grammars), with `CGO_ENABLED=0`
  and the <50 MB budget green, plus the build-tagged opt-in `graphi-broad` CGO
  flavor. Resolves OQ1. (HTML remains deferred — the coverage matrix tracks it as
  `planned` so the guard fails if it silently becomes live.)

- **FU-3 — Optional semantic search (graceful-skip path + generation).** ✅ **shipped (SW-059 + SW-061)**
  The `engine/embed` registry has a graceful-skip default (semantic search stays
  OFF until an embedder such as loopback Ollama is configured), with a
  build-tag-gated ONNX carve-out so the default binary stays CGo-free. SW-061
  closed the generation gap: `graphi index --semantic` embeds every node (keyed by
  `node_id`) and persists the vectors to a durable SQLite `vectors` sidecar
  (tagged with embedder identity and dimension), while `graphi search -semantic`
  reloads them into the in-memory brute-force cosine index on startup — a pure
  local read, with no re-embed and no dial-out — so the documented enable-flow
  returns ranked hits. (Brute-force cosine is retained; HNSW is an explicit
  follow-up.) Resolves OQ6.

- **FU-4 — Traceability docs + CI-enforced coverage matrix.** ✅ **shipped (SW-060)**
  The consolidated [Architecture Plan](../docs/architecture-plan.md) is the single
  design entry point, and the [capability coverage matrix](../docs/coverage-matrix.md)
  is **machine-checked against the live registries** — docs-vs-code drift
  (caught by `internal/coverage` and the `coverage-matrix` CI gate) breaks the
  build. This registry, the README language matrix, and the coverage matrix are
  all reconciled to a single source of truth.

- **FU-5 — Per-language cross-file resolvers.** ✅ **shipped (SW-063)**
  Each language gets its own resolver (`resolve_<lang>.go`) over the store-free
  `engine/link` registry seam (Open/Closed — a new language is a new `Register`
  call in `link.New()`, never an edit to an existing resolver). Ingest
  **dispatches the linker per language** (`FileRefs.Language` plus grouped
  `Link(lang, …)` in `engine/ingest/ingest.go`), so each registered resolver sees
  only its own files. Shared, language-agnostic resolution machinery (same-dir
  derived, relative-dir/clause/ambient cross-file heuristic, file-to-file imports,
  deterministic skip+count) lives in `resolve_common.go`; each resolver only
  models its own language's import binding.
  - **Resolvers:** Go · TypeScript family (ts/tsx/js — relative ESM, named/namespace;
    non-relative/aliased + `tsconfig` paths external → skip, no path-mapping) ·
    Python (dotted modules) · Rust (`::` paths + `mod`) · Java · Kotlin (FQN, clause =
    package segment) · C# (`using` namespaces as ambient clauses) · C · C++
    (`#include` translation units; **no overload resolution** → ambiguous skip+count) ·
    Ruby · PHP · Lua · Bash (relative `require`/`source`). **SQL** is an honest no-op
    (no provable cross-file refs at this tier → skip+count).
  - Every resolver honours the linker's invariants: tier derived from the resolution
    class (never `confirmed`), unresolved/ambiguous refs dropped and counted, no
    fabrication, a byte-identical full-vs-incremental graph (including
    rename/move cascades), `engine/link` staying store-free/I/O-free with no
    `SymbolIndex` edits, and a CGO-free default build. The README and coverage
    matrix are kept in sync per language.
