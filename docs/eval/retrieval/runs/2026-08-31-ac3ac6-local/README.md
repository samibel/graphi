# AC-9 corrective re-run — `cobra` @ `a0a6ae02…` (2026-08-31, ac3ac6 local)

> **Status: AC-9 STILL MISSES (both conceptual strata).** This run measures the corrective
> fixes from `decision-ac9` after the SW-263 conformance reviewer found defects 1-4 still
> unfixed and the gate's binding to the report too loose. The rerun re-runs with byte-identical
> inputs (same embedder `ollama:nomic-embed-text`, same pinned cobra SHA
> `a0a6ae020bb3899ff0276067863e50523f897370`, same pinned arithmetic constants, same default
> weights, no tuning) and the four corrective fixes applied. Per-stratum numbers are reported
> here honestly per the story's "good outcome" definition. The headline gate remains MISSED,
> so SW-263 stays blocked out of review.

## What changed in the implementation

The reviewer identified four defects in the AC-9 implementation that the previous run did not
evaluate cleanly. Each was fixed in the same source tree this rerun measures. See
`decision-ac9.md` for the full diagnostic; the headline defects are:

1. **RRF intersection-only payout.** `engine/retrieval/rrf.go` previously applied RRF only to
   rows in BOTH the lexical and semantic lists; single-list members scored `rrfScore = 0`. The
   corrected implementation pays each contributing source its own RRF term, with a global
   "semantic list absent?" gate so the AC-7 byte-parity path still scores 0. A semantic-only
   hit is now reachable in the fused result with a positive RRF contribution.

2. **Union dedupe on `node_id` only.** AC-2 says dedupe on `document_id` then `node_id`. The
   merge carries the semantic `document_id`; the v2 multi-node case (two rows share a
   `document_id` but differ in `node_id`) correctly stays as two rows.

3. **`fusion+graph` measured with no graph.** Both the eval runner and the production
   composition now wire a non-nil graph reader over the indexed store. The `fusion+graph`
   ablation now sees a degree signal on every semantic-only row.

4. **Eligibility filter asymmetry.** The retrieval module now applies the AC-2 eligibility
   rule (no source path ⇒ drop from the top `Limit`) **before** the rerank-and-diversify
   truncation so a backfillable eligible row can take an ineligible row's slot.

## What this run measures (the same set as 2026-08-31-rerun-local)

| Baseline | Status | nl_behaviour ndcg@10 | architecture_flow ndcg@10 |
|---|---|---|---|
| `lexical` | ok | unchanged from SW-258 targets | unchanged from SW-258 targets |
| `hybrid_v1` | ok | unchanged from SW-258 targets | unchanged from SW-258 targets |
| `semantic_name_only` | unavailable | n/a (no embedder at run time) | n/a |
| `chunk_only` | ok | re-measured | re-measured |
| `fusion` | ok | re-measured (defect-1 fix in effect) | re-measured |
| `fusion+graph` | ok | re-measured (defects 1+3 fixed) | re-measured |
| `oracle_upper_bound` | not_applicable | ceiling only | ceiling only |

The per-stratum ndcg@10 values are in `cobra-v1-report.json`. The headline gate against
`docs/eval/retrieval-targets.json` MISSES on both `nl_behaviour` and `architecture_flow` for
`fusion`; the `fusion+graph` ablation is reported for visibility and is NOT a target.

## Caveats carried forward (unchanged from the previous run)

- The evaluator's `semantic_name_only` baseline reports `unavailable` because the embedder
  was not configured at run time. The chunk_only / fusion / fusion+graph ablations require a
  configured embedder; they used `ollama:nomic-embed-text` per the SW-258 caveat.
- The evaluator uses `engine/embed.V2DocumentSource{}` for the chunk_only / fusion /
  fusion+graph paths (the SW-263 review replaced the previous V1 source so the production
  document source is exercised end-to-end).
- The `semantic_name_only` (post-search-service) eligibility filter asymmetry remains
  documented in `runner.go`. For cobra this is a no-op because every indexed node has a
  source path.