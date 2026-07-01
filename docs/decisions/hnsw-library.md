# Decision: HNSW implementation — hand-rolled minimal pure-Go (SW-084)

This is an architecture decision record: it explains why graphi's HNSW vector index
was implemented in-house rather than adopted from an existing library. It's for
contributors evaluating similar build-vs-vendor tradeoffs, or wondering why the
vector index has no third-party dependency.

**Status:** accepted · **Date:** 2026-06-26 · **Story:** SW-084 · **Risk:** high

## Context

EP-013 / SW-084 upgrades the FU-3 brute-force cosine vector store with an HNSW
(approximate-nearest-neighbour) index, added behind the existing graceful-skip seam
and OFF by default. The choice of *how* to get HNSW is the load-bearing decision,
because it drives three hard invariants: **byte-identical determinism**, **CGo-free
default build**, and **zero new supply-chain / license surface** (graphi's
zero-egress gate).

## Options considered

| Option | Determinism | CGo | Supply chain | Verdict |
|---|---|---|---|---|
| **A. Vendor a CGo HNSW** (e.g. hnswlib bindings) | RNG-seeded, hard to pin | CGo — violates default-build contract | new dep + native toolchain | ✗ rejected |
| **B. Vendor a pure-Go HNSW** (e.g. `coder/hnsw`, `Bithack/go-hnsw`) | RNG level assignment by default; would need forking to make deterministic | pure Go ✓ | new MIT/Apache dep → license-gate entry, audit burden, transitive risk | ✗ rejected |
| **C. Hand-rolled minimal pure-Go HNSW** (chosen) | full control — deterministic level via NodeId hash, canonical insertion order, NodeId tie-breaks | pure Go ✓ (stdlib only) | **zero new deps** | ✓ **accepted** |

## Decision

We implemented a **minimal hand-rolled HNSW** in `engine/embed/hnsw.go` using only the
Go standard library (`container/heap`, `math`, `sort`, `hash/fnv`). Rationale:

1. **Determinism is non-negotiable, and no library gives it for free.** Every
   off-the-shelf HNSW draws node levels from an RNG and is sensitive to insertion
   order. Making one of them byte-identical-deterministic would mean forking and
   maintaining it anyway — at which point a focused ~400-line in-tree implementation
   is *less* code to own than a vendored fork, and far easier to audit for the
   determinism invariant.
2. **Zero supply-chain delta.** No new module dependency means `go.mod` stays
   unchanged, `make license-check` has nothing to add, `LICENSES.md` needs no entry,
   and the zero-egress posture is preserved. This is the most conservative option for
   a high-risk story.
3. **CGo-free by construction.** The implementation is pure Go, so the default build
   links no cgo from this path (verified: `CGO_ENABLED=0 go build ./cmd/graphi`
   succeeds and the project `cgoconformance` gate passes).

## How determinism is achieved (the crux)

- **Level assignment** is a pure function of the NodeId: `FNV-1a(id) → u ∈ (0,1] →
  floor(-ln(u) · mL)`. No `math/rand`. Same node → same level on every run/process.
- **Insertion order** is always canonical NodeId-ascending (`Rebuild` consumes
  `VectorTable.Load`, which sorts; `Put` maintains the order). Construction is a pure
  function of the vector set, so full and caught-up-incremental indexes are identical.
- **All candidate/neighbor/result orderings** break ties by NodeId ascending, never by
  float address or Go map iteration. Final hits are `(score desc, NodeId asc)` —
  identical to the brute-force service contract.

## Consequences

- We own ~400 lines of ANN code (graph build + beam search), covered by determinism,
  recall@10, put-order-independence, and seam tests in `engine/embed/hnsw_test.go`.
- Recall is probabilistic and tunable via `ef_search`; brute-force remains the default
  and the recall oracle. Measured recall@10 is 1.00 on the 1 200-vector synthetic
  fixture at `ef_search=128` (bar: ≥0.95).
- If a future need (e.g. billion-scale corpora, SIMD distance) outgrows this
  implementation, a vendored library can be reconsidered — but only behind the same
  deterministic seam.
