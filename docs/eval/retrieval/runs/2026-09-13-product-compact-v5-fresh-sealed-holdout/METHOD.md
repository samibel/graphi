# Preregistered fresh-holdout method

Status: **candidate-neutral public scaffold**. This file contains no holdout
content and authorizes no evaluation run.

## Frozen method

The run uses the repository's qrel-blind smoke procedure. The harness addresses
the following inputs by SHA-256 in `precondition-record.json`:

- `docs/eval/retrieval/methodology.md`
- `docs/eval/retrieval-budgets.json`
- `docs/eval/retrieval-targets.json`
- this run's `grading-rubric.md`
- the independently curated sealed dataset

`docs/eval/retrieval/threat-model.md` remains governing public disclosure, but
the current freeze harness does not list it among the record's hashed inputs;
this scaffold does not claim otherwise.

The harness derives the complete answerable holdout population and its pass
threshold before any rater response exists. No query may be dropped, replaced
or retried after that derivation. The measured candidate bytes are the exact
MCP stdio JSON-RPC responses produced by the real `task_context/2` surface,
including envelope and terminating newline. The exact pinned cl100k tokenizer
counts those bytes.

## Curator preregistration and separation

No curator is presently assigned; therefore curation is a hard blocker.

Before creating the dataset, the curator must record a stable identity,
provider or employing organization where applicable, curation environment,
UTC start time, pinned corpus commit, and this independence statement:

> I did not implement or tune the candidate; I did not inspect candidate or
> development-evaluation outputs; and I did not reuse or consult any prior
> holdout question, judgement, answer, response, grade or result.

The curator works from the pinned corpus and public stratification method only.
They assign unique query and family identifiers, keep families in one split,
record the required strata and reviewed exact grade-3 source spans, validate
span coverage at the pinned corpus commit, and deliver exactly one immutable
dataset plus its SHA-256. Dataset creation is not performed by the candidate
team or by a primary rater, grader or adjudicator.

The candidate team and root orchestration agent may receive the path and digest
but not the dataset bytes, questions, judgements or answer keys. Absence of a
qualified curator, attestation, validated stratification, span coverage or
content digest is a refusal to freeze.

## Participant preregistration

Exactly two `primary` participants, one `grader` and one `adjudicator` are
declared in `participants.json` before capture. IDs are distinct. Every entry
records its provider, resolved immutable model/version identity, independence
basis and whether it participated in candidate implementation or dataset
annotation. At least one primary has `participated_in_track: false`. The grader
and adjudicator are independent of implementation and annotation and do not
serve as primaries.

Aliases such as “latest” are not identities. If the execution service cannot
prove the resolved model/build behind an alias, the run does not start.

## Visibility boundaries

- Primary: only the generated answer instructions, one query and its exact
  preserved bundle. No repository, dataset, qrels, answer key, other response,
  grade or prior-run material.
- Grader: only the frozen run-local rubric and one generated grader packet.
  The packet contains the response, exact bundle and reviewed grade-3 spans.
  The grader must not re-answer using external repository knowledge.
- Adjudicator: only the original query, answer instructions and exact bundle.
  No primary response or grade until its own answer is write-once sealed.
- Operator: may execute the harness and route minimum necessary artifacts, but
  does not implement, annotate, rate or grade. It does not manually inspect the
  dataset or answer keys and never sends holdout content to the root agent.

Isolation is instruction- and access-control-enforced, not a defence against a
malicious repository owner. The operator records the actual controls and any
limitation rather than claiming stronger isolation.

## Execution sequence

With `RUN`, `DATASET`, `COBRA`, `EMBEDDER` and the offline tokenizer directory
resolved, the independent operator performs:

```sh
test -z "$(git status --porcelain --untracked-files=normal)"
test -z "$(git -C "$COBRA" status --porcelain --untracked-files=normal)"

CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval \
  -blind-eval freeze -blind-eval-dir "$RUN" -dataset "$DATASET"

git add "$RUN/precondition-record.json"
git commit -m 'eval: freeze fresh compact holdout preconditions'

CGO_ENABLED=0 GRAPHI_EVAL_TOKENIZER_DIR="$TOKENIZER_DIR" \
go run ./cmd/retrieval-eval \
  -blind-eval capture -blind-eval-dir "$RUN" \
  -repo cobra -checkout "$COBRA" -embedder "$EMBEDDER"

git add "$RUN/pre-registration.json" "$RUN/capture-provenance.json" \
  "$RUN/prompts" "$RUN/bundles"
git commit -m 'eval: preregister fresh compact holdout'
```

No prompt is delivered before the second commit. Subsequent raw responses,
grades and adjudicator answers are created once at the harness-prescribed
paths. Each stage is sealed with `-blind-eval seal` before the next role sees
its permitted input. After all artifacts are committed, `-blind-eval decide`
revalidates frozen hashes and candidate binding and emits the only release
decision. Any missing, malformed, drifted, unbound or over-budget artifact is a
failure or refusal, never an invitation to repair and rerun the same holdout.
