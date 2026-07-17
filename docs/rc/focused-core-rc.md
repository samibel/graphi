# Focused Core RC — Dossier (SW-124 / RC-01)

> **Status:** ENTSCHEIDUNGSREIF — alle Engineering-Pakete des Master-Plans sind
> umgesetzt; die Go/No-Go-Entscheidung (§5) liegt bei Sami.
> **Stand:** 15. Juli 2026, Branch `claude/plan-feedback-f5khoi` (12 Story-Commits
> auf `main` @ `ed454de`; SW-109…SW-111, SW-113 bereits gemergt).
> **Autorität:** Master-Execution-Plan (WBS SW-109…SW-124, Gates G0–G5);
> Ausführungsordnung: [`docs/plan/superseded/2026-07-rc-endgame-pipeline.md`](../plan/superseded/2026-07-rc-endgame-pipeline.md).
> **Hinweis (SW-117, 2026-07-17):** Beide hier genannten Ordnungen — der
> Master-Execution-Plan und die RC-Endgame-Pipeline — sind seit dem Candidate-Freeze
> superseded und unter `docs/plan/superseded/` archiviert. Sie erklären, wie dieses
> Dossier entstanden ist; verbindlich für die weitere Planung ist allein
> [`docs/plan/2026-07-graphi-9of10-execution-plan.md`](../plan/2026-07-graphi-9of10-execution-plan.md).

## 1. Was dieses Dossier ist

Die G0–G4-Checkliste mit Links auf die jeweilige **eingecheckte Evidenz** —
keine Behauptung ohne Gate, Test oder Rohdaten. §4 listet die Punkte, die vor
einem Go noch offen sind (und wem sie gehören); §5 ist das Go/No-Go-Protokoll
mit dem dokumentierten Lock-Handgriff.

> **Eingefrorener M0-Candidate:** `docs/decisions/2026-07-m0-candidate-freeze.md`
> (SW-116). Dort steht die maßgebliche Candidate-SHA
> (`4e72637` — der Merge-Commit von #55 auf `main`, **nicht** `e285822`), der
> zugehörige Digest samt Provenienz — der **veröffentlichte** Release-Digest ist
> für diese SHA **UNKNOWN**, da v0.5.0 bereits am Eltern-Commit `65713de`
> publiziert ist — und die Change-Control-Regel (Plan §2.3/WP0). Jede Messung
> dieses Programms ist an diese SHA gebunden; dieses Dossier beschreibt den
> Stand v0.5.0 @ `65713de`.

## 2. Gate-Checkliste

### G0 — Sicherheits-Posture: GRÜN

| Beleg | Evidenz |
|-------|---------|
| Publish-Lock: reversibles CT-01-Gate, gates JEDEN Trigger; nach dem RC-01-Go (2026-07-15) auf `false` gedreht, Release freigegeben | `.github/publish-lock.json` (`"locked": false`); Test `cmd/publish-lock` `TestCommittedGateIsLifted` (Pin folgt dem Gate — Flip = reviewter Lift; Wieder-Einrasten dreht beide zurück) |
| Ein einziger, SHA-gebundener Release-Pfad | `.github/workflows/release-dag.yml`: gate→build→sbom→publish, jede Stufe `needs`-verkettet auf `${{ github.sha }}`; `auto-release.yml` (workflow_run-Kette auf dem FALSCHEN Workflow) gelöscht — `aa96826` (SW-120/REL-01), ADR 0005 |
| Red-Gate-Beweis: roter Gate ⇒ kein Tag, kein Release | `TestReleaseDAG_RedGateYieldsNoTagOrRelease`, `TestReleaseDAG_IsTheOnlyPublishPath` (repo-weiter Scan: genau ein Publish-Pfad) |
| Action-Supply-Chain gepinnt (ADR 0005 U1, RESOLVED) | Jede `uses:` im DAG auf 40-hex Commit-SHA; Assertion scharf: `TestReleaseDAG_EveryActionIsSHAPinned` (SW-124) |
| Nicht implementierte Refactors schlagen VOR dem Blast-Radius-Read fehl; Labs-HTTP-Routen 403 hinter `GRAPHI_HTTP_LABS`; Memory-Export nie auf Fremdpfade | `2888e1a` (SW-112/SAFE-01): `engine/edit` `ErrNotImplemented`-Validierung, `surfaces/http` `labsGuard`, `client.ErrExportPathRejected` + Tests |

### G1 — Ehrliche Capability-Oberfläche: GRÜN

| Beleg | Evidenz |
|-------|---------|
| Die 12 Stable-Ops sind eingefroren und überall identisch | `surfaces/mcp.StableOperations` (einzige Quelle); `internal/coverage` Stable-Tier-Gate: exakt 12, kein 13., keins fehlt (SW-111/SCOPE-01, main) |
| Generiertes, CI-frisches Capability-Manifest | `docs/capability-manifest.json` + `cmd/coverage -check` Freshness-Gate — `9f1f88c` (SW-117/CAP-01) |
| Consumer-owned Ports; kein Stable-Op endet in einem Stub | `surfaces/client/ports.go` (QueryPort/SearchPort/AgentContextPort/StableClient); `surfaces/capability_ports_test.go` treibt 11 der 12 Ops port-TYPISIERT (`index` läuft als Ingest-Fixture-Schritt, im Test dokumentiert) und scheitert auf jedem `*Unavailable`-Sentinel |
| Striktes Testgate ohne Rot-Toleranz | `cmd/testgate` (SW-110/TEST-01, main): GREEN = vollständiger Stream ohne Test-, Paket- oder Build-Fehler; Permission-Fixtures skippen nur, wenn der Fehlerpfad auf dem aktiven Dateisystem nicht erzwingbar ist |

### G2 — Kern-Korrektheit, Recovery, Privacy: GRÜN

| Beleg | Evidenz |
|-------|---------|
| Selektive Reads als Port-Vertrag (ADR 0003), beide Backends konform | `core/graphstore/lookup.go` + Konformanz-Suite `lookup_contract_test.go`; EXPLAIN-QUERY-PLAN-Gates — `2d174c9` (SP-11), `9988ff0` (CORE-01) |
| Alle Stable-Hotpaths selektiv, Byte-Parität bewiesen | `eb93d7c` (SW-116/CORE-02): SW-110-Golden-Tests unverändert grün; umgedrehte Scan-Pins (`TestSelectiveGate_*`); Skalen-Beweis: structural p95 ≤ 600 µs bei 43× Knoten (`docs/eval/runs/…`) |
| Crash-Recovery: Kill an jeder Batch-Grenze konvergiert byte-identisch | `4f2bd36` (SW-118/ING-DEC): `engine/ingest/faultmatrix_test.go`; zwei echte Defekte gefunden + gefixt (store-derived Purge, `RecoverWithRoot` produktiv verdrahtet); ADR 0004 (K1–K8) |
| Privacy-Defaults: gitignore an, 0600/0700 + Migration, Secret-Rejection | `8a014d7` (SW-119/PRIV-01): `engine/ingest/ignore.go`, `TightenDBFileModes`, `memory.ErrSecretRejected` + Modus-Gates `privacy_modes_test.go` |

### G3 — Session/Runtime (ADR 0002): GRÜN

| Beleg | Evidenz |
|-------|---------|
| Composition Root: open → recover → ingest → ready, einmaliges Close | `cmd/internal/runtime` — `5b9eeb5` (SW-121/RUN-01) |
| Zero-Config-MCP-Journey (Setup → initialize → list → call, ohne `-db`) | `surfaces/mcp_session_journey_subprocess_test.go` — der SW-113-Skip ist ENTFERNT, der Test ist das stehende G3-Gate |
| Daemon-Prozess terminiert (stop-RPC UND SIGTERM), Socket weg, restartfähig | `surfaces/daemon_lifecycle_subprocess_test.go` |
| Recovery vor Vertrauen beim Session-Open (ADR-0004-Restscope) | `cmd/graphi/zeroconfig_recovery_test.go` (drift-unsichtbare Divergenz wird geheilt) |

### G4 — Eval-Evidenz: Hero/Pins GRÜN; aktuelle Performance-Neubaseline OFFEN

| Beleg | Evidenz |
|-------|---------|
| 20 Hero-Aufgaben über exakt die 12 Stable-Ops, alle Fehlerklassen | `corpus/hero/` + Gate `cmd/eval/hero_test.go` — `660b5a3` (SW-122/EVAL-01); 20/20 grün (lokal + Report) |
| 3 gepinnte Real-Repos inkl. JVM-Monorepo, fail-closed SHA-Pins | `corpus/manifest.json` v2: cobra / flask / **guava v33.0.0** (`2214c63670fc`, Tier 3) |
| Full-Run-Harness + CI-Workflow | `cmd/eval -full-run` (+ hermetisches Gate `fullrun_test.go`), `.github/workflows/eval-full.yml` — `ecef54f` (SW-123/EVAL-02) |
| Erste vollständige Roh-Evidenz, eingecheckt | `docs/eval/runs/2026-07-15-local-sandbox/` (PRELIMINARY, Runnerklasse `local-sandbox`): alle 3 Repos PASS, Pins verifiziert, Hero 20/20 |
| Historische Performance-Beobachtungen | Der alte Harness beobachtete separat skalierende `agent_brief`-Latenz und hohe Index-Phasen-MAXRSS. Ihr kausaler Zusammenhang und aktuelle Werte unter dem geänderten Harness sind **UNKNOWN**; siehe ADR 0003 und `docs/eval/hero-protocol.md`. |

### G5 — Design-Partner: OFFEN (Sami, non-engineering)

3–5 Partner; blockiert keinen Build, ist aber laut Master der Trigger für jede
optionale Wette. Läuft parallel zur RC-Entscheidung.

## 3. Repo-Gates (Stand dieses Commits)

`go test ./…` via testgate: GREEN (keine Expected-Failure-Ausnahmen) · `gofmt -l`
leer · `go vet` sauber · `layerguard` PASS · `coverage -check` (Matrix +
Stable-Tier + Manifest-Freshness) PASS.

**Evidence-Index & Gate-Dashboard (SW-119):** Die 9/10-Programm-Gates (Plan §6
WP0–WP10 + §5 M0–M5) stehen in [`docs/rc/evidence-index.md`](evidence-index.md),
generiert aus [`docs/rc/evidence-index.yaml`](evidence-index.yaml) über
`go run ./cmd/evidence -generate`. `go run ./cmd/evidence -check` erzwingt die
WP0-Regel — eine PASS-Zeile braucht Evidence URI **und** SHA/Digest, sonst rot;
eine Zeile ohne belegten Status liest UNKNOWN. Heute sind praktisch alle Zeilen
UNKNOWN; das ist der Punkt. Diese RC-Checkliste (§2/§4) bleibt die maßgebliche
Freigabe-Sicht; der Index ist die Programm-Ebene darüber, nicht ihre Ablösung.

## 4. Offen vor dem Go

### 4.1 Blockierend (Reihenfolge)

1. **Stage-0-Review & Merge** — ERLEDIGT (PR #51 gemergt, `060f1ab`;
   Audit-Nachschliff PR #52, `fa2441c`).
2. **Aktuelle Referenz-Neubaseline** — **OFFEN vor dem nächsten Release**. Der
   historische `eval-full` Run
   [29418826616](https://github.com/samibel/graphi/actions/runs/29418826616)
   auf `ubuntu-latest` lieferte valide Hero-/Pin-Evidenz, nutzte aber eine ältere
   Messmethode: Index-Phasen-RSS, NodeId-Sampling und keinen `impact`-Sample.
   Die Zahlen in `docs/eval/hero-budgets.json` bleiben vorläufige, fail-closed
   Kompatibilitätsgrenzen, sind aber kein vergleichbarer Ratchet für den aktuellen
   Harness. Erst ein neuer Lauf desselben aktuellen Commits mit derselben aktuellen
   Methode darf Latenz/RSS neu baselinen. Die früheren Bezeichnungen „U2/U4/U5
   geschlossen“ sind durch die aktuelle ADR-0003-UNKNOWN-Liste ersetzt.

### 4.2 Nicht blockierend, vor dem ersten Release

- **ADR 0005 U2 — Environment Protection** auf dem `publish`-Job: Repo-Setting
  (Environments → required reviewers), kein committbares Artefakt — Sami, 5 min.
- **ADR 0002 U2 — MCP-Roots-Capture:** einmal `graphi setup` + echter Client
  gegen ein Fixture-Repo, `initialize`-Params sichern — Sami, 15 min. Justiert
  nur das Roots-Mapping, nicht die Architektur.

## 5. Historisches v0.5.0-Go/No-Go-Protokoll

Der folgende Block dokumentiert die Entscheidung vom 2026-07-15. Er ist **keine**
Freigabe des aktuellen, geänderten Harness oder dieses Worktree-Diffs.

**Entscheider:** Sami. **Historische Grundlage:** damalige §2-Checkliste + §4.1.

```text
Entscheidung: [ GO ]                 Datum: 2026-07-15
Begründung:
1. G0–G4 grün mit eingecheckter Evidenz; alle Repo-Gates grün; der
   adversariale Audit brach keine Sicherheits-/Korrektheitsbehauptung.
2. Nach damaligem Evidenzvertrag waren Review/Merge (#51/#52) und
   Budget-Freeze (#53) abgearbeitet. Diese Performance-Freigabe ist wegen der
   später geänderten Messmethode nicht auf den aktuellen Harness übertragbar.
3. Release-Pfad ist ein einziger, SHA-gebundener, red-gate-sicherer DAG;
   ein roter Gate produziert weiterhin weder Tag noch Release.
```

**GO umgesetzt (der dokumentierte Handgriff — genau ein reviewter Commit):**
`.github/publish-lock.json` `"locked": true → false` (plus der mitgedrehte
Pin `TestCommittedGateIsLifted`, damit Gate-Datei und Test nie auseinanderlaufen).

Danach läuft der Release vollständig durch den DAG: gate → build → sbom →
attest → tag → publish, alles auf einer SHA; ein roter Gate produziert
weiterhin weder Tag noch Release (`TestReleaseDAG_RedGateYieldsNoTagOrRelease`).
Die Version ergibt sich aus dem ersten Released-Header in `CHANGELOG.md`
(**v0.5.0**); ein zweiter Release entsteht durch den nächsten Header + Push.

**Wieder-Einrasten (Incident/Freeze):** `"locked"` zurück auf `true` und den
Pin mitdrehen — ein reviewter Commit, keine Workflow-Änderung.

## 6. Reproduktion (eine Zeile je Beleg)

```sh
CGO_ENABLED=0 go run ./cmd/testgate -target ./...                  # Repo-Gates (Exitcode fail-closed)
go run ./cmd/coverage -check                                       # Manifest/Stable-Tier
go test ./cmd/publish-lock/                                        # Lock + DAG-Beweise
go test ./surfaces/ -run 'SessionProfile|DaemonLifecycle'          # G3-Journeys
go test ./cmd/eval/ -run 'TestHeroSuite|TestFullRun'               # G4-Gates (hermetisch)
go run ./cmd/eval -manifest corpus/manifest.json -scenarios corpus/hero  # Hero 20/20
go run ./cmd/eval -manifest corpus/manifest.json -full-run cobra \
  -runner-class ubuntu-latest -budgets docs/eval/hero-budgets.json # Full-Run + vorläufige Kompatibilitätsgrenze (Netz)
```
