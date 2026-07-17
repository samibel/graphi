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
> `docs/plan/superseded/` liegen; wo sie noch alte Pfade (`temp/plan/…`,
> `docs/plan/…`) nennen, ist der Pfad historisch, nicht aktuell.

# Graphi — autoritativer Portfolio- und Execution-Plan

> **Status:** ACTIVE / SINGLE SOURCE OF TRUTH<br>
> **Stand:** 13. Juli 2026<br>
> **Repo-Basis:** `main` / `6db3938`, lokal identisch mit `origin/main`<br>
> **Ersetzt als Execution Authority:** `01`, `02`, `03`, `04`-Backlog, `05` und die Implementierungsphasen aus `06`
> **Diagnosequelle:** `temp/review/00_overall_project_review.md`

## 1. Entscheidung

Graphi bekommt **keinen Full Rewrite**. Der Core bleibt. Die fehlerhaften Systemgrenzen werden gezielt neu gebaut.

Die Ausführung wird in drei voneinander getrennte Ziele zerlegt:

1. **Focused Core RC:** sicheres, korrektes CLI-/MCP-stdio-Produkt.
2. **Optionale strategische Wetten:** Network/UI, Team-CI, Ingest-Rewrite, Competitive Claim und Extension Platform.
3. **9/10-Evidenz:** externe Produkt-/Business-Belege und unabhängiger Re-Review.

Nur Ziel 1 ist jetzt finanziert und ausführbar. Ziele 2 und 3 werden durch Trigger aktiviert, nicht durch Wunschlisten.

## 2. Aktueller Zustand

- Code und Konfiguration wurden durch die Reviews nicht verändert; nur `temp/` ist untracked.
- Gesamtbewertung: **5,0/10**, Risiko **HIGH**.
- Web-Test: 119/119 grün; Web-Build grün.
- Go-Core-/Engine-Pakete liefen weitgehend grün; Socket-/Loopback-Suites sind in der Review-Sandbox wegen verbotener Bind-Operationen nicht vollständig verifiziert.
- Es existiert noch kein glaubwürdiger Graphi-vs-Graphify-Harness.
- Aktuelle Benchmarks sind Regressionstests auf Tiny-Fixtures, keine Produkt- oder Competitive-Evidenz (`internal/bench/harness.go:213-218`).

### Release-blockierende Befunde für den fokussierten Core

1. Capability-Claims entsprechen nicht zuverlässig dem Runtime-Dispatch (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`).
2. `graphi setup` registriert MCP, aber Session-/Repository-/Store-Auflösung ist nicht als stabiler Produktvertrag definiert (`cmd/graphi/main.go:801`).
3. Caller/Callee/References und Agenttools enthalten Vollgraph-Scans (`engine/query/service.go:130-191`, `engine/agenttools/resolve/resolve.go:160`, `engine/agenttools/brief/brief.go:198`).
4. SQLite besitzt Endpoint-Indizes, die der Read-Vertrag nicht selektiv nutzt (`core/graphstore/sqlite.go:187-192`, `core/graphstore/sqlite.go:841-866`).
5. Privacy-Defaults und Dateirechte sind nicht durchgehend sicher (`engine/memory/memory.go:101-117`).
6. Release-Publikation ist nicht als ein SHA-gebundener DAG geschlossen (`.github/workflows/auto-release.yml:36-101`).
7. `extract`/`move` versprechen Semantik, die der Code nicht liefert (`engine/edit/refactor.go:174-208`).

### Nicht release-blockierend für den fokussierten Core

- HTTP/MCP-HTTP, Web, VS Code, TUI und GitHub Action werden deaktiviert oder als Labs markiert.
- Daemon wird nicht Teil des ersten Focused Core RC. MCP-stdio ist die langlebige Agent-Session.
- Vollständige Ingest-Zerlegung ist optional; zuerst wird Recovery charakterisiert.
- Extension Platform und Graphify-Vergleich sind separate Investmententscheidungen.

## 3. Exakter Stable Scope

### Stable Surfaces

- CLI
- MCP stdio

### Zwölf Stable Operations

1. `index`
2. `search`
3. `definition`
4. `callers`
5. `callees`
6. `references`
7. `neighborhood`
8. `impact`
9. `agent_brief`
10. `related_files`
11. `explain_symbol`
12. `change_risk`

Incremental Update ist Verhalten von `index`, keine dreizehnte Capability.

### Explizit nicht Stable

- HTTP und MCP-HTTP
- Web, VS Code und TUI
- Daemon
- GitHub Action
- PR-/Review-/Reviewer-Vertikale
- Memory, Distill, Skillgen und Savings-Dollar-Claims
- Taint/Security-Scanner als Produktionsversprechen
- alle schreibenden Refactorings
- neue Sprachen, Analyzer, Tools oder Surfaces
- SaaS, Billing, SSO/RBAC und Control Plane

## 4. Kanonische Zielverträge

Diese Namen und Verantwortungen sind verbindlich. Abweichungen benötigen ein ADR.

### Core Read Ports

```text
GraphLookup
  GetNode
  NodesByID
  Incoming
  Outgoing

SymbolLookupPort
  ExactName
  QualifiedName
  SourcePath
  Search
```

Incident-/Multi-Source-Batchabfragen werden nur ergänzt, wenn die Baseline ihren Bedarf belegt.

### Application Ports

- `QueryPort`
- `SearchPort`
- `AgentContextPort`

Alle Application Interfaces tragen das Suffix `Port`. Stable Consumer hängen nur an den Ports, die sie tatsächlich benötigen.

### Composition Root

`cmd/internal/runtime.Runtime` besitzt Store, Ingest, Session, Services und Shutdown genau einmal.

### Session/Profile

Der Vertrag definiert:

- Repository-Identität und Root;
- DB-/Meta-/State-Pfade;
- Initial-Ingest und Readiness;
- MCP `cwd`-/Roots-Verhalten;
- Session-Lifetime und Shutdown;
- unterstützte Betriebssysteme;
- Stable-/Labs-Profil.

### Capability Manifest

Das Manifest ist die einzige Wahrheit für Dispatch, MCP-Toolliste, CLI-Hilfe, Release-Profil, Coverage-Matrix und generierte Dokumentation.

## 5. Kanonischer Work Breakdown

Referenzteam für den Core RC: zwei Senior Go/Platform Engineers bei 70–75 % Fokus. P50/P80 sind Engineering-Personentage einschließlich Tests und Review, ohne externe Wartezeit.

| ID | Arbeitspaket | Owner | Abhängigkeit | P50/P80 | Exit-Evidenz |
|---|---|---|---|---:|---|
| `CT-01` | Auto-Publish sperren und Release-Artefakte einfrieren | Platform | keine | 1/2 | absichtlich rotes Gate erzeugt keinen Tag/Release |
| `TEST-01` | Charakterisierung: Outputs, Backends, Routes/Tools, MCP-Journey, Queryplan und Fullscan-Baseline | Platform | keine | 6/10 | reproduzierbare Golden-/Subprocess-/Plan-Tests; bekannte Defekte rot |
| `SCOPE-01` | Stable/Labs/Disabled-Taxonomie und exaktes 12er-Manifest | Platform/Product | `CT-01`, `TEST-01` | 3/5 | Manifest = Dispatch = CLI/MCP-Hilfe = Docs |
| `SAFE-01` | `extract`/`move`, Network-Surfaces und Remote-Export fail-closed; Claims korrigieren | Platform | `SCOPE-01` | 2/4 | keine Mutation/Exposition außerhalb Stable Scope |
| `SP-10` | Session/Profile-RFC und echte MCP-Repository-Journey | Platform | `SCOPE-01`, `TEST-01` | 5/8 | Setup→MCP initialize/list/call auf echtem Repo spezifiziert und rot/grün testbar |
| `SP-11` | Selective-Read-Spike: Endpoints, Symbolindizes, Cache-Bypass, Brief-Aggregate | Core | `TEST-01` | 4/7 | Queryplan/Rows-scanned und API-ADR; keine Scheingenauigkeit |
| `CORE-01` | `GraphLookup`/`SymbolLookupPort` für Memory und SQLite | Core | `SP-11` | 8/12 | Backend-Conformance, kanonische Ordnung, SQLite-Indizes belegt |
| `CORE-02` | Alle Stable-Hotpaths migrieren und gemessene Budgets setzen | Core | `CORE-01` | 10/16 | Query, resolve, explain, related, risk, brief ohne Vollgraph-Reads |
| `CAP-01` | Consumer-owned Ports und generiertes Capability Manifest | Platform | `SCOPE-01` | 8/12 | keine Stable-Methode als `Unavailable`-Stub; Labs-Fassade isoliert |
| `ING-DEC` | Cross-DB-Fault-Injection und Recovery-Disposition | Core | `TEST-01`, `SCOPE-01` | 5/8 | jeder Commit-/Killpunkt klassifiziert; Fix oder dokumentierte Unbedenklichkeit |
| `PRIV-01` | Ignore-, State-, Filemode- und Secret-Defaults | Security/Platform | `SP-10`, `ING-DEC` | 4/6 | `.gitignore` default-on; `0700/0600`; Secret-Fixture nicht im Graph |
| `RUN-01` | zentraler Runtime Root; CLI und MCP-stdio migrieren | Platform | `SP-10`, `CAP-01`, `CORE-02` | 8/12 | Ressourcen einmal owned/geschlossen; reale CLI-/MCP-Subprocess-E2E |
| `REL-01` | SHA-gebundener Release-DAG, Pins, SBOM und Attestation | DevOps | `CT-01`, `TEST-01`; Manifest-Join vor RC | 8/12 | Gate→Build→Provenance→Publish auf demselben SHA; Red-Gate-Test |
| `EVAL-01` | 20 Hero-Aufgaben und drei gepinnte Real-Repos definieren | Product/Eval | `SCOPE-01` | 5/8 | Source-Anker, Ambiguität, Runnerklasse und Budgets versioniert |
| `EVAL-02` | Hero-/Real-Repo-Gates ausführen | Eval/Platform | `EVAL-01`, `CORE-02`, `RUN-01`, `PRIV-01` | 8/14 | Wallclock, RSS, DB-Größe, p95, Evidence und Raw Runs publiziert |
| `RC-01` | Recovery-, Release-, Manifest- und Eval-Evidenz zusammenführen | Release Owner | `ING-DEC`, `REL-01`, `EVAL-02`, `CAP-01` | 3/5 | Focused Core RC Go/No-Go dokumentiert; Publish erst bei Go |

Gesamt: ungefähr **16–18 Personenwochen P50** und **24–26 Personenwochen P80**. Bei zwei Seniors mit 70–75 % Fokus entspricht das grob **11–18 Kalenderwochen**. Erst nach `TEST-01`, `SP-10` und `SP-11` wird die Schätzung neu belastet.

## 6. Technisch korrekte Reihenfolge

```text
CT-01 ───────────────────────────────→ REL-01 ───────────────┐
                                                            │
TEST-01 → SCOPE-01 ─┬→ SP-10 ──────────────┐                │
                    ├→ CAP-01 ──────────────┤                │
                    ├→ EVAL-01 ─────────────┼──────┐         │
                    └→ SAFE-01              │      │         │
                                            ▼      │         │
TEST-01 → SP-11 → CORE-01 → CORE-02 → RUN-01 → EVAL-02 ─────┤
TEST-01 → ING-DEC → PRIV-01 ────────────────────────────────┤
                                                            ▼
                                                          RC-01
```

### Parallel ausführbar

- `REL-01` parallel zum Core-Pfad; Publish bleibt bis `RC-01` gesperrt.
- `EVAL-01` parallel zur Implementierung.
- `ING-DEC` und `PRIV-01` parallel zu Read-/Runtime-Arbeit.
- Design-Partner-Rekrutierung startet sofort, blockiert aber keinen Build.

### Nicht vorziehen

- HTTP-SecurityEnvelope
- Web-/VS-Code-Rewrite
- GitHub-Action-Rewrite
- vollständige Ingest-Zerlegung
- Vollgraph-Cache-Neuschreibung ohne Messung
- Entfernung der Legacy-Fassade vor Migration aller Stable Consumer
- WASI-/Extension Platform
- Full Graphify Competitive Program
- SaaS-/Enterprise-Infrastruktur

## 7. Gates

### G0 — Contained

- Auto-Publish gesperrt.
- Stable/Labs/Disabled sichtbar.
- `extract`/`move` mutieren nicht.
- Network/UI/Action nicht als Stable ausgeliefert.

### G1 — Contracts Frozen

- 12er-Capability-Manifest akzeptiert.
- Session/Profile und MCP-Repository-Lifecycle akzeptiert.
- Selective-Read-ADR und Evaluation Protocol akzeptiert.
- unterstützte OS-/Release-Profile festgelegt.

### G2 — Core Slice

- Stable Queries und Agenttools umgehen Fullgraph-Cache/-Scans.
- Memory/SQLite liefern deterministisch identische Semantik.
- Manifest entspricht tatsächlichem Dispatch.
- Privacy Defaults sind grün.

### G3 — Real Journey

- frisches Install/Setup kann über CLI und MCP-stdio indexieren und alle zwölf Stable Operations beantworten.
- kein HTTP, Browser oder Daemon wird dafür benötigt.
- Fehler-, Ambiguitäts- und Partial-Support-Zustände sind ehrlich.

### G4 — Focused Core RC

- 20 Hero-Aufgaben grün.
- Full-runs auf drei gepinnten Repositories mit Raw-Evidenz.
- Recovery-Disposition geschlossen.
- Release-Artefakte stammen vom exakt getesteten SHA.
- keine High-/Critical-Findings im Stable Scope.

### G5 — Repeat Use / Feature Unfreeze

- 3–5 Design-Partner aktiviert;
- mindestens 3/5 erreichen den Hero-Workflow;
- mindestens 2 nutzen ihn vier Wochen wöchentlich;
- erst dann wird genau **eine** optionale Wette finanziert.

## 8. Optionale Wetten nach G5

### `NET-01` — Network/Editor

**Trigger:** mindestens zwei aktive Partner verlangen Web/VS Code oder Remote-MCP.<br>
**Scope:** SecurityEnvelope, Host/Origin/Auth/Scopes, HTTP-Lifecycle, Shared TS Client, Web/VS-Code-Korrektheit.<br>
**P80:** 9–15 Personenwochen.<br>
**Gate:** vollständige Security-Negativsuite plus Contract-/UI-E2E.
**Stopping Rule:** Network-Surfaces bleiben disabled, solange ein High-Finding oder Contract-Drift existiert.

### `ACT-01` — Team CI

**Trigger:** mindestens zwei aktive Teams verlangen CI-/PR-Gates.<br>
**Scope:** versioniertes Release-Artefakt, Consumer-Repo-E2E, Outputs und Upgradepfad.<br>
**P80:** 4–7 Personenwochen.<br>
**Gate:** gepinnte Version läuft außerhalb des Graphi-Sourcebaums.
**Stopping Rule:** Action bleibt nicht „shipped“, bis Consumer-E2E grün ist.

### `ING-REWRITE` — Ingest-Neuschnitt

**Trigger:** `ING-DEC` oder Real-Repo-Gates beweisen Recovery-/Ressourcenfehler.<br>
**Scope:** Scanner/Parse/Commit/Link/Checkpoint-Phasen, Journal und Recovery.<br>
**P80:** 6–12 Personenwochen.
**Stopping Rule:** kein Rewrite allein wegen Dateigröße oder Architekturästhetik.

### `COMP-PILOT` — Graphify Feasibility

**Trigger:** G4 grün und explizites Research-Budget.<br>
**Scope:** zwei gepinnte Repos, kleines tool-neutrales Goldset, 40 Agent-Aufgaben, korrekte Wallclock/RSS/Freshness.<br>
**P80:** 6–10 Engineering-Personenwochen plus Annotation/Compute.<br>
**Proceed:** Accuracy mindestens non-inferior, praktischer Speed-Vorsprung und klarer Pfad zum Full Claim.
**Stop:** kein praktischer Vorteil oder notwendiger unfinanzierter Plattform-Rewrite.

### `COMP-FULL` — Öffentlicher „besser als Graphify“-Claim

**Trigger:** `COMP-PILOT` Proceed plus eigener Charter/Budget.<br>
**Protocol:** ausschließlich `07_beat_graphify_competitive_plan.md`.<br>
**Gate:** Genauigkeit, Geschwindigkeit und Erweiterbarkeit alle separat grün.
**Hinweis:** kein Bestandteil des Focused Core RC oder des normalen 9/10-Scores.

### `EXT-PLATFORM` — External Extensions

**Trigger:** mindestens drei glaubwürdige Extension-Jobs und zwei externe Autoren verpflichten sich zu Prototypen.<br>
**Scope:** Manifest/Catalog, WASI, Sidecar, SDK, Conformance und Isolation.
**Stop:** nicht bauen, um einen Reviewscore oder Featurevergleich kosmetisch zu gewinnen.

## 9. Produkt-, Business- und Auditpfad

### Product Expansion

- MVP-Gate: 3–5 Partner; mindestens zwei wiederholte Nutzer.
- Competitive Preference: zehn Teams; mindestens 60 % bevorzugen Graphi im definierten Code-Agent-Workflow.
- 9/10 Product: zwanzig Teams plus dokumentierte Acht-Wochen-Retention.

### Commercial Gate

- 15–20 problemorientierte Interviews;
- mindestens drei konkrete Pilotangebote;
- mindestens ein bezahlter Pilot oder bedingte Kaufzusage vor kommerzieller Infrastruktur;
- für Business 8,5: fünf zahlende Organisationen und drei Verlängerungen/Erweiterungen.

Business-Evidenz blockiert keinen technisch sicheren OSS-Release.

### External 9/10 Audit

- nach mindestens 90 Tagen stabilem Betrieb;
- sechs frische unabhängige Reviewer;
- Mittelwert ≥9,0, keine Dimension <8,5;
- Security und DevOps ≥9,0;
- keine High-/Critical-Blocker.

Der 9/10-Score ist ein laggender Evidenzwert, kein Architekturtreiber. Das Competitive Claim ist davon unabhängig.

## 10. Stopping Rules

- Keine horizontale Capability-/Surface-Arbeit, solange G4 rot ist.
- P80 >150 % der freigegebenen Schätzung: stoppen und Scope neu schneiden.
- Kein Ingest-Rewrite ohne reproduzierbaren Fehler.
- Keine Network-Surface ohne vollständige Security-Negativsuite.
- Zwei gescheiterte Design-Partner-Kohorten: Expansion stoppen; Graphi als fokussierten OSS-Core betreiben.
- Kein bezahlter Pilot nach Interview-/Offer-Gate: SaaS, Billing, RBAC und Control Plane stoppen.
- Competitive Pilot ohne praktischen Vorteil: universellen Graphify-Claim verwerfen.
- Keine Extension Platform ohne konkrete externe Nachfrage.

## 11. Nächste fünf Schritte

1. `CT-01`: automatische Veröffentlichung sperren.
2. `TEST-01`: Charakterisierungstests und ehrliche Baseline erstellen.
3. `SCOPE-01`: exaktes 12er-Manifest und Stable/Labs/Disabled-Taxonomie einfrieren.
4. `SP-10` und `SP-11` parallel: Session/Profile sowie Selective-Read-Vertrag entscheiden.
5. Nach den Spikes P50/P80 neu laden und erst dann `CORE-01`, `CAP-01` und `REL-01` implementieren.

## 12. Definition of Done für diese Plan-Konsolidierung

- Nur dieses Dokument vergibt ausführbare IDs, Abhängigkeiten, Aufwand und Gates.
- Architekturdetails kommen aus `04`, Score-Rubrik aus `06`, Competitive Protocol aus `07`.
- `01`, `02`, `03` und `05` sind historische Inputs, keine Execution Authorities.
- Kein MVP-Gate enthält HTTP, UI, Action, Extension Platform oder Business-Evidenz.
- Focused RC, Competitive Claim und 9/10-Audit sind getrennt und können unabhängig Go/No-Go erreichen.
