# Compact wire v8 blind development result

Status: **FAIL — development diagnostic, not a release result**.

- Candidate: `c922fd482a8bee1bb41c1866425f97d069e0e08e`
- Registration: `1dece869ed223711dd616258b5272f2695c3a3ab4a9afa00f8f064198d743dd0`
- Population: 40 answerable development questions
- Pre-registered threshold: k=36
- Result: **34/40 passed**
- Primary responses: 80
- Primary responses beginning `INSUFFICIENT`: 3, affecting 3 queries
- Graded answers: 81 (71 pass, 10 fail), including 4 adjudicator answers
- Primary grade disagreements: 4; fresh blind adjudication passed 3 and failed 1
- Sealed outcome content digest: `bb8560b62bfe78f1c5291480a93ca6d3c68cec541d08c490e87f40dacd87f62b`

Failed query IDs:

`cb-08`, `cb-09`, `cb-19`, `ci-467`, `ci-771`, `ci-1222`.

V8 improves the formal blind result from v7's 29/40 to 34/40. Declaration
outlines, field-level flow closure and source-derived roadmaps recover five net
questions, but the result remains two questions below the registered threshold.
The failures expose four remaining selection and presentation defects:

1. a bare exact-path query is still presented as a filename rather than an
   explicit request to describe that file, causing otherwise sufficient
   declaration outlines to receive mechanical `INSUFFICIENT` responses;
2. the root-command lifecycle keeps the hook window but not enough of
   `ExecuteC` to support command resolution and the transition to `execute`;
3. traversal retains `Traverse` but omits the complementary `stripFlags` and
   `Find` behavior needed to distinguish interleaved flags from positional args;
4. completion bundles retain either the registration API or protocol landmarks,
   but omit the positional callback field or the complete directive bitmask.

These are evidence-role allocation defects, not evidence that the 1,200-token
wire ceiling must be raised. V9 targets the missing roles under the same budget.

Recompute the sealed verdict without changing it:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-13-compact-wire-v8-blind-dev
```
