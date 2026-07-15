# Full-run evidence — 2026-07-15, runner class `ubuntu-latest` (REFERENCE)

The first reproducible run on the reference runner class — the run the
ADR 0003 U5 budgets in `docs/eval/hero-budgets.json` are frozen from.

**Provenance:** workflow `eval-full` run
[29418826616](https://github.com/samibel/graphi/actions/runs/29418826616)
(run #2, `workflow_dispatch`), commit `71353f9` (= merged `main` `fa2441c`
plus only the log-print workflow step), GitHub-hosted `ubuntu-latest`,
`CGO_ENABLED=0`. The JSONs are the byte content the harness printed between
the `EVAL-FULL-REPORT-BEGIN/END` log markers (same bytes as the uploaded
artifacts); every SHA pin was verified fail-closed during clone.

| Repo | Clone | Index | Peak RSS | DB | Nodes / Edges / Files | structural p95 | search p95 | agent_tools p95 |
|------|-------|-------|----------|----|-----------------------|----------------|------------|-----------------|
| cobra (Go) | 0.6 s | 0.53 s | 337 MB | 1.1 MB | 938 / 4 206 / 58 | 252 µs | 894 µs | 8.2 ms |
| flask (Python) | 0.8 s | 0.38 s | 529 MB | 0.8 MB | 1 058 / 2 220 / 106 | 120 µs | 1.2 ms | 5.1 ms |
| guava (JVM monorepo) | 1.4 s | 13.2 s | 11 821 MB | 33 MB | 40 712 / 68 323 / 3 223 | 171 µs | 573 µs | 177.5 ms |

(Class p95 from `warm_p95_us`; per-op resolution in each JSON. Hero suite:
**20/20 pass** on the same runner, `hero-report.json`; its scorecard footer
carries areas not measured by this run — the hero gate is the per-scenario
table.)

Findings the ADR closures cite:

1. **Structural ops are scale-flat on the reference runner** (120–252 µs p95,
   independent of repo size up to 40k nodes) — ADR 0003 D7 confirmed at
   reference conditions; U5 budgets frozen from these numbers.
2. **`agent_brief` is the only scaling op** (8.5 ms → 184 ms p95, cobra →
   guava; every other op ≤ 1.3 ms) — the U2 decision input.
3. **Peak RSS scales with available memory, not the store**: 11.8 GB on the
   16 GB reference runner vs 4.2 GB in the 26 GB-heap-limited sandbox for the
   same guava index against a 33 MB store — the in-process peak is ingest
   working set + resident caches under relaxed GC pressure. U4 input.
