# Qrel-blind bundle-sufficiency grading rubric

Version: `embedded-model-qualification-grading-rubric/1`, for the embedded-model
development qualification. This file is frozen by content hash as
`grader_prompt_sha256` in `preregistration.json`, and the same digest is
re-verified under role `grading_rubric` in every blind-evidence precondition
record before any grader packet is produced. Editing it after the freeze fails
the run.

Status: **public candidate-neutral frozen-input scaffold**. This rubric contains
no question, answer or judgement from any dataset.

Grade one response using only the material in its generated grader packet. Do
not consult the repository, another response, another grade, a prior run or
outside knowledge. The reviewed grade-3 spans in the packet are the reference
facts; they are not permission to infer facts absent from the preserved bundle.

The grader decides **pass** or **fail**. There is no third outcome, no partial
credit and no score.

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

An answered response that states it cannot answer — for example the single word
`INSUFFICIENT` followed by a sentence naming what was missing — is an honest
answer and a failed one. Grade it `FAIL`. It is an answered response and
requires a grade; it is not a missing response and not a mechanical refusal.

## The same conditions for every question

The five PASS conditions are the whole rubric and apply unchanged to every
question in the population, whatever kind of question it is. There is no
category-specific criterion. What counts as answering is fixed by the question
text and by the reviewed grade-3 spans supplied for that question, not by the
question's category, and no particular shape of answer — a path, a symbol name,
a line range or prose — is required beyond what conditions 1-5 demand of the
claims the response actually makes.

## One packet at a time

Each packet is a complete and separate grading task. The same question may
appear in more than one packet, with a different bundle and a different
response. Grade each packet alone, on its own bundle bytes and its own
response. Do not compare packets, rank them, carry a rationale from one packet
to another, or adjust any grade so that a set of grades comes out a particular
way.

## How a packet was produced is not a grading input

The bundle bytes carry provenance and identity metadata: version strings,
digests, budget fields and selection fields. None of it is evidence about the
question and none of it is a grading input. Do not use it — or the shape,
length or ordering of the bundle — to form, test or apply any theory about how
the packet was produced, and do not let such a theory change how strictly
conditions 1-5 are applied. Identical claims supported by identical bytes
receive identical grades.

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
