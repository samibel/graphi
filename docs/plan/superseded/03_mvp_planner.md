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

# MVP-Plan für Graphi

> **Status: SUPERSEDED PLANNING INPUT.** Scope-Überlegungen bleiben Referenz. Der verbindliche Stable Scope und das Focused-Core-Gate stehen in `00_master_execution_plan.md`.

## Entscheidung

**TEIL-REWRITE der Systemgrenzen; kein Full Rewrite des Cores.**

Die Entscheidung ist enger als ein allgemeiner Refactor: Die Verträge für Capability-Komposition, Runtime/Lifecycle, releasefähige Distribution und später netzwerkfähige Surfaces werden neu geschnitten. Behalten und schrittweise verbessert werden Domänenmodell, Parser-/Analyzer-Registries, Provenienz, Graphstore-Schema, deterministische Serialisierung und die vorhandenen Query-/Agent-Kernels.

Ein Full Rewrite wäre sachlich falsch, weil die teuersten Invarianten bereits vorhanden sind: immutable und provenance-pflichtige Edges (`core/model/edge.go:48-113`), ein deterministischer Graphstore-Vertrag (`core/graphstore/graphstore.go:55-146`) und registrierbare Parser (`core/parse/registry.go:10-100`). Reines Weiterbauen oder nur lokale Patches wären ebenfalls falsch: Der zentrale Client bündelt Query, Suche, Analyse, Mutation, Memory und Forge in einem Interface (`surfaces/client/client.go:268-464`), Traversals scannen ganze Kantenklassen (`engine/query/service.go:145-173`), und der Daemon-Prozess kann nach `stop` nicht aus seinem Endlosblock zurückkehren (`cmd/graphi/main.go:1138-1145`). Das sind Vertrags- und Orchestrierungsfehler, keine isolierten Tippfehler.

## Minimales sinnvolles Zielbild

Graphi MVP ist **lokaler, zitierbarer Code-Kontext für Coding-Agenten**, nicht Graphplattform, IDE-Suite, PR-Bot, Refactoring-Engine oder SaaS.

Der einzige Hero-Workflow lautet:

1. Nutzer startet Graphi im Repository; Graphi indexiert lokal und zeigt Fortschritt bzw. einen ehrlichen Fehlerzustand.
2. Ein Mensch oder MCP-Agent sucht ein Symbol oder beschreibt ein Änderungsthema.
3. Graphi liefert einen kompakten Kontextpfad aus `agent_brief`, `related_files`, `explain_symbol` und `change_risk`, ergänzt um `callers`, `callees`, `references` und `impact`.
4. Jede Antwort enthält deterministische Identitäten, Evidence/Provenienz, Confidence und einen expliziten Zustand wie `ok`, `ambiguous`, `partial`, `empty` oder `unavailable`; Graphi rät nicht still.
5. Wiederholte Abfragen laufen über einen lokalen Hot-Daemon mit restriktivem Unix-Socket und sauberem Start/Stop. CLI und MCP-stdio funktionieren auch ohne Netzwerkoberfläche.

### MVP-Scope, exakt

**Enthalten:**

- vorhandener lokaler Full-/Incremental-Index und persistenter SQLite-Graph;
- lexikalische Symbolsuche;
- `callers`, `callees`, `references`, `impact`;
- `agent_brief`, `related_files`, `explain_symbol`, `change_risk`;
- CLI und MCP-stdio als primäre Produktoberflächen;
- lokaler Daemon über Unix Domain Socket ausschließlich für Hot-Index/Reads;
- explizites Capability-Manifest und kleine, read-only Capability-Ports;
- endpoint-selektive Incoming-/Outgoing-Reads in Store und Query-Service;
- sichere lokale Defaults: `.gitignore` standardmäßig respektieren, private State-Dateirechte, keine persistente Memory-Funktion im MVP;
- reproduzierbarer Release-Build für die bereits vorhandene OS/Arch-Matrix, aber nur aus einem Commit-gebundenen, autoritativen Release-DAG;
- reale Corpus-/Journey-Gates für Korrektheit, First-run und Warm-query statt einer pauschalen Produkt-Scorecard.

**Nicht MVP:**

- HTTP, SSE, eingebettete Web-UI, TUI und VS-Code-Extension;
- MCP über HTTP;
- GitHub Action, PR-Triage, PR-Konflikte, Reviewer-Vorschläge, Review-Kritik und Kommentar-Publishing;
- `extract`, `move`, `inline`, `safe-delete`, Signaturänderung und andere schreibende Codeoperationen;
- Memory, Distill, SkillGen und Savings-/Dollar-Claims;
- semantische Suche, optionale Embedding-/ONNX-Varianten;
- neue Sprachen, Parser, Analyzer, Surfaces oder Capability-Zähler;
- Hosted SaaS, Accounts, Telemetrie, Billing, SSO/RBAC, Control Plane oder Enterprise-Policy-System;
- Marketplace-, Homebrew-/Scoop- oder kommerzielle Packaging-Arbeit, bevor der Hero-Workflow real validiert ist.

Vorhandener Code außerhalb des MVP muss nicht sofort physisch gelöscht werden. Er darf aber nicht im Default-Onboarding, stabilen Capability-Manifest oder Release-Versprechen erscheinen und erhält bis zu einer eigenen Reaktivierungsentscheidung nur Security-/Build-Erhalt, keine Feature-Arbeit.

## Was zuerst gebaut wird

**Zuerst entsteht eine harte Produkt- und Release-Grenze, danach der skalierbare Read-Pfad.**

1. Auto-Publishing deaktivieren, Stable-Capability-Allowlist einführen und Nicht-MVP-Kommandos/Claims aus dem Defaultpfad nehmen.
2. `Incoming`/`Outgoing` als endpoint-selektiven Reader-Vertrag implementieren und SQLite tatsächlich über `edges_from_id`/`edges_to_id` lesen lassen.
3. Capability-spezifische Ports plus zentralen Runtime-/Lifecycle-Root einführen; CLI, MCP-stdio und Daemon nur an die Read-MVP-Ports hängen.
4. Daemon-Ende, Signalbehandlung, Cleanup und E2E-Prozesslebenszyklus schließen.
5. Erst danach reale Journey-/Performance-Gates, Release-DAG und ein externer MVP-Test.

Diese Reihenfolge verhindert, dass Validierung und Distribution noch einmal auf einem falschen Kostenmodell oder unehrlichen Capability-Vertrag aufgebaut werden.

## Was gelöscht, deaktiviert oder eingefroren wird

### Sofort deaktivieren oder aus Stable entfernen

- automatisches Release-Publishing, bis Gate, Build, Tag und Upload an exakt denselben Commit gebunden sind (`.github/workflows/auto-release.yml:36-101`, `.github/workflows/release-gate.yml:1-30`);
- `extract` und `move` aus öffentlichen Deskriptoren/Stable-Help; die aktuelle Implementierung routet beide wie Rename durch `planNameRewrite` und ignoriert die behauptete Move-Semantik (`engine/edit/refactor.go:174-186`);
- `graphi http` aus dem MVP-Releasepfad; der Handler registriert sensible Routen ohne serverseitige Authentisierung oder Host-/Origin-Schutz (`surfaces/http/server.go:181-212`);
- GitHub Action als „shipped“ oder empfohlener Installationspfad; `graphi-version` steuert den Build-Quellstand nicht, gebaut wird `./cmd/graphi` im Workspace (`extensions/github-action/action.yml:113-125`);
- Savings-, 100/100-, „all surfaces shipped“- und nicht reproduzierbare Benchmark-Claims.

### Einfrieren, nicht erweitern

- Web, VS Code, TUI, HTTP/SSE, MCP-HTTP und GitHub Action;
- PR-, Forge-, Memory-, Distill-, SkillGen-, Edit- und semantische Suchfähigkeiten;
- sämtliche neue Analyzer-, Parser-, Sprach- und Surface-Arbeit;
- große interne Umstrukturierungen des Ingesters, solange sie nicht für MVP-Korrektheit, Crash-Recovery oder messbare First-run-Ziele nötig sind.

### Behalten

- `core/model`, Graphstore-Verträge und SQLite-Schema als Migrationsbasis;
- Parser-/Analyzer-Registries und deterministische Ingest-Reihenfolge;
- Agent-Response-Contract mit Evidence/Confidence/Outcome;
- Local-first-/Zero-egress-Guards und Privacy-Gates;
- vorhandene reproduzierbare Build-Utilities.

## Schrittfolge und Prioritäten

### P0 — Release- und Trust-Bremse

- Stable-Allowlist definieren; alles außerhalb des exakten MVP-Scopes als `experimental`, `unavailable` oder nicht ausgeliefert kennzeichnen.
- Auto-Release bis zum neuen DAG abschalten.
- Falsche Schreiboperationen fail-closed schalten.
- HTTP/MCP-HTTP und die Action aus Stable-Distribution/Docs entfernen.

**Exit:** Kein Default- oder Releasepfad behauptet eine Capability, die nicht Ende-zu-Ende gegatet ist.

### P1 — Korrekte und skalierbare Read-Basis

- `Reader` um endpoint-selektive Incoming-/Outgoing-Operationen mit Kindfilter erweitern.
- SQLite-Abfragen über die bereits vorhandenen From-/To-Indizes implementieren; In-memory-Backend erhält passende Adjazenzindizes.
- Query-/Agent-Kernels auf den neuen Vertrag umstellen und deterministische Sortierung beibehalten.
- `.gitignore` standardmäßig respektieren und Migration/Opt-out explizit dokumentieren.

**Exit:** Caller-/Callee-/Reference-Abfragen sind proportional zum Knotengrad statt zur gesamten Kantenklasse; ignorierte Dateien landen im Default nicht im persistenten Graph.

### P1 — Ehrliche Capability- und Runtime-Grenze

- den monolithischen `client.Client` in mindestens `QueryClient`, `SearchClient`, `AgentContextClient` und getrennte Nicht-MVP-Ports zerlegen;
- maschinenlesbares Capability-Manifest pro Surface statt `Unavailable`-Stub-Parität;
- einen zentralen `Runtime`/Composition Root für Store, Ingest, Query, Agent-Assembler, Watcher und Cleanup bauen;
- CLI/MCP-stdio/Daemon fordern jeweils nur ihre kleinsten Ports an.

**Exit:** Eine MVP-Surface kompiliert ohne Edit-, Memory-, Forge- oder HTTP-Abhängigkeit; Manifest und wirklich aufrufbare Operationen stimmen in Contract-Tests überein.

### P1 — Lifecycle und sichere lokale Ausführung

- Daemon erhält `Done`, idempotentes `Shutdown`, Signal-Context und begrenztes Cleanup;
- `daemon stop` muss den gestarteten OS-Prozess wirklich beenden;
- Status liefert PID, Uptime, Generation, Readiness und Watcherzustand statt einer Dummy-Query;
- State-Verzeichnisse/-Dateien werden bei Erstellung und Migration auf `0700`/`0600` gesetzt.

**Exit:** Start→Ready→Query→Stop ist als echter Subprozess getestet; Socket, Prozess, Watcher und DB-Handles sind nach dem Stop innerhalb des Budgets beendet.

### P2 — Reale Produkt- und Release-Gates

- 20 repräsentative Agentenaufgaben auf mehreren echten Repositories mit Rohdaten/Runner definieren;
- Spring-Boot-ähnlichen großen Corpus vollständig und nicht nur per Proxy messen;
- Release-Gate, Build, Reproducibility, Tag und Publish in einen SHA-gebundenen DAG bringen;
- Actions im Releasepfad auf vollständige SHAs pinnen; SBOM/Attestation ergänzen.

**Exit:** Ein Release kann nur für den exakt gegateten Commit entstehen; reale Journey-Ergebnisse und Ressourcenwerte sind reproduzierbar.

### P3 — Validierung vor Expansion

- 3–5 Design Partner auf den Hero-Workflow onboarden, ohne versteckte Telemetrie;
- Aktivierung, wiederholte Nutzung, Task-Erfolg, Latenz, Fehlantworten und Kontextreduktion manuell/opt-in erfassen;
- erst danach entscheiden, ob VS Code, Web oder Team-CI der nächste Wedge ist.

**Exit:** Mindestens ein Design Partner nutzt den Workflow vier Wochen wiederholt; für einen Paid-Pilot liegt eine Zahlung oder konkrete Kaufzusage unter definierten Bedingungen vor. Andernfalls kein Paid-/Surface-Ausbau.

## Aufwandsskala

- **S:** höchstens 2 Personentage; lokaler, klar begrenzter Patch mit bestehenden Tests.
- **M:** 3–5 Personentage; ein Modul oder eine Surface, neue Tests/Docs, keine Datenmigration.
- **L:** 6–10 Personentage; mehrere Module oder ein neuer Vertrag inklusive Migration und E2E-Gate.
- **XL:** mehr als 10 Personentage oder mehrere Systemgrenzen/Teams; muss in kleinere lieferbare Tasks zerlegt werden.

Das gesamte MVP-Konsolidierungsprogramm ist **XL, grob 8–12 Personenwochen**. Die Spanne setzt zwei erfahrene Engineers, vorhandene Testsubstanz und keine überraschende Graphstore-Migration voraus. Kalenderzeit, Maintainer-Verfügbarkeit und externe Design-Partner-Rekrutierung sind UNKNOWN.

## Konkrete nächste Tasks

| ID | Task | Aufwand | Abhängigkeit | Messbare Abnahmekriterien |
|---|---|---:|---|---|
| MVP-01 | Stable-Scope-Manifest und Release-Bremse | M | keine | Manifest listet ausschließlich die 12 vereinbarten Read-Jobs/Operationen; CLI-/MCP-Contract-Test beweist Manifest=Dispatch; Auto-Publish kann bei rotem Gate keinen Tag/Release erzeugen; Nicht-MVP-Claims sind nicht im Default-Onboarding. |
| MVP-02 | Schreiboperationen fail-closed | S | MVP-01 | `extract` und `move` liefern vor jedem Read/Write einen typisierten `not implemented`-Fehler; Tests beweisen unveränderten Sourcebaum und Graph; Stable-Deskriptoren enthalten sie nicht. |
| MVP-03 | Endpoint-selektiver GraphReader | L | keine | `Incoming`/`Outgoing` existieren für SQLite und Memory; Contract-Tests prüfen identische, kanonisch sortierte Ergebnisse; SQLite-Queryplan nutzt `edges_from_id` bzw. `edges_to_id`; Query-Service ruft für Traversals nicht mehr `Edges(EdgeKind)` auf. |
| MVP-04 | Realistische Traversal-Budgets | M | MVP-03 | Fixture mit mindestens 1 Mio. Kanten; p95-Warm-Latenz und Peak-RAM werden im Repo als Baseline erfasst; Verdopplung fachfremder Kanten erhöht die Nachbarschaftslatenz nicht linear; konkrete Grenzwerte werden nach erstem reproduzierbaren Baseline-Lauf festgeschrieben. |
| MVP-05 | Capability-Ports und Manifest | L | MVP-01 | `QueryClient`, `SearchClient`, `AgentContextClient` sind getrennt; CLI/MCP-stdio/Daemon-MVP bauen ohne Edit/Memory/Forge; keine MVP-Methode wird nur durch `Err...Unavailable` formal erfüllt; Cross-Surface-Tests prüfen Bytes nur für gemeinsam deklarierte Capabilities. |
| MVP-06 | Zentraler Runtime-/Composition Root | L | MVP-05 | Store, Ingest, Watcher und Agent-Services werden genau einmal konstruiert und genau einmal geschlossen; CLI-Entrypoint enthält keine duplizierten Service-Graphen für MVP-Surfaces; Fault-Tests prüfen Cleanup nach Fehlern jeder Startphase. |
| MVP-07 | Daemon-Lifecycle E2E | M | MVP-06 | Echter Binärprozess erreicht `ready`, beantwortet Query und beendet sich nach `daemon stop` in höchstens 5 s mit Exit 0; Socket ist entfernt; erneuter Start gelingt; SIGINT/SIGTERM schließen Store und Watcher. |
| MVP-08 | Privacy-sichere Indexdefaults | M | MVP-06 | `.gitignore` gilt default-on; Fixture-Secrets in ignorierten Dateien erscheinen weder in Graph noch Suchergebnis; State-Dirs sind `0700`, Dateien `0600`; Migration korrigiert zu weite Modi; expliziter Opt-out ist sichtbar und gewarnt. |
| MVP-09 | Agent-Hero-Workflow Contract-Gate | L | MVP-03, MVP-05, MVP-06 | 20 versionierte Aufgaben mit erwarteten Evidenzankern; jede Antwort hat Outcome, Evidence und Confidence; keine Ambiguität wird als eindeutiger Treffer ausgegeben; CLI und MCP-stdio liefern für gleiche Inputs kanonisch gleiche Payloads. |
| MVP-10 | Real-Repo First-run/Hot-run Gate | L | MVP-07, MVP-09 | Vollständiger Lauf auf mindestens einem großen Java-Monorepo plus zwei anderssprachigen Repos dokumentiert Wall-clock, Time-to-first-use, Peak-RAM, DB-Größe, Warm-p95 und Signalqualität; keine Proxy-Metrik ersetzt den Full-run; Rohdaten und Runner sind eingecheckt. |
| MVP-11 | Autoritativer Release-DAG | L | MVP-01, MVP-07, MVP-09 | Gate→Build→Reproducibility→SBOM/Attestation→Tag→Publish hängt an einem unveränderlichen SHA; absichtlich rotes Gate publiziert nichts; Installer verifiziert Herkunft zusätzlich zur SHA-256-Integrität; Release-Matrix läuft für alle unterstützten Ziele. |
| MVP-12 | Externe MVP-Validierung | XL | MVP-10, MVP-11 | 3–5 Design Partner; mindestens einer nutzt Graphi vier Wochen wiederholt; dokumentierte Aktivierung und Task-Erfolg ohne heimliche Telemetrie; Entscheidung `iterate`, `expand` oder `stop` anhand vorab definierter Schwellen. |

## Abhängigkeitskritischer Pfad

`MVP-01 → MVP-05 → MVP-06 → MVP-07 → MVP-10 → MVP-11 → MVP-12`

Parallel möglich: `MVP-03 → MVP-04`, `MVP-02` direkt nach `MVP-01`, und `MVP-08` nach `MVP-06`. `MVP-09` benötigt sowohl den neuen Reader als auch Capability-/Runtime-Grenzen.

## Risiken und Gegenmaßnahmen

1. **Verdeckte Surface-Kopplung macht das Client-Splitting größer als erwartet.** Gegenmaßnahme: strangler-artig kleine Ports neben dem alten Interface einführen; nur MVP-Surfaces migrieren, Alt-Surfaces einfrieren.
2. **Endpoint-Reads brechen Byte-Determinismus oder Backend-Parität.** Gegenmaßnahme: kanonische Sortierung bleibt Vertragsbestandteil; gemeinsame Memory-/SQLite-Contract-Suite vor Umschaltung des Query-Service.
3. **Vollgraph-Cache begrenzt RAM trotz schneller Traversals.** Gegenmaßnahme: im MVP messen und Budget festschreiben; Cache-Redesign ist ein eigener Nach-MVP-Entscheid, sofern das reale Gate scheitert.
4. **Unix-Socket-Daemon schließt Windows aus.** Gegenmaßnahme: MVP-Daemon-Support explizit auf Unix-Systeme begrenzen; Windows erhält CLI/MCP-stdio, bis ein eigener Transport E2E bewiesen ist.
5. **Scope-Freeze wird durch vorhandene 142 Capabilities unterlaufen.** Gegenmaßnahme: nur das Manifest ist Stable-Wahrheit; Coverage-Zahl ist kein Release-Kriterium; jede Reaktivierung braucht Owner, Journey, Threat Model und E2E-Gate.
6. **Security-Schuld bleibt im eingefrorenen HTTP-Code latent.** Gegenmaßnahme: HTTP/MCP-HTTP nicht ausliefern oder bewerben; vor Reaktivierung verpflichtender Security-Envelope mit Bearer-Auth, Host-/Origin-Policy, read-only Default-Scopes, Bodylimits und Timeouts.
7. **Marketingdruck führt erneut zu unbewiesenen Claims.** Gegenmaßnahme: Website-Aussagen dürfen nur auf eingecheckte Rohdaten und reproduzierbare Runner referenzieren; interne Regression-Scorecard nicht als Produktqualität darstellen.
8. **Design-Partner-Rekrutierung dauert länger als Engineering.** Gegenmaßnahme: Rekrutierung startet parallel zu P1; keine SaaS-/Billing-Vorleistung, manueller Pilot genügt.
9. **Cross-DB-Crash-Recovery im Ingest bleibt unbewiesen.** Gegenmaßnahme: Fault-Injection an Phasengrenzen in MVP-10; größere Ingester-Zerlegung nur bei reproduzierbarem Fehler.

## Review- und Codebelege

- Gesamtentscheidung und Scope-Risiko: `temp/review/00_overall_project_review.md`, insbesondere Teil-Rewrite, Top-10-Probleme und „Was eingefroren oder gelöscht wird“.
- Architektur: `temp/review/01_architecture_expert.md`; der öffentliche `Query`-Filter kennt kein `From`/`To` (`core/graphstore/graphstore.go:41-53`), `directedLookup` lädt alle Kanten einer Art (`engine/query/service.go:145-173`), obwohl SQLite Endpoint-Indizes besitzt (`core/graphstore/sqlite.go:187-192`) und `Edges` den Cache scannt (`core/graphstore/sqlite.go:841-866`).
- Capability-Sprawl: `temp/review/01_architecture_expert.md` und `temp/review/02_senior_fullstack_engineer.md`; `client.Client` reicht von Query/Search bis Edit, Memory und Forge (`surfaces/client/client.go:268-464`).
- Falsche öffentliche Semantik: `temp/review/02_senior_fullstack_engineer.md`; `extract`/`move` laufen wie Rename (`engine/edit/refactor.go:174-186`), VS Code erwartet `id/path`, der Server liefert `node_id/source_path` (`extensions/vscode/src/contract.ts:103-107`, `engine/search/service.go:18-35`), und Web dedupliziert Parallelkanten nach Endpunkten (`web/src/GraphView.tsx:108-118`).
- Produktfokus und reale Validierung: `temp/review/03_product_feature_expert.md`; 142 Capabilities/42 Tools/acht Surfaces stehen einem nicht vollständig wiederholten realen Monorepo-Lauf gegenüber (`docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-177`, `docs/real-world-report.md:23-40`).
- Security/Privacy: `temp/review/04_security_privacy_expert.md`; HTTP gibt den nackten Mux ohne Auth/Host/Origin-Guard zurück (`surfaces/http/server.go:181-212`), MCP-HTTP dispatcht unauthentisiert (`surfaces/mcp/http.go:16-49`), mehrere JSON-Endpunkte haben keine Bodygrenze (`surfaces/http/server.go:698-745`).
- Production Readiness: `temp/review/05_devops_production_readiness_expert.md`; Daemon-Stop schließt Listener, aber der CLI-Prozess bleibt in `select {}` (`surfaces/daemon/daemon.go:170-186`, `cmd/graphi/main.go:1138-1145`); Release-Gate und Auto-Release sind nicht als ein SHA-gebundener DAG modelliert (`.github/workflows/auto-release.yml:36-101`).
- Business: `temp/review/06_business_monetization_expert.md`; keine repo-belegte Adoption, Retention oder Zahlungsbereitschaft, während das einzige Angebot USD 0 ist (`site/index.html:35-46`). Deshalb ist Business-Expansion kein MVP-Ersatz für Produktvalidierung.

## UNKNOWN

- reale aktive Nutzer, Installationen, Retention, Team-Rollouts und Supportlast;
- ICP, Buyer, Budget Owner, Zahlungsbereitschaft und akzeptierte Preismetrik;
- tatsächliche Veröffentlichung/Nutzung von VS Code, GitHub Action, Homebrew oder Scoop;
- vollständige aktuelle CI- und Branch-Protection-Konfiguration außerhalb des Repositories;
- konkrete Warm-p95-, Peak-RAM- und DB-Größenbudgets bei 1 Mio. Kanten; diese müssen durch MVP-04 festgelegt werden, nicht erfunden werden;
- reparierte Full-index-Werte auf Spring Boot bzw. vergleichbarem Monorepo;
- Accuracy/Recall je Sprache und ein belastbarer Stable-Sprachscope;
- Cross-DB-Crash-Recovery an allen Meta-/Graphstore-Commitgrenzen;
- aktueller CVE-Status ohne externen `govulncheck`-/OSV-/npm-audit-Lauf;
- Windows-Daemon-Lauffähigkeit; der aktuelle Daemon ist Unix-Socket-basiert;
- Threat Model für andere lokale Nutzer/Prozesse, DNS-Rebinding und kompromittierte MCP-Clients;
- Maintainer-Kapazität und damit belastbare Kalenderdauer;
- ob der Vollgraph-RAM-Cache nach endpoint-selektiven Reads noch innerhalb realer Budgets bleibt;
- ob der Hero-Workflow Agenten-Outcomes tatsächlich verbessert; Tokenreduktion allein beweist das nicht.

## Go/No-Go-Regel

**Go für einen MVP-Release** erst, wenn MVP-01 bis MVP-11 grün sind und kein Stable-Pfad eine nicht deklarierte Capability exponiert. **No-Go** für HTTP/MCP-HTTP, Web/VS Code, GitHub Action, schreibende Refactorings und private-repository-Sicherheitsclaims, solange ihre eigenen Security-, Contract- und Consumer-E2E-Gates fehlen. **No-Go** für weitere horizontale Features, bis MVP-12 reale wiederholte Nutzung belegt.
