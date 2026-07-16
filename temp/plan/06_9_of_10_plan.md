# Graphi: Plan zu einer belastbaren Bewertung von mindestens 9,0/10

> **Status: LONG-TERM SCORE RUBRIC.** Dieses Dokument definiert Ziele und Evidenz für einen unabhängigen 9/10-Review. Seine Phasen, Tasks und Zeitangaben sind keine Execution Authority; diese liegt bei `00_master_execution_plan.md`.

## Ziel und Bewertungsregel

Das Ziel ist nicht, die vorhandene interne Scorecard umzubenennen. Das Ziel ist ein **erneuter unabhängiger Review**, der nach demselben Sechs-Perspektiven-Modell mindestens **9,0/10 im arithmetischen Mittel** ergibt.

Aktueller Stand: **5,0/10**, Risiko **HIGH** (`temp/review/00_overall_project_review.md`).

Der Zielreview gilt nur als bestanden, wenn:

- Gesamtscore mindestens **9,0/10** beträgt;
- keine Einzeldisziplin unter **8,5/10** liegt;
- Security und DevOps jeweils mindestens **9,0/10** erreichen;
- kein kritischer oder hoher ungeklärter Blocker existiert;
- jede Produkt-, Performance- und Security-Aussage durch reproduzierbare Evidenz belegt ist;
- die Abschlussbewertung von frischen Reviewern durchgeführt wird, die nicht am Umbau beteiligt waren.

### Zielwerte

| Perspektive | Heute | Ziel | Erforderlicher Sprung |
|---|---:|---:|---:|
| Architektur | 5,5 | 9,2 | +3,7 |
| Fullstack/Codequalität | 6,2 | 9,2 | +3,0 |
| Produkt & Features | 5,5 | 9,0 | +3,5 |
| Security & Privacy | 5,0 | 9,3 | +4,3 |
| DevOps/Production Readiness | 4,5 | 9,3 | +4,8 |
| Business & Monetization | 3,5 | 8,5 | +5,0 |
| **Gesamt** | **5,0** | **9,1** | **+4,1** |

Der Zielwert 9,1 schafft eine kleine Reserve. Ein technischer 9/10-Zustand bei weiterhin unbewiesenem Produkt- und Geschäftsmodell reicht nicht.

## Harte Entscheidung

**Teil-Rewrite der Systemgrenzen, radikaler Produkt-Scope-Reset und echte Marktvalidierung.**

Behalten werden Core-Modell, Provenienz, Parser-/Analyzer-Registries, Graphstore-Basis und bewährte Query-Kernels. Neu gebaut werden Read-Hotpath, Capability-Verträge, Runtime/Lifecycle, Security-Grenze, TypeScript-Client und Release-/Distribution. Gleichzeitig wird das Produkt auf einen einzigen Hero-Workflow reduziert.

Bis Phase 5 gilt ein Feature-Freeze. Keine neuen Sprachen, Analyzer, MCP-Tools, CLI-Kommandos, Surfaces oder SaaS-Komponenten.

Der direkte Wettbewerbsmaßstab gegen Graphify ist im ergänzenden Plan `temp/plan/07_beat_graphify_competitive_plan.md` festgelegt. Dieser Competitive Claim ist eine **separate strategische Wette**, kein Bestandteil der ursprünglichen sechs Score-Dimensionen und keine Voraussetzung für einen technisch sicheren Core-Release oder den unabhängigen 9/10-Review. Wird die Wette separat gechartert, müssen Genauigkeit, Geschwindigkeit und Erweiterbarkeit trotzdem alle einzeln grün sein.

## Definition eines 9/10-Produkts

Graphi ist dann ein 9/10-Projekt, wenn ein Entwickler oder Coding-Agent ein reales Repository lokal indexieren und anschließend schnell, korrekt und nachvollziehbar beantworten kann:

1. Wo ist dieses Symbol definiert?
2. Wer ruft es auf und was ruft es selbst auf?
3. Welche Dateien und Symbole sind von einer Änderung betroffen?
4. Welche Evidenz und Confidence stützen die Antwort?
5. Wie bekommt der Agent den kleinsten belastbaren Kontext für die nächste Änderung?

Alles andere ist sekundär. PR-Automation, Refactoring, Security-Scanning, Memory, Skills, TUI, IDE und SaaS dürfen diesen Kern erst erweitern, wenn der Kern die 9/10-Gates bestanden hat.

## Programmumfang und realistische Dauer

### Referenzteam

- 2 Senior Go/Platform Engineers
- 1 Senior Fullstack/TypeScript Engineer
- 1 Security/DevOps Engineer
- 1 Product Lead mit Research-/Design-Verantwortung
- 1 Founder/GTM-Verantwortlicher, anfangs 50 %, später Vollzeit

### Dauer

- Focused Core Release Candidate: **11–18 Wochen** gemäß autoritativem Master
- belastbare Produkt- und Business-Evidenz: **12–18 Monate**
- glaubwürdiger externer 9/10-Re-Review: **frühestens nach 15 Monaten**

Gesamtaufwand für Core-Stabilisierung, Produktreife, Security/DevOps-Evidenz und Marktvalidierung: grob **35–60 Personenmonate**. Das optionale Competitive-/Extension-Programm benötigt separat weitere 26–40 Personenmonate. Mit nur einer Person ist ein ehrlicher 9/10-Zustand nicht realistisch.

## Phase 0 — Stoppen, vereinfachen, Wahrheit herstellen

**Zeitraum:** Woche 1–2<br>
**Priorität:** P0
**Aufwand:** M

### Änderungen

- Stable-Capability-Manifest einführen: nur CLI und MCP-stdio sowie der Read-Hero-Workflow gelten als stabil.
- HTTP, MCP-HTTP, VS Code, Web, TUI, GitHub Action, PR-Suite, Memory, Skillgen, Taint und Refactorings aus Stable-/GA-Claims entfernen.
- `extract` und `move` fail-closed schalten, bevor eine Datei verändert wird (`engine/edit/refactor.go:174-208`).
- Auto-Release deaktivieren, bis ein einziger autoritativer Release-DAG existiert (`.github/workflows/auto-release.yml:36-101`).
- öffentliche Feature-Dokumentation aus dem maschinenlesbaren Capability-Manifest generieren.
- aktuelle 100/100-Scorecard in „synthetic regression scorecard“ umbenennen; sie darf nicht mehr als Produktreife-Beleg dienen (`docs/release-scorecard.md:9-49`).

### Gate 0

- Kein öffentliches Stable-Feature besitzt nur einen `Unavailable`-Stub.
- Kein nicht implementiertes Refactoring kann Quellcode verändern.
- Kein rotes Gate kann Tag oder Release erzeugen.
- README, CLI-Hilfe, MCP-Deskriptoren und Release-Artefakt zeigen dasselbe Stable-Capability-Set.

## Phase 1 — Architektur auf 9/10 bringen

**Zeitraum:** Monat 1–3<br>
**Priorität:** P0
**Aufwand:** XL

### Änderungen

1. `GraphLookup` mit `Incoming`, `Outgoing` und Batch-Varianten einführen.
2. SQLite-Endpoint-Indizes tatsächlich für Traversals verwenden (`core/graphstore/sqlite.go:187-192`).
3. Query-Service von vollständigen Kanten-Scans lösen (`engine/query/service.go:145-173`).
4. Monolithisches `client.Client` in `QueryClient`, `SearchClient`, `AgentContextClient` und getrennte experimentelle Write-/Forge-Ports zerlegen (`surfaces/client/client.go:268-464`).
5. Einen zentralen `Runtime`-/Composition Root einführen; Store, Ingester, Watcher und Services werden exakt einmal erzeugt und geschlossen.
6. Capability-Manifest direkt aus registrierten Ports/Transports ableiten.
7. Ingest-Phasen nur dann tiefer neu schneiden, wenn Fault-Injection reale Cross-Store-Recovery-Fehler zeigt.

### Gate Architektur ≥9,2

- Nachbarschaftsabfragen sind proportional zum Knotengrad, nicht zu allen Kanten einer Art.
- SQLite und Memory bestehen denselben deterministischen Contract-Test.
- Kein stabiler Transport implementiert unpassende Methoden durch Sentinel-Stubs.
- Keine duplizierten Composition Roots in CLI, Daemon und HTTP.
- Layerguard prüft zusätzlich explizit erlaubte Same-Layer-Abhängigkeiten.
- Fault-Injection deckt jede Meta-/Graphstore-Commitgrenze ab.
- Architekturdiagramm, Capability-Manifest und reale Imports stimmen maschinell geprüft überein.

## Phase 2 — Security und Privacy: Ziel 9,3, Pass Floor 9,0

**Zeitraum:** Monat 2–4<br>
**Priorität:** P0
**Aufwand:** XL

### Änderungen

- dokumentiertes Threat Model für andere lokale Nutzer/Prozesse, DNS-Rebinding, Browser, Plugins, MCP-Clients und untrusted Repositories.
- gemeinsames `SecurityEnvelope` für HTTP/SSE/MCP-HTTP: Token-Authentisierung, Host-Allowlist, Origin default-deny, Capability-Scopes und konstante Token-Prüfung.
- Read-/Write-Routen strikt trennen; Netzwerktransporte default read-only.
- globale Request- und JSON-Body-Limits, `ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout` und begrenzter Shutdown.
- Memory-Dateien und Ledger konsequent `0600`, Parent-Verzeichnisse `0700`; bestehende Rechte migrieren.
- vermutete Secrets standardmäßig nicht persistieren; Override nur lokal, explizit und auditierbar.
- Memory-Export nimmt keinen freien Server-Dateipfad mehr an.
- Release-SBOM, Provenance und Signaturen; externe Actions auf volle Commit-SHAs pinnen.
- `govulncheck`, Dependency Review, Secret Scan und SAST als blockierende Gates.
- CGO-Broad-Parser entweder out-of-process isolieren oder dauerhaft als unsicheres Labs-Feature kennzeichnen.

### Gate Security: Ziel ≥9,3; Abschluss-Pass ≥9,0

- unabhängiger Pentest ohne offene High-/Critical-Findings.
- Negativtests für Token, Host, Origin, Scopes, Slowloris, Oversize Body, Path Traversal, Symlinks und Secret-Persistenz.
- kein repository-sensitiver Netzwerkendpunkt ohne Authentisierung.
- kein sensibler Zustand mit Gruppen-/World-Read-Rechten.
- Artefakt, SBOM, Attestation und Commit sind kryptografisch miteinander verbunden.
- dokumentierter Security-Response-Prozess mit Verantwortlichen und Fristen.

## Phase 3 — Codequalität und Nutzeroberflächen auf 9/10 bringen

**Zeitraum:** Monat 3–5<br>
**Priorität:** P1
**Aufwand:** L–XL

### Änderungen

- gemeinsamer generierter TypeScript-Client für Web und VS Code; keine manuell kopierten Payloadtypen.
- VS-Code-Suchvertrag gegen echte Go-Server-Fixtures testen (`extensions/vscode/src/contract.ts:104-107`).
- Graph-Edge-ID statt `source→target` verwenden, damit Parallelkanten erhalten bleiben (`web/src/GraphView.tsx:108-118`).
- Stale-Response-, Cancellation- und SSE-Race-Tests ergänzen.
- God-Files entlang echter Verantwortungen aufteilen, nicht nach willkürlicher Zeilenzahl.
- Lint, Race, Contract, Mutation-/Property-Tests und Coverage für kritische Pfade blockierend machen.
- alle öffentlichen Fehler typisieren und stabil versionieren.

### Gate Fullstack ≥9,2

- Web und VS Code können nicht gegen unterschiedliche API-Verträge bauen.
- alle bekannten Funktionsdefekte besitzen reproduzierbare Regressionstests.
- keine verlorenen Parallelkanten, keine stale async updates, keine stillen Schema-Fallbacks.
- kritische Services haben klare Ownership, kleine Ports und keine verdeckten globalen Einstellungen.
- Testpyramide enthält Unit-, Contract-, Integration-, Subprocess- und Consumer-E2E-Tests.
- kein stabiler Feature-Claim ohne End-to-End-Test aus Nutzerperspektive.

## Phase 4 — DevOps und Production Readiness: Ziel 9,3, Pass Floor 9,0

**Zeitraum:** Monat 3–6<br>
**Priorität:** P0/P1
**Aufwand:** XL

### Änderungen

- Daemon mit `Done`, Readiness, `signal.NotifyContext` und idempotentem Shutdown neu schneiden.
- echter Subprocess-Test: Start → Ready → Query → Stop → Exit → Restart.
- ein Commit-gebundener Release-DAG: Gate → Build → Reproducibility → SBOM/Attestation → Tag → Publish.
- GitHub Action installiert das tatsächlich angegebene Release-Artefakt und läuft in einem fremden Consumer-Repository.
- Liveness, Readiness, Diagnose, strukturierte datensparsame Logs und lokale Betriebsmetriken trennen.
- Timeouts für alle CI-Jobs, minimale Permissions, klare Required-Checks-Dokumentation.
- Windows-Support entweder mit nativem CI-/Daemon-E2E belegen oder aus der Supportmatrix entfernen.
- Upgrade-, Backup-, Restore- und Schema-Migrations-Runbooks erstellen und testen.

### Gate DevOps: Ziel ≥9,3; Abschluss-Pass ≥9,0

- Daemon stoppt auf allen unterstützten Plattformen innerhalb von fünf Sekunden ohne Restprozess/Socket.
- bewusst rotes Release-Gate erzeugt keinen Tag und kein Artefakt.
- Consumer-E2E beweist Version, Outputs und Upgradepfad der GitHub Action.
- reproduzierbare Builds, SBOM und Attestation für jede unterstützte Zielplattform.
- Restore-Drill und fehlerhafte Migration werden getestet.
- mindestens 90 Tage grüne Release-/Nightly-Historie ohne ungeklärte flakiness.
- SLOs für Start, Indexierung, Query und Crashfreiheit sind definiert und gemessen.

## Option 4A — Competitive Extension Platform, nicht Teil des 9/10-Kernpfads

**Zeitraum:** Monat 5–10, parallel zur Produktvalidierung<br>
**Priorität:** P1
**Aufwand:** XL

**Aktivierungstrigger:** Focused Core RC grün, mindestens drei glaubwürdige Extension-Jobs, zwei externe Prototyp-Autoren und separates Budget. Interne Go-Registries reichen für einen Competitive Extensibility Claim nicht; ohne diesen Trigger wird die Plattform nicht gebaut.

### Änderungen

- versioniertes `graphi.extension.v1.json` mit Capability, Schemas, Hash, Berechtigungen und Ressourcenlimits;
- zentraler `ExtensionCatalog`, aus dem CLI-, HTTP- und MCP-Capability-Beschreibungen entstehen;
- WASM/WASI-Sandbox für Parser, Resolver und Analyzer;
- explizit vertrauenswürdiges Sidecar-Protokoll für Storage und OS-/Netzwerkintegrationen;
- kanonisches deterministisches CBOR-/JSON-Schema statt interner Go-Typen;
- externe `OperationProvider`, ohne zentrale Params-/Analysis-Union zu erweitern;
- kleine Storage-Capability-Interfaces und öffentliche Conformance-Suite;
- SemVer-Negotiation, N−2-Snapshotmigration und typisierte Inkompatibilitätsfehler;
- SDK, Generator, Referenzextensions und `graphi ext test`.

### Separates Competitive Extension Gate

- Graphi erreicht mindestens 90/100 im Extensibility Index aus `07_beat_graphify_competitive_plan.md`;
- mindestens 15 Punkte Vorsprung vor dem gepinnten Graphify-Stand;
- keine Extension-Aufgabenkategorie geht verloren;
- Median Time-to-green mindestens 25 % niedriger;
- Parser, Analyzer, Storage und Transport werden ohne Host-Produktionscodeänderung und ohne Host-Rebuild installiert;
- acht unabhängige Entwickler absolvieren einen randomisierten Crossover-Test;
- Fault-Plugins für Trap, Endlosschleife, Speicher-, Output-, Path- und Netzwerkangriffe lassen den Host in 100/100 Versuchen verfügbar und erzeugen keine Teilmutation.

## Phase 5 — Produktqualität auf 9/10 bringen

**Zeitraum:** Monat 4–9<br>
**Priorität:** P1
**Aufwand:** XL

### Hero-Workflow

`Installieren → Repository indexieren → Agent verbinden → Symbol/Task erklären → zitierbaren minimalen Kontext erhalten`

### Änderungen

- 20–30 versionierte reale Agent-Aufgaben mit erwarteten Evidenzankern definieren.
- mindestens fünf repräsentative Repositories: großes Java-Monorepo, Go, TypeScript, Python und eine gemischte Codebasis.
- Full-run messen: Wall-clock, Time-to-first-value, Peak-RAM, DB-Größe, Warm-p95, Recall/Precision und Antwortnützlichkeit.
- Onboarding auf maximal einen primären Pfad reduzieren.
- Fehler-, Ambiguitäts- und Partial-Support-Zustände sichtbar machen.
- fünf moderierte Usability-Runden durchführen und Blocker vor weiterer Featurearbeit beheben.
- Stable/Experimental/Unsupported pro Sprache und Capability veröffentlichen.

### Gate Produkt ≥9,0

- mindestens 80 % der Testnutzer erreichen den Hero-Workflow ohne Hilfe.
- mediane Time-to-first-value und Abbruchrate sind definiert und über drei Releases stabil verbessert.
- Kernantworten bestehen vorab festgelegte Accuracy-/Evidence-Gates auf allen unterstützten Tier-1-Sprachen.
- großer Monorepo-Full-run erfüllt veröffentlichte Zeit-, RAM- und Speicherbudgets; keine Proxy-Metrik.
- mindestens 20 externe Teams nutzen Graphi vier Wochen wiederholt.
- mindestens 60 % der aktivierten Teams sind nach acht Wochen noch aktiv oder nennen einen dokumentierten externen Grund für den Abbruch.
- kein GA-Feature ohne dokumentierten Nutzerjob und Nutzungsnachweis.

## Phase 6 — Business und Monetarisierung auf mindestens 8,5 bringen

**Zeitraum:** Monat 6–15<br>
**Priorität:** P1 nach technischem RC
**Aufwand:** XL

### Änderungen

1. ICP auf ein Segment begrenzen, beispielsweise Teams mit privaten großen Repositories und strengen No-Egress-Anforderungen.
2. 30 problemorientierte Interviews durchführen; keine Feature-Pitches in der ersten Hälfte.
3. 5–10 Design-Partner mit gemeinsam definiertem Erfolgskriterium gewinnen.
4. Drei bezahlbare Hypothesen testen: self-hosted Team-Policy, CI-Audit/Compliance-Artefakte und Support/SLA.
5. Keine Hosted-Code-SaaS-Architektur bauen, solange sie dem Local-first-Vertrag widerspricht.
6. transparentes OSS-/Paid-Packaging, Preisexperiment, Supportumfang und Upgradepfad veröffentlichen.
7. privacy-kompatible, explizit opt-in Aktivierungs-/Retention-Messung oder manuelles Customer-Success-Tracking etablieren.

### Gate Business ≥8,5

- klarer ICP, Buyer, Budget Owner und dringender Job sind dokumentiert.
- mindestens fünf zahlende Organisationen oder verbindliche bezahlte Piloten.
- mindestens drei Kunden verlängern oder erweitern nach der Pilotphase.
- Preis und Packaging wurden mit realen Kaufentscheidungen getestet.
- Supportkosten, Bruttomarge und Maintainer-Kapazität sind gemessen.
- mindestens ein wiederholbarer Akquisitionskanal funktioniert.
- bezahlter Wertzaun ergänzt den Apache-2.0-Core, ohne Privacy-Versprechen zu brechen.

Wenn diese Evidenz ausbleibt, bleibt Graphi ein fokussiertes OSS-Projekt. Das ist ein gültiges Ergebnis, aber kein Business-Score von 8,5.

## Phase 7 — Externer 9/10-Abschlussreview

**Zeitraum:** nach mindestens 90 Tagen stabiler Nutzung des RC
**Aufwand:** M

### Ablauf

- sechs neue Reviewer mit frischem Kontext einsetzen.
- Reviewer erhalten Repo, Release-Artefakte, Evidenzpaket und reale Runbooks, aber keine Zielscore-Vorgabe.
- Security-Reviewer erhält zusätzlich Pentest und Threat Model.
- DevOps-Reviewer führt Restore, Upgrade, Daemon-Lifecycle und Release-Reproduktion selbst aus.
- Produkt-/Business-Reviewer erhalten anonymisierte Aktivierungs-, Retention-, Pilot- und Zahlungsnachweise.
- jede Dimension wird unabhängig bewertet; Gesamtwert wird erst nach Abgabe berechnet.

### Abschluss-Gate

- Mittelwert ≥9,0;
- keine Dimension <8,5;
- Security und DevOps ≥9,0;
- keine High-/Critical-Blocker;
- alle Abweichungen vom Ziel werden veröffentlicht und nicht durch interne Gegenscores überschrieben.

## Kritischer Pfad

```text
Phase 0: Freeze/Wahrheit
    → Phase 1: Architektur
    → Phase 2: Security
    → Phase 4: Production RC
    → Phase 5: reale Produktnutzung
    → Phase 6: bezahlte Validierung
    → Phase 7: unabhängiger Re-Review
```

Phase 3 läuft parallel zu Phase 2/4. Business Discovery darf früh beginnen; bezahlte Produktentwicklung beginnt erst nach technischem RC und nachgewiesenem Nutzerproblem.

## Messbares Score-Dashboard

| Bereich | Führende Kennzahlen | No-Go-Signal |
|---|---|---|
| Architektur | Traversal-p95, Peak-RAM, Stub-Anzahl, Dependency-Verstöße | O(E)-Traversal oder Stable-Stubs |
| Fullstack | Contract-Drift, E2E-Passrate, Race-/Flake-Rate | manuell divergierende Verträge |
| Competitive Extensibility (optional, unscored) | Extensibility Index, Time-to-green, Hoständerungen, Kompatibilitätsmatrix, Fault-Isolation | Plugin braucht Host-Rebuild oder eine Kategorie verliert gegen Graphify |
| Security | offene Findings, Auth-Negativtests, Dateirechte, SBOM/Attestation | offene High/Critical-Findings |
| DevOps | Release-Erfolg, Lifecycle-E2E, Restore-Drill, 90-Tage-Stabilität | ungeprüfte oder nicht reproduzierbare Releases |
| Produkt | Activation, Time-to-value, Task-Erfolg, 8-Wochen-Retention | keine wiederholte externe Nutzung |
| Business | bezahlte Piloten, Verlängerung, Supportkosten, Akquisitionskanal | keine reale Kaufentscheidung |

## Was ausdrücklich nicht getan wird

- keine neue Feature-Welle, um Scores kosmetisch zu erhöhen;
- kein Full Rewrite des Cores;
- keine erfundenen Performance-SLAs vor Baseline;
- keine interne 100/100-Scorecard als Ersatz für reale Nutzer- und Marktbelege;
- kein SaaS-Pivot gegen das Local-first-Versprechen;
- kein „shipped“-Label für Code, der nicht als Release-Artefakt konsumierbar und E2E-geprüft ist;
- keine 9/10-Selbsteinstufung ohne unabhängigen Abschlussreview.

## Nicht-autoritative Evidenzbausteine

Die folgende Liste beschreibt mögliche Evidenzbausteine. Ausführbare Reihenfolge, IDs und Finanzierung stehen ausschließlich in `00_master_execution_plan.md`.

| Reihenfolge | Task | Aufwand | Abnahme |
|---:|---|---:|---|
| 1 | Stable-Capability-Manifest und Competitive Protocol einfrieren | M | Manifest=Dispatch=Docs; Tools, Repos, Metriken und Schwellen kryptografisch gepinnt |
| 2 | Auto-Release sperren und `extract`/`move` fail-closed machen | S | kein Publish bei rotem Gate; keine Mutation |
| 3 | rote Regressionstests und ehrliche Baseline anlegen | M | bekannte Blocker rot; heutige Accuracy-/Speed-Claims mit Raw-Evidenz verknüpft oder qualifiziert |
| 4 | Subprocess-Harness, Gold-Ontologie und Scorer bauen | XL | identische Adapter; Macro-F1, Evidence, Abstention, Timings und Raw Reports |
| 5 | `GraphLookup.Incoming/Outgoing` und Batchpfade implementieren | L | SQLite nutzt Endpoint-Indizes; Backend-Parität grün |
| 6 | Capability-Ports und Runtime Root einführen | XL | keine Stable-Stubs; Services einmal owned |
| 7 | Threat Model, `SecurityEnvelope` und Daemon-Lifecycle liefern | XL | Security-Negativsuite; Stop binnen fünf Sekunden; Restart grün |
| 8 | autoritativen Release-DAG bauen | L | exakter SHA, SBOM, Attestation, Red-Gate-Test |
| 9 | Extension Manifest, Catalog und WASI-Sandbox bauen | XL | Parser/Analyzer ohne Hoständerung; Fault-Suite 100/100 |
| 10 | SDK, Storage-/Integration-Bridge und SemVer-Matrix liefern | XL | E1–E7 grün; N−2-Migration ohne Datenverlust |
| 11 | Accuracy-, Speed- und Extensibility-Head-to-Head durchführen | XL | alle konjunktiven Gates aus Plan 07 vollständig grün |
| 12 | externe Produkt-/Design-Partner-Validierung starten | XL | Full-run-Rohdaten, unabhängiger Audit und wiederholte reale Nutzung |

## Schlussurteil

Eine Bewertung von mindestens 9/10 ist erreichbar, aber nicht durch drei Monate Refactoring allein. Sie verlangt einen technisch starken Core, belastbare Security-/DevOps-Evidenz, reale Produktnutzung und kommerzielle Validierung. Der optionale Claim „genauer, schneller und erweiterbarer als Graphify“ ist davon getrennt. Selbst nach technischem Erfolg bleibt der Gesamtscore unter 9, solange Produktnutzung, Retention und Zahlungsbereitschaft `UNKNOWN` sind. Der Abschluss ist deshalb frühestens nach etwa 15 Monaten glaubwürdig.
