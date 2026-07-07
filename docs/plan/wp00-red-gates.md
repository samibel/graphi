# WP-00 — Red-Gate Scorecard (gate-first)

This is the single tracking surface for the seven acceptance metrics from the
[external-findings remediation plan](2026-07-external-findings-remediation.md)
and the [product brief](2026-07-produkt-brief-agent-pipeline.md). Per the
gate-first principle, every metric has a **runnable, checked-in gate that is RED
today and does not break CI** — it measures and logs the current (bad) value
without failing, behind an explicit *arming switch* that the fixing WP flips.
When a switch is armed the gate becomes hard: CI then enforces the target.

The rule: no fix lands without a gate that measures it, and a gate is only armed
by the WP that makes it pass. testgate stays green throughout because a disarmed
gate is a passing test.

## Status

| # | Metric | Current (RED) | Target | Arming switch | Armed by | Where |
|---|--------|---------------|--------|---------------|----------|-------|
| 1 | Taint recall (vuln-go E2E) | **0/4**, 0 FP | 4/4, precision ≥ 0.8 | `gateArmed` | WP-05 | `engine/ingest/taint_vulngo_e2e_test.go` |
| 2 | Edges/node ratio (Java fan-out) | **15.56** | < 8 (≈ < 500k on real repo) | `budgetArmed` | WP-08 | `engine/ingest/fanout_bench_test.go` |
| 3 | DB size (real monorepo) | 2.3 GB | < 300 MB | *(fixture pending)* | WP-06/WP-08 | `bench/` (on-disk store) |
| 4 | Full index time | 4m48s | < 90s | *(fixture pending)* | WP-08 | `bench/bench-budget.yml` |
| 5 | Link progress interval | minutes of silence | < 2s between events | *(gate pending)* | WP-02 | `engine/ingest` progress path |
| 6 | dead_symbol false positives (@Test/@Bean/main) | "very many" | 0 as warning | *(gate pending)* | WP-11 | `engine/diagnostic` |
| 7 | unresolved_reference diagnostics | O(edges) | 1 per external target | *(gate pending)* | WP-12 | `engine/diagnostic` |

Metrics 1 and 2 are live and runnable today (see below). Metrics 3–7 are
declared here so the scorecard is complete; each is armed as its owning WP lands
a fixture/measurement, at which point its row moves from *pending* to a concrete
switch. Metrics 3–4 are deferred to their WPs rather than pre-wired into
`bench/bench-budget.yml`, because a budgeted-but-unmeasured metric hard-fails the
bench gate — they are added together with the on-disk bench fixture in WP-06/08.

## How the live gates read

```
go test ./engine/ingest/ -run 'TestTaintE2E_VulnGoRecall|TestLinkFanout' -v
```

- `TestTaintE2E_VulnGoRecall` → `[RED GATE WP-05] vuln-go taint: recall=0/4, false_positives=0, findings=0, armed=false`
- `TestLinkFanout_EdgeExplosionBudget` → `[RED GATE WP-08] java fan-out: nodes=384, edges=5976, edges/node=15.56, budget=8.00, armed=false`

Both PASS (green CI) while disarmed; both encode the exact failure the field
tests found, so the moment WP-03/WP-01 change the underlying behavior the gate
authors flip the switch and CI begins enforcing 4/4 recall and a sub-8 ratio.

## Arming protocol

When the owning WP believes it has fixed the metric:
1. Run the gate disarmed; confirm the logged value now meets target (the test
   prints "arm the gate: set …=true" when it does).
2. Flip the switch (`gateArmed` / `budgetArmed`) to `true` in the same PR as the fix.
3. testgate then enforces it — and, per the ratchet, any later regression fails CI.
