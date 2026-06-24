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

## Frozen tier-1 list (the pure-Go `gotreesitter` runtime + its embedded grammar blobs)

> **RE-PLANNED 2026-06-24 (EP-009-REPLAN-001).** The original framing — "curated, pure-Go
> subset of `go-sitter-forest`" — was **falsified** by the SW-053 dev spike: `go-sitter-forest`
> is entirely CGO (`import "C"` + `parser.c`), so it has **no** pure-Go subset to curate and
> cannot enter the `CGO_ENABLED=0` default tier at all. The default tier instead draws from the
> genuinely pure-Go **`github.com/odvcencio/gotreesitter` v0.20.2** runtime and its Go-embedded
> grammar `.bin` blobs, selected at build time via **subset build tags** (see "Subset-tag
> default build" below). `go-sitter-forest` is retained for the opt-in CGO `graphi-broad` build
> (SW-056) only. See `projects/graphi/epics/EP-009/replan-decision.md`.

The default tier ships **pure-Go, CGo-free** parsers only (`CGO_ENABLED=0 go build
./...` stays green; `internal/cgoconformance` enforces it — no offender named). The tier-1 set
below is the curated high-value coverage delivered through the `parse.Parser` /
`SymbolExtractor` seam. It is derived from ADR 0001, the EP-009 re-plan, and **frozen** for
EP-009 fan-out. (This is the single frozen-list source; it is **mirrored** in
`projects/graphi/epics/EP-009/epic.md`. There is **no** `epics/index.md` file — earlier
pointers to it were dangling.)

| #  | Language        | Default tier (pure-Go) | Status                  |
|----|-----------------|------------------------|-------------------------|
| 1  | Go              | yes (native go/ast)    | shipped (reference)     |
| 2  | JSON            | yes (stdlib)           | shipped                 |
| 3  | TypeScript      | yes (pure-Go gotreesitter)| SW-053 green; subset-tagged default wired (AC#3) |
| 4  | JavaScript      | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 5  | TSX / JSX       | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 6  | Python          | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 7  | Java            | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 8  | C               | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 9  | C++             | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 10 | C#              | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 11 | Rust            | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 12 | Ruby            | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 13 | PHP             | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 14 | Bash / Shell    | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 15 | HTML            | grammar present, NOT subset-isolatable | **DEFERRED → SW-056** (scanner co-located with grammar_subset_blade; see HTML deferral note) |
| 16 | CSS             | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 17 | YAML            | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 18 | TOML            | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 19 | Markdown        | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 20 | SQL             | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 21 | Kotlin          | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 22 | Lua             | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |
| 23 | Dockerfile      | yes                    | tier-1                  |
| 24 | Protobuf        | yes                    | tier-1                  |
| 25 | GraphQL         | yes                    | tier-1                  |
| 26 | HCL / Terraform | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |

### Deferred to `graphi-broad` (NOT in the default tier)

Languages are deferred to the opt-in `graphi-broad` CGO build (behind an explicit
build tag) when their **extractor effort** is out of scope for tier-1, or when the only
available grammar is **CGO-only** (`go-sitter-forest`). They MUST NOT enter the default
`RegisterDefaults`, because a CGO grammar in the default tier would break `CGO_ENABLED=0`.

> **RE-PLANNED note:** the *reason* for deferral is now **extractor scope / CGO-only
> grammars**, NOT "no maintained pure-Go grammar in `go-sitter-forest`" (that premise was
> false — `gotreesitter` ships 206 pure-Go grammars, all tier-1 languages present). The
> deferred long tail is the `go-sitter-forest` 257-grammar CGO bundle consumed by SW-056.
> Re-evaluate per language if its pure-Go extractor becomes worth tier-1 effort.

## Per-worker binary-size envelope

Baseline (CGo-free stdlib build, ADR 0001): the default binary is ~3.4 MiB; with
the EP-008 HTTP/SSE surface the pinned `binary_size_bytes` baseline is
`18,500,000`, budget `20,000,000` — i.e. **~1.5 MB of headroom against the pinned
gate**, and ~30 MB against the hard < 50 MB whole-binary ceiling.

> **RE-PLANNED 2026-06-24 — size model corrected.** The old "≤ 1.0 MB **per language**,
> ≤ 25 MB **epic-total**" envelope is **superseded** (EP-009-REPLAN-001). It is the **wrong
> shape**: the `gotreesitter` `grammars` package embeds **all 206** blobs by default
> (`//go:embed grammar_blobs/*.bin`, gated `!grammar_subset`), so naively registering one
> grammar pulls the whole ~24.5 MiB blob directory in regardless of how many languages are
> wired — the cost is **not** per-language, it is a one-shot all-or-subset switch.

**Corrected runtime + per-blob model.** With the **subset build tags** (see below) the binary
embeds only the selected blobs, and the delta decomposes as:

- **Fixed cost (paid once for the whole epic):** the ~3.13 MB one-time pure-Go `gotreesitter`
  runtime (parser/lexer/query engine), linked the moment the first grammar registers.
- **Marginal cost (per registered language):** the parse-table blob only — tens to hundreds of
  KiB (TypeScript `typescript.bin` ≈ 119 KiB).
- **Governing gate:** the whole-binary **< 50 MB** hard ceiling. The projected subset-tagged
  tier-1 total is ≈ 18 MB — ~32 MB of headroom. The global `bench-budget.yml` re-pin against
  the subset total is owned by **SW-057** (untouched here).

**Subset-tag default build (SW-053 AC#3 — load-bearing).** The shipped default build MUST be
built with `-tags 'grammar_subset grammar_subset_<lang> …'` (one `grammar_subset_<lang>` per
language in `RegisterDefaults`), so only the registered blobs are embedded — **the all-206
default embed (+~24.5 MiB) is prohibited in the shipped default**. This is wired as the
single source of truth in `internal/release.DefaultGrammarSubsetTags` and applied by the
canonical `cmd/release` build (`go run ./cmd/release`); SW-054 extends that list one entry per
new language. The `release` CI job asserts the shipped binary embeds **only** the registered
blob set (any extra `grammar_blobs/*.bin` fails the gate).

- **Measurement:** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"
  -tags 'grammar_subset grammar_subset_<lang>' -o /tmp/graphi ./cmd/graphi && ls -l /tmp/graphi`,
  recording the delta vs the pre-grammar baseline. Each worker records its measured delta below.

### Measured deltas (SW-053 — TypeScript, first curated grammar)

The first curated pure-Go grammar is **TypeScript**, provided by the CGo-free
runtime `github.com/odvcencio/gotreesitter` (v0.20.2) and its `.../grammars`
sub-package. The runtime re-implements the tree-sitter parser/lexer/query engine in
Go (no `import "C"`, no `parser.c`); the TS parse table ships as a Go-embedded blob
(compressed `typescript.bin` ≈ 119 KiB). It builds green under `CGO_ENABLED=0` and
passes the `internal/cgoconformance` import-graph scan (no offender named).

Measured with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/graphi
./cmd/graphi && ls -l` on go1.26.3 / darwin-arm64 (re-recorded against the corrected
runtime + per-blob model, 2026-06-24):

| Build                                                        | Binary size (bytes) | Δ vs baseline | grammar blobs embedded |
|--------------------------------------------------------------|---------------------|---------------|------------------------|
| Pre-grammar baseline                                         | 12,264,546          | —             | 0                      |
| **Subset (SHIPPED DEFAULT)** `-tags 'grammar_subset grammar_subset_typescript'` | 15,516,658 | **+3,252,112 (~3.10 MiB)** | **1** (`typescript.bin`) |
| All-206 (no tags) — **PROHIBITED in the shipped default**    | 37,908,226          | +25,643,680 (~24.5 MiB) | 207                    |

> ✅ **AC#3 wired (SW-053, RESOLVED).** The earlier soft-envelope escalation is closed by the
> EP-009 re-plan + this slice's wiring; the old ≤ 1.0 MB per-language envelope is **superseded**
> by the runtime + per-blob model above.
>
> - The **shipped default** build is now subset-tagged. `internal/release.DefaultGrammarSubsetTags`
>   = `{grammar_subset, grammar_subset_typescript}` is the single source of truth; the canonical
>   `cmd/release` build applies it, so the shipped binary embeds **only** `typescript.bin`
>   (verified: 1 blob embedded, not 207). The `release` CI job fails on any unexpected
>   `grammar_blobs/*.bin`.
> - The **+3.10 MiB** subset delta is almost entirely the **one-time pure-Go runtime** (≈ 3.13 MB,
>   amortized across the whole epic); the TS parse-table blob itself is only **121,462 B
>   (~119 KiB)**.
> - The **+24.5 MiB all-206** number is recorded **only** as the cautionary "do not ship this"
>   contrast — it embeds 207 blobs (~200 unused) and is prohibited in the shipped default.
>
> SW-057 re-pins `bench-budget.yml` against the **subset-tagged** total (baseline + runtime +
> registered blobs), not the all-206 default. Until then, `bench-budget.yml` is unchanged here.

### Measured deltas (SW-054 — accumulated curated tier-1 subset)

SW-054 clones the SW-053 recipe over the remaining curated tier-1 languages. The shipped
default build now embeds the accumulated subset-tag set (umbrella `grammar_subset` + one
`grammar_subset_<lang>` per registered language). Measured on go1.26.3 / darwin-arm64 with
`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags '<accumulated set>' ./cmd/graphi`:

| Build | Binary size (bytes) | Δ vs pre-grammar baseline | grammar blobs embedded |
|-------|---------------------|---------------------------|------------------------|
| Pre-grammar baseline (recorded SW-053) | 12,264,546 | — | 0 |
| Subset SW-053 (`typescript` only) | 15,516,658 | +3,252,112 (~3.10 MiB) | 1 (`typescript.bin`) |
| **Subset SW-054 (SHIPPED DEFAULT, 20 blobs)** | **18,979,778** | **+6,715,232 (~6.40 MiB)** | **20** (TS + 19 SW-054) |
| All-206 (no tags) — **PROHIBITED in the shipped default** | 37,908,226 | +25,643,680 (~24.5 MiB) | 207 |

The ~18.1 MB subset total matches the EP-009 re-plan projection (≈ 18.1 MB) and stays far under
the **< 50 MB** hard whole-binary gate (~31 MB headroom). The marginal cost of the 19 SW-054
languages over the SW-053 TS-only subset is **+3,463,120 B (~3.30 MiB)** — overwhelmingly the
per-blob parse tables (the ~3.13 MB one-time runtime was already paid by SW-053). The 20 embedded
blobs are verified to be **exactly** the registered set (no all-206 embed, no unregistered blob):

`bash, c, c_sharp, cpp, css, hcl, java, javascript, kotlin, lua, markdown, php, python, ruby,
rust, sql, toml, tsx, typescript, yaml`.

Per-language verified blob sizes (from the story's measured table; the build embeds only these
plus the SW-053 `typescript.bin`):

| Language | subset tag | blob (bytes) |
|----------|------------|--------------|
| TSX | `grammar_subset_tsx` | 123,959 |
| JavaScript | `grammar_subset_javascript` | 40,559 |
| Python | `grammar_subset_python` | 60,172 |
| Java | `grammar_subset_java` | 46,587 |
| Rust | `grammar_subset_rust` | 113,341 |
| C | `grammar_subset_c` | 65,929 |
| C++ | `grammar_subset_cpp` | 415,697 |
| C# | `grammar_subset_c_sharp` | 290,957 |
| Ruby | `grammar_subset_ruby` | 148,405 |
| PHP | `grammar_subset_php` | 95,451 |
| Bash | `grammar_subset_bash` | 152,830 |
| CSS | `grammar_subset_css` | 14,325 |
| YAML | `grammar_subset_yaml` | 25,479 |
| TOML | `grammar_subset_toml` | 5,317 |
| Markdown | `grammar_subset_markdown` | 36,259 |
| SQL | `grammar_subset_sql` | 581,443 |
| Kotlin | `grammar_subset_kotlin` | 337,236 |
| Lua | `grammar_subset_lua` | 10,175 |
| HCL / Terraform | `grammar_subset_hcl` | 20,132 |

> **Subset-build cross-grammar dependency notes (verified during SW-054 dev):**
> - **TOML and Lua** lexers reference the shared helper `firstNonZeroSymbol`, which upstream
>   gotreesitter v0.20.2 defines in `java_lexer.go` (gated `grammar_subset_java`). The shipped
>   default includes `grammar_subset_java`, so both build green in the accumulated set; building
>   `grammar_subset_toml`/`grammar_subset_lua` in *isolation* (without java) fails to compile.
>   This is an upstream packaging quirk, not a graphi defect.

### HTML — DEFERRED to `graphi-broad` (SW-056)

**HTML has a pure-Go gotreesitter grammar but is NOT subset-buildable in isolation in
gotreesitter v0.20.2.** Its external scanner core (`goLexerAdapter`, `htmlDeserializeTagsInto`,
`htmlScanRawText`, `htmlScanComment`, `htmlScanStartTagName`, … — every helper `html_scanner.go`
calls) is physically co-located in upstream `blade_scanner.go`, gated `grammar_subset_blade`.
Consequently:

- `CGO_ENABLED=0 go build -tags 'grammar_subset grammar_subset_html' ./cmd/graphi` **fails to
  compile** (undefined scanner symbols).
- Enabling `grammar_subset_blade` to satisfy the compile dependency would embed an
  **unregistered `blade.bin` blob**, which AC#4 explicitly prohibits ("only those languages'
  blobs are embedded").

Therefore HTML is **deferred to `graphi-broad` (SW-056)** with this note, per story subtask 2's
deferral path (reserved for languages that cannot enter the pure-Go subset default tier). The
HTML extractor (`core/parse/parser_html.go`) is intentionally **not landed** in SW-054 to keep
the default `CGO_ENABLED=0` subset build green. Re-evaluate when upstream gotreesitter splits the
shared HTML scanner core out of `blade_scanner.go` into an html-gated file. This deferral is for a
**build-packaging** reason (subset-isolation), distinct from the SW-056 CGO-only-grammar path.

### Re-pinning (SW-057)

Once the tier-1 grammars land and real per-language deltas are measured, **SW-057**
consolidates the measured total into `bench-budget.yml` (`binary_size_bytes`
baseline/budget) and bumps `baseline_version`. Until then, this file is the
planning contract; the enforced gate value is unchanged.

## Hard constraints carried from SW-052 (+ EP-009 re-plan)

- **CGo-free default tier:** only pure-Go grammars register in `RegisterDefaults`
  (`gotreesitter` is genuinely pure-Go; `internal/cgoconformance` enforces it, no offender).
- **Subset-tag default build (AC#3):** the shipped default build uses
  `-tags 'grammar_subset grammar_subset_<lang> …'` (one per registered language); the all-206
  default embed is **prohibited** in the shipped default.
- **Zero outbound network + no telemetry** at runtime; grammar blobs are Go-embedded at build
  time (`go.mod` pin + `go mod verify` + committed `go.sum`); nothing fetched at parse time.
- **Determinism:** identical input → byte-identical nodes/edges/IDs (asserted repeated + concurrent).
- `bench-budget.yml` is **untouched** in this story (re-pin deferred to SW-057).
