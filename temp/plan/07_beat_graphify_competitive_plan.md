# Competitive Plan: Graphi nachweisbar genauer, schneller und erweiterbarer als Graphify

> **Status: CONDITIONAL COMPETITIVE PROTOCOL.** Methodik und Claim-Gates sind normativ, aber nicht finanziert oder Teil des Focused Core RC. Aktivierung erst nach `COMP-PILOT`, explizitem Charter und eigenem Budget gemäß `00_master_execution_plan.md`.

## Strategische Entscheidung

Graphi soll Graphify nicht durch mehr Formate, Sprachen oder Integrationen kopieren. Graphify ist bei Onboarding, Distributionsbreite und multimodalen Knowledge-Graphs aktuell weiter. Graphi gewinnt in einer engeren, überprüfbaren Kategorie:

> **Der genaueste, schnellste und am sichersten erweiterbare vollständig lokale Code-Graph für Coding-Agenten auf großen Repositories. Jede Antwort ist inkrementell aktuell, deterministisch und durch Source-Evidenz belegbar.**

Der Claim „besser als Graphify“ ist nur erlaubt, wenn Graphi **alle drei unabhängigen Gates gleichzeitig** besteht:

1. genauer;
2. schneller;
3. erweiterbarer.

Kein zusammengesetzter Score darf einen verlorenen Bereich verrechnen. Mehr Features kompensieren keine schlechtere Genauigkeit. Weniger RAM kompensiert keine langsamere Antwort. Interne Registries kompensieren keine fehlende externe Plugin-Plattform.

## Aktivierung: erst Feasibility, dann Full Program

Dieses Dokument beschreibt das vollständige Claim-Protokoll. Es wird nicht direkt als 26–40-Personenmonatsprogramm gestartet.

### `COMP-PILOT`

Voraussetzung: Focused Core RC aus `00_master_execution_plan.md` ist grün.

Pilotumfang:

- ERPNext plus ein gepinntes nicht-Python-Repository;
- je Repo mindestens 100 geprüfte Nodes und 200 geprüfte Edges;
- 40 balancierte Agent-Aufgaben mit Source-Gold;
- Cold Index, korrekte Warm Queries, Freshness und Peak RSS;
- zwei kleine Extension-Aufgaben gegen die offiziell vorgesehenen Erweiterungswege beider Produkte;
- sechs bis zehn Engineering-Personenwochen plus separat ausgewiesene Annotation-/Compute-Kosten.

Proceed zum Full Program nur, wenn:

- Accuracy im Punktwert mindestens non-inferior ist, Margin höchstens zwei Prozentpunkte;
- mindestens ein praktischer Speed-Vorsprung von 25 % sichtbar ist und keine primäre Metrik um mehr als 10 % verliert;
- der Weg zu den Full Gates ohne zusätzlichen ungeplanten Core-Rewrite begrenzt ist;
- für die Extension Platform mindestens drei konkrete externe Extension-Jobs und zwei externe Prototyp-Autoren existieren;
- Charter, Team, Annotation, Compute und unabhängiger Audit separat finanziert sind.

Andernfalls bleibt dieses Dokument ein Protokollarchiv. Der universelle „besser als Graphify“-Claim wird verworfen oder auf die tatsächlich gewonnene Teilkategorie begrenzt.

## Korrigierte Ausgangslage

Vergleichsstand am 13. Juli 2026: Graphify `0.9.13`, Branch `v8`, Commit `eec7a0183847cbdc8a87d92b233759a5204b89fe`. Beim tatsächlichen Protocol Freeze wird erneut die dann neueste stabile Version geprüft und gepinnt.

### Was Graphify belegt

- einfache Installation und Skills/Integrationen für mehr als 20 Assistenten;
- Code-, Dokument-, Datenbank-, Infrastruktur- und Mediengraphen;
- ungefähr 40 Code-Sprachen;
- `graph.html`, `GRAPH_REPORT.md` und `graph.json` als sofort verständliche Ergebnisse;
- Provenienzlabels `EXTRACTED`, `INFERRED`, `AMBIGUOUS`;
- veröffentlichter Ergebnisbericht für LOCOMO, LongMemEval und ERPNext.

### Was Graphify nicht belastbar belegt

Der öffentliche Code-Intelligence-Claim basiert auf einem einzigen Python-Repository und nur sechs Agent-Aufgaben: ERPNext, `70,8 % → 82,0 %` Key-Fact-Coverage, Claude Opus 4.8, maximal 14 Turns, ungefähr 140K Tokens pro Aufgabe. Aufgaben, Gold-Fakten, Raw Runs, Seeds und Confidence Intervals sind im öffentlichen Repo nicht verfügbar. Das in `BENCHMARKS.md` referenzierte `crosstool`-Harness ist dort nicht enthalten.

Die 82-%-Zahl wurde bei einem älteren Graphify-Stand veröffentlicht und darf nicht als reproduzierte Baseline für `0.9.13` behandelt werden. Sie ist historische Sanity-Evidenz, kein aktueller Head-to-Head-Wert.

Der eingebaute `graphify benchmark` ist ein Token-Schätzwerkzeug, kein Accuracy-/Speed-Benchmark: pauschale Zeichen-/Wortschätzungen, fünf feste Fragen, keine fachliche Korrektheit und übersprungene No-Match-Fragen.

Quellen: [Graphify Repository](https://github.com/Graphify-Labs/graphify), [gepinntes BENCHMARKS.md](https://github.com/Graphify-Labs/graphify/blob/eec7a0183847cbdc8a87d92b233759a5204b89fe/BENCHMARKS.md), [Benchmarkimplementierung](https://github.com/Graphify-Labs/graphify/blob/eec7a0183847cbdc8a87d92b233759a5204b89fe/graphify/benchmark.py), [Architecture](https://github.com/Graphify-Labs/graphify/blob/eec7a0183847cbdc8a87d92b233759a5204b89fe/ARCHITECTURE.md), [Security](https://github.com/Graphify-Labs/graphify/blob/eec7a0183847cbdc8a87d92b233759a5204b89fe/SECURITY.md).

### Graphis ehrlicher Ist-Zustand

Graphis Testinfrastruktur ist eine brauchbare Basis, beweist aber heute keinen Competitive Win:

- `cmd/bench` misst nur ein ungefähr 43-zeiliges Fixture; die Query-Messung verwendet teilweise ein leeres Symbol (`internal/bench/harness.go:213-218`, `internal/bench/harness.go:268-275`).
- `cmd/corpus` materialisiert gepinnte Real-Repos, prüft aber primär Exit-Code, JSON und Nicht-Leere, nicht fachliche Korrektheit (`internal/corpus/corpus.go:19-22`, `internal/corpus/run.go:127-236`).
- `budget_ms` im Corpus ist Report-Metadatum und kein Gate (`internal/corpus/corpus.go:189-205`).
- die Token-Evaluation basiert auf zwölf handgeschriebenen Fällen und darf laut eigener Dokumentation nicht als Produktmessung zitiert werden (`docs/ci/eval.md:29-49`).
- das Nightly sucht `TestBench`, obwohl kein solcher Test existiert; es kann grün laufen, ohne einen Benchmark auszuführen (`.github/workflows/bench-nightly.yml:25-30`).
- Caller/Callee/References scannen alle Kanten einer Art, obwohl SQLite Endpoint-Indizes besitzt (`engine/query/service.go:130-191`, `core/graphstore/sqlite.go:187-192`).
- Incremental Linking und Orphan Cleanup enthalten weitere Vollscans (`engine/ingest/ingest.go:1828-1859`, `engine/ingest/ingest.go:1989-2009`).

Diese Lücken werden nicht versteckt. Sie definieren die ersten Arbeitspakete.

## Gemeinsames Competitive Protocol

### Gepinnte Eingaben

- exakte Graphi-Binary, Commit und SHA-256;
- exaktes Graphify-Wheel, Commit und SHA-256;
- Container-/Runner-Image mit Digest;
- Repository-Commits mit SHA-256 der materialisierten Worktrees;
- identische CPU-, RAM-, Disk- und Timeout-Limits;
- vollständige Toolkonfiguration ohne repository-spezifisches Tuning;
- getrennte Phasen für Install, Index, Query, Mutation und Export.

### Repository-Corpus

Zwölf gepinnte Repositories:

- drei Go-Repositories;
- drei TypeScript/JavaScript-Repositories;
- drei Python-Repositories, einschließlich ERPNext;
- drei Java-Repositories, einschließlich eines großen Monorepos.

Zusätzlich ein gemischtes Repository für den Integrations-/Erweiterbarkeitstrack. Auswahl, Commits und Größenklassen werden vor Optimierungsbeginn eingefroren.

### Zwei Vergleichstracks

1. **Neutraler Adapter-Track:** identische Toolnamen, Inputs, Outputlimits und Ergebnisgrenzen für beide Produkte.
2. **Produkt-Track:** jeweils offiziell empfohlene Installation und Agent-Integration; sämtliche Prompt-, Tool-Schema- und Output-Tokens werden gezählt.

Beide Tracks erlauben dem Agenten dieselben Basiswerkzeuge wie `grep`, `read` und `list`. Zusätzlich erhält er genau ein Code-Intelligence-Tool.

### Ausführungsregeln

- Netz während Index und Evaluation vollständig gesperrt;
- ABBA-interleavte, randomisierte Reihenfolge auf derselben dedizierten Hardware;
- frischer State und kontrollierter Page Cache für Cold Runs;
- Warmups getrennt und niemals als Messsamples gezählt;
- echte Tokenzählung des verwendeten Modells, keine Zeichen-/Wortschätzung;
- Timeout, Crash, No-Match und falsche Ergebnisse bleiben im Report;
- falsche oder unbelegte Antworten gelten im Speedvergleich als Timeout, nicht als schnelle Antwort;
- Rohlogs, stdout/stderr, Traces, Exitcodes und verlorene Kategorien werden archiviert.

## Pflichtgate A — Graphi ist genauer

### Tool-neutrales Gold-Corpus

Pro Tier-1-Sprache mindestens:

- 500 menschlich geprüfte Symbole;
- 1.000 menschlich geprüfte Kanten;
- positive, negative, ambige und unlösbare Fälle;
- Source-Datei, Zeile/Spalte, normalisierte Art und Relation;
- mindestens 20 % doppelt annotiert;
- Cohen’s Kappa mindestens 0,85; alle Konflikte adjudiziert.

Gold-Identität:

```text
Node = repo-relative POSIX path + start line/column + normalized kind + bare name
Edge = matched gold source + matched gold target + normalized relation
```

Qualified Names werden separat ausgewertet, damit unterschiedliche interne IDs keinen unfairen Vorteil erzeugen.

### Retrieval- und Agent-Aufgaben

- 400 Retrieval-Fragen, 100 je Sprache;
- 240 versiegelte Agent-Aufgaben, 60 je Sprache;
- drei unabhängige Runs pro Agent-Aufgabe;
- acht gleich große Kategorien: Definition/References, Caller/Callee, Cross-Module/Dataflow, Change Impact, Bug Localization, Architecture, Incremental Update, Ambiguous/Unanswerable;
- öffentliches Dev-Set und bis zum Artefakt-Freeze verborgenes Testset.

### Accuracy-Metriken

- Symbol Precision, Recall, Macro-/Micro-F1;
- Edge Precision, Recall, Macro-/Micro-F1;
- Evidence Recall@10 und nDCG@10;
- Atomic-Fact Precision, Recall und F1;
- Source-Anchor-Precision;
- Correct-Abstention-Rate;
- Unsupported-/False-confident-Claim-Rate;
- vollständiger Task-Erfolg.

Primärwerte werden über Sprache, Repository und Relation makro-gemittelt. Micro-Werte sind sekundär.

### Konjunktive Gewinnschwellen

Alle Bedingungen müssen gelten:

- Graphi Symbol-Precision ≥98 % und Recall ≥95 %;
- Graphi Edge-Precision ≥95 % und Recall ≥90 %;
- Source-Anchor-Precision ≥99 %;
- Node-Macro-F1 mindestens 3,0 Prozentpunkte über Graphify;
- Edge-Macro-F1 mindestens 3,0 Prozentpunkte über Graphify;
- Evidence Recall@10 und nDCG@10 jeweils mindestens 5,0 Prozentpunkte über Graphify;
- Agent Fact-F1 und Task-Erfolg jeweils mindestens 5,0 Prozentpunkte über Graphify;
- Unsupported-/False-confident-Claim-Rate ≤1 % und nicht schlechter als Graphify;
- untere Grenze des gepaarten, repository-stratifizierten 95-%-Bootstrap-Intervalls liegt für jede primäre Differenz über null;
- keine Tier-1-Sprache liegt beim primären F1-Punktwert hinter Graphify.

Wenn nur eine Bedingung scheitert, ist der pauschale Accuracy-Claim `NO-GO`.

## Pflichtgate B — Graphi ist schneller

Das Speed-Gate wird erst ausgewertet, nachdem das Accuracy-Gate bestanden wurde.

### Messungen

- zehn unabhängige Cold-Full-Index-Runs pro Tool und Repository;
- 100 Warmups und mindestens 1.000 gemessene Queries je Queryklasse;
- mindestens 100 vorab signierte Incremental-Patches;
- Drift-Checkpoints nach 100, 1.000 und 10.000 Updates;
- monotone Wallclock, CPU-Zeit, cgroup-v2 `memory.peak`, Output-/DB-Größe;
- p50, p95, p99 und Bootstrap-Konfidenzintervalle;
- End-to-End Time-to-correct-answer einschließlich Prozessstart und Ergebnisvalidierung.

### Konjunktive Gewinnschwellen

Alle Bedingungen müssen gelten:

- Full-Index geometrisches Mittel mindestens 2,0× schneller; untere 95-%-CI-Grenze mindestens 1,5×;
- kein Repository bei Full Index oder primärer Query mehr als 10 % langsamer;
- korrekte Warm-query-p95 mindestens 2,0× schneller;
- absolute Warm-query-p95 unter 100 ms für Search und unter 200 ms für Caller/Impact;
- offizieller Incremental-/Refresh-Pfad mindestens 2,0× schneller;
- Graphi Incremental-Freshness-p95 absolut unter zwei Sekunden;
- Agent Time-to-correct-answer mindestens 20 % niedriger;
- Peak RSS nicht höher als Graphify; Zielwert höchstens 50 % von Graphify;
- Full-vs-Incremental-Ergebnis bleibt byte-identisch für Graphi.

Falls Graphify keinen inkrementellen Befehl anbietet, wird sein offiziell dokumentierter Refresh-Pfad gemessen. `N/A` ist kein automatischer Sieg.

### Zwingende Graphi-Arbeit vor dem Gate

- `GraphLookup.Incoming`, `Outgoing` und Batchvarianten einführen;
- vorhandene SQLite-Endpoint-Indizes tatsächlich verwenden;
- `directedLookup`, Link-Stale-Edge-Bereinigung und Orphan Cleanup von Vollscans lösen;
- Phase-Timing für Walk, Parse, Write, Link, Resolve und Checkpoint erfassen;
- nur gemessene Hotspots optimieren;
- jede Optimierung gegen Gold-F1 und Full-/Incremental-Parität prüfen.

## Pflichtgate C — Graphi ist erweiterbarer

### Ehrlicher Ausgangspunkt

Graphi besitzt bessere interne Seams als Graphify: Parser-/Analyzer-Registries, Graphstore-Interface, explizite Graphformat-Version und Contract-/Parity-Tests (`core/parse/registry.go:10-100`, `engine/analysis/registry.go:15-66`, `core/graphstore/graphstore.go:55-204`, `core/model/serialize.go:11-45`).

Graphi ist heute trotzdem keine externe Plugin-Plattform. Built-ins, Backend-Auswahl, Query-Dispatch, HTTP und MCP sind im Host verdrahtet. Eine neue Extension verlangt Host-Codeänderung und Rebuild. Deshalb ist der heutige Claim „erweiterbarer als Graphify“ ebenfalls `NO-GO`.

### Stable Extension Platform v1

Graphi baut eine plattformneutrale externe Extension-Grenze:

1. **Manifest:** `graphi.extension.v1.json` mit ID, Version, API-Bereich, Capability-Arten, Input-/Output-Schemas, Hash, Berechtigungen und Ressourcenlimits.
2. **ExtensionCatalog:** einzige Quelle für CLI-, HTTP- und MCP-Capability-Beschreibungen; keine manuell duplizierten Tooltabellen.
3. **WASM/WASI-Sandbox:** Standard für Parser, Resolver und Analyzer. Kein Netzwerk, Dateisystem, Clock oder Randomness ohne explizite Berechtigung.
4. **Trusted Sidecar:** nur für Storage und Integrationen mit notwendigem OS-/Netzwerkzugriff; Aktivierung ausschließlich über `--trust-extension`.
5. **Kein Go-`plugin`:** nicht plattformstabil und nicht geeignet für den statischen CGo-freien Distributionsvertrag.
6. **Kanonisches Protokoll:** deterministisches CBOR/JSON-Schema statt Go-internen Typen wie `root any`.
7. **OperationProvider:** externe Query-/Analyzer-Operationen mit versionierten Schemas, ohne Erweiterung zentraler Union-/Switch-Typen.
8. **Storage-Capabilities:** Reader, Writer, Batch/Transaction, Metadata und Snapshot als getrennte Verträge plus öffentliche Conformance-Suite.
9. **SemVer-Negotiation:** additive Minor-Versionen funktionieren ohne Host-/Plugin-Rebuild; echte Inkompatibilität scheitert vor Ausführung typisiert.
10. **SDK und Tooling:** Generator, Referenz-SDKs, Beispiele, `graphi ext test`, Install/Uninstall und Upgrade-Check.

### Extensibility Index

Featurezahl, Stars, vorhandene Sprachen und Integrationsanzahl geben null Punkte.

| Kategorie | Punkte |
|---|---:|
| externer Sprachparser und Resolver | 20 |
| Analyzer-/Query-Operation | 15 |
| Storage-Backend | 15 |
| Transport/Integration | 10 |
| Schema-/Versionskompatibilität | 15 |
| Isolation/Sicherheit | 15 |
| Dokumentation/Testwerkzeuge | 10 |

Gewinnbedingungen:

- Graphi mindestens 90/100;
- keine Kategorie unter 80 % ihres Maximalwerts;
- Graphi mindestens 15 Punkte über Graphify;
- Median Time-to-green mindestens 25 % niedriger;
- Graphi verliert keine Aufgabenkategorie;
- alle Graphi-Erweiterungen ohne Host-Produktionscodeänderung und ohne Host-Rebuild installierbar;
- acht unabhängige Entwickler führen einen randomisierten Crossover-Test durch.

### Verbindliche Extension-Aufgaben

#### E1 — Sprachparser und Resolver

- MiniLang plus eine erst nach Protocol Freeze ausgewählte reale Sprache;
- File Detection, Watch, Parser, Resolver, Docs und Tests;
- Hidden Symbol-/Edge-F1 ≥98 %, MiniLang exakt 100 %;
- Median Time-to-green höchstens vier beziehungsweise acht Stunden;
- Extension-Laufzeit höchstens `direct × 1,10 + 5 ms pro Datei`.

#### E2 — Analyzer/Query

- externe Operation `fan_in_hotspots`;
- ohne Surface-Codeänderung automatisch in Capability-Discovery, CLI, HTTP und MCP sichtbar;
- 100 % Ergebnis-/Surface-Parität über 50 Hidden Calls;
- Median Time-to-green höchstens zwei Stunden;
- p95-Overhead höchstens `direct × 1,10 + 5 ms`.

#### E3 — Storage

- externes persistentes KV-/JSONL-Backend;
- CRUD, deterministische Listen, atomare Batches, Rollback, Reopen, Metadaten und Crash-Test;
- 100 % Conformance, keine partielle Transaktion;
- Median Time-to-green höchstens acht Stunden;
- Bridge-Overhead höchstens 20 % gegenüber demselben direkt eingebundenen Backend.

#### E4 — Transport/Integration

- externer NDJSON-Adapter mit Health, Capability-Discovery, Invoke und Events;
- neue Operationen automatisch sichtbar;
- 100 % Parität über 50 Hidden Calls;
- Median Time-to-green höchstens zwei Stunden.

#### E5 — Kompatibilität

- Host/Plugin/Client-Matrix für `1.0`, `1.1`, `1.2` und vorheriges Graphformat;
- alte Plugins laufen auf neuem Host;
- unbekannte optionale Felder werden toleriert;
- Inkompatibilität scheitert vor Ausführung typisiert;
- Snapshot-Migration N−2 ohne Datenverlust.

#### E6 — Isolation

- Fault-Plugins für Panic/Trap, Endlosschleife, 1-GiB-Allokation, riesige Ausgabe, defektes CBOR, Path Traversal und Netzwerkversuch;
- Host bleibt in 100/100 Versuchen verfügbar;
- Default-Memorylimit 128 MiB, Outputlimit 16 MiB;
- Abbruch spätestens zwei Sekunden nach Deadline;
- keine Graphmutation bei fehlgeschlagenem Aufruf;
- Quarantäne nach drei Fehlern mit Audit-Eintrag.

#### E7 — Dokumentation

- mindestens sieben von acht externen Entwicklern schaffen Parser und Analyzer nur mit offizieller Dokumentation;
- Median bis zum ersten grünen Parser höchstens 90 Minuten;
- Analyzer-Skelett höchstens 60 Minuten;
- Beispiele liegen außerhalb des Kerncodes und laufen in CI.

## Public Competitive Harness

Neues Verzeichnis: `bench/competitive/`.

### Wiederverwendete Graphi-Bausteine

- `internal/corpus` für gepinnte Repo-Materialisierung und Subprocess-Ausführung (`internal/corpus/run.go:73-240`);
- `internal/bench` für Samples, Median/p95 und Budgetdeltas (`internal/bench/harness.go:27-166`, `internal/bench/metrics.go:31-79`);
- `engine/scenario` für versionierte Aufgaben und erwartete Evidence (`engine/scenario/scenario.go:31-145`, `engine/scenario/scenario.go:213-375`);
- `internal/evalreport` für Commit-/OS-/Corpus-Provenienz und JSON/Markdown (`internal/evalreport/report.go:16-85`);
- deterministische Graphserialisierung und Edge-Provenienz als Graphi-Adapterbasis (`core/model/serialize.go:15-163`, `core/model/edge.go:138-164`);
- Full-vs-Incremental-Conformance als Freshness-Oracle (`engine/conformance/conformance_test.go:65-185`).

### Neue Adapter-Schnittstelle

```text
Prepare
Index
ExportGraph
Query
ApplyMutation
Metadata
```

Beide Adapter rufen veröffentlichte Artefakte als Subprocess auf. Für getimte Pfade ist kein In-process-Sonderweg für Graphi erlaubt.

### Artefakte je Run

- Protokollversion und Lockfile;
- Tool-/Repo-/Container-Checksums;
- Hardware, Kernel, Filesystem und Ressourcenlimits;
- Rohantworten, normalisierte Graphen und Gold-Scores;
- monotone Timings, RSS, CPU und Outputgrößen;
- Agent-Traces und echte Tokenzählung;
- Fehler und übersprungene Kategorien;
- maschinenlesbarer JSON/JSONL-Report plus Markdown.

Nach Entfernen nichtdeterministischer Timestamps muss der Report byte-stabil sein.

## Anti-Gaming-Regeln

- Versionen, Repositories, Aufgabenverteilung, Metriken und Schwellen vor Optimierung kryptografisch einfrieren.
- Gold-Daten ausschließlich aus Source und Tests erstellen, nie aus Graphi oder Graphify.
- 40 % der Accuracy-, Agent- und Extension-Fixtures bleiben verborgen.
- keine Konfiguration pro Repository, Sprache oder Aufgabe.
- keine Benchmark-Sonderpfade, Monkeypatches, `replace`-Direktiven oder vorbereiteten Task-Plugins.
- Systeme erhalten keinen Zugriff auf Gold-Fakten, Grader oder Hidden Fixtures.
- drei Agent-Runs pro Aufgabe; Reihenfolge randomisiert.
- Toolidentitäten vor Grading anonymisieren.
- zwei Grader; mindestens 20 % menschlich doppelt prüfen; Widersprüche adjudizieren; Kappa ≥0,85.
- alle Fehlversuche und Debug-Zeiten zählen im Extension-Vergleich.
- Host-Codeänderung führt zum Verlust der Installations-/Erweiterbarkeitspunkte.
- Graphi veröffentlicht jede verlorene Kategorie.
- Graphify-Maintainer werden eingeladen, Konfiguration und Ergebnisdarstellung zu prüfen.

## Umsetzungsmilestones

### C0 — Protocol Freeze und ehrliche Baseline, 2–3 Wochen, M

- aktuelle stabile Artefakte beider Tools pinnen;
- Repo-/Hardware-/Config-Locks erstellen;
- historische Graphify-Zahlen nur als Sanity-Hinweis markieren;
- derzeitige Graphi-Claims ohne Raw-Evidenz zurückziehen oder qualifizieren;
- neutralen Baseline-Report unverändert veröffentlichen.

**Exit:** Schema lehnt mutable refs, fehlende Checksums, ungleiche Limits und unprotokollierte Konfiguration ab.

### C1 — Competitive Harness und Gold-Ontologie, 6–8 Wochen, XL

- Subprocess-Adapter und Run-Artefakte;
- tool-neutrale Node-/Edge-Ontologie;
- Annotation, Double Review und Adjudication;
- PR-Subset und vollständige Nightly-Aufteilung.

**Exit:** ein Kommando erzeugt reproduzierbare Raw- und Markdown-Reports für beide Tools.

### C2 — Accuracy Corpus und Agent Battle, 10–14 Wochen, XL

- 2.000 Nodes, 4.000 Edges, 400 Retrieval-Fragen;
- 240 versiegelte Agent-Aufgaben × drei Runs;
- Blindgrading und menschliches Audit.

**Exit:** sämtliche Bedingungen aus Pflichtgate A grün.

### C3 — Performance und Freshness, 8–12 Wochen, XL

- Endpoint-Reads und Vollscan-Entfernung;
- Cold/Warm/Incremental/Drift-Suite;
- feste Runner und Statistikpipeline.

**Exit:** sämtliche Bedingungen aus Pflichtgate B grün, ohne Accuracy-Regression.

### C4 — Stable Extension Platform v1, 8–12 Wochen, XL

- Manifest, Catalog und Schema-Negotiation;
- WASI-Runtime und Fault-Suite;
- OperationProvider und Storage-Capability-Bridge;
- SDK, Generator, Conformance und Referenzextensions.

**Exit:** E1–E7 technisch grün; keine Referenzextension ändert Host-Produktionscode.

### C5 — Unabhängiger Extensibility-Vergleich, 4–6 Wochen, L

- acht externe Entwickler;
- randomisierter Crossover Graphi/Graphify;
- Hidden Tasks und Debug-Zeitmessung;
- unabhängiges Audit.

**Exit:** sämtliche Bedingungen aus Pflichtgate C grün.

### C6 — Distribution und Zwei-Minuten-UX, 4–6 Wochen, L

- signiertes Binary und verifizierter Installer;
- `graphi setup` für Codex, Claude Code und Cursor;
- Extension Install/Uninstall/Rollback;
- netzisolierter E2E und zehn moderierte First-run-Tests.

**Exit:** mindestens acht von zehn Nutzern erreichen innerhalb zwei Minuten ohne Hilfe die erste belegte Antwort.

### C7 — Externe Produktvalidierung, 8–12 Wochen, XL

- mindestens zehn Teams testen beide Produkte auf eigenen Repositories;
- vorab definierte Aufgaben und Outcomes;
- anonymisierte Raw-Evidenz ohne heimliche Telemetrie;
- externe Reviewer prüfen Harness und Claims.

**Exit:** mindestens 60 % bevorzugen Graphi für den fokussierten Code-Agent-Workflow und benennen einen konkreten Accuracy-, Speed- oder Extension-Outcome.

## CI- und Claim-Gates

### Pull Request

- deterministisches Gold-Subset;
- Graphi-only Accuracy-/Performancebudgets;
- Extension-Conformance und Fault-Subset;
- absichtliche falsche Kante, +20-%-Latenzregression und defektes Plugin müssen rot werden.

### Nightly / On demand

- vollständiger Head-to-Head auf fester Hardware;
- alle Raw-Artefakte und verlorenen Kategorien hochladen;
- keine grüne Nacht bei null ausgeführten Benchmarks;
- vorhandenen faktisch leeren `TestBench`-Aufruf ersetzen.

### Release / Public Claim

- Report muss exakt denselben Graphi-SHA wie das Release messen;
- Graphify-/Protocol-Pins dürfen nicht veraltet sein;
- fehlende Rohdatei, Versionsdrift oder stale Report macht das Gate rot;
- Website-/README-Zahlen werden aus dem archivierten Report generiert, nicht manuell gepflegt.

## Konkreter Task-Backlog

| ID | Task | Größe | Abnahmekriterium |
|---|---|---:|---|
| COMP-01 | Protocol/Artifact/Repo Lock | M | keine mutable ref; alle Checksums und Limits vollständig |
| COMP-02 | Subprocess Competitive Adapter | L | beide Releases durch identische Lifecycle-API messbar |
| COMP-03 | Gold-Ontologie und Scorer | XL | Macro/Micro-F1, Evidence, Abstention und Anchor-Checks |
| COMP-04 | Vier-Sprachen-Gold-Corpus | XL | ≥500 Nodes und ≥1.000 Edges je Sprache; Kappa ≥0,85 |
| COMP-05 | 400 Retrieval-Fragen | L | Kategorien balanciert; Dev/Hidden-Split eingefroren |
| COMP-06 | 240-Task Agent-Harness | XL | drei Runs, echte Tokens, Blindgrading und Raw Traces |
| COMP-07 | Endpoint GraphLookup | L | SQLite nutzt From-/To-Indizes; Backend-Parität grün |
| COMP-08 | Incremental-Vollscans entfernen | XL | kein globaler Stale-/Orphan-Scan; Snapshot-Parität grün |
| COMP-09 | Cold/Warm/Freshness-Suite | L | zehn Cold Runs, ≥1.000 Queries/Klasse, ≥100 Patches |
| COMP-10 | Extension Manifest und Catalog | L | Capabilities automatisch in Surfaces sichtbar |
| COMP-11 | WASI-Sandbox und Fault-Suite | XL | 100/100 Fault-Runs ohne Hostausfall oder Teilmutation |
| COMP-12 | Parser-/Analyzer-SDK | L | E1/E2 ohne Hoständerung; Hidden F1/Parität erfüllt |
| COMP-13 | Storage-/Integration-Bridge | XL | E3/E4 Conformance und Overheadgrenzen erfüllt |
| COMP-14 | SemVer-/Migration-Matrix | L | Host/Plugin/Client 1.0–1.2 und Snapshot N−2 grün |
| COMP-15 | Externer Extension-Crossover | L | acht Entwickler; Graphi ≥90/100 und +15 Punkte |
| COMP-16 | CI/Claim Gate | M | Wrong-edge, +20-%-Latenz, Drift, Missing Raw, Stale Report rot |
| COMP-17 | Zwei-Minuten-Onboarding | L | 8/10 Nutzer ohne Hilfe erfolgreich |
| COMP-18 | Externer Benchmark-Audit | M | unabhängiger Bericht bestätigt oder korrigiert Claims |

## Aufwand und Reihenfolge

Zusätzlicher Competitive-Aufwand gegenüber der reinen 9/10-Stabilisierung:

- Harness, Gold und Agent-Evaluation: 8–12 Personenmonate;
- Performancearbeit: 6–10 Personenmonate;
- Extension Platform und SDK: 8–12 Personenmonate;
- unabhängige Vergleiche/Audit/UX: 4–6 Personenmonate.

Gesamt: **26–40 zusätzliche Personenmonate** plus Annotation, Compute, externe Entwickler und Audit. Bei vier fokussierten Engineers können Accuracy/Performance und Extension Platform teilweise parallel laufen; unter Einbezug von Gold-Erstellung, Recruiting und Audit ist ein glaubwürdiger Abschluss eher nach **12–18 Monaten** als nach neun Monaten realistisch.

Kritischer Pfad:

```text
Focused Core RC → COMP-PILOT → Charter
                              ├→ C1 Harness/Gold → C2 Accuracy → C3 Speed ─┐
                              ├→ C4 Extension Platform → C5 Extensibility ┤
                              └→ C6 UX/Distribution ──────────────────────┤
                                                                          ▼
                                                        C7 Validation/Audit
```

Das Speed-Gate bleibt absichtlich nach Accuracy: Ein schneller falscher Graph ist wertlos. Die Extension Platform darf technisch parallel ab C1 entwickelt werden, ihr Sieg wird aber erst nach bestandenem Accuracy-/Speed-Protokoll öffentlich behauptet.

## Go/No-Go für „besser als Graphify“

### NO-GO

- nur Featurelisten oder Sprachanzahl verglichen;
- historische 82-%-Zahl als aktuelle Baseline übernommen;
- weniger als zwölf Repositories oder 240 Agent-Aufgaben;
- Accuracy oder Speed nur in einem zusammengesetzten Score gewonnen;
- Extension benötigt Host-Codeänderung oder Host-Rebuild;
- Raw Runs, verlorene Kategorien oder Confidence Intervals fehlen;
- eine der konjunktiven Pflichtbedingungen scheitert.

### GO

Erst wenn A, B und C vollständig grün sind, ist diese präzise Aussage zulässig:

> **Auf den gepinnten Versionen, zwölf veröffentlichten Code-Repositories und dem auditierten Competitive Protocol ist Graphi genauer, schneller und erweiterbarer als Graphify: höhere Graph- und Agent-Accuracy, niedrigere korrekte Index-/Query-/Freshness-Zeiten und schnellere sichere externe Erweiterungen ohne Host-Rebuild.**

Der Claim verlinkt Toolversionen, Repositories, Hardware, Goldschema, Aufgaben, Rohdaten, Statistik und verlorene Kategorien. Er wird bei jeder relevanten neuen Graphi- oder Graphify-Version neu geprüft.
