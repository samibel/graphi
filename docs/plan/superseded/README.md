> **SUPERSEDED — dieser Index ist nicht mehr gültig.**
>
> **Ersetzt durch:** [`docs/plan/2026-07-graphi-9of10-execution-plan.md`](../2026-07-graphi-9of10-execution-plan.md)
> — die einzige Planungsautorität für graphi.
> **Superseded am:** 2026-07-17 (Story SW-117, Milestone M0).
>
> Dies ist der historische Index der acht Pläne, die früher unter `temp/plan/`
> lagen. **Alle acht sind superseded** und liegen jetzt neben dieser Datei unter
> `docs/plan/superseded/`. Die Tabelle unten stuft `00_master_execution_plan.md`
> als „ACTIVE / AUTHORITATIVE“ ein und nennt eine „verbindliche Lesereihenfolge“:
> beides gilt nicht mehr. Kein Dokument, das dieser Index nennt, ist noch
> verbindlich; verbindlich ist allein der oben verlinkte Plan.
>
> Der Text unten bleibt als Dokumentation der damaligen Konsolidierung unverändert.

# Plan-Index und Konsolidierungsreview

## Ergebnis des Gesamt-Reviews

Die acht ursprünglichen Pläne hatten eine konsistente Diagnose, aber keine konsistente Ausführung. Die größten Fehler waren:

1. HTTP, UI und GitHub Action waren als Nicht-MVP markiert, blockierten aber trotzdem das MVP-Go-Gate.
2. Ingest war gleichzeitig optional nach RC und verpflichtend vor RC.
3. Security-/UI-Arbeit stand in mehreren Plänen vor dem eigentlichen Read-Hotpath.
4. Das Stable Capability Set variierte zwischen neun, zehn und zwölf Operationen.
5. `GraphLookup`/`GraphReader`, `Port`/`Client` und Runtime-Pfade waren widersprüchlich benannt.
6. Identische IDs wie `T1` oder `M1` bezeichneten je Plan andere Arbeiten.
7. Aufwandssummen passten nicht zu den eigenen Taskgrößen.
8. Focused MVP, Graphify-Claim, Extension Platform, Marktvalidierung und 9/10-Review wurden in einem kritischen Pfad vermischt.

Diese Widersprüche sind im neuen autoritativen Master aufgelöst.

## Dokumenthierarchie

| Dokument | Status | Verbindlicher Inhalt |
|---|---|---|
| `00_master_execution_plan.md` | **ACTIVE / AUTHORITATIVE** | Scope, kanonische IDs, Abhängigkeiten, P50/P80, Gates, kritischer Pfad und nächste Schritte |
| `04_architecture_planner.md` | **NORMATIVE ANNEX** | Zielarchitektur, Invarianten, Migrationsseams, Verträge und Risiken; eigener Backlog/Schedule ist superseded |
| `06_9_of_10_plan.md` | **SCORE RUBRIC** | Zielscore und unabhängiger Review; Implementierungsphasen sind nicht autoritativ |
| `07_beat_graphify_competitive_plan.md` | **CONDITIONAL PROTOCOL** | Messmethodik und Claim-Gates; wird erst nach `COMP-PILOT` und separatem Charter aktiv |
| `01_technical_rebuild_planner.md` | **SUPERSEDED INPUT** | historische technische Begründung |
| `02_product_roadmap_planner.md` | **SUPERSEDED INPUT** | historische Produktbegründung |
| `03_mvp_planner.md` | **SUPERSEDED INPUT** | historische MVP-Begründung |
| `05_execution_planner.md` | **SUPERSEDED INPUT** | historischer Execution-Vorschlag |

`temp/review/00_overall_project_review.md` bleibt die autoritative Ist-Diagnose. Es besitzt keine Task-IDs oder Termine.

## Konsolidierte Scope-Horizonte

1. **Focused Core RC:** CLI, MCP-stdio, zwölf Stable Read-/Agent-Operationen.
2. **Feature Unfreeze:** erst nach wiederholter Nutzung durch Design-Partner.
3. **Optionale Wette:** genau eine von Network/Editor, Team-CI, Ingest-Rewrite oder Competitive Research.
4. **Competitive Claim:** eigener Charter; Accuracy, Speed und Extensibility separat grün.
5. **9/10-Audit:** unabhängiger lagging Review nach stabiler Nutzung und Marktbelegen.

## Verbindliche Lesereihenfolge

1. `temp/review/00_overall_project_review.md`
2. `temp/plan/00_master_execution_plan.md`
3. bei Architekturfragen: `temp/plan/04_architecture_planner.md`
4. bei Scorefragen: `temp/plan/06_9_of_10_plan.md`
5. nur bei aktivem Competitive Charter: `temp/plan/07_beat_graphify_competitive_plan.md`

Im Konfliktfall gewinnt immer `00_master_execution_plan.md`, außer bei factual review evidence; dort gewinnt `00_overall_project_review.md`.
