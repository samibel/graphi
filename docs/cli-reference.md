# CLI subcommand reference

The single `graphi` binary dispatches the subcommands below. Most accept
`-db <path>` to open a SQLite store, or `-daemon <socket>` to talk to a running
daemon. For a guided tour, start with the [How-To](HOWTO.md); for the complete
feature inventory across all surfaces, see [FEATURES.md](FEATURES.md).

The **Tier** column follows [`stability-tiers.md`](stability-tiers.md):
**GA** = one of the 12 frozen operations (or the GA transport that serves them), on
Go; `labs` = in-tree, not part of the GA promise. On a non-Go language a GA
operation is **Preview**, not GA. `graphi help` marks the same split at runtime.

| Subcommand | Tier | Purpose |
|---|---|---|
| `graphi index -root <repo> [--full] [--semantic]` | **GA** | Ingest a repo into a durable store (warm-starts on an unchanged repo; `--full` forces a cold pass). |
| `graphi callers\|callees\|references\|definition\|neighborhood <symbol>` | **GA** | Short verbs for the structural GA operations. |
| `graphi impact <symbol>` | **GA** | Blast radius of a change (fixed dispatcher; the generic `analyze` selector is Labs). |
| `graphi explain-symbol <symbol>` | **GA** | Compact, cited symbol identity summary. |
| `graphi related-files <target>` | **GA** | Ranked, cited read-first file list. |
| `graphi change-risk <target>` | **GA** | Evidence-based local blast-radius estimate. |
| `graphi agent-brief` | **GA** | Bounded, cited task-start context packet. |
| `graphi parse <file>` | labs | Parse a single file into the graph (default when no subcommand is given). |
| `graphi query <op> -symbol <id> [-depth N]` | **GA** | Structural query. `<op>` is one of `callers`, `callees`, `references`, `definition`, `neighborhood`. |
| `graphi search [-limit N] [-semantic] <query>` | **GA** | Lexical / symbol search — the **GA** tier covers the lexical operation only. The optional `-semantic` flag is **labs**: it runs the embedding search (graceful-skip when no embedder is configured). |
| `graphi setup-embedder [<selector>]` | labs | Print how to opt in to the optional semantic search (offline; semantic search stays OFF until you set `GRAPHI_EMBEDDER`). |
| `graphi analyze <analyzer> -symbol <id> [options]` | labs | Run a semantic or deep analyzer (see below). |
| `graphi mcp` | **GA** | Run the MCP **stdio** server (the agent-first surface). GA as a transport for the 12 operations; `-labs` opens the Labs catalog and is not GA. |
| `graphi daemon start\|stop\|status [-socket path] [-db path]` | labs | Manage the hot-index Unix-socket daemon. |
| `graphi http [-addr 127.0.0.1:8080] [-db path] [-root repo] [-meta dir]` | labs | Read-only HTTP REST + SSE surface (loopback-only). |
| `graphi tui [-db path] [-daemon socket]` | labs | Interactive terminal surface (select / neighbors / blast / search). |
| `graphi setup [--client claude\|copilot\|cursor\|windsurf\|claude-desktop\|all] [--dry-run] [--binary path] [--config path]` | labs | Register graphi's MCP stdio server into local MCP clients' configs (idempotent, atomic, offline). Default `--client all` wires Claude Code plus every other detected local client. Cloud agents (Devin, the Copilot coding agent) run remotely and can't reach a local stdio server, so they are out of scope. |
| `graphi search-ast [-limit N] <json-pattern>` | labs | Structural AST pattern query. |
| `graphi find-clones [<json-config>]` | labs | Clone detection. |
| `graphi diagnose [-db path] [<kind>...]` | labs | Graph-derived diagnostics + suggested code-actions. |
| `graphi inline -root <repo> [-db path] [-meta dir] [-dry-run] <target>` | labs | Inline refactor over the edit saga (single-initializer targets; fail-safe block list). |
| `graphi safe-delete -root <repo> [-db path] [-meta dir] [-dry-run] <target>` | labs | Reference-safety-gated delete. Current limitation: removes the symbol's declaration line only — review the diff for multi-line bodies. |
| `graphi list-prs` | labs | Read-only forge enumeration of open PRs. |
| `graphi triage-prs` | labs | Graph-derived multi-PR triage ranking. |
| `graphi conflicts-prs` | labs | Inter-PR conflict detection. |
| `graphi suggest-reviewers [-diff <ref>]` | labs | Ranked candidate-reviewer recommender. |
| `graphi compare-branches -base <db-path> -head <db-path>` | labs | Graph-level diff of two graphi SQLite snapshots (paths to `graphi index` outputs — it never resolves a git ref). |
| `graphi critique-review -diff <ref> [-pr N] [-review <json>\|-review-path <file>]` | labs | Deterministic graph-evidence critique of an existing PR review. |
| `graphi pr-comment -diff <ref> [-pr N] [-gate] [-publish]` | labs | Sticky PR comment + risk-threshold merge gate. |
| `graphi memory store\|recall\|forget ...` | labs | Agent memory operations. |
| `graphi distill -session <id> -decisions "..." -risks "..." -questions "..." -files "..."` | labs | Session distillation. |
| `graphi skillgen -name <n> -trigger <t> -description <d>` | labs | Deterministic skill generation. |
| `graphi privacy-audit [--target ./...]` | labs | Print the local-first proof (real CGo scan + canary egress guard); non-zero on violation. |
| `graphi savings -ledger <path>` | labs | Print the session token-savings readout from a ledger a prior MCP/daemon session wrote. |
| `graphi version` | labs | Print the version / commit / build date stamped into the binary. |

## `graphi analyze`

```
graphi analyze [-db path] [-daemon socket] <analyzer> -symbol <id> \
  [-target <id>] [-concept <term>] [-direction forward|reverse] [-max-nodes N]
```

Available analyzers: `impact`, `call-chain`, `concept`, `metrics`, `batched`, `taint`, `pdg`, `interproc`, `contracts`, `git-history`, `pr-risk`, `pr-signals`, `pr-questions`, `communities`, `notebook-ingest`, `taint-query`, `watcher-status`, `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review`.

`impact` is the only GA operation here; the generic `analyze` dispatcher and
every other analyzer are Labs.

```bash
# Reverse impact: what depends on this symbol?
graphi analyze impact -symbol p.MyFunc -direction reverse

# Call path between two symbols
graphi analyze call-chain -symbol p.Caller -target p.Callee

# Resolve a concept to graph locations
graphi analyze concept -symbol p.Root -concept "rate limiting"
```
