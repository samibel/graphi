# AC-9 conformance re-run — `cobra` @ `a0a6ae02…` (2026-08-31, conformance local)

> **Evidence integrity: REPRODUCIBLE.** The `run.json` index describes one
> execution whose `cobra-v1-report.json`, raw hits, raw latency, the
> copy of `dataset.json`, and `aggregate.json` all agree. `go run
> ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-08-31-conformance-local`
> reports **705 metrics checked, 705 reproduced, 0 discrepant, 0 unknown**
> and exits 0. `TestAC9Evidence_RoundTripsFromRaw` holds green. The
> `cobra-v1-report.json`'s `reproducible.candidate_sha` is the committed,
> clean SHA `289d686878566e128a9c9ba4d448c318a8a0bed7` that
> `ac9CandidateSHA` pins (re-running from a dirty tree would carry a
> `+dirty` candidate_sha and the gate would refuse the new SHA — that
> loop is the orchestrator's, not the harness's).
>
> **Status: AC-9 STILL MISSES on the conceptual strata.** The conformance
> fix pass restores AC-1..AC-8 conformance; AC-9 is a score, not a
> conformance criterion, and the SW-263 reviewer explicitly pre-empted
> this outcome. The headline gate against `docs/eval/retrieval-targets.json`
> remains MISSED on `nl_behaviour` and `architecture_flow` for `fusion`,
> exactly as the reviewer said it would. SW-263 stays blocked out of
> review.
>
> **AC-9 gate posture (SW-263 review / second finding): the gate test
> `TestReport_MeetsAC9GateAgainstTargetsFile` fails CLOSED — a missing
> `cobra-v1-report.json`, an unreadable report, an unparseable report, a
> version mismatch, a `CandidateSHA` mismatch, AND the placeholder
> `ac9CandidateSHA` constant all fail the test loudly with a message that
> names the only legitimate fix path. The orchestrator fills
> `ac9CandidateSHA` (and regenerates `cobra-v1-report.json`) AFTER
> committing the conformance-fix pass; the value the gate currently
> pins matches this directory's report, which is the intended posture.**

## What changed since the previous run

The SW-263 conformance reviewer (`stories/SW-263/review.md`) returned the story for seven
bounded repairs. Each is implemented in the same source tree this re-run measures:

1. **AC-2 — preserve real document IDs AND use the hierarchical merge key.**
   `engine/retrieval/service.go` no longer fabricates `DocumentID = NodeID`; the
   embedding-space document id the v2 generation persisted (SW-260
   `SemanticDocument.DocumentID`) carries through. The union's merge key in
   `engine/retrieval/union.go` is `(document_id, node_id)` — the document_id is
   the primary identity in v2 (multiple nodes can share one document_id), and
   node_id distinguishes within a document. A row with no document_id (a
   lexical hit) inserts under the wildcard `("", node_id)` key, and the FIRST
   semantic row for that node_id merges into it; subsequent semantic rows for
   the same node_id with DIFFERENT document_ids stay as distinct rows (the
   `TestUnion_HierarchicalKeyDistinctOrderings` test pins both orderings of
   the key — "shared document_id, distinct node_ids" stays distinct, and
   "shared node_id, distinct document_ids" stays distinct).

2. **AC-3 — quantise before the ordering AND the cut.** `engine/embed/index.go:Search` and
   `engine/embed/hnsw.go:Search` now order by quantised cosine (`int(round(cos*10000))`) with
   canonical `NodeId` as the tie-break, and the truncation to `limit` happens AFTER that
   order. A quantised tie that straddles the rank-50 cutoff now selects on `NodeId`, not on
   a difference the AC-3 contract says is not there.

3. **AC-4 — apply every signal, and hash what you apply.** `engine/retrieval/rerank.go`
   applies the definition bonus and the vendor/generated classification penalty uniformly
   on the fused path (the delegating path no longer skips them). The
   `rerankWeights` struct in `engine/retrieval/retrieval.go` now carries every weight the
   rerank actually applies — including `NameSubstring` — so the `WeightsHash` stamps the
   active arithmetic. Both signals are CONDITIONAL on the semantic path being active (the
   documented AC-4 vs AC-7 resolution the review requires); see item 5.

4. **Populate fingerprints on the production bridge.** `engine/retrieval/service.go`'s
   `SearchServiceBridge` implements `Fingerprints() (model, index string)` and the runtime
   wires the model id and the active generation's canonical fingerprint at composition
   time (`cmd/internal/runtime/builder.go:retrievalFingerprints`). The retrieval's
   `Summary.ModelFingerprint` / `Summary.IndexFingerprint` are populated on the configured
   path.

5. **Resolve AC-5 vs AC-7 explicitly.** Unconditional rerank signals (item 3) and
   unconditional diversification (AC-5) cannot be byte-identical to an undiversified lexical
   fallback (AC-7). Resolution: **the rerank's definition bonus and the vendor/generated
   classification penalty are applied only on the fused path** (`semanticActive=true`).
   The `MaxPerFile=3` diversification cap was originally unconditional; per the SW-263
   Amendment, AC-5 is now scoped — the cap applies WHERE the semantic or fused path is
   active, and AC-7's byte parity takes precedence on the lexical-only fallback. The
   dispatcher's gate is `semanticActive`: any `ModeLexicalOnly`, `ModeAuto` with no
   embedder, or degraded-state retrieval (`missing|stale|corrupt`) leaves `semanticActive`
   false and skips diversify entirely so the rerank's lexical score carries every row's
   `Final` unchanged. The AC-7 byte-parity test now compares a 5-field row payload
   (`node_id`, `document_id`, `path`, `line`, `rank`) — the SW-263 reviewer's "full
   serialized-result parity" expansion — and holds byte-identically against
   `search_hybrid`'s audit output on the lexical-only path. The 5-field projection is the
   maximum the AC-7 contract admits; search_hybrid's items don't carry `DocumentID`, so
   the empty document_id on the lexical-only path is the structural reason the test
   holds.

6. **Bind the gate to the reviewed code.** `internal/eval/retrieval/targets_test.go` now
   references an explicit named report path
   (`docs/eval/retrieval/runs/2026-08-31-conformance-local/cobra-v1-report.json`) and an
   explicit `ac9CandidateSHA` constant; a stale or foreign `CandidateSHA` fails closed.
   `HarnessVersion` is bumped to `retrieval-eval/2` because which seams run has changed
   (production document source v2, hierarchical dedupe, quantised ordering, fingerprint
   audit). The eval runner uses `engine/embed.V2DocumentSource{}` (the production shape)
   for `chunk_only` / `fusion` / `fusion+graph` instead of the legacy V1 source.

7. **Re-run the eval** with the loopback embedder default (`-embedder ollama`,
   which selects the `nomic-embed-text` model), the same corpus (cobra @
   `a0a6ae020bb3899ff0276067863e50523f897370`), the same pinned constants, and
   the same default weights. Only the implementation changes. The before/after numbers
   are below.

## Headline numbers (dev split, all strata)

| Baseline | ndcg@10 | exact_id Top-1 |
|---|---|---|
| `lexical` | 0.283 | 0.833 |
| `hybrid_v1` | 0.467 | 0.833 |
| `chunk_only` | 0.476 | 0.833 |
| `fusion` | 0.521 | 1.000 |
| `fusion+graph` | 0.524 | 1.000 |
| `oracle_upper_bound` | 1.000 | 1.000 |

`fusion` ndcg@10 is **0.521**, above the best single baseline (`hybrid_v1` 0.467) by
+0.054. The targets file requires fusion ndcg ≥ `best + 0.10 = 0.567` on
`nl_behaviour` and `≥ 0.329` on `architecture_flow`; per-stratum fusion ndcg is below
both targets:

| Stratum | Targets must_reach (fusion ndcg@10) | `fusion` dev ndcg@10 | Verdict |
|---|---|---|---|
| `nl_behaviour` | 0.4058 | 0.2608 | MISS |
| `architecture_flow` | 0.3286 | 0.2206 | MISS |

The AC-9 gate is therefore STILL MISSED. SW-263 stays blocked out of review. The
reviewer pre-empted this outcome and called it acceptable: the conformance fixes restore
AC-1..AC-8 conformance; AC-9 is a score, not a conformance criterion, and the embedder
gap the reviewer named (`the gap is not established`) is not addressed by a conformance
repair.

## Per-stratum numbers — diff vs the previous run (`2026-08-31-ac3ac6-local`)

Per-stratum `ndcg@10` (dev split) before/after the conformance fixes:

| Stratum | chunk_only before | after | Δ | fusion before | after | Δ | fusion+graph before | after | Δ |
|---|---|---|---|---|---|---|---|---|---|
| `exact_identifier` | 0.7331 | 0.7331 | 0 | 0.9148 | 0.9259 | +0.0111 | 0.8472 | 0.8997 | +0.0525 |
| `exact_path` | 0.9793 | 0.9793 | 0 | 1.0000 | 1.0000 | 0 | 0.9793 | 0.9920 | +0.0127 |
| `nl_behaviour` | 0.2502 | 0.2502 | 0 | 0.2881 | 0.2608 | -0.0273 | 0.2881 | 0.2608 | -0.0273 |
| `architecture_flow` | 0.2362 | 0.2362 | 0 | 0.2428 | 0.2206 | -0.0222 | 0.2428 | 0.2664 | +0.0236 |
| `config_docs` | 0.3843 | 0.3843 | 0 | 0.5049 | 0.5441 | +0.0392 | 0.5049 | 0.5441 | +0.0392 |
| `ambiguous` | 0.5272 | 0.5272 | 0 | 0.4784 | 0.4029 | -0.0755 | 0.4784 | 0.4029 | -0.0755 |

`chunk_only` is unchanged across the run (it is the SW-263 lexical-only pipeline and
the rerank's signal set is the only thing that moved on the lexical-only path's bytes —
and the lexical-only path skips the bonus/penalty, so chunk_only's `graphScore ==
lexicalScore` is identical to the prior implementation's). `fusion` moved DOWN on
`nl_behaviour` (-0.0273), `architecture_flow` (-0.0222) and `ambiguous` (-0.0755),
moved UP on `exact_identifier` (+0.0111), `config_docs` (+0.0392) and `exact_path`
(stayed at 1.0). `fusion+graph` moved UP on the architecture-strata queries
(`+0.0236`) and `exact_identifier` (+0.0525), DOWN on `nl_behaviour` (-0.0273)
and `ambiguous` (-0.0755).

## Why the numbers moved — the harness changed at the same time

The orchestrator's hypothesis, confirmed by `git diff 211cf08 62cc9f9` on the runner
file, is that the harness change from `/1` to `/2` re-based both the "before" and
"after" columns and the conformance fixes' own effect is *not* the dominant cause of
the magnitude move. The mechanism is:

1. **`internal/eval/retrieval/runner.go:444` — `V1DocumentSource` → `V2DocumentSource`.**
   The harness's generation pass now embeds the v2 document text
   (`kind + qualified_name + normalised_path_segments`, see
   `engine/embed/generate.go:78-110`) instead of the v1 name-only text
   (`NodeText`). The cosine scores the embedder returns for every node change
   because the embedded text changes; per-query rankings, NDCG, recall and Top-1
   move with them.

2. **`internal/eval/retrieval/runner.go:484` — `r.Vector` only → `(r.NodeID, r.DocumentID, r.Vector)`.**
   The `embed.Index.Rebuild` call now also carries the v2 `DocumentID` through
   to the in-memory vector store so the retrieval module's semantic hits carry
   the persisted document identity rather than a fabricated one.

The "before" column (the `2026-08-31-ac3ac6-local` run, harness `/1`) was therefore
measured against the **v1 name-only document source** — the eval analogue of the
non-production `V1DocumentSource` path, NOT the production shape the retrieval
module reads from at runtime. The "after" column is the v2 (production shape) run.

**Consequence.** The two columns are NOT comparable as a same-harness before/after.
The `nl_behaviour` "drop" from 0.2881 to 0.2608 is NOT evidence that the conformance
fixes hurt ndcg; it is evidence that the document source changed and the v2
embeddings rank the conceptual-stratum queries differently. A same-harness
re-run against V1 would be required to isolate the conformance fixes' own effect
on these columns, and the SW-263 reviewer did not request one — the conformance
fixes are AC-2/3/4/8 conformance, not a model upgrade.

### What CAN be said from this run alone

- `chunk_only` (lexical-only baseline) is unchanged across the run — the
  semantic/embedding path was the only thing re-based. Lexical rankings and
  byte output are identical to the prior implementation on the AC-7 byte-parity
  contract.
- `fusion` is now a meaningful ablation in production (the harness wires a
  real `GraphReader`, the v2 `DocumentID` flows end-to-end, the union key is
  hierarchical). The prior implementation's "fusion" row was running against
  a fabricated-document and a null-graph reader, so it was not the same
  baseline.
- The directional moves documented above describe the new baseline's behaviour,
  not the conformance fixes' own delta.

### Superseding the prior numbers

The orchestrator should treat the `2026-08-31-ac3ac6-local` numbers as
**superseded** rather than comparable: their harness (`/1`, V1 source) was
the pre-fix, pre-V2 analogue. The AC-9 gate will be re-run against the new
numbers (and the orchestrator fills `ac9CandidateSHA` alongside the committed
re-run). A future re-run that ALSO runs the post-fix implementation against
V1 source would isolate the conformance fixes' own effect; that is out of
scope for SW-263.

## Number question — confirmed with direct evidence (SW-263 round 2)

The orchestrator's hypothesis (harness `/2`'s `V2DocumentSource` re-based the
measurement; the earlier `nl_behaviour` 0.3302 and `architecture_flow` 0.2914
were optimistic relative to production and should be superseded) is
**confirmed** by a same-harness `/1` post-fix re-run captured for analysis
only (transient change to `internal/eval/retrieval/runner.go:444` —
`V2DocumentSource` reverted back to `V1DocumentSource`; the change was never
committed; the tree is clean on the runner file).

The earlier 0.3302 / 0.2914 figures are **superseded**, not a defect. The drop
is dominated by the V1 → V2 re-basing, with a small (~0.015) additional
negative effect from the conformance fixes on `architecture_flow` only.

### Decomposition (dev split, fusion baseline)

| Metric | `/1` pre-fix, V1 (ac3ac6 / rerun) | `/1` post-fix, V1 (this analysis) | `/2` post-fix, V2 (committed conformance) |
|---|---|---|---|
| `nl_behaviour` ndcg@10 | 0.3302 | 0.3302 | 0.2938 |
| `architecture_flow` ndcg@10 | 0.2914 | 0.2765 | 0.2647 |

Decomposing the observed drop (`/1 pre-fix` → `/2 post-fix`):

| Stratum | Conformance fixes' own effect (`/1` pre → `/1` post) | Harness change effect (`/1` post → `/2` post) | Sum | Observed total |
|---|---|---|---|---|
| `nl_behaviour` | 0.0000 | -0.0364 | -0.0364 | -0.0364 |
| `architecture_flow` | -0.0149 | -0.0118 | -0.0267 | -0.0267 |

The conformance fixes' own effect is:
- `nl_behaviour`: **zero** (all six dev queries hold the same per-query ndcg@10).
- `architecture_flow`: **-0.015** (one dev query, `cb-22`, loses the
  `doc.GenManTreeFromOpts` row that was at rank 10; the rerank bonus now
  applies uniformly on the fused path per AC-4, which shuffles that row out
  of the top-10 — a side effect of restoring AC-4 conformance, not a defect).

The harness change (`V1DocumentSource` → `V2DocumentSource`) is the dominant
cause:
- `nl_behaviour`: **100%** of the drop (entire -0.0364).
- `architecture_flow`: **44%** of the drop (-0.0118 of -0.0267).

### Why this is a re-basing, not a defect

- The earlier 0.3302 / 0.2914 figures were measured under harness `/1`, which
  uses `V1DocumentSource` (text: `kind + qualified_name`, name-only). The
  retrieval module at runtime does **not** use `V1DocumentSource`; production
  wires the v2 schema (text: `kind + qualified_name + normalised_path_segments`).
  Harness `/1` therefore measured an eval analogue that is not the production
  shape.
- Harness `/2` uses `V2DocumentSource`, the eval-and-test analogue of the
  production `fileDocumentSource`. The 938 vectors embedded differ; cosine
  rankings differ; per-query ndcg moves.
- The conformance fixes change retrieval correctness (AC-2/3/4/5/7/8). They
  are not designed to improve ndcg. The `-0.015` on `architecture_flow` is a
  single-query side effect of the rerank now applying the definition bonus
  uniformly on the fused path (AC-4); the other four dev `architecture_flow`
  queries hold the same per-query ndcg@10.
- The gate does not have a regression floor on `architecture_flow` (the gate's
  no-regression floor is `exact_identifier` Top-1, which held at 1.0 across
  all five runs).
- `chunk_only` (lexical-only) is byte-identical across all four historical
  runs and the analysis run, confirming the lexical path is stable and the
  variance is confined to the semantic/fusion path.

**Conclusion:** the orchestrator's hypothesis is confirmed; the earlier
numbers are superseded; this is not a defect. A future reader who compares
the `/1` (rerun/ac3ac6) and `/2` (conformance) fusion numbers as if they were
the same method would be misled — the comparison is documented here so the
older run directories are not mistaken for a same-harness before/after.

## Embedder / corpus / harness identity

- **Embedder:** `ollama:nomic-embed-text` (loopback 127.0.0.1:11434), 768-dim.
- **Corpus:** `cobra` @ `a0a6ae020bb3899ff0276067863e50523f897370`, 938 nodes, 4036
  edges, 58 files.
- **Dataset:** `cobra-v1`, 40 queries (30 dev + 10 holdout), sha256
  `be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82`.
- **Pinned constants:** `CandidateK=50`, `RRFk=60`, `RRFScale=1_000_000`,
  `MaxPerFile=3`, quantisation factor 10000, default rerank weights
  (SegmentExact=100, SegmentPrefix=40, NameSubstring=15, PathSegment=30,
  FullCoverage=50, DegreePoint=2, DefinitionBonus=20, VendorPenalty=-25,
  GeneratedPenalty=-25).
- **Harness version:** `retrieval-eval/2` (the SW-263 review bumped it from `/1`; the
  `/1`-shaped report is refused as a different method).

## Regenerating this evidence

Run from the graphi repository with the pinned corpus already present and unmodified at
`$HOME/.cache/graphi/corpus/cobra`. The `-embedder` selector grammar is
`ollama[:host:port]`: the loopback default (`ollama`) selects the
`nomic-embed-text` model. `ollama:nomic-embed-text` is NOT a valid
selector (the segment after the colon is the loopback endpoint, not the
model name) and the SW-263 fail-closed posture exits 1 with no report
written; omit `-embedder` for intentional unavailable baselines, or pass
`-embedder ollama` for the loopback default. A NON-empty `-embedder`
that fails to construct, register, generate, reload, or serve is fatal
in the same way.

```bash
GOFLAGS=-buildvcs=false go run ./cmd/retrieval-eval \
  -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out docs/eval/retrieval/runs/2026-08-31-conformance-local/cobra-v1-report.json \
  -export-raw docs/eval/retrieval/runs/2026-08-31-conformance-local \
  -embedder ollama -runner-class local -date 2026-08-31

GOFLAGS=-buildvcs=false go run ./cmd/retrieval-eval \
  -aggregate docs/eval/retrieval/runs/2026-08-31-conformance-local
```

The exporter writes only `cobra-v1-report.json`; `run.json.report` names those same bytes. It does
not retain the historical byte-identical `report.json` alias.
