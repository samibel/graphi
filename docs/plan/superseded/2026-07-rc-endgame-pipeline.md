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

# RC-Endgame-Pipeline: die letzten vier Pakete bis zum Focused Core RC

> **Historical execution snapshot.** Die Status-/UNKNOWN-Angaben unten stammen
> vom damaligen Branch und sind nicht der aktuelle Contract of Record. Für den
> implementierten Stand und die heute offenen Performance-UNKNOWNs gelten ADR
> 0002, ADR 0003 und `docs/eval/hero-protocol.md`.
>
> **Status:** EXECUTED — Stages 1–3 umgesetzt (`RUN-01` @ `5b9eeb5`, `EVAL-01`
> @ `660b5a3`, `EVAL-02` @ `ecef54f`); Stage 4 entscheidungsreif, siehe das
> RC-Dossier `docs/rc/focused-core-rc.md` (Go/No-Go: Sami). Stage 0 (Review &
> Merge des Stapels) steht aus.
> **Stand:** 15. Juli 2026, Branch `claude/plan-feedback-f5khoi`
> **Autorität:** `temp/plan/00_master_execution_plan.md` (WBS-IDs, Gates); dieses Dokument
> ordnet nur die Ausführung der Restpakete, es vergibt keine neuen IDs.

## Ausgangslage

12 von 16 Master-Paketen sind umgesetzt (SW-109…SW-120). Gates **G0, G1, G2 sind
vollständig grün**; die Verträge für die Restarbeit sind eingefroren:

- ADR 0002 (Session/Profile) — der Bauplan für `RUN-01`, inkl. red-now Journey-Test.
- ADR 0003 (Selective Read) — U2/U4/U5 warten auf EVAL-Messdaten.
- ADR 0004 (Recovery-Disposition) — Restscope „Recovery bei read-only Session-Open" an `RUN-01` zugewiesen.
- ADR 0005 (Release-DAG) — U1 (Action-Pins) und U2 (Environment Protection) offen.

Offen: `RUN-01` (→ G3), `EVAL-01` + `EVAL-02` (→ G4), `RC-01` (Go/No-Go + Lock lösen).
Dazu die **Review-Schuld**: acht ungereviewte Stories auf einem Branch.

## Die Pipeline

```text
Stage 0  Review & Merge (Sami)          ── blockiert alles Weitere
   │
   ├─ Stage 1  RUN-01  (Engineering)    ─┐   parallel, disjunkte Dateien
   └─ Stage 2  EVAL-01 (Engineering)    ─┤
                                         ▼
      Stage 3  EVAL-02 (CI-Läufe + Auswertung)
                                         ▼
      Stage 4  RC-01   (Evidenz + Go/No-Go + Lock)
                                         ▼
      G5: Design-Partner (Sami, non-engineering — läuft ab sofort parallel)
```

Arbeitsweise unverändert: ein Paket = eine Story = ein PR mit story-atomaren
Commits; jede Stufe hält alle Repo-Gates grün (testgate, gofmt, vet, layerguard,
`coverage -check`); expected-red-Pins werden umgedreht, nie gelöscht.

### Stage 0 — Review & Merge des Achterstapels (Sami; blockierend)

- PR aus `claude/plan-feedback-f5khoi` gegen `main`; die acht Commits sind in
  Commit-Reihenfolge reviewbar (SAFE-01 → SP-11 → CORE-01 → CORE-02 → CAP-01 →
  ING-DEC → PRIV-01 → REL-01), jede Message trägt die Abnahmekriterien.
- CI läuft auf dem PR — inklusive des neuen `release-dag`-Gate-Jobs (Publish
  bleibt durch den Lock tot).
- **Warum blockierend:** `RUN-01` schneidet quer durch `cmd/graphi/main.go`;
  Review-Findings danach einzuarbeiten wäre doppelt teuer. Nach dem Merge wird
  der Arbeitsbranch von `origin/main` neu aufgesetzt (gleicher Name).

### Stage 1 — `RUN-01`: Composition Root + G3 (P50/P80: 8/12 PT)

Implementiert exakt gegen ADR 0002, in drei Strangler-Slices:

1. **`cmd/internal/runtime.Runtime`**: besitzt Store, Meta, Ingester, Session
   und Services genau einmal; Profile Stable/Labs; Pfade über `internal/state`
   (D1/D2); **Session-Open = open → `RecoverWithRoot` → ready** (schließt den
   ADR-0004-Restscope); idempotenter, einmaliger Close (D5).
2. **MCP-stdio auf Runtime**: `runMCP` löst das Repo per D4-Präzedenz
   (Flag → MCP-Roots → cwd), Initial-Ingest sync-before-serve als U1-Default,
   typisiertes „no repository bound". U2 (Roots-Handshake realer Clients) wird
   mit synthetischen `initialize`-Fixtures implementiert; die Capture-Aufgabe
   gegen echte Clients (Claude Code/Desktop) ist ein dokumentierter Handgriff
   für Sami und justiert nur das Mapping, nicht die Architektur.
3. **CLI-Verben auf Runtime**: die `resolveSession`-Aufrufer beziehen ihren
   Client aus der Runtime; duplizierte `makeClient`-Verdrahtung fällt, sobald
   der letzte Stable-Aufrufer migriert ist. Der Daemon bleibt Labs, aber sein
   `select {}` (main.go:1145) weicht dem Runtime-`Done()`-Pfad — `daemon stop`
   beendet den echten Prozess (ADR 0002 weist das RUN-01 zu).

**Exit (G3):** der geskippte `mcp_session_journey_subprocess_test` wird scharf
geschaltet und ist grün (Setup → initialize → list → call auf echtem Fixture-
Repo, ohne `-db`-Handarbeit); neuer CLI-Subprocess-E2E (Start → Ready → Query →
Ende); U1-Latenzmessung auf einem mittelgroßen Repo entscheidet sync vs. async
dokumentiert; alle SW-110-Journeys bleiben byte-grün.

### Stage 2 — `EVAL-01`: 20 Hero-Aufgaben + 3 gepinnte Repos (5/8 PT; parallel zu Stage 1)

Baut ausschließlich auf Vorhandenem auf — kein neues Framework:

- **Aufgaben:** 20 versionierte Hero-Aufgaben über die 12 Stable-Ops in
  `engine/scenario`-Form (Source-Anker, erwartete Evidenz, explizite
  Ambiguitäts-/Partial-/Empty-Fälle — mindestens je eine pro Fehlerklasse).
- **Repos:** 3 der 5 bereits gepinnten Corpus-Repos (`corpus/manifest.json`,
  SHA-gepinnt, fail-closed bei Tag-Re-Point) — Auswahl: ein großes Java-/
  JVM-Repo ergänzen (Master verlangt ein Monorepo; Manifest-Erweiterung ist
  eine Datenänderung) plus zwei bestehende anderssprachige.
- **Runnerklasse + Budgets:** `ubuntu-latest` dokumentiert; absolute Budgets
  bleiben leer bis zum ersten reproduzierbaren Lauf (ADR 0003 U5 — keine
  erfundenen Zahlen), das Schema hat aber ab Tag eins Felder dafür.

**Exit:** Aufgaben, Anker, Runnerklasse und Budget-Schema versioniert im Repo;
der Runner läuft lokal gegen das Go-Fixture (Smoke), noch nicht gegen die
großen Repos.

### Stage 3 — `EVAL-02`: Gates ausführen (8/14 PT)

**Ausführungsort ist CI, nicht die Agent-Sandbox** — die Session-Netzpolitik
kann fremde Repos nicht klonen, GitHub-Actions-Runner können es (das bestehende
`corpus.yml` beweist den Pfad). Ablauf:

1. Eval-Workflow (Erweiterung von `corpus.yml`/`eval-correctness.yml`): Full-run
   je gepinntem Repo — Wallclock, Peak-RSS, DB-Größe, Warm-p95 je Op-Klasse,
   dann die 20 Hero-Aufgaben; Rohdaten als `internal/evalreport`-JSON.
2. Rohdaten werden als Artefakt publiziert UND als PR zurück ins Repo
   committet (`docs/eval/…`); der Report referenziert Commit, Runnerklasse, OS.
3. **Budgets einfrieren:** aus dem ersten grünen Lauf werden die U5-Budgets
   festgeschrieben und als Gates versioniert (ab dann Ratchets).
4. **Messentscheidungen schließen:** ADR 0003 U2 (Brief-Aggregat: Katalog-Read
   vs. SQL-Aggregate) und U4 (Vollgraph-Cache: löschen/begrenzen/Opt-in) werden
   mit den Repo-Messwerten entschieden und als ADR-Updates dokumentiert; U1
   (ExactName) nach Befund aus der CORE-02-Migration abgeschlossen.

**Exit (G4):** 20/20 Hero-Aufgaben grün; drei Full-runs mit eingecheckter
Roh-Evidenz; Budgets versioniert; keine High-/Critical-Findings im Stable Scope.

### Stage 4 — `RC-01`: Evidenz zusammenführen, Go/No-Go, Lock (3/5 PT)

1. **RC-Dossier** (`docs/rc/focused-core-rc.md`): G0–G4-Checkliste mit Links auf
   die jeweilige Evidenz — Recovery-Disposition (ADR 0004), Red-Gate-Beweis +
   DAG (ADR 0005), Manifest (CAP-01), Journey-Tests (RUN-01), Eval-Rohdaten
   (EVAL-02).
2. **REL-01-Reste schließen:** U1 Action-Pins (ein Befehl je Action, braucht
   GitHub-API-Zugriff — Sami oder eine Session mit offener Netzpolitik; danach
   Pin-Assertion im Workflow-Test scharfschalten) und U2 Environment Protection
   auf dem `publish`-Job (Repo-Setting).
3. **Go/No-Go dokumentieren** (Entscheider: Sami). Bei Go: `publish-lock.json`
   in einem eigenen, reviewten Commit auf `"locked": false` — der erste Release
   läuft danach vollständig durch den DAG. Bei No-Go: Blocker benennen, Lock
   bleibt zu.

### Parallel, außerhalb der Engineering-Pipeline

- **G5 / Design-Partner (Sami, ab sofort):** 3–5 Partner rekrutieren; blockiert
  keinen Build, ist aber der langsamste Pfad im Gesamtplan und der Trigger für
  jede optionale Wette (NET-01, ACT-01, COMP-PILOT …).
- **MCP-Roots-Capture (Sami, 15 min):** einmal `graphi setup` + echten Client
  gegen ein Fixture-Repo laufen lassen und die `initialize`-Params sichern —
  schließt ADR 0002 U2 empirisch.

## Reihenfolge-Begründung und Risiken

| Risiko | Wirkung | Gegenmaßnahme |
|---|---|---|
| Review-Findings am Achterstapel nach RUN-01-Start | doppelte Rework-Kosten in `cmd/graphi` | Stage 0 blockiert Stage 1; Endgame baut auf gereviewtem `main` |
| Sandbox kann Corpus-Repos nicht klonen | EVAL-02 lokal unmöglich | EVAL-02 als CI-Workflow; Rohdaten via Artefakt + Commit zurück |
| U2 (Roots) ohne echten Client nur synthetisch | Mapping-Drift bei realen Clients | Architektur client-agnostisch (D4-Präzedenz fix); 15-min-Capture justiert nur Daten |
| Erste Budgets zu streng/zu lax eingefroren | Flaky Gates oder Scheingenauigkeit | Budgets erst NACH erstem grünen Lauf, als Ratchet, mit Runnerklasse versioniert |
| Attestation scheitert auf privatem Repo-Plan | Publish-Job rot beim ersten echten Release | fail-closed gewollt; Ausweich (cosign) als ADR-0005-Update, nicht als continue-on-error |
| Java-Monorepo-Full-run sprengt CI-Zeit | EVAL-02 dauert/flaked | Nightly-Klasse statt PR-Gate (Muster existiert in `corpus.yml`); Repos einzeln als Matrix-Jobs |

## Aufwand und Kalender

Nach Plan-Schätzung verbleiben **24 PT P50 / 39 PT P80**. Beim beobachteten
Story-Durchsatz (agent-gestützt) realistisch:

- Stage 1 + 2: je **eine Session**, parallel möglich (nach Stage-0-Merge).
- Stage 3: **1–2 Sessions**, dominiert von CI-Laufzeiten (Nightly-Zyklen).
- Stage 4: **eine halbe Session** plus Samis Go-Entscheidung.

Kalenderkritisch sind nicht die Pakete, sondern: Samis Review (Stage 0), die
CI-Läufe (Stage 3) und die Design-Partner (G5).

## Definition of Done dieser Pipeline

- G3 und G4 grün mit eingecheckter Evidenz; RC-Dossier vollständig.
- Alle ADR-UNKNOWNs (0002 U1–U4, 0003 U1–U5, 0005 U1–U2) geschlossen oder mit
  benanntem Owner + Experiment dokumentiert.
- `RC-01`-Entscheidung dokumentiert; bei Go ist der Lock gelöst und der erste
  Release durch den DAG gelaufen.
- Kein Paket außerhalb des Master-Scopes begonnen (Stopping Rules gelten).
