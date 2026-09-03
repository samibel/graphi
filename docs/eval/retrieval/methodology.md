# SW-266 token-savings measurement contract

Status: frozen method definition, `sw266-measurement-contract/1`. This document contains no
measurement result and authorizes no claim. Later SW-266 slices may implement the instruments,
dataset, tokenizer, evaluation and release composition, but a method change requires a new
contract version. It may not be hidden behind this version.

## Estimand and claim boundary

The estimand is the median, over every answerable English query in the full frozen release
dataset for the pinned Cobra commit (development and sealed holdout together), of the paired
per-query percentage reduction in tokens consumed by exactly one complete `task_context/2`
response at the frozen 1,200-token candidate budget relative to deterministic
`GrepRead/<version>` at the earliest complete response prefix at which both arms reach that
query's same predeclared exact-grade-3 span-recall target. For a query with reached token counts
`C` (candidate) and `G` (GrepRead), its paired value is the exact rational `100 * (G-C) / G`;
rounding happens only when rendering. The population excludes `no_hit` queries, which are
reported separately. This is a descriptive, paired result for the named questions, frozen
dataset, pinned Cobra tree, candidate version, comparator version and tokenizer artifact. It
does **not** estimate performance on Go repositories generally; compare graphi with BM25, CoIR,
a lexical baseline or any other retrieval family; establish semantic superiority; establish
cross-repository performance; measure answer quality; or transfer to another dataset,
repository, language, budget, tokenizer, candidate or comparator version.

The holdout bundle-sufficiency pass count is a separate smoke gate. It does not enter the token
estimand or its interval and cannot be described as an estimate of general answerability.

Every aggregate input embeds `FrozenMeasurementContract()` and the following content identities:

- dataset SHA-256 and pinned repository commit;
- candidate and `GrepRead` versions;
- real `tokenizer_id` and vocabulary SHA-256;
- the ordered answerable population with `query_id`, `family_id`, split, stratum and recall
  target; and
- one paired observation for every member of that population.

An input with a missing or duplicate population member is invalid. There is no complete-case
mode. Query families may not cross development and holdout, and `no_hit` records may not enter
the savings population.

## Miss and censoring rule

Each arm has exactly two outcome states: `reached` or `missed`. A reached observation records the
earliest indivisible response prefix attaining the target and recomputes tokens-to-target from
the preserved bytes in that prefix. A response is indivisible: if the target is found inside a
response, the entire response is charged.

A miss is right-censored at the token count of the complete preserved transcript. For the
candidate that is the one complete `task_context/2` response. `GrepRead` remains judgement-blind
and runs to exhaustion or `MaxReads`; its full transcript is preserved even if the post-hoc
scorer finds an earlier successful prefix. On a miss:

- `tokens_to_target` and paired percentage saving are undefined;
- `censor_lower_bound_tokens` is recomputed from the complete transcript;
- the report carries the arm-specific miss count and fraction as exact integers (`m/N`) beside
  the censor bounds; and
- validation of every median, percentile or confidence interval fails. The release result is
  therefore unavailable/`RELEASE: NO`; a complete-case median cannot be substituted.

This deliberately declines to assign a flattering finite penalty to a target that was never
observed. It also declines to treat the censor bound as if it were tokens-to-target.

`ValidateSavingsAggregateInput` enforces the rule before any magnitude statistic can be
calculated. Removing the missed row still fails because the observed query set must equal the
frozen population exactly.

## Confidence method

`confidence` is the following complete method identity, not a label:

- **Estimator:** the sample median of the paired per-query percentage token savings defined
  above, with zero-savings ties retained.
- **Interval:** a two-sided, split-stratified, paired family-cluster percentile bootstrap.
  Within development and sealed holdout separately, sample the observed `family_id` values with
  replacement, drawing the same number of families as that split contains; each draw brings all
  answerable queries in that family and keeps the candidate/comparator pair together. Combine
  the two resampled splits and recompute the median. Perform 10,000 replicates. The lower and
  upper endpoints are the nearest-rank 2.5th and 97.5th percentiles of the replicate medians.
- **Level:** two-sided 95% (`level_basis_points: 9500`).
- **Population:** all answerable English queries in the full frozen release dataset for the
  pinned Cobra commit. `family_id` is the resampling unit. Split is the stratification. The
  interval describes sensitivity to the observed mix of frozen Cobra task families; it is not
  uncertainty over repositories, languages, model versions or unobserved general-Go work.
- **Reproducibility:** the replicate count is 10,000 and the random stream is seeded from
  `sha256(dataset_sha256 + newline + measurement_contract_version)`. A later implementation must
  pin its bootstrap/PRNG implementation version and export enough replicate input to reproduce
  both endpoints exactly.

The interval is invalid whenever either arm misses any population member, because the point
estimator itself is then undefined under the miss rule. `ValidateConfidenceSpec` requires every
field above exactly. Bootstrap calculation belongs to the later aggregation slice and is not
implemented here.

## Payload boundary and accounting

The measured object is bytes returned to the actor, captured below final serialization. Requests,
logs, timing records, index bytes and internal structs are outside the token estimand.

For graphi, preserve the one complete MCP stdio JSON-RPC response message for the
`task_context/2` call exactly as written: JSON-RPC envelope, request ID, result or error object,
JSON escaping, separators and terminating newline included. Counting a re-marshaled result
object, extracted evidence text or an estimated envelope is invalid.

For `GrepRead`, preserve in call order the exact UTF-8 response body returned by every operation:
the initial grep response and every read response, including empty and error responses and their
delimiters. Request bytes and evaluator-only metadata are excluded. A later baseline version must
freeze the response serialization; changing it changes the method version and the count.

Every response is stored as its own byte slice with sequence, boundary and operation labels,
SHA-256, byte count, and token counts for both `whitespace-fields-v1` and the pinned real
tokenizer. The vocabulary SHA-256 accompanies every real-tokenizer count. Raw slices are
preserved in artifacts (base64 is the JSON representation); `null`/absent bytes are invalid,
including for an empty response. Validation recomputes SHA-256, bytes and both tokenizer counts
from those slices through executable counters. A missing counter, vocabulary mismatch,
reconstructed count or estimated count is an error.

### Decision on the fixed 40-line window

The value 40 survives as `GrepReadWindowLines`, the frozen size of an actual `read` operation:
starting at the selected line, the operation returns at most 40 source lines, clipped at end of
file. Those actual returned bytes are charged. The existing retrieval-quality harness may keep
`HitContextWindowLines = 40` for its older `recall@...tok` metric, but its synthetic count of a
reopened window does **not** enter this savings estimand. In particular, no candidate MCP payload
may be reconstructed as a hypothetical 40-line window, and no baseline response may be priced
from source lines after the run instead of its preserved response bytes.

## Deterministic `GrepRead/1`

`GrepRead/1` is the fixed comparator implementation. Its complete execution API is:

```go
func GrepRead(repository fs.FS, query string) GrepReadTranscript
```

There is no options struct. In particular, the function cannot receive a judgement, qrel, answer
span, recall target or stop-at-recall callback. `GrepRead` always produces the complete transcript
before a scorer sees it. A later scorer may select the earliest whole-response prefix reaching a
predeclared target, but it may neither alter execution nor discard the preserved suffix. Adding an
oracle would therefore require an explicit change to this function signature or forbidden package
state, plus a `GrepRead` version change; `TestGrepRead_IsStructurallyJudgementBlindAndDeterministic`
pins the two-argument function type and poisons the judgements while holding its two inputs fixed.

The algorithm is fully specified as follows:

1. **Patterns.** Scan the query for maximal Unicode letter, digit or underscore runs, lowercase
   each run, retain first-occurrence order and remove exact duplicates. If at least one run has
   three or more Unicode code points, discard all shorter runs; otherwise retain the short runs as
   a fallback so a short-identifier query is not erased. Match every retained pattern as a
   case-insensitive literal substring, never as a regular expression. A query with no runs emits
   the exact grep error response `grep:error:query:no_searchable_pattern\n` and then exhausts.
2. **Files.** Include regular files whose cleaned repository-relative slash path ends in `.go`,
   including `_test.go`. Exclude symlinks, non-Go files, and any path below `vendor` or a directory
   whose name begins with `.`. There is no generated-file, filename or content exception. Included
   paths are ordered by raw UTF-8 path bytes.
3. **Search.** Visit included paths in that order and source lines in ascending order. A source
   line matching multiple patterns is one hit. Its column is the leftmost match's one-based UTF-8
   byte column. Serialize a hit as `<path>:<line>:<column>:<line text>\n`; remove the source line's
   LF or CRLF before adding that response LF. The grep response contains the first
   `GrepReadSearchLimit = 20` distinct matching lines. Read, UTF-8 and walk failures are serialized
   before hits, in path order, as `grep:error:<path>:<read_failed|invalid_utf8|walk_failed>\n` and
   do not consume the match limit. A successful no-hit grep response is an exact non-nil zero-byte
   slice.
4. **Reads and overlap.** Consider the limited hits in their canonical order. A read starts at the
   hit line and requests `GrepReadWindowLines = 40` lines, clipped at the then-current end of file.
   The response is the exact source byte slice, including its original line endings and lack of a
   final newline. A later hit in the same file is skipped when its line lies inside an earlier
   requested 40-line window; this covers same-line duplicates and overlapping reads. Windows in
   different files never deduplicate. The requested window is considered covered even if its read
   returns an error or an empty response.
5. **Termination and errors.** Reopen the file for every read, so grep and read remain distinct
   operations. A read failure emits `read:error:<path>:read_failed\n`; invalid UTF-8 emits
   `read:error:<path>:invalid_utf8\n`; a file shortened past the requested line emits an exact
   non-nil zero-byte response. These are ordinary charged responses, not top-level failures.
   Continue through every uncovered limited hit or stop after `GrepReadMaxReads = 8`. Record only
   `exhausted` or `max_reads`; recall cannot be a stop reason.

The parameter choices were made without inspecting comparator or candidate scores. Twenty search
lines keeps the single grep response bounded and directly inspectable without a paging protocol.
Eight reads bounds a single query's follow-up work while allowing multiple files to be opened; it
is an operation cap, not a quality threshold. Forty lines was frozen in the preceding measurement
contract as the size of a real read operation. The three-code-point preference suppresses the
high accidental collision rate of one- and two-character substrings without a language-specific
stopword list, while the all-short fallback preserves legitimate short identifiers. Including all
regular Go and Go-test files gives the comparator the same source-and-test scope for every query;
the exclusions are repository hygiene rules rather than query- or dataset-specific tuning.

### Exact-byte payload ledger

`GrepRead` appends each response to `PayloadLedger` at the only point from which that response is
returned. Each entry stores its sequence, boundary, operation, an owned copy of the exact bytes,
SHA-256 and byte count. There is no ledger API accepting source coordinates, a window-derived
count or already-counted tokens. Empty responses are stored as non-nil `[]byte{}`; serialized error
responses are stored like successes.

`PayloadLedger.Validate` recomputes every digest and byte count from the stored slices.
`PayloadLedger.PreservedPayloads` later accepts the pinned real tokenizer implementation and
recomputes both `whitespace-fields-v1` and real-tokenizer counts separately from every stored
slice; the executable counter receives a copy and cannot mutate the ledger. It rejects a missing
executable counter or invalid vocabulary identity. The ledger's total
byte cost is the length of `ConcatenatedBytes`, which appends every response in sequence. The
payload digest is SHA-256 of that concatenation. The transcript digest additionally covers the
query, derived patterns, included files, read requests, stop reason, response boundaries and
operations through the canonical JSON representation of `GrepReadTranscript`.

`TestPayloadLedger_CostIsEveryCapturedResponse` compares that concatenation with a literal grep +
read byte string and validates the exact total. Removing an intermediate response leaves a
sequence hole; removing the last response makes the ledger length disagree with the transcript's
read operations. Both mutations fail validation rather than silently lowering cost.

## Equal-recall comparison

Each answerable query freezes a rational target before either arm is inspected:
`required_spans / total_grade3_spans`. The grade is exactly 3. Both arms repeat that target in
their raw observation, and unequal targets fail validation. Lower-grade evidence earns no credit.

A distinct, independently reviewed grade-3 answer span is the atomic credit. It is either covered
or not covered; there is no fractional credit within a span. Multiple spans can therefore yield
fractional recall only in whole-span steps of `1 / total_grade3_spans`, and a span is credited at
most once. The earliest prefix is the first sequence of whole response slices whose credited-span
count is at least `required_spans`. Overshoot within the last response is allowed but the whole
response is charged. A relation-only citation or nearby declaration is not answer-bearing credit;
the later scorer must verify that preserved emitted source bytes and provenance resolve to and
overlap the reviewed grade-3 span in the pinned tree.

The two arms need not expose the same span when the frozen rubric declares multiple grade-3
answers; they must reach the same predeclared recall fraction under the same qrels. Equal token
counts are a tie and contribute exactly zero. `tokens_to_exact_span` is the special target whose
required count is one grade-3 span. It is undefined on a miss; the report carries the miss and
censor lower bound instead.

## Resolution and rendering

Population membership is discrete and always reported. For any overall, split or stratum
population of size `n`, one query changes an incidence proportion by exactly `1/n`; render the
authoritative count as `k/n` before any percentage. For a query with `g` reviewed grade-3 spans,
recall resolution is `1/g` and its frozen target remains the integer pair `required/g`, never a
rounded decimal. Thus the historical six-query `nl_behaviour` development population has query
incidence resolution `1/6` (16.7 percentage points); extra decimal places cannot add evidence.

Byte and token counts are integers. Per-query savings is retained as the exact integer
numerator/denominator until aggregation. The median is an observed order statistic for odd `N`
and the arithmetic mean of the two central values for even `N`; it always carries `N` and the
unique family count. Human-facing proportions, paired savings, medians and interval endpoints use
at most one decimal percentage point. Machine artifacts retain exact counts/fractions and
unrounded bootstrap values. Gates compare integer counts or exact stored rationals, never rounded
display decimals.

## Executable claim scope

`go run ./cmd/retrieval-eval -check-claim '<candidate>'` checks wording only. It does not generate
a sentence, read a report, run an evaluation or authorize publication. A permitted candidate must
name the frozen Cobra dataset and include this exact limitation:

> This is a descriptive result for that pinned Cobra dataset, not an estimate for Go repositories generally.

The only accepted grammar is `ClaimTemplateExample`, the descriptive sentence in the decision
record with typed slots for `N`, commit, saving, tokenizer, comparator version, interval and
holdout pass count. Full-template validation prevents an unlisted paraphrase from implying a
broader comparison. Within that grammar the checker also fails closed on `BM25`, `CoIR`,
lexical/keyword/sparse comparator wording, semantic/embedding/vector retrieval wording, cross-
or multi-repository wording, and affirmative general-Go/generalization wording outside the exact
limitation. The closed vocabulary is deliberately stricter than stylistic interpretation: the
public sentence has no need to name those concepts.

## Enforcement matrix

“Enforced” below means a named test executes the rule in this slice. “UNENFORCED” is deliberate:
the relevant instrument or renderer does not exist yet, so this document does not pretend a prose
rule already binds running code.

| Rule | Enforcing test or status |
|---|---|
| Frozen version, candidate method/budget, grade, comparator name, 40-line operation parameter, miss/payload/equal-recall identities and one-decimal display ceiling cannot drift under `/1`. | `TestMeasurementContract_IsFrozen` |
| `confidence` must include the exact estimator, interval type, level, population, resampling unit, stratification, replicate count and seed method. | `TestValidateSavingsAggregateInput_RequiresExactConfidenceMethod` |
| Aggregate input contains every answerable population query exactly once; complete-case subsets and duplicate/extra observations fail. | `TestValidateSavingsAggregateInput_RejectsCompleteCaseSubsetAndMisses` |
| `no_hit` is excluded and `family_id` cannot cross development/holdout. | `TestValidateSavingsAggregateInput_EnforcesEqualRecallAndPopulation` |
| A miss has no tokens-to-target, carries a recomputed complete-transcript censor bound, and makes every magnitude aggregate fail. | `TestValidateSavingsAggregateInput_RejectsCompleteCaseSubsetAndMisses` |
| Both arms use the query's same predeclared exact rational grade-3 target; only whole-span counts reach it. | `TestValidateSavingsAggregateInput_EnforcesEqualRecallAndPopulation` |
| Candidate preserves exactly one complete `task_context/2` response; GrepRead preserves initial grep plus reads and terminates only at exhaustion/`MaxReads`. | Candidate/artifact shape: `TestValidateSavingsAggregateInput_RejectsReconstructedPayloadsAndCountDrift`; actual GrepRead producer: `TestGrepRead_HandComputedGolden`, `TestGrepRead_SearchAndReadCapsAreDeterministic`. |
| Preserved byte slices are mandatory; SHA-256, byte counts, whitespace counts, real-tokenizer counts and vocabulary identity must recompute exactly; serialization mutation moves identity and counts. | `TestValidateSavingsAggregateInput_RejectsReconstructedPayloadsAndCountDrift` |
| The GrepRead executor is structurally judgement-blind; judgement changes cannot move its complete transcript or payload digest. | `TestGrepRead_IsStructurallyJudgementBlindAndDeterministic` |
| The GrepRead producer captures exact success, empty and error bytes at its response boundary, and every derived count recomputes from those bytes. Candidate MCP capture remains **UNENFORCED** until the release-composition slice owns the actual transport call. | `TestGrepRead_HandComputedGolden`, `TestGrepRead_LedgerPreservesErrorAndEmptyResponses`, `TestPayloadLedger_CostIsEveryCapturedResponse` |
| Total GrepRead cost is the concatenation of initial grep plus every read response; deleting an intermediate or final response fails validation. | `TestPayloadLedger_CostIsEveryCapturedResponse` |
| Forty lines controls the actual GrepRead read operation and is never a reconstructed accounting charge. | Constant identity: `TestMeasurementContract_IsFrozen`; execution and overlap behavior: `TestGrepRead_HandComputedGolden`. |
| Answer-bearing source bytes/provenance, not a relation-only citation or nearby declaration, earn grade-3 credit; a span is credited once. | **UNENFORCED** until the equal-recall scorer slice; `ValidateSavingsAggregateInput` validates credited whole-span counts but does not trust itself to prove how they were obtained. |
| Ties remain zero-valued paired observations; misses do not become finite penalties. | Miss half: `TestValidateSavingsAggregateInput_RejectsCompleteCaseSubsetAndMisses`; tie-to-zero arithmetic is **UNENFORCED** until the aggregate calculator exists. |
| The family-cluster bootstrap executes exactly 10,000 seeded paired replicates and nearest-rank endpoints. | Method identity: `TestValidateSavingsAggregateInput_RequiresExactConfidenceMethod`; calculation is **UNENFORCED** until the aggregate calculator exists. |
| Counts/fractions are authoritative, all populations state `n`/family count and their `1/n` resolution, and renderers use no more than one decimal percentage point. | Policy constant: `TestMeasurementContract_IsFrozen`; report rendering is **UNENFORCED** until the release report exists. |
| Candidate wording cannot mention or imply BM25, CoIR, a lexical comparator, semantic superiority, or cross-repository/general-Go performance, and must carry the frozen-Cobra limitation. | `TestCheckClaimSentence_RejectsForbiddenScope`; CLI boundary and exact rejection text: `TestRetrievalEval_CheckClaimRejectsUnsafeSentence`; narrow control: `TestRetrievalEval_CheckClaimAcceptsOnlyNarrowDescriptiveScope` |
| This slice runs no measurement, changes no dataset/targets, publishes no result and changes no ranking/product behavior. | **UNENFORCED** in code; verified by review of the changed-file set. |
