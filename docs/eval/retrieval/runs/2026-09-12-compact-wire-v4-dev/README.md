# Compact wire v4 development frontier

This is a development-only measurement over the 40 answerable development
queries. It does not use or replace the sealed holdout and is not a release
result.

Version 4 fixes a depth-allocation failure exposed by the v3 blind run. The
selector now uses the symbol and declaration kind already present in the
bundle's cited item, retains leading declaration documentation, reserves one
non-overlapping body window for long implementations, and promotes an actual
`.VisitAll` use when a query asks for a list. These are deterministic,
query-derived signals; no judgement or answer span is available during bundle
construction.

At the preregistered 250 whitespace-field source budget, v4 reaches grade-3
overlap for 40/40 queries. All 40 complete MCP payloads are at most 1,200
cl100k tokens (mean 919.8, median 916, maximum 1,169). Against the frozen
GrepRead/2 transcripts, paired median savings are +132.5 tokens (+12.1%), and
32/40 candidate payloads are cheaper. Two independent constructions are
byte-identical for all 40 queries. Full-span containment remains only a
diagnostic (8/40) and is not presented as sufficiency.

Compared with v3 at the same source budget, median payload size falls from 948
to 916 cl100k tokens, the maximum falls from 1,190 to 1,169, paired median
savings improve from +119 to +132.5 tokens, and the cheaper-query count rises
from 29/40 to 32/40. Atomic overlap remains 40/40 and complete-span coverage
remains 8/40.

Reproduce from the repository root with the pinned clean Cobra checkout:

```sh
CGO_ENABLED=0 \
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/absolute/path/to/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-12-compact-wire-v4-dev/frontier.json" \
go test ./internal/eval/retrieval -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

The next decision is made by a fresh, sealed, question-plus-payload-only blind
sufficiency run. The frontier alone cannot establish the 36/40 answerability
target.
