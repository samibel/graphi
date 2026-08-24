# Per-Language Binary-Budget Sub-Allocation (SW-052, EP-009 STEP-0)

> ## AMENDMENT — 2026-08-24 (SW-203, W5.q): the intra/parse residual's six languages measured the SW-177 way, and the gate's headroom is down to 0.0147 %
>
> Per D6 this amendment is **added**; nothing below it — including the SW-177
> amendment — is rewritten, re-pointed or deleted. Measured at `bca6cf2` (`main`)
> under `internal/release.CanonicalBuildArgs`' own contract
> (`-trimpath -buildvcs=true -tags <the 21 registered subset tags>
> -ldflags -X …version.Version=dev`), `CGO_ENABLED=0 GOOS=linux GOARCH=amd64
> GOAMD64=v1`, cross-built from darwin/arm64 on an Apple M2 Max. Full record,
> raw transcripts and captured environment:
> [`../docs/eval/runs/2026-08-24-local-sandbox/sw203-campaign.md`](../docs/eval/runs/2026-08-24-local-sandbox/sw203-campaign.md).
>
> **1. The measurement method carries a stated noise budget of ±4,096 B**, and it
> was measured before any cost below was quoted. Three dispatches of the same
> build are byte-identical (same sha256); a semantically identical build with the
> tag list merely reordered has the same size and a different sha256; and two
> additivity checks — drop(toml)+drop(lua) versus drop(toml,lua), and
> drop(css)+drop(yaml) versus drop(css,yaml) — leave residuals of **+4,192 B** and
> **−4,064 B**, one page-alignment quantum in each direction. **A marginal cost at
> or below ~4 KB is not resolvable by this method** and is reported as such rather
> than as a number.
>
> **2. Per-language marginal cost in the shipped binary, versus the blob figure
> the per-language table below publishes.** Shipped default rebuilt with and
> without each `grammar_subset_<lang>` tag; baseline **34,244,965 B**. Each
> without-`<lang>` binary was checked with `go tool nm` to confirm it dropped
> exactly that one `subsetBlobFS_<lang>` embed and kept the other 19 — without
> that check a tag that silently did nothing would look like a small cost.
>
> | language | blob (as published below) | **marginal cost in the binary** | ratio |
> |---|---:|---:|---:|
> | CSS | 14,325 | **28,077** | 1.96× |
> | HCL | 20,132 | **44,358** | 2.20× |
> | Markdown | 36,259 | **89,972** | 2.48× |
> | TOML | 5,317 | **4,590** (≤ noise budget — not resolvable) | ~0.86× |
> | YAML | 25,479 | **153,654** | **6.03×** |
> | JSON | *no blob — stdlib parser, no subset tag* | **1,690** (≤ noise budget) | — |
> | *Kotlin (SW-177 cross-check)* | *337,236* | *385,874* | *1.14×* |
>
> Kotlin is re-measured here purely as a method control: SW-177 published
> 385,418 B for the same removal and this campaign gets 385,874 B — a 456 B
> agreement, well inside the noise budget, on a different machine.
>
> **JSON has no `grammar_subset_json` tag** (it is a stdlib parser, per
> `internal/release.DefaultGrammarSubsetTags`' own comment), so the tag route is
> structurally inapplicable to it. Its figure comes from removing
> `r.Register(NewJSONParser())` from `core/parse.RegisterDefaults` instead, with
> `-buildvcs=false` on both halves so the dirty-tree VCS stamp cannot contaminate
> the delta. The same route applied to CSS gives **38,822 B** against the tag
> route's 28,077 B; the ~10.7 KB difference is graphi's own
> `core/parse/parser_css.go`, which the tag route leaves linked.
>
> **3. YAML costs 6.03× its published allocation, and the allocation is NOT
> widened.** Upstream `yaml_scanner.go` is 2,096 lines and leaves with the tag,
> which is where the 128 KB above the blob goes. This is the same shape SW-177
> found on Kotlin, on a different language: the gate is **stated as it stands**.
> Closing the gap is a separate decision and is not taken here.
>
> **4. SW-177's correction generalises, with numbers.** The per-language table
> below lists **blob** sizes. Measured against true marginal cost the ratios run
> from 0.86× to 6.03×. Blob size is not a proxy for binary cost and must stop
> being read as one. The table is left exactly as published; this note is the
> correction.
>
> **5. The whole-binary gate is met — with 0.0147 % of headroom.**
> `bench-budget.yml` still reads `binary_size_bytes: baseline 32,509,872, budget
> 34,250,000`. **Neither is moved by SW-203.**
>
> ```
> measured 34,244,965  ≤  budget 34,250,000     PASS
> headroom      5,035 B  =  0.0147 % of budget
> SW-177 recorded          87,074 B  (0.25 %)   → headroom is down 94 %
> the six intra/parse languages together cost 322,341 B = 64× the remaining headroom
> ```
>
> **6. The growth is still CODE, not the toolchain — re-measured, not carried
> over.** Both of SW-177's toolchain rows reproduce **byte-for-byte**:
>
> | tree | toolchain (read from the binary's buildinfo) | bytes | SW-177 published |
> |---|---|---:|---:|
> | `80d67ed` (v0.7.1) | go1.26.5 | 32,741,344 | 32,741,344 |
> | `80d67ed` | go1.26.6 | 32,750,198 | 32,750,198 |
> | `bca6cf2` (`main`) | go1.26.6 | 34,244,965 | — |
>
> Toolchain alone: **+8,854 B (+0.03 %)**. Code alone since v0.7.1:
> **+1,494,767 B (+4.56 %)**. The first attempt at this leg was run under the
> ambient `GOTOOLCHAIN=auto` and Go silently switched *down* to go1.26.5 to
> satisfy `80d67ed`'s `go` directive — `go version` said 1.26.6 while
> `go version -m` on the binary said 1.26.5. Both legs are now pinned and both
> are verified from buildinfo.
>
> **7. What was NOT verified, and it matters more now than it did at 0.25 %.**
> These are cross-builds from darwin/arm64; the gate runs natively on
> `ubuntu-latest`. Every **delta** above is same-method and therefore robust to a
> cross-versus-native offset, but the **absolute** 5,035 B headroom figure assumes
> a cross-built linux/amd64 binary is byte-identical to a natively built one, and
> that assumption is **still untested** — SW-177 flagged it and SW-203 did not
> close it. At 87,074 B of headroom the question was academic; at 5,035 B it is
> not. It needs one CI dispatch. Separately,
> `internal/bench/harness.go`'s `buildBinary` passes no `GOOS`/`GOARCH`, so the
> gated metric measures a **host** binary — on CI the host is linux/amd64 and the
> two coincide, on a developer machine they do not. The darwin/arm64 host leg was
> measured too (baseline 33,104,098 B; css 18,128 · hcl 34,944 · markdown 69,536
> · toml 16,928 · yaml 137,280 · kotlin 383,856) rather than letting one platform
> stand for the other.

> ## AMENDMENT — 2026-08-19 (SW-177, W1.d): Kotlin re-measured against the gate, and the gate has 0.25 % headroom
>
> Per D6 this amendment is **added**; nothing below is rewritten, re-pointed or
> deleted. Measured at the W0.f-5 candidate `3b8d43f`, using
> `internal/release.CanonicalBuildArgs`' own contract
> (`-trimpath -buildvcs=true -tags <the 21 registered subset tags> -ldflags -X …version.Version=dev`,
> `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1`) — i.e. the artifact
> `internal/bench` actually gates, not a lookalike.
>
> **1. The 337,236 B figure in the per-language table below is CORRECT for the
> blob** — `gotreesitter@v0.20.2/grammars/grammar_blobs/kotlin.bin` stats at
> exactly 337,236 bytes.
>
> **2. But the blob size is NOT the budget-relevant number, and the gap is 14 %.**
> Building the shipped default with and without `grammar_subset_kotlin`:
>
> | build | bytes |
> |---|---|
> | shipped default, all 21 registered subset tags | **34,162,926** |
> | same build, `grammar_subset_kotlin` removed | 33,777,508 |
> | **Kotlin's marginal cost in the shipped binary** | **385,418** |
>
> The tag links `kotlin_scanner.go`, the parser-result and the registry
> registration as well as the blob. **The per-language table below lists BLOB
> sizes, so it understates every language's true binary cost by a similar
> margin.** The table is left exactly as published; this note is the correction,
> and re-measuring the other 18 languages the same way is not in SW-177's scope.
>
> **3. The whole-binary gate is met, with 0.25 % of headroom left.**
> `bench-budget.yml` reads `binary_size_bytes: baseline 32,509,872, budget
> 34,250,000`.
>
> ```
> measured 34,162,926  ≤  budget 34,250,000     PASS
> headroom     87,074 B  =  0.25 % of budget
> Kotlin costs 385,418 B  =  4.4× the remaining headroom
> ```
>
> **No baseline and no budget is moved by SW-177.** The gate is stated rather
> than silently widened, which is what AC-2 asks for: Kotlin is comfortably
> *inside* the gate, and the gate itself is *not* comfortable — one further
> grammar of Kotlin's size would breach it.
>
> **4. The growth is CODE, not the toolchain — measured, not assumed.** The
> +5.1 % over the recorded baseline could plausibly have been go1.26.5 → go1.26.6.
> It is not. Rebuilding the *same* older tree with the *newer* toolchain isolates
> it:
>
> | tree | toolchain | bytes |
> |---|---|---|
> | `80d67ed` (v0.7.1) | go1.26.5 (its own `go` directive) | 32,741,344 |
> | `80d67ed` | **go1.26.6** | 32,750,198 |
> | `3b8d43f` (candidate) | go1.26.6 | 34,162,926 |
>
> Toolchain alone: **+8,854 B (+0.03 %)**. Code alone: **+1,412,728 B (+4.31 %)**.
>
> **5. What was NOT verified.** These are cross-builds from darwin/arm64; the
> gate runs natively on `ubuntu-latest`. Every **delta** above is same-method and
> therefore robust to any cross-vs-native offset, but the **absolute** headroom
> figure assumes a cross-built linux/amd64 binary is byte-identical to a natively
> built one, and that assumption was **not tested** — it needs one CI dispatch.
> Full record: [`../docs/eval/runs/2026-08-19-local-sandbox/g7-jvm-baseline.md`](../docs/eval/runs/2026-08-19-local-sandbox/g7-jvm-baseline.md) §8.

This file defines the per-worker binary-size budget for graphi's tier-1 language
grammars: which languages are in scope, how the size cost is modeled, and the
measured deltas each worker recorded. It's for contributors adding or auditing
a language grammar in the default build.

> **Current enforced whole-binary gate (2026-07-16):** the benchmark uses the
> canonical CGo-free release argument contract (`-trimpath`, VCS metadata,
> version stamp, and the 20 subset grammar tags) on
> `go1.26.5/linux-amd64/GOAMD64=v1`. Baseline: **32,509,872 B**; budget:
> **34,250,000 B**. Historical EP-009 planning figures below are retained to
> explain the per-language allocation, not as the current gate value.

> **Owner:** SW-052 (STEP-0 foundation seam — hard gate). This file defines the
> per-worker binary-budget sub-allocation that the tier-1 language workers
> (SW-053..056) build against. It **freezes** the curated tier-1 list and assigns
> each worker a binary-size envelope.
>
> **Scope boundary (do not cross in this story):** this file is added here.
> `bench/bench-budget.yml` (the gate's single source of truth) is **not** re-pinned
> in SW-052 — consolidation into `bench-budget.yml` is deferred to **SW-057**. The
> numbers below are a planning envelope, not the enforced gate value yet.

## Why a sub-allocation

The whole-binary budget is **< 50 MB** (GoReleaser), pinned in `bench-budget.yml`
as `binary_size_bytes` (the original SW-052 planning baseline was `18,500,000`,
with a `20,000,000` budget). EP-009
fans grammar work out across parallel language workers. Without a per-worker
envelope, the first worker to land could consume the shared headroom and silently
starve later workers — turning a parallelizable epic into a serialized contention
point. This file pre-divides the headroom so each worker gets a fixed contract.

## Frozen tier-1 list (the pure-Go `gotreesitter` runtime + its embedded grammar blobs)

> **RE-PLANNED 2026-06-24 (EP-009-REPLAN-001).** The original framing — "curated, pure-Go
> subset of `go-sitter-forest`" — was **falsified** by the SW-053 dev spike. `go-sitter-forest`
> is entirely CGO (`import "C"` + `parser.c`), so it has **no** pure-Go subset to curate and
> cannot enter the `CGO_ENABLED=0` default tier at all.
>
> The default tier instead draws from the genuinely pure-Go
> **`github.com/odvcencio/gotreesitter` v0.20.2** runtime and its Go-embedded grammar `.bin`
> blobs, selected at build time via **subset build tags** (see "Subset-tag default build"
> below). `go-sitter-forest` is retained for the opt-in CGO `graphi-broad` build only.

The default tier ships **pure-Go, CGo-free** parsers only (`CGO_ENABLED=0 go build
./...` stays green; `internal/cgoconformance` enforces it — no offender named). The tier-1 set
below is the curated high-value coverage delivered through the `parse.Parser` /
`SymbolExtractor` seam. It is derived from ADR 0001, the EP-009 re-plan, and **frozen** for
EP-009 fan-out. (This is the single frozen-list source; it is also mirrored in
the epic registry at [`docs/coverage-matrix.md`](../docs/coverage-matrix.md).)

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
| 23 | HCL / Terraform | yes (pure-Go gotreesitter)| SW-054 shipped; subset-tagged |

> **Tier-1 list reconciled (SW-057, EP-009 re-plan Human Decision #2).** **Dockerfile,
> Protobuf, and GraphQL** were dropped from the committed tier-1 set — they are **NOT** in
> the shipped 20-blob default build and have no `grammar_subset_<lang>` tag in
> `internal/release.DefaultGrammarSubsetTags`. Revisit them as a follow-up if their pure-Go
> extractors become worth tier-1 effort. The frozen committed tier-1 set is exactly the **20
> subset-tagged languages** below (plus the two stdlib parsers Go + JSON, which carry no
> gotreesitter blob), with **HTML deferred** to `graphi-broad` (subset-isolation blocker).

### Deferred to `graphi-broad` (NOT in the default tier)

Languages are deferred to the opt-in `graphi-broad` CGO build (behind an explicit
build tag) when their **extractor effort** is out of scope for tier-1, or when the only
available grammar is **CGO-only** (`go-sitter-forest`). They MUST NOT enter the default
`RegisterDefaults`, because a CGO grammar in the default tier would break `CGO_ENABLED=0`.

> **RE-PLANNED note:** the *reason* for deferral is now **extractor scope / CGO-only
> grammars**, not "no maintained pure-Go grammar in `go-sitter-forest`" (that premise was
> false — `gotreesitter` ships 206 pure-Go grammars, all tier-1 languages present).
>
> The deferred long tail is reachable through the `go-sitter-forest`-backed CGO seam
> (SW-056): that lane exposes a **257-grammar** seam, but it wires exactly **one** grammar
> — `zig` — as the reference (`core/parse/broad.go` `RegisterBroad`). The 257-grammar
> `forest` meta-module is **intentionally not imported** (it would statically pull in
> hundreds of MB of generated C). Re-evaluate per language if its pure-Go extractor becomes
> worth tier-1 effort, or wire it on the broad lane one subpackage at a time.

## Per-worker binary-size envelope

Historical SW-057 measurement (CGo-free stdlib build, ADR 0001): the default
binary was ~3.4 MiB; with the EP-008 HTTP/SSE surface plus the curated
20-grammar subset-tagged tier, `binary_size_bytes` was pinned to
**`28,615,410`** with a **`30,000,000`** budget
(`baseline_version: 2026-06-24-ep009`) — then ~1.4 MB of headroom and ~21 MB
below the hard < 50 MB whole-binary ceiling. These are historical numbers, not
the current enforced gate; the current values are stated at the top of this
document and in `bench/bench-budget.yml`.

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
- **Historical SW-057 gate:** the whole-binary **< 50 MB** hard ceiling already applied. The
  then-measured subset-tagged tier-1 total (20 grammars, go1.26.3 / darwin-arm64,
  bench harness) was **28,615,410 B (~27.3 MB)** — ~21 MB below that hard ceiling.
  SW-057 pinned the global manifest to baseline `28,615,410`, budget `30,000,000`,
  `baseline_version: 2026-06-24-ep009`. Those manifest values are superseded; see
  the current gate at the top of this document and the historical record below.

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

SW-054 applies the same recipe as SW-053 over the remaining curated tier-1 languages. The
shipped default build now embeds the accumulated subset-tag set (umbrella `grammar_subset`
plus one `grammar_subset_<lang>` per registered language). Measured on go1.26.3 /
darwin-arm64 with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags '<accumulated
set>' ./cmd/graphi`:

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

### Historical re-pinning (SW-057, superseded)

**Historical record (SW-057, 2026-06-24).** When the tier-1 grammars landed, the
consolidation slice re-pinned `bench-budget.yml` `binary_size_bytes` against the
then-shipped subset-tagged total and bumped `baseline_version`:

- **baseline:** `28,615,410` B (= measured shipped subset-tagged default)
- **budget:** `30,000,000` B (~4.8% headroom; ~20 MB under the < 50 MB hard ceiling)
- **baseline_version:** `2026-06-24-ep009` (with a justification comment block)

**Historical measurement note (flag reconciliation).** The per-worker SW-053/SW-054 "Measured
deltas" tables above were recorded with `-trimpath -ldflags="-s -w"` (symbol-stripped),
giving the smaller historical figures (e.g. 18,979,778 B for the 20-blob subset). The
gate at that time measured the **shipped** binary as built by the canonical
`cmd/release` path / the `cmd/bench` harness — `-trimpath`, version-stamped,
**without** `-s -w` stripping — and measured **28,615,410 B**. That superseded
gate was pinned against the shipped number, not the stripped-build figure. Both
historical figures were far under the < 50 MB ceiling; the all-206 untagged build
(~46 MB) is what the subset tags prevent shipping.

The `cmd/bench` harness now builds its measured binary with
`internal/release.CanonicalBuildArgs` plus
`internal/release.DefaultGrammarSubsetTags`, so the budget gate enforces the
same unstripped, version-stamped default release contract and the **subset
model** (20 registered blobs), never the all-206 envelope. The current enforced
numbers are recorded at the top of this document; the SW-057 values in this
section are historical.

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
