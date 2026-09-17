# Compact wire v9 development frontier

Status: **development diagnostic, not a release result**.

V9 is the source-role closure derived from the sealed v8 blind failures. It
keeps the 250-whitespace-token source allocation and the 1,200 real-token wire
ceiling unchanged. The candidate adds three general selection rules:

- exact-path requests prefer a declaration outline over completing one long
  function body;
- root/subcommand traversal follows the source call chain from `ExecuteC` to
  `Find`, `Traverse`, and `stripFlags`;
- Go shell-completion questions preserve both positional-argument and flag
  callback roles plus their `ShellCompDirective` return contract.

At the preregistered source budget of 250, the measured development frontier is:

- equal-recall overlap: **40/40**;
- complete grade-3 span: **26/40**;
- independent rebuilds byte-identical: **40/40**;
- real-token mean / median / maximum: **908.15 / 908 / 1,080**;
- within the frozen 1,200-token ceiling: **40/40**;
- cheaper than GrepRead/2: **32/40**;
- median saving: **111.5 tokens (9.9753%)**;
- first-overlap source rank median / maximum: **2 / 9**.

A tuning-only blind probe on the six v8 failures returned passing independent
grades for both answer slots on the two newly closed causal roles (`ci-467` and
`ci-1222`) and on the shell-completion file outline (`cb-09`). After exact-path
allocation was changed from body depth to outline breadth, four fresh responses
for `cb-08` and `cb-09` all answered rather than returning `INSUFFICIENT`.
These probes are not a gate; only a new preregistered 40-query run can establish
the required 36/40 result.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-compact-wire-v9-dev/frontier.json" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

Artifact SHA-256:
`416f1cbad4eea8c37575c7614555e76dfdc447ae13fdd40fb76726a88b360bbc`.
