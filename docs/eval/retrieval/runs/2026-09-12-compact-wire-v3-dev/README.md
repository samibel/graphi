# Compact task-context v3 development frontier

Status: **development diagnostic, not release evidence**. This run uses only
the extracted 40-query Cobra development population. It does not read or copy
the held-out split and does not change the frozen methodology or thresholds.

`task_context/compact-dev/3` combines two judgement-blind inputs: the preserved
semantic `task_context/2` bundle and the separately versioned, source-verified
`GrepRead/2` transcript. It reserves eight source regions for the semantic
channel and two for the query-only grep/read fallback, merges corroborating
windows at an identical source anchor, and distributes a 250-whitespace-field
source budget toward coherent regions. The full GrepRead transcript is created
before any judgement is consulted.

At the selected 250 source budget:

- grade-3 overlap is 40/40;
- all 40 serialized MCP payloads fit the unchanged 1,200 real-token ceiling;
- maximum payload size is 1,190 cl100k tokens;
- mean/median payload size is recorded in `frontier.json`;
- paired median savings versus GrepRead/2 remains positive;
- both independent candidate builds are byte-identical for all 40 queries.

Strict full-span containment is reported as a harder diagnostic, not as the
blind sufficiency result. The candidate still requires the preregistered blind
40-query test before any answer-quality claim.

Reproduce from the repository root with the pinned pure-Go tokenizer and the
clean Cobra checkout at `a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
CGO_ENABLED=0 \
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/absolute/path/to/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-12-compact-wire-v3-dev/frontier.json" \
go test ./internal/eval/retrieval -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```
