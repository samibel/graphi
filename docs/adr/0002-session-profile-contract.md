# ADR 0002 — Session / Profile Contract and the MCP Repository Lifecycle (SW-113 / SP-10)

- Status: Accepted (decision spike — contract of record for `RUN-01`)
- Date: 2026-07-14
- Story: SW-113 — SP-10 (spike): Session/Profile RFC and a real MCP repository journey
- Spec / Gate: `focused-core-rc-g0-g1` — gate **G1** (contract freeze)
- Master WBS: `SP-10`; master plan §4 "Session/Profile"
- Depends on: SW-111 (frozen 12 stable ops + Stable/Labs/Disabled tiers), SW-110 (MCP-journey baseline)
- Governs (implemented later, NOT here): `RUN-01` — the `cmd/internal/runtime.Runtime`
  composition root and the CLI + MCP-stdio migration onto it.

## Status of this document

This is a **decision spike**. It is accepted as a **written contract**, not as running code.
It defines *what* the Session/Profile behavior must be so that `RUN-01` can implement against a
fixed target; the companion red-now journey test
(`surfaces/mcp_session_journey_subprocess_test.go`) encodes the same target as an executable
assertion that is deliberately skipped ("red until RUN-01") until the contract is built. Where a
decision cannot be made from current evidence, it is recorded below as an explicit **UNKNOWN**
with the experiment that resolves it — it is **not** silently assumed.

## Context — what is undefined today (evidence)

`graphi setup` wires an MCP client to launch the stdio server, but session / repository / store
resolution is not a defined product contract. Concretely, on `main` at the time of writing:

1. **Setup writes no repository binding.** `mcpconfig.GraphiEntry` (`internal/mcpconfig/config.go:45`)
   emits a server entry whose args default to exactly `["mcp"]` — no `-db`, no `-daemon`, no cwd.
   So the registered command is literally `graphi mcp`.
2. **`runMCP` does no discovery.** `runMCP` (`cmd/graphi/main.go:801-814`) calls
   `extractFlags(args)` then `makeClientOrOpen(dbPath, socket)` with whatever `-db`/`-daemon`
   the args carried. Unlike `runQuery`/`runSearch`/`runIndex` (which call
   `resolveSession(getwd(), …)` — e.g. `main.go:239, 264, 303`), `runMCP` **never calls
   `resolveSession`**. With the setup-written args (`["mcp"]`, empty `-db`), `makeClientOrOpen`
   opens an **empty in-memory store**: the server answers, but over an empty graph.
3. **The MCP server ignores the client's roots.** `surfaces/mcp/mcp.go` `initialize`
   (`mcp.go:106-111`) returns a static handshake and reads none of the `initialize` params;
   there is no `roots/list` round-trip and no `workspaceFolders` handling. Grep for
   `roots`/`Roots` in `surfaces/mcp/` returns nothing.

Net effect: **`graphi setup` + a real MCP client today yields a server bound to an empty graph
in the process's ambient working directory** — not the user's repository. The existing SW-110
characterization (`surfaces/mcp_journey_subprocess_test.go`) only proves the honest path *when a
caller pre-indexes and passes `-db` explicitly*; it does not exercise, and does not define, the
zero-config setup→MCP path. That gap is what this contract closes.

The path-resolution machinery to close it already exists as a pure helper — `internal/state`
(`RepoRoot`, `DetectRepo`, `Fingerprint`, `Resolve`, `Ensure`, `DiscoverDB`, `DiscoverSocket`) —
but nothing on the MCP surface calls it. This ADR specifies how `RUN-01` wires it in.

## Decision

### D0. One owner: the composition root (`cmd/internal/runtime.Runtime`)

Store, ingest, session, services, and shutdown are owned **exactly once** by the PLANNED
`cmd/internal/runtime.Runtime` (master §4 "Composition Root"; does not exist yet — `RUN-01`
creates it under `cmd/internal/` so `cmd` may import it without crossing the
`cmd→surfaces→engine→core` layer guard). Both `runMCP` and the CLI verbs construct their client
*through* the Runtime rather than each calling `makeClientOrOpen` directly. The MCP surface
(`surfaces/mcp`) stays a thin transport: it holds **no** discovery, ingest, or session logic —
consistent with the "one engine, many surfaces" invariant. All Session/Profile behavior below is
Runtime behavior invoked by `runMCP`, not new logic inside `surfaces/mcp`.

### D1. Repository identity and root

- The **repository root** is resolved with the existing `internal/state.RepoRoot` /
  `DetectRepo` walk: from a candidate directory, walk up for `.git`, then `go.work`, then
  `go.mod`, and take the first hit's directory. `DetectRepo` distinguishes "this is a code
  repository" (marker found) from "just some directory" (no marker).
- **Repository identity** is `state.Fingerprint(absRoot)` — a stable, path-only 16-hex-char
  SHA-256 of the cleaned absolute root. It embeds no time and no randomness, so identity is
  deterministic across runs and across CLI vs MCP (preserving the determinism invariant).
- The candidate directory is chosen per **D4** (roots → cwd precedence). One MCP session binds to
  **exactly one** repository for its lifetime (see D5); a session does not hop repositories.

### D2. DB / meta / state path resolution

- All per-repo state lives under `state.StateDir()`: `$XDG_STATE_HOME/graphi/<fingerprint>/` when
  `XDG_STATE_HOME` is set and non-empty, else `~/.graphi/<fingerprint>/`. From `state.Resolve`
  this yields, deterministically: `db.sqlite` (authoritative SQLite sidecar), `meta/` (sidecar
  metadata dir), `daemon.sock` (UNIX socket — **not used by the RC MCP path**, see D6),
  `repo.json` (path-only descriptor, `created:"-"` placeholder — no wall-clock).
- **Override precedence (highest wins):** explicit `-db` / `-meta` flags on the `graphi mcp`
  invocation → resolved per-repo layout from D1/D4 → today's empty in-memory fallback. An
  explicit `-db` therefore continues to behave exactly as SW-110 pins it (zero regression for
  callers who pre-index and pass `-db`).
- Directory creation is `state.Ensure`: `MkdirAll(dir, 0o700)`, `meta/` `0o700`, `repo.json`
  `0o600`, owner-only, idempotent, and it never rewrites an existing `repo.json` (deterministic
  content preserved).

### D3. Initial-ingest trigger and readiness signalling

- **Trigger:** when the resolved DB (D2) does **not** yet exist for the bound repo, the Runtime
  performs an **initial full ingest** of the repo root (the same pipeline `graphi index` drives:
  `engine/ingest` → `engine/link` → `core/graphstore`) before serving repository-dependent tool
  calls. When the DB already exists, the session opens it directly and does **not** re-ingest on
  startup (incremental freshness is out of scope for this RC — the daemon/watch path is Labs).
- **Readiness — protocol shape (UNKNOWN U1, see below).** The contract *requires* that a client
  can tell "indexing, not ready" apart from "ready, empty result" and never silently receives
  wrong-because-not-ready answers. Two admissible shapes, to be chosen by U1's experiment:
  1. **Synchronous-before-serve:** the Runtime completes initial ingest *before* returning the
     `initialize` result, so a successful handshake already means "ready." Simplest; cost is a
     slow first `initialize` on large repos.
  2. **Async with an explicit not-ready signal:** `initialize` returns immediately; repository
     tool calls made before readiness return a typed, uniform "indexing in progress" MCP result
     (an `isError:true` payload with a stable reason code), never a partial/empty success.
  The default this ADR adopts pending U1 is **(1) synchronous-before-serve**, because it needs no
  new wire shape, keeps MCP↔CLI parity trivial, and matches the RC's "MCP-stdio is the long-lived
  session" posture; U1 exists to confirm the first-`initialize` latency is acceptable on a
  realistic repo or to fall back to (2).

### D4. MCP `cwd` / roots behavior

- **Resolution order for the candidate directory (highest precedence first):**
  1. an explicit `-db`/`-root` override on the `graphi mcp` command (deterministic, wins always);
  2. an MCP **root** advertised by the client — the first `file://` root from the client's
     declared `roots` (via the `initialize` `capabilities.roots` / a `roots/list` round-trip),
     mapped to a local path and fed to D1's `DetectRepo`;
  3. the server process's **ambient working directory** (`os.Getwd()`), fed to `DetectRepo`.
- **When the candidate is not a repository** (`DetectRepo` returns `ok=false`) and no override is
  given, the session binds **no** repository and repository-dependent tools report a typed
  "no repository bound" result (not a crash, not an empty-graph success). `tools/list` and
  non-repository tools still work — mirroring today's graceful capability probing.
- **Roots wire details are UNKNOWN U2** (which MCP clients populate `roots`, whether a
  `roots/list` request is needed vs. reading `initialize` params, and single- vs multi-root
  handling). The contract fixes the *precedence and fallback semantics* above; U2 fixes the exact
  handshake. Multi-root: for this RC, **bind the first `file://` root**; multi-repo sessions are
  explicitly out of scope.

### D5. Session lifetime and shutdown

- **MCP-stdio is the long-lived agent session** (master §4). One `graphi mcp` process = one
  session, bound to one repository (D1) for the whole process lifetime.
- **Lifetime:** the session lives for the life of the stdio process; `Server.Serve` already runs
  the read/dispatch/write loop until stdin reaches EOF (`surfaces/mcp/mcp.go:71-99`). When the MCP
  client closes stdin / exits, `Serve` returns and the process exits.
- **Shutdown:** the Runtime owns teardown exactly once — on `Serve` returning (EOF) or a
  termination signal, it closes the store/services via the Runtime's single `Close`/cleanup path
  (superseding today's ad-hoc `defer cleanup()` in `runMCP`, `main.go:807`). Shutdown is graceful
  and idempotent; it flushes/commits nothing beyond what each tool call already committed (reads
  dominate; the RC MCP path is read-only for the 12 stable ops).
- **Out of scope (explicitly `RUN-01`, restated so nobody implements it here):** the daemon's
  `select{}` block (`cmd/graphi/main.go:1145`) that parks the *daemon* process forever. The daemon
  is **not** part of the first Focused Core RC; the MCP-stdio session above does not use it.

### D6. Supported operating systems

- **CI-verified today: Linux only** — every workflow in `.github/workflows/` is `runs-on:
  ubuntu-latest`. That is the only OS with mechanical evidence.
- **Intended RC support: Linux + macOS.** The default binary is CGo-free/static and the MCP-stdio
  path uses only stdin/stdout + local filesystem (no UNIX-socket dependency — the daemon socket in
  D2 is unused by the RC MCP path), so macOS is expected to work; packaging already targets
  Homebrew (`cmd/gen-packaging/templates/graphi.rb.tmpl`).
- **Windows: UNKNOWN U3 (not claimed for this RC).** The stdio path has no obvious blocker, but
  `internal/state` path semantics and the (Labs) daemon's `net.Listen("unix", …)`
  (`surfaces/daemon/daemon.go:151`) are POSIX-shaped and unverified on Windows. The RC does not
  claim Windows until U3's experiment passes.

### D7. Stable vs Labs profile

- The session serves the **12 frozen Stable operations** (SW-111 / spec): `index`, `search`,
  `definition`, `callers`, `callees`, `references`, `neighborhood`, `impact`, `agent_brief`,
  `related_files`, `explain_symbol`, `change_risk`. These, and only these, are the session's
  stable product contract.
- Everything else the MCP surface can advertise today (compound query, memory/distill/skillgen,
  the PR/review/reviewer vertical, deep analyzers, edit/refactor, semantic search, savings) is
  **Labs** — kept in-tree, capability-probed, **no stable claim** — or **Disabled**. The
  Stable/Labs/Disabled tier is machine-visible via SW-111's manifest; the MCP `tools/list` for a
  **Stable profile** advertises the 12 stable tools, and Labs tools appear only under the Labs
  profile. The capability manifest (SW-111, and CAP-01 later) is the single source of truth for
  dispatch = `tools/list` = CLI help = docs = coverage matrix; this ADR does **not** re-freeze the
  set, it binds the session to it.

## Explicit UNKNOWNs (each with the experiment that resolves it)

- **U1 — readiness protocol shape.** Sync-before-serve (default) vs. async-with-not-ready-signal.
  *Experiment:* on a realistic repo (e.g. a mid-size Go module of ~50–200k LOC) measure
  wall-time of the initial full ingest; if first-`initialize` latency under sync-before-serve
  exceeds an agent-acceptable budget (target: a few seconds), adopt async (D3 shape 2) and pin a
  typed "indexing in progress" result. Resolve in `RUN-01`.
- **U2 — MCP roots handshake.** Do the target clients (Claude Code / Claude Desktop / the clients
  `mcpconfig` wires) populate `roots` in `initialize`, or is a `roots/list` request required, and
  what do they send for a single-repo workspace? *Experiment:* capture the real `initialize`
  params + `roots/list` exchange from each wired client against a fixture repo; pin the observed
  shape as the D4 mapping. Resolve in `RUN-01`.
- **U3 — Windows support.** *Experiment:* run the D4→D3→tools/call journey on
  `windows-latest` in CI against the Go fixture; if the CGo-free stdio path passes with correct
  `internal/state` path resolution, promote Windows from UNKNOWN to supported; otherwise record the
  concrete blocker. Until then the RC claims Linux + macOS only.
- **U4 — flag vs. roots override ergonomics.** Whether setup should keep writing bare `["mcp"]`
  (relying on roots/cwd discovery, D4) or write an explicit `-root`/`-db` for robustness on
  clients that don't advertise roots. *Experiment:* fold U2's findings back into
  `mcpconfig.GraphiEntry`; if a wired client advertises no usable root and launches the server in
  a non-repo cwd, setup must write an explicit binding. Resolve in `RUN-01` alongside U2.

## Governed code seams (so `RUN-01` implements against this)

- `cmd/graphi/main.go` — `runMCP` (`:801-814`), `resolveSession` (`:816-840`, the additive
  discovery seam to reuse for MCP), `extractFlags`/`extractFlagsMeta`, and the daemon `select{}`
  (`:1145`, out of scope).
- `internal/mcpconfig/config.go` — `GraphiEntry` (`:45`, the setup-written command; U4).
- `internal/state/state.go` — `RepoRoot`/`DetectRepo`/`Fingerprint`/`Resolve`/`Ensure`/
  `DiscoverDB`/`DiscoverSocket` (the path/identity machinery D1/D2 reuse).
- `surfaces/mcp/mcp.go` — `Serve` (`:71`), `handle`/`initialize` (`:101-131`, where roots
  handling and readiness surface), `toolDescriptors` (`:908`, Stable-vs-Labs advertising).
- `cmd/internal/runtime.Runtime` — **PLANNED**, created by `RUN-01`; owner of D0/D3/D5.

## Consequences

- `RUN-01` has a fixed target: wire `runMCP` through the Runtime, resolve the repo via D4→D1,
  create/open state via D2, initial-ingest + signal readiness via D3, and own shutdown via D5 —
  with the four UNKNOWNs to close by experiment, not assumption.
- The companion red-now journey test asserts this end-to-end (setup→initialize→list→call on a real
  repo). It is checked in **skipped with an explicit "red until RUN-01" reason** so the suite and
  `testgate` stay green; `RUN-01` un-skips it to turn the contract green.
- No behavior changes on `main` from this story: `runMCP`, `surfaces/mcp`, and `mcpconfig` are
  **referenced, not refactored**. Invariants (CGo-free, zero-egress, layer direction, determinism)
  are untouched because no runtime code is added.
