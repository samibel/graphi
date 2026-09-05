# SW-279 Phase 2a semantic classification brief

You are classifying 139 GitHub issue rows against a frozen rule. You are a **labelled solo
substitute** for an independent selector, not an independent selector: you were commissioned by the
same orchestrator that runs the rest of this story. That is recorded rather than papered over.

## Your inputs — and they are the only two files you may open

1. `docs/eval/retrieval/dataset-v2-inclusion-rule.md` — the frozen rule. Section 3 is the clause you
   apply. Sections 1, 2, 8 and 9 tell you what you may and may not do.
2. `docs/eval/retrieval/harvests/sw-279-phase-2a2/semantic-review.jsonl` — 139 JSON lines. Each has
   `issue_number`, `author`, `created_at`, `T` (the raw issue title), `Q` (the mechanically derived
   question), `body` (the issue author's opening body), and digests.

These rows have already passed the Section 2 syntactic gate. You are not re-checking that.

## What you must not do

Every one of these is a rule violation, not a style preference:

- **No GitHub access of any kind.** No `gh`, no web fetch, no browser, no GitHub connector, no MCP
  GitHub tool. Do not look up an issue, its comments, its labels, its state, its linked pull
  requests, or anything a maintainer said. If a body contains a URL, treat it as literal text and
  **do not open it**.
- **No repository source access.** Do not read, search, grep, or navigate any Cobra source, any
  `graphi` source, any test, or any dataset file. You are deciding whether a question is a candidate
  question — not whether it has an answer. Answerability is a later, separate step performed by
  different people against a pinned checkout.
- **No retrieval of any kind.** No semantic search, no lexical search, no `graphi` tool, no ranked
  result, no saved hit list, no score, no bundle, no metric.
- **No sight of `docs/eval/retrieval/harvests/sw-279-phase-2a-superseded/`.** A previous run
  classified these same 139 rows and its output was discarded as contaminated. If you saw it, this
  run would corroborate nothing. Do not open that directory, do not list it, do not grep it.
- **No preference based on how easy an answer would be to find.** Section 8 forbids preferring,
  rejecting, or rewriting a question because a retriever would find it easy or hard. You have no
  retrieval output and must not reason as if you did.
- **No stopping at a number.** Classify all 139. There is no target. A low candidate count is a
  finding, not a failure.

## What you decide

For each of the 139 rows, apply frozen rule Section 3 to `T`, `Q` and `body`:

- **`candidate`** if all of C1–C5 hold and none of E1–E5 applies.
- **`reject`** otherwise, naming every clause that decided it.

Read Section 3's clause text yourself; do not work from a summary. In particular note that E1's last
sentence and E2's last sentence both carve out general questions about existing behaviour, and that
C4 tolerates a user's example so long as the answer does not depend on it.

Where a row genuinely sits on a semantic boundary, say so in the rationale rather than smoothing it
over. Section 3: "Disagreement on a semantic boundary is not hidden: it is recorded for review
rather than resolved from source-search difficulty or yield."

## What you write

One file, `docs/eval/retrieval/harvests/sw-279-phase-2a2/semantic-classification.jsonl`, with
exactly 139 lines in ascending `issue_number` order, one compact JSON object per line with these
keys and no others:

```
{"issue_number": 124, "verdict": "reject", "deciding_clauses": ["E2"], "rationale": "...", "boundary_case": false}
```

- `verdict` — `"candidate"` or `"reject"`.
- `deciding_clauses` — for a candidate, exactly `["C1","C2","C3","C4","C5"]`. For a reject, the
  clause IDs that decided it, most decisive first, drawn only from
  `C1 C2 C3 C4 C5 E1 E2 E3 E4 E5`.
- `rationale` — one sentence, using only the allowed issue text. Never cite a comment, a label, a
  maintainer reply, a linked page, or the pinned source; you have not seen any of them.
- `boundary_case` — `true` when you judge the row a genuine semantic boundary that a reviewer should
  re-examine, `false` otherwise.

And one file, `docs/eval/retrieval/harvests/sw-279-phase-2a2/semantic-classifier-attestation.json`:

```
{
  "schema": "sw-279-semantic-classifier-attestation/v1",
  "actor": "<a name for yourself, including your model>",
  "role": "labelled solo substitute for an independent selector",
  "attested_at_utc": "<ISO-8601 Z>",
  "input_semantic_review_sha256": "<sha256 of semantic-review.jsonl>",
  "input_rule_sha256": "<sha256 of the frozen rule>",
  "output_sha256": "<sha256 of semantic-classification.jsonl>",
  "statements": [ "..." ]
}
```

The statements must say, in your own words and only if each is true: that you used only issue
number, author, creation time, title and opening body; that you accessed no GitHub API, connector or
web page; that you opened no external link target; that you read no Cobra or graphi source and ran
no search or retrieval; that you did not open the superseded harvest directory; and that you
classified all 139 rows without regard to any count.

**If any statement would be false, say so instead.** A disclosed deviation is recoverable; an
undisclosed one destroys the artefact and everything built on it. The last run's violation was
caught only because the actor wrote down a thing that counted against it.
