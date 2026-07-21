# ADR 0001 — Parser Tier-1 Set and CGO_ENABLED=0 Sizing (SW-001)

- Status: Accepted (PoC / sizing record)
- Status note (2026-07): the broad flavor currently wires one grammar (`zig`);
  the 257-grammar figure below describes the upstream collection, not what is
  shipped. See `graphi-broad.md`.
- Date: 2026-06-19
- Story: SW-001 — Pluggable Parser Registry with Curated CGo-Free Tier-1 Language Set
- Epic: EP-001
- Supersedes/relates to: PB-001 / OQ1 (hybrid parsing decision — already RESOLVED at the portfolio level)

## Context

graphi turns source files into ASTs through a single deterministic parse boundary
(`core/parse`). The hybrid parsing decision (PB-001/OQ1) is already resolved at
the portfolio level; the sub-decision this ADR resolves is how many languages can
ship via **pure-Go, CGo-free** parsing, and at what binary-size cost, while
preserving graphi's local-first, single-static-binary, zero-CGo posture.

Constraints (from `context/architecture.md`):
- Default `graphi` binary MUST build with `CGO_ENABLED=0` (CI-enforced gate).
- Whole-binary budget: **< 50 MB** (GoReleaser).
- Zero outbound network, no eval/exec/shell in the parse path.
- `core/parse` is a strict pure leaf — no `engine/` or `surfaces/` imports.

## Decision

1. **Stable plug-in seam.** All language backends implement one interface
   (`parse.Parser`) and register into a concurrency-safe `parse.Registry`
   (`core/parse/registry.go`). Selection is by file extension / language. This is
   the single seam through which:
   - the native **Go-AST precision path** (`go/parser`, `go/ast`, `go/token`,
     CGo-free) routes `.go` files — `core/parse/parser_go.go`;
   - a second stdlib backend (JSON structural via `encoding/json`) routes
     `.json` — `core/parse/parser_json.go` — proving open/closed pluggability;
   - **future tree-sitter tier-1 grammars** plug in unchanged; and
   - the opt-in **`graphi-broad`** CGO build (257 go-sitter-forest grammars)
     plugs in behind an explicit build tag, registering through the *same*
     interface so the default build cannot structurally pull a CGo grammar.

2. **Curated tier-1 candidate list (target ~20–40 langs, CGo-free).** The
   curated tier-1 set is the high-value language coverage to be delivered through
   pure-Go tree-sitter bindings behind the `parse.Parser` seam in follow-up
   stories. Candidate list, to be finalized as grammars are integrated:

   Go (native AST — shipped), JSON (stdlib — shipped), TypeScript, JavaScript,
   TSX/JSX, Python, Java, C, C++, C#, Rust, Ruby, PHP, Bash/Shell, HTML, CSS,
   YAML, TOML, Markdown, SQL, Kotlin, Swift, Scala, Lua, Dockerfile, Protobuf,
   GraphQL, HCL/Terraform.

   Rationale: this band covers the dominant languages across the four source
   projects' feature corpus while keeping the trusted dependency base minimal,
   with each grammar version-pinned in `go.sum` under the supply-chain gates.

3. **Defer full grammar-laden integration behind the stable interface.** SW-001
   does **not** vendor the full 257-grammar go-sitter-forest bundle. Reasons:
   network fetch risk, binary-size blow-up, and CGo exposure for the broad set —
   all of which conflict with the default-build contract. Grammar integration is
   deferred to follow-up stories and lands incrementally behind the already-stable
   `parse.Parser` seam; `graphi-broad` (CGO, 257 grammars) is reachable only via an
   explicit opt-in build tag.

## Measured sizing (this build — transparency note)

The numbers below are for the current stdlib-only build (Go-AST + JSON
backends). They are **not** a grammar-laden build — they establish the CGo-free
baseline and the headroom against budget. Per-grammar size deltas will be
recorded as each tier-1 grammar is integrated.

- Toolchain: Go 1.26.3 (darwin/arm64), `CGO_ENABLED=0`.
- Default build (`go build -o ... ./cmd/graphi`): **3,587,714 bytes (~3.42 MiB)**.
- Reproducible build (`-trimpath -ldflags="-s -w"`): **2,421,186 bytes (~2.31 MiB)**.
- CGo linkage: **none** — `go version -m` reports `build CGO_ENABLED=0`.
- Whole-binary budget: **< 50 MB** → current headroom ≈ **47.6 MB** (trimmed).
- Parse-tier sub-budget: to be set as tier-1 grammars are added; the stdlib
  parse leaf currently contributes a negligible fraction of the binary.

Reproduce:

```sh
cd workspace/graphi
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/graphi ./cmd/graphi
ls -l /tmp/graphi
go version -m /tmp/graphi | grep CGO
```

## Consequences

- The parse boundary is in place and exercised end-to-end before any grammar is
  vendored, so grammar work is additive and low-risk.
- The default binary stays CGo-free and an order of magnitude under budget,
  preserving the local-first posture.
- Honest scope: the measured size reflects the stdlib build only; grammar deltas
  are a known, tracked follow-up, not silently implied here.
