# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
