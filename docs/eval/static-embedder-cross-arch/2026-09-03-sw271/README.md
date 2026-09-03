# SW-271 static-embedder cross-architecture record

Status: **BYTE-EXACT** for all 1,024 produced float32 components (four
inputs × 256 dimensions) between `darwin/arm64` and `darwin/amd64`.

Both executions used the same directory containing
`static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
`static-embedder-archcheck verify` recomputed all four SHA-256 pins before
either execution. The canonical JSON encodes every float32 by its exact
IEEE-754 bits; both files were byte-identical at SHA-256
`cc40f422aff6cf1cce6963e391149be0d6f21fcdd592d297469e06fd9f3c0434`.

## AC-5 artifact decision

This check chooses option (c): a single-producer CI artifact handoff, which
gives a stronger same-run guarantee than a cache alone. One `prepare-model` job
fetches the model once through the existing
`cmd/graphi/staticfetch` network boundary, verifies the four pins, then uploads
that one directory as a short-lived workflow artifact. Native `linux/amd64`
and `linux/arm64` jobs only download that CI handoff; neither can contact
HuggingFace, re-download on failure, or skip. A missing or invalid handoff makes
`verify`/`record` fail before any vector result exists.

A persistent GitHub cache keyed on the pin hashes was considered but not used:
caches are best-effort acceleration, so a miss still needs a producer and two
architecture-local cache restores do not by themselves prove a single handoff.
Relying only on SHA-256 verification of two separate downloads was also
considered: it proves equal file bytes cryptographically, but it is weaker than
giving both executions the same workflow artifact and unnecessarily makes the
comparison depend on two network acquisitions.

Application model egress remains confined to `cmd/graphi/staticfetch`; the new
recorder/comparator is offline. The workflow separately performs the ordinary
Go module acquisition used throughout CI.

## Local execution

Host: macOS 26.6.2 (build 25G83), Apple arm64. Go:
`go version go1.26.6 darwin/arm64`. The arm64 binary ran natively and the amd64
binary ran under macOS Rosetta using `arch -x86_64`. Both were built with
`CGO_ENABLED=0`; `file` identified them as Mach-O arm64 and x86_64 executables.

The commands below are the recorded procedure. `SW271_MODEL_DIR` must point to
the already-present pinned artifact; none of these commands downloads it.

```bash
: "${SW271_MODEL_DIR:?set to the verified pinned model directory}"
export GOFLAGS=-buildvcs=false
export CGO_ENABLED=0

go run ./cmd/static-embedder-archcheck verify -model-dir "$SW271_MODEL_DIR"
GOOS=darwin GOARCH=arm64 go build -o /tmp/sw271-static-archcheck-darwin-arm64 ./cmd/static-embedder-archcheck
GOOS=darwin GOARCH=amd64 go build -o /tmp/sw271-static-archcheck-darwin-amd64 ./cmd/static-embedder-archcheck

/tmp/sw271-static-archcheck-darwin-arm64 record \
  -model-dir "$SW271_MODEL_DIR" \
  -inputs docs/eval/static-embedder-cross-arch/2026-09-03-sw271/inputs.json \
  -out /tmp/sw271-vectors-darwin-arm64.json
arch -x86_64 /tmp/sw271-static-archcheck-darwin-amd64 record \
  -model-dir "$SW271_MODEL_DIR" \
  -inputs docs/eval/static-embedder-cross-arch/2026-09-03-sw271/inputs.json \
  -out /tmp/sw271-vectors-darwin-amd64.json

/tmp/sw271-static-archcheck-darwin-arm64 compare \
  -left /tmp/sw271-vectors-darwin-arm64.json \
  -right /tmp/sw271-vectors-darwin-amd64.json
```

Observed comparison output:

```text
static embedder cross-architecture vectors: byte-exact (4 inputs x 256 dimensions, sha256 cc40f422aff6cf1cce6963e391149be0d6f21fcdd592d297469e06fd9f3c0434)
```

`inputs.json` has SHA-256
`423e73ff774b6f2affe9555ff4267f5ef91274d9dada4d1f26106b511479c63c`.
`vectors.json` is the one canonical copy of the byte-identical result; the two
temporary per-architecture files had the same digest and are not duplicated in
the repository. `run.json` records the environments and all artifact hashes.

The CI continuation uses native `ubuntu-24.04` x64 and
`ubuntu-24.04-arm` arm64 runners, compares them with each other, then compares
the result with `vectors.json`. Any vector divergence exits non-zero after
naming the input id and text, component index, and both exact bit patterns. It
does not round, sort, or apply a tolerance.
