# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Reading these notes: stability tiers

**[`docs/stability-tiers.md`](docs/stability-tiers.md) is the canonical definition**
of graphi's GA / Preview / Labs / Source-only tiers and of how they map onto the
CI-enforced [coverage matrix](docs/coverage-matrix.md). Two things follow for this
file:

- **Shipping ≠ supported.** An entry below announcing a capability records that it
  landed, **not** that it is GA. Only the 12 frozen operations, on **Go**, over
  **CLI + MCP stdio**, are GA. Every non-Go language is Preview; HTTP, the daemon,
  the web UI, the TUI, VS Code, the GitHub Action, refactorings, taint, memory, the
  wiki and semantic search are Labs.
- **Entries are historical and are not rewritten.** Each describes the state at its
  release date; where an older entry's tier language differs from today's, the
  canonical file wins. In particular, the `[experimental]` description prefix
  introduced in v0.1.3 was **superseded by `[labs]`** at the Focused Core RC
  (v0.5.0) — the artifact emits `[labs] ` today (`surfaces/mcp/tools.go`). Tool
  *names* have never carried a tier tag; they are frozen wire identifiers.

## [Unreleased]

> **If graphi ate your machine's memory** (macOS "your system has run out of
> application memory" during `graphi index`/`sync`, or a runaway `graphi mcp`
> spawned by an MCP client): that incident class is fixed in **v0.6.1** and
> hardened further below. What to do as a user:
> 1. Re-run the install script (`curl -fsSL …/install.sh | sh`) and confirm
>    `graphi version` reports **≥ 0.6.1**.
> 2. Fully restart MCP clients (e.g. Claude Desktop) so they relaunch
>    `graphi mcp` on the new binary.
> 3. Note that a bare `graphi sync` indexes the nearest **enclosing**
>    `.git`/`go.work`/`go.mod` root above your current directory — not the
>    `-db` you may have passed to an earlier `graphi index` run. Run it inside
>    the intended project or pass `-root` (current builds print the detected
>    root before indexing).
> 4. On very large repos, `GRAPHI_NO_TYPERESOLVE=1` or `-profile fast` skip
>    the whole-module go/types pass if memory is still tight.

### Changed
- Full-ingest peak memory is now bounded by the working set instead of the
  repository size, completing the v0.6.1 AST fix along every remaining axis;
  committed graph bytes are unchanged on all of them:
  - The walk no longer reads every file's contents up front: units carry
    path + content hash only, and each parse worker reads its file on demand
    through a shared root handle, so resident source is bounded by the
    parse-pool width instead of the whole repo. The typeresolve pass re-reads
    only what it consumes (`*.go` and `go.mod`); the drift scan
    (`graphi sync`/`status` warm path) no longer loads any file bytes at all.
  - Go ASTs are released as soon as each file's intra-procedural taint
    analysis completes (now run per file from the parse drain), instead of
    every file's go/ast + FileSet staying resident until the end-of-pass
    taint persist. Findings bytes and the malformed-config failure point are
    unchanged.
  - Ingest reads stream straight from SQLite via new optional GraphScanner
    store ports (`NodeIDs`/`ScanNodes`/`ScanEdges` with package-level
    fallbacks), so the pipeline no longer materializes whole-graph
    node/edge slices — and never (re)builds the store's whole-graph hot
    cache, which every per-phase batch commit used to evict and the next
    read used to rebuild, several times per pass. The linker's symbol index
    is built streaming (`link.IndexBuilder`); stale-edge sweeps collect ids
    during the scan and delete after it.
  - Batched write sessions no longer seed whole-graph state at open
    (previously the full node-id set plus every FTS rowid, per batch, three
    batches per pass): edge-endpoint checks memoize lazily off an indexed
    point probe, and FTS rows are keyed by a rowid derived from the NodeId
    (graphstore schema v4; a pre-v4 `search` table is rebuilt in place on
    first open — FTS is derived state, so snapshots and warm-start validity
    are unaffected). This also removes the O(table)-per-write FTS scans on
    the single-write PutNode/DeleteNode paths, whose owner-keyed deletes
    walked the whole UNINDEXED search table.

### Security
- Web client: upgraded to `react-router` 8.3.0 (dropping the retired
  `react-router-dom` wrapper) and React 19, clearing GHSA-qwww-vcr4-c8h2
  (RSC-mode CSRF in react-router 7.12–8.2) — the last high advisory the npm
  production audit flagged. No route or component behavior changes; the
  web test suite and the wiki link-rewrite preservation contract are
  unchanged.

### Fixed
- The filesystem watcher (`graphi daemon`/`serve`) no longer registers
  fsnotify watches under `node_modules`, `.git`, `vendor` and the rest of
  the ingest walk's always-pruned directory set — on macOS every watched
  path holds an open kqueue file descriptor, so dependency-heavy repos
  exhausted file descriptors and memory for trees the index never reads.
  Directories created under those names while watching are skipped too.
- The opt-in graphi-broad (CGO) flavor no longer leaks every parsed file's
  C tree: the parse retains the owning tree handle and the ingest pipeline
  releases it explicitly (`parse.ReleaseRoot`) once extraction is done —
  the bare tree-sitter runtime registers no finalizer on trees, so the Go
  GC alone never returned that memory. The pure-Go default build is
  unaffected.
- `graphi sync`/`rebuild`/`index` announce the repository root they detected
  when it is an ancestor of the current directory ("indexing repository
  root <root> (detected from <cwd>; pass -root to override)"), instead of
  silently full-indexing the nearest enclosing `.git`/`go.work`/`go.mod`
  tree — which could be a git-tracked `$HOME`.

## [0.6.1] - 2026-07-24

### Fixed
- MCP session open no longer stalls the client while it indexes: the
  repository binding (recover → warm/full ingest) now runs off the protocol
  loop, initialize answers within a short grace window, and tool calls fail
  closed with a retryable "still indexing" message until the session is
  ready. Previously a cold full index ran synchronously inside initialize;
  clients whose startup timeout expired killed and restarted the server,
  aborting and restarting the index each round — a kill/re-index spiral that
  could occupy a machine indefinitely. Closing the session (or a roots
  change) now cancels an in-flight index instead of letting it run for a
  session nobody serves.
- Concurrent sessions on the same repository share one index pass: the
  open → recover → warm/full ingest sequence of the auto-managed per-repo
  store is serialized under a cross-process SQLite lock
  (`ingest.lock.db` next to the sidecar), so N auto-started `graphi mcp`
  processes no longer each run a simultaneous full index of the same
  workspace — the winner indexes, waiters warm-start over the certified
  store. `graphi sync`/`rebuild`/`index` take the same lock.
- SQLite open DSNs apply `busy_timeout` BEFORE `journal_mode(WAL)`
  (graphstore, ingest sidecar, vector store): the WAL transition on a fresh
  database previously ran with no busy handler and could fail spuriously
  with SQLITE_BUSY when two processes opened the same state concurrently.
- Full-ingest peak memory no longer scales with the whole repo's parse
  forest: the parse phase now releases every non-Go backend AST (tree-sitter
  trees are routinely 10-40x their source size) as soon as extraction has
  produced the file's nodes/edges/refs, instead of retaining every file's
  tree until the end of the pipeline. Go ASTs are still kept for the
  go/types and taint passes. On large polyglot workspaces the old behavior
  alone drove `graphi index` to tens of GB of RSS (macOS "your system has
  run out of application memory"); committed graph bytes are unchanged.

## [0.6.0] - 2026-07-22

### Added
- Everyday lifecycle verbs over the auto-managed per-repo store, so normal use
  needs no `-root`/`-db`/`-meta` knowledge: `graphi sync` (flagless incremental
  update; announces `Branch switch detected: main → feature/login` after a
  checkout and summarizes the delta as added/changed/removed), `graphi rebuild`
  (explicit cold full pass), and `graphi status` (strictly read-only freshness
  report — repo, branch, drift, last sync — with `--json` and the exit-code
  contract 0 = current, 1 = actionable, 2 = error, so `graphi status || graphi
  sync` scripts cleanly). All three are facades over the stable `index`
  lifecycle; their coverage-matrix rows are `tier: labs` because the stable-12
  set is frozen.
- `[labs]` Named graph snapshots: `graphi snapshot <name>` freezes the
  checked-out worktree as a one-shot full index under
  `~/.graphi/<fingerprint>/snapshots/` (atomic tmp+rename build; branch names
  sanitize, `feature/login` → `feature-login`); bare `graphi snapshot` lists
  (with each snapshot's frozen branch@commit), `-rm <name>` deletes. `graphi
  compare <base> <head>` diffs two snapshots by name — or the reserved
  `current` for the live store — delegating to the `compare-branches` engine
  pass for byte-identical output; a missing name is an error listing what
  exists, never an empty-store diff.
- Sync metadata: every successful ingest (sync/rebuild/index, bare `graphi`,
  MCP session open) stamps `sync.last_time` / `sync.branch` / `sync.commit`
  into the store's `kv_meta`, resolved by the new stdlib-only
  `internal/gitinfo` (reads `.git` directly — worktrees, packed-refs, detached
  HEAD; no `git` subprocess, no cgo). Bare `graphi` now opens with a
  `graphi: repo <root> (main @ 1a2b3c4)` banner plus the branch-switch notice.
- Read-only open paths backing `graphi status`:
  `graphstore.OpenSQLiteReadOnly` and `ingest.NewReadOnly` (mode=ro +
  query_only, no schema writes, mutating entry points fail with
  `ErrReadOnly`), plus `ingest.DriftDetail` splitting the drift set into
  added/modified/deleted (`DriftSetWithProgress` is now a byte-identical
  wrapper over it).

### Changed
- `graphi index` without `-root` no longer errors inside a repository: it
  detects the cwd repo and (when `-db`/`-meta` are also omitted) targets the
  auto-managed per-repo store, exactly like `graphi sync`. The explicit-root
  contract is unchanged byte-for-byte, including the in-memory default without
  `-db`; after an explicit-root run a TTY-only stderr tip points at
  `sync`/`rebuild`. `graphi help` leads with the lifecycle verbs and moves the
  `index` long form under "Advanced".

### Fixed
- The release DAG no longer wedges on the unpublished draft of a superseded
  candidate: when the tag is absent or already peels to the gated SHA, the
  publish preflight deletes the stale draft by immutable release id and
  recreates it at the gated SHA (a stale *tag* still fails closed and needs
  manual removal). The draft lookup right after `gh release create` now polls
  the eventually consistent release list with a bounded retry instead of
  failing a fully green candidate on a read-after-write race — the failure
  that interrupted the first v0.6.0 publish attempt.

## [0.5.1] - 2026-07-19

### Added
- The M0 candidate is frozen and recorded in
  `docs/decisions/2026-07-m0-candidate-freeze.md`: the candidate is the merge
  commit of #55 on `main`, `4e72637d3c2c0dc7d32142a590d46c0c62c10733` (not the
  branch head `e285822`), and every measurement in the 9/10 program binds to it.
  The record states its digest with provenance — a reproducible verify-only build
  digest (`sha256=03f22af4…`, from the `release` workflow's `reproducible static
  release binary` job) genuinely exists for that SHA, while the **published**
  release digest is **UNKNOWN**: the candidate publishes nothing, because
  `CHANGELOG.md`'s first released header is still v0.5.0, which is already
  published at the parent commit `65713de`. Per plan §2.4, that UNKNOWN counts as
  not passed. The record also carries the change-control rule: the candidate SHA
  moves only for a documented blocker fix, and every move must list the
  measurements it invalidates. Linked from `docs/rc/focused-core-rc.md` §1.
- `docs/README.md`: a documentation map for the `docs/` tree. It separates user
  documentation, architecture/contributor documentation, and the
  machine-written, CI-wired files (coverage matrix, capability manifest,
  release scorecard, eval and RC evidence, ratchet baselines), and marks the
  CI-wired paths that must not be moved or hand-edited because Go code and
  workflows hard-code them.

### Changed
- Root `.gitignore` and `.graphi/taint.json` loading is root-confined and
  fail-closed: final/outside symlinks, non-regular files, concurrent path
  replacement, files over 1 MiB, and malformed content abort ingest before the
  repository walk. Missing files remain valid; nested `.gitignore` files remain
  unsupported, and the explicit `GRAPHI_RESPECT_GITIGNORE=0` opt-out bypasses
  only root `.gitignore` validation. Invalid gitignore errors expose the line
  number but never echo raw pattern content.
- The test gate no longer accepts expected failures. Permission-denial fixtures
  probe whether the active filesystem enforces mode bits and skip when that path
  cannot be exercised; every emitted test, package, or build failure is fatal.
  First-party Go packages are discovered with `go list` and dependency trees
  such as `node_modules` are excluded from execution.
- Stable reads now hydrate only requested nodes and bounded neighborhoods;
  resolution, related-file, risk, brief, and impact paths no longer fall back to
  an unbounded whole-graph materialization. Impact now reads incident edges
  through composite SQLite indexes or logarithmic-write in-memory ordered
  indexes, enforces MaxNodes-derived node/edge/kind work caps, selects capped
  kinds with bounded auxiliary memory, and reports every exhausted cap through
  `truncated`.
- MCP advertises exactly the 11 query operations available in a running session;
  `index` remains a lifecycle operation and Labs tools require explicit opt-in.
  Stable client ports expose a dedicated typed impact call instead of a generic
  analysis escape hatch.
- Evaluation reports enforce runner-bound budgets, execute every stable
  operation and validate its envelope/outcome class, mark dirty worktrees in
  provenance, and explicitly distinguish internal scores from independent
  project or competitor ratings. Task-level correctness is limited to Hero
  anchors and declared confirmed-edge assertions; broader real-repository
  accuracy remains unmeasured.

### Fixed
- Full-pass recovery now uses a persistent cross-store generation handshake and
  replays from reopened databases after interruption; warm start fails closed on
  mismatched or incomplete state.
- Definition lookup follows incoming `defines` edges, matching the graph's edge
  direction and the checked-in hero contract.
- REST and MCP HTTP requests have strict size limits and Host/Origin protection;
  the REST server additionally has bounded timeouts, signal-aware graceful
  shutdown, and active SSE cancellation. The VS Code client no longer presents
  an unvalidated bearer token as authentication.
- Web and VS Code clients consume the canonical search and impact payloads; the
  web graph supports parallel edges, and definition locations correctly convert
  one-based protocol columns to zero-based editor coordinates.
- The GitHub Action builds Graphi from the action source checkout instead of the
  consumer repository.
- Release publication is fail-closed around immutable published releases,
  peeled tag SHAs, exact draft reuse, asset checksums, workflow-bound provenance,
  and self-describing historical asset sets.

### Security
- Every remote GitHub Action used by repository workflows is pinned to a full
  commit SHA. A repository-wide regression test rejects floating refs; PRs also
  receive dependency-diff review, pinned `govulncheck` source analysis, and
  high/critical production-dependency audits for both npm workspaces. The
  publish DAG repeats the Go and npm vulnerability gates on the exact SHA it
  can tag, so an independent or stale workflow result cannot authorize release.
- The minimum Go toolchain is 1.26.5 in both `go.mod` and `go.work`, closing
  four reachable standard-library vulnerabilities reported against 1.26.3,
  including the `os.Root` confinement escape fixed by GO-2026-4970.
- Existing and newly created state directories/files are normalized to owner-only
  permissions (`0700`/`0600`), including SQLite sidecars.
- Full and incremental ingest open source files through a repository-anchored
  `os.Root`, validate the opened descriptor against root-confined `Lstat`
  results, and reject final symlinks, non-regular files, concurrent path
  replacement, and intermediate symlinks that escape the repository. Reads are
  capped at `MaxFileSize+1`, so growth after the descriptor-size check cannot
  bypass the memory bound; replacing an indexed file with a rejected path still
  removes its stale graph state.
- HTTP Labs endpoints and MCP Labs tools remain disabled unless explicitly
  enabled; unadvertised MCP tool calls are rejected.

## [0.5.0] - 2026-07-15

The **Focused Core RC**: the stable surface is frozen to 12 operations and
everything on it is now evidenced by an armed gate — selective reads, crash
recovery, privacy defaults, a zero-config MCP session, an SHA-bound release
pipeline, and a versioned evaluation harness. Publishing this version runs
through the new release DAG and requires lifting the publish lock
(`.github/publish-lock.json`, see `docs/rc/focused-core-rc.md` §5).

> **Upgrade note:** `.gitignore` is now respected **by default** (see
> Security below). The first index after upgrading runs one certified cold
> pass because the ignore-scope semantics stamp changed; set
> `GRAPHI_RESPECT_GITIGNORE=0` to restore the old scope.

### Added
- **Real-World Report Card** ([`docs/real-world-report.md`](docs/real-world-report.md)):
  the before/after record for two external field findings. Checked-in gates
  remeasure and protect the declared boundaries; exact table values are
  historical snapshots and may vary inside those budgets.
- **Per-project taint config** (`.graphi/taint.json`): merge custom
  sources/sinks/sanitizers over the built-in defaults by id (a new id appends, a
  matching id overrides or disables a default). Absent file → defaults unchanged;
  a malformed or invalid file fails the index **closed** rather than silently
  reverting to defaults. Read at index time; adding, editing, or removing it
  re-certifies warm-start with one cold pass.
- **Zero-config MCP session**: `graphi mcp` with no `-db` resolves the
  repository from the process working directory, performs the initial ingest
  (recovery replay included) before serving, and then answers the stable
  operations against the real indexed graph — `graphi setup` + a real client
  is now enough. The end-to-end journey is a standing subprocess test.
- **Frozen stable surface**: exactly 12 stable operations (`index`, `search`,
  `definition`, `callers`, `callees`, `references`, `neighborhood`, `impact`,
  `explain_symbol`, `related_files`, `change_risk`, `agent_brief`), enforced
  by a CI gate, published as a generated capability manifest
  ([`docs/capability-manifest.json`](docs/capability-manifest.json)), and
  consumable through typed client ports (`surfaces/client/ports.go`). No
  stable operation can silently degrade to a stub.
- **SHA-bound release pipeline**: one workflow (`release-dag.yml`) carries a
  single commit through gate → build → SBOM → provenance attestation → tag →
  publish; a red gate yields no tag and no release, and a reversible publish
  lock keeps releases impossible until it is lifted in a reviewed commit.
  Every action in the DAG is pinned to a full commit SHA.
- **Evaluation harness**: 20 versioned hero tasks over the 12 stable ops
  (`corpus/hero/`, with ambiguity/partial/empty/not-found failure classes and
  negative anchors), a per-repo full-run measurement harness
  (`cmd/eval -full-run`: index wallclock, peak RSS, DB size, warm per-op p95),
  a weekly `eval-full` CI workflow over the pinned real repos, and a Java/JVM
  monorepo (guava v33.0.0, SHA-pinned) joining the corpus. The v0.5.0 budgets
  were frozen from its then-current reference-runner method, never invented
  ([`docs/eval/hero-budgets.json`](docs/eval/hero-budgets.json)); after the
  measurement method changed they remain provisional compatibility ceilings,
  not a comparable current-performance ratchet.
- **RC dossier** ([`docs/rc/focused-core-rc.md`](docs/rc/focused-core-rc.md)):
  the G0–G4 evidence checklist, the Go/No-Go protocol, and the documented
  lock-lift step.

### Changed
- **Stable operations read selectively.** Every stable hotpath (structural
  queries, resolution, impact, related files, change risk) now uses indexed
  point lookups on both backends instead of whole-graph scans — byte-identical
  results (golden-tested), with measured scale-flat structural latency
  (≤ 600 µs p95 from a 1k-node to a 40k-node repo). The port contract is
  ADR 0003; EXPLAIN-plan gates pin the SQLite query shapes.
- **Session open replays crash recovery before trusting the store**: dirty
  units from an interrupted ingest are re-applied on open, and interrupted
  full passes purge crash orphans from the store itself (not a cache that may
  have rolled back). A kill-at-every-batch-boundary fault matrix proves
  byte-identical convergence with an uninterrupted index.

### Removed
- **`auto-release.yml`**: the `workflow_run` auto-tag chain listened to the
  wrong workflow (`release`, not `release-gate`) and could tag a commit whose
  gates never ran. The release DAG is now the only publish path — enforced by
  a repo-wide workflow-scan test.

### Fixed
- **Taint found 0/4 real injections → now 5/5, 0 false positives.** External call
  targets (`os/exec.Command`, `database/sql.DB.Query`, …) are materialized as
  interned `external` nodes (import-alias selectors + syntactic receiver-type
  inference), and a new intra-procedural dataflow connects a source to a sink
  inside a function with sanitizer-aware precision — closing the field finding
  where `analyze taint` reported a confident all-clear on a vulnerable app.
- **Java import fan-out collapsed** from file→file edges against every
  same-basename directory repo-wide to a single `file →imports→ package` edge on
  an interned package node (edges/node 15.56 → 0.96 on the fan-out fixture).
- **Storage diet**: edges are no longer FTS-indexed and the repetitive edge
  `reason` is interned into a dictionary (~500 → ~226 bytes/edge).
- **Link phase emits incremental progress** instead of minutes of silence on a
  large repo, and `receiverMethod` resolution is O(1) via a reverse index.
- **Monorepo defaults**: `node_modules`/`target`/`build`/`.gradle`/`dist` are
  pruned by default (opt back in with `GRAPHI_INDEX_ALL`).
- **`diagnose` de-noised**: `dead_symbol` exempts entry points
  (`@Test`/`@Bean`/`@Component`/`main`/test paths) via a new non-identity node
  `Meta` and `safe-delete` refuses to remove a live bean; `unresolved_reference`
  is aggregated to one diagnostic per target with a count instead of one per edge.
- **Honest taint verdict**: a graph with no sink candidates reports
  `no_sink_candidates`, not an empty/clean result.
- **Interned external nodes rolled out to Java/Kotlin/Python/TypeScript** (was
  Go-only): an import-path-keyed reference to a stdlib/3rd-party symbol whose
  package clause is absent from the repo becomes one interned `external` node
  with its exact fully-qualified name, so taint sinks and unresolved-target
  aggregation have a real node to match. Guarded so it never fabricates a node
  for an in-repo symbol or local (no node flood).
- **Community detection and the generated wiki are symbol-only**: `external`,
  `package`, and `file` artifact nodes no longer leak into community partitions
  or wiki member lists (the structural query and search surfaces already
  excluded them).
- **`dead_symbol` exempts `override` members** across the tier-1 languages: the
  Kotlin/C#/TypeScript `override` keyword joins Java's `@Override`. An override
  is invoked polymorphically through its supertype (an edge the static graph
  resolves to the base type), so it is reported as an info `entrypoint_candidate`
  and protected from `safe-delete`, never flagged dead.
- **`dead_symbol` exempts decorated TypeScript symbols**: an Angular/NestJS
  decorator (`@Component`, `@Injectable`, `@Controller`, `@Get`, …) on a class or
  method marks it as framework-invoked (a wiring the static graph cannot see), so
  it is an info `entrypoint_candidate` and protected from `safe-delete`.
- **`graphi daemon stop` (and SIGTERM) now terminates the daemon process.**
  Previously the listener and socket were torn down but the host process
  parked in `select {}` forever, so deferred cleanups (watcher stop, store
  close) never ran. Both paths now exit 0, remove the socket, and are
  restartable — pinned by subprocess lifecycle tests.
- **Crash-recovery gaps found by fault injection**: the post-crash purge was
  derived from a meta cache that had rolled back (orphaning nodes of renamed/
  deleted files); it is now derived from the store. `RecoverWithRoot` had no
  production caller; it now runs on every session open.

### Security
- **`.gitignore` is respected by default** when indexing: ignored files are
  exactly where secrets, local configs, and credentials live, and indexing
  them into a persistent, searchable graph violated the privacy default.
  Opt out with `GRAPHI_RESPECT_GITIGNORE=0` (see the upgrade note above).
- **On-disk state is owner-only**: graph databases (including `-wal`/`-shm`),
  the meta sidecar, and memory journals are created `0600` in `0700`
  directories, and existing world-readable files are migrated on open.
- **The memory store rejects secret-like content by default** (API keys,
  private keys, tokens) before anything is written; override with
  `GRAPHI_MEMORY_ALLOW_SECRETS=1`.
- **Labs HTTP routes are disabled by default**: PR/branch/review/distill
  endpoints answer 403 unless `GRAPHI_HTTP_LABS=1`, so experimental surface
  is opt-in.
- **Unimplemented refactors fail closed**: `extract`/`move` are rejected
  before any blast-radius read instead of returning a half-planned answer,
  and memory export renders inline instead of writing caller-supplied paths.

## [0.4.0] - 2026-07-05

### Added
- **Readable graph view.** Nodes in the web UI now carry their qualified name
  as an on-canvas label (files show their basename) and are colored by symbol
  kind (function, method, type, file, package, variable — see the legend);
  edges are labeled with their relationship kind ("calls", "references", …).
- **Deterministic radial layout.** The seed symbol sits at the center with one
  ring per hop (direct neighbors on ring 1, depth-2 nodes on ring 2). Positions
  are stable: an SSE refresh of the same graph no longer re-scrambles the view
  (previously every node landed at a fresh random position).
- The web UI adopts the site's terminal design system: deep green-charcoal
  palette, phosphor-teal primary, monospace chrome, and a dark node-hover
  label (Sigma's stock white box was unreadable on the dark canvas).

### Fixed
- **Clicking a node no longer white-screens the app.** Selecting a symbol
  (compare off) applied the citation highlight with the Sigma edge type
  `"dashed"`, which Sigma v3 has no program for — the render threw and
  unmounted the whole page. Citation edges are now amber + thicker, and a new
  error boundary contains any future canvas failure to an inline message with
  a retry button instead of a blank page.
- Edge clicks now work: graph edges are keyed by their payload id, so the
  "why connected" panel can actually resolve the clicked edge (the previous
  auto-generated keys never matched and the click was silently dropped).
- Deep-linking or reloading `/wiki` (and `/wiki/c/{id}`) in a bundled binary
  now serves the app instead of raw markdown bytes: browser document
  navigations (`Accept: text/html`) get the SPA shell, while the client's
  data fetches (`Accept: text/markdown`) still receive markdown — matching
  the vite dev-server behavior.

## [0.3.0] - 2026-07-05

### Added
- **Opt-in index scope:** `GRAPHI_RESPECT_GITIGNORE=1` honors the repository
  ROOT `.gitignore` (documented subset incl. `!` negation, anchoring,
  `dir/`-only patterns, `*`/`?`/`[...]`/`**`; nested .gitignore files are not
  consulted), and `GRAPHI_IGNORE=name,name` prunes extra directory basenames
  at any depth. Both change graph CONTENT, so both are off by default, the
  filesystem watcher agrees with the walk, and the warm-start stamp carries an
  ignore fingerprint — a store certified under one scope never warm-starts
  under another (one full re-index re-certifies).

### Changed
- **`graphi index` now warm-starts like bare `graphi`.** On an unchanged
  repository the command is a drift scan (milliseconds), and a small edit
  re-ingests only the changed files plus their cascade (seconds, including
  the go/types confirmed-tier recompute). `--full` forces the cold pass —
  e.g. to re-certify a store. Measured on this repository: cold 37.5s,
  unchanged re-run 21ms, one-file Go edit ~2s.
- Endpoint indexes on `edges(from_id)` / `edges(to_id)`: node-delete cascades
  and incident-edge lookups no longer full-scan the edge table. Content-
  neutral — listings stay id-ordered, graph bytes are unchanged.

## [0.2.2] - 2026-07-02

### Added
- **Warm start: bare `graphi` no longer re-indexes an unchanged repository.**
  When the per-repo state already holds a full index written under the
  current ingest semantics, startup runs only an animated drift scan
  (`graphi: checking for changes… N files`) and re-ingests just the
  changed/deleted files plus their dependency cascade through the incremental
  path — whose graph is byte-identical to a full pass (invariant-tested,
  including the confirmed tier). An unchanged repo starts in seconds with
  `graphi: index up to date (N files, checked in Xs)`; a small edit reports
  `graphi: updated M files in Xs`. Safety valves: a semantics stamp in the
  meta sidecar forces one full re-index after a graphi upgrade (content
  hashes cannot see binary changes), and ANY warm-path failure falls back to
  the tolerant full pass. Background ingests (watcher, edit applier) are
  unchanged and stay silent — the delta progress is scoped to the interactive
  start via `IngestChangedWithProgress`.

## [0.2.1] - 2026-07-02

### Added
- Live indexing progress in the terminal. Bare `graphi` (and `graphi index` /
  `graphi http`) now render an in-place status line on stderr while the repo
  is indexed — spinner, phase (scanning / indexing / linking / resolving
  types), and once the file total is known a percentage, the current file,
  and an ETA, e.g. `⠙ graphi: indexing 342/1200 files (28%) ~1m40s left —
  engine/ingest/ingest.go` — ending in a durable `graphi: indexed N files in
  Xs` summary. Non-TTY runs (pipes, CI, `TERM=dumb`) degrade to plain phase
  lines plus 25% milestones with no escape bytes. Under the hood
  `Ingester.WithProgress` reports full-ingest phase/per-file events
  (incremental/watcher paths stay silent), and the same events are mirrored
  to the observe broker as a throttled `ingest-progress` SSE event,
  advertised in the `/contract` stream descriptors.
- Homebrew/Scoop publishing automation: the `release-assets` job now renders
  the Homebrew formula and Scoop manifest from the release's real `SHA256SUMS`
  and pushes them to `samibel/homebrew-graphi` (`Formula/graphi.rb`) and
  `samibel/scoop-graphi` (`bucket/graphi.json`). The step is gated on the
  `PACKAGING_PUSH_TOKEN` secret (fine-grained PAT, contents:write on only
  those two repos) and skips cleanly until the maintainer configures it.

### Fixed
- `gen-packaging` version-prefix quirk: the Homebrew `version` field and the
  Scoop `version` (which feeds the `v$version` autoupdate URL) are now stamped
  with the BARE semver (`0.2.0`) while download URLs use the tag path
  (`/releases/download/v0.2.0/`) — previously one string served both, so a
  release render was wrong on one side no matter which form was passed. Both
  input forms now render byte-identically (unit-tested); the committed
  placeholder manifests were regenerated accordingly.

## [0.2.0] - 2026-07-02

### Changed
- Corpus manifest entries now pin the checkout HEAD sha (case-insensitive
  prefix, >=12 hex chars, fail-closed) in addition to the release tag —
  recorded from the first green corpus run. A re-pointed upstream tag now
  fails the pin step instead of silently changing the corpus.

### Added
- **The v0.2.0 milestone: type-checked (confirmed-tier) `calls`/`references`/
  `implements` edges for Go.** `engine/ingest` now runs the `engine/typeresolve`
  go/types pass as a third phase after the heuristic linker, at both the full
  and the incremental site. The whole repository is re-checked from the
  already-walked bytes, so the confirmed edge set is a pure function of the
  final source state and full-vs-incremental **byte parity holds by
  construction** (pinned by a dedicated invariant test). Every relation the
  type-checker proves is upserted at `confirmed`/1.0 over the heuristic edge
  with the same (from,to,kind) identity — correct receiver-type method
  dispatch, shadowing, and import resolution now come from the compiler's own
  answer, not from name matching. Degradation is honest and non-destructive: a
  package the checker cannot prove (parse error, import cycle, checker panic)
  keeps its heuristic edges — the proof is withdrawn, the knowledge is not
  (invariant-tested, including the round-trip back to confirmed once the cycle
  is fixed). Operational controls: `GRAPHI_NO_TYPERESOLVE=1` restores the
  heuristic-only behavior; non-Go edits skip the recompute; a go.mod edit
  re-links every linkable file so a confirmed edge that loses its proof
  degrades instead of disappearing (parity-tested against a fresh index).
  Acceptance is enforced in CI: the corpus harness gained `confirmed_edges`
  assertions (anchored on exact symbol-name matches, with hermetic bite-proof
  tests) and pins `Command.Execute → Command.ExecuteC` in spf13/cobra — a
  receiver-method dispatch the name heuristic cannot prove — at the confirmed
  tier.
- `engine/typeresolve` type-check + edge emission (dark, slice 3 of 4): a
  `Resolve` pass that runs stdlib `types.Config.Check` over the package units
  in dependency order with a tolerant importer (intra-repo imports served from
  already-checked units, stdlib/third-party as empty stubs, per-unit errors
  swallowed and counted — a broken package degrades itself, never the pass)
  and derives the first **confirmed-tier (1.0)** `calls`/`references`/
  `implements` edges from `types.Info` and `types.Implements`. Never
  fabricates: an endpoint must reconstruct to a NodeId in the committed node
  set or the intent is dropped and counted. The test fixtures pin the cases
  where the name heuristic is provably wrong and the type-checker is right —
  shadowed locals, same-named methods on two receiver types, same-named
  functions in two packages — each asserted against the REAL extractor+linker
  output over the same source. Still dark: ingest wiring is slice 4.
- `engine/typeresolve` package-graph plumbing (dark, slice 2 of 4): a pure
  go.mod `module`-directive parser (no exec, no network), directory=package
  grouping over the ingest walk's file bytes (test files excluded in v1,
  multi-clause directories degraded), intra-module import→directory
  resolution, and a deterministic Tarjan-SCC check order where import cycles
  degrade to heuristic-only instead of aborting. All pure functions,
  table-tested, including a 50-iteration determinism pin.
- `engine/typeresolve` (dark — not yet wired into ingest): first slice of the
  go/types confirmed-tier resolution pass for Go (v0.2.0 milestone). Contains
  the types.Object → NodeId identity mapping that mirrors the core/parse
  extractor's naming rules byte-exactly (receiver star/generics stripping,
  init and blank funcs, package-scope-only values), plus the golden cross-test
  that pins the real extractor's emitted NodeIds against the reconstruction in
  both directions — fabrication and drift each fail a test instead of silently
  dropping confirmed edges later. stdlib go/types only; no x/tools, no new
  dependencies.
- Real-repository smoke corpus (`cmd/corpus` + `internal/corpus` +
  `.github/workflows/corpus.yml`): CI now drives the built binary end-to-end
  (index → search → query → analyze → diagnose) against five pinned real-world
  repositories (cobra, flask, sinatra, ky, express — chosen to cover the
  historical first-contact bug classes and language spread), failing on any
  crash, panic marker, non-zero exit, or empty result where the manifest
  promises one. Assertions live in `corpus/manifest.json` (adding a repo is a
  data change); the harness's own tests are hermetic (local fixture repo,
  including a `.DS_Store` and a malformed-JSON file) and prove the harness
  bites — a crashing binary and a vacuous index both turn the run red. The
  workflow is deliberately separate from the zero-egress canary posture
  (shallow clones need the network). Runs on PR, push to main, and nightly.

## [0.1.3] - 2026-07-02

### Changed
- The PR-triage vertical (`list_prs`, `triage_prs`, `conflicts_prs`,
  `suggest_reviewers`, `compare_branches`, `critique_review`) and the agent
  memory/distill/skillgen suite are now marked **experimental**: their MCP
  descriptions carry an `[experimental]` prefix (single source
  `surfaces/mcp/tools.go`, CI-tested) and the README splits capabilities into
  Core vs. Experimental. Tool names are unchanged (frozen wire identifiers);
  these surfaces are unproven against real-world use and may change shape or
  be removed before 1.0.
- **BREAKING: `impact` direction semantics corrected (and the default is now
  `reverse`).** The engine had the two direction names swapped relative to the
  README, the tutorial, the HOWTO, and the reverse-dependency (rdeps)
  convention: `-direction reverse` silently returned *dependencies* (callees)
  and `forward` returned *dependents*. Every documented "reverse impact = what
  depends on this symbol" example therefore returned the wrong set, and the
  TUI blast-radius panel (which hardcodes `reverse`) showed dependencies
  instead of the blast radius. The vocabulary is now:

  | direction | before (wrong) | now |
  |---|---|---|
  | `reverse` | dependencies (outgoing edges) | **dependents / blast radius (incoming edges) — the default** |
  | `forward` | dependents (incoming edges) | dependencies (outgoing edges) |

  The engine owns the default (empty direction → `reverse`); the CLI and MCP
  surfaces pass the direction through verbatim. Internal blast-radius callers
  (edit planner, pr-risk, batched) were flipped with the swap, so their
  behavior is unchanged. A new cross-layer invariant test pins
  `impact reverse(X) ⊇ query callers(X)` so this class of inversion can never
  ship silently again. If you scripted `-direction forward` to mean "who is
  affected", change it to `reverse` (or drop the flag — it is the default now).
- `defines` is no longer a default impact edge kind: a file "defining" a
  symbol is containment, not dependency, and it put a file node into every
  symbol's blast radius as depth-1 noise. Pass
  `-kinds calls,references,defines` to opt back in.

- `cold_start_p95_ms` bench budget re-pinned from 100 ms (a fast-local pin the
  shared CI runners repeatedly failed at 261–294 ms, leading to retrigger
  roulette) to 400 ms with the CI runner class as the measured baseline.
- Documentation honesty pass: taint/PDG doc comments and README/FEATURES no
  longer claim statement-level dataflow the symbol graph cannot support
  (Sharir–Pnueli / "flow-sensitive" / statement-node phrasing corrected);
  `compare-branches` is documented as diffing graphi SQLite snapshot paths
  (never git refs); `refactor -kind extract|move` marked as currently
  performing a rename-style rewrite; the token-parity eval doc states the gate
  measures frozen hand-authored fixtures, not live engine output; the
  safe-delete one-line-removal limitation is documented. Internal sprint/epic
  planning artifacts (`sprints/`, `epics/`) removed from the public tree; the
  VS Code extension's `repository` URL corrected to `samibel/graphi`.

### Fixed
- **The documented CLI query path works against a persistent store again.**
  `makeClientOrOpenMeta` closed the SQLite store via `defer` before returning
  the client wrapping it, so every `graphi query|search|analyze ... -db <path>`
  ran against a closed store and failed — and the failure was swallowed
  (exit 1 with no output). The store now lives until the command finishes
  (caller-owned cleanup), and every CLI dispatcher prints the underlying error
  to stderr instead of exiting silently.
- Global `-db` / `-daemon` / `-meta` flags are now accepted anywhere in the
  argument list. They were only extracted from the FRONT of argv while every
  documented example places them after the operation
  (`graphi query callers -symbol X -db graph.db`), so the documented form
  silently ignored them.
- `graphi analyze` now runs the same per-repo session discovery as
  `query`/`search`; previously a bare `analyze` after a zero-config index
  silently ran against an empty in-memory store.
- `graphi diagnose`, `graphi inline`, and `graphi safe-delete` are now actually
  wired into the CLI dispatch. They were documented in the README, HOWTO, and
  FEATURES but fell through to the parse-a-file fallback
  (`cannot read "diagnose": no such file or directory`). The coverage matrix
  gains a machine-checked `cli-subcommand` category (statically enumerated from
  the dispatch switch in `cmd/graphi/main.go`) so this class of
  documented-but-unwired drift now fails CI.
- `inline` parenthesizes compound right-hand sides when splicing: inlining
  `Foo = a + b` into `x * Foo` previously produced `x * a + b`, silently
  changing semantics; it now produces `x * (a + b)`.
- `graphi savings` without `-ledger` prints a clear "pass -ledger <path>"
  message instead of a cryptic `open ledger: ledger: open : ...` error.
- Data race in the SSE test harness (`surfaces/http`), and the compound-path
  purity guard now pins `CGO_ENABLED=0` in its `go list` subprocess so it
  checks the shipped default build rather than inheriting the test process's
  environment — together these make the suite `-race`-clean.

- `graphi` (zero-config indexing) no longer aborts the entire ingest on the
  first `.json` file that is not valid strict JSON (`parse: json syntax error
  in "...": invalid character '{' looking for beginning of object key
  string`) — reported on a WireMock `__files` response body that uses
  Handlebars response-templating (`{{...}}` at a structural position), which
  WireMock renders at runtime but is not valid strict JSON. More generally,
  any genuine parse/syntax error in a file that DOES have a registered parser
  is now a recorded `SkipParseError` diagnostic (fail-closed skip) instead of
  a hard error, matching the existing oversize/timeout/max-depth/unreadable
  pattern. A single malformed file can no longer sink a FULL index of the rest
  of the repository. The INCREMENTAL path (`IngestChanged`, used by the edit
  applier and the filesystem watcher) stays strict: if a file it was asked to
  reparse no longer parses, it returns a hard error so its metadata transaction
  rolls back atomically — keeping the metadata sidecar consistent with the
  graphstore, and letting the edit saga compensate (roll back) an edit that
  produces source the parser rejects. Only PRE-EXISTING malformed files (seen
  by the full index) are tolerated.

### Added
- Release binaries now embed the web UI (`-tags webui_embed`, built via a new
  `cmd/release -webui` flag + node step in the release workflow), so the quick
  start's "your browser opens with the interactive code graph" is true for the
  binaries users install. Previously releases served the "UI not bundled"
  notice page at `/`.
- New `lint` CI workflow: `go vet ./...` and a `go test -race ./...` job — the
  standard Go hygiene gates the suite previously lacked. A `gofmt` job was
  added alongside them (after a one-time mechanical `gofmt -w` sweep of 47
  drifted files) so formatting drift cannot re-accumulate.
- Per-subcommand CLI help: `graphi help <subcommand>` and
  `graphi <subcommand> --help` print a synopsis, usage line, and a
  copy-pasteable example. The help map is completeness-tested against the same
  static dispatch-switch scan the coverage matrix uses, so a new subcommand
  cannot ship help-less.


## [0.1.2] - 2026-07-01

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest on the
  first file with no registered parser (`parse: no parser registered for
  file type`) — reported via a macOS `.DS_Store` file, but the same crash
  applied to any image, font, PDF, lockfile, or other unrecognized-extension
  asset, which is the overwhelming majority of non-source files in a
  typical repository. This is now a silent, expected skip (not a recorded
  diagnostic — finding a non-source file isn't noteworthy), matching the
  existing fail-closed pattern already used for oversize/timeout/max-depth/
  unreadable files.

## [0.1.1] - 2026-07-01

### Fixed
- `graphi` (zero-config indexing) no longer aborts the entire ingest when a
  repository contains a symlink whose target is a directory — the pnpm
  `node_modules/.pnpm` layout links whole package directories this way, and
  hit this on a real-world JS/TS repo (`EISDIR` while reading the symlink as
  if it were a regular file). Any unreadable path (symlink-to-directory,
  broken symlink, permission denied) is now a recorded `SkipUnreadable`
  diagnostic instead of a hard error, matching the existing
  oversize/timeout/max-depth fail-closed skip pattern.
- `node_modules`, `.git`, `vendor`, `.venv`/`venv`, `__pycache__`, and
  `bower_components` are now pruned from indexing entirely (never
  descended into), on both the initial full index and the live filesystem
  watcher. These hold dependency trees or VCS metadata, not a repository's
  own code — besides being where the pnpm symlink layout above lives,
  indexing them was slow and drowned query results in third-party noise.

## [0.1.0] - 2026-06-28

### Added
- First tagged release. The `release-assets` CI job cross-compiles the
  `internal/release.ReleaseTargets` matrix (linux/darwin amd64+arm64,
  windows amd64), generates `SHA256SUMS`, and uploads every asset to the
  GitHub Release — so the one-line installer's
  `releases/latest/download/...` URLs resolve instead of returning 404.
- Open-source community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, issue templates, and a pull-request template.

<!--
When cutting a release, move entries from Unreleased into a new section, e.g.:

## [0.1.0] - 2026-06-28
### Added
- ...
-->
