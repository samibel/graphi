# graphi — How-To (Install & Use)

A practical, end-to-end guide: build graphi, index a repository, and use every
surface — CLI, HTTP/SSE API, web client, TUI, VS Code extension, and the MCP
server for Claude Code. Everything runs **locally**; no code leaves your machine.

> New to graphi? Read the [README](../readme.md) for the “what & why”. This guide
> is the “how”.

---

## 1. Prerequisites

| You want to use… | You need |
|---|---|
| The `graphi` binary (CLI, HTTP, MCP, daemon, TUI) | **Go 1.26+** — no C toolchain (the default build is CGo-free) |
| The web client (Sigma graph viz + wiki) | **Node.js 18+** and npm |
| The VS Code extension | Node.js 18+, npm, and **VS Code 1.80+** (plus `@vscode/vsce` for packaging) |
| The MCP integration | **Claude Code** installed |

Check Go:

```bash
go version   # must report go1.26 or newer
```

---

## 2. Install / Build

graphi is a single static binary built from `./cmd/graphi`.

### 2.1 Build the binary

```bash
git clone https://github.com/samibel/graphi.git
cd graphi

# CGo-free build of the CLI binary
CGO_ENABLED=0 go build -o graphi ./cmd/graphi

# (optional) put it on your PATH
sudo mv graphi /usr/local/bin/        # or: install -m755 graphi ~/.local/bin/

# verify
graphi version
```

> The default binary is intentionally lean and **excludes the interactive TUI**
> (its Bubble Tea dependency tree roughly doubles the binary). See
> [§6.4](#64-tui-terminal-ui) to build with the TUI included.

### 2.2 Build everything (sanity check)

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
```

---

## 3. Core concepts (30 seconds)

- **Code graph** — graphi parses a repository into nodes (functions, types,
  files) and edges (calls, references, definitions), with **deterministic ids**.
- **Provenance** — every edge carries a confidence tier (heuristic / derived /
  confirmed), a reason, and evidence, so you can trust each relationship.
- **One engine, many surfaces** — the CLI, daemon, MCP server, HTTP/SSE API, web
  client, TUI, and VS Code extension all answer from the *same* engine, so they
  never diverge.
- **Local-first** — zero outbound network, no telemetry, loopback-only servers.
  Provable with `graphi privacy-audit` ([§7](#7-prove-its-local-first-privacy-audit)).

---

## 4. Three ways to drive graphi (progressive disclosure)

graphi meets you at three levels of detail. Start at level 1; reach for the lower
levels only when you need the control they add.

### 1. graphi (zero-config)

The shortest path: run the bare command inside any repository.

```bash
cd your-repo && graphi
```

graphi detects the repo, indexes it incrementally into an auto-managed state
dir, serves the UI on a free loopback port, and opens your default browser at
the graph. It also prints a "Saved $X this session" token-savings readout.

On a headless box or over SSH — or with `--no-browser` / `GRAPHI_NO_BROWSER=1` —
graphi prints the local URL instead of trying to open a browser:

```bash
graphi --no-browser        # prints e.g. http://127.0.0.1:54321 instead of opening it
```

No flags, no database path, no port to pick — auto-discovery hides all of that.
On first run, graphi also offers (once, interactively) to connect Claude Code;
in a non-interactive shell it prints a hint instead.

### 2. Short verbs

Once indexed, ask structural and analysis questions with one-word verbs — no
`query`/`analyze` prefix needed:

```bash
graphi callers <symbol>        # who calls it
graphi callees <symbol>        # what it calls
graphi references <symbol>     # everything that references it
graphi definition <symbol>     # where it is defined
graphi neighborhood <symbol>   # local subgraph around it
graphi impact <symbol>         # what a change to it affects
graphi taint  <symbol>         # flow-sensitive taint from sources to sinks

graphi ui        # explicitly serve the graph + open the browser
graphi claude    # wire graphi into Claude Code (MCP setup)
```

These are aliases over the same engine as the full forms below.

### 3. Full flags (advanced)

The long forms expose every knob: explicit DB / meta sidecar, the daemon, and
the HTTP surface address. Power-user flags (`-db`, `-daemon`, `-root`, `-meta`,
`-addr`) all still work — auto-discovery just hides them by default.

```bash
# Structural query, long form — pin the store and/or talk to a daemon
graphi query callers -symbol pkg.MyFunc -db ~/.graphi/graph.db
graphi query neighborhood -symbol pkg.MyFunc -depth 2 -daemon /tmp/graphi.sock

# Analyzers, long form — explicit store + meta sidecar
graphi analyze impact -symbol pkg.MyFunc -direction reverse -db ~/.graphi/graph.db -meta ~/.graphi/meta

# Serve the read-only HTTP/SSE API on a pinned loopback port
graphi http -addr 127.0.0.1:8080 -root ./my-repo
# → "graphi http listening on 127.0.0.1:8080 (schema_version=1)"

# Run the hot-index daemon and query it over a Unix socket
graphi daemon start  -socket /tmp/graphi.sock -db ~/.graphi/graph.db
graphi query callers -symbol pkg.MyFunc -daemon /tmp/graphi.sock
```

Query the HTTP surface from another terminal:

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/contract        # served schema version + descriptors
curl -s 'http://127.0.0.1:8080/query/callers?symbol=pkg.MyFunc'
```

…or point the [web client](#63-web-client), [TUI](#64-tui-terminal-ui), or
[VS Code extension](#65-vs-code-extension) at the same `http://127.0.0.1:8080`.

> `-addr` defaults to `127.0.0.1:0` (a random free port, printed on startup).
> Pin a port with `-addr 127.0.0.1:8080` so the other surfaces can find it. The
> sections below ([§5](#5-indexing-a-repository)–[§6](#6-using-each-surface))
> document each full-flag surface in depth.

---

## 5. Indexing a repository

There are two ways to build the graph, depending on whether you want it to
persist.

### 5.1 In-memory (ephemeral, simplest)

`graphi http -root <repo>` ingests the whole repo on startup into an in-memory
graph for that session. Great for ad-hoc exploration; the index is gone when the
process exits.

```bash
graphi http -addr 127.0.0.1:8080 -root ./my-repo
```

### 5.2 Persistent (SQLite, reusable by CLI / MCP / daemon)

Pass `-db <path>` to persist the graph into a CGo-free SQLite store. Build it
once, then reuse it from any surface without re-ingesting.

```bash
mkdir -p ~/.graphi

# Build a persistent index once (ingests ./my-repo into the SQLite store)
graphi http -db ~/.graphi/graph.db -root ./my-repo -addr 127.0.0.1:8080
#   Ctrl-C once ingest is done if you only wanted to build the db.

# Reuse it — no -root needed, the graph is already in the db:
graphi query callers -symbol pkg.MyFunc -db ~/.graphi/graph.db
graphi mcp -db ~/.graphi/graph.db
```

> A single file: `graphi parse <file>` parses just one file and prints its
> metadata — it does **not** build a queryable graph. Use `-root` (whole repo)
> for that.

---

## 6. Using each surface

### 6.1 CLI

Most CLI commands accept `-db <path>` (open a SQLite store) **or**
`-daemon <socket>` (talk to a running daemon). With neither, the store is
in-memory (empty unless you ingested first).

```bash
# Structural queries — <op> ∈ callers | callees | references | definition | neighborhood
graphi query callers     -symbol pkg.MyFunc -db ~/.graphi/graph.db
graphi query neighborhood -symbol pkg.MyFunc -depth 2 -db ~/.graphi/graph.db

# Lexical / symbol search
graphi search -limit 20 "rate limiter" -db ~/.graphi/graph.db

# Semantic & deep analyzers (see list below)
graphi analyze impact     -symbol pkg.MyFunc -direction reverse -db ~/.graphi/graph.db
graphi analyze call-chain -symbol pkg.Caller -target pkg.Callee -db ~/.graphi/graph.db
graphi analyze concept    -symbol pkg.Root   -concept "rate limiting" -db ~/.graphi/graph.db

# Session token-savings readout (from a ledger a prior MCP/daemon session wrote), and version
graphi savings -ledger <path>
graphi version
```

**Analyzers** (`graphi analyze <analyzer> -symbol <id> [opts]`):
`impact`, `call-chain`, `concept`, `metrics`, `batched`, `taint`, `pdg`,
`interproc`, `contracts`, `git-history`.
Options: `-target <id>`, `-concept <term>`, `-direction forward|reverse`,
`-max-nodes N`.

### 6.2 HTTP / SSE API

Start it with `graphi http` ([§4](#4-quick-start--2-minutes)). It is **read-only**
and **loopback-only** (refuses any non-loopback bind). All responses carry a
`schema_version` envelope (drift-gated; see `/contract`).

| Method & path | Purpose |
|---|---|
| `GET /healthz` | Liveness check |
| `GET /contract` | Served schema version + available resource/stream descriptors |
| `GET /query/{op}?symbol=<id>&depth=N` | Structural query (`op` = callers/callees/references/definition/neighborhood) |
| `GET /search?q=<term>&limit=N` | Lexical / symbol search |
| `GET /analyze/{analyzer}?symbol=<id>&…` | Run an analyzer |
| `GET /events` | Server-Sent Events stream (ingest/graph-change events) |
| `GET /wiki`, `GET /wiki/c/{id}` | Auto-generated wiki (index + per-community pages, Markdown) |

```bash
curl -s 'http://127.0.0.1:8080/query/neighborhood?symbol=pkg.MyFunc&depth=2'
curl -N  http://127.0.0.1:8080/events     # live SSE stream
```

> Schema negotiation: send `X-Graphi-Schema-Version: 1`. A mismatch returns
> `412 Precondition Failed` instead of silently mis-decoding.

### 6.3 Web client

A TypeScript/React single-page app (Sigma.js graph viz with blast-radius &
citation highlights, plus the browsable wiki). It is a pure HTTP/SSE client of
the backend above.

```bash
# 1) Start the backend
graphi http -addr 127.0.0.1:8080 -root ./my-repo

# 2) In another terminal, run the web client
cd web
npm install
npm run dev          # Vite dev server (proxies /query,/search,/contract,/events,/wiki to :8080)
# open the URL Vite prints (usually http://localhost:5173)
```

Production build:

```bash
cd web
npm run build        # tsc --noEmit && vite build  →  web/dist/
npm run preview      # serve the built dist locally
```

Interactions: pan/zoom the graph, click a symbol to highlight its blast-radius
(dependents) and citation/evidence edges, and browse the generated wiki under
`/wiki`.

### 6.4 TUI (terminal UI)

The interactive terminal surface is **opt-in behind the `tui` build tag** (so the
default binary stays lean). It consumes the HTTP/SSE API — start `graphi http`
first.

```bash
# Build a binary that includes the TUI
CGO_ENABLED=0 go build -tags tui -o graphi-tui ./cmd/graphi

# Start the backend (separate terminal)
graphi http -addr 127.0.0.1:8080 -root ./my-repo

# Launch the TUI against it (loopback-only; fails closed on non-loopback)
graphi-tui tui -addr http://127.0.0.1:8080
```

Keyboard-driven panes: a navigator, a content pane, and a persistent provenance
pane that always shows where the displayed answer came from. It is strictly
read-only.

> If you run `graphi tui` on the **default** (non-TUI) binary, it prints a hint
> telling you to rebuild with `-tags tui`.

### 6.5 VS Code extension

Read-only code intelligence + an interactive graph webview, inside the editor.
It talks to the same loopback HTTP/SSE backend.

**Build & install the VSIX:**

```bash
cd extensions/vscode
npm install
npm run build
npm run package                       # produces graphi-<version>.vsix
code --install-extension graphi-*.vsix
```

**Configure & use:**

1. Start the backend: `graphi http -addr 127.0.0.1:8080 -root ./my-repo`.
2. In VS Code settings, set **`graphi.daemonUrl`** (default `http://127.0.0.1:8080`;
   must be loopback). Optionally set a daemon auth token via the command
   *“graphi: Set daemon auth token”* (stored in VS Code SecretStorage, never in
   settings/URLs).
3. Use the Command Palette (`graphi:` prefix):
   - **Show blast-radius** — impact of the symbol under the cursor
   - **Search symbols**
   - **Show graph (webview)** — interactive Sigma graph; selecting a node reveals
     the source location; live-updates over SSE
   - **Retry daemon connection**

The status bar shows connected / disconnected; the extension reconnects with
bounded backoff and never blocks the editor UI thread.

### 6.6 MCP server for local AI agents

graphi exposes its read-only graph to AI agents over **MCP (stdio)**.

**One-command onboarding** (idempotent, atomic, offline):

```bash
graphi setup
# → registers graphi's stdio MCP server into every detected LOCAL MCP client and
#   prints each config path it wrote. Then restart/reload that client.
```

By default (`--client all`), `setup` wires **Claude Code** (created if absent)
plus every other local client that looks installed:

| Client | Config it writes | Servers key |
|---|---|---|
| Claude Code | `~/.claude.json` (or `$CLAUDE_CONFIG_PATH`) | `mcpServers` |
| GitHub Copilot (VS Code) | `<user-config>/Code/User/mcp.json` | `servers` |
| Cursor | `~/.cursor/mcp.json` | `mcpServers` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json` | `mcpServers` |
| Claude Desktop | `<user-config>/Claude/claude_desktop_config.json` | `mcpServers` |

> **Cloud agents are out of scope.** Devin and the GitHub Copilot *coding agent*
> run in a remote sandbox and cannot reach a local stdio graphi — reaching it
> would also break the zero-egress contract. They need graphi installed
> *inside* their environment via a separate, repo-side setup step, not this
> command.

Useful flags:

```bash
graphi setup --dry-run               # show the planned changes without writing
graphi setup --client cursor         # wire just one client
graphi setup --binary /path/graphi   # register a specific binary (default: this one)
graphi setup --config /path/config   # target a specific config file (single client)
```

`setup` is safe to re-run. Per client it converges to exactly one canonical
entry, preserves unrelated MCP servers, and makes a timestamped backup with an
atomic write plus fail-closed rollback (a failed run leaves your config
byte-identical). The first bare `graphi` run also offers — once, only on a
TTY, and never without your consent — to connect every detected client.

To run the MCP server directly (e.g. for a non-Claude MCP client), point the
client at:

```bash
graphi mcp -db ~/.graphi/graph.db     # or -daemon <socket>
```

### 6.7 Daemon (hot index)

Keep the graph hot in a background process and query it over a local Unix socket
for instant responses.

```bash
graphi daemon start  -socket /tmp/graphi.sock -db ~/.graphi/graph.db
graphi daemon status -socket /tmp/graphi.sock
graphi query callers -symbol pkg.MyFunc -daemon /tmp/graphi.sock
# Stop: terminate the daemon process directly (Ctrl-C / kill).
```

> The daemon serves whatever is in its store; build the `-db` first
> ([§5.2](#52-persistent-sqlite-reusable-by-cli--mcp--daemon)).

---

## 7. Prove it’s local-first (`privacy-audit`)

```bash
graphi privacy-audit
```

It runs a real CGo-free scan and a canary egress guard and prints a verdict:

- **CONFIRMED** (exit 0) — zero outbound network observed.
- **VIOLATED** (non-zero) — egress detected.
- **UNVERIFIED** (non-zero) — the network layer couldn’t be observed (e.g. no
  Linux network-namespace capability on this host). This is **not** a pass — it
  fails closed so a false green is impossible. The authoritative green proof runs
  under a Linux deny-egress sandbox in CI.

---

## 8. Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `refusing non-loopback bind` | `-addr` host must be `127.0.0.1`, `localhost`, or `::1`. The HTTP surface is loopback-only by design. |
| Queries return nothing | The store is empty — ingest a repo first (`graphi http -root <repo>` or build a `-db`, see [§5](#5-indexing-a-repository)). |
| `412` from the HTTP API | Client/server `schema_version` mismatch — rebuild the client against the current `/contract`. |
| `graphi tui` prints “compiled without the TUI surface” | Rebuild with `-tags tui` ([§6.4](#64-tui-terminal-ui)). |
| Web client shows a version-mismatch banner | The backend’s schema version differs from the one the client was built against — rebuild the web client (`npm run gen:types && npm run build`). |
| `privacy-audit` reports UNVERIFIED locally | Expected off-Linux / unprivileged — it fails closed. The CI Linux job is the live proof. |
| Claude Code doesn’t see graphi’s tools | Re-run `graphi setup`, then fully restart Claude Code. |

---

## 9. Subcommand reference

```text
graphi parse <file>                                  Parse one file, print metadata
graphi query <op> -symbol <id> [-depth N]            callers|callees|references|definition|neighborhood|
                                                     implementers|implements|overrides|subtypes|supertypes
graphi search [-limit N] [-semantic] <query>          Lexical / symbol search
graphi search-ast [-limit N] <json-pattern>          AST pattern query (SW-082)
graphi find-clones [<json-config>]                   Clone detection (SW-083)
graphi diagnose [-db p] [<kind>...]                  Graph-derived diagnostics + code-actions (EP-015)
graphi inline -root <repo> [-dry-run] <target>       Inline refactor over the edit saga (EP-015)
graphi safe-delete -root <repo> [-dry-run] <target>  Reference-safety-gated delete; removes the declaration line only (EP-015)
graphi refactor-preview -kind <k> -target <id>        Preview refactor blast radius
graphi refactor -kind <k> -target <id>                Apply refactor (atomic saga)
graphi undo -token <tok> [-actor who]                Reverse an applied edit
graphi analyze <analyzer> -symbol <id> [opts]        impact|call-chain|concept|metrics|batched|
                                                     taint|pdg|interproc|contracts|git-history|
                                                     pr-risk|pr-signals|pr-questions|
                                                     communities|notebook-ingest|taint-query|watcher-status|
                                                     triage-prs|conflicts-prs|suggest-reviewers|
                                                     compare-branches|critique-review
graphi list-prs                                      Forge enumeration of open PRs (EP-018)
graphi triage-prs                                    Graph-derived PR triage ranking (EP-018)
graphi conflicts-prs                                 Inter-PR conflict detection (EP-018)
graphi suggest-reviewers [-diff <ref>]               Reviewer recommendation (EP-018)
graphi compare-branches -base <db> -head <db>        Graph-level diff of two graphi SQLite snapshots (EP-018)
graphi critique-review -diff <ref> [-pr N] [-review <json>]   Critique of an existing PR review (EP-018)
graphi pr-comment -diff <ref> [-pr N] [-gate] [-publish]      Sticky PR comment + merge gate
graphi memory store|recall|forget ...                Agent memory operations (EP-012)
graphi distill -session <id> -decisions "..." -risks "..." -questions "..." -files "..."
                                                     Session distillation (EP-012)
graphi skillgen -name <n> -trigger <t> -description <d>      Skill generation (EP-012)
graphi setup-embedder [<selector>]                   Print how to opt in to semantic search
graphi http   [-addr 127.0.0.1:8080] [-db p] [-root r] [-meta d]   Read-only HTTP/SSE (loopback)
graphi tui    [-addr http://127.0.0.1:8080]          Interactive TUI (build with -tags tui)
graphi mcp    [-db p] [-daemon sock]                 MCP stdio server (agent surface)
graphi daemon start|stop|status [-socket p] [-db p]  Hot-index Unix-socket daemon
graphi setup  [--client id|all] [--dry-run] [--binary p] [--config p]  Register MCP server into local clients
graphi privacy-audit                                 Local-first proof (CONFIRMED/VIOLATED/UNVERIFIED)
graphi savings -ledger <path>                        Token-savings readout from a session ledger
graphi version                                       Version / commit / build date

Common flags: -db <sqlite path>   -daemon <unix socket>   (most CLI subcommands)
```

For the complete feature inventory grouped by epic (with 10 Mermaid diagrams), see **[`docs/FEATURES.md`](FEATURES.md)**.

---

## License

Apache-2.0.
