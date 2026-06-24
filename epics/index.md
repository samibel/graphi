# graphi — Epic Registry & Status

> Traceability map for PB-001 → EP-001..EP-008. This is the in-repo answer to
> "has every point in the concept been worked on?" Status reflects code present
> on the default branch, verified by tests/CI, not intent.
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
| EP-001 | Core code graph & structural queries | ✅ shipped | `core/parse`, `core/graphstore`, `engine/{query,search,ingest,link}` | Graph, queries, search, incremental ingest, daemon all shipped. **22 CGo-free parsers** registered (FU-2 ✅), **optional semantic search** wired (FU-3 ✅, OFF by default), and the **cross-file/cross-package linker** (FU-1 ✅, `engine/link`) runs post-ingest in both full and incremental paths while preserving the byte-identical invariant. |
| EP-002 | Local-first trust, DevOps & eval | ✅ shipped | `internal/{canary,cgoconformance,audit}`, `.github/workflows/*` | Egress canary, CGo-free gate, zero-telemetry, ledger audit, eval, reproducible release, layer-direction guard. |
| EP-003 | Token-savings ledger & token-efficient context | ✅ shipped | `engine/{meter,price,ledger,cap,context}` | Per-call metering, embedded USD price table, durable JSONL ledger, anti-gaming cap, `graphi savings`. |
| EP-004 | Impact analysis & semantic queries | ✅ shipped | `engine/analysis/{impact,callchain,concept,metrics,batched}` | Operates on the graph; now exercised by real extracted Go nodes/edges. |
| EP-005 | Deep analysis (taint, PDG, interproc, contracts, git-history) | ✅ shipped | `engine/analysis/{taint,pdg,interproc,contracts,githistory}` | Analyzers + MCP/CLI wiring. Depth/recall scale with extraction coverage (see FU-1/FU-2). Taint is held to PB-001 §10 "100% recall on labeled set" by a committed labeled corpus + recall gate (`engine/analysis/taint/{corpus,recall}_test.go`) that fails CI on recall **or** precision drift. |
| EP-006 | Graph-aware edits & refactoring | ✅ shipped | `engine/edit` | Atomic edit saga, rename/extract/move/signature refactor, provenance + recovery, MCP/CLI surface. |
| EP-007 | GitHub PR review vertical | ✅ shipped | `engine/review`, `extensions/github-action` | Risk/signals/questions/gate + sticky comment; GitHub host asserted as sole network user. |
| EP-008 | Multi-surface expansion | ✅ shipped | `surfaces/{http,daemon,cli,mcp,tui}`, `web/`, `extensions/vscode` | HTTP/SSE, React+Sigma web client, TUI, VS Code ext, `graphi setup`/`privacy-audit`. Parity-tested across surfaces. |

## Resolved concept decisions (PB-001 frontmatter)

| # | Topic | Decision | In-repo state |
|---|---|---|---|
| OQ1 | Parsing | Hybrid: CGo-free default + opt-in `graphi-broad` | ✅ Curated 22-language CGo-free tier + opt-in `graphi-broad` CGO flavor shipped (FU-2). |
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
  committed: it resolves selector calls (`pkg.Fn`, `recv.Method`) and imports
  against the fully-committed symbol index and emits cross-file
  `calls`/`references`/`imports` edges with provenance (name-resolved calls
  `derived`; cross-package/selector/`recv.Method` carry the heuristic tier). It is
  wired into both the full and incremental ingest paths
  (`engine/ingest/ingest.go`) and preserves the full-vs-incremental byte-identical
  invariant (deterministic edge ordering, idempotent across repeated `Link`).
  This unlocks whole-repo callers/callees, impact, and taint on real code.

- **FU-2 — Curated pure-Go language tier + `graphi-broad`.** ✅ **shipped**
  22 CGo-free parsers registered behind the `RegisterDefaults` seam (2 stdlib +
  20 subset-tagged pure-Go `gotreesitter` grammars), `CGO_ENABLED=0` and the
  <50 MB budget green, plus the build-tagged opt-in `graphi-broad` CGO flavor.
  Resolves OQ1. (HTML remains deferred — see the coverage matrix `planned` row.)

- **FU-3 — Optional semantic search (graceful-skip path + generation).** ✅ **shipped (SW-059 + SW-061)**
  `engine/embed` registry with a graceful-skip default (semantic search OFF until
  an embedder such as loopback Ollama is configured), build-tag-gated ONNX
  carve-out so the default binary stays CGo-free. SW-061 closes the generation gap:
  `graphi index --semantic` embeds every node (keyed by `node_id`) and persists the
  vectors to a durable SQLite `vectors` sidecar (tagged with embedder identity +
  dimension); `graphi search -semantic` reloads them into the in-memory brute-force
  cosine index on startup — a pure local read, no re-embed, no dial — so the
  documented enable-flow returns ranked hits. (Brute-force cosine retained; HNSW is
  an explicit follow-up.) Resolves OQ6.

- **FU-4 — Traceability docs + CI-enforced coverage matrix.** ✅ **shipped (SW-060)**
  The consolidated [Architecture Plan](../docs/architecture-plan.md) is the single
  design entry point, and the [capability coverage matrix](../docs/coverage-matrix.md)
  is **machine-checked against the live registries** — a docs-vs-code drift
  (`internal/coverage` + the `coverage-matrix` CI gate) breaks the build. This
  registry, the README language matrix, and the coverage matrix are reconciled to
  a single source of truth.
