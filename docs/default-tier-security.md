# Default-Tier Security & Isolation Controls (SW-055)

This document records the security controls added in **SW-055** that make the
CGo-free / zero-egress guarantees of graphi's **default tier** *provable and
regression-proof*. These are security controls, not packaging conveniences: the
CGo-free default build is the architectural firewall against the native-code
surface that the opt-in `graphi-broad` flavor (SW-056) introduces.

> **Scope split (default-tier-NOW vs SW-056-owned).** SW-055 owns the
> **default-tier firewall** and builds the shared, reusable guard/bounds/harness
> that SW-056 later wires into the broad lane. The `graphi-broad` smoke lane,
> `go-sitter-forest` license verification, and broad-flavor offline build /
> resource bounds are **SW-056-owned**. The default-tier firewall is provably safe
> **independently of SW-056's state**.

## Why these are security controls

The default tier is the trust boundary a user relies on when they run graphi
locally against untrusted source repositories:

- **No native code** → no cgo attack surface, no dynamic C dependency, a static
  reproducible binary.
- **No egress / no telemetry** → a local-first tool that never phones home.
- **Pinned, provenanced, license-verified supply chain** → no surprise
  dependency, no surprise license, no build-time network fetch.
- **Fail-closed resource bounds** → an untrusted input cannot exhaust resources
  (multi-GB file, billion-laughs nesting) or leak its own bytes through an error.

Each guarantee must be **regression-proof**: enforced by a test that fails the
moment the guarantee silently regresses.

## Before → After

| Control | Before SW-055 | After SW-055 |
|---|---|---|
| **No-CGO default tier** | Build-graph cgo scan only (`internal/cgoconformance`); the CGo-free guarantee could silently regress at the *registration* layer. | Release-blocking **registration-level guard** (`parse.AssertPureGoDefaults`) over `RegisterDefaults` output asserts every parser declares a pure-Go `Runtime`, **plus** a static `go-sitter-forest`-unreachable scan in the import graph. Paired with an **anti-vacuity negative test** that registers a synthetic CGO-marked parser and proves the guard rejects it. |
| **Zero egress / no telemetry** | `internal/audit.checkNoTelemetry()` returned a **hard-coded declared PASS**. | Backed by the **real `internal/canary` static gate** (telemetry-import denylist + type-checked outbound-dial AST scan over the default graph) + a **runtime zero-egress test** that exercises every default-tier parser under an **injected failing dialer** (no live sockets). |
| **Supply chain** | `go.mod` pin + `go.sum` only; license **assumed** Apache-2.0. | **Provenance/license record** (`internal/release.DefaultTierGrammarProvenance`): pinned `gotreesitter v0.20.2`, source URL, **actual license = MIT** read from the resolved module cache. Tests assert pin-match against `go.mod`, `go mod verify`, and SPDX-permissive license (fails on a license-changing bump). |
| **Offline build** | Assumed from the `//go:embed` mechanism, never tested. | **Actively tested**: the default flavor builds under `GOPROXY=off` + warm cache (the real risk is a *module* fetch, not a grammar fetch — blobs are Go-embedded via subset tags). |
| **Parse-time resource bounds** | **None** — `engine/ingest` read files unbounded (`os.ReadFile`), `Parse()` had no enforced size/timeout/depth. (The `parser_go.go` comment referenced a guard that did not exist.) | **Introduced fail-closed** (`parse.ResourceBounds`): max file size checked via `FileInfo.Size()` *before* read; parse timeout via `context.WithTimeout`; CST nesting depth capped inside the gotreesitter walk. On any breach the file is **skipped with a structured diagnostic** — never parse-anyway, never truncate. |
| **Error/log source sanitization** | Unaudited; parser errors could echo raw source. | **Default-deny** (`parse.SanitizedError` / `Provenance`): errors carry only structured provenance (file, language, byte-span, node-kind), **never raw source bytes**. Verified by sentinel-secret negative tests across every failure mode. |
| **CI test-suite assertion** | `go test ./...` green with no carve-out machinery. | **Explicit expected-failure allowlist** (`internal/testgate`): exactly the two known `internal/mcpconfig` root-perms tests, **no wildcard**, **length asserted == 2**, **privilege-conditional** (expected-fail only under root), consumed via `go test -json`. A third failure — or an allowlisted test that starts passing — fails the gate, so **regressions cannot hide behind the carve-out**. |

## Defense-in-depth: the two complementary no-CGO layers

```mermaid
flowchart TD
    subgraph Build["Build / import-graph layer"]
        A["internal/cgoconformance:<br/>CgoUsingPackages over ./cmd/graphi<br/>(CGO_ENABLED=0)"]
        B["internal/cgoconformance:<br/>ForestReachablePackages<br/>(go-sitter-forest absent from go list -deps)"]
    end
    subgraph Reg["Registration / runtime layer"]
        C["parse.AssertPureGoDefaults:<br/>every RegisterDefaults parser declares<br/>a pure-Go Runtime (go/ast | stdlib | gotreesitter)"]
        D["Negative / anti-vacuity test:<br/>synthetic CGO-marked parser into a<br/>throwaway registry → guard MUST reject it"]
    end
    A --> V{Release-blocking gate}
    B --> V
    C --> V
    D --> C
    V -->|all pass| OK["Default tier provably CGo-free"]
    V -->|any fail| BLOCK["Release blocked"]
```

The build layer proves `go-sitter-forest` (wholly CGO) can never enter the default
graph. The registration layer proves every *registered* default parser is pure-Go
and that the guard is **non-vacuous** (it rejects a planted CGO offender). Neither
layer subsumes the other.

## Fail-closed resource bounds & default-deny sanitization

```mermaid
flowchart LR
    F["Untrusted source file"] --> S{"size > MaxFileSize?<br/>(FileInfo.Size, before read)"}
    S -->|yes| SKIP1["skip + structured diagnostic"]
    S -->|no| R["os.ReadFile"]
    R --> T{"Parse exceeds<br/>ParseTimeout?"}
    T -->|yes| SKIP2["skip + structured diagnostic"]
    T -->|no| D{"CST depth > MaxDepth?"}
    D -->|yes| SKIP3["SanitizedError(ErrMaxDepthExceeded)<br/>→ skip + structured diagnostic"]
    D -->|no| OK["parse, commit graph"]
    SKIP1 --> CONT["ingestion continues"]
    SKIP2 --> CONT
    SKIP3 --> CONT
```

Every skip path emits **only** structured provenance — file, language, byte-span,
node-kind — and never the raw source bytes, so a secret embedded in an oversize /
deeply-nested / failing file can never leak into an error or log line.

## Where the controls live

| Control | Code | Tests |
|---|---|---|
| Runtime provenance marker | `core/parse/runtime.go`, `Parser.Runtime()` | `core/parse/bounds_test.go` |
| Registration no-CGO guard + negative test | `core/parse/guard.go` | `core/parse/guard_test.go` |
| Static forest-unreachable scan | `internal/cgoconformance/gate.go` (`ForestReachablePackages`) | `internal/cgoconformance/gate_test.go` |
| Zero-egress / no-telemetry (real) | `internal/audit/audit.go` (`checkNoTelemetry` → `canary.RunGate`) | `internal/audit/telemetry_test.go`, `core/parse/egress_test.go` |
| Supply chain + offline build | `internal/release/provenance.go` | `internal/release/provenance_test.go` |
| Fail-closed bounds + sanitization | `core/parse/bounds.go`, `engine/ingest/ingest.go` | `core/parse/bounds_test.go`, `engine/ingest/bounds_test.go` |
| Expected-failure allowlist + drift-guard | `internal/testgate/allowlist.go`, `cmd/testgate` | `internal/testgate/allowlist_test.go` |

## CI wiring

- `.github/workflows/cgoconformance.yml` — adds a **release-blocking** "no-cgo
  registration guard" step (`go test ./core/parse/ -run TestAssertPureGoDefaults`)
  ahead of the named `cgo-free-conformance` check (which now also runs the static
  forest-unreachable scan).
- `.github/workflows/testgate.yml` — runs the privilege-aware expected-failure
  allowlist gate over `go test -json ./...`.
