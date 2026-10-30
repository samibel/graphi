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
- the searched tree belongs to a repository graphi has indexed: starting from
  the search's own path (the Grep `path`, a Bash operand, the directory after a
  `cd`, else the session directory) the hook walks up to the nearest `.git`,
  `go.work` or `go.mod` the way `internal/state.RepoRoot` does and checks
  `<state>/<fingerprint>/db.sqlite` with one `stat`; if that root has no index
  it keeps walking up through enclosing repositories. No `graphi` process runs.
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
| `path` under `docs/`, `site/`, `corpus/`, `testdata/`, `resources/`, `node_modules/`, `.git/` | |
| `path` in no indexed repository (a dependency, `/usr/include`, a clone without an index) | |
| `type` / `glob` selects only non-code (`md`, `json`, `yaml`, `toml`, `csv`, ...) | |
| pattern is not symbol-shaped | |
| identical search already blocked once this session | |

### `Bash` tool (`grep -r`, `rg`, `ag`, `ack`, `git grep`)

The command is split on `&&`, `||`, `;` and newlines; each segment is judged on
its own, and a `cd` segment moves the hook's view of relative paths along with
it. Only the first command of a pipeline can be a repo search. A search without
operands runs in the effective working directory, which must itself be indexed.

| Allowed | Blocked |
| --- | --- |
| no search tool in the command (pure-shell prefilter, nothing is spawned) | a repo-wide search for a symbol-shaped pattern: no operand, or only existing directories inside the repository |
| the search reads from a pipe (`git log \| grep Foo`) | |
| `grep` without `-r` / `-R` | |
| an operand is an existing file, under a non-code directory, or in no indexed repository | |
| an operand is a glob or does not exist (cannot be judged) | |
| command substitution, backticks, heredocs, unbalanced quotes (not parsed) | |
| pattern not symbol-shaped or not confidently extractable | |
| identical command already blocked once this session | |

### What counts as symbol-shaped

Conservative on purpose: a miss only costs the agent a grep it was going to run
anyway, while a false block costs a round trip.

- CamelCase or snake_case identifiers (`IngestAll`, `parse_config`)
- bare lowercase identifiers of six or more characters (`scheduler`), except a
  stop list of language keywords and common English words (`version`,
  `license`, `timeout`, `package`, `import`, ...)
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

The `Bash` gate runs on every Bash call. The command text is sliced out of the
raw payload with shell parameter expansion and a `case` test on that slice
exits before anything is spawned unless the command could contain a search
tool (a test pins this: a plain `ls` never starts `jq`). Enablement is `command -v` plus one `sha256sum` and one `stat`;
`graphi` itself is never started. Hooks run with a 5 s timeout, which fails open.

## Limitations

- The gate checks that an index **exists**, not that it is fresh. A stale index
  can still send the agent to outdated answers; run `graphi sync` after large
  edits and trust the source when it disagrees with the graph (the skill says so).
- The repository root is derived per search path, not once per session, so a
  portfolio layout works: the session runs in `portfolio/` (its own repository,
  not indexed) while `portfolio/workspace/<name>/` are product clones with
  their own `.git` and their own graphi index. `rg Foo workspace/mdp/src` or
  `cd workspace/mdp && rg Foo` is gated against `workspace/mdp`'s index; a bare
  `rg Foo` from `portfolio/` is not gated, because the portfolio itself has no
  index. The deny message names the checkout it found so the agent can run the
  CLI from there (`cd <root> && graphi search ...`) or pick that checkout's MCP
  server.
- Search commands hidden inside `$(...)`, backticks or heredocs are not parsed
  and pass through, as are operands the hook cannot resolve (globs, paths that
  do not exist from its point of view).
- Requires a POSIX `sh`; not supported on native Windows.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Nothing is ever blocked | `graphi status` (is the repo indexed?), `command -v jq graphi`, `echo $GRAPHI_SHUNT_DISABLE` |
| A legitimate text search is blocked | repeat it once; if the pattern shape keeps tripping, set `GRAPHI_SHUNT_DISABLE=1` and open an issue with the pattern |
| The agent ignores the redirect | confirm `graphi doctor` reports the MCP server registered |

## Releasing a change

Claude Code caches an installed plugin and treats it as updated only when its
version string changes ("users stay on their cached copy until the string
changes", per its marketplace docs). **Raise `version` in
`.claude-plugin/plugin.json` in the same commit as any change to the hooks, the
skill or the manifest**, and keep `version` out of the repository-root
`marketplace.json` entry (the manifest wins, and setting both only hides which
one is in effect). Users then run:

```bash
claude plugin marketplace update graphi
claude plugin update graphi-shunt@graphi
```

and `/reload-plugins` (or restart) in an open session, because a running session
keeps the previous hooks. This is independent of graphi's own release version.

## Development

The package under [`validate/`](validate) is a Go test suite in the root module,
so `CGO_ENABLED=0 go test ./extensions/claude-plugin/...` (and the repository
test gate) runs it with no extra wiring:

- static checks on `hooks.json`, both manifests, `SKILL.md` front matter, POSIX-sh
  conformance and the fail-open guards
- the deny text, the skill and this README recommend only stable tools: any name
  from the MCP catalog (`docs/mcp-tool-baseline.json`) they mention must be in the
  stable descriptor fixture (`surfaces/mcp/testdata/mcp-descriptors-stdio-stable.json`)
- behavioural tests that execute the real gate scripts against hook payloads and
  assert every row of the tables above, the retry ledger (including that it has
  no time window), every fail-open path, that the prefilter spawns nothing for a
  plain command, and that the shell root walk-up and fingerprint find the store
  `internal/state` creates - from the root, from subdirectories of it, and in
  the portfolio layout (unindexed outer repository, indexed clones beneath it)

The behavioural tests skip when `jq` or a SHA-256 tool is missing; the static
checks always run.

## Not in this version

`graphi setup --claude-plugin` (one-command install) and a `graphi claude-hook`
subcommand that would move the gate logic into Go (no `jq` dependency, one static
binary) are deliberately left for a later phase.
