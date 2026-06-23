# Per-Language Binary-Budget Sub-Allocation (SW-052, EP-009 STEP-0)

> **Owner:** SW-052 (STEP-0 foundation seam — hard gate). This file defines the
> per-worker binary-budget sub-allocation that the tier-1 language workers
> (SW-053..056) build against. It **freezes** the curated tier-1 list and assigns
> each worker a binary-size envelope.
>
> **Scope boundary (do NOT cross in this story):** this file is ADDED here.
> `bench/bench-budget.yml` (the gate's single source of truth) is **NOT** re-pinned
> in SW-052 — consolidation into `bench-budget.yml` is deferred to **SW-057**. The
> numbers below are a planning envelope, not the enforced gate value yet.

## Why a sub-allocation

The whole-binary budget is **< 50 MB** (GoReleaser), pinned in `bench-budget.yml`
as `binary_size_bytes` (current baseline `18,500,000`, budget `20,000,000`). EP-009
fans grammar work out across parallel language workers. Without a per-worker
envelope, the first worker to land could consume the shared headroom and silently
starve later workers, turning a parallelizable epic into a serialized contention
point. This file pre-divides the headroom so each worker has a fixed contract.

## Frozen tier-1 list (curated, pure-Go subset of `go-sitter-forest`)

The default tier ships **pure-Go, CGo-free** parsers only (`CGO_ENABLED=0 go build
./...` stays green; `internal/cgoconformance` enforces it). The tier-1 set below is
the curated high-value coverage delivered through the `parse.Parser` /
`SymbolExtractor` seam. It is derived from ADR 0001 and **frozen** for EP-009 fan-out
as of this story.

| #  | Language        | Default tier (pure-Go) | Status                  |
|----|-----------------|------------------------|-------------------------|
| 1  | Go              | yes (native go/ast)    | shipped (reference)     |
| 2  | JSON            | yes (stdlib)           | shipped                 |
| 3  | TypeScript      | yes                    | tier-1 (SW-053..056)    |
| 4  | JavaScript      | yes                    | tier-1                  |
| 5  | TSX / JSX       | yes                    | tier-1                  |
| 6  | Python          | yes                    | tier-1                  |
| 7  | Java            | yes                    | tier-1                  |
| 8  | C               | yes                    | tier-1                  |
| 9  | C++             | yes                    | tier-1                  |
| 10 | C#              | yes                    | tier-1                  |
| 11 | Rust            | yes                    | tier-1                  |
| 12 | Ruby            | yes                    | tier-1                  |
| 13 | PHP             | yes                    | tier-1                  |
| 14 | Bash / Shell    | yes                    | tier-1                  |
| 15 | HTML            | yes                    | tier-1                  |
| 16 | CSS             | yes                    | tier-1                  |
| 17 | YAML            | yes                    | tier-1                  |
| 18 | TOML            | yes                    | tier-1                  |
| 19 | Markdown        | yes                    | tier-1                  |
| 20 | SQL             | yes                    | tier-1                  |
| 21 | Kotlin          | yes                    | tier-1                  |
| 22 | Lua             | yes                    | tier-1                  |
| 23 | Dockerfile      | yes                    | tier-1                  |
| 24 | Protobuf        | yes                    | tier-1                  |
| 25 | GraphQL         | yes                    | tier-1                  |
| 26 | HCL / Terraform | yes                    | tier-1                  |

### Deferred to `graphi-broad` (NOT in the default tier)

Languages **without a maintained pure-Go grammar** in `go-sitter-forest` are
**deferred to the opt-in `graphi-broad` CGO build** (behind an explicit build tag).
They MUST NOT enter the default `RegisterDefaults`, because a CGO grammar in the
default tier would break `CGO_ENABLED=0`. Notable deferrals: **Swift**, **Scala**
(no maintained pure-Go grammar at freeze time), plus the long tail of the
257-grammar bundle. Re-evaluate per language if/when a maintained pure-Go grammar
appears.

## Per-worker binary-size envelope

Baseline (CGo-free stdlib build, ADR 0001): the default binary is ~3.4 MiB; with
the EP-008 HTTP/SSE surface the pinned `binary_size_bytes` baseline is
`18,500,000`, budget `20,000,000` — i.e. **~1.5 MB of headroom against the pinned
gate**, and ~30 MB against the hard < 50 MB whole-binary ceiling.

Each tier-1 grammar adds a parse table. To keep the epic parallelizable, each
language worker is allocated a **soft per-language envelope** and the epic a
**total grammar envelope** measured against the < 50 MB ceiling (not the current
pinned gate, which SW-057 re-pins once the real deltas are known):

- **Per-language soft envelope:** **≤ 1.0 MB** added to the `CGO_ENABLED=0`
  default binary per grammar. A worker exceeding this halts and escalates rather
  than silently consuming shared headroom.
- **Epic total grammar envelope:** **≤ 25 MB** across all tier-1 grammars,
  leaving ≥ 5 MB of margin under the 50 MB ceiling for non-parse growth.
- **Measurement:** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/graphi
  ./cmd/graphi && ls -l /tmp/graphi`, recording the delta vs the pre-grammar
  baseline. Each worker records its measured delta in its own story.

### Re-pinning (SW-057)

Once the tier-1 grammars land and real per-language deltas are measured, **SW-057**
consolidates the measured total into `bench-budget.yml` (`binary_size_bytes`
baseline/budget) and bumps `baseline_version`. Until then, this file is the
planning contract; the enforced gate value is unchanged.

## Hard constraints carried from SW-052

- **CGo-free default tier:** only pure-Go grammars register in `RegisterDefaults`.
- **Zero outbound network + no telemetry** at runtime.
- **Determinism:** identical input → identical nodes/edges/IDs.
- `bench-budget.yml` is **untouched** in this story (deferred to SW-057).
