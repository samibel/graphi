# Produkt-Brief & Agent-Pipeline: Graphi „Real-World-Ready" (Juli 2026)

> **Rolle dieses Dokuments.** Dies ist der Orchestrierungs-Brief, der eine
> Pipeline von Coding-Agenten startet. Es beantwortet drei Fragen:
> (1) *Warum* diese Arbeit das Produkt erfolgreich macht (Produkt-Brief),
> (2) ein *kritisches Review* des bestehenden technischen Plans, und
> (3) *wie* die Arbeit in agent-große, unabhängig verifizierbare Pakete
> zerlegt wird (die Pipeline). Jedes Work Package ist so geschnitten, dass es
> ein einzelner Agent mit einem klaren Input-/Output-Contract und einem
> messbaren Gate abschließen kann.

Ausgangslage: graphi ist bei **v0.4.0**, die interne Release-Scorecard steht
auf **100/100**, alle Surfaces sind byte-paritätisch, und der Local-First-
Vertrag ist CI-erzwungen. Das Produkt ist technisch reif — auf *synthetischen*
Fixtures. Zwei unabhängige externe Feldtests (Spring-Boot-Monorepo, vuln-go)
haben aber genau die Lücke zwischen „grün auf Fixtures" und „nützlich auf echten
Repos" freigelegt. Diese Lücke zu schließen ist der einzige Schritt, der jetzt
zählt.

---

## Teil 1 — Produkt-Brief: die Erfolgswette

### Was graphi als Open-Source-Produkt erfolgreich macht

Für ein lokales, agent-first Code-Intelligence-Tool entscheidet **nicht** die
Feature-Zahl über Adoption — die ist bereits überdurchschnittlich groß. Es
entscheiden zwei Dinge, und beide sind aktuell gebrochen:

1. **Vertrauen (Trust).** graphi verkauft „provenance-backed, fail-closed,
   nie fabrizierte Auflösung". Genau deshalb ist der vuln-go-Befund
   **existenziell**: `analyze taint` fand **0 von 4** realen Injektionen und
   meldete `solved: true, flows: []` — also „geprüft, sauber" für eine
   nachweislich verwundbare App. Ein Security-Signal, das mit voller
   Zuversicht *falsch* schweigt, ist schlimmer als kein Signal. Der erste
   Nutzer, der das bemerkt, schreibt keinen Bug-Report, sondern einen Blogpost.

2. **Time-to-Value auf dem *eigenen* Repo.** Der erste Eindruck ist `cd my-repo
   && graphi`. Auf dem Spring-Boot-Monorepo bedeutete das: **4m48s**, **2,3 GB**
   DB, minutenlange stille Link-Phase, `diagnose` unbrauchbar verrauscht. Das
   ist der klassische „einmal installiert, wieder deinstalliert"-Moment. Kein
   Feature-Set rettet einen kaputten ersten Lauf.

**Die Wette:** Wenn graphi auf einem echten Monorepo in **< 90s** indexiert,
unter **300 MB** bleibt, sichtbaren Fortschritt zeigt und `analyze taint` **4/4**
echte Flows mit Präzision ≥ 0,8 findet — dann kippt das Produkt von „beeindruckende
Demo" zu „Tool, das ich meinem Team zeige". Das ist ein diskreter, messbarer
Sprung, kein vages „besser machen".

### Warum diese Wette *sehr wahrscheinlich* gewinnt

Der übliche Grund, warum solche Vorhaben scheitern — „wir wissen nicht, was
kaputt ist" — trifft hier **nicht** zu:

- **Root Causes sind datei- und zeilengenau verifiziert.** Der Plan in
  `docs/plan/2026-07-external-findings-remediation.md` benennt jede Ursache mit
  `datei.go:zeile`. Es ist keine Diagnose-Arbeit mehr offen, nur noch Umsetzung.
- **Ein Architektur-Muster löst mehrere Probleme.** Die „Internierung externer
  Welt als markierte Knoten zweiter Klasse" repariert Taint, entstört Diagnose
  *und* liefert das Package-Konzept, das die Edge-Explosion beendet. Ein Konzept,
  drei Payoffs — das ist eine effiziente, risikoarme Wette.
- **Messbare, CI-gegatete Akzeptanzkriterien existieren schon** (Tabelle unten).
  Erfolg ist nicht Meinung, sondern ein grüner Gate.
- **Die Fail-Closed-Philosophie bleibt erhalten.** Externe Knoten sind immer
  `heuristic`-Tier mit Provenance, nie `confirmed`. Der Fix verletzt nicht den
  Markenkern, der die im ersten Test *gelobte* Präzision ausmacht.

### Was Erfolg konkret heißt (die Definition, gegen die die Pipeline gebaut wird)

| Metrik | Ist | Ziel (Gate) |
|---|---|---|
| Taint-Recall vuln-go-Fixture (E2E) | 0/4 | **4/4, Präzision ≥ 0,8** |
| Spring-Boot Edges | 4,27 Mio. | **< 500k** |
| Spring-Boot DB | 2,3 GB | **< 300 MB** |
| Spring-Boot Vollindex | 4m48s | **< 90s** |
| Link-Phase ohne Progress-Event | Minuten | **< 2s zwischen Events** |
| dead_symbol-FPs (@Test/@Bean/main) | „sehr viele" | **0 als warning** |
| unresolved_reference-Diagnosen | O(Edges) | **1 pro externem Ziel** |

---

## Teil 2 — Review des bestehenden Plans

Der Remediation-Plan (`2026-07-external-findings-remediation.md`) ist technisch
**stark**: korrekte Priorisierung (Track B entstört alles andere zuerst),
sauberes Sequencing über M1–M5, gebündelte Migrationen (ein Warm-Start-Stamp-Bump
statt fünf), und ehrliche Risiken. Er ist als *technischer* Plan
umsetzungsreif. Als *Produkt*-Plan hat er vier Lücken, die die Pipeline
schließen muss — sonst ist die Arbeit korrekt und trotzdem wirkungslos:

1. **Gates zuerst, nicht zuletzt.** Der Plan listet das Bench-Fixture (B5) und
   das E2E-Recall-Fixture (A4) *innerhalb* ihrer Tracks. Als OSS-Praxis ist das
   riskant: ohne die Fixtures *vor* dem Fix kann kein Agent seinen eigenen
   Fortschritt beweisen, und Reviewer müssen Zahlen glauben statt sie zu sehen.
   → **Pipeline-Korrektur: Phase 0 baut alle Mess-Fixtures und roten Gates,
   bevor irgendein Fix beginnt.** Jeder spätere Agent hat damit ein objektives
   „fertig".

2. **Der Beweis muss öffentlich sein.** Der Plan repariert die Findings, aber
   plant nicht, sie zu *zeigen*. Für ein Trust-Produkt ist das die halbe Miete:
   die zwei Feldtest-Ergebnisse offen dokumentieren (vorher/nachher, reproduzierbar)
   ist der stärkste Adoptions-Hebel, den ein OSS-Projekt hat. „Ein externer Test
   fand 0/4 — hier ist der Commit, der es auf 4/4 bringt, mit reproduzierbarem
   Fixture" ist Vertrauen, das man nicht kaufen kann.
   → **Pipeline-Ergänzung: WP-13 „Real-World Report Card".**

3. **Regressionsschutz für das Gelobte.** Der erste Test *lobte* die Präzision
   der Struktur-Provenance. Der External-Node-Fix (A1) könnte genau die
   verwässern (callers/impact ziehen plötzlich externe Knoten rein). Der Plan
   erwähnt Default-Exklusion, aber kein hartes Regressions-Gate.
   → **Pipeline-Härtung: Konformanz-Snapshot der callers/impact-Ergebnisse
   vor/nach als eigenes Gate (in WP-05 verankert).**

4. **Adversariales Review als Pipeline-Stufe.** „solved: true, flows: []" ist
   genau die Klasse Bug, die ein Autor nicht sieht — ehrlich falsch. Ein
   zweiter, gegnerisch gestimmter Agent pro Paket („versuche, dieses Gate mit
   einem realistischen Input zu brechen") ist billiger als der nächste Blogpost.
   → **Pipeline-Struktur: jedes WP durchläuft find → build → adversarial verify.**

**Fazit des Reviews:** Der technische Plan ist gut genug, um ihn zu übernehmen —
die Pipeline übernimmt seine Meilensteine M1–M5 unverändert und legt vier
Produkt-Schichten darüber (Gate-first, öffentlicher Beweis, Regressionsschutz,
adversariales Verify).

---

## Teil 3 — Die Agent-Pipeline

### Betriebsregeln für *jeden* Agenten (repo-spezifisches „Definition of Done")

Jedes Work Package gilt erst als fertig, wenn **alle** Punkte grün sind — das
sind die im Repo bereits CI-erzwungenen Invarianten:

- `CGO_ENABLED=0 go build ./...` und `CGO_ENABLED=0 go test ./...` grün.
- Local-First unverletzt: Egress-Canary + CGo-Conformance-Gate grün, kein
  Non-Loopback-Dial (`readme.md#the-local-first-contract`).
- Layer-Richtung `cmd → surfaces → engine → core` eingehalten
  (`layer-direction`-Guard).
- Docs-vs-Code-Parität: bei neuem Node-Kind / Analyzer / MCP-Tool die
  `docs/coverage-matrix.{md,yaml}` mitziehen, sonst bricht der Build.
- Determinismus + byte-identische full-vs-incremental Ingest bleiben grün.
- Bei Schema-/Node-Kind-Änderung: Warm-Start-Semantics-Stamp bumpen
  (`warmstart.go`), Migration im CHANGELOG ankündigen.
- Diff minimal, im Stil der Umgebung; `gofmt`/`go vet` sauber.

### Rollen in der Pipeline

- **Orchestrator** (1×, das ist der Einstiegs-Agent aus diesem Brief): löst
  Abhängigkeiten auf, startet WPs in der erlaubten Reihenfolge, sammelt Gate-
  Ergebnisse, blockt Merge bei rotem Gate.
- **Builder** (1× pro WP): implementiert das Paket gegen seinen Output-Contract.
- **Adversarial Verifier** (1× pro WP): versucht, das Gate mit realistischem
  Input zu brechen, *bevor* das Paket als fertig gilt. Default-Haltung:
  „nicht bestanden, bis das Gegenteil bewiesen ist."

### Phasen, Pakete, Contracts

Reihenfolge folgt den Meilensteinen des technischen Plans, mit vorgezogener
Phase 0. `[∥]` = parallel startbar, sobald Abhängigkeiten grün sind.

---

#### Phase 0 — Mess-Infrastruktur zuerst (entsperrt alles)

**WP-00 · Real-World-Fixtures & rote Gates** `[∥]`
- **Ziel:** Alle Zielmetriken messbar machen, *bevor* gefixt wird. Die Gates
  sind zunächst **rot** — das ist gewollt.
- **Enthält:**
  - vuln-go-artiges **echtes** `.go`-Fixture ins Corpus (HTTP-Handler → eigener
    Wrapper → `exec.Command`/`db.Query`; 2× SQLi, 1× RCE, 1× LFI) **plus**
    sanitisierte Negativ-Pfade (parametrisierte Query, `strconv.Atoi`-validiert),
    auf denen **kein** Finding erlaubt ist. Muster: `engine/ingest/*_e2e_test.go`.
  - Spring-Boot-artiges Bench-Fixture in `bench/` mit repetitiven Package-Namen
    (`service`/`model`/`impl`/`dto`), das die Edge-Explosion reproduziert.
  - CI-Budgets: Edges/Node-Ratio, DB-Bytes/Edge, Link-Dauer, Taint-Recall/Präzision.
- **Output-Contract:** grün lauffähige Test-/Bench-Targets, die aktuell die
  Ist-Werte (0/4, 4,27 Mio., 2,3 GB, 4m48s) **rot** melden.
- **Gate:** die Fixtures existieren, laufen deterministisch, und der Report zeigt
  exakt die sieben Metriken aus der Tabelle.
- **Abhängigkeiten:** keine. **Startet sofort.**

---

#### Phase 1 — M1: Edge-Explosion & Observability (entstört alles andere)

**WP-01 · Package-Knoten statt Datei→Datei-Fan-out** `[∥ nach WP-00]`
- **Ziel:** Ein Import = **eine** Kante `file →imports→ package`-Knoten (neuer
  Node-Kind `package`, interniert pro voll-qualifiziertem Package-Pfad), statt N
  Kanten auf jede namensgleiche Datei.
- **Kern:** Clause-Key auf **vollen** Package-Pfad umstellen
  (`resolve_common.go:23-29`); Java/Kotlin-Parser die `package`-Deklaration
  extrahieren lassen (`parser_java.go`) statt Verzeichnis-Basename zu raten.
- **Gate (aus WP-00):** Spring-Boot-Fixture Edges **< 500k**; callers/impact-
  Konformanz-Snapshot unverändert; Warm-Start-Stamp gebumpt.
- **Abhängigkeiten:** WP-00.

**WP-02 · Link-Phase beobachtbar & inkrementell ehrlich** `[∥ nach WP-00]`
- **Ziel:** Kein stiller Minuten-Block mehr in der Link-Phase.
- **Kern:** Progress-Events *innerhalb* `linkFiles` (`ingest.go:1690-1769`),
  `ProgressEvent.Total` für Link befüllen; Riesen-Transaktion in ~50k-Edge-Chunks
  schneiden **im Lockstep mit der Ingest-Meta-Transaktion** (pro Chunk ein
  Meta-Checkpoint; Edge-Writes idempotent/replaybar); Stale-Edge-Sweep über
  `edges_from_id`-Index gezielt statt Full-Read (`ingest.go:1701,1707`);
  `receiverMethod` Reverse-Index `methodName → dirs` beim `BuildIndex`
  (`index.go:160-183`).
- **Gate:** **< 2s** zwischen Progress-Events auf dem Bench-Fixture; abgebrochener
  Link-Lauf ist selbstheilend nachziehbar (kein Divergieren).
- **Abhängigkeiten:** WP-00. **Parallel zu WP-01** (unabhängige Dateien; bei
  Konflikt gewinnt WP-01 den `resolve_*`-Bereich, WP-02 den `ingest`-Progress-Bereich).

---

#### Phase 2 — M2: Taint reparieren (der eigentliche Blocker)

**WP-03 · Internierte External-Nodes + heuristic `calls`-Edges (Go zuerst)**
- **Ziel:** Neuer Node-Kind `external`, interniert (**ein** Knoten pro
  eindeutigem QN wie `os/exec.Command`, nicht pro Callsite → Wachstum O(unique)).
- **Kern:** An den drei Drop-Punkten (`resolve_go.go:86-116`) statt
  `st.Skipped++` einen External-Node minten + `calls`-Edge im `heuristic`-Tier
  (`aliasToPath` liegt zur Link-Zeit bereits vor); `link.Stats` nicht mehr
  verwerfen (`ingest.go:1757`), im `PhaseDone`-Summary ausgeben.
- **Query-Hygiene (Regressionsschutz):** External-Nodes **default-ausgeschlossen**
  in callers/impact/neighborhood, opt-in per Flag; Taint/Contracts opten ein.
  (`contracts/registry.go:224,244` erwartet solche Knoten bereits.)
- **Gate:** callers/impact-Konformanz-Snapshot **identisch** zu vor WP-03
  (die gelobte Struktur-Präzision bleibt); `matchSink` (`config.go:205-206`)
  zeigt jetzt auf existierende Knoten.
- **Abhängigkeiten:** WP-01 (Internierungs-Muster + volle Package-Pfade).

**WP-04 · Ehrliche Verdicts statt falscher Sicherheit** `[∥ nach WP-03-Design]`
- **Ziel:** Nie wieder „geprüft, sauber", wenn der Graph gar keine Sinks
  enthalten *kann*.
- **Kern:** Neues Verdict `no_sink_candidates` (statt `empty`), wenn null
  External-Nodes / null klassifizierte Sinks — mit Handlungshinweis.
- **Gate:** Auf einem Graph ohne Sinks liefert `analyze taint` `no_sink_candidates`,
  nicht `empty`.
- **Abhängigkeiten:** WP-03 (Node-Kind existiert).

**WP-05 · E2E-Recall-Gate scharf schalten**
- **Ziel:** Das rote Gate aus WP-00 auf **grün** bringen — der Test, der die
  Lücke von Anfang an gefangen hätte.
- **Kern:** ingest → link → `analyze taint` → **4/4** Flows asserted; die
  Negativ-Pfade melden **kein** Finding (Präzision ≥ 0,8). Wrapper-Source
  (`.URL.Query` als External-Node → `GetQueryParam` propagiert über
  `solve.go:213-232` ohne weitere Änderung).
- **Gate:** vuln-go-Fixture **4/4 Recall, 0 False Positives auf Negativ-Pfaden**.
- **Abhängigkeiten:** WP-03, WP-04. **Dies ist der Meilenstein-Beweis der ganzen Wette.**

---

#### Phase 3 — M3: Storage & Monorepo-Defaults (Migration bündeln)

**WP-06 · Storage-Diät** `[∥ nach WP-01]`
- **Kern:** FTS5 nur noch für Nodes, nicht für Edge-`reason`-Boilerplate
  (`sqlite.go:446-453`); `reason` internieren (Dictionary-Tabelle/Template statt
  freiem String); `evidence` für High-Volume-heuristic-Edges auf `line`-Spalte
  reduzieren.
- **Gate:** Spring-Boot-Fixture DB **< 300 MB** (nach WP-01 vermutlich schon < 500 MB).
- **Abhängigkeiten:** WP-01. **Migration mit WP-01 in *einen* Stamp-Bump bündeln.**

**WP-07 · Sinnvolle Monorepo-Defaults** `[∥]`
- **Kern:** kleine eingebaute Default-Denylist für Build-Output (`target/`,
  `build/`, `.gradle/`, `node_modules/`, `dist/`) **default-on mit Opt-out**;
  `GRAPHI_RESPECT_GITIGNORE` bleibt opt-in.
- **Gate:** Spring-Boot-Fixture indexiert keinen Generator-Output; Watcher stimmt
  mit dem Walk überein.
- **Abhängigkeiten:** WP-00.

**WP-08 · Bench-Gate in CI verankern**
- **Kern:** die WP-00-Budgets als harten CI-Gate scharf schalten
  (Edges/Node-Ratio, DB-Bytes/Edge, Link-Dauer). Vollindex-Ziel **< 90s**.
- **Gate:** CI schlägt fehl, wenn ein Budget gerissen wird.
- **Abhängigkeiten:** WP-01, WP-02, WP-06, WP-07.

---

#### Phase 4 — M4/M5: Config, Meta-Modell, Diagnose entlärmen

**WP-09 · Taint-Config-Loader** `[∥ nach WP-05]`
- **Kern:** `LoadTaintConfig(path)` für projektspezifische Sources/Sinks/
  Sanitizer (Structs sind bereits JSON-getaggt, `config.go:13-62`); Suchpfad
  `.graphi/taint.json`, Merge über Defaults, Anschluss in `dispatch.go:72,119`.
- **Gate:** ein Custom-Sink aus `.graphi/taint.json` erzeugt ein Finding, das
  ohne Config fehlt.
- **Abhängigkeiten:** WP-05.

**WP-10 · Annotations/Modifier ins Modell (gemeinsames Fundament)**
- **Kern:** kompaktes `Meta`-Feld an `core/model/node.go` (Annotations-Liste +
  Flags `static`/`main`/`test-path`); den `modifiers`-Kindknoten in
  `parser_java.go:115-142` lesen; durchfädeln über `addDef`
  (`parser_tswalk.go:73-89`), `nodeSpecs`, `MapTreeSitter`.
- **Gate:** Java-`@Test`/`@Bean`/`@Component` erscheinen als Meta am Node;
  full-vs-incremental byte-identisch.
- **Abhängigkeiten:** WP-01 (Parser-Bereich stabil).

**WP-11 · Entry-Point-aware dead_symbol + safe_delete-Gate**
- **Kern:** Exclusion-Prädikat an `analyze.go:181-184` (Entry-Point-Annotationen,
  `main`-Signatur, Test-Konvention); optional Severity-Downgrade auf `info`
  (`entrypoint_candidate`) statt Binär-Skip. **Dieselbe Liste als Gate in
  `safe_delete.go:87-136`** — heute würde es einen lebenden Spring-Bean löschen
  (Korrektheits-, kein Komfort-Fix).
- **Gate:** dead_symbol-FPs auf @Test/@Bean/main = **0 als warning**; safe_delete
  verweigert einen annotierten Bean.
- **Abhängigkeiten:** WP-10.

**WP-12 · Diagnose-Suppression & Aggregation** `[∥ nach WP-11]`
- **Kern:** `Diagnose(ctx, reader, kinds)` um Options-Struct (Pfad-Excludes,
  Severity-Floor, Annotations-Allowlist), CLI-Flags (`cli.go:275-294`), Config
  `.graphi/diagnostics.json`; `unresolved_reference` aggregieren: **eine**
  Diagnose pro externem Ziel mit Zähler statt einer pro Edge (`analyze.go:132-158`).
- **Gate:** unresolved_reference = **1 pro externem Ziel** auf dem Bench-Fixture.
- **Abhängigkeiten:** WP-01, WP-03, WP-11.

---

#### Phase 5 — Produkt-Beweis (der OSS-Hebel)

**WP-13 · Real-World Report Card** `[nach WP-05, WP-08]`
- **Ziel:** Den Fix öffentlich und reproduzierbar machen — der stärkste
  Adoptions-Hebel.
- **Kern:** Doc-Seite (z. B. `docs/real-world-report.md`) mit den zwei
  Feldtest-Ergebnissen vorher/nachher, verlinkt auf die reproduzierbaren
  Fixtures/Bench-Targets; die sieben Zielmetriken als Vorher/Nachher-Tabelle;
  CHANGELOG-Eintrag; Landing-Page-Verweis im `site/`-Benchmarks-Abschnitt.
- **Gate:** Jede Zahl im Report ist per eingecheckt em Fixture reproduzierbar
  (kein hand-gepflegter Wert).
- **Abhängigkeiten:** WP-05 (Taint 4/4), WP-08 (Perf-Budgets grün).

**WP-14 · A1-Rollout Java/Python/TS** `[nach WP-05]`
- **Kern:** External-Node-Materialisierung über `resolve_common.go` auf die
  weiteren Sprachen ausrollen (nach WP-01 haben Java-Imports volle Package-Pfade,
  externe FQNs wie `org.springframework….RestTemplate.exchange` sind sauber baubar).
- **Gate:** je Sprache ein E2E-Recall-Fixture analog WP-00 grün.
- **Abhängigkeiten:** WP-05, WP-10.

---

### Abhängigkeits-Übersicht

```
Phase 0:  WP-00 ──────────────────────────────────────────────┐
                                                               │
Phase 1:  WP-01 ─┬─ WP-02            (M1, parallel)            │
                 │                                             │
Phase 2:  WP-03 ─┴─ WP-04 ── WP-05   (M2 · Taint-Beweis)       │
                                                               │
Phase 3:  WP-06 · WP-07 ── WP-08     (M3 · Storage/Perf)       │
                                                               │
Phase 4:  WP-09 · WP-10 ─ WP-11 ─ WP-12   (M4/M5 · Diagnose)   │
                                                               │
Phase 5:  WP-13 (Report Card) · WP-14 (Sprach-Rollout) ◀───────┘
```

Kritischer Pfad zum Produkt-Sprung: **WP-00 → WP-01 → WP-03 → WP-05**
(Taint 4/4) und **WP-00 → WP-01 → WP-06 → WP-08** (Perf-Budgets). Beide enden in
**WP-13**, dem öffentlichen Beweis.

---

## Teil 4 — Warum diese Pipeline „auf jeden Fall oder sehr wahrscheinlich" liefert

1. **Gate-first:** Kein Fix startet ohne ein rotes Gate, das ihn misst (WP-00).
   Fortschritt ist beobachtbar, nicht behauptet.
2. **Verifizierte Root Causes:** Jedes WP referenziert `datei.go:zeile` aus dem
   Remediation-Plan — es ist Umsetzung, keine Forschung.
3. **Ein Muster, mehrere Payoffs:** Internierung (WP-01/WP-03) repariert Perf,
   Taint und Diagnose zugleich — wenig Code-Risiko, große Wirkung.
4. **Regressionsschutz eingebaut:** Der callers/impact-Konformanz-Snapshot
   (WP-03) schützt genau die Stärke, die der erste Test lobte.
5. **Adversariales Verify pro Paket:** Die „ehrlich falsch"-Bug-Klasse
   (solved:true, flows:[]) wird strukturell abgefangen, nicht dem Autor überlassen.
6. **Fail-Closed bleibt Fail-Closed:** Externe Knoten sind immer `heuristic` mit
   Provenance — der Markenkern wird nicht verwässert.
7. **Der Erfolg ist öffentlich (WP-13):** „Ein externer Test fand 0/4 — hier ist
   der reproduzierbare Commit auf 4/4" ist der glaubwürdigste OSS-Trust-Beweis,
   den es gibt.

**Einziges verbleibendes Substanzrisiko** ist die tatsächliche Edge-Reduktion
durch WP-01 auf realen Java-Wildcard-Imports — deshalb misst WP-00 sie
*zuerst*, und die Kappung-pro-Datei (Budget, geloggt statt still — „No silent
caps") ist der Fallback. Alle anderen Risiken sind durch Bündelung von
Migrationen und Default-Exklusion adressiert.

---

*Nächster Schritt für den Orchestrator: WP-00 starten (keine Abhängigkeiten),
dann WP-01 ∥ WP-02, sobald die roten Gates stehen. Merge eines WP nur bei
grünem Gate **und** bestandenem Adversarial-Verify.*
