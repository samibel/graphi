# Compact development sufficiency v3 result

This is a development-only diagnostic. It is not release evidence and did not
load, copy, or execute the sealed holdout split.

## Result

The preregistered task_context/compact-dev/3 candidate at a 250
whitespace-field source budget fails the diagnostic:

- population: 40 answerable development queries;
- required: 36 passes (MinimumPassCount(40));
- observed: 29 passes;
- diagnostic_pass: false;
- sealed outcome identity: 6e54c67756499a41072e84c9f1b69454743ad3372ec8d6a161ac7f14f9ee22da;
- outcome file SHA-256: a04af6fc4e0d31280a69d754e760a25610d8073a819def5e66be8e1b861d7f7b.

Both independent primary readers returned INSUFFICIENT for the same ten
queries. Of the other 60 individual responses, the independent grader passed
58 and failed both responses for ci-771. No primary grades disagreed, so
adjudication was not required.

| Stratum | Passed | Total |
| --- | ---: | ---: |
| exact_identifier | 4 | 4 |
| exact_path | 3 | 3 |
| ambiguous | 3 | 3 |
| nl_behaviour | 4 | 6 |
| architecture_flow | 3 | 5 |
| config_docs | 12 | 19 |

Compared with compact-dev/2, query passes rose from 25 to 29. Newly passing
queries are cb-13, cb-21, cb-22, and ci-1408; there are no regressions among
the v2 query passes. Architecture-flow sufficiency improved from 1/5 to 3/5.

## Diagnosis

The ten mechanical failures all identify missing implementation depth: the
response contains a declaration, comment, or call site but truncates before
the decisive body. Examples include Find/findNext traversal, the version
execution branch, the full Run-hook sequence, later completion flag-group
branches, flag enumeration/value lookup, and help-flag override behavior.

The only answered failure, ci-771, demonstrates a remaining candidate
retrieval miss. Its bundle now contains the hidden __complete command and
directive constants, but still omits the completion-line output, final
:<directive> record, and stderr-handling protocol. Both answers therefore
describe portable callbacks for supported shells rather than how to implement
an adapter for another shell.

This evidence rejects another simple increase in contiguous top-source depth.
The next useful design is an extractive multi-window representation: retain a
declaration header plus the highest-value body windows inside long functions,
so multiple decisive branches can fit under the unchanged wire budget.

## Token and reproducibility envelope

The separately preserved v3 frontier records the selected 250-source-budget
row:

- grade-3 overlap: 40/40;
- serialized payloads within 1,200 cl100k tokens: 40/40;
- maximum serialized payload: 1,190 tokens;
- candidate median: 948 tokens;
- paired median savings versus GrepRead/2: 119 tokens (11.21%);
- byte-identical independent candidate builds: 40/40.

These are development diagnostics, not a release savings claim.

## Audit and reproduction

The run is bound to candidate commit
8c40427537c1338857b0e2dead6701c2ee2c1623. The registration binds both
input channels, candidate files, participants, prompts, 250-source budget,
rubric, and threshold. records/ contains the 140-entry append-only hash chain;
raw/ preserves all first responses and grades; outcome.json is write-once and
sealed.

Recompute the sealed decision:

    CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
      -run-dir docs/eval/retrieval/runs/2026-09-12-compact-dev-sufficiency-v3
