# Compact development sufficiency CLI

This is the `compact-dev-sufficiency/1` diagnostic, always labelled
`development_diagnostic_not_release`. The compiled-in dataset is only
`docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json` (digest pinned).
The compiled-in inputs are the two-build `bundles-after.json` from
`2026-09-07-answer-recovery-dev` and the query-only, source-verified
`GrepRead/2` transcripts from `2026-09-07-grepread-v2-dev`. The command combines
both retrieval channels and builds compact responses for all 44 development
questions before consulting qrels to register the 40 answerable questions.
For compact-dev/9, `prepare -repository` additionally hydrates exact Go
declarations and one bounded query-relevant reference from a clean Cobra
checkout whose HEAD must equal the dataset's pinned repository SHA.
Source budget is 250; `MinimumPassCount(40)` derives k=36. There are no dataset,
budget, k or threshold overrides.

The candidate must be committed and clean except for the exact run directory.
Run directories must be named repository-relative subdirectories beneath
`docs/eval/retrieval/runs/`. A dirty or untracked source file outside that
directory refuses every phase, even when omitted from the supplemental file
manifest. No command creates commits. This CLI cannot freeze the current dirty
worktree without a separately prepared clean candidate snapshot.

Prepare these two JSON files inside the selected run directory. The sorted,
unique candidate-file manifest contains repository-relative paths and SHA-256
of their actual bytes. Include the compact selector, harness and command source
and relevant build inputs. The full Git SHA plus clean-worktree check binds the
whole candidate; this file list provides additional inspectable identities.

```json
[
  {"path":"cmd/compact-sufficiency-dev/main.go","sha256":"<64 lowercase hex>"},
  {"path":"internal/eval/retrieval/compact_sufficiency_dev.go","sha256":"<64 lowercase hex>"},
  {"path":"internal/eval/retrieval/compact_wire_dev.go","sha256":"<64 lowercase hex>"}
]
```

The participant file freezes four distinct session IDs, including exact model
and provider identities. Two independent sessions may use the same model.
Substitute real available identities before preparation; placeholders are not
evidence that any external rater was invoked.

```json
{
  "primary": [
    {"id":"primary-a-session","provider":"provider","model":"exact-model-id"},
    {"id":"primary-b-session","provider":"provider","model":"exact-model-id"}
  ],
  "grader":{"id":"grader-session","provider":"provider","model":"exact-model-id"},
  "adjudicator":{"id":"adjudicator-session","provider":"provider","model":"exact-model-id"}
}
```

Use the four phases from the repository root (replace the SHA and query ID).
The variables below name only the new diagnostic directory and candidate SHA.

```sh
GRAPHI_DEV_RUN=docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency
GRAPHI_DEV_SHA=<exact-40-hex-candidate-commit>
GRAPHI_COBRA_CHECKOUT=<clean-checkout-at-the-pinned-cobra-sha>

CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev prepare \
  -run-dir "$GRAPHI_DEV_RUN" -candidate-sha "$GRAPHI_DEV_SHA" \
  -candidate-files "$GRAPHI_DEV_RUN/candidate-files.json" \
  -participants "$GRAPHI_DEV_RUN/participants.json" \
  -repository "$GRAPHI_COBRA_CHECKOUT"

CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev response \
  -run-dir "$GRAPHI_DEV_RUN" -query <query-id> -slot 0 \
  -status answered -response-file "$GRAPHI_DEV_RUN/raw/primary-0-answer.txt"

CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev grade \
  -run-dir "$GRAPHI_DEV_RUN" -query <query-id> -slot 0 \
  -outcome pass -rationale-file "$GRAPHI_DEV_RUN/raw/primary-0-rationale.txt"

CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir "$GRAPHI_DEV_RUN"
```

Send each answerer only its exact `prompts/<query-id>.txt`. It must have no
repository tools, answer key, other responses or grading context. Store its
first output verbatim before ingestion. The CLI records outputs; it does not
invoke a model, enforce its sandbox or prove that the operator used the stated
model. Preserve external invocation receipts separately inside the run.

Slots 0 and 1 are primaries. Slot 2 is a fresh blind adjudicator and is accepted
only after two answered primary responses have different grades. The grader
receives the development answer key after the relevant answer is sealed.
Missing/empty/refused/`INSUFFICIENT` answers fail mechanically and cannot be
graded or adjudicated away. For explicit missing/empty status, supply an empty
raw text file. Participant identities and response digests are resolved from
the frozen registration and record log; there are no identity override flags.

`decide` returns a snapshot with counts and digests only. An incomplete run
cannot produce `diagnostic_pass`. Add `-seal` only when the run is final: it
writes `outcome.json` once and closes all future appends. Identical preparation
is idempotent, but response and grade retries are always refused. No response,
prompt, judgement or rationale text is printed to stdout.

Focused tests (do not execute dataset tests that load the combined holdout):

```sh
CGO_ENABLED=0 go test ./cmd/compact-sufficiency-dev -count=1
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^(TestCompactDevSufficiency|TestMinimumPassCount)' -count=1
```
