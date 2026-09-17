# Compact wire v8 — development frontier

Status: **development diagnostic, not a release result**.

V8 responds to the sealed v7 blind failure (29/40). It changes source
selection without consulting qrels during construction:

- exact-path queries receive a package/declaration outline instead of a long
  licence/header prefix;
- query-relevant receiver fields connect setters and accessors to their first
  source-level consumers;
- command-parent, test-flag, currently-executing-flag and parent-flag-value
  intents preserve their causal units;
- lifecycle and custom-shell bundles reserve evidence for each required role;
- the concise summary emits a flow roadmap only when every named landmark is
  present in the selected source bytes.

The frozen wire ceiling remains 1,200 real `cl100k_base` tokens. The selected
source budget remains 250 whitespace-field tokens.

## Result at source budget 250

- Source overlap with a grade-3 answer span: **40/40**
- Complete grade-3 qrel span: **26/40**
- Byte-identical independent rebuilds: **40/40**
- Mean / median / maximum serialized real tokens: **919.85 / 927.5 / 1,080**
- Serialized responses within 1,200 tokens: **40/40**
- Candidate cheaper than GrepRead/2: **32/40**
- Paired median saving: **111.5 tokens (9.98%)**
- First overlapping source rank: median **2.5**, maximum **9**

The complete-span proxy rises from v7's 25/40 to 26/40, but it remains only a
proxy. A non-registered blind probe over all eleven sealed v7 failures produced
an answer and a passing independent grade for 11/11. That probe is tuning
evidence, not a gate result; only a new preregistered run can establish 36/40.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-compact-wire-v8-dev/frontier.json" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

Artifact SHA-256:
`e9a1e4a7643766524f959d8f7fe1636f260240a58b18c35bd6253927cce4e28f`.
