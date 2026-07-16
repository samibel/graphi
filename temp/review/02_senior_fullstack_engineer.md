# Senior-Fullstack-Review

## Kurzurteil

Graphi ist kein schlecht gebauter Prototyp. Der Go-Kern zeigt überdurchschnittlich viel Disziplin bei deterministischen IDs, Provenienz, Fehlergrenzen, Transaktionen und Cross-Surface-Parität. Die Abhängigkeitsrichtung ist explizit beschrieben und automatisiert geprüft (`internal/layerguard/guard.go:1-17`, `.github/workflows/release.yml:52-64`), und das Web hat einen echten generierten Envelope-Vertrag samt Drift-Check (`web/src/types.ts:1-15`, `web/package.json:9-14`).

Trotzdem ist das Produkt in seinem aktuellen Zustand **nicht ehrlich release-reif**. Drei konkrete Defekte sitzen direkt in öffentlich angebotenen Funktionen: Die VS-Code-Suche erwartet andere JSON-Felder als der Go-Server liefert (`extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:18-35`), die Sigma-Ansicht verwirft verschiedene Kanten zwischen denselben Knoten (`web/src/GraphView.tsx:108-118`, `core/model/edge.go:10-12`), und die angeblichen Refactorings `extract` und `move` sind faktisch nur globale Namensersetzungen; `DestinationFile` wird ignoriert (`engine/edit/refactor.go:174-186`, `surfaces/cli/cli.go:65-90`). Das sind keine Stilfragen, sondern falsches Verhalten und falsche Produktversprechen.

## Score 0–10

**6,2/10**

- Go-Kern und Testdisziplin: **8/10** — starke Invarianten, deterministische Modelle, viele Conformance-Gates (`core/model/edge.go:44-113`, `surfaces/parity_test.go:1-5`, `.github/workflows/lint.yml:18-60`).
- Struktur und Wartbarkeit: **6/10** — sinnvolle Top-Level-Schichten, aber mehrere God-Files und ein lückenhafter Layer-Guard (`engine/ingest/ingest.go:128-192`, `engine/ingest/ingest.go:2569-2583`, `internal/layerguard/guard.go:15-17`).
- React/TypeScript: **5/10** — saubere Backend-Grenze und Schema-Guard, aber Race Conditions, unnötige Voll-Rebuilds und ein Datenverlust im Graph-Rendering (`web/src/graphiClient.ts:67-127`, `web/src/useGraph.ts:205-259`, `web/src/GraphView.tsx:96-205`).
- VS-Code-Client: **4/10** — manuell kopierter Vertrag ist bereits real gedriftet; Kernfunktion Suche ist dadurch kaputt (`extensions/vscode/src/contract.ts:1-6`, `extensions/vscode/src/citations.ts:31-40`, `engine/search/service.go:20-35`).

## Größte Stärken

1. **Der Domänenkern modelliert Invarianten statt sie nur zu kommentieren.** `Edge` ist immutable, seine ID hängt deterministisch von `(from,to,kind)` ab, und fehlende Provenienz wird im Konstruktor abgewiesen (`core/model/edge.go:48-63`, `core/model/edge.go:68-113`). Das ist gutes Go: kleine Werttypen, geschlossene Enums, Fehler früh an der Grenze.

2. **Surface-Parität ist ein erstklassiges Qualitätsziel.** CLI und MCP werden gegen denselben Store gefahren und byteweise verglichen (`surfaces/parity_test.go:72-137`). Der HTTP-Server routet über Go-1.22-Patterns und legt den SPA-Fallback bewusst zuletzt an (`surfaces/http/server.go:181-212`). Das reduziert die sonst typische Logikduplikation zwischen CLI, MCP und HTTP.

3. **Die Web-API-Grenze ist zentral und fail-closed.** Jede Envelope-Antwort durchläuft denselben Schema-Check; auch Fehlerantworten und unparsebare 200-Antworten werden nicht blind gecastet (`web/src/graphiClient.ts:67-117`). Der Web-Envelope wird aus dem JSON-Schema generiert und im Lint-Lauf auf Drift geprüft (`web/package.json:9-14`).

4. **Asynchrone Stale-Result-Probleme sind dem Team grundsätzlich bekannt.** `AgentToolsPanel` schützt Updates mit einer monotonen Sequenznummer und verwirft alte Antworten (`web/src/AgentToolsPanel.tsx:114-141`). Das ist genau das Pattern, das an anderen Stellen fehlt.

5. **CI prüft mehr als nur „kompiliert“.** Formatierung, `go vet`, Race Detector, Full Build/Test, Layer-Richtung und Release-Reproduzierbarkeit sind explizite Jobs (`.github/workflows/lint.yml:18-60`, `.github/workflows/release.yml:35-78`). Der Release-Gate führt außerdem die Web-Vitest-Suite aus und verlangt benannte UX-Suites (`cmd/release-gate/runners.go:87-140`).

## Größte Schwächen

1. **Öffentliche Refactoring-Namen versprechen Semantik, die nicht existiert.** Kommentare definieren `extract` als Herausziehen einer Region und `move` als Verschieben in eine andere Datei (`engine/edit/refactor.go:25-33`); die Implementierung schickt jedoch `rename`, `signature_change`, `extract` und `move` alle in dieselbe `planNameRewrite`-Funktion (`engine/edit/refactor.go:174-186`). Selbst die CLI-Hilfe gesteht ein, dass `DestinationFile` nicht berücksichtigt wird (`surfaces/cli/cli.go:65-75`). Das ist gefährlicher als ein fehlendes Feature, weil Automation eine erfolgreiche, aber semantisch falsche Operation erhält.

2. **Der VS-Code-Vertrag ist nicht „by construction“, sondern Copy-Paste.** Der Dateiheader behauptet einen verbatim portierten Vertrag (`extensions/vscode/src/contract.ts:1-8`). Tatsächlich erwartet `SearchResult.matches` `{id,path,line}` (`extensions/vscode/src/contract.ts:104-107`), während der Go-Service `{node_id,kind,qualified_name,source_path,line,column,rank}` serialisiert (`engine/search/service.go:18-35`). `toSearchCitations` liest folglich `m.id` und `m.path` (`extensions/vscode/src/citations.ts:31-40`), und `runSearch` verwendet genau dieses Mapping für QuickPick und Navigation (`extensions/vscode/src/search.ts:24-35`). Ergebnis: Labels/Pfade werden `undefined`, und die Navigation kann nicht stattfinden.

3. **Die Graphansicht verliert gültige Domänendaten.** Das Kernmodell erlaubt mehrere Kanten derselben Endpunkte, weil `kind` Bestandteil der Edge-ID ist (`core/model/edge.go:10-12`, `core/model/edge.go:76-80`). `GraphView` fügt eine Kante aber nur hinzu, wenn zwischen den Endpunkten noch gar keine Kante existiert (`web/src/GraphView.tsx:113-118`). Eine `calls`- und eine `references`-Kante zwischen demselben Paar werden daher zu einer zufällig durch Payload-Reihenfolge ausgewählten Kante reduziert. Provenienz, Kantenart und Click-Details gehen verloren.

4. **Mehrere React-Flows sind nicht gegen Out-of-order-Antworten geschützt.** `useGraph.load` startet asynchrone Neighborhood-/Search-Aufrufe und schreibt deren Ergebnis ohne Request-ID oder Abbruchsignal in den State (`web/src/useGraph.ts:205-259`). `select` macht dasselbe beim Impact-Aufruf (`web/src/useGraph.ts:273-297`). Bei schnellem Seed- oder Selektionswechsel kann eine ältere Antwort den neueren Zustand überschreiben. `GraphPage.handleSearch` hat dieselbe Lücke (`web/src/GraphPage.tsx:63-75`). Dass das robuste Gegenmuster bereits existiert, zeigt `AgentToolsPanel` (`web/src/AgentToolsPanel.tsx:116-141`).

5. **`GraphView` baut zu häufig den gesamten Graph neu.** Der Effekt berechnet Layout, erzeugt alle Graphology-Knoten/Kanten neu und setzt den Sigma-Graph (`web/src/GraphView.tsx:96-154`), hängt aber vom kompletten `state` und von einer bei jedem `GraphPage`-Render neu erzeugten Callback-Funktion ab (`web/src/GraphView.tsx:204-205`, `web/src/GraphPage.tsx:89-97`). Schon Loading-, Fehler- oder Auswahlstatus können damit einen O(V+E)-Rebuild auslösen. Refs für aktuelle Callbacks sind bereits vorhanden (`web/src/GraphView.tsx:87-94`), wodurch `onSelect` in der Dependency-Liste sogar unnötig ist.

6. **Die größten Backend-Dateien bündeln zu viele Verantwortlichkeiten.** `Ingester` besitzt Parser, Store, SQLite-Metadaten, Linker, Profile, Observability, Progress/Heartbeat, Ignore-State, Parallelität und Test-Hooks in einem Typ (`engine/ingest/ingest.go:128-192`); die Datei endet erst bei Zeile 2583 (`engine/ingest/ingest.go:2569-2583`). `cmd/graphi/main.go` ist zugleich Verb-Registry, Flag-Parser, Client-Wiring und Implementierungsort zahlreicher Run-Funktionen (`cmd/graphi/main.go:51-188`, `cmd/graphi/main.go:1684-1714`). Das ist aktuell testbar, aber Änderungskopplung und Review-Kosten steigen unnötig.

7. **Der Architektur-Guard gibt mehr Sicherheit vor, als er liefert.** `internal/*` ist ausdrücklich ungerankt und damit unbeschränkt (`internal/layerguard/guard.go:15-17`); geprüft wird nur, ob ein Import einen höheren Rang hat (`internal/layerguard/guard.go:129-146`). Gleichrangige Seitwärtsimporte werden entgegen der Kommentarformulierung „upward/sideways“ nicht beanstandet (`internal/layerguard/guard.go:74-85`, `internal/layerguard/guard.go:139-146`). Paketzyklen verhindert Go, aber fachliche Modulgrenzen werden so nicht geschützt.

## Kritische Blocker

1. **VS-Code-Suche reparieren und mit dem realen Go-Payload contract-testen.** Der derzeitige Typ und Mapper widersprechen dem kanonischen Servermodell (`extensions/vscode/src/contract.ts:104-107`, `engine/search/service.go:20-35`). Vor einem Release müssen mindestens `node_id`, `qualified_name` und `source_path` korrekt verwendet werden; ein Fixture aus einer echten Go-Response muss den Clienttest speisen.

2. **Parallelkanten im Web verlustfrei rendern.** Graphology muss als Multi-Graph erstellt werden, oder verschiedene Edge-IDs müssen ohne Endpoint-Deduplizierung eingefügt/visuell aggregiert werden. Die aktuelle Bedingung löscht fachlich gültige Relationen (`web/src/GraphView.tsx:108-118`, `core/model/edge.go:76-80`).

3. **`extract` und `move` entweder implementieren oder aus allen öffentlichen Verträgen entfernen.** Aktuell dokumentiert der Typ echte Strukturänderungen (`engine/edit/refactor.go:25-33`, `engine/edit/refactor.go:44-58`), führt aber ausschließlich Namensrewrites aus (`engine/edit/refactor.go:174-186`). Ein stilles Alias auf Rename ist für ein schreibendes Code-Intelligence-Tool nicht akzeptabel.

4. **Stale Requests in `useGraph` und Suche verhindern.** Seed-, Select- und Suchwechsel benötigen AbortController oder Sequenznummern, bevor alte Responses State überschreiben dürfen (`web/src/useGraph.ts:205-297`, `web/src/GraphPage.tsx:63-75`).

## Technische Schulden

- **Ein Contract-Generator für beide TypeScript-Clients fehlt.** Web generiert und prüft das Envelope-Schema (`web/package.json:9-14`), VS Code pflegt einen manuellen Spiegel (`extensions/vscode/src/contract.ts:1-8`). Der bereits eingetretene Search-Drift beweist, dass der manuelle Ansatz nicht tragfähig ist.
- **Payloads sind auch im Web absichtlich handkuratiert.** `payload.ts` erklärt Payload-Validierung ausdrücklich als out of scope (`web/src/payload.ts:1-5`). Damit schützt der Schema-Guard nur die Hülle, nicht die zur Laufzeit gecasteten Nutzdaten (`web/src/graphiClient.ts:111-117`). Für eine Analyse-/Editierplattform ist Runtime-Validierung an der Vertrauensgrenze sinnvoll.
- **Die VS-Code-Qualitätskommandos existieren, sind aber in den gezeigten zentralen CI-Jobs nicht verdrahtet.** Das Paket bietet Build, Typecheck, Lint und Test separat an (`extensions/vscode/package.json:108-116`); Release- und Lint-Workflow führen dagegen Go-Gates aus, während der Release-Gate nur Web-Abhängigkeiten installiert (`.github/workflows/release-gate.yml:21-30`, `.github/workflows/lint.yml:18-60`). Genau dadurch konnte ein intern typkonsistenter, aber serverinkompatibler Suchtyp bestehen bleiben.
- **Testabdeckung konzentriert sich teilweise auf lokale Mocks statt echte Verträge.** Der VS-Code-Clienttest füttert `search()` mit einem generischen Neighborhood-artigen Payload und prüft nur die HTTP-Methode (`extensions/vscode/src/graphiClient.test.ts:99-121`); der Such-Citation-Mapper hat keinen Test mit dem kanonischen Go-Suchpayload, während `citations.test.ts` nur Impact-Citations prüft (`extensions/vscode/src/citations.test.ts:1-29`).
- **Story-/AC-Kommentare dominieren stellenweise den Code.** Die Kommentare sind oft fachlich wertvoll, aber Implementierungsdateien tragen dauerhaft Ticketnummern und historische Begründungen, etwa im zentralen Ingester (`engine/ingest/ingest.go:145-191`) und im CLI-Dispatch (`cmd/graphi/main.go:54-167`). Ein Teil davon gehört in ADRs; im Code sollten aktuelle Invarianten und das „Warum“ bleiben.
- **God-Files sollten entlang existierender Verantwortlichkeiten geschnitten werden.** Sinnvolle Seams sind `IngestCoordinator`, `MetaRepository`, `LinkPhase`, `TaintPhase` und `ProgressReporter` statt eines weiter wachsenden `Ingester` (`engine/ingest/ingest.go:128-192`). Für CLI: deklarative Command-Registry plus pro Command eine Datei statt des Switches und der Run-Funktionen in derselben 1714-Zeilen-Datei (`cmd/graphi/main.go:67-169`, `cmd/graphi/main.go:1709-1714`).

## Konkrete Codebeispiele

### 1. Defekter VS-Code-Search-Mapper

Ist-Zustand:

```ts
// extensions/vscode/src/citations.ts:31-40
matches.map((m) => ({
  label: m.id,
  detail: `${m.path}:${m.line}`,
  filePath: m.path,
}));
```

Der Server liefert `node_id`, `qualified_name` und `source_path` (`engine/search/service.go:20-27`). Korrekte Richtung:

```ts
type SearchMatch = {
  node_id: string;
  qualified_name: string;
  source_path: string;
  line: number;
};

matches.map((m) => ({
  label: m.qualified_name,
  description: m.node_id,
  detail: `${m.source_path}:${m.line}`,
  filePath: m.source_path,
  line: m.line,
}));
```

### 2. Datenverlust bei Parallelkanten

Ist-Zustand:

```ts
// web/src/GraphView.tsx:113-118
if (g.hasNode(e.from) && g.hasNode(e.to) && !g.hasEdge(e.from, e.to)) {
  g.addEdgeWithKey(e.id, e.from, e.to, { ...e });
}
```

Da `kind` zur kanonischen Identität gehört (`core/model/edge.go:76-80`), muss jede eindeutige `e.id` erhalten bleiben. Beispielsweise:

```ts
const g = new MultiDirectedGraph();
for (const e of state.edges) {
  if (g.hasNode(e.from) && g.hasNode(e.to) && !g.hasEdge(e.id)) {
    g.addEdgeWithKey(e.id, e.from, e.to, { ...e });
  }
}
```

Ob Sigma die Kanten getrennt kurvenförmig zeichnet oder die UI sie gruppiert, ist eine UX-Entscheidung; stilles Wegwerfen ist keine.

### 3. Stale-Response-Schutz vereinheitlichen

`AgentToolsPanel` hat bereits das richtige Muster (`web/src/AgentToolsPanel.tsx:116-141`). Dasselbe gehört in `useGraph.load`, `select` und `GraphPage.handleSearch` (`web/src/useGraph.ts:205-297`, `web/src/GraphPage.tsx:63-75`):

```ts
const loadSeq = useRef(0);

const load = useCallback(async (seed: string, depth: number) => {
  const request = ++loadSeq.current;
  const result = await fetchNeighborhood(seed, depth);
  if (request !== loadSeq.current) return;
  setState(/* result */);
}, []);
```

Zusätzlich ist `AbortController` besser, wenn die Fetch-Grenze ein `signal` akzeptiert, weil dann nicht nur der State-Write, sondern auch unnötige Arbeit abgebrochen wird.

### 4. Refactor-Kinds ehrlich trennen

Der aktuelle Switch ist semantisch falsch (`engine/edit/refactor.go:180-186`). Bis echte Planner existieren, sollte er fail-closed sein:

```go
switch op.Kind {
case RefactorRename, RefactorSignatureChange:
    return planNameRewrite(a.root, files, op.OldName, op.NewName)
case RefactorExtract, RefactorMove:
    return nil, fmt.Errorf("%w: %s is not implemented", ErrInvalidOp, op.Kind)
default:
    return nil, fmt.Errorf("%w: unknown refactor kind %q", ErrInvalidOp, op.Kind)
}
```

Für `move` muss ein eigener Planner `DestinationFile` validieren und tatsächliche Delete/Insert-Operationen planen; die derzeitige Felddefinition behauptet genau diese Semantik (`engine/edit/refactor.go:53-58`).

## UNKNOWN

- **Gesamte Go-Suite:** UNKNOWN. `go test ./...` konnte in der eingeschränkten Review-Umgebung nicht vollständig verifiziert werden, weil mehrere Tests absichtlich Loopback-/Unix-Sockets öffnen, etwa `httptest.NewServer` in `surfaces/http/server_test.go:303-309` und der echte Daemon in `surfaces/parity_test.go:1702`. Die nicht netzwerkabhängigen Pakete liefen weitgehend erfolgreich; Sandbox-Bind-Fehler sind kein Beleg für Produktfehler.
- **VS-Code-Teststatus:** UNKNOWN. Im lokalen Checkout waren die VS-Code-`node_modules` nicht installiert; deshalb konnten die vorhandenen `test`, `typecheck` und `lint` Scripts (`extensions/vscode/package.json:108-116`) read-only nicht ausgeführt werden. Der Vertragsfehler ist unabhängig davon statisch eindeutig belegt.
- **Go-Coverage-Prozent und ungetestete Branches:** UNKNOWN. Vorhandene Tests sind zahlreich und qualitativ teilweise stark, aber ohne vollständig erfolgreichen Coverage-Lauf ist keine belastbare Prozentzahl seriös.
- **Browser-/WebGL-Verhalten auf realer Hardware und bei großen Graphen:** UNKNOWN. Die Komponenten-Tests mocken `GraphView` in zentralen Page-Tests (`web/src/GraphPage.test.tsx:12-15`, `web/src/App.test.tsx:10-13`); WebGL, Parallelkanten und Rebuild-Kosten werden damit nicht real geprüft.
- **Parserpräzision auf großen realen Multi-Language-Repositories:** UNKNOWN. Fixtures und Conformance-Tests belegen deterministisches Verhalten, aber nicht automatisch Recall/Precision für produktive Repos.
- **Produktionsmigrationen mit großen bestehenden SQLite-Dateien:** UNKNOWN. Die Migration löscht beim alten Edge-Layout bewusst Edge-Daten und verlässt sich auf einen anschließenden Voll-Reindex (`core/graphstore/sqlite.go:210-218`, `core/graphstore/sqlite.go:253-260`); Dauer, Crash-Verhalten und Speicherbedarf auf großen Beständen wurden in diesem Review nicht gemessen.

## Harte Empfehlung

**REFactor vor Weiterbauen. Kein Full Rewrite, kein Teil-Rewrite.**

### Direkte Begründung

Der Kern rechtfertigt keinen Rewrite: Domäneninvarianten sind sauber modelliert (`core/model/edge.go:44-113`), die Surface-Grenzen sind grundsätzlich richtig angelegt (`surfaces/parity_test.go:1-5`), und CI prüft wichtige Go-Eigenschaften (`.github/workflows/lint.yml:18-60`). Ein Rewrite würde diese hart erarbeitete Korrektheit vernichten und sehr wahrscheinlich neue Fehler einführen.

Aber einfach weiterbauen ist ebenfalls falsch. Die drei kritischen Defekte liegen an Produktgrenzen und beschädigen Vertrauen: eine kaputte IDE-Suche, verlorene Graphrelationen und irreführende Schreiboperationen. Deshalb zuerst ein fokussierter Refactor in dieser Reihenfolge:

1. `extract`/`move` sofort fail-closed schalten oder aus Deskriptoren entfernen; danach echte Planner separat implementieren (`engine/edit/refactor.go:174-208`).
2. Einen gemeinsamen generierten Payload-Vertrag für Web und VS Code einführen und den realen Search-Payload als Contract-Fixture testen (`web/src/payload.ts:1-5`, `extensions/vscode/src/contract.ts:1-8`).
3. `GraphView` auf verlustfreie Multi-Edges umstellen und einen nicht gemockten Graphology-Test hinzufügen (`web/src/GraphView.tsx:108-118`).
4. Request-Sequenzen/Abort-Signale für alle interaktiven Fetches ergänzen (`web/src/useGraph.ts:205-297`, `web/src/GraphPage.tsx:63-75`).
5. Erst danach God-Files schrittweise entlang vorhandener Seams schneiden; keine Big-Bang-Umschreibung (`engine/ingest/ingest.go:128-192`, `cmd/graphi/main.go:67-169`).

Wenn diese Blocker behoben und VS Code in dieselben CI-Gates wie Web aufgenommen ist, ist **Weiterbauen** vertretbar. Vorher nicht.
