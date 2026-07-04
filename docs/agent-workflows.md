# Agent Workflows

`graphi` is the local code memory an AI agent asks before it opens many
files. This page documents the recommended call order, per-client examples,
and how to read the shared response contract.

## Recommended call order

At the start of a task, call the four agent-first tools in this order:

1. **`agent_brief`** — get the bounded, cited project context packet:
   start-here files, key symbols, known facts from local memory, risks.
2. **`related_files`** — turn the task description (or a symbol/path) into a
   ranked read-first file list with reasons and evidence.
3. **`explain_symbol`** — before reading a file top to bottom, ask for the
   symbol's definition site, callers, callees, and references.
4. **`change_risk`** — before editing, ask for the evidence-based local
   blast radius (`low` / `medium` / `high` / `unknown`).

Every tool is read-only and local-only: no account, no cloud service, no
network egress.

## The shared response contract

All four tools return one envelope:

```json
{
  "outcome": "found",
  "summary": "short human-readable answer",
  "items": [{"ref_id": "...", "rank": 3, "reason": "...", "evidence_ref_ids": ["e1"]}],
  "evidence": [{"ref_id": "e1", "path": "pkg/file.go", "line": 12, "role": "caller"}],
  "confidence": {"distribution": {"confirmed": 0.7, "heuristic": 0.3}, "top": "confirmed", "method": "edge_tiers"},
  "limits": {"truncated": false, "cap_applied": 20, "total_available": 4, "dropped": 0}
}
```

Outcomes to branch on:

- `found` — use the items.
- `partial` — usable, but the item cap truncated the list (`limits` says how much).
- `ambiguous` — the reference matched several symbols; the items are
  candidates. Retry with a node id or full path from a candidate.
- `empty` — nothing matched; the summary carries next-step hints.
- `unavailable` — no indexed database is wired; run `graphi index` first.

Confidence is a product semantic, not an internal detail: `confirmed` and
`derived` results are safe to act on; `heuristic` results should be verified
(the summary and item reasons mark them).

## Claude Code

Register the MCP server once:

```bash
graphi setup            # or: claude mcp add graphi -- graphi mcp -db /path/to/graph.db
graphi doctor           # verify registration, DB, and PATH health
```

Then, in a session, the agent calls the tools directly:

```
agent_brief    {"symbol": "add auth middleware"}
related_files  {"target": "add auth middleware"}
explain_symbol {"symbol": "auth.Middleware"}
change_risk    {"target": "auth.Middleware"}
```

## Codex-style CLI agents

The same tools are plain CLI verbs with byte-identical output (parity is
tested), so shell-oriented agents can pipe them:

```bash
graphi agent-brief   -db graph.db -topic "add auth middleware"
graphi related-files -db graph.db -direction dependents auth.Middleware
graphi explain-symbol -db graph.db auth.Middleware
git diff | graphi change-risk -db graph.db -diff -
```

## Generic MCP clients

Any MCP client can drive the stdio or streamable-HTTP transport:

```bash
graphi mcp -db graph.db          # stdio JSON-RPC
graphi http -db graph.db         # loopback HTTP; tools at /analyze/{tool}
```

Over HTTP the tools are `GET /analyze/agent_brief?topic=…`,
`/analyze/related_files?target=…&direction=…`,
`/analyze/explain_symbol?symbol=…`, `/analyze/change_risk?target=…` —
all advertised in `GET /contract`.

## Memory: teach the brief project facts

`agent_brief` reads the local memory store. Store durable facts with a kind
from the closed taxonomy (`architecture`, `command`, `convention`,
`decision`, `risk`, `dependency`, `workflow`):

```bash
graphi memory store -scope repo -notebook conventions \
  -payload "tests run via make test" -kind command -source user -confidence confirmed
graphi memory list
graphi memory forget -id <id>     # memory is local, inspectable, deletable
```

Secret-looking payloads are marked `secret_suspected` and withheld from
briefs.

## Diagnostics for agents

`graphi diagnose` defaults to high-confidence, low-noise findings with
evidence. Use `--explain-suppressed` to audit what was withheld and why, and
`--all` only during deep audits:

```bash
graphi diagnose -db graph.db                      # default: high-signal
graphi diagnose -db graph.db -explain-suppressed  # + suppressed, tagged
graphi diagnose -db graph.db -all                 # everything
```

## Troubleshooting

- `graphi doctor` explains binary, PATH, MCP registration, DB, and
  privacy-audit health in one read-only pass (`--json` for machines).
- `unavailable` outcomes mean no graph services are wired: pass `-db` or run
  `graphi index` first.
- Index profiles trade speed for depth: `graphi index -profile fast|balanced|deep`.
