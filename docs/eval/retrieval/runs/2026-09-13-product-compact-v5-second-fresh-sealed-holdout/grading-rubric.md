# Qrel-blind bundle-sufficiency grading rubric

Status: **public candidate-neutral frozen-input scaffold**. This rubric contains
no question, answer or judgement from any dataset.

Grade one response using only the material in its generated grader packet. Do
not consult the repository, another response, another grade, a prior run or
outside knowledge. The reviewed grade-3 spans in the packet are the reference
facts; they are not permission to infer facts absent from the preserved bundle.

## PASS

Return `PASS` only when all of the following hold:

1. The response directly and substantively answers the question asked.
2. Its material factual claims are supported by the exact preserved bundle.
3. It identifies the essential behavior represented by the reviewed grade-3
   spans to the extent needed to answer the question.
4. Any cited path or line range exists in the supplied bundle and supports the
   claim attached to it.
5. It contains no material contradiction, invented API, invented control flow
   or unsupported repository-specific assertion.

Paraphrase, different organization and additional bundle-supported detail are
allowed. A response need not copy the reviewed spans verbatim.

## FAIL

Return `FAIL` when any PASS condition is not met, including when the response:

- is materially incomplete, evasive or does not answer the question;
- relies on a required fact present only in the reviewed spans but absent from
  the preserved bundle;
- makes a material claim that cannot be verified from the bundle;
- cites a nonexistent or non-supporting location;
- contradicts the bundle or reviewed grade-3 facts; or
- appears correct only by relying on external repository knowledge.

Missing, empty or refused primary responses are handled as failures by the
harness and are not converted into answered responses by the grader.

## Required raw grade format

Write exactly one plain-text grade beginning with one of:

```text
PASS: <specific bundle-grounded rationale>
FAIL: <specific bundle-grounded rationale>
```

The rationale must name the decisive supported behavior or the decisive gap.
Do not include both outcomes, a score, a probability, a conditional verdict or
an alternative grade. A grade without a substantive rationale is invalid.

When uncertain whether a necessary fact is supported by the bundle, grade
`FAIL`; this gate measures bundle sufficiency, not the grader's ability to fill
missing context from memory.
