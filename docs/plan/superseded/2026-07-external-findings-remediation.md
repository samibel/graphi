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

# Remediation-Plan: Externe Real-World-Findings (Juli 2026)

Zwei unabhängige externe Tests haben graphi gegen reale Projekte laufen lassen:

1. **Spring Boot Monorepo (11.746 Dateien):** Index funktional, aber 4m48s Laufzeit,
   2,3 GB SQLite-DB, 4,27 Mio. Edges bei nur 55k Nodes, minutenlange Stille in der
   Link-Phase, `diagnose` unbrauchbar verrauscht (dead_symbol auf `@Test`/`@Bean`).
2. **vuln-go (Ground-Truth-Security-App):** `analyze taint` fand **0 von 4** realen
   Injections. Ursache architektonisch: externe/Stdlib-Call-Ziele werden nie als
   Graph-Knoten materialisiert, daher kann kein Sink je matchen.

Beide Befunde wurden per Codebase-Analyse verifiziert. Dieses Dokument ist der
priorisierte Umsetzungsplan. Die drei Tracks teilen eine gemeinsame architektonische
Idee: **interne Symbol-Knoten bleiben die präzise Wahrheit; externe Welt wird als
internierte, explizit markierte Knoten zweiter Klasse ergänzt** — nie fabrizierte
Auflösung, immer `heuristic`-Tier mit Provenance. Das erhält die Fail-Closed-
Philosophie und macht gleichzeitig Taint, Imports und Diagnose korrekt.

---

## Verifizierte Root Causes (Datei/Zeile)

### Taint: 0/4 Recall
- `engine/link/resolve_go.go:86-99` — unaufgelöste externe Calls werden per Design
  verworfen (`st.Skipped++`), kein Knoten, keine Kante. Der Package-Doc-Kommentar
  (`resolve_go.go:26-28`) dokumentiert das explizit.
- `engine/ingest/ingest.go:1757` — `link.Stats` (enthält jeden verworfenen externen
  Call) wird mit `_` weggeworfen.
- `engine/analysis/taint/config.go:174-186` — `matchSink`/`matchSource` matchen rein
  über `Node.QualifiedName()` + `Kind()`. Da `os/exec.Command` nie ein Knoten ist,
  ist `sinks` leer → `taint.go:110-115` returnt sofort leer. Der Interproc-Solver
  (`interproctaint/solve.go:152-177`) hat dasselbe Problem: Call-Graph nur aus
  `calls`-Edges, die es zu externen Zielen nicht gibt. „solved: true, flows: []" ist
  ein ehrliches „Fixpoint berechnet, nichts gefunden" — es maskiert, dass der Graph
  die nötigen Knoten physisch nicht enthalten *kann*.
- `engine/analysis/taint/corpus_test.go:46-133` — der Korpus fabriziert synthetische
  `"call"`-Knoten mit QN == Sink-Pattern. Genau diese Knoten erzeugt die echte
  Pipeline nie. Der 100%-Recall-Gate in `recall_test.go` testet also eine Fiktion.
- Wrapper-Sources: `httpHelper.GetQueryParam` matcht kein Source-Pattern, und sein
  interner `.URL.Query`-Call wurde verworfen → Wrapper wird nie geseedet.

### Edge-Explosion & DB-Größe
- `engine/link/resolve_common.go:187-217` + `resolve_java.go:42-63` +
  `engine/link/index.go:96-105` — Java-Imports werden auf das **vorletzte
  Punktsegment** kollabiert (`com.example.service.Foo` → Clause `"service"`), und
  `byClause` gruppiert **jedes Verzeichnis mit diesem Basename repo-weit**. Ein
  Import erzeugt Datei→Datei-Kanten zu *jeder Datei in jedem* `service`/`model`/
  `impl`/`dto`-Verzeichnis des Monorepos. Das ist der 4,27-Mio.-Edge-Multiplikator.
- `core/graphstore/sqlite.go:133-137, 446-453` + `batch.go:214-223` — jede Edge
  speichert `reason`-String + `evidence`-JSON pro Zeile **und** wird zusätzlich in
  die FTS5-Tabelle indiziert; dazu drei B-Tree-Indizes. ≈ 500 Bytes/Edge → 2,3 GB.
  Quelltext ist es nicht — der wird nie in der DB gespeichert.
- `engine/ingest/ingest.go:664` — `PhaseLink` wird genau **einmal** emittiert, dann
  laufen `store.Nodes()`, `BuildIndex`, Full-Edge-Sweep (`ingest.go:1701-1730`,
  O(E) pro Pass!) und 4,27 Mio. `PutEdge` ohne ein einziges weiteres Progress-Event.
- `engine/link/index.go:160-183` — `receiverMethod` scannt pro unaufgelöstem
  recv.method-Call **alle** Verzeichnisse (O(dirs), Go-only-Hotspot).

### Diagnose-Rauschen
- `engine/diagnostic/analyze.go:165-200` — dead_symbol = „kein eingehender
  statischer Edge". Keine Framework-, Annotations- oder Entry-Point-Awareness.
- `core/model/node.go:24-31` — Node hat exakt 6 Felder; **keine** Annotations,
  Modifier oder Test/Main-Klassifikation. `core/parse/parser_java.go:133-142` liest
  den tree-sitter-`modifiers`-Knoten (wo `@Test`, `@Bean`, `public static` leben)
  nie. Das Signal existiert im Modell schlicht nicht.
- `engine/diagnostic/analyze.go:132-158` — `unresolved_reference` flaggt **jede**
  heuristic-Edge einzeln → auf 4,27 Mio. Import-Edges Millionen Diagnostics.
- `engine/edit/safe_delete.go:87-136` — das Safe-Delete-Gate hat dieselbe Lücke:
  es würde einen lebenden Spring-Bean als löschbar durchwinken.

---

## Track B zuerst: Import-Fan-out & Storage (P0 — entstört alles andere)

Der Fan-out-Fix kommt vor Taint, weil er (a) die Edge-Zahl um ~90 % senkt, (b) die
`unresolved_reference`-Flut trockenlegt, (c) Link-Zeit und DB-Größe dominiert und
(d) das Internierungs-Muster einführt, auf dem Track A aufbaut.

### B1. Package-Knoten statt Datei→Datei-Fan-out
**Architektur:** Ein Import erzeugt **eine** Kante `file →imports→ package`-Knoten
(neuer Node-Kind `package`, interniert pro voll qualifiziertem Package-Pfad),
statt N Kanten auf jede Datei jedes namensgleichen Verzeichnisses. Die
Package→Datei-Zuordnung ist über `defines`-Kanten bzw. `source_path` rekonstruierbar
— Struktur-Queries verlieren nichts, gewinnen aber ein echtes Package-Konzept.

- Clause-Key auf den **vollen Package-Pfad** umstellen, nicht `packageSegment`
  (`resolve_common.go:23-29`). Dafür muss der Java/Kotlin-Parser die
  `package`-Deklaration der Datei extrahieren (heute wird der QN aus dem
  Verzeichnis-Basename geraten) — kleiner Eingriff in `parser_java.go`, großer
  Präzisionsgewinn auch für `crossModule` (`resolve_common.go:350`).
- Erwartung Spring Boot: 4,27 Mio. → grob O(#Import-Statements) ≈ Zehntausende
  Edges. Messen, nicht hoffen: Bench-Fixture s. B5.
- Migration: Semantics-Stamp des Warm-Starts bumpen (`warmstart.go:49-62`), damit
  bestehende DBs sauber invalidieren.

### B2. Storage-Diät
- **FTS5 nur noch für Nodes**, nicht für Edge-`reason`-Boilerplate
  (`sqlite.go:446-453`). Niemand volltextsucht „file imports package …" × 4 Mio.
  Wer Edge-Suche braucht, bekommt sie über kind/tier-Filter.
- `reason` **internieren**: Dictionary-Tabelle (`reason_id INTEGER`) oder
  Template+Param statt freiem String pro Zeile.
- `evidence` für High-Volume-heuristic-Edges weglassen oder auf `line`-Spalte
  reduzieren (das JSON-Array mit einem Element ist reines Gewicht).
- Ziel: Spring Boot < 300 MB. Nach B1 allein vermutlich schon < 500 MB.

### B3. Link-Phase beobachtbar & inkrementell ehrlich machen
- Progress-Events **innerhalb** von `linkFiles` (`ingest.go:1690-1769`): Done/Total
  pro Sprache/Datei-Batch; `ProgressEvent.Total` für Link befüllen statt 0
  (`progress.go:41`). CLI-Renderer (`cmd/graphi/progress.go:244`) zeigt dann echten
  Fortschritt statt statischem Spinner.
- Die eine Riesen-Transaktion in Chunks schneiden (z. B. 50k Edges/Commit): macht
  Fortschritt real sichtbar, deckelt die WAL-Größe und macht Abbrüche resumierbar.
  **Konsistenz-Bedingung:** Chunking muss mit der Ingest-Meta-Transaktion
  koordiniert sein — kein Graph-Store-Write, während eine Meta-Transaktion noch
  zurückrollen kann. Konkret: pro Chunk ein Meta-Checkpoint (Graph und Meta-Cache
  committen im Lockstep), und Link-Edge-Writes bleiben idempotent/replaybar, sodass
  ein abgebrochener Pass beim nächsten Link-Lauf selbstheilend nachgezogen wird
  statt zu divergieren.
- Stale-Edge-Sweep entkoppeln: statt Full-Read aller Edges pro Pass
  (`ingest.go:1701,1707`) from-owned Edges über den vorhandenen
  `edges_from_id`-Index gezielt löschen.
- `receiverMethod` (`index.go:160-183`): Reverse-Index `methodName → dirs` beim
  `BuildIndex` aufbauen statt O(dirs)-Scan pro Lookup.

### B4. Sinnvolle Defaults für Monorepos
- `GRAPHI_RESPECT_GITIGNORE` bleibt opt-in, aber eine kleine eingebaute
  Default-Denylist für Build-Output (`target/`, `build/`, `.gradle/`, `node_modules/`,
  `dist/`) wird default-on mit Opt-out — ein Spring-Repo indexiert heute
  Generatoroutput mit.

### B5. Bench-Gate
- Reales Fixture (Spring-Boot-Modul-Snapshot o. ä. mit repetitiven Package-Namen)
  in `bench/` + CI-Budget: Edges/Node-Ratio, DB-Bytes/Edge, Link-Dauer. Die
  bestehende Bench-Harness (`docs/ci/bench.md`) hat dafür schon die Infrastruktur.

---

## Track A: Taint reparieren — externe Welt materialisieren (P0, der Blocker)

### A1. Internierte External-Nodes + heuristic `calls`-Edges
**Architektur:** Neuer Node-Kind `external` (interniert: **ein** Knoten pro
eindeutigem qualifizierten Namen wie `os/exec.Command`, nicht pro Callsite —
Knotenwachstum ist damit O(unique externe Symbole), typisch wenige Tausend, kein
Performance-Risiko). Callsite-Details (Datei:Zeile) leben in der Edge-Evidence.

- **Kein Extractor-Umbau nötig für Go:** `resolve_go.go` besitzt zur Link-Zeit
  bereits `aliasToPath["exec"]="os/exec"`. Am heutigen Drop-Punkt
  (`resolve_go.go:86-116`, alle drei Pfade: Selector, Receiver-Methode, Bare-Ident)
  statt `st.Skipped++`: External-Node `os/exec.Command` minten + `calls`-Edge im
  `heuristic`-Tier mit Reason `external call (unresolved import os/exec)`.
- `ingest.go:1757`: `link.Stats` nicht mehr verwerfen — persistieren und im
  `PhaseDone`-Summary ausgeben („12.403 externe Referenzen als heuristic
  materialisiert").
- **Query-Hygiene:** Default-Ausschluss von `external`-Nodes in Struktur-Queries
  (callers/impact/neighborhood), opt-in per Flag. Das schützt die im ersten Test
  gelobte Präzision der Struktur-Provenance — Taint und Contracts opten ein.
  (Der `contracts`-Registry-Code erwartet solche Knoten übrigens schon:
  `contracts/registry.go:224,244` matcht auf ein `"call"`-Kind, das nie produziert
  wird — dieselbe Lücke, gleicher Fix.)
- Sprach-Rollout: Go zuerst (Ground Truth vorhanden), dann Python/TS/Java über
  `resolve_common.go` — nach B1 haben Java-Imports volle Package-Pfade, sodass
  externe FQNs (`org.springframework….RestTemplate.exchange`) sauber baubar sind.

**Damit matcht `matchSink` ohne jede Änderung an der Matching-Logik** — die
Default-Sinks (`config.go:205-206`) zeigen dann auf existierende Knoten.

### A2. Wrapper-Sources & Config-Datei
- Sobald `.URL.Query` ein External-Node ist, wird er als Source klassifiziert; der
  Interproc-Solver (`solve.go:213-232`) propagiert das Label durch
  `GetQueryParam` zu allen Handlern **ohne weitere Änderung** — die
  Summary-Maschinerie existiert und funktioniert, ihr fehlten nur die Seeds.
- Zusätzlich: `LoadTaintConfig(path)` für projektspezifische Sources/Sinks/
  Sanitizer. Die `taint.Config`-Structs sind bereits vollständig JSON-getaggt
  (`config.go:13-62`) — der Loader wurde offensichtlich geplant und nie gebaut.
  Suchpfad `.graphi/taint.json`, Merge über Defaults, Anschluss in
  `dispatch.go:72,119`.

### A3. Ehrliche Verdicts statt falscher Sicherheit
- Solange ein Graph null External-Nodes und null klassifizierte Sinks enthält,
  darf das Ergebnis nicht `outcome: empty` heißen. Neues Verdict
  `no_sink_candidates` mit Hinweis („keine Sink-Symbole im Graph — external-node
  Materialisierung prüfen / Custom-Config laden"). Ein leeres Ergebnis, das wie
  „geprüft, sauber" aussieht, ist schlimmer als „weiß nicht" — genau der Kritikpunkt
  des Testers.

### A4. E2E-Recall-Gate mit echter Mini-App
- vuln-go-artiges Fixture (HTTP-Handler → eigener Wrapper → `exec.Command`/
  `db.Query`, 2× SQLi, 1× RCE, 1× LFI) als **echte .go-Quellen** ins Corpus,
  E2E-Test: ingest → link → `analyze taint` → 4/4 Flows asserted (Muster:
  `engine/ingest/*_e2e_test.go`). Das Fixture assertet **Recall und Präzision**:
  neben den 4 erwarteten Flows enthält es sanitisierte/harmlose Pfade
  (parametrisierte Query, `strconv.Atoi`-validierter Input), auf denen **kein**
  Finding gemeldet werden darf — sonst würde Over-Tainting unbemerkt durchgehen. Der bestehende synthetische Korpus bleibt als
  Unit-Ebene, verliert aber seine Gate-Funktion an das E2E-Fixture. Das ist der
  Test, der diese Lücke von Anfang an gefangen hätte.

---

## Track C: Diagnose entlärmen (P1)

### C1. Annotations/Modifier ins Modell (gemeinsames Fundament)
- `core/model/node.go`: kompaktes `Meta`-Feld (Annotations-Liste + Flags wie
  `static`, `main`, `test-path`). Durchfädeln über `addDef`
  (`parser_tswalk.go:73-89`), `nodeSpecs`, `MapTreeSitter`.
- `parser_java.go:115-142`: den `modifiers`-Kindknoten von
  `class_/method_declaration` lesen — die Annotationen stehen dort bereits im
  tree-sitter-Baum, sie werden nur nie angefasst. Analog später Python-Decorators/
  TS-Decorators über dieselbe Meta-Schiene.
- Das nützt dreifach: Diagnose (C2), Taint (Framework-Sources wie `@RequestParam`),
  und künftige Query-Filter („nur Tests", „nur Controller").

### C2. Entry-Point-aware dead_symbol
- Exclusion-Prädikat an `analyze.go:181-184`: skip bei (a) Entry-Point-Annotationen
  (eingebaute Liste: `@Test`, `@Bean`, `@Configuration`, `@Component`-Familie,
  `@Controller`, `@PostConstruct`, `@Override`, JUnit-Lifecycle …), (b) `main`-
  Signatur, (c) Datei unter Test-Konvention (`src/test/`, `*_test.go`, `test_*.py`).
- Statt Binär-Skip optional Severity-Downgrade auf `info` mit Reason
  `entrypoint_candidate` — bleibt sichtbar, rauscht nicht.
- **Dieselbe Liste als Gate in `safe_delete.go:87-136`** — heute würde das Tool
  einen lebenden Spring-Bean löschen; das ist ein Korrektheits-, kein Komfort-Fix.

### C3. Suppression-Surface & Aggregation
- `Diagnose(ctx, reader, kinds)` um Options-Struct erweitern: Pfad-Excludes,
  Severity-Floor, Annotations-Allowlist; CLI-Flags in `cli.go:275-294`; Config in
  `.graphi/diagnostics.json` (gleiche Datei-Konvention wie A2).
- `unresolved_reference` aggregieren: **eine** Diagnose pro externem Ziel mit
  Zähler statt einer pro Edge (`analyze.go:132-158`). Nach B1/A1 sind externe
  Imports ohnehin klassifizierte External-Nodes — die Diagnose wird dann vom
  Rauschen zum Feature („Top externe Abhängigkeiten dieses Moduls").

---

## Reihenfolge, Abhängigkeiten, Meilensteine

```
M1  B1 Package-Knoten + voller Clause-Key     ─┐  entstört Edges, Diagnose, Zeit
    B3 Link-Progress + Chunk-Commits           │  (unabhängig, parallel machbar)
M2  A1 External-Nodes (Go) + A3 Verdicts      ─┤  baut auf Internierungs-Muster
    A4 E2E-Recall-Fixture (Gate ab hier grün)  │
M3  B2 Storage-Diät + B4 Defaults + B5 Bench  ─┤  Migration bündeln (1× Stamp-Bump)
M4  A2 Config-Loader + Wrapper-Sources        ─┤
    C1 Meta-Modell + Java-Annotations          │  ein gemeinsamer Modell-Change
M5  C2 dead_symbol/safe_delete + C3 Filter    ─┘
    A1-Rollout Java/Python/TS
```

DB-Schema/Node-Kind-Änderungen (B1, A1, C1) jeweils mit Warm-Start-Semantics-Bump;
B2+B1 in eine Migration bündeln, damit Nutzer nur einmal neu indexieren.

## Akzeptanzkriterien (messbar, CI-gegated)

| Metrik | Ist | Ziel |
|---|---|---|
| Taint-Recall vuln-go-Fixture (E2E) | 0/4 | 4/4, Präzision ≥ 0,8 |
| Spring Boot Edges | 4,27 Mio. | < 500k |
| Spring Boot DB | 2,3 GB | < 300 MB |
| Spring Boot Vollindex | 4m48s | < 90s |
| Link-Phase ohne Progress-Event | Minuten | < 2s zwischen Events |
| dead_symbol-FPs auf Spring-GraphQL-Modul (@Test/@Bean/main) | „sehr viele" | 0 als warning |
| unresolved_reference-Diagnosen | O(Edges) | 1 pro externem Ziel |

## Risiken

- **External-Node-Flut bei Java-Wildcard-Imports:** Internierung + Kappung pro
  Datei (Budget, geloggt statt still) — „No silent caps".
- **Query-Regression durch External-Nodes:** Default-Exklusion in Struktur-Queries,
  Konformanz-Tests auf bestehende callers/impact-Ergebnisse vor/nach.
- **Warm-Start-Invalidierung nervt Nutzer:** Migrationen bündeln (M1+M3), im
  Changelog ankündigen.
- **Package-Deklaration fehlt in exotischen Java-Dateien:** Fallback auf heutige
  Verzeichnis-Heuristik, als `heuristic`-Tier markiert — nie schlechter als heute.
