# Compact development sufficiency v4 result

This is a development-only diagnostic. It is not release evidence and did not
load, copy, or execute the sealed holdout split.

## Result

The preregistered `task_context/compact-dev/4` candidate at a 250
whitespace-field source budget fails the diagnostic:

- population: 40 answerable development queries;
- required: 36 passes (`MinimumPassCount(40)`);
- observed: 28 passes;
- `diagnostic_pass`: false;
- sealed outcome identity: `0a833be7e5ec09ad0199b9ed6bd3364bb5c84cd2903faa98a7de63a7c39304c6`;
- outcome file SHA-256: `de568592b7db1b941d64e7b398cac9659d3820695f40974ec1819e681db0ad07`.

Both independent primary readers returned `INSUFFICIENT` for the same nine
queries. Of the other 62 individual responses, the independent grader passed
56 and failed both responses for `cb-21`, `ci-771`, and `ci-1222`. No primary
grades disagreed, so adjudication was not required.

| Stratum | Passed | Total |
| --- | ---: | ---: |
| exact_identifier | 4 | 4 |
| exact_path | 3 | 3 |
| ambiguous | 3 | 3 |
| nl_behaviour | 4 | 6 |
| architecture_flow | 2 | 5 |
| config_docs | 12 | 19 |

Compared with compact-dev/3, query passes fell from 29 to 28. `cb-12`,
`cb-15`, `cb-24`, and `ci-1970` newly pass, while `cb-13`, `cb-21`, `cb-22`,
`ci-1222`, and `ci-1991` regress. Architecture-flow sufficiency falls from
3/5 to 2/5.

## Diagnosis

The v4 multi-window policy improved aggregate wire cost, but its secondary
window heuristic is not stable enough. It chooses a high-scoring distant
window without proving that the window completes the question's operation.
That helps long-function branch questions such as `cb-12`, but it can displace
the tail of a short definition (`cb-21`), a recursive loop (`cb-22`), or the
actual suggestion logic (`cb-13`). The selector is therefore optimizing local
query-term density rather than answer-complete structural units.

The remaining failures divide into three actionable classes:

1. incomplete function bodies or control flow: `cb-12`, `cb-13`, `cb-19`,
   `cb-21`, `cb-22`, `ci-511`;
2. missing companion APIs or value/protocol operations: `ci-467`, `ci-771`,
   `ci-1222`, `ci-1991`, `ci-2314`;
3. answer spread across related declarations/comments: `cb-28`.

The evidence rejects adding more term-scored fragments. The next candidate
should preserve complete small functions and allocate long-function windows by
control-flow landmarks and symbol/call relationships. Companion operations
such as registration plus callback type, flag lookup plus value access, and
protocol request plus output parsing need to be selected as linked evidence.

## Token and reproducibility envelope

The separately preserved v4 frontier records the selected 250-source-budget
row:

- grade-3 overlap: 40/40;
- serialized payloads within 1,200 cl100k tokens: 40/40;
- maximum serialized payload: 1,169 tokens;
- candidate median: 916 tokens;
- paired median savings versus GrepRead/2: 132.5 tokens (12.1%);
- candidate cheaper on 32/40 development queries;
- byte-identical independent candidate builds: 40/40.

These are development diagnostics, not a release savings claim.

## Audit and reproduction

The run is bound to candidate commit
`79cc1cf261856ef6ca76770b90bd9beb1ee4ae95`. The registration binds both
input channels, candidate files, participants, prompts, 250-source budget,
rubric, and threshold. `records/` contains the 142-entry append-only hash
chain; `raw/` preserves all first responses and grades; `outcome.json` is
write-once and sealed.

Recompute the sealed decision:

    CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
      -run-dir docs/eval/retrieval/runs/2026-09-12-compact-dev-sufficiency-v4
