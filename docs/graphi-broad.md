# The opt-in `graphi-broad` CGO flavor (SW-056)

This document records the **opt-in `graphi-broad` CGO flavor** added in **SW-056**:
broad 257-grammar coverage over the *same* `SymbolExtractor` contract the pure-Go
default tier uses, **build-tag isolated** so the default build stays provably
unaffected. It is the companion to [`default-tier-security.md`](default-tier-security.md)
(SW-055), which owns the default-tier firewall this flavor must never cross.

> **Scope.** SW-056 ships the lane and its in-slice transferable controls
> (supply-chain pin, C-level egress tripwire, `MaxFileSize`) plus the recorded
> human acceptance of the residual native-C risk. **Out-of-process / sandbox C-parser
> isolation is NOT in this slice — it is the named follow-up [SW-058].**

## Before → After

| Aspect | Before SW-056 | After SW-056 |
|---|---|---|
| **Language coverage** | Default **pure-Go tier only** (curated tier-1 `gotreesitter` grammars). | Adds the opt-in `graphi-broad` flavor wiring the **257-grammar `go-sitter-forest`** set (native C tree-sitter via `go-tree-sitter-bare`) over the **same** `SymbolExtractor` contract. |
| **Build** | `CGO_ENABLED=0 go build ./...` — no C toolchain. | Unchanged default; **plus** `CGO_ENABLED=1 go build -tags graphi_broad ./...` for the broad flavor. The forest backend is reached **only** via `RegisterBroad` in a `//go:build graphi_broad` file; `RegisterDefaults` is byte-identical. |
| **Default-build isolation** | CGo-free default graph, firewall in place. | **Unchanged and re-proven** with `go-sitter-forest` now in `go.mod`: `parse.AssertPureGoDefaults` + the static `go-sitter-forest`-unreachable import-graph scan stay green; `go list -deps ./cmd/graphi` contains no forest/bare package. |
| **Supply chain** | Forest **not pinned** in `go.sum`. | `go-sitter-forest/zig` + `go-tree-sitter-bare` **version-pinned** in `go.mod`/`go.sum` (one online pass), `go mod verify`'d, license-verified MIT at the pinned path; the lane builds **offline** (`GOPROXY=off` + `-mod=readonly`) thereafter. |
| **Runtime egress (broad lane)** | n/a (no broad lane). | A live `CGO_ENABLED=1 -tags graphi_broad` loopback-only **netns deny-egress** job **with a tripwire** proves the broad smoke parse performs zero outbound network at the **C level** — the static Go-AST canary is structurally blind to a C `socket()`/`connect()`. |

## Why a separate, build-tag-isolated flavor

`go-sitter-forest` is **wholly CGO** (`import "C"` + generated `parser.c`), which is
exactly why it belongs to the opt-in flavor and **not** the default tier. The goal
is broad coverage **without compromising the CGo-free default build**:

- Every forest-touching file carries `//go:build graphi_broad`; an untagged build
  never imports it.
- Two complementary guards keep the default graph pure-Go: a **registration-level**
  guard (`parse.AssertPureGoDefaults` rejects any non-pure-Go `Runtime`) and a
  **static import-graph** scan (`internal/cgoconformance` proves `go-sitter-forest`
  is unreachable from `./cmd/graphi`).
- Only **one grammar subpackage** is imported per language
  (`github.com/alexaandru/go-sitter-forest/zig`), never the top-level `forest`
  meta-module — which statically imports all ~257 grammars (hundreds of MB of
  generated C).

### Build-tag spelling (DN-2)

The flavor **name** is `graphi-broad`, but the Go **build tag** is `graphi_broad`
(underscore) because `-` is illegal in a Go build constraint. `internal/cgoconformance`
recognizes **both** spellings in `IsBroadFlavor` / `SanitizeGoFlags`, so the
default-graph gate's broad-strip never silently no-ops on the real
`-tags graphi_broad` flag.

## Residual security limitation (read before enabling — DN-5 / SW-056-SEC-001)

The `graphi-broad` flavor runs **native C** over source. graphi's Go-side resource
bounds do **NOT** contain a native-C fault:

- `recover` **cannot** catch a C `SIGSEGV`.
- A synchronous CGO call **cannot** be interrupted by a `context` deadline.
- The CST **depth guard bounds only the Go-side walk** — the C parser is not bounded
  by it. (The broad-lane adversarial CI step exercises the Go-side bound; a C stack
  overflow there is the *accepted residual*, not a regression.)
- Only **`MaxFileSize`** cleanly transfers to the C path.

Consequences:

- `graphi-broad` is **opt-in, NOT memory-safety-isolated, intended for trusted / CI
  input only.**
- `SetMaxParseDepth` is process-global and **not** honored by the C parser, so the
  broad tier **MUST NOT** run concurrently in-process with a default tier relying on
  a different depth bound.

This residual native-C crash / OOM / RCE risk is **explicitly accepted** for the
opt-in lane by human decision **SW-056-SEC-001** (`status: accepted`). Closing it
requires **out-of-process / sandbox C-parser isolation** (subprocess-per-parse with
rlimit/cgroup + signal trapping and/or seccomp), tracked as the named follow-up
**SW-058**. Until SW-058 lands, do not point `graphi-broad` at untrusted source.

## Verification (what proves the above)

- **Default isolation intact (with forest in `go.mod`):** `CGO_ENABLED=0 go build
  ./...`; `internal/cgoconformance`; `parse.AssertPureGoDefaults` positive + negative.
- **Broad lane builds + smoke-parses:** `CGO_ENABLED=1 go build -tags graphi_broad
  ./...`; the broad unit tests smoke-parse `zig` (a CGO-only grammar) to a **frozen
  expected vocabulary** asserting `Runtime() == RuntimeCGOForest`.
- **Supply chain:** `go mod verify`; offline build under `GOPROXY=off`; MIT license at
  the pinned grammar path.
- **Runtime egress:** the live `netns` deny-egress + tripwire job (`graphi-broad.yml`).

[SW-058]: out-of-process / sandbox C-parser isolation (deferred follow-up)
