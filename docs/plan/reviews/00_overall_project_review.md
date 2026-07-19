# Gesamtprojekt-Review: Graphi

## Kurzurteil

Graphi ist **kein schlechtes Projekt und kein Full-Rewrite-Kandidat**. Der Core besitzt echte Substanz: deterministische Modelle, Provenienz, registrierbare Parser, SQLite-Persistenz, umfangreiche Tests und eine klare Local-first-Idee (`core/model/edge.go:48-113`, `core/parse/registry.go:10-100`, `core/graphstore/graphstore.go:55-204`).

Brutal ehrlich: Das Projekt hat schneller Features und Oberflächen produziert, als es deren Korrektheit, Sicherheit, Distribution und realen Nutzerwert beweisen konnte. 142 behauptete Capabilities, 42 MCP-Tools, 42 CLI-Kommandos und acht Surfaces bei v0.4.0 sind kein Reifezeichen, sondern Scope-Entgleisung (`docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-177`). Mehrere öffentlich beworbene Pfade sind konkret falsch oder irreführend: die GitHub Action baut nicht zuverlässig Graphi, der VS-Code-Suchvertrag ist gedriftet, HTTP-Authentisierung fehlt trotz Token-UI, der Daemon beendet seinen Prozess nach `stop` nicht, Parallelkanten gehen im Web verloren und `extract`/`move` halten ihre Semantik nicht ein (`extensions/github-action/action.yml:101-125`, `extensions/vscode/src/contract.ts:104-107`, `surfaces/http/server.go:181-212`, `cmd/graphi/main.go:1138-1145`, `web/src/GraphView.tsx:108-118`, `engine/edit/refactor.go:174-186`).

## Gesamtscore

**5,0 / 10**

Arithmetisches Mittel der sechs unabhängigen Bewertungen, auf eine Dezimalstelle gerundet:

| Perspektive | Score | Harte Empfehlung |
|---|---:|---|
| Architektur | 5,5 | Refactor |
| Senior Fullstack | 6,2 | Refactor |
| Produkt & Features | 5,5 | Produkt-/Scope-Refactor |
| Security & Privacy | 5,0 | Teil-Rewrite der Surface-Security |
| DevOps & Production Readiness | 4,5 | Teil-Refactor |
| Business & Monetization | 3,5 | Produkt-/Packaging-/Distribution-Refactor |

Der Score bedeutet: **starker technischer Kern, unzuverlässige Produktperipherie, unbewiesene Marktreife**. Eine interne 100/100-Scorecard ändert daran nichts; deren Per-Repo-Metriken sind leer und die Szenarien überwiegend Fixtures oder synthetisch (`docs/release-scorecard.md:9-49`).

## Risiko-Level

**HIGH**

Nicht `CRITICAL`, weil kein belegter Remote-Unauthenticated-RCE-Pfad im Default-Betrieb gefunden wurde und der Local-first-/Loopback-Guard reale Schutzwirkung hat (`surfaces/guard/guard.go:25-98`). Nicht `MEDIUM`, weil mehrere reale Release-, Security-, Lifecycle- und Funktionsblocker gleichzeitig existieren. Besonders gefährlich ist die Kombination aus überzogenen Produktclaims und nicht durch reale Repositories bewiesener Korrektheit.

## Klare Entscheidung

**TEIL-REWRITE**

Kein Full Rewrite. Kein blindes Weiterbauen. Der Core bleibt. Neu geschnitten werden die fehlerhaften Systemgrenzen:

- Surface-/Capability-Verträge und HTTP/MCP-Security
- Ingest-/Query-Hotpaths und Lifecycle-Orchestrierung
- gemeinsamer Web-/VS-Code-Clientvertrag
- Release-/Action-Distribution

Der Begriff Teil-Rewrite ist bewusst härter als „Refactor“: Bei Authentisierung, Capability-Segregation, Daemon-Lifecycle und Action-Packaging reichen lokale Pflaster nicht. Diese Grenzen brauchen neue Verträge und neue End-to-End-Gates. Der Engine-Core braucht dagegen keinen Neustart.

## Top 10 Probleme

1. **GitHub Action ist als Consumer-Distribution vermutlich funktionsunfähig.** `graphi-version` steuert die Source-Selektion nicht; `go build ./cmd/graphi` läuft im Consumer-Workspace (`extensions/github-action/action.yml:101-125`). Schwere: kritisch, Wahrscheinlichkeit: hoch, Auswirkung: Team-/CI-Kanal kaputt.
2. **HTTP-Sicherheit ist nur behauptet, nicht durchgesetzt.** Keine serverseitige Authentisierung, kein Host-/Origin-Schutz; der VS-Code-Client sendet einen Bearer-Token, den der Server nicht prüft (`surfaces/http/server.go:181-212`, `extensions/vscode/src/graphiClient.ts:138-156`). Schwere: hoch, Wahrscheinlichkeit: hoch, Auswirkung: lokaler Datenzugriff/DNS-Rebinding-Risiko.
3. **Daemon-Lifecycle ist defekt.** `daemon stop` schließt Listener/Socket, aber der CLI-Prozess hängt weiter in `select {}` (`surfaces/daemon/daemon.go:170-186`, `cmd/graphi/main.go:1138-1145`). Schwere: hoch, Wahrscheinlichkeit: sicher, Auswirkung: Zombie-Prozess und unzuverlässiger Betrieb.
4. **Öffentliche Features liefern falsches Verhalten.** VS-Code-Suche erwartet falsche Felder, Web verwirft Parallelkanten, `extract`/`move` sind semantisch keine echten Refactorings (`extensions/vscode/src/contract.ts:104-107`, `web/src/GraphView.tsx:108-118`, `engine/edit/refactor.go:174-186`). Schwere: hoch, Wahrscheinlichkeit: sicher, Auswirkung: Nutzervertrauen und Datenkorrektheit.
5. **Scope ist außer Kontrolle.** 142 Capabilities über acht Surfaces erzeugen Support-, Test- und Dokumentationskosten ohne belegte Adoption (`docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-177`). Schwere: hoch, Wahrscheinlichkeit: sicher, Auswirkung: jede weitere Änderung wird teurer und riskanter.
6. **Traversal skaliert am falschen Kostenmodell.** Caller/Callee-Queries filtern vollständige Kantenklassen im Service, obwohl SQLite From-/To-Indizes besitzt (`engine/query/service.go:145-173`, `core/graphstore/sqlite.go:187-192`, `core/graphstore/sqlite.go:841-866`). Schwere: hoch, Wahrscheinlichkeit: hoch bei großen Repos, Auswirkung: Latenz/RAM.
7. **Capability-Parität wird mit Byte-Parität verwechselt.** Das breite `Client`-Interface wird von Remote-Adaptern mit zahlreichen `Unavailable`-Stubs erfüllt (`surfaces/client/client.go:268-464`, `surfaces/daemon/client.go:203-301`). Schwere: hoch, Wahrscheinlichkeit: sicher, Auswirkung: inkonsistente Produktoberflächen.
8. **Privacy-at-rest ist unzureichend.** Memory kann als Klartext mit `0644` angelegt werden; vermutete Secrets werden markiert, aber trotzdem persistiert (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`). Schwere: hoch, Wahrscheinlichkeit: mittel, Auswirkung: Quellcode-/Secret-Leak auf Mehrbenutzersystemen.
9. **Release-DAG und Supply Chain sind nicht hart genug.** Auto-Release ist nicht sauber an den vollständigen Release-Gate-Commit gekoppelt; Actions und Artefakte sind nicht durchgehend SHA-/Signatur-/Provenance-gesichert (`.github/workflows/auto-release.yml:36-101`, `.github/workflows/release.yml:135-205`). Schwere: hoch, Wahrscheinlichkeit: mittel, Auswirkung: kompromittierte oder ungeprüfte Releases.
10. **Produkt- und Geschäftswert sind unbewiesen.** Keine repo-belegten Adoption-, Retention-, Zahlungs- oder Umsatzdaten; kein Angebot, Pricing oder Funnel (`site/index.html:35-46`, `site/index.html:583-611`). Schwere: hoch, Wahrscheinlichkeit: sicher, Auswirkung: hohe Entwicklungsinvestition ohne nachgewiesenen Markt.

## Top 10 Chancen

1. **Local-first MCP-Code-Intelligence als enger OSS-Wedge.** Der Nutzen und die Datenschutzpositionierung sind klar (`readme.md:96-117`).
2. **Core behalten statt Rewrite-Kapital zu vernichten.** Modell-, Provenienz-, Parser- und Graphstore-Invarianten sind wertvoll und testbar (`core/model/edge.go:48-113`, `core/parse/registry.go:10-100`).
3. **Caller/Impact/Explain/Related Files als kleines glaubwürdiges MVP.** Diese Workflows passen direkt zum Kernversprechen (`readme.md:96-110`).
4. **Endpoint-selektive Graphabfragen nutzen bereits vorhandene SQLite-Indizes.** Hoher Performance-ROI ohne Datenmodell-Rewrite (`core/graphstore/sqlite.go:187-192`).
5. **Explizites Capability-Manifest statt Runtime-Stubs.** Macht Surfaces ehrlich, kleiner und unabhängig entwickelbar (`surfaces/client/client.go:268-464`).
6. **Shared TypeScript Client für Web und VS Code.** Beseitigt den bereits eingetretenen Contract-Drift (`extensions/vscode/src/contract.ts:1-6`, `web/src/contract.gen.ts:1-20`).
7. **Privacy-/Security-Positionierung als Enterprise-Hebel.** Zero-egress-Gates und Local-first-Vertrag sind ungewöhnlich stark (`.github/workflows/privacy-audit.yml:21-55`, `docs/setup-privacy.md:42-63`).
8. **Self-hosted Team-/CI-Workflow als spätere Paid-Hypothese.** PR-Gates, zentrale Policies und Audit-Artefakte passen besser zum Markenkern als ein sofortiger SaaS-Pivot (`extensions/github-action/action.yml:54-95`).
9. **Reale öffentliche Benchmarks als Vertrauensasset.** Das Projekt dokumentiert Feldfehler bereits offen; echte Post-Fix-Monorepo-Messungen würden Behauptungen belastbar machen (`docs/real-world-report.md:9-40`).
10. **Release- und Testdisziplin als Basis für schnelle Konsolidierung.** Reproduzierbare Builds, Conformance-Gates und hoher Testanteil verkürzen einen gezielten Teil-Rewrite (`internal/release/build.go:146-199`, `.github/workflows/lint.yml:18-60`).

## Was erhalten bleibt

- `core/model`, Graphstore-Verträge und SQLite-Schema als Ausgangsbasis
- Parser-/Analyzer-Registries und deterministische Provenienz
- Query-/Analyse-Kernels, soweit reale Corpus-Gates ihre Korrektheit belegen
- Local-first-/Zero-egress-Guard und Datenschutz-Tests
- Reproduzierbare Build- und Release-Utilities

## Was ersetzt oder neu geschnitten wird

- monolithisches `surfaces/client.Client` durch capability-spezifische Ports plus Manifest
- HTTP/MCP-HTTP-Grenze mit echter Authentisierung, Host-/Origin-Regeln, Body-/Timeout-Grenzen
- Daemon-/HTTP-Lifecycle mit Signalsteuerung, `Done`, Graceful Shutdown und Readiness
- Graph-Traversal-API mit endpoint-selektiven Store-Abfragen
- gemeinsamer generierter TypeScript-Client für Web und VS Code
- GitHub Action mit echter versionierter Binary-/Source-Auswahl und Consumer-E2E-Test
- Release-DAG mit Commit-Bindung, SHA-Pins, SBOM und signierter Provenance

## Was eingefroren oder gelöscht wird

- sofortiger Feature-Freeze für neue Analyzer, Tools, Sprachen und Surfaces
- experimentelle PR-, Memory-, Skill-, Security- und Refactoring-Funktionen aus dem stabilen Produktversprechen entfernen, bis reale Gates existieren
- falsche oder nicht implementierte `extract`-/`move`-Claims deaktivieren oder als experimentell kennzeichnen
- nicht konsumierbare GitHub Action nicht als „shipped“ führen
- keine SaaS-, Billing- oder Enterprise-Control-Plane bauen, bevor Nachfrage und Zahlungsbereitschaft belegt sind

## Widersprüche zwischen den Experten und Auflösung

### Refactor versus Teil-Rewrite

Architektur, Fullstack, Produkt und Business empfehlen „Refactor“. Security fordert explizit einen Teil-Rewrite der Surface-Security; DevOps einen Teil-Refactor. Der Widerspruch ist semantisch, nicht sachlich: Kein Experte will den Core verwerfen. Die Entscheidung lautet **Teil-Rewrite der Systemgrenzen**, weil Auth-, Capability-, Lifecycle- und Distribution-Verträge neu definiert werden müssen. Der Core bleibt erhalten.

### Technische Stärke versus Produktionsreife

Fullstack bewertet den Core hoch, DevOps nur 4,5/10. Beides stimmt: interne Code- und Testqualität ist überdurchschnittlich, die tatsächlichen Betriebs- und Distributionspfade sind trotzdem fehlerhaft. Testmenge ist kein Ersatz für funktionierende Consumer-Journeys.

### Breite als Stärke versus Scope-Sprawl

Produkt und Business erkennen mehrere Distributionschancen, werten die vorhandene Breite aber gleichzeitig als Risiko. Auflösung: Die Surfaces sind Optionswert, nicht MVP-Scope. Nur CLI/MCP und ein minimaler lokaler UI-Pfad bleiben vorrangig; alle anderen müssen Adoption oder strategischen Nutzen beweisen.

### Interne Scorecard versus Feldbefunde

Die 100/100-Scorecard widerspricht den dokumentierten realen Ausfällen. Die Feldbefunde gewinnen, weil sie echte Repositories und konkrete Nutzerpfade abbilden (`docs/release-scorecard.md:9-49`, `docs/real-world-report.md:9-40`).

## UNKNOWN

- reale aktive Nutzer, Installationen, Retention, Organisationen, Umsatz und Zahlungsbereitschaft
- aktueller öffentlicher CI-/Branch-Protection-Status und tatsächliche Release-Nutzung
- ob VS-Code-Extension, Homebrew/Scoop oder GitHub Action extern publiziert und genutzt werden
- Post-Fix-Spring-Boot-Wallclock, DB-Größe und Peak-RAM auf vergleichbarer Hardware
- Genauigkeit pro Sprache auf repräsentativen realen Corpora
- externe Vulnerability-/Dependency-Scan-Ergebnisse
- Maintainer-Kapazität, Supportlast, SLOs und Incident-Historie

## Warum diese Entscheidung richtig ist

Ein Full Rewrite wäre Verschwendung: Der schwerste Teil — ein deterministischer, provenance-tragender Core mit Parser-, Store- und Testinfrastruktur — existiert. Reines Weiterbauen wäre verantwortungslos: Mehr Features würden kaputte Verträge, schlechte Skalierung und unbewiesenen Produktwert multiplizieren. Der Teil-Rewrite konzentriert Aufwand genau dort, wo Belege systemische Fehler zeigen, und schützt zugleich die brauchbare technische Substanz.
