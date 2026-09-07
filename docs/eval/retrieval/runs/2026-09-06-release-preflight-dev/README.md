# Release preflight: a frozen comparator miss blocks the savings claim

Verdict: **the existing full-population token-savings claim cannot receive YES
through candidate improvements alone**. The unchanged `GrepRead/1` comparator
emits no source overlapping any reviewed grade-3 answer span for **35/40**
grade-3-answerable development questions. The frozen miss rule requires both
arms to reach their target on every member of the full release population.
These development questions are members of that population, so no holdout
execution is needed to establish this blocker. This is not a new release
evaluation, a savings magnitude, or an answer-quality rating.

## What was executed

`TestGrepReadDevCapture` runs the existing, unchanged two-argument `GrepRead`
function on the clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`. It loads only the already-extracted
development dataset under `2026-09-06-sw282-gate-local/dataset.json`, checks its
SHA-256, and refuses non-development rows. All 44 dev records are captured;
40 contain grade-3 answers. The other four (`cb-31`, `cb-36`, `cb-37`, `cb-38`)
remain in the artifact, explicitly outside this grade-3 diagnostic. `cb-31`
has only a grade-2 judgement and is not silently called a no-hit query.

The complete transcript exists before judgements are consulted. Every emitted
grep line and read response is checked against the pinned source; hypothetical
read windows, coordinates without source bytes, and error messages earn no
credit. Grep-only hits are included. For this negative proof, even one emitted
line overlapping a grade-3 span counts in the comparator's favour. This is an
optimistic upper bound on coverage, not a substitute for the answer-bearing
equal-recall scorer. Zero even under that bound proves a miss for every valid
positive target; a nonzero result does not prove sufficient evidence.

`grepread.json` preserves complete transcripts, exact response bytes, response
SHA-256 and byte counts, both frozen tokenizer counts, verified source
coordinates, all query-level overlap results, and source/contract hashes.
Two executions per query produced identical full transcripts for **44/44**
records. Tokenization uses the checked-in SHA-verified ordinary cl100k artifact
with `CGO_ENABLED=0`; no model or network call is needed.

Only five questions have even an optimistic source overlap: `cb-02`, `cb-03`,
`cb-04`, `cb-08`, and `cb-26`. No median or interval over these five is valid:
the contract explicitly forbids complete-case substitution.

## Concrete failure mechanisms

For `cb-14` (shell-completion dispatch), the comparator retains conversational
terms including `the`. Its global first-20-match cap fills entirely in
`active_help.go`, starting at the copyright/license comments. Its two reads
emit lines 1–40 and 41–67 of that file. The grade-3 dispatch implementations are
in `completions.go:193-264` and `completions.go:266-520`, absent from the entire
preserved transcript.

For `cb-01`, the exact question is `ExecuteC`. Even without conversational
terms, early test call sites fill the global match cap before the definition
in `command.go:1051-1137`. The eight reads are in `active_help_test.go` and
`args_test.go`. Thus removing stopwords alone would not solve the comparator
problem: matching file order and stopping after the first global matches can
hide a definition behind its uses.

`TestGrepReadDevSources_SearchCapCanHideExactDefinition` minimizes that second
failure to two files: 20 early uses in `a_test.go` hide an exact declaration in
`z.go`. The test characterizes the frozen baseline; it does not change it.

## Separate this from the candidate's quality gates

The existing candidate-admission dev report still passes the three ranking
targets: architecture-flow nDCG@10 **0.4624671523** against **0.4578575263**,
NL-behaviour **0.7029047224** against **0.5449702531**, and exact-identifier
Top-1 **4/4**, matching the required 1.0. The CLI also prints a PASS for its
historical six-query bundle artifact; that is not a new 40-query evaluation.
It exits 1 on the historical qrel-blind smoke. That result is not a rating of
the current candidate and may not be relabeled as one.

The current candidate capture has grade-3 source overlap on **37/40** dev
questions and at least one fully contained grade-3 span on **34/40**. Neither
number measures blind answer sufficiency. Three whole-file path qrels explain
part of the gap between overlap and full containment; complete-file emission
is not a justified requirement for answering a path question. Actual zero
overlap remains on `ci-511`, `ci-678`, and `ci-1222`.

Those candidate defects and the missing candidate-bound blind rating remain
real work. Fixing them cannot repair the frozen comparator's 35 misses. A new
LLM or embedding model cannot change these comparator transcripts either.

## Decision boundary / next useful work

Keep the frozen contract, dataset, budget, scorer, thresholds, and old outcomes
unchanged. Do not publish a savings claim or declare release readiness.

A representative comparison requires an **explicitly approved, separately
versioned** comparator/method, not a hidden change to `GrepRead/1`. A design
to review is a query-only search/read policy that distinguishes exact-symbol
lookup from natural-language search, chooses useful terms, and ranks matches
before bounded reading instead of exhausting a global file-order prefix.
It must remain judgement-blind and preserve exact response bytes. Its policy
must be frozen before measuring its quality; no success is promised for it.
This preflight does not implement or authorize that method change.

Independently, continue development-only candidate work on missing sources,
then freeze a clean candidate and arrange a candidate-bound independent blind
evaluation under an explicitly agreed protocol. The current contract does not
say that a new holdout must automatically be invented. This session does not
open or run the sealed split, replace it, self-rate bundles using known qrels,
commit unrelated work, publish a release, or update gate evidence pointers.

The diagnosis skill led to a minimized baseline reproduction and a fail-closed
development preflight instead of more candidate tuning for a claim that the
comparator already makes undefined.

## Reproduce

From the repository root, with a clean pinned Cobra checkout:

```sh
export CGO_ENABLED=0
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370
export GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact"

GRAPHI_GREPREAD_DEV_OUT=/tmp/graphi-grepread-dev.json \
GRAPHI_GREPREAD_REQUIRE_ALL=1 \
go test ./internal/eval/retrieval -run '^TestGrepReadDevCapture$' -count=1 -v
```

Expected **exit 1**: `35/40` zero-overlap queries; `44/44` identical
transcripts. The artifact is written before the failure. Without
`GRAPHI_GREPREAD_REQUIRE_ALL=1`, successful capture exits 0, but its JSON status
still says `frozen_savings_claim_blocked_by_comparator_misses`; capture success
is not a release pass. A missing corpus/tokenizer or bad source roundtrip is
an error, never a zero-query success.

Offline artifact, source-provenance and minimized-reproduction checks:

```sh
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestGrepReadDev(Sources.*|Artifact)$' -count=1 -v
```

The offline check rederives coverage coordinates from the preserved transcript
and recomputes all digests and tokenizer counts. Recapture additionally checks
those bytes against the pinned source tree. No test consults the sealed data.

Existing ranking target check (expected exit 1 on the historical blind gate):

```sh
CGO_ENABLED=0 go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-06-candidate-admission-dev/after/cobra-v2-dev-report.json
```

The candidate before/after measurements and their complete reproduction
commands remain in `../2026-09-06-candidate-admission-dev/README.md`.

## Verification in this preflight

- Comparator capture reproduced the 35/40 misses; its strict readiness check
  exits 1. `grepread.json` SHA-256:
  `247af5b587bc011c805b93e007e4cc0d8ae2e10fe05c3235794aed4ded52eb7c`.
- A fresh two-index `TestRecoveryDevCapture` run produced 44/44 identical MCP
  payloads, digests and tokenizer counts despite distinct internal freshness
  generations. Its entire output file is byte-identical to the preserved
  candidate-admission `bundles-after.json`, SHA-256
  `99e27862c8af1716fec566e16452a281e870fede441a88f2a63f6c51e919f1fe`.
  Thus overlap remains 37/40 and full-span containment remains 34/40.
- The existing seven-baseline ranking harness was rerun on all 44 dev records;
  all reproducible baseline results equal the candidate-admission report.
- `CGO_ENABLED=0 go test ./internal/eval/retrieval
  ./engine/agenttools/taskctx ./engine/context ./engine/retrieval` passed.
- `CGO_ENABLED=0 go test ./internal/eval/tokenizer ./engine/embed/static`
  passed, including the run-artifact pin inventories.
- `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go run ./cmd/layerguard`, and
  `git diff --check` passed. The whole `go test ./...` suite was not rerun in
  this preflight; the commands above state the actual verification scope.
- The methodology, thresholds, and sealed dataset retain their previous
  SHA-256 values. No production retrieval behavior was changed in this
  preflight; it adds the diagnostic, artifact checks and this negative result.
