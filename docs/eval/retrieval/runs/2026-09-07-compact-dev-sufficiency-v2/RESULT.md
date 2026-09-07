# Compact development sufficiency v2 result

This is a development-only diagnostic. It is not a release result and did not
load or copy the sealed holdout split.

## Result

The preregistered `task_context/compact-dev/2` candidate at a 140
whitespace-field source budget fails the diagnostic:

- population: 40 answerable development queries
- required: 36 passes (`MinimumPassCount(40)`)
- observed: 25 passes
- outcome: `diagnostic_pass=false`
- outcome SHA-256: `c34740505203487b9f83f47ffe833a890737be3aabee8e3fabd67313c2c2a2e7`

Both independent primary readers returned `INSUFFICIENT` for the same 12
queries. Of the remaining 56 answered responses, the independent grader passed
50 and failed six. The six failures were both responses for `cb-13`, `cb-21`,
and `ci-771`; no primary grades disagreed, so adjudication was not required.

| Stratum | Passed | Total |
| --- | ---: | ---: |
| `exact_identifier` | 4 | 4 |
| `exact_path` | 3 | 3 |
| `ambiguous` | 3 | 3 |
| `nl_behaviour` | 3 | 6 |
| `architecture_flow` | 1 | 5 |
| `config_docs` | 11 | 19 |

Compared with compact-dev/1, query passes rose from 22 to 25 and the number of
queries with a mechanical insufficient response fell from 18 to 12. The
coherent-region selector is therefore directionally useful but not sufficient
for the 36/40 gate.

## Remaining failure

Every remaining failure still names missing bundle bytes. Twelve questions
omit required matching, parsing, lifecycle, flag-access, or output behavior.
Three answered questions contain only part of the required behavior:

- `cb-13` omits the effect of explicit `SuggestFor` matches;
- `cb-21` shows parent assignment but cuts before child-list insertion;
- `ci-771` omits the hidden completion request/response protocol required for
  integrating another shell.

This remains a selection/ranking problem, not primarily an answering-model
problem. Both readers made identical sufficiency decisions, and their answered
responses were correct in 50 of 56 individual cases.

## Audit

The run is bound to candidate commit
`fbfb8b7844abd8f4097898bd3c51637eb406e37f`. `pre-registration.json` binds the
candidate files, participants, two byte-identical captures, prompts, 140-source
budget, rubric, and threshold. `records/` is the append-only hash chain;
`raw/` preserves all first responses and grades; `outcome.json` is write-once
and sealed.

Recompute the sealed decision:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-07-compact-dev-sufficiency-v2
```
