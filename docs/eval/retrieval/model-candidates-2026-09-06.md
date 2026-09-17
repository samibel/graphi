# Embedding-model candidates for graphi

Research date: 2026-09-06. Primary-source research and local code inspection;
no alternative model has been installed, activated or evaluated by this note.
Research was performed directly, without delegation, as requested. The research
skill's source-verification and written-record workflow was used; its background
agent suggestion was not followed because the user explicitly prohibited it.

## Recommendation

Test **Qwen3-Embedding-0.6B through local Ollama** first, keeping Potion as the
unchanged reference. This is an integration/operating-cost choice, not a claim
that Qwen already wins on Cobra. Use **CodeRankEmbed-137M** as the next code-focused
control if the first comparison is inconclusive. Do not replace the production
model pin or reinterpret historical gate results before measuring the candidate.

Ollama is already available on this machine and its loopback `/api/tags`
responded. Qwen was not among the installed models returned. Read-only hardware
inspection reports Apple M2 Max and 64 GiB RAM. These observations establish local
availability, not throughput, memory-at-inference, or deterministic output.

## Shortlist and source-backed facts

| Candidate | Verified properties | Fit for this task |
|---|---|---|
| **Qwen3-Embedding-0.6B** | Apache-2.0 model card; 0.6B parameters, 32K model context, up to 1024 dimensions, multilingual and code retrieval. The official Ollama `0.6b` tag lists Q8_0 and a 639 MB artifact. | First practical local candidate: existing loopback transport, no hosted API. A larger context is an input capacity, not permission to widen the output bundle. [Qwen card](https://huggingface.co/Qwen/Qwen3-Embedding-0.6B), [Ollama artifact](https://ollama.com/library/qwen3-embedding:0.6b). |
| **CodeRankEmbed** | MIT model card; 137M parameters and 8192-token context; specifically trained for code retrieval. Queries require its code-search instruction prefix; code documents do not use that query prefix. | Particularly informative control because it is Potion's teacher. Verified upstream inference uses SentenceTransformers; a compatible local serving backend still needs selection and validation, so this is not a drop-in static model. [Nomic card](https://huggingface.co/nomic-ai/CodeRankEmbed). |
| **Qwen3-Embedding-4B** | Same family with a 4B model, 32K context and up to 2560 dimensions. | A later capacity comparison, not the first experiment. More parameters do not establish more useful snippets per actor token. [Qwen family specifications](https://huggingface.co/Qwen/Qwen3-Embedding-0.6B#qwen3-embedding-series-model-list). |
| **voyage-code-4** | Vendor announced it on 2026-08-13 for coding-agent retrieval, available through hosted APIs at a listed $0.12 per million input tokens. | Relevant external quality candidate, but outside graphi's current no-off-box embedder contract. Do not route source through a local proxy to disguise off-box inference. [Voyage announcement](https://blog.voyageai.com/2026/08/13/voyage-code-4/). |

Jina's code-embedding model is technically relevant, but its public 1.5B model
card is marked `CC-BY-NC-4.0`; it is not the straightforward default choice for
an unrestricted developer product. This is a licensing flag for review, not a
legal determination. [Jina model card](https://huggingface.co/jinaai/jina-code-embeddings-1.5b).

No cross-vendor leaderboard numbers are used to declare a winner. Model cards
can use different task versions, retrieval directions, prompts and corpora.
The 639 MB Ollama artifact is not an estimate of peak inference RAM.

## Evidence that a model comparison is worthwhile

Potion's own card says it distills CodeRankEmbed into static embeddings. In
one table, its authors report CoIR average nDCG@10 of **39.08 for Potion-v2**
and **59.14 for CodeRankEmbed**. The same card warns that CoIR combines different
retrieval directions and tasks; the average is not a measure of developer
question answerability. The authors' general-purpose Potion-32M variants also
score below Potion-code-v2 in that table, so merely selecting a larger static
Potion is not the most evidence-backed next experiment.
[Potion's comparison and caveats](https://huggingface.co/minishlab/potion-code-16M-v2#results).

Inference: there is plausible quality headroom in replacing static embeddings
with a contextual encoder. This is not a prediction of our Cobra score or of
token savings. The implementation repairs already demonstrated gains without
changing the model; a model cannot fix a discarded source span by itself.
The existing local result remains the reference: architecture nDCG 0.4624671523,
exact-name Top-1 4/4, at least one complete grade-3 span in 33/40 bundles, and
mean complete-response cost 8018.55 cl100k tokens.
[Current development report](runs/2026-09-06-bundle-selection-dev/README.md).

## Integration findings that must precede a valid comparison

1. **Local HTTP is already supported without CGo in graphi.** The existing
   Ollama leaf uses Go's standard HTTP library and rejects non-loopback hosts.
   A transformer served by Ollama preserves `CGO_ENABLED=0` for graphi, but
   adds a separate inference process/native runtime. It is not the same
   deployment footprint as the self-contained static model.
   [Adapter](../../../engine/embed/ollama/ollama.go),
   [embedder contract](../../../engine/embed/embed.go).
2. **Configuration does not yet select arbitrary Ollama models.** The registered
   constructor passes its argument as an endpoint and hard-codes
   `nomic-embed-text`. `New(endpoint, model)` supports model choice internally,
   but `GRAPHI_EMBEDDER=ollama:qwen3-embedding:0.6b` is not a valid way to select
   Qwen in current code. Add an explicit backwards-compatible configuration
   path; do not relabel another model as `nomic-embed-text`.
   [Constructor and New](../../../engine/embed/ollama/ollama.go).
3. **Query and document preparation must be distinguishable.** Semantic search
   currently calls the same `Embed` method directly for a bare question. Qwen
   recommends a query instruction and no corresponding document instruction;
   CodeRankEmbed requires its own query prefix. Add an explicit query path
   with a fallback for existing providers, not a guess based on input contents.
   Pin the preparation policy in identity and pass the original question to
   lexical retrieval; prefixes belong only to the semantic model input.
   [Current query call](../../../engine/search/semantic.go),
   [Qwen usage](https://huggingface.co/Qwen/Qwen3-Embedding-0.6B#usage),
   [CodeRankEmbed usage](https://huggingface.co/nomic-ai/CodeRankEmbed#usage).
4. **A mutable model tag is not an artifact fingerprint.** The adapter's
   `dimDigest` and `dimCtx` fields currently have readers but no assignments.
   Bind the actual installed digest, effective context, dimension, quantization,
   serving version and preparation policy before building an index. Detect
   model changes during the run; fail rather than publishing mislabeled vectors.
   Ollama's model-list API exposes artifact digests. The existing admission
   identity is designed to invalidate generations when its inputs change.
   [Adapter Profile](../../../engine/embed/ollama/ollama.go),
   [admission identity](../../../engine/embed/admission.go),
   [Ollama model-list API](https://docs.ollama.com/api/tags).
5. **No silent truncation.** Keep the existing `truncate:false`; Ollama documents
   that this makes over-context inputs errors. Do not silently copy truncating
   model-card examples into graphi. Model input preparation must remain
   consistent with the stored document hash and the source admission contract.
   [Ollama embed API](https://docs.ollama.com/api/embed),
   [document admission](../../../engine/embed/admission.go).
6. **Baseline identity matters.** The harness's `semantic_name_only` is a
   model-dependent v1-NodeText embedding control, not a model-free exact string
   lookup. Its scores can change with an embedder switch. Preserve the frozen
   thresholds for this experiment and report new-model comparator scores
   separately; do not re-derive a more convenient target. The default static
   pin remains untouched. Its eventual rotation has separate governance and
   must not be smuggled into an opt-in experiment.
   [Runner controls](../../../internal/eval/retrieval/runner.go),
   [pin governance](../../../engine/embed/static/PIN_ROTATION.md).

## Bounded experiment

Keep the existing development-only dataset, all 44 records, existing ranking
scorer, current retrieval weights, snippet-selection algorithm, 40-item default
and 1200 snippet budget. Report the 40 grade-3 source population separately from
the 41 scored ranking records, as in the current report. No holdout use.

First compare Potion with one pinned Qwen-0.6B artifact and one fixed,
model-appropriate query instruction. Rebuild independent indexes; old vectors
cannot be reused. Before calling this a model-only comparison, verify that both
models received the same canonical source documents, with any prescribed
model-specific preparation explicitly recorded. Different admission policies
can otherwise change content as well as the encoder. Report any such difference
as a model-plus-input-preparation comparison, not an isolated model effect.

Collect the existing full baseline reports/raw hits, source overlap and complete
span retention, full MCP cl100k counts, index time, query p50/p95, memory and
vector-storage size. Repeat captures across independent indexes and require
byte/digest/token equality. A served transformer may fail that requirement;
do not mask it by dropping precision or excluding disagreeing responses.

Use the current successful exact-name and architecture values as regression
references, while retaining the frozen target checks. A higher ranking score
alone is not sufficient to adopt the model: the actual bundles must improve
without an unacceptable latency/cost increase. If 0.6B fails, preserve the
negative result before trying CodeRankEmbed or 4B. Do not run a large hidden
model/prompt grid against this small development set.

## Recheck the local observations

These checks are read-only and make no embedding requests:

```sh
command -v ollama
sysctl -n hw.memsize machdep.cpu.brand_string
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:11434/api/tags
rg -n 'defaultModel|New\(arg|dimCtx|dimDigest' engine/embed/ollama/ollama.go
rg -n 'emb.Embed' engine/search/semantic.go
```

Research changed only this note. No model download, API account, external source
upload, production setting, frozen threshold, pin or evaluation artifact was
changed. Implementation and the model comparison are the next work, not results
claimed by this research.

Implementation follow-up: the subsequent local model trial, including failed
attempts and the unchanged Potion control, is recorded separately in
[the Qwen development experiment](runs/2026-09-06-qwen-dev/README.md). Its
observations supersede speculation about local behavior, not the historical
scope of this research note.
