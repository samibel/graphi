# GrepRead/2 development prototype

Status: **development diagnostic, not release evidence**.

This run evaluates an explicitly versioned, deterministic `GrepRead/2`
prototype on the 44-record development slice only. Forty records have at
least one grade-3 judgement and enter the equal-recall diagnostic. No holdout
record was read or executed, and this run changes neither the frozen
methodology nor `GrepRead/1`.

## Result

| Measure | GrepRead/2 dev result |
| --- | ---: |
| Grade-3 equal-recall overlap | 40/40 |
| Equal-recall misses | 0/40 |
| Strict full-span containment (harder, non-contract diagnostic) | 33/40 |
| Repeated transcripts byte-identical | 44/44 |
| Mean cl100k tokens to first equal-recall overlap | 990.2 |
| Median cl100k tokens to first equal-recall overlap | 1,076 |
| Mean complete-transcript cl100k tokens | 3,400.4 |
| Median complete-transcript cl100k tokens | 3,757 |

The old development preflight found zero source overlap for 35/40 queries
under `GrepRead/1`. `/2` fixes that search-cap failure by ranking exact
declarations before uses, ranking natural-language hits before applying the
search cap, diversifying the bounded reads, and reading long declarations or
named files in real 40-line chunks. The execution interface remains only
`fs.FS` plus query text; judgements are consulted after the complete transcript
exists.

This is a useful negative result for the product claim. Against the current
development `task_context/2` captures, the candidate averaged 8,027.05 cl100k
tokens while `GrepRead/2` reached the same contract target at 990.2. The
candidate was cheaper on 0/40 queries, and the development paired median
saving was approximately -731.6%. This is not a release aggregate, confidence
interval, or cross-repository result. It says that strengthening the comparator
alone cannot make the current serialized MCP payload earn the token-savings
claim.

The current atomic-span rule also deserves explicit scrutiny before a later
contract version is proposed: 38/40 `/2` queries reach the target in the grep
response itself, and an exact-path query can earn credit from one returned line
inside a judgement spanning an entire file. This run obeys the frozen rule; it
does not silently alter it.

Machine-readable evidence is in [grepread-v2.json](grepread-v2.json), with the
summary and content identities in [measurement.json](measurement.json).

## Reproduce

Use a clean checkout of Cobra at the pinned commit and the checked-in pure-Go
tokenizer fixture:

```sh
graphi_root=$(pwd)
task_cobra_dir=$(mktemp -d /tmp/graphi-grepread2-cobra.XXXXXX)
git clone --quiet --filter=blob:none --no-checkout https://github.com/spf13/cobra.git "$task_cobra_dir"
git -C "$task_cobra_dir" checkout --quiet a0a6ae020bb3899ff0276067863e50523f897370

GRAPHI_RECOVERY_COBRA="$task_cobra_dir" \
GRAPHI_GREPREAD_V2_DEV_OUT="$graphi_root/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestGrepReadV2DevCapture$' -count=1 -v

shasum -a 256 docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json
CGO_ENABLED=0 go test ./internal/eval/retrieval -run '^(TestGrepReadV2|TestGrepRead_)' -count=1
```
