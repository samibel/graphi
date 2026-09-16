# Embedded-model release recovery design

Date: 2026-09-16
Status: design and written spec confirmed in chat; implementation not started

## Decision

GrapHi will keep its daemonless default and retain Potion as the fast local
embedding profile. It will test CodeRankEmbed as a separately installed,
explicitly selected, loopback-only quality profile. CodeRankEmbed will first be
integrated through a development-only adapter. It becomes a public product
profile only if a preregistered paired development experiment proves that its
quality gain survives candidate generation, ranking, projection, serialization,
and blind grading.

The spent holdout result remains `47/64`, `RELEASE: NO`. Its threshold will not
be changed and it will not be reused for candidate selection. A release can be
authorized only by a newly frozen candidate on a newly curated independent
holdout.

This design is based on
[`2026-09-16-embedded-model-holdout-feasibility.md`](../../eval/retrieval/research/2026-09-16-embedded-model-holdout-feasibility.md).
That research makes the current embedded model a plausible contributor, not an
established sole cause. The design therefore measures every stage rather than
assuming that a model replacement fixes the final bundles.

## Problem

The current qrel-blind holdout passed 47 of 64 answerable queries. The release
gate requires at least 56 of 64, so the candidate needs nine more passing whole
queries. The losses are concentrated in `ambiguous`, `architecture_flow`, and
`nl_behaviour`. Repairing every `ambiguous` loss alone would reach only 55 of
64, so a viable candidate must improve at least two weak strata.

Development evidence does not identify one binding cause. The same Potion
weights produced substantial gains after candidate-ranking and projection
changes, while a previous larger-model trial did not improve final bundle
utility. The release problem is therefore a transfer and end-to-end evidence
problem, not merely a ranking-score problem.

## Fixed product and evaluation contracts

The following constraints do not change:

- The standard GrapHi binary remains buildable with `CGO_ENABLED=0`.
- No embedder or model is shipped, activated, downloaded, or contacted without
  explicit operator opt-in.
- GrapHi performs no automatic non-loopback model access.
- Model, revision, runtime, tokenizer, query preparation, precision, dimension,
  and admission behavior are reproducibly pinned.
- A durable fingerprint change invalidates the semantic generation and requires
  re-indexing.
- The primary bundle ceiling remains exactly 1,200 `cl100k_base` tokens.
- The release population remains `N=64`, with the preregistered threshold
  `k=56`.
- `RELEASE: YES` requires both at least 56 passing queries and every other
  preregistered stratum, reproducibility, operating-budget, and run-validity
  gate to pass.
- No override, waiver, post-hoc threshold change, or reinterpretation of a
  failed holdout is permitted.

## Product profiles

The standard product remains fully daemonless. With no configured embedder,
semantic search retains its current typed-unavailable behavior and all lexical
capabilities remain available.

Potion remains the fast, daemonless opt-in embedding profile. CodeRankEmbed may
become an additional opt-in quality profile backed by a separately installed
local process. The standard binary contains only a pure-Go client adapter; it
does not contain the model or its inference runtime and does not start or update
the sidecar automatically.

CodeRankEmbed is never an automatic fallback for Potion, and Potion is never an
automatic fallback for CodeRankEmbed. An operator selects one embedding space
explicitly.

## Evidence architecture

### Frozen development population

Before any result is opened, an independent curator will produce and review a
new holdout-shaped development population of 64 queries with the same stratum
counts as the release holdout:

| stratum | count |
|---|---:|
| `ambiguous` | 10 |
| `architecture_flow` | 11 |
| `config_docs` | 10 |
| `exact_identifier` | 11 |
| `exact_path` | 11 |
| `nl_behaviour` | 11 |

The source checkout, questions, strata, answerability decisions, qrels,
complete grade-3 spans, scoring code, model configurations, query preparation,
admission profiles, reader and grader prompts, random seeds, and promotion
rules are frozen before the first comparison. The curator does not inspect
candidate outputs while authoring or reviewing the population.

Only one preregistered CodeRank configuration is tested. There is no hidden
model, prompt, pooling, context, or ranking grid against this population.

### Comparison arms

All arms use identical source nodes, candidate limits, ranking constants,
`compact/17` selection, exact 1,200-token serialization, prompts, readers, and
graders.

| arm | purpose |
|---|---|
| `M0` lexical-only | Negative control for the dense signal. |
| `M1` Potion/512 | Current pinned Potion weights and production 512-token admission profile. |
| `M2` Potion/8192 | Diagnostic-only use of the same Potion weights with a separately fingerprinted 8,192-token admission profile and unchanged node boundaries. |
| `M3` CodeRankEmbed | Pinned contextual teacher, pinned runtime and tokenizer, document input without a query prefix, and one fixed versioned code-search query instruction. |

`M2` is not a release candidate. It distinguishes model-weight limitations from
the local Potion admission profile. If its implementation cannot preserve
canonical node boundaries and exact admitted-byte provenance, the experiment is
invalid rather than silently changing the diagnostic.

### Paired measurement boundaries

For every query and arm, the evaluation records:

1. presence and best rank of a judged candidate in the semantic top 50;
2. presence and rank after lexical/semantic fusion and graph expansion, before
   the final cut;
3. presence of a complete grade-3 span in the exact serialized 1,200-token
   bundle; and
4. the final whole-query result under the existing blind reader and grader
   method.

The report includes whole-query paired deltas, candidate overlap, admission
truncation, unknown-token and zero-vector rates, and results overall and by
preregistered stratum. The primary inferential result is the paired `M3-M1`
whole-query pass delta. Its deterministic percentile bootstrap uses 100,000
query-level paired resamples and a preregistered seed; the two-sided 95% interval
must exclude zero in the positive direction.

Per-stratum results are directional promotion gates, not separate significance
claims. No multiplicity-adjusted per-stratum hypothesis tests are used.

### Stage-ceiling controls

Qrels may be used only after regular capture to construct these evaluation-only
controls:

| control | question answered |
|---|---|
| Current candidates plus oracle 1,200-token packer | Did retrieval find the evidence but projection omit it? |
| Oracle relevant candidate plus current selector | Can the current selector preserve the answer when retrieval is made perfect? |
| Oracle relevant candidate plus oracle 1,200-token packer | Is the fixed representation and budget itself the ceiling? |

Oracle information never enters a production candidate, query rewrite, ranker,
selector, or normal capture. Oracle bundles receive the same blind grading when
the control is used to estimate final answer sufficiency.

## CodeRank promotion gate

CodeRankEmbed advances to product integration only when every condition below
passes:

1. `M3` achieves at least 56 of 64 blind development passes.
2. `M3` achieves at least nine additional paired whole-query passes over `M1`.
3. The preregistered paired 95% interval excludes zero in the positive
   direction.
4. `M3` has a positive within-stratum net whole-query gain over `M1` in at
   least two of `ambiguous`, `architecture_flow`, and `nl_behaviour`.
5. `M3` has no negative within-stratum net whole-query delta in `config_docs`,
   `exact_identifier`, or `exact_path`.
6. The improvement survives into complete serialized spans and final blind
   passes; ranking-only gains do not qualify.
7. Two independent index builds produce byte-identical document and query
   vectors, persisted rows, final bundles, digests, and token counts.
8. The model satisfies every operating-budget gate below.
9. Every capture is valid: the semantic generation is `StateReady`, its full
   durable fingerprint matches `M3`, and the retrieval reports no sidecar
   degradation.

Lexical candidates and lexical backfill inside an otherwise valid
`StateReady` fused retrieval are normal ranking inputs. They are not sidecar
degradation and do not invalidate a capture.

### Operating-budget gates

The reference hardware, operating-system version, runtime configuration,
thread count, and background-load protocol are frozen with the experiment.
After warm-up, the quality profile must satisfy all of these limits:

- at most 2 GiB additional peak sidecar RSS;
- at most 1 GiB total installed model artifacts required by the profile;
- at most one second p95 query-embedding latency over at least 100 recorded
  CPU-only query embeddings;
- at most ten minutes to rebuild the 768-document reference corpus from an
  empty semantic generation; and
- correct CPU-only operation, with GPU acceleration optional and outside the
  qualifying measurement.

## Decision branches after development evaluation

- If `M3` passes every promotion gate, proceed to product integration.
- If `M3` improves semantic rank but not complete bundles or blind passes, do
  not integrate the model. Use the stage measurements to address fusion,
  selection, or budget allocation on development data.
- If `M2` meets the pass-count, paired-effect, stratum, serialized-span, and
  reproducibility criteria while `M3` does not, treat admission as the leading
  cause. Design and qualify a separate Potion-admission candidate under its own
  operating budget; `M2` itself remains diagnostic and cannot proceed directly
  to a holdout.
- If both `M2` and `M3` pass, prefer the simpler Potion path unless `M3` has a
  strictly larger final paired pass gain and independently satisfies every
  CodeRank operating and reproducibility gate. Either choice becomes a new
  frozen candidate and requires its own production-parity check.
- If oracle relevant candidate plus oracle packer cannot reach 56 blind passes,
  stop model work. The representation or 1,200-token contract is the binding
  ceiling and requires a separately governed design change.
- If no arm passes, do not spend another holdout.

## Sidecar contract

### Transport and activation

The CodeRank adapter uses standard-library HTTP and accepts only a positive
allowlist of loopback endpoints (`127.0.0.0/8`, `::1`, or literal
`localhost`). Redirects, DNS-derived non-loopback targets, credentials, and
automatic remote discovery are rejected.

Activation is explicit through the existing embedder-selection boundary. The
configuration names a local manifest whose content is hashed into the selected
profile. The manifest pins:

- model name, immutable revision, and artifact SHA-256;
- tokenizer identity and SHA-256;
- sidecar runtime name and version;
- vector dimension, precision, normalization, and compute profile;
- admission limit and algorithm version;
- query-preparation profile and instruction digest; and
- protocol version.

The standard binary neither installs nor launches the sidecar. Setup and repair
instructions remain explicit operator actions.

### Admission and text identity

The adapter owns model-specific admission. The sidecar tokenizer returns the
admitted byte prefix and exact token count. GrapHi verifies that the result is
an unchanged UTF-8 prefix of the canonical document, then uses those exact
bytes for the document hash, document identity, and embedding request. Silent
truncation and server-side rewriting are invalid.

Documents are embedded without the query instruction. GrapHi applies one
fixed, versioned CodeRank instruction only through the query-embedding path.
The instruction digest is part of the durable fingerprint.

### Durable fingerprint and carry-forward

The durable fingerprint covers every model, tokenizer, runtime, query,
admission, dimension, document-schema, chunking, and graph-generation field.
Existing vectors may be carried into a new generation only when this complete
durable fingerprint is identical and the admitted document hash is unchanged.
No subset comparison is sufficient.

The process epoch is deliberately absent from the durable fingerprint. It is a
runtime TOCTOU barrier, not an embedding-space identity, and therefore does not
invalidate deterministic vectors merely because an identical sidecar process
was restarted.

## Attestation and TOCTOU behavior

### Trust boundary

The operator pins expected digests outside the sidecar. The sidecar reports its
loaded identity and GrapHi compares every field with the pinned manifest. This
is an honest-but-fallible local-process contract protecting against drift,
retagging, accidental reloads, and configuration mistakes. It is not
cryptographic remote attestation and does not protect against a deliberately
lying local process. That stronger adversary is outside the repository's
error/accident/drift threat model.

### Adapter session and process epoch

When an adapter instance is constructed, it obtains and pins a sidecar process
epoch after validating the full durable identity. Within that adapter lifetime,
every later operation must observe the same epoch. A sidecar restart or reload
therefore fails closed. A newly constructed adapter may accept a new epoch only
after revalidating the complete durable identity; an unchanged durable
fingerprint does not require re-indexing.

### Build sequence

1. Read and verify the full attestation and pin the process epoch.
2. Discover and validate the dimension.
3. Construct the durable fingerprint and open a staging generation.
4. Require every admission and embedding response to carry the same identity
   digest and process epoch.
5. Immediately before generation commit, repeat the attestation check.
6. On any mismatch, abort staging and retain the previous active generation.
7. On success, use the existing commit validation and atomic active-pointer
   transaction.

A build containing only carried-forward vectors still performs the initial and
pre-commit checks. A process change after the final successful attestation and
after the last embedding cannot alter already produced vectors. It does not
corrupt the committed generation. The next operation through the existing
adapter detects the epoch change and fails closed.

### Query sequence

Before every query embedding, GrapHi verifies the full attestation, identity
digest, and pinned epoch. The embedding response must bind the request to the
same identity digest and epoch, which GrapHi verifies before comparing the query
vector with stored vectors. This prevents an index built in one embedding space
from being queried with a vector from a changed sidecar.

## Failure and fallback semantics

The existing lexical degradation behavior remains part of the product:

- Direct semantic search returns a typed unavailable response when its selected
  embedder or generation is unavailable.
- Hybrid retrieval may return lexical-only results for a missing, stale,
  corrupt, or unavailable semantic generation, while reporting the exact typed
  degradation state.
- A plain sidecar protocol or identity error fails the semantic operation; a
  repairable availability error may use the existing typed-unavailable path.

"No fallback" has the narrower and binding meaning that GrapHi never:

- substitutes Potion for explicitly selected CodeRankEmbed or vice versa;
- mixes vectors from different durable fingerprints;
- labels lexically degraded output as a CodeRank result; or
- counts a degraded development or holdout capture as a valid candidate run.

For development and holdout evaluation, any query lacking `StateReady`, the
expected durable fingerprint, or a no-degradation state invalidates the entire
run before grading. Valid lexical candidates within a ready fused retrieval do
not trigger this rule.

## Production promotion and parity

After CodeRank passes the development gate, the development adapter is promoted
through the public embedder-selection boundary. The production form must
reproduce the qualified adapter's admitted bytes, document and query vectors,
rankings, serialized bundles, fingerprints, and diagnostics byte for byte.
Failure of that parity check returns the candidate to development; it does not
permit a holdout.

The product documentation must distinguish:

- the daemonless standard configuration;
- Potion's fast local profile;
- CodeRank's sidecar-backed quality profile;
- installation size and CPU/RAM/index-time costs;
- explicit setup, health, repair, and re-index commands; and
- the absence of automatic downloads and automatic provider fallback.

## Verification strategy

Implementation verification must include:

- default-build tests proving `CGO_ENABLED=0` and no active default embedder;
- no-dial tests when no profile is selected;
- non-loopback, redirect, and identity-mismatch rejection;
- manifest, model, tokenizer, runtime, dimension, instruction, and admission
  fingerprint invalidation tests;
- document/query preparation separation;
- admitted-byte and document-hash identity tests;
- initial, per-response, pre-commit, and per-query attestation tests;
- process-epoch changes before embedding, during a build, after final embedding,
  and before the next query;
- carry-forward only under a complete durable-fingerprint match;
- build abort preserving the prior active generation;
- stale and corrupt generations never serving semantic vectors;
- lexical degradation retaining its typed state;
- lexical backfill under `StateReady` remaining a valid fused result;
- strict evaluation capture rejecting every degraded query;
- independent-index byte reproducibility;
- the four-arm paired experiment and three oracle controls; and
- all operating-budget measurements on the frozen reference machine.

## Release procedure

Only after product parity and all development gates pass is a new holdout
commissioned. Its curator and evaluation operators are independent of candidate
tuning. Before any response is opened, the repository freezes the candidate
commit, indexed checkout, sidecar manifest, all artifact digests, runtime,
admission and query profiles, dataset, graders, prompts, targets, and decision
rules.

That preregistration must enumerate every stratum metric and numeric threshold,
every reproducibility and operating-budget check, and every run-validity
condition. An omitted or post-hoc gate cannot be supplied after responses are
opened; an incomplete preregistration makes the run invalid rather than easier
to pass.

The final decision is:

- `RELEASE: YES` only when the candidate records at least 56 of 64 passing
  queries **and** every preregistered stratum, reproducibility,
  operating-budget, and run-validity gate passes; otherwise
- `RELEASE: NO`, without override or waiver.

## Explicit non-goals

This design does not:

- lower or reinterpret the release threshold;
- reuse the spent holdout for selection or authorization;
- flip any embedder on by default;
- ship or download model weights automatically;
- add hosted or non-loopback inference;
- introduce automatic provider fallback or a multi-model ensemble;
- widen the 1,200-token primary contract;
- tune a broad model or prompt grid; or
- claim protection from a malicious local sidecar.
