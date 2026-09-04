# SW-279 Phase 2a report

Status: stopped before family assignment, as required. This report covers only the population
manifest, allowed issue-text archive, candidate classification, and mechanical `T` to `Q`
derivation.

## Population seal and phase ordering

- Frozen rule commit: `a0a13a757c66e8d4f0747d4a68955fe95d072573`
- Frozen rule SHA-256: `d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c`
- Rule commit time: `2026-09-03T22:57:37.000000Z` (`2026-09-04T00:57:37+02:00`)
- First successful population fetch start: `2026-09-03T23:07:53.583000Z`
- Ordering evidence: fetch start is 616.583 seconds after the rule commit time.
- Population: 1,255 distinct GitHub objects classified by `is:issue`, created at or before the
  cutoff, sorted by ascending integer issue number.
- Manifest serialization: each decimal issue number followed by LF, including the final LF.
- Manifest SHA-256: `b9f712af1bea40bbde437dee649a35346de023891839e8ae148138a94a8c4a17`

The count matches the 1,255-row Phase 1 expectation. The ledger, archive, and manifest each contain
exactly 1,255 rows in the same order.

## Candidate-classification counts

- Candidates: 66 (5.26% of the population)
- Rejects: 1,189
- Unresolved: 0
- Syntactically eligible and semantically reviewed: 139
- Mechanical Section 2 rejects: 1,116
- Semantic C/E rejects: 73

The exclusive primary-clause counts below sum to all 1,189 rejects:

| Primary clause | Rejects |
|---|---:|
| `S2_CODEPOINT_COUNT` | 1 |
| `S2_TOKEN_COUNT` | 50 |
| `S2_QUESTION_MARK` | 1 |
| `S2_FIRST_TOKEN` | 1,064 |
| `C1` | 1 |
| `C2` | 12 |
| `C3` | 5 |
| `E1` | 29 |
| `E2` | 11 |
| `E3` | 1 |
| `E4` | 12 |
| `E5` | 2 |

Because the ledger retains every deciding clause, some rejects have more than one clause. The
nonexclusive clause-mention counts are: `S2_CODEPOINT_COUNT` 1, `S2_TOKEN_COUNT` 51,
`S2_QUESTION_MARK` 1, `S2_FIRST_TOKEN` 1,116, `C1` 1, `C2` 12, `C3` 11, `E1` 30, `E2` 13,
`E3` 1, `E4` 42, and `E5` 3.

## T-to-Q spot checks

| Issue | Original `T` | Derived `Q` |
|---:|---|---|
| 2 | `How to "wrap" command line arguments so they won't be interpreted as commands?` | `How to "wrap" command line arguments so they won't be interpreted as commands?` |
| 365 | `Question: How do we provide conditional default values` | `How do we provide conditional default values` |
| 434 | `question: how to find what flags is been called in the command` | `how to find what flags is been called in the command` |
| 821 | ``Is there  a way to not show `Global Flags` on --help.`` | ``Is there a way to not show `Global Flags` on --help.`` |
| 1111 | `Question: How to disable message "Run 'xxxxx --help' for usage." ?` | `How to disable message "Run 'xxxxx --help' for usage." ?` |
| 1564 | `[Question] How to make arguments show up in usage text?` | `How to make arguments show up in usage text?` |
| 1889 | `[question] How to pass two values for an option?` | `How to pass two values for an option?` |

All 1,255 ledger `Q` values were regenerated from `T` during the final audit and byte-compared; all
66 candidate questions also passed the frozen syntactic test.

## Revised yield estimate

The observed candidate-gate yield is 66 issues (5.26%), before family dependence and before any
answerability inspection. The Phase 1 estimate was 38–63 final answerable issues, point estimate
about 50. That range would now require 58–95% of the 66 candidates to survive A1–A4.

Without inspecting source, the revised estimate is approximately 30–45 final answerable issues,
with a point estimate around 38. This is plainly below the Phase 1 point estimate of 50. It is not
an answerability verdict and did not influence candidate selection. Family count and dev/holdout
yield cannot yet be estimated reliably because this phase intentionally assigned neither.

## Ambiguities and practical limitations; not repaired

1. Section 2 lists overlapping markers (`[question]` and `[question]:`, `(question)` and
   `(question):`) without explicitly saying whether the longest match wins. The implementation
   uses the complete/longest marker first. No population title used either colon-bearing bracketed
   or parenthesized form, so this ambiguity did not affect a row.
2. C1 admits questions about existing capability while E2 excludes requests framed as “can Cobra
   support ...”. Present-capability questions and implicit change requests are not mechanically
   separable. The ledger treats a request for current behavior as C1 and an explicit desired
   addition/change as E2, but this remains a semantic boundary.
3. C3 says `Q` itself must express standalone meaning, while the overall predicate is applied to
   title and opening body. The rule does not state how much the body may disambiguate a vague `Q`.
   Rows whose `Q` depended on an omitted screenshot, linked example, or undefined reference were
   rejected under C3.
4. The requested local `gh api` route was unusable: `gh auth status` reported that the active token
   was invalid, and direct API networking was blocked. The installed GitHub connector was reachable
   but returned broad normalized issue envelopes containing fields outside the requested
   projection. Before the manifest seal only `issue_number` and `created_at` were projected; during
   archive/classification only issue number, author login, creation time, title, and opening body
   were projected. Label/comment values were discarded and never used or emitted to the selector.
   The first-fetch record and access ledger disclose this limitation. If Section 1's “must not be
   fetched” is interpreted to prohibit transport-level overfetch even when those values are never
   exposed or used, this run is noncompliant and requires the owner's decision; this report does
   not silently relax that clause.

## Boundary confirmation

No family, stratum, rubric, split, answerability verdict, source judgement, or dataset row was
assigned. No pinned Cobra source was inspected or searched. No candidate or baseline retrieval was
run or inspected. `docs/eval/retrieval-targets.json` and all dataset files are untouched. No commit
was created or amended.
