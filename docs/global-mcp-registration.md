# Global checkout-bound MCP registration

This document records the compatibility and safety contract for Graphi's
global, named MCP registrations. Examples use synthetic repositories only.

## Verified implementation baseline

The implementation starts from these existing contracts:

- `graphi setup` already writes global JSON client configurations and keeps the
  historical server key `graphi`. Its separate `--project` mode writes
  `<repo>/.mcp.json`; `--attach` pins `mcp -db ... -meta ...` to the existing
  per-repository state layout.
- `internal/mcpconfig` already has adapters for several JSON clients. Its
  writer creates a private backup before atomic replacement and preserves
  unrelated top-level keys and sibling servers. It does not yet provide TOML,
  ownership receipts, cross-process serialization, or managed-field merging.
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
`CGO_ENABLED=0`. No TOML library is present in the baseline dependency graph.

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
