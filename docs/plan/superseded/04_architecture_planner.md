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

# Architekturplan: Graphi konsolidieren, Grenzen neu bauen

> **Status: NORMATIVE ARCHITECTURE ANNEX.** Zielarchitektur, Invarianten, Verträge, Migrationsseams und Risiken bleiben verbindliche Referenz. Der eigene Backlog, kritische Pfad, Aufwand und Schedule sind durch `00_master_execution_plan.md` superseded.

## Executive Decision

**Entscheidung: Teil-Rewrite der Systemgrenzen, kein Full Rewrite des Produkts.**

Der vorhandene Core bleibt erhalten: `core/model`, Parser-/Analyzer-Registries, deterministische Serialisierung, Graphstore-Schema und Provenienz-Invarianten sind wertvolle, testbare Substanz. Neu gebaut werden die Grenzen, deren heutige Verträge systemisch falsch sind:

1. Graph-Read-Port und SQLite-Traversal-Hotpath,
2. Capability-Komposition zwischen Engine und Surfaces,
3. HTTP/SSE/MCP-HTTP-Trust-Boundary,
4. Runtime-Komposition und Prozess-Lifecycle,
5. gemeinsamer Web-/VS-Code-Vertrag,
6. Release- und Action-Distribution.

Das ist mehr als ein lokaler Refactor: Authentisierung, Capability-Segregation, Lifecycle und Distribution benötigen neue Verträge und neue End-to-End-Gates. Es ist aber ausdrücklich **kein Full Rewrite**, weil Datenmodell, Parser, Store-Durability, Query-/Analyse-Kernels und große Teile der Testinfrastruktur nicht verworfen werden.

**Gesamtaufwand: XL, grob 14–20 Engineer-Wochen** bis zu einer belastbaren, fokussierten Release-Basis. Das ist keine Kalenderzusage; mit zwei erfahrenen Engineers und wenig Parallelstörung sind etwa 8–12 Kalenderwochen plausibel. Marktvalidierung, vollständige echte `extract`-/`move`-Implementierungen und eine Enterprise-Control-Plane sind nicht enthalten.

## Warum diese Entscheidung

### Warum kein Full Rewrite

- `core/model.Edge` erzwingt Confidence, Reason und Evidence und bildet eine deterministische Identität; diese Invarianten neu zu implementieren wäre unnötiges Korrektheitsrisiko (`core/model/edge.go:48-113`).
- `Graphstore`, `Writer`, `Batcher` und `Batch` trennen Read-/Write- und Transaktionsfähigkeiten bereits sinnvoll (`core/graphstore/graphstore.go:55-204`).
- Parser- und Analyzer-Registries sind offene, threadsichere Erweiterungspunkte (`core/parse/registry.go:10-100`, `engine/analysis/dispatch.go:42-59`).
- SQLite ist autoritativ, Writes werden vor Cache-Updates committed und Full-Ingest committed deterministisch (`core/graphstore/sqlite.go:24-46`, `engine/ingest/ingest.go:622-720`).
- Reproduzierbare Builds, Privacy-Gates und vorhandene Conformance-Tests liefern wertvolle Migrationssicherungen (`internal/release/build.go:146-199`, `.github/workflows/privacy-audit.yml:21-55`).

### Warum ein normaler Refactor nicht ausreicht

- Der Read-Vertrag kann Endpunkte nicht ausdrücken. Caller-/Callee-Abfragen laden deshalb alle Kanten einer Art und filtern im Service, obwohl SQLite bereits `from_id`-/`to_id`-Indizes besitzt (`core/graphstore/graphstore.go:41-53`, `engine/query/service.go:145-173`, `core/graphstore/sqlite.go:187-192`). Hier muss der Port geändert werden.
- `surfaces/client.Client` mischt Query, Search, Analyse, Edit, Memory, Agent- und Forge-Fähigkeiten in einem breiten Interface. Remote-Adapter simulieren Konformität mit `Unavailable`-Stubs (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`). Hier muss der Capability-Vertrag ersetzt werden.
- HTTP registriert sensible Read- und Write-Routen auf einem nackten Mux. `schemaGuard` ist Versionsprüfung, keine Authentisierung; Host und Origin werden nicht geprüft (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`). Hier braucht es eine neue gemeinsame Trust-Boundary.
- Daemon und HTTP besitzen keinen einheitlichen Lifecycle. `daemon stop` schließt den Listener, während der CLI-Prozess in `select {}` verbleibt (`surfaces/daemon/daemon.go:170-186`, `cmd/graphi/main.go:1138-1145`). Hier muss Ownership neu definiert werden.
- Die GitHub Action behauptet Version-Pinning, baut aber `./cmd/graphi` aus dem Consumer-Workspace; der Release-DAG kann das separate Release-Gate umgehen (`extensions/github-action/action.yml:101-125`, `.github/workflows/auto-release.yml:36-101`). Das ist eine neue Distributionskette, kein Kommentarfix.

## Leitprinzipien des Zielbilds

1. **Core-Invarianten bleiben stabil.** Keine Änderung an Edge-/Node-Identität oder Provenienz ohne separate Migration und Snapshot-Kompatibilitätsnachweis.
2. **Ports spiegeln echte Fähigkeiten.** Kein Adapter muss Methoden implementieren, die sein Transport nicht unterstützt.
3. **Read und Write sind getrennte Trust-Zonen.** Remote-Schreibrechte sind nie implizit und standardmäßig aus.
4. **SQLite beantwortet selektive Fragen.** Vollgraph-Listen sind Export-/Adminpfade, nicht der Traversal-Hotpath.
5. **Eine Runtime besitzt Ressourcen.** Store, Meta-DB, Watcher, Broker und Server haben genau einen Owner und einen begrenzten Shutdown.
6. **Ein kanonischer Vertrag erzeugt Clients.** Web und VS Code pflegen keine unabhängigen Payload-Spiegel.
7. **Migration über Seams, nicht Big Bang.** Neue und alte Ports laufen zeitweise parallel; Vergleichstests entscheiden über Umschaltung und Löschung.
8. **Capability-Parität ist nicht Byte-Parität.** Für gemeinsam unterstützte Operationen gelten gleiche kanonische Bytes; jede Surface veröffentlicht zusätzlich ihr tatsächliches Capability-Manifest.
9. **Fail closed.** Nicht implementierte Refactorings, fehlende Auth, unbekannte Scopes und nicht bestandene Release-Gates liefern Fehler statt scheinbaren Erfolgs.
10. **Kein weiterer horizontaler Scope**, bis Kernpfade auf realen Repositories belegt sind.

## Konkrete Zielarchitektur

```text
cmd/graphi
  └─ cmd/internal/runtime (Composition Root + Lifecycle Owner)
       ├─ core/model + core/parse
       ├─ core/graphstore
       │    ├─ GraphLookup: Node, NodesByID, Incoming, Outgoing
       │    ├─ GraphCatalog: Nodes/Edges für Export und Diagnose
       │    └─ GraphWriter/Batcher
       ├─ engine/ingest
       │    ├─ Scanner → ParsePipeline → GraphCommitter
       │    └─ LinkPhase → ResolvePhase → RecoveryJournal
       ├─ engine/query/search/analysis (nur schmale Read-Ports)
       └─ CapabilitySet
            ├─ QueryPort / SearchPort / AgentContextPort
            ├─ AnalysisPort
            ├─ EditPort / MemoryPort / ForgePort (optional, explizit)
            └─ HealthPort / Lifecycle

Surfaces
  ├─ CLI / MCP-stdio: kleinster benötigter Port, lokale Operatorrechte
  ├─ Daemon UDS: Capability-Handshake, per-RPC Port
  └─ HTTP/SSE/MCP-HTTP
       └─ SecurityEnvelope
            Host policy → Origin policy → Bearer auth → scope authz
            → body/deadline limits → schema guard → route adapter

Contract pipeline
  Go response types + contract schema
       → generated TS package
            ├─ web
            └─ VS Code

Release pipeline
  exact SHA → all required gates → build → SBOM/attestation/signing
            → tag exact SHA → publish assets/action → consumer E2E
```

### 1. Core- und Persistenzkomponenten

#### `GraphLookup` — neuer Read-Hotpath-Port

Der heutige allgemeine `Edges(Query)`-Port bleibt zunächst als Katalog-/Exportfunktion bestehen, darf aber nicht mehr für Nachbarschaftstraversal verwendet werden. Der neue Port ist endpoint-selektiv:

```go
type GraphLookup interface {
    GetNode(context.Context, model.NodeId) (model.Node, error)
    NodesByID(context.Context, []model.NodeId) ([]model.Node, error)
    Incoming(context.Context, model.NodeId, ...model.EdgeKind) ([]model.Edge, error)
    Outgoing(context.Context, model.NodeId, ...model.EdgeKind) ([]model.Edge, error)
}
```

Verträge:

- kanonische Sortierung bleibt Bestandteil des Ports;
- SQLite nutzt gebundene `WHERE to_id = ?`/`WHERE from_id = ?`-Queries und die vorhandenen Indizes;
- der Memory-Store erhält Adjazenzindizes;
- `NodesByID` verhindert N+1-Reads bei Traversals;
- Batches/Multi-Source-Lookups werden erst nach Messung ergänzt, nicht vorsorglich.

Langfristig wird der Vollgraph-Edge-Cache nicht mehr Standard-Lesepfad. Er kann für Export/kleine Repositories vorerst bestehen; nach Migration aller Hotpaths wird entschieden, ob `cache.edges` gelöscht oder als explizit budgetierter optionaler Cache geführt wird.

#### Ingest-Pipeline

`Ingester` wird nicht neu geschrieben. Seine bereits sichtbaren Phasen werden schrittweise hinter interne Ports extrahiert:

- `Scanner`: Walk, Ignore-Policy, Ressourcengrenzen,
- `ParsePipeline`: paralleles reines Parsing,
- `GraphCommitter`: deterministische serielle Writes,
- `LinkPhase` und `ResolvePhase`: Cross-File-/Type-Auflösung,
- `MetaRepository`: kapselt Sidecar-DB statt `MetaDB()`-Escape-Hatch,
- `RecoveryJournal`: Semantik-Stamps, Dirty-State und Wiederanlauf.

Die bestehende Reihenfolge bleibt bis zu Fault-Injection-Beweisen unverändert. Die getrennten Meta-DB-/Graphstore-Commits werden als Saga formalisiert; ein gemeinsamer DB-Rewrite ist nicht Teil der ersten Tranche.

### 2. Application- und Capability-Komponenten

#### Schmale Ports

`surfaces/client.Client` wird ersetzt durch kleine Interfaces, beispielsweise:

- `QueryPort`: Query, Compound, AST, Clones,
- `SearchPort`: lexical und optional semantic,
- `AgentContextPort`: Brief, ExplainSymbol, RelatedFiles, ChangeRisk,
- `AnalysisPort`: registrierte Analyzer,
- `EditPort`: Preview, Commit, Undo, Inline, SafeDelete,
- `MemoryPort`, `ForgePort`, `DiagnosticsPort`,
- `HealthPort`: Liveness, Readiness, Runtime-Status.

Command-Handler akzeptieren jeweils den kleinsten Port. Ein Aggregat darf nur im Composition Root existieren, nicht als Pflichtinterface für Transportadapter.

#### `CapabilityManifest`

Jede Runtime publiziert maschinenlesbar:

- Capability-ID und Vertragsversion,
- `read`/`write`/`external-egress`-Klasse,
- Surface-Verfügbarkeit,
- Status `stable`, `experimental`, `disabled`,
- erforderliche Auth-Scopes,
- optionale Abhängigkeiten und Unavailable-Grund.

Router registrieren nur vorhandene Capabilities. Fehlende Methoden verschwinden aus dem Transportvertrag, statt erst zur Laufzeit `Unavailable` zu liefern. `/contract` und Daemon-Handshake verwenden dieselbe Manifestquelle.

#### Ein Composition Root

Eine Factory unter dem gerankten Cmd-Layer, z. B. `cmd/internal/runtime`, öffnet und besitzt Store, Meta-Repository, Ingester, Watcher, Broker und Capability-Services. Sie liefert Profile wie:

- `LocalRead`: CLI/MCP-stdio Kernabfragen,
- `LocalInteractive`: Read + Web/SSE,
- `LocalEdit`: explizite lokale Schreibfähigkeiten,
- `TeamCI`: deterministischer PR-Gate-Pfad,
- `Labs`: explizit experimentelle Fähigkeiten.

Die Profile werden validiert; ungültige Kombinationen scheitern beim Start. `cmd/graphi/main.go` dekodiert danach nur Argumente, wählt ein Profil und startet eine Surface.

### 3. Transport-Security und Privacy

#### Gemeinsames `SecurityEnvelope`

HTTP, SSE und ein künftig freigegebenes MCP-HTTP verwenden dieselbe Middleware-Kette:

1. erlaubter Request-Host (`127.0.0.1`, `localhost`, `[::1]` plus tatsächlich gebundener Port),
2. Origin default-deny; nur expliziter Same-Origin-/Extension-Handshake,
3. zufälliger pro Prozess erzeugter Bearer-Token, constant-time verglichen,
4. scopes aus Capability-Manifest (`graph:read`, `memory:write`, `edit:write`, `forge:publish`),
5. globales und optional routespezifisches Body-Limit,
6. Deadlines/Idle-Timeouts; SSE separat drainbar,
7. Schema-/Content-Type-Prüfung,
8. sanitizierte Fehler und datensparsame strukturierte Logs.

`/healthz` darf tokenlos reine Prozess-Liveness liefern. `/readyz`, `/contract` mit sensiblen Details und alle Datenrouten sind geschützt. Der Token wird weder in argv noch URL geschrieben. Der sichere Übergabekanal für Browser und VS Code muss als eigener Handshake spezifiziert und negativ getestet werden.

MCP-HTTP bleibt deaktiviert, bis dieses Envelope und eine read-only Default-Capability existieren. MCP-stdio und der UDS-Daemon behalten ihre transportbezogenen lokalen Schutzmechanismen; Write-Capabilities werden trotzdem explizit verhandelt.

#### Filesystem- und At-rest-Grenzen

- Memory-Export gibt auf Remote-Ports Bytes zurück; nur eine lokale CLI-Operatoraktion schreibt einen vom Nutzer angegebenen Pfad.
- Memory-Verzeichnis wird `0700`, Journal `0600`; vorhandene zu weite Modi werden beim Öffnen korrigiert.
- `SecretSuspect` löst `reject`, `redact` oder einen expliziten lokalen Override aus; bloßes Markieren und Persistieren entfällt.
- `.gitignore`-Semantik wird als explizite Produkt-/Privacy-Entscheidung dokumentiert und in sicheren Profilen standardmäßig respektiert.
- GitHub API Bases müssen saubere HTTPS-Origins sein; cross-origin Redirects mit Authorization werden blockiert.

### 4. Lifecycle und Betriebsvertrag

`Runtime` und Server implementieren einen einheitlichen Lifecycle:

```go
type Lifecycle interface {
    Ready(context.Context) Readiness
    Done() <-chan struct{}
    Shutdown(context.Context) error
}
```

- `signal.NotifyContext` und Daemon-Stop laufen in denselben Shutdown-Pfad.
- Shutdown-Reihenfolge: neue Requests stoppen → SSE/RPC drain → Watcher stoppen → Ingest abbrechen/Checkpoint sichern → Store/Meta-DB schließen → `Done` schließen.
- `Shutdown` ist idempotent und zeitbegrenzt.
- `/healthz` ist Liveness; `/readyz` prüft Store-Selfcheck, Initial-Ingest und kritischen Watcher-Zustand.
- Daemon-Status zeigt PID, Uptime, Generation, Workspace, Fähigkeiten, Readiness und letzten Ingestzustand.
- Strukturierte Logs enthalten Route, Status, Dauer und Request-ID, aber keine Symbole, Pfade, Queries, Source oder Tokens.

### 5. Frontend-/IDE-Vertrag

Ein versioniertes Contract-Artefakt wird aus kanonischen Go-Typen/Schemata erzeugt. Daraus entsteht ein gemeinsames TypeScript-Paket mit Typen, Runtime-Validatoren und Transportclient für Web und VS Code.

- Search verwendet `node_id`, `qualified_name`, `source_path`, `line`, `column`, `rank`; der heutige manuelle `{id,path,line}`-Spiegel entfällt (`extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:18-35`).
- Graphdaten verwenden Edge-ID als Identität und einen Multi-Graph; Endpoint-Deduplizierung ist verboten (`web/src/GraphView.tsx:108-118`, `core/model/edge.go:10-12`).
- Interaktive Requests erhalten Abort-/Sequenzschutz.
- Eine echte Go-Response-Fixture wird in beiden Clients getestet; Schema-Drift blockiert CI.

### 6. Release- und Distribution-Architektur

Ein einziger Commit-gebundener DAG transportiert dieselbe SHA durch:

`quality gates → real corpus/contract gates → reproducible build → SBOM/vulnerability scan → provenance/signature → tag exact SHA → publish → install/consumer E2E`.

Der Tag entsteht erst nach allen Gates. Floating Major-Tags in kritischen Actions werden durch volle SHAs ersetzt. Die GitHub Action lädt ein versioniertes, verifiziertes Graphi-Artefakt oder checkt Graphi explizit unter dem gewählten Ref in ein separates Verzeichnis aus; sie baut nie implizit aus dem Consumer-Workspace. Ein leeres Fremd-Repository und ein realistisches Consumer-Repository bilden verpflichtende E2E-Fixtures.

## Datenfluss im Zielbild

### Indexieren

1. Surface fordert über Runtime einen Indexlauf an.
2. `Scanner` erzeugt kanonisch sortierte Units unter Ignore-/Resource-Policy.
3. `ParsePipeline` parst parallel und erzeugt immutable Modelle mit Provenienz.
4. `GraphCommitter` schreibt deterministisch in Graphstore-Batches.
5. `MetaRepository` und `RecoveryJournal` markieren Saga-Fortschritt.
6. Link-/Resolve-Phasen arbeiten auf committed State.
7. Checkpoint/Semantik-Stamp wird zuletzt gesetzt; Broker publiziert nur Metadaten.
8. Readiness wechselt erst nach erfolgreichem Initiallauf auf ready.

### Query/Agent-Kontext

1. Surface authentisiert und autorisiert die konkrete Capability.
2. Adapter validiert Schema und dekodiert in einen Engine-Request.
3. Engine löst Symbol/Seed auf und ruft `Incoming`/`Outgoing` statt `Edges(kind)` auf.
4. SQLite verwendet Endpointindex; `NodesByID` lädt die Gegenseite gebündelt.
5. Engine serialisiert einmal kanonisch.
6. Surface envelopt nur Transportmetadaten; Payloadbytes bleiben für dieselbe Capability identisch.

### Mutation

1. Nur ein explizites lokales/autorisiertes Write-Profil exponiert `EditPort` oder `MemoryPort`.
2. Preview liefert Blast Radius und geplante Operationen.
3. Nicht implementierte Semantik (`extract`, `move`) scheitert vor Mutation.
4. Commit läuft über bestehende atomare Edit-Saga/Undo-Anker.
5. Remote-Dateiexport existiert nicht; CLI schreibt Rückgabebytes als separate Operatoraktion.

## Migrationsseams und Kompatibilitätsstrategie

### Seam A — `GraphLookup` neben `Reader`

- Neue Store-Methoden werden additiv eingeführt.
- Ein temporärer `LegacyLookupAdapter` darf `Edges(Query)` filtern, damit Engine-Migration paketweise möglich ist; er ist nur Test-/Kompatibilitätscode und bekommt ein Löschdatum.
- SQLite- und Memory-Implementierung laufen gegen dieselbe Contract-Suite.
- Shadow-Tests vergleichen alte und neue Ergebnisse byteweise sowie Query-Pläne/Rows-scanned.
- Erst nach Migration aller Traversal-Caller wird der Legacy-Fallback verboten.

### Seam B — kleine Ports neben `client.Client`

- Der alte Client wird temporär durch einen `LegacyClientFacade` aus kleinen Ports zusammengesetzt.
- Neue Command-/Transporthandler dürfen nur kleine Ports importieren.
- Ein statischer Guard verhindert neue Referenzen auf `client.Client` außerhalb des Facade-Verzeichnisses.
- `Unavailable`-Stubs werden capabilityweise gelöscht, sobald die letzte alte Callsite migriert ist.

### Seam C — gesicherter Router neben Legacy-Handler

- Routes werden in öffentliche Liveness und geschützte Capability-Routes geteilt.
- Tests starten ausschließlich den neuen gesicherten Handler.
- Falls für lokale Entwicklung nötig, existiert höchstens ein explizites `--insecure-loopback-dev` mit Warnung, nur in nicht-releasefähigen Builds; kein stiller Kompatibilitätsmodus.
- Schema-Version wird beim Auth-/Manifest-Wechsel erhöht; VS Code/Web werden im selben Changeset migriert.

### Seam D — Runtime-Strangler

- Zuerst öffnen Daemon und HTTP Ressourcen über `Runtime`; CLI-Readpfade folgen, Edit zuletzt.
- Bestehende `makeClient`-/`makeEditorClient`-Factories delegieren temporär an Runtime.
- Charakterisierungstests vergleichen Cleanup, Bytes und Capability-Sets.
- Nach vollständiger Umschaltung werden duplizierte Konstruktoren entfernt.

### Seam E — Frontend-Contract

- Generierte Typen werden zunächst neben manuellen Typen erzeugt und durch Compile-Time-/Fixture-Vergleich gegatet.
- Danach wechseln Web und VS Code atomar auf das Paket.
- Manuelle Payloadtypen werden gelöscht; kein dauerhafter Dual-Source-Vertrag.

### Seam F — Ingest-Extraktion

- Jede extrahierte Phase wird zunächst als dünner Wrapper um bestehende Funktionen gebaut.
- Determinismus-, Snapshot- und Fault-Injection-Tests müssen vor und nach jedem Schnitt identisch sein.
- Keine gleichzeitige Algorithmusänderung und Paketverschiebung.

## Was zuerst gebaut werden muss

Die ersten Grundlagen sind nicht UI und nicht neue Analyzer:

1. **Release-/Feature-Freeze und ehrliche Deaktivierung** falscher öffentlicher Pfade.
2. **Charakterisierungs-Gates:** kanonische Bytes, Traversal-Ergebnisse, Daemon-Prozessende, Auth-Negativtests, Consumer-Action-E2E.
3. **Threat Model und Capability-Inventar** mit Read/Write/Egress-Klassifikation.
4. **`GraphLookup`-Contract-Suite und realistische Performancebudgets.**
5. **Kleine Ports + CapabilityManifest.**
6. **Runtime/Lifecycle-Abstraktion.**

Ohne diese Grundlagen würden UI-, Security- und Distributionfixes erneut an voneinander abweichende Service-Graphen gekoppelt.

## Priorisierter Schritt-für-Schritt-Plan

### Phase 0 — Schaden begrenzen und Baseline einfrieren

1. Automatische Releases pausieren, GitHub Action nicht extern als shipped bewerben.
2. `extract` und `move` fail-closed schalten oder aus stabilen Deskriptoren entfernen; `safe-delete`/`inline` korrekt als eingeschränkt kennzeichnen.
3. Neue Sprachen, Analyzer, Tools und Surfaces einfrieren.
4. Aktuelle Core-Snapshots, kanonische Responses und Capability-Sets als Charakterisierungsfixtures sichern.
5. Threat Model, unterstützte Angreifer und GA-Capabilities entscheiden.

**Exit:** Keine bekannte falsche Schreibsemantik oder ungesicherte Remote-Mutation wird als stabil ausgeliefert.

### Phase 1 — Read-Hotpath und Messbarkeit

1. `GraphLookup` und gemeinsame Contract-Suite definieren.
2. Memory-Adjazenzindizes sowie SQLite-`Incoming`/`Outgoing`/`NodesByID` implementieren.
3. Caller, Callee, References, Definition und Agent-Kontext paketweise migrieren.
4. Shadow-Vergleich und Millionen-Kanten-Benchmark mit Latenz-, Heap- und Rows-scanned-Budget einführen.
5. Vollgraph-Scans im Traversalpfad statisch/testseitig verbieten.

**Exit:** Kerntraversals skalieren mit Knotengrad statt Kantenklasse und liefern alte kanonische Bytes.

### Phase 2 — Capability-Segregation und Composition Root

1. Kleine Ports und Manifest-Schema einführen.
2. Legacy-Facade aus Ports zusammensetzen.
3. `cmd/internal/runtime` mit validierten Profilen und zentralem Resource Ownership bauen.
4. Daemon und HTTP auf Runtime/Manifest migrieren; CLI/MCP-stdio danach.
5. Unavailable-Stubs und duplizierte Factories löschen.
6. Layerguard um erlaubte Same-Layer-Paketkanten und ein Verbot neuer Legacy-Client-Abhängigkeiten ergänzen.

**Exit:** Jede Surface exponiert nur echte Fähigkeiten; Store/Watcher/Broker werden genau einmal konstruiert und geschlossen.

### Phase 3 — Security- und Privacy-Teil-Rewrite

1. SecurityEnvelope mit Auth, Host, Origin, Scopes, Limits und Error Policy implementieren.
2. HTTP/SSE auf den gesicherten Router umstellen; Token-Handshake für Web/VS Code bauen.
3. MCP-HTTP read-only und standardmäßig deaktiviert wieder einführen; Mutationen nur per explizitem Scope.
4. Remote-Dateiexport entfernen; Memory-Modi und Secret-Policy migrieren.
5. GitHub-Origin-/Redirect-Policy härten.
6. Negative Integrationstests für fehlenden/falschen Token, DNS-Rebinding-Host, Origin, Oversize Body und Scope Escalation hinzufügen.

**Exit:** Keine sensible Route ist ohne Auth erreichbar; Write-/Egress-Capabilities sind explizit autorisiert.

### Phase 4 — Lifecycle und gemeinsame Clientverträge

1. `Done`, `Ready`, idempotentes `Shutdown` und Signalsteuerung implementieren.
2. Daemon-Stop-E2E als echter Subprozess testen; HTTP/SSE-Drain testen.
3. `/readyz` und datensparsame strukturierte Betriebsereignisse ergänzen.
4. gemeinsames TS-Contract-Paket generieren und in Web/VS Code integrieren.
5. VS-Code-Suche, Web-Multiedges und Stale-Request-Schutz reparieren.

**Exit:** Prozesse enden deterministisch; beide TS-Clients akzeptieren echte Go-Payloads ohne Datenverlust oder Contract-Drift.

### Phase 5 — Release und Distribution neu schließen

1. Einen autoritativen SHA-gebundenen Release-DAG bauen.
2. Third-Party Actions pinnen, Vulnerability-Gate, SBOM und Provenienz/Signatur ergänzen.
3. Action auf wirklich versioniertes Binary/Source umstellen.
4. Consumer-Repo-E2E, Installer-E2E und Artefaktverifikation verpflichtend machen.
5. Distributionsmatrix in `code`, `default build`, `release binary`, `published`, `experimental` aufteilen.

**Exit:** Kein Tag/Asset ohne alle Gates; die veröffentlichte Action funktioniert außerhalb des Graphi-Repositories.

### Phase 6 — Ingest modularisieren und Real-World-Gates

1. MetaRepository und Phase-Wrapper extrahieren.
2. RecoveryJournal formalisieren und Crashpunkte fault-injecten.
3. Scanner, ParsePipeline, GraphCommitter, Link/Resolve schrittweise aus `Ingester` lösen.
4. Spring-Boot-Fullrun und repräsentative reale Repositories mit Wallclock, Peak-RAM, DB-Größe, Time-to-first-query und Signalqualität messen.
5. Nur belegte GA-Fähigkeiten auftauen; Labs bleiben separat.

**Exit:** Ingest-Saga ist testbar und verständlich; Kernversprechen besitzt reale, reproduzierbare Budgets.

## Konkreter Task-Backlog

### Aufwandsskala

| Größe | Definition |
|---|---|
| **S** | 1–3 Engineer-Tage; lokal, bekannter Vertrag, geringe Migration. |
| **M** | 4–10 Engineer-Tage; mehrere Dateien/Pakete, neue Tests oder ein Adapter. |
| **L** | 2–4 Engineer-Wochen; systemübergreifender Vertrag, mehrere Consumer, E2E-Gates. |
| **XL** | 5–8 Engineer-Wochen; mehrere gekoppelte Subsysteme oder externe Distribution. Muss vor Umsetzung in S/M/L-Tasks zerlegt werden. |

### Tasks, Abhängigkeiten und Abnahmekriterien

| ID | Task | Größe | Abhängigkeiten | Abnahmekriterien |
|---|---|---:|---|---|
| A00 | Release-/Feature-Freeze und Stable/Labs-Liste | S | – | Auto-Publish pausiert; Action nicht als shipped; keine neuen horizontalen Capabilities; Owner und Löschdatum je eingefrorenem Pfad dokumentiert. |
| A01 | Falsche Edit-Semantik fail-closed | S | A00 | `extract`/`move` mutieren keine Datei; alle Surfaces liefern denselben typed unavailable/not-implemented Fehler; negative Tests beweisen null Writes. |
| A02 | Threat Model + Capability-Klassifikation | M | A00 | Lokaler Prozess, anderer OS-User, Browser/DNS-Rebinding, MCP-Client und Supply Chain bewertet; jede Capability hat Read/Write/Egress, Stabilität und Scope. |
| A03 | Architektur- und Characterization-Gates | M | A00 | Golden Bytes für Kernqueries; Capability-Snapshot; Daemon-Subprozess-Harness; HTTP-Negativtest-Harness; aktuelle Snapshot-Kompatibilität gesichert. |
| Q10 | `GraphLookup`-Port + Contract-Suite | M | A03 | Kanonische Reihenfolge, Cancellation, Missing-Node- und Multi-Kind-Semantik für alle Backends festgelegt; keine Engine-Abhängigkeit im Core. |
| Q11 | Memory-Adjazenzindizes | M | Q10 | Incoming/Outgoing ohne Vollscan; Put/Delete aktualisieren Indizes atomar; Contract- und Race-Tests grün. |
| Q12 | SQLite endpoint-selektive Reads | L | Q10 | Queries nutzen `from_id`/`to_id`; `EXPLAIN QUERY PLAN` belegt Index; `NodesByID` ist gebündelt; Ergebnisse bytegleich zum Legacy-Pfad. |
| Q13 | Kerntraversals migrieren | L | Q11,Q12 | Caller/Callee/References/Definition und Agent-Kern verwenden kein `Edges(Query{EdgeKind})`; Shadow-Tests grün; Performancebudget erfüllt. |
| Q14 | Cache-Entscheidung und Budget | M | Q13 | Gemessene Entscheidung für Löschen, Begrenzen oder explizites Opt-in von `cache.edges`; Peak-RAM-Gate auf realistischer Fixture. |
| C20 | Kleine Capability-Ports | M | A02,A03 | Handler kompilieren gegen kleinsten Port; Legacy-Facade ist einzige neue Stelle, die das alte Gesamtinterface kennt. |
| C21 | Versioniertes CapabilityManifest | M | C20,A02 | `/contract` und Daemon-Handshake stammen aus einer Quelle; Read/Write/Egress/Status/Scope maschinenlesbar; fehlende Fähigkeiten werden nicht geroutet. |
| C22 | Zentraler Runtime-Composition-Root | L | C20,C21 | Ein Owner für Store, Meta-DB, Watcher, Broker; Profile validiert; Daemon/HTTP nutzen dieselbe Factory; Cleanup-Test beweist genau ein Close. |
| C23 | CLI/MCP migrieren und Legacy-Client löschen | L | C22,Q13 | Alle Handler auf kleinen Ports; `client.Client`, Unavailable-Stubs und duplizierte `make*Client`-Verdrahtung entfernt; Paritätstests grün. |
| G24 | Layerguard um Paketregeln erweitern | M | C20 | Same-Layer-Allowlist/Regeln sind CI-geprüft; neue Legacy-Facade- oder Engine-Querabhängigkeiten schlagen fehl; bestehende erlaubte Kanten dokumentiert. |
| S30 | SecurityEnvelope | L | A02,C21 | Host/Origin/Auth/Scopes/Limits/Schema/Error-Policy in einer Middleware; `/healthz` einzige bewusst öffentliche Route; Negativmatrix vollständig grün. |
| S31 | Sicherer Web-/VS-Code-Token-Handshake | L | S30,T41 | Token nicht in argv/URL/Logs; Rotation pro Prozess; falscher/alter Token abgewiesen; Browser- und Extension-E2E grün. |
| S32 | MCP-HTTP Capability-Sandbox | M | S30,C21 | Default disabled/read-only; Mutationen erfordern Scope; unauthentisierte und Scope-Escalation-Calls scheitern; Body > Limit wird explizit abgewiesen. |
| S33 | Memory/Export Privacy-Migration | M | A02,C20 | Remote-Export liefert Bytes; Journal 0600/Parent 0700 inkl. Migration; Secret-Policy reject/redact/override getestet; kein arbitrary path write. |
| S34 | Forge-Origin-Härtung | S | A02 | Nur saubere HTTPS-Origin; Authorization folgt keinem Cross-Origin-Redirect; Enterprise-Allowlist testbar. |
| L40 | Gemeinsamer Runtime-Lifecycle | L | C22 | Signal und Stop-RPC nutzen einen Pfad; Shutdown idempotent und timeoutgebunden; Reihenfolge getestet; keine Handles/Watcher nach Exit. |
| L41 | Readiness und datensparsame Observability | M | L40 | `/healthz`/`readyz` getrennt; Store/Ingest/Watcher in Readiness; Logs enthalten keine Pfade, Queries, Source oder Token; Status zeigt Capabilities. |
| T40 | Kanonischer Generator für TS-Vertrag | L | C21,A03 | Go-Response-Fixtures generieren Typen + Runtime-Validatoren; Drift-Check blockiert CI; Web und VS Code verwenden dasselbe Paket. |
| T41 | VS-Code-Suche korrigieren | S | T40 | `node_id`, `qualified_name`, `source_path` korrekt; QuickPick/Navigation mit echter Go-Fixture getestet. |
| T42 | Web-Multiedges + Request-Cancellation | M | T40 | Jede Edge-ID bleibt erhalten; Multi-Edge-Test ohne GraphView-Mock; ältere Responses können neuen State nicht überschreiben; unnötige Voll-Rebuilds gemessen reduziert. |
| R50 | Autoritativer Release-DAG | L | A00,A03 | Exakte SHA durch alle Jobs; Publish braucht alle Gates; rotes Release-Gate kann weder Tag noch Asset erzeugen; idempotenter Retry. |
| R51 | Supply-Chain-Artefakte | L | R50 | Actions SHA-gepinnt; Vulnerability-Gate; SBOM; signierte/attestierte Provenienz; Installer verifiziert Herkunft, nicht nur gleich-origin Hash. |
| R52 | GitHub Action Distribution neu bauen | L | R50,R51 | `graphi-version` bestimmt nachweislich Binary/Source; Fremd-Repo ohne `cmd/graphi` funktioniert; Working Directory korrekt; echte JSON-Ausgabe ohne `grep`/`sed`; Consumer-E2E required. |
| I60 | `MetaRepository` und Ingest-Phasenports | L | C22,Q13 | Kein neuer `MetaDB()`-Zugriff; Extraktion ändert Bytes/Reihenfolge nicht; jede Phase einzeln testbar. |
| I61 | RecoveryJournal + Fault Injection | L | I60 | Jeder Commit-/Crashpunkt getestet; Wiederanlauf konvergiert zu Full-Reindex; Dirty-/Semantik-Stamp-Zustände dokumentiert; kein stiller Mischzustand. |
| V70 | Reale Performance-/Outcome-Gates | XL | Q13,L40,R52 | Reproduzierbarer Spring-Boot-Fullrun plus mehrere reale Repos; Wallclock, Peak-RAM, DB, First Query, Ergebnisqualität veröffentlicht; Agent-Aufgaben mit/ohne Graphi messen Outcome statt nur Tokens. |
| D80 | Legacy- und Labs-Bereinigung | M | C23,S32,T42,R52,V70 | unten genannte Altteile gelöscht; Capability-/Distributionsmatrix stimmt mit Binaries; nur belegte GA-Fähigkeiten aufgetaut. |

### Kritischer Pfad

`A00 → A03 → Q10 → Q12 → Q13 → C20/C21 → C22 → S30 + L40 → T40/S31 → R50/R52 → V70 → D80`

Parallel möglich:

- A01 unmittelbar nach A00,
- S33/S34 nach A02 und C20,
- T41/T42 nach T40,
- R51 parallel zur Action-Neufassung nach R50,
- I60/I61 erst nach stabiler Runtime und Read-API; nicht auf dem ersten Security-/Release-Critical-Path.

## Zu löschende oder einzufrierende Architekturteile

### Sofort einfrieren/deaktivieren

- neue Analyzer, Sprachen, Tools und Surfaces;
- `extract`, `move` und irreführend „safe“ benannte Editpfade als Stable-Capabilities;
- MCP-HTTP als Produktionsoberfläche, bis Auth/Scopes vorhanden sind;
- automatische Releases im aktuellen entkoppelten DAG;
- externe Bewerbung der aktuellen GitHub Action;
- PR-, Memory-, Distill-, Skillgen- und breite Security-Vertikalen im GA-Versprechen; Code darf vorerst in `Labs` verbleiben;
- neue Fullgraph-Consumer von `Edges(Query{})`.

### Nach erfolgreicher Migration löschen

- monolithisches `surfaces/client.Client` und die zugehörigen `Unavailable`-Stub-Methoden;
- duplizierte Composition-Roots in `makeClient`, `makeEditorClient`, Daemon und HTTP;
- nackter HTTP-Mux als exportierter Produktionshandler und doppelte Loopback-Prüflogik;
- Remote-`export_to_path`-Filesystemprimitive;
- handkopierter VS-Code-Payloadvertrag und doppelte TS-Transportclients;
- Endpoint-Deduplizierung `!g.hasEdge(from,to)` im Web;
- heutiger `select {}`-Daemon-Block und unkontrolliertes `Serve` ohne Shutdown;
- aktueller `auto-release`-Workflow, sobald der autoritative DAG produktiv ist;
- Action-Build aus `./cmd/graphi` im Consumer-Workspace;
- temporäre `LegacyLookupAdapter`-/`LegacyClientFacade`-Seams nach ihren Löschgates;
- aspirative Dokumentationsbehauptung, alle Surfaces könnten niemals divergieren.

### Nur nach Messung löschen oder ändern

- vollständiger SQLite-`memGraph.edges`-Cache: zunächst Hotpath umgehen, dann anhand RAM/Latenz entscheiden;
- Meta-Sidecar-DB: Saga zuerst formalisieren; Zusammenlegung in eine DB nur bei belegtem Recovery-/Betriebsvorteil;
- Parser-/Analyzer-Registries und bestehende kanonische Serializer: ausdrücklich behalten, sofern Contract-Gates grün bleiben.

## Risiken und Gegenmaßnahmen

| Risiko | Wahrscheinlichkeit / Auswirkung | Gegenmaßnahme |
|---|---|---|
| Kanonische Bytes ändern sich beim neuen Read-Port | M / H | gemeinsame Contract-Suite, Shadow-Reads, Golden Bytes, deterministische Sortierung im Port. |
| SQLite-Einzelqueries erzeugen N+1-Regressionskosten | M / H | `NodesByID`, Query-Plan-Gates, rows-scanned-/Latenzbudgets, erst gemessene Batch-Erweiterung. |
| Security-Neufassung bricht Browser-/Extension-Onboarding | H / M | sicherer Handshake als eigenes Produktfeature, E2E in Web und VS Code, klare Fehler statt stiller Fallback. |
| Capability-Segregation erzeugt vorübergehend doppelte APIs | H / M | eine Legacy-Facade, statischer Importguard, Owner/Löschdatum, keine neuen Legacy-Caller. |
| Runtime-Migration verändert Cleanup oder Ingest-Reihenfolge | M / H | Charakterisierung vor Migration, Subprozess-/Fault-Tests, keine gleichzeitige Algorithmusänderung. |
| Ingest-Saga verliert Zustand an Cross-DB-Crashpunkten | M / H | RecoveryJournal, Dirty-State-Matrix, Fault Injection an jedem Commitseam. |
| Release-Pipeline wird komplexer und langsamer | H / M | reusable Jobs, Caches, ein DAG, schnelle PR-Gates vs. vollständiges Publish-Gate trennen, gleiche SHA bewahren. |
| Scope-Freeze wird organisatorisch umgangen | H / H | GA-/Labs-Manifest maschinenprüfen; neue Capabilities brauchen reale Akzeptanzmetrik und Architekturfreigabe. |
| Bestehende Nutzer hängen an unauthentisiertem HTTP | UNKNOWN / H | Major/Schema-Versionierung, explizite Migration; kein stiller unsicherer Compatibility-Modus. |
| Windows-Lifecycle/Transport bleibt ungetestet | M / M | Windows-CI und explizite Transportstrategie vor Daemon-Supportclaim. |
| Aufwandsplan unterschätzt Client-/Docs-/Releasekopplung | M / M | XL-Epics vor Start zerlegen, vertikale dünne Slices, Exit-Kriterien pro Phase. |

## Review- und Codebelege

Die Planung basiert auf allen Berichten unter `temp/review/*.md` und einer direkten Read-only-Prüfung der entscheidenden Pfade. Maßgebliche Belege:

| Architekturentscheidung | Code-/Reviewbeleg |
|---|---|
| Core behalten | `core/model/edge.go:48-113`; `core/parse/registry.go:10-100`; `core/graphstore/graphstore.go:55-204`; Reviews `00`, `01`, `02`. |
| Traversal-Port neu bauen | `core/graphstore/graphstore.go:41-53`; `engine/query/service.go:145-173`; `core/graphstore/sqlite.go:187-192`, `841-866`; Reviews `00`, `01`. |
| Capability-Ports statt Monolith | `surfaces/client/client.go:268-464`; `surfaces/client/direct.go:34-155`; `surfaces/daemon/client.go:203-301`, `343-382`; Reviews `00`, `01`. |
| zentraler Composition Root | `cmd/graphi/main.go:181-188`, `1002-1046`, `1111-1138`, `1195-1248`; Review `01`. |
| SecurityEnvelope erforderlich | `surfaces/http/server.go:181-212`, `251-267`, `298-312`, `698-745`; `surfaces/mcp/http.go:16-49`; Review `04`. |
| Memory-/Exportgrenze neu schneiden | `engine/memory/memory.go:101-117`, `239-317`; `surfaces/client/direct.go:658-677`; Reviews `00`, `04`. |
| Lifecycle vereinheitlichen | `surfaces/daemon/daemon.go:170-186`; `cmd/graphi/main.go:1138-1145`, `1237-1252`; Reviews `00`, `05`. |
| TS-Vertrag generieren | `extensions/vscode/src/contract.ts:104-107`; `engine/search/service.go:18-35`; `web/package.json:9-14`; Review `02`. |
| Web muss Parallelkanten erhalten | `web/src/GraphView.tsx:108-118`; `core/model/edge.go:10-12`, `76-80`; Reviews `00`, `02`. |
| Refactoring fail-closed | `engine/edit/refactor.go:174-186`; `surfaces/cli/cli.go:65-90`; Reviews `00`, `02`, `03`. |
| Release-DAG neu koppeln | `.github/workflows/auto-release.yml:36-101`; `.github/workflows/release-gate.yml:1-30`; `.github/workflows/release.yml:11-34`; Reviews `00`, `05`. |
| Action-Distribution ersetzen | `extensions/github-action/action.yml:101-125`; `extensions/github-action/README.md:71-107`; Reviews `00`, `05`, `06`. |
| Same-Layer-Regeln ergänzen | `internal/layerguard/guard.go:74-85`, `129-147`; `docs/architecture-plan.md:39-47`; Reviews `01`, `02`. |
| Feature-/Surface-Freeze | `docs/coverage-matrix.md:15`, `72-177`; `docs/real-world-report.md:9-40`; Reviews `00`, `03`, `06`. |

## UNKNOWN und vor Umsetzung zu klärende Entscheidungen

Die folgenden Punkte sind nicht durch das Repository belegt und dürfen nicht still angenommen werden:

1. **Lastprofil:** reale Graphgrößen, Gradverteilungen, Query-Mix, Peak-RAM und der konkrete Kipppunkt des Vollgraph-Caches.
2. **Kompatibilität:** Zahl und Version bestehender HTTP-, Daemon-, VS-Code-, Action- und MCP-HTTP-Consumer; tolerierbares Breaking-Change-Fenster.
3. **Threat Model:** ob andere lokale Prozesse, andere OS-User, DNS-Rebinding und kompromittierte MCP-Clients bislang bewusst außerhalb des Modells lagen.
4. **Windows:** unterstützter Daemontransport und reales Lifecycle-Verhalten; heutiger Code ist UDS-zentriert.
5. **Recovery:** vollständige Crashmatrix über Meta-DB- und Graphstore-Commits; keine umfassende Fault-Injection-Evidenz liegt vor.
6. **Performance nach Fixes:** Spring-Boot-Wallclock, finale DB-Größe, Peak-RAM und Time-to-first-query wurden nicht real vollständig nachgemessen.
7. **Capability-Nutzung:** reale Nutzung und Stabilitätsanforderungen von PR, Memory, Distill, Skillgen, Refactor und breiten Analyzern.
8. **Distribution:** tatsächliche Marketplace-/Action-/Homebrew-/Scoop-Nutzung und Veröffentlichungsstatus.
9. **Release-Governance:** Branch Protection, Required Checks, Environments, Approvals, Secrets und externe Attestations außerhalb des Repos.
10. **SLOs:** akzeptierte Latenz-, Speicher-, Ingest-, Shutdown- und Recovery-Budgets.
11. **Secret-Policy:** ob Default `reject` oder `redact` sein soll und welche expliziten lokalen Overrides zulässig sind.
12. **Auth-UX:** sicherer Token-Übergabekanal für eingebettete Web-UI und VS Code ohne URL/argv/Log-Leak.
13. **Datenklassifikation/Retention:** welche Graph-, Meta- und Memory-Daten wie lange gespeichert, exportiert und gelöscht werden dürfen.
14. **Business-/Produktpriorität:** validierter ICP, Käufer, Zahlungsbereitschaft und ob Team-CI tatsächlich der nächste bezahlbare Workflow ist.
15. **Maintainerkapazität:** verfügbare Engineers, Supportlast und Ownership je Migrationsstrang.

Diese UNKNOWNs blockieren nicht den Beginn von Phase 0/1. Sie blockieren jedoch GA-Claims, exakte Kapazitätsziele, Auth-Kompatibilitätszusagen und das Auftauen experimenteller Vertikalen.

## Definition of Done für den Teil-Rewrite

Der Architekturumbau ist erst abgeschlossen, wenn alle folgenden Aussagen durch Gates belegt sind:

- Kerntraversals verwenden endpoint-selektive Reads und erfüllen reale Latenz-/RAM-Budgets.
- Jede Surface veröffentlicht echte Capabilities; kein Pflichtinterface wird mit Unavailable-Stubs vorgetäuscht.
- Store, Meta-DB, Watcher, Broker und Server haben einen zentralen Owner und deterministischen Shutdown.
- Alle repository-sensitiven HTTP/SSE/MCP-HTTP-Routen sind authentisiert, Host-/Origin-geprüft, limitiert und scope-autorisiert.
- Remote-Transporte besitzen keine generische lokale Dateischreibprimitive.
- Memory-Dateirechte und Secret-Policy sind sicher migriert.
- Web und VS Code verwenden denselben generierten Vertrag; echte Go-Fixtures und Drift-Gates sind grün.
- Parallelkanten bleiben erhalten, Stale Responses überschreiben keinen neuen UI-State.
- `extract`/`move` sind entweder korrekt implementiert oder nachweislich fail-closed und nicht als stable beworben.
- Daemon-Stop und SIGTERM beenden den realen Prozess; HTTP/SSE drainen begrenzt.
- Kein Release kann bei rotem Gate entstehen; Action und Installer funktionieren in echten Consumer-Umgebungen mit verifizierter Herkunft.
- Legacy-Facades, duplizierte Factories und der alte Releasepfad sind gelöscht.
- Reale Repo-Gates veröffentlichen Rohdaten für Wallclock, RAM, DB-Größe, First Query und Ergebnisqualität.

Erst danach sollte horizontaler Feature-Ausbau wieder zugelassen werden.
