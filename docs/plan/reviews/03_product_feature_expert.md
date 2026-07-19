# Product-&-Feature-Review

## Kurzurteil

graphi hat eine echte, differenzierte Produktidee: ein lokaler, zitierbarer Codegraph als gemeinsames Gedächtnis für Coding-Agenten und Entwickler. Der stärkste Keil ist nicht die Graphvisualisierung und auch nicht die lange Analyzer-Liste, sondern der enge Agenten-Workflow `agent_brief → related_files → explain_symbol → change_risk`. Dieser adressiert ein konkretes, häufiges Problem: Agenten lesen zu viel Code, starten ohne belastbaren Kontext und schätzen Änderungsrisiken schlecht ein.

Als Produkt ist graphi trotzdem noch nicht belastbar „real-world-ready“. Das Repository belegt vor allem außerordentlich viel Implementierung, interne Konformität und synthetische Tests. Es belegt nicht ausreichend, dass reale Nutzer die 142 katalogisierten Capabilities zuverlässig verstehen, brauchen und im Alltag einsetzen. Zwei externe Tests fanden unmittelbar existenzielle Fehler bei Security-Signalen und Time-to-Value; die Remediation ist technisch gut dokumentiert, aber der wichtigste Monorepo-Erfolg wurde nicht erneut als reale End-to-End-Messung belegt. Parallel wächst der Scope weiter auf 42 MCP-Tools, 42 CLI-Subcommands und acht Surfaces. Das ist Feature- und Surface-Sprawl vor nachgewiesenem Product-Market-Fit.

## Score 0–10

**5,5 / 10**

- **Produktidee/Nutzerwert: 8/10.** Lokal, provenance-backed und agent-first ist klar differenziert; die vier neuen Agenten-Tools bilden einen verständlichen Workflow.
- **Feature-Fokus: 4/10.** 142 Capabilities, 42 MCP-Tools, 42 CLI-Kommandos und acht Surfaces sind für v0.4.0 nicht fokussiert, sondern ein Entdeckbarkeits- und Qualitätsrisiko (`docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-130`, `docs/coverage-matrix.md:132-177`).
- **Reale Nutzbarkeit: 5/10.** Zero-config, Warm-Start, Progress und UI sind gute Grundlagen; der dokumentierte Erstlauf auf Spring Boot lag aber bei 4m48s und 2,3 GB (`docs/real-world-report.md:18-20`). Der reale Full-Index-Nachtest fehlt (`docs/real-world-report.md:30-40`).
- **Validierung: 4/10.** Es gibt zwei wertvolle externe Tests und fünf Real-Repo-Smokes. Die Release-Scorecard und viele Claims messen jedoch überwiegend Fixtures, technische Gates oder Proxies statt Nutzererfolg.
- **Vertrauenswürdigkeit des Produktversprechens: 5/10.** Die Provenance- und Fail-closed-Idee ist stark. Gleichzeitig erzeugen 100/100-Selbstevaluation, unklare Artefakt-/Surface-Aussagen und nicht repo-reproduzierbare Marketingzahlen unnötiges Misstrauen.

## Größte Stärken

1. **Ein echtes, scharfes Problem mit einer plausiblen Lösung.** Das README benennt die Kernfragen verständlich: callers, impact und Verbindungen zwischen Funktionen (`readme.md:13-17`, `readme.md:96-115`). Das ist wesentlich stärker als ein generisches „AI developer tool“.

2. **Local-first ist ein relevanter Produktvorteil, nicht nur Architektur.** Kein Account, lokale Verarbeitung und Provenance reduzieren Einführungs- und Vertrauenshürden für private Codebasen (`readme.md:455-465`). Die Sicherheits- und Egress-Gates machen dieses Versprechen technisch glaubwürdiger als bloßes Marketing.

3. **Der Agenten-Workflow ist das beste Feature-Bündel.** `agent_brief`, `related_files`, `explain_symbol` und `change_risk` ergeben eine klare Reihenfolge vor dem Editieren (`docs/agent-workflows.md:7-18`). Der gemeinsame Outcome-/Evidence-/Confidence-Vertrag macht Ergebnisse maschinenlesbar und erlaubt ehrliche `ambiguous`, `partial`, `empty` und `unavailable`-Zustände (`docs/agent-workflows.md:23-49`). Das ist ein produktreiferes Konzept als 38/42 isolierte Tools.

4. **Gute Progressive Disclosure im primären Einstieg.** `cd your-repo && graphi`, kurze Verben und erst danach Power-Flags reduzieren die anfängliche Bedienlast (`docs/HOWTO.md:76-128`). Warm-Start und sichtbarer Indexfortschritt adressieren konkret die Time-to-Value (`CHANGELOG.md:107-116`, `CHANGELOG.md:120-150`).

5. **Ungewöhnlich ehrliche Fehleraufarbeitung.** Das Repo dokumentiert offen, dass Taint 0/4 echte Injections übersah und dabei fälschlich „all clear“ suggerierte (`docs/real-world-report.md:9-17`). Diese Offenheit und die reproduzierbaren Regressionstests sind ein starker Trust-Baustein.

6. **Breite technische Grundlage für spätere Expansion.** 22 ausgelieferte Sprachen, mehrere Oberflächen, deterministische Antworten und ein gemeinsamer Client ermöglichen grundsätzlich eine Plattformstrategie. Die Coverage-Matrix erzwingt wenigstens, dass implementierte Registries und Dokumentation nicht völlig auseinanderlaufen (`docs/coverage-matrix.md:1-15`).

## Größte Schwächen

1. **Feature-Sprawl ersetzt Priorisierung.** Das Produkt katalogisiert 142 Capabilities, darunter PR-Triage, Konflikterkennung, Reviewer-Empfehlung, Review-Kritik, Memory, Skill-Generation, Refactoring, Taint, Wiki, TUI, Web und VS Code. Der eigene Produktbrief sagt korrekt, dass nicht Feature-Zahl, sondern Vertrauen und Time-to-Value über Adoption entscheiden (`docs/plan/superseded/2026-07-produkt-brief-agent-pipeline.md:37-61`). Dennoch ist der Scope bereits wieder auf EP-024 und 42 MCP-Tools gewachsen (`docs/coverage-matrix.md:72-117`). Das erschwert Positionierung, Onboarding, Qualität und Support.

2. **„Shipped“ bedeutet zu oft nur „im Repository vorhanden“.** Die Coverage-Matrix markiert TUI, VS Code, GitHub Action und Web als shipped (`docs/coverage-matrix.md:119-130`). Der normale Go-Build schließt die TUI aber explizit aus (`docs/HOWTO.md:49-51`), während die Website sagt, `graphi tui` „ships in the same binary“ (`site/index.html:179-195`). Die Release-Workflow-Tags betten nur `webui_embed`, nicht `tui`, ein (`.github/workflows/release.yml:160-175`). Für Nutzer ist diese Taxonomie irreführend.

3. **Die Produktvalidierung ist schwächer als die Scorecard suggeriert.** `docs/release-scorecard.md` meldet 100/100 in allen Bereichen, enthält aber keine Per-Repo-Metriken und fast ausschließlich 0-ms-Szenarien (`docs/release-scorecard.md:9-49`). Mehrere Signaltests sind ausdrücklich synthetisch (`docs/release-scorecard.md:44-49`). Eine interne, kriterienspezifische Gate-Scorecard darf nicht wie eine umfassende Produktbewertung auftreten.

4. **Der wichtigste Real-World-Fix ist nur teilweise real nachgewiesen.** Für Spring Boot werden nach der Remediation Fan-out und Bytes/Edge gemessen, der Full-Index jedoch nur „proxied“ (`docs/real-world-report.md:23-40`). Der eigene Erfolgsvertrag verlangte <90 s und <300 MB auf dem Monorepo (`docs/plan/superseded/2026-07-produkt-brief-agent-pipeline.md:81-91`). Das Repo belegt weder die neue reale Gesamtzeit noch die neue reale DB-Größe. Genau der First-run-Moment, den das Produkt gewinnen muss, bleibt damit unbewiesen.

5. **Marketing-Evidenz ist im Repo nicht reproduzierbar.** Die Website behauptet 99,4 % weniger Kontexttokens über 37 Query-Paare auf vier realen Repositories sowie statistische Signifikanz (`site/index.html:198-219`, `site/index.html:260-316`). Im Repository wurden dafür keine zugehörigen Datensätze, Rohresultate oder Reproduktionsskripte gefunden. Das vorhandene Eval-Dokument warnt sogar ausdrücklich, dass seine handgeschriebenen String-Fixtures keine empirische Produktmessung sind und nicht als solche zitiert werden dürfen (`docs/ci/eval.md:27-49`). Der Website-Claim kann ein anderer Benchmark sein, ist repo-intern aber nicht auditierbar.

6. **Dokumentation und Positionierung driften.** Website und `docs/FEATURES.md` sprechen von 38 MCP-Tools (`site/index.html:98`, `docs/FEATURES.md:56-87`), die maschinengeprüfte Matrix von 42 (`docs/coverage-matrix.md:72-117`). `docs/FEATURES.md` bezeichnet sich als vollständiges Inventar, räumt aber selbst ein, nur ein historischer Snapshot zu sein (`docs/FEATURES.md:1-11`). Ein potenzieller Nutzer kann den tatsächlichen Produktumfang nicht schnell verlässlich erfassen.

7. **Die Sprache der Analysefähigkeiten ist stellenweise stärker als ihre Semantik.** Das README nennt Taint gleichzeitig „symbol-graph“, „reachability-based“ und ohne statement-level dataflow (`readme.md:326-334`), während die Coverage-Matrix pauschal „flow-sensitive source→sink taint analysis“ sagt (`docs/coverage-matrix.md:63-69`). Nach einem realen 0/4-Sicherheitsbefund ist präzise, einheitliche Capability-Sprache zwingend.

8. **Entwickler-UX konkurriert mit Agenten-UX statt ihr klar untergeordnet zu sein.** Die Web-UI verlangt initial einen Seed-Symbol-String und zeigt sonst nur „Enter a seed symbol“ (`web/src/GraphPage.tsx:144-216`). Search ist ein separater Button auf demselben Input; Wiki, Compare, Agent Tools, Export und Graph-Selektion liegen danach nebeneinander (`web/src/GraphPage.tsx:212-255`). Das ist funktional, aber kein klarer, auf eine primäre Aufgabe optimierter Nutzerpfad.

9. **Einige „safe“ Edit-Funktionen sind produktseitig noch zu schmal.** `safe-delete` entfernt laut README nur die Deklarationszeile und verlangt manuelle Diff-Prüfung für mehrzeilige Bodies (`readme.md:551-553`). Unter dem Namen „safe-delete“ ist das eine gefährliche Erwartungslücke. `inline` ist ebenfalls auf Single-Line-Initializer begrenzt (`cmd/graphi/help.go:145-158`).

## Kritische Blocker

1. **Kein belastbarer End-to-End-Beweis für den primären Nutzen.** Es fehlt ein reproduzierbarer Vergleich auf realen Repositories: gleiche Agentenaufgaben mit/ohne graphi, gemessene Erfolgsquote/Qualität, Latenz, tatsächlich gelesene Tokens und Fehlentscheidungen. Ein Tokenzähler allein validiert keinen besseren Agenten-Outcome.

2. **Monorepo-First-run bleibt ungeklärt.** Solange der reparierte Spring-Boot-Lauf nicht real mit Zeit, finaler DB-Größe, Signalqualität und interaktivem Time-to-first-use wiederholt wurde, ist „real-world-ready“ nicht belegt. Proxies sind für Regressionen sinnvoll, nicht für das Produktversprechen.

3. **Trust-Kommunikation ist inkonsistent.** 100/100, „measured on real code“, „ships in the same binary“ und pauschales „not a single byte leaves“ müssen exakt den jeweiligen Build-, Surface- und Test-Scope nennen. Gerade ein Trust-Produkt darf technische Teilwahrheiten nicht als Gesamtprodukt-Evidenz präsentieren.

4. **Kein klarer GA-Kern.** Experimentelle PR- und Memory/Skill-Vertikalen sind zwar markiert (`readme.md:154-163`, `readme.md:336-366`), konkurrieren aber weiter um Dokumentations- und Qualitätsbudget. Es fehlt eine harte Definition: Welche 5–8 Jobs-to-be-done müssen für 1.0 exzellent sein, und welche Features werden bis dahin eingefroren oder entfernt?

5. **Distribution mehrerer Surfaces ist nicht produktklar.** Code-Präsenz, Build-Tag-Verfügbarkeit, Release-Binary-Inhalt, Marketplace-Veröffentlichung und Endnutzer-Support werden unter „shipped“ vermischt. Ohne klare Distributionsmatrix kann die versprochene Oberfläche nach Installation fehlen.

## Technische Schulden/Produktschulden

### Produktschulden

- **Scope-Schuld:** 42 Tools und acht Oberflächen erzeugen Entdeckbarkeits-, Dokumentations- und Supportkosten, bevor Kernadoption belegt ist.
- **Evidence-Schuld:** Reale Nutzer-Outcomes, Retention, wiederholte Nutzung und Fehlerraten sind nicht belegt. Zwei externe Tests sind wertvoll, aber keine belastbare Marktvalidierung.
- **Positionierungs-Schuld:** Das Produkt ist gleichzeitig Codegraph, Security Analyzer, Refactoring Engine, PR-Bot, Wiki, Memory Store, Skill Generator, IDE Extension und Savings Calculator. Die klare „local code memory for agents“-Story wird dadurch verwässert.
- **Onboarding-Schuld:** Der Zero-config-Start ist gut, aber der nächste Erfolgsmoment ist nicht geführt. In der UI muss der Nutzer bereits einen Seed-Symbolnamen kennen (`web/src/GraphPage.tsx:153-165`).
- **Claim-Schuld:** Website-Benchmarks und interne 100/100-Scores sind stärker formuliert, als die repo-belegte Validierung trägt.
- **Nomenklatur-Schuld:** „shipped“, „partial“, „experimental“, Build-Tag-verfügbar und tatsächlich installierbar sind nicht sauber getrennt.
- **Lifecycle-Schuld:** Die GitHub-Action dokumentiert als Default und Beispiel `v0.43.0`, während der Produktbrief den Stand v0.4.0 nennt (`extensions/github-action/README.md:45-55`, `extensions/github-action/README.md:71-99`, `docs/plan/superseded/2026-07-produkt-brief-agent-pipeline.md:25-31`). Ob `v0.43.0` existiert, ist repo-intern nicht belegbar; als Copy-paste-Pfad ist das mindestens verwirrend.

### Technische Schulden mit direkter Produktwirkung

- **Nicht-Go-Auflösung bleibt heuristisch.** Das README dokumentiert fehlende `tsconfig`-Path-Mappings, fehlende C++-Overload-Auflösung und weitere Resolver-Grenzen (`readme.md:215-229`). Für die beworbenen 22 Sprachen bedeutet „Support“ sehr unterschiedliche Tiefe.
- **Semantische Suche ist standardmäßig nicht vorhanden.** Das ist ehrlich dokumentiert (`readme.md:387-453`), reduziert aber den Nutzen natürlicher Task-Beschreibungen im Agenten-Workflow, solange kein Embedder eingerichtet ist.
- **Brute-force Vector Search und optionale Build-Varianten erhöhen Konfigurationskomplexität.** Das Produkt hat Default-, WebUI-, TUI-, ONNX- und Broad-CGO-Varianten; Nutzer müssen verstehen, welche Capability in welchem Artefakt lebt.
- **Web-UI ist eine graphzentrierte Expertenoberfläche.** Sie bietet viele Panels, aber keinen taskzentrierten Einstieg, keine Repo-Übersicht als Startzustand und keinen geführten „Was soll ich als Nächstes tun?“-Pfad.
- **Real-Repo-Smoke ist zu flach.** Von fünf externen Corpus-Repositories hat nur Cobra eine konkrete bestätigte Edge-Anforderung; die übrigen verlangen überwiegend nur nichtleere Symbolsuche (`corpus/manifest.json:4-80`). Das beweist Crash-Freiheit und minimale Extraktion, nicht korrekte Nutzerantworten.

## Konkrete Beispiele mit Pfad:Zeile

| Befund | Repo-Beleg | Produktwirkung |
|---|---|---|
| Klarer Kernnutzen | `readme.md:13-17`, `readme.md:96-115` | Verständliche Jobs-to-be-done: callers, impact, connection. |
| Bester Agenten-Workflow | `docs/agent-workflows.md:7-18` | Vier zusammenhängende Calls statt Tool-Sammlung. |
| Sehr großer Scope | `docs/coverage-matrix.md:15`, `docs/coverage-matrix.md:72-177` | 142 Capabilities, 42 MCP-Tools, 42 CLI-Kommandos, acht Surfaces. |
| Existentieller Feldfehler | `docs/real-world-report.md:9-20` | Falsches Security-All-clear und unbrauchbarer Monorepo-First-run. |
| Unvollständiger Real-World-Nachweis | `docs/real-world-report.md:23-40` | Full-index-Zeit bleibt Proxy; reale Zielerreichung offen. |
| Synthetische 100/100 | `docs/release-scorecard.md:9-49` | Überhöhtes Qualitätssignal ohne reale Per-Repo-Ergebnisse. |
| Marketing-Benchmark ohne Repo-Artefakt | `site/index.html:198-219`, `site/index.html:260-316` | 99,4-%-Claim ist aus diesem Repo nicht reproduzierbar. |
| Vorhandenes Eval ist ausdrücklich nicht empirisch | `docs/ci/eval.md:27-49` | Darf nicht als Produktwirkung verkauft werden. |
| Surface-Distribution widersprüchlich | `docs/HOWTO.md:49-51`, `site/index.html:179-195`, `.github/workflows/release.yml:160-175` | TUI wird als shipped beworben, ist aber nicht im normalen/release Build belegt. |
| Inventar driftet | `docs/FEATURES.md:1-11`, `docs/FEATURES.md:56-87`, `docs/coverage-matrix.md:72-117` | 38 versus 42 MCP-Tools; Nutzer können Scope schwer beurteilen. |
| Flache Real-Repo-Akzeptanz | `corpus/manifest.json:4-80` | Vier von fünf externen Repos prüfen primär nur nichtleere Suche. |
| Safe-delete-Erwartungslücke | `readme.md:551-553` | Ein „safe“ benanntes Feature entfernt nur eine Deklarationszeile. |
| UI startet ohne Orientierung | `web/src/GraphPage.tsx:144-216` | Nutzer muss bereits einen Seed kennen; keine startbereite Repo-Übersicht. |

## UNKNOWN

- **UNKNOWN: reale Adoption.** Stars, Downloads, aktive Installationen, wiederkehrende Nutzer, Teams im Produktiveinsatz und Churn sind im Repo nicht dokumentiert. Wegen „no telemetry“ ist fehlende interne Usage-Telemetrie erwartbar; externe, datenschutzfreundliche Validierung wäre dennoch möglich.
- **UNKNOWN: tatsächliche Agenten-Outcome-Verbesserung.** Es ist nicht belegt, ob Agenten mit graphi häufiger korrekte Änderungen liefern, weniger Regressionen erzeugen oder Aufgaben schneller abschließen.
- **UNKNOWN: Reproduzierbarkeit des Website-Benchmarks.** Die Zahlen können aus einem externen Lauf stammen; Rohdaten und Runner sind im geprüften Repo nicht auffindbar.
- **UNKNOWN: reparierte Spring-Boot-Endwerte.** Reale Vollindexzeit, finale DB-Größe, Peak-RAM und Time-to-first-query nach den Fixes sind nicht dokumentiert.
- **UNKNOWN: Release-Distribution der VS-Code-Extension und GitHub Action.** Repository-Code und Packaging existieren; eine tatsächlich veröffentlichte Marketplace-/Action-Version ist repo-intern nicht belegt.
- **UNKNOWN: Nutzungsrelevanz der experimentellen Features.** Es gibt keine repo-belegte Nachfrage für PR-Konfliktanalyse, Review-Kritik, Skillgen, Distill oder lokale Memory-Workflows.
- **UNKNOWN: Qualität über alle beworbenen Sprachen.** Parserregistrierung und Resolvertests belegen Implementierung, aber keine einheitliche, taskbasierte Accuracy/Recall-Messung auf 22 realen Sprachcorpora.
- **UNKNOWN: Existenz des in der Action-Dokumentation verwendeten Tags `v0.43.0`.** Das lokale Repo belegt nur die Referenz, nicht eine veröffentlichte Release-Asset-Kette.

## Harte Empfehlung (Weiterbauen/Refactor/Teil-Rewrite/Full Rewrite)

**Empfehlung: Refactor — primär Produkt- und Scope-Refactor, kein technischer Rewrite.**

### Direkte Begründung

Der technische Kern ist zu wertvoll und zu weit entwickelt für einen Teil- oder Full-Rewrite. Parser, Graphmodell, Provenance, lokale Surfaces, Determinismus, Warm-Start und Agenten-Response-Contract sind ein belastbares Fundament. Das Problem ist nicht fehlender Code, sondern fehlender Fokus und überzogene Gleichsetzung von Implementierung mit Produktreife.

Konkret:

1. **Bis zur belastbaren Validierung Feature-Freeze außerhalb des Agenten-Kerns.** GA-Scope auf `index`, `search`, `agent_brief`, `related_files`, `explain_symbol`, `change_risk`, callers/callees/references und einen klaren UI-/CLI-Einstieg begrenzen. PR-Suite, Skillgen, Distill, graph-aware edits und tiefe Analyzer als Labs/experimental separieren, nicht prominent mitzählen.

2. **Ein einziges reales Erfolgs-Gate etablieren.** Mindestens 20 repräsentative Coding-Aufgaben über mehrere echte Repos: Agent mit/ohne graphi; messen Task-Erfolg, Regressionen, Zeit, Kontexttokens und Zahl unnötig gelesener Dateien. Rohdaten und Runner einchecken. Erst dieses Gate darf die Website-Headline tragen.

3. **Spring-Boot erneut vollständig laufen lassen.** Reale Wall-clock-Zeit, Peak-RAM, DB-Größe, Time-to-first-query und Diagnose-Signalqualität veröffentlichen. Bis dahin „real-world-ready“ und Monorepo-Performance nicht behaupten.

4. **„Shipped“ in fünf klare Zustände aufteilen:** im Code vorhanden, im Default-Build, im Release-Binary, separat installierbar/veröffentlicht, experimental. Website und README müssen diese Matrix direkt verwenden.

5. **Claims radikal härten.** Interne Gate-Scorecard als „engineering regression score“, nicht „product quality 100/100“ benennen. Website-Benchmark entweder vollständig reproduzierbar machen oder entfernen. Jede Analyzer-Beschreibung muss Granularität, Sprachscope und Failure/Unknown-Zustände nennen.

6. **Onboarding am Job statt am Graph ausrichten.** Startseite/UI zuerst: „Was möchtest du ändern/verstehen?“ → Task oder Symbol eingeben → Agent Brief + Related Files → Graph optional als Beleg. Der Graph ist Evidenzvisualisierung, nicht das primäre Produktziel.

Wenn dieser Refactor konsequent erfolgt, ist Weiterbauen sinnvoll. Ohne ihn wird zusätzliche Feature-Arbeit die zentrale Schwäche verschärfen: ein beeindruckendes technisches Inventar, dessen reale Nutzerwirkung und Vertrauenswürdigkeit hinter seiner eigenen Präsentation zurückbleiben.
