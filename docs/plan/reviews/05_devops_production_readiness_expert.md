# DevOps & Production Readiness Review

## Kurzurteil

Graphi hat fuer ein Pre-1.0-Local-First-CLI-Projekt ungewoehnlich viele und teilweise sehr gute Quality Gates. Der Build ist reproduzierbar angelegt, Releases werden fuer mehrere Plattformen gebaut, Installationsartefakte werden gehasht, und Datenschutz-/CGo-Invarianten werden explizit getestet. Das ist deutlich ueber Prototyp-Niveau.

Trotzdem ist das System aktuell **nicht release-sicher und nicht als langlebiger lokaler Dienst betriebsreif**. Zwei Findings sind harte Blocker: Erstens kann die automatische Release-Pipeline taggen und Assets publizieren, obwohl der separate `release-gate`-Workflow rot ist. Zweitens beendet `graphi daemon stop` zwar Listener und Socket, aber nicht den CLI-Prozess, der danach in `select {}` haengen bleibt. Zusaetzlich ist die publizierte GitHub Action in ihrer dokumentierten Fremd-Repo-Nutzung sehr wahrscheinlich funktionsunfaehig: `graphi-version` wird nur als Environment-Variable gesetzt; gebaut wird `./cmd/graphi` aus dem Consumer-Workspace statt aus einer auf diese Version ausgecheckten Graphi-Quelle.

Die richtige Reaktion ist kein Rewrite. Die Codebasis braucht einen fokussierten Production-Readiness-Refactor mit einem einzigen autoritativen Release-DAG, sauberem Prozess-Lifecycle, realen Action-End-to-End-Tests und minimaler Betriebs-Telemetrie.

## Score 0–10

**4,5 / 10**

Teilbewertungen:

| Bereich | Score | Urteil |
|---|---:|---|
| Lokales Setup / Developer Experience | 6,5 | Gute Quickstart- und Contributor-Pfade, aber kein einheitlicher lokaler CI-Befehl und ein irrefuehrender Doctor-Check. |
| CI-Qualitaet | 7,0 | Breite Gates inklusive Race, Bench, Privacy, CGo und Corpus; Supply-Chain-Pinning und Workflow-Konsolidierung fehlen. |
| Release / Distribution | 3,0 | Reproduzierbare Matrix und Checksums stark; der Release-DAG kann das eigene harte Gate umgehen. |
| Prozess- und Service-Lifecycle | 2,5 | Daemon-Stop beendet den Prozess nicht; HTTP hat keinen Graceful Shutdown. |
| Observability / Betrieb | 2,5 | Einfache Health- und Status-Sichten existieren, aber keine strukturierte Request-Telemetrie, Readiness oder belastbarer Lifecycle. |
| Konfiguration | 5,5 | Sichere lokale Defaults und Flag/Env-Praezedenz; Konfiguration bleibt ueber Subcommands verteilt und ist nicht als Gesamtzustand inspizierbar. |
| Supply Chain | 4,0 | Checksums und einzelne Provenance-Pruefungen vorhanden; eigene Workflows nutzen floating Action-Tags, Signaturen/SBOM/Vulnerability-Gate fehlen im Repository. |

## Groesste Staerken

1. **Reproduzierbarer, statischer Release-Build ist real implementiert.** Der Build erzwingt `CGO_ENABLED=0`, `-trimpath`, VCS-Stamping und eine Version per `ldflags` (`internal/release/build.go:146-171`). Die Reproduzierbarkeit wird durch zwei Builds und SHA-256-Vergleich geprueft (`internal/release/build.go:174-199`). Die Release-Matrix deckt Linux/macOS amd64/arm64 sowie Windows amd64 ab (`internal/release/build.go:96-107`).

2. **Distribution hat einen sinnvollen Fail-Closed-Checksum-Pfad.** Die Pipeline erzeugt kanonische `SHA256SUMS` (`internal/release/dist.go:32-48`), und der Unix-Installer verweigert die Installation bei fehlendem oder falschem Hash (`install.sh:117-145`). Das PowerShell-Pendant vergleicht ebenfalls SHA-256 und bricht bei Abweichung ab (`install.ps1:50-65`).

3. **CI prueft mehr als nur `go test`.** Der Release-Workflow baut und testet das Workspace (`.github/workflows/release.yml:35-50`), prueft die Schichtrichtung (`.github/workflows/release.yml:52-64`) und den reproduzierbaren Build (`.github/workflows/release.yml:66-78`). Dazu kommen Race Detection (`.github/workflows/lint.yml:50-60`), ein Budget-Benchmark (`.github/workflows/bench.yml:28-36`), ein realer Corpus-Smoke-Test (`.github/workflows/corpus.yml:27-44`) sowie harte Privacy-/Egress-Checks in einem Linux-Netzwerk-Namespace (`.github/workflows/privacy-audit.yml:21-55`).

4. **Lokale Sicherheitsdefaults sind gut.** HTTP bindet ausschliesslich an explizite Loopback-Hosts und lehnt andere Adressen ab (`surfaces/http/server.go:270-295`). Der Daemon setzt den Unix-Socket auf `0600` (`surfaces/daemon/daemon.go:141-159`) und verwirft unsichere, world-writable Socket-Parents (`surfaces/daemon/daemon.go:384-410`). Per-Repo-State-Verzeichnisse werden mit `0700` und die Metadatei mit `0600` angelegt (`internal/state/state.go:146-168`).

5. **Setup und Diagnose sind als Produktpfade gedacht.** Der README-Quickstart ist kurz und nutzt vorgebaute, CGo-freie Binaries (`readme.md:41-66`). `graphi doctor` prueft Binary, PATH, MCP, DB, Privacy und Local-First-Posture (`cmd/graphi/doctor.go:42-73`). Der persistente Zustand folgt XDG bzw. einem deterministischen Fallback (`internal/state/state.go:21-33`) und ist repo-isoliert (`internal/state/state.go:116-134`).

6. **Schema- und Datenhaltungsarbeit ist besser als die Diagnose-Dokumentation vermuten laesst.** Der Graphstore hat eine explizite Schema-Version 3 (`core/graphstore/sqlite.go:107-122`), Migrationen fuer Edge-Layout und Node-Metadaten (`core/graphstore/sqlite.go:137-149`) und stempelt `PRAGMA user_version` (`core/graphstore/sqlite.go:201-206`).

## Groesste Schwächen

1. **Die CI-Landschaft ist breit, aber nicht als ein autoritativer DAG modelliert.** Es existieren 17 einzelne Workflows; viele fuehren auf jedem PR aehnliche Checkout-/Go-Setup-Schritte aus. Der entscheidende Fehler ist nicht die Menge, sondern dass `release-gate` und `release` unabhaengige Workflows sind (`.github/workflows/release-gate.yml:1-30`, `.github/workflows/release.yml:11-34`). Sichtbare gruene Einzelchecks sind kein Beweis, dass genau der veroeffentlichte Commit alle Gates bestanden hat.

2. **Eigene CI-Dependencies sind weniger streng gepinnt als die ausgelieferte Composite Action behauptet.** Die Composite Action verlangt volle 40-stellige SHAs (`extensions/github-action/action.yml:97-110`), waehrend praktisch alle Repository-Workflows floating Major-Tags wie `actions/checkout@v4` und `actions/setup-go@v5` nutzen, z. B. `.github/workflows/release.yml:41-45` und `.github/workflows/lint.yml:22-26`. Das ist eine inkonsistente Supply-Chain-Policy.

3. **Observability ist fuer einen langlebigen Prozess zu duenn.** `/healthz` antwortet bedingungslos mit `status=ok` und prueft weder Store noch Watcher noch Ingest-Bereitschaft (`surfaces/http/server.go:315-317`). Fehler werden mit dem unstrukturierten Standard-Logger ausgegeben (`surfaces/http/server.go:894-935`); Request-Rate, Latenz, aktive SSE-Verbindungen, Queue-/Watcher-Zustand und Resource Usage werden nicht instrumentiert. Der Daemon-Status kennt immerhin PID, Uptime, Generation und Workspaces (`surfaces/daemon/control.go:61-70`), aber der normale CLI-Befehl `daemon status` testet nur eine Dummy-Query und rendert diesen Status nicht (`cmd/graphi/main.go:1156-1163`).

4. **HTTP ist nur gegen langsame Header teilweise gehaertet.** Der Server setzt `ReadHeaderTimeout`, aber keinen kontrollierbaren Shutdown-Pfad (`surfaces/http/server.go:251-267`). `runHTTP` blockiert direkt auf `Serve` und behandelt weder SIGINT/SIGTERM noch `http.Server.Shutdown` (`cmd/graphi/main.go:1237-1252`). Fuer SSE ist ein globaler Write-Timeout bewusst problematisch; das entschuldigt aber nicht fehlende Connection-/Shutdown-Strategie und fehlende per-handler Deadlines fuer unare Endpunkte.

5. **Der Doctor erzeugt in einer korrekten Binary-Installation einen falschen Fehler.** Der Nutzer-Quickstart verspricht ein vorgebautes Binary (`readme.md:41-48`), doch `PATHCheck` meldet fehlendes `go` als `StatusFail` (`internal/doctor/checks.go:136-150`). Go ist eine Build-Voraussetzung fuer Contributor (`CONTRIBUTING.md:22-37`), keine Runtime-Voraussetzung fuer den installierten Endnutzer.

6. **Die Graphstore-Diagnose ist gegen die Implementierung veraltet.** `doctor` behauptet, der Graphstore erfasse keine explizite Version (`internal/doctor/checks.go:244-249`), obwohl `graphstoreSchemaVersion = 3` existiert und bei jedem Init in `user_version` geschrieben wird (`core/graphstore/sqlite.go:107-122`, `core/graphstore/sqlite.go:201-206`). Das reduziert den Wert des Diagnosewerkzeugs gerade bei Upgrade-Problemen.

7. **Release-Integritaet endet bei einem Hash aus derselben Vertrauensdomäne.** Binary und `SHA256SUMS` werden vom selben GitHub Release geladen (`install.sh:107-128`); ein kompromittierter Release-Upload kann beide ersetzen. Im untersuchten Repository gibt es keinen erkennbaren Signatur-, Attestation-, SBOM- oder Vulnerability-Scan-Schritt. Die Checksums schuetzen gut gegen Transferfehler, nicht gegen einen kompromittierten Publisher.

## Kritische Blocker

### Blocker 1: Automatische Releases koennen das harte Release-Gate umgehen

`auto-release` wartet ausschliesslich auf den Workflow namens `release` und prueft nur dessen Success-Conclusion (`.github/workflows/auto-release.yml:36-55`). Danach erzeugt und pusht er den Tag (`.github/workflows/auto-release.yml:74-88`) und dispatcht erneut `release.yml` fuer die Assets (`.github/workflows/auto-release.yml:90-101`). Der separate Workflow `release-gate` laeuft zwar auf `main`, ist aber weder Trigger noch `needs`-Abhaengigkeit dieses Pfades (`.github/workflows/release-gate.yml:1-30`). Der Asset-Job erstellt bei Bedarf den GitHub Release und laedt Assets hoch (`.github/workflows/release.yml:176-186`).

Damit ist folgende reale Reihenfolge moeglich: `release` gruen, `release-gate` rot, `auto-release` taggt trotzdem, `release-assets` publiziert trotzdem. Branch Protection kann verhindern, dass ein roter PR gemergt wird; sie kann aber den hier bereits auf `main` ausgeloesten Workflow-DAG nicht nachtraeglich koppeln. Das widerspricht direkt dem Anspruch, die 90/80-Qualitaetsschwellen seien release-blockierend (`readme.md:379-385`).

**Muss vor dem naechsten automatischen Release behoben werden.**

### Blocker 2: `graphi daemon stop` stoppt nicht den Daemon-Prozess

Beim Start blockiert die CLI nach `srv.Start` fuer immer in `select {}` (`cmd/graphi/main.go:1138-1145`). Der Stop-RPC liefert erst ein Ack (`surfaces/daemon/daemon.go:375-378`); danach schliesst `handleConn` Listener und Socket (`surfaces/daemon/daemon.go:232-237`, `surfaces/daemon/daemon.go:170-186`). Es gibt jedoch keinen Kanal, kein `Wait`, keinen Signal-Context und keinen Rueckweg aus dem `select {}` der CLI. Der Prozess und seine Deferred-Cleanups fuer Store und Watcher bleiben damit am Leben (`cmd/graphi/main.go:1100-1105`, `cmd/graphi/main.go:1128-1129`).

Das ist nicht kosmetisch: Upgrades, Neustarts, DB-Handles, File-Watcher und Prozessverwaltung werden unzuverlaessig. Die vorhandenen Socket-Tests pruefen das Serverobjekt, nicht den Ende-zu-Ende-Lebenszyklus des gestarteten CLI-Prozesses.

**Muss vor der Einstufung als betriebsreifer Hot-Index-Daemon behoben werden.**

### Blocker 3: Die dokumentierte GitHub Action baut nicht die angeforderte Graphi-Version

Die Action dokumentiert `graphi-version` als gepinnte Engine-Version (`extensions/github-action/action.yml:36-43`, `extensions/github-action/README.md:45-58`). Im Build-Step wird dieser Wert aber lediglich in `GRAPHI_VERSION` gelegt; der Befehl ist schlicht `go build ... ./cmd/graphi` im aktuellen Workspace (`extensions/github-action/action.yml:113-125`). Es gibt keinen Checkout, Download oder `go install` der angegebenen Graphi-Version. Der vorausgehende `actions/checkout` checkt standardmaessig das Consumer-Repository in `$GITHUB_WORKSPACE` aus (`extensions/github-action/action.yml:101-105`). In der dokumentierten Nutzung in einem fremden Repository (`extensions/github-action/README.md:71-107`) existiert `./cmd/graphi` normalerweise nicht; falls zufaellig doch, wird Consumer-Code statt der gepinnten Graphi-Engine gebaut.

Die Validatoren pruefen Input-Form, Output-Projektion und SHA-Pinning der `uses:`-Schritte (`extensions/github-action/validate/validate.go:182-243`), aber nicht, ob `graphi-version` tatsaechlich die gebaute Quelle bestimmt. Damit ist die behauptete Runtime-Pinnung nicht belegt und die publizierte Action vermutlich nur innerhalb des Graphi-Repositories lauffaehig.

**Vor externer Bewerbung oder Marketplace-Publikation blockieren und mit einem echten Consumer-Repo-E2E-Test absichern.**

## Technische Schulden

1. **Ein Release-Orchestrator statt lose gekoppelter Workflows.** `release-gate`, Build, Reproducibility, Packaging und Publish muessen in einem Commit-gebundenen DAG liegen. `workflow_run` nur auf den generischen `release`-Namen ist zu schwach (`.github/workflows/auto-release.yml:36-55`).

2. **Graceful Lifecycle als gemeinsame Abstraktion.** HTTP und Daemon brauchen `signal.NotifyContext`, einen `Done()`-Kanal, begrenztes Drain/Shutdown und definierte Exit Codes. Derzeit blockieren beide Entrypunkte ohne Signalsteuerung (`cmd/graphi/main.go:1143-1145`, `cmd/graphi/main.go:1244-1252`).

3. **Process Supervision fehlt.** Es gibt keine mitgelieferte systemd-/launchd-/Windows-Service-Definition und keine dokumentierte Restart-/Log-Rotation-Policy. Fuer ein bewusst benutzerlokales, bei Bedarf gestartetes Tool ist das zunaechst akzeptabel; fuer den beworbenen dauerhaften Hot-Index-Daemon ist es offene Betriebsarbeit.

4. **Observability-Contract definieren.** Health, Readiness und Diagnose sollten getrennt werden. `healthz` ist heute reine Prozess-Liveness (`surfaces/http/server.go:315-317`); Watcher-Health existiert bereits intern (`cmd/graphi/main.go:1051-1075`) und sollte in Readiness/Status eingebunden werden.

5. **Request-Grenzen vereinheitlichen.** Einige POST-Endpunkte limitieren Bodies auf 1 MiB (`surfaces/http/server.go:394-445`), andere decodieren direkt ohne `MaxBytesReader`, etwa Memory/Distill/SkillGen (`surfaces/http/server.go:698-745`). Loopback reduziert die Exposition, beseitigt aber lokale DoS-/Fehlkonfigurationsrisiken nicht.

6. **CI konsolidieren und cachen.** Die spezialisierten Gates sind wertvoll, aber 17 Workflows vervielfachen Runner-Setup, Queue-Zeit und Branch-Protection-Konfiguration. Ein zentraler PR-Workflow mit wiederverwendbaren Workflows/Jobs wuerde dieselben Signale mit klarerer Required-Check-Semantik liefern.

7. **Supply-Chain-Haertung.** Alle Third-Party Actions auf volle SHAs pinnen, Dependency Review plus `govulncheck` einfuehren, releasebezogene SBOM erzeugen und Artefakte/Provenance signieren. Die eigene Composite Action zeigt bereits, dass SHA-Pinning verstanden ist (`extensions/github-action/action.yml:97-110`).

8. **Installations-UX absichern.** `curl | sh` und `iwr | iex` sind bequem (`readme.md:41-54`), laden aber den Installer selbst ungepinnt von `main`. Eine dokumentierte Alternative mit versioniertem Installer/Release-Asset und separater Signatur sollte der empfohlene High-Trust-Pfad sein.

9. **Doctor auf Runtime-Anforderungen beschraenken.** Go darf bei einem Release-Binary hoechstens Info/Warn fuer Source-Build-Funktionen sein; es darf die Runtime-Gesundheit nicht rot machen (`internal/doctor/checks.go:136-150`). Die echte Graphstore-Schema-Version muss korrekt angezeigt werden (`core/graphstore/sqlite.go:107-122`).

10. **Action-Artefakte robust parsen.** Die Action extrahiert JSON mit `grep`/`sed` und schreibt globale Dateien nach `/tmp` (`extensions/github-action/entrypoint.sh:51-91`). Parallele/nested Invocations koennen kollidieren; JSON-Formatierungen koennen das Parsing brechen. Pro Run ein `mktemp -d` und ein echter JSON-Parser bzw. ein dedizierter CLI-Output-Modus sind angemessen.

## Konkrete Codebeispiele

### 1. Release-DAG hart koppeln

Zielstruktur in einem einzigen Workflow oder einem wiederverwendbaren `workflow_call`:

```yaml
jobs:
  release-gate:
    uses: ./.github/workflows/release-gate-reusable.yml

  build-assets:
    needs: release-gate
    if: needs.release-gate.result == 'success'
    # build + checksums + attestations

  publish:
    needs: build-assets
    environment: release
    permissions:
      contents: write
    # tag and upload the exact tested SHA
```

Der Tag darf erst im letzten Job entstehen. Nicht `auto-release` auf einen anderen Workflow lauschen lassen, sondern das getestete `github.sha` als unveraenderliche Release-Identitaet durchreichen. Der heutige Entkopplungspunkt ist `.github/workflows/auto-release.yml:36-55`; das Publish liegt in `.github/workflows/release.yml:176-186`.

### 2. Daemon sauber beenden

```go
ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stopSignals()

srv := daemon.NewServerWithWatch(handler, watchMgr)
if err := srv.Start(socket); err != nil { /* ... */ }

select {
case <-ctx.Done():
case <-srv.Done(): // wird von Stop nach Listener-Close geschlossen
}

shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
return exitCode(srv.Shutdown(shutdownCtx))
```

`Server.Stop` muss den Done-Kanal genau einmal schliessen. Damit endet der heutige Endlosblock in `cmd/graphi/main.go:1143-1145`, und Deferred-Cleanups laufen tatsaechlich.

### 3. HTTP-Lifecycle und Readiness

```go
httpServer := &http.Server{
    Handler:           app.Handler(),
    ReadHeaderTimeout: 5 * time.Second,
    IdleTimeout:       60 * time.Second,
}

go func() { serveErr <- httpServer.Serve(listener) }()

select {
case <-ctx.Done():
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    return httpServer.Shutdown(shutdownCtx)
case err := <-serveErr:
    return err
}
```

`/healthz` bleibt Liveness; `/readyz` prueft Store-Selfcheck, abgeschlossenen Initial-Ingest und kritischen Watcher-Zustand. Die heutige bedingungslose Health-Antwort steht in `surfaces/http/server.go:315-317`.

### 4. GitHub Action wirklich auf `graphi-version` pinnen

Eine robuste Variante laedt das versionierte Release-Binary und prueft es gegen signierte Checksums/Attestation. Alternativ muss Graphi explizit in ein separates Verzeichnis ausgecheckt werden:

```yaml
- name: Checkout consumer repository
  uses: actions/checkout@<full-sha>
  with:
    path: consumer

- name: Checkout pinned graphi source
  uses: actions/checkout@<full-sha>
  with:
    repository: samibel/graphi
    ref: ${{ inputs.graphi-version }}
    path: .graphi-src

- name: Build pinned graphi
  shell: bash
  run: |
    cd .graphi-src
    CGO_ENABLED=0 go build -trimpath -o "$RUNNER_TEMP/graphi" ./cmd/graphi
```

Danach muss `working-directory` relativ zu `consumer` aufgeloest werden. Entscheidend ist, dass der Input die Source-Selektion steuert; heute tut er das nicht (`extensions/github-action/action.yml:113-125`).

### 5. Request-Logging minimal, strukturiert und datensparsam

```go
logger.Info("http_request",
    "method", r.Method,
    "route", matchedRoute, // keine Query/Symbole loggen
    "status", status,
    "duration_ms", time.Since(start).Milliseconds(),
    "request_id", requestID,
)
```

Keine Source-Pfade, Querytexte oder Tokens loggen. Das passt zur bestehenden Sanitization-Absicht (`surfaces/http/server.go:163-178`), ersetzt aber die derzeitigen unstrukturierten `log.Printf`-Punkte (`surfaces/http/server.go:894-935`).

## UNKNOWN

1. **Branch Protection / Required Checks: UNKNOWN.** Repository-Dateien zeigen nicht, welche Workflows auf `main` tatsaechlich required sind. Selbst perfekte Branch Protection beseitigt jedoch nicht die fehlende Kopplung des post-merge `auto-release`-DAGs.

2. **Aktueller CI-Status und letzte reale Release-Laeufe: UNKNOWN.** In diesem Read-only-Review wurden keine GitHub-Run-Daten abgefragt. Aussagen beziehen sich auf die eingecheckte Workflow-Logik.

3. **Secrets und Environments: UNKNOWN.** Ob `PACKAGING_PUSH_TOKEN`, geschuetzte GitHub Environments oder manuelle Approvals konfiguriert sind, ist aus dem Repository nicht feststellbar. Der Packaging-Publish wird bei fehlendem Token bewusst uebersprungen (`.github/workflows/release.yml:198-205`).

4. **Artifact Signing / externe Attestations: UNKNOWN.** Im Repository ist kein entsprechender Schritt sichtbar; moegliche externe GitHub-Organisationseinstellungen oder nachgelagerte Systeme sind nicht einsehbar.

5. **Production-SLOs, Crash-Rate, Speicherverbrauch und reale Repositories: UNKNOWN.** Es gibt Bench-Budgets und Corpus-Tests, aber keine Betriebsdaten oder SLO-Dokumentation im untersuchten Code.

6. **Backup-/Restore-Policy fuer lokale DBs: UNKNOWN.** Portable, versionierte Snapshots existieren im Code (`core/graphstore/snapshot.go:16-56`), aber eine operatorische Backup-, Restore- und Upgrade-Prozedur fuer den Daemon wurde nicht als Release-Runbook gefunden.

7. **Vollstaendiger lokaler Teststatus: UNKNOWN.** Ein gezielter Lauf von `go test` konnte kompilieren, aber Socket-/HTTP-Tests scheiterten in dieser Review-Sandbox am verbotenen lokalen `bind`, nicht an einer nachgewiesenen Produktregression. `cmd/release-gate` selbst lief dabei gruen. Dieses Ergebnis ist nicht als vollstaendiger Suite-Verdict verwendbar.

8. **Windows-Daemon-Verhalten: UNKNOWN.** Die Distribution baut Windows (`internal/release/build.go:101-107`), der Daemon basiert aber explizit auf Unix Domain Sockets (`surfaces/daemon/daemon.go:1-9`). Ob der CLI-Build/Runtime-Pfad unter dem unterstuetzten Windows-Ziel vollstaendig funktioniert, muss in einem echten Windows-CI-Job bewiesen werden; die zentrale Workspace-CI laeuft nur auf Ubuntu (`.github/workflows/release.yml:35-50`).

## Harte Empfehlung

**Empfehlung: Teil-Refactor. Nicht Full Rewrite, nicht blind Weiterbauen.**

Direkte Begruendung:

- Die Basis ist substanziell: reproduzierbare Builds, eine vernuenftige Release-Matrix, deterministische Datenhaltung, umfangreiche Tests und starke Local-First-Invarianten sind bereits vorhanden (`internal/release/build.go:146-199`, `.github/workflows/privacy-audit.yml:21-55`). Ein Rewrite wuerde funktionierende Sicherheits- und Qualitaetsarbeit vernichten.
- Die groessten Risiken liegen an wenigen Orchestrierungs- und Lifecycle-Grenzen, nicht im gesamten System: Release-DAG (`.github/workflows/auto-release.yml:36-101`), Daemon-Prozessende (`cmd/graphi/main.go:1138-1145`) und Action-Source-Pinning (`extensions/github-action/action.yml:113-125`). Diese Punkte sind isoliert und gezielt reparierbar.
- **Bis diese drei Blocker behoben sind: keine automatische Release-Publikation und keine externe Bewerbung der GitHub Action.** Feature-Entwicklung, die nicht unmittelbar diese Betriebsrisiken adressiert, sollte pausieren.
- Danach in dieser Reihenfolge weiterbauen: (1) ein autoritativer Release-DAG, (2) Daemon-/HTTP-Graceful-Shutdown plus CLI-E2E-Lifecycle-Test, (3) echter Consumer-Repo-Test fuer die Action, (4) SHA-Pinning + Vulnerability/SBOM/Signing, (5) Readiness und datensparsame strukturierte Betriebsmetriken.

Das Zielbild ist realistisch innerhalb eines fokussierten Refactors. Der aktuelle Stand ist ein gut getestetes Engineering-Projekt mit ernsthaften Release- und Prozessluecken, noch kein belastbar produzierbares Local-Service-Produkt.
