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

# Technischer Rebuild-Plan: Graphi

> **Status: SUPERSEDED PLANNING INPUT.** Technische Begründungen bleiben Referenz. Task-IDs, Reihenfolge, Aufwand und Gates wurden durch `00_master_execution_plan.md` ersetzt.

## Entscheidung

**Teil-Rewrite der Systemgrenzen; kein Full Rewrite des Kerns.**

Graphi besitzt einen erhaltenswerten, deterministischen Kern: unveränderliche Kanten mit Provenienz-Invarianten, registrierbare Parser, ein expliziter Graphstore-Vertrag und reproduzierbare Persistenz-/Build-Grundlagen (`core/model/edge.go:48-113`, `core/parse/registry.go:10-100`, `core/graphstore/graphstore.go:55-146`, `internal/release/build.go:146-199`). Ein Full Rewrite würde genau die schwer zu reproduzierende Korrektheit vernichten, ohne die belegten Probleme an den Produktgrenzen automatisch zu lösen.

Ein gewöhnlicher lokaler Refactor reicht ebenfalls nicht. Vier Systemverträge sind strukturell falsch und müssen neu definiert werden:

1. **Capabilities und Transporte:** Ein 28+ Methoden breites `Client`-Interface vermischt Reads, Writes, Memory und Forge. Remote-Adapter erfüllen es vielfach nur durch `Unavailable`-Stubs (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`, `surfaces/daemon/client.go:343-382`).
2. **Trust Boundary:** HTTP registriert sensible Routen ohne Authentisierung sowie ohne Host-/Origin-Policy; der Client sendet einen Token, den der Server nicht prüft (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`, `extensions/vscode/src/graphiClient.ts:138-156`).
3. **Read-Hotpath:** Caller-/Callee-Abfragen laden eine vollständige Kantenklasse und filtern im Service, obwohl SQLite Endpoint-Indizes besitzt (`engine/query/service.go:145-173`, `core/graphstore/sqlite.go:187-192`, `core/graphstore/sqlite.go:841-866`).
4. **Lifecycle und Distribution:** Der Daemon-Prozess bleibt nach `stop` in `select {}` hängen; Release-Publikation ist nicht an das Release-Gate gebunden; die Action baut aus dem Consumer-Workspace statt aus der gewählten Graphi-Version (`cmd/graphi/main.go:1138-1145`, `.github/workflows/auto-release.yml:36-101`, `extensions/github-action/action.yml:101-125`).

Die richtige Strategie ist daher ein **inkrementeller Teil-Rewrite hinter Contract- und End-to-End-Gates**. Core-Modell, Parser und bewährte Analysealgorithmen werden migriert, nicht neu erfunden. Alte und neue Grenzen dürfen vorübergehend parallel existieren, aber jede Phase braucht eine zeitlich begrenzte Compatibility Bridge und ein festes Löschkriterium.

## Zielbild

```text
cmd/graphi (Argumente, Exit Codes, Signal-Context)
        |
        v
app.Runtime / Composition Root
  - owns Store, Ingest, Watch, Services, lifecycle
  - explicit profiles: cold-cli | daemon | http-readonly | editor
  - Done(), Ready(), Shutdown(ctx), Capabilities()
        |
        +-------------------+-------------------+
        v                   v                   v
  QueryPort/SearchPort  Analysis/AgentPorts  explicit WritePorts
        |                   |               (Edit, Memory, Forge)
        +-------------------+-------------------+
                            |
                  engine services / core
                            |
          GraphReader: Incoming/Outgoing/NodesByID
                            |
           SQLite indexed reads | Mem adjacency index

Transporte:
  CLI/stdio -> kleinster benötigter Port
  Daemon    -> Capability-Manifest + versionierte RPCs
  HTTP/SSE  -> Security Envelope -> read-only default
  MCP-HTTP  -> aus oder auth + explizite Write-Scopes

Clients:
  kanonisches Schema -> generierter TypeScript Client
                       -> Web und VS Code

Release:
  exact SHA -> gate -> build/test -> SBOM/attestation -> tag/publish
```

### Architekturinvarianten des Zielbilds

- Eine Surface hängt nur von den Ports ab, die sie tatsächlich anbietet. Fehlende Fähigkeiten sind im Manifest nicht vorhanden und werden nicht erst bei Aufruf durch Sentinel Errors entdeckt.
- Read- und Write-Capabilities sind getrennt. HTTP und MCP-HTTP starten read-only; jede Mutation benötigt expliziten Scope und Authentisierung.
- Der Prozess-Lifecycle hat genau einen Owner. Store, Watcher, Listener und Ingest werden in umgekehrter Erzeugungsreihenfolge begrenzt beendet.
- Nachbarschaftsabfragen sind proportional zum Knotengrad, nicht zur Größe der Kantenklasse.
- Web und VS Code konsumieren denselben generierten Payload-Vertrag; reale Go-Antworten sind Contract-Fixtures.
- Nur der exakt gegatete Commit darf publiziert werden. Tag, Artefakte, SBOM und Provenance referenzieren denselben SHA.
- Reale Repositories und Nutzer-Journeys sind Release-Evidenz; synthetische Scorecards bleiben Regressionsevidenz und werden entsprechend benannt.

## Was erhalten bleibt

- `core/model` einschließlich deterministischer IDs, Confidence und Provenienz (`core/model/edge.go:48-135`).
- Parser-/Analyzer-Registries und deren deterministischer Dispatch (`core/parse/registry.go:10-100`, `engine/analysis/dispatch.go:42-59`).
- Graphstore-Durability, SQLite-Schema und Migrationen als Ausgangspunkt; keine voreilige Datenformat-Neuerfindung (`core/graphstore/sqlite.go:107-206`).
- Query-/Analyse-Kernels, sofern Corpus- und Contract-Gates ihre Semantik belegen.
- Local-first-/Zero-egress-Guard, Fehlerbereinigung, atomare Writes und bestehende Conformance-Tests (`surfaces/guard/guard.go:25-83`, `surfaces/http/server.go:163-178`, `engine/edit/write.go:9-56`).
- Reproduzierbare Build-Utilities, Checksummen und Cross-Platform-Matrix (`internal/release/build.go:96-107`, `internal/release/build.go:146-199`).

## Was sofort eingefroren, deaktiviert oder gelöscht wird

### Sofort einfrieren

- Keine neuen Sprachen, Analyzer, MCP-Tools, CLI-Verben oder Surfaces bis Abschluss der Phasen 0–4.
- PR-Suite, Memory, Distill, Skillgen, Security-Vertikalen und graph-aware Edits bleiben experimentell und werden nicht als GA-/Release-Capability gezählt.
- Keine SaaS-, Billing-, RBAC- oder Enterprise-Control-Plane-Arbeit. Nachfrage und Wertzaun sind nicht belegt.
- Keine automatische Release-Publikation und keine externe Bewerbung der GitHub Action, bis die jeweiligen Blocker geschlossen sind.

### Sofort deaktivieren oder fail-closed schalten

- `extract` und `move`: aus öffentlichen Deskriptoren entfernen oder mit `not implemented` ablehnen. Beide laufen aktuell als Namensrewrite; `DestinationFile` bleibt ungenutzt (`engine/edit/refactor.go:174-186`, `engine/edit/refactor.go:189-208`).
- MCP-HTTP: nicht als Produktionssurface anbieten, bis Security Envelope, read-only Default und Scope-Tests existieren (`surfaces/mcp/http.go:16-49`, `surfaces/mcp/mcp.go:230-260`).
- Memory-Dateiexport über Remote-/MCP-/HTTP-Pfade: entfernen; Remote liefert Bytes, nur eine lokale CLI-Operatoraktion darf in Dateien schreiben (`surfaces/client/direct.go:658-677`).
- GitHub Action: aus „shipped“ entfernen, bis ein Consumer-Repo-E2E-Test beweist, dass `graphi-version` tatsächlich die Runtime auswählt (`extensions/github-action/action.yml:113-125`).

### Nach Migration löschen

- Das monolithische `surfaces/client.Client` und alle dazugehörigen `Unavailable`-Stub-Methoden, sobald alle stabilen Surfaces auf kleine Ports umgestellt sind.
- Doppelte Composition-Pfade in `makeClient`, `makeEditorClient`, Daemon und HTTP nach Übernahme durch `app.Runtime` (`cmd/graphi/main.go:181-188`, `cmd/graphi/main.go:1002-1046`, `cmd/graphi/main.go:1111-1138`, `cmd/graphi/main.go:1195-1248`).
- Manuell gepflegte VS-Code-Payloadtypen nach Einführung des gemeinsamen Generators (`extensions/vscode/src/contract.ts:1-8`).
- Entkoppelte Auto-Release-Orchestrierung nach Einführung eines einzigen Commit-gebundenen Release-DAGs (`.github/workflows/auto-release.yml:36-101`).
- Alte Compatibility Bridges spätestens eine Minor-Version nach erfolgreicher Migration; jede Bridge erhält Owner, Ablaufdatum und Löschtest.

## Aufwandsskala und Planungsannahmen

Referenzteam: **4 Personen** – zwei Senior Go/Platform Engineers, ein Senior Fullstack Engineer, ein Security/DevOps Engineer; Product/Design unterstützt punktuell. Zeiten sind Kalenderzeiten bei 70–80 % Fokus inklusive Tests, Review und Dokumentation.

| Größe | Teamaufwand | Typische Kalenderzeit | Bedeutung |
|---|---:|---:|---|
| S | 2–5 Personentage | 1–3 Tage | lokaler Fix mit vorhandener Seam und engem Testumfang |
| M | 6–15 Personentage | 1–2 Wochen | paketübergreifender Refactor mit Contract-/Integrationstests |
| L | 16–35 Personentage | 2–5 Wochen | neuer Systemvertrag, Migration mehrerer Consumer, E2E-Gates |
| XL | 36–70 Personentage | 5–9 Wochen | mehrere gekoppelte Systemgrenzen oder Daten-/Release-Migration |

Gesamtschätzung für die technische Stabilisierung bis zu einem glaubwürdigen Release Candidate: **XL, etwa 55–70 Personentage bzw. 8–10 Kalenderwochen** mit dem Referenzteam. Produktvalidierung und spätere echte Implementierung von `extract`/`move` sind nicht enthalten.

## Priorisierter Schritt-für-Schritt-Plan

### Phase 0 — Containment und belastbare Baseline (Woche 1, M)

1. Release-Automatik deaktivieren bzw. publikationsseitig sperren; Action aus öffentlichen „shipped“-Claims nehmen.
2. `extract`/`move` fail-closed schalten; Remote-Memory-Export und MCP-HTTP-Mutationen deaktivieren.
3. Eine maschinenlesbare Surface-Matrix einführen: `code`, `default build`, `release artifact`, `published`, `experimental`.
4. Regressionstests für die bestätigten Defekte zuerst rot einchecken: Daemon-Prozessende, unauthentisierte HTTP-Anfrage, VS-Code-Suchpayload, Parallelkanten, Action im Consumer-Repo.
5. Baseline auf realen Repositories erfassen: Full Index, Peak-RAM, DB-Größe, Time-to-first-query und fünf Kernantworten mit erwarteter Evidenz.

**Exit:** Keine bekannte gefährliche Funktion behauptet Erfolg; kein ungeprüfter Commit kann automatisch publiziert werden; alle kritischen Defekte besitzen einen reproduzierbaren roten Test.

### Phase 1 — Trust Boundary und sichere Defaults (Woche 1–3, L)

1. Ein gemeinsames `SecurityEnvelope` für HTTP, SSE und MCP-HTTP bauen: zufälliger per-process Token, constant-time Bearer-Prüfung, Host-Allowlist, Origin default-deny, Request-ID und sanitizierte Fehler.
2. Routen klassifizieren: tokenlose Liveness; authentisierte Read-Routen; separat gescopte Write-Routen. HTTP/MCP-HTTP bleiben standardmäßig read-only.
3. Globale Body-Limits und Decoder mit `DisallowUnknownFields`; `ReadTimeout`, `IdleTimeout` und route-spezifische Strategie für SSE ergänzen (`surfaces/http/server.go:255-267`, `surfaces/http/server.go:698-745`).
4. Memory härten: Parent `0700`, Datei `0600`, Migration bestehender Modi, Secret-Policy `reject` als Default und expliziter lokaler Override (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`).
5. Exportpfad aus dem Transportvertrag entfernen. Optionaler CLI-Export schreibt empfangene Bytes mit sicherer Pfad-/Symlink-Policy.
6. Konfigurierbare GitHub-API-Basis auf saubere HTTPS-Origin validieren; Cross-Origin-Redirects mit Authorization blockieren.

**Exit:** Negative Integrationstests beweisen 401/403 für fehlende/falsche Tokens, Host-/Origin-Abweisung, Body-Limits und fehlende Write-Scopes. Kein Remote-Request kann einen frei wählbaren Pfad schreiben. Security-Review gibt die Surface frei.

### Phase 2 — Lifecycle und ein Composition Root (Woche 2–4, L)

1. `app.Runtime` als einzigen Owner von Store, Ingest, Watcher und Services einführen; Profile sind explizit und validiert.
2. `Done()`, `Ready()`, `Shutdown(ctx)` und idempotentes Close definieren. `signal.NotifyContext` gehört in den CLI-Entrypoint, nicht in Engine-Pakete.
3. Daemon-`stop` schließt `Done`; der gestartete CLI-Prozess verlässt den Block und führt Deferred-Cleanups aus (`cmd/graphi/main.go:1100-1145`, `surfaces/daemon/daemon.go:170-186`).
4. HTTP über einen besessenen `http.Server` starten und bei SIGINT/SIGTERM begrenzt drainen; SSE-Verbindungen werden gezielt beendet (`cmd/graphi/main.go:1237-1252`).
5. `/healthz` als Liveness behalten, `/readyz` für Store, Initial-Ingest und kritischen Watcher-Status ergänzen (`surfaces/http/server.go:315-317`).
6. Bestehende Wiring-Pfade schrittweise auf Runtime migrieren und danach löschen.

**Exit:** Subprocess-E2E beweist Start → Ready → Query → Stop → Prozessende innerhalb fünf Sekunden; SIGTERM beendet HTTP und Daemon sauber; Race-/Leak-Tests bleiben grün; jede Ressource hat genau einen Owner.

### Phase 3 — Endpoint-selektive Read-Architektur (Woche 3–5, L)

1. Kleinen `GraphReader` definieren: `Node`, `NodesByID`, `Incoming`, `Outgoing`, jeweils nach Kind filterbar und kanonisch sortiert.
2. SQLite-Implementierung direkt über `from_id`/`to_id` und gebundene Parameter führen; vorhandene Indizes nutzen (`core/graphstore/sqlite.go:187-192`).
3. MemStore erhält Adjazenzindizes mit identischer Determinismussemantik. Das bisherige `Edges(Query)` bleibt nur als zeitlich begrenzte Listing-API.
4. Query-, Impact- und Agent-Hotpaths migrieren; keine neue Analyse darf vollständige Kantenklassen für einfache Nachbarschaftsabfragen scannen.
5. Cache-Strategie messen: Vollgraph-Cache nicht sofort entfernen, sondern nach realem RAM-/Latenzvergleich auf Nodes/Hotsets begrenzen.
6. Benchmarks mit Millionen-Kanten-Fixture und mindestens einem realen Monorepo als Gate festschreiben.

**Exit:** Query-Plan bzw. instrumentierter Test beweist Endpoint-Index-Nutzung; Latenz skaliert mit Degree statt `E_kind`; kanonische Bytes bleiben gegenüber bestehenden Fixtures gleich; Peak-RAM und P95 liegen innerhalb vorab definierter Budgets.

### Phase 4 — Capability-Segregation und ehrliche Surfaces (Woche 4–7, XL)

1. Kleine Ports definieren: `QueryPort`, `SearchPort`, `AnalysisPort`, `AgentPort`, `EditPort`, `MemoryPort`, `ForgePort`, `DiagnosticsPort`.
2. Versioniertes Capability-Manifest mit Read/Write-Klasse, Scope und Contract-Version einführen.
3. CLI-Kommandos verlangen den kleinsten Port; Runtime-Profile liefern nur valide Portkombinationen.
4. Daemon/HTTP/MCP implementieren ausschließlich deklarierte Fähigkeiten. Nicht vorhandene Capabilities fehlen im Manifest und in Deskriptoren statt als Runtime-Stub aufzutauchen.
5. Alten `Client` über dünne, zeitlich begrenzte Adapter weiterbetreiben; Consumer vertikal migrieren: Query/Search → Agent → Analysis → optionale Writes.
6. Byte-Parität nur für gemeinsam unterstützte Capabilities prüfen; Capability-Parität nicht mehr behaupten.
7. Alten Client und Stub-Code löschen, sobald Repository-Suche keinen stabilen Consumer mehr findet.

**Exit:** Kein stabiler Remote-Adapter enthält `Unavailable`-Methoden nur zur Interface-Erfüllung; jede Surface besteht Manifest-vs-Routes/Tools-Tests; Read-only-Profile können Write-Ports weder kompilieren noch registrieren.

### Phase 5 — Ein kanonischer Web-/VS-Code-Vertrag und UI-Korrektheit (Woche 5–7, L)

1. Kanonische JSON-Schemas für Envelope **und Payloads** aus Go-Verträgen erzeugen; TypeScript-Typen und Runtime-Validatoren für Web und VS Code daraus generieren.
2. Echte Go-Response-Fixtures als Cross-Language-Contracttests verwenden. Der Search-Fix muss `node_id`, `qualified_name`, `source_path`, `line` und `column` abdecken (`engine/search/service.go:18-35`, `extensions/vscode/src/contract.ts:104-107`).
3. Graphology als Multi-Graph betreiben oder Parallelkanten explizit aggregieren; niemals über Endpoint-Paar deduplizieren (`web/src/GraphView.tsx:108-118`, `core/model/edge.go:76-80`).
4. Interaktive Requests über `AbortController` oder Sequenznummern vor stale State-Writes schützen (`web/src/useGraph.ts:205-297`, `web/src/GraphPage.tsx:63-75`).
5. Graph-Rebuild-Dependencies auf Nodes/Edges/Layout reduzieren; nicht jeder State-/Callback-Wechsel darf O(V+E) auslösen (`web/src/GraphView.tsx:96-205`).
6. VS-Code Typecheck, Lint, Tests und Contract-Drift in den autoritativen CI-Gate aufnehmen.

**Exit:** Ein Server-Fixture kann unverändert durch beide Clients gelesen werden; zwei verschiedenartige Parallelkanten bleiben anklickbar und behalten Provenienz; stale Response-Tests und CI-Driftcheck sind grün.

### Phase 6 — Release-, Action- und Supply-Chain-Rebuild (Woche 6–8, L)

1. Einen Commit-gebundenen Release-DAG bauen: Gate → Build/Test → Reproducibility → SBOM/Vulnerability Scan → Attestation/Signatur → Tag/Publish.
2. Der Tag entsteht erst nach allen Gates; jeder Job verwendet denselben unveränderlichen SHA. Die separate `workflow_run`-Kopplung wird entfernt (`.github/workflows/auto-release.yml:36-101`, `.github/workflows/release-gate.yml:1-30`).
3. Third-Party Actions auf Full SHA pinnen; Attestations-/Signaturprüfung in den High-Trust-Installerpfad integrieren.
4. GitHub Action lädt ein versioniertes Release-Artefakt oder checkt Graphi explizit in ein separates Verzeichnis aus. `graphi-version` muss die Source-/Binary-Selektion bestimmen.
5. Consumer-Repo-E2E: fremdes Repository ohne `cmd/graphi`, gepinnte Version, Working Directory, PR-Outputs, Offline-/Failure-Modi.
6. Windows-Build und unterstützte Lifecycle-Pfade in echter Windows-CI prüfen; nicht unterstützte Daemon-Funktion explizit deklarieren.

**Exit:** Ein absichtlich rotes Release-Gate verhindert Tag und Release; Artefakt-SHA ist auf den gegateten Commit zurückführbar; Consumer-E2E beweist die angeforderte Graphi-Version; Installer verifiziert Herkunft, nicht nur Transferintegrität.

### Phase 7 — Ingest entkoppeln und Crash-Korrektheit beweisen (nach RC, XL)

1. Erst nach Stabilisierung der Grenzen `Ingester` entlang bestehender Phasen schneiden: Scanner, ParsePipeline, GraphCommitter, LinkPhase, TaintPhase, ProgressReporter, RecoveryJournal (`engine/ingest/ingest.go:128-192`, `engine/ingest/ingest.go:607-809`).
2. Meta-Sidecar und Graphstore als explizite Saga modellieren; Checkpoint-/Dirty-State-Vertrag dokumentieren.
3. Fault-Injection an jedem Commit-/Crash-Punkt; Neustart muss entweder den alten konsistenten Zustand oder den vollständig neuen Zustand herstellen.
4. `MetaDB()`-Escape-Hatch durch fachliche Read-/Write-Ports ersetzen (`engine/ingest/ingest.go:581-589`).
5. Layerguard um deklarierte Same-Layer-Paketregeln erweitern (`internal/layerguard/guard.go:74-85`, `internal/layerguard/guard.go:133-147`).

**Exit:** Fehlerpunktmatrix ist vollständig grün; kein Sibling-Paket benötigt den rohen Meta-DB-Handle; Ingest-Phasen sind einzeln testbar; Architekturregeln erkennen unzulässige Same-Layer-Kopplung.

## Was zuerst gebaut wird

Der erste produktive Baustein ist **nicht** eine neue Capability, sondern ein vertikaler „Secure Read Slice“:

1. `app.Runtime` mit Query/Search-Profil und sauberem Lifecycle.
2. Endpoint-selektiver `GraphReader` für Caller/Callee.
3. HTTP Security Envelope mit read-only Capability-Manifest.
4. Generierter Search-Vertrag für Web und VS Code.
5. Commit-gebundener Gate-Job für genau diesen Slice.

Dieser Slice beantwortet eine Kernfrage (`callers`/`callees`/`search`) schnell, authentisiert und identisch über CLI, Daemon, HTTP, Web und VS Code. Er erzwingt früh die neuen Architekturverträge, ohne experimentelle Writes oder breite Analyzer mitzuschleppen.

## Konkrete nächste Tasks

| ID | Task | Größe | Abhängigkeit | Abnahmekriterien |
|---|---|---:|---|---|
| T0 | Release-Publish sperren und Action/`extract`/`move`/MCP-HTTP als nicht GA markieren bzw. fail-closed schalten | S | keine | Kein automatischer Publish; nicht implementierte Refactors mutieren keine Datei; Surface-Matrix zeigt den realen Status. |
| T1 | Kritische rote Regressionstests anlegen | M | T0 | Reproduzierbar rot für Daemon-Prozessende, HTTP ohne Auth, VS-Code-Search, Parallelkanten und Consumer-Action; Testnamen dokumentieren den jeweiligen Vertrag. |
| T2 | `SecurityEnvelope` und Routenklassifikation implementieren | L | T1 | Token-, Host-, Origin-, Body-Limit- und Scope-Negativtests grün; `/healthz` ist die einzige bewusst öffentliche Route. |
| T3 | Remote-Memory-Export entfernen und Memory-Dateirechte/Secret-Policy migrieren | M | T1 | Kein Transport akzeptiert einen freien Ausgabepfad; neue und bestehende Memory-Dateien sind `0600`; Secret-Suspect wird defaultmäßig nicht persistiert. |
| T4 | `app.Runtime` mit `Done/Ready/Shutdown` einführen | L | T1 | Subprocess-Test beendet Daemon nach Stop; SIGTERM drainiert HTTP; Store/Watcher schließen einmalig. |
| T5 | `GraphReader.Incoming/Outgoing/NodesByID` plus SQLite-/Mem-Implementierung | L | T1 | Indexnutzungs- und Determinismustests grün; Query-Service nutzt nicht mehr `Edges(kind)` für direkte Traversals. |
| T6 | Millionen-Kanten- und Real-Repo-Performancegate | M | T5 | Vorab festgelegte P95-/RAM-Budgets werden auf fixer Hardware/Runnerklasse gemessen; Rohresultate werden als Artefakt publiziert. |
| T7 | Capability-Ports und Manifest v1 | L | T2, T4 | Query/Search-Profile laufen über kleine Ports; Manifest entspricht exakt registrierten Routes/Tools; read-only Build registriert keine Write-Capability. |
| T8 | Gemeinsamer Payload-Generator und Search-Contracttest | L | T7 | Web und VS Code werden aus derselben Schemaquelle erzeugt; echte Go-Fixture navigiert in VS Code zum korrekten Pfad/Zeile. |
| T9 | Web Multi-Edge und stale-request Fix | M | T8 | Parallelkanten bleiben erhalten; Race-Test verhindert Überschreiben durch ältere Antworten; Graph wird nur bei Datenänderung neu gebaut. |
| T10 | Commit-gebundener Release-DAG | L | T1 | Rot gesetztes Gate erzeugt weder Tag noch Release; grüner Lauf publiziert ausschließlich Artefakte des gegateten SHA. |
| T11 | GitHub Action versioniert beziehen und Consumer-E2E | L | T10 | Fremd-Repo ohne Graphi-Quellen läuft; Test verifiziert tatsächlich ausgeführte Version und erwartete Outputs. |
| T12 | Supply-Chain-Härtung: SHA-Pins, SBOM, Vulnerability Gate, Attestation/Signatur | L | T10 | Alle releasekritischen Actions sind SHA-gepinnt; SBOM und Attestation existieren; High-Trust-Install lehnt unbestätigte Artefakte ab. |
| T13 | Alten Client und duplizierte Composition Roots entfernen | L | T7–T9 | Kein stabiler Consumer referenziert `client.Client`; keine Surface besitzt reine Interface-Erfüllungs-Stubs; alte Bridges sind gelöscht. |
| T14 | Real-Repo-RC-Gate und Claim-Audit | L | T6, T9, T11, T12, T13 | Full-Index, Peak-RAM, DB-Größe, Time-to-first-query, Kernantwortqualität und Distribution sind reproduzierbar; öffentliche Claims verweisen auf Rohdaten. |

### Kritischer Pfad

`T0 → T1 → {T2, T4, T5, T10} → T7 → T8 → T9 → T13 → T14`

Parallel dazu: `T3` nach `T1`, `T6` nach `T5`, `T11/T12` nach `T10`. Phase 7 beginnt erst nach `T14`; sie ist kein Release-Blocker für den fokussierten Secure Read Slice.

## Risiken und Gegenmaßnahmen

| Risiko | Auswirkung | Gegenmaßnahme |
|---|---|---|
| Compatibility Bridge wird dauerhaft | Zwei Architekturen und doppelte Wartung | Owner und Ablaufdatum pro Bridge; Lösch-Task im selben Epic; maximal eine Minor-Version Parallelbetrieb. |
| Port-Zerlegung erzeugt Interface-Explosion | Neue Komplexität ohne Nutzen | Ports nach Nutzeroperation und Trust-Klasse schneiden, nicht pro Methode; Consumer definiert das Interface; Architecture Decision Record. |
| Auth bricht Zero-config-UX | Nutzer umgehen Security oder deaktivieren sie | Token automatisch erzeugen und über sicheren Handshake/Secret Storage verteilen; kein Token in argv/URL; klare Recovery. |
| SSE kollidiert mit globalen Timeouts | Abbrüche oder DoS-Lücke | Eigene SSE-Server-/Route-Policy, Heartbeat und Shutdown-Drain; unäre Endpunkte behalten harte Deadlines. |
| Indexed Reads ändern Byte-Reihenfolge | Surface-Parität regressiert | Sortierung explizit im Portvertrag; Golden-/Backend-Conformance-Tests vor Umschaltung. |
| Vollgraph-Cache wird zu früh entfernt | Kleine Repos werden langsamer | Erst messen; selektive Reads einführen, Cache separat benchmarken, adaptive Strategie hinter internem Schalter. |
| Capability-Manifest driftet von Routes/Tools | Erneut falsche Produktclaims | Manifest generiert Deskriptoren/Docs oder wird im Test gegen Registrierungen verglichen; keine manuelle Doppelpflege. |
| Release-DAG wird durch Sonderpfade umgangen | Ungeprüfte Artefakte | Ein einziger Publish-Job mit Environment-Protection; keine alternative Tag-/Dispatch-Pipeline; SHA als unveränderlicher Input. |
| Real-Repo-Gates werden langsam/flaky | Teams deaktivieren sie | Kleine deterministische PR-Gates plus geplante Nightly-Corpus-Gates; Repos/Commits pinnen; Runnerklasse und Budgets versionieren. |
| Secret-Heuristik erzeugt False Positives | Memory-UX verschlechtert sich | Default reject mit erklärtem lokalen Override; niemals still speichern; Treffergrund ausgeben, Payload nicht loggen. |
| Team parallelisiert abhängige Rewrites zu früh | Merge-Konflikte in God-Files | Vertikale Slices, klare Code-Owner, Sequenz auf kritischem Pfad; `cmd/graphi/main.go` erst über Runtime entlasten, dann schneiden. |
| Experimentelle Features binden weiter Support | Kernstabilisierung verzögert sich | Feature-Freeze technisch und dokumentarisch durchsetzen; keine GA-Zählung; Bugs nur bei Datenverlust/Security behandeln. |

## Release-Go/No-Go für den Rebuild

Ein Release Candidate ist erst **Go**, wenn alle folgenden Punkte erfüllt sind:

- Keine ungeauthentisierte repository-sensitive HTTP-/MCP-HTTP-Route.
- Daemon und HTTP bestehen reale Subprocess-Lifecycle-Tests.
- Caller/Callee nutzen Endpoint-selektive Store-Abfragen und erfüllen das Performancebudget.
- VS Code und Web bestehen denselben generierten Search-/Payload-Vertrag; Parallelkanten gehen nicht verloren.
- `extract`/`move` sind entweder ehrlich deaktiviert oder semantisch korrekt separat implementiert.
- GitHub Action besteht ein fremdes Consumer-Repository und führt nachweislich die gepinnte Version aus.
- Der veröffentlichte SHA hat Release-Gate, Tests, Reproducibility, SBOM/Vulnerability-Prüfung und Attestation durchlaufen.
- Ein vollständiger realer Monorepo-Lauf dokumentiert Wall-clock, Peak-RAM, DB-Größe, Time-to-first-query und Signalqualität.

Bis dahin bleibt interne Entwicklung **Go**, automatische/externe Veröffentlichung der betroffenen Surfaces jedoch **No-Go**.

## Review- und Codebelege

| Schlussfolgerung | Reviewbeleg | Code-/Repo-Beleg |
|---|---|---|
| Teil-Rewrite statt Full Rewrite | `temp/review/00_overall_project_review.md:32-43`, `temp/review/01_architecture_expert.md:133-145`, `temp/review/04_security_privacy_expert.md:191-199` | `core/model/edge.go:48-113`, `core/parse/registry.go:10-100`, `surfaces/http/server.go:181-212` |
| Endpoint-selektive Reads zuerst | `temp/review/01_architecture_expert.md:29-49`, `temp/review/00_overall_project_review.md:52-64` | `engine/query/service.go:145-173`, `core/graphstore/graphstore.go:41-53`, `core/graphstore/sqlite.go:187-192`, `core/graphstore/sqlite.go:841-866` |
| Capability-Ports statt Monolith/Stubs | `temp/review/01_architecture_expert.md:33-51`, `temp/review/00_overall_project_review.md:53-64` | `surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`, `surfaces/daemon/client.go:343-382` |
| Surface-Security braucht Neufassung | `temp/review/04_security_privacy_expert.md:5-18`, `temp/review/04_security_privacy_expert.md:61-69`, `temp/review/04_security_privacy_expert.md:191-199` | `surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`, `surfaces/http/server.go:698-745`, `surfaces/mcp/http.go:16-49` |
| Memory-at-rest und Export sind Blocker | `temp/review/04_security_privacy_expert.md:50-57`, `temp/review/04_security_privacy_expert.md:71-77`, `temp/review/00_overall_project_review.md:54-54` | `engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`, `surfaces/client/direct.go:658-677` |
| Lifecycle muss zentralisiert werden | `temp/review/05_devops_production_readiness_expert.md:48-49`, `temp/review/05_devops_production_readiness_expert.md:67-73`, `temp/review/05_devops_production_readiness_expert.md:85-91` | `cmd/graphi/main.go:1100-1145`, `cmd/graphi/main.go:1237-1252`, `surfaces/daemon/daemon.go:170-186` |
| Gemeinsamer TS-Vertrag und Multi-Edge-Fix | `temp/review/02_senior_fullstack_engineer.md:30-38`, `temp/review/02_senior_fullstack_engineer.md:46-58`, `temp/review/00_overall_project_review.md:50-50` | `extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:18-35`, `extensions/vscode/src/citations.ts:31-40`, `web/src/GraphView.tsx:108-118` |
| Schreiboperationen ehrlich fail-closed | `temp/review/02_senior_fullstack_engineer.md:32-32`, `temp/review/02_senior_fullstack_engineer.md:52-52`, `temp/review/03_product_feature_expert.md:51-51` | `engine/edit/refactor.go:174-208`, `surfaces/cli/cli.go:65-90` |
| Release-DAG und Action blockieren Veröffentlichung | `temp/review/05_devops_production_readiness_expert.md:57-81`, `temp/review/05_devops_production_readiness_expert.md:85-99`, `temp/review/00_overall_project_review.md:47-55` | `.github/workflows/auto-release.yml:36-101`, `.github/workflows/release-gate.yml:1-30`, `extensions/github-action/action.yml:101-125` |
| Feature-Freeze und fokussierter Agent-/Query-Kern | `temp/review/03_product_feature_expert.md:1-24`, `temp/review/03_product_feature_expert.md:121-136`, `temp/review/06_business_monetization_expert.md:122-160` | `docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-177`, `docs/agent-workflows.md:7-49` |
| Reale Gates statt Scorecard-Claims | `temp/review/03_product_feature_expert.md:45-66`, `temp/review/06_business_monetization_expert.md:77-104` | `docs/release-scorecard.md:9-49`, `docs/real-world-report.md:9-40`, `corpus/manifest.json:4-80` |

## UNKNOWN / bewusst offene Entscheidungen

Die folgenden Punkte sind mit dem aktuellen Repository- und Review-Stand **UNKNOWN** und dürfen nicht durch unbelegte Annahmen ersetzt werden:

- Der konkrete P95-/RAM-Grenzwert wird in T6 aus Baseline und Zielhardware beschlossen; ohne reproduzierbaren Lauf wäre eine Zahl Scheingenauigkeit.
- At-rest-Verschlüsselung für Graph-/Meta-DB bleibt eine Threat-Model-Entscheidung. Sichere Dateirechte, Retention und Secret-Policy sind unabhängig davon Pflicht.
- Echte `extract`-/`move`-Implementierung ist ein späteres Produkt-Epic. Für den Rebuild genügt ehrliches Fail-closed-Verhalten.
- Ob Daemon unter Windows unterstützt wird, wird durch einen echten Windows-CI-Nachweis entschieden, nicht durch Cross-Compilation allein.
- Welche experimentellen Fähigkeiten wieder GA werden, entscheidet reale wiederholte Nutzung; technische Existenz ist kein Freigabekriterium.
