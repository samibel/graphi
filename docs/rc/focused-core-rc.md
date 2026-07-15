# Focused Core RC — Dossier (SW-124 / RC-01)

> **Status:** ENTSCHEIDUNGSREIF — alle Engineering-Pakete des Master-Plans sind
> umgesetzt; die Go/No-Go-Entscheidung (§5) liegt bei Sami.
> **Stand:** 15. Juli 2026, Branch `claude/plan-feedback-f5khoi` (12 Story-Commits
> auf `main` @ `ed454de`; SW-109…SW-111, SW-113 bereits gemergt).
> **Autorität:** Master-Execution-Plan (WBS SW-109…SW-124, Gates G0–G5);
> Ausführungsordnung: `docs/plan/2026-07-rc-endgame-pipeline.md`.

## 1. Was dieses Dossier ist

Die G0–G4-Checkliste mit Links auf die jeweilige **eingecheckte Evidenz** —
keine Behauptung ohne Gate, Test oder Rohdaten. §4 listet die Punkte, die vor
einem Go noch offen sind (und wem sie gehören); §5 ist das Go/No-Go-Protokoll
mit dem dokumentierten Lock-Handgriff.

## 2. Gate-Checkliste

### G0 — Sicherheits-Posture: GRÜN

| Beleg | Evidenz |
|-------|---------|
| Publish-Lock eingerastet; Release unmöglich bis RC-Go | `.github/publish-lock.json` (`"locked": true`); Test `cmd/publish-lock` `TestCommittedGateIsEngaged` (SW-109/CT-01, main) |
| Ein einziger, SHA-gebundener Release-Pfad | `.github/workflows/release-dag.yml`: gate→build→sbom→publish, jede Stufe `needs`-verkettet auf `${{ github.sha }}`; `auto-release.yml` (workflow_run-Kette auf dem FALSCHEN Workflow) gelöscht — `aa96826` (SW-120/REL-01), ADR 0005 |
| Red-Gate-Beweis: roter Gate ⇒ kein Tag, kein Release | `TestReleaseDAG_RedGateYieldsNoTagOrRelease`, `TestReleaseDAG_IsTheOnlyPublishPath` (repo-weiter Scan: genau ein Publish-Pfad) |
| Action-Supply-Chain gepinnt (ADR 0005 U1, RESOLVED) | Jede `uses:` im DAG auf 40-hex Commit-SHA; Assertion scharf: `TestReleaseDAG_EveryActionIsSHAPinned` (SW-124) |
| Nicht implementierte Refactors schlagen VOR dem Blast-Radius-Read fehl; Labs-HTTP-Routen 403 hinter `GRAPHI_HTTP_LABS`; Memory-Export nie auf Fremdpfade | `2888e1a` (SW-112/SAFE-01): `engine/edit` `ErrNotImplemented`-Validierung, `surfaces/http` `labsGuard`, `client.ErrExportPathRejected` + Tests |

### G1 — Ehrliche Capability-Oberfläche: GRÜN

| Beleg | Evidenz |
|-------|---------|
| Die 12 Stable-Ops sind eingefroren und überall identisch | `surfaces/mcp.StableOperations` (einzige Quelle); `internal/coverage` Stable-Tier-Gate: exakt 12, kein 13., keins fehlt (SW-111/SCOPE-01, main) |
| Generiertes, CI-frisches Capability-Manifest | `docs/capability-manifest.json` + `cmd/coverage -check` Freshness-Gate — `9f1f88c` (SW-117/CAP-01) |
| Consumer-owned Ports; kein Stable-Op endet in einem Stub | `surfaces/client/ports.go` (QueryPort/SearchPort/AgentContextPort/StableClient); `surfaces/capability_ports_test.go` treibt alle 12 Ops port-TYPISIERT und scheitert auf jedem `*Unavailable`-Sentinel |
| Testgate-Allowlist statt stiller Rot-Toleranz | `cmd/testgate` (SW-110/TEST-01, main): GREEN = nur die 2 dokumentierten Root-Carve-outs |

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

### G4 — Eval-Evidenz: GRÜN mit einem dokumentierten Rest (§4.2)

| Beleg | Evidenz |
|-------|---------|
| 20 Hero-Aufgaben über exakt die 12 Stable-Ops, alle Fehlerklassen | `corpus/hero/` + Gate `cmd/eval/hero_test.go` — `660b5a3` (SW-122/EVAL-01); 20/20 grün (lokal + Report) |
| 3 gepinnte Real-Repos inkl. JVM-Monorepo, fail-closed SHA-Pins | `corpus/manifest.json` v2: cobra / flask / **guava v33.0.0** (`2214c63670fc`, Tier 3) |
| Full-Run-Harness + CI-Workflow | `cmd/eval -full-run` (+ hermetisches Gate `fullrun_test.go`), `.github/workflows/eval-full.yml` — `ecef54f` (SW-123/EVAL-02) |
| Erste vollständige Roh-Evidenz, eingecheckt | `docs/eval/runs/2026-07-15-local-sandbox/` (PRELIMINARY, Runnerklasse `local-sandbox`): alle 3 Repos PASS, Pins verifiziert, Hero 20/20 |
| U2/U4 messbar eingegrenzt | ADR 0003: `agent_brief` einziger Skalierungs-Ausreißer (11 ms → 558 ms); guava 4,2 GB RSS vs. 35 MB Store |

### G5 — Design-Partner: OFFEN (Sami, non-engineering)

3–5 Partner; blockiert keinen Build, ist aber laut Master der Trigger für jede
optionale Wette. Läuft parallel zur RC-Entscheidung.

## 3. Repo-Gates (Stand dieses Commits)

`go test ./…` via testgate: GREEN (nur die 2 Allowlist-Carve-outs) · `gofmt -l`
leer · `go vet` sauber · `layerguard` PASS · `coverage -check` (Matrix +
Stable-Tier + Manifest-Freshness) PASS.

## 4. Offen vor dem Go

### 4.1 Blockierend (Reihenfolge)

1. **Stage-0-Review & Merge** des Branch-Stapels (13 Commits, story-atomar,
   in Commit-Reihenfolge reviewbar) — Sami. Ohne Merge läuft kein Workflow.
2. **Referenzlauf + Budget-Freeze:** nach dem Merge `eval-full.yml` manuell
   dispatchen (ubuntu-latest). Artefakte herunterladen, unter
   `docs/eval/runs/<datum>-ubuntu-latest/` einchecken, die `null`-Budgets in
   `docs/eval/hero-budgets.json` als Ratchets füllen — **ein** reviewter PR,
   der den Workflow-Run zitiert. Gleichzeitig ADR 0003 **U2** (Brief-Digest:
   Katalog-Read vs. SQL-Aggregat — Messfrage einer einzigen Op) und **U4**
   (memGraph-Cache: mit/ohne-Paar) als ADR-Updates schließen.

### 4.2 Nicht blockierend, vor dem ersten Release

- **ADR 0005 U2 — Environment Protection** auf dem `publish`-Job: Repo-Setting
  (Environments → required reviewers), kein committbares Artefakt — Sami, 5 min.
- **ADR 0002 U2 — MCP-Roots-Capture:** einmal `graphi setup` + echter Client
  gegen ein Fixture-Repo, `initialize`-Params sichern — Sami, 15 min. Justiert
  nur das Roots-Mapping, nicht die Architektur.

## 5. Go/No-Go-Protokoll

**Entscheider:** Sami. **Grundlage:** §2-Checkliste + §4.1 abgearbeitet.

```text
Entscheidung: [ GO / NO-GO ]        Datum: ____________
Begründung (3 Sätze reichen):
1. ____________________________________________________________
2. ____________________________________________________________
3. ____________________________________________________________
```

**Bei GO — der dokumentierte Handgriff (genau ein Commit):**

```sh
# .github/publish-lock.json: "locked": true -> false — sonst NICHTS im Diff.
# Eigener, reviewter Commit; Commit-Message referenziert dieses Dossier.
```

Danach läuft der erste Release vollständig durch den DAG: gate → build → sbom
→ attest → tag → publish, alles auf einer SHA; ein roter Gate produziert
weiterhin weder Tag noch Release (`TestReleaseDAG_RedGateYieldsNoTagOrRelease`).

**Bei NO-GO:** Blocker hier benennen, Lock bleibt zu, Dossier bleibt der
Wiedervorlage-Punkt.

## 6. Reproduktion (eine Zeile je Beleg)

```sh
CGO_ENABLED=0 go test -json ./... | go run ./cmd/testgate -stdin   # Repo-Gates
go run ./cmd/coverage -check                                       # Manifest/Stable-Tier
go test ./cmd/publish-lock/                                        # Lock + DAG-Beweise
go test ./surfaces/ -run 'SessionProfile|DaemonLifecycle'          # G3-Journeys
go test ./cmd/eval/ -run 'TestHeroSuite|TestFullRun'               # G4-Gates (hermetisch)
go run ./cmd/eval -manifest corpus/manifest.json -scenarios corpus/hero  # Hero 20/20
go run ./cmd/eval -manifest corpus/manifest.json -full-run cobra   # Full-Run (Netz)
```
