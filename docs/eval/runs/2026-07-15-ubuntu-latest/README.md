# Historical full-run evidence — 2026-07-15, runner class `ubuntu-latest`

The first reproducible run on the reference runner class. Its numeric values are
retained as provisional compatibility ceilings in `docs/eval/hero-budgets.json`,
but this report used the previous harness and is not a comparable baseline for
the current all-12/degree-stratified/post-suite-RSS method.

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

What these historical files establish:

1. The previous structural pool reported 120–252 µs p95. It omitted `impact`
   and used the earlier sample, so it does not establish the current pool's
   scale behavior.
2. Within the operations that old harness measured, `agent_brief` reported
   8.5 ms on cobra and 184 ms on guava; every other recorded operation was at
   most 1.3 ms. This is historical observation, not a post-change result.
3. The 11,821 MB guava MAXRSS sample was taken immediately after `IngestAll`,
   before the warm operation suite. The reports do not identify its cause and
   cannot attribute it to `agent_brief`, Stable reads, cache materialization,
   available memory, or Go GC policy. Cause: **UNKNOWN**.
