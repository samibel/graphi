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

## Operational workflow

1. Run `graphi sync` inside a checkout. Registration never creates or refreshes
   an index on its own.
2. Preview the exact client target with `graphi setup --per-repo --client
   <client> --dry-run`.
3. Run the same command without `--dry-run` to create one global named entry.
   The entry pins the validated DB and matching meta directory; changing the
   shell or client working directory does not reroute it.
4. Add `--auto-register --yes` only when future explicit CLI syncs should add
   newly synchronized checkouts to that concrete client/config target. A newly
   installed client or a newly resolved config path is not automatically added
   to the consent snapshot.
5. Use `--no-auto-register` to stop future sync writes. This changes policy
   only; it does not remove existing entries or indexes. Use `--unregister` for
   an unchanged, receipt-owned entry. The index is still retained.

`--name` is a complete `graphi-...` server key for a first registration.
Existing names remain stable. A foreign collision fails rather than being
overwritten or causing an existing Graphi server to be renamed.

## Visibility, selection, and provenance

A global server is visible to the selected client outside the checkout that it
describes. This is convenience, not repository isolation or an authorization
boundary. Tool metadata and requested results are delivered to that client and
may be sent onward according to the client's own behavior and account policy.
Graphi does not claim that a model will always choose the intended named server.
The stable name and short repository description are selection hints; explicit
user requests remain the reliable way to choose another registered checkout.

Each managed attach validates its `repo.json`, checkout fingerprint, DB, meta
sidecar, and registration reference before serving. MCP `initialize`
instructions identify only that bound checkout. Tool descriptions repeat only
its short label, and tool-result `_meta` carries namespaced provenance without
changing existing content or input schemas. A legacy arbitrary `-db` attach
remains supported but reports repository provenance as unavailable. A path that
looks managed but contradicts its descriptor fails closed.

Registration says which index a server reads; it does not prove index freshness.
Run `graphi status` and `graphi sync` after source or branch changes.
Unsaved editor changes are not implied to be indexed.

## Ownership, backups, and recovery

Graphi never treats the `graphi-` prefix as ownership. A matching manual entry
is left alone unless `--adopt` is supplied, and adoption succeeds only when its
binary, DB, meta path, and checkout binding match. Unknown fields, foreign
environment keys, and manual enabled/disabled state are preserved.

Before a client config replacement, Graphi records only digests and a redacted
managed-field snapshot in the private manifest. The writer takes a per-target
lock, validates the complete new JSON or TOML, creates a protected backup, and
atomically replaces the target. On restart, a pending digest matching the
target confirms the receipt; a digest matching the old state permits a retry;
any third state is reported as a conflict. Graphi does not blindly restore an
entire config. Separate client files are not one transaction, so a partial
success is reported and can be retried. A non-cooperating client can still
write after Graphi's last observed-state check; no stronger cross-application
transaction guarantee is claimed.

## Version evidence and untested variants

The public configuration contracts above were rechecked against official
documentation on 2026-10-02. Read-only local version discovery observed
Claude Code 2.1.287, Codex CLI 0.157.1, and Devin CLI 3000.11.3; it did not authenticate,
connect a model, or modify their real profiles. Automated tests cover Claude's
JSON shape, Codex TOML, Devin's dedicated and legacy JSON paths, and the
fail-closed ambiguous Devin-path case. Other client versions and undocumented
config variants remain unknown and must not be inferred as supported.

Real Claude/Codex/Devin connection smoke tests and model-selection experiments
were **NOT RUN** because no separate permission was given to exercise installed
clients. Consequently, no server-selection rate, startup benchmark, memory
scaling result, or token-use reduction is claimed.

## Deterministic acceptance evidence

All fixtures below use temporary synthetic checkouts and isolated home/state
directories. They require no account or network access.

| Matrix | Automated evidence |
| --- | --- |
| A01, A05, A07, A18, A19 | `TestGlobalMCPAcceptanceMatrixSynthetic` creates three named servers, checks distinct stores, byte-identical repeat setup, unchanged consumer trees, and a cross-repo attach from a foreign CWD. |
| A02–A04 | `TestSameBasenameDifferentRoots`, `TestSameRemoteDifferentCheckouts`, `TestSymlinkAliasKeepsLegacyID`, and `TestMultipleAliasStoresConflict`. |
| A06 | `TestTwoServersKeepSeparateContext`. |
| A08–A12 | Named-entry, unknown-field/number, invalid-config, dry-run, adoption, backup-failure, and disabled-state tests in `internal/mcpconfig` and `internal/mcpregistration`. |
| A13–A15 | `TestSuccessfulExplicitSyncRegisters`, `TestOnlyConsentedClientsUpdated`, `TestFailedSyncDoesNotRegister`, `TestMCPAutoBindDoesNotChangeConfigs`, and `TestPartialClientFailureKeepsIndex`. |
| A16 | `TestPendingReceiptRecovery` covers target/old/conflict digests without whole-config restore. |
| A17 | Missing-repo, fingerprint-mismatch, alias-conflict, and `TestManagedDescriptorMismatchFailsClosed` tests. |
| A20 | The visibility and selection section above is the product contract; no isolation claim is made. |
| A21 | Synthetic fixture names plus the final changed-file/diff privacy scan; real profiles and repositories are outside test scope. |
| A22 | Claude JSON, Codex TOML, Devin dedicated/legacy/ambiguous-path adapter tests; real authenticated smoke tests are explicitly NOT RUN. |
