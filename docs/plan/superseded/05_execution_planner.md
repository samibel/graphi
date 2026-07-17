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

# Execution Plan: Graphi konsolidieren und release-faehig machen

> **Status: SUPERSEDED PLANNING INPUT.** Dieser Ausführungsvorschlag bleibt historische Begründung. Kanonische IDs, DAG, Aufwand und Gates stehen ausschließlich in `00_master_execution_plan.md`.

## 1. Entscheidung

**Entscheidung: Teil-Rewrite der Systemgrenzen, gezielter Refactor des bestehenden Kerns. Kein Full Rewrite.**

Der Begriff Teil-Rewrite gilt fuer vier Grenzen, deren heutige Vertraege strukturell falsch sind und nicht durch weitere lokale Sonderfaelle stabil werden:

1. HTTP/MCP-HTTP-Security und Capability-Autorisierung,
2. Surface-Capabilities und gemeinsamer Clientvertrag,
3. Daemon-/HTTP-Lifecycle und zentraler Composition Root,
4. Release-/GitHub-Action-Distribution.

`core/model`, Parser-/Analyzer-Registries, Graphstore-Schema und deterministische Serialisierung bleiben erhalten. Der Graph-Read-Pfad wird kompatibel erweitert und intern umgestellt; der Ingest wird spaeter entlang vorhandener Phasen extrahiert, nicht neu implementiert.

### Warum weder einfacher Refactor noch Full Rewrite

- Ein Full Rewrite wuerde belegte Substanz vernichten: immutable/provenance-tragende Edges, deterministische Store-Vertraege, Parser-Registry, serialisierte Ingest-Commits und Surface-Paritaet sind brauchbare Grundlagen (`core/model/edge.go:48-135`, `core/parse/registry.go:10-100`, `core/graphstore/graphstore.go:55-204`, `engine/ingest/ingest.go:622-777`).
- Ein rein inkrementeller Refactor reicht an den Trust Boundaries nicht: Der HTTP-Mux besitzt keine Auth-, Host- oder Origin-Pruefung; MCP-HTTP dispatcht denselben mutierenden Toolbestand; das breite `client.Client` zwingt Adapter zu `Unavailable`-Stubs (`surfaces/http/server.go:181-212`, `surfaces/mcp/http.go:16-49`, `surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`).
- Release, Daemon und Action haben belegte End-to-End-Vertragsfehler. Diese Grenzen brauchen neue Lebenszyklus- und Distributionsvertraege, nicht nur Patches (`.github/workflows/auto-release.yml:36-101`, `cmd/graphi/main.go:1138-1145`, `extensions/github-action/action.yml:101-125`).

## 2. Zielbild

Graphi wird als fokussiertes, local-first Code-Intelligence-Produkt betrieben:

- **GA-Kern:** `index`, `search`, callers/callees/references sowie `agent_brief`, `related_files`, `explain_symbol`, `change_risk` ueber CLI/MCP-stdio; ein kleiner lokaler Web-/IDE-Read-Pfad darf hinzukommen, wenn dessen Vertrag und Security-Gates gruen sind.
- **Ehrliche Capabilities:** Jede Surface publiziert ein maschinenlesbares Manifest. Commands haengen nur von kleinen Ports wie `QueryClient`, `SearchClient`, `AgentClient` oder `EditClient` ab. Fehlende Features sind nicht formal implementiert und scheitern nicht erst zur Laufzeit.
- **Sichere lokale Transporte:** Liveness darf anonym sein; jede repository-sensitive HTTP-, SSE- und MCP-HTTP-Route verlangt ein kurzlebiges Token, validierten Host und eine default-deny Origin-Policy. Schreibende Capabilities sind explizit gescoped und standardmaessig aus.
- **Ein Runtime-Owner:** Eine `Runtime`/`App`-Factory besitzt Store, Ingest, Watcher und Services. Daemon und HTTP haben `Done`, Signalsteuerung, Readiness und begrenzten Graceful Shutdown. CLI dekodiert Argumente, baut aber keine parallelen Service-Graphen mehr.
- **Selektive Graphreads:** Incoming/Outgoing-Reads werden direkt ueber `from_id`/`to_id` bedient; typische Nachbarschaftsabfragen skalieren mit Knotengrad statt kompletter Kantenklasse.
- **Ein Clientvertrag:** Go-Payloads sind die kanonische Quelle; Web und VS Code generieren Typen und Contract-Fixtures daraus. Parallelkanten bleiben erhalten, alte Requests koennen neueren UI-State nicht ueberschreiben.
- **Commit-gebundene Releases:** Gate, Build, Attestation und Publish bilden einen autoritativen DAG fuer exakt einen SHA. Die GitHub Action bezieht eine nachweisbar versionierte Graphi-Binary/Source und wird in einem fremden Consumer-Repo getestet.
- **Evidenz statt Inventar:** Release- und Marketingclaims beruhen auf reproduzierbaren Real-Repo-Runs und Agentenaufgaben. Produktbreite, Monetarisierung und weitere Surfaces bleiben bis zur validierten Nutzung eingefroren.

## 3. Aufwandsskala

Schaetzung fuer eine erfahrene Person inklusive Implementierung, Tests, Dokumentation und Review; Kalenderzeit sinkt nur bei wirklich unabhaengigen Paketen.

| Groesse | Nettoaufwand | Bedeutung |
|---|---:|---|
| **S** | 1-3 Personentage | Lokaler Fix mit vorhandenem Vertrag und kleiner Testflaeche |
| **M** | 4-8 Personentage | Ein Paket/Surface, neue Tests und begrenzte Vertragsaenderung |
| **L** | 2-4 Personenwochen | Mehrere Pakete/Surfaces, Migration und End-to-End-Gates |
| **XL** | 5-8 Personenwochen | Systemgrenzen ueber mehrere Runtimes/Distributionen; in kleinere Lieferpakete zu zerlegen |

Gesamtrahmen bis zum belastbaren Release Candidate: **XL, ca. 16-24 Personenwochen**. Produktvalidierung laeuft kalenderzeitlich parallel, ersetzt aber keine technischen Exit-Gates.

## 4. Prioritaeten und harte Reihenfolge

- **P0 – Containment und Wahrheit:** falsche Mutationen/Claims stoppen, automatische Releases und externe Action-Bewerbung einfrieren.
- **P0 – Security und Lifecycle:** Daten-/Schreibgrenzen, Prozessende und Release-Identitaet korrigieren.
- **P1 – Skalierbare Read-Basis:** selektive Store-Abfragen, bevor weitere Analyzer dieselben Vollscans vervielfachen.
- **P1 – Capability-/Runtime-Schnitt:** kleine Ports, Manifest, Composition Root und gemeinsamer TS-Vertrag.
- **P2 – Ingest-Entkopplung und Production Hardening:** erst nach stabilen Runtime-/Read-Vertraegen.
- **P2 – Reale Produktvalidierung und fokussierte Distribution:** vor Feature-Unfreeze oder Paid-Plattformbau.

Nicht parallelisieren, wenn die Abhaengigkeit semantisch ist: Security-Scopes benoetigen das Capability-Manifest; Daemon-/HTTP-Shutdown benoetigt den Runtime-Owner; Action-Publish benoetigt den neuen Release-DAG. Kleine P0-Korrekturen und der Traversal-Prototyp koennen dagegen unabhaengig laufen.

## 5. Zuerst zu bauende Grundlagen

1. **Release-/Capability-Inventar als maschinenlesbare Quelle (M):** stabil/experimental, read/write, Surface-Verfuegbarkeit und Distributionsstatus pro Capability. Das Inventar treibt `/contract`, MCP-Toolregistrierung, CLI-Hilfe, Docs und Tests.
2. **Threat Model und Security-Envelope-Spezifikation (S):** relevante Angreifer, Token-Lifecycle, Host-/Origin-Regeln, Scopes, Body-/Timeout-Budgets, SSE-Ausnahme und Memory-Datenklasse. Ohne diese Entscheidung entstehen erneut voneinander abweichende Checks.
3. **Cross-Surface Contract-Fixtures (M):** kanonische Go-Responses fuer Search, Query, Agent-Tools und Fehlerfaelle; dieselben Fixtures werden in Web und VS Code konsumiert.
4. **E2E-Harness fuer Prozesse und Consumer (M):** echte Child-Prozesse fuer Daemon-Stop/Signal/HTTP-Shutdown sowie minimales fremdes GitHub-Action-Repository. Unit-Tests am Serverobjekt reichen fuer die belegten Fehler nicht.
5. **Messbasis (M):** feste kleine, mittlere und Millionen-Kanten-Fixtures sowie dokumentierte Hardware-/Messregeln fuer Latenz, Peak-RSS und DB-Groesse.

## 6. Milestones und Arbeitspakete

### M0 – Freeze, Claims und Fail-Closed-Basis (Woche 1, P0, M)

**Abhaengigkeiten:** keine. **Exit Gate:** Kein bekannter oeffentlicher Pfad behauptet oder vollzieht falsche Semantik; ungepruefte Releases sind blockiert.

1. **M0.1 Feature- und Release-Freeze (S):** Neue Analyzer, Sprachen, Tools und Surfaces stoppen. `auto-release` bis M2 deaktivieren oder mit einem expliziten manuellen Approval blockieren. GitHub Action nicht als shipped/extern nutzbar bewerben.
2. **M0.2 Gefaehrliche Edit-Claims fail-closed (S):** `extract` und `move` aus stabilen Deskriptoren entfernen oder mit typed `not implemented` ablehnen; `safe-delete` und `inline` als begrenzt/experimental kennzeichnen. `extract`/`move` duerfen nicht mehr `planNameRewrite` ausfuehren (`engine/edit/refactor.go:174-186`).
3. **M0.3 Sofortige Privacy-Fixes (S):** Memory-Datei und Parent auf `0600`/`0700` migrieren; Secret-Suspects standardmaessig ablehnen oder redigieren; frei waehlbaren `export_to_path` aus Remote-/MCP-Vertraegen entfernen (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`, `surfaces/client/direct.go:658-677`).
4. **M0.4 Produktwahrheit (S):** 100/100 als Engineering-Regression-Score kennzeichnen; nicht reproduzierbaren 99,4-%-Claim und widerspruechliche shipped-/TUI-/38-vs-42-Aussagen entfernen oder exakt qualifizieren (`docs/release-scorecard.md:9-49`, `site/index.html:198-316`, `docs/coverage-matrix.md:72-177`).

### M1 – Security-Envelope und Capability-Segregation Slice 1 (Wochen 2-4, P0, L)

**Abhaengigkeiten:** Threat Model und Capability-Inventar. **Exit Gate:** Alle repository-sensitiven Netzwerkpfade bestehen positive und negative Auth-/Scope-Tests.

1. **M1.1 Gemeinsame Middleware (M):** Auth-Token mit konstantzeitlichem Vergleich, Host-Allowlist, default-deny Origin-Policy, Request-ID, einheitliche Body-Limits und datensparsame Fehler/Logs fuer HTTP/SSE/MCP-HTTP. `/healthz` bleibt reine Liveness; `/readyz` wird separat authentisiert oder bewusst lokal eingeschraenkt.
2. **M1.2 MCP-HTTP read-only default (M):** Read- und Write-Toolsets trennen. Mutationen (`refactor`, `undo`, Memory write/forget/export, PR publish) benoetigen explizite, getestete Scopes. Bis dahin ist der exportierte HTTP-Handler experimental und nicht produktiv verdrahtet.
3. **M1.3 Body-/Timeout-Policy (S):** `MaxBytesReader`, `DisallowUnknownFields`, `ReadTimeout`/`IdleTimeout` fuer unaere Requests; SSE bekommt eine dokumentierte eigene Streaming-Policy. Heute sind Memory/Distill/SkillGen unbegrenzt (`surfaces/http/server.go:698-745`).
4. **M1.4 Token-Hand-off (M):** Pro Prozess kryptographisch zufaelliges Token, nicht in argv/URL/Logs; sicherer Uebergabepfad an VS Code/Web-Start. Entfernen der derzeit irrefuehrenden Client-only-Token-Semantik (`extensions/vscode/src/graphiClient.ts:138-156`).
5. **M1.5 Negative Integrationstests (M):** fehlendes/falsches Token, boeser Host, Fremd-Origin, oversized Body, write ohne Scope, SSE-Reconnect ohne Auth und DNS-Rebinding-aehnlicher Host werden abgewiesen; Health bleibt erreichbar.

### M2 – Lifecycle, Runtime und Release-DAG (Wochen 3-7, P0/P1, L)

**Abhaengigkeiten:** M0; Capability-Inventar fuer Runtime-Schnitt. **Exit Gate:** Daemon/HTTP enden deterministisch; kein Artefakt kann fuer einen ungeprueften SHA publiziert werden.

1. **M2.1 Daemon-Lifecycle (M):** `Done()`/idempotentes `Shutdown(ctx)`, `signal.NotifyContext`, Ack-then-drain und definierte Exit Codes. E2E-Test startet echte Binary, sendet `stop`, wartet mit Zeitbudget auf Prozessende und prueft Socket-, Watcher- und DB-Cleanup. Das ersetzt `select {}` (`cmd/graphi/main.go:1138-1145`).
2. **M2.2 HTTP-Lifecycle/Readiness (M):** Listener und `http.Server` werden vom Runtime-Owner gehalten; SIGINT/SIGTERM fuehren zu begrenztem `Shutdown`. `/readyz` prueft Store, Initial-Ingest und kritischen Watcherzustand; `/healthz` bleibt Liveness (`surfaces/http/server.go:251-267`, `surfaces/http/server.go:315-317`).
3. **M2.3 Composition Root Slice 1 (L):** eine `app.Runtime`/Factory fuer Store, Ingest, Watcher, Query/Search/Agent und Capability-Manifest. `makeClient`, Editor-, Daemon- und HTTP-Komposition werden schrittweise darauf migriert; jedes Slice behaelt bestehende kanonische Bytes (`cmd/graphi/main.go:181-188`, `cmd/graphi/main.go:1002-1248`).
4. **M2.4 Autoritativer Release-DAG (M):** Gate -> Tests -> reproduzierbarer Build -> SBOM/Attestation -> Publish fuer exakt denselben SHA; Tag erst nach erfolgreichen Gates. `auto-release` darf nicht nur auf den separaten `release`-Workflow lauschen (`.github/workflows/auto-release.yml:36-101`).
5. **M2.5 Supply Chain (M):** alle Release-/Privacy-Actions auf volle SHAs, `govulncheck`/Dependency Review, SBOM und signierte Provenance; Installer dokumentiert/verifiziert den Herkunftsnachweis, nicht nur Hash aus derselben Release-Domaene.

### M3 – Endpoint-selektiver Graph-Read-Pfad (Wochen 5-8, P1, L)

**Abhaengigkeiten:** Messbasis; kann mit M1/M2 teilweise parallel beginnen. **Exit Gate:** Caller/Callee/Reference-Hotpaths scannen keine vollstaendige Kantenklasse mehr und halten ein verbindliches Budget.

1. **M3.1 Narrow Reader API (M):** `Incoming`, `Outgoing` und batched Multi-Source-Varianten mit kanonischer Sortierung definieren; Conformance-Tests fuer SQLite und Memory erstellen. Die bestehende `Query` unterstuetzt heute keine Endpunkte (`core/graphstore/graphstore.go:41-53`).
2. **M3.2 Backend-Indizes nutzen (M):** SQLite direkt ueber vorhandene `edges_from_id`/`edges_to_id` abfragen; Memory-Backend erhaelt Adjazenzindizes. Semantik, Provenienz und deterministische Reihenfolge bleiben bytegleich (`core/graphstore/sqlite.go:187-192`, `core/graphstore/sqlite.go:841-866`).
3. **M3.3 Query-Service migrieren (S):** `directedLookup` und weitere Nachbarschaftspfade verwenden nur endpoint-selektive Reads statt `Edges(EdgeKind)` plus Filter (`engine/query/service.go:145-173`).
4. **M3.4 Cache-Entscheidung (M/L, Decision Gate):** Nach Messung entscheiden: Vollgraph-Cache beibehalten, partiell machen oder fuer grosse DBs deaktivieren. Keine Cache-Neuschreibung ohne RSS-/Latenzbeleg.
5. **M3.5 Performance-Gate (M):** Bei 1 Mio. Kanten wird die Zahl betrachteter Rows/Edges proportional zum Degree nachgewiesen; p95 fuer degree <=100 und Peak-RSS erhalten vorab festgelegte Budgets. Exakte Schwellen werden nach Baseline auf CI-Hardware festgelegt und versioniert.

### M4 – Ehrliche Surface-Vertraege und Frontend-Korrektheit (Wochen 7-11, P1, L)

**Abhaengigkeiten:** Capability-Inventar, Contract-Fixtures, Runtime Slice 1. **Exit Gate:** Keine Surface erfuellt einen Monolithen durch Stubs; Go/Web/VS Code bestehen dieselben Payload-Fixtures.

1. **M4.1 Interface Segregation (L):** `client.Client` in Query-, Search-, Analysis-, Agent-, Edit-, Memory- und Forge-Ports zerlegen. CLI/MCP-Handler verlangen den kleinsten Port; Capability-Manifest ersetzt Stub-Konformitaet. Migration in kompatiblen Slices, nicht Big Bang (`surfaces/client/client.go:268-464`).
2. **M4.2 Kanonische TypeScript-Generierung (M):** Search- und Kernpayloads aus Go-Schema/OpenAPI/JSON-Schema generieren. Web und VS Code haben einen Drift-Check in CI. Die VS-Code-Suche verwendet `node_id`, `qualified_name`, `source_path`, `line` statt `id/path` (`engine/search/service.go:18-35`, `extensions/vscode/src/contract.ts:104-107`).
3. **M4.3 VS-Code CI und Consumer-Test (S/M):** `typecheck`, `lint`, `test` werden required. Ein echtes Go-Search-Fixture prueft QuickPick-Label, Pfad und Navigation.
4. **M4.4 Web-Datenkorrektheit (M):** `MultiDirectedGraph` oder fachlich explizite Aggregation erhaelt jede eindeutige Edge-ID; nicht gemockter Graphology-Test belegt zwei Kanten gleicher Endpunkte (`web/src/GraphView.tsx:108-118`, `core/model/edge.go:76-80`).
5. **M4.5 Request-Races und Renderkosten (M):** AbortController/Sequenznummern fuer Load, Select und Search; nur Graphdatenaenderungen triggern O(V+E)-Rebuilds. Tests lassen alte Antworten absichtlich nach neuen eintreffen (`web/src/useGraph.ts:205-297`, `web/src/GraphPage.tsx:63-75`).

### M5 – GitHub Action, Ingest-Hardening und Production RC (Wochen 10-15, P1/P2, L/XL)

**Abhaengigkeiten:** M2 Release-DAG und Runtime, M3 Read-Pfad, M4 Contracts. **Exit Gate:** Installations-, Action-, Upgrade- und Crash-Journeys laufen auf den unterstuetzten Plattformen reproduzierbar.

1. **M5.1 Action-Packaging neu schneiden (M/L):** `graphi-version` selektiert nachweisbar ein Release-Artefakt oder einen separaten Graphi-Checkout; Consumer-Workspace und Engine-Source sind getrennt. Checksums/Attestation werden verifiziert (`extensions/github-action/action.yml:101-125`).
2. **M5.2 Fremd-Repo-E2E (M):** minimales Consumer-Repo ruft die Action mit gepinnter Version auf, erzeugt erwartete Outputs und beweist, dass kein `./cmd/graphi` im Consumer notwendig ist. Falsche Version/Checksum muss fail-closed enden.
3. **M5.3 Ingest phasenweise extrahieren (L):** `Scanner`, `ParsePipeline`, `GraphCommitter`, `LinkPhase`, `MetaRepository`, `RecoveryJournal`; Verhalten zuerst charakterisieren, dann verschieben. Keine Algorithmus-Neuerfindung im selben Change (`engine/ingest/ingest.go:128-192`, `engine/ingest/ingest.go:607-809`).
4. **M5.4 Crash-/Migrationstests (L):** Fault Injection zwischen Meta-Transaktion und jedem Graph-Batch, Recovery nach Kill, grosse Schema-Migration, Snapshot/Restore. Exit Gate: nach jedem injizierten Abbruch entweder letzter gueltiger Stand oder deterministisch vollstaendiger Recovery-Pfad, nie still gemischte Generationen.
5. **M5.5 Cross-Platform Matrix (M):** echte Windows-Binary-Journey oder dokumentierter Ausschluss des Unix-Socket-Daemons; Linux/macOS Daemon/HTTP/Installer; Doctor meldet fehlendes Go bei Release-Binary nicht als Runtime-Fail und liest echte Schema-Version.

### M6 – Real-World-Evidenz und Produkt-Unfreeze (parallel ab Woche 2, Abschluss nach M5, P2, L)

**Abhaengigkeiten:** stabile M3-M5-Builds fuer finale Zahlen. **Exit Gate:** Claims und Scope werden durch eingecheckte Rohdaten/Runner sowie Nutzungssignale getragen.

1. **M6.1 Reale Repo-Benchmarks (M):** Spring Boot nach Fix komplett messen: Wall-clock, Time-to-first-query, Peak-RAM, DB-Groesse und Signalqualitaet; keine Proxyzahl als End-to-End-Ergebnis.
2. **M6.2 Agenten-Outcome-Evaluation (L):** mindestens 20 versionierte Coding-Aufgaben ueber mehrere reale Repos, mit/ohne Graphi; Task-Erfolg, Regressionen, Zeit, Kontexttokens und unnoetig gelesene Dateien messen. Runner und anonymisierte Rohresultate werden eingecheckt.
3. **M6.3 GA-/Labs-Schnitt (S):** Nur Capabilities mit Contract-, Security-, Corpus- und Nutzer-Gate werden GA. Rest bleibt Labs, wird nicht in die Headline-Summe eingerechnet und hat keine Paritaetszusage.
4. **M6.4 ICP/Pilot (M, nicht primaer Engineering):** 8-12 Interviews und 2-3 Design-Partner fuer einen self-hosted Team-/CI-Workflow; manuelles Pilotangebot mit Preisexperiment und Erfolgskriterien. Keine Billing-, SaaS- oder Enterprise-Control-Plane vor wiederholter Nutzung und konkreter Zahlungszusage.
5. **M6.5 Unfreeze-Entscheidung:** Neue Features erst, wenn mindestens ein Design-Partner den Hero-Workflow wiederholt nutzt und die technischen RC-Gates gruen sind. Paid-Build nur bei belegter Zahlungsbereitschaft.

## 7. Konkrete naechste Tasks mit messbaren Abnahmekriterien

Diese Tasks bilden den unmittelbar ausfuehrbaren ersten Backlog; Nummerierung ist Reihenfolge, ausser wo Parallelitaet markiert ist.

1. **T1 – `extract`/`move` fail-closed (S, P0).**
   - `planRefactor` akzeptiert nur real implementierte Kinds; Tests beweisen, dass `extract` und `move` vor Dateizugriff/Mutation mit typed Fehler enden.
   - CLI-, MCP- und Help-/Capability-Ausgabe behaupten die Funktion nicht mehr als stabil.
   - Eine Fixture-Datei bleibt nach jedem abgelehnten Request byteidentisch.
2. **T2 – Memory-Minimumstandard (S, P0; parallel zu T1).**
   - Neu angelegte und bestehende Journale sind nach `Open` exakt `0600`, Parent-Verzeichnis `0700`.
   - Secret-Fixtures werden ohne expliziten lokalen Override nicht im Klartext persistiert.
   - Remote- und MCP-Requests koennen keinen beliebigen Dateipfad mehr erzeugen/ueberschreiben.
3. **T3 – Release-Publish sperren (S, P0; parallel).**
   - Ein Test/Validator modelliert `release-gate=failure` und zeigt: weder Tag noch Release/Assets werden erzeugt.
   - Der publizierte SHA ist identisch mit dem SHA, fuer den Gate und Repro-Build gruen waren.
4. **T4 – Security-Spezifikation und Testskelett (S/M, P0).**
   - Threat Model entscheidet lokale Prozesse, andere lokale Nutzer, Browser/DNS-Rebinding und MCP-Clients explizit.
   - Eine Tabelle definiert pro Route Auth, Scope, Bodylimit und Timeout.
   - Negative Tests sind vor Implementierung rot und decken Token, Host, Origin, Oversize und Write-Scope ab.
5. **T5 – Daemon-E2E-Lifecycle (M, P0; parallel zu T4).**
   - Test startet echte Binary, wartet auf Readiness, sendet `stop` und beobachtet Exit innerhalb 5 s.
   - Socket ist entfernt; zweiter Stop ist idempotent; Deferred-Cleanup ist ueber erneut oeffenbare DB/Watcher-Ressourcen belegt.
6. **T6 – Search-Contract-Hotfix plus Fixture (S, P0).**
   - VS Code verarbeitet eine echte Go-Response mit `node_id`, `qualified_name`, `source_path`, `line`.
   - QuickPick-Label und Navigation werden getestet; Typecheck/Lint/Test laufen in CI.
   - Dieser Hotfix wartet nicht auf den spaeteren Generator.
7. **T7 – Parallelkanten und stale UI requests (M, P0/P1; parallel zu T6).**
   - Zwei Edges gleicher Endpunkte mit unterschiedlicher ID/kind existieren nach Rendering im Graph.
   - Eine spaete alte Search-/Impact-Antwort kann weder Seed, Selection noch Fehlerstate einer neueren Antwort ueberschreiben.
8. **T8 – Incoming/Outgoing Spike und Baseline (M, P1).**
   - Conformance-API fuer SQLite/Memory, Queryplan nutzt `edges_from_id`/`edges_to_id`.
   - 1-Mio.-Kanten-Fixture dokumentiert vorher/nachher p50/p95, Peak-RSS und betrachtete Rows.
   - Semantische Ergebnisbytes fuer vorhandene Paritaetsfixtures bleiben gleich.
9. **T9 – Capability-Manifest Slice 1 (M, P1).**
   - Query/Search/Agent/Edit sind getrennt als read/write + stable/experimental beschrieben.
   - `/contract`, MCP-Toolliste und mindestens eine CLI-Hilfe werden aus derselben Quelle getestet.
   - Daemon/HTTP melden nur tatsaechlich verdrahtete Capabilities; kein `Unavailable`-Stub ist fuer diesen Slice noetig.
10. **T10 – GitHub-Action Consumer-E2E (M/L, P1, nach T3).**
    - Consumer ohne Graphi-Source fuehrt die Action mit einer existierenden Version erfolgreich aus.
    - Aus Log/Attestation ist die verwendete Graphi-Version eindeutig.
    - Manipuliertes Asset oder nicht existierende Version bricht vor Ausfuehrung der Engine ab.

## 8. Einzufrierende, zu entfernende oder bewusst nicht zu bauende Teile

### Sofort einfrieren

- neue Sprachen, Analyzer, MCP-Tools, CLI-Verben und Surfaces;
- PR-Konfliktanalyse, Review-Kritik, SkillGen, Distill, Memory-Ausbau und graph-aware Editing ausser fuer Security-/Korrektheitsfixes;
- SaaS, Billing, SSO/RBAC und Enterprise-Control-Plane bis zur Pilotvalidierung;
- neue Marketing-/Savings-Claims ohne reproduzierbaren Runner und Rohdaten.

### Aus stabilem Vertrag entfernen oder als Labs markieren

- `extract`, `move` bis zu echten, sprachbewussten Plannern;
- `safe-delete`/`inline` solange deren enge Grenzen nicht dem Namen entsprechen;
- MCP-HTTP-Mutationen bis Auth und Scopes gruen sind;
- GitHub Action, TUI und VS Code aus der Kategorie „shipped“, solange Release-Binary/Marketplace/Consumer-E2E nicht belegt sind;
- experimentelle PR-, Memory-, Skill- und tiefe Security-Funktionen aus GA-Toolzaehlern.

### Nach Migration loeschen

- monolithisches `client.Client` und die dazugehoerigen reinen `Unavailable`-Stubmethoden;
- duplizierte Composition-Funktionen in `cmd/graphi`, sobald alle Surfaces den Runtime-Owner nutzen;
- manuell kopierte VS-Code-Payloadtypen nach erfolgreicher Generator-/Schema-Migration;
- entkoppeltes `auto-release`-Konstrukt nach Einfuehrung des autoritativen DAG;
- frei waehlbare Remote-Dateiexport-Primitive.

## 9. Risiken und Gegenmassnahmen

| Risiko | Wirkung | Gegenmassnahme / Gate |
|---|---|---|
| Vertragsbruch bei Interface-Segregation | viele Surfaces brechen gleichzeitig | Strangler-Slices, Adapter nur temporaer, Byte-Paritaetsfixtures pro Slice |
| Token-UX zerstoert Zero-Config | Web/VS Code verbinden nicht mehr | sicherer Start-Handshake und E2E-Journey vor Aktivierung der Pflichtauth |
| SSE kollidiert mit globalen Timeouts | Streams werden ungewollt getrennt | getrennte Streaming-Policy, Reconnect- und Shutdown-Tests |
| Selektive SQL-Reads veraendern Reihenfolge/Provenienz | Surface-Paritaet regressiert | kanonische Sortierung und bestehende Byte-Fixtures als hartes Gate |
| Cache-Redesign verlangsamt kleine Repos | lokaler Hauptpfad wird schlechter | Cache erst nach Baseline/Decision Gate aendern; kleine und grosse Budgets getrennt |
| Composition-Root-Refactor wird Big Bang | lange instabile Branches | Query/Search zuerst, eine Surface pro Slice, alte Factory erst nach E2E entfernen |
| Release-Hardening blockiert Releases zu lange | Distribution stockt | manuelle, commit-gebundene RCs erlaubt; keine automatische Umgehung |
| Ingest-Extraktion vermischt Struktur und Semantik | Recovery-/Graphregression | Characterization + Fault Injection vor Verschieben, Algorithmusaenderungen separat |
| Scope-Freeze wird durch dringende Kundenwuensche aufgeweicht | erneuter Sprawl | Ausnahme nur mit benanntem Nutzer, Erfolgskriterium und Owner fuer Supportkosten |
| Keine Marktvalidierung trotz Technikarbeit | technisch gutes, nicht genutztes Produkt | Design-Partner-Track ab Woche 2; Stop-Regel vor M6-Unfreeze |

## 10. Review- und Codebelege fuer die Priorisierung

| Prioritaet | Direkter Beleg | Schlussfolgerung |
|---|---|---|
| Teil-Rewrite statt Full Rewrite | Gesamturteil 5,0/10 und explizite Teil-Rewrite-Empfehlung (`temp/review/00_overall_project_review.md`); Core-Invarianten in `core/model/edge.go:48-135` | Core behalten, Grenzen neu schneiden |
| Security P0 | nackter Mux ohne Auth/Host/Origin (`surfaces/http/server.go:181-212`); MCP-HTTP direkt zu `handle` (`surfaces/mcp/http.go:16-49`) | Netzwerkfreigabe fuer private Repos ist No-Go bis M1 |
| Privacy P0 | Memory `0644`, Secret nur markiert (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`) | sofortiger Minimalfix vor Architekturarbeit |
| Lifecycle P0 | CLI blockiert in `select {}`, Server-Stop schliesst nur Listener/Socket (`cmd/graphi/main.go:1138-1145`, `surfaces/daemon/daemon.go:170-186`) | echter Prozess-E2E und neuer Lifecycle-Vertrag |
| Release P0 | Auto-Release wartet nur auf `release`, nicht `release-gate` (`.github/workflows/auto-release.yml:36-101`, `.github/workflows/release-gate.yml:1-30`) | Publish muss commit-gebunden neu orchestriert werden |
| Action P0/P1 | `graphi-version` ist nur Env; Build nutzt Consumer-Workspace `./cmd/graphi` (`extensions/github-action/action.yml:101-125`) | Action vor externer Nutzung neu paketieren |
| Query P1 | API ohne Endpointfilter, Service scannt Kantenklasse trotz SQL-Indizes (`core/graphstore/graphstore.go:41-53`, `engine/query/service.go:145-173`, `core/graphstore/sqlite.go:187-192`) | hoechster Architektur-/Performance-ROI |
| Capability P1 | breites Interface plus Remote-Stubs (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`) | Manifest und kleine Ports vor Featurewachstum |
| Frontend P0/P1 | Search-Felder driften; Parallelkanten werden verworfen (`extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:18-35`, `web/src/GraphView.tsx:108-118`) | reale Nutzerpfade sofort korrigieren |
| Produktfokus P2 | 142 Capabilities/acht Surfaces ohne Adoption; reales Monorepo-Retest fehlt (`temp/review/03_product_feature_expert.md`, `docs/real-world-report.md:23-40`) | Freeze und evidenzbasiertes Unfreeze |
| Business erst nach Validierung | kein Preis, Funnel oder zahlender Kunde (`temp/review/06_business_monetization_expert.md`, `site/index.html:35-46`) | kein SaaS-/Billing-Bau im RC-Pfad |

## 11. UNKNOWN und Entscheidungspunkte

1. **Threat Model:** Sind andere lokale Nutzer/Prozesse, Browser-DNS-Rebinding und eingebettete MCP-Clients offiziell im Scope? Defaultentscheidung: ja, solange HTTP/Sockets exponiert sind. Entscheidung vor M1.
2. **MCP-HTTP-Nutzung:** Im Haupt-CLI ist derzeit nur stdio verdrahtet; externe Embedder sind UNKNOWN. Entscheidung: API bis Nutzungsnachweis experimental halten oder ganz unexportieren.
3. **GA-Surfaces:** Externe Nutzung/Marketplace-Status von VS Code, Action, TUI, Homebrew/Scoop ist UNKNOWN. Entscheidung nach Distributionsaudit in M0/M6; nicht aus Code-Praesenz auf shipped schliessen.
4. **Performancebudgets:** Kipppunkt, p95 und Peak-RSS auf Zielhardware sind UNKNOWN. Entscheidung nach T8-Baseline; vorher keine erfundenen absoluten SLA-Zahlen.
5. **Vollgraph-Cache:** Ob der Cache fuer kleine Repos behalten, adaptiv begrenzt oder ersetzt wird, bleibt bis M3.4 offen.
6. **Cross-DB-Recovery:** Vollstaendigkeit heutiger Dirty-/Checkpoint-Semantik an allen Kill-Punkten ist UNKNOWN. Entscheidung ueber tiefere Ingest-Neustrukturierung erst nach M5-Fault-Injection.
7. **Windows:** Ob Unix-Daemon auf Windows offiziell unterstuetzt werden soll, ist UNKNOWN. Entscheidung: nativer Transport + CI oder klare Feature-Matrix mit Ausschluss.
8. **At-rest-Verschluesselung:** Datenklassifikation und Kundenanforderung sind UNKNOWN. `0600`/Secret-Policy ist Pflicht; OS-Keychain/Envelope Encryption erst nach Threat Model/Design-Partner-Anforderung.
9. **Real-World-Qualitaet:** Parser-Recall/Precision ueber 22 Sprachen, Spring-Boot-Endwerte und Agenten-Outcome sind UNKNOWN. Diese Punkte werden nicht durch interne Conformance-Scores ersetzt.
10. **Markt/Monetarisierung:** Adoption, Retention, ICP, Zahlungsbereitschaft, Supportlast und Maintainer-Kapazitaet sind UNKNOWN. Entscheidung ueber Paid-Wertzaun erst nach Interviews/Piloten.
11. **Branch Protection/externe Attestations:** Repository-Settings und aktuelle CI-Runs sind ausserhalb des Codes UNKNOWN. Sie muessen im Release-Audit explizit erfasst werden, koennen aber den fehlerhaften eingecheckten DAG nicht kompensieren.
12. **`extract`/`move`-Zukunft:** Echte semantische Implementierung ist sprach- und AST-abhaengig. Entscheidung nach GA-Kern: eigener sprachbegrenzter Planner mit Korrektheitscorpus oder dauerhafte Entfernung.

## 12. Go/No-Go-Gates

- **Naechster automatischer Release:** No-Go bis M0.1/M2.4.
- **Bewerbung als sicher fuer sensible/private Repositories ueber HTTP:** No-Go bis M1 komplett.
- **Hot-Daemon als betriebsreif:** No-Go bis M2.1/M2.2 inklusive Prozess-E2E.
- **GitHub Action extern/Marketplace:** No-Go bis M5.1/M5.2.
- **Feature-Unfreeze:** No-Go bis M3/M4 RC-Gates und M6-Nutzungssignal.
- **Full Rewrite:** No-Go; neu bewerten nur, wenn Conformance-/Fault-Injection-Arbeit zeigt, dass Kerninvarianten nicht erhaltbar sind. Dafuer gibt es derzeit keinen Beleg.

Der kritische Pfad ist damit: **Containment -> Security/Capability-Grundlage -> Lifecycle/Release-DAG -> selektive Reads -> Surface-/Contract-Migration -> Action/Ingest-RC -> reale Evidenz -> Unfreeze.**
