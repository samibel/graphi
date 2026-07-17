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

# Product Roadmap Plan: Graphi

> **Status: SUPERSEDED PLANNING INPUT.** Produktbegründungen bleiben Referenz. Roadmap, Termine und Gates wurden durch `00_master_execution_plan.md` ersetzt.

## Entscheidung

**Entscheidung: Teil-Rewrite der Systemgrenzen, kein Full Rewrite.**

Der Begriff ist bewusst eng gefasst:

- **Behalten und gezielt refactoren:** `core/model`, Parser-/Analyzer-Registries, Graphstore-Schema als Ausgangspunkt, Provenienz- und Determinismus-Invarianten sowie bewährte Query-/Analyse-Kernels.
- **Neu schneiden:** HTTP-/MCP-HTTP-Sicherheitsgrenze, Capability-Verträge, Runtime-/Lifecycle-Orchestrierung, endpoint-selektive Traversal-Reads, gemeinsamer TypeScript-Vertrag sowie Release-/Action-Distribution.
- **Nicht tun:** den Engine-Core neu schreiben, weitere horizontale Analyzer/Sprachen/Surfaces hinzufügen oder jetzt eine SaaS-/Billing-Control-Plane bauen.

### Begründung

Ein Full Rewrite würde die stärksten Assets vernichten: unveränderliche, provenance-tragende Kanten, deterministische IDs, registrierbare Parser, SQLite-Persistenz, reproduzierbare Builds und umfangreiche Tests (`core/model/edge.go:48-113`, `core/parse/registry.go:10-100`, `core/graphstore/graphstore.go:55-204`, `internal/release/build.go:146-199`; Reviews `00`, `01`, `02`, `05`). Reines Weiterbauen ist ebenfalls nicht vertretbar: öffentlich sichtbare Pfade sind nachweislich falsch oder unsicher, und 142 Capabilities über acht Surfaces vervielfachen diese Fehlerfläche ohne belegte Adoption (`temp/review/00_overall_project_review.md`, `temp/review/03_product_feature_expert.md`).

Lokale Patches reichen an den betroffenen Grenzen nicht. Ein nackter HTTP-Mux ohne Authentisierung, ein breites Client-Interface mit zahlreichen `Unavailable`-Stubs, ein CLI-Prozess ohne beendbaren Lifecycle und eine Action ohne echte Source-Selektion sind Vertragsfehler, keine isolierten Bugs (`surfaces/http/server.go:181-212`, `surfaces/client/client.go:268-464`, `cmd/graphi/main.go:1138-1145`, `extensions/github-action/action.yml:113-125`; Reviews `00`, `01`, `04`, `05`). Daher: **Teil-Rewrite dieser Grenzen, inkrementeller Refactor des Kerns.**

## Produktbezogenes Zielbild

Graphi wird zunächst ein **lokales, evidence-backed Kontext- und Risiko-Werkzeug für Coding Agents und Entwickler**, nicht eine universelle Code-Intelligence-Suite.

Der GA-Kern beantwortet verlässlich einen zusammenhängenden Job:

> „Bevor Code geändert wird: Was ist relevant, warum ist es relevant, wer hängt davon ab und welches Änderungsrisiko besteht?“

Der primäre Workflow ist:

1. Repository lokal indexieren.
2. Task oder Symbol suchen.
3. `agent_brief` und `related_files` erzeugen.
4. `explain_symbol`, callers/callees/references und `change_risk` mit Provenienz liefern.
5. Unsicherheit explizit als `ambiguous`, `partial`, `empty` oder `unavailable` ausgeben.

Primäre GA-Surfaces sind **CLI und MCP-stdio**. Eine lokale Webansicht ist nachrangig und dient als Evidenzvisualisierung. HTTP, VS Code und GitHub Action gelten bis zum Abschluss ihrer jeweiligen Sicherheits-, Contract- und Consumer-E2E-Gates nicht als GA. Diese Eingrenzung folgt dem stärksten zusammenhängenden Produktpfad aus Review `03`; sie ist keine Aussage über reale Adoption, denn diese ist **UNKNOWN**.

Eine spätere self-hosted Team-/CI-Ausprägung ist eine **zu validierende Hypothese**, kein beschlossenes Geschäftsmodell. Hosted SaaS widerspricht derzeit dem belegten Local-first-/No-Account-Kern und wird nicht geplant (`readme.md:112-117`; Review `06`).

## Aufwandsskala und Planungsannahmen

Die Größen gelten für **eine erfahrene Person mit Repositorykenntnis**, inklusive Implementierung, Tests, Dokumentation und Review:

| Größe | Richtwert | Bedeutung |
|---|---:|---|
| S | 1–3 Arbeitstage | Lokaler, klar begrenzter Fix; vorhandene Test-Seam. |
| M | 1–2 Wochen | Mehrere Dateien/Packages oder ein neuer Contract plus Integrationsgate. |
| L | 3–5 Wochen | Querschnitt über mehrere Surfaces mit Migration und E2E-Abnahme. |
| XL | 6–10 Wochen | Systemgrenze wird neu geschnitten; mehrere abhängige Migrationsschritte. |

Kalenderdauer bei mehreren Personen, Maintainer-Verfügbarkeit, Review-Latenz und externe Distribution sind **UNKNOWN**. Größen sind keine Commitments; nach Phase 0 und den Spikes werden sie neu geschätzt.

## Priorisierte Roadmap

### Phase 0 — Freeze, Claims und Release-Schadensbegrenzung (Woche 0–1, S–M)

Ziel: Keine weiteren Nutzer in nachweislich falsche oder unsichere Pfade führen.

1. **Automatische Releases und externe Action-Bewerbung pausieren (S).**
   - Abhängigkeiten: keine.
   - Abnahme: Kein Publish-Pfad kann ohne explizite Freigabe laufen; Website/README markieren Action und nicht verifizierte Surfaces als experimental/unavailable.
   - Beleg: `auto-release` kann das separate Release-Gate umgehen (`.github/workflows/auto-release.yml:36-101`; Review `05`).
2. **`extract` und `move` fail-closed schalten; irreführende Claims entfernen (S).**
   - Abhängigkeiten: keine.
   - Abnahme: Aufruf mutiert keine Datei und liefert stabil `unavailable/not implemented`; CLI, MCP, HTTP und Doku behaupten keine echte Extract-/Move-Semantik.
   - Beleg: Beide Operationen routen auf bloßes Name-Rewrite (`engine/edit/refactor.go:174-186`; Reviews `00`, `02`).
3. **Produktstatus-Taxonomie einführen (S).**
   - Abhängigkeiten: keine.
   - Abnahme: Jede Capability/Surface hat genau einen Status: `code-only`, `default build`, `release artifact`, `published`, `experimental`; Website, README und Coverage-Matrix verwenden dieselbe Quelle.
   - Beleg: 38 versus 42 MCP-Tools und widersprüchliche TUI-Claims (`docs/FEATURES.md:1-11`, `docs/coverage-matrix.md:72-117`, `docs/HOWTO.md:49-51`; Review `03`).
4. **Unbelegte Marketingclaims zurücknehmen oder reproduzierbar belegen (S).**
   - Abhängigkeiten: keine.
   - Abnahme: 99,4-%-/100-von-100-Claims sind entfernt oder mit eingechecktem Runner, Rohdaten, Scope und Reproduktionsanleitung verknüpft.
   - Beleg: `site/index.html:198-219`, `docs/release-scorecard.md:9-49`; Reviews `00`, `03`, `06`.

**Exit-Gate:** Es gibt keine aktive Distribution oder prominente Aussage mehr, die den aktuell belegten Zustand überzeichnet.

### Phase 1 — Trust Boundary und Lifecycle neu schneiden (Woche 1–8, XL)

Ziel: Lokale Dienste sind authentisiert, capability-begrenzt und sauber betreibbar.

1. **Capability-Ports und Manifest spezifizieren (M).**
   - Abhängigkeiten: Phase 0.
   - Abnahme: Kleine Ports mindestens für Query, Search, Analysis, Edit, Memory und Forge; jede Surface veröffentlicht ihr echtes Capability-Manifest; keine Capability-Parität durch `Unavailable`-Stubs.
   - Beleg: monolithischer Client und zahlreiche Stubs (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`; Reviews `01`, `04`).
2. **Gemeinsames Security-Envelope für HTTP/SSE/MCP-HTTP implementieren (L).**
   - Abhängigkeiten: Capability-Manifest.
   - Abnahme: zufälliger Bearer-Token, Constant-Time-Prüfung, Host-Allowlist, Origin default-deny, globale Body-Limits, passende Timeouts und negative Integrationstests; `/healthz` darf separat minimal öffentlich bleiben. MCP-HTTP ist read-only by default, Mutationen benötigen explizite Scopes.
   - Beleg: keine Auth-/Host-/Origin-Prüfung (`surfaces/http/server.go:181-212`, `surfaces/mcp/http.go:16-49`; Review `04`).
3. **Memory- und Export-Grenze härten (M).**
   - Abhängigkeiten: Capability-Manifest; parallel zu Security-Envelope möglich.
   - Abnahme: Remote-APIs akzeptieren keinen frei wählbaren Dateipfad; Export liefert Bytes oder schreibt nur in ein explizites, containment-geprüftes Ziel. Memory-Datei/Verzeichnis sind `0600`/`0700`; Secret-Policy ist `reject`, `redact` oder expliziter lokaler Override; Migrationstest für bestehende Rechte.
   - Beleg: `os.Create(req.ExportToPath)` und Memory `0644` (`surfaces/client/direct.go:658-677`, `engine/memory/memory.go:101-117`; Review `04`).
4. **Zentralen Runtime-/Composition-Root und beendbaren Lifecycle bauen (L).**
   - Abhängigkeiten: Capability-Ports.
   - Abnahme: Store, Ingest, Watcher und Surfaces werden genau einmal komponiert; `Done`, Signal-Context und begrenzter Shutdown existieren; `daemon stop` beendet den echten CLI-Prozess; HTTP drainiert kontrolliert; Deferred-Cleanups laufen in E2E-Tests.
   - Beleg: mehrfaches Wiring und Endlos-`select` (`cmd/graphi/main.go:1002-1248`; Reviews `01`, `05`).
5. **Autoritativen, SHA-gebundenen Release-DAG bauen (M).**
   - Abhängigkeiten: Phase 0; parallel zu 1–4 möglich.
   - Abnahme: Gate → Build → Reproducibility → Package → Publish ist ein DAG für exakt denselben Commit; Tag entsteht erst nach allen Gates; Third-Party-Actions sind SHA-gepinnt; SBOM und Provenance/Attestation sind Release-Artefakte.
   - Beleg: entkoppelte Workflows und Floating Tags (`.github/workflows/auto-release.yml:36-101`, `.github/workflows/release.yml:35-205`; Reviews `04`, `05`).

**Exit-Gate:** Negative Security-Tests, Daemon-/HTTP-Lifecycle-E2E und commit-gebundener Dry-Run-Release sind grün. Vorher keine GA-Freigabe für HTTP/MCP-HTTP.

### Phase 2 — Kernprodukt korrekt und surface-konsistent machen (Woche 6–12, L)

Ziel: Der enge Agenten-Kontext-Workflow liefert über unterstützte Surfaces dieselbe, verlustfreie Semantik.

1. **Gemeinsamen generierten Payload-Vertrag für Web und VS Code schaffen (M).**
   - Abhängigkeiten: Capability-Manifest.
   - Abnahme: kanonische Go-Fixtures/Schema generieren beide Clients; VS-Code-Search nutzt `node_id`, `qualified_name`, `source_path`; CI blockiert Drift.
   - Beleg: VS Code erwartet `{id,path,line}`, Server liefert andere Felder (`extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:18-35`; Review `02`).
2. **Web-Graph verlustfrei und race-sicher machen (M).**
   - Abhängigkeiten: gemeinsamer Vertrag.
   - Abnahme: Parallelkanten bleiben über eindeutige Edge-ID erhalten; nicht gemockter Graphology-Test; alte Requests können neue Zustände nicht überschreiben; keine Voll-Rebuilds allein durch Selection/Loading.
   - Beleg: Endpoint-Deduplizierung verwirft gültige Kanten (`web/src/GraphView.tsx:108-118`) und Fetches haben keinen Stale-Request-Schutz (`web/src/useGraph.ts:205-297`; Review `02`).
3. **GA-Capability-Set als Contract-Suite festschreiben (M).**
   - Abhängigkeiten: Capability-Ports, gemeinsamer Vertrag.
   - Abnahme: `index`, `search`, callers/callees/references, `agent_brief`, `related_files`, `explain_symbol`, `change_risk` besitzen reale Corpus-Fixtures, Failure-State-Tests und Byte-/Semantikparität für CLI und MCP-stdio.
   - Beleg: bestehende Parität ist wertvoll, deckt aber nicht alle Produktpfade ab (`surfaces/parity_test.go:1-5`; Reviews `01`, `03`).
4. **Task-first Onboarding vor Graph-first UI prototypisieren (M).**
   - Abhängigkeiten: stabiler GA-Contract.
   - Abnahme: Nutzer kann Task oder Symbol eingeben und erhält zuerst Brief/Related Files/Risk; Graph ist optionaler Evidenz-Drilldown; fünf moderierte Tests oder dokumentierte interne Dogfood-Sessions erfassen Time-to-first-value und Fehlstellen. Reale externe Nutzerzahl bleibt bis zur Durchführung UNKNOWN.
   - Beleg: aktuelle UI verlangt bereits einen Seed und präsentiert viele gleichrangige Panels (`web/src/GraphPage.tsx:144-255`; Review `03`).

**Exit-Gate:** Der Hero-Workflow ist auf CLI/MCP deterministisch, auf Web verlustfrei und mit ehrlichen Failure States demonstrierbar. VS Code bleibt experimental, bis Marketplace-/Nutzungspfad separat validiert ist.

### Phase 3 — Skalierung und reale Produktvalidierung (Woche 10–18, L–XL)

Ziel: Performance- und Nutzenversprechen werden an realen Aufgaben statt an Proxy-Scorecards bewiesen.

1. **Endpoint-selektiven GraphReader implementieren (L).**
   - Abhängigkeiten: stabiler GA-Contract; API-Spike zuerst.
   - Abnahme: `Incoming`/`Outgoing` und Batchvarianten nutzen SQLite-`from_id`/`to_id`-Indizes; Caller/Callee/Reference-Queries scannen keine vollständige Kantenklasse; RAM-, Latenz- und Ergebnisparitätstests gegen realistische Millionen-Kanten-Fixtures.
   - Beleg: aktuelle Query filtert alle Kanten einer Art im Service trotz vorhandener Indizes (`engine/query/service.go:145-173`, `core/graphstore/sqlite.go:187-192`; Reviews `00`, `01`).
2. **Spring-Boot-/Monorepo-Feldlauf vollständig wiederholen (M).**
   - Abhängigkeiten: selektive Reads; Ingest-Fixes müssen bereits im Ausgangsstand enthalten sein.
   - Abnahme: dokumentierte Hardware, Commit, Wall-clock, Peak-RAM, finale DB-Größe, Time-to-first-query und Signalqualität; Rohlogs/Runner eingecheckt. Ziele werden erst nach Baseline verbindlich festgelegt, weil aktuelle Post-Fix-Werte UNKNOWN sind.
   - Beleg: bisheriger Full-Index-Nachtest ist nur proxied (`docs/real-world-report.md:23-40`; Review `03`).
3. **Taskbasierte Evaluation mit/ohne Graphi bauen (L).**
   - Abhängigkeiten: GA-Contract, reproduzierbarer Corpus.
   - Abnahme: mindestens 20 repräsentative Coding-Aufgaben über mehrere reale Repositories; misst Task-Erfolg, Regressionen, Laufzeit, gelesene Dateien/Tokens und Fehlentscheidungen; Runner und Rohdaten sind reproduzierbar. Kein Marketingclaim vor bestandenem Review der Methodik.
   - Beleg: heutige Scorecard/Savings-Metrik misst überwiegend synthetische Gates oder Whole-File-Proxies (`docs/release-scorecard.md:9-49`, `docs/meter/metering.md:31-55`; Reviews `03`, `06`).
4. **Privacy-kompatiblen Validierungskanal etablieren (S–M).**
   - Abhängigkeiten: Threat Model und Produktstatus-Taxonomie.
   - Abnahme: explizites Opt-in oder manuelle Pilotmessung; keine Source-Pfade, Queries, Tokens oder Inhalte; dokumentiert Activation, wiederholte Nutzung, Workflow-Abschluss und Fehlerklasse.
   - Beleg: Adoption, Retention und Nutzung sind vollständig UNKNOWN (Reviews `03`, `06`).

**Exit-Gate:** Reale Performance und mindestens ein messbarer Nutzer-Outcome verbessern sich; andernfalls werden Claims und GA-Scope weiter reduziert, nicht neue Features ergänzt.

### Phase 4 — Distribution und Geschäftsvalidierung (frühestens ab Woche 18, L; konditional)

Ziel: Erst nach technischem Vertrauen prüfen, ob ein wiederholt genutzter Team-Workflow existiert.

1. **GitHub Action als echtes Consumer-Artefakt neu paketieren (M).**
   - Abhängigkeiten: Release-DAG, GA-Contract, Security-Scopes.
   - Abnahme: `graphi-version` bestimmt nachweislich Binary/Source; Checksums/Attestation werden geprüft; E2E läuft in einem fremden Minimal-Consumer-Repo; keine globalen `/tmp`-Kollisionen; stabile JSON-Ausgabe ohne `grep`/`sed`-Parsing.
   - Beleg: Action baut derzeit `./cmd/graphi` im Consumer-Workspace (`extensions/github-action/action.yml:113-125`; Review `05`).
2. **Design-Partner-Discovery und manuelles Pilotangebot (M, überwiegend Produktarbeit).**
   - Abhängigkeiten: funktionierende Action oder klarer lokaler Team-Workflow.
   - Abnahme: ICP-Hypothese, Buyer, akuter Job, Erfolgskriterien, Supportumfang und Preisexperiment dokumentiert; mindestens ein Partner nutzt den Workflow wiederholt und bezahlt oder gibt eine konkrete, bedingte Kaufzusage. Bis dahin Zahlungsbereitschaft **UNKNOWN**.
   - Beleg: kein ICP, Preis, Funnel, Umsatz oder Zahlungsnachweis im Repository (`site/index.html:35-46`, `site/index.html:583-611`; Review `06`).
3. **Go/No-Go für Teamprodukt entscheiden (S).**
   - Abhängigkeiten: Pilotresultate.
   - Abnahme: dokumentierte Entscheidung anhand Nutzung, Outcome, Supportkosten und Zahlungsbereitschaft. Bei No-Go bleibt Graphi fokussiertes OSS-Core-Produkt; bei Go folgt erst dann eine Roadmap für Policy, Audit, SSO/RBAC oder SLA. Welche Paid-Fähigkeit wertvoll ist, bleibt bis zur Discovery UNKNOWN.

## Zuerst zu bauen oder zu validieren

In strikter Reihenfolge:

1. Release-/Claim-Freeze und fail-closed Edit-Funktionen.
2. Capability-Manifest als Voraussetzung für ehrliche Surfaces und Security-Scopes.
3. HTTP/MCP-HTTP-Security plus sichere Memory-/Exportgrenze.
4. Beendbarer Runtime-Lifecycle und commit-gebundener Release-DAG.
5. Gemeinsamer Clientvertrag und Korrektur der bereits belegten UI-/VS-Code-Fehler.
6. GA-Workflow-Contract für CLI/MCP.
7. Endpoint-selektive Reads und reale Monorepo-Messung.
8. Taskbasierte Nutzer-Outcome-Evaluation.
9. Erst danach Action-Distribution und Geschäftsvalidierung.

Die Reihenfolge schützt zunächst Daten und Nutzervertrauen, stabilisiert dann den engen Wertpfad und investiert erst anschließend in Skalierung und Distribution.

## Einzufrierende oder zu löschende Features

### Sofort aus GA/Marketing entfernen oder deaktivieren

- `extract` und `move`, bis echte semantische Planner existieren.
- `safe-delete` als „safe“-Versprechen, solange nur eine Deklarationszeile entfernt wird; entweder umbenennen, fail-closed machen oder Semantik vollständig implementieren (`readme.md:551-553`; Review `03`).
- GitHub Action, VS-Code-Suche und MCP-HTTP als „shipped/production-ready“, bis ihre jeweiligen Gates grün sind.
- Nicht reproduzierbare 99,4-%-, Savings- und 100/100-Produktclaims.

### Bis nach Phase 3 einfrieren

- neue Sprachen, Analyzer, MCP-Tools, CLI-Kommandos und Surfaces;
- PR-Triage/-Konflikte/-Reviewer/-Critique;
- Memory, Distill und Skillgen außerhalb lokaler Labs;
- graph-aware Edit/Undo/Inline/Safe-delete als GA;
- Taint/Security als pauschale „all clear“-Funktion; nur mit klarer Granularität, Sprachscope und `unknown/partial`;
- Wiki/TUI/Graphvisualisierung als primäres Produktnarrativ;
- semantische Suche, neue Embedder und breite CGo-Varianten, sofern sie nicht direkt für die GA-Evaluation nötig sind.

### Nicht beginnen

- Hosted SaaS, Accounts, Billing, Entitlements oder Enterprise-Control-Plane;
- organisationsweite SSO/RBAC-/Policy-Funktionen ohne Design-Partner-Beleg;
- weitere Marketing-/Marketplace-Expansion ohne verifizierten Installations- und Supportpfad.

Code muss nicht sofort physisch gelöscht werden. Zuerst werden Registrierung, Dokumentation und Distribution entfernt; nach zwei Roadmap-Zyklen ohne belegte Nutzung folgt eine eigene Löschentscheidung. Reale Nutzung dieser Features ist derzeit **UNKNOWN**.

## Hauptrisiken und Gegenmaßnahmen

| Risiko | Wahrscheinlichkeit / Wirkung | Gegenmaßnahme |
|---|---|---|
| Teil-Rewrite wächst zum Big Bang | mittel / hoch | Strangler-Migration pro Port/Surface; alte und neue Pfade nur befristet parallel; pro Phase Exit-Gate. |
| Security-Härtung bricht lokale UX | mittel / hoch | sicheren Token-Handshake zuerst als Spike; CLI/MCP-stdio unverändert halten; negative und Happy-Path-E2E. |
| Capability-Split erzeugt lange Adaptermigration | hoch / mittel | mit GA-Ports beginnen; experimentelle Ports nicht sofort migrieren, sondern deaktivieren. |
| Performancefix ändert Ergebnisreihenfolge/Provenienz | mittel / hoch | Golden-/Byte-Parität gegen bestehende Reader plus deterministische Sortierung. |
| Marketing-/Feature-Reduktion senkt kurzfristig Sichtbarkeit | hoch / mittel | Status offen kommunizieren; Trust und reproduzierbare Evidenz als Produktwert behandeln. |
| Reale Evaluation zeigt keinen relevanten Outcome | mittel / hoch, tatsächliche Wahrscheinlichkeit UNKNOWN | vorab Stop-Kriterien; Scope weiter reduzieren oder Projekt als OSS-Infrastruktur positionieren. |
| Maintainer-/Review-Kapazität reicht nicht | UNKNOWN / hoch | Phase 0 zuerst; XL-Arbeit nur nach Personalkapazitäts- und Ownership-Zuordnung starten. |
| Cross-Platform-Lifecycle bleibt lückenhaft | mittel / hoch | Windows-CI und echter Windows-Daemon-Pfad vor entsprechender Supportaussage; aktueller Status UNKNOWN. |
| Privacy-at-rest des Graphstores bleibt ungeklärt | UNKNOWN / hoch | Datenklassifikation, Retention und Threat Model in Phase 1; keine pauschale Private-Repo-Sicherheitsaussage vorher. |
| Team-/Paid-Hypothese wird aus Technik statt Nachfrage abgeleitet | hoch / hoch | Phase 4 strikt konditional; keine Control-Plane vor wiederholter Nutzung/Kaufzusage. |

## Konkrete nächste Tasks

| ID | Task | Aufwand | Abhängigkeiten | Abnahmekriterien |
|---|---|---:|---|---|
| N1 | Release-Freeze und Claim-Inventar | S | keine | Auto-Publish deaktiviert; jede problematische Aussage hat Owner und Status; Action nicht als GA beworben. |
| N2 | `extract`/`move` fail-closed | S | keine | keine Mutation; stabiler Fehler über alle registrierten Surfaces; Regressionstest. |
| N3 | Capability-RFC mit GA-Manifest | M | N1 | kleine Ports, Capability-Discovery, Read-/Write-Scope und Migrationsreihenfolge akzeptiert; GA-Set explizit. |
| N4 | HTTP-/MCP-HTTP-Threat-Model und Security-Envelope-Spike | M | N3 | Angreifermodell, Tokenübergabe, Host/Origin, Scopes, Limits und SSE-Timeoutstrategie durch lauffähigen Spike plus negative Tests belegt. |
| N5 | Memory-Rechte und pfadloser Remote-Export | M | N3 | `0600`/`0700`, Migrationstest, Secret-Policy, kein Remote-Arbitrary-Write. |
| N6 | Runtime `Done`/Shutdown und Daemon-E2E | M–L | N3 | echter gestarteter Prozess endet nach `daemon stop`; Store/Watcher geschlossen; SIGTERM- und HTTP-Drain-Test. |
| N7 | Autoritativer Release-DAG | M | N1 | Publish nur für exakt gegatetes SHA; Dry Run erzeugt Checksums, SBOM und Attestation; keine Floating Action-Tags. |
| N8 | Gemeinsames TS-Payload-Schema | M | N3 | Go-Fixture generiert Web/VS-Code; Search-Contracttest grün; Driftcheck required. |
| N9 | Multi-Edge-/Stale-Request-Fixes | M | N8 | keine verlorenen Parallelkanten; alte Responses überschreiben keinen neuen State; nicht gemockter Test. |
| N10 | GA-Workflow-Corpus und Failure-State-Suite | M | N3, N8 | alle GA-Jobs über CLI/MCP mit deterministischen Evidence-/Confidence-/Failure-Ergebnissen. |
| N11 | Endpoint-selektiver Reader-Spike | M | N10 | API, Queryplan und Ergebnisparität auf repräsentativer Fixture bewiesen; belastbare L-Schätzung. |
| N12 | Reale Monorepo- und 20-Task-Evaluation | L | N10, N11 Implementierung | reproduzierbare Rohdaten für Performance und Nutzer-Outcome; Claims daraus ableitbar oder explizit verworfen. |

## Review- und Codebelege

Die Roadmap basiert auf allen sieben Reviews in `temp/review/`:

- `00_overall_project_review.md`: Gesamturteil Teil-Rewrite, Top-Risiken und Erhalt/Neuschnitt.
- `01_architecture_expert.md`: selektive Reads, Capability-Segregation, Composition Root.
- `02_senior_fullstack_engineer.md`: VS-Code-Contractdrift, Parallelkanten, Stale Requests, falsche Refactor-Semantik.
- `03_product_feature_expert.md`: Agenten-Kernworkflow, Scope-Freeze, reale Outcome- und Monorepo-Validierung.
- `04_security_privacy_expert.md`: Auth/Host/Origin, MCP-Scopes, Memory/Export, Body-/Timeout-Grenzen.
- `05_devops_production_readiness_expert.md`: Release-DAG, Daemon-/HTTP-Lifecycle, Consumer-Action.
- `06_business_monetization_expert.md`: fehlende ICP-/Adoption-/Zahlungsbelege und konditionale Team-/CI-Hypothese.

Kritische Aussagen wurden zusätzlich read-only im aktuellen Code bestätigt:

- Vollscan bei gerichteter Nachbarschaft: `engine/query/service.go:145-173`.
- HTTP-Routen ohne sichtbare Auth-/Host-/Origin-Middleware: `surfaces/http/server.go:181-212`.
- Daemon-Prozess blockiert nach Start in `select {}`: `cmd/graphi/main.go:1138-1145`.
- Action baut `./cmd/graphi`, während `GRAPHI_VERSION` nur als Variable gesetzt wird: `extensions/github-action/action.yml:113-125`.
- `extract`/`move` nutzen denselben Name-Rewrite-Planer: `engine/edit/refactor.go:174-186`.

## Explizite UNKNOWNs und Entscheidungsregeln

Folgendes ist aus Repository und Reviews nicht belastbar bekannt:

- aktive Nutzer, Installationen, Downloads, Retention und produktive Organisationen;
- ICP, Buyer, Zahlungsbereitschaft, Umsatz, Supportkosten und Sales Cycle;
- tatsächliche Marketplace-/Package-Manager-Veröffentlichung und externe Action-Nutzung;
- Post-Fix-Monorepo-Wallclock, Peak-RAM, DB-Größe und Time-to-first-query;
- taskbasierte Accuracy/Outcome-Verbesserung durch Graphi;
- Qualität und Nutzungstiefe je beworbener Sprache;
- Maintainer-Kapazität, SLOs, Incident-Historie, Branch Protection und externe Security-Scans;
- Cross-Platform-Daemon-Verhalten, insbesondere Windows;
- angemessene Datenklassifikation, Retention und At-rest-Verschlüsselung für Graphdaten.

Diese UNKNOWNs werden nicht durch Annahmen ersetzt. Die Entscheidungsregeln sind:

1. Kein neuer Feature-Scope vor grünen Phase-1- und Phase-2-Exit-Gates.
2. Keine Performance- oder ROI-Claims ohne reproduzierbare Phase-3-Daten.
3. Keine Paid-/Enterprise-Entwicklung ohne wiederholte Nutzung und Kaufbeleg aus Phase 4.
4. Scheitert der enge GA-Workflow an Outcome oder Betrieb, wird Scope reduziert; der Core wird nicht vorschnell neu geschrieben.

## Erfolgsdefinition

Die Roadmap ist erfolgreich, wenn Graphi nicht mehr durch Featurezahl, sondern durch einen kleinen, glaubwürdigen Vertrag definiert ist:

- lokale CLI/MCP-Nutzung ist sicher, deterministisch und beendbar;
- der Agenten-Kontext-Workflow liefert nachweislich korrekte, provenance-tragende Antworten;
- große Repositories werden ohne Vollscan-Hotpath und mit veröffentlichten Realmessungen bedient;
- jede Surface und jeder Claim hat einen überprüfbaren Status;
- zusätzliche Distribution oder Monetarisierung beginnt nur nach belegter Nutzung.

Bis dahin lautet die Produktposition ehrlich: **technisch substanzieller Local-first-Core in fokussierter Stabilisierung, nicht allgemeine produktionsreife Code-Intelligence-Plattform.**
