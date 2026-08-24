# corpus — the pinned evaluation repositories

`manifest.json` is the corpus definition. Adding a repository is a **data change
here, never a code change**: assertions, tiers, licenses and file counts all live in
the manifest, and `internal/corpus` only enforces their shape.

This file records **why each repository is in the corpus** — the half of the
selection that JSON cannot carry, and the half a reader needs a year from now to
tell a deliberate choice from an arbitrary one.

## Shape of the corpus (v3)

Go is the only GA language: the twelve stable operations are scored against Go, and
every P0 accuracy and performance gate is a statement about Go repositories. v2 was
built for *language spread* (one Go repo among six); v3 adds **Go depth** — six
pinned Go repositories carrying the ten stratification properties of PRD FR-2 —
while keeping the non-Go repositories for cross-language regression detection.

| Repo | Ref | Tier | License | Go files | Why this one |
|---|---|---|---|---|---|
| [uuid](https://github.com/google/uuid) | v1.6.0 | 3 | BSD-3-Clause | 21 | The floor of the size range: one package, no build tags, no generation. If an operation is wrong here, it is wrong everywhere. |
| [lo](https://github.com/samber/lo) | v1.39.0 | 3 | MIT | 17 | Generics carrier: 13 of 17 files declare type-parameterised functions, so a resolver that ignores type parameters degrades visibly instead of silently. |
| [cobra](https://github.com/spf13/cobra) | v1.8.0 | 1 | Apache-2.0 | 36 | Mid-size CLI application, and the historical typeresolve acceptance target: `Command.Execute` → `c.ExecuteC()` is a receiver dispatch only the go/types pass can prove (`confirmed_edges`). |
| [gin](https://github.com/gin-gonic/gin) | v1.9.1 | 3 | MIT | 92 | Web/API service shape (router, middleware, binding, render) **and** the build-tag carrier: 16 files sit behind `//go:build` constraints. |
| [grpc-go](https://github.com/grpc/grpc-go) | v1.60.1 | 3 | Apache-2.0 | 831 | The workhorse: 277 Go package directories, 11 `go.mod` files, 49 generated protobuf files, 287 test files — four properties at a size the nightly run can still afford. |
| [kubernetes](https://github.com/kubernetes/kubernetes) | v1.29.0 | **4** | Apache-2.0 | 15718 | The FR-2 stress target (≥10 000 source files): 3611 package directories, 34 modules, `staging/` publishing 5899 of the files as separate modules. |
| flask, sinatra, ky, express | — | 1–3 | BSD-3-Clause / MIT / MIT / MIT | — | Kept from v2: they cover the historical first-contact bug classes (non-source assets, symlinked layouts, malformed JSON fixtures) and give cross-language regression signal. Out of scope for P0 accuracy and performance claims. |
| [guava](https://github.com/google/guava) | v33.0.0 | 3 | Apache-2.0 | 3204 | The **Java** monorepo (guava, guava-testlib, guava-gwt, android flavor). Brought to the v3 measured standard (WP-J6): full 40-char sha + a `measured` census (3204 `.java` of 3298 tracked, via the `source_files` field). Cross-language regression; out of scope for P0 accuracy. |
| [okio](https://github.com/square/okio) | 3.9.1 | 3 | Apache-2.0 | 313 | The first **Kotlin** pin (language-GA program G5): a Kotlin-multiplatform IO library, 284 `.kt` + 29 `.java` at the pin. Cross-language regression + JVM-binder capability — graphi parses all 313 files with zero parse crashes and produces confirmed Java+Kotlin call sites. Its `measured` block uses the new `source_files` census (the non-Go analog of `go_files`). |
| [kotlinx.serialization](https://github.com/Kotlin/kotlinx.serialization) | v1.6.3 | 3 | Apache-2.0 | 615 | The second **Kotlin** pin (G5, WP-J6): JetBrains' reflectionless serialization library, 609 `.kt` + 6 `.java` (the `.java` are `module-info.java` JPMS declarations) at the pin. Complements okio with a compiler-plugin-heavy, densely-generic codebase — graphi tables all 615 JVM files with zero crashes. **Binding rate (W1.b/SW-175): 19.16 % = 2 949 bound call sites / 15 388 CST call sites**, with the skip histogram, every exclusion size and the parse caveat (ERROR nodes in 80 of 609 `.kt`) in [`docs/rc/jvm-binding-rate.md`](../docs/rc/jvm-binding-rate.md). *Superseded here: "the binder resolves 3517 typed Kotlin sites" — kept rather than overwritten, because 3517 was a **numerator without a denominator** (R6's objection), and today's binder yields 3 433 on this pin, so it is not reproducible either.* |

Every count above is **measured**, not estimated: each entry's `measured` block
records the numbers, the date, and the exact command sequence (a shallow clone at
the pinned sha plus `git ls-files`), so anyone can re-derive them from the pin.
`go_files` is the strictest reading of "source files" — no docs, YAML or vendored
assets are counted toward the stress threshold.

## Stratification: property → repository

`manifest.json`'s `stratification` block maps each of the ten FR-2 properties to the
repository that carries it, with the measured evidence. Five repositories cannot each
carry ten properties, so the mapping is explicit rather than implied by the selection:

| Property | Repository |
|---|---|
| small library | uuid |
| mid-size CLI application | cobra |
| web or API service | gin |
| multi-package repository | grpc-go |
| large repository or monorepo | kubernetes |
| generics | lo |
| multiple go.mod | grpc-go |
| build tags | gin |
| generated code | grpc-go |
| tests and benchmarks | grpc-go |

A property no repository covers is recorded as an **explicit gap** (`"gap": true`
with a reason) rather than omitted — `LoadManifest` rejects a mapping that names
neither a known repository nor a gap, so silent coverage claims cannot survive.
The list itself is pinned by `TestCheckedInManifest_GoCorpus`.

## Tiers — and why kubernetes is tier 4

| Tier | Meaning | Where it runs |
|---|---|---|
| 1 | local fixtures, no network | every PR (`corpus.yml`, `-max-tier 2`) |
| 2 | pinned SHAs, cheap | every PR |
| 3 | nightly/manual, larger repos | nightly schedule + `workflow_dispatch` |
| 4 | **manual-only stress target** | never scheduled — explicit `-tier 4` only |

The Go entries added in v3 are tier 3 or 4, so **PR wall-clock does not grow**.

Kubernetes is tier 4 on measured grounds, not caution: a full index of the pinned
checkout costs **~3 min 16 s wall and ~9 GB peak RSS** on an 8-core laptop
(2026-07-27, v0.6.7 dev build, `CGO_ENABLED=0`), producing a ~640 MB store; the whole
smoke flow (clone → index → search → query → analyze → diagnose) measured 207 s. The
wall-clock is affordable; the **9 GB working set is not** — a hosted runner absorbing
that on a schedule is one memory-limit change away from a nightly red. That is the
point of a stress target, and the reason it is invoked deliberately. `corpus.yml` caps even its nightly run at `-max-tier 3`, so tier 4 runs
only when someone asks for it:

```bash
go run ./cmd/corpus -manifest corpus/manifest.json -tier 4      # stress smoke
go run ./cmd/eval   -manifest corpus/manifest.json -full-run kubernetes
```

`cmd/eval -full-run` selects by name and ignores tiers, so the stress target is
available to the P0 performance harness without ever joining a scheduled job.

## Pins fail closed

`ref` pins a **release tag**; `sha` additionally pins the checkout HEAD. The v3 Go
entries and the three JVM entries (guava, okio, kotlinx.serialization) pin the
**full 40-character commit sha** (FR-2); the remaining older non-Go entries keep
their recorded 12-character prefixes. If an upstream tag is re-pointed — or a clone
lands on any other commit — the run fails at the `pin` step *before* indexing, with
the observed HEAD in the failure detail. It is never a warning, and there is no
"latest" fallback. `TestRunner_PinFailsClosed` holds that behaviour in place
hermetically (wrong sha, unreadable HEAD, matching sha).

## Not yet: the weekly eval-full matrix

`eval-full.yml` still measures cobra, flask and guava only. Its `-budgets` gate is
fail-closed — a repository absent from `docs/eval/hero-budgets.json`'s
`real_repos.selection` fails the run — and those budgets are historical ceilings
from a retired harness. Naming the runner class and re-baselining the budgets is
SW-123/SW-124; the new Go entries join the weekly matrix in the same change that
gives them real budgets. Until then they are exercised by `corpus.yml`'s nightly
tier-3 run.

## Adding a repository

1. Clone it at the tag you intend to pin, and record `git rev-parse HEAD` (full sha).
2. Count files from that clone (`git ls-files '*.go' | wc -l`, `git ls-files | wc -l`)
   and fill `measured` with the counts, the date and the command.
3. Document `language`, `license` and `permitted_use` — a repository whose terms are
   undocumented is rejected by the loader.
4. Give it a tier: 3 unless it belongs on every PR, 4 if it is a stress target.
5. Add at least one `expect_nonempty` search, so a run that indexes nothing cannot
   read as green.
6. If it carries a stratification property, say so in `properties` and in
   `stratification`.

## Measured block table — W5.j SW-196

Cross-file-heuristic residual (9 langs) v3 pins, measured block counts taken
from a real clone at the pinned sha via `git ls-files` filtered to the
language's tracked files. Reproducibility: every count was taken at the
pinned sha on the recorded date; the per-pin captured output lives under
`/tmp/sw196-measured/` (one `.count` file per pin, plus the recorded sha).

| Lang | Repository | Pin (ref) | Sha | Source files | Tracked files | Date measured |
|---|---|---|---|---|---|---|
| bash | — | — | — | (no pin) | — | — |
| c | cjson | v1.7.19 | `c859b25da02955fef659d658b8f324b5cde87be3` | 99 (.c + .h) | 229 | 2026-08-23 |
| c_sharp | Newtonsoft.Json | 13.0.3 | `0a2e291c0d9c0c7675d445703e51750363a549ef` | 933 (.cs) | 1158 | 2026-08-23 |
| cpp | nlohmann/json | v3.11.3 | `9cca280a4d0ccf0c08f47a99aa71d1b0e52f8d03` | 455 (.cpp + .hpp + .cc + .hh + .hxx + .cxx + .h) | 1090 | 2026-08-23 |
| lua | lua-resty-core | v0.1.26 | `407000a9856d3a5aab34e8c73f6ab0f049f8b8d7` | 35 (.lua) | 176 | 2026-08-23 |
| php | composer | 2.9.8 | `39ee8baff8e97a1b657bbfcd6a236ff93a5efbb2` | 581 (.php) | 1029 | 2026-08-23 |
| ruby | sinatra | v4.0.0 | `b626e2d82c23b4fde0b51782fd32ca27ccde1d1a` | 143 (.rb) | 321 | 2026-08-20 |
| rust | serde | v1.0.219 | `49d098debdf8b5c38bfb6868f455c6ce542c422c` | 188 (.rs) | 333 | 2026-08-23 |
| sql | — | — | — | (no pin) | — | — |

Honest abstentions: **bash** (no representative open-source bash project at
the pin tier — bash is per-project glue, every project's scripts are an
idiosyncratic DSL extension of POSIX sh) and **sql** (no canonical SQL
corpus at the pin tier — SQL is not an import-driven language, its cross-file
construct is JOIN/VIEW not import; ADR-W1 documents). Each abstention has a
no_pin entry in `corpus/manifest.json` with a named `no_pin_reason`, and the
G5 row in `docs/rc/evidence-index.yaml` stays UNKNOWN with the reason
re-stated in `current`.

### Pin sha provenance (sha vs tag-object sha)

Lightweight tags (cjson v1.7.19, Newtonsoft.Json 13.0.3, nlohmann/json
v3.11.3, lua-resty-core v0.1.26) carry the commit sha directly — the
tag-object sha returned by `git ls-remote refs/tags/<tag>` IS the commit sha,
because lightweight tags are pointers, not objects. Annotated tags (composer
2.9.8, serde v1.0.219, sinatra v4.0.0) carry a tag object whose sha is the
tag-object sha, distinct from the commit sha at `^{}`. Per SW-196 AC-3, the
manifest pins the COMMIT sha (the deref result) in both cases; the
`release_tag` field carries the tag name verbatim for human reading.
