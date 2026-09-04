# SW-279 Phase 2 stratum assignment brief

You assign one stratum to each accepted candidate question, applying Section 5 of the frozen
inclusion rule. This happens **before any source access**, by anyone, for any of these questions.
Section 5 says so explicitly, and Section 8 makes a candidate-directed source access before the
seal a detectable violation.

## Your inputs — the only files you may open

1. `docs/eval/retrieval/dataset-v2-inclusion-rule.md` — Section 5 is the operative clause. Read it
   verbatim; do not work from a paraphrase.
2. `docs/eval/retrieval/harvests/sw-279-phase-2a2/stratum-input.jsonl` — one line per candidate,
   with `issue_number`, `Q` (the question) and `body` (the issue author's opening body).

## What you must not do

- **No source access.** Do not read, grep, glob or navigate any Cobra source, any `graphi` source,
  any test, or any dataset file. Section 5 assigns a stratum from the question and the opening body
  alone: "Apply the rules in this precedence order to `Q` and the issue author's opening body,
  before source access."
- **No retrieval.** No `graphi` tool, no `mcp__graphi__*`, no search, no ranked result, no score.
- **No GitHub access.** No `gh`, no web fetch, no connector. Do not look up the issue.
- **No counting toward a target.** Section 5's own violation clause: "Assignment based on where an
  answer was found, the number of answer spans, observed retrieval performance, or a desired
  per-stratum count is a violation." There is no per-stratum target. Do not balance the strata.

## The decision

Section 5 gives three strata in strict precedence order. Take the **first** that applies:

1. `config_docs` — the requested answer is a caller-facing procedure or setting: how to use, set,
   enable, disable, register, customize, or generate something through Cobra's public API, fields,
   options, flags, templates, or documented commands.
2. `architecture_flow` — otherwise, when the question **explicitly** asks about a lifecycle,
   traversal, propagation, registration path, dispatch path, ordering, or interaction among two or
   more named stages or components.
3. `nl_behaviour` — every other candidate: the location, mechanism, condition, meaning, or reason
   for one existing Cobra behaviour.

Precedence is not a tie-break you may reorder. If rule 1 applies, the answer is `config_docs` even
if rule 2 would also fit. Rule 3 is the residual and takes everything left.

Issue-derived questions may **never** be assigned `exact_identifier`, `exact_path`, `ambiguous`, or
`no_hit`.

## What you write

`docs/eval/retrieval/harvests/sw-279-phase-2a2/stratum-assignments.jsonl`, one compact JSON object
per line, in ascending `issue_number` order, one line per input row, keys exactly:

```
{"issue_number": 1234, "stratum": "config_docs", "first_applicable_rule": 1, "rationale": "...", "assigned_by": "<your name and model>"}
```

- `first_applicable_rule` is `1` for `config_docs`, `2` for `architecture_flow`, `3` for
  `nl_behaviour`. Section 5 requires the first applicable numbered rule to be recorded, not just the
  stratum. A downstream check refuses the seal if the number and the stratum disagree.
- `rationale` — one sentence, grounded only in `Q` and the opening body, naming which of rule 1's
  or rule 2's listed triggers you found (or saying that neither applied, for rule 3).
- `assigned_by` — your own name including your model.

Also write `docs/eval/retrieval/harvests/sw-279-phase-2a2/stratum-assigner-attestation.json`:

```
{
  "schema": "sw-279-stratum-assigner-attestation/v1",
  "actor": "<your name and model>",
  "attested_at_utc": "<ISO-8601 Z>",
  "input_sha256": "<sha256 of stratum-input.jsonl>",
  "input_rule_sha256": "<sha256 of the frozen rule>",
  "output_sha256": "<sha256 of stratum-assignments.jsonl>",
  "statements": ["..."]
}
```

The statements must say, in your own words and only where true: that you used only `Q` and the
opening body; that you opened no Cobra source, no `graphi` source, no dataset and no test; that you
ran no search and saw no retrieval output; that you made no GitHub or web request; and that you did
not balance the strata toward any count. **If any of that would be false, say so plainly instead.**
