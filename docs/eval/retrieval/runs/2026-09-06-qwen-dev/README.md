# Qwen development trial: architecture improves, bundle utility regresses

This is a local development experiment, not a model-pin rotation or a release
pass. Qwen3-Embedding-0.6B is installed and selectable, but no persistent
configuration or default model was changed. The existing Potion reference is
rerun, not substituted with external benchmark numbers.

The complete Qwen CPU ranking run succeeds. Architecture improves, but overall
ranking declines, especially on configuration/how-to questions. This is not an
unconditional upgrade. The completed two-independent-index MCP capture is
byte-reproducible but retains less answer source: at least one complete
grade-3 span falls from 33/40 questions to 27/40. Recommendation: retain Potion
for the current pipeline; keep this Qwen configuration experimental.
The failed attempts below are retained as failures, not converted to zero-quality
scores or successful empty reports.

## Scope and identity

- Clean Cobra checkout: `a0a6ae020bb3899ff0276067863e50523f897370`.
- Only the existing dev slice is queried:
  `../2026-09-06-sw282-gate-local/dataset.json`, SHA-256
  `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
  All 44 records remain: 40 grade-3-answerable, one grade-2-only and three
  `no_hit`. Ranking scores 41; grade-3 source diagnostics explicitly score 40.
- Working-tree candidate: `5d0ef8729be141e901e996d8660ec5bfe6dd182d+dirty`.
  These are development artifacts, not clean-commit-bound release evidence.
- Reference: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.
- Candidate: Ollama `qwen3-embedding:0.6b`, full manifest SHA-256
  `ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d`,
  Q8_0, 1,024 dimensions. Downloaded artifact size: 639,150,858 bytes.
- Local server version: `0.33.3` (from `/api/version`, not the older CLI
  version). Requested context: 8,192 input tokens; `truncate:false`.
- Hardware: Apple M2 Max, 64 GiB. CPU mode requests `num_gpu=0,num_thread=1`;
  `/api/ps` confirmed zero VRAM allocation for that mode. The daemon was not
  restarted and its global settings were not changed.
- Retrieval remains `retrieval/5`, source selection `context-definitions/2`,
  tool `task_context/2`, frozen snippet argument 1,200. The scoring rules,
  repetitions and thresholds are unchanged. No held-out retrieval or rating
  was run, and no generative answer model participates in these measurements.

## What changed in the implementation

1. An optional `embed.QueryEmbedder` interface lets an adapter prepare queries
   differently from source documents. Search calls one engine-owned helper;
   existing symmetric providers retain their exact path. The Qwen adapter
   applies one fixed instruction, versioned as `qwen3-code-v1`:

   ```text
   Instruct: Given a question about a code repository, retrieve relevant source code that answers the question.
   Query: <unaltered question>
   ```

   No development question or answer identifier is special-cased. Documents
   receive no instruction prefix. This keeps model-specific preparation inside
   the provider instead of spreading it through retrieval and MCP surfaces.
2. Explicit Ollama selectors require the model, manifest digest, context,
   runtime version and query profile. Optional compute placement is part of
   the identity too. Legacy `ollama[:host:port]` keeps its original model.
   Construction/reload remains zero-I/O. Before and after embedding a batch,
   including a search query, installed identity is checked. Persistent retags,
   runtime drift, invalid dimensions and redirects fail closed. This trusts
   the local daemon: Ollama does not atomically attest a digest in each vector
   response, so these checks cannot prove absence of a concurrent ABA retag.
3. Build, production reload, status and both evaluation paths now use the same
   `FingerprintFor` helper. Production reload and the ranking runner previously
   reconstructed only part of the provider fingerprint. The runner also ignored
   the returned readiness state. It now requires `ready` and full fingerprint
   equality, rather than serving a generation reported stale. A production
   roundtrip test binds all provider metadata and still requires zero reload
   calls to the embedder. The fresh Potion reference verifies no result drift.
4. The dev-only real MCP capture accepts an explicit experimental provider and
   records node/text-hash/source-range manifests for both independent builds.
   This makes input-admission differences visible: Potion has a 512-token
   model-input cap while Ollama uses server-side admission plus the existing
   16 KiB resource cap. A provider comparison is not automatically an isolated
   change of weights; query preparation and admitted text can differ too.

The graphi build remains `CGO_ENABLED=0`. Ollama is an additional local runtime.
Model installation downloaded public weights; source-embedding and search
requests stayed on loopback.

## Failed attempts and determinism diagnosis

The first automatic/GPU run built the source and name-only vector indexes but
the existing harness stopped at `semantic_name_only`, query `cb-03`
(`GenMarkdownTree`), because executions 1 and 2 differed. No complete report
was produced. `sameRanking` compares full internal hit structs, including
floating-point scores: this error alone does **not** prove reordered hits.

The opt-in live probe isolates the provider: five repetitions of three
interleaved fixed inputs, exact float32 vectors, no rounding or cache added by
graphi. Its preserved vectors establish the numerical difference directly:

| Probe | Identical vectors | Maximum absolute component difference |
|---|---:|---:|
| Automatic placement / GPU, `determinism-default.json` | No | 0.00014276057481765747 |
| Single-thread CPU, `determinism-cpu.json` | Yes | 0 |

Changing compute policy isolated a useful candidate condition, not the precise
kernel responsible: GPU placement and thread policy changed together. Three
short inputs do not establish whole-corpus or cross-platform reproducibility.
No scoring tolerance, vector quantization workaround, query cache, reduced
repeat count or determinism-gate exemption was introduced.

The first full CPU attempt hit the existing 30-second per-request deadline
during source embedding; see `attempt-cpu30.log`. The retry allows 120 seconds
per CPU HTTP request. Auto/legacy remain at 30 seconds. Only the request
deadline changed, not source admission or the output budget.

The 120-second CPU run completed with 768 source-document vectors, zero failed
documents, and a separate 768-vector name-only control. Every baseline ran all
44 queries with the original three executions each. The existing exact
repeatability check passed; `qwen-cpu120/aggregate.json` reproduces 761/761
metrics with no discrepancy or unknown. The CLI log is `attempt-cpu120.log`.

The initial test-only MCP capture stopped before embedding because the test
binary had not imported the Ollama provider registration, which the CLI already
imports (`attempt-capture-registration.log`). The dev diagnostic now imports
that constructor explicitly; registration itself performs no I/O. There was
no fallback, empty-population pass, or quality score from that failed attempt.

## Before/after ranking on the same development questions

| Metric | Potion | Qwen CPU | Frozen requirement |
|---|---:|---:|---:|
| Architecture nDCG@10, 5 | 0.4624671523 | 0.5172262341 | 0.4578575263 — both PASS |
| NL behaviour nDCG@10, 6 | 0.7029047224 | 0.7171075873 | 0.5449702531 — both PASS |
| Exact identifier Top-1, 4 | 4/4 | 4/4 | 4/4 — both PASS |
| Config/docs nDCG@10, 19 | 0.4358348732 | 0.2986363031 | — |
| Exact path nDCG@10, 3 | 1.0 | 1.0 | — |
| Ambiguous nDCG@10, 4 | 0.4216634707 | 0.4266141960 | — |
| Overall nDCG@10, 41 scored | 0.5645491768 | 0.5107082184 | — |
| Overall Top-1 | 22/41 | 22/41 | — |

Architecture gains 0.05476, while overall nDCG loses 0.05384. The unchanged
frozen architecture threshold is applied to both. For context only, Qwen's own
name-only comparator is 0.3975776203; the candidate also exceeds that value
plus 0.10. No target file was rederived or replaced.
Of the 41 scored questions, nDCG improves for 12, declines for 21, and is
unchanged for eight. These counts come from the existing per-query scores;
`ranking-delta.jq` checks population equality and subtracts them, without
introducing a new scorer.

The regression is concrete, not an inference from model size. For `ci-1408`
(global flags in subcommands), Potion's first three hits are `Command.Flag`,
`Command.Flags`, and `Command.persistentFlag`. Qwen's are a version-flag test
and the local-flag documentation headings; nDCG falls from 0.84425 to 0.04730.
For `ci-1991` (parent flag values), nDCG falls from 0.68917 to 0.08168 with a
similar move from flag accessors to documentation/tests. Even within the
improved architecture aggregate, `cb-19` falls from 0.60238 to 0.28301.
These examples establish changed candidate ordering, not that test/doc hits
are intrinsically useless or that the entire loss is caused by the weights
alone. The input-manifest and bundle results below quantify those limitations.

## Actual MCP bundles and independent rebuilds

`bundles-qwen-cpu.json` preserves both full populations. The live capture passed
with 768 documents in each new index, distinct internal freshness generations,
and **44/44 identical MCP byte strings, payload SHA-256 digests and tokenizer
counts**. Every emitted snippet roundtripped against its cited source lines;
every response stayed within the unchanged 1,200 snippet-token argument.
The two-build capture took 1,705.85 seconds, including indexing and all 88 MCP
captures, not just inference. See `capture-cpu.log` for the observed completion.
This is local same-machine reproduction, not a cross-platform guarantee.

| Source diagnostic, same 40 grade-3-answerable queries | Potion | Qwen CPU |
|---|---:|---:|
| Any emitted source overlapping grade 3 | 36/40 | 31/40 |
| At least one complete grade-3 span | 33/40 | 27/40 |
| Every grade-3 span complete | 21/40 | 19/40 |
| Complete grade-3 spans | 39/63 | 29/63 |
| Mean snippet whitespace tokens | 1107.60 | 1007.70 |
| Mean entire MCP response cl100k tokens | 8018.55 | 7946.90 |
| Independent-build byte/digest/token equality | 44/44 | 44/44 |

Qwen saves only 71.65 complete-response tokens on this population while losing
ten complete answer spans. This is **not** an equal-recall savings claim.
No blind answer sufficiency was rated. The 1,200 argument still limits source
snippet whitespace tokens, not the approximately 8,000 real tokens charged
for the complete serialized response; neither counting rule was altered.

Architecture is a genuine but narrow benefit: complete spans improve from
5/7 to 6/7, with both persistent-flag spans now complete for `cb-20`. At least
one complete architecture span remains present for all five questions. Across
the full population, none of the four original zero-overlap misses is repaired.
Five new zero-overlap misses appear: `cb-35`, `ci-467`, `ci-943`, `ci-1236`,
and `ci-2314`. Qwen's nine misses also include the original `ci-511`, `ci-678`,
`ci-771`, and `ci-1222`. Per-query changes are derived by the existing
`bundle-selection-dev/changes.jq`, not hand-edited scores.

The candidate diagnostic points upstream of snippet allocation: none of these
nine misses has a grade-3 point match within the first 15 retrieved rows.
Examples: `cb-35` first matches at 42, `ci-943` at 25, and `ci-2314` at 17;
`ci-467`, `ci-511`, and `ci-678` have no match even within 50. Those positions
are preserved in `grade3_ranks_in_50_candidates`. This uses the existing
`SpanMatches` rule on each row's start line, not a proof that every potentially
answer-bearing enclosing declaration is absent. Merely allocating deeper
snippets to the same leading candidates is not supported as a complete fix.

Input control: both providers embed the same 768 node IDs, and each provider's
two input manifests agree. However, **58/768 admitted text hashes differ
between providers**. Thus this compares the complete configured providers
(weights, query instruction, tokenizer/admission and serving runtime), not
weights in isolation. No claim that Qwen is universally worse follows.

For the requested developer/AI product, the measured configuration is not a
replacement: extra architecture margin does not compensate for more missing
answer source overall. Retain the existing default/configuration. Further
work should investigate candidate admission and evidence selection on the
remaining development misses, and the large serialized metadata cost.
Do not route by evaluation stratum or hardcode the questions to combine the
winning rows. A model/input-preparation ablation would be a separate trial.

## Fresh Potion reference

`potion/` contains the complete seven-baseline run and raw observations.
`-aggregate` reproduces 761/761 published metrics with no discrepancy or
unknown metric. This verifies reproduction, not release quality.

| Reference metric | Fresh value | Frozen requirement |
|---|---:|---:|
| Architecture nDCG@10, 5 dev queries | 0.4624671523 | 0.4578575263 — PASS |
| NL behaviour nDCG@10, 6 | 0.7029047224 | 0.5449702531 — PASS |
| Exact identifier Top-1, 4 | 4/4 | 4/4 — PASS |
| Overall nDCG@10, 41 scored | 0.5645491768 | — |
| Overall Top-1 | 22/41 | — |

The new `bundles-potion.json` has byte-identical MCP payloads to the previous
`../2026-09-06-bundle-selection-dev/bundles-after.json` for every one of the 44
queries, not merely equal aggregate scores. Its two independent indexes also
agree on all 44 payload bytes, SHA-256 digests and exact tokenizer counts.
Fresh source diagnostics: overlap 36/40, at least one complete grade-3 span
33/40, every span complete 21/40, complete spans 39/63. Mean snippet cost is
1,107.60 whitespace tokens; mean complete-response cost is 8,018.55 cl100k
tokens. These are source-retention diagnostics, not blind answer sufficiency
or equal-recall token savings.
All seven `potion/raw/hits-*.json` files are also byte-identical to the previous
`bundle-selection-dev/after/raw/` reference.

The full target check is still not a release PASS: the historical blind smoke
remains red; historical bundle coverage is not fresh candidate validation.
Local runs overlapped reference/testing work. Reported process RSS excludes
the Ollama daemon, and `index_ms` measures graph ingestion, not end-to-end
embedding build time. These fields must not be used as a total-resource or
isolated-speed comparison between providers.

## Reproduce

Use a new output directory; do not overwrite the preserved observations. The
pinned Cobra checkout, Potion artifact, Qwen installation and matching Ollama
server must already be present. Indexing and querying do not download models.

```sh
export CGO_ENABLED=0
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/pinned/cobra
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact"
qwen_selector='ollama:127.0.0.1:11434?model=qwen3-embedding:0.6b&digest=ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d&context=8192&runtime=0.33.3&profile=qwen3-code-v1&compute=cpu'
model_run=$(mktemp -d)

GRAPHI_OLLAMA_TEST_SELECTOR="$qwen_selector" \
GRAPHI_OLLAMA_TEST_OUT="$model_run/determinism.json" \
go test ./engine/embed/ollama -run '^TestPinnedLiveDeterminism$' -count=1 -v

# The ordinary complete seven-baseline run, with all original repetitions.
go run ./cmd/retrieval-eval -repo cobra -checkout "$GRAPHI_RECOVERY_COBRA" \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder "$qwen_selector" -runner-class local -date 2026-09-06 \
  -out "$model_run/report.json" -export-raw "$model_run/ranking"

# Reference command: use the same arguments and a different output directory,
# replacing only -embedder with the static selector above.

# Two independent indexes, all exact MCP bytes, strict equality; no fallback.
GRAPHI_RECOVERY_EMBEDDER="$qwen_selector" \
GRAPHI_RECOVERY_OUT="$model_run/bundles.json" \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' \
  -count=1 -timeout=60m -v

run_dir=docs/eval/retrieval/runs/2026-09-06-qwen-dev
go run ./cmd/retrieval-eval -aggregate "$run_dir/potion"
go run ./cmd/retrieval-eval -aggregate "$run_dir/qwen-cpu120"
jq -s -f "$run_dir/ranking-delta.jq" \
  "$run_dir/potion/cobra-v2-dev-report.json" \
  "$run_dir/qwen-cpu120/cobra-v2-dev-report.json"
go test ./internal/eval/retrieval -run '^TestRecoveryDevArtifactPayloads$' -count=1
jq -n -f docs/eval/retrieval/runs/2026-09-06-bundle-selection-dev/metrics.jq \
  "$run_dir/bundles-potion.json" "$run_dir/bundles-qwen-cpu.json"
jq -s -f docs/eval/retrieval/runs/2026-09-06-bundle-selection-dev/changes.jq \
  "$run_dir/bundles-potion.json" "$run_dir/bundles-qwen-cpu.json"

# Same node population, but 58 admitted source texts differ between providers.
jq -s '.[0].input_documents[0] as $a | .[1].input_documents[0] as $b |
  {before_count:($a|length), after_count:($b|length),
   same_node_universe:([$a[].node_id]==[$b[].node_id]),
   before_builds_match:(.[0].input_documents[0]==.[0].input_documents[1]),
   after_builds_match:(.[1].input_documents[0]==.[1].input_documents[1]),
   changed_text_hashes:([$a[] as $d | $b[] |
     select(.node_id==$d.node_id and .text_hash!=$d.text_hash)]|length)}' \
  "$run_dir/bundles-potion.json" "$run_dir/bundles-qwen-cpu.json"

# Expected exit 1: current ranking passes, historical blind smoke fails.
go run ./cmd/retrieval-eval -check-targets "$run_dir/potion/cobra-v2-dev-report.json"
go run ./cmd/retrieval-eval -check-targets "$run_dir/qwen-cpu120/cobra-v2-dev-report.json"
```

For the automatic-placement probe/run, remove `&compute=cpu` from the selector.
The historical 30-second CPU attempt predates the timeout change and is not
reproduced by the final 120-second CPU adapter. Its failure log is evidence of
that intermediate attempt, not a claim about the final request deadline.

## Validation and unchanged controls

Final artifact-tree validation passed: all 136 test-bearing packages under
`CGO_ENABLED=0 go test ./...`, the CGo-free build, layerguard, focused provider
and production-reload tests, and `git diff --check`. The historical quality
target check remains exit 1 as disclosed above; test success is not a release
approval.

The offline artifact test validates the complete preserved MCP bytes, digests,
real-tokenizer counts, source-overlap/containment recounts, frozen snippet
budget, complete populations, and both 768-document input manifests. The live
capture additionally checked every snippet against the clean pinned checkout.
Default `go test` does not secretly run live model evaluations: their explicit
environment opt-ins are shown above, and their actual records are preserved.

```sh
CGO_ENABLED=0 go test ./engine/embed/... ./engine/search ./cmd/internal/runtime -count=1
CGO_ENABLED=0 go test ./internal/eval/retrieval -run '^TestRecoveryDevArtifactPayloads$' -count=1
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go run ./cmd/layerguard
git diff --check

# Hash checks only; do not open or run held-out queries.
shasum -a 256 docs/eval/retrieval/methodology.md \
  docs/eval/retrieval-targets.json \
  internal/eval/retrieval/testdata/datasets/cobra-v2.json \
  docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json
```

Unchanged SHA-256 values:

- Methodology: `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d`.
- Frozen targets: `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d`.
- Full sealed dataset: `7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc`.
- Dev slice: `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.

The model and tokenizer pin tables are unchanged. Their dependency inventories
now include this directory's Potion reference. No commit, push, persistent
model selection, release approval or held-out evaluation was performed.
