# Full-run evidence — 2026-07-15, runner class `local-sandbox` (PRELIMINARY)

First complete execution of the SW-123 full-run harness (`cmd/eval -full-run`)
over the three EVAL-02 repos plus the 20-task hero suite. Produced in the
development sandbox, **not** on the reference runner class — these numbers
demonstrate the pipeline end-to-end and direct the ADR 0003 U2/U4 decisions,
but they freeze **no** budgets (`docs/eval/hero-budgets.json` stays null until
the first green `eval-full.yml` run on `ubuntu-latest`).

| Repo | Index | Peak RSS | DB | Nodes / Edges / Files | structural p95 | search p95 | agent_tools p95 |
|------|-------|----------|----|----------------------|----------------|------------|-----------------|
| cobra (Go) | 0.8 s | 275 MB | 1.1 MB | 938 / 4 206 / 58 | 300 µs | 1.2 ms | 11.3 ms |
| flask (Python) | 0.6 s | 283 MB | 0.8 MB | 1 058 / 2 220 / 106 | 345 µs | 2.2 ms | 9.9 ms |
| guava (JVM monorepo) | 19.5 s | 4 229 MB | 35 MB | 40 712 / 68 323 / 3 223 | 596 µs | 1.2 ms | 558 ms |

(Class p95 = worst op in class from `warm_p95_us_per_op`; full distributions
in the JSON files. Hero suite: 20/20 pass, `hero-report.json`.)

Two findings that carry into the ADRs:

1. **Structural ops are scale-flat** (≤ 600 µs p95 at 43× node count) — the
   CORE-02 selective-read migration behaves as designed (ADR 0003 D7).
2. **`agent_brief` is the only scaling outlier** (11 ms → 558 ms cobra →
   guava; every other op stays ≤ ~2 ms). The U2 decision (catalog read vs.
   SQL aggregates) is now a measured question about ONE operation.

All three SHA pins verified fail-closed during clone (`repo.sha` in each
report matches the manifest pin).
