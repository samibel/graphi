# Engineering change records

Point-in-time **before/after records** of completed engineering slices: what the
codebase looked like before the change, what changed, and why. They are kept for
contributors tracing how a subsystem got to its current shape — they are **not**
maintained as living documentation, so where a record disagrees with the code or
with the live docs under [`docs/`](..), the record is the historical one.

Current behavior is documented in [`docs/architecture-plan.md`](../architecture-plan.md)
and the [capability coverage matrix](../coverage-matrix.md).

| Record | Slice |
|---|---|
| [parse-registry.md](parse-registry.md) | Introduction of the pluggable parse registry (`core/parse`) |
| [symbol-extractor-seam.md](symbol-extractor-seam.md) | The `SymbolExtractor` seam, tree-sitter mapping helper, `PendingRef` contract |
| [typescript-extractor.md](typescript-extractor.md) | TypeScript — the first curated language over the seam, and the recipe the rest followed |
| [tier1-languages.md](tier1-languages.md) | Completion of the curated tier-1 pure-Go language set |
| [ep009-consolidation.md](ep009-consolidation.md) | EP-009 convergence: single `bench-budget.yml` re-pin (SW-057) |
