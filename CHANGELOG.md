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
  the web UI, VS Code, the GitHub Action, refactorings, taint, memory, the
  wiki and semantic search are Labs.
- **Entries are historical and are not rewritten.** Each describes the state at its
  release date; where an older entry's tier language differs from today's, the
  canonical file wins. In particular, the `[experimental]` description prefix
  introduced in v0.1.3 was **superseded by `[labs]`** at the Focused Core RC
  (v0.5.0) — the artifact emits `[labs] ` today (`surfaces/mcp/tools.go`). Tool
  *names* have never carried a tier tag; they are frozen wire identifiers.

## [Unreleased]

## [0.12.1] - 2026-08-29

**A correction, not an improvement.** 0.12.0's headline feature is an embedded
privacy attestation, and as shipped that attestation was forgeable in a way the
binary could have detected and did not. Anyone running a 0.12.0 binary they did
not build themselves should upgrade.

### Fixed

- **The privacy attestation is now bound to every toolchain fact the binary
  records about itself.** `graphi privacy-audit` previously cross-checked only the
  VCS revision and the target platform. Three further claims — `vcs.modified`, the
  `CGO_ENABLED` setting and the `-tags` build-tag set — were validated for shape
  and never compared against the values Go had written into the same binary via
  `debug.ReadBuildInfo()`, even though the code already read that structure for
  the revision. All five are now compared, and a disagreement yields `UNVERIFIED`,
  never a PASS.

  Each gap was demonstrated rather than inferred, and each demonstration was
  reproduced independently at review: a binary built from a genuinely dirty tree
  claiming `source_modified: false`; a binary linked with `CGO_ENABLED=1` claiming
  `cgo_enabled: "0"` and printing *"canonical build contract enforced
  CGO_ENABLED=0"*; and a binary linked with **no** build tags claiming the
  canonical 21-tag set.

  The third is the one that mattered most. The build-tag set scopes the evidence
  digest and is the exact input the report tells a sceptical user to feed into
  `go run ./cmd/canary -tags '<…>'` — so that lie did not merely misreport, it
  would have sent the verifier to scan a dependency graph the binary was never
  built from, and the reproduction would have agreed.

  `EvidenceDigest` remains the one field with no in-binary ground truth; that is
  inherent — checking it requires the source tree — and is stated in the report
  rather than implied away.

- **The 0.12.0 release notes above have been restored** to describe what 0.12.0
  actually shipped. The fix commit had edited that section to describe the
  corrected behaviour, which left the notes for a published release claiming more
  than its binaries do.

### Note on what this does and does not defend against

The official 0.12.0 binaries are honest: they were built by the canonical path and
their attestations are true. The defect is not that released binaries lie. It is
that the attestation exists precisely so a user can trust a binary they did not
build — and until this fix, a third party could ship a forged one that
`graphi privacy-audit` would vouch for. An attestation that cannot distinguish
itself from a forgery does not do the job it was added for.

## [0.12.0] - 2026-08-29

Privacy evidence now describes the shipped binary instead of accidentally
describing whichever source checkout happens to be the current directory. This
is a trust-boundary correction: the static telemetry proof remains a build-time
source measurement, while the runtime command reports exactly which measurement
was accepted for the binary being executed.

The embedded record is deliberately not presented as an independent signature.
`privacy-audit` prints the running binary's SHA-256 together with the source
revision, target, build tags and evidence digest; a sceptical user still verifies
that binary against the published `SHA256SUMS`/build provenance or reproduces the
source scan and build.

### Changed

- **Canonical release binaries carry source-bound privacy evidence.** Before each
  target is linked, `cmd/release` runs the static telemetry gate against the exact
  `GOOS`/`GOARCH`, CGo-free setting and build-tag set. Only a complete PASS with a
  deterministic evidence digest can be embedded; a failed, unavailable or
  incomplete measurement stops the release build instead of producing an
  assertion. `graphi privacy-audit` binds that record to the running binary by
  cross-checking the VCS revision and the target platform against what the Go
  toolchain independently recorded into the same binary. The report also prints
  the binary digest needed to check published provenance.

  **Corrected after release — see 0.12.1.** As shipped, 0.12.0 cross-checked only
  two of the record's five toolchain-observable claims. `vcs.modified`, the
  `CGO_ENABLED` setting and the `-tags` build-tag set were validated for shape
  only and never compared against the values Go had recorded into the same
  binary, so a hand-forged ldflag payload could make a dirty-tree, CGo-enabled or
  differently-flavored binary describe itself as clean, CGo-free and scanned.
  This paragraph originally described the fixed behaviour; it has been restored
  to what 0.12.0 actually does.

- **An unavailable source scan is no longer rendered as a toolchain failure.** A
  non-canonical developer binary run outside the graphi module now reports the
  static CGo and telemetry properties as `UNVERIFIED`, explains that no embedded
  evidence or source module is available, and gives an actionable verification
  path. It does not expose a raw `go list: exit status 1`, and it never turns the
  missing measurement into PASS or FAIL. The live zero-outbound check and its
  three-state verdict remain independent.

- **Privacy CI exercises the end-user invocation.** The gating Linux job builds
  through the canonical release path and runs the resulting binary from `/tmp`
  under loopback-only network isolation, so a checkout-relative implementation
  cannot satisfy the gate.

### Added

- **`graphi privacy-audit --source` keeps the developer checkout scan explicit.**
  Its output is scoped to the selected source tree and says that it does not
  describe the running binary. `--target` is accepted only with `--source`, while
  `cmd/canary` exposes target platform and build-tag inputs for reproducing an
  attested release measurement offline.

## [0.11.1] - 2026-08-28

A diagnostics-only patch. No operation behaves differently, no wire name, request
schema, canonical result byte or error code moves, and the eleven-tool default MCP
profile is unchanged.

**Cut now, deliberately, and before any evidence accrues.** `0.11.0` reports the
executor seam's divergence record in wording that reads as *not yet observed* when the
truth on a stock install is *cannot be observed here* — so assessing SW-238's
precondition (a) with a `0.11.0` binary reproduces the exact defect this release fixes.
The observation count is still zero everywhere, which makes this the last moment at
which every future observation can come from a single, honest binary: the persisted
record carries `schema` and `pid` but no binary version, so provenance across two
versions would have to be attested by hand rather than read.

**If you parse the JSON**: the document-level `state` gains `UNKNOWN-AND-UNOBSERVABLE`,
which *replaces* a bare `UNKNOWN` on a stock install rather than appearing beside it.
This is a diagnostic surface with no stability contract, hence a patch — but it is not
an additive change, and a consumer matching on `UNKNOWN` exactly will need updating.

### Changed

- **A shadow-path operation no shipped profile can reach now says so** (SW-248).
  `v0.11.0` reports `10 migrated operation(s): 0 legacy, 10 shadow, 0 active` and
  `UNKNOWN: no dual-run observation has been recorded`. Both lines are true and
  together they mislead: all ten migrated operations are Labs, the default MCP
  profile advertises the eleven Stable tools, so a client bound to the shipped
  default cannot reach one of them and the record stays empty however long a
  release line runs. The output read as *not yet*; the truth was *not possible
  in this profile*.

  `graphi doctor` and `graphi doctor -divergence` now carry a second axis beside
  the observation state — whether anything can reach the operation at all — and
  three conditions that used to render identically now read differently: **not
  yet observed but reachable**, **not observable through the bound profile**, and
  a record that **cannot fill** because nothing on the seam is reachable there.
  The document-level `state` gains `UNKNOWN-AND-UNOBSERVABLE` for the third; see
  `docs/executor-seam-rollback.md` §5 for the JSON detail.

  This is disclosure, **not** a promotion. No tier moved: Stable wire names,
  request schemas, canonical result bytes, error codes and the eleven-tool
  default profile are byte-unchanged, and `cmd/coverage -check` passes 7/7 with
  no tier-row change.

### Added

- **`cmd/seamreach`, the reachability gate** (SW-248). It fails a build where an
  operation dual-runs on the executor seam that no shipped surface profile can
  reach, and checks the live matrix against a reviewable declaration
  (`internal/seamreach/reachability.txt`) so a change to what the seam runs — or
  to what a profile advertises — appears in the diff. The `executor-rollback`
  workflow runs it and then runs it again with a deliberately introduced
  violation, asserting it exits non-zero: the gap this closes existed because
  nothing asked the question at merge time.

### Documentation

- **The command table lists the shipped `graphi extension` verbs** (SW-246). The
  rule-pack family — `validate` · `install` · `list` · `doctor` · `enable` · `disable`
  · `remove`, plus the developer kit's `init` · `lint` · `conform` — shipped in
  SW-229/SW-230 and was absent from `readme.md`, whose only occurrence of the word
  "extension" meant the VS Code editor extension. A reader searching for the feature
  found a different one. The collision is resolved and the row added.

  A contradiction fell out of it: the readme claimed "all **59**" subcommands while
  its own gated census, eighteen lines earlier, already said **60** — the manifest
  carries 60 `cli-subcommand` rows and `extension` is the 60th. The wrong number
  survived because it sat in HTML bold with no census label, where the claims gate
  could not see it.

  Room for the new row was made inside the 400-line ceiling by deleting a duplicated
  subcommand row, not by moving the ceiling; four standing refusal conditions are now
  recorded beside the constant that holds it.

## [0.11.0] - 2026-08-28

### Changed

- **Shadow mode is off the caller's critical path** (SW-245, AX-12). In `shadow`,
  `DispatchOperation` now returns on the legacy result as soon as the legacy method
  answers; the executor pass, the byte-exact comparison and the recorder hand-off
  run on a single worker over a bounded 64-deep queue. No sampling: every dispatch
  is queued for the same byte-exact comparison it received before.

  **Measured**, under SW-242's recalibrated AX-06 method (same-run A/A control,
  N=200 per arm): p50 **783.542 µs (2.05× legacy) → 0.994× legacy**, p95 1.65× →
  0.90×, both inside the run's A/A control band — the honest claim is "no longer
  separable from legacy at this resolution", not "faster". The non-vacuity leg
  (an injected synchronous dual run) fails the new bar at 2.2×.

  **Correction, round 3.** The AX-06 sampler first shipped this without draining
  the deferred comparisons between samples, on the argument that the worker's
  concurrent load is what `shadow` really costs a host. The rotation is cyclic,
  so adjacency is fixed rather than shuffled — `shadow` is followed by `legacy-a`
  in three of every four occurrences and by `legacy-b` in one — and the leaked
  comparison therefore landed on one A/A control arm three times as often as on
  the other. On PR #172's runner that drove the gate's own control to 26–38 % of
  the baseline (it can judge a median only under 13.3 %) and took the AX-06 gate
  to UNKNOWN for three rounds against an *injected 2× seam regression*, while
  flattering AC-1's own p95 (0.918× undrained, 1.012× drained). The sampler now
  drains outside the timed window after every call; `TestSW245_SamplerDrainsBetweenSamples`
  pins it, and §7.2.1 of `docs/rc/ax06-canary-latency.md` records the correction.
  No gate arithmetic changed: `canaryLatencyBudget`, `evaluateCanaryStat`, the
  floor/noise/ceiling constants and the `budget ≤ 4×fixedBar` clamp are
  byte-for-byte SW-242's.

  **What did not get cheaper, stated rather than omitted** (AC-6): allocations stay
  **2.03×** legacy; a saturating single caller still absorbs 1.26× ns/op; at
  `GOMAXPROCS=1` 1.89×, where the queue starts dropping and says so. The work
  moved, it did not shrink.

  **Coverage is disclosed, never inferred** (AC-4). The `executor-divergence-v1`
  record gains three fields — `dispatches`, `skipped`, `coverage` — and
  `graphi doctor -divergence` prints a coverage line on every document
  (`N of N dispatch(es) compared (100%) — no sampling, nothing dropped`) and
  refuses PASS while anything is skipped. Three skip reasons exist and each is
  counted and flushed immediately: `queue-full`, `drain-abandoned` (shutdown drain
  out of budget) and `caller-cancelled`. Old records read correctly:
  `dispatches = observations + skipped`.

  **A cancelled caller is not compared.** A caller that cancels or times out
  during the legacy call would otherwise produce a false, permanent
  `error-presence` divergence (`legacy: context canceled / executor: ok`) and flip
  doctor to DIVERGED on no evidence. Such dispatches are skipped and counted
  `caller-cancelled`; a live caller's ordinary legacy error is still compared.

  **Durability** (AC-3). `Runtime.Close` drains the queue before the closers run,
  then flushes — a mismatch still queued when the session ends is on disk after
  `Close()` alone. The recorder is drained before it is retired or replaced on a
  state-dir rebind. `graphi http` drains on exit too, but installs no recorder and
  applies no kill switch (unchanged, now documented instead of over-claimed). The
  known ≤2 s coalescing loss now applies to killed processes only.

  **Untouched:** `canaryLatencyBudget`, SW-242's 250 µs floor, 10 % relative term,
  3× noise term and the `budget ≤ 4×fixedBar` clamp; the AX-06 gate still goes red
  on an injected seam regression on both shapes. Stable-12 wire names, request
  schemas, canonical result bytes, error codes and the default MCP profile are
  byte-unchanged; `cmd/coverage -check` 7/7 with zero tier-row changes.

  Known, disclosed, not fixed: a busy state-dir rebind can stall `OpenSession`
  up to the 5 s drain bound (then counts `drain-abandoned`); a legacy *success*
  whose caller cancels between return and hand-off is counted `caller-cancelled`
  (coverage loss only).

- **The shipped canary default is `shadow`** (SW-244, AX-12). SW-228 compiled the
  executor seam in `legacy`, so `graphi doctor` reported 10 legacy / 0 shadow /
  0 active on tip and SW-238's precondition — a release line with the shadow
  catalog live — was unreachable. The change is one constant; the work was
  proving it affordable and proving the record it turns on actually fills.
  SW-228's reversal condition ("only together with a way to READ a divergence
  outside the test binary") was met by SW-232, and
  `TestSW244_ShippedDefaultIsShadow` now pins `shadow` with the two bars that
  must hold for it to stay.

  Measured cost at the time of the flip, same method: p50 2.05×, p95 1.65×,
  allocations 2.04× — residue after subtracting `legacy + executor` was 9.5 µs
  and exactly zero allocations, i.e. the seam itself is cheap and the price was
  the second path running synchronously. SW-245 (above) removes that price in
  this same release. `canaryLatencyBudget` was extracted, not changed;
  `bench/bench-budget.yml` untouched; binary size +0 B.

  Consequences that moved with the flip: `ExecutorSeamCheck` no longer WARNs a
  stock install about its own shipped configuration — the WARN is keyed on an
  *overridden* mode, the compiled-in shadow is INFO carrying the measured cost and
  the opt-out; the divergence readout footer no longer explains all-UNKNOWN as
  "because the shipped position is legacy"; a CI leg drives real `graphi mcp`
  sessions and a separate `graphi doctor -divergence` end to end. Recorded, not
  smoothed over: the CLI does not reach the seam (MCP/HTTP only, by AX-06 design).

- **Contention-aware AX-06 latency gate** (SW-242). A decisive FAIL is no longer
  erased by an unjudgeable measurement. PR #169 failed on GitHub's runners with
  rounds FAIL, FAIL, UNKNOWN and reported UNKNOWN — which rendered green, because
  `cmd/testgate` does not summarise skips. Two stacked ordering bugs: within a
  round, a p95 FAIL at 42× its budget was discarded because that round's p50 was
  unjudgeable; across rounds, that one UNKNOWN erased two FAIL rounds that had
  caught the same regression. A FAIL is now named **decisive** when
  `overhead > 3 × max(ceiling, widest same-run A/A control at that percentile)`,
  and only a decisive FAIL outranks the absence of a measurement; everything else
  stays marginal inside an UNKNOWN, which keeps the false-FAIL rate where it was.
  Measured over 2000 three-round trials per condition under the contended noise
  model: shipped 0.05 %, rejected "any FAIL stands" 9.75 %; the literal "3× the
  failing round's own control" form was implemented first, measured at 2.365 %
  (a 24× regression) and rejected — which is why the bar is the widest control in
  evidence, not the luckiest. Same-run A/A reference, N ≥ 200 per arm, 250 µs
  floor, 10 % relative term, 3× noise term, `budget ≤ 4×fixedBar` clamp.

- **Binary-size baseline re-pinned, budget held, ratchet stated** (SW-243).
  Measured from a clean checkout of main @ `ebe0e87` in the canonical release
  shape (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1`, `-trimpath
  -buildvcs=true`, the 21 default grammar-subset tags): **35,351,306 B**,
  reproducible, against the unchanged **36,100,000 B** budget — 748,694 B
  (2.07 %) headroom. The baseline had gone 1,084,199 B stale, so every green run
  reported a wrong delta; the budget does not move because the condition that
  justified the 2026-08-24 raise (headroom below one toolchain patch bump) is
  absent by 84.6×. Ceiling and refusals are now written into
  `bench/bench-budget.yml` and `docs/ci/bench.md`: no raise without same-method
  attribution; none for a newly linked existing dependency until shown needed on
  the default path; none while headroom exceeds 10× the largest measured
  toolchain-noise event (88,540 B); no budget above 40,000,000 B without an ADR
  amending ADR 0001.

- **One MCP tool descriptor per tool, across binding profiles** (SW-241, AX-12).
  This is a **wire change to the `-labs` profile only**; the shipped default MCP
  profile is byte-identical.

  **What was wrong.** Ten of the eleven Stable MCP tools advertised two different
  descriptors depending on which profile a session bound. `callers`, `callees`,
  `references`, `definition`, `neighborhood` and `search` carried read-only
  annotations in the default profile and none in `-labs`. `explain_symbol`,
  `related_files`, `change_risk` and `agent_brief` carried a different description
  *and* a different input schema in each — `-labs` sessions were never told about
  `explain_symbol`'s, `related_files`' or `change_risk`'s `limit` argument, though
  the server accepted it. Only `impact` agreed. SW-223 found this and SW-225
  deliberately preserved it as a recorded fact rather than papering over it,
  because collapsing it touches a Stable-12 request schema.

  **What changed, and for whom.** The **maximal (`-labs`) profile adopted the
  default profile's advertisement**. A `-labs` session now receives the read-only
  annotations on those six structural tools and on `search`, and the `limit`
  argument on `explain_symbol`, `related_files` and `change_risk`. It no longer
  receives the longer six-facet prose those four descriptors carried; the terse
  form the default profile always served is now the only form. Sessions on the
  **default profile — the shipped `graphi mcp` binding — see no change at all**,
  which the untouched `stable`, `stdio-stable` and `daemon-stable` descriptor
  goldens prove rather than assert.

  **Why this direction.** The other direction (default adopts maximal) would have
  stripped read-only annotations and a documented, accepted argument from the
  profile every shipped client actually binds, to give four Labs descriptors
  longer prose. Losing prose in an opt-in profile is a smaller cost than removing
  a working argument from the default one.

## [0.10.0] - 2026-08-23

### Added

- **Incremental-indexing benchmark suite** (P4, roadmap TODO 19). The SW-010
  budget-gated benchmark harness gains seven metrics over the same frozen
  fixture (mutations happen only in a runtime copy — the pinned fixture
  digest is unchanged): `incremental_ten_file_ms` (4 modified + 6 created
  files absorbed via `IngestChanged` + proving query), `branch_switch_sim_ms`
  (checkout-shaped modify+add+delete delta), `mcp_startup_ms` (measured
  binary: `mcp -db` spawn → first `initialize` response; skipped and omitted
  for external binaries, never a fake zero), `symbol_lookup_ms` /
  `callers_query_ms` / `context_query_ms` (named query latencies on the hot
  store, medians), and `index_heap_alloc_bytes` (post-GC heap after a full
  index). Index-bound metrics gate at fail severity, the spawn-/sub-ms-/
  GC-sensitive ones start at warn. Every published number is CI-produced via
  the `bench-report.json` artifact — no hand-written figures in docs.

- **[labs] `framework_map` — framework intelligence from recorded
  annotations** (P3). The application-level view on top of the code graph:
  HTTP routes (`@GetMapping`, NestJS `@Get`, `[HttpGet]`), event handlers
  (`@EventListener`, `@KafkaListener`, `@EventPattern`, `@Scheduled`),
  injection points (`@Autowired`, `@Inject`), DI-managed components
  (`@RestController`, `@Service`, `@Injectable`, Angular `@Component`), and
  configuration units (`@Configuration`, `@Bean`, `@Module`) — derived
  purely from the annotation/decorator names the parsers already record,
  via deterministic provider tables: spring (Java/Kotlin), nest
  (TypeScript/JavaScript), dotnet (C#). Every fact cites its annotation and
  definition site; graphs without annotation metadata (Go, Python sources)
  return an honest typed empty outcome naming the provider scope. CLI
  `graphi framework-map`, MCP `framework_map` (labs), HTTP
  `/analyze/framework_map`.

- **[labs] `dead_code` — precise dead-code candidates** (P2). The agent-facing
  view of the EP-015 `dead_symbol` diagnostic: symbols with zero live inbound
  references (calls/references/implements/inherits/overrides), each scored by
  a pinned integer signal model — exported API and dynamic-dispatch methods
  score lower, penalties quoted in every reason — and the exclusions made
  VISIBLE with their reasons instead of silently dropped: framework/language
  entry points (annotations, main, test paths, overrides, decorators), test
  fixtures, generated/vendored paths, and exported API without usage
  evidence. A candidate-free graph returns an explicit cited "clean" item.
  Far better than "references == 0". CLI `graphi dead-code`, MCP `dead_code`
  (labs), HTTP `/analyze/dead_code`.

- **[labs] `architecture` + `architecture_violations` — architecture
  intelligence** (P2). `architecture` is the automatic community/layer view of
  the code graph: deterministic Louvain communities (the SW-103 detector
  semantics over the symbol-only projection) labeled by their dominant package
  prefix, layered by dependency DIRECTION (edge majority between communities;
  foundation = layer 1), with per-community depends-on/used-by neighbor lists
  and the strongest inter-community dependencies. `architecture_violations`
  detects cycles (with edge counts along the loop), unexpected dependencies
  (edges against the dominant direction), high-coupling pairs (≥3 edges both
  ways), and god modules (≥50% of inter-community edges while touching ≥60% of
  communities) — pinned integer thresholds quoted in every finding, and an
  explicit cited "clean" item when nothing fires. No LLM classification
  anywhere. CLI `graphi architecture` / `graphi architecture-violations`, MCP
  `architecture` / `architecture_violations` (labs), HTTP
  `/analyze/architecture` / `/analyze/architecture_violations`.

- **[labs] `search_hybrid` — embedding-free hybrid search** (P3 repository
  search). Multi-token queries ranked by identifier-segment matching
  (camelCase/snake_case aware), path relevance, and bounded graph degree —
  deterministic integer weights, hash-stamped, per-signal breakdown in every
  reason. "authentication token validation" ranks `TokenValidator` ahead of
  accidental substring hits; no vector database, no model, no egress. CLI
  `graphi search-hybrid`, MCP `search_hybrid` (labs), HTTP
  `/analyze/search_hybrid`. The optional semantic search (`search_semantic`,
  the roadmap's "SemanticProvider" — embedder opt-in via `GRAPHI_EMBEDDER`,
  default none) is untouched and remains the separate, explicit opt-in.

- **[labs] `hotspots` — churn × dependency-centrality file ranking** (P2 git
  intelligence). One call ranks the files that change constantly AND that the
  graph depends on — `commits-in-window × (1 + edge endpoints)`, integer
  breakdown in every row — with single-author bus-factor warnings for the
  ranked hotspots and a concrete next call. CLI `graphi hotspots
  [-max-commits n]`, MCP `hotspots` (labs catalog), HTTP `/analyze/hotspots`.
  Without git history the tool returns a typed unavailable outcome.
- **[labs] `change_impact` gained a co-change section** (P2 git
  intelligence): files that historically change together with the changed
  files but are NOT in this change — "`B` usually changes with `A`
  (N co-commits) — not in this change" — as cited items plus a reason line.
  Absent without a git provider, so provider-less output is unchanged.
- **`surfaces/gitlog` — the production git-history provider.** The
  `githistory.GitProvider` seam existed since SW-032 but was only ever wired
  with nil; every history consumer silently returned empty. The new provider
  reads bounded local history (`git log --name-only`, max-commits/max-age
  window) at the surface boundary — the engine stays exec-free — and the
  session composition root now injects it into the analysis service
  (**`analyze_githistory`, `suggest_reviewers`, and `analyze_pr_signals`
  produce real churn/co-change/bus-factor/reviewer signals for the first
  time**) and into the labs agent-intelligence tools. Attach mode (`-db`)
  stays provider-less and degrades gracefully as before.

- **[labs] `test_impact` — which tests must run for a change** (P1 test
  intelligence). Diff or target in, four evidence-cited buckets out:
  `must_run` (a direct call edge at confirmed/derived tier proves the test
  exercises a changed symbol), `recommended` (transitive reach, naming
  conventions, same-directory test files), `probably_unaffected` (the
  remaining test-file universe, counted in full), and `unknown` (diff paths
  with no graph symbols — never guessed). A change with no test signal at all
  is itself a cited finding ("treat it as untested"). The symbol↔test mapping
  is DERIVED on demand by the new `engine/testintel` leaf — bounded reverse
  walks plus exact SourcePath listings, no materialized edges, so the frozen
  `index` output and the parity gates keep their bytes. CLI
  `graphi test-impact (<target> | -diff <file|->)` — pipe
  `git diff <range>` in for range selection; the product stays exec-free.
- **[labs] `change_impact` — the Change Risk 2.0 assessment** (P1 change
  intelligence). One call returns the changed symbols, their public-API
  subset, direct dependents with edge evidence, the bounded transitive
  closure, the tests covering the change, configuration files riding the
  diff, explicit machine-checkable reasons ("public interface changed",
  "no test directly covers X", dependent counts), and a risk level —
  `change_risk`'s single-source thresholds plus a one-step escalation when
  exported surface changed. The confidence distribution is derived from the
  consumed edge tiers, never invented. The frozen-stable `change_risk`
  operation keeps its bytes: this is a separate labs operation. CLI
  `graphi change-impact (<target> | -diff <file|->)`.
- **[labs] `symbol_context` — the unified single-call symbol view** (P0 agent
  intelligence). One call returns the definition site with an optional
  token-budgeted source snippet, the type-hierarchy relations, callers,
  callees, references, the test files that exercise the symbol (bounded
  reverse walk: per-node edge limit, global node budget, depth clamp — never
  a whole-graph scan), and a `change_risk`-consistent risk level, every claim
  cited. CLI `graphi symbol-context`, MCP `symbol_context` (Labs catalog,
  `graphi mcp -labs`), HTTP `/analyze/symbol_context` (403 without
  `GRAPHI_HTTP_LABS=1`). Byte-parity across surfaces through the single
  `engine/agenttools/symbolcontext` assembly.
- **[labs] `task_context` — free-text task → ranked context bundle** (P0
  agent intelligence). One call turns a task phrase into primary seeds,
  related symbols, callers/callees, nearby tests and configuration files, a
  related-file roll-up, a risk level, a recommended read order, and source
  snippets under a hard token budget. Ranking is a fixed **integer** weight
  model whose sha256 hash is stamped into every summary — deterministic by
  construction, no floats, no LLM estimates. Multi-word tasks fall back to a
  deterministic token search instead of stalling on the quoted FTS
  expression. CLI `graphi task-context`, MCP `task_context`, HTTP
  `/analyze/task_context`.
- **[labs] `repo_overview` — the one-call repository summary** (P0 agent
  intelligence). Totals with edge-confidence tiers, a directory tree ranked
  by symbol count, the language mix, entry-point candidates (each reason
  names its signal), the highest-centrality symbols, test and
  generated/vendored areas, external boundaries, and concrete suggested next
  calls. The default call reads only the compact `BriefStats`/`TrustStats`
  aggregates; `-communities` is the documented opt-in full-graph pass. CLI
  `graphi repo-overview`, MCP `repo_overview`, HTTP `/analyze/repo_overview`.
- **`contract.Evidence.Snippet`** — an additive, `omitempty` field carrying
  token-budgeted source text whose citation is the evidence's
  `path`/`line`(+`span`). The frozen stable operations never set it; the
  characterization suites pin that their serialized bytes are unchanged.
- **`engine/classify`** — the shared path-classification leaf (test,
  generated/vendored, config, language-by-extension), unifying the previously
  duplicated heuristics in triage, safe-delete and diagnostic suppression.
  `change_risk`'s private test-path predicate is deliberately not migrated
  (stable-output freeze); unification there is a recorded follow-up.
- The previously unwired `engine/context` token-budget assembler gained its
  first production consumers (snippets in `symbol_context`/`task_context`),
  plus an exported `Reader` seam, a repo-rooted reader, and `FilterReadable`.

- **`graphi mcp -root <repo>` and the `GRAPHI_ROOT` environment variable** (the
  flag wins) pin the MCP session's repository root explicitly — for MCP clients
  that launch the server outside the repository (cwd=`$HOME` is common) and
  supply no roots, where previously every tool call failed with the `-32002`
  auto-bind refusal. An explicit pin is deliberate intent in the CLI `-root`
  sense: no detection walk, no home-directory guard, precedence over
  client-supplied roots and the process cwd (only `-db`/`-daemon` rank higher;
  combining the flag with either is a usage error). The pinned path must be an
  existing directory, else the bind fails closed with the actionable error.
- **`graphi setup --project [--root <repo>] [--attach]`** automates that pin
  per repository: it upserts graphi into the project-scoped `.mcp.json` at the
  repo root with `mcp -root <abs root>` in the entry — the per-repo follow-up
  the global-config-only setup contract deliberately deferred. `--attach`
  writes the store pin instead (`mcp -db <store> -meta <sidecar>`), with the
  paths derived from the same per-repo state layout the flagless verbs use —
  no more hand-copying fingerprint paths; the report reminds you Attach mode
  does not auto-ingest (pair with `graphi sync`) and warns when the store does
  not exist yet. Same guarantees as the client path (idempotent upsert, atomic
  write, fail-closed backup, offline); the file carries absolute paths and is
  machine-specific, so gitignore it or run the command once per clone.

- **JVM declared-type resolution for Java and Kotlin — shipped, but DEFAULT-OFF**
  ([ADR 0008](docs/adr/0008-jvm-declared-type-resolution.md)). New
  `engine/jvmresolve` builds a declaration table, resolves written type names
  under a JLS-scoping approximation with a **strict ambiguity rule** (where a
  scope step yields more than one candidate the walk stops and nothing is
  ranked — ambiguity and externality cost recall, never soundness),
  propagates declared-type receivers through Java and Kotlin call sites, and
  emits `calls` / `references` at the **confirmed** tier. New `engine/semantic`
  owns the one product-wide resolver registry `engine/ingest` dispatches its
  third phase over, so the capability a binary *claims* and the passes it
  *runs* cannot disagree.

  **It does nothing unless you ask for it.** The Java and Kotlin registrants
  are gated behind `GRAPHI_JVM_TYPERESOLVE` (any value other than unset or
  `0`). With the variable unset the registry holds exactly the `go/types`
  resolver and, in the package's own words, "every shipped byte — graph,
  snapshot, trust report, capability matrix — is unchanged"
  (`engine/semantic/semantic.go`). **Java and Kotlin remain Preview.** The
  binder is reachable so the language-GA program can *measure* it, not because
  it is ready; flipping the default is a separate, deliberate change.

  Four wrong-confirmed-edge defects were found by the bytecode ground-truth
  harness and closed **before the binder's first release** — no released
  binary ever contained them ([ADR 0013](docs/decisions/0013-jvmsound-003-004-jvmharn-001-closure.md)).
  Two are worth naming: `JVMSOUND-003`, where a comment node inside a Java
  argument list was counted as an argument, so `r.apply(1 /* the scale */)`
  reported arity 2 to the binder while `javac` compiled arity 1 and a wrong
  most-derived overload edge was emitted at every same-arity overload pair
  beside such a call; and `JVMSOUND-004`, where the callable signature key
  erased array dimensionality, so `apply(Thing)` and `apply(Thing[])` produced
  an identical signature, an overload set was misread as an override pair, and
  the most-derived member won where `AmbiguousMember` was required.

- **The GA *language* axis is machine-encoded — and only Go is on it.**
  A `category: ga-language` row in `docs/coverage-matrix.yaml` declares a
  language GA at a stated capability level, and
  `internal/coverage.CheckGALanguages` binds every such row both to the live
  capability derivation `graphi trust-report` serves and to green
  `GA-LANG-<lang>-*` rows in the evidence index — so a language can no longer
  be flipped GA by a prose edit. v0.9.0 carried **no** `ga-language` rows at
  all; this release carries exactly **one**: `go`, at `typed-confirmed`.
  Candidate rows for the other **21** shipped languages (`java`, `kotlin`,
  `python`, `typescript`, `tsx`, `javascript`, `bash`, `c`, `c_sharp`, `cpp`,
  `lua`, `php`, `ruby`, `rust`, `sql`, `css`, `hcl`, `json`, `markdown`,
  `toml`, `yaml`) were **withdrawn on 2026-08-21** because their `GA-LANG-*`
  evidence rows are not all PASS; each withdrawn row names the story that may
  re-introduce it. **Every non-Go language therefore remains Preview**, which
  is what the tier note at the top of this file says and what the matrix now
  enforces.

- **`graphi doctor` attributes an MCP finding to the client it came from.**
  The aggregate `mcp` check already built a per-client line for every detected
  client, sorted it, and then threw it away — the emitted result said only
  *"one or more MCP clients need attention"* while the process demonstrably
  knew which client was unhealthy and why. Those lines, plus the per-client
  contention lines, now populate `CheckResult.Detail` (an existing optional
  member of the schema): `RenderHuman` indents them beneath the check line,
  and the JSON renderer emits `detail` with `omitempty`, so it is absent when
  every client passes. Purely additive — check ids, categories, status
  derivation, messages, action strings and `JSONSchemaVersion` 2 are all
  unchanged, and checks without a detail keep their single-line format.

- **The freshness/incremental evaluation harness is language-scoped**
  (`EVALFRESH-001` / `EVALBUDGET-001`). This is `cmd/eval`, a repo-internal
  harness — a release builds only `./cmd/graphi` — but what it measures is
  graphi's own incremental indexing, so the numbers below are the point of the
  entry. The harness was Go-only in two places at once: its package-clause
  reader understood only Go's `package <ident>`, and its directory gate
  dropped any directory whose files carried no Go-shaped clause. New
  `cmd/eval/sourcefamily.go` replaces both with **15 language families** (`go`,
  `java`, `kotlin`, `python`, `typescript`, `tsx`, `javascript`, `ruby`,
  `rust`, `c`, `cpp`, `csharp`, `php`, `lua`, `bash`), each declaring its own
  clause shape — *or that it has none*, which is why Python and the TypeScript
  family are now admissible instead of silently dropped.
  `TestSourceFamilies_ComplementIsCompleteAgainstTheShippedRegistry` walks
  every extension the shipped parser registry registers and forces each one to
  be either a declared mutation family or a declared data language, so the
  filter cannot drift from the registry again. `historical_ceilings` in
  `docs/eval/hero-budgets.json` had been **inert** — no Go code read it — and
  is now load-bearing for `-runner-class local-sandbox` runs.

  **Measured on darwin/arm64, comparison class, `-incremental-changes 100`.**
  Every pin now exits 0; every pin except the cobra control previously aborted
  with *"the index contains no modifiable Go source files to change"*, and
  `sinatra` had never been measured at all:

  | pin | language | completed | cross-package classes |
  |---|---|---|---|
  | cobra (Go control) | go | 100/100 | yes |
  | guava | java | 99/100 | no |
  | okio | kotlin | 73/100 | no |
  | kotlinx.serialization | kotlin | 97/100 | no |
  | flask | python | 100/100 | **yes** |
  | ky | typescript | 98/100 | no |
  | express | javascript | 100/100 | no |
  | sinatra | ruby | 99/100 | **yes** |

  **Read these as what they are:** comparison-class figures on **Preview**
  languages, on one platform, not a reference-class measurement and not a
  support statement. Only `flask` and `express` clear the 100-change floor.
  The residual failures are parser-coverage gaps **in graphi itself**, each
  reduced to a three-line repro: the Kotlin extractor stops at the first
  top-level `suspend fun` (24 of okio's 27 failures land on the one file that
  is also that pin's only cross-package target), the TypeScript extractor
  yields no symbols for a file containing a type-predicate arrow function
  (`export const isObject = (value: unknown): value is object => …`), and the
  Java extractor yields none for guava-testlib's 583-line `Helpers.java`.

  The same change closed an **undisclosed regression on `main`**: the cobra Go
  control — whose entire job is to prove that a non-Go abort is language scope
  and not a broken harness — had fallen to **95/100**, below the same floor,
  because an earlier filter admitted `.md` files as mutation candidates. Five
  markdown "changes" could never become answerable by a search, so all five
  failed. Markdown has no top-level declaration to append, so the family
  narrowing excludes it and restores the control to 100/100 with a candidate
  list and 100-step sequence digest byte-identical to the pre-regression run.

### Changed

- **The Go toolchain floor is now go1.26.6** (`go.mod`), raised for five stdlib
  CVEs. It binds anyone building graphi from source; the published CGo-free
  binaries are already built at or above it.

- **Upgrading from 0.9.0 costs one cold re-index.** The graph-semantics stamp
  `ingestSemanticsVersion` moved from `11` to `12`
  (`engine/ingest/warmstart.go`), so a 0.10.0 binary will not warm-start a
  store an older binary wrote — it re-indexes cold once, and the stamp's own
  note describes that as forcing "one cold re-index via `graphi rebuild`".
  This is convenient rather than incidental: this release changes linker rules
  three times ([ADR 0009](docs/adr/0009-go-module-import-resolution.md),
  [ADR 0010](docs/adr/0010-relink-unit-invariant.md),
  [ADR 0011](docs/adr/0011-imports-edge-targets-package-source-files.md)), and
  as the LINK-001 entry below explains, `graphi sync` alone never heals a
  linker-rule change — drift is content-hash based, so where no source file
  changed it reports "up to date" and the old edges survive.

### Removed

- **The `tui` terminal surface (Labs) is gone** — `graphi tui`, `surfaces/tui/`,
  the `tui` build tag, `docs/surfaces-tui.md` and the `bubbles` / `bubbletea` /
  `lipgloss` / `x/exp/teatest` dependencies. **No released binary ever contained
  it:** every published `graphi` build compiled the no-op stub, which printed
  *"this build was compiled without the TUI surface"* and exited 2, so nothing
  a user could have installed loses a working feature. Reaching the real
  implementation required building from source with `-tags tui` and then
  pointing it at a separately started `graphi http`. `graphi tui` now gets the
  standard unknown-subcommand response. The documentation that described it was
  wrong in the specifics anyway (it advertised `-db`/`-daemon` flags the code
  never had, and denied the Bubble Tea dependency tree the code required), and
  its two `coverage-matrix.yaml` rows both claimed `status: shipped`.

### Fixed

- **An `imports` edge now targets the imported package's SOURCE files**
  (LINK-001, [ADR 0011](docs/adr/0011-imports-edge-targets-package-source-files.md)).
  It used to target **every file in the resolved directory** — `README.md`,
  `.golangci.yml`, `.sh` and `_test.go` included. Measured on pinned clones:
  44 of 340 `imports` edges on cobra pointed at `.md`/`.yml` plus 126 at
  `_test.go`; 2 120 of 23 575 on grpc-go pointed at `.md`/`.sh`. Membership is
  now decided on the **file extension**, by the asking resolver: Go takes `.go`
  minus `_test.go` (a test file is a package member but is not *importable* —
  the ruling `engine/typeresolve` already made), Python takes `.py` **and**
  `.ipynb` and keeps `test_*.py` (Python test modules are importable), C# `.cs`,
  Rust `.rs`. Java, Kotlin, the TypeScript family, C, C++, Ruby, PHP, Lua, Bash
  and SQL are structurally unaffected and their graph bytes are unchanged (SQL
  emits no `imports` edge at all).

  **BEHAVIOUR CHANGE, three surfaces, stated rather than left to be discovered:**

  - `related_files` and `imports` gain precision and narrow. The published
    pre-fix measurement on pinned cobra is
    `related-files -max-files 12 doc/man_docs.go` → 12 items, 5 genuinely
    related, **four of the seven additions not Go source**
    ([the record](docs/rc/parity-matrix-real-repo.md)). The post-fix number on
    cobra has **not** been re-measured yet, so no "after" figure is claimed
    here.
  - **A `.md`/`.yml` file used as a `related_files` ANCHOR now answers empty.**
    `related_files` accepts a repo-relative path, a Markdown file mints only
    intra-file symbols, and same-file neighbours are skipped — so the inbound
    `imports` edge was that file's *only* cross-file path. `graphi related-files
    README.md` therefore returns an explicit empty outcome (with its reason)
    where it used to list the importing packages. The edge it lost was wrong,
    but the operation-level effect is real and is pinned by
    `TestRelatedFiles_MarkdownAnchorLosesItsOnlyPath`.
  - **`neighborhood` changes shape at depth ≥ 2.** `query.Neighborhood`
    (`engine/query/service.go`) walks all edge kinds and excludes only
    `package`/`external` nodes, so it reaches `.md`/`.yml` file nodes through an
    `imports` edge today and stops doing so. Whether `file` nodes belong in
    neighborhood traversal at all is a separate product question, not decided
    here.
  - **Everything degree-ranked shifts**, because the removed edges were carrying
    degree: `agent_brief` and hybrid search, `hotspots` (its score multiplies
    commit count by edge endpoints), `change_risk` fan-in/fan-out,
    `explain_symbol` and `change_impact` at depth 1, `task_context`, and the
    hub/bridge centrality in `engine/analysis` that feeds PR signals.

  **UPGRADING AN EXISTING INDEX REQUIRES `graphi rebuild`.** Verified on a real
  fixture with both binaries: an index built by the previous release keeps its
  wrong `imports` edges, and `graphi sync` does **not** remove them — drift is
  content-hash based, the source files did not change, so nothing is re-linked
  and `sync` correctly reports "up to date". `graphi rebuild` applies the fix.
  (One manual before/after run on that fixture, **not** a gated measurement:
  `related_files` on the importer went 4 items → 1, confirmed-tier share
  0.2 → 0.5.) This is the standing
  property of an incremental index whenever a *linker rule* changes rather than
  a source file; it is stated here because "upgrade the binary and the graph is
  fixed" would otherwise be a reasonable and wrong assumption.

  **Two honest losses**, accepted rather than worked around and measured with
  both binaries over a mixed-extension package directory: (1) a non-Go build
  input **in the package directory** — a `.sql`, `.md`, `.yml`/`.yaml`, `.toml`,
  `.css` or `.sh`, embedded via `//go:embed` or merely co-located — and (2) a
  cgo package's `.c`/`.h` sources, which the C parser does commit as file nodes.
  Both lose their only graph path — graphi models no embed, codegen or cgo
  relation, and building one is its own epic. The user-visible face of loss (1)
  is the `related_files` **anchor** break described above.
  **Not losses, contrary to an earlier draft of this entry:** a `.proto` or a
  `.tmpl` (no registered parser), a `.json` (parser is parse-only and mints no
  nodes) and anything under `testdata/` (a different directory from the resolved
  package directory) were never `imports` targets, so nothing about them
  changes. LINK-001's disclosure (readme "Known limits", the doctor
  `known-defects` check and its test) is retracted in this same change, per the
  disclosure contract.

- **`graphi mcp -db <path>` now honors `-meta <dir>`** the way every CLI verb
  does, so an attached MCP session reloads the durable semantic vectors from
  the sidecar. Previously `-meta` was extracted and silently dropped — the
  session started, but `search -semantic` had no vectors and nothing said so.
  A `-meta` without `-db` is now a usage error instead of that silent no-op
  (the zero-config session auto-manages its per-repo sidecar, and a daemon
  session never reads one).

- **The default `balanced` profile was dropping TRUE `imports` edges**
  (PARITY-003, [ADR 0010](docs/adr/0010-relink-unit-invariant.md)). A
  pass-scoped import aggregation collapsed the importers of one target onto a
  single representative, so a file that really did import a package could end
  up with **no `imports` edge at all**, while the surviving edge carried a
  *different* importer's `file:line` evidence. That is a recall defect in GA
  operations (`related_files`, `imports`), not a size optimisation, and
  `balanced` is what every `graphi index` / `sync` / `rebuild` resolves to. It
  survived two releases because `ingest.New` left the profile at its zero
  value while the CLI resolved `balanced`, so the parity table never executed
  the configuration the product ships. The aggregation is **removed, not
  repaired**: `balanced` now behaves like `deep` for `imports` edges (`fast`
  still drops them), and the synthetic `aggregated N imports of …` edges are
  gone. Measured at the ADR 0010 candidate on `Linux-X64/ccr-container`,
  go1.26.6 linux/amd64, the shipped default had been keeping roughly **40 of
  340** `imports` edges on pinned cobra, **99 of 291** on gin and **670 of
  23 575** on grpc-go; the real-repo parity matrix went to **19/19 PASS** over
  two agreeing dispatches. Those totals are pre-LINK-001 and were changed
  again by ADR 0011 below, so read them as the size of the recall loss, not as
  today's edge counts.

- **A Go `import` now resolves to the one directory its module path names**
  (PARITY-002, [ADR 0009](docs/adr/0009-go-module-import-resolution.md)).
  Resolution used to key on the package *clause* — `path.Base(importPath)` —
  and union **every directory in the repository declaring that clause**, so an
  importer of `x/json` also got `imports` edges fanning out into an unrelated
  `y/json`. Worse than the wrong edges, the divergence was **frozen**: the
  incremental cascade is directory-local and nothing imported `y/json`, so
  those edges were never re-emitted and no amount of re-syncing healed the gap
  between `sync` and `rebuild`. Imports now resolve through a per-pass map of
  every `go.mod` in the tree — longest matching module path wins, matched on
  segment boundaries rather than raw prefixes, nested modules excluding their
  own subtree, fail-closed to external for stdlib and third-party — and a
  `go.mod` change at **any** depth now triggers the full re-link where the old
  predicate matched only the root. **Go emission only:** Python, Ruby and the
  JS families keep their clause fan-out and Java/Kotlin keep the single
  interned file→`package` edge; non-Go emission never consults the module map.

  Two accepted costs, stated rather than left to be discovered. An import
  naming a module path not present in the tree — the state a half-finished
  module rename leaves behind — now resolves as **external** (interned node,
  heuristic edge) instead of being clause-guessed back onto an intra-repo
  directory. And because any `go.mod` change pulls every linkable file into
  the re-link set, a pre-existing escalation makes the incremental pass
  **hard-fail if any file in that set cannot be parsed**; that was previously
  reachable only from the root `go.mod`.

- **Deleting a file no longer diverges `sync` from `rebuild` permanently**
  (PARITY-001). The deleted-path purge is now committed before `linkFiles`.
  Deleting a file that declared a symbol another package called through an
  intra-module import used to leave the two paths permanently apart — the full
  pass minted the interned external node and its edge, one incremental apply
  minted neither, and a converged drift set never revisited it. On pinned
  cobra, `delete_file` read incremental 895/3784 against full 897/3789 and now
  reads **897/3789 on both sides**. Atomicity changed and is disclosed: purge
  and re-link are no longer one batch, so a crash between the graph purge
  commit and the meta transaction leaves the file in `file_content_cache`,
  healed by the next drift pass re-purging.

  **Still open, and disclosed rather than closed:** `PARITY-004` — rebuild
  while an intra-module import points at a missing package, then restore the
  package and `sync`, and the importer is never re-linked; a stale `external`
  node and a false heuristic `calls` edge survive beside the confirmed edge,
  and the `imports` edge a rebuild emits is missing. Reproduced through the
  CLI (`sync` settles at 7 nodes where `rebuild` gives 6, unrepaired by three
  further syncs) and carried in the readme's known limits.

## [0.9.0] - 2026-08-05

**The P1 trust surface reconciled against PRD v1.0.** 0.8.0 shipped that surface
built to the July P1 PRD; a second PRD registered on 2026-08-05 fixes three of its
wire values differently, and this release follows it. The breaking changes below
are confined to **Labs** — `graphi trust-report`, `graphi query-strict` and the
`graph_health` MCP tool. **Stable-12 is untouched:** the twelve frozen operations,
their schemas, and the eleven-tool default MCP profile are byte-for-byte what
0.8.0 served.

Read the breaking section first if you script against any Labs trust surface;
one of the three changes (the exit-code collapse) is deliberate and lossy, and
says so.

### Changed — BREAKING (labs)

The P1 trust surface shipped in 0.8.0 against the July P1 PRD. A second PRD —
"Graphi P1: Trust & Coverage Intelligence" v1.0, registered as
`docs/plan/2026-08-graphi-p1-prd-v1.md` — fixes three wire values differently,
and the owner decided it wins where the two contradict. The reconciliation,
including what deliberately did *not* change, is
`docs/plan/2026-08-graphi-p1-prd-v1-delta.md`; the trust contract is amended to
v1.1 accordingly.

All three changes are confined to **Labs** surfaces (`graphi trust-report`,
`graphi query-strict`, the `graph_health` MCP tool). Stable-12 is untouched.

- **The fourth verdict is `UNVERIFIED`, was `UNKNOWN`.** The new name is the
  precise one: it marks evidence that is missing *or not generation-bound* —
  not verifiable, rather than merely unknown. Affects `policy.verdict` in the
  trust-report document and the `graph_health` output.
- **Policies are selected by their canonical versioned identifier:
  `exploratory-v1`, `review-v1`, `automated-change-v1`** — was `exploratory`,
  `review`, `automated_change`. The bare names are **rejected**, not accepted
  alongside; accepting both would leave the superseded contract silently alive.
  Affects `--policy` on `trust-report`, `-policy` on `query-strict`, and the
  `graph_health` input schema. The wire `policy` object gains an additive `id`
  field carrying that identifier; `name` and `version` remain as its
  decomposition, and `id` is always derived from them so the three cannot
  disagree.
- **`graphi trust-report` exit codes are 0/1/2, were 0/1/2/3/4.** 0 = PASS, 1 =
  WARN, 2 = everything else: FAIL, UNVERIFIED, a non-current snapshot when no
  policy was given, and usage or operational errors. Missing evidence still
  never exits 0. Note the cost, recorded rather than glossed: FAIL and a
  mistyped flag now share code 2. Scripts that need to tell them apart must read
  the document — an error writes nothing to stdout, whereas FAIL and UNVERIFIED
  always emit the canonical document with its `policy.verdict`.

### Added (labs)

- **Package assessment.** A package target now resolves and is judged, instead
  of reading `TARGET_NOT_FOUND` and abstaining. `graphi trust-report --target
  util` (and `graph_health` with the same target) answers with the package's own
  persisted evidence row — checked / checked-with-errors / degraded, its
  confirmed-edge count and its skipped-file count — graded by the rules that
  already existed for package state.

  Resolution is confirmation-only and fail-closed: a key the evidence does not
  know, or a sidecar that cannot be read, leaves the target unresolved exactly
  as before. Two consequences worth knowing: the old "contains a slash" guess
  for package-looking targets is gone, so top-level packages and the repository
  root key `.` resolve too; and a package whose files were skipped during
  parsing now reports `PARSE_SKIPPED_IN_SCOPE` from its own row rather than
  reading clean.

- **`strict_query` MCP tool** — the strict-query wrapper reaches MCP agents,
  which is where PRD v1.0 aims it: `graphi query-strict` shipped in 0.8.0 as a
  CLI verb only, so the primary persona could not use it. Runs one of the Stable
  structural queries unchanged, then withholds result edges below
  `minimum_tier` and reports the withheld count. A result emptied by the filter
  always carries an explicit limitation — filtered emptiness never reads as
  proven emptiness. Optional fail-closed `policy` preflight blocks before the
  query runs. Labs-only; the CLI verb keeps its `query-strict` spelling.
- **Per-language capability matrix** in the trust-report document (field
  `capabilities`, additive under `schema_version: 1`): each language graded
  `typed-confirmed`, `cross-file-heuristic`, `intra-file-only` or `parse-only`.
  Derived at read time from the live type-checker, resolver and parser
  registries rather than a maintained table, and drift-tested against them.

### Changed

- The CLI and MCP halves of both `query-strict`/`strict_query` and the trust
  report now share one composition each (`surfaces/client/`), so the two
  surfaces are byte-identical by construction rather than by review.

### Fixed

- **Emitted paths are now length-bounded (`trust.MaxPathLength`, 240 bytes).**
  Snapshot sample lists were capped in count but each path was emitted verbatim,
  so a repository could push arbitrarily long attacker-chosen text into the
  trust snapshot — against the PRD's "nutzerkontrollierte Pfade …
  längenbegrenzt" rule and against the ≤ 1 MB snapshot budget. Found by the new
  privacy fixture. A truncated path carries a visible marker so it cannot be
  mistaken for a real one.

## [0.8.0] - 2026-08-05

### Added (labs)

- **P1 trust surface** — a persisted, generation-bound trust snapshot answers
  "how far may I trust this graph answer for the planned action?", fail-closed:
  missing, stale or corrupt evidence reads `UNAVAILABLE`/`STALE`/`INCOMPLETE`,
  never healthy.
  - `graphi trust-report` (labs): snapshot state, confidence-tier counts,
    coverage gaps, external boundaries, per-target scope evidence, and an
    optional policy verdict (`exploratory` / `review` / `automated_change` —
    versioned static rules, sealed by a 60+-case matrix; a PASS always carries
    its explicit checks-passed list). Exit codes 0 PASS/current, 1 WARN,
    2 error, 3 FAIL, 4 UNKNOWN/unavailable.
  - `graph_health` (labs MCP tool): the same canonical document, byte-identical
    to `trust-report --json` through one shared composition.
  - `graphi query-strict` (labs): stable queries with edges below `-min-tier`
    excluded; the envelope carries the excluded count, filtered emptiness
    always carries an explicit limitation, and an optional `-policy` preflight
    blocks fail-closed before the query runs.
  - Per-file and per-package trust evidence persisted in the ingest sidecar
    (schema v3), generation-keyed with fail-closed selective read ports.
  - Governance: trust contract v1 (`docs/plan/2026-08-graphi-p1-trust-contract-v1.md`),
    ADR 0006 (status-vs-trust separation), and the start-before-P0-GO decision
    record. Adversarial reviews closed 17+ proven false-green/laundering holes,
    each pinned by a regression test.

### Documentation

- Trust surface documented across the user docs (readme, CLI reference,
  How-To §6.8, agent-workflows `graph_health` preflight); FEATURES.md tool
  counts corrected to the measured registry (44 maximal = 11 stable + 33 labs).
- `docs/plan/2026-08-graphi-p1-evidence-index.md` — the P1 evidence index
  (PRD §48 rows, GREEN only with a named artifact; evaluation rows honestly
  OPEN) — and a `trust-surface feedback` issue template inviting first-time
  users to answer the PRD §36.4 usability questions from real runs.

## [0.7.1] - 2026-07-29

**A measurement-code-only correction release.** The product tree is intended to be
byte-identical to v0.7.0: this release exists so the P0 measurement instrument can
produce the evidence PRD §12.2 gate 9 requires, not to change what graphi does. No
shipped operation, tier, threshold or wire identifier moves.

### Fixed

- **The agent-context latency pool no longer discards correct observations —
  `cmd/eval/querylatency.go` (SW-136).** The countability rule for
  `explain_symbol`, `change_risk` and `related_files` declared
  `allowed = ["found"]` (and `["found","empty"]` for `related_files`) while
  `agent_brief` — the fourth member of the **same** FR-8 pool behind the
  `agent_context_p95` gate — declared `["found","partial"]`. One percentile was
  therefore being reported over four operations whose observations had been
  admitted under two incompatible rules. All four now count `partial`.

  This is a correction, not a loosening. A `partial` **is** a resolved target with
  a valid envelope: the operation answered and truthfully declared its own
  truncation — designed, documented, GA-frozen behaviour that
  `corpus/hero/hero-17-explain-symbol-partial.yaml` asserts. The rejection rule's
  own justification, *"a fast wrong answer is not a fast answer"*, never applied to
  it. `not_found`, `ambiguous` and `error` remain uncountable, and no operation
  outside the pool is touched.

  The exclusion was also **systematic and biased toward the slow tail**: the
  answers large enough to truncate are the ones with the most items to resolve,
  rank and assemble, so the retained distribution was missing its heaviest members
  by construction rather than at random. It cost exactly **25 of FR-8's 1000**
  executions (16 `explain_symbol`, 5 `related_files`, 4 `change_risk`) in **both**
  published runs, on **two CPU families**, leaving `agent_context_p95` permanently
  `UNKNOWN` — a shortfall no number of re-runs at `5815db5` could have closed.
  Diagnosed in `docs/eval/p0/partial-outcome-diagnosis.md` (SW-134); the decision
  to move the candidate for it is `docs/decisions/2026-07-p0-candidate-decision.md`
  (SW-135, Outcome B).

  **This is not a performance improvement and not evidence about gate 9.** Nothing
  here is a measurement. The recovered executions were never timed into the
  published pool, and both this correction and the harness's item cap push the
  measured distribution **upward**, not downward. Gate 9 stays `UNKNOWN` until a
  fresh baseline runs against this release; anyone expecting a number near the old
  undersampled 471.250 ms is expecting the wrong thing.

  **Not fixed here, deliberately:** the harness resolves an omitted item cap to
  **10** while every shipped surface resolves it to `shape.DefaultMaxItems` = **20**
  (`engine/scenario/fixture.go`), so the instrument still times a configuration no
  default-configured user runs (F4 in the diagnosis). It is a real
  measurement-integrity defect, it is on `backlog.md`, and it is **not** what forced
  this candidate to move — SW-135 named exactly one forcing defect and this release
  corrects exactly that one, so any change in the next baseline has one possible
  cause instead of two.

### Changed

- **The P0 candidate moved to v0.7.0 at `5815db5` — the first candidate its own
  measurement harness can actually run against —
  `docs/decisions/2026-07-p0-candidate-freeze-v070.md` (new).** The previous candidate
  (**v0.6.7 at `fb3bf03`**, frozen one day earlier and announced under `[0.7.0]` below)
  was a properly published, tagged and attested release, and was still **unmeasurable by
  construction**. Two independent mechanical reasons, either of which alone is fatal: the
  P0 harness **does not exist at that SHA** (`git ls-tree fb3bf03 cmd/eval/` returns 11
  entries — `coldseries.go`, `querylatency.go`, `incremental.go`, `stalls.go` and
  `rawexport.go` are all absent), and the harness has **no external-binary path** (all 13
  `flag.String`s in `cmd/eval/main.go` accept no `graphi` executable; `fullrun.go` links
  the product packages in and times `ing.IngestAll` in-process), so it cannot be pointed
  at the candidate either — running it from `main` sets `candidateMatch = false`, which
  forces every §12.2 gate to `UNKNOWN` before a threshold is ever compared. A candidate no
  harness can measure defeats the purpose of freezing one, which is a documented blocker
  fix under the freeze record's §9 — *not* "`main` has moved on", which that rule
  explicitly rejects.

  The move is unusually safe, and the record says why without overclaiming: the **product
  tree is byte-identical** between the two candidates — `engine`, `core`, `surfaces`,
  `cmd/graphi`, `go.mod` and `go.sum` have the same git tree hashes at both SHAs, and the
  entire difference is measurement code, corpus data, docs and CI. The new candidate ships
  the *same product*; it differs only in carrying the instruments that can measure it.
  That is the argument for **soundness** and is explicitly **not** evidence about
  performance, accuracy or any gate — every §12.2 gate remains `UNKNOWN`.

  Nothing measured was invalidated, because nothing had been measured: at the time of the
  move no evidence row read `PASS` and none carried an `evidence_uri`. The rows that named
  the old candidate in prose (**WP2**, **M0**) stay `STALE` and now name what superseded
  them — they are re-marked, never silently re-pointed. `docs/decisions/2026-07-p0-candidate-freeze.md`
  is marked **superseded** rather than deleted, and its §9 change-control rule is inherited
  unchanged. All five published v0.7.0 binaries were rebuilt **bit-for-bit** from the
  frozen SHA on a different host OS, and all eight assets verify against the release
  workflow identity and source digest `5815db5` — with the superseded candidate as a
  negative control that correctly fails.

## [0.7.0] - 2026-07-28

### Added

- **Published performance numbers are now reproducible from the raw measurements — `-export-raw` and `-aggregate` (new).**
  The committed evidence runs held aggregates and nothing else: a `warm_p95_us`, a
  `peak_rss_mb`, a `db_size_bytes`. Nothing can be recomputed from a p95, so every
  published figure had to be taken on trust. A measurement run can now export a **run
  directory** — `docs/eval/runs/<date>-<runner-class>/`, the shape the historical runs
  already use — holding the individual measurements from all four performance harnesses
  beside the report they produced, and `-aggregate <dir>` recomputes **every** published
  statistic from those samples and diffs them.
- **The raw format carries samples and nothing derived.** The harnesses already retained
  their individual measurements, but inside the same structure as the percentiles derived
  from them — checking one against the other would have been comparing a number with a
  file that already contained it. The exported `raw/` files carry measurements plus the
  pool-membership lists a recomputation needs, and no percentile, aggregate or verdict.
- **A discrepancy is an error, not a rounding note.** The comparison is exact: every
  percentile in the tree is a nearest-rank *observed sample* rather than an interpolation,
  so two correct derivations over the same samples agree bit for bit, and a tolerance
  would only be somewhere for drift to hide. A report edited away from its samples — by
  hand, by a half-finished refactor, or by two runs' files landing in one directory —
  turns the job red at the moment it is produced.
- **The run environment is captured, and a gap in it reads as a gap.** CPU, RAM, OS,
  kernel, Go version, filesystem and observed page-cache state are recorded alongside the
  runner class, the frozen candidate SHA and the harness and scorer versions. A probe
  that fails leaves the field **absent** with the reason recorded, and it renders
  `UNKNOWN`: an empty `kernel` never reads as a documented kernel. A run whose environment
  is incomplete is not publishable however cleanly its arithmetic reproduces.
- **Missing raw data makes a metric UNKNOWN, and an unpublishable run says so in its exit
  code.** `-aggregate` exits `0` only when every published metric reproduced *and* the
  environment is documented; `1` on a discrepancy, `3` when the run is incomplete, `2`
  when the directory cannot be read. `3` is deliberately not `1` — "the number is wrong"
  and "the number cannot be checked" are different facts, and merging them would let a
  real discrepancy be triaged as a flaky job.
- **Raw format and measurement method are versioned separately.** `format_version` pins
  the file shape and `harness_version` the measurement method. A directory whose raw
  files disagree about the harness version is refused rather than warned about: an old and
  a new methodology are not one measurement, and averaging them silently is exactly the
  risk this versioning exists to remove.
- **Every reference-scenario CI job now exports and reproduces its own numbers.** The
  four `eval-full.yml` jobs that produce candidate evidence export their raw samples and
  immediately check that their report follows from them, `if: always()` — a run whose
  gates went red still has to be internally consistent, and that is when a contradiction
  between report and samples matters most. The exported directory is uploaded with the
  report, so an artifact never arrives without the data behind it.

- **Query latency is now measured at the contract's scale — `-query-executions` (new).**
  The warm half of `cmd/eval -full-run` reported a p95 per operation class over a
  sample size chosen by wall-clock pragmatism — around 30 executions for the
  structural class — while the measurement contract asks for **at least 1000
  executions per query class**. Nothing recorded whether that floor was met, so a p95
  over 30 executions and a p95 over 1000 landed in the same field and looked identical
  to every consumer. `-query-executions N` now plans enough timed executions that every
  query class *and* every individual performance gate's operation pool clears the
  floor: a class target alone would leave the caller/callee/impact gate reading a
  percentile over roughly half the executions it needs, however green the class looked.
- **p50 as well as p95, per class and per operation.** The performance gates are stated
  on both, and the report carried only the tail. `warm_p50_us` and `warm_p50_us_per_op`
  now sit beside the existing p95 maps (which are unchanged, so the budget artifact and
  the committed historical runs still read), and both come from the one nearest-rank
  implementation the cold series already uses — a p50 and a p95 that disagreed about
  even sample counts would show up as an unexplainable gate result rather than a test
  failure.
- **Undersampling is a visible state, not a silent one.** Every class and every gate
  pool publishes its execution count beside the floor it is read against. A pool below
  the floor makes its gates **UNKNOWN** — never PASS — even when the measured latency is
  comfortably inside the threshold, and the reason names the count it got and the floor
  it missed.
- **Every individual measurement is retained.** Each operation keeps its full list of
  per-execution latencies, and one exported function derives every published class,
  pool and operation statistic from nothing but those samples, so a number that
  disagrees with its own raw data is a test failure rather than a discrepancy nobody
  can see.
- **The symbol sample is deterministic and published verbatim.** Two runs over
  different symbol samples are not two runs of the same measurement, and the
  measurement contract asks for two consecutive green runs. The ordered sampled symbol
  ids now travel in the report with a digest over them, so a drift between two runs is
  one string comparison; a test indexes the same tree twice from scratch and requires
  an identical sample.
- **The operation → query-class mapping is stated, not inferred.** All twelve stable
  operations appear in the report with an explicit class, and `index` is declared
  **lifecycle-only** — it is the ingest lifecycle operation, its cost is the cold-index
  wallclock, and it carries no query-latency samples, no execution floor and no query
  gate. A drift test fails the build if the mapping and the frozen twelve diverge.
- **Timing covers the operation and nothing else.** Argument assembly and symbol
  selection moved into an untimed prepare step, each operation runs warmup executions
  that are invoked and discarded before the first timed one, and the warmup count is
  recorded per operation. A test inflates the setup cost and requires the reported
  latency to be unchanged.
- The weekly `eval-full` workflow gained a `query-latency-series` job that runs the
  full-scale measurement over the reference scenario. The PR path is untouched: the
  default invocation keeps the historical warm sample counts exactly, reports itself as
  below the floor rather than looking like a full-scale run, and a guard test keeps the
  new flag out of the PR gate and the per-repo compatibility runs.
- **Cold indexing is now measured ten times, not once — `-cold-runs` (new).**
  `cmd/eval -full-run` measured a single cold index and reported it as though one
  sample were a result. A distribution cannot be derived from one number, and the
  measurement contract asks for at least ten runs reported as **p50 and p95**.
  `-cold-runs N` repeats the existing measurement N times, **one process per run** —
  peak RSS is a process-lifetime figure, so repeating inside one process would have
  republished the first run's peak for every later run and called the result a
  distribution. Every individual sample is kept in the report next to the
  aggregates, and one exported function derives the aggregates from those samples,
  so every published number can be recomputed from the raw data rather than taken on
  trust. Wallclock, peak RSS, DB size, nodes, edges and bytes-per-edge are all
  captured per run and aggregated.
- **"Cold" is now produced and verified per run, not assumed.** Each run records
  whether its store and ingest metadata really were absent beforehand, and what state
  the page cache was actually in — with `-drop-caches` running the reference class's
  declared protocol between the clone and the timed index (dropping it *before* the
  clone would have warmed the cache with exactly the files about to be measured). A
  run on the reference class that did not reach its own declared protocol is recorded
  as **not verified cold** instead of being published as a cold number.
- **The 8 GB OOM gate has a measurement point for the first time — `-oom-check`.** It
  was previously neither passing nor failing: it was unmeasured, which means UNKNOWN.
  The run is now executed under the contract's imposed 8 GB cgroup v2 limit with swap
  disabled, the limit is **read back from inside the constrained process** and
  compared to the exact byte figure, and three failure signals (the cgroup `oom_kill`
  counter, a SIGKILL/137 exit, a kernel OOM record for the measured pid) are
  collected. A pass requires a verified limit, a completed run, and all three signals
  observed and absent — a limit that could not be verified, or a signal nobody
  managed to look at, reads **UNKNOWN**, never PASS.
- **Aborted runs stay visible.** A run that produced no cold-index measurement is
  counted, named and kept in the report; it never silently drops out of the
  distribution. A run whose *warm* checks failed still contributes its cold sample,
  with its own verdict attached — the cold measurement is valid regardless.
- **Runs are tied to the frozen candidate.** Every run carries the runner class and
  the revision that measured it, and the series cites the frozen candidate from the
  evidence index. A series measured on anything else — including a dirty worktree —
  is marked as such, and its gates read UNKNOWN: a gate result about an artifact
  nobody installs is not evidence about the candidate. That applies to **every**
  gate including the OOM one, which has a measurement method of its own but no
  exemption of its own — a clean constrained run measured off the reference
  scenario, off the candidate, or from a dirty tree reads UNKNOWN with the observed
  result named beside the reason, never PASS.
- Gates are read from the reference-scenario contract rather than restated, and the
  weekly `eval-full` workflow gained a `cold-index-series` job that runs the ten
  reference-scenario runs and the OOM check. The PR path is untouched: the default
  invocation is still exactly one run, and a guard test keeps the repetition flag out
  of the PR gate and the per-repo compatibility runs.
- **The reference scenario exists — `docs/eval/reference-scenario.json` (new).** Every
  performance number in the P0 measurement contract was scoped to "the defined
  reference scenario", and that scenario was defined nowhere: "cold index p50 ≤ 90 s"
  without a named repository and a named machine is a number, not a claim, and a gate
  that cannot fail cannot pass either. The contract now exists as data. **Exactly one**
  runner class is the reference — `ubuntu-latest`, the class every existing gate
  workflow already uses — documented with CPU, RAM, OS, kernel, Go version, filesystem
  and the *cold* cache protocol. The development machine is declared a **comparison**
  class and labelled as such: its numbers are never reported as reference values and
  never freeze a budget. Each of the ten performance gates is mapped **by name** to a
  repository from the v3 corpus (`grpc-go` v1.60.1, the largest non-stress entry —
  `kubernetes` stays the stress target under the program-wide 4 GB peak-RSS stop rule,
  not under the gates), the 8 GB-host OOM gate has a **method** (cgroup v2
  `MemoryMax=8G`, `MemorySwapMax=0`, the imposed limit read back and recorded, with
  `oom_kill`/137/kernel-log as the failure signal) instead of a statement of intent,
  and the scope limitation travels inline so a consumer reading only the JSON cannot
  publish the gates as universal guarantees. Loader, fail-closed validator and drift
  tests: `cmd/eval/refscenario.go`, `cmd/eval/refscenario_test.go`. A gate pointing at
  a repository that is not pinned in `corpus/manifest.json` is a test failure.
- **`go run ./cmd/eval -check-reference-scenario`** validates that contract against the
  corpus manifest and the budget artifact and prints the gate→repository map; `eval-full.yml`
  runs it before it measures anything. `cmd/eval -full-run` takes `-reference-scenario`,
  stamps each report with the runner class's declared **role** (`runner_role`,
  `reference_scenario`, `scenario_source` in `internal/evalreport.FullRunReport`), and
  **fails closed on a runner class the contract does not declare** — numbers from an
  unnamed machine no longer sit beside reference values with equal standing.

- **The P0 candidate is frozen on a real, published artifact —
  `docs/decisions/2026-07-p0-candidate-freeze.md` (new).** The previous candidate
  (`4e72637`, 2026-07-16) was 99 commits and 8 tags behind `main` and, as its own
  record honestly said, **nothing was ever published from it** — its release digest
  read `UNKNOWN`. Measuring it would have proven the quality of an artifact nobody
  installs. P0 is now frozen on **v0.6.7 at `fb3bf03`**: a tagged, published release
  with eight recorded asset digests, an SPDX SBOM and SLSA build provenance, all bound
  to that one SHA by the release DAG's attestation. Every field the measurement
  contract requires is recorded — SHA, tag, version, digest, build command, Go
  version, `CGO_ENABLED`, build tags, target platforms, SBOM and attestation
  references, dates, owner — read back from the published artifact rather than
  transcribed. The record also states what the candidate does **not** contain.
  **Reproducibility is demonstrated, not asserted:** all five published platform
  binaries were rebuilt **bit-for-bit** from the frozen SHA on a different OS and
  architecture, and the two non-obvious preconditions (a *tagless* checkout, and a
  real clone rather than a linked `git worktree` — either one changes every digest)
  are written down so the next person's correct build does not look like a failure.
  `docs/decisions/2026-07-m0-candidate-freeze.md` is marked **superseded**, not
  deleted, and the two documents that still *pointed* at the dead candidate — the
  execution plan's authority note and the RC dossier — now name the new one, so
  "one candidate, one truth" holds across the repository rather than only in the
  decision record. No release was cut, tagged or published to produce this record.
- **`STALE` is a first-class evidence-index status.** A candidate move used to leave
  dependent rows reading `UNKNOWN`, which cannot be told apart from a gate nobody ever
  measured — so the row would quietly inherit the new candidate. `STALE` (the
  measurement contract's fourth status, beside PASS/FAIL/UNKNOWN) says it out loud:
  like `UNKNOWN` it counts as **not passed**, and `go run ./cmd/evidence -check` now
  **rejects a `STALE` row that does not name what superseded it**, so the marking can
  never be silent. The two rows that spoke about the old candidate are marked STALE
  and re-measured, never re-pointed; the rows that never referred to a candidate are
  deliberately left `UNKNOWN`, and the record says which and why.
- **Go-depth evaluation corpus — `corpus/manifest.json` v3.** The corpus now pins
  **six Go repositories** (uuid, lo, cobra, gin, grpc-go, kubernetes) instead of one,
  each to a release tag **and** a full 40-character commit sha, with the ten required
  stratification properties mapped by name to a specific repository (or recorded as an
  explicit gap) in a new `stratification` block. `kubernetes` v1.29.0 is the stress
  target: **15 718 Go files**, measured from a real clone, not estimated — every entry
  carries a `measured` census with the date and the exact command behind the numbers.
  License and permitted use are now recorded per repository and **required** for any
  entry the harness clones. Rationale per repository: **`corpus/README.md`** (new).
- **Corpus tier 4 — manual-only stress targets.** `corpus.yml` still caps its nightly
  run at `-max-tier 3`, so tier 4 runs only on an explicit `-tier 4` (or through
  `cmd/eval -full-run`, which selects by name). Measured reason: indexing the pinned
  kubernetes checkout costs ~3 min and ~9 GB peak RSS — a working set no hosted runner
  should absorb on a schedule. PR wall-clock is unchanged: every new entry is tier 3
  or 4.

### Changed

- **The dead hero budgets are labelled instead of implied.** `docs/eval/hero-budgets.json`
  is now `schema_version: 3` and declares itself `historical: true` / `ratcheting: false`
  with a recorded `historical_reason`; `cmd/eval` rejects an artifact that claims both,
  omits the reason, or still carries the old schema. Historical does **not** mean
  disabled — the ceilings are still enforced fail-closed for cobra, flask and guava,
  and a missing, malformed or wrong-runner-class budget is still a failure, not a skip.
  Re-baselining is blocked on the re-frozen candidate and a comparable measurement, and
  the file says so rather than looking like a ratchet.
- **Removed the all-zero latency budgets.** `hero_suite.measured_max_latency_ms_per_op`
  held `0` for all twelve Stable operations, because the historical run measured every
  hero task below the millisecond floor. Nothing read the map, and a zero budget that
  silently counts as met is worse than no budget: it renders green. It is gone,
  replaced by `latency_signal: "none"`, and `cmd/eval` now rejects **any** numeric zero
  anywhere in a budget artifact.

## [0.6.7] - 2026-07-27

### Fixed
- Flagless `graphi search -semantic` (and `search-ast` / `find-clones`) now
  discovers the per-repo ingest meta sidecar the same way it already
  discovered the store, so the durable vectors written by a flagless
  `graphi index --semantic` are actually reloaded. Previously the meta dir
  came only from the `-meta` flag: default discovery found the auto-managed
  `db.sqlite` but reloaded no vectors, and a semantic query silently
  returned zero hits unless `-meta ~/.graphi/<fingerprint>/meta` was spelled
  out by hand. Discovery is read-only (`state.DiscoverMeta` returns the dir
  only when its `ingest-meta.db` already exists) and an explicit `-meta`
  still wins unchanged.

## [0.6.6] - 2026-07-25

### Fixed
- A zero-config MCP session no longer silently binds the user's HOME
  directory (or the filesystem root) as the repository. A dotfiles `.git`
  (or a stray `go.mod`) directly in `$HOME` makes the upward marker walk
  land there whenever an MCP client spawns `graphi mcp` outside the project
  (observed with the Devin CLI launching from the home directory) — the
  session then starts indexing the entire home tree, which effectively
  never finishes and holds the cross-process ingest lock the whole time.
  Auto-detection now fails closed with an actionable error ("refusing to
  auto-bind your home directory …"); with multiple client roots, a
  home-resolving candidate is skipped and a later real repository still
  binds. Explicit intent keeps working: `graphi mcp -db <path>` pins a
  store, CLI verbs take `-root`, and `GRAPHI_ALLOW_HOME_ROOT=1` opts a
  deliberately home-rooted setup back in.
- `graphi doctor`'s `mcp` check now warns when one client config contains
  several zero-config graphi entries (e.g. a hand-added `graphi-myrepo`
  next to the setup-managed `graphi`): each spawns its own process, they
  resolve the SAME repository, and all but the indexing winner sit blocked
  on its ingest lock reporting "repository is not bound". The warning
  names the entries; entries pinned with `-db`/`-daemon` never contend and
  are not flagged.

## [0.6.5] - 2026-07-25

### Fixed
- MCP tool calls during a long first index no longer return a static,
  uninformative -32002 forever. The retryable error now reports live state:
  the resolved repository root, the ingest phase with per-file progress and
  elapsed time (`repository is not bound: indexing <root>: parse 1234/5678
  files (3m10s elapsed); retry in a moment`) — or, when ANOTHER graphi
  process holds the cross-process ingest lock for the same repository (the
  common trigger: two MCP server entries pointing at one repo, where one
  indexes and the other silently queued forever), `waiting for another
  graphi process indexing <root> (waited 2m5s)`. The MCP server also
  announces the resolved root and prints the CLI's milestone progress lines
  on stderr, so client log panes show what is happening instead of nothing.
  The -32002 code, the `repository is not bound: ` prefix and the `; retry
  in a moment` suffix are unchanged, so clients that match the retryable
  shape keep working.
- A roots-change during an in-flight bind no longer lets the replacement
  bind race its cancelled predecessor's still-held ingest lock: the new
  attempt joins the old one before opening a session, and an attempt
  cancelled during that join resolves to a bind error instead of reporting
  "still indexing" forever. The ingest walk is now cancellation-aware, so an
  aborted bind releases the ingest lock promptly instead of scanning a huge
  tree to completion first.
- `graphi doctor` (new `index` check) and `graphi status` now read the
  full-pass recovery marker and probe the ingest lock non-destructively,
  distinguishing "another graphi process is indexing right now — wait" from
  "a previous index did not complete — rebuild". Both previously conflated
  the two into "run `graphi index`" / "needs a rebuild", advice that would
  just queue behind the same lock while an index was running. `graphi
  status --json` gains the additive `index.full_pass_in_progress` and
  `index.lock_held` fields.

### Notes
- The per-repo state dir (`~/.graphi/<fingerprint>/` or
  `$XDG_STATE_HOME/graphi/<fingerprint>/`; `repo.json` maps a fingerprint
  back to its repository) contains `meta/ingest.lock.db`: a lock-only SQLite
  database holding NO data. It only serializes concurrent indexes of one
  repository; deleting it is safe whenever no graphi process is running, and
  after a crash nothing needs deleting — the OS releases the lock with the
  process, and it is the full-pass recovery marker (cleared by one completed
  `graphi index`) that persists instead.

## [0.6.4] - 2026-07-25

### Added
- `graphi setup` can now register the MCP server into the **Devin CLI**
  (`--client devin`, config at `~/.config/devin/config.json`; included in the
  default `--client all` sweep when detected). The former blanket "cloud
  agents are out of scope" note is narrowed: purely cloud-sandboxed agents
  still are, but locally-installed agent CLIs that spawn stdio servers are
  supported.

### Fixed
- MCP `tools/list` no longer fails while the session's first (cold) index is
  still running. Since v0.6.1 the index runs off the protocol loop and tool
  *calls* fail closed with a retryable -32002 "still indexing" — but
  `tools/list` failed the same way, and MCP clients (Claude Code, Claude
  Desktop, Devin CLI, …) list tools exactly once at session start and treat
  that error as a dead server: on any repository whose first index outlives
  the client's startup listing, the server was shown as permanently "failed
  to list tools" even though it was healthy and indexing. The catalog is
  profile-static and needs no repository, so `tools/list` now serves it
  during the bind (initialize advertises `tools.listChanged`, and
  `notifications/tools/list_changed` is emitted once the binding lands — or
  fails — so the client re-lists and converges on the bound,
  capability-narrowed catalog, or surfaces the real bind error). Tool calls
  during indexing keep the retryable fail-closed contract, and a session
  whose binding has genuinely failed still fails `tools/list` closed.

## [0.6.3] - 2026-07-25

### Changed
- `graphi index --semantic` reports embedding progress instead of going
  silent between the ingest summary and its final "embedded N nodes" line —
  on a real repo the generation pass runs one embedder round-trip per node
  and takes minutes, which read as a hang. It now announces the phase up
  front ("embedding N nodes via …"), renders a throttled in-place status
  line on a TTY (sparse 25%-milestone lines otherwise), and embeds in
  chunks — which also bounds the in-flight vector memory to a chunk instead
  of the whole repo. Persisted vectors are unchanged; on an embed error,
  already-completed chunks now remain persisted (derived state a re-run
  overwrites) instead of all-or-nothing.

## [0.6.2] - 2026-07-24

> **If graphi ate your machine's memory** (macOS "your system has run out of
> application memory" during `graphi index`/`sync`, or a runaway `graphi mcp`
> spawned by an MCP client): that incident class is fixed in **v0.6.1** and
> hardened further in this release. What to do as a user:
> 1. Re-run the install script (`curl -fsSL …/install.sh | sh`) and confirm
>    `graphi version` reports **≥ 0.6.2**.
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
