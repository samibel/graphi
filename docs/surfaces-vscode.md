# VS Code Extension (`extensions/vscode/`)

> Read-only code intelligence and visualization inside the IDE, for
> developers who want graph-backed context without leaving their editor.
> Local-first: talks only to the local graphi daemon over loopback.

## Before / after

| | Before | After |
|---|---|---|
| **IDE integration** | none | blast-radius, search, graph webview in VS Code |
| **Backend** | n/a | HTTP contract only (loopback daemon) |
| **Citations** | n/a | `file:line` → click-navigable in the editor |
| **Network** | n/a | loopback-only (asserted at construction); zero outbound |

## Why

The extension surfaces graphi's code intelligence directly inside the editor:
put the cursor on a symbol to see its blast-radius with clickable citations,
search symbols and jump to them, or visualize the neighborhood in a webview —
all without leaving the IDE, all read-only, all local.

## Architecture
- **Standalone TS package** under `extensions/vscode/`; depends only on the `vscode` API and the HTTP contract (re-declared in `src/contract.ts`). **No Engine/core internals.**
- **Read-only by construction:** `src/graphiClient.ts` exposes only `getNeighborhood/getImpact/search/health` — no write/refactor/undo verbs exist, so the extension cannot mutate the graph or workspace.
- **Loopback-enforced:** `assertLoopback()` rejects non-loopback daemon URLs at construction (mirrors the HTTP surface's `http.AssertLoopback`); zero outbound.
- **Robust:** daemon-unreachable → status-bar "offline" + actionable Retry; commands never throw out (no IDE crash).

## Commands
| Command | Action |
|---|---|
| `graphi: Show blast-radius` | cursor symbol → `/analyze/impact` → QuickPick of impacted `file:line` citations → click navigates |
| `graphi: Search symbols` | input → `/search` → QuickPick with `file:line` → navigate |
| `graphi: Show graph (webview)` | cursor symbol → `/query/neighborhood` → SVG graph webview; node click reveals citation |
| `graphi: Retry daemon connection` | re-checks the daemon URL |

```mermaid
flowchart LR
    E[VS Code extension] -->|GET /query /analyze /search| D[graphi http daemon 127.0.0.1]
    D --> ENG[graphi engine]
    E --> QP[QuickPick file:line citations]
    QP --> ED[reveal in editor]
    E --> WV[graph webview]
    WV -->|node click| ED
```

## Local-first / zero-outbound
- Daemon URL MUST be loopback (`127.0.0.1`/`localhost`/`::1`); asserted at construction, non-loopback refused.
- No telemetry, no remote scripts in the webview (local resources only).

## Build / test
```bash
cd extensions/vscode && npm install
npm run compile     # tsc -> out/ (extension compiles against vscode API + contract)
npm test            # vitest: loopback-enforcement + citation mapping
# run the daemon: graphi http -addr 127.0.0.1:8080 ...
```

## Tests
- **Build proof:** `npx tsc -p ./` compiles to `out/` against the `vscode` API and the typed HTTP contract.
- **Unit (vitest):** `graphiClient.test.ts` (loopback acceptance/refusal + construction fail-fast), `citations.test.ts` (impact → `file:line` citations; missing-node tolerated). 7/7 green.
