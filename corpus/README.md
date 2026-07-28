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
| flask, sinatra, ky, express, guava | — | 1–3 | BSD-3-Clause / MIT / MIT / MIT / Apache-2.0 | — | Kept from v2: they cover the historical first-contact bug classes (non-source assets, symlinked layouts, malformed JSON fixtures) and give cross-language regression signal. Out of scope for P0 accuracy and performance claims. |

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
entries pin the **full 40-character commit sha** (FR-2), while the older entries keep
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
