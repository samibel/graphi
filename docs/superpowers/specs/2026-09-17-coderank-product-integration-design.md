# CodeRank product integration and release requalification design

Date: 2026-09-17
Status: approved in chat; implementation is gated on `DEVELOPMENT PROMOTION: YES`

## Decision

GrapHi will retain its CGo-free, embedderless default and Potion as its fast,
daemonless opt-in profile. If and only if the preregistered embedded-model
development qualification returns `DEVELOPMENT PROMOTION: YES`, GrapHi may
promote the existing CodeRank adapter into an explicitly selected,
loopback-sidecar-backed product quality profile.

The product integration must use the same manifest, admission, query
preparation, attestation, and embedding implementation that passed development
qualification. It must not introduce a second product-only adapter or silently
change the qualified embedding space.

The spent holdout remains `RELEASE: NO` at 47/64. It is not reused for model
selection, tuning, or release authorization. Product integration, product-path
development requalification, and a new independent sealed holdout are all
required before a future `RELEASE: YES` is possible.

This specification refines and, where they conflict, supersedes the product
integration and fallback sections of
[`2026-09-16-embedded-model-release-recovery-design.md`](2026-09-16-embedded-model-release-recovery-design.md).
In particular, an explicitly selected CodeRank profile never degrades into a
successful Potion or lexical-only retrieval.

Its normative evidence and protocol inputs are:

- [`2026-09-16-embedded-model-holdout-feasibility.md`](../../eval/retrieval/research/2026-09-16-embedded-model-holdout-feasibility.md);
- [`embedded-model-qualification.md`](../../eval/retrieval/embedded-model-qualification.md);
  and
- [`coderank-sidecar-protocol.md`](../../eval/retrieval/coderank-sidecar-protocol.md).

## Preconditions

Product integration must not begin until the frozen four-arm development
qualification reports `DEVELOPMENT PROMOTION: YES`. That decision requires all
of its quality, paired-effect, bootstrap, stratum, reproducibility,
operating-budget, and run-validity gates to pass.

The qualifying comparison remains:

| arm | role |
|---|---|
| `M0_lexical` | lexical negative control |
| `M1_potion_512` | current pinned Potion production profile |
| `M2_potion_8192` | diagnostic extended-admission Potion profile |
| `M3_coderank` | pinned CodeRank candidate |

The minimum CodeRank development result remains 56/64, not 60/64. CodeRank
must additionally gain at least nine paired passes over Potion, have a positive
bootstrap lower bound, improve at least two weak strata, avoid losses in the
three strong strata, transfer its gain into serialized spans and blind passes,
and pass every reproducibility and operating gate.

If development qualification returns `NO`, product integration stops. A
diagnostically justified projection or fusion change may be attempted, but
only one change is frozen per iteration and the complete development
qualification is rerun.

## Fixed product constraints

- The standard GrapHi binary remains buildable with `CGO_ENABLED=0`.
- An empty embedder selector constructs, activates, downloads, and contacts no
  embedder.
- GrapHi never performs automatic external model access.
- Potion remains the fast daemonless standard profile offered by
  `setup-embedder`.
- CodeRank is optional, local, CPU-qualified, and accessed only through an
  operator-managed loopback sidecar.
- GrapHi ships neither CodeRank weights nor Python, PyTorch, Transformers, or
  SentenceTransformers runtimes.
- GrapHi does not install, launch, update, restart, or supervise the CodeRank
  sidecar.
- Model, revision, tokenizer, runtime, precision, normalization, dimension,
  admission, and query instruction remain reproducibly pinned.
- A model, profile, or candidate change requires new development evidence and
  then a new independent holdout.
- The primary serialized bundle ceiling remains exactly 1,200
  `cl100k_base` tokens.
- No release threshold, stratum gate, run-validity gate, or operating budget
  may be waived after evidence is opened.

## Product boundary and explicit selection

### Binary boundary

The standard Go binary contains only the pure-Go CodeRank client adapter,
strict manifest parsing, loopback transport, fingerprint logic, attestation
checks, generation persistence, and status diagnostics. All inference code and
artifacts remain outside the binary and outside GrapHi's lifecycle.

### Selector

CodeRank is selected only through the existing embedder-selection boundary:

```text
GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json
```

The manifest path must be absolute and canonical. A relative path is rejected
so selection cannot change with the working directory. Endpoint, model name,
or credentials are not accepted as separate selector parameters; the strict
manifest is the single CodeRank configuration source.

For this design, selection has three relevant modes:

| mode | meaning |
|---|---|
| `unconfigured` | no embedder was selected; current lexical behavior remains available |
| `potion` | the existing daemonless opt-in profile was selected |
| `coderank_required` | CodeRank was explicitly selected and semantic operations are fail-closed |

Other existing opt-in provider support is not removed by this work. It is
outside this specification.

The composition root resolves `GRAPHI_EMBEDDER` once and passes an immutable
selection object to CLI, MCP, HTTP, daemon, and status wiring. Deeper modules do
not reread the environment or independently choose an embedder.

The constructor boundary becomes context-aware so initial sidecar attestation
can be cancelled and bounded. CodeRank is registered as an available scheme at
the product composition root but is never registered as an active default.

### Required-profile behavior

Once `coderank_required` has been recognized, any manifest, construction,
availability, attestation, generation, or query failure is a hard failure for
the semantic operation. A composition root must not translate it into an empty
registry, Potion selection, or successful lexical-only retrieval.

An explicitly requested lexical-only command remains valid because it does not
attempt a semantic operation. Lexical candidates and bounded lexical backfill
inside an otherwise valid `StateReady` fused retrieval are also valid; they are
not provider fallback and record `degradation == none`.

## Durable identity and vector reuse

### Durable CodeRank identity

`embed.Fingerprint.Canonical()` remains the authoritative persisted vector
identity. For CodeRank it binds all of the following:

- model ID, immutable revision, and full model-tree SHA-256;
- tokenizer ID, immutable revision, and tokenizer SHA-256;
- runtime name, exact version, and runtime-inventory SHA-256;
- dimension, precision, normalization, and compute mode;
- admission maximum, reserve, algorithm, and algorithm version;
- query profile ID, version, exact instruction, and instruction SHA-256;
- document schema and chunking/profile configuration; and
- graph generation.

The canonical endpoint-free CodeRank manifest profile remains part of the
fingerprint. The embedder ID also carries the manifest identity digest. This
intentional redundancy makes both human-facing identity and complete canonical
equality bind the same space.

### Excluded runtime fields

The endpoint and process epoch are excluded from the durable fingerprint.
Changing only a loopback port or restarting an otherwise identical sidecar does
not change the mathematical embedding space and therefore does not invalidate
persisted vectors.

For a sealed development or holdout run, the exact manifest bytes are still
captured separately. A changed endpoint therefore changes the run artifact
even though it does not change the reusable vector-space fingerprint.

### Carry-forward rule

A persisted vector may be carried into a new generation only when all three
conditions hold:

```text
active generation state == StateReady
AND stored fingerprint == requested fingerprint byte-for-byte
AND admitted document text_hash is unchanged
```

No subset comparison is sufficient. Matching model names, dimensions, or
artifact digests alone does not authorize reuse. The process epoch never enters
this comparison: it binds a running operation, not persisted deterministic
vectors.

## Runtime attestation and process epoch

### Adapter construction

The CodeRank adapter strictly loads the manifest, constructs a loopback-only
transport, reads the sidecar attestation, validates the full durable identity,
protocol, dimension, and positive operating fields, and then freezes:

```text
expected runtime binding = (protocol, identity_digest, epoch)
```

The adapter session is threadsafe and has the states `unbound`, `bound`, and
`poisoned`. A successfully constructed adapter is `bound`.

### Query checks

Every query embedding performs these operations in order:

1. capture one immutable live generation snapshot;
2. require `StateReady` and exact durable fingerprint equality;
3. call `VerifyRuntime("before query embedding")`;
4. prepend the pinned query instruction exactly once;
5. request the query embedding;
6. validate protocol, identity digest, epoch, dimension, cardinality,
   unknown-token count, and finite vector values in the response; and
7. only then compare the vector with the captured index.

The preflight detects a switch before the request. The response binding detects
a switch between preflight and response.

### Build checks

Every build performs these operations in order:

1. verify the runtime before opening a staging generation;
2. discover and validate dimension;
3. construct the durable fingerprint;
4. require every admission and embedding response to retain the pinned
   protocol, identity digest, and epoch;
5. verify the runtime again immediately before generation commit; and
6. commit only after every row and coverage invariant has passed.

A carry-forward-only build still performs both the initial and pre-commit
attestation checks.

### Poisoning

Any protocol, identity, or epoch mismatch atomically moves the adapter session
to `poisoned`. Poisoning is terminal for that adapter instance:

- the current operation fails;
- no new admission, embedding, or publication operation begins;
- concurrent operations may succeed only if their own response is still bound
  to the expected attestation; and
- the adapter never silently binds itself to a replacement sidecar.

A newly constructed adapter may accept a new epoch only after it revalidates
the complete durable identity. A long-running GrapHi daemon therefore requires
an explicit restart after a sidecar restart. A new short-lived CLI process may
bind the new epoch immediately.

## Atomic generation publication

### Durable staging

All rows are written to a staging generation. The existing generation store
validates row count, vector dimensions, nonempty node identities, and other
persisted invariants before moving the active pointer. Demotion of the previous
generation and promotion of the staging generation remain one SQLite
transaction.

An attestation failure before commit aborts staging and preserves the previous
active generation.

### In-memory staging

The live vector index is not mutated during generation construction. The build
creates a private index and a complete immutable snapshot containing at least:

```text
generation_id
fingerprint
state
vector_index
```

A published vector index is immutable. Queries capture one snapshot and use it
for the entire operation, so a concurrent publication cannot mix a query's
generation, fingerprint, and rows.

### Publication barrier

SQLite commit and a process-memory pointer swap cannot form one physical
transaction. GrapHi therefore provides logical atomicity with a fail-closed
publication barrier:

1. Before durable commit, the prior generation remains active.
2. The validated staging generation is committed atomically in SQLite.
3. During the interval before the live snapshot swap, semantic CodeRank
   retrieval is not ready.
4. The complete immutable snapshot is swapped in one operation.
5. Only then may the process report `StateReady` for the new live generation.

If durable commit succeeds but snapshot publication fails, the running process
reports `semantic_snapshot_inconsistent` and serves neither the old in-memory
index under the new generation nor a lexical fallback. A restart reloads and
validates the committed generation from the durable source of truth.

## Runtime state machine and retrieval semantics

### Orthogonal states

Profile selection, generation state, and adapter session are independent:

| axis | states |
|---|---|
| selection | `unconfigured`, `potion`, `coderank_required` |
| generation | `missing`, `stale`, `corrupt`, `ready` |
| CodeRank session | `unbound`, `bound`, `poisoned` |

CodeRank semantic retrieval is permitted only when:

```text
selection == coderank_required
AND generation == ready
AND generation fingerprint == selected fingerprint
AND session == bound
```

### Search and retrieval ownership

`engine/search` owns the safe semantic operation: snapshot capture, generation
and fingerprint validation, runtime verification, query embedding, response
binding, and vector search.

`engine/retrieval` receives either a successful semantic result or a hard
error. Only after semantic success does it compose semantic hits with lexical
candidates, exact-name evidence, and graph expansion.

The distinction is explicit:

```text
semantic success with zero hits -> valid fusion may continue
semantic hard error             -> retrieval aborts
```

The retrieval layer must not convert a provider error into an empty semantic
hit list.

### Hard failure conditions

Under `coderank_required`, no retrieval result is published when any of these
conditions occurs:

- invalid or missing manifest;
- unreachable or non-loopback sidecar;
- missing, stale, or corrupt generation;
- durable fingerprint mismatch;
- attestation or epoch mismatch;
- malformed admission or embedding response;
- invalid query vector;
- poisoned adapter session; or
- durable/live snapshot inconsistency.

Potion is never substituted, and lexical-only output is never labelled as a
CodeRank result.

### Error vocabulary

The engine owns these stable error causes:

- `coderank_profile_invalid`
- `coderank_runtime_unavailable`
- `coderank_attestation_mismatch`
- `semantic_generation_missing`
- `semantic_generation_stale`
- `semantic_generation_corrupt`
- `semantic_snapshot_inconsistent`
- `semantic_generation_publish_failed`

CLI, MCP, and HTTP may map them to surface-appropriate transport codes and
rendering, but not change their meanings. Attestation errors expose the failing
phase and a repair action without exposing expected or observed digests or raw
epochs.

## Component boundaries

### Selection layer

The generic embed selection layer represents profile identity, whether the
selection was explicit, whether it is required, and the constructed embedder.
It does not perform retrieval.

### CodeRank adapter

`engine/embed/coderank` owns strict manifest loading, loopback transport,
admission, query preparation, response validation, expected attestation,
session poisoning, and the endpoint-free durable profile. It does not own
fallback policy, generation activation, or surface rendering.

### Generation layer

`engine/embed` owns durable staging, exact fingerprint reuse, validation,
transactional active-pointer movement, private index construction, and atomic
live-snapshot publication. The durable store knows no process epoch.

### Search and retrieval layers

`engine/search` owns the attested query boundary. `engine/retrieval` owns
candidate fusion only after semantic success. Neither layer chooses a different
provider.

### Product surfaces

CLI, MCP, HTTP, daemon, and zero-config wiring receive the same resolved
selection and engine results. No surface adds fallback behavior. The canonical
semantic-status document remains byte-identical across
`graphi semantic status --json`, MCP `semantic_status`, and
`GET /semantic/status`.

Status adds at least:

- selected profile;
- `explicit` and `required` selection flags;
- generation state and active generation ID;
- durable fingerprint;
- session state; and
- exact operator repair action.

Raw process epochs are not emitted.

### Evaluation path

After product integration, evaluation constructs CodeRank through the public
selection path. It does not retain a separate evaluation-only adapter. Capture
hooks may record diagnostics, but they may not alter construction, admission,
query preparation, embedding, ranking, or failure behavior.

## Operator lifecycle

### First activation

The operator:

1. provisions the pinned model tree and pinned Python runtime separately;
2. verifies the manifest and artifacts with the sidecar tool;
3. starts the sidecar manually on a literal loopback address;
4. exports the explicit CodeRank selector;
5. checks `graphi semantic status` for `coderank`, `bound`, and the expected
   fingerprint;
6. runs `graphi index --semantic`; and
7. checks `StateReady` and the active generation before querying.

GrapHi performs none of these repair or lifecycle actions automatically.

### Change matrix

| change | durable fingerprint | persisted vectors | required action |
|---|---|---|---|
| GrapHi restart only | unchanged | reusable | none |
| Sidecar restart with identical identity | unchanged | reusable | restart GrapHi or its daemon |
| Loopback endpoint change only | unchanged | reusable | update manifest endpoint and restart GrapHi |
| Model, revision, or model digest | changed | not reusable | new dev evidence, then reindex |
| Tokenizer or admission | changed | not reusable | new dev evidence, then reindex |
| Runtime pin, precision, or normalization | changed | not reusable | new dev evidence, then reindex |
| Query instruction or query profile | changed | not reusable | new dev evidence, then reindex |
| Graph generation only | changed | not reusable | reindex the same qualified profile |
| Persisted row corruption | unchanged but `corrupt` | unusable | reindex |

### Build failure recovery

- Before durable commit, staging is aborted and the active pointer is
  unchanged.
- A SQLite commit failure rolls back the transaction.
- A post-commit live-publication failure makes the running process unavailable
  for semantic retrieval until it reloads the committed generation.
- An attestation inconsistency additionally poisons the adapter instance.
- No recovery path silently changes provider or serves lexical-only output as
  the requested CodeRank retrieval.

## Verification strategy

### Default and boundary tests

- Build and test the standard binary with `CGO_ENABLED=0`.
- Prove that an empty selector constructs no embedder and opens no socket.
- Prove that CodeRank artifacts and inference runtimes are not shipped.
- Preserve Potion's daemonless selection and behavior.
- Reject relative manifests, non-loopback endpoints, redirects, credentials,
  and external discovery.
- Prove that no product command launches or downloads the sidecar.

### Fingerprint tests

Table-driven mutation tests change every durable field individually and require
a different canonical fingerprint. Separate tests require endpoint and epoch
changes not to alter that fingerprint.

Carry-forward tests require exact fingerprint equality, `StateReady`, and an
unchanged admitted-document hash. Tests explicitly reject model-only,
dimension-only, or partial-profile matches.

### Attestation and concurrency tests

A controllable fake sidecar injects runtime changes:

- before a query;
- between query preflight and embedding response;
- before a build;
- during admission;
- between document embeddings;
- immediately before commit;
- after commit and before the next query; and
- during concurrent queries.

Every affected operation must fail. The first observed inconsistency must
poison the adapter, and later calls through that instance must fail without
silently rebinding.

### Publication tests

Failure injection covers staging creation, row writes, row validation,
pre-commit attestation, SQLite promotion, private index construction, and live
snapshot swap. Tests prove that:

- the old active generation survives every pre-commit failure;
- an unvalidated generation is never active;
- published indexes are immutable;
- a query sees exactly one generation/fingerprint/index tuple; and
- a post-commit swap failure serves no inconsistent snapshot.

### Retrieval and surface tests

- CodeRank failures abort direct semantic and hybrid retrieval.
- No Potion or lexical-only fallback result is produced.
- Explicit lexical-only retrieval remains available.
- Lexical backfill under a ready CodeRank retrieval remains valid with
  `degradation == none`.
- CLI, MCP, and HTTP agree on the error cause and degradation meaning.
- Dev and holdout capture invalidate a complete run after any query lacks
  `StateReady`, the expected fingerprint, a bound session, or
  `degradation == none`.

### Product parity

On identical frozen development inputs, the qualified adapter and public
product path must be byte-identical for:

- admitted bytes and token counts;
- document and query vectors;
- durable fingerprints;
- persisted rows;
- semantic top-50 results;
- fused candidates;
- exact 1,200-token bundles; and
- diagnostics and final blind decisions.

After this direct parity check, the complete development qualification is run
again through the public product selector. It must again pass every quality,
paired-effect, bootstrap, stratum, reproducibility, operating-budget, and
run-validity gate.

## Candidate freeze and release procedure

### Freeze

Only after product-path development requalification succeeds are these inputs
frozen together:

- candidate commit and clean candidate-diff digest;
- source checkout;
- exact CodeRank manifest bytes;
- durable fingerprint;
- model, tokenizer, and runtime pins;
- admission and query profiles;
- ranking, fusion, projection, and serialization behavior;
- exact 1,200-token budget;
- reader and grader prompts;
- new holdout dataset and stratum distribution; and
- every decision threshold and run-validity rule.

Any subsequent model, profile, or candidate change invalidates the freeze and
requires new complete development evidence before another holdout is allowed.

### Independent holdout

The new sealed 64-query holdout is evaluated exactly once for the frozen
candidate. It is independent of candidate tuning and retains the preregistered
stratum distribution.

The final compound decision is:

```text
RELEASE: YES
iff pass_count >= 56/64
and every preregistered stratum gate passes
and both independent builds are reproducible
and every operating-budget gate passes
and every capture is StateReady
and every fingerprint equals the frozen fingerprint
and every capture has degradation == none
and every run-validity check passes
```

Otherwise the result is `RELEASE: NO`. After results are opened, there is no
threshold tuning, candidate change, waiver, or repeat on the same holdout. A
new attempt requires a changed and requalified candidate plus a newly curated
independent holdout.

## Explicit non-goals

This design does not:

- implement CodeRank before `DEVELOPMENT PROMOTION: YES`;
- change or lower the 56/64 release threshold;
- reuse the spent holdout;
- activate any embedder by default;
- ship or download CodeRank artifacts or runtimes;
- add hosted or non-loopback inference;
- supervise or automatically restart the sidecar;
- add automatic provider fallback, ensembles, or threshold tuning;
- change the 1,200-token bundle contract; or
- claim protection against a deliberately malicious local sidecar.

## Acceptance criteria

The design is satisfied only when all of the following are true:

1. Development qualification is `YES` before any product integration begins.
2. The standard binary remains CGo-free and embedderless by default.
3. CodeRank requires the explicit absolute-manifest selector.
4. Persisted vectors are reused only under exact durable fingerprint and
   document-hash equality.
5. Every query and build is bound to a validated identity and process epoch.
6. Any sidecar inconsistency poisons the adapter and fails the operation.
7. Durable and in-memory generation publication never exposes a mixed
   snapshot.
8. Explicit CodeRank selection never falls back to Potion or lexical-only
   retrieval.
9. Product-path development qualification reproduces the promoted result and
   passes all gates.
10. Only a newly frozen candidate on one new independent sealed holdout can
    produce `RELEASE: YES`.
