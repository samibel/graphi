# graphi — Epic Registry & Status

> Traceability map for PB-001 → EP-001..EP-008. This is the in-repo answer to
> "has every point in the concept been worked on?" Status reflects code present
> on the default branch, verified by tests/CI, not intent.

## Legend

- ✅ **shipped** — implemented, tested, and (where applicable) CI-gated.
- 🟡 **partial** — core implemented; named follow-ups remain.
- ⏳ **planned** — scoped, not yet started.

## Epics

| Epic | Title | Status | Key packages | Notes |
|---|---|---|---|---|
| EP-001 | Core code graph & structural queries | 🟡 partial | `core/parse`, `core/graphstore`, `engine/{query,search,ingest}` | Graph, queries, search, incremental ingest, daemon all shipped. **Go symbol extraction now populates the graph** (intra-file). Cross-file/package linker + more languages outstanding (see FU-1, FU-2). |
| EP-002 | Local-first trust, DevOps & eval | ✅ shipped | `internal/{canary,cgoconformance,audit}`, `.github/workflows/*` | Egress canary, CGo-free gate, zero-telemetry, ledger audit, eval, reproducible release, layer-direction guard. |
| EP-003 | Token-savings ledger & token-efficient context | ✅ shipped | `engine/{meter,price,ledger,cap,context}` | Per-call metering, embedded USD price table, durable JSONL ledger, anti-gaming cap, `graphi savings`. |
| EP-004 | Impact analysis & semantic queries | ✅ shipped | `engine/analysis/{impact,callchain,concept,metrics,batched}` | Operates on the graph; now exercised by real extracted Go nodes/edges. |
| EP-005 | Deep analysis (taint, PDG, interproc, contracts, git-history) | ✅ shipped | `engine/analysis/{taint,pdg,interproc,contracts,githistory}` | Analyzers + MCP/CLI wiring. Depth/recall scale with extraction coverage (see FU-1/FU-2). |
| EP-006 | Graph-aware edits & refactoring | ✅ shipped | `engine/edit` | Atomic edit saga, rename/extract/move/signature refactor, provenance + recovery, MCP/CLI surface. |
| EP-007 | GitHub PR review vertical | ✅ shipped | `engine/review`, `extensions/github-action` | Risk/signals/questions/gate + sticky comment; GitHub host asserted as sole network user. |
| EP-008 | Multi-surface expansion | ✅ shipped | `surfaces/{http,daemon,cli,mcp,tui}`, `web/`, `extensions/vscode` | HTTP/SSE, React+Sigma web client, TUI, VS Code ext, `graphi setup`/`privacy-audit`. Parity-tested across surfaces. |

## Resolved concept decisions (PB-001 frontmatter)

| # | Topic | Decision | In-repo state |
|---|---|---|---|
| OQ1 | Parsing | Hybrid: CGo-free default + opt-in `graphi-broad` | Seam present (`core/parse`); curated tier & broad build are FU-2. |
| OQ2 | License | Apache-2.0 | ✅ |
| OQ3 | Name | graphi | ✅ |
| OQ4 | Headline metric | ~50× fewer tokens, eval-gated | ✅ `internal/eval` claim gate on committed dataset. |
| OQ5 | Launch hero | Claude Code (`graphi setup`) | ✅ |
| OQ6 | Default embedder | Graceful-skip until configured | ⏳ embedder/semantic search not yet present (FU-3). |

## Open follow-up stories (the remaining "points")

These are the concrete gaps surfaced by the PB-001 coverage audit. They are
sequenced by leverage.

- **FU-1 — Cross-file / cross-package linker pass.** ⏳
  `core/parse/extract_go.go` emits only edges provable within one file because
  `graphstore.PutEdge` requires both endpoints to exist and ingest commits one
  file at a time. Add a post-ingest linker that resolves selector calls
  (`pkg.Fn`, `recv.Method`) and imports against the fully-committed symbol table,
  emitting cross-file `calls`/`references`/`imports` edges while preserving the
  full-vs-incremental byte-identical invariant. Highest leverage: unlocks
  whole-repo callers/callees, impact, and taint on real code.

- **FU-2 — Curated pure-Go language tier + `graphi-broad`.** ⏳
  Beyond Go/JSON, add CGo-free tier-1 grammars (target ~20–40 languages) behind
  the existing `RegisterDefaults` seam, generalizing the extractor over a
  language-neutral node/edge mapping, keeping `CGO_ENABLED=0` and the <50 MB
  budget green. Then add the build-tagged `graphi-broad` CGO flavor wiring the
  257-grammar set (zero-egress + no-telemetry still enforced; the hard CGo-free
  gate is exempted for that flavor only). Resolves OQ1 fully.

- **FU-3 — Embedder graceful-skip path.** ⏳
  Add an `engine/embed` registry with a graceful-skip default (semantic search
  off until an embedder such as Ollama is configured), build-tag-gated ONNX
  carve-out so the default binary stays CGo-free. Resolves OQ6.

- **FU-4 — Traceability docs.** 🟡
  This registry + the README language-support matrix are the first installment.
  A consolidated `docs/architecture-plan.md` and a per-capability feature
  coverage matrix (CI-failing on drift) remain.
