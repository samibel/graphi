# Compact wire v9 blind development result

Status: **PASS — development diagnostic, not a release result**.

- Candidate: `857b27f77df5fa38aef8637f59453d40955d1ce6`
- Registration: `1195c0f9e70c51a87c53270325f61adec6789c503841af5136e36b1db287e529`
- Population: 40 answerable development questions
- Pre-registered threshold: k=36
- Result: **37/40 passed**
- Primary responses: 80
- Primary responses beginning `INSUFFICIENT`: 0
- Graded answers: 85 (75 pass, 10 fail), including 5 adjudicator answers
- Primary grade disagreements: 5; fresh blind adjudication passed 4 and failed 1
- Sealed outcome content digest: `c5a952f743d8f0227cee4239f121b1ab4299399885cf473302f6a68f7da02f7d`
- Sealed `outcome.json` file digest: `22c52e3e1fdeb212316474762369d1e9aaff79adbdfd7b5f6914cad67d2e2890`

Failed query IDs:

`ci-771`, `ci-943`, `ci-2314`.

V9 improves the sealed v8 blind result from 34/40 to 37/40 and crosses the
pre-registered 36/40 development threshold. The change keeps the 250-token
source allocation, the frozen 1,200 real-token wire ceiling, the dataset,
rubric and decision rule unchanged. It closes three evidence-role defects
identified from v8:

1. exact-path requests now state the requested operation and allocate source
   breadth to a declaration outline instead of completing one long body;
2. traversal questions retain the source call chain across `ExecuteC`, `Find`,
   `Traverse` and `stripFlags`;
3. shell-completion callback questions retain both positional and flag callback
   roles and identify their directive return contract.

The matching v9 frontier independently measures 40/40 answer-span overlap,
40/40 byte-identical rebuilds and 40/40 bundles within 1,200 real tokens. Its
mean / median / maximum are 908.15 / 908 / 1,080 tokens; 32/40 bundles are
cheaper than GrepRead/2 and the median saving is 111.5 tokens (9.9753%). See
`../2026-09-13-compact-wire-v9-dev/frontier.json`.

This is positive development evidence, not permission to tune on or inspect the
held-out split and not a release verdict. The remaining failures are preserved
rather than hidden: the shell-directive protocol (`ci-771`), one completion
flow (`ci-943`) and one command relationship (`ci-2314`) still lack a sufficient
answer under this blind procedure.

The separate retrieval target report already passes the architecture-flow
target at 0.4624671523 (required 0.4578575263) and the exact-identifier floor at
1.0. Its overall checker still exits 1 solely because the frozen historical
qrel-blind smoke says `RELEASE: NO`; this development diagnostic does not
rewrite or waive that evidence:

```sh
CGO_ENABLED=0 go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/after/cobra-v2-dev-report.json
```

Recompute the sealed verdict without changing it. The harness requires `HEAD`
to equal the registered candidate, so copy this recorded run as the sole
untracked directory into a temporary worktree at that commit:

```sh
GRAPHI_ROOT="$PWD"
GRAPHI_VERIFY="$(mktemp -d /tmp/graphi-v9-verify.XXXXXX)"
git worktree add --detach "$GRAPHI_VERIFY/candidate" \
  857b27f77df5fa38aef8637f59453d40955d1ce6
mkdir -p "$GRAPHI_VERIFY/candidate/docs/eval/retrieval/runs"
cp -R "$GRAPHI_ROOT/docs/eval/retrieval/runs/2026-09-13-compact-wire-v9-blind-dev" \
  "$GRAPHI_VERIFY/candidate/docs/eval/retrieval/runs/"
(
  cd "$GRAPHI_VERIFY/candidate"
  CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
    -run-dir docs/eval/retrieval/runs/2026-09-13-compact-wire-v9-blind-dev
)
git worktree remove --force "$GRAPHI_VERIFY/candidate"
rmdir "$GRAPHI_VERIFY"
```

Expected result: `passed: 37`, `k: 36`, `complete: true`,
`diagnostic_pass: true`, and outcome digest
`c5a952f743d8f0227cee4239f121b1ab4299399885cf473302f6a68f7da02f7d`.
