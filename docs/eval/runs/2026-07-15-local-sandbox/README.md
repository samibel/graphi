# Full-run evidence — 2026-07-15, runner class `local-sandbox` (PRELIMINARY)

First complete execution of the SW-123 full-run harness (`cmd/eval -full-run`)
over the three EVAL-02 repos plus the 20-task hero suite. Produced in the
development sandbox, **not** on the reference runner class — these numbers
demonstrate the historical pipeline end-to-end but decide no current ADR or
budget. The current harness measures a different workload; see ADR 0003 and
`docs/eval/hero-protocol.md` before comparing these values.

| Repo | Index | Peak RSS | DB | Nodes / Edges / Files | structural p95 | search p95 | agent_tools p95 |
|------|-------|----------|----|----------------------|----------------|------------|-----------------|
| cobra (Go) | 0.8 s | 275 MB | 1.1 MB | 938 / 4 206 / 58 | 459 µs | 1.2 ms | 11.3 ms |
| flask (Python) | 0.6 s | 283 MB | 0.8 MB | 1 058 / 2 220 / 106 | 345 µs | 2.2 ms | 9.9 ms |
| guava (JVM monorepo) | 19.5 s | 4 229 MB | 35 MB | 40 712 / 68 323 / 3 223 | 596 µs | 1.2 ms | 558 ms |

(Class p95 = worst op in class from `warm_p95_us_per_op`; full distributions
in the JSON files. Hero suite: 20/20 pass, `hero-report.json`.)

Two historical observations under that harness:

1. The old structural pool observed ≤ 600 µs p95 at 43× node count. It omitted
   `impact` and used NodeId sampling, so it is not a current-harness baseline.
2. `agent_brief` was the slowest sampled operation (11 ms → 558 ms cobra →
   guava; the other sampled operations stayed ≤ ~2 ms). This motivated the
   aggregate implementation, but it does not establish current performance or
   explain RSS. Those values and causality remain **UNKNOWN** until the new
   comparable reference run.

All three SHA pins verified fail-closed during clone (`repo.sha` in each
report matches the manifest pin).

Two provenance notes for careful readers:

- `hero-report.md` carries a scorecard footer of **Pass: false**. That footer
  is the EP-019 overall scorecard, which folds in areas this run does not
  measure (the `ux` area is *carried* from the baseline at 62.0, below its
  floor). The hero gate is the per-scenario table — 20/20 pass; the carried
  scorecard areas are out of scope for this evidence run.
- `header.commit` in the JSONs is `660b5a3`: the run executed on the working
  tree one commit BEFORE the commit that checked it in (the harness landed in
  the same change set). Consistent with the PRELIMINARY label; the reference
  run on `ubuntu-latest` will carry a clean committed SHA.
