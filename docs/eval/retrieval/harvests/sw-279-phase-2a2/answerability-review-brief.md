# SW-279 Phase 2 answerability review brief

You are the **independent reviewer** for someone else's answerability annotations. Section 4 of the
frozen inclusion rule requires every proposed grade-3 span to record "annotator, and an independent
reviewer", and Section 6 makes "a missing independent reviewer" a violation. You did not make these
judgements and you are not here to ratify them.

You have not seen, and must not seek, the annotator's reasoning beyond what is written in the file
you are given.

## The pin, checked first

The pin is `a0a6ae020bb3899ff0276067863e50523f897370`. Before reading any file, run
`git -C "$COBRA_ROOT" rev-parse HEAD` and confirm it prints exactly that. A span is only valid at
the pin; a checkout at any other commit invalidates the whole review.

## What you may and may not use

Allowed, inside the verified checkout only: reading files, `grep`, `git grep`, ordinary navigation.

Forbidden, exactly as for the annotator: **no retrieval of any kind** (`graphi`, `mcp__graphi__*`,
`task_context`, any semantic or lexical search tool, any rank, score, bundle, metric or trace);
**no GitHub or web access** (no issue comments, labels, or linked pull requests); **no other
repository**; and **no search-effort reasoning** — "hard to find" is never a defect in a span and
never a reason to reject a question.

## What you check, per row

Section 4's own detection procedure: "A reviewer detects a violation by verifying the checkout SHA,
resolving every span and anchor, checking the span against the frozen rubric, and examining every
rejection for positive D-clause evidence."

For each row in the annotator's file:

1. **Resolve the span.** Open `path` at the pin and read `start_line`..`end_line` inclusive.
   - Does the file exist at the pin, tracked and regular?
   - Is the range within the file?
   - Does `anchor` occur **inside that range**, verbatim?
2. **Check the grade against the sealed rubric.** For a grade-3 span, does it contain the
   answer-bearing bytes needed to satisfy the sealed answer mode, or does it merely point toward
   them? A span that names the right symbol without containing the mechanism, value, condition or
   procedure asked for is **not** a 3. Padding a range to make it look answer-bearing is a
   violation; so is grade inflation.
3. **Check A1–A4 for every `answerable` row.** In particular A2 — at least one grade-3 span in a
   tracked `.go` file — and A3, that the answer needs nothing outside the pinned tree.
4. **Check every `not_answerable` row for positive D-clause evidence.** The row must name a
   disqualifier and cite evidence for it. A rejection whose real reason is "I could not find it",
   "too hard", a time limit, or an expectation about retrieval is a violation, and you must say so
   rather than accept it.
5. **Check every `unresolved` row.** Is it genuinely unsettled, or is it a reject or an accept in
   disguise? An unresolved row blocks completion of Phase 2 and is reported, so it must be real.

Where you disagree, say what the correct verdict or grade is and why, citing the pinned bytes. Do
not edit the annotator's file.

## What you write

One JSON object per line, ascending `issue_number`, at the path named in your task:

```
{"issue_number": 1234, "annotator_verdict": "answerable", "reviewer_verdict": "answerable", "agrees": true, "span_checks": [{"path": "command.go", "start_line": 1146, "end_line": 1168, "anchor_resolves": true, "grade_as_annotated": 3, "grade_as_reviewed": 3, "grade_agrees": true, "note": "..."}], "note": "...", "reviewer": "<your name and model>"}
```

- `anchor_resolves` must be the result of you actually reading those lines, not an assumption.
- `grade_agrees: false` and `agrees: false` are the useful outputs of this job. A review that agrees
  with everything is either a very clean batch or a review that did not happen, and the difference
  has to be visible in your span notes.

Then an attestation at the path named in your task:

```
{
  "schema": "sw-279-answerability-reviewer-attestation/v1",
  "actor": "<your name and model>",
  "role": "independent reviewer",
  "attested_at_utc": "<ISO-8601 Z>",
  "checkout_head_verified": "a0a6ae020bb3899ff0276067863e50523f897370",
  "reviewed_issue_numbers": [...],
  "annotator_file_sha256": "<sha256 of the file you reviewed>",
  "output_sha256": "<sha256 of your output>",
  "statements": ["..."]
}
```

Statements, in your own words and only where true: that you verified the checkout HEAD first; that
you opened and read every cited span rather than trusting the annotator's description; that you ran
no retrieval tool and saw no ranked, scored or bundled result; that you made no GitHub or web
request; and that no verdict of yours turned on how hard something was to find. **If any of that
would be false, say so plainly.**
