# graphi-shunt - a Claude Code plugin that routes agents to graphi

Agents grep. graphi answers *who calls this*, *what breaks if I change it* and
*where is this defined* from an indexed code graph, in one call, with a
`file:line` and a confidence tier on every edge. graphi-shunt makes the graph the
path of least resistance: a `PreToolUse` hook blocks a repo-wide **symbol**
search and the block message names the graphi tool that answers it better.

The pattern is borrowed from Spotify's
[shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt)
plugin, which gates large file reads the same way. Tier: **Labs** (not part of
the frozen GA operation set; see [stability tiers](../../docs/stability-tiers.md)).

## Install

```bash
claude plugin marketplace add samibel/graphi
claude plugin install graphi-shunt@graphi
```

Prerequisites: `graphi` on `PATH` with the repository indexed (`graphi index`),
the graphi MCP server registered (`graphi setup`), and `jq`. Without any of
them the gates do nothing.

## What it does

Three layers, from hard gate to soft guidance:

1. **Hooks** (`hooks/`) - two `PreToolUse` gates, one for Claude's `Grep` tool
   and one for `Bash`. They block symbol-shaped repo-wide searches and explain
   the graphi alternative.
2. **Skill** (`skills/graphi-code-graph/SKILL.md`) - teaches the agent when to
   use which graphi tool, the `agent_brief` -> `related_files` ->
   `explain_symbol` -> `change_risk` workflow, how to read the response
   envelope, and when grep really is the right tool.
3. **No scripts layer, deliberately.** Shunt needs wrapper scripts because its
   backends are fragile shell pipelines. graphi's CLI verbs already are
   deterministic named-flag commands with byte-identical output to the MCP
   tools, and the primary path is MCP, where the agent never writes shell.

## Fail-open design

A gate blocks only when **all** of these hold, and allows the call otherwise:

- `GRAPHI_SHUNT_DISABLE` is unset
- `jq` and `graphi` are on `PATH`
- an index for this repository exists (`<state>/<fingerprint>/db.sqlite`, the
  same layout as `internal/state`; checked with one `stat`, no `graphi` process)
- the search looks like a symbol lookup
- this exact search has not been attempted before in the session

Any unexpected condition (unparsable input, missing tool, unwritable temp
directory, timeout) allows the call.

## Decision tables

### `Grep` tool

| Allowed | Blocked |
| --- | --- |
| gate not enabled (see above) | otherwise |
| `path` is a single existing file | |
| `path` under `docs/`, `site/`, `corpus/`, `testdata/`, `node_modules/`, `.git/` | |
| `type` / `glob` selects only non-code (`md`, `json`, `yaml`, `toml`, `csv`, ...) | |
| pattern is not symbol-shaped | |
| identical search already blocked once this session | |

### `Bash` tool (`grep -r`, `rg`, `ag`, `ack`, `git grep`)

The command is split on `&&`, `;` and newlines; each segment is judged on its
own. Only the first command of a pipeline can be a repo search.

| Allowed | Blocked |
| --- | --- |
| no search tool in the command (pure-shell prefilter, nothing is spawned) | a repo-wide search for a symbol-shaped pattern |
| the search reads from a pipe (`git log \| grep Foo`) | |
| `grep` without `-r` / `-R` | |
| every operand is an existing file or under a non-code directory | |
| command substitution, backticks, heredocs (not parsed) | |
| pattern not symbol-shaped or not confidently extractable | |
| identical command already blocked once this session | |

### What counts as symbol-shaped

Conservative on purpose: a miss only costs the agent a grep it was going to run
anyway, while a false block costs a round trip.

- CamelCase or snake_case identifiers (`IngestAll`, `parse_config`)
- bare lowercase identifiers of six or more characters (`scheduler`)
- qualified names (`auth.Middleware`, `ingest::IngestAll`), except file names
- definition probes (`func Foo`, `class Foo`, `type Foo`)
- call probes (`Foo\(`)

Everything else is text: short words (`main`), markers (`TODO`), multi-word
phrases, regexes, literals.

## Escape hatches

- **Repeat the search.** The first symbol-shaped search is blocked and recorded;
  the identical second attempt is allowed. Use it when graphi answers `empty`,
  `ambiguous` or `unavailable`, or when you need a literal match.
- **`GRAPHI_SHUNT_DISABLE=1`** turns both gates off for the session.
- Uninstall the plugin: `claude plugin uninstall graphi-shunt`.

## Latency

The `Bash` gate runs on every Bash call. A pure-shell `case` test on the raw
payload exits before anything is spawned unless the command could contain a
search tool. Enablement is `command -v` plus one `sha256sum` and one `stat`;
`graphi` itself is never started. Hooks run with a 5 s timeout, which fails open.

## Limitations

- The gate checks that an index **exists**, not that it is fresh. A stale index
  can still send the agent to outdated answers; run `graphi sync` after large
  edits and trust the source when it disagrees with the graph (the skill says so).
- The project directory comes from `CLAUDE_PROJECT_DIR` (fallback:
  `git rev-parse --show-toplevel`). If that is not the root graphi indexed, the
  index is not found and the gate stays off.
- Search commands hidden inside `$(...)`, backticks or heredocs are not parsed
  and pass through.
- Requires a POSIX `sh`; not supported on native Windows.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Nothing is ever blocked | `graphi status` (is the repo indexed?), `command -v jq graphi`, `echo $GRAPHI_SHUNT_DISABLE` |
| A legitimate text search is blocked | repeat it once; if the pattern shape keeps tripping, set `GRAPHI_SHUNT_DISABLE=1` and open an issue with the pattern |
| The agent ignores the redirect | confirm `graphi doctor` reports the MCP server registered |

## Development

The package under [`validate/`](validate) is a Go test suite in the root module,
so `CGO_ENABLED=0 go test ./extensions/claude-plugin/...` (and the repository
test gate) runs it with no extra wiring:

- static checks on `hooks.json`, both manifests, `SKILL.md` front matter, POSIX-sh
  conformance and the fail-open guards
- the deny text and the skill recommend only tools from the stable MCP descriptor
  fixture (`surfaces/mcp/testdata/mcp-descriptors-stdio-stable.json`)
- behavioural tests that execute the real gate scripts against hook payloads and
  assert every row of the tables above, the retry ledger, every fail-open path,
  and that the shell fingerprint finds the store `internal/state.Fingerprint` creates

The behavioural tests skip when `jq` or a SHA-256 tool is missing; the static
checks always run.

## Not in this version

`graphi setup --claude-plugin` (one-command install) and a `graphi claude-hook`
subcommand that would move the gate logic into Go (no `jq` dependency, one static
binary) are deliberately left for a later phase.
