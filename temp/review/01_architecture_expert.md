# Architektur-Review

## Kurzurteil

Graphi hat einen deutlich überdurchschnittlich disziplinierten Architekturkern: ein kleines unveränderliches Domänenmodell, eine explizite Persistenzabstraktion, registrierbare Parser und Analyzer, deterministische Serialisierung sowie maschinell geprüfte Layer-Richtung. Das ist kein zufällig gewachsenes Repository. Die deklarierte Hauptachse `cmd → surfaces → engine → core` ist im Code erkennbar und grundsätzlich tragfähig (`docs/architecture-plan.md:16-38`, `internal/layerguard/guard.go:32-57`).

Brutal ehrlich: Für den eigenen Anspruch einer Code-Intelligence-Engine für große Repositories ist die aktuelle Read-Architektur noch nicht tragfähig. Der Store hält den gesamten Graphen als Cache im RAM und die wichtigste Nachbarschaftsabfrage lädt bzw. scannt alle Kanten einer Art, statt die vorhandenen `from_id`/`to_id`-Indizes zu nutzen (`core/graphstore/sqlite.go:24-40`, `core/graphstore/sqlite.go:841-866`, `engine/query/service.go:145-173`). Gleichzeitig ist die angeblich dünne Surface-Seam zu einem 28 Methoden breiten Capability-Monolithen angewachsen, dessen Remote-Implementierungen viele Methoden nur mit `Unavailable` erfüllen (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`, `surfaces/daemon/client.go:343-382`). Damit sind Modulgrenzen formal vorhanden, aber nicht mehr fein genug, um weiteres Feature-Wachstum sauber aufzunehmen.

## Score 0–10

**5,5 / 10**

Begründung: 8/10 für Architekturabsicht, Domäneninvarianten und Test-/Conformance-Disziplin; 3/10 für Query-Skalierung und reale Capability-Komposition; 5/10 für langfristige Änderbarkeit. Der gewichtete Gesamtzustand ist ein guter Kern mit zwei systemischen Engpässen, nicht ein Rewrite-Kandidat.

## Größte Stärken

1. **Klare Abhängigkeitsrichtung mit automatischem Guard.** Die vier Schichten sind explizit gerankt; der Guard analysiert reale Go-Imports und meldet aufwärts gerichtete Kanten (`internal/layerguard/guard.go:32-57`, `internal/layerguard/guard.go:95-157`). Das verhindert die gefährlichste Form architektonischer Erosion.

2. **Starker, unveränderlicher Domänenkern.** Eine Kante kann ohne gültige Confidence-Stufe, Confidence, Reason und Evidence nicht konstruiert werden; Felder sind privat und Evidence wird defensiv kopiert und kanonisch sortiert (`core/model/edge.go:15-53`, `core/model/edge.go:68-113`, `core/model/edge.go:116-135`). Damit liegt eine wichtige Datenqualitätsinvariante an der richtigen Stelle: im Modell, nicht in einzelnen Adaptern.

3. **Sinnvolle Ports für Persistenz und reine Reads.** `Graphstore` definiert deterministische Semantik und wird zusätzlich in kleinere Fähigkeiten wie `Writer`, `Batcher` und `Batch` zerlegt (`core/graphstore/graphstore.go:55-146`, `core/graphstore/graphstore.go:162-204`). `query.Service` hängt nur an einem read-only `Reader`, nicht am mutierenden Store (`engine/query/service.go:57-76`). Das ist gute Testbarkeit und korrekt gerichtete Abhängigkeit.

4. **Erweiterbarkeit an den richtigen Hotspots.** Die Parserauswahl ist eine threadsichere Registry und neue Parser können registriert werden, ohne den Dispatcher zu verändern (`core/parse/registry.go:10-38`, `core/parse/registry.go:48-100`). Dasselbe Muster wird für Analyzer und einen einzigen Dispatch-Einstieg genutzt (`engine/analysis/dispatch.go:42-59`, `engine/analysis/dispatch.go:162-179`).

5. **Durability- und Determinismusgedanke ist tief verankert.** SQLite ist autoritativ, Writes gehen zuerst dorthin, der Cache ist evictable, WAL und Single-Writer-Disziplin sind dokumentiert (`core/graphstore/sqlite.go:24-46`, `core/graphstore/sqlite.go:65-98`). Full-Ingest parst parallel, committed aber seriell in stabiler Reihenfolge (`engine/ingest/ingest.go:622-629`, `engine/ingest/ingest.go:656-720`). Das ist eine starke Grundlage für reproduzierbare Ergebnisse.

6. **Surface-Parität wird zumindest für zentrale Pfade real getestet.** Der Test führt dieselbe Query über CLI und MCP aus und vergleicht Bytes (`surfaces/parity_test.go:1-5`, `surfaces/parity_test.go:136-160`). Das ist wesentlich besser als bloße Architekturprosa.

## Größte Schwächen

1. **Die Read-Architektur skaliert asymptotisch schlecht.** `graphstore.Query` kann Kanten nur nach Art/Text filtern, nicht nach `From` oder `To` (`core/graphstore/graphstore.go:41-53`). `directedLookup` holt daher alle Kanten eines Typs und filtert erst im Service (`engine/query/service.go:145-173`). Die SQLite-Implementierung bedient `Edges` aus dem vollständigen RAM-Cache und iteriert/sortiert die gesamte Ergebnismenge (`core/graphstore/sqlite.go:841-866`). Die Datenbank besitzt zwar `edges_from_id` und `edges_to_id`, aber genau diese Indizes werden für Traversals nicht verwendet (`core/graphstore/sqlite.go:187-192`). Ergebnis: typische Caller/Callee-Queries sind O(E_kind) statt näherungsweise O(log E + degree). Bei Millionen Kanten wird der beworbene Hot-Daemon nicht durch SQLite beschleunigt, sondern durch Vollscan und Vollgraph-RAM begrenzt.

2. **`Client` ist ein Capability-Monolith, keine dünne Transport-Seam mehr.** Das Interface bündelt Query, Search, Analyse, Mutation, Memory, Distillation, Skill-Generation, Diagnose und Forge/PR-Funktionen (`surfaces/client/client.go:268-464`). `Direct` importiert entsprechend 17 interne Subsysteme und hält elf optionale Dependencies (`surfaces/client/direct.go:3-32`, `surfaces/client/direct.go:34-52`). Jede neue Fähigkeit zwingt alle Transportadapter zur Änderung, auch wenn sie diese Fähigkeit prinzipbedingt nicht anbieten.

3. **„One engine, many surfaces“ ist nur teilweise wahr.** Der Architekturplan behauptet, Oberflächen könnten nicht divergieren (`docs/architecture-plan.md:31-35`). Tatsächlich fehlen dem Daemon Edit, Review, Memory, Distill, SkillGen, Agent-Tools und Diagnose (`surfaces/daemon/client.go:203-301`); Forge und neue PR-Analysen fehlen ebenfalls (`surfaces/daemon/client.go:343-382`). HTTP implementiert dasselbe breite Interface überwiegend mit `Unavailable`, inklusive sämtlicher Mutationen und mehrerer Read-Capabilities (`surfaces/client/http_client.go:304-353`, `surfaces/client/http_client.go:393-447`). Byte-Parität für gemeinsam unterstützte Operationen ist real; Capability-Parität aller Oberflächen ist es nicht. Die Dokumentation vermischt beides.

4. **Der Layerguard schützt nur Makro-Layer, nicht Modulgrenzen innerhalb eines Layers.** Obwohl Kommentar und Typ von „upward/sideways“ sprechen, ist ausschließlich `importedRank > importerRank` verboten; Same-Layer-Imports werden als erlaubt verbucht (`internal/layerguard/guard.go:74-85`, `internal/layerguard/guard.go:133-147`). Dadurch können `engine/*`-Pakete frei einander koppeln. Beispiele sind `engine/analysis` → `engine/community` und `engine/query` sowie `engine/wiki` → `engine/community` (`engine/analysis/communities.go:11-17`, `engine/wiki/wiki.go:20-24`). Go verhindert Zyklen, aber nicht eine langfristig unübersichtliche Feature-Matrix.

5. **Komposition ist im 1.714-Zeilen-Entrypoint verteilt und mehrfach aufgebaut.** Der Command-Switch allein listet mehr als 40 Verben (`cmd/graphi/main.go:51-169`). Ein einfacher Client wird separat gebaut (`cmd/graphi/main.go:181-188`), der Editor-Client initialisiert Store, Ingester, Full-Ingest, Consistency Checker, Applier und Recorder nochmals (`cmd/graphi/main.go:1002-1046`), während Daemon und HTTP wieder eigene Service-Graphen bauen (`cmd/graphi/main.go:1111-1138`, `cmd/graphi/main.go:1195-1248`). Das erhöht das Risiko unterschiedlicher Profile, Capability-Sets, Lifetimes und Cleanup-Semantik.

6. **Ingest ist ein God Object und enthält zu viele Verantwortungen.** `Ingester` hält Store, Parser, Meta-DB, Linker, Profil, Bounds, Diagnostics, Event-Broker, Progress/Heartbeat, Worker-Konfiguration, Ignore-State und Test-Hooks (`engine/ingest/ingest.go:128-192`). `IngestAll` orchestriert Walk, Parallel-Parse, Meta-Transaktion, mehrere unabhängige Graph-Batches, Reverse-Deps, Linker, Type-Resolution, Taint-Persistenz und Checkpoint (`engine/ingest/ingest.go:607-809`). Diese zentrale Orchestrierung ist nachvollziehbar, aber Änderungskopplung und Fehlerflächen wachsen mit jeder neuen Analysephase.

7. **Zwei Persistenzdomänen bilden eine komplexe, nicht atomare Saga.** Der Ingest führt eine Transaktion auf der Meta-Sidecar-DB aus und committed darin mehrere separate Graphstore-Batches (`engine/ingest/ingest.go:635-670`, `engine/ingest/ingest.go:718-754`, `engine/ingest/ingest.go:757-777`). Das kann korrekt recoverbar sein, ist aber keine gemeinsame atomare Transaktion. Crash-Korrektheit hängt deshalb vollständig von Dirty-Flags, Semantik-Stamps und Recovery-Reihenfolge ab, nicht von ACID über den Gesamtzustand.

## Kritische Blocker

1. **Blocker für „große Repositories“: endpoint-selektive Traversal-API fehlt.** Vor weiterem Analyzer-Ausbau braucht `Reader`/Graphstore APIs wie `Outgoing(ctx, node, kinds...)`, `Incoming(...)` und idealerweise batched/multi-source Varianten. SQLite muss diese direkt über `edges_from_id`/`edges_to_id` beantworten; der RAM-Backend-Vertrag kann passende Adjazenzindizes halten. Solange der Kernquery alle Kanten scannt (`engine/query/service.go:145-173`), sind zusätzliche Features Multiplikatoren eines falschen Kostenmodells.

2. **Blocker für konsistente Oberflächen: Capability-Komposition fehlt.** Ein einziges Interface erzwingt vorgetäuschte Implementierung durch Fehler-Stubs (`surfaces/client/client.go:271-464`, `surfaces/daemon/client.go:203-301`). Das muss in kleine Ports (`QueryClient`, `SearchClient`, `AnalysisClient`, `EditClient`, `AgentClient`, `ForgeClient`) und ein explizites Capability-Manifest zerlegt werden. Dann kann jede Surface ihren echten Vertrag implementieren, statt formal `Client` zu erfüllen und zur Laufzeit zu scheitern.

3. **Blocker für weiteres Feature-Wachstum: kein zentraler Composition Root.** Die wiederholte Konstruktion in `makeClient`, `makeEditorClient`, Daemon und HTTP (`cmd/graphi/main.go:181-188`, `cmd/graphi/main.go:1002-1046`, `cmd/graphi/main.go:1111-1138`, `cmd/graphi/main.go:1195-1248`) muss durch eine `Runtime`/`App`-Factory mit Options und zentralem Lifecycle ersetzt werden. Sonst wird jede neue Fähigkeit viermal unterschiedlich verdrahtet.

## Technische Schulden

- **Vollgraph-Cache als Standard:** `SQLiteStore.cache` spiegelt alle Nodes und Edges (`core/graphstore/sqlite.go:32-40`, `core/graphstore/sqlite.go:58-63`). Für kleine/mittlere Repos schnell, für große Graphen RAM-intensiv und inkompatibel mit echter DB-selektiver Abfrage.
- **Unbenutzte Traversalindizes:** Schema investiert in From-/To-Indizes (`core/graphstore/sqlite.go:187-192`), der öffentliche Read-Vertrag kann sie aber nicht adressieren (`core/graphstore/graphstore.go:41-53`).
- **Feature Flags durch nil + Sentinel Errors:** `Direct` konfiguriert optionale Subsysteme über viele `WithX`-Methoden (`surfaces/client/direct.go:59-151`); ungültige Kombinationen sind zur Compile-Zeit darstellbar.
- **Extrem große Dateien als Änderungs-Hotspots:** `engine/ingest/ingest.go` hat 2.583 Zeilen, `cmd/graphi/main.go` 1.714 und `surfaces/client/direct.go` 902. Die strukturelle Evidenz sind die stark konzentrierten Typen/Funktionen selbst (`engine/ingest/ingest.go:128-192`, `cmd/graphi/main.go:51-169`, `surfaces/client/direct.go:34-155`).
- **Offene String-Vokabulare über Layer hinweg:** Edge-Kinds sind absichtlich offene Strings im Core, während Query dieselben Begriffe erneut festlegt (`core/model/edge.go:65-66`, `engine/query/service.go:11-32`). Das vermeidet Core-Feature-Coupling, verlagert aber Tippfehler-/Drift-Risiko in Registries und Adapter.
- **Sidecar-Leak als Escape Hatch:** `Ingester.MetaDB()` gibt den read/write DB-Handle an sibling Packages und fordert Ownership nur per Kommentar ein (`engine/ingest/ingest.go:581-589`). Das umgeht eine saubere Persistence-Port-Grenze.
- **Dokumentation ist präzise, aber stellenweise aspirativ:** Die absolute Behauptung, Oberflächen könnten niemals divergieren (`docs/architecture-plan.md:31-35`), widerspricht expliziten `Unavailable`-Implementierungen (`surfaces/client/http_client.go:304-353`, `surfaces/client/http_client.go:393-447`).

## Konkrete Codebeispiele

### 1. Kernquery mit falschem Kostenmodell

```go
edges, err := s.reader.Edges(ctx, graphstore.Query{EdgeKind: edgeKind})
// ...
for _, e := range edges {
    if inbound {
        if e.To() != id { continue }
    } else {
        if e.From() != id { continue }
    }
}
```

Diese Logik lädt erst die vollständige Kantenklasse und filtert danach (`engine/query/service.go:145-173`). Zielbild:

```go
type GraphReader interface {
    Node(ctx context.Context, id model.NodeId) (model.Node, error)
    Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)
    Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)
}
```

SQLite kann das mit den bereits vorhandenen Indizes bedienen (`core/graphstore/sqlite.go:187-192`). Das ist der höchste ROI im gesamten Repo.

### 2. Interface-Segregation statt 28-Methoden-Client

Der aktuelle Vertrag beginnt bei Query/Search und endet bei PR-Kritik (`surfaces/client/client.go:271-464`). Die Remote-Adapter antworten deshalb dutzendfach nur mit `Err...Unavailable` (`surfaces/daemon/client.go:203-301`, `surfaces/daemon/client.go:343-382`). Zielbild:

```go
type QueryClient interface { Query(...); Compound(...) }
type SearchClient interface { Search(...); SemanticSearch(...) }
type AnalysisClient interface { Analyze(...) }
type EditClient interface { Refactor(...); Undo(...); Inline(...); SafeDelete(...) }

type Capabilities struct {
    Query, Search, Analysis, Edit, Forge bool
}
```

CLI-Funktionen verlangen dann nur den kleinsten Port; HTTP muss keine Mutationsmethoden vortäuschen.

### 3. Layerguard benennt Sideways Coupling, prüft es aber nicht

Der Typ kommentiert eine „upward/sideways edge“ (`internal/layerguard/guard.go:74-85`), die Bedingung prüft nur höhere Ränge (`internal/layerguard/guard.go:133-147`). Harte Modulgrenzen brauchen zusätzlich erlaubte Same-Layer-Abhängigkeiten, etwa eine deklarative Paketgruppen-Matrix. Nicht jedes Same-Layer-Import ist falsch; unbeschränkt sind sie aber keine Architekturregel.

### 4. Composition Root statt wiederholter Service-Graphen

`makeClient` baut nur Query/Search/Analysis (`cmd/graphi/main.go:181-188`), `makeEditorClient` baut eine zweite, wesentlich reichere Runtime (`cmd/graphi/main.go:1002-1046`), Daemon und HTTP bauen weitere Varianten (`cmd/graphi/main.go:1111-1138`, `cmd/graphi/main.go:1195-1248`). Zielbild ist eine einzige Factory:

```go
runtime, err := app.Open(app.Options{Root: root, DB: db, Profile: profile})
defer runtime.Close()

httpServer := http.New(runtime.Query, runtime.Analysis, runtime.Capabilities())
```

Die Runtime besitzt Store, Ingest, Watcher und optionale Services; Surfaces besitzen nur Transport.

## UNKNOWN

- **Gesamte Testsuite und Layerguard-Status: UNKNOWN.** Der read-only Versuch `go run ./cmd/layerguard` scheiterte in dieser Sandbox, weil Go in nicht freigegebene globale Modul-/Build-Caches schreiben wollte. Es wurde keine Eskalation angefordert und keine Repo-Datei verändert. Die statische Guard-Logik ist analysiert, der aktuelle Live-Pass aber nicht verifiziert (`internal/layerguard/guard.go:95-157`).
- **Gemessene Skalierungsgrenzen: UNKNOWN.** Es liegen Bench-Dokumente und CI-Gates vor, aber in diesem Review wurde kein Millionen-Kanten-Lasttest ausgeführt. Die O(E)-Aussage folgt direkt aus API und Implementierung (`core/graphstore/graphstore.go:41-53`, `core/graphstore/sqlite.go:841-866`, `engine/query/service.go:145-173`); der konkrete Kipppunkt in RAM/Latenz ist unbekannt.
- **Crash-Recovery über Meta-DB und Graphstore zusammen: UNKNOWN.** Die getrennten Commit-Grenzen sind sichtbar (`engine/ingest/ingest.go:635-670`, `engine/ingest/ingest.go:718-777`), aber alle möglichen Prozessabbruchpunkte wurden nicht fault-injected geprüft.
- **Produktionsnutzung der experimentellen PR-/Memory-/Skill-Subsysteme: UNKNOWN.** Code und Registries zeigen viele Fähigkeiten, aber aus dem Repository allein folgt keine reale Nutzungsfrequenz oder Stabilitätsanforderung.
- **Cross-Platform-Verhalten des Daemons: UNKNOWN.** Der Client nutzt Unix Domain Sockets (`surfaces/daemon/client.go:37-52`, `surfaces/daemon/client.go:307-332`); ob Windows einen alternativen Transport/Wiring-Pfad besitzt, wurde in diesem Architekturreview nicht vollständig verifiziert.

## Harte Empfehlung

**Empfehlung: Refactor — gezielt und vor weiterem Feature-Ausbau. Kein Full Rewrite, kein Teil-Rewrite.**

Direkte Begründung: Core-Modell, Parser-/Analyzer-Registries, Graphstore-Vertrag, deterministische Serialisierung und Ingest-Phasen sind wertvolle, getestete Substanz (`core/model/edge.go:48-113`, `core/parse/registry.go:10-100`, `core/graphstore/graphstore.go:55-146`, `engine/ingest/ingest.go:622-777`). Ein Rewrite würde genau die schwer erarbeiteten Invarianten riskieren. Weiterbauen ohne Refactor würde dagegen die beiden systemischen Probleme verschärfen: jede neue Analyse multipliziert Vollgraph-Scans, und jede neue Capability verbreitert den Client-Monolithen plus alle Stub-Adapter (`engine/query/service.go:145-173`, `surfaces/client/client.go:271-464`).

Reihenfolge der zwingenden Maßnahmen:

1. Endpoint-selektiven `GraphReader` einführen und SQLite-Queries über `from_id`/`to_id` ausführen; Performance-/RAM-Budgets mit realistischen Millionen-Kanten-Fixtures festschreiben.
2. `client.Client` in kleine Capability-Ports zerlegen; echte Capability-Negotiation statt `Unavailable`-Stub-Konformität.
3. Einen zentralen Runtime/Composition Root mit einheitlichem Lifecycle schaffen; `cmd/graphi` auf reine Argumentdekodierung und Surface-Start reduzieren.
4. `Ingester` phasenweise extrahieren (`Scanner`, `ParsePipeline`, `GraphCommitter`, `LinkPhase`, `RecoveryJournal`) und die Cross-DB-Saga mit Fault-Injection-Tests absichern.
5. Layerguard um explizite Same-Layer-Paketregeln ergänzen und die Architektur-Dokumentation von „alle Oberflächen identisch“ auf „identische Bytes bei gemeinsam unterstützten Capabilities“ korrigieren.

Erst nach 1–3 sollte der Feature-Backlog wieder wachsen. Das Repo braucht keine neue Architektur; es braucht, dass seine gute Architekturabsicht jetzt konsequent auf Datenzugriff, Capability-Grenzen und Komposition angewendet wird.
