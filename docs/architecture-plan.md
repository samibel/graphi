# graphi — Architecture Plan

> The single design entry point for graphi. It ties together the layered model,
> the data flow, the parse/extract pipeline, the provenance contract, and the
> local-first guarantees with the CI gates that enforce them. It links out to the
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
surfaces/*   CLI · daemon · MCP stdio · embeddable MCP HTTP adapter · HTTP/SSE · TUI · web · extensions · forge · guard
   ↓
engine/*     query · search · analysis · edit · review · ingest · observe · overlay · watch · community · interproc-taint · conformance · ledger · context · memory · distill · skillgen · wiki
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
surfaces (CLI · daemon · MCP · HTTP · TUI · web · extensions)
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

## 6. Per-subsystem documentation index

- **Parsing / languages:** [graphi-broad.md](graphi-broad.md) ·
  [default-tier-security.md](default-tier-security.md)
- **CI & local-first:** [ci/](ci) · [setup-privacy.md](setup-privacy.md)
- **Token-savings:** [ledger/](ledger) · [meter/](meter) · [price/](price) · [savings/](savings)
- **Edits / context:** [edit/](edit) · [context/](context)
- **Surfaces:** [surfaces-http.md](surfaces-http.md) · [surfaces-tui.md](surfaces-tui.md) ·
  [surfaces-web.md](surfaces-web.md) · [surfaces-vscode.md](surfaces-vscode.md) ·
  [surfaces-wiki.md](surfaces-wiki.md)
- **Decisions:** [adr/](adr)
- **Inventory & status:** [coverage-matrix.md](coverage-matrix.md) · [FEATURES.md](FEATURES.md)
