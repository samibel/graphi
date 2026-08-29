# graphi — Architecture Plan

> The single design entry point for graphi. It ties together the layered model,
> the data flow, the parse/extract pipeline, the provenance contract, the
> local-first guarantees with the CI gates that enforce them, and — as built,
> not as planned — the extension kernel (§6). It links out to the
> per-subsystem docs under [`docs/`](.) rather than duplicating them. Status here
> reflects code on the default branch — the machine-checked
> [capability coverage matrix](coverage-matrix.md) is the authoritative,
> CI-enforced inventory of what is real.

Related:
[capability coverage matrix](coverage-matrix.md) · [How-To guide](HOWTO.md).

---

## 1. The layered model: `cmd → surfaces → engine → core`

graphi is one Go workspace with **a single engine serving every surface**. The
dependency direction is strictly downward:

```
cmd/*        entry points & wiring (graphi, layerguard, coverage, canary, …)
   ↓
surfaces/*   CLI · daemon · MCP stdio · embeddable MCP HTTP adapter · HTTP/SSE · web · extensions · forge · gitlog · guard
   ↓
engine/*     query · search · analysis · agenttools · testintel · classify · edit · review · ingest · observe · overlay · watch · community · interproc-taint · conformance · ledger · context · memory · distill · skillgen · wiki
   ↓
core/*       model · parse · graphstore · community   (pure leaves)
```

- **One engine, many surfaces.** No surface holds query, search, traversal,
  ordering, serialization, or analysis logic of its own — every surface
  dispatches through the shared `surfaces/client` seam and returns the engine's
  canonical serialized bytes, so surfaces are byte-identical for identical inputs
  and **can never diverge** (parity by construction).
- **Lower layers never import higher ones.** `core/parse` and `core/graphstore`
  are pure leaves.

### Layer-direction invariant (mechanically enforced)

[`internal/layerguard`](../internal/layerguard) parses the import graph of the
ranked packages (`go list -json`), classifies each package into its layer
(`core`=1 … `cmd`=4), and fails on any upward/sideways edge. `internal/*` and
`bench/*` are unranked tooling (rank 0) and intentionally unconstrained — they
may read any layer's registries. The rule is declared once, in code, and run
in CI via `go run ./cmd/layerguard` (release gate). The FU-4 coverage guard
(`internal/coverage`) follows the same pattern.

---

## 2. Data flow: source repo → surfaces

```
source repo
  │  incremental ingest (engine/ingest): per-file dirty-flag, crash recovery
  ▼
graphstore (core/graphstore)
  │  hot in-memory graph  +  durable SQLite/FTS5 sidecar
  ▼
query · search · analysis (engine/*)
  ▼
surfaces (CLI · daemon · MCP · HTTP · web · extensions)
```

- **Ingest is incremental and crash-safe.** Files are re-parsed only when their
  dirty flag indicates change; a crash mid-ingest recovers to a consistent graph.
  A full re-index and an incremental update converge on a **byte-identical** graph
  (the invariant cross-file linking, FU-1, must preserve).
- **graphstore** keeps a hot in-memory graph for traversal speed backed by a
  durable SQLite + FTS5 sidecar for persistence and lexical search.
- **query / search / analysis** are read-only over the store; **edit** (EP-006)
  mutates through an atomic saga with undo.

---

## 3. Parsing & extraction model

The parse boundary is an **open/closed registry** (`core/parse`): callers extend
language coverage purely by calling `Register` with a new `Parser` — no existing
parser code is edited.

- **Default tier (CGo-free, shipped).** [`RegisterDefaults`](../core/parse/defaults.go)
  wires two stdlib parsers (Go, JSON) plus 20 subset-tagged pure-Go
  `gotreesitter` grammars — **22 shipped languages, one `r.Register(...)` line
  each** (the 23rd, `html`, is in the coverage matrix as ⏳ planned;
  `graphi-broad` opts into it later). The Go path uses the reference
  AST→graph extractor ([extract_go.go](../core/parse/extract_go.go)).
- **Opt-in `graphi-broad` (CGO).** The broad grammar set plugs into the same seam
  behind a build tag; the hard CGo-free gate is exempted for that flavor only. See
  [graphi-broad.md](graphi-broad.md).
- **Honest current vs. roadmap.** The Go extractor emits symbol nodes and
  **intra-file** `defines`/`calls`/`references` edges today. **Cross-file /
  cross-package resolution (FU-1) is ✅ shipped**: a post-ingest linker in
  `engine/link` resolves selector calls and imports against the
  fully-committed symbol table, preserving the byte-identical
  full-vs-incremental invariant. The coverage matrix marks FU-1 `shipped` and
  HTML `planned` (deferred to `graphi-broad`); the guard fails if either
  silently drifts.

---

## 4. The provenance contract

Every edge carries provenance so downstream analysis and review can weigh
evidence rather than trust blindly. The closed vocabulary
([`core/model`](../core/model/edge.go)):

- **tier** — `heuristic` | `derived` | `confirmed` (ascending confidence);
- **reason** — why the edge was emitted;
- **evidence** — the citation backing it (node/edge/source reference).

Analyzers and the PR-review vertical (EP-007) propagate this provenance; the edit
saga (EP-006) records auditable change records with actor + undo token.

---

## 5. The local-first contract and the CI gates that enforce it

graphi's promise: **runs entirely on your machine, CGo-free by default, no
telemetry, no non-loopback egress.** Each clause is backed by a CI gate, not a
README claim:

| Gate | Workflow / entrypoint | Enforces |
|---|---|---|
| **Egress canary** | [`canary.yml`](../.github/workflows/canary.yml) · `internal/canary` | zero non-loopback network on the default path; loopback-only allowlist |
| **CGo-free conformance** | [`cgoconformance.yml`](../.github/workflows/cgoconformance.yml) · `cmd/cgoconformance` | default binary is statically linked, no cgo package in the import graph |
| **Ledger audit** | [`ledgeraudit.yml`](../.github/workflows/ledgeraudit.yml) | token-savings ledger integrity / anti-gaming cap |
| **Eval claim gate** | [`eval.yml`](../.github/workflows/eval.yml) · `internal/eval` | the headline token-savings metric on a committed dataset |
| **Bench budget** | [`bench.yml`](../.github/workflows/bench.yml) · `bench/lang-budget.md` | binary-size budget (<50 MB) for the default tier |
| **Privacy audit** | [`privacy-audit.yml`](../.github/workflows/privacy-audit.yml) | zero-telemetry static scan |
| **Strict test gate** | [`testgate.yml`](../.github/workflows/testgate.yml) · `cmd/testgate` | complete full-suite stream is green; no expected-failure carve-out |
| **Layer direction** | `release.yml` · `cmd/layerguard` | `cmd→surfaces→engine→core` import direction |
| **Coverage matrix** | [`coverage-matrix.yml`](../.github/workflows/coverage-matrix.yml) · `cmd/coverage` | the checked-in [coverage matrix](coverage-matrix.md) matches the live registries (FU-4) |
| **Reproducible release** | [`release.yml`](../.github/workflows/release.yml) | deterministic, CGo-free release build |

### The coverage-matrix gate (FU-4)

[`internal/coverage`](../internal/coverage) derives the **live** capability set
straight from the registries the product runs on — registered parsers
(`parse.NewDefaultRegistry().Languages()`), registered analyzers (`analysis`
default registry `Names()`), advertised MCP tools (`mcp.ToolNames()`), and
present surfaces — and diffs it against [`coverage-matrix.yaml`](coverage-matrix.yaml).
A docs-only change that omits a real capability, claims a phantom `shipped`
one, or marks a live capability `planned` **fails the build**. Update flow:

```
# edit docs/coverage-matrix.yaml, then:
go run ./cmd/coverage -generate   # refresh docs/coverage-matrix.md
go run ./cmd/coverage -check      # same check CI runs (exit 1 on drift)
```

This is the in-repo, drift-proof answer to *"what does graphi actually do, and is
it all real?"* — the closing piece of the project's end-to-end traceability story.

---

## 6. The extension kernel as built

> As of `main @ 4f14966` (2026-08-29, SW-252 / AX-13). This section describes
> what the Extension Platform Kernel (SW-220..SW-232, SW-238..SW-248) actually
> shipped — not the plan it was built from. Every path below exists at that
> commit; every number names the command that produced it. The measured facts
> in §6.10 are a snapshot and go stale the moment a seam moves; re-run the
> command before quoting one.

Four labelled parts of this section are **Transitional — scheduled for removal
only under the AX-17 rule**: the legacy adapter table (§6.4), the shadow
comparison (§6.6), the canary modes (§6.6), and the dual descriptor/contract
sources (§6.5). The AX-17 rule, recorded in the portfolio spec
`ax13-18-as-built-and-cutover.md` and repeated here so it travels with the
code: legacy adapters, duplicate descriptor/contract sources, shadow
comparisons and canary code may be removed only when every dependent
operation is evidence-based `active`, at least one release line has passed
with zero unexplained divergence since the flip,
[executor-seam-rollback.md](executor-seam-rollback.md) has been updated, the
Stable goldens are unchanged, and the removal measurably reduces complexity —
**never in the same slice in which an operation first turns `active`.** Today
zero operations are `active`, so every trigger is unmet by construction.

### 6.1 Module kernel and the RuntimeBuilder

- **[`engine/module`](../engine/module)** is the built-in module set and the
  builder the runtime composes it through (SW-227 / AX-07). A `Module` is a
  `Manifest` (id, version, `Requires`) plus one `Register` function that
  contributes capabilities through typed `Add*` methods — `AddOperation`,
  `AddParser`, `AddAnalyzer`, `AddResolver`
  ([module.go](../engine/module/module.go)). It is ADR 0013 tier B and
  nothing else: statically compiled first-party Go, no runtime loading, no
  ABI, no discovery.
- **Three built-in modules**, declared in
  [`engine/module/builtin.go`](../engine/module/builtin.go): `core.parse`
  (the CGo-free parser set), `engine.analysis` (the analyzer set) and
  `engine.operations` (requires the other two; contributes **the whole
  operation catalog as specs** — see §6.3). No built-in module contributes a
  resolver; `engine/ingest` still constructs that registry itself.
- **`cmd/internal/runtime` is the only composition root.**
  [`cmd/internal/runtime/builder.go`](../cmd/internal/runtime/builder.go)
  (`runtime.NewBuilder(store) → With* → Build()`) calls
  `module.BuildBuiltins` and is the one place in the tree allowed to import
  `engine/module`. The rule is a test, not a convention:
  `TestAX07_OnlyTheCompositionRootImportsTheModuleSet` in
  [`engine/module/boundary_test.go`](../engine/module/boundary_test.go) fails
  the build on any other importer, and its sibling pins that the module set
  itself depends only on engine-rank capability packages.
- The builder is **not a service locator**: it exists only during startup,
  `Build` consumes it, and the returned `Composition` is frozen with
  read-only accessors (`Operations()`, `Parsers()`, `Analysis()`,
  `Resolvers()`, `Frozen()`).

### 6.2 Registry lifecycle: register → validate → freeze → execute

- **[`core/registry`](../core/registry)** (SW-222 / AX-02) is the one
  lifecycle vocabulary every registry in the tree speaks. A registry is
  populated (`Register`), optionally checked for cross-registrant obligations
  (`Validate` — first needed by the operation catalog, then by the module
  set), then **frozen** (`Lifecycle.Freeze`); after that every mutation entry
  point returns an `ErrFrozen`-typed error rather than panicking, and only
  then does anything execute against it.
- **Collision policy is declared per registry**, never implied:
  `PolicyLastWins` (kept for `core/parse`, so an opt-in CGO grammar may
  supersede a stdlib default), `PolicyFirstWins`, and
  `PolicyFirstWinsWithReplace` (`engine/analysis`'s narrow, sanctioned
  `Replace`). The module builder and the rule-pack store both declare
  `PolicyFirstWins`, so reaching a last-wins seam *through* them cannot
  silently shadow a built-in — ADR 0013 threat T5 as code.
- **Typed sentinels** callers match with `errors.Is`
  ([registry.go](../core/registry/registry.go)): `ErrDuplicate` (a key
  registered twice under first-wins), `ErrUnsupportedOverride` (a `Replace`
  on a policy without one), `ErrMissingDependency` (the thing the operation
  needs is not registered), `ErrFrozen` (mutation after composition) and
  `ErrCycle` (added by SW-227 for the module set's `Requires` graph). Every
  error names its offenders; there is no parallel error kind anywhere on the
  kernel.

### 6.3 The operation catalog is the canonical metadata source

- **[`engine/opcatalog`](../engine/opcatalog)** (SW-223 / AX-03) is the one
  place an operation's id, contract version, tier, description, input schema,
  ports/permissions and determinism are stated. The population is **data**:
  [`engine/opcatalog/shadow.json`](../engine/opcatalog/shadow.json) (56
  `OperationSpec`s, one per MCP operation advertised at the AX-00 baseline),
  embedded and loaded once, canonical (id-sorted) iteration everywhere,
  frozen after `Build`.
- **MCP descriptors and the HTTP contract are projections of it, not
  siblings.** Two test files are the gate that keeps the projection honest,
  re-derived from the *live* builders on every run:
  [`surfaces/mcp/opcatalog_parity_test.go`](../surfaces/mcp/opcatalog_parity_test.go)
  (`TestAX03_ShadowCatalog_*`, `TestAX03_ParityGate_DetectsEveryDriftClass`)
  and
  [`surfaces/http/contract_projected_test.go`](../surfaces/http/contract_projected_test.go)
  (`TestAX05_ProjectedContractResources_MatchTheLegacyList`). Drift in either
  direction breaks the build.
- The catalog reuses `core/registry`'s errors and freeze vocabulary but
  deliberately **not** its `GRAPHI_REGISTRY_FREEZE` escape hatch: the catalog
  was never mutable after construction, so there is no prior behaviour to
  roll back to.

### 6.4 Generic executor and legacy-compatibility adapters

- **[`surfaces/client/executor.go`](../surfaces/client/executor.go)** (SW-224
  / AX-04) is the seam through which an operation is invoked **by name**
  (`Request{Operation, Version, Arguments}` → `Executor.Execute`) instead of
  by widening the ~40-method `Client` interface. Identity comes from the
  catalog: the adapter table is keyed by catalog id and `NewExecutorWithCatalog`
  fails if an adapter names an id the catalog does not declare. Rejections
  are `registry.ErrMissingDependency`, whether the gap is the id, the
  contract version, or the adapter.
- **Legacy adapter table — Transitional (AX-17).**
  [`surfaces/client/executor_adapters.go`](../surfaces/client/executor_adapters.go)
  (`legacyAdapters()`) maps every adapted id to a typed `Arguments` whose
  `invoke(ctx, c Client)` calls the legacy `Client` method. It is built once
  per `Executor` under `PolicyFirstWins` and never handed out.
- **What "the executor path" means today, stated without hedging:** the
  executor transports canonical bytes, it never makes them. `Execute`
  resolves the request against the catalog, picks the adapter, and returns
  `args.invoke(ctx, e.client)` — **the same `Client` method the legacy path
  calls**, returning that method's bytes unchanged. No operation has an
  engine-side handler; `module.Builder.AddOperation` registers a *spec* only.
  Legacy-vs-executor byte parity is therefore exact by construction and, for
  now, tautological. The first real engine-side handler is AX-15 (SW-255).
- Surfaces reach the seam through `client.DispatchOperation`
  ([canary.go](../surfaces/client/canary.go)), which consults the canary
  position (§6.6) before choosing a path.

### 6.5 Surface projections for MCP and HTTP

- **MCP:** [`surfaces/mcp/descriptors_projected.go`](../surfaces/mcp/descriptors_projected.go)
  (SW-225 / AX-05) derives every `tools/list` descriptor body from the
  catalog, per binding profile (Stable / maximal). Dispatch in
  [`surfaces/mcp/toolcalls.go`](../surfaces/mcp/toolcalls.go) has **one
  generic branch** for migrated operations plus a `migratedTools`
  argument-mapping table; a migrated capability has no arm of its own, and a
  test pins the table's keys to `client.MigratedOperations()`.
- **HTTP:** [`surfaces/http/contract_projected.go`](../surfaces/http/contract_projected.go)
  derives the `/contract` resource list from the catalog;
  [`surfaces/http/handlers.go`](../surfaces/http/handlers.go) keeps a
  per-operation `switch` for query-parameter mapping and dispatches migrated
  operations through the same generic `client.DispatchOperation` call.
- **Dual descriptor/contract sources — Transitional (AX-17).** Both files
  still carry the hand-written legacy source behind a source-level switch
  (`descriptorSource = descriptorSourceProjected`,
  `contractSource = contractSourceProjected`). Deliberately **not** an
  environment variable: descriptors and `/contract` are wire contract, and two
  processes on one version must not be able to advertise differently. Rolling
  back is a one-line source change plus a release, and the tests flip the
  switch to prove both sources produce identical bytes.

### 6.6 Canary positions: `legacy` · `shadow` · `active` — Transitional (AX-17)

- **[`surfaces/client/canary.go`](../surfaces/client/canary.go)** (SW-226 /
  AX-06, per-operation since SW-228 / AX-08) defines the three-position
  switch in front of the executor seam, applied to the closed
  `migratedOperations` set and to those only:

  | Position | What runs | What the caller gets |
  |---|---|---|
  | `legacy` | the legacy `Client` method only | the legacy bytes |
  | `shadow` **(shipped default)** | both paths, compared off the caller's critical path (SW-245) | the **legacy** bytes |
  | `active` | the executor path only | the executor bytes — **not a shipped position** |

- **Shadow comparison — Transitional (AX-17).** In `shadow` the executor's
  answer is compared and recorded through `internal/divergence` into a durable
  record under the state directory (SW-232), readable without a server via
  `graphi doctor -divergence [--json]`; it is never returned. The comparison
  queue is bounded (`shadowQueueCapacity = 64`,
  [`surfaces/client/canary_shadow.go`](../surfaces/client/canary_shadow.go));
  overflow is counted as *lost coverage*, never as agreement.
- **Shipped default and override.** `canaryModeDefault = CanaryModeShadow`
  (`canary.go`; moved from `legacy` by SW-244 / AX-12). The operator override
  is read once, in the composition root, from the environment:
  `GRAPHI_CANARY_<OP>` per operation (`runtime.EnvCanaryModeFor`,
  [`cmd/internal/runtime/runtime.go`](../cmd/internal/runtime/runtime.go))
  and `GRAPHI_CANARY_ALL` for every operation without its own variable; a
  per-operation variable always wins. An unrecognised value **fails the
  session at startup**. The operator page is
  [executor-seam-rollback.md](executor-seam-rollback.md) — its content is live
  and exercised by the `executor-rollback` CI leg; this section links it and
  does not restate it.

### 6.7 Declarative rule packs — the only shipped extension tier

- **[`engine/extpack`](../engine/extpack)** (SW-229 / AX-09) is ADR 0013 tier
  A and the only extension product graphi ships. A pack is two files — a
  versioned manifest and one SHA-256-pinned data artifact — and both are
  data: nothing in the package compiles, links, evaluates or executes what a
  pack contains, and nothing opens a socket. `permissions` accepts exactly
  one value (`graph.read`); collision policy is `PolicyFirstWins` with **no**
  `Replace` path, so a pack adds capability keys and never takes one. Merge
  order is a function of lockfile content, not install order. The
  [`engine/extpack/conformance`](../engine/extpack/conformance) harness and
  the `graphi extension {init,lint,conform,validate,install}` verbs
  (SW-230, SW-246) are documented in
  [extension-developer-kit.md](extension-developer-kit.md).
- Tier B (static first-party modules, §6.1) is the standard for anything
  Stable; it is not an *extension* product — it is the product.

### 6.8 Process extensions: NO-GO

Tier C (a trusted subprocess, SW-231 / AX-11) was spiked, built, measured, and
**declined** for this phase:
[decisions/2026-08-process-extension-go-no-go.md](decisions/2026-08-process-extension-go-no-go.md).
Four of the five go/no-go criteria were met; *"a real user case justifies the
added complexity"* was not, and it is a conjunction member. The spike — its
host package and the example extension, at the paths the decision record
names — is retained **unwired** as the evidence for the record and is not in
the `cmd/graphi` import closure; the spike's own isolation test forbids any
file outside it from naming those paths, which is why this section does not.
Tier D (WASM) was never shipped (ADR 0013 N5).
Reopening either requires a new ADR, not a design review.

### 6.9 Current defaults and the rollback path

| Switch | Position of record | Where | How to roll back |
|---|---|---|---|
| Canary position | `canaryModeDefault = CanaryModeShadow` | [`surfaces/client/canary.go`](../surfaces/client/canary.go) | `GRAPHI_CANARY_<OP>=legacy` for one operation (`runtime.EnvCanaryModeFor`), `GRAPHI_CANARY_ALL=legacy` for the seam — an environment variable, because it is an operator kill switch for a path that produces bytes |
| Composition path | `defaultCompositionMode = CompositionBuilder` | [`cmd/internal/runtime/compositionmode.go`](../cmd/internal/runtime/compositionmode.go) | one-line source change of the constant, or `runtime.SetCompositionMode(runtime.CompositionLegacy)` programmatically; **no environment variable exists for it**, by design — the two compositions are required to be indistinguishable to a client |
| MCP descriptor source | `descriptorSource = descriptorSourceProjected` | [`surfaces/mcp/descriptors_projected.go`](../surfaces/mcp/descriptors_projected.go) | one-line source change plus a release; no env variable |
| HTTP contract source | `contractSource = contractSourceProjected` | [`surfaces/http/contract_projected.go`](../surfaces/http/contract_projected.go) | same |
| Registry freeze enforcement | enforced | [`core/registry/registry.go`](../core/registry/registry.go) | `registry.SetFreezeEnforced` / `GRAPHI_REGISTRY_FREEZE` (AX-02's rollback; the operation catalog deliberately does not honour it) |

Two switches, two mechanisms, on purpose: the canary is an environment
variable because an operator must be able to flip a byte-producing path
without a release; the composition and projection switches are source-level
because exposing a knob with no client-visible consequence would advertise a
choice that does not exist while inviting untested combinations.

### 6.10 Measured seam facts at `4f14966`

| Fact | Value | Command |
|---|---|---|
| Operations on the executor seam | **10**, all Labs: `architecture`, `architecture_violations`, `compound`, `dead_code`, `find_clones`, `framework_map`, `repo_overview`, `search_ast`, `search_hybrid`, `test_impact` | `go run ./cmd/seamreach` |
| Canary positions | **0 legacy / 10 shadow / 0 active**, every one at the compiled-in default | `go run ./cmd/graphi doctor` (the `[executor-seam]` line) |
| `surfaces/client` direct internal import fan-out | **44** against the AX-00 baseline of **41** (+3: `core/registry`, `engine/extpack`, `engine/opcatalog`); reported, not gated (SW-220) | `CGO_ENABLED=0 go test -v ./internal/importfanout` (baseline: [`rc/ax00-import-fanout.json`](rc/ax00-import-fanout.json)) |
| Divergence record | `state: UNKNOWN-AND-UNOBSERVABLE`, 0 observations, 0 mismatches, `reachable_in_default: 0` | `go run ./cmd/graphi doctor -divergence --json` |
| Seam reachability | 0 of 10 dual-running operations reachable through `graphi mcp`; 10 of 10 through `graphi mcp -labs`; gate PASS | `go run ./cmd/seamreach -check` (declaration: `internal/seamreach/reachability.txt`) |

**In one sentence:** the ten migrated operations are Labs and unreachable
through the default MCP profile, so a stock install records no dual-run
evidence at all — the divergence record's emptiness is a fact about the
profile, not a statement that the two paths agree (SW-248).

---

## 7. Per-subsystem documentation index

- **Parsing / languages:** [graphi-broad.md](graphi-broad.md) ·
  [default-tier-security.md](default-tier-security.md)
- **CI & local-first:** [ci/](ci) · [setup-privacy.md](setup-privacy.md)
- **Token-savings:** [ledger/](ledger) · [meter/](meter) · [price/](price) · [savings/](savings)
- **Edits / context:** [edit/](edit) · [context/](context)
- **Surfaces:** [surfaces-http.md](surfaces-http.md) ·
  [surfaces-web.md](surfaces-web.md) · [surfaces-vscode.md](surfaces-vscode.md) ·
  [surfaces-wiki.md](surfaces-wiki.md)
- **Extension kernel:** §6 above · [extension-developer-kit.md](extension-developer-kit.md) ·
  [executor-seam-rollback.md](executor-seam-rollback.md) ·
  [adr/0013-extension-trust-tiers.md](adr/0013-extension-trust-tiers.md) ·
  [decisions/2026-08-process-extension-go-no-go.md](decisions/2026-08-process-extension-go-no-go.md)
- **Decisions:** [adr/](adr)
- **Inventory & status:** [coverage-matrix.md](coverage-matrix.md) · [FEATURES.md](FEATURES.md)
