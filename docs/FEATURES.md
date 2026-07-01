# graphi — Feature Inventory

> Single-source catalogue of every capability graphi ships today, grouped by epic.
> Every entry maps to the machine-checked [coverage matrix](coverage-matrix.md)
> (CI-enforced — docs-vs-code drift breaks the build) and to the live
> `surfaces/mcp.ToolNames()` set. The companion source of truth for the
> `analyze` subcommand set is `engine/analysis/dispatch.go`.
>
> Generated 2026-06-27 against branch `ep-016-live-ide-transport` (HEAD `a6628e6`).
> See [`readme.md` § Diff vs. main](../readme.md#diff-vs-main) for the
> 19-commit, +17,437-line diff against `main`.

## Contents

- [Epic roadmap](#epic-roadmap)
- [MCP tool taxonomy](#mcp-tool-taxonomy)
- [Surface matrix](#surface-matrix)
- [Per-epic feature tables](#per-epic-feature-tables)
  - [EP-004 — Impact & semantic queries](#ep-004--impact--semantic-queries)
  - [EP-005 — Deep analysis](#ep-005--deep-analysis)
  - [EP-007 — GitHub PR review vertical](#ep-007--github-pr-review-vertical)
  - [EP-012 — Agent memory & skills](#ep-012--agent-memory--skills)
  - [EP-013 — Pattern queries (AST + clones)](#ep-013--pattern-queries-ast--clones)
  - [EP-015 — Diagnostics & code actions](#ep-015--diagnostics--code-actions)
  - [EP-016 — Live IDE transport](#ep-016--live-ide-transport)
  - [EP-017 — Notebooks, watcher, interproc taint, communities](#ep-017--notebooks-watcher-interproc-taint-communities)
  - [EP-018 — PR-tool suite](#ep-018--pr-tool-suite)
- [PR-tool pipeline](#pr-tool-pipeline)
- [Live-IDE transport sequence](#live-ide-transport-sequence)
- [Watcher + conformance](#watcher--conformance)
- [Refactor saga (incl. inline & safe-delete)](#refactor-saga-incl-inline--safe-delete)
- [Diagnostic → code-action flow](#diagnostic--code-action-flow)
- [Counts at a glance](#counts-at-a-glance)

## Epic roadmap

```mermaid
flowchart LR
  EP001["EP-001<br/>Core code graph"]:::shipped --> EP002["EP-002<br/>Local-first trust"]
  EP001 --> EP003["EP-003<br/>Token-savings ledger"]
  EP001 --> EP004["EP-004<br/>Impact & semantic"]
  EP001 --> EP005["EP-005<br/>Deep analysis"]
  EP001 --> EP006["EP-006<br/>Graph-aware edits"]
  EP001 --> EP007["EP-007<br/>PR review vertical"]
  EP001 --> EP008["EP-008<br/>Multi-surface"]
  EP004 --> EP011["EP-011<br/>Compound + hierarchy queries"]
  EP004 --> EP012["EP-012<br/>Agent memory & skills"]
  EP001 --> EP013["EP-013<br/>AST pattern + clones"]
  EP006 --> EP015["EP-015<br/>Diagnostics & code actions"]
  EP008 --> EP016["EP-016<br/>Live IDE transport"]
  EP005 --> EP017["EP-017<br/>Notebooks/Watch/Taint/Comm"]
  EP007 --> EP018["EP-018<br/>PR-tool suite"]
  classDef shipped fill:#dff5e1,stroke:#1a7a3a,color:#0a3015
```

## MCP tool taxonomy

38 MCP tools, grouped by capability. The wire-visible identifier is the
canonical name; the dispatcher in `surfaces/mcp/mcp.go` routes each call to
the shared `surfaces/client.Client` interface — so CLI, MCP stdio, MCP
streamable-HTTP, and HTTP/SSE surfaces return byte-identical bytes by
construction.

```mermaid
flowchart TD
  ROOT["MCP tools (38)"]:::root
  ROOT --> Q["Structural query (10)"]
  ROOT --> P["Pattern query (2)"]
  ROOT --> S["Search / readout (3)"]
  ROOT --> A["Analyze (1 generic + 9 dedicated)"]
  ROOT --> E["Edit / refactor (3)"]
  ROOT --> C["Code actions (3)"]
  ROOT --> R["PR review (8)"]
  ROOT --> M["Memory & skills (3)"]
  Q --> Q1["callers · callees · references · definition · neighborhood"]
  Q --> Q2["implementers · implements · overrides · subtypes · supertypes"]
  P --> P1["search_ast · find_clones"]
  S --> S1["search · search_semantic · savings"]
  A --> A1["analyze"]
  A --> A2["analyze_taint · analyze_pdg · analyze_interproc · analyze_contracts · analyze_githistory"]
  A --> A3["analyze_pr_risk · analyze_pr_signals · analyze_pr_questions"]
  E --> E1["refactor_preview · refactor · undo"]
  C --> C1["diagnose · inline · safe_delete (CLI-only today)"]
  R --> R1["pr_comment · list_prs · triage_prs · conflicts_prs"]
  R --> R2["suggest_reviewers · compare_branches · critique_review"]
  M --> M1["memory · distill · skillgen"]
  classDef root fill:#fff7d0,stroke:#7a5a00,color:#3a2c00
```

## Surface matrix

```mermaid
flowchart LR
  U["User / Agent"]
  U --> S1["CLI<br/>(surfaces/cli)"]
  U --> S2["MCP stdio<br/>(surfaces/mcp mcp.go)"]
  U --> S3["MCP streamable-HTTP<br/>(surfaces/mcp http.go)"]
  U --> S4["HTTP + SSE<br/>(surfaces/http)"]
  U --> S5["TUI<br/>(surfaces/tui)"]
  U --> S6["Web (React + Sigma)<br/>(web/)"]
  U --> S7["VS Code ext<br/>(extensions/vscode)"]
  U --> S8["GitHub Action<br/>(extensions/github-action)"]
  S1 --> C["surfaces/client.Client"]
  S2 --> C
  S3 --> C
  S4 --> C
  S5 --> C
  S6 --> C
  S7 --> C
  S8 --> C
  C --> E["engine/*"]
  E --> CORE["core/*"]
  S3 -. per-class SSE .- E
  S4 -. "/events?analyzer=…" .- E
```

## Per-epic feature tables

> Notation: a tool is listed under the surface(s) that currently expose it.
> The MCP, HTTP, and CLI surfaces share the shared `surfaces/client.Client`
> interface, so the engine-side implementation is identical — only the
> transport differs.

### EP-001 — Core code graph & structural queries

- **Status:** ✅ shipped (foundation epic; everything builds on it)
- **Key packages:** `core/{model,parse,graphstore}`, `engine/{query,search,ingest,link}`
- **Capabilities:** 23 CGo-free parsers (22 shipped + 1 html ⏳ planned), FU-1 cross-file linker (shipped), FU-3 graceful-skip semantic search, 10 structural MCP tools.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `callers`, `callees`, `references`, `definition`, `neighborhood`, `implementers`, `implements`, `overrides`, `subtypes`, `supertypes` | `graphi parse`, `graphi query <op>`, `graphi search`, `graphi setup-embedder` | `GET /query/{op}`, `GET /search`, `GET /search/semantic`, `GET /contract`, `GET /healthz` | (foundational — consumed by all analyzers) |

### EP-004 — Impact & semantic queries

- **Status:** ✅ shipped
- **Key packages:** `engine/analysis/{impact,callchain,concept,metrics,batched}`
- **What it is:** structural reachability, call-path reconstruction, concept-to-graph resolution, and a batched composite.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `analyze` (generic dispatcher) | `graphi analyze <analyzer>` | `GET /analyze/{analyzer}` | `impact`, `call-chain`, `concept`, `metrics`, `batched` |

```bash
graphi analyze impact    -symbol p.MyFunc -direction forward
graphi analyze call-chain -symbol p.Caller -target p.Callee
graphi analyze concept   -symbol p.Root -concept "rate limiting"
```

### EP-005 — Deep analysis

- **Status:** ✅ shipped
- **Key packages:** `engine/analysis/{taint,pdg,interproc,contracts,githistory}`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `analyze_taint`, `analyze_pdg`, `analyze_interproc`, `analyze_contracts`, `analyze_githistory` | `graphi analyze <analyzer>` | `GET /analyze/{analyzer}` | `taint`, `pdg`, `interproc`, `contracts`, `git-history` |

### EP-006 — Graph-aware edits & refactoring

- **Status:** ✅ shipped
- **Key packages:** `engine/edit`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `refactor_preview`, `refactor`, `undo` | `graphi refactor-preview`, `graphi refactor`, `graphi undo` | (not exposed on HTTP — direct CLI / MCP) | (engine-side) |

### EP-007 — GitHub PR review vertical

- **Status:** ✅ shipped
- **Key packages:** `engine/review`, `extensions/github-action`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `analyze_pr_risk`, `analyze_pr_signals`, `analyze_pr_questions`, `pr_comment` | `graphi analyze <analyzer>`, `graphi pr-comment -diff <ref> [-gate] [-publish]` | (review vertical — host integration) | `pr-risk`, `pr-signals`, `pr-questions` |

### EP-011 — Compound + hierarchy queries

- **Status:** ✅ shipped
- **Key packages:** `engine/query/{dispatch,compound}`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `compound`, `implementers`, `implements`, `overrides`, `subtypes`, `supertypes` | `graphi query <op>`, `graphi compound` | `POST /compound` | (consumed by hierarchy analyses) |

### EP-012 — Agent memory & skills

- **Status:** ✅ shipped
- **Key packages:** `engine/{memory,distill,skillgen}`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `memory`, `distill`, `skillgen` | `graphi memory store\|recall\|forget …`, `graphi distill -session <id> …`, `graphi skillgen -name <n> -trigger <t> -description <d>` | `POST /memory`, `POST /distill`, `POST /skillgen` | (engine-side) |

### EP-013 — Pattern queries (AST + clones)

- **Status:** ✅ shipped
- **Key packages:** `engine/query/{searchast,findclones}`

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `search_ast`, `find_clones` | `graphi search-ast [-limit N] <json-pattern>`, `graphi find-clones [<json-config>]` | `POST /query-ast`, `POST /find-clones` | (engine-side) |

### EP-015 — Diagnostics & code actions

- **Status:** ✅ shipped (SW-091 … SW-094)
- **Key packages:** `engine/diagnostic/`, `engine/edit/inline.go`, `engine/edit/safe_delete.go`, `engine/edit/serialize.go`, `surfaces/client/` (marshaller extensions), `surfaces/ep015_parity_test.go`
- **What it is:** graph-derived diagnostics with severity and a suggested code-action, a reference-correct inline refactor with a fail-safe block list, and a reference-safety-gated safe-delete refactor. Surface exposure rides the shared marshaller and is byte-parity-tested.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| (diagnose / inline / safe_delete are CLI-only on the first cut; MCP dispatcher extension is a follow-up) | `graphi diagnose [<kind>...]`, `graphi inline [-dry-run] <target>`, `graphi safe-delete [-dry-run] <target>` | (CLI / parity-tested only) | (engine-side; `engine/diagnostic`) |

```bash
# What is the editor showing diagnostics for?
graphi diagnose

# Inline this helper into every caller (fail-safe on ambiguous references).
graphi inline p/Helper -dry-run
graphi inline p/Helper

# Delete this symbol — only if no inbound references remain.
graphi safe-delete p/LegacyThing -dry-run
graphi safe-delete p/LegacyThing
```

### EP-016 — Live IDE transport

- **Status:** ✅ shipped (SW-095 … SW-099)
- **Key packages:** `engine/overlay/`, `surfaces/daemon/{control,service}`, `surfaces/mcp/http.go`, `engine/observe/class.go`, `surfaces/guard/`, `internal/canary/gate.go`
- **What it is:**
  - The in-memory editor-overlay subsystem tracks unsaved buffers.
  - The daemon control plane adds RPCs (`DaemonStop`, `WatchStatus`, `IngestNotebook`, `AnalyzeCommunities`/`TaintQuery`/`WatcherStatus`).
  - The MCP streamable-HTTP transport keeps stdio envelope parity.
  - Per-class SSE subscriptions let an editor subscribe to only the event classes it cares about.
  - The **zero-egress enforcement guard** rejects any non-loopback dial at the surface boundary, and the central loopback/egress chokepoint is fail-closed.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| (streamable-HTTP transport exposes the same 38-tool set) | `graphi daemon start\|stop\|status` | `GET /events` (with optional `?analyzer=<name>` for one-shot analysis frames, SW-104) | (overlay + observe + guard) |

### EP-017 — Notebooks, watcher, interproc taint, communities

- **Status:** ✅ shipped (SW-100 … SW-104, capstone SW-104)
- **Key packages:** `engine/ingest/notebook.go`, `engine/watch/{watch,manager,pool,service,debounce,config}.go`, `engine/analysis/interproctaint/`, `core/community/louvain.go`, `engine/community/detector.go`, `engine/analysis/communities.go`, `engine/analysis/notebookingest.go`, `engine/analysis/watcherstatus.go` (the `taint-query` analyzer is registered in `engine/analysis/dispatch.go` and the implementation lives in `engine/analysis/interproctaint/solve.go`), `engine/conformance/`
- **What it is:** `.ipynb` cell-provenance ingestion (cell-as-symbol granularity), an `fsnotify` watcher with bounded worker-pool and deterministic canonical-ordered apply, an interprocedural taint fixpoint over Sharir–Pnueli procedure summaries, deterministic Louvain community detection behind a single grouping seam, single-dispatch surfacing through the analysis pipeline, and a full-vs-incremental byte-parity conformance gate.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| (surfaced through `analyze <name>`; no new singleton tools) | `graphi analyze <analyzer>`, `graphi daemon start` | `GET /analyze/{analyzer}`, `GET /events?analyzer=…` | `communities`, `notebook-ingest`, `taint-query`, `watcher-status` |

### EP-018 — PR-tool suite

- **Status:** ✅ shipped (SW-105 … SW-108, capstone SW-108)
- **Key packages:** `surfaces/forge/forge.go`, `engine/analysis/{triage,conflicts,suggest_reviewers,compare_branches,critique_review}.go`
- **What it is:** read-only forge PR enumeration (`list_prs`), single-pass graph-derived PR triage (`triage_prs`), inter-PR conflict detection (`conflicts_prs` — textual + graph-semantic + asymmetric contract-dependency), reviewer recommendation (`suggest_reviewers` — ownership/churn + affected-subgraph proximity), graph-level branch diff (`compare_branches` — keyed by canonical NodeId), and deterministic graph-evidence critique of an EXISTING PR review (`critique_review` — gap / over_flag / unsupported_claim, no LLM prose). The engine never resolves a git ref and never fetches a review — the only egress is at the surface boundary, and it's optional/inline.

| MCP tools | CLI subcommands | HTTP endpoints | Analyzers |
|---|---|---|---|
| `list_prs`, `triage_prs`, `conflicts_prs`, `suggest_reviewers`, `compare_branches`, `critique_review` | `graphi list-prs`, `graphi triage-prs`, `graphi conflicts-prs`, `graphi suggest-reviewers [-diff <ref>]`, `graphi compare-branches -base <ref> -head <ref>`, `graphi critique-review -diff <ref> [-pr N] [-review <json>\|-review-path <file>]` | `GET /prs`, `GET /prs/triage`, `GET /prs/conflicts`, `GET /prs/suggest-reviewers`, `GET /branches/compare`, `GET /reviews/critique` | `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review` |

```bash
# Read-only forge enumeration.
graphi list-prs

# Single-pass graph-derived ranking.
graphi triage-prs

# Inter-PR conflict detection.
graphi conflicts-prs

# Reviewer recommendation for a touched set.
graphi suggest-reviewers -diff origin/main..HEAD

# Graph-level branch diff.
graphi compare-branches -base origin/main -head feature/EP-018

# Critique an existing review (no LLM prose; graph evidence only).
graphi critique-review -diff origin/main..HEAD -pr 42 -review-path review.json
```

## PR-tool pipeline

The six EP-018 tools form a left-to-right pipeline: enumerate → rank →
conflict-check → reviewer-pick → branch-compare → review-critique. Every
step is zero-egress on the engine; the only egress is at the surface
boundary (forge PR list fetch, review fetch — both optional and inline).

```mermaid
flowchart LR
  F["Forge (GitHub, GitLab, …)"] -- "enumeration" --> LP["list_prs"]
  LP --> TP["triage_prs"]
  TP --> CP["conflicts_prs"]
  CP --> SR["suggest_reviewers"]
  CB["compare_branches"] --> SR
  F -- "existing review" --> CR["critique_review"]
  CR --> PC["pr_comment"]
  TP -- "ranking" --> PC
  subgraph engine["engine/* (zero egress)"]
    LP
    TP
    CP
    SR
    CB
    CR
  end
  PC --> F
```

## Live-IDE transport sequence

```mermaid
sequenceDiagram
  participant ED as Editor
  participant MC as MCP streamable-HTTP
  participant DA as graphi daemon
  participant EN as engine/observe
  participant CL as client.Client
  ED->>MC: tools/call <name> (POST /mcp)
  MC->>DA: dial control plane
  DA->>CL: client.<op>(args)
  CL->>EN: analyze / query / edit
  EN-->>CL: canonical result bytes
  CL-->>DA: surface-format envelope
  DA-->>MC: JSON-RPC 2.0 response
  MC-->>ED: result + optional follow-up SSE
  Note over ED,EN: editor subscribes to per-class SSE (e.g. ingest, analyze, overlay)
  ED->>MC: GET /events?class=ingest
  MC->>EN: observe.Subscribe(class=ingest)
  EN-->>MC: event stream (typed)
  MC-->>ED: SSE frame (typed, envelope-versioned)
```

## Watcher + conformance

EP-017 ships an `fsnotify`-backed watcher with a bounded worker-pool and
deterministic canonical-ordered apply. The conformance gate
(`engine/conformance`) proves that an incremental re-index produces the
**byte-identical** graph as a full re-index — same NodeId set, same edge
provenance ordering, same tier assignments.

```mermaid
flowchart TD
  FS["filesystem event"] --> FB["fsnotify backend"]
  FB --> DB["debounce window"]
  DB --> WP["bounded worker pool (FIFO)"]
  WP --> CO["canonical-ordered apply"]
  CO --> IG["engine/ingest.Incremental"]
  IG --> GS["graphstore"]
  IG --> CFG["conformance gate<br/>(full ≡ incremental)"]
  CFG --> ER["error report on drift<br/>(fail-closed)"]
  CO --> ST["watcher-status (per-root honest errors)"]
```

## Refactor saga (incl. inline & safe-delete)

The atomic saga coordinates writes across filesystem + graphstore +
ingest-meta in dependency order; failure in any domain triggers
compensating actions. EP-015 adds two new saga branches (`inline`,
`safe_delete`) on top of the SW-035 `refactor` (rename / extract / move /
signature) family.

```mermaid
sequenceDiagram
  participant CLI
  participant CL as client.Client
  participant AP as edit.Applier
  participant FS as filesystem
  participant GS as graphstore
  participant IG as ingest-meta
  CLI->>CL: RunInline / RunSafeDelete / RunRefactor
  CL->>AP: Apply(plan)
  AP->>FS: snapshot source files
  AP->>GS: plan preview / refs check
  alt reference-correct?
    AP->>FS: write new sources
    AP->>GS: mutate nodes/edges
    AP->>IG: IngestChanged (deterministic)
  else reference-unsafe
    AP-->>CLI: fail-safe block list triggered
  end
  IG-->>AP: ok | error
  AP-->>CL: ChangeRecorder token
  CL-->>CLI: undo token
```

## Diagnostic → code-action flow

EP-015's `diagnose` produces severity-tagged diagnostics, each with a
suggested code-action. The editor overlay tracks unsaved buffers so
diagnostics are anchored to the live editor state, not the on-disk file.

```mermaid
flowchart TD
  S["editor / CLI / MCP"] --> D["graphi diagnose"]
  D --> E1["engine/diagnostic"]
  E1 --> G["code graph"]
  E1 --> R["severity + code-action list"]
  R --> O["engine/overlay<br/>(in-memory editor state)"]
  O --> P["presented to user<br/>(deterministic order)"]
  P --> A["apply code-action"]
  A --> SAG["refactor saga<br/>(inline / safe_delete / rename / extract)"]
  SAG --> G
```

## Counts at a glance

| Dimension | Count | Source of truth |
|---|---|---|
| Parsers (CGo-free tier) | 23 | `core/parse` registry + `docs/coverage-matrix.md` § Parsers |
| Analyzers | 22 | `engine/analysis/dispatch.go` + `docs/coverage-matrix.md` § Analyzers |
| MCP tools | 38 | `surfaces/mcp/tools.go` (10 query ops + 28 singletons) |
| CLI subcommands | ~30 | `surfaces/cli/cli.go` + `cmd/graphi/main.go` |
| HTTP endpoints | 22 | `surfaces/http/server.go` (incl. `/prs/*`, `/branches/compare`, `/reviews/critique`) |
| Surfaces | 8 | `docs/coverage-matrix.md` § Surfaces |
| Feature Units | 5 | `docs/coverage-matrix.md` § Feature-Unit |
| New MCP tools (branch vs main) | +6 | `list_prs`, `triage_prs`, `conflicts_prs`, `suggest_reviewers`, `compare_branches`, `critique_review` |
| New analyzers (branch vs main) | +9 | `communities`, `notebook-ingest`, `taint-query`, `watcher-status`, `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review` |
| New CLI subcommands (branch vs main) | +9 | `diagnose`, `inline`, `safe-delete`, `list-prs`, `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review` |
| New HTTP endpoints (branch vs main) | +6 | `/prs`, `/prs/triage`, `/prs/conflicts`, `/prs/suggest-reviewers`, `/branches/compare`, `/reviews/critique` (+ `/events?analyzer=` query) |
| New engine subsystems (branch vs main) | +5 | `engine/overlay`, `engine/watch`, `engine/community`, `engine/interproctaint` (under `engine/analysis/`), `engine/conformance` |
| New surfaces (branch vs main) | +2 | `surfaces/forge`, `surfaces/guard` |

---

Last reconciled to: `docs/coverage-matrix.md` (CI-enforced) and `surfaces/mcp/tools.go::ToolNames()` (drift guard).
