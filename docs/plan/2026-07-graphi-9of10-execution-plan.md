# Graphi 9/10 — ausführbarer 32-Wochen-Plan

> ## Autoritätsvermerk (SW-117, 2026-07-17)
>
> **Dieses Dokument ist seit dem Candidate-Freeze die einzige Planungsautorität für
> graphi.** Es ersetzt die zwölf zuvor konkurrierenden Pläne, die inzwischen aus dem
> Repository entfernt wurden (auffindbar über die Git-Historie). Im Konfliktfall
> gewinnt dieses Dokument.
>
> **Candidate-Freeze:** Der maßgebliche Candidate ist
> `4e72637d3c2c0dc7d32142a590d46c0c62c10733`, festgehalten in
> [`docs/decisions/2026-07-m0-candidate-freeze.md`](../decisions/2026-07-m0-candidate-freeze.md)
> (SW-116). **Nicht** der unten stehende `Ausgangs-SHA: e285822` — der stammt aus der
> Zeit vor dem Freeze; `release-dag.yml` trägt eine einzige `github.sha` durch
> gate → build → SBOM → publish, weshalb ein Candidate auf einem Feature-Branch keine
> Attestation tragen kann. Das Entscheidungsdokument ist auf diesen Punkt maßgeblich.
>
> **Zur fehlenden PRD:** WP0 nennt als erstes Ergebnis „eine freigegebene PRD als
> einzige Planungsautorität“. Diese PRD existiert nirgends — weder in einem der beiden
> Checkouts noch in der Git-History oder im Stash; sie war immer nur Prompt-Text. Bis
> sie geschrieben ist, trägt dieser Plan die Autorität. Das Verfassen der PRD ist als
> datierter Backlog-Eintrag im Scrum-Portfolio festgehalten (Story SW-117). Eine
> Rekonstruktion der PRD aus dem Plan, der sie zitiert, wurde bewusst verworfen: sie
> würde genau die Autorität erfinden, die M0 wahrheitsfähig machen soll.
>
> **Status und Datum unten** beziehen sich auf die ursprüngliche Fassung. Der Inhalt
> dieses Plans ist seit dem Carry aus `~/Documents/Graphi` unverändert; dieser Vermerk
> ist die einzige Ergänzung.

**Status:** Proposed

**Datum:** 2026-07-16

**Ausgangs-SHA:** `e285822`

**Ziel:** Die Definition of Done der PRD „Graphi 9/10“ mit demselben,
reproduzierbaren GA-Release-Artefakt erfüllen.

**Planungshorizont:** P50 32 Wochen, P80 40 Wochen

**Rewrite:** Nein

## 1. Ergebnis, das dieser Plan liefern muss

Am Ende existiert genau ein eingefrorenes, signiertes und attestiertes
GA-Release für Go, CLI und MCP stdio. Sechs unabhängige Experten prüfen dieses
Artefakt und das dazugehörige Evidence Pack. Der Durchschnitt beträgt
mindestens 9,0; keine Dimension liegt unter 8,5; Security und DevOps liegen
jeweils bei mindestens 9,0; es gibt keine offenen High- oder Critical-Blocker.

Der Plan ist nicht erfolgreich, wenn nur der Code oder die CI grün ist. Er ist
erst erfolgreich, wenn Accuracy, Performance, Security, Reliability, externe
Nutzung, Retention, Zahlungsbereitschaft und Renewals nachgewiesen sind.

## 2. Nicht verhandelbare Leitplanken

1. Der GA-Scope bleibt auf Go, CGo-freies Binary, CLI, MCP stdio, `index`, die
   zwölf stabilen Operationen, lokalen Graph Store, inkrementelle Aktualisierung
   und Source Anchors begrenzt.
2. HTTP, Daemon, UI, TUI, VS Code, GitHub Action, Refactorings, Taint, Memory,
   Wiki, semantische Suche, weitere Sprachen und SaaS bleiben Labs/Preview oder
   Source-only. Sie erhalten in diesem Programm keine Feature-Arbeit.
3. Der Candidate-SHA wird nach M0 nur über dokumentierte Blocker-Fixes bewegt.
   Jeder neue SHA invalidiert alle davon abhängigen Messungen und Artefakte.
4. UNKNOWN zählt als nicht bestanden. Interne Scores, Downloads, Stars und
   Smoke-Tests ersetzen kein PRD-Gate.
5. Performance wird nur nach reproduzierbarem Profiling optimiert.
6. Go darf nach den ersten zehn ICP-Interviews genau einmal ersetzt werden.
   Danach ist der Sprachscope eingefroren.
7. Ohne fünf bezahlte Piloten und drei Renewals/Expansions gibt es keine 9/10.

## 3. Ausgangslage am SHA `e285822`

| Bereich | Bereits vorhanden | Für 9/10 noch offen |
|---|---|---|
| Release | CGo-freier, SHA-gebundener DAG, Checksums, SBOM/Attestation-Gates | finaler Candidate und 10/10 Reproduktionsnachweis |
| Core | Stable-Ops, Capability Contract, selective reads, Recovery, Surface-Ports | unabhängiger Erweiterbarkeitstest mit 4 Entwicklern |
| Evaluation | 20 Hero-Tasks, Full-Run-Harness, gepinnte Smoke-Repositories | 5 reale Go-Repos, 1.000 Symbole, 2.000 Beziehungen, 100 versiegelte Tasks, Adjudikation |
| Performance | aktueller Harness und vorläufige Budgets | Current-Candidate-Baseline, 10k-Datei-Stressrepo, Rohdaten, alle PRD-Perzentile |
| Security | Privacy-, Egress-, Pfad- und Supply-Chain-Gates | Threat Model, externer Pentest, Finding-SLAs, Datenklassifikation/Retention |
| Reliability | Crash-/Recovery-Tests und Release-Gates | 90 Tage RC-Historie, Drill-Protokolle, First-Attempt-/Flake-Metriken |
| Produkt | technische Journey-Tests | 25 unbeaufsichtigte Tests, SUS, TTFV, 20 Organisationen, Week-8-Retention |
| Business | keine belastbare Evidenz | 30 Interviews, 5 bezahlte Piloten, 3 Renewals/Expansions, Unit Economics |
| Claims | Capability-Dokumentation vorhanden | öffentliche GA-/Preview-/Labs-Texte konsequent auf PRD-Scope reduzieren |

Wichtig: Das bestehende Real-Repository-Corpus ist ein Smoke-/Regressionstest,
kein Gold-Corpus. Die vorhandenen 20 Hero-Aufgaben sind eine gute technische
Basis, erfüllen aber nicht das Gate von 100 versiegelten Coding Tasks.

## 4. Programmstruktur und Verantwortliche

| Rolle | Kürzel | Hauptverantwortung |
|---|---|---|
| Founder/GTM Owner | GTM | ICP, Design-Partner, Piloten, Pricing, Renewals |
| Product Researcher/Designer | PROD | Journey, Usability, SUS, Retention, Research Ops |
| Evaluation/Data Engineer | EVAL | Ontologie, Annotation, Scoring, Benchmark-Evidenz |
| Senior Go/Platform Engineers (2) | ENG | Candidate, Accuracy-Fixes, Performance, Release, Recovery |
| Security/DevOps Engineer (0,5) | SEC | Threat Model, Pentest-Koordination, RC-Metriken, Drills |
| Externe Annotatoren | ANN | Gold-Corpus nach Annotation Guide |
| Unabhängige Reviewer (6) | AUDIT | abschließende Sechs-Dimensionen-Prüfung |

Eine Person ist pro Arbeitspaket accountable. „Team“ ist kein Owner. GTM und
PROD beginnen in Woche 1; sie warten nicht auf die technische Baseline.

## 5. Meilensteinplan

| Meilenstein | Zeitraum | Ergebnis | Exit-Gate |
|---|---:|---|---|
| M0 Wahrheit und Scope | W0–2 | Candidate, Claims und Messvertrag eingefroren | kein Claim widerspricht Artefakt oder PRD |
| M1 Baseline | W2–6 | reproduzierbare Accuracy-/Performance-Rohbaseline | identischer SHA, Runner, Pins und Rohdaten |
| M2 Accuracy/Performance | W4–12 | Gold-Corpus und alle technischen KPI-Gates grün | alle PRD-Accuracy-/Performance-Grenzen erfüllt |
| M3 Security und RC | W6–14 | Pentest-Findings geschlossen, RC gestartet | keine offenen High/Critical; 90-Tage-Uhr läuft |
| M4 Product/Business | W1–28 | Retention, Piloten und Renewals belegt | alle Product-/Business-Gates erfüllt |
| M5 Audit | W28–32 | unabhängige Bewertung desselben Artefakts | Ø ≥9,0, keine Dimension <8,5 |
| P80-Puffer | W33–40 | nur Finding-Fixes und erneute Prüfung | identische Gates nach Fix erneut grün |

### Kritischer Pfad

Der längste Pfad ist nicht Engineering, sondern:

`ICP-Interviews → Design-Partner → bezahlter Pilot → 8-Wochen-Nutzung → Renewal/Expansion → Audit`

Parallel dazu läuft:

`Candidate → Baseline → technische Fixes → Pentest → 90-Tage-RC → Evidence Freeze → Audit`

Ein Pilot muss deshalb spätestens in Woche 8 starten, damit Week-8-Retention und
Renewal vor Woche 28 überhaupt messbar sind. Der fokussierte RC muss spätestens
in Woche 14 starten, um vor dem Audit 90 aufeinanderfolgende Tage zu erreichen.

## 6. Arbeitspakete

### WP0 — Program Control und Evidence Contract

**Owner:** ENG Lead

**Zeitraum:** W0–2, danach wöchentlich

Lieferobjekte:

- eine freigegebene PRD als einzige Planungsautorität;
- Decision Log für Candidate-Wechsel, Scope und Stop-Regeln;
- Evidence-Index mit SHA, Release-Digest, Runner, Repo-Pins, Toolversionen,
  Rohdaten, Auswertung und Sign-off;
- wöchentliches Gate-Dashboard mit PASS/FAIL/UNKNOWN;
- Change-Control-Regel: Candidate-Wechsel nur bei PRD-Blocker, mit expliziter
  Liste aller zu wiederholenden Messungen.

Gate: Jede 9/10-Behauptung lässt sich auf ein versioniertes Rohartefakt
zurückführen. Kein manuell gepflegtes „grün“ ohne Beleg.

### WP1 — GA-Scope und Claims bereinigen

**Owner:** PROD, technische Freigabe durch ENG

**Zeitraum:** W0–2

Aufgaben:

- README, Website, Installationspfad, CLI-Hilfe, Capability-Matrix und Release
  Notes gegen den PRD-GA-Scope prüfen;
- Go als einziges GA-Sprachziel markieren; alle anderen Sprachen Preview;
- Browser/UI, HTTP, Daemon, TUI, PR-Automation, Refactoring, Taint, Memory,
  Wiki und semantische Suche sichtbar als Labs/Preview kennzeichnen;
- unbelegte Speed-, Savings-, Token- und Wettbewerbsclaims entfernen;
- Journey auf `install → index → MCP stdio → zitierte Antwort` reduzieren;
- automatisierten Claim-/Scope-Regressionstest ergänzen, soweit Aussagen
  maschinenlesbar sind.

Gate: Ein externer Reviewer kann anhand öffentlicher Dokumentation GA, Preview,
Labs und Source-only eindeutig unterscheiden; kein Text verspricht mehr als das
Candidate-Artefakt.

### WP2 — Candidate und reproduzierbare technische Baseline

**Owner:** ENG

**Zeitraum:** W1–6

Aufgaben:

- Candidate-SHA und Release-Digest festlegen;
- `eval-full` auf genau diesem SHA und dem daraus gebauten Release-Binary laufen
  lassen, nicht über ein abweichendes Development-Binary;
- mindestens fünf reale, gepinnte Go-Repositories auswählen; eines davon mit
  mindestens 10.000 Quelldateien oder ein zusätzliches gepinntes Stressrepo;
- Runner vollständig dokumentieren: CPU, RAM, OS/Image, Toolchain,
  Strom-/CPU-Policy, Cache-Zustand und Messsoftware;
- zehn Cold Runs, mindestens 1.000 Queries je Query-Klasse und mindestens 100
  inkrementelle Änderungen erfassen;
- Rohdaten für Index p50/p95, Peak RSS, DB-Größe, Progress-Stall, Query p95,
  Context p95, Freshness p95 und Full-/Incremental-Parität speichern;
- CPU-, Heap-, Allocation- und I/O-Profile für jeden verfehlten KPI erzeugen.

Gate: Ein unabhängiger Engineer kann die Baseline mit dokumentiertem Befehl und
gleichem Artefakt reproduzieren. Abweichungen und Messunsicherheit sind sichtbar.

### WP3 — Gold-Corpus und Accuracy Scoring

**Owner:** EVAL

**Zeitraum:** W2–10

Aufgaben:

- Ontologie für Symbol, Beziehung, Source Anchor, Ambiguität, Abstention und
  High-Confidence-Fehler vor der Annotation einfrieren;
- Annotation Guide mit Positiv-, Negativ- und Grenzfällen erstellen;
- aus mindestens fünf gepinnten Go-Repositories 1.000 Symbole und 2.000
  Beziehungen stratifiziert ziehen;
- mindestens 20 % blind doppelt annotieren und Konflikte adjudizieren;
- Cohen’s Kappa berechnen; bei `<0,85` Guide/Training korrigieren und die
  betroffene Stichprobe neu annotieren;
- 100 reale Coding Tasks versiegeln; Evaluationsdaten von der Implementierung
  trennen und Zugriff protokollieren;
- Scorer für Symbol/Edge Precision und Recall, Anchor Precision, Abstention,
  High-Confidence-Falschaussagen und Task Success implementieren;
- Thresholds vor dem ersten vollständigen Lauf im Repository versionieren.

Gate:

- Symbol Precision ≥98 %, Recall ≥95 %;
- Edge Precision ≥95 %, Recall ≥90 %;
- Source-Anchor Precision ≥99 %;
- korrekte Abstention ≥95 %;
- High-Confidence-Falschaussagen ≤1 %;
- mindestens 90/100 versiegelte Tasks erfolgreich;
- Kappa ≥0,85.

### WP4 — Gemessene Accuracy- und Performance-Fixes

**Owner:** ENG, Priorisierung durch EVAL

**Zeitraum:** W5–12

Arbeitsweise:

1. Fehler werden nach Nutzerwirkung, Häufigkeit und Confidence klassifiziert.
2. Jeder Fix beginnt mit einem Gold-/Regressionstest oder einem Profil.
3. Nur Go und Stable-Ops dürfen Produktionsänderungen treiben.
4. Nach jedem Batch laufen Accuracy, Performance, Parität und Recovery erneut.
5. Eine KPI-Verbesserung darf keine andere Mindestgrenze unterschreiten.

Performance-Gate:

- Cold Index p50 ≤90 s, p95 ≤120 s;
- Peak RSS ≤2 GB und kein OOM auf 8-GB-Host;
- DB ≤300 MB, Progress-Stall p95 ≤2 s;
- Warm Search p95 ≤100 ms;
- Caller/Callee/Impact p95 ≤200 ms;
- Agent Context p95 ≤500 ms;
- Freshness p95 ≤2 s;
- Full-/Incremental-Parität 100 %.

Exit: Zwei aufeinanderfolgende vollständige Läufe auf dem Candidate erfüllen
alle Accuracy- und Performance-Gates. Der zweite Lauf ist die Reproduktion, nicht
ein selektiv gewählter „bester“ Lauf.

### WP5 — Security

**Owner:** SEC

**Zeitraum:** W4–14

Aufgaben:

- Threat Model für Installation, Repo-Input, Parser, Graph Store, MCP stdio,
  Update/Release, lokale Dateigrenzen und Supply Chain erstellen;
- Datenklassifikation, lokale Speicherorte, Retention und Löschung dokumentieren;
- kein Network Listener im GA-Artefakt durch Binary-/Runtime-Test nachweisen;
- Root-/Path-Confinement- und Secret-Rejection-Suiten adversarial erweitern;
- alle Actions und erreichbaren Dependencies prüfen; erreichbare Findings
  priorisieren und dokumentieren;
- Security-Kontakt, Acknowledgement ≤2 Werktage, High-Fix ≤7 Tage und
  Medium-Fix ≤30 Tage als Prozess mit Übungsfall testen;
- externen Pentest bis W6 beauftragen, Scope und Artefakt-Digest vertraglich
  festhalten, Test in W9–10 durchführen und Retest bis W14 abschließen.

Gate: Null offene Critical-, High- oder in-scope Medium-Findings; Pfad- und
Secret-Suiten 100 % grün; Checksums, SBOM und Attestation für jedes Artefakt.

### WP6 — Reliability und 90-Tage-RC

**Owner:** SEC/DevOps

**Zeitraum:** Vorbereitung W6–14, Messfenster spätestens W14–27

Aufgaben:

- fokussierten RC aus dem grün geprüften Candidate veröffentlichen;
- Scheduled Gates mit unveränderlichen Rohlogs und First-Attempt-Status führen;
- Flake und echten Fehler getrennt erfassen; Reruns nie als First-Attempt-Pass
  umetikettieren;
- Restore-, Upgrade- und Rollback-Drill jeweils dreimal durchführen;
- Release-Reproduktion auf sauberen Runnern zehnmal durchführen;
- Installations-E2E auf jeder öffentlich behaupteten Plattform ausführen;
- Incidents spätestens nach 48 Stunden klassifizieren und auflösen;
- Mutable Assets verhindern; Release-Digest und Attestationsbezug prüfen.

Gate nach 90 aufeinanderfolgenden Tagen:

- Scheduled First-Attempt-Pass ≥99 %;
- Flake-Rerun <1 %;
- kein ungeklärtes Rot >48 h;
- Release-Reproduktion 10/10;
- Restore/Upgrade/Rollback jeweils 3/3;
- Recovery-/Crash-Fault-Suite 100 % grün.

### WP7 — Erweiterbarkeitstest

**Owner:** ENG, Durchführung durch 4 unabhängige Entwickler

**Zeitraum:** Vorbereitung W8–10, Test W11–13

Aufgaben:

- verblindete Parser-, Analyzer- und Consumer-Adapter-Aufgabe definieren;
- ausschließlich öffentliche Dokumentation und öffentliches SDK/Harness
  bereitstellen;
- Zeit, geänderte Produktionsdateien, Core-/Stable-Op-Änderungen und Gate-Status
  automatisiert erfassen;
- Aufgaben nicht durch interne Maintainer begleiten; nur generische
  Verständnisfragen protokollieren.

Gate: Mindestens 3/4 lösen alle Aufgaben; Parser ≤4 h, Analyzer ≤2 h, Adapter
≤4 h; höchstens zwei vorhandene Produktionsdateien je Aufgabe geändert; keine
Änderung an Stable-Ops; Conformance und Regression zu 100 % grün.

Die externe Plugin-Plattform bleibt außerhalb dieses Plans, solange nicht drei
Extension-Jobs und zwei verbindliche externe Provider-Entwickler belegt sind.

### WP8 — Product Discovery, Onboarding und Retention

**Owner:** PROD

**Zeitraum:** W1–24

Aufgaben:

- Research Protocol, Consent, Rekrutierungsfilter und Task-Skript einfrieren;
- 25 neue ICP-Nutzer unbeaufsichtigt durch die GA-Journey führen;
- Start/Ende, Hilfen, Fehler, erste belegte Antwort und SUS erfassen;
- Onboarding nur anhand beobachteter Abbrüche verbessern;
- mindestens 20 externe Organisationen mit eigenen privaten Repositories
  onboarden;
- vorab definieren, was ein „sinnvoller belegter Workflow“ ist;
- acht Wochen lang wöchentlich aktive Nutzung datensparsam und mit Zustimmung
  erfassen; alternativ signierte Nutzerprotokolle statt Produkttelemetrie;
- Task-Success gegen reale, vorher festgelegte Aufgaben messen.

Gate:

- ≥80 % schaffen Install → Index → erste zitierte Antwort ohne Hilfe;
- TTFV Median ≤5 min, p90 ≤15 min;
- SUS ≥80;
- mindestens 20 externe Organisationen;
- mindestens 12/20 in mindestens 6 von 8 Wochen aktiv;
- ≥90 % reale Tasks erfolgreich.

### WP9 — ICP, Piloten und Business-Evidenz

**Owner:** GTM

**Zeitraum:** W1–28

Kohortenplan:

- W1–3: 10 Interviews; Go-Sprachentscheidung genau einmal bestätigen/ändern;
- W2–6: weitere 20 Interviews, 5–10 Design-Partner qualifizieren;
- W4–8: standardisiertes Pilotangebot verkaufen;
- spätestens W8: erste fünf bezahlte Piloten gestartet;
- W8–20: 8–12 Wochen Pilotdurchführung mit wöchentlichem Outcome Review;
- W16–28: Renewals oder bezahlte Expansionen abschließen.

Pilotangebot:

- Preis mindestens 5.000 € für 8–12 Wochen;
- klarer Kernjob, Baseline, Zielmetriken, Datenschutzgrenze, Supportumfang,
  Abschlussentscheidung und Renewal-Kriterium;
- kein Custom Feature Development außerhalb des GA-Scope;
- Supportzeit je Organisation wöchentlich erfassen;
- Akquisekanal, Sales-Aufwand, direkte Kosten und Expansion separat erfassen.

Gate:

- 30 dokumentierte ICP-Interviews, davon ≥20 mit Kernjob als Top-3-Problem;
- Buyer und Budget Owner identifiziert;
- 5–10 aktive Design-Partner;
- mindestens fünf bezahlte Piloten;
- mindestens drei Renewals oder bezahlte Expansions;
- Support ≤4 h/Organisation/Woche;
- Contribution Margin ≥70 %;
- ein Kanal erzeugt drei bezahlte Piloten;
- modellierter CAC Payback ≤12 Monate.

### WP10 — Evidence Freeze und unabhängiges Audit

**Owner:** Program Lead; Prüfung durch AUDIT

**Zeitraum:** W24–32

Aufgaben:

- sechs unabhängige Reviewer spätestens W20 vertraglich sichern;
- Interessenkonflikte offenlegen; Reviewer je Dimension qualifizieren;
- Candidate-Digest, Quell-SHA, SBOM, Attestation, Benchmarks, Gold-Corpus-
  Methodik, Pentest, RC-Historie, Product- und Business-Evidenz einfrieren;
- jedem Reviewer dasselbe Artefakt und dasselbe Evidence Pack geben;
- Scores und Findings zuerst unabhängig erfassen, dann adjudizieren;
- nur High/Critical oder Score-blockierende Findings beheben;
- nach jeder Artefaktänderung alle betroffenen Gates und Reviewerprüfungen
  wiederholen.

Finales Gate: arithmetischer Mittelwert ≥9,0, keine Dimension <8,5, Security
und DevOps jeweils ≥9,0, kein High/Critical offen.

## 7. Wochenrhythmus und Steuerung

Jede Woche gibt es genau drei kurze Steuerungsereignisse:

1. **Montag — Gate Review:** Neue Evidenz, FAIL/UNKNOWN, Candidate-Änderungen,
   kritischer Pfad.
2. **Mittwoch — Product/Business Review:** Interviews, Onboarding, aktive Teams,
   Pilot-Pipeline, Support und Renewal-Risiken.
3. **Freitag — Evidence Freeze:** Rohdaten und Protokolle versionieren; keine
   nachträgliche manuelle Datenkorrektur ohne Audit Trail.

Dashboard-Spalten:

`Gate | Threshold | Current | PASS/FAIL/UNKNOWN | Evidence URI | SHA/Digest | Owner | Next action | Due date`

## 8. Die ersten zehn Arbeitstage

### Tag 1–2

- Program Owner und Rollen namentlich festlegen.
- PRD freigeben und alte Planungsautoritäten als superseded markieren.
- Candidate-Entscheidung für `e285822` treffen oder genau einen Nachfolge-SHA
  mit Begründung festlegen.
- externen Pentest und sechs Audit-Reviewer anfragen.

### Tag 3–4

- Claims-Inventar über README, Website, CLI-Hilfe und Release Notes erstellen.
- Evidence-Index und Gate-Dashboard anlegen.
- fünf reale Go-Repositories plus 10k-Datei-Stressziel auswählen und pinnen.
- Gold-Ontologie-Workshop durchführen.

### Tag 5

- Candidate-Binary reproduzierbar bauen, Digest/SBOM/Attestation sichern.
- ersten vollständigen Baseline-Lauf starten.
- Research Protocol und Interviewleitfaden freigeben.

### Tag 6–7

- erste fünf ICP-Interviews führen.
- erste Profile und Messlücken aus der Baseline klassifizieren.
- Annotation Guide pilotieren; 50 Symbole und 100 Beziehungen doppelt
  annotieren, bevor die große Annotation beginnt.

### Tag 8–9

- weitere fünf ICP-Interviews führen und Go einmalig bestätigen oder ersetzen.
- Claims-/Scope-Korrekturen abschließen.
- Pentest-Scope und Termin verbindlich machen.
- erste fünf Design-Partner für Onboarding und Pilot ansprechen.

### Tag 10 — M0 Go/No-Go

M0 ist nur grün, wenn Candidate, GA-Scope, Claims, Ontologie, Messvertrag,
Research Protocol, Evidence-Index und externe Termine feststehen. Andernfalls
wird kein Feature- oder Optimierungs-Backlog geöffnet; nur M0-Lücken werden
geschlossen.

## 9. Stop- und Eskalationsregeln

- Accuracy-Gate verfehlt: Sprache bleibt/werden Preview; Threshold nie senken.
- Peak RSS >4 GB: alle Scale-Claims stoppen.
- High/Critical aus Pentest: Release und Audit stoppen, Finding schließen und
  betroffene Evidenz neu erzeugen.
- Neue GA-Surface: Security- und Reliability-Evidenz gilt als zurückgesetzt.
- Pilot-Kohorte 1 scheitert: Ursachenhypothese und Angebot einmal überarbeiten.
- Zwei Partnerkohorten scheitern: kommerzielle Expansion stoppen und
  Produktthese neu entscheiden.
- Kein bezahlter Pilot: kein SaaS, Billing, SSO oder RBAC bauen.
- <12 retained Teams oder <3 Renewals: kein 9/10-Audit deklarieren.
- Kein reproduzierbarer A/B-Test: keinen Graphify-Vergleich veröffentlichen.

## 10. P50-/P80-Entscheidungspunkte

| Woche | Entscheidung | Bei FAIL |
|---:|---|---|
| 2 | M0 Scope/Candidate/Claims grün? | Programm bleibt in M0; keine Featurearbeit |
| 3 | 10 Interviews bestätigen Go und Kernjob? | Go einmalig wechseln oder These stoppen |
| 6 | Baseline reproduzierbar, 5+ Design-Partner in Pipeline? | Messvertrag/Pipeline reparieren; P80 markieren |
| 8 | fünf bezahlte Piloten gestartet? | 32-Wochen-Ziel gefährdet; Angebot/Kanal einmal korrigieren |
| 12 | Accuracy und Performance grün? | nur belegte Blocker-Fixes; Audit-Termin neu bewerten |
| 14 | Pentest sauber und RC-Uhr gestartet? | Audit frühestens 90 Tage nach neuem RC-Start |
| 20 | Product-Retention-Pfad und Renewals realistisch? | keine Expansion; P80 oder Stop |
| 28 | alle sechs Dimensionen intern evidence-ready? | kein Audit starten; fehlende Dimension schließen |
| 32 | Audit erfüllt Definition of Done? | Findings im P80-Puffer schließen und gezielt re-auditieren |

## 11. Definition „fertig“ für jedes Arbeitspaket

Ein Arbeitspaket ist nur fertig, wenn:

- das Ergebnis am Candidate-SHA oder Release-Digest hängt;
- Rohdaten und Methode versioniert sind;
- ein unabhängiger Reproduktions- oder Review-Schritt stattgefunden hat;
- der PRD-Threshold automatisch oder nachvollziehbar berechnet wurde;
- FAIL und UNKNOWN sichtbar bleiben;
- Owner, Datum und Sign-off dokumentiert sind;
- eine Änderung am Candidate die betroffenen Evidenzen automatisch als stale
  markiert.

## 12. Ausdrücklich nicht Teil dieses Plans

- Graphify-Benchmark vor grünem M2;
- Extension-v1 ohne bestätigte Nachfrage;
- neue GA-Sprachen oder Surfaces;
- SaaS-, Billing-, SSO- oder RBAC-Implementierung;
- horizontaler Ausbau der vorhandenen Labs-Features;
- Optimierung ohne Profil;
- Marketingclaims ohne reproduzierbaren Beleg.

Diese Auslassungen sind keine späteren Backlog-Prioritäten, sondern schützen den
kritischen Pfad zur 9/10.
