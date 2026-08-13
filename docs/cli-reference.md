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
| `graphi sync` | **GA** (facade) | Bring the auto-managed graph up to date with the checked-out code — incremental, flagless, branch-switch aware. The everyday form of `index`. |
| `graphi rebuild` | **GA** (facade) | Re-index the repo from scratch (cold full pass). The everyday form of `index --full`. |
| `graphi status [--json]` | **GA** (facade) | Read-only freshness report: repo, branch, drift, last sync. Exit 0 current, 1 actionable, 2 error — `graphi status \|\| graphi sync` scripts cleanly. |
| `graphi trust-report [--json] [--details] [--limit n] [--target <symbol\|path\|package>] [--policy exploratory-v1\|review-v1\|automated-change-v1]` | labs | Trust surface over the persisted trust snapshot: snapshot state (`CURRENT`/`STALE`/`INCOMPLETE`/`UNAVAILABLE`), confidence tiers, coverage gaps, boundaries, the per-language capability matrix (`typed-confirmed`/`cross-file-heuristic`/`intra-file-only`/`parse-only`, derived from the live parser/resolver/type-checker registries), optional fail-closed policy verdict. `--json` is byte-identical to the `graph_health` MCP tool. Exit 0 PASS/current, 1 WARN, 2 everything else — FAIL, UNVERIFIED, a non-current snapshot without a policy, and usage/operational errors alike. Missing evidence never exits 0; only an error writes nothing to stdout. |
| `graphi snapshot [<name> \| -rm <name>]` | labs | List, freeze, or delete named graph states of this repo (input to `graphi compare`). |
| `graphi compare <base> <head>` | labs | Diff two named graph states (snapshot names, or `current` for the live graph); byte-identical to `compare-branches` on the resolved paths. |
| `graphi index [-root <repo>] [--full] [--semantic]` | **GA** | Advanced long form of `sync`/`rebuild` with explicit paths: ingest a repo into a durable store (warm-starts on an unchanged repo; `--full` forces a cold pass). Without `-root` it targets the cwd repo's auto-managed store like `sync`. |
| `graphi callers\|callees\|references\|definition\|neighborhood <symbol>` | **GA** | Short verbs for the structural GA operations. |
| `graphi impact <symbol>` | **GA** | Blast radius of a change (fixed dispatcher; the generic `analyze` selector is Labs). |
| `graphi explain-symbol <symbol>` | **GA** | Compact, cited symbol identity summary. |
| `graphi related-files <target>` | **GA** | Ranked, cited read-first file list. |
| `graphi change-risk <target>` | **GA** | Evidence-based local blast-radius estimate. |
| `graphi agent-brief` | **GA** | Bounded, cited task-start context packet. |
| `graphi symbol-context [-depth 1-3] [-max-items n] [-token-budget n] <symbol\|path\|node-id>` | labs | Unified one-call symbol view: definition + token-budgeted source snippet, type hierarchy, callers/callees/references, covering tests (bounded reverse walk), and a `change_risk`-consistent risk level. |
| `graphi task-context [-max-items n] [-token-budget n] <task text>` | labs | Free-text task → deterministically ranked, cited, token-budgeted context bundle with a recommended read order (integer weight model, hash-stamped in the summary). |
| `graphi repo-overview [-max-items n] [-communities]` | labs | One-call repository summary: totals, directory tree, language mix, entry points, central symbols, test/generated areas, external boundaries. `-communities` opts into the full-graph community pass. |
| `graphi test-impact [-depth 1-3] [-max-items n] (<target> \| -diff <file\|->)` | labs | Bucket the repository's tests for a change: `must_run` / `recommended` / `probably_unaffected` / `unknown`. Pipe `git diff <range>` into `-diff -` for range selection. |
| `graphi change-impact [-depth 1-3] [-max-items n] (<target> \| -diff <file\|->)` | labs | Change Risk 2.0: changed symbols, public-API subset, dependents, covering tests, co-change partners, explicit reasons, risk level. The stable `change-risk` quick check is unchanged. |
| `graphi search-hybrid [-max-items n] <query text>` | labs | Embedding-free hybrid search: lexical retrieval re-ranked by identifier segments, path relevance and bounded graph degree (per-signal breakdown in every reason). |
| `graphi hotspots [-max-commits n] [-max-items n]` | labs | Churn × dependency-centrality file ranking with bus-factor warnings, over the repo's bounded local git history. |
| `graphi architecture [-max-items n]` | labs | Automatic community/layer view: deterministic Louvain communities labeled by dominant package prefix, layered by dependency direction, with per-community depends-on/used-by neighbors. |
| `graphi architecture-violations [-max-items n]` | labs | Cycles, unexpected dependencies (edges against the dominant direction), high-coupling pairs, and god modules on the community graph; explicit cited "clean" item when nothing fires. |
| `graphi dead-code [-max-items n]` | labs | Scored dead-code candidates (zero live inbound references, integer signal model) with exclusions listed and explained: entry points, test fixtures, generated paths, exported API. |
| `graphi parse <file>` | labs | Parse a single file into the graph (default when no subcommand is given). |
| `graphi query <op> -symbol <id> [-depth N]` | **GA** | Structural query. `<op>` is one of `callers`, `callees`, `references`, `definition`, `neighborhood`. |
| `graphi query-strict <op> -symbol <id> [-min-tier confirmed\|derived\|heuristic] [-policy <name>]` | labs | Strict wrapper: the stable query runs unchanged, then result edges below `-min-tier` are excluded; the envelope carries the excluded count and filtered emptiness always carries an explicit limitation. Optional `-policy` preflight blocks fail-closed on FAIL/UNVERIFIED before the query runs. Byte-identical to the `strict_query` MCP tool (labs). |
| `graphi search [-limit N] [-semantic] <query>` | **GA** | Lexical / symbol search — the **GA** tier covers the lexical operation only. The optional `-semantic` flag is **labs**: it runs the embedding search (graceful-skip when no embedder is configured). |
| `graphi setup-embedder [<selector>]` | labs | Print how to opt in to the optional semantic search (offline; semantic search stays OFF until you set `GRAPHI_EMBEDDER`). |
| `graphi analyze <analyzer> -symbol <id> [options]` | labs | Run a semantic or deep analyzer (see below). |
| `graphi mcp [-root <repo>]` | **GA** | Run the MCP **stdio** server (the agent-first surface). GA as a transport for the 12 operations; `-labs` opens the Labs catalog and is not GA. `-root` (or the `GRAPHI_ROOT` env var; the flag wins) pins the repository root explicitly — for clients that launch the server outside the repository and supply no MCP roots. |
| `graphi daemon start\|stop\|status [-socket path] [-db path]` | labs | Manage the hot-index Unix-socket daemon. |
| `graphi http [-addr 127.0.0.1:8080] [-db path] [-root repo] [-meta dir]` | labs | Read-only HTTP REST + SSE surface (loopback-only). |
| `graphi setup [--client claude\|copilot\|cursor\|devin\|windsurf\|claude-desktop\|all] [--dry-run] [--binary path] [--config path]` | labs | Register graphi's MCP stdio server into local MCP clients' configs (idempotent, atomic, offline). Default `--client all` wires Claude Code plus every other detected local client. Purely cloud-sandboxed agents (the Copilot coding agent's remote runner) can't reach a local stdio server and stay out of scope; locally-installed agent CLIs that spawn stdio servers (Devin CLI) are supported. |
| `graphi setup --project [--root <repo>] [--attach] [--dry-run] [--binary path]` | labs | Per-repository variant: upsert graphi into the project-scoped `.mcp.json` at the repo root with the session root pinned (`mcp -root <abs root>`), so clients that launch the server outside the repo and supply no MCP roots still bind it. Root detected from cwd, or named with `--root`. `--attach` pins the auto-managed per-repo store instead (`mcp -db <store> -meta <sidecar>`, paths derived from the state layout — Attach mode: no auto-ingest, keep it fresh with `graphi sync`). The file carries absolute paths (machine-specific): gitignore it, or run the command once per clone. |
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
| `graphi compound <query>` | labs | Cypher-like compound query (SEED/HOP/WHERE/MAXDEPTH). |
| `graphi refactor-preview -kind rename\|signature_change -target-symbol <id> -old-name <n> -new-name <n>` | labs | Preview a graph-aware refactor (blast radius + planned edits) without mutating. |
| `graphi refactor …` | labs | Apply a refactor through the atomic edit saga (auditable change record + undo token). |
| `graphi undo -token <undo-token>` | labs | Reverse a previously applied edit by its undo token. |
| `graphi doctor` | labs | Read-only diagnostic checks: MCP registrations, DB, PATH health. |
| `graphi ui` | labs | Index the current repo and open the local web UI. |
| `graphi claude` | labs | Wire graphi into Claude Code (MCP) — the single-client shortcut for `setup`. |
| `graphi upgrade [-print]` | labs | Update to the latest release (user-initiated; never automatic). |
| `graphi help [<subcommand>]` | labs | Usage overview, or detailed help for one subcommand. |
| `graphi privacy-audit [--target ./...]` | labs | Print the local-first proof (real CGo scan + canary egress guard); non-zero on violation. |
| `graphi savings -ledger <path>` | labs | Print the session token-savings readout from a ledger a prior MCP/daemon session wrote. |
| `graphi version` | labs | Print the version / commit / build date stamped into the binary. |

## `graphi analyze`

```
graphi analyze [-db path] [-daemon socket] <analyzer> -symbol <id> \
  [-target <id>] [-concept <term>] [-direction forward|reverse] [-max-nodes N]
```

Available analyzers: `impact`, `call-chain`, `concept`, `metrics`, `batched`, `taint`, `pdg`, `interproc`, `contracts`, `git-history`, `pr-risk`, `pr-signals`, `pr-questions`, `communities`, `notebook-ingest`, `taint-query`, `watcher-status`, `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review`.

> `git-history`, `pr-signals` and `suggest-reviewers` read real local history
> via the `surfaces/gitlog` provider when run from a git repository (they were
> provider-less and always empty before the P2 git-intelligence work). In
> attach mode (`-db`) they degrade to empty results as before.

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
