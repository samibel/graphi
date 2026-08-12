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

## One-call context (labs): `repo_overview`, `task_context`, `symbol_context`

With the Labs catalog (`graphi mcp -labs`), the P0 agent-intelligence tools
collapse the call sequence above into single calls — fewer round-trips, fewer
tokens, the same cited contract envelope:

1. **`repo_overview`** — first call in an unfamiliar repository: totals with
   edge-confidence tiers, directory tree, language mix, entry-point
   candidates, the highest-centrality symbols, test and generated areas,
   external boundaries, and concrete suggested next calls. The default call
   reads only compact aggregates; pass `communities: true` for the opt-in
   full-graph community pass.
2. **`task_context`** — phrase the task in a few words and get a ranked,
   token-budgeted bundle: primary seeds, related symbols, callers/callees,
   nearby tests and configs, a related-file roll-up, a risk level, a
   recommended read order, and source snippets under `token_budget`. The
   ranking is a fixed integer weight model; its hash is stamped in every
   summary, so identical inputs rank identically everywhere.
3. **`symbol_context`** — when the symbol is known, one call replaces
   `explain_symbol` + `callers` + `callees` + `references` + `change_risk`:
   definition with an optional token-budgeted snippet, hierarchy relations,
   all three relation lists, the tests that exercise the symbol (bounded
   reverse walk, `depth` 1–3), and a `change_risk`-consistent risk level.

After editing, two further labs tools close the loop:

4. **`test_impact`** — pass the diff (`git diff <range> | graphi test-impact
   -diff -`) and get the tests bucketed: `must_run` (direct-call proof),
   `recommended` (transitive/naming/proximity), `probably_unaffected` (the
   rest, counted in full), `unknown` (paths the graph doesn't know — never
   guessed). Run seven tests instead of the whole suite, with evidence.
5. **`change_impact`** — the Change Risk 2.0 assessment for the same diff:
   changed symbols, public-API subset, dependents (direct + bounded
   transitive), covering tests, co-change partners from bounded git history
   ("`B` usually changes with `A` — not in this change"), config changes,
   explicit reasons, and a risk level with a tier-derived confidence
   distribution. The stable `change_risk` quick check is unchanged; this is
   its richer labs sibling.
6. **`hotspots`** — where does the repository hurt? Files ranked by churn ×
   dependency centrality with per-row breakdowns and single-author
   bus-factor warnings. Use it to plan refactors and prioritize review
   attention before a task even starts.

These are labs operations: their shapes may still change, and they are only
advertised under `graphi mcp -labs` (HTTP: 403 without `GRAPHI_LABS=1`). The
four stable tools above remain the frozen, GA-supported path.

## Trust preflight (`graph_health`, labs)

Before a risky task, ask how far graph evidence may be trusted for the planned
action — a technically successful query result is not automatically complete
or safe to act on. With the Labs catalog (`graphi mcp -labs`), call
`graph_health` first:

```text
1. graph_health(policy="review-v1", target="engine/query.Service")
2. Check snapshot_state == "CURRENT"   (STALE/INCOMPLETE/UNAVAILABLE: evidence
   is not usable for the current source — never treat it as healthy)
3. Read policy.verdict and the findings
4. FAIL or UNVERIFIED → do not make a definitive graph-only claim; inspect the
   source or run additional tools instead
5. Otherwise run the queries, and cite the trust limitations in your answer
6. For an unattended automated change: require an automated-change-v1 PASS —
   anything else means a human decides
```

### Acting only on strong evidence (`strict_query`, labs)

`graph_health` grades the repository; `strict_query` grades an individual
answer. It runs one of the Stable structural queries unchanged, then withholds
result edges below a minimum confidence tier and reports how many it withheld:

```text
strict_query(operation="callers", symbol="<id>", minimum_tier="confirmed")
```

**Read the envelope, not just the list.** `filter.excluded_edges > 0` with an
empty result means the answer was *filtered*, not that no such relationships
exist — `limitations` says so explicitly. Treating that as "no callers" is a
false negative, which on this surface is worse than no answer at all. Pass
`policy` to run the same fail-closed preflight first; a non-PASS/WARN verdict
returns an error and the query never runs.

The three policies are versioned static rule sets, fail-closed by
construction: missing evidence never yields PASS. The document is
byte-identical to `graphi trust-report --json`, so CLI-side scripting and
MCP-side agents read the same facts. Example of a safe agent claim:

> Graphi found 14 local dependents; the graph is current. Twelve relevant
> edges are confirmed or derived, two are heuristic. The path reaches one
> external dependency whose internals are outside graphi's coverage — treat
> this as a structural local blast-radius estimate, not a runtime guarantee.

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

Generic MCP clients use the shipped stdio command. HTTP consumers use Graphi's
separate REST/SSE surface; `graphi http` is not an MCP endpoint:

```bash
graphi mcp -db graph.db          # stdio JSON-RPC
graphi http -db graph.db         # loopback REST/SSE; tools at /analyze/{tool}
```

`surfaces/mcp.Server.HTTPHandler` is a package-level adapter for embedders, not a
shipped CLI transport. Binder-backed use must provide `rootUri` or inline roots
during `initialize`; this POST-only adapter deliberately rejects lifecycle flows
that would require a server-initiated `roots/list` request.

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
