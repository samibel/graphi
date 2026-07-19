> **SUPERSEDED — dieser Plan ist nicht mehr gültig und darf nicht als Anweisung gelesen werden.**
>
> **Ersetzt durch:** [`docs/plan/2026-07-graphi-9of10-execution-plan.md`](../2026-07-graphi-9of10-execution-plan.md)
> — die einzige Planungsautorität für graphi.
> **Superseded am:** 2026-07-17 (Story SW-117, Milestone M0).
>
> Archiviert, nicht gelöscht: der Text unten ist unverändert und dokumentiert
> Entscheidungen, die einmal real waren. Er wird nicht mehr gepflegt, und
> Widersprüche zu anderen archivierten Plänen werden bewusst nicht aufgelöst.
> Querverweise auf andere Pläne zeigen auf Dateien, die ebenfalls unter
> `docs/plan/superseded/` liegen; wo sie noch alte Pfade (`temp/plan/…`, `temp/review/…`,
> `docs/plan/…`) nennen, ist der Pfad historisch, nicht aktuell.

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
| 1 | Taint recall (vuln-go E2E) | 0/4 → **5/5, 0 FP** ✅ ARMED (precision ~1.0 on realistic shapes; own-source 10→1) | 4/4, precision ≥ 0.8 | `gateArmed` (=true) | WP-05a/b | `engine/ingest/taint_vulngo_e2e_test.go` |
| 2 | Edges/node ratio (Java fan-out) | 15.56 → **0.96** ✅ ARMED | < 8 (≈ < 500k on real repo) | `budgetArmed` (=true) | WP-01 | `engine/ingest/fanout_bench_test.go` |
| 3 | DB size — bytes/edge proxy | ~500 B/edge → **226 B/edge** ✅ ARMED (budget 360) | < 300 MB (WP-01 ~10× fewer edges × WP-06 smaller/edge) | `maxBytesPerEdge` | WP-06/WP-08 | `engine/ingest/storage_budget_test.go` |
| 4 | Full index time | 4m48s | < 90s | *(wall-clock; not a checked-in gate — flaky/machine-bound)* | WP-02 proxy | `bench/bench-budget.yml` (`full_index_ms`) + link-progress |
| 5 | Link progress interval | minutes of silence → **≥2 incremental events, Done↑** ✅ ARMED | < 2s between events | inherent (≥2 PhaseLink events, Done increasing) | WP-02 | `engine/ingest/link_progress_test.go` |
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
- `TestLinkFanout_EdgeExplosionBudget` → `[RED GATE WP-08] java fan-out: nodes=416, edges=400, edges/node=0.96, budget=8.00, armed=true` (WP-01 collapsed the import fan-out to a single file→package edge; the gate is now ARMED and enforces the sub-8 budget)
- `TestLinkProgress_Incremental` → `[GATE WP-02] link phase emitted 5 PhaseLink events; Done sequence: 0/200->64/200->128/200->192/200->200/200` (WP-02 made link progress real: linkFiles emits a PhaseLink event per batch of files linked, so the phase is a stream of strictly-increasing Done toward a non-zero Total instead of one silent block. ARMED — the gate hard-fails if link regresses to < 2 incremental events. The event-count + increasing-Done is the deterministic stand-in for the "< 2s between events" target; wall-clock timing is deliberately not asserted.)

Gate 1 PASSES green while disarmed. Gate 2 was RED (15.56) until WP-01 replaced
the Java/Kotlin file→file import fan-out with one file→package edge to an
interned `package` node; it now measures **0.96** and is ARMED, so CI enforces
the sub-8 budget and any regression fails. Gate 1 stays disarmed until WP-05
lands 4/4 taint recall.

## Arming protocol

When the owning WP believes it has fixed the metric:
1. Run the gate disarmed; confirm the logged value now meets target (the test
   prints "arm the gate: set …=true" when it does).
2. Flip the switch (`gateArmed` / `budgetArmed`) to `true` in the same PR as the fix.
3. testgate then enforces it — and, per the ratchet, any later regression fails CI.
