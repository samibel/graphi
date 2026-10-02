# Global checkout-bound MCP registration

This document records the compatibility and safety contract for Graphi's
global, named MCP registrations. Examples use synthetic repositories only.

## Verified implementation baseline

The implementation starts from these existing contracts:

- `graphi setup` already writes global JSON client configurations and keeps the
  historical server key `graphi`. Its separate `--project` mode writes
  `<repo>/.mcp.json`; `--attach` pins `mcp -db ... -meta ...` to the existing
  per-repository state layout.
- At the verified baseline, `internal/mcpconfig` had adapters for several JSON
  clients and a private backup/atomic replacement writer, but no TOML,
  ownership receipts, cross-process serialization, or managed-field merging.
  The implementation below extends that single writer path rather than adding
  a competing config subsystem.
- `internal/state` derives checkout state from an absolute repository root and
  its path fingerprint. `Ensure` creates the state directory and `repo.json`;
  read-only registration discovery must not call it or inherit its current
  working-directory fallback when no user home is available.
- `graphi sync` is the explicit CLI ingest path. It resolves and ensures state,
  runs `runtime.SyncRepo`, and returns success only after ingest completes. A
  registration hook belongs after the ingest session has released its files
  and lock, not in the shared ingest core.
- `runtime.Attach` opens an explicitly supplied store without repository
  discovery or ingest. `runtime.OpenSession` is the separate root-aware path
  that performs binding, locking, recovery, and ingest.
- `graphi doctor` is read-only. Its MCP check asks each adapter for a plan and
  must remain safe when named registrations and new formats are added.

The repository requires Go 1.26.6. The default build and tests run with
`CGO_ENABLED=0`. No TOML library was present in the baseline dependency graph.

## TOML editing choice

Codex configuration uses `github.com/pelletier/go-toml/v2` v2.4.3. It is a
pure-Go, MIT-licensed TOML parser. Graphi validates the complete document with
the stable decoder, then uses the pinned parser's raw syntax-tree ranges to
replace only Graphi-managed values. Unrelated bytes, comments, table order,
manual `enabled` settings, and nested tool policy remain untouched. Duplicate,
invalid, or structurally ambiguous tables fail closed. The raw AST API is
explicitly marked unstable by its upstream project, so the dependency stays
pinned and its focused preservation tests are the upgrade gate.

JSON and TOML writes share private backups, per-target Graphi locking,
same-filesystem temporary files, syntax validation, and a final observed-state
check before atomic replacement. A non-cooperating client can still write in
the narrow interval after that final check; Graphi does not claim a transaction
with external processes.

## Client compatibility

| Client | Global user configuration | Entry shape | Compatibility notes |
| --- | --- | --- | --- |
| Claude Code | `~/.claude.json` | `mcpServers.<name>` JSON object with `command`, `args`, and optional `env` | User-scope servers are visible in every project. Local and project definitions with the same name have higher precedence. `CLAUDE_CONFIG_DIR` is the documented configuration-directory override; Graphi's existing `CLAUDE_CONFIG_PATH` override remains supported for explicit test and non-standard paths. |
| Codex | `$CODEX_HOME/config.toml`, defaulting to `~/.codex/config.toml` | `[mcp_servers.<name>]` TOML table with `command`, optional `args`, and optional `env` | Preserve unrelated settings, comments, nested tool policy, and `enabled = false`. `CODEX_HOME` must already exist when explicitly set. |
| Devin CLI v3000.3 and newer | `~/.config/devin/mcp_config.json`; `%APPDATA%\devin\mcp_config.json` on Windows | `mcpServers.<name>` JSON object | v3000.3 (Local 3.6) moved MCP servers to this dedicated file. Preserve `disabled` and unknown fields. |
| Devin CLI before v3000.3 | `~/.config/devin/config.json`; `%APPDATA%\devin\config.json` on Windows | `mcpServers.<name>` inside the main JSON config | Do not rewrite both legacy and dedicated files. Ambiguous installations require an explicit config target. |

Authoritative references:

- [Claude Code MCP configuration](https://code.claude.com/docs/en/mcp)
- [Claude Code environment variables](https://code.claude.com/docs/en/env-vars)
- [Codex MCP configuration](https://developers.openai.com/codex/mcp)
- [Codex environment variables](https://learn.chatgpt.com/docs/config-file/environment-variables)
- [Devin CLI MCP configuration](https://docs.devin.ai/cli/extensibility/mcp/configuration)

## Development safety

Tests use `internal/testenv.Isolate` to redirect Unix and Windows home, state,
configuration, and supported client-home variables into a temporary directory.
They must pass explicit synthetic paths to every writer. Development and CI do
not modify real client profiles or write integration files into consumer
repositories.

Generated registrations bind an absolute Graphi binary to one validated
existing `db.sqlite` and its matching metadata directory. Registration does
not refresh an index, grant isolation from the selected AI client, or guarantee
which MCP server a model chooses.

## Named registration examples

```bash
# Preview or register the current already-synchronized checkout globally.
graphi setup --per-repo --client claude --dry-run
graphi setup --per-repo --client claude

# Select an existing checkout and its client-native config format.
graphi setup --per-repo --root /tmp/synthetic/service --client codex

# Consent once to future registration after explicit CLI syncs.
graphi setup --per-repo --client all --auto-register --yes

# Disable only that future policy; do not inspect a repo or remove an index.
graphi setup --per-repo --client claude --no-auto-register
```

`--all-repos` reads only existing Graphi `repo.json` descriptors. `--adopt`
requires an explicit matching name and store binding. `--unregister` requires a
matching ownership receipt and unchanged full entry; it never removes the DB or
metadata. Dry-run creates no config, backup, lock, manifest, or policy file.

Auto-registration runs only after a successful, explicit CLI `graphi sync` and
after the ingest session and lock have been released. MCP startup, tool calls,
library ingest, and `graphi rebuild` do not write client configurations. For
`graphi sync`, exit `0` means both sync and all consented integrations succeeded,
exit `1` means sync itself failed (so registration did not run), and exit `2`
means the index sync succeeded but at least one client integration failed. In
the exit-2 case the valid index and any successful client updates are retained;
the per-client error identifies what can be retried.
