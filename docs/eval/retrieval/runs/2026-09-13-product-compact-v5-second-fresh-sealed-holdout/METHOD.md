# Preregistered second fresh-holdout method

Status: **public pre-capture freeze** for product candidate
`1430daf27e795c8c3a7dcc230d858935044bc33f`. This file contains no holdout
content and authorizes no evaluation response.

The governing method is `docs/eval/retrieval/methodology.md`, using the same
qrel-blind bundle-sufficiency procedure, six answerable strata, exact pinned
token accounting, participant separation, and fail-closed validation as the
valid first fresh holdout. The run-local `grading-rubric.md` is byte-identical
to that run's frozen public rubric.

The independently curated sealed population is fixed before candidate capture:

- `N=64` answerable holdout queries;
- `k=56` minimum passing responses;
- two-sided exact Clopper–Pearson 95% confidence interval;
- lower confidence bound floor `3/4`;
- no `no_hit` items in the answerable population; and
- all items carry reviewed exact grade-3 source spans at the pinned corpus SHA.

The dataset, `N`, `k`, strata, families, qrels, and rubric may not be changed in
response to candidate behavior. There are no retries or substitutions.

## Mandatory sequence

1. Verify the base, candidate, corpus, dataset, method, budget, target, and
   rubric identities recorded in `curation-pre-registration.json`.
2. Freeze the harness preconditions and candidate binding.
3. Capture all real `task_context/2` responses for the sealed population.
4. Hash and commit the complete capture, prompts, and preserved bundles.
5. Only after that capture commit may any primary, grader, or adjudicator run.

Starting a rater, disclosing a prompt, or opening a candidate response before
step 4 completes invalidates the run. This curation commit performs none of
those stages.

## 2026-09-13 PRE-FREEZE OPERATOR AMENDMENT

This amendment precedes freeze, capture, and every response. Participant IDs
are stable logical role/configuration IDs, not conversation IDs. They bind a
concrete non-alias OpenAI Codex model ID, `codex-cli 0.153.4`, reasoning effort
`high`, and the controls below. OpenAI exposes no immutable backend snapshot or
build digest; model-level behavior is therefore not byte-reproducible, and the
limitation is recorded rather than replaced by an invented identity.

The operator performs two captures from the same committed static-input tree in
separate clean worktrees. Each capture constructs a fresh production index in a
different temporary work directory and invokes the real MCP stdio
`task_context/2` path. Before any prompt is delivered, the operator mechanically
requires:

- 64 responses in each build and distinct ready index generation identities;
- actor-visible `task_context/2-compact/5` on every response;
- exact byte, response-digest, and cl100k-token-count equality for all 64 pairs;
- an exact maximum of 1,200 cl100k tokens per complete MCP response; and
- identical prompt and preserved-bundle bytes for the official run.

The official capture and a content-free two-build reproducibility record are
committed before participant execution. A mismatch, degradation, drift,
overwrite, or incomplete build refuses the run; neither build is retried.

Every participant item is evaluated by one new process with exactly one
allowed prompt or grader packet:

```sh
test ! -e "$OUTPUT"
test ! -e "$EXECUTION_LOG"
codex exec --ephemeral --ignore-user-config --ignore-rules \
  -s read-only -m "$MODEL" -c 'model_reasoning_effort="high"' \
  -C "$EMPTY_ITEM_REPOSITORY" --json -o "$OUTPUT" - \
  < "$ONLY_ALLOWED_INPUT" > "$EXECUTION_LOG"

jq -e -s '
  (map(.type) ==
    ["thread.started", "turn.started", "item.completed", "turn.completed"]) and
  ([.[] | select(.type == "item.completed") | .item.type] ==
    ["agent_message"])
' "$EXECUTION_LOG" >/dev/null
```

Any other event or item type, including `command_execution`, `web_search`, or
an MCP event, is an immediate mechanical refusal without retry. Raw output and
the confidential item-local JSONL log are hashed, made read-only, and committed
at their stage. Their content is never sent to the root orchestration agent.
