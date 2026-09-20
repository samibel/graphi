# Preregistered third fresh-holdout method

Status: **public pre-capture freeze scaffold** for product candidate
`task_context/2-compact/14`. The candidate commit is the freeze commit chosen
by the operator and is not known to the curator; it remains `to be bound at
freeze` in the curation pre-registration. This file contains no holdout
content and authorizes no evaluation response.

The frozen governing method is
`docs/eval/retrieval/methodology-v2.md`, which incorporates
`docs/eval/retrieval/methodology.md` by reference and selects measurement
contract `sw266-measurement-contract/2`, qrel-blind smoke contract
`sw280-qrel-blind-smoke-evaluation/2`, and `followup_max_lines: 120`.
The measured candidate object and the graded material are the preserved
transcript: the complete `task_context/2` response plus, only when that
response designates one, the exact single follow-up read. The rater prompt
and grader packet carry both responses in order when a follow-up exists and
one response otherwise. Each response remains an indivisible payload
boundary and the earliest-prefix charging rule in methodology-v2 applies.

The independently curated sealed population is fixed before candidate capture:

- `N=64` answerable holdout queries;
- `k=56` minimum passing responses;
- two-sided exact Clopper–Pearson 95% confidence interval;
- lower confidence bound floor `3/4`;
- no `no_hit` items in the answerable population; and
- all items carry one reviewed exact grade-3 source span at the pinned corpus SHA.

The answer-span ceiling was computed only after `k` was fixed by the frozen
smallest-count rule. Its aggregate report establishes that all 64 questions
have at least one complete feasible span; it did not alter `k`.

The dataset, `N`, `k`, strata, families, qrels, transcript contract, follow-up
cap, and rubric may not be changed in response to candidate behavior. There
are no retries or substitutions.

## Mandatory sequence

1. Verify the base, frozen candidate, corpus, dataset, method, budget, target,
   rubric, contract-version, and follow-up-cap identities recorded in
   `curation-pre-registration.json` and the operator's precondition record.
2. Freeze the harness preconditions and candidate binding to one resolvable
   commit carrying actor-visible `task_context/2-compact/14`.
3. Capture every real `task_context/2` response for the sealed population and,
   exactly when it designates one, capture its single 120-line-capped follow-up
   read under `task_context/2-followup-read/1`.
4. Validate every two-slice or one-slice transcript, then hash and commit the
   complete capture, two-response prompts, and preserved transcripts.
5. Only after that capture commit may any primary, grader, or adjudicator run.

Starting a rater, disclosing a prompt, opening a candidate response, or taking
a qrel-selected read before step 4 completes invalidates the run. This
curation work performs none of those stages.

## Freeze-time operator binding and per-item execution discipline

At freeze the operator binds stable logical participant/configuration IDs to
concrete non-alias model IDs, `codex-cli 0.153.4`, reasoning effort `high`, and
the controls below. OpenAI exposes no immutable backend snapshot or build
digest; model-level behavior is therefore not byte-reproducible, and the
limitation must be recorded rather than replaced by an invented identity.

The operator performs two captures from the same committed static-input tree
in separate clean worktrees. Each capture constructs a fresh production index
in a different temporary work directory and invokes the real MCP stdio
`task_context/2` path plus only its response-designated follow-up read. Before
any prompt is delivered, the operator mechanically requires:

- 64 transcripts in each build and distinct ready index generation identities;
- actor-visible `task_context/2-compact/14` on every first response;
- exact byte, response-digest, and cl100k-token-count equality for all first
  response pairs and every corresponding follow-up pair;
- an exact maximum of 1,200 cl100k tokens per complete MCP first response;
- every follow-up, when present, to match the first response's designation and
  the pinned checkout, with at most 120 lines; and
- identical two-response prompts and preserved-transcript bytes for the
  official run.

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
the confidential item-local JSONL log are hashed, made read-only, and
committed at their stage. Their content is never sent to the root orchestration
agent.
