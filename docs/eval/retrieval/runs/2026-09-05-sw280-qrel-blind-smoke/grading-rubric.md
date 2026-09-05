# Frozen grading rubric — SW-280 qrel-blind smoke evaluation

Version: `sw280-grading-rubric/1`. This file is frozen by content hash in
`precondition-record.json` before the evaluation may start, and re-hashed at the end of the run.
Editing it after the freeze fails the run.

## What is being graded

One rater response to one question, produced from the question text and the exact serialized
`task_context/2` bundle bytes and nothing else. The grader decides **pass** or **fail**. There is no
third outcome, no partial credit and no score.

## Inputs the grader receives

- the question text;
- the exact preserved bundle bytes the rater saw;
- the rater's response, identified by its content address;
- the reviewed grade-3 answer spans for that question from the sealed dataset — path, line range and
  anchor text.

The grader does NOT receive: the other rater's response, any other rater's grade, the rater's
identity beyond the recorded id, or any lower-grade judgement.

That the grader sees the answer key while the raters do not is deliberate. The raters are the thing
being measured; the grader is the instrument that reads their answers. Grading "is this correct?"
without the key would measure the grader's own knowledge of Cobra rather than the response.

## Pass criteria — all three must hold

1. **Answers the question asked.** The response addresses the actual question, not a nearby one.
2. **Substantively correct.** The claim the response makes is consistent with at least one reviewed
   grade-3 answer span for that question. A response that names a different but genuinely equivalent
   location or mechanism passes; a response that names something the answer key contradicts fails.
3. **Grounded in the bundle.** The response cites evidence that is actually present in the bundle it
   was given — a file path, symbol name or snippet line that appears in those bytes. A correct answer
   that cites nothing from the bundle, or that cites something absent from it, fails: the question is
   whether the bundle was sufficient, not whether the rater already knew Cobra.

## Fail criteria — any one is enough

- the response is about a different question;
- the response contradicts every reviewed grade-3 answer span;
- the response supplies its answer from outside the bundle (nothing it cites appears in the bytes);
- the response says it cannot answer (`INSUFFICIENT`), which is an honest answer and a failed one;
- the response is hedged to the point of making no checkable claim.

## Cases that are not graded at all

A missing, empty or refused response is a **failure by rule** and is not graded. Its outcome is
mechanical. Recording a grade for one is an error, and the evaluation refuses it.

## The grader's obligations

- Grade only from the inputs above.
- Record a rationale naming which grade-3 span the response was checked against, and which bundle
  evidence it cited.
- Never re-grade a response. A response is graded once; its grade is bound to its content address.
