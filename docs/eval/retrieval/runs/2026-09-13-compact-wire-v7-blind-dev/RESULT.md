# Compact wire v7 blind development result

Status: **FAIL — development diagnostic, not a release result**.

- Candidate: `16c24444d3cba7d4fff1017139b3d207997625eb`
- Registration: `557cec1a10596047de2289ae984742901bca14f3872ad0a6752d866b50863cde`
- Population: 40 answerable development questions
- Pre-registered threshold: k=36
- Result: **29/40 passed**
- Primary responses: 80
- Primary responses beginning `INSUFFICIENT`: 12, affecting 7 queries
- Graded answers: 76 (63 pass, 13 fail), including 8 adjudicator answers
- Primary grade disagreements: 8; fresh blind adjudication passed 5 and failed 3
- Sealed outcome content digest: `bab83216a0e0a44d6ebd8196ac7ad2dfb4e595d20a10130704d8668a8ed13a58`

Failed query IDs:

`cb-07`, `cb-08`, `cb-09`, `cb-19`, `cb-21`, `ci-678`, `ci-771`, `ci-943`,
`ci-1133`, `ci-1991`, `ci-2314`.

V7 improves the formal blind result from v6's 25/40 to 29/40. It recovers the
exact-identifier regressions and most targeted control-flow failures, but the
result remains seven questions below the registered threshold. The remaining
failures identify three distinct source-selection defects:

1. exact-path queries spend too much of the bundle on the file header and too
   little on a declaration-level overview;
2. setter, parent-pointer and flag-value questions contain the named accessor
   but omit the consumer or value-access step;
3. lifecycle and custom-shell questions contain correct landmarks but do not
   preserve enough of the causal chain for a blind answerer to assemble them.

The result also confirms that changing the answer model is not the primary
fix. Both primaries used the same pinned model as v6, while source-chain changes
alone recovered four net questions. V8 therefore targets declaration breadth,
field-level data flow and explicit evidence roadmaps under the same 1,200-token
wire ceiling.

Recompute the sealed verdict without changing it:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-13-compact-wire-v7-blind-dev
```
