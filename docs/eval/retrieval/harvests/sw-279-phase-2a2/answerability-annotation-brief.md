# SW-279 Phase 2 answerability annotation brief

You decide whether a sealed candidate question is **answerable at the pinned Cobra commit**, and
if it is, you cite the source span that answers it. This is Section 8 step 4: it happens only
after question, stratum, rubric, family and provisional split are sealed, and the seal record
proves that ordering.

## The pin, and checking it before you read anything

The pin is `a0a6ae020bb3899ff0276067863e50523f897370`. Before you open a single file, run:

```
git -C "$COBRA_ROOT" rev-parse HEAD
```

and confirm it prints exactly that. Section 4: "Source annotators may then read and search a local
checkout only after verifying that its `HEAD` is the exact pin." If it prints anything else, stop
and report — do not annotate against a different tree.

"The pinned repository" means the tracked files at exactly that commit. Not another Cobra release,
not the current default branch, not a dependency checkout, not a web page, not an installed binary,
and not your memory of what Cobra looks like.

## What you may and may not use

**Allowed, and only inside the verified checkout:** reading files, `grep`, `git grep`, `git log`
on tracked files at the pin, and ordinary source navigation.

**Forbidden:**
- **No retrieval.** No `graphi` CLI, no `mcp__graphi__*`, no `task_context`, no semantic or lexical
  search tool, no ranked result, no score, no bundle, no saved hit list, no metric, no trace.
  Section 8 forbids these to source annotators before the final seal, and this annotation is
  before it. Plain `grep` inside the checkout is allowed; a retrieval engine is not.
- **No GitHub access.** No `gh`, no web fetch, no browser. Do not read the issue's comments, its
  labels, its linked pull requests, or anything a maintainer said. You have the question; that is
  the whole of what the reporter is allowed to tell you.
- **No other repository.** Do not read the `graphi` repository's own source, its datasets, its
  tests, or any previous harvest.
- **No search-effort reasoning.** Section 4 is explicit: "Difficulty is not D5. Failure to find an
  answer quickly, a large search area, an answer deep in a call chain, poor identifier overlap, or
  an expectation that retrieval will miss is never a reject reason." There is no time budget and no
  file-count budget. If a question is hard, keep looking.

## The decision, per question

Read Section 4 of `docs/eval/retrieval/dataset-v2-inclusion-rule.md` verbatim. In outline:

**`answerable`** requires all of A1–A4:
- **A1** the pinned repository contains an *affirmative* answer, not just evidence that the thing is
  absent;
- **A2** at least one finite, contiguous span in a **tracked, regular `.go` file at the pin**
  directly supports the rubric's required answer — containing the actual value, condition,
  mechanism, operation or procedure asked for. A nearby name, a call edge, a test assertion or a
  relation-only citation is **not** enough on its own. Extra supporting spans may be in other
  tracked text files, but documentation alone cannot establish answerability;
- **A3** the answer can be written from `Q` and the pinned repository alone — no issue discussion,
  no external docs, no dependency source, no historical commit, no experiment, no private state;
- **A4** the span answers the question *as written at the pin*, without quietly translating a
  question about another version into a question about this one.

**`not_answerable`** requires a **positive** finding of one of D1–D5, cited:
- **D1** the behaviour is explicitly tied to another release/branch/commit or a before/after
  regression, and removing that version context would change the question's meaning;
- **D2** the conclusion depends on values that exist only while a particular program runs;
- **D3** the necessary answer exists only outside the pinned tree;
- **D4** the honest answer is only that Cobra does not support the capability — absence is never
  converted into a grade-3 span;
- **D5** you can find related code or tests but no span satisfying A2, or the proposed answer needs
  speculation or synthesis the cited bytes do not support.

**`unresolved`** is a real third option and you must use it rather than guess. Section 4: "An
unresolved candidate is not silently treated as a reject: it blocks completion of Phase 2 and is
reported." Marking something unresolved is not a failure on your part; misreporting it as a reject
is.

## Grading the spans

Grades are the existing four, not redefined for this dataset:
- **3 — exact answer:** the span contains the answer-bearing bytes needed to satisfy the sealed
  answer mode, rather than merely pointing toward them.
- **2 — directly relevant:** materially explains or connects the answer but cannot satisfy the
  answer mode without another span or non-local inference.
- **1 — marginal:** an example, test, sibling, caller or nearby declaration that corroborates the
  topic without explaining the requested answer.
- **0 — irrelevant/negative:** a plausible false positive.

At least one reviewed grade-3 `.go` span is mandatory for an `answerable` verdict. Use the
**smallest contiguous range that preserves the answer-bearing declaration or documentation unit**.
Do not enlarge a range to catch anything — there is nothing to catch, because you have no retrieval
output and must not seek any.

Every span records: repository path (relative to the checkout root), inclusive `start_line` and
`end_line`, an `anchor` substring that occurs **inside that line range at the pin**, a `grade`, a
`reason`, and your name as `annotator`. Verify each anchor by re-reading the cited lines. A span
whose anchor does not resolve is a violation, and a downstream check will catch it.

The sealed rubric is given to you per question. **Do not rewrite it, do not add bespoke expected-
answer wording, and do not change an answer mode.** Section 6 names all three as violations. Your
job is to decide whether the pinned source satisfies the rubric as sealed.

## What you write

One JSON object per line, ascending `issue_number`, in the output file named in your task:

```
{"issue_number": 1234, "verdict": "answerable", "disqualifier": null, "judgements": [{"path": "command.go", "start_line": 1146, "end_line": 1168, "anchor": "func (c *Command) ValidateRequiredFlags(", "grade": 3, "reason": "...", "annotator": "<your name and model>"}], "note": "..."}
{"issue_number": 1235, "verdict": "not_answerable", "disqualifier": "D4", "judgements": [], "note": "<the positive evidence for D4, citing the issue text and/or pinned source>"}
{"issue_number": 1236, "verdict": "unresolved", "disqualifier": null, "judgements": [], "note": "<what you could not settle and why>"}
```

`verdict` is exactly one of `answerable`, `not_answerable`, `unresolved`. A `not_answerable` row
**must** name a disqualifier and give its positive evidence in `note`; "I could not find it" is D5
only if you can say what you did find and why it fails A2, and it is never a reason on grounds of
effort.

Finally write an attestation JSON at the path named in your task:

```
{
  "schema": "sw-279-answerability-annotator-attestation/v1",
  "actor": "<your name and model>",
  "role": "source annotator",
  "attested_at_utc": "<ISO-8601 Z>",
  "checkout_head_verified": "a0a6ae020bb3899ff0276067863e50523f897370",
  "assigned_issue_numbers": [...],
  "output_sha256": "<sha256 of your output file>",
  "statements": ["..."]
}
```

The statements must say, in your own words and only where true: that you verified the checkout HEAD
before reading anything; that you ran no retrieval tool and saw no ranked, scored or bundled result;
that you made no GitHub or web request and read no issue comment; that you read no repository other
than the pinned Cobra checkout; that every anchor you cite occurs inside its cited line range at the
pin; and that no verdict of yours was decided by how long a search took. **If any of that would be
false, say so plainly instead of leaving it out.**
