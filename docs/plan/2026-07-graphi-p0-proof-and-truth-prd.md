# PRD — Graphi P0: Proof and Truth

**Dokumentstatus:** Angenommen — in Kraft für den P0-Scope (SW-120, 2026-07-27)  
**Produkt:** Graphi  
**Programmphase:** P0 — Proof and Truth  
**Primärer Scope:** Go, CGo-freies Default-Binary, CLI, MCP stdio, zwölf Stable Operations  
**Pfad im Repository:** `docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md`  
**Autorität:** Präzisierung der Plan-Meilensteine M0–M2 — siehe den Autoritätsvermerk unmittelbar unter diesem Kopf.  
**Delta-PRD (nur die verbleibende P0-Arbeit):** [`docs/plan/2026-07-graphi-p0-completion-delta-prd.md`](2026-07-graphi-p0-completion-delta-prd.md) — registriert durch SW-132 (2026-07-29), wörtlich vom Product Owner geliefert. Zugehörige Checkliste SW-132…SW-149: [`docs/plan/2026-07-graphi-p0-completion-checklist.md`](2026-07-graphi-p0-completion-checklist.md).  
**Owner:** Graphi Maintainer (Solo-Projekt — eine Person trägt alle Rollen; siehe §8.8 Solo-Substitute). Beantwortet Open Question 4.  
**Technische Freigabe:** Graphi Maintainer  
**Review erforderlich durch:** Engineering, Evaluation, Security/DevOps, Product — im Solo-Betrieb dieselbe Person in vier Rollen; wo das Gate eine *unabhängige* Instanz verlangt, greift die Substitutionsregel in §8.8.  
**Letzte Aktualisierung:** 2026-07-27

---

> ## Autoritätsvermerk (SW-120, 2026-07-27)
>
> **Diese PRD ist eine Präzisierung der Meilensteine M0–M2 des Ausführungsplans**
> ([`docs/plan/2026-07-graphi-9of10-execution-plan.md`](2026-07-graphi-9of10-execution-plan.md)),
> nicht dessen Ablösung. Die Autoritätsregel lautet genau so:
>
> - **Innerhalb des P0-Scope** (Plan-M0 „Wahrheit und Scope“, M1 „Baseline“, M2
>   „Accuracy/Performance“, und die daran hängenden Arbeitspakete WP0–WP4) **gewinnt diese
>   PRD.** Sie ist dort feiner: sie benennt Messgrößen, Mindestlaufzahlen, Scorer und
>   Abnahmekriterien, die der Plan nur als Gate-Satz führt.
> - **Außerhalb des P0-Scope** (M3 Security/RC, M4 Product/Business, M5 Audit, WP5–WP10,
>   Zeitplan, Rollenmodell, Programmstruktur) **gewinnt der Ausführungsplan.** Diese PRD
>   trifft dort keine Aussagen und darf nicht so gelesen werden.
> - **Eine Präzisierung darf ein Plan-Gate nicht aufweichen.** Wo beide eine Zahl nennen,
>   muss die PRD-Zahl gleich oder strenger sein. Ein Widerspruch in der Zahl ist ein
>   Fehler dieser PRD, kein stillschweigender Freibrief.
>
> **Verhältnis zur Delta-PRD (SW-132, 2026-07-29).** Die
> [Delta-PRD](2026-07-graphi-p0-completion-delta-prd.md) beschreibt ausschließlich die
> *verbleibende* Arbeit bis zum Abschluss von P0. Sie **ersetzt diese PRD nicht** und
> eröffnet keinen bereits geschlossenen Punkt neu (ihr §2.1 führt diese Arbeit als
> ererbten Input). Sie führt **kein neues Gate-Vokabular** ein und verschiebt keine
> Schwelle: wo sie ein Gate nennt, ist es das Gate dieser PRD. Widerspricht eine Zahl der
> Delta-PRD einer Zahl dieser PRD, gilt diese PRD und der Widerspruch ist ein Fehler der
> Delta-PRD. Der Status eines Gates wird weiterhin ausschließlich in
> [`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml) gelesen.
>
> **Keine eigenen Gate-IDs.** Diese PRD führt kein drittes Gate-Vokabular ein. Wo sie ein
> Gate benennt, referenziert sie die Zeilen-IDs, die
> [`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml) bereits führt (WP0–WP10 aus
> Plan §6, M0–M5 aus Plan §5) und die `go run ./cmd/evidence -check` in CI prüft. Die
> Bandbezeichnungen P0-A…P0-E in §14 sind **Backlog-Bänder, keine Gates**: keine
> Evidence-Zeile wird jemals über sie geschlüsselt. Die Zuordnung PRD-Arbeitsblock →
> Plan-Gate-ID steht in §13.
>
> **Angewandte Review-Korrekturen.** Diese Fassung ist keine Kopie des Entwurfs. Sie wurde
> gegen das Repository bei `fb3bf03` geprüft; die dabei gefundenen Widersprüche sind in
> derselben Änderung behoben und in §0 einzeln aufgeführt. Ein Dokument mit bekannten
> Widersprüchen zur Autorität zu erheben wäre genau der Fehlermodus, den P0 beseitigen
> soll.

---

## 0. Was der Review gegenüber dem Entwurf geändert hat

Der Entwurf (Portfolio-Fassung, 2026-07-27) wurde vor der Übernahme ins Repository gegen
den Code bei `fb3bf03` gelesen. Fünf Punkte sind korrigiert; keine Gate-Zahl wurde dabei
verändert.

| # | Befund im Entwurf | Korrektur in dieser Fassung |
|---|---|---|
| 1 | §7.1 und FR-4 zählen `index` zu den bewerteten Operationen — `index` ist aber Lifecycle-only und keine abfragbare Operation (`surfaces/mcp/tools.go`, [`docs/stability-tiers.md`](../stability-tiers.md)). | `index` ist in §7.1 als Lifecycle-only markiert; **FR-4 bewertet elf Operationen, nicht zwölf**. Die Zwölf bleiben als Stability-Taxonomie unverändert eingefroren. |
| 2 | §12.2 und FR-8 führen die Full-/Incremental-Parität als *Performance*-Metrik. | Parität ist eine Zuverlässigkeitseigenschaft und steht in FR-7; sie ist aus der Performance-Tabelle entfernt und in die neue Tabelle §12.3 (Zuverlässigkeit) verschoben. |
| 3 | „korrekte Abstention ≥ 95 %“ nennt keinen Nenner — ohne Nenner ist die Zahl nicht messbar. | FR-6 und §12.1 definieren den Nenner explizit. |
| 4 | §12.2 nennt 2 GB Peak RSS, §17 nennt 4 GB — ohne erkennbare Beziehung. | §12.2 und §17 stellen die Beziehung ausdrücklich her: 2 GB ist das **Gate** im Referenzszenario, 4 GB die **programmweite Stop-Regel** über jedes gemessene Szenario. |
| 5 | §13 und §23 führen ein eigenes Gate-Vokabular (`WP0–WP8` in PRD-eigener Nummerierung, `P0-ACC-EDGE-PRECISION`) — die PRD-WP-Nummern kollidieren dabei mit den gleichnamigen, anders belegten Plan-WPs. | §13 ist auf die Plan-Gate-IDs abgebildet, §23 zeigt das reale Zeilenschema aus `docs/rc/evidence-index.yaml`. |

Zusätzlich beantwortet §21 die Open Questions 1, 2, 3, 4, 10 und 12, statt sie offen zu
lassen, und §8.8 hält die Solo-Substitutionsregel fest.

---

## 1. Executive Summary

P0 macht aus Graphi keinen breiteren Funktionskatalog. P0 beweist, dass der bereits definierte Go-Kern zuverlässig, reproduzierbar, performant und ehrlich beschrieben ist.

Graphi besitzt bereits eine technisch umfangreiche Plattform mit Stable-, Preview-, Labs- und Source-only-Funktionen. Die größte aktuelle Produktgefahr ist deshalb nicht ein Mangel an Features, sondern ein Mangel an unabhängig nachvollziehbarer Evidenz für die zentralen Produktbehauptungen:

- Wie vollständig und präzise ist der Go-Code-Graph?
- Wie zuverlässig sind die zwölf Stable Operations auf realen Repositories?
- Welche Performance ist auf fest definierten Systemen reproduzierbar?
- Welche Ergebnisse sind bestätigt, abgeleitet, heuristisch oder unbekannt?
- Stimmen öffentliche Claims mit dem tatsächlich ausgelieferten Artefakt überein?
- Produzieren Full- und Incremental-Index denselben Graphen?
- Bleibt der lokale und CGo-freie Produktvertrag unter realen Release-Bedingungen erhalten?

P0 liefert deshalb vier zusammenhängende Ergebnisse:

1. **Einen eingefrorenen, reproduzierbaren Candidate** für den fokussierten Go-Kern.
2. **Eine belastbare technische Baseline** auf realen, gepinnten Go-Repositories.
3. **Ein versiegeltes Gold-Corpus** für Symbol-, Edge-, Anchor- und Task-Qualität.
4. **Eine bereinigte Claim- und Dokumentationsoberfläche**, die nicht mehr verspricht als das Artefakt belegt.

P0 ist erfolgreich, wenn ein unabhängiger Engineer mit demselben Candidate, denselben Repositories, derselben Messmethode und denselben Scorern die Ergebnisse reproduzieren kann und alle definierten Gates erfüllt sind.

P0 ist nicht erfolgreich, wenn nur `go test ./...` grün ist, ein interner Score hoch ausfällt oder einzelne Demo-Repositories gute Ergebnisse zeigen.

---

## 2. Problem Statement

### 2.1 Aktuelle Situation

Graphi verfolgt einen bewusst engen GA-Vertrag:

- Go ist die einzige GA-Sprache.
- Die zwölf Stable Operations sind eingefroren.
- CLI und MCP stdio bilden die GA-Surfaces.
- Das Default-Binary bleibt CGo-frei.
- Externe Dependencies werden als Grenzknoten modelliert, aber nicht strukturell navigiert.
- Nicht-Go-Sprachen bleiben Preview.
- Taint, Refactoring, Semantic Search, Agent Memory, UI, HTTP, Daemon und weitere Funktionen bleiben Labs oder Source-only.

Diese Scope-Disziplin ist sinnvoll. Sie löst aber nicht automatisch die Frage, wie gut der GA-Kern auf realen Repositories funktioniert.

Aktuell bestehen insbesondere folgende Risiken:

1. Interne Tests können reale Codebasen unzureichend repräsentieren.
2. Benchmarkwerte können an alte Harnesses, andere SHAs oder nicht vergleichbare Runner gebunden sein.
3. Hero-Tasks sind wertvoll, aber noch kein ausreichend großes Gold-Corpus.
4. Ein reproduzierbarer Graph ist nicht automatisch ein vollständiger oder fachlich korrekter Graph.
5. Dokumentation, Code-Kommentare und öffentliche Claims können gegenüber dem aktuellen Verhalten driften.
6. Unbekannte oder degradierte Bereiche können durch aggregierte Erfolgswerte verdeckt werden.
7. Neue Features können die Glaubwürdigkeit weiter verwässern, bevor der Kern bewiesen ist.

### 2.2 Nutzerproblem

Ein Entwickler oder AI-Agent, der Graphi nutzt, muss einschätzen können, ob die Antwort belastbar ist.

Ohne P0 bleibt für Nutzer unklar:

- Ob ein fehlender Caller wirklich nicht existiert oder nur nicht erkannt wurde.
- Ob ein `impact`-Ergebnis vollständig genug für eine riskante Änderung ist.
- Ob eine Performancezahl für das eigene Repository relevant ist.
- Ob ein hoher Score aus einer unabhängigen Messung oder einem selbst definierten internen Gate stammt.
- Ob ein Release-Artefakt exakt dem gemessenen Candidate entspricht.
- Ob eine inkrementelle Aktualisierung denselben Graphen wie ein Full Rebuild erzeugt.
- Ob öffentliche Aussagen zum aktuellen Code und Release passen.

### 2.3 Geschäftliches Problem

Ohne belastbare Kern-Evidenz ist jede weitere Expansion riskant:

- neue Sprachen erhöhen die Evaluationsfläche,
- mehr Labs-Tools erhöhen Support- und Trust-Kosten,
- Enterprise-Claims werden schwerer belegbar,
- potenzielle Design-Partner können das Produkt nicht seriös bewerten,
- externe Reviewer können technische Qualität nicht reproduzieren,
- Fehler in Security- oder Refactoring-Funktionen beschädigen das Vertrauen in den gesamten Graphen.

P0 reduziert dieses Risiko, bevor Graphi weiter horizontal wächst.

---

## 3. Produktvision für P0

> Graphi soll für seinen fokussierten Go-Kern nicht nur technisch funktionieren, sondern nachweisbar, reproduzierbar und ehrlich funktionieren.

P0 soll Graphi zu einem Produkt machen, bei dem jede zentrale Aussage einen sichtbaren Evidence Path besitzt:

```text
Public Claim
    ↓
Versioned Requirement
    ↓
Pinned Candidate SHA / Release Digest
    ↓
Pinned Corpus / Repository SHA
    ↓
Reproducible Command
    ↓
Raw Measurements
    ↓
Versioned Scorer
    ↓
PASS / FAIL / UNKNOWN
```

UNKNOWN ist ein gültiger und notwendiger Zustand. UNKNOWN darf nicht automatisch als PASS behandelt werden.

---

## 4. Ziele

### 4.1 Primäre Ziele

P0 muss folgende Ziele erreichen:

1. Einen einzigen Candidate-SHA und den zugehörigen Release-Digest festlegen.
2. Sicherstellen, dass alle Messungen auf genau diesem Candidate beziehungsweise dem daraus erzeugten Release-Binary laufen.
3. Mindestens fünf reale Go-Repositories unveränderlich pinnen.
4. Mindestens ein großes Stressziel mit mindestens 10.000 Quelldateien einbeziehen.
5. Eine reproduzierbare Accuracy- und Performance-Baseline erzeugen.
6. Ein Gold-Corpus mit mindestens 1.000 Symbolen und 2.000 Beziehungen erstellen.
7. 100 reale, versiegelte Coding-Tasks definieren und auswerten.
8. Full-vs-Incremental-Parität für den Candidate beweisen.
9. Claims, Dokumentation und Code-Kommentare gegen das tatsächliche Verhalten bereinigen.
10. Alle Ergebnisse mit Rohdaten, Methode, SHA, Runner und Sign-off versionieren.
11. Verhindern, dass neue Feature-Arbeit P0-Gaps verdeckt oder verzögert.
12. Einen klaren Go/No-Go-Entscheid für P1 ermöglichen.

### 4.2 Sekundäre Ziele

- Einen wiederverwendbaren Evaluationsvertrag für spätere Sprachen schaffen.
- Ein belastbares Regression-Ratchet für zukünftige Releases etablieren.
- Externe Reviews und Design-Partner mit nachvollziehbarer Evidenz versorgen.
- High-Confidence-Falschaussagen früh sichtbar machen.
- Fehlende oder degradierte Coverage ausdrücklich als solche dokumentieren.

---

## 5. Nicht-Ziele

P0 umfasst ausdrücklich nicht:

- neue GA-Sprachen,
- eine TypeScript-, Java-, Kotlin- oder Python-GA-Promotion,
- neue Stable Operations,
- eine neue Stable Surface,
- SaaS, Billing, SSO oder RBAC,
- Cloud Indexing,
- Cross-Repository Search,
- vollständige Navigation durch Third-Party-Dependencies,
- Ausbau von `graphi-broad`,
- neue Labs-Analyzer,
- neue UI-Funktionen,
- horizontale Erweiterung der MCP-Tool-Fläche,
- Taint-GA,
- Refactoring-GA,
- autonome Codeänderungen,
- Marketingclaims ohne reproduzierbaren Beleg,
- Optimierungen ohne Profil oder gemessenen Gate-Verstoß,
- das Nachbauen von Sourcegraph, CodeQL oder Semgrep,
- künstliches Erhöhen interner Scores durch leichtere Fixtures,
- Senken der Gates, um einen Candidate grün zu bekommen.

---

## 6. Zielnutzer und Stakeholder

### 6.1 Primäre Nutzer

#### AI-Coding-Agent

Benötigt:

- stabile, strukturierte Antworten,
- eindeutige Symbolidentitäten,
- belastbare Caller-, Callee-, Reference- und Impact-Beziehungen,
- Source Anchors,
- reproduzierbares Verhalten,
- klare Abstention bei Unsicherheit.

#### Go-Entwickler

Benötigt:

- schnelle lokale Navigation,
- nachvollziehbare Ergebnisse,
- keine versteckten Cloud-Abhängigkeiten,
- verlässliche inkrementelle Aktualisierung,
- klare Grenzen des Tools.

#### Platform- oder Security-Team

Benötigt:

- reproduzierbare Release-Artefakte,
- CGo-freien Default-Build,
- nachvollziehbare Privacy- und Egress-Gates,
- SBOM und Attestation,
- klaren Scope.

### 6.2 Sekundäre Stakeholder

- Graphi Maintainer
- Evaluation Engineer
- Product Owner
- Security/DevOps
- externe Reviewer
- Design-Partner
- Open-Source-Contributors
- potenzielle Käufer oder Sponsoren

---

## 7. Scope

### 7.1 In Scope

#### Sprache

- Go

#### Operations

Die zwölf eingefrorenen Stable Operations. `index` ist davon **Lifecycle-only**: es baut
und aktualisiert den Graphen, beantwortet aber keine Frage über ihn und wird deshalb nicht
als MCP-Tool angeboten (`surfaces/mcp/tools.go`, [`docs/stability-tiers.md`](../stability-tiers.md)
— elf Stable-MCP-Tools bei zwölf Stable Operations).

- `index` — **Lifecycle-only**; bewertet wird es über FR-7 (Parität), FR-8 (Cold Index,
  Freshness, Incremental Update) und NFR-5 (Recovery), nicht über FR-4.
- `search`
- `definition`
- `callers`
- `callees`
- `references`
- `neighborhood`
- `impact`
- `agent_brief`
- `related_files`
- `explain_symbol`
- `change_risk`

Die Zwölf bleiben eingefroren. Diese Unterscheidung ändert die Taxonomie nicht; sie sagt
nur, welches Gate welche Operation misst.

#### Surfaces

- CLI
- MCP stdio

#### Build

- CGo-freies Default-Binary

#### Speicher und Aktualisierung

- lokaler Graph Store
- Full Index
- Incremental Index
- Recovery
- Source Anchors
- lokale Metadaten

### 7.2 Out of Scope, aber beobachtbar

Folgende Komponenten dürfen für Regressionserkennung getestet werden, treiben aber keine P0-Produktanforderungen:

- Preview-Sprachen
- Labs-Analyzer
- HTTP
- Daemon
- Web UI
- TUI
- VS-Code-Extension
- GitHub Action
- Refactorings
- Taint
- Semantic Search
- Agent Memory

Ein Fehler in diesen Bereichen darf den Candidate nur blockieren, wenn er:

- den GA-Build beeinflusst,
- die Security- oder Privacy-Grenze verletzt,
- den Release-Prozess beschädigt,
- Stable Operations regressiert,
- das CGo-freie Default-Binary verändert,
- öffentliche Claims falsch macht.

---

## 8. Leitprinzipien

### 8.1 Measured, not asserted

Keine Aussage ohne:

- Artefakt,
- reproduzierbaren Befehl,
- Rohdaten,
- Scorer,
- Candidate-SHA,
- Repository-Pin,
- Runnerbeschreibung.

### 8.2 UNKNOWN ist kein PASS

Kann ein Wert nicht belastbar gemessen werden, lautet der Status UNKNOWN.

### 8.3 Ein Candidate, eine Wahrheit

Alle P0-Gates beziehen sich auf denselben Candidate. Ein Candidate-Wechsel macht abhängige Evidenz automatisch stale.

### 8.4 Keine Feature-Kompensation

Ein Accuracy-Problem darf nicht durch neue Features überdeckt werden.

### 8.5 Test oder Profil vor Fix

Jeder Produktionsfix startet mit:

- einem Gold-Corpus-Fehler,
- einem Regressionstest,
- einem reproduzierbaren Profil,
- oder einem klaren Security-Finding.

### 8.6 Confidence-Ehrlichkeit

Reproduzierbarkeit, Confidence und Vollständigkeit werden getrennt bewertet.

Ein deterministischer Output darf nicht als vollständig oder mathematisch fehlerfrei bezeichnet werden.

### 8.7 Fail closed

Bei fehlender Evidenz, unvollständiger Baseline oder inkonsistentem Candidate darf kein Release-Gate fälschlich grün werden.

### 8.8 Solo-Substitute

Graphi wird von einer Person ohne Budget entwickelt. Mehrere Gates dieser PRD setzen ein
Team voraus: eine *unabhängige* zweite Person, externe Annotatoren, ein bezahlter Pentest.
Diese Gates werden weder stillschweigend gestrichen noch stillschweigend für erfüllt
erklärt. Stattdessen gilt eine explizite Substitutionsregel.

**Die Regel:**

1. Ein teamabhängiges Gate darf durch ein **benanntes Substitut** erfüllt werden, wenn das
   Substitut dieselbe *Fehlerklasse* aufdeckt wie das Original — nur schwächer.
2. **Ein substituiertes Gate wird niemals berichtet, als wäre es das Original.** Die
   Evidence-Zeile trägt die Substitution im Feld `current` im Klartext („substituiert:
   sauberer CI-Runner statt unabhängiger Person“). Ein Report, der die Substitution
   verschweigt, ist eine Falschaussage im Sinne von §2 und blockiert P0.
3. Substituierte Evidenz ist **schwächere** Evidenz. Sie kann ein Gate auf PASS bringen,
   trägt aber niemals einen öffentlichen Claim, der Unabhängigkeit behauptet.
4. Wo kein tragfähiges Substitut existiert, bleibt das Gate **UNKNOWN** — und UNKNOWN
   zählt nach §8.2 als nicht bestanden. Ein fehlendes Substitut ist kein Grund, ein Gate
   zu senken (§17).
5. Fällt die Teamvoraussetzung später weg (Budget, Mitwirkende), wird das Original
   nachgeholt und die substituierte Evidenz als überholt markiert.

**Die Substitutionstabelle für P0:**

| Gate | Original | Substitut in P0 | Warum schwächer |
|---|---|---|---|
| FR-9 — unabhängige Reproduktion | zweiter vollständiger Lauf durch eine andere Person | **sauberer CI-Runner** (`eval-full.yml`, `ubuntu-latest`), ausgelöst aus einem frischen Checkout ohne lokalen Cache — FR-9 lässt das ausdrücklich zu („eine andere Person **oder** ein sauberer Runner“) | prüft Umgebungsunabhängigkeit, nicht Verständlichkeit: ein Runner kann keine mehrdeutige Anleitung bemängeln und keine unausgesprochene Annahme des Autors bemerken |
| WP0/§13 — Review durch vier Rollen | vier Personen | dieselbe Person in vier getrennten, dokumentierten Durchgängen gegen Checklisten | kein unabhängiger Blick; Bestätigungsfehler bleibt möglich (Risiko 5) |
| FR-3 — 20 % Doppelannotation, Cohen's Kappa ≥ 0,85 | zwei unabhängige Annotatoren | **kein Substitut** — Kappa zwischen einer Person und sich selbst misst nichts | bleibt UNKNOWN; das gesamte Band P0-B ist deshalb zurückgestellt, nicht abgeschwächt |
| Plan-WP5 — externer Pentest | beauftragtes Sicherheitsteam | **kein Substitut** in P0; außerhalb des P0-Scope (M3) | bleibt UNKNOWN |
| Plan-WP7 — Erweiterbarkeitstest mit vier Entwicklern | vier unabhängige Entwickler | **kein Substitut** in P0; außerhalb des P0-Scope | bleibt UNKNOWN |

Wo diese Tabelle „kein Substitut“ sagt, ist die ehrliche Antwort UNKNOWN. Das ist genau
der Zustand, den §8.2 vorsieht, und er ist einem erfundenen PASS vorzuziehen.

---

## 9. User Journeys

### 9.1 Entwickler-Journey

```text
Install
→ Repository öffnen
→ Graphi indexiert den Go-Code
→ Entwickler fragt nach Definition, Caller oder Impact
→ Graphi liefert eine strukturierte Antwort mit Source Anchors
→ dieselbe Anfrage liefert auf demselben Graphen dieselbe Antwort
→ inkrementelle Änderung aktualisiert den Graphen
→ Full Rebuild erzeugt denselben finalen Graphen
```

### 9.2 Agent-Journey

```text
MCP stdio starten
→ initialize
→ tools/list
→ Stable Tool aufrufen
→ kompakte, zitierte Antwort erhalten
→ keine vollständigen irrelevanten Dateien laden
→ fehlendes Symbol führt zu eindeutigem NotFound
→ leerer Graphpfad wird von fehlendem Symbol unterschieden
```

### 9.3 Evaluations-Journey

```text
Candidate auswählen
→ Release-Binary bauen
→ Digest speichern
→ gepinntes Repository auschecken
→ Index ausführen
→ Queries ausführen
→ Rohdaten speichern
→ Gold-Corpus scoren
→ Resultat als PASS / FAIL / UNKNOWN veröffentlichen
→ unabhängiger Engineer reproduziert den Lauf
```

---

## 10. Funktionale Anforderungen

### FR-1 — Candidate Freeze

Der Prozess muss genau einen P0-Candidate definieren.

Der Candidate-Eintrag muss enthalten:

- Git Commit SHA,
- Branch oder Tag,
- Release-Version,
- Binary Digest,
- Build Command,
- Go-Version,
- `CGO_ENABLED`,
- Build Tags,
- Zielplattformen,
- SBOM-Referenz,
- Attestation-Referenz,
- Freigabedatum,
- Owner,
- Sign-off.

#### Acceptance Criteria

- Es existiert genau ein aktiver P0-Candidate.
- Kein Gate referenziert einen anderen SHA.
- Ein Candidate-Wechsel erzeugt einen Decision-Log-Eintrag.
- Alle betroffenen Evidenzen werden als STALE markiert.
- Ein Release-Binary kann aus dem Candidate reproduzierbar neu gebaut werden.

### FR-2 — Repository Corpus

P0 muss mindestens fünf reale Go-Repositories verwenden.

#### Corpus-Anforderungen

- Jedes Repository ist öffentlich oder mit dokumentiertem Zugriff verfügbar.
- Jedes Repository ist auf eine vollständige Commit-SHA gepinnt.
- Mindestens ein Repository besitzt mindestens 10.000 Quelldateien oder es existiert ein separates Stressrepo.
- Unterschiedliche Repository-Formen sind vertreten.
- Keine Messung darf still auf einen neueren Branchstand wechseln.

#### Empfohlene Stratifizierung

- kleine Library,
- mittelgroße CLI-Anwendung,
- Web- oder API-Service,
- Multi-Package-Repository,
- großes Repository oder Monorepo,
- Repository mit Generics,
- Repository mit mehreren `go.mod`,
- Repository mit Build Tags,
- Repository mit Generated Code,
- Repository mit Tests und Benchmarks.

#### Acceptance Criteria

- `corpus/manifest.json` oder ein äquivalentes Manifest enthält alle Pins.
- Jeder Pin wird vor dem Lauf fail-closed geprüft.
- Ein nicht auflösbarer oder abweichender Pin blockiert die Evaluation.
- Lizenz und zulässige Nutzung der Repositories sind dokumentiert.

### FR-3 — Accuracy Gold-Corpus

P0 muss ein versiegeltes Gold-Corpus erzeugen.

#### Mindestumfang

- 1.000 Symbole
- 2.000 Beziehungen
- 100 reale Coding-Tasks
- mindestens 20 % doppelt annotierte Einträge
- unabhängige Adjudikation von Konflikten

#### Symbolklassen

Mindestens:

- package
- file
- function
- method
- type
- interface
- struct
- variable
- constant
- receiver method
- test function
- benchmark function
- entry point

#### Beziehungsklassen

Mindestens:

- defines
- calls
- references
- imports
- implements
- inherits oder embeds, soweit im Go-Modell vorhanden
- overrides, soweit im Go-Modell vorhanden

#### Sonderfälle

- Shadowing
- Pointer Receiver
- Value Receiver
- Embedded Interface
- Embedded Struct
- Interface Satisfaction
- Method Expressions
- Method Values
- Generics
- Type Alias
- Build Tags
- Generated Files
- Multi-Module-Struktur
- Import Cycles
- Parse Errors
- externe Imports
- mehrdeutige Namen
- identische Symbolnamen in mehreren Packages

#### Acceptance Criteria

- Annotation Guide ist vor dem vollständigen Corpus eingefroren.
- Gold-Daten sind von Implementierungsdetails getrennt.
- Änderungen am Corpus besitzen Audit Trail.
- Cohen’s Kappa beträgt mindestens 0,85.
- Bei Kappa unter 0,85 werden Guide und Training korrigiert und betroffene Daten neu annotiert.

### FR-4 — Stable Operation Scoring

**FR-4 bewertet elf Operationen, nicht zwölf.** Von den zwölf eingefrorenen Stable
Operations ist `index` Lifecycle-only (§7.1): es beantwortet keine Frage und hat daher
keinen Gold-Task-Typ. Sein Verhalten wird über FR-7, FR-8 und NFR-5 gemessen. Die elf hier
aufgeführten Operationen — `search`, `definition`, `callers`, `callees`, `references`,
`neighborhood`, `impact`, `agent_brief`, `related_files`, `explain_symbol`, `change_risk` —
werden jeweils separat bewertet.

#### `search`

- relevante Symboltreffer,
- Ranking,
- False Positives,
- Umgang mit identischen Namen,
- Grenzen freier Textsuche.

#### `definition`

- richtige Symbolidentität,
- richtiger Source Anchor,
- korrekte Package-Zuordnung.

#### `callers`

- Edge Precision,
- Edge Recall,
- Receiver Dispatch,
- Package-Grenzen,
- fehlende oder degradierte Beziehungen.

#### `callees`

- direkte lokale Calls,
- Methoden,
- externe Grenzknoten,
- keine erfundenen Ziele.

#### `references`

- Non-Call References,
- Type References,
- Variable References,
- Source Anchors.

#### `neighborhood`

- deterministische Sortierung,
- Depth Clamp,
- keine External-Node-Leaks,
- bounded output.

#### `impact`

- Reverse Reachability,
- Direction Semantics,
- keine Aussage „wird sicher brechen“,
- struktureller Scope.

#### `agent_brief`

- bounded output,
- relevante File-Auswahl,
- Source Anchors,
- keine unnötigen Full-File-Ausgaben.

#### `related_files`

- nachvollziehbares Ranking,
- deterministische Ausgabe,
- relevante Nachbarschaft.

#### `explain_symbol`

- korrekte Identität,
- korrekte unmittelbare Umgebung,
- kompakte Evidence.

#### `change_risk`

- deterministische Regeln,
- Evidence für Risikofaktoren,
- keine unbelegten semantischen Behauptungen.

#### Acceptance Criteria

- Jede der **elf** bewerteten Operationen besitzt mindestens einen eigenen Gold-Task-Typ.
- Kein aggregierter Score darf eine stark schwache Einzeloperation verdecken.
- Operationen mit FAIL blockieren P0.
- UNKNOWN wird sichtbar ausgewiesen.
- `index` erscheint in keiner FR-4-Auswertung — weder als bestandene noch als fehlende
  Operation. Ein FR-4-Bericht, der zwölf Operationen ausweist, ist falsch.

### FR-5 — Source Anchor Accuracy

Graphi muss Source Anchors mit hoher Präzision liefern.

#### Erforderliche Felder

- Repository-relative Datei
- Startzeile
- optional Startspalte
- optional Endzeile
- Symbol-ID
- Symbolname
- Qualified Name

#### Acceptance Criteria

- Source-Anchor Precision mindestens 99 %.
- Ein Anchor darf nicht auf eine andere Symboldefinition zeigen.
- Normalisierte Pfade bleiben repository-relative.
- Pfade außerhalb des Repository-Roots werden abgelehnt.
- Generated oder nicht mehr vorhandene Dateien werden eindeutig markiert.

### FR-6 — Abstention und NotFound

Graphi muss zwischen verschiedenen Negativfällen unterscheiden.

#### Erforderliche Zustände

- `found`
- `empty`
- `not_found`
- `degraded`
- `unsupported`
- `error`

Nicht jeder Zustand muss sofort als neues Stable-Wire-Format eingeführt werden, wenn dies den eingefrorenen Vertrag verändern würde. Die Evaluation muss diese Fälle jedoch getrennt erfassen.

#### Definition der Abstentionsrate

„Korrekte Abstention“ ist eine Rate und braucht einen Nenner. Er lautet:

```text
                    Anzahl abstentionspflichtiger Gold-Tasks,
                    bei denen Graphi den korrekten Negativzustand liefert
korrekte Abstention = ───────────────────────────────────────────────────
                    Anzahl abstentionspflichtiger Gold-Tasks
```

- **Nenner — abstentionspflichtige Gold-Tasks:** genau die Gold-Tasks, deren annotierte
  Sollantwort ein Negativ- oder Enthaltungszustand ist, also `empty`, `not_found`,
  `degraded` oder `unsupported`, sowie mehrdeutige Anfragen, für die keine beweisbare
  Beziehung existiert. Tasks mit positiver Sollantwort zählen **nicht** in den Nenner;
  sie werden über Precision und Recall gemessen.
- **Zähler:** die Teilmenge davon, bei der Graphi genau den annotierten Negativzustand
  liefert. Der falsche Negativzustand (`empty` statt `not_found`) zählt als Fehler, nicht
  als korrekte Abstention. Eine erfundene positive Antwort zählt ebenfalls als Fehler und
  fällt, wenn sie mit `confirmed`-Confidence ausgegeben wird, zusätzlich unter die
  High-Confidence-Falschaussagen.
- Der Nenner wird zusammen mit der Rate veröffentlicht. Eine Abstentionsrate ohne
  ausgewiesene Grundgesamtheit ist kein gültiger Messwert und zählt als UNKNOWN.

Die Zahl selbst bleibt unverändert bei ≥ 95 %; präzisiert ist nur, worüber sie gebildet
wird.

#### Acceptance Criteria

- Ein unbekanntes Symbol wird nicht als leere erfolgreiche Antwort dargestellt.
- Mehrdeutige oder nicht beweisbare Beziehungen werden nicht als bestätigt ausgegeben.
- Korrekte Abstention mindestens 95 % über die oben definierte Grundgesamtheit.
- High-Confidence-Falschaussagen höchstens 1 %, gebildet über **alle** ausgewerteten
  Gold-Tasks mit `confirmed`-Confidence.

### FR-7 — Full-vs-Incremental-Parität

Für denselben finalen Repository-Inhalt müssen Full Rebuild und inkrementelle Aktualisierung denselben Graphzustand erzeugen.

**Parität ist eine Zuverlässigkeits-, keine Performance-Eigenschaft.** Sie ist hier
verankert und wird in Tabelle §12.3 (Zuverlässigkeit) gegatet — nicht in der
Performance-Tabelle §12.2. Sie misst nicht, wie schnell ein Lauf ist, sondern ob zwei
Wege zum selben Graphen führen; ein langsamer Lauf kann paritätisch sein, ein schneller
nicht. Umgesetzt wird sie im Band P0-D. Die Basis existiert bereits als Byte-Parität in
`engine/conformance`; die vollständige 15-Klassen-Matrix ist noch offen.

#### Änderungsklassen

Mindestens:

- Datei hinzufügen
- Datei ändern
- Datei löschen
- Symbol umbenennen
- Symbol verschieben
- Package umbenennen
- Call hinzufügen
- Call entfernen
- Interface ändern
- Implementierung hinzufügen
- Implementierung entfernen
- Branch-Wechsel
- Build-Tag-Änderung
- Generated File ersetzen
- externe Importänderung

#### Acceptance Criteria

- 100 % Parität über die definierte Änderungsmatrix.
- Vergleich umfasst Nodes, Edges, Evidence, Confidence, IDs und relevante Metadaten.
- Wiederholte Ausführung ist idempotent.
- Crash Recovery konvergiert zum gleichen finalen Zustand.
- Keine orphaned external nodes.
- Keine stale linker edges.

### FR-8 — Performance Baseline

Performance wird ausschließlich auf dokumentierten Runnern und gepinnten Repositories gemessen.

#### Messungen

- Cold Index p50 und p95
- Warm Start
- Peak RSS
- DB-Größe
- Nodes
- Edges
- Bytes pro Edge
- längster Progress-Stall
- Search p50/p95
- Definition p50/p95
- Callers p50/p95
- Callees p50/p95
- References p50/p95
- Neighborhood p50/p95
- Impact p50/p95
- Agent Context p50/p95
- Freshness p50/p95
- Incremental Update p50/p95

#### Mindestlaufzahl

- zehn Cold Runs je primärem Benchmark-Szenario,
- mindestens 1.000 Query-Ausführungen je Query-Klasse,
- mindestens 100 inkrementelle Änderungen.

#### Performance Gates

- Cold Index p50 ≤ 90 Sekunden
- Cold Index p95 ≤ 120 Sekunden
- Peak RSS ≤ 2 GB
- kein OOM auf einem 8-GB-Host
- DB-Größe ≤ 300 MB für das definierte Referenzszenario
- Progress-Stall p95 ≤ 2 Sekunden
- Warm Search p95 ≤ 100 ms
- Caller/Callee/Impact p95 ≤ 200 ms
- Agent Context p95 ≤ 500 ms
- Freshness p95 ≤ 2 Sekunden

Die Full-/Incremental-Parität ist hier bewusst **nicht** aufgeführt: sie ist ein
Zuverlässigkeitsgate und steht in FR-7 beziehungsweise Tabelle §12.3.

Die Gates gelten ausschließlich für die definierten Referenzszenarien. Sie dürfen nicht als universelle Garantie für jedes Repository beworben werden. Welches Szenario das ist, definiert SW-123 (Open Question 12).

#### Peak RSS: Gate (2 GB) und Stop-Regel (4 GB)

Die beiden Speichergrenzen dieser PRD sind verschiedene Instrumente und gelten über
verschiedene Grundgesamtheiten. Sie widersprechen einander nicht:

| Grenze | Instrument | Gilt für | Wirkung beim Überschreiten |
|---|---|---|---|
| **2 GB** Peak RSS | **Gate** (§12.2) | ausschließlich das definierte Referenzszenario | Das Performance-Gate ist FAIL. P0 kann kein GO erhalten, bis der Wert gemessen unter der Grenze liegt oder die Ursache profilgestützt behoben ist. |
| **4 GB** Peak RSS | **Stop-Regel** (§17) | **jedes** gemessene Szenario, auch das 10k-Stressziel und jedes Nicht-Referenz-Repository | Scale-Claims werden gestoppt: Graphi darf über große Repositories keine Skalierungsaussage mehr veröffentlichen, bis der Wert erklärt oder behoben ist. Das Gate selbst wird dadurch nicht ersetzt. |

Daraus folgt die Lesart der Zwischenbereiche:

- Referenzszenario über 2 GB → Gate FAIL, unabhängig davon, ob 4 GB erreicht sind.
- Nicht-Referenz-Repository zwischen 2 GB und 4 GB → **kein** Gate-Verstoß, da das Gate
  nur das Referenzszenario bindet; der Wert wird trotzdem mit seiner Scope-Einschränkung
  veröffentlicht (NFR-6).
- Irgendein gemessenes Szenario über 4 GB → Stop-Regel greift programmweit, auch wenn das
  Referenzszenario sein 2-GB-Gate hält.

Die Stop-Regel ist damit strikt weiter gefasst als das Gate und niemals eine mildere
Alternative zu ihm.

#### Acceptance Criteria

- Rohmessungen werden gespeichert.
- Aggregationen sind aus Rohdaten reproduzierbar.
- CPU, RAM, OS, Kernel, Go-Version, Dateisystem und Cache-Zustand sind dokumentiert.
- Ein verfehltes Performance-Gate erzeugt Profile.
- Keine Optimierung ohne Profil.

### FR-9 — Reproducibility

Ein unabhängiger Engineer muss P0 reproduzieren können.

#### Erforderliche Bestandteile

- ein einziger ausführbarer Entry Point,
- dokumentierte Voraussetzungen,
- automatische Pin-Prüfung,
- automatischer Candidate-Abgleich,
- definierter Output-Pfad,
- maschinenlesbarer Ergebnisbericht,
- Rohdaten,
- Scorer-Version,
- klare Exit Codes.

#### Beispiel

```bash
CGO_ENABLED=0 go run ./cmd/eval \
  -p0-full-run \
  -candidate <sha> \
  -manifest corpus/p0-manifest.json \
  -output docs/eval/p0/<run-id>
```

Der konkrete Befehl kann anders lauten. Entscheidend ist ein einzelner dokumentierter Reproduktionspfad.

#### Acceptance Criteria

- Zwei vollständige aufeinanderfolgende Läufe sind grün.
- Der zweite Lauf wird von einer anderen Person oder einem sauberen Runner durchgeführt.
  Im Solo-Betrieb ist das der sauberer CI-Runner — ein **Substitut** nach §8.8, das als
  solches in der Evidence-Zeile ausgewiesen wird.
- Unterschiede werden nicht manuell verborgen.
- Alle Abweichungen besitzen eine Erklärung oder führen zu FAIL/UNKNOWN.

### FR-10 — Claims Inventory

Alle öffentlichen Aussagen müssen inventarisiert werden.

#### Zu prüfende Flächen

- `readme.md`
- Website
- Tutorial
- CLI Help
- Stability Tiers
- Feature Inventory
- Coverage Matrix
- Release Notes
- Benchmarkseiten
- Install Scripts
- Security- und Privacy-Dokumentation
- Code-Kommentare in akzeptanzrelevanten Tests

#### Claim-Kategorien

- Accuracy
- Performance
- Token Savings
- Security
- Privacy
- Zero Egress
- CGo-frei
- Language Support
- GA/Preview/Labs
- Binary Size
- External Dependencies
- Determinism
- Reproducibility
- Competitor Comparisons

#### Acceptance Criteria

Jeder öffentliche quantitative Claim besitzt:

- Candidate oder Release,
- Messartefakt,
- Methode,
- Runner,
- Datum,
- Einschränkung.

Jeder qualitative Claim ist so formuliert, dass er den tatsächlichen Scope nicht überschreitet.

Verbotene oder zu korrigierende Formulierungen:

- „fehlerfrei“
- „vollständig mathematisch bewiesen“
- „funktioniert auf jedem Repository in Millisekunden“
- „jede Netzwerkverbindung wird auf jedem System blockiert“
- „unterstützt alle Sprachen gleichwertig“
- „ersetzt CodeQL, Semgrep oder Sourcegraph“
- „garantiert, dass eine Änderung nichts bricht“

### FR-11 — Stale Comment and Documentation Detection

Akzeptanzrelevante Tests und Dokumente dürfen nicht offensichtlich dem aktuellen Code widersprechen.

#### Mindestanforderungen

- Kommentare mit historischen Zuständen wie `gateArmed=false` müssen aktualisiert werden, wenn der Code `true` verwendet.
- Erwartete Recall-Zahlen müssen mit dem aktuellen Fixture übereinstimmen.
- Entfernte oder superseded Pläne müssen eindeutig markiert sein.
- Referenzen auf alte Candidate-SHAs dürfen nicht als aktueller Stand erscheinen.

#### Acceptance Criteria

- Manuelles Review aller Hero-, Gold- und Security-Gates.
- CI-Prüfung für maschinenlesbare Claim- und Capability-Daten.
- Keine bekannte direkte Kommentar-Code-Kontradiktion in P0-relevanten Gates.

### FR-12 — Evidence Index

Jedes Gate benötigt einen Evidence-Eintrag. Der Index existiert bereits als
[`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml) (Quelle) mit generiertem
`evidence-index.md`, geprüft durch `go run ./cmd/evidence -check`. P0 **erweitert diesen
Index, es baut keinen zweiten** und legt keine neuen Zeilen-IDs an; §23 zeigt die genaue
Form.

#### Pflichtfelder

Die folgenden Angaben müssen für jedes Gate vorliegen. Wo der Index sie nicht als
Zeilenspalte führt (Corpus-Version, Runner, Scorer-Version, Datum, Sign-off), stehen sie im
Artefakt hinter `evidence_uri`; Candidate-SHA und Release-Digest stehen einmal im
`candidate:`-Block der Datei. Damit ist die Anforderung erfüllt, ohne die Form des Index zu
ändern.

```text
Gate
Threshold
Current Value
Status
Evidence URI
Candidate SHA
Release Digest
Corpus Version
Runner
Scorer Version
Owner
Date
Sign-off
Next Action
```

#### Statuswerte

- PASS
- FAIL
- UNKNOWN
- STALE

#### Acceptance Criteria

- PASS ohne Evidence URI ist unmöglich.
- PASS ohne Candidate SHA oder Digest ist unmöglich.
- Candidate-Wechsel markiert Einträge als STALE.
- UNKNOWN bleibt sichtbar.
- Der Evidence Index ist versioniert.

---

## 11. Nicht-funktionale Anforderungen

### NFR-1 — Determinismus

Identische Inputs müssen identische graphrelevante Outputs erzeugen:

- Node IDs
- Edge IDs
- Sortierung
- Evidence-Reihenfolge
- serialisierte Stable Responses
- Gold-Scoring

### NFR-2 — Local-first

P0 darf keine Cloud-Abhängigkeit einführen:

- keine Accounts,
- keine notwendige Remote API,
- keine Telemetrie,
- keine automatische Code-Übertragung,
- keine versteckten Repository-Uploads.

### NFR-3 — CGo-freier Default-Build

Der P0-Candidate muss mit `CGO_ENABLED=0` gebaut werden.

### NFR-4 — Security

- Repository Root Confinement
- sichere lokale Dateirechte
- keine ungeprüften Pfadausbrüche
- SBOM
- Checksums
- Attestation
- gepinnte GitHub Actions
- eindeutiger Release-Pfad

### NFR-5 — Reliability

- Crash Recovery
- idempotente Wiederholung
- kein stiller Datenverlust
- kein grüner Gate nach unvollständigem Lauf
- klare Exit Codes

### NFR-6 — Performance Transparency

Jeder Benchmark muss seine Grenzen offenlegen.

### NFR-7 — Maintainability

- Scorer getrennt von Produktionslogik
- Corpus versioniert
- keine versteckten Test-Ausnahmen
- keine Expected-Failure-Dauerlösungen
- keine manuelle Scorepflege

---

## 12. Messvertrag

### 12.1 Accuracy

| Metrik | Gate |
|---|---:|
| Symbol Precision | ≥ 98 % |
| Symbol Recall | ≥ 95 % |
| Edge Precision | ≥ 95 % |
| Edge Recall | ≥ 90 % |
| Source-Anchor Precision | ≥ 99 % |
| korrekte Abstention (Nenner: abstentionspflichtige Gold-Tasks, FR-6) | ≥ 95 % |
| High-Confidence-Falschaussagen (Nenner: alle Gold-Tasks mit `confirmed`-Confidence) | ≤ 1 % |
| Task Success | ≥ 90/100 |
| Cohen’s Kappa | ≥ 0,85 |

Jede Rate wird mit ihrem Nenner veröffentlicht. Eine Rate ohne ausgewiesene
Grundgesamtheit zählt als UNKNOWN.

### 12.2 Performance

Alle Werte dieser Tabelle gelten **ausschließlich für das definierte Referenzszenario**
(SW-123) und sind keine universelle Garantie.

| Metrik | Gate |
|---|---:|
| Cold Index p50 | ≤ 90 s |
| Cold Index p95 | ≤ 120 s |
| Peak RSS (Referenzszenario — Gate; vgl. 4-GB-Stop-Regel §17) | ≤ 2 GB |
| OOM auf 8-GB-Host | 0 |
| DB-Größe Referenzszenario | ≤ 300 MB |
| Progress-Stall p95 | ≤ 2 s |
| Warm Search p95 | ≤ 100 ms |
| Caller/Callee/Impact p95 | ≤ 200 ms |
| Agent Context p95 | ≤ 500 ms |
| Freshness p95 | ≤ 2 s |

Die Full-/Incremental-Parität stand in früheren Fassungen hier. Sie ist eine
Zuverlässigkeitseigenschaft und steht jetzt in §12.3.

### 12.3 Zuverlässigkeit

| Metrik | Gate |
|---|---:|
| Full-/Incremental-Parität über die Änderungsmatrix (FR-7) | 100 % |
| Idempotenz bei Wiederholung (FR-7) | 100 % |
| Recovery-Konvergenz nach Crash (FR-7, NFR-5) | 100 % |
| orphaned external nodes | 0 |
| stale linker edges | 0 |

### 12.4 Release und Reproduktion

| Metrik | Gate |
|---|---:|
| reproduzierbarer Build | PASS |
| Candidate SHA konsistent | 100 % |
| Release Digest konsistent | 100 % |
| Pin Verification | 100 % |
| zwei vollständige grüne Läufe | erforderlich |
| offene High/Critical Findings | 0 |
| Claim-Widersprüche | 0 bekannte |

---

## 13. Arbeitsblöcke, ausgedrückt in den Gate-IDs des Plans

Diese PRD führt **keine eigenen Gate-IDs**. Die Arbeitsblöcke unten sind Beschreibungen von
Arbeit; das dazugehörige Gate ist immer eine bestehende Zeile aus
[`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml), geprüft durch
`go run ./cmd/evidence -check`. Frühere Fassungen nummerierten die Blöcke selbst `WP0–WP8`
— diese Nummern kollidierten mit den anders belegten Plan-WPs (die PRD-`WP1` war
Candidate/Release, die Plan-`WP1` ist Claims-Bereinigung). Die Nummerierung ist deshalb
entfernt und durch die Zuordnung ersetzt.

| Arbeitsblock dieser PRD | Gate-ID im Evidence Index | Plan-Fundstelle |
|---|---|---|
| Programmkontrolle und Evidence Contract | `WP0` | Plan §6 WP0 |
| Candidate und Release Contract | `WP2` | Plan §6 WP2 (Candidate-SHA und Release-Digest festlegen) |
| Corpus und Runner Baseline | `WP2`, Exit über `M1` | Plan §6 WP2, §5 M1 |
| Gold-Corpus und Accuracy Scoring | `WP3` | Plan §6 WP3 |
| Accuracy-Fixes | `WP4`, Exit über `M2` | Plan §6 WP4, §5 M2 |
| Performance-Fixes | `WP4`, Exit über `M2` | Plan §6 WP4 (Performance-Gate) |
| Parität und Recovery | `WP4` (Paritätszeile), `WP6` (Recovery/RC) | Plan §6 WP4, WP6 |
| Claims und Dokumentation | `WP1`, Exit über `M0` | Plan §6 WP1, §5 M0 |
| Unabhängige Reproduktion | `WP2` (Exit-Gate), `M1` | Plan §6 WP2 Gate, §5 M1 |

Der P0-Scope als Ganzes ist die Präzisierung von `M0`, `M1` und `M2`. Wo diese PRD ein
Gate „erfüllt“ nennt, ist die zugehörige Evidence-Zeile gemeint, nicht ein PRD-eigener
Bezeichner.

### Programmkontrolle und Evidence Contract → `WP0`

#### Deliverables

- freigegebene PRD,
- benannte Owner,
- Candidate Decision Log,
- Evidence Index,
- Gate Dashboard,
- Change-Control-Regel.

#### Exit Gate

Kein P0-Claim ohne versionierte Evidenz.

### Candidate und Release Contract → `WP2`

#### Aufgaben

1. Candidate SHA auswählen.
2. Release-DAG verifizieren.
3. Binary bauen.
4. Digest erzeugen.
5. SBOM und Attestation erzeugen.
6. Build reproduzieren.
7. Plattformen dokumentieren.
8. Candidate Freeze veröffentlichen.

#### Exit Gate

Ein unabhängiger Build erzeugt dasselbe erwartete Artefakt beziehungsweise einen dokumentiert reproduzierbaren Release-Output.

### Corpus und Runner Baseline → `WP2`, Exit über `M1`

#### Aufgaben

1. fünf reale Go-Repositories auswählen,
2. Pins festlegen,
3. 10k-Stressziel auswählen,
4. Runnerklasse definieren,
5. Hardware und Toolchain dokumentieren,
6. Baseline Harness auf Candidate umstellen,
7. ersten vollständigen Lauf erzeugen.

#### Exit Gate

Alle Repositories sind gepinnt, alle Runs referenzieren denselben Candidate, Rohdaten sind vorhanden.

### Gold-Corpus und Accuracy Scoring → `WP3`

#### Aufgaben

1. Ontologie einfrieren,
2. Annotation Guide erstellen,
3. Pilotannotation mit 50 Symbolen und 100 Edges,
4. Kappa prüfen,
5. vollständiges Corpus annotieren,
6. 100 Tasks versiegeln,
7. Scorer implementieren,
8. Thresholds versionieren.

#### Exit Gate

Corpus-Größe, Kappa und Scorer-Anforderungen erfüllt.

### Accuracy-Fixes → `WP4`, Exit über `M2`

#### Aufgaben

- Gold-Fehler klassifizieren,
- Priorisierung nach Nutzerwirkung und Confidence,
- Regressionstest vor Fix,
- nur Go und Stable Operations ändern,
- nach jedem Batch komplette Gates erneut ausführen.

#### Exit Gate

Alle Accuracy-Gates in zwei aufeinanderfolgenden vollständigen Läufen grün.

### Performance-Fixes → `WP4`, Exit über `M2`

#### Aufgaben

- verfehlte Metriken profilieren,
- CPU-, Heap-, Allocation- und I/O-Profile sichern,
- nur nachgewiesene Hotspots optimieren,
- keine Genauigkeit gegen Geschwindigkeit tauschen,
- große Repositories erneut messen.

#### Exit Gate

Alle Performance-Gates in zwei vollständigen Läufen grün.

### Parität und Recovery → `WP4` (Paritätszeile), `WP6` (Recovery)

#### Aufgaben

- Änderungsmatrix ausführen,
- Full-vs-Incremental vergleichen,
- Crash-Injection,
- Restore,
- Branch-Wechsel,
- stale edge und orphan node checks.

#### Exit Gate

100 % Parität und Recovery-Konvergenz.

### Claims und Dokumentation → `WP1`, Exit über `M0`

#### Aufgaben

- Claim Inventory,
- README Review,
- Website Review,
- CLI Help Review,
- Stability-Tier Review,
- Kommentarbereinigung,
- quantitative Claims an Evidence binden.

#### Exit Gate

Kein bekannter Claim widerspricht Candidate oder Messung.

### Unabhängige Reproduktion → `WP2` (Exit-Gate), `M1`

#### Aufgaben

- sauberen Runner bereitstellen,
- unabhängigen Engineer benennen — im Solo-Betrieb durch den sauberen CI-Runner
  **substituiert** (§8.8), und als Substitution in der Evidence-Zeile ausgewiesen,
- vollständigen Lauf ohne Maintainer-Hilfe durchführen,
- Abweichungen dokumentieren,
- Sign-off einholen.

#### Exit Gate

Unabhängige Reproduktion bestanden — oder, bei Substitution, als substituiert bestanden
gekennzeichnet. Ein substituierter Nachweis wird nie als unabhängige Reproduktion
berichtet.

---

## 14. Priorisierte Backlog-Struktur

Die Bezeichnungen P0-A bis P0-E sind **Backlog-Bänder zur Priorisierung, keine Gate-IDs**.
Keine Evidence-Zeile wird über sie geschlüsselt; das gemessene Gate ist immer eine WP/M-Zeile
nach §13. Die Bänder sagen, in welcher Reihenfolge gearbeitet wird — nicht, was bewiesen ist.

### P0-A — Blocker

1. P0 Owner und Reviewer benennen.
2. Candidate SHA einfrieren.
3. Release Digest erzeugen.
4. aktuelle Referenz-Neubaseline ausführen.
5. Corpus Manifest mit fünf Go-Repositories erstellen.
6. Runnerklasse dokumentieren.
7. Gold-Ontologie einfrieren.
8. Annotation Guide pilotieren.
9. Claim Inventory erstellen.
10. veraltete akzeptanzrelevante Kommentare korrigieren.

### P0-B — Accuracy

11. Symbol Gold Scorer.
12. Edge Gold Scorer.
13. Anchor Scorer.
14. Abstention Scorer.
15. High-Confidence-Error Scorer.
16. 100 Task Harness.
17. Go Type Resolver Degradation Report.
18. Package-Level Accuracy Breakdown.
19. Operation-Level Accuracy Breakdown.
20. Regression Corpus für gefundene Fehler.

### P0-C — Performance

21. Cold Index Harness.
22. Query Latency Harness.
23. Freshness Harness.
24. Peak RSS Capture.
25. DB Size Capture.
26. Progress Stall Measurement.
27. 10k Stressrepo.
28. Profiling Automation.
29. Raw Data Export.
30. Result Aggregator.

### P0-D — Reliability

31. Full-vs-Incremental Matrix.
32. Rename/Move Cascade Tests.
33. Delete Last Reference Test.
34. Orphan External Node Test.
35. Crash Boundary Matrix.
36. Branch Switch Journey.
37. Recovery Reproduction.
38. Idempotency Run.

### P0-E — Evidence

39. Evidence Index Generator.
40. Candidate Staleness Detection.
41. Claim-to-Evidence Mapping.
42. Independent Reproduction Script.
43. P0 Final Report.
44. P0 Go/No-Go Decision Record.

---

## 15. Delivery Sequence

### Phase 1 — Control and Freeze

- PRD freigeben
- Owner benennen
- Candidate festlegen
- Evidence Contract festlegen
- Claims-Inventar beginnen

### Phase 2 — Baseline

- Repositories pinnen
- Runner einfrieren
- aktuelle vollständige Baseline ausführen
- Messlücken klassifizieren

### Phase 3 — Gold-Corpus

- Ontologie
- Pilotannotation
- Kappa
- vollständige Annotation
- Task-Versiegelung
- Scorer

### Phase 4 — Fix Loop

```text
Failure
→ Gold/Regression Test
→ Root Cause
→ Minimal Fix
→ Accuracy
→ Performance
→ Parity
→ Recovery
→ Security
```

### Phase 5 — Independent Reproduction

- Candidate-Binary neu bauen
- kompletten Lauf reproduzieren
- Evidenz einfrieren
- Sign-off

### Phase 6 — Go/No-Go

P0 ist nur GO, wenn alle blockierenden Gates PASS sind.

---

## 16. Go/No-Go-Kriterien

### GO

P0 erhält GO, wenn:

- Candidate und Digest eingefroren sind,
- zwei vollständige Läufe grün sind,
- alle Accuracy-Gates erfüllt sind,
- alle Performance-Gates erfüllt sind,
- Parität 100 % beträgt,
- unabhängige Reproduktion bestanden ist,
- keine offenen High/Critical Findings bestehen,
- kein bekannter Claim-Widerspruch besteht,
- Evidence Index vollständig ist.

### CONDITIONAL GO

Nicht zulässig für den Abschluss von P0.

Ein Candidate mit offenen UNKNOWN- oder FAIL-Gates darf nicht als P0-erfolgreich bezeichnet werden.

### NO-GO

P0 ist NO-GO bei:

- Candidate Drift,
- fehlender Reproduktion,
- Symbol- oder Edge-Gates unter Threshold,
- High-Confidence-Falschaussagen über 1 %,
- Full-/Incremental-Divergenz,
- OOM im Referenzszenario,
- nicht reproduzierbaren Benchmarks,
- offenem High/Critical Security Finding,
- öffentlichen Claims ohne Evidence,
- manipulierten oder nachträglich erleichterten Thresholds.

---

## 17. Stop-Regeln

- Kein Feature-Backlog öffnen, solange Candidate, Baseline oder Gold-Corpus fehlen.
- Thresholds werden nicht gesenkt, um einen Release zu ermöglichen.
- Ein Candidate-Wechsel setzt abhängige Evidenz zurück.
- Ein High/Critical Finding stoppt Release und Reproduktion.
- Peak RSS über 4 GB **in irgendeinem gemessenen Szenario** stoppt Scale-Claims. Diese
  Stop-Regel ist weiter gefasst als das 2-GB-Gate aus §12.2, das nur das Referenzszenario
  bindet, und ersetzt es nicht: das Referenzszenario kann sein Gate reißen, ohne dass die
  Stop-Regel greift, und die Stop-Regel kann greifen, während das Referenzszenario sein
  Gate hält. Die vollständige Beziehung steht in FR-8, Abschnitt „Peak RSS: Gate (2 GB)
  und Stop-Regel (4 GB)“.
- Nicht reproduzierbare Performancewerte werden nicht veröffentlicht.
- Ein Preview- oder Labs-Feature wird nicht genutzt, um einen Stable-Fehler zu kaschieren.
- Keine neue GA-Sprache vor erfolgreichem P0.
- Keine neue Stable Operation vor erfolgreichem P0.
- Kein „9/10“-Claim auf Basis interner Scorecards allein.

---

## 18. Risiken und Mitigationen

### Risiko 1 — Gold-Corpus wird an die Implementierung angepasst

**Mitigation:**

- Annotation Guide vor vollständigem Lauf einfrieren.
- Daten von Implementierung trennen.
- unabhängige Annotation.
- versiegelte Tasks.
- Audit Trail.

### Risiko 2 — Benchmark Overfitting

**Mitigation:**

- mehrere reale Repositories,
- Stressrepo,
- Rohdaten,
- getrennte Baseline- und Validierungsruns,
- kein Optimieren auf nur ein Fixture.

### Risiko 3 — Candidate bewegt sich zu häufig

**Mitigation:**

- Change Control,
- nur blockierende Fixes,
- automatische STALE-Markierung.

### Risiko 4 — Scope wächst während P0

**Mitigation:**

- Non-Goals verbindlich,
- keine neue GA-Sprache,
- keine neuen Stable Operations,
- separate Post-P0-Ideenliste.

### Risiko 5 — Interne Reviewer bestätigen eigene Annahmen

**Mitigation:**

- unabhängiger Reproduktionslauf,
- externe Annotation oder Review,
- klare Evidence-Links.

### Risiko 6 — Gute Precision bei schlechtem Recall

**Mitigation:**

- Precision und Recall getrennt gaten,
- Abstention separat messen,
- fehlende Edges als eigenes Fehlerbild.

### Risiko 7 — Determinismus wird mit Korrektheit verwechselt

**Mitigation:**

- getrennte Metriken,
- Claim-Regeln,
- Confidence und Coverage offenlegen.

### Risiko 8 — Alte Dokumente widersprechen dem Candidate

**Mitigation:**

- Claim Inventory,
- superseded marker,
- Docs-vs-Code Gates, soweit maschinenlesbar.

### Risiko 9 — Performancewerte sind nicht vergleichbar

**Mitigation:**

- Runnerklasse einfrieren,
- Harness-Version speichern,
- alte und neue Methodik nicht vermischen.

---

## 19. Telemetrie und Datenschutz

P0 führt keine Produkttelemetrie ein.

Messungen erfolgen:

- lokal,
- in CI,
- über explizit ausgeführte Evaluationsharnesses,
- auf gepinnten öffentlichen oder autorisierten Repositories.

Erfasste Daten:

- technische Laufzeiten,
- Speicherverbrauch,
- Graphgrößen,
- Accuracy-Ergebnisse,
- Scorer-Ausgaben,
- Candidate- und Runner-Metadaten.

Nicht erfasst:

- private Quellcodeinhalte in externen Services,
- Nutzeridentität ohne Zustimmung,
- versteckte Nutzungsdaten,
- automatische Telemetrie.

---

## 20. Security Requirements

- Default-Build bleibt CGo-frei.
- kein erforderlicher Netzwerkzugriff.
- keine Telemetrie-SDKs.
- keine unsanktionierten Outbound Dials.
- Pfade bleiben innerhalb des Repository-Roots.
- lokale Graph- und Sidecar-Dateien besitzen restriktive Rechte.
- Release Actions sind auf vollständige SHAs gepinnt.
- genau ein autorisierter Publish-Pfad.
- SBOM und Attestation je Candidate.
- Security Findings sind im Evidence Index sichtbar.

---

## 21. Open Questions

Sechs der zwölf Fragen sind durch die Aufnahme dieser PRD ins Repository beantwortet
beziehungsweise einer benannten Story zugewiesen. Die übrigen bleiben ausdrücklich offen —
eine offene Frage wird nicht durch eine plausible Vermutung geschlossen.

### Beantwortet oder zugewiesen

| # | Frage | Antwort |
|---|---|---|
| **1** | Welche fünf Go-Repositories bilden das primäre Corpus? | **Zugewiesen an SW-122.** Heute enthält `corpus/manifest.json` (v2, fail-closed gepinnt) sechs reale Repositories, davon genau **eines in Go** (cobra v1.8.0). SW-122 hebt das Manifest auf v3 mit fünf gepinnten Go-Repositories nach der Stratifizierung aus FR-2. Bis dahin ist FR-2 nicht erfüllt, und keine Accuracy- oder Performance-Zahl darf für „das Corpus“ sprechen. |
| **2** | Welches Repository oder Fixture bildet das 10k-Stressziel? | **Zugewiesen an SW-122**, in derselben Manifest-Anhebung. Das Stressziel wird gepinnt wie jedes andere Repository; ein nicht auflösbarer Pin blockiert die Evaluation (FR-2). |
| **3** | Welche Runnerklasse ist die offizielle Referenz? | **Zugewiesen an SW-123.** Die faktisch verwendete Klasse ist heute `ubuntu-latest` (GitHub-hosted), so ausgewiesen in `docs/eval/runs/2026-07-15-ubuntu-latest` und in `-runner-class` von `cmd/eval`; `local-sandbox` existiert daneben als nicht vergleichbare zweite Klasse. SW-123 erklärt genau eine davon zur Referenz und dokumentiert CPU, RAM, Image, Toolchain und Cache-Zustand nach FR-8. Werte aus einer anderen Klasse werden nicht mit Referenzwerten gemischt (Risiko 9). |
| **4** | Wer ist accountable für P0? | **Beantwortet: der Graphi Maintainer.** Das Projekt ist Solo ohne Budget; die Rollen ENG/EVAL/PROD/SEC aus dem Plan §4 liegen bei einer Person. Das ist keine Erfüllung des Rollenmodells, sondern dessen Substitution — die Regeln und Grenzen stehen in §8.8. |
| **10** | Muss der bestehende Candidate übernommen oder neu eingefroren werden? | **Beantwortet: neu einfrieren; zugewiesen an SW-121.** Der Candidate `4e72637` aus `docs/decisions/2026-07-m0-candidate-freeze.md` (2026-07-16) liegt **99 Commits und 8 Tags** hinter `main`; die Releases v0.5.1 … v0.6.6 sind danach erschienen, und sein `release_digest` steht in `docs/rc/evidence-index.yaml` bis heute auf `UNKNOWN`, weil aus diesem SHA nie veröffentlicht wurde. Ihn zu messen hieße, die Qualität eines Artefakts zu belegen, das niemand installiert. P0 friert daher auf einem **getaggten Release mit realem Digest, SBOM und Attestation** neu ein; die davon abhängige Evidenz wird nach FR-1 als STALE markiert. |
| **12** | Welche Performancegrenzen gelten pro Repository und welche nur für das Referenzszenario? | **Beantwortet: sämtliche Gates aus §12.2 gelten ausschließlich für das Referenzszenario.** Kein Performance-Gate dieser PRD bindet ein beliebiges Repository. Für alle anderen gemessenen Szenarien gilt nur die programmweite 4-GB-Stop-Regel aus §17; ihre Werte werden veröffentlicht, aber mit Scope-Einschränkung und ohne Gate-Charakter (NFR-6, FR-8). Welches Szenario das Referenzszenario ist, legt SW-123 fest. |

### Weiterhin offen

Diese Fragen sind **nicht** in dieser Scheibe beantwortet und bleiben UNKNOWN:

5. Wer führt die unabhängige Reproduktion durch? — Die Substitutionsregel in §8.8 begrenzt
   die zulässigen Antworten bereits (sauberer CI-Runner, als Substitut ausgewiesen), aber
   der konkrete Lauf ist erst mit SW-130 zu benennen.
6. Wo werden versiegelte Gold-Daten gespeichert? — Band P0-B, zurückgestellt.
7. Welche Teile des Gold-Corpus dürfen öffentlich sein? — Band P0-B, zurückgestellt.
8. Wie wird Candidate-Staleness technisch markiert? — Die *manuelle* Markierung auf dem
   Candidate-Wechsel liegt in SW-121; die *automatische* Erkennung ist Band P0-E und
   zurückgestellt.
9. Welches Format wird für Rohmessungen verwendet? — Richtung ist vorgezeichnet
   (`internal/evalreport` statt eines zweiten Berichtsformats), das Schema entscheidet
   SW-128.
11. Welche Claims sind Release-blockierend und welche Patch-Follow-ups? — Band P0-A,
    Claim-Inventar; noch nicht geschnitten.

---

## 22. Decision Log Template

```markdown
## Decision P0-XXX

**Date:**  
**Owner:**  
**Status:** Proposed | Accepted | Rejected | Superseded  
**Decision:**  
**Context:**  
**Alternatives considered:**  
**Consequences:**  
**Affected evidence:**  
**Required reruns:**  
**Candidate changed:** yes/no  
```

---

## 23. Evidence Entry Template

Das Schema ist nicht neu zu erfinden: `docs/rc/evidence-index.yaml` führt es bereits, und
`go run ./cmd/evidence -check` prüft es in CI. Eine P0-Zeile sieht daher so aus — mit einer
**bestehenden** ID, nicht mit einem PRD-eigenen Bezeichner:

```yaml
- id: WP3                      # bestehende Zeilen-ID (WP0–WP10 | M0–M5), keine neue
  gate: Gold corpus & accuracy scoring
  section: plan §6 WP3
  threshold: "Edge Precision ≥95%; …"
  current: "UNKNOWN — Ontologie nicht eingefroren; Gold-Corpus nicht gebaut."
  status: UNKNOWN              # PASS | FAIL | UNKNOWN | STALE
  evidence_uri: ""             # PASS ohne diesen Wert ist in CI unmöglich
  sha: ""                      # PASS ohne diesen Wert ist in CI unmöglich
  owner: EVAL
  next_action: "Ontologie einfrieren, Corpus bauen, Thresholds versionieren, dann scoren."
  due: W2–10
```

Der Candidate-SHA und der Release-Digest stehen **einmal** im `candidate:`-Block am Kopf
derselben Datei, nicht pro Zeile. Die zusätzlichen Felder aus FR-12 (Corpus-Version,
Runner, Scorer-Version, Sign-off, Datum) gehören in das Artefakt, auf das `evidence_uri`
zeigt — nicht in neue Zeilenspalten. So bleibt die Form des Index unverändert, während die
Pflichtangaben trotzdem vollständig sind.

Eine Zeile mit `gate: P0-ACC-EDGE-PRECISION` — so stand es im Entwurf — wäre ein drittes
Gate-Vokabular und würde den einzigen Report brechen, der heute ehrlich meldet. Sie ist
nicht zulässig.

---

## 24. Definition of Done

P0 ist abgeschlossen, wenn:

- [x] PRD freigegeben ist. — SW-120: diese Datei, mit Autoritätsvermerk und angewandten
      Review-Korrekturen (§0).
- [x] Owner und Reviewer benannt sind. — Graphi Maintainer, solo; Rollensubstitution nach
      §8.8 ausgewiesen (Open Question 4).
- [x] Candidate SHA festgelegt ist. — SW-131: `5815db5`, Tag `v0.7.0`
      (`docs/decisions/2026-07-p0-candidate-freeze-v070.md`). Löst SW-121s `fb3bf03` /
      `v0.6.7` durch dokumentierten Blocker-Fix ab: jener Candidate war
      konstruktionsbedingt nicht messbar (P0-Harness bei der SHA nicht vorhanden; kein
      Pfad für ein externes Binary). Produktbaum zwischen beiden SHAs byte-identisch —
      Begründung der Unbedenklichkeit, keine Messaussage.
- [x] Release Digest vorliegt. — SW-131: acht Asset-Digests des veröffentlichten
      Releases, nicht mehr `UNKNOWN`; Referenzplattform `graphi-linux-amd64`
      `sha256:f91aa839…9d25`. Alle fünf Plattform-Binaries bit-für-bit aus dem
      eingefrorenen SHA nachgebaut (tagloser Clone, kein linked worktree).
- [x] SBOM und Attestation vorliegen. — SW-131: SPDX-SBOM und SLSA-v1-Provenance als
      Assets desselben Release-Runs (30363482852), an Workflow-Identität und
      Source-Digest `5815db5` gebunden; alle acht Assets verifizieren, Negativkontrolle
      mit `fb3bf03` schlägt fehl (Exit 1).
- [ ] fünf reale Go-Repositories gepinnt sind.
- [ ] ein 10k-Stressziel gepinnt ist.
- [ ] Runnerklasse dokumentiert ist.
- [ ] zehn Cold Runs durchgeführt wurden.
- [ ] 1.000 Queries je Query-Klasse gemessen wurden.
- [ ] 100 inkrementelle Änderungen gemessen wurden.
- [ ] Gold-Corpus mindestens 1.000 Symbole enthält.
- [ ] Gold-Corpus mindestens 2.000 Beziehungen enthält.
- [ ] 100 versiegelte Tasks vorhanden sind.
- [ ] mindestens 20 % doppelt annotiert wurden.
- [ ] Kappa mindestens 0,85 beträgt.
- [ ] Symbol Precision mindestens 98 % beträgt.
- [ ] Symbol Recall mindestens 95 % beträgt.
- [ ] Edge Precision mindestens 95 % beträgt.
- [ ] Edge Recall mindestens 90 % beträgt.
- [ ] Source-Anchor Precision mindestens 99 % beträgt.
- [ ] korrekte Abstention mindestens 95 % beträgt.
- [ ] High-Confidence-Falschaussagen höchstens 1 % betragen.
- [ ] mindestens 90 von 100 Tasks erfolgreich sind.
- [ ] alle Performance-Gates erfüllt sind.
- [ ] Full-/Incremental-Parität 100 % beträgt.
- [ ] Recovery-Gates grün sind.
- [ ] zwei vollständige aufeinanderfolgende Läufe grün sind.
- [ ] unabhängige Reproduktion bestanden ist.
- [ ] Claim Inventory abgeschlossen ist.
- [ ] kein bekannter öffentlicher Claim dem Candidate widerspricht.
- [ ] keine offenen High/Critical Findings bestehen.
- [ ] Evidence Index vollständig ist.
- [ ] P0 Go/No-Go dokumentiert ist.

---

## 25. Post-P0 Entry Criteria für P1

P1 darf beginnen, wenn P0 GO erhält.

Erlaubte erste P1-Themen:

- `graphi trust-report`
- MCP `graph_health`
- Confidence- und Coverage-Sichtbarkeit
- Strict Query Mode als Labs-Prototyp
- bessere Darstellung von degraded Packages
- External Usage Index als späterer Schritt

Nicht automatisch erlaubt:

- neue GA-Sprache,
- neue Stable Operation,
- Taint-GA,
- Refactoring-GA,
- SaaS,
- Cross-Repository Cloud Index.

Diese benötigen eigene PRDs und zusätzliche Evidenz.

---

## 26. Kurzfassung der Produktentscheidung

P0 priorisiert nicht mehr Features, sondern mehr Wahrheit.

```text
Candidate einfrieren
→ reale Repositories messen
→ Gold-Corpus erstellen
→ Stable Operations scoren
→ gemessene Fehler beheben
→ Performance profilbasiert verbessern
→ Parität und Recovery beweisen
→ Claims korrigieren
→ unabhängig reproduzieren
→ Go/No-Go
```

Das Ergebnis von P0 soll ein fokussierter Go-Kern sein, dessen Grenzen, Qualität und Performance nicht nur behauptet, sondern reproduzierbar belegt sind.
