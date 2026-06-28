# Contributing to graphi

Thanks for your interest in contributing! graphi is a local-first, CGo-free
code-intelligence engine. This guide covers how to build, test, and submit
changes.

By participating you agree to abide by our [Code of Conduct](./CODE_OF_CONDUCT.md).

## Ground rules

- **Local-first is non-negotiable.** The default `graphi` binary must stay
  CGo-free and must not introduce network egress at runtime. CI enforces both
  (the egress canary and the CGo-conformance gate). Changes that violate the
  [local-first contract](./readme.md#the-local-first-contract) will not be merged.
- **Layered architecture.** Imports may only flow `cmd → surfaces → engine → core`.
  Lower layers never depend on higher ones (`core/parse`, `core/graphstore` are
  pure leaves). The `layer-direction` CI guard fails any upward/sideways import.
- **Docs-vs-code parity.** The [capability coverage matrix](./docs/coverage-matrix.md)
  is CI-enforced — if you add or remove a parser, analyzer, MCP tool, or surface,
  update the matrix or the build breaks.

## Prerequisites

- Go (see the version pinned in [`go.work`](./go.work)).
- The repository is a Go workspace (`go.work`); `./...` honors the `use`
  directives, so all modules are included automatically.

## Build

```bash
# Default: static, CGo-free binary
CGO_ENABLED=0 go build ./...

# Optional opt-in flavors
CGO_ENABLED=1 go build -tags graphi_broad ./...    # broad Tree-sitter coverage (CGO_ENABLED=1)
go build -tags webui_embed ./...     # bundle the web UI into the binary
```

## Test

```bash
# Full suite, the way CI runs it (CGo-free)
CGO_ENABLED=0 go test ./...

# The privilege-aware expected-failure gate CI uses
go test -json ./... | go run ./cmd/testgate -stdin
```

Please add or update tests for any behavior change. Determinism and
byte-identical full-vs-incremental ingest are core invariants — keep them green.

## Submitting changes

1. Fork the repo and create a topic branch off `main`.
2. Make your change with focused commits and clear messages.
3. Ensure `go build ./...` and `go test ./...` pass under `CGO_ENABLED=0`.
4. Run `gofmt`/`go vet` and keep the diff minimal and in the style of the
   surrounding code.
5. Open a pull request using the PR template. Describe the change, the
   motivation, and how you verified it.

## Reporting bugs & requesting features

Use the GitHub issue templates. For security issues, **do not** open a public
issue — see [SECURITY.md](./SECURITY.md).
