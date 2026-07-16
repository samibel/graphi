# ADR 0002 — Session/Profile Contract and MCP Repository Lifecycle

- Status: **Accepted and implemented** (`RUN-01` is green)
- Original decision: 2026-07-14
- Implementation status reconciled: 2026-07-15
- Scope: zero-config CLI/MCP repository binding, readiness, profile selection, and shutdown
- Canonical implementation: `cmd/internal/runtime`, `cmd/graphi.runMCP`, `surfaces/mcp`

## Status of this document

This ADR is no longer a design spike for future work. The composition root and the
zero-config MCP repository journey are implemented. The former red-now journey in
`surfaces/mcp_session_journey_subprocess_test.go` is unskipped and green.

This document records the behavior that exists now. It does not claim compatibility
with MCP clients, operating systems, or repository sizes that have not been exercised.
Those gaps remain explicit under **UNKNOWNs**.

## Decision implemented

### D0. One resource owner: `cmd/internal/runtime.Runtime`

`Runtime` owns the store, ingester, observe broker, repository identity, surface client,
and cleanup callbacks. `Close` is idempotent and closes owned resources in reverse
construction order.

There are two deliberately different entry points:

- `runtime.Attach(dbPath, socket, metaDir)` preserves the explicit-path behavior. A
  socket creates a daemon client; otherwise the exact database is opened. An empty
  database path uses the historical in-memory store. Attach does no discovery and no
  ingest.
- `runtime.OpenSession(ctx, Options)` is the zero-config path. It resolves one real
  repository, creates the per-repository state, opens SQLite, recovers interrupted
  ingest work, performs warm or full ingest, and only then exposes the client.

The MCP transport does not discover repositories or construct stores. It receives a
`BindFunc` from the command composition layer and owns only the protocol lifecycle.

**Evidence:** `cmd/internal/runtime/runtime.go`,
`cmd/internal/runtime/runtime_test.go`, `cmd/graphi/main.go` (`runMCP`).

### D1. Repository binding and precedence

The implemented precedence is:

1. explicit `-db` or `-daemon` on `graphi mcp` uses `runtime.Attach` and bypasses
   discovery and ingest;
2. transport roots are authoritative when supplied;
3. the process working directory is considered only when the transport supplied no
   roots at all (`Options.Roots == nil`).

For transport roots, `runtime.OpenSession` tests candidates in advertised order and
binds the first candidate for which `state.DetectRepo` finds `.git`, `go.work`, or
`go.mod`. An explicitly empty or unrelated root set does **not** fall back to the
process working directory. Failure to bind returns `runtime.ErrNoRepository`; an empty
graph is never served as a successful substitute.

Tool discovery is binding-aware. The in-process binding exposes all 11 Stable
MCP tools; a transport that implements `client.CapabilityReporter` removes
unwired operations from both `tools/list` and the dispatch allow-list without
performing I/O. The current daemon binding therefore exposes seven Stable tools
and omits its four unwired agent-context RPCs.

One runtime binds one repository. A `notifications/roots/list_changed` notification
closes the current binding, fails subsequent tool calls, and requires a fresh MCP
session. It does not silently hop the running session to another repository.

**Evidence:** `runtime.resolveRepositoryRoot`,
`TestOpenSession_ClientRootsOverrideProcessCwd`,
`TestOpenSession_AuthoritativeEmptyRootsRejectCwd`,
`TestSessionBinding_RootsListChangedFailsClosedAndClosesSession`.

### D2. MCP roots lifecycle

The protocol accepts local filesystem roots through three paths:

- legacy `initialize.rootUri`;
- inline roots in the initialize payload;
- `roots/list`, requested after `notifications/initialized` when the client advertises
  the roots capability.

Only local absolute paths and local `file://` URIs are accepted. Remote-file hosts,
network URLs, and relative paths are rejected.

Binding timing is intentionally different by client capability:

- `rootUri`, inline roots, and clients without roots capability bind synchronously
  during `initialize`;
- roots-capable clients receive a `roots/list` request after `initialized`. Until its
  response has been validated and bound, repository-dependent tool calls fail closed
  with a waiting error.

Therefore, a successful `initialize` means "ready" only for the synchronous paths. For
the `roots/list` path, readiness begins after the roots response has produced a valid
binding. The old blanket claim that every successful initialize implies readiness was
wrong.

#### Transport boundary

The productive `graphi mcp` command implements this bidirectional lifecycle over
**stdio**. `roots/list` discovery and `roots/list_changed` handling require that
bidirectional stream.

`Server.HTTPHandler()` is a reusable, loopback-guarded, POST-only embedding adapter:
one HTTP request carries one JSON-RPC message and receives one response. It has no
request-associated SSE channel on which the server could issue `roots/list`. A
binder-backed HTTP initialize must therefore include `rootUri` or inline `roots`;
otherwise it fails with JSON-RPC `-32602`. The handler is not exposed as a standalone
full MCP streamable-HTTP server by the current CLI. `graphi http` is the separate Labs
REST/SSE surface in `surfaces/http`, not a listener for `surfaces/mcp.HTTPHandler`.

**Evidence:** `surfaces/mcp/session_test.go`,
`TestSessionProfile_MCPRepositoryJourney`,
`TestSessionProfile_MCPRootsListJourney`,
`TestMCP_HTTP_BinderRequiresInlineRepositoryRoot`,
`TestMCP_HTTP_BinderAcceptsRootURI`.

### D3. Durable state and permissions

Repository identity is `state.Fingerprint(absRoot)`, a deterministic path-derived
identifier. State resolves to:

- `$XDG_STATE_HOME/graphi/<fingerprint>/` when `XDG_STATE_HOME` is set;
- otherwise `~/.graphi/<fingerprint>/`.

The layout contains `db.sqlite`, `meta/`, `daemon.sock`, and `repo.json`. State
directories are owner-only (`0700`) and state files are owner-only (`0600`).
`state.Ensure` also tightens already-existing permissive state before returning; this
is not merely a creation-time promise.

**Evidence:** `internal/state/state.go`,
`TestEnsure_PermsAndRepoJSON`,
`TestEnsure_MigratesExistingPermissiveStateBeforeEarlyReturn`.

### D4. Recovery, ingest, and readiness

`OpenSession` executes the following order before returning a client:

1. resolve and ensure state;
2. open SQLite and the ingest sidecar;
3. replay durable interrupted-ingest state with `RecoverWithRoot`;
4. use the warm path only when the store and generation metadata are trustworthy;
5. otherwise run a full ingest;
6. construct query, lexical search, analysis, and review services.

This is synchronous-before-bind. No stable operation is dispatched against a partial
repository. A roots-capable MCP client may complete the protocol initialize exchange
before binding, but tool dispatch remains blocked until the synchronous bind/ingest
finishes.

Freshness is evaluated at session startup. Continuous watching is a separate Labs
daemon capability; the default MCP session does not promise live re-indexing after the
session is ready.

**Evidence:** `runtime.OpenSession`, `runtime.WarmOrFullIngest`,
`engine/ingest/faultmatrix_test.go`,
`surfaces/mcp_session_journey_subprocess_test.go`.

### D5. Stable and Labs MCP profiles

The product contract freezes 12 Stable operations:

`index`, `search`, `definition`, `callers`, `callees`, `references`,
`neighborhood`, `impact`, `agent_brief`, `related_files`, `explain_symbol`,
`change_risk`.

`index` is a repository lifecycle operation, not an MCP `tools/call`. Consequently:

- default `graphi mcp` advertises exactly **11** tools: the Stable set minus `index`;
- `graphi mcp -labs` explicitly opts into the capability-gated Labs catalog;
- the maximal registered union is **43** tools, but an actual Labs session may expose
  fewer when optional services such as forge/review are not wired;
- an unadvertised tool call is rejected before it reaches the client;
- `impact` has a dedicated Stable descriptor and dispatches only
  `StableClient.Impact`; a caller cannot select another analyzer through it;
- generic `analyze`, semantic search, edits, PR tools, memory/skills, compound and
  hierarchy extensions remain Labs.

Profile selection is a server construction option (`mcp.WithLabs`). The shipped CLI
passes it only for the explicit `-labs` flag; an embedding host may choose the same
option deliberately. It cannot be enabled by request payload or ambient client input.

**Evidence:** `surfaces/mcp/tools.go`, `surfaces/mcp/profile_test.go`,
`cmd/graphi/mcp_profile_test.go`, `surfaces/client/ports.go`.

### D6. Lifetime and shutdown

One `graphi mcp` process is one MCP session. EOF, `SIGINT`, or `SIGTERM` cancels the
serve context. Cancellation closes a closeable input such as `os.Stdin`, unblocking a
scanner that would otherwise wait forever. `Server.Close` releases the active binding
once, and the runtime cleanup remains idempotent.

**Evidence:** `mcp.Server.Serve`, `mcp.Server.Close`, `runMCP`,
`TestSessionProfile_MCPSignalGracefulShutdown`.

## Rejected behavior

- Serving an empty in-memory graph when zero-config discovery fails.
- Falling back to the process cwd after a client supplied authoritative roots.
- Returning successful empty answers while roots binding or ingest is pending.
- Changing repositories in-place after `roots/list_changed`.
- Advertising Labs tools in the default MCP profile.
- Routing Stable `impact` through the generic analyzer selector.

## Explicit UNKNOWNs

- **U1 — real-client roots compatibility.** Repository tests cover the protocol shapes,
  but captured end-to-end exchanges for every setup-supported MCP client are not in the
  repository. Client-by-client compatibility is **UNKNOWN** until those journeys are
  recorded and gated.
- **U2 — first-session latency at production scale.** Index performance is measured by
  the eval harness, but a user-visible first MCP binding/handshake budget on large
  repositories is not pinned. Acceptable synchronous startup latency is **UNKNOWN**.
- **U3 — multi-root product behavior.** The implementation chooses the first detectable
  repository in advertised order and binds one repo. A coherent multi-repository graph
  and cross-root semantics are **UNKNOWN** and not claimed.
- **U4 — macOS and Windows lifecycle parity.** Linux is CI-evidenced. Cross-compiled
  artifacts do not prove roots exchange, filesystem permissions, signals, or state
  semantics. End-to-end macOS and Windows support is **UNKNOWN**.
- **U5 — live freshness in default MCP.** The default session snapshots freshness at
  startup. Whether it should adopt a bounded watcher without inheriting the Labs daemon
  lifecycle is **UNKNOWN**.
- **U6 — full MCP streamable-HTTP product surface.** The repository contains a secure
  POST embedding adapter, not a CLI-owned bidirectional HTTP/SSE session server. Session
  IDs, server requests, reconnect/resume, and request-associated SSE semantics are
  **UNKNOWN** and not shipped as a product claim.

## Consequences

The former `RUN-01` blocker is closed: zero-config MCP sessions bind and ingest a real
repository, roots are scoped fail-closed, and cleanup is owned once. Remaining work is
validation and product policy, not completion of the old planned composition root.
