# Compact development sufficiency result

This is a development-only diagnostic. It is not a release result and it does
not use or copy the sealed holdout split.

## Result

The preregistered `task_context/compact-dev/1` candidate at a 140
whitespace-field source budget fails the sufficiency diagnostic:

- population: 40 answerable development queries
- required: 36 passes (`MinimumPassCount(40)`)
- observed: 22 passes
- outcome: `diagnostic_pass=false`
- outcome SHA-256: `568df74fb5fe7fc07dff85adb9620aa5e101e2a4e25a6d91b068e751a1cda62d`

Two independent primary readers produced 80 first-attempt responses. Primary
slot 0 returned 18 `INSUFFICIENT` responses and slot 1 returned 16. Eighteen
queries had at least one non-answer and therefore failed mechanically. The
remaining 46 answered responses were independently graded: all 46 passed. No
primary grade disagreed, so the preregistered adjudicator was not used.

Per stratum, the query-level result was:

| Stratum | Passed | Total |
| --- | ---: | ---: |
| `exact_identifier` | 4 | 4 |
| `exact_path` | 3 | 3 |
| `ambiguous` | 3 | 3 |
| `nl_behaviour` | 2 | 6 |
| `architecture_flow` | 0 | 5 |
| `config_docs` | 10 | 19 |

## Diagnosis

This run separates answer correctness from evidence sufficiency. Whenever both
readers found enough evidence to answer, both answers passed grading. The 18
failed queries instead omitted the implementation, configuration sequence, or
complete example required to answer.

The deterministic selector spends breadth before depth: it admits one anchor
line from every ranked source and then grows all admitted fragments in
round-robin order. On the failed queries it emitted 275 source fragments
(15.28 per query); 267 of 275 fragments were at most three lines long. The
longest fragment averaged 3.11 lines. For example, `cb-22` returned 16 separate
one-line fragments. The correct spans were touched, but their explanatory
bodies were cut away.

This rejects the hypothesis that changing the answering LLM is the primary
fix. The answered responses were correct; the missing bytes cannot be recovered
by a stronger reader. The next candidate must change source ranking and budget
allocation to prefer a few coherent implementation/prose regions over many
anchors, while retaining the exact-identifier floor.

## Reproduction and audit

The run is bound to candidate commit
`b175aeab0d0a85e55e2a7dc36b6408846355bb27`. `pre-registration.json` contains
the fixed participants, candidate file digests, dataset digest, two identical
captures per query, exact prompts, budget, rubric, and derived threshold.
`records/` is an append-only SHA-256 chain containing all responses and grades.
The original response and grade text is under `raw/`. `outcome.json` is the
write-once sealed decision.

Recompute the decision without changing the run:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency
```

Focused harness verification:

```sh
CGO_ENABLED=0 go test ./cmd/compact-sufficiency-dev \
  ./internal/eval/retrieval \
  -run '^(TestCompactDevSufficiency|TestQrelBlindSmoke_MinimumPassCountGoldenTable)' \
  -count=1
```
